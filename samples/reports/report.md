# Infrastructure Utilization & Rightsizing Report

Generated 2026-07-30 00:47 UTC · schema 1.0.0 · analyzer 0.1.0 · fully offline

## Fleet overview

| Hosts | SQL instances | Allocated vCPU | Allocated RAM | Spike events |
|---|---|---|---|---|
| 6 | 3 | 68 | 248 GB | 29 |

Recommendation categories:

- `no_change_recommended`: 1 host(s)
- `ha_dr_constraint`: 1 host(s)
- `likely_rightsizing_candidate`: 1 host(s)
- `optimize_recurring_workload_before_rightsizing`: 1 host(s)
- `insufficient_os_headroom`: 2 host(s)

Indicative upper bound of reclaimable capacity (only candidates, before validation): **4 vCPU, 6 GB RAM**. This is a technical ceiling, not a commitment.

## Host summaries

### web-linux-01 (`ura-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa01`, linux)

| | |
|---|---|
| OS | linux Ubuntu 22.04 |
| Virtualization | vm:vmware |
| Allocated | 8 vCPU, 16 GB RAM |
| Observed | 14 days at 99% coverage |
| CPU % (P50/P95/P99/max) | 12 / 19 / 20 / 25 |
| Longest sustained CPU >70% | 0 min (peak 0%) |
| Memory % (P50/P95/P99/max) | 33 / 35 / 35 / 35 (P95 ≈ 5.6 GB) |
| Memory pressure | — |
| Disk I/O | latency P95 3.4 ms, busy P95 14% |
| Network | rx P95 3.0 MB/s, tx P95 1.5 MB/s |
| Spikes | 0 event(s), 0 recurring pattern(s) |


**Recommendation: `likely_rightsizing_candidate`** (confidence 66%, validation required)

Utilization is consistently far below allocation with no pressure evidence. Strong technical rightsizing candidate — validate with the application owner before resizing.

- suggested vCPU range: **3–4** (currently 8)
- suggested RAM range: **8–10 GB** (currently 16 GB)
- keeps CPU P95 ≤ 70% and RAM P95 + 30% headroom; validate against month-end/quarterly peaks not observed in the window

Rationale:
- CPU P95 19% and memory P95 35% of allocation over 14 days
- longest sustained CPU period above 70%: 0 minutes

### batch-linux-02 (`ura-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa02`, linux)

| | |
|---|---|
| OS | linux Ubuntu 22.04 |
| Virtualization | vm:vmware |
| Allocated | 8 vCPU, 32 GB RAM |
| Observed | 14 days at 100% coverage |
| CPU % (P50/P95/P99/max) | 14 / 20 / 92 / 100 |
| Longest sustained CPU >70% | 27 min (peak 95%) |
| Memory % (P50/P95/P99/max) | 40 / 41 / 41 / 41 (P95 ≈ 13.1 GB) |
| Memory pressure | — |
| Disk I/O | latency P95 3.4 ms, busy P95 15% |
| Network | rx P95 3.0 MB/s, tx P95 1.5 MB/s |
| Spikes | 14 event(s), 1 recurring pattern(s) |

- 🔁 cpu spike daily around 01:59 UTC (14 occurrences on 14 of 14 observed days, mean peak 96, mean duration 25m0s)
- probable correlate: python3 (etl-nightly.service) ×14

**Recommendation: `optimize_recurring_workload_before_rightsizing`** (confidence 66%, validation required)

Baseline utilization is low, but recurring scheduled workloads drive the peaks. Optimize or reschedule those jobs first; rightsizing before that risks extending the job windows.

Rationale:
- cpu spike daily around 01:59 UTC (14 occurrences on 14 of 14 observed days, mean peak 96, mean duration 25m0s)

### sql-win-01 (`ura-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa03`, windows)

| | |
|---|---|
| OS | windows Microsoft Windows Server 2022 Standard |
| Virtualization | vm:vmware |
| Allocated | 16 vCPU, 64 GB RAM |
| Observed | 14 days at 100% coverage |
| CPU % (P50/P95/P99/max) | 25 / 37 / 92 / 100 |
| Longest sustained CPU >70% | 27 min (peak 96%) |
| Memory % (P50/P95/P99/max) | 90 / 91 / 91 / 91 (P95 ≈ 58.1 GB) |
| Memory pressure | — |
| Disk I/O | latency P95 3.4 ms, busy P95 14% |
| Network | rx P95 3.0 MB/s, tx P95 1.5 MB/s |
| Spikes | 14 event(s), 1 recurring pattern(s) |
| SQL instances | ura-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa03|MSSQLSERVER |

- 🔁 cpu spike daily around 01:59 UTC (14 occurrences on 14 of 14 observed days, mean peak 96, mean duration 25m0s)
- probable correlate: sqlservr.exe (MSSQLSERVER) ×14

**Recommendation: `insufficient_os_headroom`** (confidence 66%, validation required)

Memory headroom is insufficient; do not reduce resources. Consider investigating consumers or adding RAM.

Rationale:
- memory P95 91% with pressure evidence []

### sqlag-win-02 (`ura-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa04`, windows)

| | |
|---|---|
| OS | windows Microsoft Windows Server 2022 Standard |
| Virtualization | vm:vmware |
| Allocated | 16 vCPU, 64 GB RAM |
| Observed | 14 days at 100% coverage |
| CPU % (P50/P95/P99/max) | 47 / 65 / 66 / 72 |
| Longest sustained CPU >70% | 0 min (peak 0%) |
| Memory % (P50/P95/P99/max) | 82 / 83 / 83 / 83 (P95 ≈ 53.0 GB) |
| Memory pressure | — |
| Disk I/O | latency P95 3.4 ms, busy P95 14% |
| Network | rx P95 3.0 MB/s, tx P95 1.5 MB/s |
| Spikes | 0 event(s), 0 recurring pattern(s) |
| SQL instances | ura-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa04|MSSQLSERVER |


**Recommendation: `no_change_recommended`** (confidence 66%, validation required)

Memory utilization or pressure is high enough that reducing capacity would create risk. No reduction recommended.

Rationale:
- memory P95 83% with pressure evidence []

### sqlag-win-03 (`ura-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa05`, windows)

| | |
|---|---|
| OS | windows Microsoft Windows Server 2022 Standard |
| Virtualization | vm:vmware |
| Allocated | 16 vCPU, 64 GB RAM |
| Observed | 14 days at 100% coverage |
| CPU % (P50/P95/P99/max) | 8 / 12 / 13 / 18 |
| Longest sustained CPU >70% | 0 min (peak 0%) |
| Memory % (P50/P95/P99/max) | 76 / 77 / 77 / 77 (P95 ≈ 49.2 GB) |
| Memory pressure | — |
| Disk I/O | latency P95 3.4 ms, busy P95 14% |
| Network | rx P95 3.0 MB/s, tx P95 1.5 MB/s |
| Spikes | 0 event(s), 0 recurring pattern(s) |
| SQL instances | ura-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa05|MSSQLSERVER |


**Recommendation: `ha_dr_constraint`** (confidence 66%, validation required)

This host is an Always On secondary replica. Its low utilization is expected; it must be sized for the primary's workload after failover. Evaluate together with its replica group only.

Rationale:
- local SQL instance reports SECONDARY availability-group role

### mem-linux-03 (`ura-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa06`, linux)

| | |
|---|---|
| OS | linux Ubuntu 22.04 |
| Virtualization | vm:vmware |
| Allocated | 4 vCPU, 8 GB RAM |
| Observed | 14 days at 100% coverage |
| CPU % (P50/P95/P99/max) | 37 / 49 / 50 / 56 |
| Longest sustained CPU >70% | 0 min (peak 0%) |
| Memory % (P50/P95/P99/max) | 94 / 96 / 96 / 96 (P95 ≈ 7.7 GB) |
| Memory pressure | memory PSI pressure (some avg 10.0%); memory PSI pressure (some avg 10.1%); memory PSI pressure (some avg 10.2%); memory PSI pressure (some avg 10.3%); memory PSI pressure (some avg 10.4%); memory PSI pressure (some avg 10.5%); memory PSI pressure (some avg 10.6%); memory PSI pressure (some avg 10.7%); memory PSI pressure (some avg 10.8%); memory PSI pressure (some avg 10.9%); memory PSI pressure (some avg 11.0%); memory PSI pressure (some avg 11.1%); memory PSI pressure (some avg 11.2%); memory PSI pressure (some avg 11.3%); memory PSI pressure (some avg 11.4%); memory PSI pressure (some avg 11.5%); memory PSI pressure (some avg 11.6%); memory PSI pressure (some avg 11.7%); memory PSI pressure (some avg 11.8%); memory PSI pressure (some avg 11.9%); memory PSI pressure (some avg 12.0%); memory PSI pressure (some avg 12.1%); memory PSI pressure (some avg 12.2%); memory PSI pressure (some avg 12.3%); memory PSI pressure (some avg 12.4%); memory PSI pressure (some avg 12.5%); memory PSI pressure (some avg 12.6%); memory PSI pressure (some avg 12.7%); memory PSI pressure (some avg 12.8%); memory PSI pressure (some avg 12.9%); memory PSI pressure (some avg 13.0%); memory PSI pressure (some avg 13.1%); memory PSI pressure (some avg 13.2%); memory PSI pressure (some avg 13.3%); memory PSI pressure (some avg 13.4%); memory PSI pressure (some avg 13.5%); memory PSI pressure (some avg 13.6%); memory PSI pressure (some avg 13.7%); memory PSI pressure (some avg 13.8%); memory PSI pressure (some avg 13.9%); memory PSI pressure (some avg 14.0%); memory PSI pressure (some avg 8.0%); memory PSI pressure (some avg 8.1%); memory PSI pressure (some avg 8.2%); memory PSI pressure (some avg 8.3%); memory PSI pressure (some avg 8.4%); memory PSI pressure (some avg 8.5%); memory PSI pressure (some avg 8.6%); memory PSI pressure (some avg 8.7%); memory PSI pressure (some avg 8.8%); memory PSI pressure (some avg 8.9%); memory PSI pressure (some avg 9.0%); memory PSI pressure (some avg 9.1%); memory PSI pressure (some avg 9.2%); memory PSI pressure (some avg 9.3%); memory PSI pressure (some avg 9.4%); memory PSI pressure (some avg 9.5%); memory PSI pressure (some avg 9.6%); memory PSI pressure (some avg 9.7%); memory PSI pressure (some avg 9.8%); memory PSI pressure (some avg 9.9%); sustained paging activity observed |
| Disk I/O | latency P95 3.4 ms, busy P95 14% |
| Network | rx P95 3.0 MB/s, tx P95 1.5 MB/s |
| Spikes | 1 event(s), 0 recurring pattern(s) |

- probable correlate: java ×1

**Recommendation: `insufficient_os_headroom`** (confidence 66%, validation required)

Memory headroom is insufficient; do not reduce resources. Consider investigating consumers or adding RAM.

Rationale:
- memory P95 96% with pressure evidence [memory PSI pressure (some avg 10.0%) memory PSI pressure (some avg 10.1%) memory PSI pressure (some avg 10.2%) memory PSI pressure (some avg 10.3%) memory PSI pressure (some avg 10.4%) memory PSI pressure (some avg 10.5%) memory PSI pressure (some avg 10.6%) memory PSI pressure (some avg 10.7%) memory PSI pressure (some avg 10.8%) memory PSI pressure (some avg 10.9%) memory PSI pressure (some avg 11.0%) memory PSI pressure (some avg 11.1%) memory PSI pressure (some avg 11.2%) memory PSI pressure (some avg 11.3%) memory PSI pressure (some avg 11.4%) memory PSI pressure (some avg 11.5%) memory PSI pressure (some avg 11.6%) memory PSI pressure (some avg 11.7%) memory PSI pressure (some avg 11.8%) memory PSI pressure (some avg 11.9%) memory PSI pressure (some avg 12.0%) memory PSI pressure (some avg 12.1%) memory PSI pressure (some avg 12.2%) memory PSI pressure (some avg 12.3%) memory PSI pressure (some avg 12.4%) memory PSI pressure (some avg 12.5%) memory PSI pressure (some avg 12.6%) memory PSI pressure (some avg 12.7%) memory PSI pressure (some avg 12.8%) memory PSI pressure (some avg 12.9%) memory PSI pressure (some avg 13.0%) memory PSI pressure (some avg 13.1%) memory PSI pressure (some avg 13.2%) memory PSI pressure (some avg 13.3%) memory PSI pressure (some avg 13.4%) memory PSI pressure (some avg 13.5%) memory PSI pressure (some avg 13.6%) memory PSI pressure (some avg 13.7%) memory PSI pressure (some avg 13.8%) memory PSI pressure (some avg 13.9%) memory PSI pressure (some avg 14.0%) memory PSI pressure (some avg 8.0%) memory PSI pressure (some avg 8.1%) memory PSI pressure (some avg 8.2%) memory PSI pressure (some avg 8.3%) memory PSI pressure (some avg 8.4%) memory PSI pressure (some avg 8.5%) memory PSI pressure (some avg 8.6%) memory PSI pressure (some avg 8.7%) memory PSI pressure (some avg 8.8%) memory PSI pressure (some avg 8.9%) memory PSI pressure (some avg 9.0%) memory PSI pressure (some avg 9.1%) memory PSI pressure (some avg 9.2%) memory PSI pressure (some avg 9.3%) memory PSI pressure (some avg 9.4%) memory PSI pressure (some avg 9.5%) memory PSI pressure (some avg 9.6%) memory PSI pressure (some avg 9.7%) memory PSI pressure (some avg 9.8%) memory PSI pressure (some avg 9.9%) sustained paging activity observed]

## SQL Server instance summaries

### MSSQLSERVER on sql-win-01

| | |
|---|---|
| Version / edition | 15.0.4360.2 / Standard Edition (64-bit) |
| HA role | standalone |
| Collection level | deep |
| Host RAM | 64 GB |
| min / max server memory | 0 MB / UNLIMITED DEFAULT (2147483647) |
| SQL process memory P50/P95/max | 57.0 / 57.0 / 57.0 GB (P95 = 89% of host) |
| Total Server Memory P50/P95/max | 55.0 / 55.0 / 55.0 GB (Target 56.0 GB) |
| Memory outside memory manager | 2.0 GB |
| OS memory headroom (observed avail P5) | 4.6 GB |
| Engine uptime | 75.0 days |
| PLE P5 / grants pending max | 5220 s / 0 |
| Batch req P95 / SQL CPU P95 | 1470/s / 54% |
| Data-file read latency P95 | 5.8 ms |
| Top waits | CXPACKET (10054s), PAGEIOLATCH_SH (4015s) |
| SQL-correlated spikes | 14 |
- probable correlation: SQL Agent job: Nightly ETL Load

**Recommendation: `sql_memory_configuration_issue`** (confidence 66%, validation required)

max server memory is left at its unlimited default (2147483647 MB). SQL Server will grow toward all host RAM and can pressure the OS. Set an explicit limit (suggested ≈ 57 GB for this 64 GB host) before considering any RAM change.

- sys.configurations max server memory (MB) = 2147483647

### MSSQLSERVER on sqlag-win-02

| | |
|---|---|
| Version / edition | 15.0.4360.2 / Standard Edition (64-bit) |
| HA role | PRIMARY (AG-CORE) |
| Collection level | deep |
| Host RAM | 64 GB |
| min / max server memory | 0 MB / 51200 MB |
| SQL process memory P50/P95/max | 50.5 / 50.5 / 50.5 GB (P95 = 79% of host) |
| Total Server Memory P50/P95/max | 48.5 / 48.5 / 48.5 GB (Target 49.5 GB) |
| Memory outside memory manager | 2.0 GB |
| OS memory headroom (observed avail P5) | 9.7 GB |
| Engine uptime | 75.0 days |
| PLE P5 / grants pending max | 5199 s / 0 |
| Batch req P95 / SQL CPU P95 | 1470/s / 54% |
| Data-file read latency P95 | 5.8 ms |
| Top waits | CXPACKET (10074s), PAGEIOLATCH_SH (3998s) |

**Recommendation: `no_change_recommended`** (confidence 66%, validation required)

SQL memory configuration and observed pressure give no safe reduction opportunity.

- Total 48.5 GB / Target 49.5 GB, PLE P5 5199s, SQL CPU P95 54%

### MSSQLSERVER on sqlag-win-03

| | |
|---|---|
| Version / edition | 15.0.4360.2 / Standard Edition (64-bit) |
| HA role | SECONDARY (AG-CORE) |
| Collection level | deep |
| Host RAM | 64 GB |
| min / max server memory | 0 MB / 51200 MB |
| SQL process memory P50/P95/max | 50.5 / 50.5 / 50.5 GB (P95 = 79% of host) |
| Total Server Memory P50/P95/max | 48.5 / 48.5 / 48.5 GB (Target 49.5 GB) |
| Memory outside memory manager | 2.0 GB |
| OS memory headroom (observed avail P5) | 13.5 GB |
| Engine uptime | 75.0 days |
| PLE P5 / grants pending max | 5199 s / 0 |
| Batch req P95 / SQL CPU P95 | 5/s / 4% |
| Data-file read latency P95 | 5.8 ms |
| Top waits | CXPACKET (10074s), PAGEIOLATCH_SH (3998s) |

**Recommendation: `ha_dr_constraint`** (confidence 66%, validation required)

Secondary replica of availability group AG-CORE. Do not size from its own (expectedly idle) workload — it must absorb the primary's load after failover. Assess the replica group together.

- availability-group SECONDARY role detected

## HA replica groups

- **AG-CORE**: ura-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa04|MSSQLSERVER (PRIMARY); ura-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa05|MSSQLSERVER (SECONDARY)
  - replica servers: sqlag-win-03, sqlag-win-02
  - Replicas of one availability group: evaluate sizing together; a secondary must absorb the primary's workload after failover. Topology-level validation required before any resize.

## Spike report (29 events)

| Host | Start (UTC) | Res | Dur | Baseline→Peak | Process / job | Recurrence | Conf | Next validation step |
|---|---|---|---|---|---|---|---|---|
| sql-win-01 | 07-16 01:57 | cpu | 25m0s | 22→94 | sqlservr.exe / MSSQLSERVER ⟨SQL Agent job running: Nightly ETL Load⟩ | recurring | 75% | Confirm the SQL Agent job schedule with the DBA and whether its window can move or its plan be tuned. |
| batch-linux-02 | 07-16 02:02 | cpu | 25m0s | 12→95 | python3 / etl-nightly.service | recurring | 75% | Confirm with the owner of etl-nightly.service that this scheduled run is expected and sized correctly. |
| sql-win-01 | 07-17 02:00 | cpu | 25m0s | 22→95 | sqlservr.exe / MSSQLSERVER ⟨SQL Agent job running: Nightly ETL Load⟩ | recurring | 75% | Confirm the SQL Agent job schedule with the DBA and whether its window can move or its plan be tuned. |
| batch-linux-02 | 07-17 02:00 | cpu | 25m0s | 12→95 | python3 / etl-nightly.service | recurring | 75% | Confirm with the owner of etl-nightly.service that this scheduled run is expected and sized correctly. |
| batch-linux-02 | 07-18 01:59 | cpu | 25m0s | 12→97 | python3 / etl-nightly.service | recurring | 75% | Confirm with the owner of etl-nightly.service that this scheduled run is expected and sized correctly. |
| mem-linux-03 | 07-18 02:00 | memory | 10m0s | 93→98 | java |  | 45% | Ask the application owner of java whether this burst is expected at this time. |
| sql-win-01 | 07-18 02:02 | cpu | 25m0s | 22→96 | sqlservr.exe / MSSQLSERVER ⟨SQL Agent job running: Nightly ETL Load⟩ | recurring | 75% | Confirm the SQL Agent job schedule with the DBA and whether its window can move or its plan be tuned. |
| batch-linux-02 | 07-19 01:57 | cpu | 25m0s | 12→97 | python3 / etl-nightly.service | recurring | 75% | Confirm with the owner of etl-nightly.service that this scheduled run is expected and sized correctly. |
| sql-win-01 | 07-19 01:57 | cpu | 25m0s | 22→97 | sqlservr.exe / MSSQLSERVER ⟨SQL Agent job running: Nightly ETL Load⟩ | recurring | 75% | Confirm the SQL Agent job schedule with the DBA and whether its window can move or its plan be tuned. |
| batch-linux-02 | 07-20 02:00 | cpu | 25m0s | 12→94 | python3 / etl-nightly.service | recurring | 75% | Confirm with the owner of etl-nightly.service that this scheduled run is expected and sized correctly. |
| sql-win-01 | 07-20 02:00 | cpu | 25m0s | 22→96 | sqlservr.exe / MSSQLSERVER ⟨SQL Agent job running: Nightly ETL Load⟩ | recurring | 75% | Confirm the SQL Agent job schedule with the DBA and whether its window can move or its plan be tuned. |
| sql-win-01 | 07-21 01:57 | cpu | 25m0s | 22→96 | sqlservr.exe / MSSQLSERVER ⟨SQL Agent job running: Nightly ETL Load⟩ | recurring | 75% | Confirm the SQL Agent job schedule with the DBA and whether its window can move or its plan be tuned. |
| batch-linux-02 | 07-21 02:00 | cpu | 25m0s | 12→94 | python3 / etl-nightly.service | recurring | 75% | Confirm with the owner of etl-nightly.service that this scheduled run is expected and sized correctly. |
| sql-win-01 | 07-22 02:00 | cpu | 25m0s | 22→95 | sqlservr.exe / MSSQLSERVER ⟨SQL Agent job running: Nightly ETL Load⟩ | recurring | 75% | Confirm the SQL Agent job schedule with the DBA and whether its window can move or its plan be tuned. |
| batch-linux-02 | 07-22 02:01 | cpu | 25m0s | 12→95 | python3 / etl-nightly.service | recurring | 75% | Confirm with the owner of etl-nightly.service that this scheduled run is expected and sized correctly. |
| batch-linux-02 | 07-23 01:57 | cpu | 25m0s | 12→95 | python3 / etl-nightly.service | recurring | 75% | Confirm with the owner of etl-nightly.service that this scheduled run is expected and sized correctly. |
| sql-win-01 | 07-23 01:57 | cpu | 25m0s | 22→97 | sqlservr.exe / MSSQLSERVER ⟨SQL Agent job running: Nightly ETL Load⟩ | recurring | 75% | Confirm the SQL Agent job schedule with the DBA and whether its window can move or its plan be tuned. |
| sql-win-01 | 07-24 01:57 | cpu | 25m0s | 22→95 | sqlservr.exe / MSSQLSERVER ⟨SQL Agent job running: Nightly ETL Load⟩ | recurring | 75% | Confirm the SQL Agent job schedule with the DBA and whether its window can move or its plan be tuned. |
| batch-linux-02 | 07-24 02:02 | cpu | 25m0s | 12→96 | python3 / etl-nightly.service | recurring | 75% | Confirm with the owner of etl-nightly.service that this scheduled run is expected and sized correctly. |
| sql-win-01 | 07-25 02:01 | cpu | 25m0s | 22→96 | sqlservr.exe / MSSQLSERVER ⟨SQL Agent job running: Nightly ETL Load⟩ | recurring | 75% | Confirm the SQL Agent job schedule with the DBA and whether its window can move or its plan be tuned. |
| batch-linux-02 | 07-25 02:01 | cpu | 25m0s | 12→97 | python3 / etl-nightly.service | recurring | 75% | Confirm with the owner of etl-nightly.service that this scheduled run is expected and sized correctly. |
| batch-linux-02 | 07-26 01:59 | cpu | 25m0s | 12→97 | python3 / etl-nightly.service | recurring | 75% | Confirm with the owner of etl-nightly.service that this scheduled run is expected and sized correctly. |
| sql-win-01 | 07-26 01:59 | cpu | 25m0s | 22→97 | sqlservr.exe / MSSQLSERVER ⟨SQL Agent job running: Nightly ETL Load⟩ | recurring | 75% | Confirm the SQL Agent job schedule with the DBA and whether its window can move or its plan be tuned. |
| sql-win-01 | 07-27 02:00 | cpu | 25m0s | 22→96 | sqlservr.exe / MSSQLSERVER ⟨SQL Agent job running: Nightly ETL Load⟩ | recurring | 75% | Confirm the SQL Agent job schedule with the DBA and whether its window can move or its plan be tuned. |
| batch-linux-02 | 07-27 02:01 | cpu | 25m0s | 12→96 | python3 / etl-nightly.service | recurring | 75% | Confirm with the owner of etl-nightly.service that this scheduled run is expected and sized correctly. |
| batch-linux-02 | 07-28 01:57 | cpu | 25m0s | 12→94 | python3 / etl-nightly.service | recurring | 75% | Confirm with the owner of etl-nightly.service that this scheduled run is expected and sized correctly. |
| sql-win-01 | 07-28 02:01 | cpu | 25m0s | 22→98 | sqlservr.exe / MSSQLSERVER ⟨SQL Agent job running: Nightly ETL Load⟩ | recurring | 75% | Confirm the SQL Agent job schedule with the DBA and whether its window can move or its plan be tuned. |
| sql-win-01 | 07-29 01:58 | cpu | 25m0s | 22→94 | sqlservr.exe / MSSQLSERVER ⟨SQL Agent job running: Nightly ETL Load⟩ | recurring | 75% | Confirm the SQL Agent job schedule with the DBA and whether its window can move or its plan be tuned. |
| batch-linux-02 | 07-29 01:59 | cpu | 25m0s | 12→98 | python3 / etl-nightly.service | recurring | 75% | Confirm with the owner of etl-nightly.service that this scheduled run is expected and sized correctly. |

- sql-win-01 `cpu` 07-16 01:57: event used ~7.0 cores of 16 for 25m0s; at 8 vCPU this CPU-bound work could take up to ~24m0s — validate the batch window
- batch-linux-02 `cpu` 07-16 02:02: event used ~7.0 cores of 8 for 25m0s; at 4 vCPU this CPU-bound work could take up to ~49m0s — validate the batch window
- sql-win-01 `cpu` 07-17 02:00: event used ~7.0 cores of 16 for 25m0s; at 8 vCPU this CPU-bound work could take up to ~24m0s — validate the batch window
- batch-linux-02 `cpu` 07-17 02:00: event used ~7.0 cores of 8 for 25m0s; at 4 vCPU this CPU-bound work could take up to ~49m0s — validate the batch window
- batch-linux-02 `cpu` 07-18 01:59: event used ~7.0 cores of 8 for 25m0s; at 4 vCPU this CPU-bound work could take up to ~49m0s — validate the batch window
- sql-win-01 `cpu` 07-18 02:02: event used ~7.0 cores of 16 for 25m0s; at 8 vCPU this CPU-bound work could take up to ~24m0s — validate the batch window
- batch-linux-02 `cpu` 07-19 01:57: event used ~7.0 cores of 8 for 25m0s; at 4 vCPU this CPU-bound work could take up to ~49m0s — validate the batch window
- sql-win-01 `cpu` 07-19 01:57: event used ~7.0 cores of 16 for 25m0s; at 8 vCPU this CPU-bound work could take up to ~24m0s — validate the batch window
- batch-linux-02 `cpu` 07-20 02:00: event used ~7.0 cores of 8 for 25m0s; at 4 vCPU this CPU-bound work could take up to ~49m0s — validate the batch window
- sql-win-01 `cpu` 07-20 02:00: event used ~7.0 cores of 16 for 25m0s; at 8 vCPU this CPU-bound work could take up to ~24m0s — validate the batch window
- sql-win-01 `cpu` 07-21 01:57: event used ~7.0 cores of 16 for 25m0s; at 8 vCPU this CPU-bound work could take up to ~24m0s — validate the batch window
- batch-linux-02 `cpu` 07-21 02:00: event used ~7.0 cores of 8 for 25m0s; at 4 vCPU this CPU-bound work could take up to ~49m0s — validate the batch window
- sql-win-01 `cpu` 07-22 02:00: event used ~7.0 cores of 16 for 25m0s; at 8 vCPU this CPU-bound work could take up to ~24m0s — validate the batch window
- batch-linux-02 `cpu` 07-22 02:01: event used ~7.0 cores of 8 for 25m0s; at 4 vCPU this CPU-bound work could take up to ~49m0s — validate the batch window
- batch-linux-02 `cpu` 07-23 01:57: event used ~7.0 cores of 8 for 25m0s; at 4 vCPU this CPU-bound work could take up to ~49m0s — validate the batch window
- sql-win-01 `cpu` 07-23 01:57: event used ~7.0 cores of 16 for 25m0s; at 8 vCPU this CPU-bound work could take up to ~24m0s — validate the batch window
- sql-win-01 `cpu` 07-24 01:57: event used ~7.0 cores of 16 for 25m0s; at 8 vCPU this CPU-bound work could take up to ~24m0s — validate the batch window
- batch-linux-02 `cpu` 07-24 02:02: event used ~7.0 cores of 8 for 25m0s; at 4 vCPU this CPU-bound work could take up to ~49m0s — validate the batch window
- sql-win-01 `cpu` 07-25 02:01: event used ~7.0 cores of 16 for 25m0s; at 8 vCPU this CPU-bound work could take up to ~24m0s — validate the batch window
- batch-linux-02 `cpu` 07-25 02:01: event used ~7.0 cores of 8 for 25m0s; at 4 vCPU this CPU-bound work could take up to ~49m0s — validate the batch window
- batch-linux-02 `cpu` 07-26 01:59: event used ~7.0 cores of 8 for 25m0s; at 4 vCPU this CPU-bound work could take up to ~49m0s — validate the batch window
- sql-win-01 `cpu` 07-26 01:59: event used ~7.0 cores of 16 for 25m0s; at 8 vCPU this CPU-bound work could take up to ~24m0s — validate the batch window
- sql-win-01 `cpu` 07-27 02:00: event used ~7.0 cores of 16 for 25m0s; at 8 vCPU this CPU-bound work could take up to ~24m0s — validate the batch window
- batch-linux-02 `cpu` 07-27 02:01: event used ~7.0 cores of 8 for 25m0s; at 4 vCPU this CPU-bound work could take up to ~49m0s — validate the batch window
- batch-linux-02 `cpu` 07-28 01:57: event used ~7.0 cores of 8 for 25m0s; at 4 vCPU this CPU-bound work could take up to ~49m0s — validate the batch window
- sql-win-01 `cpu` 07-28 02:01: event used ~7.0 cores of 16 for 25m0s; at 8 vCPU this CPU-bound work could take up to ~24m0s — validate the batch window
- sql-win-01 `cpu` 07-29 01:58: event used ~7.0 cores of 16 for 25m0s; at 8 vCPU this CPU-bound work could take up to ~24m0s — validate the batch window
- batch-linux-02 `cpu` 07-29 01:59: event used ~7.0 cores of 8 for 25m0s; at 4 vCPU this CPU-bound work could take up to ~49m0s — validate the batch window

---
*Recommendations are technical capacity assessments based on the observed window only. Every downsizing action requires application-owner validation; peaks outside the monitoring window (month-end, quarterly, failover) must be considered separately.*
