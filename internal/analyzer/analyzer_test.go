package analyzer

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bobarudragos94-wq/costoptimization/internal/model"
)

// mkHost builds a host dataset with the given flat utilization for `days`.
func mkHost(id string, days int, cpuPct, memPct float64) *HostData {
	h := newHostData(id)
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	ramBytes := uint64(32) << 30
	for m := 0; m < days*1440; m++ {
		ts := start.Add(time.Duration(m) * time.Minute)
		h.Minutes = append(h.Minutes, model.HostMinute{
			Meta: model.NewMeta(model.KindHostMinute, ts), Samples: 4,
			CPU: model.CPUAgg{AvgPct: cpuPct, MaxPct: cpuPct + 5},
			Mem: model.MemAgg{UsedPctAvg: memPct, UsedPctMax: memPct + 2,
				UsedBytesAvg:  uint64(memPct / 100 * float64(ramBytes)),
				AvailBytesMin: uint64((100 - memPct - 2) / 100 * float64(ramBytes))},
		})
	}
	h.Inventory = &model.Inventory{
		Meta: model.NewMeta(model.KindInventory, start), HostID: id, Hostname: "host-" + id,
		OS: "linux", LogicalCPUs: 8, AllocatedVCPU: 8, TotalRAMBytes: ramBytes,
	}
	h.Manifests = []*model.Manifest{{
		SchemaVersion: model.SchemaVersion, HostID: id, Hostname: "host-" + id,
		ConfigHash: "abc", BundleID: "b-" + id,
		Health: model.HealthSummary{ExpectedMinutes: days * 1440, CollectedMinutes: days * 1440},
	}}
	return h
}

func addSQL(h *HostData, name string, maxMemMB uint64, haMode, haRole string, totalKB uint64) {
	key := h.HostID + "|" + name
	inv := &model.SQLInstanceInventory{
		Meta: model.NewMeta(model.KindSQLInventory, time.Now()), InstanceKey: key,
		InstanceName: name, MaxServerMemoryMB: maxMemMB,
		MaxMemoryUnlimitedDefault: maxMemMB >= 2147483647,
		PhysMemVisibleKB:          32 << 20,
		StartTime:                 time.Now().AddDate(0, -1, 0), UptimeS: 30 * 86400,
		CollectionLevel:           model.SQLLevelDeep,
		HA:                        model.HAInfo{Mode: haMode},
	}
	if haRole != "" {
		inv.HA.AGs = []model.AGInfo{{AGName: "AG1", Role: haRole}}
	}
	h.SQLInv[key] = inv
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 200; i++ {
		h.SQLSamples[key] = append(h.SQLSamples[key], model.SQLSample{
			Meta: model.NewMeta(model.KindSQLSample, start.Add(time.Duration(i)*5*time.Minute)),
			InstanceKey:           key,
			PhysicalMemoryInUseKB: totalKB + 2<<20,
			TotalServerMemoryKB:   totalKB + uint64(i)*1024, // slight growth for dist
			TargetServerMemoryKB:  totalKB + 200*1024,
			DatabaseCacheKB:       uint64(float64(totalKB) * 0.8),
			PLESeconds:            8000, SQLProcessCPUPct: 10, BatchRequestsPS: 50,
		})
	}
}

func TestDegradedCPUCollectorBlocksDownsizing(t *testing.T) {
	h := mkHost("h1", 14, 15, 40) // clear rightsizing candidate numbers
	// Control: healthy collection → candidate.
	if hr := analyzeHost(h); hr.Category != CatLikelyRightsizing {
		t.Fatalf("control: %s", hr.Category)
	}
	// Degraded CPU collector reported by the agent's own health records.
	h.Health = []model.Health{{
		Meta:            model.NewMeta(model.KindHealth, time.Now()),
		CollectorStatus: map[string]string{"cpu.times": "degraded:permission", "host": "ok"},
	}}
	finalizeHost(h)
	hr := analyzeHost(h)
	if hr.Category != CatInsufficientData {
		t.Fatalf("degraded CPU collector must force insufficient_data, got %s", hr.Category)
	}
	if hr.Suggested != nil {
		t.Fatal("no capacity suggestion may survive a degraded-collector gate")
	}
	if hr.Confidence > 0.2 {
		t.Fatalf("confidence must collapse: %v", hr.Confidence)
	}
}

func TestDroppedSamplesBlockDownsizing(t *testing.T) {
	h := mkHost("h2", 14, 15, 40)
	h.Health = []model.Health{{
		Meta:             model.NewMeta(model.KindHealth, time.Now()),
		CollectorStatus:  map[string]string{"host": "ok"},
		SamplesDropped:   20000, // >5% of 14d × 1440 × 4
		SamplesCollected: 60000,
	}}
	finalizeHost(h)
	hr := analyzeHost(h)
	if hr.Category != CatInsufficientData {
		t.Fatalf("heavy sample loss must force insufficient_data, got %s", hr.Category)
	}
}

func TestManifestCoverageOverridesSpanCoverage(t *testing.T) {
	// Minutes span looks complete, but the agent's persisted counters reveal
	// it was down half the period between exports.
	h := mkHost("h3", 7, 15, 40)
	h.Manifests[0].Health = model.HealthSummary{ExpectedMinutes: 7 * 1440 * 2, CollectedMinutes: 7 * 1440}
	finalizeHost(h)
	hr := analyzeHost(h)
	if hr.CoveragePct > 55 {
		t.Fatalf("agent-reported coverage must win when lower: %.1f", hr.CoveragePct)
	}
	if hr.Category == CatLikelyRightsizing {
		t.Fatal("50% coverage must not produce a likely candidate")
	}
}

func TestConsistencyValidation(t *testing.T) {
	h := mkHost("h4", 7, 15, 40)
	h.Manifests = append(h.Manifests, &model.Manifest{
		SchemaVersion: model.SchemaVersion, HostID: "h4", Hostname: "DIFFERENT-NAME",
		ConfigHash: "otherhash", BundleID: "b2",
	})
	h.Inventory.HostID = "not-h4" // substituted inventory
	finalizeHost(h)
	joined := strings.Join(h.Problems, "\n")
	for _, want := range []string{"hostname changed", "configuration changed", "does not match manifest host_id"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing consistency problem %q in:\n%s", want, joined)
		}
	}
}

func TestSchemaMajorMismatch(t *testing.T) {
	if err := schemaCompatible("2.0.0"); err == nil {
		t.Fatal("major version mismatch must be rejected")
	}
	if err := schemaCompatible("1.9.5"); err != nil {
		t.Fatalf("minor drift within major must be accepted: %v", err)
	}
	if err := schemaCompatible(""); err == nil {
		t.Fatal("missing schema version must be rejected")
	}
}

func TestSQLMemoryDistributionReported(t *testing.T) {
	h := mkHost("h5", 14, 30, 70)
	addSQL(h, "MSSQLSERVER", 20480, "standalone", "", 18<<20) // 18 GB total mem
	hr := analyzeHost(h)
	sr := analyzeSQLInstance(h, h.SQLInv["h5|MSSQLSERVER"], &hr)
	if sr.SQLProcessMem.P50 == 0 || sr.SQLProcessMem.P95 == 0 || sr.SQLProcessMem.Max == 0 {
		t.Fatalf("process memory distribution missing: %+v", sr.SQLProcessMem)
	}
	if sr.TotalServerMem.Max < sr.TotalServerMem.P50 {
		t.Fatalf("distribution inconsistent: %+v", sr.TotalServerMem)
	}
}

func TestOSHeadroomUsesObservedAvailableMemory(t *testing.T) {
	// Host: 32 GB RAM at 70% used → ~28% available ≈ 9 GB observed headroom.
	// SQL process is only 8 GB, so the OLD formula (RAM - SQLmem = 24 GB)
	// would overstate headroom nearly 3×.
	h := mkHost("h6", 14, 30, 70)
	addSQL(h, "MSSQLSERVER", 20480, "standalone", "", 8<<20)
	hr := analyzeHost(h)
	sr := analyzeSQLInstance(h, h.SQLInv["h6|MSSQLSERVER"], &hr)
	if sr.OSHeadroomGB > 12 {
		t.Fatalf("headroom must reflect observed available memory (~9 GB), got %.1f", sr.OSHeadroomGB)
	}
	if sr.OSHeadroomGB < 6 {
		t.Fatalf("headroom implausibly low: %.1f", sr.OSHeadroomGB)
	}
}

func TestMaxMemorySuggestionNeverExceedsCurrentLimit(t *testing.T) {
	// Cache peak sits at ~97% of the 20 GB limit; peak×1.2 would exceed the
	// limit. The engine must not "suggest" a raise disguised as a reduction.
	h := mkHost("h7", 14, 20, 70)
	addSQL(h, "MSSQLSERVER", 20480, "standalone", "", uint64(20340000))
	hr := analyzeHost(h)
	sr := analyzeSQLInstance(h, h.SQLInv["h7|MSSQLSERVER"], &hr)
	if sr.SuggestedMaxMemGB != nil {
		if sr.SuggestedMaxMemGB.MaxRAMGB >= 20 {
			t.Fatalf("suggested max %.0f GB >= current 20 GB limit", sr.SuggestedMaxMemGB.MaxRAMGB)
		}
	}
	if sr.Category == CatPossibleRightsizing && sr.SuggestedMaxMemGB == nil {
		t.Fatal("possible_rightsizing without a suggestion is inconsistent")
	}
	// With a genuinely low cache peak, a suggestion is allowed but capped.
	h2 := mkHost("h8", 14, 20, 70)
	addSQL(h2, "MSSQLSERVER", 20480, "standalone", "", 8<<20) // 8 GB of 20 GB... but pct-of-max < 95 → no_change path
	hr2 := analyzeHost(h2)
	sr2 := analyzeSQLInstance(h2, h2.SQLInv["h8|MSSQLSERVER"], &hr2)
	if sr2.SuggestedMaxMemGB != nil && sr2.SuggestedMaxMemGB.MaxRAMGB >= 20 {
		t.Fatalf("cap violated: %+v", sr2.SuggestedMaxMemGB)
	}
}

func TestFCIIsHADRConstraint(t *testing.T) {
	h := mkHost("h9", 14, 15, 40)
	addSQL(h, "MSSQLSERVER", 20480, "fci", "", 8<<20)
	hr := analyzeHost(h)
	sr := analyzeSQLInstance(h, h.SQLInv["h9|MSSQLSERVER"], &hr)
	if sr.Category != CatHADRConstraint {
		t.Fatalf("FCI must be an HA/DR constraint, got %s", sr.Category)
	}
}

func TestDegradedSQLCollectorBlocksSQLRecommendation(t *testing.T) {
	h := mkHost("h10", 14, 15, 40)
	addSQL(h, "MSSQLSERVER", 20480, "standalone", "", uint64(20340000))
	h.DegradedCollectors["sql.MSSQLSERVER.wait_stats"] = "degraded:timeout"
	hr := analyzeHost(h)
	sr := analyzeSQLInstance(h, h.SQLInv["h10|MSSQLSERVER"], &hr)
	if sr.Category != CatInsufficientData {
		t.Fatalf("degraded SQL collection must force insufficient_data, got %s", sr.Category)
	}
}

func TestReconcileHostBlockedBySQLConfig(t *testing.T) {
	h := mkHost("h11", 14, 15, 40) // host numbers say likely candidate
	addSQL(h, "MSSQLSERVER", 2147483647, "standalone", "", 20<<20)
	b := newReportBuilder()
	b.AddHost(h)
	rep := b.Finish()
	host := rep.Hosts[0]
	if host.Category != CatSQLMemoryConfig {
		t.Fatalf("host must be blocked by the SQL memory config issue, got %s", host.Category)
	}
	if host.Suggested != nil {
		t.Fatal("blocked host must not carry a capacity suggestion")
	}
	if rep.SQLInstances[0].Category != CatSQLMemoryConfig {
		t.Fatalf("sql: %s", rep.SQLInstances[0].Category)
	}
}

func TestReconcileHostBlockedByHASecondary(t *testing.T) {
	h := mkHost("h12", 14, 5, 30) // idle secondary numbers
	addSQL(h, "MSSQLSERVER", 20480, "alwayson_ag", "SECONDARY", 8<<20)
	b := newReportBuilder()
	b.AddHost(h)
	rep := b.Finish()
	if rep.Hosts[0].Category != CatHADRConstraint {
		t.Fatalf("idle AG secondary must be HA-constrained at host level, got %s", rep.Hosts[0].Category)
	}
	if rep.Hosts[0].Suggested != nil {
		t.Fatal("secondary must not get a downsizing suggestion")
	}
}

func TestReconcileHostCappedByProcessOnlySQL(t *testing.T) {
	h := mkHost("h13", 14, 15, 40)
	key := "h13|MSSQLSERVER"
	h.SQLInv[key] = &model.SQLInstanceInventory{
		Meta: model.NewMeta(model.KindSQLInventory, time.Now()), InstanceKey: key,
		InstanceName: "MSSQLSERVER", CollectionLevel: model.SQLLevelProcessOnly,
		CollectionNote: "SQL Server detected; deep SQL telemetry unavailable.",
	}
	b := newReportBuilder()
	b.AddHost(h)
	rep := b.Finish()
	if rep.Hosts[0].Category != CatMonitorLonger {
		t.Fatalf("host with blind SQL must be capped at monitor_for_longer, got %s", rep.Hosts[0].Category)
	}
}

func TestMultiInstanceStillReportedPerInstance(t *testing.T) {
	h := mkHost("h14", 14, 25, 75)
	addSQL(h, "INST1", 20480, "standalone", "", 18<<20)
	addSQL(h, "INST2", 20480, "standalone", "", 18<<20)
	b := newReportBuilder()
	b.AddHost(h)
	rep := b.Finish()
	if len(rep.SQLInstances) != 2 {
		t.Fatalf("both instances must be reported: %d", len(rep.SQLInstances))
	}
	for _, s := range rep.SQLInstances {
		if s.Category != CatMultiInstanceRisk {
			t.Fatalf("combined 40 GB max on a 32 GB host must flag multi-instance risk, got %s", s.Category)
		}
	}
}

func TestBuilderStreamingMatchesBatch(t *testing.T) {
	// The streaming path (AddHost one at a time) and the batch path must
	// produce identical categories.
	mk := func() []*HostData {
		a := mkHost("s1", 14, 15, 40)
		bh := mkHost("s2", 14, 85, 70)
		return []*HostData{a, bh}
	}
	ds := &Dataset{Hosts: map[string]*HostData{}}
	for _, h := range mk() {
		ds.Hosts[h.HostID] = h
	}
	batch := Build(ds)
	sb := newReportBuilder()
	for _, h := range mk() {
		sb.AddHost(h)
	}
	streamed := sb.Finish()
	if fmt.Sprint(catList(batch)) != fmt.Sprint(catList(streamed)) {
		t.Fatalf("streaming vs batch divergence: %v vs %v", catList(batch), catList(streamed))
	}
}

func catList(r *Report) []string {
	var out []string
	for _, h := range r.Hosts {
		out = append(out, h.HostID+":"+h.Category)
	}
	return out
}
