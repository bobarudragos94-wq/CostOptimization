# SQL Server Least-Privilege Model

Setup script: `deploy/sql/setup-least-privilege.sql` (idempotent grants +
teardown block). The agent is strictly read-only: no writes, no user-table
reads, no query text.

## Required grants

| Grant | Used for | Notes |
|---|---|---|
| `VIEW SERVER STATE` | all performance DMVs: `dm_os_process_memory`, `dm_os_sys_info`, `dm_os_performance_counters`, `dm_os_memory_clerks`, `dm_os_wait_stats`, `dm_io_virtual_file_stats`, `dm_exec_requests`, `dm_exec_sessions`, `dm_exec_query_memory_grants`, `dm_hadr_*`, `dm_server_services` | SQL Server 2016–2019: the minimal available grant |
| `VIEW SERVER PERFORMANCE STATE` | same performance DMVs | **SQL Server 2022+ only** — more granular; preferred replacement for VIEW SERVER STATE where available (script contains both variants) |
| `VIEW ANY DEFINITION` | `sys.configurations` values, `sys.master_files` sizes | metadata only |
| `SELECT` on `msdb.dbo.sysjobs`, `sysjobactivity`, `syssessions` | SQL Agent job correlation | optional — without it, spike correlation degrades from `agent_job` to `active_request`/`process_only` and is reported as such |

## Authentication

* **Windows (preferred): integrated authentication.** The agent service
  account gets a Windows login; zero stored credentials. go-mssqldb uses SSPI
  automatically when no credentials are configured.
* **Linux / fallback: dedicated SQL login** (`ura_readonly`). Password rules:
  stored only in the file referenced by `sql.password_file`, owner = service
  user, mode 0600, outside the config file, never logged, never exported,
  read at connect time only. Generate long random passwords; `CHECK_POLICY=ON`.

## Version compatibility

| Version | Status |
|---|---|
| SQL Server 2016 SP1+ / 2017 / 2019 | primary target; all queries verified against documented DMV columns |
| SQL Server 2022+ | supported; optionally switch to `VIEW SERVER PERFORMANCE STATE` |
| SQL Server 2012/2014 | mostly works; `sys.dm_os_memory_clerks.pages_kb` exists since 2012, older builds are out of scope |
| SQL Server on Linux (2017+) | supported; single default instance per host, `mssql.conf` memory limit collected |

## Failure behavior (tested)

Every query degrades independently. Missing `VIEW SERVER STATE` entirely →
the instance is exported at collection level `process_only` with the exact
note **"SQL Server detected; deep SQL telemetry unavailable."**, host
collection continues, and the analyzer prescribes granting the login. Missing
only msdb access → job correlation downgraded, everything else intact. All
denials appear in `permission_issues` inside the bundle.
