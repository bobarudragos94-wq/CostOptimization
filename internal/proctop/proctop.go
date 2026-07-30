// Package proctop samples per-process telemetry with bounded cardinality:
// only the top-N processes by CPU and by memory are retained per snapshot.
// Command-line arguments are never collected (see threat model); only the
// executable path and name identify a process.
package proctop

import (
	"sort"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/process"

	"github.com/bobarudragos94-wq/costoptimization/internal/model"
)

// ServiceMapper resolves a PID to its service/unit name. Implementations:
// systemd cgroup parsing on Linux, SCM enumeration on Windows.
type ServiceMapper interface {
	ServiceFor(pid int32) string
}

type prevProc struct {
	cpuSeconds float64
	readBytes  uint64
	writeBytes uint64
	seenAt     time.Time
}

type Collector struct {
	topN   int
	mapper ServiceMapper
	issues func(collector, err string)
	mu     sync.Mutex
	prev   map[int32]prevProc
	// history holds recent snapshots for spike attribution. It is bounded by
	// AGE, not count: during an active spike capture snapshots arrive every
	// 15 s instead of every minute, and a count-based ring would silently
	// shrink the attribution window exactly when it matters most. A hard
	// count cap guards memory regardless of snapshot frequency.
	history     []model.ProcTop
	histMaxAge  time.Duration
	histMaxSnap int
}

func New(topN int, mapper ServiceMapper, issues func(string, string)) *Collector {
	if issues == nil {
		issues = func(string, string) {}
	}
	return &Collector{
		topN: topN, mapper: mapper, issues: issues,
		prev:       map[int32]prevProc{},
		histMaxAge: 30 * time.Minute, histMaxSnap: 240,
	}
}

// Snapshot samples all processes, computes CPU/IO rates against the previous
// snapshot and returns the merged top-N by CPU and by RSS. numCPU normalizes
// CPU% to total host capacity.
func (c *Collector) Snapshot(numCPU int) model.ProcTop {
	now := time.Now()
	procs, err := process.Processes()
	if err != nil {
		c.issues("proc.list", err.Error())
		return model.ProcTop{Meta: model.NewMeta(model.KindProcTop, now)}
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	type scored struct {
		s   model.ProcSample
		cpu float64
	}
	var all []scored
	next := map[int32]prevProc{}
	permDenied := false

	for _, p := range procs {
		pid := p.Pid
		times, err := p.Times()
		if err != nil {
			continue // exited or inaccessible
		}
		cpuSec := times.User + times.System
		pp, hadPrev := c.prev[pid]
		np := prevProc{cpuSeconds: cpuSec, seenAt: now}

		var cpuPct float64
		if hadPrev {
			elapsed := now.Sub(pp.seenAt).Seconds()
			if elapsed > 0 && numCPU > 0 {
				cpuPct = (cpuSec - pp.cpuSeconds) / elapsed / float64(numCPU) * 100
				if cpuPct < 0 {
					cpuPct = 0
				}
			}
		}

		s := model.ProcSample{PID: pid, CPUPct: round2(cpuPct), CPUTimeS: round2(cpuSec)}
		if name, err := p.Name(); err == nil {
			s.Name = name
		}
		if ppid, err := p.Ppid(); err == nil {
			s.PPID = ppid
		}
		if mi, err := p.MemoryInfo(); err == nil && mi != nil {
			s.RSSBytes = mi.RSS
		}
		if io, err := p.IOCounters(); err == nil && io != nil {
			np.readBytes, np.writeBytes = io.ReadBytes, io.WriteBytes
			if hadPrev {
				elapsed := now.Sub(pp.seenAt).Seconds()
				if elapsed > 0 {
					s.ReadBytesPS = round2(deltaU64(io.ReadBytes, pp.readBytes) / elapsed)
					s.WriteBytesPS = round2(deltaU64(io.WriteBytes, pp.writeBytes) / elapsed)
				}
			}
		} else if err != nil {
			permDenied = true
		}
		if ct, err := p.CreateTime(); err == nil {
			s.StartTime = time.UnixMilli(ct).UTC()
		}
		next[pid] = np
		all = append(all, scored{s: s, cpu: cpuPct})
	}
	c.prev = next // pruning: only live PIDs carry over (bounded)
	if permDenied {
		c.issues("proc.io", "per-process I/O counters unavailable for some processes (insufficient privileges); process disk attribution degraded")
	}

	// Top-N by CPU union top-N by RSS.
	byCPU := make([]scored, len(all))
	copy(byCPU, all)
	sort.Slice(byCPU, func(i, j int) bool { return byCPU[i].cpu > byCPU[j].cpu })
	byRSS := make([]scored, len(all))
	copy(byRSS, all)
	sort.Slice(byRSS, func(i, j int) bool { return byRSS[i].s.RSSBytes > byRSS[j].s.RSSBytes })

	pick := map[int32]model.ProcSample{}
	take := func(list []scored) {
		n := 0
		for _, sc := range list {
			if n >= c.topN {
				break
			}
			if _, ok := pick[sc.s.PID]; !ok {
				pick[sc.s.PID] = sc.s
				n++
			}
		}
	}
	take(byCPU)
	take(byRSS)

	top := model.ProcTop{Meta: model.NewMeta(model.KindProcTop, now)}
	for _, s := range pick {
		// Exe path and service mapping only for the retained few (cheap).
		if c.mapper != nil {
			s.ServiceUnit = c.mapper.ServiceFor(s.PID)
		}
		if p, err := process.NewProcess(s.PID); err == nil {
			if exe, err := p.Exe(); err == nil {
				s.Exe = exe
			}
		}
		top.Procs = append(top.Procs, s)
	}
	sort.Slice(top.Procs, func(i, j int) bool { return top.Procs[i].CPUPct > top.Procs[j].CPUPct })

	c.history = append(c.history, top)
	cutoff := now.Add(-c.histMaxAge)
	drop := 0
	for drop < len(c.history) && c.history[drop].TS.Before(cutoff) {
		drop++
	}
	if over := len(c.history) - drop - c.histMaxSnap; over > 0 {
		drop += over
	}
	if drop > 0 {
		c.history = append(c.history[:0], c.history[drop:]...)
	}
	return top
}

// History returns recent snapshots overlapping [from, to] (spike attribution).
func (c *Collector) History(from, to time.Time) []model.ProcTop {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []model.ProcTop
	for _, h := range c.history {
		if !h.TS.Before(from) && !h.TS.After(to) {
			out = append(out, h)
		}
	}
	return out
}

func deltaU64(cur, prev uint64) float64 {
	if cur < prev {
		return 0
	}
	return float64(cur - prev)
}

func round2(v float64) float64 { return float64(int(v*100)) / 100 }
