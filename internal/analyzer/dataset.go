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

	// Derived from health records + manifests by finalize (analyzer safety):
	DegradedCollectors map[string]string // collector -> status (degraded/unavailable only)
	PermissionIssues   []string
	DroppedSamples     uint64
	// ManifestCoverage is expected vs collected minutes as reported by the
	// agent itself (persisted across restarts) — the authoritative coverage.
	ManifestExpectedMin  int
	ManifestCollectedMin int
}

// newHostData allocates an empty per-host dataset.
func newHostData(hostID string) *HostData {
	return &HostData{
		HostID:             hostID,
		SQLInv:             map[string]*model.SQLInstanceInventory{},
		SQLSamples:         map[string][]model.SQLSample{},
		DegradedCollectors: map[string]string{},
	}
}

// schemaCompatible rejects records from a different major schema version.
func schemaCompatible(v string) error {
	if v == "" {
		return fmt.Errorf("missing schema version")
	}
	if major(v) != major(model.SchemaVersion) {
		return fmt.Errorf("schema major version %s is incompatible with analyzer %s", v, model.SchemaVersion)
	}
	return nil
}

func major(v string) string {
	if i := strings.IndexByte(v, '.'); i > 0 {
		return v[:i]
	}
	return v
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
	seenBundles := map[string]string{}
	for _, p := range paths {
		ds.Bundles++
		res := bundle.Import(p, identities)
		if res.Fatal != "" {
			ds.RejectedBundles++
			ds.Warnings = append(ds.Warnings, fmt.Sprintf("bundle %s rejected: %s", filepath.Base(p), res.Fatal))
			continue
		}
		if err := schemaCompatible(res.Manifest.SchemaVersion); err != nil {
			ds.RejectedBundles++
			ds.Warnings = append(ds.Warnings, fmt.Sprintf("bundle %s rejected: %v", filepath.Base(p), err))
			continue
		}
		if prev, dup := seenBundles[res.Manifest.BundleID]; dup {
			ds.Warnings = append(ds.Warnings, fmt.Sprintf("bundle %s is a duplicate of %s (bundle id %s); skipped",
				filepath.Base(p), prev, res.Manifest.BundleID))
			continue
		}
		seenBundles[res.Manifest.BundleID] = filepath.Base(p)
		hostID := res.Manifest.HostID
		h := ds.Hosts[hostID]
		if h == nil {
			h = newHostData(hostID)
			ds.Hosts[hostID] = h
		}
		partial := mergeImport(h, res)
		if partial {
			ds.PartialBundles++
			ds.Warnings = append(ds.Warnings, fmt.Sprintf("bundle %s imported with damaged segments (host %s)", filepath.Base(p), hostID))
		} else {
			ds.OKBundles++
		}
	}

	for _, h := range ds.Hosts {
		finalizeHost(h)
	}
	return ds, nil
}

// mergeImport folds one imported bundle into a host dataset; returns whether
// the bundle was partial (damaged segments).
func mergeImport(h *HostData, res *bundle.ImportResult) (partial bool) {
	h.Manifests = append(h.Manifests, res.Manifest)
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
	return partial
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

// finalizeHost normalizes series, extracts collection-health facts and
// validates cross-bundle consistency (hostname, host ID, config hash).
func finalizeHost(h *HostData) {
	normalize(h)

	// Health-derived facts: statuses from the LATEST health record (current
	// collector state), permission issues and dropped counts across all.
	permSeen := map[string]bool{}
	for _, hr := range h.Health {
		for _, p := range hr.PermissionIssues {
			permSeen[p] = true
		}
		// SamplesDropped is cumulative per agent lifetime; max is a safe
		// lower bound across restarts.
		if hr.SamplesDropped > h.DroppedSamples {
			h.DroppedSamples = hr.SamplesDropped
		}
	}
	if len(h.Health) > 0 {
		latest := h.Health[len(h.Health)-1]
		for coll, status := range latest.CollectorStatus {
			if strings.HasPrefix(status, "degraded") || strings.HasPrefix(status, "unavailable") {
				h.DegradedCollectors[coll] = status
			}
		}
	}
	for p := range permSeen {
		h.PermissionIssues = append(h.PermissionIssues, p)
	}
	sort.Strings(h.PermissionIssues)

	// Manifest facts: agent-reported coverage (latest manifest wins — it
	// carries the persisted whole-period counters) + consistency checks.
	var hostnames, hashes []string
	for _, m := range h.Manifests {
		if m.Health.ExpectedMinutes >= h.ManifestExpectedMin {
			h.ManifestExpectedMin = m.Health.ExpectedMinutes
			h.ManifestCollectedMin = m.Health.CollectedMinutes
		}
		for _, p := range m.Health.PermissionIssues {
			if !permSeen[p] {
				permSeen[p] = true
				h.PermissionIssues = append(h.PermissionIssues, p)
			}
		}
		hostnames = appendUnique(hostnames, m.Hostname)
		if m.ConfigHash != "" {
			hashes = appendUnique(hashes, m.ConfigHash)
		}
		if m.HostID != h.HostID {
			h.Problems = append(h.Problems, fmt.Sprintf(
				"manifest host_id %s does not match dataset host %s (bundle %s)", m.HostID, h.HostID, m.BundleID))
		}
	}
	if len(hostnames) > 1 {
		h.Problems = append(h.Problems, fmt.Sprintf(
			"hostname changed across bundles of host %s: %v — verify these are the same machine", h.HostID, hostnames))
	}
	if len(hashes) > 1 {
		h.Problems = append(h.Problems, fmt.Sprintf(
			"collection configuration changed mid-window (%d distinct config hashes) — thresholds/intervals differ across the period", len(hashes)))
	}
	if h.Inventory != nil {
		if h.Inventory.HostID != h.HostID {
			h.Problems = append(h.Problems, fmt.Sprintf(
				"inventory host_id %s does not match manifest host_id %s (possible bundle substitution)", h.Inventory.HostID, h.HostID))
		}
		if len(hostnames) == 1 && hostnames[0] != "" && h.Inventory.Hostname != hostnames[0] {
			h.Problems = append(h.Problems, fmt.Sprintf(
				"inventory hostname %q differs from manifest hostname %q", h.Inventory.Hostname, hostnames[0]))
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
