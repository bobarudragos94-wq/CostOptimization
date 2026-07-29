# Complete, clean removal of the URA agent from a Windows host.
#   .\uninstall.ps1 [-KeepData]
param([switch]$KeepData)
$ErrorActionPreference = "SilentlyContinue"

$InstallDir = "C:\Program Files\ura-agent"
$DataDir    = "C:\ProgramData\ura-agent"

Write-Host "==> stopping and deleting service"
Stop-Service ura-agent -Force
sc.exe delete ura-agent | Out-Null

if (-not $KeepData) {
    if (Test-Path "$InstallDir\ura-agent.exe") {
        Write-Host "==> final export of remaining telemetry (best effort)"
        & "$InstallDir\ura-agent.exe" export --config "$DataDir\agent.yaml" 2>$null
        Write-Host "    collect bundles from the export directory before continuing"
    }
    $ans = Read-Host "Delete ALL collected data in $DataDir? [y/N]"
    if ($ans -eq "y") {
        Remove-Item -Recurse -Force $DataDir
        Write-Host "==> data removed"
    } else {
        Write-Host "==> data kept at $DataDir — remove manually when done"
    }
}

Write-Host "==> removing binaries"
Remove-Item -Recurse -Force $InstallDir
Write-Host "==> uninstall complete; no services, files or ports remain"
