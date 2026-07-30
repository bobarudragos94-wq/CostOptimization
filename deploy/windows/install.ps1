# URA agent installation for Windows Server / Windows 10+. Run in an elevated
# PowerShell from the unpacked release directory. Fully offline.
#
#   .\install.ps1 [-BinPath .\ura-agent.exe] [-ServiceAccount 'NT SERVICE\ura-agent']
#
# The service is REGISTERED but NOT STARTED until the configuration validates
# (in particular, agent.recipient must be a real age public key — the
# template placeholder is rejected by 'ura-agent check-config'). Re-run this
# script (or Start-Service ura-agent) after fixing the configuration.
#
# Least privilege: default service account is the virtual account
# 'NT SERVICE\ura-agent'. Reading some per-process counters of other users'
# processes may require 'NT AUTHORITY\SYSTEM'; see docs/DEPLOY_WINDOWS.md.
param(
    [string]$BinPath = ".\ura-agent.exe",
    [string]$ServiceAccount = "NT SERVICE\ura-agent"
)
$ErrorActionPreference = "Stop"

$InstallDir = "C:\Program Files\ura-agent"
$DataDir    = "C:\ProgramData\ura-agent"
$ConfigPath = Join-Path $DataDir "agent.yaml"
$ExePath    = Join-Path $InstallDir "ura-agent.exe"

Write-Host "==> creating directories"
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
New-Item -ItemType Directory -Force -Path $DataDir | Out-Null
Copy-Item $BinPath $ExePath -Force

if (-not (Test-Path $ConfigPath)) {
    $template = ".\agent.windows.example.yaml"
    if (-not (Test-Path $template)) { $template = ".\agent.example.yaml" }
    Copy-Item $template $ConfigPath
}

Write-Host "==> restricting configuration ACLs (Administrators + SYSTEM + service account)"
icacls $DataDir /inheritance:r /grant "Administrators:(OI)(CI)F" "SYSTEM:(OI)(CI)F" "$ServiceAccount:(OI)(CI)M" | Out-Null

Write-Host "==> registering Windows service (not started yet)"
$svc = Get-Service -Name "ura-agent" -ErrorAction SilentlyContinue
if ($svc) {
    if ($svc.Status -eq 'Running') { Stop-Service ura-agent -Force }
    sc.exe delete ura-agent | Out-Null
    Start-Sleep 2
}
sc.exe create ura-agent binPath= "`"$ExePath`" run --config `"$ConfigPath`"" `
    start= auto obj= $ServiceAccount DisplayName= "URA Utilization Agent" | Out-Null
sc.exe description ura-agent "Temporary utilization & rightsizing telemetry collector (offline; no listening ports)" | Out-Null
sc.exe failure ura-agent reset= 86400 actions= restart/10000/restart/30000/restart/60000 | Out-Null

Write-Host "==> validating configuration"
& $ExePath check-config --config $ConfigPath
if ($LASTEXITCODE -ne 0) {
    Write-Warning "Configuration is NOT valid — the service was registered but NOT started."
    Write-Warning "Most likely: agent.recipient is still the placeholder."
    Write-Warning "  1. On the analyzer machine: ura-analyzer keygen --out keys"
    Write-Warning "  2. Paste the content of keys\recipient.txt into $ConfigPath (agent.recipient)"
    Write-Warning "  3. Then run:  Start-Service ura-agent   (or re-run this script)"
    exit 1
}

Start-Service ura-agent
$state = (Get-Service ura-agent).Status
Write-Host "==> service state: $state"
if ($state -ne 'Running') {
    Write-Error "service failed to start — check Application event log and $DataDir"
    exit 1
}
Write-Host "==> installed and running. Verify: Get-Service ura-agent"
Write-Host "    For SQL integrated auth, grant the least-privilege login to"
Write-Host "    '$ServiceAccount' (see deploy\sql\setup-least-privilege.sql)."
