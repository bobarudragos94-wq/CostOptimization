# Performance & Overhead Benchmark

## Budget (specification §18) vs measured

| Metric | Budget | Measured | Status |
|---|---|---|---|
| Average CPU overhead | ≤ ~1 % | **0.08 %** | ✅ 12× headroom |
| Memory (RSS) | ≤ ~150 MB | **14–16 MB** steady | ✅ 10× headroom |
| Disk usage | configurable cap | ~0.1–2 MB/day/host compressed (see below) + 2 GB default hard cap | ✅ |
| Binary size | — | 8.1 MB static (agent) | |

## Method (reproducible)

Environment: Linux 6.18 x86-64, 4 vCPU/16 GB VM. Agent built with
`-trimpath -ldflags '-s -w'`, default sampling configuration
(host 15 s, proc 60 s, health 5 m).

```
ura-agent run --config bench/agent.yaml &   # scratch data_dir, auto_daily off
# 180 s run; sample ps every 15 s; read cumulative CPU from /proc/<pid>/stat
```

Result: cumulative 0.15 CPU-seconds over 182 s wall = **0.082 %** of one core
(≈0.02 % of this 4-vCPU host). RSS stabilized at 14–16 MB. The agent also
self-reports `self_cpu_pct`/`self_rss_bytes` in every health record, so
overhead is continuously observable inside exported bundles.

## Disk profile

* Minute aggregates + health + proc snapshots: the 14-day synthetic host with
  SQL telemetry exports ≈1.3 MB encrypted (~95 KB/day). A busy host with many
  disks/NICs and frequent spikes should be planned at 1–2 MB/day.
* 90 days ≈ 90–180 MB/host — well under the default 2 GB spool cap.
* Retention enforcement (size + age) is tested; on cap breach the oldest
  closed segments are deleted first and the deletion is counted in health.

## Bounded-resource guarantees (by construction, tested)

* process cardinality: top-N only (default 10 by CPU ∪ 10 by RSS);
* spike capture buffers: 120/360/60 points with decimation;
* in-memory record buffers: flush at 512 records; on disk-full the batch is
  dropped and counted (`samples_dropped`), never queued unboundedly;
* per-segment size cap 32 MB (compressed) forces rotation;
* systemd unit adds kernel-enforced backstops: `MemoryMax=200M`, `CPUQuota=25%`.

## Windows expectation

The same code paths run on Windows (gopsutil uses native APIs); service-side
numbers must be captured during the M8 Windows smoke test. The PDH-free
design avoids the usual WMI-query CPU spikes; expect the same order of
magnitude.

## Re-running

Any environment: `make build && scripts/verify-no-network.sh ./bin/ura-agent`
covers the security half; repeat the ps/proc sampling above (or any APM) for
the overhead half. Numbers materially worse than 0.5 % CPU / 60 MB RSS on
server-class hardware indicate a regression and should block release.
