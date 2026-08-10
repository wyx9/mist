# game/scripts/gen_configs.ps1
# Generate Go code (*.gen.go) + JSON data (data/*.json) from game/configs/excels/*.xlsx.
# Usage: powershell -NoProfile -ExecutionPolicy Bypass -File scripts/gen_configs.ps1 [-Push]
# NOTE: keep this file ASCII-only. Windows PowerShell 5.1 reads .ps1 without BOM as
# ANSI, and non-ASCII comments can swallow the following newline and break parsing.

param(
    [switch]$Push
)

$ErrorActionPreference = "Stop"

$root = Resolve-Path (Join-Path $PSScriptRoot "..")
$pkgDir = Join-Path $root "configs"
$dataDir = Join-Path $pkgDir "data"

# 1. Clean generated artifacts (only *.gen.go + data\*.json), then regenerate
$pkgResolved = (Resolve-Path $pkgDir).Path
$rootResolved = (Resolve-Path $root).Path
if (-not $pkgResolved.StartsWith($rootResolved)) {
    throw "refusing to clean outside module root: $pkgResolved"
}
Remove-Item -Path (Join-Path $pkgDir "*.gen.go") -Force
if (Test-Path $dataDir) {
    $dataResolved = (Resolve-Path $dataDir).Path
    if (-not $dataResolved.StartsWith($pkgResolved)) {
        throw "refusing to clean outside configs dir: $dataResolved"
    }
    Remove-Item -LiteralPath $dataResolved -Recurse -Force
}
New-Item -ItemType Directory -Force -Path $dataDir | Out-Null

Push-Location $root
try {
    if ($Push) {
        go run ./configs/gen -push
    } else {
        go run ./configs/gen
    }
} finally {
    Pop-Location
}
Write-Host "[gen_configs] generated: $pkgDir\*.gen.go + data\*.json"
