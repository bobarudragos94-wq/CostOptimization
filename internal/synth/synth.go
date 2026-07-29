// Package synth generates a realistic synthetic fleet and exports it as real
// encrypted bundles through the production store and bundle code paths, so
// the complete workflow is demonstrable with no access to any environment.
//
// The fleet is designed to exercise every analyzer decision path:
//
//	web-linux-01   8 vCPU/16 GB, ~12% CPU        → likely rightsizing candidate
//	batch-linux-02 8 vCPU/32 GB, nightly 02:00 ETL spike → optimize workload first
//	sql-win-01    16 vCPU/64 GB, SQL max memory unlimited → SQL config issue
//	sqlag-win-02  16 vCPU/64 GB, AG PRIMARY, busy         → no change
//	sqlag-win-03  16 vCPU/64 GB, AG SECONDARY, idle       → HA/DR constraint
//	mem-linux-03   4 vCPU/8 GB, memory pressure           → insufficient headroom
package synth

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"time"

	"github.com/bobarudragos94-wq/costoptimization/internal/bundle"
	"github.com/bobarudragos94-wq/costoptimization/internal/model"
	"github.com/bobarudragos94-wq/costoptimization/internal/store"
)

type hostSpec struct {
	id, name, os     string
	vcpu             int
	ramGB            float64
	baseCPU, ampCPU  float64 // diurnal base and amplitude
	baseMemPct       float64
	nightlySpike     bool // 02:00 UTC CPU spike (ETL)
	spikeProc        string
	spikeUnit        string
	memPressure      bool
	sql              *sqlSpec
}

type sqlSpec struct {
	instance     string
	maxMemMB     uint64 // 2147483647 = unlimited default
	agRole       string // "", "PRIMARY", "SECONDARY"
	agName       string
	partner      string
	busy         bool
}

func fleet() []hostSpec {
	return []hostSpec{
		{id: "ura-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa01", name: "web-linux-01", os: "linux",
			vcpu: 8, ramGB: 16, baseCPU: 10, ampCPU: 6, baseMemPct: 32},
		{id: "ura-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa02", name: "batch-linux-02", os: "linux",
			vcpu: 8, ramGB: 32, baseCPU: 12, ampCPU: 5, baseMemPct: 38,
			nightlySpike: true, spikeProc: "python3", spikeUnit: "etl-nightly.service"},
		{id: "ura-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa03", name: "sql-win-01", os: "windows",
			vcpu: 16, ramGB: 64, baseCPU: 22, ampCPU: 12, baseMemPct: 88,
			nightlySpike: true, spikeProc: "sqlservr.exe", spikeUnit: "MSSQLSERVER",
			sql: &sqlSpec{instance: "MSSQLSERVER", maxMemMB: 2147483647, busy: true}},
		{id: "ura-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa04", name: "sqlag-win-02", os: "windows",
			vcpu: 16, ramGB: 64, baseCPU: 45, ampCPU: 18, baseMemPct: 80,
			sql: &sqlSpec{instance: "MSSQLSERVER", maxMemMB: 51200,
				agRole: "PRIMARY", agName: "AG-CORE", partner: "sqlag-win-03", busy: true}},
		{id: "ura-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa05", name: "sqlag-win-03", os: "windows",
			vcpu: 16, ramGB: 64, baseCPU: 6, ampCPU: 3, baseMemPct: 74,
			sql: &sqlSpec{instance: "MSSQLSERVER", maxMemMB: 51200,
				agRole: "SECONDARY", agName: "AG-CORE", partner: "sqlag-win-02"}},
		{id: "ura-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa06", name: "mem-linux-03", os: "linux",
			vcpu: 4, ramGB: 8, baseCPU: 35, ampCPU: 12, baseMemPct: 93, memPressure: true},
	}
}

// GenerateFleet writes one encrypted bundle per synthetic host into outDir.
func GenerateFleet(recipient, outDir string, days int) ([]string, error) {
	if days < 3 {
		days = 3
	}
	end := time.Now().UTC().Truncate(24 * time.Hour)
	start := end.AddDate(0, 0, -days)
	var paths []string
	for _, hs := range fleet() {
		p, err := generateHost(hs, recipient, outDir, start, end)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", hs.name, err)
		}
		paths = append(paths, p)
	}
	return paths, nil
}

func generateHost(hs hostSpec, recipient, outDir string, start, end time.Time) (string, error) {
	tmp, err := os.MkdirTemp("", "ura-synth-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	st, err := store.Open(tmp)
	if err != nil {
		return "", err
	}
	rng := rand.New(rand.NewSource(int64(len(hs.name)) * 7919))

	st.Append(model.KindInventory, makeInventory(hs, start))
	st.Append(model.KindSched, makeSched(hs, start))

	minutes := 0
	for ts := start; ts.Before(end); ts = ts.Add(time.Minute) {
		hm, spiking := makeMinute(hs, ts, rng)
		// Simulate a small nightly collection gap on one host (data quality path).
		if hs.name == "web-linux-01" && ts.Hour() == 4 && ts.Minute() < 20 {
			continue
		}
		st.Append(model.KindHostMinute, hm)
		minutes++
		_ = spiking
	}

	// Spike events: nightly 02:00 for spike hosts, one ad-hoc event otherwise.
	if hs.nightlySpike {
		for d := start; d.Before(end); d = d.Add(24 * time.Hour) {
			evStart := d.Add(2 * time.Hour).Add(time.Duration(rng.Intn(6)-3) * time.Minute)
			ev := makeSpike(hs, evStart, 25*time.Minute, rng)
			st.Append(model.KindSpike, ev)
			if hs.sql != nil {
				st.Append(model.KindSQLSpike, makeSQLSpike(hs, ev))
			}
		}
	} else if hs.memPressure {
		ev := makeMemSpike(hs, start.Add(50*time.Hour))
		st.Append(model.KindSpike, ev)
	}

	if hs.sql != nil {
		st.Append(model.KindSQLInventory, makeSQLInventory(hs, start))
		for ts := start; ts.Before(end); ts = ts.Add(5 * time.Minute) {
			st.Append(model.KindSQLSample, makeSQLSample(hs, ts, rng))
		}
	}

	// Health records with a permission issue on the pressure host.
	h := model.Health{
		Meta: model.NewMeta(model.KindHealth, end.Add(-time.Hour)),
		AgentVersion: model.AgentVersion, AgentUptimeS: uint64(end.Sub(start).Seconds()),
		SamplesCollected: uint64(minutes) * 4,
		CollectorStatus:  map[string]string{"host": "ok", "proc": "ok"},
		SelfRSSBytes:     48 << 20, SelfCPUPct: 0.4,
	}
	if hs.memPressure {
		h.PermissionIssues = []string{"proc.io: per-process I/O counters unavailable (insufficient privileges)"}
	}
	st.Append(model.KindHealth, h)

	expected := int(end.Sub(start).Minutes())
	return bundle.Export(bundle.ExportInput{
		Store: st, Recipient: recipient, HostID: hs.id, Hostname: hs.name,
		Timezone: "UTC", ConfigHash: "synthetic-demo",
		OutDir: outDir, RemoveAfter: true,
		Health: model.HealthSummary{ExpectedMinutes: expected, CollectedMinutes: minutes,
			PermissionIssues: h.PermissionIssues},
	})
}

func makeMinute(hs hostSpec, ts time.Time, rng *rand.Rand) (*model.HostMinute, bool) {
	hour := float64(ts.Hour()) + float64(ts.Minute())/60
	diurnal := hs.baseCPU + hs.ampCPU*math.Sin((hour-9)/24*2*math.Pi)
	cpu := math.Max(1, diurnal+rng.Float64()*4)
	spiking := false
	if hs.nightlySpike && hour >= 2 && hour < 2.45 {
		cpu = 88 + rng.Float64()*8
		spiking = true
	}
	memPct := hs.baseMemPct + rng.Float64()*3
	hm := &model.HostMinute{
		Meta: model.NewMeta(model.KindHostMinute, ts), Samples: 4,
		CPU: model.CPUAgg{AvgPct: r2(cpu), MaxPct: r2(math.Min(100, cpu+5)),
			UserPct: r2(cpu * 0.7), SystemPct: r2(cpu * 0.25), IOWaitPct: r2(rng.Float64() * 2),
			Load1: r2(cpu / 100 * float64(hs.vcpu))},
		Mem: model.MemAgg{UsedPctAvg: r2(memPct), UsedPctMax: r2(memPct + 2),
			UsedBytesAvg:  uint64(memPct / 100 * hs.ramGB * (1 << 30)),
			AvailBytesMin: uint64((100 - memPct - 2) / 100 * hs.ramGB * (1 << 30))},
		Disks: []model.DiskAgg{{Device: "sda", ReadIOPS: r2(20 + rng.Float64()*30),
			WriteIOPS: r2(40 + rng.Float64()*40), ReadLatMS: r2(1 + rng.Float64()*2),
			WriteLatMS: r2(1.5 + rng.Float64()*2), QueueDepth: r2(rng.Float64()), BusyPct: r2(5 + rng.Float64()*10)}},
		Net: []model.NetAgg{{Name: "eth0", RxBytesPS: r2(2e6 + rng.Float64()*1e6),
			TxBytesPS: r2(1e6 + rng.Float64()*5e5), RxPktsPS: 1200, TxPktsPS: 900}},
	}
	if spiking && hs.sql == nil {
		hm.Disks[0].BusyPct = r2(60 + rng.Float64()*20)
	}
	if hs.memPressure {
		hm.Mem.MajorFaultPS = r2(150 + rng.Float64()*300)
		hm.Mem.PageOutPS = r2(600 + rng.Float64()*400)
		gb := float64(uint64(1) << 30)
		hm.Mem.SwapUsedBytes = uint64(1.2 * gb)
		if hs.os == "linux" {
			hm.PSI = &model.PSIAgg{MemSomeAvg: r2(8 + rng.Float64()*6), MemFullAvg: r2(1.5 + rng.Float64()),
				IOSomeAvg: r2(4 + rng.Float64()*3), CPUSomeAvg: r2(2 + rng.Float64())}
		}
	} else if hs.os == "linux" {
		hm.PSI = &model.PSIAgg{MemSomeAvg: r2(rng.Float64() * 0.5), IOSomeAvg: r2(rng.Float64())}
	}
	if ts.Minute() == 0 { // hourly FS usage
		hm.FS = []model.FSUsage{{Mount: "/", TotalBytes: 200 << 30, AvailBytes: 120 << 30}}
	}
	return hm, spiking
}

func makeSpike(hs hostSpec, start time.Time, dur time.Duration, rng *rand.Rand) *model.SpikeEvent {
	ev := &model.SpikeEvent{
		Meta:    model.NewMeta(model.KindSpike, start.Add(dur)),
		EventID: fmt.Sprintf("%s-%s", hs.name, start.Format("20060102T1504")),
		Resource: model.ResCPU, StartTS: start, EndTS: start.Add(dur),
		DurationS: dur.Seconds(), Baseline: hs.baseCPU, Peak: 94 + rng.Float64()*4,
		Threshold: 85, Trigger: "static",
		CorrelatedProcess:      hs.spikeProc,
		CorrelatedServiceOrJob: hs.spikeUnit,
		AttributionConfidence:  0.75,
		AttributionEvidence: []string{
			fmt.Sprintf("top process during event: %s (86%% of process CPU core-seconds)", hs.spikeProc),
			"scheduled job matches event start time: " + hs.spikeUnit,
		},
		ProbableCause: fmt.Sprintf("probably caused by %s via %s (temporal correlation; validate with owner)",
			hs.spikeProc, hs.spikeUnit),
		ValidationRequired: true,
		CoreSecondsByProc:  map[string]float64{hs.spikeProc: dur.Seconds() * 6.5, "other": dur.Seconds() * 0.5},
		TopProcs: []model.ProcSample{{
			PID: 4242, Name: hs.spikeProc, ServiceUnit: hs.spikeUnit,
			CPUPct: 82, CPUTimeS: 90000, RSSBytes: 3 << 30}},
	}
	for i := 0; i < 20; i++ {
		ev.PreSamples = append(ev.PreSamples, model.MetricPoint{TS: start.Add(time.Duration(i-20) * 15 * time.Second), Value: hs.baseCPU + rng.Float64()*5})
		ev.DuringSamples = append(ev.DuringSamples, model.MetricPoint{TS: start.Add(time.Duration(i) * time.Minute), Value: 90 + rng.Float64()*8})
		ev.PostSamples = append(ev.PostSamples, model.MetricPoint{TS: ev.EndTS.Add(time.Duration(i) * 15 * time.Second), Value: hs.baseCPU + rng.Float64()*5})
	}
	if hs.sql != nil {
		ev.SQLCorrelated = true
		ev.SQLInstance = hs.sql.instance
	}
	return ev
}

func makeMemSpike(hs hostSpec, start time.Time) *model.SpikeEvent {
	return &model.SpikeEvent{
		Meta:    model.NewMeta(model.KindSpike, start.Add(10*time.Minute)),
		EventID: hs.name + "-mem-" + start.Format("20060102T1504"),
		Resource: model.ResMemory, StartTS: start, EndTS: start.Add(10 * time.Minute),
		DurationS: 600, Baseline: hs.baseMemPct, Peak: 98.5, Threshold: 90, Trigger: "static",
		CorrelatedProcess: "java", AttributionConfidence: 0.45,
		AttributionEvidence: []string{"top process during event: java (5.9 GiB RSS)"},
		ProbableCause:       "probably caused by java (temporal correlation; validate with owner)",
		ValidationRequired:  true,
		TopProcs:            []model.ProcSample{{PID: 1801, Name: "java", CPUPct: 30, RSSBytes: 6337748992}},
	}
}

func makeSQLSpike(hs hostSpec, ev *model.SpikeEvent) *model.SQLSpikeContext {
	return &model.SQLSpikeContext{
		Meta:    model.NewMeta(model.KindSQLSpike, ev.StartTS.Add(2*time.Minute)),
		EventID: ev.EventID, InstanceKey: hs.id + "|" + hs.sql.instance,
		CorrelationLevel: model.CorrAgentJob,
		AgentJobs: []model.SQLAgentJobRun{{
			JobName: "Nightly ETL Load", StartTime: ev.StartTS.Add(-30 * time.Second),
			Running: true, Outcome: "running"}},
		ActiveRequests: []model.SQLActiveRequest{{
			SessionID: 71, Database: "SalesDB", Command: "INSERT", Status: "running",
			CPUTimeMS: 840000, ElapsedMS: 900000, LogicalReads: 52000000,
			PhysicalReads: 400000, Writes: 900000, WaitType: "CXPACKET",
			ProgramName: "SQLAgent - TSQL JobStep",
			QueryHash:   "0x9F1B2C3D4E5F6071", PlanHash: "0x1122334455667788"}},
	}
}

func makeSQLInventory(hs hostSpec, start time.Time) *model.SQLInstanceInventory {
	s := hs.sql
	inv := &model.SQLInstanceInventory{
		Meta:        model.NewMeta(model.KindSQLInventory, start.Add(time.Hour)),
		InstanceKey: hs.id + "|" + s.instance, MachineName: hs.name, InstanceName: s.instance,
		IsDefault: s.instance == "MSSQLSERVER",
		Version:   "15.0.4360.2", Build: "15.0.4360.2", ProductLevel: "RTM",
		Edition:   "Standard Edition (64-bit)",
		StartTime: start.AddDate(0, -2, 0), UptimeS: uint64(time.Since(start.AddDate(0, -2, 0)).Seconds()),
		CPUCountVisible: hs.vcpu, SchedulerCount: hs.vcpu,
		PhysMemVisibleKB: uint64(hs.ramGB * (1 << 20)), PID: 4242,
		ServiceAccount: "NT Service\\MSSQLSERVER", MaxDOP: 0, CostThreshold: 5,
		MinServerMemoryMB: 0, MaxServerMemoryMB: s.maxMemMB,
		MaxMemoryUnlimitedDefault: s.maxMemMB >= 2147483647,
		LockPagesInMemory:         "not_detected",
		CollectionLevel:           model.SQLLevelDeep,
		Databases: []model.SQLDatabase{
			{Name: "SalesDB", SizeMB: 51200, LogSizeMB: 8192},
			{Name: "master", SizeMB: 8, LogSizeMB: 2},
		},
		HA: model.HAInfo{Mode: "standalone"},
	}
	if s.agRole != "" {
		inv.HA = model.HAInfo{Mode: "alwayson_ag", AGs: []model.AGInfo{{
			AGName: s.agName, Role: s.agRole, SyncState: "HEALTHY",
			ReadableSecondary: "NO", PartnerReplicas: []string{s.partner}}}}
	}
	return inv
}

func makeSQLSample(hs hostSpec, ts time.Time, rng *rand.Rand) *model.SQLSample {
	s := hs.sql
	hostKB := uint64(hs.ramGB * (1 << 20))
	var totalKB uint64
	if s.maxMemMB >= 2147483647 {
		totalKB = uint64(float64(hostKB) * 0.86) // unlimited: grows toward host RAM
	} else {
		totalKB = uint64(float64(s.maxMemMB) * 1024 * 0.97)
	}
	batch := 40 + rng.Float64()*30
	cpuPct := 8 + rng.Float64()*6
	if s.busy {
		batch = 900 + rng.Float64()*600
		cpuPct = 30 + rng.Float64()*25
	}
	if s.agRole == "SECONDARY" {
		batch = 2 + rng.Float64()*3
		cpuPct = 2 + rng.Float64()*2
	}
	return &model.SQLSample{
		Meta: model.NewMeta(model.KindSQLSample, ts), InstanceKey: hs.id + "|" + s.instance,
		PhysicalMemoryInUseKB: totalKB + 2<<20, // process > memory manager (stacks, DLLs)
		MemoryUtilizationPct:  92,
		TotalServerMemoryKB:   totalKB, TargetServerMemoryKB: totalKB + 1<<20,
		DatabaseCacheKB: uint64(float64(totalKB) * 0.8),
		StolenServerMemoryKB: uint64(float64(totalKB) * 0.15),
		FreeMemoryKB:         uint64(float64(totalKB) * 0.05),
		MemoryGrantsPending:  0, MemoryGrantsActive: int64(rng.Intn(5)),
		PLESeconds: int64(5000 + rng.Intn(4000)),
		MemoryClerksTopKB: map[string]uint64{
			"MEMORYCLERK_SQLBUFFERPOOL": uint64(float64(totalKB) * 0.8),
			"CACHESTORE_SQLCP":          uint64(float64(totalKB) * 0.08),
		},
		BatchRequestsPS: r2(batch), CompilationsPS: r2(batch * 0.05),
		SQLProcessCPUPct: r2(cpuPct),
		PageReadsPS:      r2(rng.Float64() * 100), PageWritesPS: r2(rng.Float64() * 50),
		WaitDeltas: map[string]model.WaitDelta{
			"CXPACKET":       {WaitMS: r2(rng.Float64() * 5000), Count: 100},
			"PAGEIOLATCH_SH": {WaitMS: r2(rng.Float64() * 2000), Count: 60},
		},
		FileIO: []model.DBFileIO{{Database: "SalesDB", FileType: "data",
			ReadLatMS: r2(2 + rng.Float64()*4), WriteLatMS: r2(1 + rng.Float64()*2),
			ReadPS: r2(50 + rng.Float64()*100), WritePS: r2(20 + rng.Float64()*40)}},
		TempdbUsedMB: r2(512 + rng.Float64()*512), TempdbTotalMB: 8192,
	}
}

func makeInventory(hs hostSpec, start time.Time) *model.Inventory {
	osVer, kernel := "Ubuntu 22.04", "5.15.0-générique"
	if hs.os == "windows" {
		osVer, kernel = "Microsoft Windows Server 2022 Standard", "10.0.20348"
	}
	return &model.Inventory{
		Meta: model.NewMeta(model.KindInventory, start), HostID: hs.id, Hostname: hs.name,
		OS: hs.os, OSVersion: osVer, KernelBuild: kernel,
		BootTime: start.AddDate(0, -3, 0), UptimeS: 7776000,
		Timezone: "UTC", Virtualization: "vm:vmware",
		CPUModel: "Intel(R) Xeon(R) Gold 6348 CPU @ 2.60GHz",
		PhysicalCores: hs.vcpu / 2, LogicalCPUs: hs.vcpu, AllocatedVCPU: hs.vcpu,
		TotalRAMBytes: uint64(hs.ramGB * (1 << 30)), SwapTotalBytes: 4 << 30,
		Disks:       []model.DiskInfo{{Device: "sda", SizeBytes: 200 << 30}},
		Filesystems: []model.FSInfo{{Mount: "/", Device: "sda1", FSType: "ext4", TotalBytes: 200 << 30, AvailBytes: 120 << 30}},
		NICs:        []model.NICInfo{{Name: "eth0", MAC: "00:50:56:ab:cd:ef", SpeedMbps: 10000}},
		AgentVersion: model.AgentVersion, ConfigHash: "synthetic-demo",
	}
}

func makeSched(hs hostSpec, start time.Time) model.SchedSnapshot {
	snap := model.SchedSnapshot{Meta: model.NewMeta(model.KindSched, start)}
	if hs.nightlySpike {
		src, name := "systemd_timer", "etl-nightly.timer"
		if hs.os == "windows" {
			src, name = "sql_agent", "Nightly ETL Load"
		}
		snap.Entries = append(snap.Entries, model.SchedEntry{
			Source: src, Name: name, Schedule: "0 2 * * *", Unit: hs.spikeUnit})
	}
	snap.Entries = append(snap.Entries, model.SchedEntry{
		Source: "cron", Name: "logrotate", Schedule: "0 3 * * *"})
	return snap
}

func r2(v float64) float64 { return math.Round(v*100) / 100 }
