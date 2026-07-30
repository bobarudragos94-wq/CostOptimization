package proctop

import (
	"testing"
	"time"

	"github.com/bobarudragos94-wq/costoptimization/internal/model"
)

// TestHistoryIsDurationBased verifies that attribution history keeps a time
// window, not a snapshot count: high-frequency capture snapshots must not
// evict the pre-spike context.
func TestHistoryIsDurationBased(t *testing.T) {
	c := New(5, nil, nil)
	c.histMaxAge = 10 * time.Minute
	now := time.Now()

	push := func(ts time.Time) {
		c.mu.Lock()
		c.history = append(c.history, model.ProcTop{Meta: model.NewMeta(model.KindProcTop, ts)})
		c.mu.Unlock()
	}
	// 8 minutes ago: normal minute-cadence snapshots.
	for i := 0; i < 5; i++ {
		push(now.Add(-8*time.Minute + time.Duration(i)*time.Minute))
	}
	// Burst: 40 snapshots at 15 s cadence in the last minute (spike capture).
	for i := 0; i < 40; i++ {
		push(now.Add(-time.Minute + time.Duration(i)*1500*time.Millisecond))
	}
	// Trigger the pruning path via a real snapshot.
	c.Snapshot(1)

	old := c.History(now.Add(-9*time.Minute), now.Add(-7*time.Minute))
	if len(old) == 0 {
		t.Fatal("burst of high-frequency snapshots evicted older in-window history (count-based ring regression)")
	}
	// And genuinely old entries are pruned.
	c.mu.Lock()
	c.history = append([]model.ProcTop{{Meta: model.NewMeta(model.KindProcTop, now.Add(-time.Hour))}}, c.history...)
	c.mu.Unlock()
	c.Snapshot(1)
	stale := c.History(now.Add(-2*time.Hour), now.Add(-30*time.Minute))
	if len(stale) != 0 {
		t.Fatal("entries older than histMaxAge must be pruned")
	}
}

// TestHistoryHardCap guards the memory bound regardless of frequency.
func TestHistoryHardCap(t *testing.T) {
	c := New(5, nil, nil)
	c.histMaxAge = 24 * time.Hour
	c.histMaxSnap = 50
	now := time.Now()
	for i := 0; i < 300; i++ {
		c.mu.Lock()
		c.history = append(c.history, model.ProcTop{Meta: model.NewMeta(model.KindProcTop, now.Add(time.Duration(i)*time.Second))})
		c.mu.Unlock()
	}
	c.Snapshot(1)
	if len(c.history) > 51 { // cap + the snapshot itself
		t.Fatalf("hard cap not enforced: %d", len(c.history))
	}
}
