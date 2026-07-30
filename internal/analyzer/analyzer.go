// Package analyzer imports encrypted bundles and produces the consolidated
// rightsizing reports. Fully offline: file I/O only.
package analyzer

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"filippo.io/age"

	"github.com/bobarudragos94-wq/costoptimization/internal/bundle"
	"github.com/bobarudragos94-wq/costoptimization/internal/model"
)

// Report is the complete machine-readable output.
type Report struct {
	GeneratedAt   time.Time  `json:"generated_at"`
	SchemaVersion string     `json:"schema_version"`
	ToolVersion   string     `json:"tool_version"`
	Fleet         FleetSummary `json:"fleet_summary"`
	Hosts         []HostReport `json:"hosts"`
	SQLInstances  []SQLReport  `json:"sql_instances"`
	HAGroups      []HAGroup    `json:"ha_groups,omitempty"`
	Spikes        []SpikeReportEntry `json:"spikes"`
	DataQuality   []string     `json:"data_quality,omitempty"`
}

type FleetSummary struct {
	Hosts             int            `json:"hosts"`
	SQLInstances      int            `json:"sql_instances"`
	TotalAllocVCPU    int            `json:"total_allocated_vcpu"`
	TotalAllocRAMGB   float64        `json:"total_allocated_ram_gb"`
	SpikeEvents       int            `json:"spike_events"`
	Categories        map[string]int `json:"recommendation_categories"`
	PotentialVCPU     int            `json:"indicative_vcpu_reduction_upper_bound"`
	PotentialRAMGB    float64        `json:"indicative_ram_reduction_upper_bound_gb"`
}

// HAGroup groups replicas that must be evaluated together.
type HAGroup struct {
	AGName   string   `json:"ag_name"`
	Members  []string `json:"members"` // instance keys
	Replicas []string `json:"replica_server_names,omitempty"`
	Note     string   `json:"note"`
}

// RunResult summarizes an analyze invocation for the CLI.
type RunResult struct {
	Bundles, OKBundles, PartialBundles, RejectedBundles int
	Hosts, SQLInstances, Spikes                         int
	Warnings                                            []string
}

// Run imports every bundle under inDir and writes report.json/.md/.html to
// outDir. Hosts are processed SEQUENTIALLY: bundle manifests are read first
// (streaming, without buffering segments) to group bundles per host and
// reject duplicates/incompatible schemas, then each host's bundles are
// imported, analyzed and released before the next host — peak memory is one
// host's dataset, not the fleet's.
func Run(inDir string, identities []age.Identity, outDir string) (*RunResult, error) {
	var paths []string
	err := filepath.Walk(inDir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(p, ".urab") {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no .urab bundles found under %s", inDir)
	}
	sort.Strings(paths)

	res := &RunResult{}
	b := newReportBuilder()

	// Pass 1: manifests only — group by host, validate, dedupe.
	hostPaths := map[string][]string{}
	seenBundles := map[string]string{}
	var hostOrder []string
	for _, p := range paths {
		res.Bundles++
		mf, err := bundle.ReadManifestOnly(p, identities)
		if err != nil {
			res.RejectedBundles++
			res.Warnings = append(res.Warnings, fmt.Sprintf("bundle %s rejected: %v", filepath.Base(p), err))
			continue
		}
		if err := schemaCompatible(mf.SchemaVersion); err != nil {
			res.RejectedBundles++
			res.Warnings = append(res.Warnings, fmt.Sprintf("bundle %s rejected: %v", filepath.Base(p), err))
			continue
		}
		if prev, dup := seenBundles[mf.BundleID]; dup {
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"bundle %s is a duplicate of %s (bundle id %s); skipped", filepath.Base(p), prev, mf.BundleID))
			continue
		}
		seenBundles[mf.BundleID] = filepath.Base(p)
		if _, ok := hostPaths[mf.HostID]; !ok {
			hostOrder = append(hostOrder, mf.HostID)
		}
		hostPaths[mf.HostID] = append(hostPaths[mf.HostID], p)
	}
	sort.Strings(hostOrder)

	// Pass 2: one host at a time.
	for _, hostID := range hostOrder {
		h := newHostData(hostID)
		for _, p := range hostPaths[hostID] {
			ir := bundle.Import(p, identities)
			if ir.Fatal != "" {
				res.RejectedBundles++
				res.Warnings = append(res.Warnings, fmt.Sprintf("bundle %s rejected: %s", filepath.Base(p), ir.Fatal))
				continue
			}
			if mergeImport(h, ir) {
				res.PartialBundles++
				res.Warnings = append(res.Warnings, fmt.Sprintf("bundle %s imported with damaged segments (host %s)", filepath.Base(p), hostID))
			} else {
				res.OKBundles++
			}
		}
		finalizeHost(h)
		b.AddHost(h)
		// h goes out of scope here; the builder retains only report rows.
	}
	rep := b.Finish()
	rep.DataQuality = append(res.Warnings, rep.DataQuality...)

	if err := writeReports(rep, outDir); err != nil {
		return nil, err
	}
	res.Hosts = len(rep.Hosts)
	res.SQLInstances = len(rep.SQLInstances)
	res.Spikes = len(rep.Spikes)
	return res, nil
}

func writeReports(rep *Report, outDir string) error {
	if err := os.MkdirAll(outDir, 0o750); err != nil {
		return err
	}
	jb, _ := json.MarshalIndent(rep, "", "  ")
	if err := os.WriteFile(filepath.Join(outDir, "report.json"), jb, 0o640); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "report.md"), []byte(RenderMarkdown(rep)), 0o640); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outDir, "report.html"), []byte(RenderHTML(rep)), 0o640)
}

// Build computes the full report from a fully-loaded dataset (test and
// programmatic path; the CLI uses the streaming Run).
func Build(ds *Dataset) *Report {
	b := newReportBuilder()
	var hostIDs []string
	for id := range ds.Hosts {
		hostIDs = append(hostIDs, id)
	}
	sort.Strings(hostIDs)
	for _, id := range hostIDs {
		b.AddHost(ds.Hosts[id])
	}
	rep := b.Finish()
	rep.DataQuality = append(append([]string{}, ds.Warnings...), rep.DataQuality...)
	return rep
}

// reportBuilder accumulates per-host results without retaining host datasets.
type reportBuilder struct {
	rep *Report
	ags map[string]*HAGroup
}

func newReportBuilder() *reportBuilder {
	return &reportBuilder{
		rep: &Report{
			GeneratedAt:   time.Now().UTC(),
			SchemaVersion: model.SchemaVersion,
			ToolVersion:   model.AgentVersion,
			Fleet:         FleetSummary{Categories: map[string]int{}},
		},
		ags: map[string]*HAGroup{},
	}
}

func (b *reportBuilder) AddHost(h *HostData) {
	rep := b.rep
	hr := analyzeHost(h)

	var sqlReports []SQLReport
	var sqlKeys []string
	for key := range h.SQLInv {
		sqlKeys = append(sqlKeys, key)
	}
	sort.Strings(sqlKeys)
	for _, key := range sqlKeys {
		sqlReports = append(sqlReports, analyzeSQLInstance(h, h.SQLInv[key], &hr))
	}

	// A host recommendation must never contradict its SQL layer.
	reconcileHostWithSQL(&hr, sqlReports)

	rep.Hosts = append(rep.Hosts, hr)
	rep.SQLInstances = append(rep.SQLInstances, sqlReports...)
	rep.Fleet.Hosts++
	rep.Fleet.SQLInstances += len(sqlReports)
	rep.Fleet.TotalAllocVCPU += hr.AllocVCPU
	rep.Fleet.TotalAllocRAMGB += hr.AllocRAMGB
	rep.Fleet.SpikeEvents += hr.SpikeCount
	rep.Fleet.Categories[hr.Category]++
	if hr.Suggested != nil {
		if hr.Suggested.MinVCPU > 0 && hr.Suggested.MinVCPU < hr.AllocVCPU {
			rep.Fleet.PotentialVCPU += hr.AllocVCPU - hr.Suggested.MaxVCPU
		}
		if hr.Suggested.MinRAMGB > 0 && hr.Suggested.MaxRAMGB < hr.AllocRAMGB {
			rep.Fleet.PotentialRAMGB += hr.AllocRAMGB - hr.Suggested.MaxRAMGB
		}
	}

	patterns := DetectPatterns(h.Spikes, hr.ObservedDays)
	for _, s := range h.Spikes {
		rep.Spikes = append(rep.Spikes, spikeEntry(h, &hr, s, patterns))
	}
	for _, p := range h.Problems {
		rep.DataQuality = append(rep.DataQuality, fmt.Sprintf("%s: %s", hr.Hostname, p))
	}
	if hr.CoveragePct < 90 && len(h.Minutes) > 0 {
		rep.DataQuality = append(rep.DataQuality,
			fmt.Sprintf("%s: collection coverage only %.0f%% — gaps reduce percentile reliability", hr.Hostname, hr.CoveragePct))
	}
	for _, deg := range criticalDegradations(&hr) {
		rep.DataQuality = append(rep.DataQuality, fmt.Sprintf("%s: %s", hr.Hostname, deg))
	}

	// HA-group accumulation (cross-host, lightweight).
	for key, inv := range h.SQLInv {
		for _, ag := range inv.HA.AGs {
			g := b.ags[ag.AGName]
			if g == nil {
				g = &HAGroup{AGName: ag.AGName,
					Note: "Replicas of one availability group: evaluate sizing together; a secondary must absorb the primary's workload after failover. Topology-level validation required before any resize."}
				b.ags[ag.AGName] = g
			}
			g.Members = append(g.Members, key+" ("+ag.Role+")")
			for _, repName := range ag.PartnerReplicas {
				g.Replicas = appendUnique(g.Replicas, repName)
			}
		}
	}
}

func (b *reportBuilder) Finish() *Report {
	for _, g := range b.ags {
		sort.Strings(g.Members)
		b.rep.HAGroups = append(b.rep.HAGroups, *g)
	}
	sort.Slice(b.rep.HAGroups, func(i, j int) bool { return b.rep.HAGroups[i].AGName < b.rep.HAGroups[j].AGName })
	sort.Slice(b.rep.Spikes, func(i, j int) bool { return b.rep.Spikes[i].StartTS.Before(b.rep.Spikes[j].StartTS) })
	return b.rep
}

// reconcileHostWithSQL downgrades a host-level rightsizing candidate when its
// SQL layer carries a blocking constraint: recommending a host resize while
// the SQL memory configuration is broken, instances overcommit the host, the
// instance participates in HA/FCI, or SQL telemetry is missing would be
// contradictory and unsafe.
func reconcileHostWithSQL(hr *HostReport, sqls []SQLReport) {
	if hr.Category != CatLikelyRightsizing && hr.Category != CatPossibleRightsizing {
		return
	}
	blockOrder := []string{CatHADRConstraint, CatMultiInstanceRisk, CatSQLMemoryConfig, CatInsufficientHeadroom, CatInsufficientData}
	for _, blocking := range blockOrder {
		for _, s := range sqls {
			if s.Category != blocking {
				continue
			}
			hr.Category = blocking
			hr.Suggested = nil
			hr.Confidence = math.Min(hr.Confidence, s.Confidence)
			hr.Recommendation = fmt.Sprintf(
				"Host utilization alone would make this a rightsizing candidate, but SQL instance %s is blocking: %s Resolve the SQL-level finding first, then re-evaluate the host.",
				s.InstanceName, s.Recommendation)
			hr.Rationale = append(hr.Rationale,
				fmt.Sprintf("blocked by SQL instance %s: %s", s.InstanceKey, s.Category))
			return
		}
	}
	// SQL present but only process-level visibility: the host numbers are
	// fine, yet a resize would be blind to SQL configuration — cap at
	// monitor_for_longer.
	for _, s := range sqls {
		if s.CollectionLevel == model.SQLLevelProcessOnly {
			hr.Category = CatMonitorLonger
			hr.Suggested = nil
			hr.Recommendation = "Host utilization is low, but SQL Server is present without deep telemetry (permissions missing). Grant the least-privilege SQL login and monitor again before rightsizing — SQL memory configuration cannot be assessed blind."
			hr.Rationale = append(hr.Rationale,
				fmt.Sprintf("SQL instance %s: %s", s.InstanceKey, "SQL Server detected; deep SQL telemetry unavailable."))
			return
		}
	}
}

func spikeEntry(h *HostData, hr *HostReport, s model.SpikeEvent, patterns []Pattern) SpikeReportEntry {
	e := SpikeReportEntry{
		HostID: h.HostID, Hostname: hr.Hostname,
		EventID: s.EventID, StartTS: s.StartTS, DurationS: s.DurationS,
		Resource: s.Resource, Device: s.Device,
		Baseline: s.Baseline, Peak: s.Peak,
		Process:  s.CorrelatedProcess, ServiceOrJob: s.CorrelatedServiceOrJob,
		SQLInstance: s.SQLInstance,
		ProbableCause: s.ProbableCause, Confidence: s.AttributionConfidence,
		Evidence: s.AttributionEvidence,
	}
	if p := PatternFor(patterns, s.EventID); p != nil {
		e.Recurrence = p.Description
	}
	for _, sc := range h.SQLSpikes {
		if sc.EventID != s.EventID {
			continue
		}
		switch sc.CorrelationLevel {
		case model.CorrAgentJob:
			for _, j := range sc.AgentJobs {
				if j.Running {
					e.SQLEvidence = "SQL Agent job running: " + j.JobName
				}
			}
		case model.CorrActiveRequest:
			if len(sc.ActiveRequests) > 0 {
				ar := sc.ActiveRequests[0]
				e.SQLEvidence = fmt.Sprintf("active request: db=%s cmd=%s cpu=%dms query_hash=%s",
					ar.Database, ar.Command, ar.CPUTimeMS, ar.QueryHash)
			}
		}
	}
	e.NextValidationStep = validationStep(&e, s)
	if s.Resource == model.ResCPU && len(s.CoreSecondsByProc) > 0 && hr.AllocVCPU > 0 {
		e.VCPUImpactNote = vcpuImpact(s, hr.AllocVCPU)
	}
	return e
}

// validationStep proposes the concrete next check for an operator.
func validationStep(e *SpikeReportEntry, s model.SpikeEvent) string {
	switch {
	case e.SQLEvidence != "" && e.Recurrence != "":
		return "Confirm the SQL Agent job schedule with the DBA and whether its window can move or its plan be tuned."
	case e.SQLEvidence != "":
		return "Review the captured query/plan hash with the DBA to identify the statement."
	case e.Recurrence != "" && e.ServiceOrJob != "":
		return fmt.Sprintf("Confirm with the owner of %s that this scheduled run is expected and sized correctly.", e.ServiceOrJob)
	case e.Recurrence != "":
		return "Identify which scheduled task fires at this time (pattern is recurring but no job matched); check schedules around the event time."
	case e.Process != "":
		return fmt.Sprintf("Ask the application owner of %s whether this burst is expected at this time.", e.Process)
	default:
		return "Insufficient attribution evidence — extend monitoring or raise process sampling frequency around this time window."
	}
}

// vcpuImpact estimates batch-duration sensitivity to a vCPU reduction from
// captured core-seconds: work is fixed, so duration scales with available
// parallel capacity (upper-bound estimate for CPU-bound jobs).
func vcpuImpact(s model.SpikeEvent, alloc int) string {
	var total float64
	for _, cs := range s.CoreSecondsByProc {
		total += cs
	}
	if total <= 0 || s.DurationS <= 0 {
		return ""
	}
	usedCores := total / s.DurationS
	newAlloc := alloc / 2
	if newAlloc < minVCPU {
		return ""
	}
	if usedCores <= float64(newAlloc)*0.8 {
		return fmt.Sprintf("event used ~%.1f cores of %d; halving vCPU to %d would likely not extend this job materially", usedCores, alloc, newAlloc)
	}
	newDur := time.Duration(total/(float64(newAlloc)*0.9)) * time.Second
	return fmt.Sprintf("event used ~%.1f cores of %d for %s; at %d vCPU this CPU-bound work could take up to ~%s — validate the batch window",
		usedCores, alloc, (time.Duration(s.DurationS) * time.Second).Round(time.Second), newAlloc, newDur.Round(time.Minute))
}

func appendUnique(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}
