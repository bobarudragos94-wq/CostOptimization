// Package stats provides exact percentile and sustained-utilization math for
// the analyzer. Percentiles are computed over persisted minute aggregates
// (average-of-minute for typical load, max-of-minute for peaks).
package stats

import (
	"math"
	"sort"
	"time"
)

// Summary of a metric series.
type Summary struct {
	P50   float64 `json:"p50"`
	P95   float64 `json:"p95"`
	P99   float64 `json:"p99"`
	Max   float64 `json:"max"`
	Avg   float64 `json:"avg"`
	Count int     `json:"count"`
}

func Summarize(values []float64) Summary {
	if len(values) == 0 {
		return Summary{}
	}
	s := make([]float64, len(values))
	copy(s, values)
	sort.Float64s(s)
	var sum float64
	for _, v := range s {
		sum += v
	}
	return Summary{
		P50:   round2(Quantile(s, 0.50)),
		P95:   round2(Quantile(s, 0.95)),
		P99:   round2(Quantile(s, 0.99)),
		Max:   round2(s[len(s)-1]),
		Avg:   round2(sum / float64(len(s))),
		Count: len(s),
	}
}

// Quantile over a sorted slice, linear interpolation.
func Quantile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	pos := q * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return sorted[lo]
	}
	frac := pos - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}

// TimedValue is a minute observation.
type TimedValue struct {
	TS time.Time
	V  float64
}

// SustainedRun describes a contiguous run of minutes above a threshold.
type SustainedRun struct {
	Start   time.Time `json:"start"`
	End     time.Time `json:"end"`
	Minutes int       `json:"minutes"`
	PeakV   float64   `json:"peak"`
}

// Sustained returns the longest run above threshold and total minutes above.
// Values must be time-ordered; gaps larger than gapTolerance break a run.
func Sustained(values []TimedValue, threshold float64, gapTolerance time.Duration) (longest SustainedRun, totalMinutes int) {
	var cur *SustainedRun
	var lastTS time.Time
	for _, tv := range values {
		if tv.V >= threshold {
			totalMinutes++
			if cur != nil && tv.TS.Sub(lastTS) <= gapTolerance {
				cur.End = tv.TS
				cur.Minutes++
				if tv.V > cur.PeakV {
					cur.PeakV = tv.V
				}
			} else {
				if cur != nil && cur.Minutes > longest.Minutes {
					longest = *cur
				}
				cur = &SustainedRun{Start: tv.TS, End: tv.TS, Minutes: 1, PeakV: tv.V}
			}
			lastTS = tv.TS
		} else {
			if cur != nil && cur.Minutes > longest.Minutes {
				longest = *cur
			}
			cur = nil
		}
	}
	if cur != nil && cur.Minutes > longest.Minutes {
		longest = *cur
	}
	return longest, totalMinutes
}

// TimeAbovePct returns the fraction (0..1) of observations at/above threshold.
func TimeAbovePct(values []float64, threshold float64) float64 {
	if len(values) == 0 {
		return 0
	}
	n := 0
	for _, v := range values {
		if v >= threshold {
			n++
		}
	}
	return float64(n) / float64(len(values))
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }
