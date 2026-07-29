# Architecture & Key Design Decisions

## System overview

```
┌─ monitored server ──────────────────────────────┐      ┌─ analyst workstation ─────────┐
│  ura-agent (Windows Service / systemd)          │      │  ura-analyzer                 │
│                                                 │      │                               │
│  gopsutil ─┐                                    │      │  keygen ──► identity (private)│
│  /proc,PSI ├─► sampler ─► minute agg ─► spool   │      │            recipient (public) │
│  SCM/cgroup┘        │                    │      │      │                 │             │
│                     ▼                    ▼      │      │  .urab bundles ─┼─► import    │
│              spike detector ──► attribution     │ USB/ │   (encrypted)   │   verify    │
│                     ▲                │          │ scp  │                 ▼   decrypt   │
│  DMV collectors ────┘                ▼          │─────►│  consolidate ► recommend      │
│  (read-only, loopback)        gzip segments     │      │        │                      │
│                                      │          │      │        ▼                      │
│                     age encrypt ◄────┘          │      │  report.json / .md / .html    │
│                (public key only on server)      │      └───────────────────────────────┘
└─────────────────────────────────────────────────┘
```

Two binaries, one codebase, one schema (`internal/model` — the single source
of truth, versioned via `SchemaVersion`).

## Decision log

### Language: Go

* One codebase compiles to a **single static binary** for Windows and Linux —
  no runtime to install on customer servers, trivial complete uninstall.
* Cross-compilation from any CI machine (`GOOS=windows`), `CGO_ENABLED=0`.
* Low idle footprint (measured: ~40–70 MB RSS, <0.5 % CPU; docs/BENCHMARK.md).
* First-class service integration: `x/sys/windows/svc` and systemd Type=simple.

### Host telemetry: gopsutil v4 (proven component, not reimplemented)

The library powering Telegraf and the Datadog agent. Read-only `/proc`, sysfs,
and Windows API/PDH access. Platform gaps are closed with direct reads:
Linux PSI (`/proc/pressure/*`), `/proc/vmstat` paging counters, sysfs NIC speed.

### Storage: gzip-member NDJSON segments (no embedded database)

Each flush appends **one complete gzip member** to the active per-kind segment
file with a single `write()`; a crash can only tear the final member, and the
multistream reader recovers every earlier record. Segments rotate on UTC day
or size, are finalized by atomic rename with a sidecar metadata file, and are
individually encrypted at export.

Why not SQLite/Parquet: 90 days of minute aggregates is ≈130 k rows/host
(≈1–2 MB/host/day compressed — measured ~120 KB/day in the synthetic fleet).
At that volume a database adds failure modes (WAL recovery, locking, native
deps) without measurable benefit; JSON+gzip is portable, self-describing,
corruption-isolated per day, and diffable for schema evolution. The analyzer
holds a whole fleet in memory comfortably.

* **Spike events are separate segment files** from long-term aggregates, so
  high-resolution capture never bloats the primary series.
* Normal periods: 15 s samples aggregated to 1-minute records (avg/max/min).
  High-resolution data is preserved only around events (pre/during/post,
  bounded).

### Encryption: age (X25519 + ChaCha20-Poly1305 streaming AEAD)

* Reviewed, widely deployed library (`filippo.io/age`); zero custom crypto.
* **Asymmetric by design**: agents hold only the recipient (public) key;
  compromising a monitored server yields nothing decryptable. The customer
  controls the private identity on the analyzer machine.
* Every segment is an independent age stream → per-64 KiB-chunk authentication
  detects tampering *and truncation*; one damaged segment never affects others.
* The manifest additionally pins the SHA-256 of every segment *ciphertext*,
  so segment substitution between bundles is detected.
* Rotation: issue a new identity, update `agent.recipient`; the analyzer
  accepts multiple identities during the overlap.

### Spike engine

Per rule (resource, device): trigger = `value ≥ min(static_threshold,
baseline + k·1.4826·MAD, floored)` sustained for the configured duration.
The baseline is a rolling median over 24 h of samples (robust to the spikes
themselves — event samples are excluded from their own baseline). The rolling
pre-event ring buffer supplies the "before" series; capture is bounded
(120/360/60 points) with shape-preserving decimation. A cooldown prevents
event storms.

**Attribution is structurally honest**: `probable_cause` is always phrased as
probable, confidence is capped at 0.9, `validation_required` stays true, and
every evidence item is listed. Confidence is additive from independent signals:
top process (0.3) + core-second dominance (0.2) + service/unit mapping (0.15)
+ schedule match (0.2) + SQL evidence.

### SQL Server collection

* Detection: process scan (`sqlservr`), Windows registry instance enumeration
  + SCM service→PID mapping, Linux `mssql.conf`. Detection without SQL access
  degrades to `process_only` with the required wording
  *"SQL Server detected; deep SQL telemetry unavailable."* — host collection
  never fails.
* Deep collector: strictly read-only DMV SELECTs behind a `Querier` seam
  (fixture-tested offline). Counter DMVs are cumulative; the collector keeps
  per-instance state and emits deltas/rates.
* **SQL process CPU comes from OS-side measurement of the mapped PID** instead
  of parsing the `RING_BUFFER_SCHEDULER_MONITOR` XML — cheaper, version-
  independent, and consistent with host numbers (documented deviation from
  the dm_os_ring_buffers approach).
* Interpretation rules encoded in the analyzer: max server memory is not a
  process limit; high SQL memory ≠ overprovisioning; Total vs Target only
  meaningful with uptime; PLE only flagged with corroborating signals.

### Analyzer recommendation ladder (explainable, ordered)

1. `insufficient_data` (<3 days or <50 % coverage) → no judgement.
2. `monitor_for_longer` (<7 days) — weekly/month-end cycles unseen.
3. `ha_dr_constraint` — AG secondary: sized for failover, never by own load.
4. Memory pressure / `insufficient_os_headroom` — never downsize under pressure.
5. `optimize_recurring_workload_before_rightsizing` — low baseline + recurring
   spike patterns explain the peaks (checked **before** raw high-CPU, since
   the spikes are what push time-above-threshold up).
6. High CPU → `no_change_recommended`.
7. `likely` (P95 CPU <40 %, mem <60 %, no pressure) /
   `possible` (<55 %/<75 %) rightsizing candidates with **technical ranges**:
   vCPU keeps P95 ≤70 % and P99 peak <100 % of the new size; RAM = P95 used
   × 1.3. Never a cloud SKU.
8. CPU-steal >5 % downgrades any "likely" to "possible" (hypervisor contention
   makes guest numbers understate demand).

Confidence = f(observed days, coverage), capped at 0.9; every category carries
rationale strings and `validation_required`.

### Extension points (v2+; deliberately unimplemented)

* `sqlserver.Querier` / `Discovery` seams → other database engines.
* `model` record kinds are open enums → new collectors add kinds without
  breaking the analyzer (unknown kinds are skipped and counted).
* `analyzer.Dataset` → alternative importers (Azure Monitor export, Prometheus
  remote-read dumps) can populate the same structures; the recommendation
  ladder and reports are source-agnostic. See docs/EXTENSIBILITY.md.
