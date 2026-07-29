package agent

import (
	"sort"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/process"

	"github.com/bobarudragos94-wq/costoptimization/internal/model"
)

const maxRecentErrors = 50

// healthTracker aggregates collector status, permission problems and error
// samples for the exported collection-health records. Deduplicated: each
// (collector, error) pair is stored once with the first timestamp.
type healthTracker struct {
	mu         sync.Mutex
	start      time.Time
	collected  uint64
	statuses   map[string]string
	permIssues map[string]bool
	errs       []model.CollectorError
	errSeen    map[string]bool
	selfProc   *process.Process
	prevCPU    float64
	prevCPUTS  time.Time
}

func newHealthTracker() *healthTracker {
	h := &healthTracker{
		start:      time.Now(),
		statuses:   map[string]string{},
		permIssues: map[string]bool{},
		errSeen:    map[string]bool{},
	}
	h.selfProc, _ = process.NewProcess(int32(processPID()))
	return h
}

// Issue records a collector problem (used as the Issues callback everywhere).
func (h *healthTracker) Issue(collector, err string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	key := collector + "|" + err
	if h.errSeen[key] {
		return
	}
	h.errSeen[key] = true
	if isPermissionError(err) {
		h.permIssues[collector+": "+err] = true
		h.statuses[collector] = "degraded:permission"
	} else {
		h.statuses[collector] = "degraded:" + truncate(err, 120)
	}
	if len(h.errs) < maxRecentErrors {
		h.errs = append(h.errs, model.CollectorError{TS: time.Now().UTC(), Collector: collector, Error: truncate(err, 300)})
	}
}

// OK marks a collector healthy (called on each successful cycle).
func (h *healthTracker) OK(collector string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, degraded := h.statuses[collector]; !degraded {
		h.statuses[collector] = "ok"
	}
}

// Unavailable marks a collector permanently absent on this platform.
func (h *healthTracker) Unavailable(collector, why string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.statuses[collector] = "unavailable:" + why
}

func (h *healthTracker) AddSamples(n uint64) {
	h.mu.Lock()
	h.collected += n
	h.mu.Unlock()
}

// Snapshot builds a health record. dropped/spool come from the store.
func (h *healthTracker) Snapshot(dropped, spoolBytes uint64) model.Health {
	h.mu.Lock()
	defer h.mu.Unlock()
	rec := model.Health{
		Meta:             model.NewMeta(model.KindHealth, time.Now()),
		AgentVersion:     model.AgentVersion,
		AgentUptimeS:     uint64(time.Since(h.start).Seconds()),
		SamplesCollected: h.collected,
		SamplesDropped:   dropped,
		CollectorStatus:  map[string]string{},
		SpoolBytes:       spoolBytes,
	}
	for k, v := range h.statuses {
		rec.CollectorStatus[k] = v
	}
	for p := range h.permIssues {
		rec.PermissionIssues = append(rec.PermissionIssues, p)
	}
	sort.Strings(rec.PermissionIssues)
	rec.RecentErrors = append(rec.RecentErrors, h.errs...)

	if h.selfProc != nil {
		if mi, err := h.selfProc.MemoryInfo(); err == nil && mi != nil {
			rec.SelfRSSBytes = mi.RSS
		}
		if t, err := h.selfProc.Times(); err == nil {
			now := time.Now()
			cpu := t.User + t.System
			if !h.prevCPUTS.IsZero() {
				el := now.Sub(h.prevCPUTS).Seconds()
				if el > 0 {
					rec.SelfCPUPct = (cpu - h.prevCPU) / el * 100
				}
			}
			h.prevCPU, h.prevCPUTS = cpu, now
		}
	}
	return rec
}

// PermissionIssues returns the current list (for the manifest summary).
func (h *healthTracker) PermissionIssues() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for p := range h.permIssues {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func isPermissionError(s string) bool {
	for _, sub := range []string{"permission", "denied", "access is denied", "VIEW SERVER STATE", "privilege"} {
		if containsFold(s, sub) {
			return true
		}
	}
	return false
}

func containsFold(s, sub string) bool {
	return len(s) >= len(sub) && indexFold(s, sub) >= 0
}

func indexFold(s, sub string) int {
	n := len(sub)
	for i := 0; i+n <= len(s); i++ {
		match := true
		for j := 0; j < n; j++ {
			a, b := s[i+j], sub[j]
			if 'A' <= a && a <= 'Z' {
				a += 32
			}
			if 'A' <= b && b <= 'Z' {
				b += 32
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
