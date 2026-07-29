package spike

import (
	"testing"
	"time"

	"github.com/bobarudragos94-wq/costoptimization/internal/config"
	"github.com/bobarudragos94-wq/costoptimization/internal/model"
)

func testCfg() *config.Config {
	c := config.Default()
	c.Agent.Recipient = "age1test" // not used by detector
	c.Sampling.HostInterval = config.Duration{Duration: 15 * time.Second}
	c.Spikes.PreBuffer = config.Duration{Duration: 2 * time.Minute}
	c.Spikes.PostCapture = config.Duration{Duration: 1 * time.Minute}
	c.Spikes.Cooldown = config.Duration{Duration: 5 * time.Minute}
	c.Spikes.Rules = []config.SpikeRule{
		{Resource: "cpu", StaticThreshold: 85, Sustained: config.Duration{Duration: 1 * time.Minute}, BaselineK: 4, BaselineFloor: 40},
		{Resource: "memory", StaticThreshold: 90, Sustained: config.Duration{Duration: 1 * time.Minute}},
		{Resource: "disk_latency", StaticThreshold: 50, Sustained: config.Duration{Duration: 30 * time.Second}},
	}
	return c
}

// run feeds a value series at 15s cadence, returns completed events.
func run(t *testing.T, d *Detector, key Key, vals []float64, start time.Time) []*model.SpikeEvent {
	t.Helper()
	var out []*model.SpikeEvent
	for i, v := range vals {
		ts := start.Add(time.Duration(i) * 15 * time.Second)
		out = append(out, d.Offer(Point{TS: ts, Values: map[Key]float64{key: v}})...)
	}
	return out
}

func series(pattern ...struct {
	n int
	v float64
}) []float64 {
	var s []float64
	for _, p := range pattern {
		for i := 0; i < p.n; i++ {
			s = append(s, p.v)
		}
	}
	return s
}

func seg(n int, v float64) struct {
	n int
	v float64
} {
	return struct {
		n int
		v float64
	}{n, v}
}

func TestCPUSpikeDetectedAndCaptured(t *testing.T) {
	d := NewDetector(testCfg())
	start := time.Date(2026, 7, 1, 2, 0, 0, 0, time.UTC)
	// 10 min quiet, 5 min at 95%, back to quiet long enough for close+post.
	vals := series(seg(40, 20), seg(20, 95), seg(40, 15))
	events := run(t, d, Key{Resource: "cpu"}, vals, start)
	if len(events) != 1 {
		t.Fatalf("want 1 event, got %d", len(events))
	}
	ev := events[0]
	if ev.Resource != "cpu" || ev.Peak != 95 {
		t.Fatalf("bad event: %+v", ev)
	}
	if ev.DurationS < 4*60 || ev.DurationS > 6*60 {
		t.Fatalf("duration ~5min expected, got %v", ev.DurationS)
	}
	if len(ev.PreSamples) == 0 || len(ev.DuringSamples) == 0 || len(ev.PostSamples) == 0 {
		t.Fatalf("pre/during/post must be captured: %d/%d/%d",
			len(ev.PreSamples), len(ev.DuringSamples), len(ev.PostSamples))
	}
	if ev.StartTS.Sub(start) > 11*time.Minute {
		t.Fatalf("start should be near the climb: %v", ev.StartTS)
	}
	if !ev.ValidationRequired || ev.ProbableCause != "unattributed" {
		t.Fatalf("detector must not claim causes: %+v", ev)
	}
}

func TestShortBlipIgnored(t *testing.T) {
	d := NewDetector(testCfg())
	start := time.Date(2026, 7, 1, 2, 0, 0, 0, time.UTC)
	// Two samples (30s) above threshold — below the 1 min sustained requirement.
	vals := series(seg(40, 20), seg(2, 99), seg(40, 20))
	events := run(t, d, Key{Resource: "cpu"}, vals, start)
	if len(events) != 0 {
		t.Fatalf("short blip must not trigger, got %d events", len(events))
	}
}

func TestCooldownSuppressesRepeat(t *testing.T) {
	d := NewDetector(testCfg())
	start := time.Date(2026, 7, 1, 2, 0, 0, 0, time.UTC)
	vals := series(seg(10, 20), seg(8, 95), seg(10, 10), // event 1 + close + post
		seg(4, 95), seg(10, 10)) // within cooldown: suppressed
	events := run(t, d, Key{Resource: "cpu"}, vals, start)
	if len(events) != 1 {
		t.Fatalf("cooldown should suppress the second event, got %d", len(events))
	}
}

func TestMemorySpikeAndDiskLatencyIndependent(t *testing.T) {
	d := NewDetector(testCfg())
	start := time.Date(2026, 7, 1, 2, 0, 0, 0, time.UTC)
	var events []*model.SpikeEvent
	for i := 0; i < 100; i++ {
		ts := start.Add(time.Duration(i) * 15 * time.Second)
		mem, lat := 50.0, 5.0
		if i >= 20 && i < 40 {
			mem = 95 // memory event
		}
		if i >= 50 && i < 60 {
			lat = 120 // disk latency event on device sda
		}
		events = append(events, d.Offer(Point{TS: ts, Values: map[Key]float64{
			{Resource: "memory"}:                      mem,
			{Resource: "disk_latency", Device: "sda"}: lat,
		}})...)
	}
	var mems, lats int
	for _, e := range events {
		switch e.Resource {
		case "memory":
			mems++
		case "disk_latency":
			lats++
			if e.Device != "sda" {
				t.Fatalf("device lost: %+v", e)
			}
		}
	}
	if mems != 1 || lats != 1 {
		t.Fatalf("want 1 memory + 1 disk_latency, got %d/%d", mems, lats)
	}
}

func TestBaselineDeviationTrigger(t *testing.T) {
	c := testCfg()
	// Static threshold far away; baseline trigger should still fire.
	c.Spikes.Rules = []config.SpikeRule{
		{Resource: "cpu", StaticThreshold: 99, Sustained: config.Duration{Duration: 1 * time.Minute}, BaselineK: 4, BaselineFloor: 30},
	}
	d := NewDetector(c)
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	// Long quiet baseline around 10% (some noise), then sustained 60%: far
	// above median+4·MAD but below the 99% static threshold.
	var vals []float64
	for i := 0; i < baselineMinSamples+50; i++ {
		vals = append(vals, 8+float64(i%5)) // 8..12
	}
	vals = append(vals, series(seg(20, 60), seg(40, 10))...)
	events := run(t, d, Key{Resource: "cpu"}, vals, start)
	if len(events) != 1 {
		t.Fatalf("baseline deviation should trigger once, got %d", len(events))
	}
	if events[0].Trigger != "baseline_deviation" {
		t.Fatalf("trigger = %s, want baseline_deviation", events[0].Trigger)
	}
	if events[0].Baseline < 5 || events[0].Baseline > 15 {
		t.Fatalf("baseline should reflect quiet median, got %v", events[0].Baseline)
	}
}

func TestBoundedCapture(t *testing.T) {
	d := NewDetector(testCfg())
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	// Very long event: capture buffers must stay bounded.
	vals := series(seg(40, 20), seg(2000, 95), seg(40, 10))
	events := run(t, d, Key{Resource: "cpu"}, vals, start)
	if len(events) != 1 {
		t.Fatalf("want 1 event, got %d", len(events))
	}
	ev := events[0]
	if len(ev.DuringSamples) > maxDuringPoints || len(ev.PreSamples) > maxPrePoints || len(ev.PostSamples) > maxPostPoints {
		t.Fatalf("capture unbounded: %d/%d/%d", len(ev.PreSamples), len(ev.DuringSamples), len(ev.PostSamples))
	}
	if ev.DurationS < 2000*15*0.9 {
		t.Fatalf("duration should still cover the whole event: %v", ev.DurationS)
	}
}
