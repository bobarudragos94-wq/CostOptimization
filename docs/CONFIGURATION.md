# Configuration Reference (`agent.yaml`)

Annotated example: `configs/agent.example.yaml`. Validation:
`ura-agent check-config --config <path>`. The SHA-256 of the effective
configuration is stamped into every inventory record and bundle manifest, so
reports can prove which settings produced the data. **The config must never
contain secrets** — SQL passwords live in the separate `sql.password_file`.

## agent

| Key | Default | Meaning |
|---|---|---|
| `data_dir` | `/var/lib/ura-agent` · `C:\ProgramData\ura-agent` | spool, state, export root |
| `duration_days` | 30 | planned monitoring window (1–365); informational for coverage math |
| `recipient` | — (required) | age public key from `ura-analyzer keygen` |
| `privacy_mode` | false | pseudonymize SQL Agent job names in exports (schema-supported; broader pseudonymization is v2) |

## sampling

| Key | Default | Notes |
|---|---|---|
| `host_interval` | 15s | cheap host metrics; ≥1s |
| `proc_interval` | 60s | top-N process sampling (also every 15 s while a spike capture is active) |
| `proc_top_n` | 10 | bounded cardinality, 1–100 |
| `proc_persist_interval` | 5m | how often a snapshot is persisted long-term |
| `sql_sample_interval` | 60s | light DMV sample per instance |
| `sql_inventory_interval` | 6h | instance inventory + HA topology refresh |
| `health_interval` | 5m | collection-health records |
| `sched_interval` | 6h | cron/timer/task snapshot |

## retention

| Key | Default | Notes |
|---|---|---|
| `max_spool_mb` | 2048 | hard disk cap; oldest closed segments deleted first, deletions counted in health |
| `max_age_days` | 100 | age-based deletion |
| `keep_after_export` | false | keep spooled segments after successful export |

## spikes

`cooldown` (10m), `pre_buffer` (5m), `post_capture` (3m), and per-resource
`rules`: `resource` (`cpu|memory|paging|disk_latency|disk_queue|disk_io|net`),
`static_threshold` (units: % for cpu/memory/disk_io/net, ms for disk_latency,
requests for disk_queue, events/s for paging), `sustained` (duration the
threshold must hold), `baseline_k` (MAD multiplier for baseline deviation;
0 disables), `baseline_floor` (baseline trigger never drops below this).

The `net` rule fires on **interface utilization percent** and therefore only
evaluates NICs whose link speed is known (sysfs on Linux; unknown on Windows
in v1 — the rule is inert there and this is surfaced in the report rather
than silently ignored).

Scheduled-job correlation (cron / Task Scheduler times) is evaluated in the
**host's local timezone** including DST, since that is when those schedulers
actually fire; all stored timestamps remain UTC.

## sql

| Key | Default | Notes |
|---|---|---|
| `enabled` | true | detection alone is always on; this gates deep collection |
| `auth` | `integrated` (Win) / `sqllogin` (Linux) | |
| `username` / `password_file` | — | sqllogin mode only; file must be 0600 |
| `connect_timeout` | 5s | |
| `query_timeout` | 10s | hard bound on every individual DMV query (≥1s). SQL work additionally runs on its own goroutine, so a stalled instance can never block host collection — worst case is a skipped SQL round plus a health entry |
| `collect_sql_text` | false | **hard-disabled in v1** — the collector never selects SQL text regardless of this value |

## export

| Key | Default | Notes |
|---|---|---|
| `auto_daily` | true | one bundle per UTC day, shortly after midnight |
| `export_dir` | `<data_dir>/export` | where `.urab` files appear |
