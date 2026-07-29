package agent

import (
	"time"

	"github.com/shirou/gopsutil/v4/process"

	"github.com/bobarudragos94-wq/costoptimization/internal/proctop"
)

func timeNow() time.Time { return time.Now() }

// processCPUPct returns the most recent CPU% for a PID from the proc
// collector's history; falls back to a direct probe.
func processCPUPct(procs *proctop.Collector, pid int32) float64 {
	now := time.Now()
	for _, snap := range procs.History(now.Add(-3*time.Minute), now) {
		for _, p := range snap.Procs {
			if p.PID == pid {
				return p.CPUPct
			}
		}
	}
	if p, err := process.NewProcess(pid); err == nil {
		if pct, err := p.CPUPercent(); err == nil {
			return pct
		}
	}
	return 0
}
