package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/bobarudragos94-wq/costoptimization/internal/model"
)

func snap(ts time.Time, procs ...model.ProcSample) model.ProcTop {
	return model.ProcTop{Meta: model.NewMeta(model.KindProcTop, ts), Procs: procs}
}

func TestAttributionDominantProcessWithServiceAndSchedule(t *testing.T) {
	start := time.Date(2026, 7, 1, 2, 0, 0, 0, time.UTC)
	ev := &model.SpikeEvent{
		Resource: model.ResCPU, StartTS: start, EndTS: start.Add(10 * time.Minute),
		Peak: 95, ProbableCause: "unattributed", ValidationRequired: true,
	}
	snaps := []model.ProcTop{
		snap(start.Add(1*time.Minute),
			model.ProcSample{PID: 100, Name: "python3", ServiceUnit: "etl.service", CPUPct: 80, CPUTimeS: 1000, RSSBytes: 1 << 30},
			model.ProcSample{PID: 200, Name: "nginx", CPUPct: 3, CPUTimeS: 500, RSSBytes: 100 << 20}),
		snap(start.Add(9*time.Minute),
			model.ProcSample{PID: 100, Name: "python3", ServiceUnit: "etl.service", CPUPct: 85, CPUTimeS: 1400, RSSBytes: 1 << 30},
			model.ProcSample{PID: 200, Name: "nginx", CPUPct: 2, CPUTimeS: 505, RSSBytes: 100 << 20}),
	}
	scheds := []model.SchedEntry{{Source: "cron", Name: "etl-job", Schedule: "0 2 * * *"}}
	attribute(ev, snaps, scheds, 8, nil, time.UTC)

	if ev.CorrelatedProcess != "python3" {
		t.Fatalf("correlated process: %s", ev.CorrelatedProcess)
	}
	if ev.CorrelatedServiceOrJob != "etl.service" {
		t.Fatalf("service: %s", ev.CorrelatedServiceOrJob)
	}
	if ev.AttributionConfidence < 0.6 || ev.AttributionConfidence > 0.9 {
		t.Fatalf("confidence out of range: %v", ev.AttributionConfidence)
	}
	if !ev.ValidationRequired {
		t.Fatal("validation must remain required")
	}
	if !strings.Contains(ev.ProbableCause, "probably") {
		t.Fatalf("cause must be phrased as probable: %q", ev.ProbableCause)
	}
	if ev.CoreSecondsByProc["python3"] != 400 {
		t.Fatalf("core seconds: %+v", ev.CoreSecondsByProc)
	}
	found := false
	for _, e := range ev.AttributionEvidence {
		if strings.Contains(e, "scheduled job matches") {
			found = true
		}
	}
	if !found {
		t.Fatalf("schedule evidence missing: %v", ev.AttributionEvidence)
	}
}

func TestAttributionSQLProcess(t *testing.T) {
	start := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	ev := &model.SpikeEvent{Resource: model.ResCPU, StartTS: start, EndTS: start.Add(5 * time.Minute)}
	snaps := []model.ProcTop{snap(start.Add(time.Minute),
		model.ProcSample{PID: 4321, Name: "sqlservr", CPUPct: 70, CPUTimeS: 9000, RSSBytes: 20 << 30})}
	attribute(ev, snaps, nil, 8, map[int32]string{4321: "MSSQLSERVER"}, time.UTC)
	if !ev.SQLCorrelated || ev.SQLInstance != "MSSQLSERVER" {
		t.Fatalf("SQL correlation lost: %+v", ev)
	}
}

func TestAttributionNoSnapshots(t *testing.T) {
	ev := &model.SpikeEvent{Resource: model.ResMemory,
		StartTS: time.Now(), EndTS: time.Now().Add(time.Minute)}
	attribute(ev, nil, nil, 4, nil, time.UTC)
	if ev.AttributionConfidence != 0 || !ev.ValidationRequired {
		t.Fatalf("no-evidence case must have zero confidence: %+v", ev)
	}
}

func TestScheduleMatchUsesHostLocalTimezone(t *testing.T) {
	// Host in UTC+2: a cron "0 2 * * *" fires at 02:00 LOCAL = 00:00 UTC.
	loc := time.FixedZone("UTC+2", 2*3600)
	start := time.Date(2026, 7, 1, 0, 1, 0, 0, time.UTC) // 02:01 local
	ev := &model.SpikeEvent{Resource: model.ResCPU, StartTS: start, EndTS: start.Add(5 * time.Minute)}
	snaps := []model.ProcTop{snap(start.Add(time.Minute),
		model.ProcSample{PID: 9, Name: "backup", CPUPct: 88, CPUTimeS: 100, RSSBytes: 1 << 30})}
	scheds := []model.SchedEntry{{Source: "cron", Name: "backup-job", Schedule: "0 2 * * *"}}

	attribute(ev, snaps, scheds, 4, nil, loc)
	if ev.CorrelatedServiceOrJob != "cron:backup-job" {
		t.Fatalf("cron must match in host-local time: %+v", ev.CorrelatedServiceOrJob)
	}

	// The same event evaluated as if the host were UTC must NOT match —
	// proving the match came from the timezone conversion, not coincidence.
	ev2 := &model.SpikeEvent{Resource: model.ResCPU, StartTS: start, EndTS: start.Add(5 * time.Minute)}
	attribute(ev2, snaps, scheds, 4, nil, time.UTC)
	if ev2.CorrelatedServiceOrJob == "cron:backup-job" {
		t.Fatal("control failed: 00:01 UTC should not match a 02:00 cron in UTC")
	}
}

func TestFrequentAndMangledCronEntriesNeverCorrelate(t *testing.T) {
	// Regression from live smoke testing: Debian's stock
	// "*/10 * * * * root [ -x /usr/lib/php/sessionclean ]..." entry parsed to
	// a job named "[" with a schedule matching every event time — it was
	// attached as schedule evidence (+0.2 confidence) to an unrelated spike.
	start := time.Date(2026, 7, 1, 14, 23, 0, 0, time.UTC)
	ev := &model.SpikeEvent{Resource: model.ResCPU, StartTS: start, EndTS: start.Add(3 * time.Minute)}
	snaps := []model.ProcTop{snap(start.Add(time.Minute),
		model.ProcSample{PID: 7, Name: "bash", CPUPct: 95, CPUTimeS: 500, RSSBytes: 10 << 20})}
	scheds := []model.SchedEntry{
		{Source: "cron", Name: "[", Schedule: "*/10 * * * *"},          // mangled + frequent
		{Source: "cron", Name: "sessionclean", Schedule: "*/10 * * * *"}, // clean name, still frequent
	}
	attribute(ev, snaps, scheds, 4, nil, time.UTC)
	if ev.CorrelatedServiceOrJob != "" {
		t.Fatalf("frequent/mangled cron entries must never correlate: %q", ev.CorrelatedServiceOrJob)
	}
	for _, e := range ev.AttributionEvidence {
		if strings.Contains(e, "scheduled job") {
			t.Fatalf("no schedule evidence expected: %v", ev.AttributionEvidence)
		}
	}
	// A daily entry at the right time still correlates.
	ev2 := &model.SpikeEvent{Resource: model.ResCPU, StartTS: start, EndTS: start.Add(3 * time.Minute)}
	attribute(ev2, snaps, []model.SchedEntry{
		{Source: "cron", Name: "daily-report", Schedule: "23 14 * * *"}}, 4, nil, time.UTC)
	if ev2.CorrelatedServiceOrJob != "cron:daily-report" {
		t.Fatalf("legitimate daily schedule must still correlate: %q", ev2.CorrelatedServiceOrJob)
	}
}

func TestMemorySpikeRankedByRSS(t *testing.T) {
	start := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	ev := &model.SpikeEvent{Resource: model.ResMemory, StartTS: start, EndTS: start.Add(5 * time.Minute)}
	snaps := []model.ProcTop{snap(start.Add(time.Minute),
		model.ProcSample{PID: 1, Name: "busy-cpu", CPUPct: 90, CPUTimeS: 100, RSSBytes: 50 << 20},
		model.ProcSample{PID: 2, Name: "java", CPUPct: 5, CPUTimeS: 50, RSSBytes: 6 << 30})}
	attribute(ev, snaps, nil, 4, nil, time.UTC)
	if ev.CorrelatedProcess != "java" {
		t.Fatalf("memory spikes must rank by RSS, got %s", ev.CorrelatedProcess)
	}
}
