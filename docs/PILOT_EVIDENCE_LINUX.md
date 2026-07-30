# Linux Smoke-Test Evidence (hardening branch)

The Windows pilot itself **has not been executed** — this environment is
Linux-only. What follows is the evidence from executing the equivalent smoke
procedure (`deploy/linux/smoke-test.sh`, the same steps as
`deploy/windows/smoke-test.ps1`) against the hardened agent on a real Linux
host (4 vCPU / 16 GB VM, kernel 6.18). It validates the cross-platform
mechanisms; it does not validate any Windows-specific code path.

## Run 1 — 22 minutes, 2026-07-30 00:42–01:04 UTC

Procedure: placeholder-rejection check → 6 min baseline → 150 s CPU spike
(3 busy loops) → 4 GiB memory allocation (120 s) → 120 s synced disk writes →
**SIGKILL** → restart → 8 min collection → SIGTERM → export → analyze.

| Check | Result |
|---|---|
| `age1REPLACE_ME` placeholder rejected by check-config | **PASS** |
| Agent socket count at 4 sampling points | **PASS** — 0 sockets throughout |
| CPU spike captured | **PASS** — peak 98.0 %, duration 150 s, attributed to `bash` (the busy loops), confidence 0.7 |
| Forced SIGKILL → restart → spool recovery | **PASS** — new PID, collection resumed, no data loss before the kill |
| Encrypted export after restart | **PASS** — single `.urab`, imported `1 ok / 0 partial / 0 rejected` |
| Analyzer coverage/percentiles | **PASS** — 95.7 % coverage (the SIGKILL gap is visible, as designed) |
| Recommendation honesty | **PASS** — `insufficient_data` (only ~22 min observed) |
| Memory spike captured | **FAIL** — see below |
| Disk spike captured | **MISS (warning)** — container I/O accounting; disk busy% did not cross the threshold |

### Run 1 failure analysis — both findings led to fixes

1. **Memory spike missed — test-parameter error, not a detector bug.** The
   host baseline was 4 % used; 4 GiB on 16 GB raised usage to a 29.6 %
   observed peak, below the fixed 40 % test threshold. The detector was never
   given a crossing to detect. Fix: both smoke scripts now derive the memory
   threshold from the machine's actual baseline (used % + 12) and size the
   allocation relative to RAM, so the controlled load is guaranteed to cross.
2. **Real product bug found by the run:** the captured CPU spike carried
   `correlated_service_or_job = "cron:["` — Debian's stock
   `*/10 * * * * root [ -x /usr/lib/php/sessionclean ]…` crontab line was
   parsed into a job named `[`, and its every-10-minutes schedule matches
   *any* event time within the ±10 min tolerance, silently adding +0.2
   attribution confidence to every spike. Fixed in
   `internal/agent/attribution.go` + `internal/sched`: schedules firing more
   than hourly are excluded from correlation evidence, and mangled inline
   shell names are rejected. Regression tests:
   `TestFrequentAndMangledCronEntriesNeverCorrelate`,
   `TestTooFrequentForCorrelation`.

## Run 2 — 14 minutes, corrected thresholds, post-fix build, 01:06–01:21 UTC

Executed with `deploy/linux/smoke-test.sh` against the rebuilt agent. The
script derived the threshold from the live baseline: *"baseline memory 4% ->
threshold 16%, allocation 4 GiB"*.

| Check | Result |
|---|---|
| Placeholder rejected | **PASS** |
| Sockets at 4 sampling points | **PASS** — 0 socket fds every time (pid 32555, then 2738) |
| CPU spike | **PASS** — peak 100.0 %, 150 s, process `bash`, confidence 0.5 |
| Memory spike | **PASS** — peak 29.7 %, 145 s, process `python3`, confidence 0.3 |
| Disk spike | **MISS** — still not captured (see below) |
| SIGKILL → restart | **PASS** — pid 32555 killed, restarted as 2738, collection resumed |
| Export | **PASS** — one `.urab`; import `1 ok / 0 partial / 0 rejected` |
| Analyzer | **PASS** — 2 spikes, host analyzed |
| Cron overclaiming regression | **PASS** — both spikes now carry `job=''`; the pre-fix build attached `cron:[` to every event |

Confidence values behaved as designed: 0.5 for the CPU event (top process +
core-second dominance, no service/job evidence) and 0.3 for the memory event
(top process only). Neither reached the 0.9 cap, and both retained
`validation_required`.

### Still-unresolved observation: disk spike not captured

120 s of `dd oflag=dsync` did not cross the `disk_io` busy-% threshold on
either run. Most likely the container's block-device accounting
(`/proc/diskstats` for the overlay-backed device) does not reflect the writes
the way a bare-metal or VM disk would. **This path is therefore unvalidated
end-to-end** — it must be exercised during the Windows pilot (where the
smoke script writes with `FlushFileBuffers`-equivalent semantics to a real
volume) or on a Linux host with a real block device before disk-I/O spike
detection is trusted.

## What this evidence does NOT show

* Anything about Windows: service lifecycle, SCM restart, registry SQL
  discovery, integrated auth, PDH-adjacent metrics — all still validated only
  by compilation and unit tests. **Run `deploy/windows/smoke-test.ps1` on the
  pilot machine before trusting the agent there.**
* Deep SQL telemetry against a live SQL Server (no instance in this
  environment; DMV collectors remain fixture-tested only).
* Long-duration behavior (retention cycling over weeks, daily export cadence,
  DST transitions).
* The disk-I/O spike path fired on neither run in this container; it needs
  validation on a machine with honest block-device accounting.
