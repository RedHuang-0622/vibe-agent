param(
    [string]$Version = "0.1.0",
    [string]$OutputDir = "artifacts"
)

$ErrorActionPreference = "Stop"
$root = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$outputRoot = if ([System.IO.Path]::IsPathRooted($OutputDir)) {
    [System.IO.Path]::GetFullPath($OutputDir)
} else {
    [System.IO.Path]::GetFullPath((Join-Path $root $OutputDir))
}
$packageName = "vibe-agent-windows-amd64-$Version"
$stage = Join-Path $outputRoot $packageName
$zipPath = Join-Path $outputRoot "$packageName.zip"

Push-Location $root
try {
    if (Test-Path -LiteralPath $stage) {
        $resolvedStage = [System.IO.Path]::GetFullPath($stage)
        $resolvedOutput = [System.IO.Path]::GetFullPath($outputRoot)
        if (-not $resolvedStage.StartsWith($resolvedOutput + [System.IO.Path]::DirectorySeparatorChar, [System.StringComparison]::OrdinalIgnoreCase) -or
            (Split-Path -Leaf $resolvedStage) -ne $packageName) {
            throw "refusing to replace unexpected staging directory: $resolvedStage"
        }
        Remove-Item -LiteralPath $resolvedStage -Recurse -Force
    }
    New-Item -ItemType Directory -Path (Join-Path $stage "bin") -Force | Out-Null
    New-Item -ItemType Directory -Path (Join-Path $stage "config") -Force | Out-Null

    go build -trimpath -o (Join-Path $stage "bin/hub.exe") ./cmd/hub
    if ($LASTEXITCODE -ne 0) { throw "hub build failed" }
    go build -trimpath -o (Join-Path $stage "bin/sessiond.exe") ./cmd/sessiond
    if ($LASTEXITCODE -ne 0) { throw "sessiond build failed" }
    go build -trimpath -o (Join-Path $stage "bin/startup.exe") ./cmd/startup
    if ($LASTEXITCODE -ne 0) { throw "startup build failed" }
    go build -trimpath -o (Join-Path $stage "bin/tui.exe") ./cmd/tui
    if ($LASTEXITCODE -ne 0) { throw "tui build failed" }

    $configFiles = @(
        "config/README.md",
        "config/llm-accounts.example.jsonl",
        "config/deepseek-account.example.jsonl",
        "config/embedding-rerank.example.jsonl",
        "config/llm-smoke.example.jsonl",
        "config/llm-accounts.local.jsonl",
        "config/llm-smoke.local.jsonl"
    )
    foreach ($file in $configFiles) {
        Copy-Item -LiteralPath (Join-Path $root $file) -Destination (Join-Path $stage $file)
    }
    foreach ($file in Get-ChildItem -LiteralPath (Join-Path $stage "config") -Filter "*.jsonl") {
        foreach ($line in Get-Content -LiteralPath $file.FullName) {
            if (-not $line.Trim()) { continue }
            $record = $line | ConvertFrom-Json
            foreach ($service in $record.services.PSObject.Properties) {
                if ($service.Value.api_key_env -and $service.Value.api_key_env -notmatch '^[A-Za-z_][A-Za-z0-9_]*$') {
                    throw "non-environment credential reference in $($file.Name)"
                }
            }
        }
    }
    Copy-Item -LiteralPath (Join-Path $root "README.md") -Destination (Join-Path $stage "README.md")
    Copy-Item -LiteralPath (Join-Path $root ".env.example") -Destination (Join-Path $stage ".env.example")
    $launcherName = ([char]0x542F).ToString() + ([char]0x52A8).ToString() + "TUI.bat"
    Copy-Item -LiteralPath (Join-Path $root "packaging/start-tui.bat") -Destination (Join-Path $stage $launcherName)

    $versionText = @(
        "version=$Version"
        "platform=windows-amd64"
        "built_at=$((Get-Date).ToUniversalTime().ToString('o'))"
        "credential_policy=environment-only"
    )
    [System.IO.File]::WriteAllLines((Join-Path $stage "VERSION.txt"), $versionText)

    $packageReadme = @'
# vibe_agent Windows package

## Start

Double-click the provided launcher BAT. It will:

- keep runtime data under the package's `data/` directory;
- prompt for `DEEPSEEK_API_KEY` when the environment variable is absent;
- start the hidden hub and import the local account configuration on first use;
- launch `tui.exe`.

Manual start:

1. Set the provider key in the current process environment:

   ```powershell
   $env:DEEPSEEK_API_KEY = '<your-key>'
   ```

2. Import the local account configuration:

   ```powershell
   .\bin\hub.exe llm account add --file .\config\llm-accounts.local.jsonl
   ```

3. Start the TUI:

   ```powershell
   .\bin\tui.exe
   ```

## Configuration

- `config/llm-accounts.local.jsonl` references `env:DEEPSEEK_API_KEY`.
- Example files contain placeholders only.
- This package does not contain a plaintext API key.
- Keep local configuration out of source control and GitHub releases.
'@
    [System.IO.File]::WriteAllText((Join-Path $stage "PACKAGE-README.md"), $packageReadme)

    $secretPattern = 'sk-[A-Za-z0-9]{20,}'
    $leaks = Get-ChildItem -LiteralPath $stage -Recurse -File |
        Select-String -Pattern $secretPattern -ErrorAction SilentlyContinue
    if ($leaks) {
        throw "refusing to package plaintext provider keys"
    }

    $files = Get-ChildItem -LiteralPath $stage -Recurse -File |
        Sort-Object FullName |
        ForEach-Object {
            $relative = $_.FullName.Substring($stage.Length).TrimStart("\", "/").Replace("\", "/")
            $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $_.FullName).Hash.ToLowerInvariant()
            "$hash  $relative"
        }
    [System.IO.File]::WriteAllLines((Join-Path $stage "SHA256SUMS.txt"), $files)

    if (Test-Path -LiteralPath $zipPath) {
        Remove-Item -LiteralPath $zipPath -Force
    }
    Compress-Archive -LiteralPath $stage -DestinationPath $zipPath -CompressionLevel Optimal
    $zipHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $zipPath).Hash.ToLowerInvariant()
    [System.IO.File]::WriteAllText("$zipPath.sha256", "$zipHash  $packageName.zip`r`n")

    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $archive = [System.IO.Compression.ZipFile]::OpenRead($zipPath)
    try {
        $entries = $archive.Entries.Count
    }
    finally {
        $archive.Dispose()
    }
    Write-Output "PACKAGE=$zipPath"
    Write-Output "SHA256=$zipHash"
    Write-Output "ENTRIES=$entries"
}
finally {
    Pop-Location
}
