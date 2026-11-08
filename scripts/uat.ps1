param(
    [string]$BinDir = "bin"
)

$ErrorActionPreference = "Stop"
$root = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$bin = (Resolve-Path (Join-Path $root $BinDir)).Path
$uatRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("vibe-agent-uat-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $uatRoot | Out-Null
$processes = @()
$sessionPIDs = @()
$oldEnvironment = @{
    VIBE_AGENT_HOME = $env:VIBE_AGENT_HOME
    VIBE_HUB_ADDR = $env:VIBE_HUB_ADDR
    UAT_FAKE_KEY = $env:UAT_FAKE_KEY
    FAKE_PROVIDER_ADDR = $env:FAKE_PROVIDER_ADDR
    APPDATA = $env:APPDATA
}

function Get-FreePort {
    $listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
    $listener.Start()
    $port = $listener.LocalEndpoint.Port
    $listener.Stop()
    return $port
}

function Start-CapturedProcess {
    param([string]$FilePath, [string[]]$Arguments, [string]$Name)
    $stdout = Join-Path $uatRoot "$Name.stdout.log"
    $stderr = Join-Path $uatRoot "$Name.stderr.log"
    $startArgs = @{
        WindowStyle = "Hidden"
        FilePath = $FilePath
        PassThru = $true
        RedirectStandardOutput = $stdout
        RedirectStandardError = $stderr
    }
    if ($Arguments.Count -gt 0) {
        $startArgs.ArgumentList = $Arguments
    }
    $process = Start-Process @startArgs
    $script:processes += $process
    return $process
}

function Invoke-Hub {
    param([string[]]$Arguments)
    $output = & (Join-Path $bin "hub.exe") @Arguments 2>&1
    if ($LASTEXITCODE -ne 0) {
        throw "hub command failed: $Arguments`n$output"
    }
    return ($output | Out-String).Trim()
}

function Wait-Until {
    param([scriptblock]$Condition, [string]$Description, [int]$Seconds = 15)
    $deadline = (Get-Date).AddSeconds($Seconds)
    while ((Get-Date) -lt $deadline) {
        if (& $Condition) {
            return
        }
        Start-Sleep -Milliseconds 100
    }
    throw "timed out waiting for $Description"
}

try {
    $hubPort = Get-FreePort
    $providerPort = Get-FreePort
    $env:VIBE_AGENT_HOME = $uatRoot
    $env:VIBE_HUB_ADDR = "127.0.0.1:$hubPort"
    $env:UAT_FAKE_KEY = "uat-secret"
    $env:FAKE_PROVIDER_ADDR = "127.0.0.1:$providerPort"
    $env:APPDATA = Join-Path $uatRoot "appdata"

    $providerExe = Join-Path $uatRoot "fake-provider.exe"
    & go build -o $providerExe (Join-Path $root "testdata/fake_provider.go")
    if ($LASTEXITCODE -ne 0) { throw "fake provider build failed" }
    $provider = Start-CapturedProcess $providerExe @() "provider"
    Wait-Until {
        try {
            Invoke-RestMethod -Method Post -Uri "http://127.0.0.1:$providerPort/chat" `
                -ContentType "application/json" -Body '{"messages":[]}' | Out-Null
            return $true
        } catch {
            return $false
        }
    } "fake provider"

    $hub = Start-CapturedProcess (Join-Path $bin "hub.exe") @("serve") "hub"
    Wait-Until {
        try {
            Invoke-Hub @("status") | Out-Null
            return $true
        } catch {
            return $false
        }
    } "hub"

    $accountsPath = Join-Path $uatRoot "accounts.jsonl"
    $accounts = @(
        (@{
            schema_version = 1; account_id = "fake-chat"; revision = 1; enabled = $true
            display_name = "Fake Chat"; priority = 100; default_for = @("chat", "tool_call", "search")
            services = @{
                chat = @{ capability = "chat"; provider = "deepseek"; protocol = "openai_compatible"; base_url = "http://127.0.0.1:$providerPort"; path = "/chat"; model = "deepseek-flash"; credential_ref = "env:UAT_FAKE_KEY"; thinking = @{ type = "enabled" }; reasoning_effort = "high"; timeout_ms = 5000 }
                tool_call = @{ capability = "tool_call"; provider = "deepseek"; protocol = "openai_compatible"; base_url = "http://127.0.0.1:$providerPort"; path = "/chat"; model = "deepseek-flash"; credential_ref = "env:UAT_FAKE_KEY"; thinking = @{ type = "enabled" }; reasoning_effort = "high"; timeout_ms = 5000 }
                search = @{ capability = "search"; provider = "fake"; protocol = "json_search_v1"; base_url = "http://127.0.0.1:$providerPort"; path = "/search"; model = "fake-search"; credential_ref = "env:UAT_FAKE_KEY"; timeout_ms = 5000 }
            }
        } | ConvertTo-Json -Compress -Depth 8),
        (@{
            schema_version = 1; account_id = "fake-embedding"; revision = 1; enabled = $true
            display_name = "Fake Embedding"; priority = 100; default_for = @("embedding")
            services = @{ embedding = @{ capability = "embedding"; provider = "fake"; protocol = "openai_embeddings_v1"; base_url = "http://127.0.0.1:$providerPort"; path = "/embedding"; model = "fake-embedding"; credential_ref = "env:UAT_FAKE_KEY"; timeout_ms = 5000; dimensions = 2; batch_size = 8; normalize = $false } }
        } | ConvertTo-Json -Compress -Depth 8),
        (@{
            schema_version = 1; account_id = "fake-rerank"; revision = 1; enabled = $true
            display_name = "Fake Rerank"; priority = 100; default_for = @("rerank")
            services = @{ rerank = @{ capability = "rerank"; provider = "fake"; protocol = "json_rerank_v1"; base_url = "http://127.0.0.1:$providerPort"; path = "/rerank"; model = "fake-rerank"; credential_ref = "env:UAT_FAKE_KEY"; timeout_ms = 5000; top_n = 5; max_candidates = 20 } }
        } | ConvertTo-Json -Compress -Depth 8)
    )
    [System.IO.File]::WriteAllLines($accountsPath, $accounts)
    Invoke-Hub @("llm", "account", "add", "--file", $accountsPath) | Out-Null

    $sessionA = Invoke-Hub @("session", "create", "SessionA")
    $sessionB = Invoke-Hub @("session", "create", "SessionB")
    if ($sessionA -eq $sessionB) { throw "session IDs are not unique" }

    $eventA = Join-Path $uatRoot "sessions/$sessionA/messages.jsonl"
    $eventB = Join-Path $uatRoot "sessions/$sessionB/messages.jsonl"
    Wait-Until { (Test-Path $eventA) -and (Test-Path $eventB) } "session ledgers"
    if (-not (Get-Content $eventA -First 1 | ConvertFrom-Json).seq -eq 1) { throw "session A did not start at seq=1" }
    if (-not (Get-Content $eventB -First 1 | ConvertFrom-Json).seq -eq 1) { throw "session B did not start at seq=1" }

    if ((Invoke-Hub @("tool", "call", $sessionA, "/calculator", "1+2*3")) -notmatch '"value":7') { throw "session A calculator failed" }
    if ((Invoke-Hub @("tool", "call", $sessionB, "/calculator", "(12+4)*3")) -notmatch '"value":48') { throw "session B calculator failed" }

    $chat = Invoke-Hub @("run", $sessionA, "please calc 1+2*3")
    if ($chat -notmatch "calculator result is 7") { throw "ReAct chat failed: $chat" }

    $memoryWriteRequest = Join-Path $uatRoot "memory-write.json"
    $memorySearchRequest = Join-Path $uatRoot "memory-search.json"
    [System.IO.File]::WriteAllText($memoryWriteRequest, '{"text":"dense alpha knowledge","scope":"workspace","idempotency_key":"uat-m1"}')
    [System.IO.File]::WriteAllText($memorySearchRequest, '{"query":"dense","scope":"workspace","limit":5,"rerank":true}')
    $memoryWrite = Invoke-Hub @("tool", "call", $sessionA, "/memory_write", "@$memoryWriteRequest")
    if ($memoryWrite -notmatch '"status":"ready"') { throw "memory write failed" }
    $memorySearch = Invoke-Hub @("tool", "call", $sessionA, "/memory_search", "@$memorySearchRequest")
    if ($memorySearch -notmatch '"channels":\["bm25","dense","rerank"\]') { throw "hybrid retrieval failed" }
    if ((Invoke-Hub @("tool", "call", $sessionA, "/search", "go context")) -notmatch '"results"') { throw "search tool failed" }
    if ((Invoke-Hub @("tool", "call", $sessionA, "/weatherapi", "Shanghai")) -notmatch '"temperature_c":20') { throw "weather tool failed" }
    if ((Invoke-Hub @("tool", "call", $sessionA, "/todo", "ship runtime")) -notmatch '"status":"open"') { throw "todo tool failed" }
    $workspace = Join-Path $uatRoot "sessions/$sessionA/workspace"
    New-Item -ItemType Directory -Force -Path $workspace | Out-Null
    [System.IO.File]::WriteAllText((Join-Path $workspace "note.txt"), "line one`nline two")
    if ((Invoke-Hub @("tool", "call", $sessionA, "/read_docs", "note.txt")) -notmatch "line two") { throw "read_docs tool failed" }

    $quotaPath = Join-Path $uatRoot "quota.jsonl"
    $quota = @{
        schema_version = 1; account_id = "fake-quota-search"; revision = 1; enabled = $true
        display_name = "Fake Quota Search"; priority = 200; default_for = @("search")
        services = @{ search = @{ capability = "search"; provider = "fake-quota"; protocol = "json_search_v1"; base_url = "http://127.0.0.1:$providerPort"; path = "/quota"; model = "fake-search"; credential_ref = "env:UAT_FAKE_KEY"; timeout_ms = 5000 } }
    } | ConvertTo-Json -Compress -Depth 8
    [System.IO.File]::WriteAllText($quotaPath, $quota)
    Invoke-Hub @("llm", "account", "add", "--file", $quotaPath) | Out-Null
    $oldErrorActionPreference = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    $quotaOutput = & (Join-Path $bin "hub.exe") tool call $sessionA /search "go context" 2>&1
    $quotaExitCode = $LASTEXITCODE
    $ErrorActionPreference = $oldErrorActionPreference
    if ($quotaExitCode -eq 0 -or ($quotaOutput | Out-String) -notmatch "quota_exhausted") {
        throw "quota error was not stable: $quotaOutput"
    }

    $inspectA = Invoke-Hub @("session", "inspect", $sessionA) | ConvertFrom-Json
    $pidA = [int]$inspectA.pid
    $sessionPIDs += $pidA
    Stop-Process -Id $pidA -Force
    Invoke-Hub @("tool", "call", $sessionB, "/calculator", "2*21") | Out-Null
    Invoke-Hub @("tool", "call", $sessionA, "/calculator", "6*7") | Out-Null
    $recoveredA = Invoke-Hub @("session", "inspect", $sessionA) | ConvertFrom-Json
    if ($recoveredA.generation -lt 2) { throw "session A generation did not advance" }
    $sessionPIDs += [int]$recoveredA.pid

    $inspectB = Invoke-Hub @("session", "inspect", $sessionB) | ConvertFrom-Json
    $pidB = [int]$inspectB.pid
    $sessionPIDs += $pidB
    Stop-Process -Id $hub.Id -Force
    Start-Sleep -Milliseconds 300
    $hub = Start-CapturedProcess (Join-Path $bin "hub.exe") @("serve") "hub-restarted"
    Wait-Until {
        try {
            Invoke-Hub @("session", "inspect", $sessionA) | Out-Null
            return $true
        } catch {
            return $false
        }
    } "reattached hub"
    $reattachedB = Invoke-Hub @("session", "inspect", $sessionB) | ConvertFrom-Json
    if ([int]$reattachedB.pid -ne $pidB) { throw "hub restart replaced healthy session B" }
    $memoryAfterRestart = Invoke-Hub @("tool", "call", $sessionA, "/memory_search", "@$memorySearchRequest")
    if ($memoryAfterRestart -notmatch '"channels":\["bm25","dense","rerank"\]') {
        throw "memory index did not survive hub restart"
    }

    $stopSession = Invoke-Hub @("session", "create", "StopMe")
    Invoke-Hub @("session", "stop", $stopSession) | Out-Null
    $stopped = Invoke-Hub @("session", "inspect", $stopSession) | ConvertFrom-Json
    if ($stopped.state -ne "stopped") { throw "graceful session stop did not reach stopped state" }

    $stressSessions = @()
    $stressPIDs = @()
    for ($index = 1; $index -le 20; $index++) {
        $stressID = Invoke-Hub @("session", "create", "Stress-$index")
        $stress = Invoke-Hub @("session", "inspect", $stressID) | ConvertFrom-Json
        $stressSessions += $stressID
        $stressPIDs += [int]$stress.pid
        $sessionPIDs += [int]$stress.pid
    }
    if (($stressPIDs | Select-Object -Unique).Count -ne 20) {
        throw "20 sessions did not create 20 distinct sessiond processes"
    }
    Stop-Process -Id $stressPIDs[0] -Force
    Invoke-Hub @("tool", "call", $stressSessions[1], "/calculator", "1+1") | Out-Null

    $compression = Invoke-Hub @("compress", $sessionA, "--auto")
    if ($compression -notmatch "committed=true") { throw "automatic compression failed: $compression" }

    $traceBefore = Invoke-Hub @("trace", $sessionA)
    $statsBefore = Invoke-Hub @("stats", $sessionA)
    $projectionDir = Join-Path $uatRoot "sessions/$sessionA/projections"
    Remove-Item -Recurse -Force $projectionDir
    $traceAfter = Invoke-Hub @("trace", $sessionA)
    $statsAfter = Invoke-Hub @("stats", $sessionA)
    if ($traceBefore -ne $traceAfter -or $statsBefore -ne $statsAfter) { throw "projection rebuild changed results" }
    if (-not (Test-Path (Join-Path $projectionDir "trace.jsonl"))) { throw "trace projection was not rebuilt" }

    Invoke-Hub @("llm", "account", "validate", "fake-chat") | Out-Null
    $deepseekTemplateReport = Invoke-Hub @("llm", "account", "validate", "--file", (Join-Path $root "config/deepseek-account.example.jsonl"))
    if ($deepseekTemplateReport -notmatch "valid=true") {
        throw "DeepSeek account template failed validation: $deepseekTemplateReport"
    }
    $providerTemplateReport = Invoke-Hub @("llm", "account", "validate", "--file", (Join-Path $root "config/embedding-rerank.example.jsonl"))
    if ([regex]::Matches($providerTemplateReport, "valid=true").Count -lt 2) {
        throw "embedding/rerank template failed validation: $providerTemplateReport"
    }
    Invoke-Hub @("llm", "account", "smoke", "fake-embedding", "embedding") | Out-Null
    $smokeCasesPath = Join-Path $uatRoot "smoke-cases.jsonl"
    $smokeCase = @{
        schema_version = 1; case_id = "uat-embedding"; account_id = "fake-embedding"
        capability = "embedding"; enabled = $true; tags = @("uat"); timeout = "5s"
        input = @{ texts = @("uat", "embedding") }
        assertions = @{ vector_count = 2 }
    } | ConvertTo-Json -Compress -Depth 8
    [System.IO.File]::WriteAllText($smokeCasesPath, $smokeCase + "`n")
    $offlineSmoke = Invoke-Hub @("llm", "account", "smoke", "--file", $smokeCasesPath, "--tag", "uat", "--offline")
    if ($offlineSmoke -notmatch "offline case=uat-embedding") { throw "offline smoke failed: $offlineSmoke" }
    $fileSmoke = Invoke-Hub @("llm", "account", "smoke", "--file", $smokeCasesPath, "--tag", "uat")
    if ($fileSmoke -notmatch "passed=true") { throw "file smoke failed: $fileSmoke" }

    $startup = Join-Path $bin "startup.exe"
    & $startup run
    if ($LASTEXITCODE -ne 0) { throw "startup run failed" }
    & $startup install
    if ($LASTEXITCODE -ne 0) { throw "startup install failed" }
    $oldErrorActionPreference = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    $autostart = & $startup status 2>&1
    $startupStatusExit = $LASTEXITCODE
    $ErrorActionPreference = $oldErrorActionPreference
    if ($startupStatusExit -ne 0 -or ($autostart | Out-String) -notmatch "autostart=true") {
        throw "startup status failed: $autostart"
    }
    & $startup uninstall
    if ($LASTEXITCODE -ne 0) { throw "startup uninstall failed" }

    $tuiInput = "/use $sessionA`n/calculator 1+2*3`nplease calc 1+2*3`n/use $sessionB`nhello from B`n/exit`n"
    $tuiOutput = $tuiInput | & (Join-Path $bin "tui.exe") 2>&1 | Out-String
    if ($tuiOutput -notmatch '"value":7') { throw "TUI direct tool failed: $tuiOutput" }
    if ($tuiOutput -notmatch "calculator result is 7") { throw "TUI chat after slash failed: $tuiOutput" }
    if ($tuiOutput -notmatch "fake answer") { throw "TUI chat after session switch failed: $tuiOutput" }

    $leaks = Get-ChildItem -LiteralPath $uatRoot -Recurse -File |
        Where-Object { $_.Extension -in @(".json", ".jsonl", ".log") } |
        Select-String -SimpleMatch "uat-secret"
    if ($leaks) { throw "credential value leaked into persisted/log output" }

    Write-Output "UAT passed: $sessionA / $sessionB"
}
finally {
    foreach ($process in $processes) {
        if ($process -and -not $process.HasExited) {
            Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
        }
    }
    foreach ($sessionPID in ($sessionPIDs | Select-Object -Unique)) {
        Stop-Process -Id $sessionPID -Force -ErrorAction SilentlyContinue
    }
    $resolvedUatRoot = [System.IO.Path]::GetFullPath($uatRoot)
    $resolvedTempRoot = [System.IO.Path]::GetFullPath([System.IO.Path]::GetTempPath())
    if ($resolvedUatRoot.StartsWith($resolvedTempRoot, [System.StringComparison]::OrdinalIgnoreCase) -and
        (Split-Path -Leaf $resolvedUatRoot) -like "vibe-agent-uat-*") {
        Remove-Item -LiteralPath $resolvedUatRoot -Recurse -Force -ErrorAction SilentlyContinue
    }
    foreach ($key in $oldEnvironment.Keys) {
        if ($null -eq $oldEnvironment[$key]) {
            Remove-Item -Path "Env:$key" -ErrorAction SilentlyContinue
        } else {
            Set-Item -Path "Env:$key" -Value $oldEnvironment[$key]
        }
    }
}
