# Windows Pilot Smoke Test

Purpose: the first safe pilot on a real Windows machine. The automated script
`deploy/windows/smoke-test.ps1` performs the complete procedure and collects
evidence; this document describes what it does, what "pass" means, and what a
pass does **not** prove.

## Prerequisites

* Windows 10/11 or Windows Server 2016+, elevated PowerShell.
* A directory containing: `ura-agent.exe`, `ura-analyzer.exe`,
  `agent.windows.example.yaml`, `install.ps1`, `uninstall.ps1`,
  `smoke-test.ps1` (the `dist/windows` release layout plus the analyzer exe).
* No production SQL Server dependency: if an instance exists it will be
  detected and integrated-auth collection attempted; absence is fine.
* The machine may get visibly busy for ~7 minutes (deliberate CPU/memory/
  disk load). Close latency-sensitive work first.

## Run

```powershell
.\smoke-test.ps1                 # 45 minutes (default)
.\smoke-test.ps1 -Minutes 30     # minimum meaningful duration
.\smoke-test.ps1 -Minutes 5 -AllowShort   # dry run of the machinery only
```

## What it verifies (in order)

| Step | Assertion |
|---|---|
| placeholder | `check-config` REJECTS the template `age1REPLACE_ME` recipient; the service is never started on an invalid config |
| install | `install.ps1` → service registered under `NT SERVICE\ura-agent` and Running |
| sockets | at six sampling points: the agent process owns **zero listening sockets** and zero non-loopback connections (`sockets.log`) |
| CPU spike | a per-core busy loop (150 s) is captured as a `cpu` spike event |
| memory spike | a ~4 GiB allocation (120 s) is captured as a `memory` spike event |
| disk spike | 120 s of synced writes; warning-only (very fast NVMe may not cross thresholds) |
| restart | `Stop-Process -Force` on the agent → SCM failure action restarts it with a new PID; spool recovery is exercised |
| export | manual `ura-agent export` produces a `.urab` bundle |
| analyze | `ura-analyzer analyze` decrypts and produces reports; the CPU and memory events appear with process attribution |
| uninstall | `uninstall.ps1` leaves no service, no Program Files entry, no ProgramData directory |

Evidence is written to `.\smoke-evidence\`: `smoke.log` (timestamped
transcript), `sockets.log`, `results.json` (per-step PASS/FAIL), the exported
bundle, the decrypted reports, and the generated keys (smoke keys — discard
after the test).

## What a pass does NOT prove

* Nothing about **deep SQL telemetry** unless a local SQL Server instance
  with the least-privilege login was present — run the pilot on a machine
  with SQL Server and check the report's SQL section separately.
* Nothing about 30–90 **days** of operation (retention cycling, daily
  exports, DST transitions, log growth). The pilot validates mechanisms, not
  longevity.
* Nothing about domain policies (AppLocker/WDAC, EDR interference, GPO
  service hardening) on production servers — pilot those environments
  explicitly.
* The spike thresholds used are deliberately low test values; production
  deployments use the defaults in `agent.windows.example.yaml`.
