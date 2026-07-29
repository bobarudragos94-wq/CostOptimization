# Data Dictionary & Schema

Schema version: **1.0.0** (`internal/model.SchemaVersion`). Every record is one
NDJSON line carrying `kind`, `schema`, and `ts` (UTC). Host timezone is
preserved in the inventory record. Breaking changes bump the major version and
must be noted here.

## Bundle layout (`.urab` = tar)

| Entry | Content |
|---|---|
| `manifest.json.age` | encrypted `Manifest`: bundle ID, host ID, agent+schema versions, period, config hash, per-segment ciphertext SHA-256 + record counts + time ranges, health summary (expected vs collected minutes, permission issues) |
| `segments/seg-<kind>-<yyyymmdd>-<nnn>.ndjson.gz.age` | encrypted, gzip-compressed NDJSON records of one kind |

## Record kinds

### `inventory` — versioned host snapshot (daily + at start)
host_id (random, stable per deployment), hostname, os, os_version,
kernel_build, boot_time, uptime_s, timezone + tz_offset_sec, virtualization
(`physical_or_unknown` | `vm:<hypervisor>`), cpu_model, physical_cores,
logical_cpus, allocated_vcpu (in-guest view), total_ram_bytes,
swap_total_bytes, disks[], filesystems[] (mount, total/avail), nics[] (name,
mac, speed_mbps, addrs), agent_version, config_hash, privacy_mode.

### `host_minute` — long-term aggregate (1/min)
- `cpu`: avg_pct, max_pct, user/system/iowait/steal_pct, per_core_max[],
  load1/5, run_queue, ctx_switch_ps
- `mem`: used_pct_avg/max, used_bytes_avg, avail_bytes_min, committed_bytes,
  swap_used_bytes, page_in_ps, page_out_ps, major_fault_ps
- `disks[]` per device: read/write_iops, read/write_bytes_ps,
  read/write_lat_ms, queue_depth, busy_pct
- `fs[]`: mount, total/avail bytes (sampled once per minute)
- `net[]` per NIC: rx/tx_bytes_ps, rx/tx_pkts_ps, errs_ps, drops_ps, util_pct
  (when link speed known)
- `psi` (Linux): cpu/mem/io some+full avg10 averages and maxima
- `samples`: high-res samples aggregated into this minute (coverage signal)

### `proc_top` — bounded top-N process snapshot (persisted every 5 min; high-frequency in memory during spikes)
pid, ppid, name, exe (path only — **never arguments**), service_unit
(systemd unit / Windows service), cpu_pct (of host), cpu_time_s (cumulative),
rss_bytes, read/write_bytes_ps, start_time.

### `spike_event`
event_id, resource (`cpu|memory|paging|disk_latency|disk_queue|disk_io|net`),
device, start/end/duration, baseline, peak, threshold, trigger
(`static|baseline_deviation`), pre/during/post sample series (bounded),
top_procs[], core_seconds_by_proc{}, probable_cause, correlated_process,
correlated_service_or_job, attribution_confidence (0–0.9),
attribution_evidence[], validation_required, sql_correlated, sql_instance.

### `sched_snapshot`
entries[]: source (`cron|systemd_timer|win_task|sql_agent`), name, schedule
(cron expression when known), unit, last_run, next_run.

### `sql_inventory` (per instance, 6-hourly)
instance_key (`hostid|instance`), machine/instance name, is_default, version,
build, product_level, edition, start_time, uptime_s, cpu_count_visible,
scheduler_count, phys_mem_visible_kb, pid, service_account, maxdop,
cost_threshold, min/max_server_memory_mb, max_memory_unlimited_default,
lock_pages_in_memory (`detected|not_detected|unknown`), linux_memory_limit_mb,
ha {mode: standalone|fci|alwayson_ag, ags[]: name, role, sync_state,
readable_secondary, partner_replicas[]}, databases[] (name, data/log MB),
collection_level (`deep|process_only`), collection_note.

### `sql_sample` (per instance, 1/min)
From `sys.dm_os_process_memory`: physical_memory_in_use_kb,
locked_page_allocations_kb, large_page_allocations_kb,
memory_utilization_pct, process_physical/virtual_memory_low.
From `sys.dm_os_performance_counters`: total/target_server_memory_kb,
database_cache_kb, stolen_server_memory_kb, free_memory_kb,
memory_grants_pending/active, ple_seconds.
memory_clerks_top_kb{} (top 8). Rates from counter deltas:
batch_requests_ps, compilations_ps, recompilations_ps, page_reads/writes_ps,
lazy_writes_ps, checkpoint_pages_ps. sql_process_cpu_pct (OS-side, see
ARCHITECTURE). wait_deltas{} (top waits, ms + count since previous sample),
blocked_sessions, file_io[] (per db/file-type read/write latency + rate from
`sys.dm_io_virtual_file_stats` deltas), tempdb_used/total_mb.

### `sql_spike_context`
event_id, instance_key, correlation_level (`process_only|agent_job|
active_request|scheduled_job_confirmed|insufficient_evidence`),
active_requests[] (session, db, command, status, cpu/elapsed ms,
logical/physical reads, writes, wait_type, blocking_session, program_name,
query_hash, plan_hash — **no SQL text**), agent_jobs[] (name or pseudonym,
start, duration, outcome, running).

### `health` (5-minutely)
agent_version, agent_uptime_s, samples_collected/dropped, collector_status{}
(`ok|degraded:<why>|unavailable:<why>`), permission_issues[], recent_errors[]
(bounded 50), spool_bytes, self_rss_bytes, self_cpu_pct.

## Compatibility rules

* The analyzer accepts records whose major schema version matches; unknown
  kinds and unknown fields are ignored (forward-compatible additions are
  minor-version changes).
* Golden-format guard: `test/e2e` asserts every kind round-trips through
  export → import with the schema tag intact.
