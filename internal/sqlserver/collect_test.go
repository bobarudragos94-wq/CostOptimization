package sqlserver

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bobarudragos94-wq/costoptimization/internal/model"
)

// fakeQuerier serves canned rows per query name; missing names simulate
// permission-denied (VIEW SERVER STATE absent).
type fakeQuerier struct {
	fixtures map[string][]Row
	denied   map[string]bool
}

func (f *fakeQuerier) Query(_ context.Context, name, _ string) ([]Row, error) {
	if f.denied[name] {
		return nil, errors.New("The user does not have permission to perform this action. (VIEW SERVER STATE required)")
	}
	rows, ok := f.fixtures[name]
	if !ok {
		return nil, errors.New("fixture missing: " + name)
	}
	return rows, nil
}
func (f *fakeQuerier) Close() error { return nil }

func baseFixtures() map[string][]Row {
	start := time.Date(2026, 6, 1, 3, 0, 0, 0, time.UTC)
	return map[string][]Row{
		"server_props": {{
			"machine_name": "SQLHOST1", "instance_name": "MSSQLSERVER",
			"version": "15.0.4360.2", "product_level": "RTM",
			"edition": "Standard Edition (64-bit)", "is_clustered": int64(0), "is_hadr": int64(0),
		}},
		"sys_info": {{
			"cpu_count": int64(8), "scheduler_count": int64(8),
			"physical_memory_kb": int64(33554432), // 32 GB
			"committed_kb":       int64(26214400),
			"committed_target_kb": int64(26214400),
			"sqlserver_start_time": start,
		}},
		"configurations": {
			{"name": "max server memory (MB)", "value_in_use": int64(2147483647)},
			{"name": "min server memory (MB)", "value_in_use": int64(0)},
			{"name": "max degree of parallelism", "value_in_use": int64(0)},
			{"name": "cost threshold for parallelism", "value_in_use": int64(5)},
		},
		"server_services": {{
			"servicename": "SQL Server (MSSQLSERVER)", "process_id": int64(4321),
			"service_account": "NT Service\\MSSQLSERVER",
		}},
		"process_memory": {{
			"physical_memory_in_use_kb": int64(25165824), // 24 GB
			"locked_page_allocations_kb": int64(0), "large_page_allocations_kb": int64(0),
			"memory_utilization_percentage": int64(96),
			"process_physical_memory_low":  false, "process_virtual_memory_low": false,
		}},
		"perf_counters": {
			{"counter_name": "Total Server Memory (KB)", "cntr_value": int64(24117248)},
			{"counter_name": "Target Server Memory (KB)", "cntr_value": int64(26214400)},
			{"counter_name": "Database Cache Memory (KB)", "cntr_value": int64(18874368)},
			{"counter_name": "Stolen Server Memory (KB)", "cntr_value": int64(4194304)},
			{"counter_name": "Free Memory (KB)", "cntr_value": int64(1048576)},
			{"counter_name": "Memory Grants Pending", "cntr_value": int64(0)},
			{"counter_name": "Memory Grants Outstanding", "cntr_value": int64(3)},
			{"counter_name": "Page life expectancy", "cntr_value": int64(4500)},
			{"counter_name": "Batch Requests/sec", "cntr_value": int64(1000000)},
			{"counter_name": "SQL Compilations/sec", "cntr_value": int64(50000)},
			{"counter_name": "SQL Re-Compilations/sec", "cntr_value": int64(1000)},
			{"counter_name": "Page reads/sec", "cntr_value": int64(700000)},
			{"counter_name": "Page writes/sec", "cntr_value": int64(300000)},
			{"counter_name": "Lazy writes/sec", "cntr_value": int64(9000)},
			{"counter_name": "Checkpoint pages/sec", "cntr_value": int64(120000)},
		},
		"memory_clerks": {
			{"type": "MEMORYCLERK_SQLBUFFERPOOL", "kb": int64(18874368)},
			{"type": "CACHESTORE_SQLCP", "kb": int64(2097152)},
			{"type": "MEMORYCLERK_SOSNODE", "kb": int64(524288)},
		},
		"wait_stats": {
			{"wait_type": "CXPACKET", "wait_time_ms": int64(500000), "waiting_tasks_count": int64(20000)},
			{"wait_type": "PAGEIOLATCH_SH", "wait_time_ms": int64(300000), "waiting_tasks_count": int64(15000)},
		},
		"file_io": {
			{"db": "SalesDB", "file_type": "data", "reads": int64(1000000), "writes": int64(200000),
				"stall_read_ms": int64(5000000), "stall_write_ms": int64(600000)},
		},
		"memory_grants": {{"active": int64(3), "waiting": int64(0)}},
		"blocked":       {{"blocked": int64(0)}},
		"tempdb":        {{"used_mb": 512.0, "total_mb": 8192.0}},
		"databases": {
			{"name": "master", "data_mb": 8.0, "log_mb": 2.0},
			{"name": "SalesDB", "data_mb": 51200.0, "log_mb": 8192.0},
		},
	}
}

func TestCollectInventoryDefaults(t *testing.T) {
	q := &fakeQuerier{fixtures: baseFixtures()}
	inv, err := CollectInventory(context.Background(), q, Instance{PID: 4321}, "ura-h1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if inv.InstanceName != "MSSQLSERVER" || !inv.IsDefault {
		t.Fatalf("%+v", inv)
	}
	if !inv.MaxMemoryUnlimitedDefault {
		t.Fatal("max server memory left at 2147483647 must be flagged as unlimited default")
	}
	if inv.HA.Mode != "standalone" {
		t.Fatalf("HA mode: %s", inv.HA.Mode)
	}
	if inv.LockPagesInMemory != "not_detected" {
		t.Fatalf("LPIM: %s", inv.LockPagesInMemory)
	}
	if inv.CPUCountVisible != 8 || inv.PhysMemVisibleKB != 33554432 {
		t.Fatalf("sizing fields: %+v", inv)
	}
	if inv.ServiceAccount != "NT Service\\MSSQLSERVER" {
		t.Fatalf("service account: %s", inv.ServiceAccount)
	}
	if len(inv.Databases) != 2 {
		t.Fatalf("databases: %d", len(inv.Databases))
	}
	if inv.InstanceKey != "ura-h1|MSSQLSERVER" {
		t.Fatalf("key: %s", inv.InstanceKey)
	}
}

func TestCollectInventoryAlwaysOn(t *testing.T) {
	fx := baseFixtures()
	fx["server_props"][0]["is_hadr"] = int64(1)
	fx["ag_replicas"] = []Row{
		{"ag_name": "AG1", "replica_server_name": "SQLHOST1", "role_desc": "SECONDARY",
			"is_local": int64(1), "sync_health": "HEALTHY", "readable_secondary": "NO"},
		{"ag_name": "AG1", "replica_server_name": "SQLHOST2", "role_desc": "PRIMARY",
			"is_local": int64(0), "sync_health": "HEALTHY", "readable_secondary": "NO"},
	}
	q := &fakeQuerier{fixtures: fx}
	inv, err := CollectInventory(context.Background(), q, Instance{}, "ura-h1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if inv.HA.Mode != "alwayson_ag" || len(inv.HA.AGs) != 1 {
		t.Fatalf("%+v", inv.HA)
	}
	ag := inv.HA.AGs[0]
	if ag.Role != "SECONDARY" || len(ag.PartnerReplicas) != 1 || ag.PartnerReplicas[0] != "SQLHOST2" {
		t.Fatalf("%+v", ag)
	}
}

func TestCollectSampleLevelsAndRates(t *testing.T) {
	q := &fakeQuerier{fixtures: baseFixtures()}
	// First sample: levels only, primes counters.
	s1, st1 := CollectSample(context.Background(), q, "k", nil, nil)
	if s1.TotalServerMemoryKB != 24117248 || s1.TargetServerMemoryKB != 26214400 {
		t.Fatalf("memory manager: %+v", s1)
	}
	if s1.PLESeconds != 4500 || s1.PhysicalMemoryInUseKB != 25165824 {
		t.Fatalf("%+v", s1)
	}
	if s1.BatchRequestsPS != 0 {
		t.Fatal("first sample must not report rates")
	}

	// Advance counters by 60s worth of work.
	fx2 := baseFixtures()
	for _, r := range fx2["perf_counters"] {
		if r.str("counter_name") == "Batch Requests/sec" {
			r["cntr_value"] = int64(1000000 + 6000) // 100/s
		}
	}
	fx2["wait_stats"] = []Row{
		{"wait_type": "CXPACKET", "wait_time_ms": int64(560000), "waiting_tasks_count": int64(21000)},
		{"wait_type": "PAGEIOLATCH_SH", "wait_time_ms": int64(300000), "waiting_tasks_count": int64(15000)},
	}
	fx2["file_io"] = []Row{
		{"db": "SalesDB", "file_type": "data", "reads": int64(1006000), "writes": int64(200000),
			"stall_read_ms": int64(5030000), "stall_write_ms": int64(600000)},
	}
	q2 := &fakeQuerier{fixtures: fx2}
	st1.TS = st1.TS.Add(-60 * time.Second) // pretend a minute passed
	s2, _ := CollectSample(context.Background(), q2, "k", st1, nil)
	if s2.BatchRequestsPS < 95 || s2.BatchRequestsPS > 105 {
		t.Fatalf("batch rate: %v", s2.BatchRequestsPS)
	}
	wd, ok := s2.WaitDeltas["CXPACKET"]
	if !ok || wd.WaitMS != 60000 {
		t.Fatalf("wait delta: %+v", s2.WaitDeltas)
	}
	if _, has := s2.WaitDeltas["PAGEIOLATCH_SH"]; has {
		t.Fatal("zero-delta waits must be omitted")
	}
	if len(s2.FileIO) != 1 || s2.FileIO[0].ReadLatMS != 5.0 {
		t.Fatalf("file io latency: %+v", s2.FileIO)
	}
}

func TestPermissionDeniedDegradesGracefully(t *testing.T) {
	q := &fakeQuerier{
		fixtures: baseFixtures(),
		denied: map[string]bool{
			"process_memory": true, "perf_counters": true, "wait_stats": true,
			"file_io": true, "memory_clerks": true, "memory_grants": true,
			"blocked": true, "tempdb": true,
		},
	}
	var issues []string
	track := func(name, err string) { issues = append(issues, name) }
	s, _ := CollectSample(context.Background(), q, "k", nil, track)
	if s == nil {
		t.Fatal("sample must not be nil even with all queries denied")
	}
	if len(issues) < 5 {
		t.Fatalf("denied queries must be reported: %v", issues)
	}
	// Inventory still works from SERVERPROPERTY alone.
	inv, err := CollectInventory(context.Background(), q, Instance{}, "h", track)
	if err != nil || inv.Edition == "" {
		t.Fatalf("inventory should survive partial denial: %v", err)
	}
	if inv.LockPagesInMemory != "unknown" {
		t.Fatalf("LPIM must stay unknown when process_memory denied, got %s", inv.LockPagesInMemory)
	}
}

func TestSpikeContextCorrelationLevels(t *testing.T) {
	fx := baseFixtures()
	fx["active_requests"] = []Row{{
		"session_id": int64(51), "request_id": int64(0), "db": "SalesDB",
		"command": "SELECT", "status": "running", "cpu_time": int64(120000),
		"total_elapsed_time": int64(180000), "logical_reads": int64(5000000),
		"reads": int64(100000), "writes": int64(0), "wait_type": "CXPACKET",
		"blocking_session_id": int64(0), "program_name": "SQLAgent - TSQL JobStep",
		"query_hash": "0xABC", "plan_hash": "0xDEF",
	}}
	fx["agent_jobs"] = []Row{{
		"name": "Nightly ETL", "start_execution_date": time.Date(2026, 7, 1, 2, 0, 0, 0, time.UTC),
	}}
	q := &fakeQuerier{fixtures: fx}
	sc := CollectSpikeContext(context.Background(), q, "k", "ev1", false, nil)
	if sc.CorrelationLevel != model.CorrAgentJob {
		t.Fatalf("running agent job should dominate: %s", sc.CorrelationLevel)
	}
	if len(sc.ActiveRequests) != 1 || sc.ActiveRequests[0].QueryHash != "0xABC" {
		t.Fatalf("%+v", sc.ActiveRequests)
	}
	if sc.AgentJobs[0].JobName != "Nightly ETL" || !sc.AgentJobs[0].Running {
		t.Fatalf("%+v", sc.AgentJobs)
	}

	// Pseudonymized job names.
	sc2 := CollectSpikeContext(context.Background(), q, "k", "ev2", true, nil)
	if sc2.AgentJobs[0].JobName != "job-001" {
		t.Fatalf("pseudonymization failed: %s", sc2.AgentJobs[0].JobName)
	}

	// msdb denied: falls back to active-request correlation.
	q.denied = map[string]bool{"agent_jobs": true}
	sc3 := CollectSpikeContext(context.Background(), q, "k", "ev3", false, nil)
	if sc3.CorrelationLevel != model.CorrActiveRequest {
		t.Fatalf("%s", sc3.CorrelationLevel)
	}

	// Everything denied: process correlation only.
	q.denied["active_requests"] = true
	sc4 := CollectSpikeContext(context.Background(), q, "k", "ev4", false, nil)
	if sc4.CorrelationLevel != model.CorrProcessOnly {
		t.Fatalf("%s", sc4.CorrelationLevel)
	}
}

func TestMergeJobsCompletedDuringEvent(t *testing.T) {
	evStart := time.Date(2026, 7, 1, 2, 0, 0, 0, time.UTC)
	evEnd := evStart.Add(20 * time.Minute)

	// Open-time capture: one job running, no active requests.
	sc := &model.SQLSpikeContext{
		EventID: "ev1", CorrelationLevel: model.CorrAgentJob,
		AgentJobs: []model.SQLAgentJobRun{{
			JobName: "Nightly ETL", StartTime: evStart.Add(-1 * time.Minute),
			Running: true, Outcome: "running"}},
	}
	// Close-time listing: same run now completed (during the event), plus an
	// unrelated job that finished hours before the event.
	closeJobs := []model.SQLAgentJobRun{
		{JobName: "Nightly ETL", StartTime: evStart.Add(-1 * time.Minute),
			Outcome: "completed", DurationS: (15 * time.Minute).Seconds()},
		{JobName: "Old Job", StartTime: evStart.Add(-6 * time.Hour),
			Outcome: "completed", DurationS: 60},
	}
	MergeJobsIntoContext(sc, closeJobs, evStart, evEnd)
	if len(sc.AgentJobs) != 1 {
		t.Fatalf("only the overlapping run belongs in the context: %+v", sc.AgentJobs)
	}
	j := sc.AgentJobs[0]
	if j.Running || j.Outcome != "completed" || j.DurationS != 900 {
		t.Fatalf("open-time running job must be refreshed to its completed outcome: %+v", j)
	}
	if sc.CorrelationLevel != model.CorrAgentJob {
		t.Fatalf("level: %s", sc.CorrelationLevel)
	}

	// A job that started AND completed inside the event window, discovered
	// only at close (started after the open capture).
	sc2 := &model.SQLSpikeContext{EventID: "ev2", CorrelationLevel: model.CorrProcessOnly}
	MergeJobsIntoContext(sc2, []model.SQLAgentJobRun{{
		JobName: "Mid-event job", StartTime: evStart.Add(5 * time.Minute),
		Outcome: "completed", DurationS: 300}}, evStart, evEnd)
	if len(sc2.AgentJobs) != 1 || sc2.CorrelationLevel != model.CorrAgentJob {
		t.Fatalf("job completed during event must upgrade correlation: %+v %s", sc2.AgentJobs, sc2.CorrelationLevel)
	}

	// No overlapping jobs but active requests: stays active_request.
	sc3 := &model.SQLSpikeContext{EventID: "ev3", CorrelationLevel: model.CorrActiveRequest,
		ActiveRequests: []model.SQLActiveRequest{{SessionID: 1}}}
	MergeJobsIntoContext(sc3, nil, evStart, evEnd)
	if sc3.CorrelationLevel != model.CorrActiveRequest {
		t.Fatalf("level: %s", sc3.CorrelationLevel)
	}
}

func TestCoreDeniedFlagsProcessOnly(t *testing.T) {
	q := &fakeQuerier{fixtures: baseFixtures(), denied: map[string]bool{
		"process_memory": true, "perf_counters": true,
	}}
	_, st := CollectSample(context.Background(), q, "k", nil, nil)
	if !st.CoreDenied {
		t.Fatal("both core queries denied must set CoreDenied")
	}
	// One core query denied for a non-permission reason must NOT demote.
	q2 := &fakeQuerier{fixtures: baseFixtures(), denied: map[string]bool{"process_memory": true}}
	_, st2 := CollectSample(context.Background(), q2, "k", nil, nil)
	if st2.CoreDenied {
		t.Fatal("partial denial must not demote to process_only")
	}
}

func TestFileIOCounterResetSkipped(t *testing.T) {
	q1 := &fakeQuerier{fixtures: baseFixtures()}
	_, st := CollectSample(context.Background(), q1, "k", nil, nil)
	st.TS = st.TS.Add(-60 * time.Second)
	// Instance restarted: cumulative file IO counters went backwards.
	fx := baseFixtures()
	fx["file_io"] = []Row{{"db": "SalesDB", "file_type": "data",
		"reads": int64(100), "writes": int64(10),
		"stall_read_ms": int64(500), "stall_write_ms": int64(60)}}
	s2, _ := CollectSample(context.Background(), &fakeQuerier{fixtures: fx}, "k", st, nil)
	if len(s2.FileIO) != 0 {
		t.Fatalf("counter reset must not produce negative rates: %+v", s2.FileIO)
	}
}

func TestMultiInstanceMemoryMath(t *testing.T) {
	// Two instances on one 32 GB host, each max=20 GB: combined 40 GB > RAM.
	mk := func(name string, maxMB int64) *model.SQLInstanceInventory {
		fx := baseFixtures()
		fx["server_props"][0]["instance_name"] = name
		fx["configurations"][0]["value_in_use"] = maxMB
		q := &fakeQuerier{fixtures: fx}
		inv, err := CollectInventory(context.Background(), q, Instance{}, "h", nil)
		if err != nil {
			t.Fatal(err)
		}
		return inv
	}
	a, b := mk("INST1", 20480), mk("INST2", 20480)
	if a.MaxMemoryUnlimitedDefault || b.MaxMemoryUnlimitedDefault {
		t.Fatal("explicit max must not be flagged unlimited")
	}
	combined := a.MaxServerMemoryMB + b.MaxServerMemoryMB
	hostMB := a.PhysMemVisibleKB / 1024
	if combined <= hostMB {
		t.Fatalf("fixture should overcommit: %d <= %d", combined, hostMB)
	}
}
