# URA agent installation for Windows Server. Run in an elevated PowerShell
# from the unpacked release directory. Fully offline; nothing is downloaded.
#
#   .\install.ps1 [-BinPath .\ura-agent.exe] [-ServiceAccount 'NT SERVICE\ura-agent']
#
# Least privilege: by default the service runs as the virtual account
# 'NT SERVICE\ura-agent' (no password, minimal rights). Reading some
# per-process counters of other users' processes may require running as
# 'NT AUTHORITY\SYSTEM' — a documented trade-off; see docs/DEPLOY_WINDOWS.md.
param(
    [string]$BinPath = ".\ura-agent.exe",
    [string]$ServiceAccount = "NT SERVICE\ura-agent"
)
$ErrorActionPreference = "Stop"

$InstallDir = "C:\Program Files\ura-agent"
$DataDir    = "C:\ProgramData\ura-agent"

Write-Host "==> creating directories"
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
New-Item -ItemType Directory -Force -Path $DataDir | Out-Null
Copy-Item $BinPath (Join-Path $InstallDir "ura-agent.exe") -Force

if (-not (Test-Path (Join-Path $DataDir "agent.yaml"))) {
    Copy-Item .\agent.example.yaml (Join-Path $DataDir "agent.yaml")
    Write-Host "==> EDIT $DataDir\agent.yaml and set agent.recipient (from 'ura-analyzer keygen')"
}

Write-Host "==> restricting configuration ACLs (Administrators + service account only)"
icacls $DataDir /inheritance:r /grant "Administrators:(OI)(CI)F" "SYSTEM:(OI)(CI)F" "$ServiceAccount:(OI)(CI)M" | Out-Null

Write-Host "==> validating configuration"
& (Join-Path $InstallDir "ura-agent.exe") check-config --config (Join-Path $DataDir "agent.yaml")

Write-Host "==> registering Windows service"
$svc = Get-Service -Name "ura-agent" -ErrorAction SilentlyContinue
if ($svc) { sc.exe delete ura-agent | Out-Null; Start-Sleep 2 }
sc.exe create ura-agent binPath= "`"$InstallDir\ura-agent.exe`" run --config `"$DataDir\agent.yaml`"" `
    start= auto obj= $ServiceAccount DisplayName= "URA Utilization Agent" | Out-Null
sc.exe description ura-agent "Temporary utilization & rightsizing telemetry collector (offline; no listening ports)" | Out-Null
sc.exe failure ura-agent reset= 86400 actions= restart/10000/restart/30000/restart/60000 | Out-Null

Start-Service ura-agent
Write-Host "==> installed and started. Verify: Get-Service ura-agent"
Write-Host "    For SQL integrated auth, grant the documented least-privilege"
Write-Host "    login to '$ServiceAccount' (see deploy\sql\setup-least-privilege.sql)."
