package analyzer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"filippo.io/age"

	"github.com/bobarudragos94-wq/costoptimization/internal/bundle"
	"github.com/bobarudragos94-wq/costoptimization/internal/model"
)

// HostData is everything imported for one host, normalized across OSes.
type HostData struct {
	HostID    string
	Inventory *model.Inventory // latest
	Minutes   []model.HostMinute
	Spikes    []model.SpikeEvent
	ProcTops  []model.ProcTop
	Sched     []model.SchedSnapshot
	SQLInv    map[string]*model.SQLInstanceInventory // latest per instance key
	SQLSamples map[string][]model.SQLSample          // per instance key
	SQLSpikes []model.SQLSpikeContext
	Health    []model.Health
	Manifests []*model.Manifest
	Problems  []string // import-level issues for data-quality reporting
}

// Dataset is the consolidated fleet.
type Dataset struct {
	Hosts    map[string]*HostData
	Warnings []string
	Bundles, OKBundles, PartialBundles, RejectedBundles int
}

// LoadBundles imports every *.urab under dir (recursively) and normalizes
// records into per-host datasets. Damaged segments/bundles are recorded, not
// fatal.
func LoadBundles(dir string, identities []age.Identity) (*Dataset, error) {
	var paths []string
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(p, ".urab") {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no .urab bundles found under %s", dir)
	}
	sort.Strings(paths)

	ds := &Dataset{Hosts: map[string]*HostData{}}
	for _, p := range paths {
		ds.Bundles++
		res := bundle.Import(p, identities)
		if res.Fatal != "" {
			ds.RejectedBundles++
			ds.Warnings = append(ds.Warnings, fmt.Sprintf("bundle %s rejected: %s", filepath.Base(p), res.Fatal))
			continue
		}
		hostID := res.Manifest.HostID
		h := ds.Hosts[hostID]
		if h == nil {
			h = &HostData{HostID: hostID,
				SQLInv:     map[string]*model.SQLInstanceInventory{},
				SQLSamples: map[string][]model.SQLSample{}}
			ds.Hosts[hostID] = h
		}
		h.Manifests = append(h.Manifests, res.Manifest)
		partial := false
		for _, seg := range res.Segments {
			switch seg.Status {
			case "ok":
			case "truncated_records":
				partial = true
				h.Problems = append(h.Problems, fmt.Sprintf("segment %s: %s", seg.Meta.Name, seg.Err))
			default:
				partial = true
				h.Problems = append(h.Problems,
					fmt.Sprintf("segment %s %s: %s (records lost: ~%d)", seg.Meta.Name, seg.Status, seg.Err, seg.Meta.Records))
				continue
			}
			for _, raw := range seg.Records {
				decodeInto(h, raw)
			}
		}
		if partial {
			ds.PartialBundles++
			ds.Warnings = append(ds.Warnings, fmt.Sprintf("bundle %s imported with damaged segments (host %s)", filepath.Base(p), hostID))
		} else {
			ds.OKBundles++
		}
	}

	for _, h := range ds.Hosts {
		normalize(h)
	}
	return ds, nil
}

func decodeInto(h *HostData, raw json.RawMessage) {
	var meta model.Meta
	if json.Unmarshal(raw, &meta) != nil {
		return
	}
	switch meta.Kind {
	case model.KindInventory:
		var v model.Inventory
		if json.Unmarshal(raw, &v) == nil {
			if h.Inventory == nil || v.TS.After(h.Inventory.TS) {
				h.Inventory = &v
			}
		}
	case model.KindHostMinute:
		var v model.HostMinute
		if json.Unmarshal(raw, &v) == nil {
			h.Minutes = append(h.Minutes, v)
		}
	case model.KindSpike:
		var v model.SpikeEvent
		if json.Unmarshal(raw, &v) == nil {
			h.Spikes = append(h.Spikes, v)
		}
	case model.KindProcTop:
		var v model.ProcTop
		if json.Unmarshal(raw, &v) == nil {
			h.ProcTops = append(h.ProcTops, v)
		}
	case model.KindSched:
		var v model.SchedSnapshot
		if json.Unmarshal(raw, &v) == nil {
			h.Sched = append(h.Sched, v)
		}
	case model.KindSQLInventory:
		var v model.SQLInstanceInventory
		if json.Unmarshal(raw, &v) == nil {
			key := v.InstanceKey
			if cur, ok := h.SQLInv[key]; !ok || v.TS.After(cur.TS) {
				h.SQLInv[key] = &v
			}
		}
	case model.KindSQLSample:
		var v model.SQLSample
		if json.Unmarshal(raw, &v) == nil {
			h.SQLSamples[v.InstanceKey] = append(h.SQLSamples[v.InstanceKey], v)
		}
	case model.KindSQLSpike:
		var v model.SQLSpikeContext
		if json.Unmarshal(raw, &v) == nil {
			h.SQLSpikes = append(h.SQLSpikes, v)
		}
	case model.KindHealth:
		var v model.Health
		if json.Unmarshal(raw, &v) == nil {
			h.Health = append(h.Health, v)
		}
	}
}

// normalize sorts time series and deduplicates minutes that appear in
// overlapping daily exports (same host, same minute).
func normalize(h *HostData) {
	sort.Slice(h.Minutes, func(i, j int) bool { return h.Minutes[i].TS.Before(h.Minutes[j].TS) })
	dedup := h.Minutes[:0]
	for i, m := range h.Minutes {
		if i > 0 && m.TS.Equal(h.Minutes[i-1].TS) {
			continue
		}
		dedup = append(dedup, m)
	}
	h.Minutes = dedup
	sort.Slice(h.Spikes, func(i, j int) bool { return h.Spikes[i].StartTS.Before(h.Spikes[j].StartTS) })
	seen := map[string]bool{}
	sp := h.Spikes[:0]
	for _, s := range h.Spikes {
		if s.EventID != "" && seen[s.EventID] {
			continue
		}
		seen[s.EventID] = true
		sp = append(sp, s)
	}
	h.Spikes = sp
	for k := range h.SQLSamples {
		ss := h.SQLSamples[k]
		sort.Slice(ss, func(i, j int) bool { return ss[i].TS.Before(ss[j].TS) })
		h.SQLSamples[k] = ss
	}
	sort.Slice(h.Health, func(i, j int) bool { return h.Health[i].TS.Before(h.Health[j].TS) })
}
