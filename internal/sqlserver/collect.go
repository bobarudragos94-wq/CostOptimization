package sqlserver

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bobarudragos94-wq/costoptimization/internal/model"
)

// Every statement here is a read-only SELECT against system DMVs/catalogs.
// Raw SQL text of user queries is never selected (v1 hard rule; see threat
// model). Each query failure degrades that one metric group and is reported
// via the issues callback — never fatal to the instance or host.

const (
	qServerProps = `SELECT
  CAST(SERVERPROPERTY('MachineName') AS nvarchar(128)) AS machine_name,
  CAST(ISNULL(SERVERPROPERTY('InstanceName'),'MSSQLSERVER') AS nvarchar(128)) AS instance_name,
  CAST(SERVERPROPERTY('ProductVersion') AS nvarchar(128)) AS version,
  CAST(SERVERPROPERTY('ProductLevel') AS nvarchar(128)) AS product_level,
  CAST(SERVERPROPERTY('Edition') AS nvarchar(128)) AS edition,
  CAST(SERVERPROPERTY('IsClustered') AS int) AS is_clustered,
  CAST(ISNULL(SERVERPROPERTY('IsHadrEnabled'),0) AS int) AS is_hadr`

	qSysInfo = `SELECT cpu_count, scheduler_count, physical_memory_kb,
  committed_kb, committed_target_kb, sqlserver_start_time
  FROM sys.dm_os_sys_info`

	qConfigurations = `SELECT name, CAST(value_in_use AS bigint) AS value_in_use
  FROM sys.configurations
  WHERE name IN ('max server memory (MB)','min server memory (MB)',
                 'max degree of parallelism','cost threshold for parallelism')`

	qServerServices = `SELECT servicename, process_id, service_account
  FROM sys.dm_server_services WHERE servicename LIKE 'SQL Server (%'`

	qProcessMemory = `SELECT physical_memory_in_use_kb, locked_page_allocations_kb,
  large_page_allocations_kb, memory_utilization_percentage,
  process_physical_memory_low, process_virtual_memory_low
  FROM sys.dm_os_process_memory`

	qPerfCounters = `SELECT RTRIM(counter_name) AS counter_name, cntr_value
  FROM sys.dm_os_performance_counters
  WHERE (object_name LIKE '%:Memory Manager%' AND counter_name IN
          ('Total Server Memory (KB)','Target Server Memory (KB)',
           'Database Cache Memory (KB)','Stolen Server Memory (KB)',
           'Free Memory (KB)','Memory Grants Pending','Memory Grants Outstanding'))
     OR (object_name LIKE '%:Buffer Manager%' AND counter_name IN
          ('Page life expectancy','Page reads/sec','Page writes/sec',
           'Lazy writes/sec','Checkpoint pages/sec'))
     OR (object_name LIKE '%:SQL Statistics%' AND counter_name IN
          ('Batch Requests/sec','SQL Compilations/sec','SQL Re-Compilations/sec'))`

	qMemoryClerks = `SELECT TOP 8 type, SUM(pages_kb) AS kb
  FROM sys.dm_os_memory_clerks GROUP BY type
  HAVING SUM(pages_kb) > 1024 ORDER BY kb DESC`

	qWaitStats = `SELECT TOP 20 wait_type, wait_time_ms, waiting_tasks_count
  FROM sys.dm_os_wait_stats
  WHERE wait_type NOT IN ('SLEEP_TASK','BROKER_TO_FLUSH','BROKER_TASK_STOP',
    'SQLTRACE_INCREMENTAL_FLUSH_SLEEP','LAZYWRITER_SLEEP','XE_TIMER_EVENT',
    'XE_DISPATCHER_WAIT','FT_IFTS_SCHEDULER_IDLE_WAIT','LOGMGR_QUEUE',
    'CHECKPOINT_QUEUE','REQUEST_FOR_DEADLOCK_SEARCH','BROKER_EVENTHANDLER',
    'HADR_FILESTREAM_IOMGR_IOCOMPLETION','DIRTY_PAGE_POLL','DISPATCHER_QUEUE_SEMAPHORE',
    'SP_SERVER_DIAGNOSTICS_SLEEP','QDS_PERSIST_TASK_MAIN_LOOP_SLEEP','WAITFOR',
    'QDS_CLEANUP_STALE_QUERIES_TASK_MAIN_LOOP_SLEEP','SLEEP_SYSTEMTASK','BROKER_RECEIVE_WAITFOR')
  ORDER BY wait_time_ms DESC`

	qFileIO = `SELECT DB_NAME(vfs.database_id) AS db,
  CASE WHEN mf.type = 1 THEN 'log' ELSE 'data' END AS file_type,
  SUM(vfs.num_of_reads) AS reads, SUM(vfs.num_of_writes) AS writes,
  SUM(vfs.io_stall_read_ms) AS stall_read_ms, SUM(vfs.io_stall_write_ms) AS stall_write_ms
  FROM sys.dm_io_virtual_file_stats(NULL, NULL) vfs
  JOIN sys.master_files mf ON vfs.database_id = mf.database_id AND vfs.file_id = mf.file_id
  GROUP BY vfs.database_id, CASE WHEN mf.type = 1 THEN 'log' ELSE 'data' END`

	qMemoryGrants = `SELECT COUNT(*) AS active,
  SUM(CASE WHEN grant_time IS NULL THEN 1 ELSE 0 END) AS waiting
  FROM sys.dm_exec_query_memory_grants`

	qBlocked = `SELECT COUNT(*) AS blocked FROM sys.dm_exec_requests WHERE blocking_session_id <> 0`

	qTempdb = `SELECT SUM(allocated_extent_page_count)*8/1024.0 AS used_mb,
  SUM(total_page_count)*8/1024.0 AS total_mb
  FROM tempdb.sys.dm_db_file_space_usage`

	qDatabases = `SELECT DB_NAME(database_id) AS name,
  SUM(CASE WHEN type = 0 THEN CAST(size AS bigint)*8/1024.0 ELSE 0 END) AS data_mb,
  SUM(CASE WHEN type = 1 THEN CAST(size AS bigint)*8/1024.0 ELSE 0 END) AS log_mb
  FROM sys.master_files GROUP BY database_id`

	qAGReplicas = `SELECT ag.name AS ag_name, ar.replica_server_name,
  ISNULL(ars.role_desc,'') AS role_desc, ISNULL(ars.is_local,0) AS is_local,
  ISNULL(ars.synchronization_health_desc,'') AS sync_health,
  ar.secondary_role_allow_connections_desc AS readable_secondary
  FROM sys.availability_groups ag
  JOIN sys.availability_replicas ar ON ag.group_id = ar.group_id
  LEFT JOIN sys.dm_hadr_availability_replica_states ars ON ar.replica_id = ars.replica_id`

	qActiveRequests = `SELECT r.session_id, r.request_id,
  ISNULL(DB_NAME(r.database_id),'') AS db, r.command, r.status,
  r.cpu_time, r.total_elapsed_time, r.logical_reads, r.reads, r.writes,
  ISNULL(r.wait_type,'') AS wait_type, r.blocking_session_id,
  ISNULL(s.program_name,'') AS program_name,
  ISNULL(CONVERT(varchar(34), r.query_hash, 1),'') AS query_hash,
  ISNULL(CONVERT(varchar(34), r.query_plan_hash, 1),'') AS plan_hash
  FROM sys.dm_exec_requests r
  JOIN sys.dm_exec_sessions s ON r.session_id = s.session_id
  WHERE s.is_user_process = 1 AND r.session_id <> @@SPID`

	qAgentJobs = `SELECT j.name, ja.start_execution_date, ja.stop_execution_date
  FROM msdb.dbo.sysjobactivity ja
  JOIN msdb.dbo.sysjobs j ON ja.job_id = j.job_id
  WHERE ja.session_id = (SELECT MAX(session_id) FROM msdb.dbo.syssessions)
    AND ja.start_execution_date IS NOT NULL
    AND (ja.stop_execution_date IS NULL
         OR ja.stop_execution_date > DATEADD(minute, -60, GETDATE()))`
)

// Issues receives "query-name: error" notes for collection health.
type Issues func(name, err string)

// CollectInventory gathers the instance inventory. Partial data on partial
// permissions; err only when even SERVERPROPERTY is unreachable.
func CollectInventory(ctx context.Context, q Querier, inst Instance, hostID string, issues Issues) (*model.SQLInstanceInventory, error) {
	if issues == nil {
		issues = func(string, string) {}
	}
	now := time.Now()
	inv := &model.SQLInstanceInventory{
		Meta: model.NewMeta(model.KindSQLInventory, now),
		PID:  inst.PID, LinuxMemoryLimitMB: inst.LinuxMemoryLimitMB,
		CollectionLevel: model.SQLLevelDeep,
		LockPagesInMemory: "unknown",
	}

	rows, err := q.Query(ctx, "server_props", qServerProps)
	if err != nil || len(rows) == 0 {
		return nil, fmt.Errorf("server properties unreachable: %v", err)
	}
	r := rows[0]
	inv.MachineName = r.str("machine_name")
	inv.InstanceName = r.str("instance_name")
	inv.IsDefault = inv.InstanceName == "MSSQLSERVER"
	inv.Version = r.str("version")
	inv.ProductLevel = r.str("product_level")
	inv.Edition = r.str("edition")
	inv.InstanceKey = hostID + "|" + inv.InstanceName
	inv.HA.Mode = "standalone"
	if r.i64("is_clustered") == 1 {
		inv.HA.Mode = "fci"
	}
	isHadr := r.i64("is_hadr") == 1

	if rows, err := q.Query(ctx, "sys_info", qSysInfo); err == nil && len(rows) > 0 {
		r := rows[0]
		inv.CPUCountVisible = int(r.i64("cpu_count"))
		inv.SchedulerCount = int(r.i64("scheduler_count"))
		inv.PhysMemVisibleKB = r.u64("physical_memory_kb")
		inv.StartTime = r.time("sqlserver_start_time")
		if !inv.StartTime.IsZero() {
			inv.UptimeS = uint64(now.Sub(inv.StartTime).Seconds())
		}
	} else if err != nil {
		issues("sys_info", err.Error())
	}

	if rows, err := q.Query(ctx, "configurations", qConfigurations); err == nil {
		for _, r := range rows {
			v := r.i64("value_in_use")
			switch r.str("name") {
			case "max server memory (MB)":
				inv.MaxServerMemoryMB = uint64(v)
				inv.MaxMemoryUnlimitedDefault = v >= 2147483647
			case "min server memory (MB)":
				inv.MinServerMemoryMB = uint64(v)
			case "max degree of parallelism":
				inv.MaxDOP = int(v)
			case "cost threshold for parallelism":
				inv.CostThreshold = int(v)
			}
		}
	} else {
		issues("configurations", err.Error())
	}

	if rows, err := q.Query(ctx, "server_services", qServerServices); err == nil && len(rows) > 0 {
		inv.ServiceAccount = rows[0].str("service_account")
		if pid := rows[0].i64("process_id"); pid > 0 && inv.PID == 0 {
			inv.PID = int32(pid)
		}
	} else if err != nil {
		issues("server_services", err.Error())
	}

	if rows, err := q.Query(ctx, "process_memory", qProcessMemory); err == nil && len(rows) > 0 {
		if rows[0].u64("locked_page_allocations_kb") > 0 {
			inv.LockPagesInMemory = "detected"
		} else {
			inv.LockPagesInMemory = "not_detected"
		}
	} else if err != nil {
		issues("process_memory", err.Error())
	}

	if rows, err := q.Query(ctx, "databases", qDatabases); err == nil {
		for _, r := range rows {
			inv.Databases = append(inv.Databases, model.SQLDatabase{
				Name: r.str("name"), SizeMB: r.f64("data_mb"), LogSizeMB: r.f64("log_mb"),
			})
		}
	} else {
		issues("databases", err.Error())
	}

	if isHadr {
		if rows, err := q.Query(ctx, "ag_replicas", qAGReplicas); err == nil && len(rows) > 0 {
			inv.HA.Mode = "alwayson_ag"
			ags := map[string]*model.AGInfo{}
			for _, r := range rows {
				name := r.str("ag_name")
				ag := ags[name]
				if ag == nil {
					ag = &model.AGInfo{AGName: name}
					ags[name] = ag
				}
				if r.boolean("is_local") {
					ag.Role = r.str("role_desc")
					ag.SyncState = r.str("sync_health")
					ag.ReadableSecondary = r.str("readable_secondary")
				} else {
					ag.PartnerReplicas = append(ag.PartnerReplicas, r.str("replica_server_name"))
				}
			}
			for _, ag := range ags {
				inv.HA.AGs = append(inv.HA.AGs, *ag)
			}
			sort.Slice(inv.HA.AGs, func(i, j int) bool { return inv.HA.AGs[i].AGName < inv.HA.AGs[j].AGName })
		} else if err != nil {
			issues("ag_replicas", err.Error())
		}
	}
	return inv, nil
}

// SampleState carries cumulative counters between samples for delta rates.
type SampleState struct {
	TS       time.Time
	Counters map[string]int64            // perf counter cumulative values
	Waits    map[string][2]int64         // wait_type -> {wait_ms, count}
	FileIO   map[string][4]int64         // db|type -> {reads, writes, stall_r, stall_w}
	// CoreDenied is set when the core memory telemetry queries
	// (dm_os_process_memory AND the memory-manager performance counters)
	// both failed with permission errors: the login lacks VIEW SERVER STATE
	// and the instance must be treated as process_only.
	CoreDenied bool
}

func isPermissionErr(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "permission") || strings.Contains(s, "denied") ||
		strings.Contains(s, "view server state")
}

// CollectSample gathers the periodic light-weight memory/workload sample.
// prev == nil primes counters (first sample yields levels but zero rates).
func CollectSample(ctx context.Context, q Querier, instanceKey string, prev *SampleState, issues Issues) (*model.SQLSample, *SampleState) {
	if issues == nil {
		issues = func(string, string) {}
	}
	now := time.Now()
	s := &model.SQLSample{
		Meta:        model.NewMeta(model.KindSQLSample, now),
		InstanceKey: instanceKey,
	}
	state := &SampleState{TS: now, Counters: map[string]int64{}, Waits: map[string][2]int64{}, FileIO: map[string][4]int64{}}
	var elapsed float64
	if prev != nil {
		elapsed = now.Sub(prev.TS).Seconds()
	}

	pmDenied, pcDenied := false, false
	if rows, err := q.Query(ctx, "process_memory", qProcessMemory); err == nil && len(rows) > 0 {
		r := rows[0]
		s.PhysicalMemoryInUseKB = r.u64("physical_memory_in_use_kb")
		s.LockedPageAllocKB = r.u64("locked_page_allocations_kb")
		s.LargePageAllocKB = r.u64("large_page_allocations_kb")
		s.MemoryUtilizationPct = int(r.i64("memory_utilization_percentage"))
		s.ProcessPhysicalMemoryLow = r.boolean("process_physical_memory_low")
		s.ProcessVirtualMemoryLow = r.boolean("process_virtual_memory_low")
	} else if err != nil {
		pmDenied = isPermissionErr(err)
		issues("process_memory", err.Error())
	}

	if rows, err := q.Query(ctx, "perf_counters", qPerfCounters); err == nil {
		for _, r := range rows {
			name := strings.TrimSpace(r.str("counter_name"))
			v := r.i64("cntr_value")
			state.Counters[name] = v
			switch name {
			case "Total Server Memory (KB)":
				s.TotalServerMemoryKB = uint64(v)
			case "Target Server Memory (KB)":
				s.TargetServerMemoryKB = uint64(v)
			case "Database Cache Memory (KB)":
				s.DatabaseCacheKB = uint64(v)
			case "Stolen Server Memory (KB)":
				s.StolenServerMemoryKB = uint64(v)
			case "Free Memory (KB)":
				s.FreeMemoryKB = uint64(v)
			case "Memory Grants Pending":
				s.MemoryGrantsPending = v
			case "Memory Grants Outstanding":
				s.MemoryGrantsActive = v
			case "Page life expectancy":
				s.PLESeconds = v
			}
		}
		if prev != nil && elapsed > 0 {
			d := func(name string) float64 {
				cur, curOK := state.Counters[name]
				pv, prevOK := prev.Counters[name]
				if !curOK || !prevOK || cur < pv {
					return 0
				}
				return float64(cur-pv) / elapsed
			}
			s.BatchRequestsPS = d("Batch Requests/sec")
			s.CompilationsPS = d("SQL Compilations/sec")
			s.RecompilationsPS = d("SQL Re-Compilations/sec")
			s.PageReadsPS = d("Page reads/sec")
			s.PageWritesPS = d("Page writes/sec")
			s.LazyWritesPS = d("Lazy writes/sec")
			s.CheckpointPagesPS = d("Checkpoint pages/sec")
		}
	} else {
		pcDenied = isPermissionErr(err)
		issues("perf_counters", err.Error())
	}
	state.CoreDenied = pmDenied && pcDenied

	if rows, err := q.Query(ctx, "memory_clerks", qMemoryClerks); err == nil {
		s.MemoryClerksTopKB = map[string]uint64{}
		for _, r := range rows {
			s.MemoryClerksTopKB[r.str("type")] = r.u64("kb")
		}
	} else {
		issues("memory_clerks", err.Error())
	}

	if rows, err := q.Query(ctx, "wait_stats", qWaitStats); err == nil {
		s.WaitDeltas = map[string]model.WaitDelta{}
		for _, r := range rows {
			wt := r.str("wait_type")
			cur := [2]int64{r.i64("wait_time_ms"), r.i64("waiting_tasks_count")}
			state.Waits[wt] = cur
			if prev != nil {
				if pv, ok := prev.Waits[wt]; ok && cur[0] >= pv[0] {
					if dms := cur[0] - pv[0]; dms > 0 {
						s.WaitDeltas[wt] = model.WaitDelta{WaitMS: float64(dms), Count: float64(cur[1] - pv[1])}
					}
				}
			}
		}
	} else {
		issues("wait_stats", err.Error())
	}

	if rows, err := q.Query(ctx, "file_io", qFileIO); err == nil {
		for _, r := range rows {
			key := r.str("db") + "|" + r.str("file_type")
			cur := [4]int64{r.i64("reads"), r.i64("writes"), r.i64("stall_read_ms"), r.i64("stall_write_ms")}
			state.FileIO[key] = cur
			if prev == nil || elapsed <= 0 {
				continue
			}
			pv, ok := prev.FileIO[key]
			if !ok {
				continue
			}
			dr, dw := cur[0]-pv[0], cur[1]-pv[1]
			if dr < 0 || dw < 0 || cur[2] < pv[2] || cur[3] < pv[3] {
				continue // counter reset (instance restart): skip this delta
			}
			io := model.DBFileIO{Database: r.str("db"), FileType: r.str("file_type"),
				ReadPS: float64(dr) / elapsed, WritePS: float64(dw) / elapsed}
			if dr > 0 {
				io.ReadLatMS = float64(cur[2]-pv[2]) / float64(dr)
			}
			if dw > 0 {
				io.WriteLatMS = float64(cur[3]-pv[3]) / float64(dw)
			}
			if io.ReadPS > 0.1 || io.WritePS > 0.1 {
				s.FileIO = append(s.FileIO, io)
			}
		}
	} else {
		issues("file_io", err.Error())
	}

	if rows, err := q.Query(ctx, "memory_grants", qMemoryGrants); err == nil && len(rows) > 0 {
		if s.MemoryGrantsActive == 0 {
			s.MemoryGrantsActive = rows[0].i64("active")
		}
	}
	if rows, err := q.Query(ctx, "blocked", qBlocked); err == nil && len(rows) > 0 {
		s.BlockedSessions = int(rows[0].i64("blocked"))
	} else if err != nil {
		issues("blocked", err.Error())
	}
	if rows, err := q.Query(ctx, "tempdb", qTempdb); err == nil && len(rows) > 0 {
		s.TempdbUsedMB = rows[0].f64("used_mb")
		s.TempdbTotalMB = rows[0].f64("total_mb")
	} else if err != nil {
		issues("tempdb", err.Error())
	}
	return s, state
}

// CollectSpikeContext gathers active-request and Agent-job evidence for a
// host spike correlated with this instance's process. It is designed to be
// called while the spike is ACTIVE (the detector's OnOpen hook) — active
// requests are transient and are usually gone by the time the event closes.
func CollectSpikeContext(ctx context.Context, q Querier, instanceKey, eventID string, pseudonymize bool, issues Issues) *model.SQLSpikeContext {
	if issues == nil {
		issues = func(string, string) {}
	}
	sc := &model.SQLSpikeContext{
		Meta:        model.NewMeta(model.KindSQLSpike, time.Now()),
		EventID:     eventID,
		InstanceKey: instanceKey,
		CorrelationLevel: model.CorrProcessOnly,
	}
	if rows, err := q.Query(ctx, "active_requests", qActiveRequests); err == nil {
		for _, r := range rows {
			sc.ActiveRequests = append(sc.ActiveRequests, model.SQLActiveRequest{
				SessionID: int(r.i64("session_id")), RequestID: int(r.i64("request_id")),
				Database: r.str("db"), Command: r.str("command"), Status: r.str("status"),
				CPUTimeMS: r.i64("cpu_time"), ElapsedMS: r.i64("total_elapsed_time"),
				LogicalReads: r.i64("logical_reads"), PhysicalReads: r.i64("reads"),
				Writes: r.i64("writes"), WaitType: r.str("wait_type"),
				BlockingSession: int(r.i64("blocking_session_id")),
				ProgramName:     r.str("program_name"),
				QueryHash:       r.str("query_hash"), PlanHash: r.str("plan_hash"),
			})
		}
		if len(sc.ActiveRequests) > 0 {
			sc.CorrelationLevel = model.CorrActiveRequest
		}
	} else {
		issues("active_requests", err.Error())
	}

	sc.AgentJobs = CollectAgentJobs(ctx, q, pseudonymize, issues)
	for _, j := range sc.AgentJobs {
		if j.Running {
			sc.CorrelationLevel = model.CorrAgentJob
		}
	}
	return sc
}

// CollectAgentJobs returns recent SQL Agent job activity (running plus jobs
// stopped within the last hour). Degrades to nil when msdb access is denied.
func CollectAgentJobs(ctx context.Context, q Querier, pseudonymize bool, issues Issues) []model.SQLAgentJobRun {
	if issues == nil {
		issues = func(string, string) {}
	}
	rows, err := q.Query(ctx, "agent_jobs", qAgentJobs)
	if err != nil {
		issues("agent_jobs", err.Error())
		return nil
	}
	var out []model.SQLAgentJobRun
	for i, r := range rows {
		name := r.str("name")
		if pseudonymize {
			name = fmt.Sprintf("job-%03d", i+1)
		}
		run := model.SQLAgentJobRun{JobName: name, StartTime: r.time("start_execution_date")}
		stop := r.time("stop_execution_date")
		if stop.IsZero() {
			run.Running = true
			run.Outcome = "running"
		} else {
			run.Outcome = "completed"
			run.DurationS = stop.Sub(run.StartTime).Seconds()
		}
		out = append(out, run)
	}
	return out
}

// MergeJobsIntoContext folds a fresh job listing (taken at event close) into
// a context captured at event open: jobs whose run window overlaps the event
// window are correlated — including jobs that COMPLETED during the event,
// which an open-time capture alone would list as still running or miss.
func MergeJobsIntoContext(sc *model.SQLSpikeContext, jobs []model.SQLAgentJobRun, start, end time.Time) {
	if sc == nil {
		return
	}
	overlaps := func(j model.SQLAgentJobRun) bool {
		if j.StartTime.IsZero() || j.StartTime.After(end) {
			return false
		}
		if j.Running {
			return true // started before event end and still running
		}
		stop := j.StartTime.Add(time.Duration(j.DurationS * float64(time.Second)))
		return !stop.Before(start)
	}
	seen := map[string]int{}
	for i, j := range sc.AgentJobs {
		seen[j.JobName+"|"+j.StartTime.UTC().Format(time.RFC3339)] = i
	}
	for _, j := range jobs {
		if !overlaps(j) {
			continue
		}
		key := j.JobName + "|" + j.StartTime.UTC().Format(time.RFC3339)
		if idx, ok := seen[key]; ok {
			// Same run seen at open: refresh outcome (running -> completed).
			sc.AgentJobs[idx] = j
		} else {
			sc.AgentJobs = append(sc.AgentJobs, j)
		}
	}
	// Recompute correlation: any job overlapping the event window counts,
	// completed or not; active requests still rank above process-only.
	hasJob := false
	for _, j := range sc.AgentJobs {
		if overlaps(j) {
			hasJob = true
			break
		}
	}
	switch {
	case hasJob:
		sc.CorrelationLevel = model.CorrAgentJob
	case len(sc.ActiveRequests) > 0:
		sc.CorrelationLevel = model.CorrActiveRequest
	case sc.CorrelationLevel == model.CorrAgentJob:
		sc.CorrelationLevel = model.CorrProcessOnly
	}
}
