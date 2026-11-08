param(
    [string]$BinDir = "bin",
    [int]$TimeoutSeconds = 60
)

$ErrorActionPreference = "Stop"
$root = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$ptyCheck = & python -c "import winpty; print('ok')" 2>&1
if ($LASTEXITCODE -ne 0 -or ($ptyCheck | Out-String).Trim() -ne "ok") {
    throw "pywinpty is required for the hidden PTY smoke. Run: python -m pip install -r scripts/requirements-pty.txt"
}
$bin = if ([System.IO.Path]::IsPathRooted($BinDir)) {
    [System.IO.Path]::GetFullPath($BinDir)
} else {
    [System.IO.Path]::GetFullPath((Join-Path $root $BinDir))
}
$tempRoot = [System.IO.Path]::GetFullPath((Join-Path $env:TEMP ("vibe-agent-toolchain-" + [guid]::NewGuid().ToString("N"))))
$tempBase = [System.IO.Path]::GetFullPath($env:TEMP)
if (-not $tempRoot.StartsWith($tempBase, [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "refusing to use smoke directory outside TEMP: $tempRoot"
}
New-Item -ItemType Directory -Path $tempRoot -Force | Out-Null
New-Item -ItemType Directory -Path $bin -Force | Out-Null
$providerProcess = $null
$oldEnvironment = @{
    ANTHROPIC_AUTH_TOKEN = $env:ANTHROPIC_AUTH_TOKEN
    ANTHROPIC_BASE_URL = $env:ANTHROPIC_BASE_URL
    ANTHROPIC_MODEL = $env:ANTHROPIC_MODEL
}

try {
    $listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
    $listener.Start()
    $providerPort = $listener.LocalEndpoint.Port
    $listener.Stop()
    $providerExe = Join-Path $tempRoot "fake-provider.exe"
    Push-Location $root
    go build -o $providerExe ./testdata/fake_provider.go
    if ($LASTEXITCODE -ne 0) { throw "fake provider build failed" }
    Pop-Location

    $env:FAKE_PROVIDER_ADDR = "127.0.0.1:$providerPort"
    $providerProcess = Start-Process `
        -FilePath $providerExe `
        -PassThru `
        -WindowStyle Hidden `
        -RedirectStandardOutput (Join-Path $tempRoot "provider.stdout.log") `
        -RedirectStandardError (Join-Path $tempRoot "provider.stderr.log")
    $ready = $false
    for ($attempt = 0; $attempt -lt 100; $attempt++) {
        $client = [System.Net.Sockets.TcpClient]::new()
        try {
            $client.Connect("127.0.0.1", $providerPort)
            $ready = $true
            break
        }
        catch {
            Start-Sleep -Milliseconds 50
        }
        finally {
            $client.Dispose()
        }
    }
    if (-not $ready) { throw "fake provider did not listen" }

    $env:ANTHROPIC_AUTH_TOKEN = "fake-secret"
    $env:ANTHROPIC_BASE_URL = "http://127.0.0.1:$providerPort"
    $env:ANTHROPIC_MODEL = "fake-anthropic-model"
    & powershell -NoProfile -ExecutionPolicy Bypass `
        -File (Join-Path $root "scripts/tui-real-api-smoke.ps1") `
        -RunRealApi -BinDir $bin -TimeoutSeconds $TimeoutSeconds
    if ($LASTEXITCODE -ne 0) { throw "PTY toolchain smoke failed" }
    Write-Output "TOOLCHAIN SMOKE PASS: fake Anthropic-compatible provider"
}
finally {
    if ($providerProcess -and -not $providerProcess.HasExited) {
        Stop-Process -Id $providerProcess.Id -Force -ErrorAction SilentlyContinue
    }
    foreach ($key in $oldEnvironment.Keys) {
        if ($null -eq $oldEnvironment[$key]) {
            Remove-Item -Path "Env:$key" -ErrorAction SilentlyContinue
        } else {
            Set-Item -Path "Env:$key" -Value $oldEnvironment[$key]
        }
    }
    Remove-Item -Path "Env:FAKE_PROVIDER_ADDR" -ErrorAction SilentlyContinue
    if ($tempRoot.StartsWith($tempBase, [System.StringComparison]::OrdinalIgnoreCase)) {
        Remove-Item -LiteralPath $tempRoot -Recurse -Force -ErrorAction SilentlyContinue
    }
}
