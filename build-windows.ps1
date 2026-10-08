param(
    [ValidateSet("386", "amd64")]
    [string]$Architecture = "386",
    [string]$MsysRoot = "C:\msys64",
    [int]$Parallel = 14
)

$ErrorActionPreference = "Stop"

$mingwName = if ($Architecture -eq "386") { "mingw32" } else { "mingw64" }
$targetPrefix = if ($Architecture -eq "386") { "i686-w64-mingw32" } else { "x86_64-w64-mingw32" }
$mingwRoot = Join-Path $MsysRoot $mingwName
$gcc = Join-Path $mingwRoot "bin\$targetPrefix-gcc.exe"
$gxx = Join-Path $mingwRoot "bin\$targetPrefix-g++.exe"
$msysBin = Join-Path $MsysRoot "usr\bin"

if (-not (Test-Path $gcc)) {
    throw "Missing $gcc. Install the MSYS2 $mingwName toolchain or pass -MsysRoot."
}
if (-not (Test-Path $gxx)) {
    throw "Missing $gxx. Install the MSYS2 C++ toolchain or pass -MsysRoot."
}

$outputDirectory = Join-Path $PSScriptRoot "dist"
New-Item -ItemType Directory -Force -Path $outputDirectory | Out-Null

$env:GOOS = "windows"
$env:GOARCH = $Architecture
$env:CGO_ENABLED = "1"
$env:CC = $gcc
$env:CXX = $gxx
$env:Path = "$($mingwRoot)\bin;$msysBin;$env:Path"

$output = Join-Path $outputDirectory "gocanledenabler-win$Architecture.exe"
go mod download
if ($LASTEXITCODE -ne 0) {
    throw "go mod download failed."
}

go build -p $Parallel -tags j2534 -ldflags "-H=windowsgui" -o $output .
if ($LASTEXITCODE -ne 0) {
    throw "Go build failed for Windows $Architecture."
}

Write-Host "Built $output"