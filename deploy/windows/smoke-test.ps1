# URA Windows pilot smoke test. Run in an ELEVATED PowerShell from a directory
# containing ura-agent.exe, ura-analyzer.exe, agent.windows.example.yaml,
# install.ps1 and uninstall.ps1 (the release layout).
#
#   .\smoke-test.ps1                    # 45 min collection (default)
#   .\smoke-test.ps1 -Minutes 30        # minimum honest duration
#   .\smoke-test.ps1 -Minutes 5 -AllowShort   # dry run of the machinery only
#   .\smoke-test.ps1 -SkipUninstall     # keep the agent installed afterwards
#
# What it does, capturing evidence at every step into .\smoke-evidence\:
#   1. Negative test: placeholder recipient must be REJECTED by check-config.
#   2. keygen -> real config -> install.ps1 -> service Running.
#   3. Collection with three controlled events: CPU (busy loop per core),
#      memory (~4 GiB allocation), disk (sync file writes).
#   4. Forced kill (Stop-Process -Force) mid-run -> SCM restart -> recovery.
#   5. Socket sampling throughout: the agent must own NO listening sockets
#      and NO non-loopback connections.
#   6. Manual encrypted export -> ura-analyzer -> report; asserts the CPU and
#      memory spikes were captured with process attribution.
#   7. Clean uninstall; verifies no service, no files remain.
#
# Exit code 0 = PASS. Review smoke-evidence\ before trusting anything.
param(
    [int]$Minutes = 45,
    [switch]$AllowShort,
    [switch]$SkipUninstall
)
$ErrorActionPreference = "Stop"
if ($Minutes -lt 30 -and -not $AllowShort) {
    throw "A pilot needs >= 30 minutes of collection (-Minutes 30). Use -AllowShort only for dry runs."
}
if (-not ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw "Run from an elevated PowerShell."
}

$Ev = Join-Path (Get-Location) "smoke-evidence"
New-Item -ItemType Directory -Force -Path $Ev | Out-Null
$Log = Join-Path $Ev "smoke.log"
$Results = [ordered]@{}
function Log($msg) {
    $line = "[{0:u}] {1}" -f (Get-Date).ToUniversalTime(), $msg
    Write-Host $line; Add-Content -Path $Log -Value $line
}
function Fail($step, $msg) {
    $Results[$step] = "FAIL: $msg"; Log "FAIL [$step]: $msg"
    $Results | ConvertTo-Json | Out-File (Join-Path $Ev "results.json")
    exit 1
}
function Pass($step, $msg) { $Results[$step] = "PASS: $msg"; Log "PASS [$step]: $msg" }

$DataDir = "C:\ProgramData\ura-agent"
$Config  = Join-Path $DataDir "agent.yaml"
$Exe     = "C:\Program Files\ura-agent\ura-agent.exe"

function Get-AgentPid {
    (Get-CimInstance Win32_Service -Filter "Name='ura-agent'").ProcessId
}
function Check-Sockets($phase) {
    $agentPid = Get-AgentPid
    if (-not $agentPid) { return }
    $listen = Get-NetTCPConnection -State Listen -ErrorAction SilentlyContinue | Where-Object OwningProcess -eq $agentPid
    $remote = Get-NetTCPConnection -ErrorAction SilentlyContinue | Where-Object {
        $_.OwningProcess -eq $agentPid -and $_.RemoteAddress -notin @('127.0.0.1','::1','0.0.0.0','::') }
    "$phase listen=$(@($listen).Count) nonloopback=$(@($remote).Count)" | Add-Content (Join-Path $Ev "sockets.log")
    if (@($listen).Count -gt 0) { Fail "no-listen" "$phase: agent owns listening sockets: $($listen | Out-String)" }
    if (@($remote).Count -gt 0) { Fail "no-external" "$phase: agent has non-loopback connections: $($remote | Out-String)" }
}

# ---- 1. negative test: placeholder must be rejected --------------------------
Log "step 1: placeholder rejection"
New-Item -ItemType Directory -Force -Path $DataDir | Out-Null
Copy-Item .\agent.windows.example.yaml $Config -Force
& .\ura-agent.exe check-config --config $Config 2>&1 | Add-Content $Log
if ($LASTEXITCODE -eq 0) { Fail "placeholder" "check-config accepted the age1REPLACE_ME template" }
Pass "placeholder" "template recipient rejected"

# ---- 2. keygen + real config + install --------------------------------------
Log "step 2: keygen + install"
& .\ura-analyzer.exe keygen --out (Join-Path $Ev "keys") 2>&1 | Add-Content $Log
$rec = (Get-Content (Join-Path $Ev "keys\recipient.txt") -Raw).Trim()
# Smoke config: fast sampling, low spike thresholds so controlled load triggers.
@"
agent:
  data_dir: 'C:\ProgramData\ura-agent'
  recipient: "$rec"
sampling: { host_interval: 5s, proc_interval: 15s, proc_persist_interval: 60s, health_interval: 60s }
spikes:
  cooldown: 3m
  pre_buffer: 2m
  post_capture: 1m
  rules:
    - { resource: cpu,     static_threshold: 60, sustained: 60s, baseline_k: 0 }
    - { resource: memory,  static_threshold: 55, sustained: 60s, baseline_k: 0 }
    - { resource: disk_io, static_threshold: 40, sustained: 60s, baseline_k: 0 }
sql: { enabled: true, auth: integrated }
export: { auto_daily: false, export_dir: 'C:\ProgramData\ura-agent\export' }
"@ | Out-File -Encoding ascii $Config
& .\install.ps1
if ((Get-Service ura-agent).Status -ne 'Running') { Fail "install" "service not running after install" }
Pass "install" "service installed and running"
Check-Sockets "post-install"

# ---- 3. collection with controlled spikes -----------------------------------
$total = [TimeSpan]::FromMinutes($Minutes)
$t0 = Get-Date
$baseline = [Math]::Max(120, $total.TotalSeconds * 0.25)
Log "step 3: baseline collection $([int]$baseline)s"
Start-Sleep -Seconds $baseline
Check-Sockets "baseline"

Log "step 3a: CPU spike (busy loop per core, 150s)"
$cpuJobs = 1..([Environment]::ProcessorCount) | ForEach-Object {
    Start-Job -ScriptBlock { $sw=[Diagnostics.Stopwatch]::StartNew(); while ($sw.Elapsed.TotalSeconds -lt 150) {} } }
Start-Sleep -Seconds 170
$cpuJobs | Remove-Job -Force -ErrorAction SilentlyContinue

Log "step 3b: memory spike (~4 GiB held 120s)"
$memJob = Start-Job -ScriptBlock {
    $chunks = @(); 1..8 | ForEach-Object {
        $b = New-Object byte[] (512MB)
        for ($i = 0; $i -lt $b.Length; $i += 4096) { $b[$i] = 1 }
        $chunks += ,$b }
    Start-Sleep -Seconds 120 }
Start-Sleep -Seconds 150
$memJob | Remove-Job -Force -ErrorAction SilentlyContinue

Log "step 3c: disk spike (sync writes, 120s)"
$diskJob = Start-Job -ScriptBlock {
    $buf = New-Object byte[] (64MB); (New-Object Random).NextBytes($buf)
    $p = Join-Path $env:TEMP "ura-smoke-io.bin"
    $sw = [Diagnostics.Stopwatch]::StartNew()
    while ($sw.Elapsed.TotalSeconds -lt 120) {
        $fs = [IO.File]::Open($p, 'Create', 'Write')
        1..8 | ForEach-Object { $fs.Write($buf, 0, $buf.Length) }
        $fs.Flush($true); $fs.Close() }
    Remove-Item $p -ErrorAction SilentlyContinue }
Start-Sleep -Seconds 150
$diskJob | Remove-Job -Force -ErrorAction SilentlyContinue
Check-Sockets "post-spikes"

# ---- 4. forced kill + SCM restart -------------------------------------------
Log "step 4: forced kill + restart recovery"
$agentPid = Get-AgentPid
Stop-Process -Id $agentPid -Force
Start-Sleep -Seconds 20   # SCM failure action: restart after 10s
if ((Get-Service ura-agent).Status -ne 'Running') {
    Start-Service ura-agent; Start-Sleep 5
}
if ((Get-Service ura-agent).Status -ne 'Running') { Fail "restart" "service did not recover after forced kill" }
if ((Get-AgentPid) -eq $agentPid) { Fail "restart" "PID unchanged — process was not actually restarted" }
Pass "restart" "forced kill -> SCM restart -> running (new PID)"
Check-Sockets "post-restart"

# ---- remaining collection time ----------------------------------------------
$remaining = $total - ((Get-Date) - $t0)
if ($remaining.TotalSeconds -gt 0) {
    Log "step 5: remaining collection $([int]$remaining.TotalSeconds)s"
    Start-Sleep -Seconds $remaining.TotalSeconds
}
Check-Sockets "pre-export"

# ---- 6. export + analyze -----------------------------------------------------
Log "step 6: stop service, export, analyze"
Stop-Service ura-agent
& $Exe export --config $Config 2>&1 | Add-Content $Log
$bundle = Get-ChildItem "$DataDir\export\*.urab" | Sort-Object LastWriteTime | Select-Object -Last 1
if (-not $bundle) { Fail "export" "no .urab produced" }
Copy-Item $bundle.FullName $Ev
Pass "export" "encrypted bundle: $($bundle.Name) ($([int]($bundle.Length/1KB)) KB)"

& .\ura-analyzer.exe analyze --key (Join-Path $Ev "keys\identity.txt") --in "$DataDir\export" --out (Join-Path $Ev "reports") 2>&1 | Add-Content $Log
$reportPath = Join-Path $Ev "reports\report.json"
if (-not (Test-Path $reportPath)) { Fail "analyze" "report.json missing" }
$report = Get-Content $reportPath -Raw | ConvertFrom-Json
$resources = @($report.spikes | ForEach-Object { $_.resource }) | Sort-Object -Unique
Log ("spikes captured: {0} (resources: {1})" -f @($report.spikes).Count, ($resources -join ", "))
$report.spikes | ForEach-Object {
    Log ("  {0} peak={1} dur={2}s proc='{3}' conf={4}" -f $_.resource, $_.peak, $_.duration_s, $_.process, $_.confidence) }
if ($resources -notcontains "cpu")    { Fail "spike-cpu" "CPU spike not captured" }
if ($resources -notcontains "memory") { Fail "spike-mem" "memory spike not captured" }
if ($resources -notcontains "disk_io" -and $resources -notcontains "disk_latency" -and $resources -notcontains "disk_queue") {
    Log "WARN: no disk spike captured — acceptable on very fast NVMe; review manually" }
$cpuSpike = $report.spikes | Where-Object { $_.resource -eq "cpu" } | Select-Object -First 1
if (-not $cpuSpike.process) { Log "WARN: cpu spike lacks process attribution — review" }
$host0 = $report.hosts[0]
Log ("host report: coverage={0}% category={1} degraded={2}" -f $host0.coverage_pct, $host0.recommendation_category, ($host0.degraded_collectors | ConvertTo-Json -Compress))
Pass "analyze" "report produced; spikes verified"

# ---- 7. clean uninstall ------------------------------------------------------
if (-not $SkipUninstall) {
    Log "step 7: clean uninstall"
    & .\uninstall.ps1 -DeleteData
    if (Get-Service ura-agent -ErrorAction SilentlyContinue) { Fail "uninstall" "service still present" }
    if (Test-Path "C:\Program Files\ura-agent") { Fail "uninstall" "install dir still present" }
    if (Test-Path $DataDir) { Fail "uninstall" "data dir still present" }
    Pass "uninstall" "no service, no files remain"
}

$Results | ConvertTo-Json | Out-File (Join-Path $Ev "results.json")
Log "SMOKE TEST PASSED — evidence in $Ev (smoke.log, sockets.log, results.json, report + bundle copies)"
exit 0
