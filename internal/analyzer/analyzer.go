// Package analyzer imports encrypted bundles and produces the consolidated
// rightsizing reports. Fully offline: file I/O only.
package analyzer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"filippo.io/age"

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

// Run imports every bundle under inDir and writes report.json/.md/.html to outDir.
func Run(inDir string, identities []age.Identity, outDir string) (*RunResult, error) {
	ds, err := LoadBundles(inDir, identities)
	if err != nil {
		return nil, err
	}
	rep := Build(ds)
	if err := os.MkdirAll(outDir, 0o750); err != nil {
		return nil, err
	}
	jb, _ := json.MarshalIndent(rep, "", "  ")
	if err := os.WriteFile(filepath.Join(outDir, "report.json"), jb, 0o640); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(outDir, "report.md"), []byte(RenderMarkdown(rep)), 0o640); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(outDir, "report.html"), []byte(RenderHTML(rep)), 0o640); err != nil {
		return nil, err
	}
	return &RunResult{
		Bundles: ds.Bundles, OKBundles: ds.OKBundles,
		PartialBundles: ds.PartialBundles, RejectedBundles: ds.RejectedBundles,
		Hosts: len(rep.Hosts), SQLInstances: len(rep.SQLInstances), Spikes: len(rep.Spikes),
		Warnings: ds.Warnings,
	}, nil
}

// Build computes the full report from an imported dataset.
func Build(ds *Dataset) *Report {
	rep := &Report{
		GeneratedAt:   time.Now().UTC(),
		SchemaVersion: model.SchemaVersion,
		ToolVersion:   model.AgentVersion,
		Fleet:         FleetSummary{Categories: map[string]int{}},
	}
	rep.DataQuality = append(rep.DataQuality, ds.Warnings...)

	var hostIDs []string
	for id := range ds.Hosts {
		hostIDs = append(hostIDs, id)
	}
	sort.Strings(hostIDs)

	for _, id := range hostIDs {
		h := ds.Hosts[id]
		hr := analyzeHost(h)
		rep.Hosts = append(rep.Hosts, hr)
		rep.Fleet.Hosts++
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

		var sqlKeys []string
		for key := range h.SQLInv {
			sqlKeys = append(sqlKeys, key)
		}
		sort.Strings(sqlKeys)
		for _, key := range sqlKeys {
			sr := analyzeSQLInstance(h, h.SQLInv[key], &hr)
			rep.SQLInstances = append(rep.SQLInstances, sr)
			rep.Fleet.SQLInstances++
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
	}

	rep.HAGroups = buildHAGroups(ds)
	sort.Slice(rep.Spikes, func(i, j int) bool { return rep.Spikes[i].StartTS.Before(rep.Spikes[j].StartTS) })
	return rep
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

// buildHAGroups links replicas across hosts by availability-group name.
func buildHAGroups(ds *Dataset) []HAGroup {
	byAG := map[string]*HAGroup{}
	for _, h := range ds.Hosts {
		for key, inv := range h.SQLInv {
			for _, ag := range inv.HA.AGs {
				g := byAG[ag.AGName]
				if g == nil {
					g = &HAGroup{AGName: ag.AGName,
						Note: "Replicas of one availability group: evaluate sizing together; a secondary must absorb the primary's workload after failover. Topology-level validation required before any resize."}
					byAG[ag.AGName] = g
				}
				g.Members = append(g.Members, key+" ("+ag.Role+")")
				for _, rep := range ag.PartnerReplicas {
					g.Replicas = appendUnique(g.Replicas, rep)
				}
			}
		}
	}
	var out []HAGroup
	for _, g := range byAG {
		sort.Strings(g.Members)
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AGName < out[j].AGName })
	return out
}

func appendUnique(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}
