Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$RepoDir = Split-Path -Parent $ScriptDir
$DistDir = Join-Path $RepoDir "dist"

$goCmd = Get-Command go -ErrorAction SilentlyContinue
if (-not $goCmd) {
    throw "go executable not found in PATH"
}
$goExe = $goCmd.Source

New-Item -ItemType Directory -Force -Path $DistDir | Out-Null

Push-Location $RepoDir
try {
    & $goExe test ./...
    $ext = ".so"
    $isWindowsOS = $env:OS -eq "Windows_NT"
    $isMacOSOS = $false
    $isMacOSVar = Get-Variable -Name IsMacOS -ErrorAction SilentlyContinue
    if ($isMacOSVar) {
        $isMacOSOS = [bool]$isMacOSVar.Value
    }
    if ($isWindowsOS) {
        $ext = ".dll"
    }
    elseif ($isMacOSOS) {
        $ext = ".dylib"
    }
    $out = Join-Path $DistDir ("cpa-window-primer" + $ext)
    & $goExe build -buildmode=c-shared -o $out .
    Write-Output "[OK] Built $out"
}
finally {
    Pop-Location
}
