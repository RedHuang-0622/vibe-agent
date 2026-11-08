param(
    [string]$Root = "."
)

$ErrorActionPreference = "Stop"
Set-Location -LiteralPath $Root
$packageDirs = & go list -f '{{.Dir}}' ./... | Sort-Object -Unique
$missing = @()
$invalid = @()

foreach ($packageDir in $packageDirs) {
    $readme = Join-Path $packageDir "README.md"
    if (-not (Test-Path -LiteralPath $readme)) {
        $missing += $packageDir
        continue
    }

    $text = Get-Content -Raw -LiteralPath $readme
    foreach ($heading in @("^# ", "^## Role$", "^## API / Methods$", "^## Boundaries$", "^## Verification$")) {
        if ($text -notmatch "(?m)$heading") {
            $invalid += "$readme missing heading $heading"
        }
    }
}

if ($missing.Count -gt 0) {
    Write-Output "Missing package README:"
    $missing | ForEach-Object { Write-Output "  $_" }
}
if ($invalid.Count -gt 0) {
    Write-Output "Invalid package README:"
    $invalid | ForEach-Object { Write-Output "  $_" }
}
if ($missing.Count -gt 0 -or $invalid.Count -gt 0) {
    exit 1
}

Write-Output "package README check passed for $($packageDirs.Count) packages"
