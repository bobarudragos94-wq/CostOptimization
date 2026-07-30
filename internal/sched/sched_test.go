package sched

import (
	"testing"
	"time"
)

func TestTooFrequentForCorrelation(t *testing.T) {
	cases := []struct {
		expr string
		want bool
	}{
		{"*/10 * * * *", true},  // every 10 min: matches everything — worthless
		{"* * * * *", true},     // every minute
		{"0,30 * * * *", true},  // twice hourly = 48/day
		{"0 * * * *", false},    // hourly: exactly the 24/day boundary
		{"0 2 * * *", false},    // daily
		{"17 3 * * 0", false},   // weekly
		{"broken", true},        // unparseable: never use as evidence
	}
	for _, c := range cases {
		if got := TooFrequentForCorrelation(c.expr); got != c.want {
			t.Errorf("TooFrequentForCorrelation(%q) = %v, want %v", c.expr, got, c.want)
		}
	}
}

func TestCronMatch(t *testing.T) {
	at := func(h, m int) time.Time {
		return time.Date(2026, 7, 1, h, m, 0, 0, time.UTC) // a Wednesday
	}
	tol := 10 * time.Minute
	cases := []struct {
		expr string
		t    time.Time
		want bool
	}{
		{"0 2 * * *", at(2, 0), true},
		{"0 2 * * *", at(2, 8), true},   // within tolerance
		{"0 2 * * *", at(2, 30), false}, // outside tolerance
		{"0 2 * * *", at(14, 0), false},
		{"*/15 * * * *", at(9, 31), true}, // 9:30 within tolerance
		{"0 3 * * 0", at(3, 0), false},    // Sunday-only, today is Wednesday
		{"0 3 * * 3", at(3, 0), true},     // Wednesday
		{"30 1-5 * * *", at(4, 30), true},
		{"0 2 1 * *", at(2, 0), true},  // 1st of month
		{"0 2 15 * *", at(2, 0), false},
	}
	for _, c := range cases {
		if got := MatchesTime(c.expr, c.t, tol); got != c.want {
			t.Errorf("MatchesTime(%q, %s) = %v, want %v", c.expr, c.t, got, c.want)
		}
	}
}
