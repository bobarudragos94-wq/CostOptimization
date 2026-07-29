// Package spike implements host spike detection and capture.
//
// Detection combines, per rule (resource, optionally per device):
//
//   - a configurable static threshold;
//   - a sustained-duration requirement (a single high sample is not a spike);
//   - deviation from the host's own baseline: median + k·MAD over a trailing
//     window, with a floor so a quiet host doesn't alarm on trivial noise.
//
// The detector keeps a rolling pre-event ring buffer of high-resolution
// samples; events preserve capped pre/during/post series. Normal periods are
// left to the minute aggregation path — only events carry high-res data.
//
// The detector is deterministic and side-effect free: feed samples with
// Offer(), receive opened/completed events. Attribution (top processes,
// services, jobs, SQL) is attached by the agent when an event completes.
package spike

import (
	"crypto/rand"
	"encoding/hex"
	"math"
	"sort"
	"time"

	"github.com/bobarudragos94-wq/costoptimization/internal/config"
	"github.com/bobarudragos94-wq/costoptimization/internal/model"
)

// Key identifies a monitored series.
type Key struct {
	Resource string
	Device   string // empty for host-level series
}

// Point is one high-resolution observation of several series.
type Point struct {
	TS     time.Time
	Values map[Key]float64
}

const (
	maxPrePoints    = 120
	maxDuringPoints = 360
	maxPostPoints   = 60
	// closeBelow: the event closes after the value stays below threshold this long.
	closeBelow = 60 * time.Second
	// baselineWindow: how many recent samples feed the baseline estimate.
	baselineWindow = 5760 // 24h at 15s
	// baselineMinSamples before baseline triggering activates.
	baselineMinSamples = 240 // 1h at 15s
)

type ringBuf struct {
	pts  []model.MetricPoint
	cap_ int
}

func (r *ringBuf) push(p model.MetricPoint) {
	r.pts = append(r.pts, p)
	if len(r.pts) > r.cap_ {
		r.pts = r.pts[len(r.pts)-r.cap_:]
	}
}

type seriesState struct {
	rule     config.SpikeRule
	key      Key
	pre      ringBuf
	hist     []float64 // trailing values for baseline
	histPos  int
	histFull bool
	baseline float64
	mad      float64
	baseAge  int // samples since baseline recompute

	aboveSince   time.Time
	belowSince   time.Time
	cooldownTill time.Time

	open *openEvent
}

type openEvent struct {
	ev        *model.SpikeEvent
	inPost    bool
	postUntil time.Time
}

// Detector consumes Points and produces spike events.
type Detector struct {
	cfg    *config.Config
	series map[Key]*seriesState
	// OpenEvents lets the agent know a capture is in progress (to snapshot
	// processes at higher frequency).
	openCount int
}

func NewDetector(cfg *config.Config) *Detector {
	return &Detector{cfg: cfg, series: map[Key]*seriesState{}}
}

// CaptureActive reports whether any event is currently open (agent uses this
// to sample processes at spike resolution).
func (d *Detector) CaptureActive() bool { return d.openCount > 0 }

// Offer feeds one multi-series sample. Returns events that completed at this
// step (fully captured, ready for attribution and persistence).
func (d *Detector) Offer(p Point) []*model.SpikeEvent {
	var done []*model.SpikeEvent
	for key, val := range p.Values {
		rule, ok := d.ruleFor(key.Resource)
		if !ok {
			continue
		}
		st := d.series[key]
		if st == nil {
			preCap := int(d.cfg.Spikes.PreBuffer.Duration/d.cfg.Sampling.HostInterval.Duration) + 1
			if preCap < 4 {
				preCap = 4
			}
			st = &seriesState{rule: rule, key: key, pre: ringBuf{cap_: preCap},
				hist: make([]float64, 0, baselineWindow)}
			d.series[key] = st
		}
		if ev := d.step(st, p.TS, val); ev != nil {
			done = append(done, ev)
		}
	}
	return done
}

func (d *Detector) ruleFor(resource string) (config.SpikeRule, bool) {
	for _, r := range d.cfg.Spikes.Rules {
		if r.Resource == resource {
			return r, true
		}
	}
	return config.SpikeRule{}, false
}

func (d *Detector) step(st *seriesState, ts time.Time, val float64) *model.SpikeEvent {
	mp := model.MetricPoint{TS: ts.UTC(), Value: val}

	// Baseline bookkeeping (only from non-event samples so the spike itself
	// doesn't inflate its own baseline).
	if st.open == nil {
		if len(st.hist) < baselineWindow {
			st.hist = append(st.hist, val)
		} else {
			st.hist[st.histPos] = val
			st.histPos = (st.histPos + 1) % baselineWindow
			st.histFull = true
		}
		st.baseAge++
		if st.baseAge >= 20 || (st.baseline == 0 && len(st.hist) >= 4) {
			st.baseline, st.mad = medianMAD(st.hist)
			st.baseAge = 0
		}
	}

	threshold, trigger := d.effectiveThreshold(st)
	above := val >= threshold

	if st.open != nil {
		oe := st.open
		if !oe.inPost {
			oe.ev.DuringSamples = appendCapped(oe.ev.DuringSamples, mp, maxDuringPoints)
			if val > oe.ev.Peak {
				oe.ev.Peak = val
			}
			if above {
				st.belowSince = time.Time{}
			} else {
				if st.belowSince.IsZero() {
					st.belowSince = ts
				}
				if ts.Sub(st.belowSince) >= closeBelow {
					oe.ev.EndTS = st.belowSince.UTC()
					oe.ev.DurationS = oe.ev.EndTS.Sub(oe.ev.StartTS).Seconds()
					oe.inPost = true
					oe.postUntil = ts.Add(d.cfg.Spikes.PostCapture.Duration)
				}
			}
			return nil
		}
		// post-capture phase
		oe.ev.PostSamples = appendCapped(oe.ev.PostSamples, mp, maxPostPoints)
		if ts.After(oe.postUntil) {
			ev := oe.ev
			st.open = nil
			d.openCount--
			st.cooldownTill = ts.Add(d.cfg.Spikes.Cooldown.Duration)
			st.aboveSince = time.Time{}
			return ev
		}
		return nil
	}

	// No open event.
	st.pre.push(mp)
	if !above || ts.Before(st.cooldownTill) {
		st.aboveSince = time.Time{}
		return nil
	}
	if st.aboveSince.IsZero() {
		st.aboveSince = ts
	}
	if ts.Sub(st.aboveSince) < st.rule.Sustained.Duration {
		return nil
	}

	// Sustained: open an event. Pre-samples = ring content (they include the
	// climb since aboveSince).
	var id [8]byte
	rand.Read(id[:])
	ev := &model.SpikeEvent{
		Meta:      model.NewMeta(model.KindSpike, ts),
		EventID:   hex.EncodeToString(id[:]),
		Resource:  st.key.Resource,
		Device:    st.key.Device,
		StartTS:   st.aboveSince.UTC(),
		Baseline:  round2(st.baseline),
		Peak:      val,
		Threshold: round2(threshold),
		Trigger:   trigger,
		// Attribution defaults; the agent fills these in. Never a definite cause.
		ProbableCause:      "unattributed",
		ValidationRequired: true,
	}
	pre := make([]model.MetricPoint, len(st.pre.pts))
	copy(pre, st.pre.pts)
	ev.PreSamples = decimate(pre, maxPrePoints)
	st.open = &openEvent{ev: ev}
	d.openCount++
	st.belowSince = time.Time{}
	return nil
}

// effectiveThreshold returns the lower of the static threshold and the
// baseline-deviation trigger (when armed), plus which one is effective.
func (d *Detector) effectiveThreshold(st *seriesState) (float64, string) {
	static := st.rule.StaticThreshold
	th, trig := static, "static"
	if st.rule.BaselineK > 0 && len(st.hist) >= baselineMinSamples {
		bt := st.baseline + st.rule.BaselineK*madToSigma(st.mad)
		if bt < st.rule.BaselineFloor {
			bt = st.rule.BaselineFloor
		}
		if bt < th {
			th, trig = bt, "baseline_deviation"
		}
	}
	return th, trig
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func appendCapped(s []model.MetricPoint, p model.MetricPoint, cap_ int) []model.MetricPoint {
	if len(s) >= cap_ {
		// Keep first half history, decimate by dropping every other trailing
		// point: bounded memory with preserved shape.
		s = append(s[:0], decimate(s, cap_/2)...)
	}
	return append(s, p)
}

func decimate(s []model.MetricPoint, target int) []model.MetricPoint {
	if len(s) <= target || target <= 0 {
		return s
	}
	out := make([]model.MetricPoint, 0, target)
	step := float64(len(s)) / float64(target)
	for i := 0; i < target; i++ {
		out = append(out, s[int(float64(i)*step)])
	}
	return out
}

func medianMAD(vals []float64) (float64, float64) {
	if len(vals) == 0 {
		return 0, 0
	}
	tmp := make([]float64, len(vals))
	copy(tmp, vals)
	sort.Float64s(tmp)
	med := quantileSorted(tmp, 0.5)
	dev := make([]float64, len(tmp))
	for i, v := range tmp {
		dev[i] = math.Abs(v - med)
	}
	sort.Float64s(dev)
	return med, quantileSorted(dev, 0.5)
}

// madToSigma converts MAD to a robust sigma estimate (normal consistency).
func madToSigma(mad float64) float64 {
	s := 1.4826 * mad
	if s < 1 { // avoid a zero band on perfectly flat series
		s = 1
	}
	return s
}

func quantileSorted(sorted []float64, q float64) float64 {
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

func round2(v float64) float64 { return math.Round(v*100) / 100 }
