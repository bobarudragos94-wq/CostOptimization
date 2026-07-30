package analyzer

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/bobarudragos94-wq/costoptimization/internal/analyzer/stats"
	"github.com/bobarudragos94-wq/costoptimization/internal/model"
)

// Recommendation categories (section 16 of the specification).
const (
	CatLikelyRightsizing   = "likely_rightsizing_candidate"
	CatPossibleRightsizing = "possible_rightsizing_candidate"
	CatOptimizeWorkload    = "optimize_recurring_workload_before_rightsizing"
	CatSQLMemoryConfig     = "sql_memory_configuration_issue"
	CatMultiInstanceRisk   = "multi_instance_memory_allocation_risk"
	CatInsufficientHeadroom = "insufficient_os_headroom"
	CatHADRConstraint      = "ha_dr_constraint"
	CatNoChange            = "no_change_recommended"
	CatInsufficientData    = "insufficient_data"
	CatMonitorLonger       = "monitor_for_longer"
)

// Sizing targets (documented headroom policy; see docs/ARCHITECTURE.md).
const (
	cpuTargetPeakPct   = 70.0 // suggested capacity keeps P95 below this
	cpuSustainedLimit  = 85.0 // sustained peaks must stay below this
	memHeadroomFactor  = 1.30 // suggested RAM = P95 used × this
	osReserveMinGB     = 4.0  // minimum RAM kept for the OS beside SQL
	minVCPU            = 2
)

// CapacityRange is a technical suggestion, never a cloud SKU.
type CapacityRange struct {
	MinVCPU  int     `json:"min_vcpu,omitempty"`
	MaxVCPU  int     `json:"max_vcpu,omitempty"`
	MinRAMGB float64 `json:"min_ram_gb,omitempty"`
	MaxRAMGB float64 `json:"max_ram_gb,omitempty"`
	Note     string  `json:"note,omitempty"`
}

// HostReport is the per-host summary (section 15).
type HostReport struct {
	HostID       string  `json:"host_id"`
	Hostname     string  `json:"hostname"`
	OS           string  `json:"os"`
	OSVersion    string  `json:"os_version,omitempty"`
	Virtualization string `json:"virtualization,omitempty"`
	AllocVCPU    int     `json:"allocated_vcpu"`
	AllocRAMGB   float64 `json:"allocated_ram_gb"`

	ObservedDays     int     `json:"observed_days"`
	CoveragePct      float64 `json:"coverage_pct"`
	DataProblems     []string `json:"data_problems,omitempty"`
	DegradedCollectors map[string]string `json:"degraded_collectors,omitempty"`
	PermissionIssues   []string          `json:"permission_issues,omitempty"`
	DroppedSamples     uint64            `json:"dropped_samples,omitempty"`

	CPU          stats.Summary `json:"cpu_pct"`      // avg-of-minute
	CPUPeak      stats.Summary `json:"cpu_peak_pct"` // max-of-minute
	CPUSteal     float64       `json:"cpu_steal_avg_pct,omitempty"`
	SustainedCPU stats.SustainedRun `json:"longest_sustained_cpu_over_70"`
	MinutesOver70 int          `json:"minutes_cpu_over_70"`
	MinutesOver85 int          `json:"minutes_cpu_over_85"`

	Mem            stats.Summary `json:"mem_used_pct"`
	MemUsedP95GB   float64       `json:"mem_used_p95_gb"`
	// MemAvailP5GB is the 5th percentile of observed available memory: the
	// real OS headroom including every consumer, used by the SQL reports.
	MemAvailP5GB   float64       `json:"mem_avail_p5_gb"`
	MemPressure    []string      `json:"memory_pressure_evidence,omitempty"`
	PagingEvidence bool          `json:"paging_evidence"`

	DiskCapacity []model.FSUsage `json:"disk_capacity,omitempty"`
	DiskLatencyP95MS float64     `json:"disk_latency_p95_ms"`
	DiskBusyP95Pct   float64     `json:"disk_busy_p95_pct"`

	NetRxP95MBs float64 `json:"net_rx_p95_mbs"`
	NetTxP95MBs float64 `json:"net_tx_p95_mbs"`

	SpikeCount int       `json:"spike_count"`
	Patterns   []Pattern `json:"repetitive_spikes,omitempty"`
	TopCorrelates []string `json:"probable_correlated_processes,omitempty"`

	SQLInstances []string `json:"sql_instances,omitempty"`

	Category           string        `json:"recommendation_category"`
	Recommendation     string        `json:"recommendation"`
	Suggested          *CapacityRange `json:"suggested_capacity,omitempty"`
	Confidence         float64       `json:"confidence"`
	ValidationRequired bool          `json:"validation_required"`
	Rationale          []string      `json:"rationale"`
}

// SQLReport is the per-instance summary (section 15).
type SQLReport struct {
	HostID       string `json:"host_id"`
	Hostname     string `json:"hostname"`
	InstanceKey  string `json:"instance_key"`
	InstanceName string `json:"instance_name"`
	Version      string `json:"version,omitempty"`
	Edition      string `json:"edition,omitempty"`
	HARole       string `json:"ha_role"`
	HAGroup      string `json:"ha_group,omitempty"`
	CollectionLevel string `json:"collection_level"`

	HostRAMGB        float64 `json:"host_ram_gb"`
	MinServerMemMB   uint64  `json:"min_server_memory_mb"`
	MaxServerMemMB   uint64  `json:"max_server_memory_mb"`
	MaxIsUnlimited   bool    `json:"max_memory_unlimited_default"`
	SQLProcessMemGB  float64 `json:"sql_process_memory_gb"` // physical_memory_in_use, latest
	// Distributions over the whole window, not just the last sample: a
	// single snapshot hides growth, restarts and cache churn.
	SQLProcessMem  stats.Summary `json:"sql_process_memory_gb_dist"`
	TotalServerMem stats.Summary `json:"total_server_memory_gb_dist"`
	TotalServerMemGB float64 `json:"total_server_memory_gb"` // latest
	TargetServerMemGB float64 `json:"target_server_memory_gb"`
	SQLMemPctOfHost  float64 `json:"sql_memory_pct_of_host"`
	SQLMemPctOfMax   float64 `json:"sql_memory_pct_of_configured_max"`
	NonBufferMemGB   float64 `json:"memory_outside_memory_manager_gb,omitempty"`
	OSHeadroomGB     float64 `json:"os_memory_headroom_gb"`
	EngineUptimeDays float64 `json:"engine_uptime_days"`

	PLEP5          int64    `json:"ple_p5_seconds"`
	GrantsPendingMax int64  `json:"memory_grants_pending_max"`
	PressureSignals []string `json:"memory_pressure_indicators,omitempty"`

	BatchReqP95   float64 `json:"batch_requests_p95_ps"`
	SQLCPUP95     float64 `json:"sql_cpu_p95_pct"`
	FileIOReadP95MS float64 `json:"file_io_read_latency_p95_ms"`
	TopWaits      []string `json:"top_waits,omitempty"`

	SQLSpikes     int      `json:"sql_correlated_spikes"`
	JobCorrelates []string `json:"probable_job_or_request_correlation,omitempty"`

	Category           string   `json:"recommendation_category"`
	Recommendation     string   `json:"recommendation"`
	SuggestedMaxMemGB  *CapacityRange `json:"suggested_memory,omitempty"`
	Confidence         float64  `json:"confidence"`
	ValidationRequired bool     `json:"validation_required"`
	Rationale          []string `json:"rationale"`
}

// SpikeReportEntry is one row of the consolidated spike report.
type SpikeReportEntry struct {
	HostID    string    `json:"host_id"`
	Hostname  string    `json:"hostname"`
	EventID   string    `json:"event_id"`
	StartTS   time.Time `json:"start_ts"`
	DurationS float64   `json:"duration_s"`
	Resource  string    `json:"resource"`
	Device    string    `json:"device,omitempty"`
	Baseline  float64   `json:"baseline"`
	Peak      float64   `json:"peak"`
	Recurrence string   `json:"recurrence,omitempty"`
	Process   string    `json:"process,omitempty"`
	ServiceOrJob string `json:"service_or_job,omitempty"`
	SQLInstance  string `json:"sql_instance,omitempty"`
	SQLEvidence  string `json:"sql_evidence,omitempty"`
	ProbableCause string `json:"probable_cause"`
	Confidence   float64 `json:"confidence"`
	Evidence     []string `json:"evidence,omitempty"`
	NextValidationStep string `json:"proposed_next_validation_step"`
	VCPUImpactNote string   `json:"vcpu_reduction_impact,omitempty"`
}

// ---------------------------------------------------------------------------
// Host analysis
// ---------------------------------------------------------------------------

func analyzeHost(h *HostData) HostReport {
	r := HostReport{HostID: h.HostID, Hostname: "(unknown)", OS: "unknown"}
	if h.Inventory != nil {
		inv := h.Inventory
		r.Hostname = inv.Hostname
		r.OS = inv.OS
		r.OSVersion = inv.OSVersion
		r.Virtualization = inv.Virtualization
		r.AllocVCPU = inv.AllocatedVCPU
		r.AllocRAMGB = round1(float64(inv.TotalRAMBytes) / (1 << 30))
	}
	r.DataProblems = append(r.DataProblems, h.Problems...)

	if len(h.Minutes) == 0 {
		r.Category = CatInsufficientData
		r.Recommendation = "No aggregated telemetry was imported for this host; no assessment is possible."
		r.ValidationRequired = true
		return r
	}

	first, last := h.Minutes[0].TS, h.Minutes[len(h.Minutes)-1].TS
	span := last.Sub(first)
	r.ObservedDays = int(span.Hours()/24) + 1
	expected := span.Minutes() + 1
	r.CoveragePct = round1(math.Min(100, float64(len(h.Minutes))/expected*100))
	// The agent's own persisted coverage counters see gaps the imported span
	// cannot (e.g. the agent was down for days between exports). Take the
	// more pessimistic of the two.
	if h.ManifestExpectedMin > 0 {
		mc := round1(math.Min(100, float64(h.ManifestCollectedMin)/float64(h.ManifestExpectedMin)*100))
		if mc < r.CoveragePct {
			r.CoveragePct = mc
		}
	}
	r.DegradedCollectors = h.DegradedCollectors
	r.PermissionIssues = h.PermissionIssues
	r.DroppedSamples = h.DroppedSamples

	var cpuAvg, cpuMax, memPct, steal []float64
	var memUsedBytes, memAvailBytes []float64
	var latencies, busies, rx, tx []float64
	var cpuTimed []stats.TimedValue
	pressure := map[string]bool{}
	paging := false
	for _, m := range h.Minutes {
		cpuAvg = append(cpuAvg, m.CPU.AvgPct)
		cpuMax = append(cpuMax, m.CPU.MaxPct)
		steal = append(steal, m.CPU.StealPct)
		memPct = append(memPct, m.Mem.UsedPctAvg)
		memUsedBytes = append(memUsedBytes, float64(m.Mem.UsedBytesAvg))
		memAvailBytes = append(memAvailBytes, float64(m.Mem.AvailBytesMin))
		cpuTimed = append(cpuTimed, stats.TimedValue{TS: m.TS, V: m.CPU.AvgPct})
		for _, d := range m.Disks {
			latencies = append(latencies, math.Max(d.ReadLatMS, d.WriteLatMS))
			busies = append(busies, d.BusyPct)
		}
		for _, n := range m.Net {
			rx = append(rx, n.RxBytesPS/1e6)
			tx = append(tx, n.TxBytesPS/1e6)
		}
		if m.Mem.MajorFaultPS > 100 || m.Mem.PageOutPS > 500 {
			paging = true
			pressure["sustained paging activity observed"] = true
		}
		if m.PSI != nil {
			if m.PSI.MemSomeAvg > 5 || m.PSI.MemFullAvg > 1 {
				pressure[fmt.Sprintf("memory PSI pressure (some avg %.1f%%)", m.PSI.MemSomeAvg)] = true
			}
			if m.PSI.IOSomeAvg > 10 {
				pressure[fmt.Sprintf("I/O PSI pressure (some avg %.1f%%)", m.PSI.IOSomeAvg)] = true
			}
		}
	}
	r.CPU = stats.Summarize(cpuAvg)
	r.CPUPeak = stats.Summarize(cpuMax)
	r.CPUSteal = stats.Summarize(steal).P95
	r.Mem = stats.Summarize(memPct)
	r.MemUsedP95GB = round1(stats.Summarize(memUsedBytes).P95 / (1 << 30))
	if len(memAvailBytes) > 0 {
		sort.Float64s(memAvailBytes)
		r.MemAvailP5GB = round1(stats.Quantile(memAvailBytes, 0.05) / (1 << 30))
	}
	r.SustainedCPU, r.MinutesOver70 = stats.Sustained(cpuTimed, cpuTargetPeakPct, 3*time.Minute)
	_, r.MinutesOver85 = stats.Sustained(cpuTimed, cpuSustainedLimit, 3*time.Minute)
	r.DiskLatencyP95MS = round1(stats.Summarize(latencies).P95)
	r.DiskBusyP95Pct = round1(stats.Summarize(busies).P95)
	r.NetRxP95MBs = round1(stats.Summarize(rx).P95)
	r.NetTxP95MBs = round1(stats.Summarize(tx).P95)
	r.PagingEvidence = paging
	for p := range pressure {
		r.MemPressure = append(r.MemPressure, p)
	}
	sort.Strings(r.MemPressure)

	// Latest filesystem usage.
	for i := len(h.Minutes) - 1; i >= 0; i-- {
		if len(h.Minutes[i].FS) > 0 {
			r.DiskCapacity = h.Minutes[i].FS
			break
		}
	}

	r.SpikeCount = len(h.Spikes)
	r.Patterns = DetectPatterns(h.Spikes, r.ObservedDays)
	corr := map[string]int{}
	for _, s := range h.Spikes {
		if s.CorrelatedProcess != "" {
			key := s.CorrelatedProcess
			if s.CorrelatedServiceOrJob != "" {
				key += " (" + s.CorrelatedServiceOrJob + ")"
			}
			corr[key]++
		}
	}
	for k, n := range corr {
		r.TopCorrelates = append(r.TopCorrelates, fmt.Sprintf("%s ×%d", k, n))
	}
	sort.Strings(r.TopCorrelates)
	for key := range h.SQLInv {
		r.SQLInstances = append(r.SQLInstances, key)
	}
	sort.Strings(r.SQLInstances)

	classifyHost(&r, h)
	return r
}

// classifyHost applies the explainable decision ladder. Order matters and is
// documented in docs/ARCHITECTURE.md.
func classifyHost(r *HostReport, h *HostData) {
	r.ValidationRequired = true
	conf := confidenceFrom(r.ObservedDays, r.CoveragePct)
	r.Confidence = conf

	haRole := localHARole(h)
	memHigh := r.Mem.P95 >= 80 || len(r.MemPressure) > 0
	cpuHigh := r.CPU.P95 >= 75 || r.MinutesOver85 > 30

	switch {
	case r.ObservedDays < 3 || r.CoveragePct < 50:
		r.Category = CatInsufficientData
		r.Recommendation = "Not enough clean telemetry to assess this host. Continue monitoring."
		r.Rationale = append(r.Rationale,
			fmt.Sprintf("only %d observed day(s) at %.0f%% coverage", r.ObservedDays, r.CoveragePct))

	case r.ObservedDays < 7:
		r.Category = CatMonitorLonger
		r.Recommendation = "Early signals only. Keep monitoring; monthly and weekly cycles are not yet covered."
		r.Rationale = append(r.Rationale,
			fmt.Sprintf("%d observed days — month-end and weekly batch cycles not yet observed", r.ObservedDays))

	case r.CoveragePct < 70:
		r.Category = CatMonitorLonger
		r.Recommendation = "Collection coverage is too low for a confident assessment — the gaps may hide exactly the peaks that matter. Investigate the agent downtime and keep monitoring."
		r.Rationale = append(r.Rationale,
			fmt.Sprintf("only %.0f%% of the monitoring window was actually collected", r.CoveragePct))

	case haRole == "SECONDARY":
		r.Category = CatHADRConstraint
		r.Recommendation = "This host is an Always On secondary replica. Its low utilization is expected; it must be sized for the primary's workload after failover. Evaluate together with its replica group only."
		r.Rationale = append(r.Rationale, "local SQL instance reports SECONDARY availability-group role")

	case memHigh:
		if r.Mem.P95 >= 90 {
			r.Category = CatInsufficientHeadroom
			r.Recommendation = "Memory headroom is insufficient; do not reduce resources. Consider investigating consumers or adding RAM."
		} else {
			r.Category = CatNoChange
			r.Recommendation = "Memory utilization or pressure is high enough that reducing capacity would create risk. No reduction recommended."
		}
		r.Rationale = append(r.Rationale, fmt.Sprintf("memory P95 %.0f%% with pressure evidence %v", r.Mem.P95, r.MemPressure))

	// The recurring-workload check must precede the high-CPU check: on a
	// batch host the recurring spikes themselves are what push time-above-85%
	// up, and the right answer is "optimize the job", not "keep everything".
	case dominatedByRecurringSpikes(r):
		r.Category = CatOptimizeWorkload
		r.Recommendation = "Baseline utilization is low, but recurring scheduled workloads drive the peaks. Optimize or reschedule those jobs first; rightsizing before that risks extending the job windows."
		for _, p := range r.Patterns {
			r.Rationale = append(r.Rationale, p.Description)
		}
		if r.Suggested = suggestCapacity(r); r.Suggested != nil {
			r.Suggested.Note = "range only valid after the recurring workload is optimized or accepted as-is"
		}

	case cpuHigh:
		r.Category = CatNoChange
		r.Recommendation = "CPU utilization is high enough that reducing capacity would create risk. No reduction recommended."
		r.Rationale = append(r.Rationale, fmt.Sprintf("CPU P95 %.0f%%, %d minutes above %.0f%%", r.CPU.P95, r.MinutesOver85, cpuSustainedLimit))

	case r.CPU.P95 < 40 && r.Mem.P95 < 60 && !r.PagingEvidence:
		r.Category = CatLikelyRightsizing
		r.Recommendation = "Utilization is consistently far below allocation with no pressure evidence. Strong technical rightsizing candidate — validate with the application owner before resizing."
		r.Rationale = append(r.Rationale,
			fmt.Sprintf("CPU P95 %.0f%% and memory P95 %.0f%% of allocation over %d days", r.CPU.P95, r.Mem.P95, r.ObservedDays),
			fmt.Sprintf("longest sustained CPU period above %.0f%%: %d minutes", cpuTargetPeakPct, r.SustainedCPU.Minutes))
		r.Suggested = suggestCapacity(r)

	case r.CPU.P95 < 55 && r.Mem.P95 < 75:
		r.Category = CatPossibleRightsizing
		r.Recommendation = "Moderate headroom exists. A conservative reduction may be possible after validating peak windows with the owner."
		r.Rationale = append(r.Rationale,
			fmt.Sprintf("CPU P95 %.0f%%, memory P95 %.0f%%", r.CPU.P95, r.Mem.P95))
		r.Suggested = suggestCapacity(r)
		r.Confidence = math.Max(0.2, conf-0.15)

	default:
		r.Category = CatNoChange
		r.Recommendation = "Utilization does not leave enough safe margin for a reduction."
		r.Rationale = append(r.Rationale,
			fmt.Sprintf("CPU P95 %.0f%%, memory P95 %.0f%%", r.CPU.P95, r.Mem.P95))
	}

	if r.CPUSteal > 5 {
		r.Rationale = append(r.Rationale,
			fmt.Sprintf("WARNING: CPU steal P95 %.1f%% — hypervisor contention; guest CPU numbers understate demand", r.CPUSteal))
		if r.Category == CatLikelyRightsizing {
			r.Category = CatPossibleRightsizing
		}
	}

	// Collection-health gate (applied LAST so it overrides everything):
	// downsizing may only ever be recommended from trustworthy data. If a
	// CPU/memory collector was degraded, or a meaningful share of samples
	// was dropped, the numbers understate real utilization — the honest
	// answer is insufficient_data, not a candidate.
	if r.Category == CatLikelyRightsizing || r.Category == CatPossibleRightsizing {
		if bad := criticalDegradations(r); len(bad) > 0 {
			r.Category = CatInsufficientData
			r.Suggested = nil
			r.Confidence = math.Min(r.Confidence, 0.2)
			r.Recommendation = "Utilization appears low, but host telemetry collection was degraded during the window — the data cannot support a downsizing recommendation. Fix collection (see rationale) and monitor again."
			r.Rationale = append(r.Rationale, bad...)
		}
	}
}

// criticalDegradations lists collection problems that invalidate CPU/memory
// conclusions. Process-attribution degradations (proc.io etc.) do not
// invalidate utilization percentiles and are excluded.
func criticalDegradations(r *HostReport) []string {
	var out []string
	for coll, status := range r.DegradedCollectors {
		switch {
		case coll == "host" || strings.HasPrefix(coll, "cpu"),
			strings.HasPrefix(coll, "mem"), coll == "vmstat", coll == "disk.io":
			out = append(out, fmt.Sprintf("collector %s was %s during the window", coll, status))
		}
	}
	total := float64(r.DroppedSamples)
	if total > 0 && r.ObservedDays > 0 {
		// Expected high-res samples ≈ minutes × 4 (15 s cadence).
		expected := float64(r.ObservedDays) * 24 * 60 * 4
		if total/expected > 0.05 {
			out = append(out, fmt.Sprintf("%d samples (>5%%) were dropped under disk pressure — utilization percentiles are unreliable", r.DroppedSamples))
		}
	}
	sort.Strings(out)
	return out
}

// dominatedByRecurringSpikes: low baseline but recurring patterns explain the peaks.
func dominatedByRecurringSpikes(r *HostReport) bool {
	if len(r.Patterns) == 0 {
		return false
	}
	return r.CPU.P50 < 35 && (r.CPUPeak.Max >= 85 || r.CPU.P99 >= 60)
}

// suggestCapacity computes the technical vCPU/RAM range with documented
// headroom. Never a SKU.
func suggestCapacity(r *HostReport) *CapacityRange {
	if r.AllocVCPU == 0 || r.AllocRAMGB == 0 {
		return nil
	}
	// vCPU: keep observed P95 below cpuTargetPeakPct at the new size, and the
	// observed absolute peak below 100% of the new size.
	needP95 := float64(r.AllocVCPU) * r.CPU.P95 / cpuTargetPeakPct
	needPeak := float64(r.AllocVCPU) * r.CPUPeak.P99 / 100
	lo := int(math.Ceil(math.Max(needP95, needPeak)))
	if lo < minVCPU {
		lo = minVCPU
	}
	hi := lo + (r.AllocVCPU-lo)/3 // conservative upper bound of the range
	if hi <= lo {
		hi = lo + 1
	}
	if lo >= r.AllocVCPU {
		return nil // nothing to gain
	}
	ram := r.MemUsedP95GB * memHeadroomFactor
	ramLo := math.Max(4, roundGB(ram))
	ramHi := math.Max(ramLo+2, roundGB(ram*1.25))
	if ramLo >= r.AllocRAMGB {
		ramLo, ramHi = 0, 0
	}
	return &CapacityRange{
		MinVCPU: lo, MaxVCPU: hi, MinRAMGB: ramLo, MaxRAMGB: ramHi,
		Note: fmt.Sprintf("keeps CPU P95 ≤ %.0f%% and RAM P95 + %.0f%% headroom; validate against month-end/quarterly peaks not observed in the window",
			cpuTargetPeakPct, (memHeadroomFactor-1)*100),
	}
}

func localHARole(h *HostData) string {
	for _, inv := range h.SQLInv {
		for _, ag := range inv.HA.AGs {
			if ag.Role == "SECONDARY" {
				return "SECONDARY"
			}
			if ag.Role == "PRIMARY" {
				return "PRIMARY"
			}
		}
	}
	return ""
}

func confidenceFrom(days int, coveragePct float64) float64 {
	d := math.Min(float64(days), 30) / 30
	c := coveragePct / 100
	return round2(math.Min(0.9, 0.25+0.45*d+0.2*c))
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
func round2(v float64) float64 { return math.Round(v*100) / 100 }
func roundGB(v float64) float64 { return math.Ceil(v) }
