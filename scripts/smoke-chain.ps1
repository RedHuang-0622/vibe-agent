param(
    [string]$BinDir = "bin"
)

$ErrorActionPreference = "Stop"
$repoRoot = Split-Path -Parent $PSScriptRoot
$binPath = if ([System.IO.Path]::IsPathRooted($BinDir)) {
    [System.IO.Path]::GetFullPath($BinDir)
} else {
    [System.IO.Path]::GetFullPath((Join-Path $repoRoot $BinDir))
}
$tempRoot = [System.IO.Path]::GetFullPath((Join-Path $env:TEMP ("vibe-agent-smoke-" + [guid]::NewGuid())))
$tempBase = [System.IO.Path]::GetFullPath($env:TEMP)
if (-not $tempRoot.StartsWith($tempBase, [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "refusing to use smoke directory outside TEMP: $tempRoot"
}

New-Item -ItemType Directory -Path $binPath -Force | Out-Null
New-Item -ItemType Directory -Path $tempRoot -Force | Out-Null
$hubExe = Join-Path $binPath "hub.exe"
$sessiondExe = Join-Path $binPath "sessiond.exe"
$stdoutLog = Join-Path $tempRoot "hub.stdout.log"
$stderrLog = Join-Path $tempRoot "hub.stderr.log"
$hubProcess = $null

Push-Location $repoRoot
try {
    go build -o $hubExe ./cmd/hub
    if ($LASTEXITCODE -ne 0) { throw "hub build failed" }
    go build -o $sessiondExe ./cmd/sessiond
    if ($LASTEXITCODE -ne 0) { throw "sessiond build failed" }

    $env:VIBE_AGENT_HOME = $tempRoot
    $env:VIBE_HUB_ADDR = "127.0.0.1:0"
    $env:VIBE_HUB_PATH = $hubExe
    $env:VIBE_SESSIOND_PATH = $sessiondExe

    $hubProcess = Start-Process `
        -FilePath $hubExe `
        -ArgumentList "serve" `
        -PassThru `
        -WindowStyle Hidden `
        -RedirectStandardOutput $stdoutLog `
        -RedirectStandardError $stderrLog

    $ready = $false
    for ($attempt = 0; $attempt -lt 100; $attempt++) {
        if (Test-Path -LiteralPath (Join-Path $tempRoot "hub\state.json")) {
            $savedPreference = $ErrorActionPreference
            $ErrorActionPreference = "SilentlyContinue"
            try {
                & $hubExe status *> $null
                if ($LASTEXITCODE -eq 0) {
                    $ready = $true
                }
            }
            finally {
                $ErrorActionPreference = $savedPreference
            }
            if ($ready) {
                break
            }
        }
        Start-Sleep -Milliseconds 100
    }
    if (-not $ready) {
        throw "hub did not become ready; stderr: $(Get-Content -Raw -ErrorAction SilentlyContinue $stderrLog)"
    }

    $sessionA = (& $hubExe session create smoke-a | Out-String).Trim()
    $sessionB = (& $hubExe session create smoke-b | Out-String).Trim()
    if (-not $sessionA -or -not $sessionB -or $sessionA -eq $sessionB) {
        throw "session creation did not return distinct ids"
    }
    & $hubExe tool call $sessionA /calculator "(1+2)*3" | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "session A direct tool failed" }
    & $hubExe tool call $sessionB /calculator "4*5" | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "session B direct tool failed" }

    $snapshotA = (& $hubExe snapshot $sessionA --tail-limit 3 | Out-String) | ConvertFrom-Json
    $snapshotB = (& $hubExe snapshot $sessionB --tail-limit 3 | Out-String) | ConvertFrom-Json
    if ($snapshotA.session_id -ne $sessionA -or @($snapshotA.events).Count -eq 0) {
        throw "session A snapshot is empty or misaddressed"
    }
    if ($snapshotB.session_id -ne $sessionB -or @($snapshotB.events).Count -eq 0) {
        throw "session B snapshot is empty or misaddressed"
    }
    if ($snapshotA.watermark -lt 1 -or $snapshotB.watermark -lt 1) {
        throw "snapshot watermark did not advance"
    }
    foreach ($event in @($snapshotA.events)) {
        if (-not $event.event_id.StartsWith($sessionA + "-seq-")) {
            throw "session A snapshot leaked another session event: $($event.event_id)"
        }
    }
    foreach ($event in @($snapshotB.events)) {
        if (-not $event.event_id.StartsWith($sessionB + "-seq-")) {
            throw "session B snapshot leaked another session event: $($event.event_id)"
        }
    }

    $firstSeq = [int64]$snapshotA.events[0].seq
    $history = (& $hubExe history $sessionA --before-seq $firstSeq --limit 2 | Out-String) | ConvertFrom-Json
    if ($history.session_id -ne $sessionA -or $history.resync_required) {
        throw "history page is not resumable"
    }

    $events = (& $hubExe events $sessionA --after 0 --limit 100 | Out-String) | ConvertFrom-Json
    if ($events.resync_required -or $events.cursor -lt 1) {
        throw "event page did not expose a valid cursor"
    }
    $timed = (& $hubExe events $sessionA --after $snapshotA.watermark --limit 10 --wait-ms 300 | Out-String) | ConvertFrom-Json
    if (-not $timed.timed_out -or @($timed.events).Count -ne 0) {
        throw "long-poll timeout semantics are incorrect"
    }

    Write-Host "SMOKE PASS: snapshot/watch/history/seq-isolation"
}
finally {
    if ($hubProcess -and -not $hubProcess.HasExited) {
        & $hubExe daemon-shutdown *> $null
        for ($attempt = 0; $attempt -lt 50 -and -not $hubProcess.HasExited; $attempt++) {
            Start-Sleep -Milliseconds 100
        }
        if (-not $hubProcess.HasExited) {
            Stop-Process -Id $hubProcess.Id -Force
        }
    }
    Pop-Location
    if ($tempRoot.StartsWith($tempBase, [System.StringComparison]::OrdinalIgnoreCase)) {
        Remove-Item -LiteralPath $tempRoot -Recurse -Force -ErrorAction SilentlyContinue
    }
}
