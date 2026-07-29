package agent

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bobarudragos94-wq/costoptimization/internal/model"
	"github.com/bobarudragos94-wq/costoptimization/internal/sched"
)

// attribute fills the correlation fields of a completed spike event from
// process snapshots, scheduled-job data and SQL detection.
//
// Structural rule: temporal correlation is never presented as a definite
// cause. Confidence is capped at 0.9, probable_cause is phrased as probable,
// and validation_required stays true unless evidence is strong and multi-source.
func attribute(ev *model.SpikeEvent, snapshots []model.ProcTop, schedEntries []model.SchedEntry, numCPU int, sqlPIDs map[int32]string) {
	ev.ValidationRequired = true
	ev.AttributionConfidence = 0

	procs := rankProcesses(ev, snapshots, numCPU)
	if len(procs) == 0 {
		ev.ProbableCause = "no process telemetry captured during event"
		ev.AttributionEvidence = append(ev.AttributionEvidence, "no overlapping process snapshots")
		return
	}
	ev.TopProcs = procs
	top := procs[0]
	ev.CorrelatedProcess = top.Name
	conf := 0.3
	ev.AttributionEvidence = append(ev.AttributionEvidence,
		fmt.Sprintf("top process during event: %s (pid %d, %.1f%% CPU, %s RSS)",
			top.Name, top.PID, top.CPUPct, fmtBytes(top.RSSBytes)))

	// Core-seconds: dominance of the top process in the event window.
	if ev.Resource == model.ResCPU && len(ev.CoreSecondsByProc) > 0 {
		var total, topCS float64
		for name, cs := range ev.CoreSecondsByProc {
			total += cs
			if name == top.Name {
				topCS = cs
			}
		}
		if total > 0 && topCS/total > 0.5 {
			conf += 0.2
			ev.AttributionEvidence = append(ev.AttributionEvidence,
				fmt.Sprintf("%s consumed %.0f%% of process CPU core-seconds during the event (%.1f of %.1f core-s)",
					top.Name, topCS/total*100, topCS, total))
		}
	}

	if top.ServiceUnit != "" {
		ev.CorrelatedServiceOrJob = top.ServiceUnit
		conf += 0.15
		ev.AttributionEvidence = append(ev.AttributionEvidence,
			"process belongs to service/unit "+top.ServiceUnit)
	}

	// Scheduled-job timing correlation (±10 min around event start).
	if job := matchSchedule(schedEntries, ev.StartTS); job != "" {
		if ev.CorrelatedServiceOrJob == "" {
			ev.CorrelatedServiceOrJob = job
		}
		conf += 0.2
		ev.AttributionEvidence = append(ev.AttributionEvidence,
			"scheduled job matches event start time: "+job)
	}

	// SQL process involvement.
	if inst, ok := sqlPIDs[top.PID]; ok || isSQLProcess(top.Name) {
		ev.SQLCorrelated = true
		if ok {
			ev.SQLInstance = inst
		}
		ev.AttributionEvidence = append(ev.AttributionEvidence,
			"top process is a SQL Server engine process")
	}

	if conf > 0.9 {
		conf = 0.9
	}
	ev.AttributionConfidence = round2f(conf)
	ev.ProbableCause = fmt.Sprintf("probably caused by %s%s (temporal correlation; validate with owner)",
		top.Name, suffixIf(ev.CorrelatedServiceOrJob != "", " via "+ev.CorrelatedServiceOrJob))
}

// rankProcesses merges snapshots overlapping the event and ranks by the
// resource that spiked. Also computes CPU core-seconds per process from
// cumulative CPU-time deltas (used for vCPU-reduction impact estimates).
func rankProcesses(ev *model.SpikeEvent, snapshots []model.ProcTop, numCPU int) []model.ProcSample {
	type acc struct {
		s          model.ProcSample
		firstCPUs  float64
		lastCPUs   float64
		maxCPUPct  float64
		maxRSS     uint64
		maxIOps    float64
	}
	byPID := map[int32]*acc{}
	for _, snap := range snapshots {
		for _, p := range snap.Procs {
			a := byPID[p.PID]
			if a == nil {
				a = &acc{s: p, firstCPUs: p.CPUTimeS}
				byPID[p.PID] = a
			}
			a.lastCPUs = p.CPUTimeS
			if p.CPUPct > a.maxCPUPct {
				a.maxCPUPct = p.CPUPct
				a.s.CPUPct = p.CPUPct
			}
			if p.RSSBytes > a.maxRSS {
				a.maxRSS = p.RSSBytes
				a.s.RSSBytes = p.RSSBytes
			}
			if io := p.ReadBytesPS + p.WriteBytesPS; io > a.maxIOps {
				a.maxIOps = io
				a.s.ReadBytesPS, a.s.WriteBytesPS = p.ReadBytesPS, p.WriteBytesPS
			}
			if p.ServiceUnit != "" {
				a.s.ServiceUnit = p.ServiceUnit
			}
		}
	}
	coreSeconds := map[string]float64{}
	var out []model.ProcSample
	for _, a := range byPID {
		cs := a.lastCPUs - a.firstCPUs
		if cs > 0 {
			coreSeconds[a.s.Name] += round2f(cs)
		}
		out = append(out, a.s)
	}
	if len(coreSeconds) > 0 {
		ev.CoreSecondsByProc = coreSeconds
	}
	sort.Slice(out, func(i, j int) bool {
		switch ev.Resource {
		case model.ResMemory, model.ResPaging:
			return out[i].RSSBytes > out[j].RSSBytes
		case model.ResDiskIO, model.ResDiskLatency, model.ResDiskQueue:
			return out[i].ReadBytesPS+out[i].WriteBytesPS > out[j].ReadBytesPS+out[j].WriteBytesPS
		default:
			return coreSeconds[out[i].Name] > coreSeconds[out[j].Name]
		}
	})
	if len(out) > 10 {
		out = out[:10]
	}
	return out
}

// matchSchedule finds a scheduled entry whose firing time matches the event
// start within ±10 minutes.
func matchSchedule(entries []model.SchedEntry, start time.Time) string {
	const tol = 10 * time.Minute
	for _, e := range entries {
		switch {
		case e.Schedule != "" && sched.MatchesTime(e.Schedule, start, tol):
			return e.Source + ":" + e.Name
		case !e.NextRun.IsZero() && sameTimeOfDay(e.NextRun, start, tol):
			return e.Source + ":" + e.Name
		case !e.LastRun.IsZero() && absDur(start.Sub(e.LastRun)) <= tol:
			return e.Source + ":" + e.Name
		}
	}
	return ""
}

func sameTimeOfDay(a, b time.Time, tol time.Duration) bool {
	am := a.UTC().Hour()*60 + a.UTC().Minute()
	bm := b.UTC().Hour()*60 + b.UTC().Minute()
	d := am - bm
	if d < 0 {
		d = -d
	}
	if d > 720 {
		d = 1440 - d
	}
	return time.Duration(d)*time.Minute <= tol
}

func isSQLProcess(name string) bool {
	n := strings.ToLower(strings.TrimSuffix(name, ".exe"))
	return n == "sqlservr"
}

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

func suffixIf(cond bool, s string) string {
	if cond {
		return s
	}
	return ""
}

func fmtBytes(b uint64) string {
	const g = 1 << 30
	const m = 1 << 20
	switch {
	case b >= g:
		return fmt.Sprintf("%.1f GiB", float64(b)/g)
	case b >= m:
		return fmt.Sprintf("%.0f MiB", float64(b)/m)
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func round2f(v float64) float64 { return float64(int(v*100)) / 100 }
