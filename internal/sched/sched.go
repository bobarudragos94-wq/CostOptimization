// Package sched snapshots the host's scheduled work (cron, systemd timers,
// Windows Task Scheduler) so spikes can be correlated with jobs. Read-only:
// file parsing plus read-only query commands; nothing is ever modified.
package sched

import (
	"strconv"
	"strings"
	"time"

	"github.com/bobarudragos94-wq/costoptimization/internal/model"
)

// Snapshot collects the platform's scheduled jobs.
func Snapshot(issues func(collector, err string)) model.SchedSnapshot {
	if issues == nil {
		issues = func(string, string) {}
	}
	snap := model.SchedSnapshot{Meta: model.NewMeta(model.KindSched, time.Now())}
	snap.Entries = platformEntries(issues)
	return snap
}

// TooFrequentForCorrelation reports whether a cron schedule fires so often
// (more than hourly) that a time match carries no attribution value: a
// "*/10 * * * *" entry matches EVERY event within any reasonable tolerance
// and must never count as evidence — doing so inflates confidence on every
// spike (observed in live smoke testing with Debian's stock php sessionclean
// cron entry).
func TooFrequentForCorrelation(cronExpr string) bool {
	fields := strings.Fields(cronExpr)
	if len(fields) < 5 {
		return true
	}
	// Count firings across one representative day at minute resolution.
	day := time.Date(2026, 1, 7, 0, 0, 0, 0, time.UTC) // a Wednesday, day 7
	fires := 0
	for m := 0; m < 24*60; m++ {
		tt := day.Add(time.Duration(m) * time.Minute)
		if cronFieldMatch(fields[0], tt.Minute(), 0, 59) &&
			cronFieldMatch(fields[1], tt.Hour(), 0, 23) &&
			cronFieldMatch(fields[2], tt.Day(), 1, 31) &&
			cronFieldMatch(fields[3], int(tt.Month()), 1, 12) &&
			cronFieldMatch(fields[4], int(tt.Weekday()), 0, 6) {
			fires++
			if fires > 24 {
				return true
			}
		}
	}
	return false
}

// MatchesTime reports whether a cron expression fires within ±tolerance of t
// (minute resolution). Supports the standard 5-field syntax with *, lists,
// ranges and steps — enough for correlation, not a full cron engine.
func MatchesTime(cronExpr string, t time.Time, tolerance time.Duration) bool {
	fields := strings.Fields(cronExpr)
	if len(fields) < 5 {
		return false
	}
	for off := -tolerance; off <= tolerance; off += time.Minute {
		tt := t.Add(off)
		if cronFieldMatch(fields[0], tt.Minute(), 0, 59) &&
			cronFieldMatch(fields[1], tt.Hour(), 0, 23) &&
			cronFieldMatch(fields[2], tt.Day(), 1, 31) &&
			cronFieldMatch(fields[3], int(tt.Month()), 1, 12) &&
			cronFieldMatch(fields[4], int(tt.Weekday()), 0, 6) {
			return true
		}
	}
	return false
}

func cronFieldMatch(field string, v, min, max int) bool {
	for _, part := range strings.Split(field, ",") {
		expr, step := part, 1
		if i := strings.IndexByte(part, '/'); i >= 0 {
			expr = part[:i]
			if s, err := strconv.Atoi(part[i+1:]); err == nil && s > 0 {
				step = s
			}
		}
		lo, hi := min, max
		switch {
		case expr == "*" || expr == "":
		case strings.Contains(expr, "-"):
			bounds := strings.SplitN(expr, "-", 2)
			a, err1 := strconv.Atoi(bounds[0])
			b, err2 := strconv.Atoi(bounds[1])
			if err1 != nil || err2 != nil {
				continue
			}
			lo, hi = a, b
		default:
			n, err := strconv.Atoi(expr)
			if err != nil {
				continue
			}
			lo, hi = n, n
		}
		if v >= lo && v <= hi && (v-lo)%step == 0 {
			return true
		}
	}
	return false
}
