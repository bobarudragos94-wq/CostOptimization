# Known Limitations (v0.1 MVP)

## Platform

1. **Windows paths are code-complete but not smoke-tested** in this
   environment (Linux-only CI). Cross-compiled and unit-tested via seams;
   milestone M8 (PLAN.md) requires a real Windows Server validation before
   production use.
2. Linux without systemd (SysV/OpenRC) is unsupported for service management;
   the binary itself runs anywhere.
3. Windows gaps in v1, all surfaced in collection health: pages/sec and
   context switches (needs PDH), committed bytes, NIC link speed (needs IP
   Helper), processor queue length. CPU/memory/disk/network core metrics are
   unaffected.
4. "Allocated vCPU" is the in-guest logical CPU count. Hypervisor-side
   truths (reservations, overcommit, host contention beyond steal time) are
   invisible to an offline in-guest agent. CPU-steal is collected and high
   steal automatically downgrades rightsizing confidence.

## Collection

5. Process disk-I/O attribution requires privileges the default service user
   may lack (per-process io counters); degraded → attribution falls back to
   CPU/memory evidence, reported in health.
6. Network spikes are collected per NIC but the `net` spike rule ships
   disabled by default (no per-process network attribution without packet
   inspection, which is out of scope by design).
7. Executable hashing (`exe_sha256`) is schema-supported but not computed in
   v1 (cost/benefit: hashing large binaries during spikes).
8. Disk error counters (SMART) are not collected — requires elevated ioctls.
9. Sub-15-second bursts between samples can be missed; by design (overhead
   trade-off, documented sampling theory limit).

## SQL Server

10. Validated against fixture data modeled on SQL Server 2016–2022 DMVs, not
    yet against a live instance (no SQL Server in the dev environment) — part
    of milestone M8. The Querier seam confines any breakage to query strings.
11. SQL Server versions before 2012 are out of scope.
12. Named-instance connection on Windows uses the SQL Browser service; if
    Browser is disabled, deep collection reports the connection failure and
    falls back to `process_only` (static port configuration is a v2 item).
13. `scheduled_job_confirmed` correlation level requires both a running Agent
    job and schedule-time match; the MVP reaches `agent_job` level.
14. FCI awareness is flag-level (`IsClustered`); cluster-group resources are
    not enumerated.

## Analyzer / recommendations

15. Recommendations are technical capacity assessments over the observed
    window only. Peaks outside the window (month-end, quarterly, DR drills)
    are explicitly the operator's checklist — every category carries
    `validation_required`.
16. No monetary savings (by scope); the fleet summary exposes an indicative
    vCPU/RAM reclaim ceiling only.
17. Pseudonymization (privacy mode) currently covers SQL Agent job names
    only; full hostname/process pseudonymization is schema-ready but
    unimplemented.
18. Bundles are integrity-protected but not source-authenticated (no
    per-agent signing keys yet) — see THREAT_MODEL.md residual risk 1.
19. Report HTML is intentionally minimal (self-contained, no JS); rich
    interactive reporting is a v2 concern.

## Operational

20. The agent keeps collecting past `duration_days` (the value feeds coverage
    math); stopping at the deadline is the uninstall step. Automatic
    self-disable is a v2 nicety.
21. One agent instance per host is assumed (single spool directory).
