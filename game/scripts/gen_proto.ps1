# game/scripts/gen_proto.ps1
# Generate Go code from game/api/protos/*.proto into game/api/protos/pt/.
# Auto-checks and installs missing tools:
#   protoc (winget Google.Protobuf), protoc-gen-go / protoc-gen-go-grpc (go install).
# NOTE: keep this file ASCII-only. Windows PowerShell 5.1 reads .ps1 without BOM as
# ANSI, and non-ASCII comments can swallow the following newline and break parsing.

$ErrorActionPreference = "Stop"

$root = Resolve-Path (Join-Path $PSScriptRoot "..")
$protoDir = Join-Path $root "api\protos"
$goBin = Join-Path $env:USERPROFILE "go\bin"
$wingetPackages = Join-Path $env:LOCALAPPDATA "Microsoft\WinGet\Packages"

function Find-Protoc {
    $cmd = Get-Command protoc -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }
    # winget may not update PATH in the current session; search its package dir
    $exe = Get-ChildItem -Path $wingetPackages -Recurse -Filter protoc.exe -ErrorAction SilentlyContinue |
        Select-Object -First 1
    if ($exe) { return $exe.FullName }
    return $null
}

# 1. Go plugins: versions aligned with go.mod deps
$needGo = -not ((Get-Command protoc-gen-go -ErrorAction SilentlyContinue) -or
    (Test-Path (Join-Path $goBin "protoc-gen-go.exe")))
if ($needGo) {
    Write-Host "[gen_proto] installing protoc-gen-go@v1.36.11 ..."
    go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
}

$needGRPC = -not ((Get-Command protoc-gen-go-grpc -ErrorAction SilentlyContinue) -or
    (Test-Path (Join-Path $goBin "protoc-gen-go-grpc.exe")))
if ($needGRPC) {
    Write-Host "[gen_proto] installing protoc-gen-go-grpc@v1.5.1 ..."
    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1
}
$env:PATH = "$goBin;$env:PATH"

# 2. protoc compiler
$protoc = Find-Protoc
if (-not $protoc) {
    Write-Host "[gen_proto] installing protoc via winget (Google.Protobuf) ..."
    winget install --id Google.Protobuf -e --accept-package-agreements --accept-source-agreements
    $protoc = Find-Protoc
}
if (-not $protoc) {
    throw "protoc not found; install it first (winget install --id Google.Protobuf -e)"
}
$env:PATH = "$(Split-Path $protoc);$env:PATH"

# 3. Clean generated files, then regenerate
if (-not (Test-Path $protoDir)) {
    throw "proto dir not found: $protoDir"
}
$outDir = Join-Path $protoDir "pt"
if (Test-Path $outDir) {
    Remove-Item -Path (Join-Path $outDir "*.pb.go") -Force
    Write-Host "[gen_proto] cleaned generated files in $outDir"
}
Push-Location $protoDir
try {
    protoc --go_out=. --go-grpc_out=. *.proto
} finally {
    Pop-Location
}
Write-Host "[gen_proto] generated: $protoDir\pt\*.pb.go"
