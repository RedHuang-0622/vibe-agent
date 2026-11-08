param(
    [switch]$RunRealApi,
    [string]$BinDir = "bin",
    [int]$TimeoutSeconds = 180
)

$ErrorActionPreference = "Stop"
$root = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$bin = if ([System.IO.Path]::IsPathRooted($BinDir)) {
    [System.IO.Path]::GetFullPath($BinDir)
} else {
    [System.IO.Path]::GetFullPath((Join-Path $root $BinDir))
}
$tempRoot = [System.IO.Path]::GetFullPath((Join-Path $env:TEMP ("vibe-agent-real-api-" + [guid]::NewGuid().ToString("N"))))
$tempBase = [System.IO.Path]::GetFullPath($env:TEMP)
if (-not $tempRoot.StartsWith($tempBase, [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "refusing to use smoke directory outside TEMP: $tempRoot"
}
if (-not $RunRealApi) {
    Write-Output "SMOKE BLOCKED: pass -RunRealApi to opt in to paid provider calls"
    exit 2
}
$providerName = ""
$providerProtocol = ""
$providerBaseURL = ""
$providerPath = ""
$providerModel = ""
$credentialRef = ""
$credentialValue = ""
if ($env:DEEPSEEK_API_KEY) {
    $providerName = "deepseek"
    $providerProtocol = "openai_compatible"
    $providerBaseURL = "https://api.deepseek.com"
    $providerPath = "/chat/completions"
    $providerModel = "deepseek-flash"
    $credentialRef = "env:DEEPSEEK_API_KEY"
    $credentialValue = $env:DEEPSEEK_API_KEY
}
elseif ($env:ANTHROPIC_AUTH_TOKEN -and $env:ANTHROPIC_BASE_URL) {
    $providerModel = if ($env:ANTHROPIC_MODEL) {
        $env:ANTHROPIC_MODEL
    } else {
        $env:ANTHROPIC_DEFAULT_HAIKU_MODEL
    }
    if (-not $providerModel) {
        Write-Output "SMOKE BLOCKED: an Anthropic-compatible model environment variable is required"
        exit 2
    }
    $providerName = "anthropic-compatible"
    $providerProtocol = "anthropic_messages_v1"
    $providerBaseURL = $env:ANTHROPIC_BASE_URL.TrimEnd("/")
    $providerPath = "/v1/messages"
    $credentialRef = "env:ANTHROPIC_AUTH_TOKEN"
    $credentialValue = $env:ANTHROPIC_AUTH_TOKEN
}
else {
    Write-Output "SMOKE BLOCKED: set DEEPSEEK_API_KEY or ANTHROPIC_AUTH_TOKEN + ANTHROPIC_BASE_URL"
    exit 2
}

New-Item -ItemType Directory -Path $bin -Force | Out-Null
New-Item -ItemType Directory -Path $tempRoot -Force | Out-Null
$hubExe = Join-Path $bin "hub.exe"
$sessiondExe = Join-Path $bin "sessiond.exe"
$tuiExe = Join-Path $bin "tui.exe"
$hubProcess = $null
$completed = $false
$sessionID = $null
$oldEnvironment = @{
    VIBE_AGENT_HOME = $env:VIBE_AGENT_HOME
    VIBE_HUB_ADDR = $env:VIBE_HUB_ADDR
    VIBE_HUB_PATH = $env:VIBE_HUB_PATH
    VIBE_SESSIOND_PATH = $env:VIBE_SESSIOND_PATH
}

function Invoke-Hub {
    param([string[]]$Arguments)
    $output = & $hubExe @Arguments 2>&1
    if ($LASTEXITCODE -ne 0) {
        throw "hub command failed: $Arguments`n$output"
    }
    return ($output | Out-String).Trim()
}

Push-Location $root
try {
    go build -o $hubExe ./cmd/hub
    if ($LASTEXITCODE -ne 0) { throw "hub build failed" }
    go build -o $sessiondExe ./cmd/sessiond
    if ($LASTEXITCODE -ne 0) { throw "sessiond build failed" }
    go build -o $tuiExe ./cmd/tui
    if ($LASTEXITCODE -ne 0) { throw "tui build failed" }

    $env:VIBE_AGENT_HOME = $tempRoot
    $env:VIBE_HUB_ADDR = "127.0.0.1:0"
    $env:VIBE_HUB_PATH = $hubExe
    $env:VIBE_SESSIOND_PATH = $sessiondExe

    $accountsPath = Join-Path $tempRoot "real-accounts.jsonl"
    $account = @{
        schema_version = 1; account_id = "real-anthropic"; revision = 1; enabled = $true
        display_name = "Real Anthropic-compatible"; priority = 100
        default_for = @("chat", "tool_call")
        services = @{
            chat = @{
                capability = "chat"; provider = $providerName
                protocol = $providerProtocol; base_url = $providerBaseURL
                path = $providerPath; model = $providerModel
                credential_ref = $credentialRef; timeout = "120s"
            }
            tool_call = @{
                capability = "tool_call"; provider = $providerName
                protocol = $providerProtocol; base_url = $providerBaseURL
                path = $providerPath; model = $providerModel
                credential_ref = $credentialRef; timeout = "120s"
            }
        }
    } | ConvertTo-Json -Compress -Depth 8
    [System.IO.File]::WriteAllText($accountsPath, $account + "`n")

    $hubProcess = Start-Process `
        -FilePath $hubExe `
        -ArgumentList "serve" `
        -PassThru `
        -WindowStyle Hidden `
        -RedirectStandardOutput (Join-Path $tempRoot "hub.stdout.log") `
        -RedirectStandardError (Join-Path $tempRoot "hub.stderr.log")
    $ready = $false
    for ($attempt = 0; $attempt -lt 120; $attempt++) {
        if (Test-Path -LiteralPath (Join-Path $tempRoot "hub\state.json")) {
            $savedPreference = $ErrorActionPreference
            $ErrorActionPreference = "SilentlyContinue"
            try {
                & $hubExe status *> $null
                if ($LASTEXITCODE -eq 0) { $ready = $true }
            }
            finally {
                $ErrorActionPreference = $savedPreference
            }
            if ($ready) { break }
        }
        Start-Sleep -Milliseconds 100
    }
    if (-not $ready) {
        throw "hub did not become ready: $(Get-Content -Raw -ErrorAction SilentlyContinue (Join-Path $tempRoot 'hub.stderr.log'))"
    }
    Write-Output "[1] hidden PTY prerequisites and hub readiness -> PASS"

    Invoke-Hub @("llm", "account", "add", "--file", $accountsPath) | Out-Null
    Invoke-Hub @("llm", "account", "validate", "real-anthropic") | Out-Null
    $sessionID = Invoke-Hub @("session", "create", "real-api-pty")
    $ledgerPath = Join-Path $tempRoot "sessions/$sessionID/messages.jsonl"
    Write-Output "[2] real account/sessiond readiness session_id=$sessionID -> PASS"

    $prompt = "You are testing an operational board. You MUST call the todo_board tool in this order, using real tool calls: " +
        "(1) action=create with title='release smoke', details='PTY real API', priority='high', idempotency_key='smoke-create'; " +
        "(2) action=list to verify it appears; " +
        "(3) action=update the returned id with expected_version from the create/read result, status='in_progress', idempotency_key='smoke-update'; " +
        "(4) action=get the item so the updated status is explicit; " +
        "(5) action=delete using the latest expected_version and idempotency_key='smoke-delete'. " +
        "After the delete succeeds, briefly report the action sequence. Do not skip tool calls."
    $ptyOutput = Join-Path $tempRoot "tui.pty.log"
    $configPath = Join-Path $tempRoot "pty-config.json"
    $config = @{
        exe = $tuiExe; cwd = $tempRoot; session_id = $sessionID; prompt = $prompt
        output = $ptyOutput; timeout_seconds = $TimeoutSeconds
    } | ConvertTo-Json -Compress -Depth 5
    [System.IO.File]::WriteAllText($configPath, $config)

    $harnessOut = Join-Path $tempRoot "harness.stdout.log"
    $harnessErr = Join-Path $tempRoot "harness.stderr.log"
    $harness = Start-Process `
        -FilePath "python" `
        -ArgumentList @((Join-Path $root "scripts/pty_tui_probe.py"), "--config", $configPath) `
        -PassThru `
        -Wait `
        -WindowStyle Hidden `
        -RedirectStandardOutput $harnessOut `
        -RedirectStandardError $harnessErr
    if ($harness.ExitCode -ne 0) {
        throw "PTY probe failed exit=$($harness.ExitCode): $(Get-Content -Raw -ErrorAction SilentlyContinue $harnessErr)"
    }
    if (-not (Test-Path -LiteralPath $ptyOutput)) {
        throw "PTY output was not captured"
    }
    $tuiText = Get-Content -Raw -LiteralPath $ptyOutput
    $earlyEvents = @()
    if (Test-Path -LiteralPath $ledgerPath) {
        $earlyEvents = @(Get-Content -LiteralPath $ledgerPath | ForEach-Object { $_ | ConvertFrom-Json })
    }
    $authFailure = $earlyEvents | Where-Object {
        $_.type -eq "llm.failed" -and
        $_.payload.reason_code -in @("authentication_failed", "missing_credential")
    }
    if ($authFailure) {
        Write-Output "[3] provider authentication -> BLOCKED reason=$($authFailure.payload.reason_code)"
        exit 2
    }
    if ($tuiText -notmatch "\[tool\] requested name=todo_board" -or
        $tuiText -notmatch "\[tool\] completed name=todo_board") {
        $debugEvents = @()
        if (Test-Path -LiteralPath $ledgerPath) {
            $debugEvents = @(Get-Content -LiteralPath $ledgerPath | ForEach-Object { $_ | ConvertFrom-Json } |
                Select-Object -ExpandProperty type)
        }
        throw "TUI did not render the todo_board lifecycle: $tuiText event_types=$debugEvents"
    }
    Write-Output "[3] TUI rendered tool request/result lifecycle -> PASS"

    $events = @(Get-Content -LiteralPath $ledgerPath | ForEach-Object { $_ | ConvertFrom-Json })
    for ($index = 0; $index -lt $events.Count; $index++) {
        if ([int64]$events[$index].seq -ne ($index + 1)) {
            throw "event seq is not contiguous at index $index"
        }
    }
    $modelCalls = @($events | Where-Object { $_.type -eq "tool.requested" -and $_.payload.invocation_mode -eq "model" })
    $modelCompleted = @($events | Where-Object { $_.type -eq "tool.completed" -and $_.payload.invocation_mode -eq "model" })
    if ($modelCalls.Count -lt 5 -or $modelCompleted.Count -lt 5) {
        throw "expected at least five model tool round trips, requested=$($modelCalls.Count) completed=$($modelCompleted.Count)"
    }
    foreach ($call in $modelCalls) {
        if (-not ($modelCompleted | Where-Object { $_.call_id -eq $call.call_id })) {
            throw "tool call $($call.call_id) has no completed result"
        }
    }
    $actions = @($modelCompleted | ForEach-Object { $_.payload.result.action })
    foreach ($action in @("create", "list", "update", "get", "delete")) {
        if ($actions -notcontains $action) {
            throw "model tool chain did not execute todo_board action=$action; actions=$actions"
        }
    }
    if (-not ($events | Where-Object { $_.type -eq "assistant.message.completed" })) {
        throw "no final assistant message"
    }
    Write-Output "[4] complete model tool chain create/list/update/get/delete -> PASS"

    $directCompleted = @($events | Where-Object { $_.type -eq "tool.completed" -and $_.payload.invocation_mode -eq "direct" })
    $boardSnapshot = $directCompleted | Where-Object { $_.payload.result.action -eq "list" } | Select-Object -Last 1
    if (-not $boardSnapshot -or [int]$boardSnapshot.payload.result.summary.total -ne 0) {
        throw "TUI /board did not confirm the deleted item is gone"
    }
    Write-Output "[5] TUI board read model confirmed delete -> PASS"

    $leaks = Get-ChildItem -LiteralPath $tempRoot -Recurse -File |
        Where-Object { $_.Extension -in @(".json", ".jsonl", ".log", ".txt") } |
        Select-String -SimpleMatch $credentialValue
    if ($leaks) {
        throw "credential value leaked into persisted/test output"
    }
    Write-Output "[6] secret scan and event identity -> PASS"
    Write-Output "SMOKE PASS: hidden PTY -> provider model -> todo_board CRUD"
    $completed = $true
}
finally {
    if ($hubProcess -and -not $hubProcess.HasExited) {
        if ($sessionID) {
            $savedPreference = $ErrorActionPreference
            $ErrorActionPreference = "SilentlyContinue"
            try { Invoke-Hub @("session", "stop", $sessionID) | Out-Null } catch {}
            $ErrorActionPreference = $savedPreference
        }
        $savedPreference = $ErrorActionPreference
        $ErrorActionPreference = "SilentlyContinue"
        try { & $hubExe daemon-shutdown *> $null } catch {}
        $ErrorActionPreference = $savedPreference
        for ($attempt = 0; $attempt -lt 50 -and -not $hubProcess.HasExited; $attempt++) {
            Start-Sleep -Milliseconds 100
        }
        if (-not $hubProcess.HasExited) {
            Stop-Process -Id $hubProcess.Id -Force -ErrorAction SilentlyContinue
        }
    }
    Pop-Location
    foreach ($key in $oldEnvironment.Keys) {
        if ($null -eq $oldEnvironment[$key]) {
            Remove-Item -Path "Env:$key" -ErrorAction SilentlyContinue
        } else {
            Set-Item -Path "Env:$key" -Value $oldEnvironment[$key]
        }
    }
    if ($completed -and $tempRoot.StartsWith($tempBase, [System.StringComparison]::OrdinalIgnoreCase)) {
        Remove-Item -LiteralPath $tempRoot -Recurse -Force -ErrorAction SilentlyContinue
    } else {
        Write-Output "SMOKE ARTIFACTS: $tempRoot"
    }
}
