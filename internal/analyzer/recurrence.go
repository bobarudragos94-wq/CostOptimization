package analyzer

import (
	"fmt"
	"sort"
	"time"

	"github.com/bobarudragos94-wq/costoptimization/internal/model"
)

// Pattern describes a repetitive spike pattern on one host.
type Pattern struct {
	Resource     string   `json:"resource"`
	Device       string   `json:"device,omitempty"`
	TimeOfDayUTC string   `json:"time_of_day_utc"` // e.g. "02:00"
	Occurrences  int      `json:"occurrences"`
	DistinctDays int      `json:"distinct_days"`
	ObservedDays int      `json:"observed_days"`
	MeanDurationS float64 `json:"mean_duration_s"`
	MeanPeak     float64  `json:"mean_peak"`
	Cadence      string   `json:"cadence"` // daily | weekdays | weekly | irregular-recurring
	EventIDs     []string `json:"event_ids"`
	Description  string   `json:"description"`
}

const recurrenceTolMin = 20 // cluster tolerance around time-of-day, minutes

// DetectPatterns clusters spike events by (resource, device, time-of-day)
// across days. A pattern needs >= 3 occurrences on >= 3 distinct days.
func DetectPatterns(spikes []model.SpikeEvent, observedDays int) []Pattern {
	type key struct{ res, dev string }
	groups := map[key][]model.SpikeEvent{}
	for _, s := range spikes {
		k := key{s.Resource, s.Device}
		groups[k] = append(groups[k], s)
	}
	var out []Pattern
	for k, evs := range groups {
		sort.Slice(evs, func(i, j int) bool { return evs[i].StartTS.Before(evs[j].StartTS) })
		used := make([]bool, len(evs))
		for i := range evs {
			if used[i] {
				continue
			}
			cluster := []model.SpikeEvent{evs[i]}
			used[i] = true
			base := minuteOfDay(evs[i].StartTS)
			for j := i + 1; j < len(evs); j++ {
				if used[j] {
					continue
				}
				if minuteDist(base, minuteOfDay(evs[j].StartTS)) <= recurrenceTolMin {
					cluster = append(cluster, evs[j])
					used[j] = true
				}
			}
			days := map[string]bool{}
			weekdaysOnly := true
			var sumDur, sumPeak float64
			var sumMin int
			var ids []string
			for _, e := range cluster {
				days[e.StartTS.UTC().Format("20060102")] = true
				wd := e.StartTS.UTC().Weekday()
				if wd == time.Saturday || wd == time.Sunday {
					weekdaysOnly = false
				}
				sumDur += e.DurationS
				sumPeak += e.Peak
				sumMin += minuteOfDay(e.StartTS)
				ids = append(ids, e.EventID)
			}
			if len(cluster) < 3 || len(days) < 3 {
				continue
			}
			meanMin := sumMin / len(cluster)
			p := Pattern{
				Resource: k.res, Device: k.dev,
				TimeOfDayUTC:  fmt.Sprintf("%02d:%02d", meanMin/60, meanMin%60),
				Occurrences:   len(cluster),
				DistinctDays:  len(days),
				ObservedDays:  observedDays,
				MeanDurationS: sumDur / float64(len(cluster)),
				MeanPeak:      sumPeak / float64(len(cluster)),
				EventIDs:      ids,
			}
			switch {
			case observedDays > 0 && float64(len(days))/float64(observedDays) >= 0.8:
				p.Cadence = "daily"
			case weekdaysOnly && observedDays >= 7:
				p.Cadence = "weekdays"
			case observedDays >= 14 && len(days) >= observedDays/7:
				p.Cadence = "weekly"
			default:
				p.Cadence = "irregular-recurring"
			}
			p.Description = fmt.Sprintf("%s spike %s around %s UTC (%d occurrences on %d of %d observed days, mean peak %.0f, mean duration %s)",
				k.res, p.Cadence, p.TimeOfDayUTC, p.Occurrences, p.DistinctDays, observedDays,
				p.MeanPeak, (time.Duration(p.MeanDurationS) * time.Second).Round(time.Second))
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Occurrences > out[j].Occurrences })
	return out
}

// PatternFor finds the pattern containing an event ID, if any.
func PatternFor(patterns []Pattern, eventID string) *Pattern {
	for i := range patterns {
		for _, id := range patterns[i].EventIDs {
			if id == eventID {
				return &patterns[i]
			}
		}
	}
	return nil
}

func minuteOfDay(t time.Time) int {
	u := t.UTC()
	return u.Hour()*60 + u.Minute()
}

func minuteDist(a, b int) int {
	d := a - b
	if d < 0 {
		d = -d
	}
	if d > 720 {
		d = 1440 - d
	}
	return d
}
