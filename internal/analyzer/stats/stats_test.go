package stats

import (
	"testing"
	"time"
)

func TestSummarize(t *testing.T) {
	var vals []float64
	for i := 1; i <= 100; i++ {
		vals = append(vals, float64(i))
	}
	s := Summarize(vals)
	if s.P50 != 50.5 || s.Max != 100 || s.Count != 100 {
		t.Fatalf("%+v", s)
	}
	if s.P95 < 95 || s.P95 > 96 || s.P99 < 99 || s.P99 > 100 {
		t.Fatalf("percentiles off: %+v", s)
	}
}

func TestSummarizeEmpty(t *testing.T) {
	if s := Summarize(nil); s.Count != 0 || s.Max != 0 {
		t.Fatalf("%+v", s)
	}
}

func TestSustained(t *testing.T) {
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	var vals []TimedValue
	for i := 0; i < 120; i++ {
		v := 30.0
		if i >= 10 && i < 25 { // 15-minute run
			v = 90
		}
		if i >= 60 && i < 65 { // 5-minute run
			v = 92
		}
		vals = append(vals, TimedValue{TS: base.Add(time.Duration(i) * time.Minute), V: v})
	}
	longest, total := Sustained(vals, 85, 2*time.Minute)
	if longest.Minutes != 15 {
		t.Fatalf("longest = %d, want 15", longest.Minutes)
	}
	if total != 20 {
		t.Fatalf("total minutes above = %d, want 20", total)
	}
	if longest.PeakV != 90 {
		t.Fatalf("peak = %v", longest.PeakV)
	}
}

func TestSustainedGapBreaksRun(t *testing.T) {
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	vals := []TimedValue{
		{TS: base, V: 95},
		{TS: base.Add(1 * time.Minute), V: 95},
		{TS: base.Add(30 * time.Minute), V: 95}, // collection gap
		{TS: base.Add(31 * time.Minute), V: 95},
	}
	longest, total := Sustained(vals, 90, 2*time.Minute)
	if longest.Minutes != 2 || total != 4 {
		t.Fatalf("gap must break run: longest=%d total=%d", longest.Minutes, total)
	}
}

func TestTimeAbovePct(t *testing.T) {
	if p := TimeAbovePct([]float64{10, 90, 95, 20}, 85); p != 0.5 {
		t.Fatalf("%v", p)
	}
}
