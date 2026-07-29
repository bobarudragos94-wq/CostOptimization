package analyzer

import (
	"fmt"
	"math"
	"sort"

	"github.com/bobarudragos94-wq/costoptimization/internal/analyzer/stats"
	"github.com/bobarudragos94-wq/costoptimization/internal/model"
)

// analyzeSQLInstance builds the per-instance report. Interpretation rules
// (documented in docs/ARCHITECTURE.md):
//   - max server memory is not a limit on the whole sqlservr process;
//   - high SQL memory usage is normal caching behavior, never by itself proof
//     of overprovisioning;
//   - Total vs Target Server Memory is only meaningful with enough uptime;
//   - PLE is a trend signal, only flagged together with corroborating
//     pressure (grants pending, lazy writes, process-memory-low flags).
func analyzeSQLInstance(h *HostData, inv *model.SQLInstanceInventory, hostReport *HostReport) SQLReport {
	r := SQLReport{
		HostID: h.HostID, Hostname: hostReport.Hostname,
		InstanceKey: inv.InstanceKey, InstanceName: inv.InstanceName,
		Version: inv.Version, Edition: inv.Edition,
		CollectionLevel: inv.CollectionLevel,
		HARole:          "standalone",
		MinServerMemMB:  inv.MinServerMemoryMB,
		MaxServerMemMB:  inv.MaxServerMemoryMB,
		MaxIsUnlimited:  inv.MaxMemoryUnlimitedDefault,
		EngineUptimeDays: round1(float64(inv.UptimeS) / 86400),
	}
	if hostReport.AllocRAMGB > 0 {
		r.HostRAMGB = hostReport.AllocRAMGB
	} else if inv.PhysMemVisibleKB > 0 {
		r.HostRAMGB = round1(float64(inv.PhysMemVisibleKB) / (1 << 20))
	}
	if inv.HA.Mode == "fci" {
		r.HARole = "fci"
	}
	for _, ag := range inv.HA.AGs {
		r.HARole = ag.Role
		r.HAGroup = ag.AGName
	}

	samples := h.SQLSamples[inv.InstanceKey]
	if inv.CollectionLevel == model.SQLLevelProcessOnly || len(samples) == 0 {
		r.Category = CatMonitorLonger
		r.Recommendation = "SQL Server detected; deep SQL telemetry unavailable. Only process-level data exists. Grant the documented least-privilege login (VIEW SERVER STATE) and monitor again for memory-configuration analysis."
		r.ValidationRequired = true
		r.Confidence = 0.2
		if inv.CollectionNote != "" {
			r.Rationale = append(r.Rationale, inv.CollectionNote)
		}
		return r
	}

	latest := samples[len(samples)-1]
	var ples, grants, batch, sqlcpu, readLat []float64
	waitAgg := map[string]float64{}
	for _, s := range samples {
		ples = append(ples, float64(s.PLESeconds))
		grants = append(grants, float64(s.MemoryGrantsPending))
		batch = append(batch, s.BatchRequestsPS)
		sqlcpu = append(sqlcpu, s.SQLProcessCPUPct)
		for _, io := range s.FileIO {
			if io.FileType == "data" {
				readLat = append(readLat, io.ReadLatMS)
			}
		}
		for wt, wd := range s.WaitDeltas {
			waitAgg[wt] += wd.WaitMS
		}
	}
	sort.Float64s(ples)
	r.PLEP5 = int64(stats.Quantile(ples, 0.05))
	r.GrantsPendingMax = int64(stats.Summarize(grants).Max)
	r.BatchReqP95 = round1(stats.Summarize(batch).P95)
	r.SQLCPUP95 = round1(stats.Summarize(sqlcpu).P95)
	r.FileIOReadP95MS = round1(stats.Summarize(readLat).P95)
	type wt struct {
		name string
		ms   float64
	}
	var waits []wt
	for k, v := range waitAgg {
		waits = append(waits, wt{k, v})
	}
	sort.Slice(waits, func(i, j int) bool { return waits[i].ms > waits[j].ms })
	for i, w := range waits {
		if i >= 5 {
			break
		}
		r.TopWaits = append(r.TopWaits, fmt.Sprintf("%s (%.0fs)", w.name, w.ms/1000))
	}

	r.SQLProcessMemGB = round1(float64(latest.PhysicalMemoryInUseKB) / (1 << 20))
	r.TotalServerMemGB = round1(float64(latest.TotalServerMemoryKB) / (1 << 20))
	r.TargetServerMemGB = round1(float64(latest.TargetServerMemoryKB) / (1 << 20))
	if r.HostRAMGB > 0 {
		r.SQLMemPctOfHost = round1(r.SQLProcessMemGB / r.HostRAMGB * 100)
		r.OSHeadroomGB = round1(r.HostRAMGB - r.SQLProcessMemGB)
	}
	if r.MaxServerMemMB > 0 && !r.MaxIsUnlimited {
		r.SQLMemPctOfMax = round1(float64(latest.TotalServerMemoryKB) / 1024 / float64(r.MaxServerMemMB) * 100)
	}
	// Memory the memory manager does not account for (thread stacks, DLLs,
	// CLR, columnstore builds): process physical memory minus Total Server
	// Memory, when positive.
	if nb := r.SQLProcessMemGB - r.TotalServerMemGB; nb > 0.5 {
		r.NonBufferMemGB = round1(nb)
	}

	// Pressure indicators — always corroborated, never PLE alone.
	if latest.ProcessPhysicalMemoryLow {
		r.PressureSignals = append(r.PressureSignals, "process_physical_memory_low flag set")
	}
	if r.GrantsPendingMax > 0 {
		r.PressureSignals = append(r.PressureSignals, fmt.Sprintf("memory grants pending observed (max %d)", r.GrantsPendingMax))
	}
	bufGB := math.Max(1, float64(latest.DatabaseCacheKB)/(1<<20))
	pleFloor := 300 * bufGB / 4 // classic 300s per 4 GB of buffer pool
	if float64(r.PLEP5) < pleFloor && r.GrantsPendingMax > 0 {
		r.PressureSignals = append(r.PressureSignals,
			fmt.Sprintf("PLE P5 %ds below scaled floor %.0fs with grants pending", r.PLEP5, pleFloor))
	}
	if hostReport.PagingEvidence {
		r.PressureSignals = append(r.PressureSignals, "host-level paging during the window")
	}

	// SQL-correlated spikes and job correlation.
	for _, sc := range h.SQLSpikes {
		if sc.InstanceKey != inv.InstanceKey {
			continue
		}
		r.SQLSpikes++
		for _, j := range sc.AgentJobs {
			if j.Running {
				r.JobCorrelates = append(r.JobCorrelates, "SQL Agent job: "+j.JobName)
			}
		}
		if sc.CorrelationLevel == model.CorrActiveRequest && len(sc.ActiveRequests) > 0 {
			ar := sc.ActiveRequests[0]
			r.JobCorrelates = append(r.JobCorrelates,
				fmt.Sprintf("active request in db %s (%s, query_hash %s)", ar.Database, ar.Command, ar.QueryHash))
		}
	}
	r.JobCorrelates = dedupStrings(r.JobCorrelates)

	classifySQL(&r, h, inv, hostReport)
	return r
}

func classifySQL(r *SQLReport, h *HostData, inv *model.SQLInstanceInventory, hostReport *HostReport) {
	r.ValidationRequired = true
	r.Confidence = confidenceFrom(hostReport.ObservedDays, hostReport.CoveragePct)

	// Combined multi-instance math on this host.
	var combinedMaxMB, combinedProcKB uint64
	unlimitedCount := 0
	for key, other := range h.SQLInv {
		if other.MaxMemoryUnlimitedDefault {
			unlimitedCount++
		} else {
			combinedMaxMB += other.MaxServerMemoryMB
		}
		if ss := h.SQLSamples[key]; len(ss) > 0 {
			combinedProcKB += ss[len(ss)-1].PhysicalMemoryInUseKB
		}
	}
	hostMB := uint64(hostReport.AllocRAMGB * 1024)
	osReserveMB := uint64(math.Max(osReserveMinGB*1024, float64(hostMB)/10))
	multi := len(h.SQLInv) > 1

	uptimeOK := r.EngineUptimeDays >= 3
	targetReached := latestSample(h, inv.InstanceKey) != nil &&
		float64(latestSample(h, inv.InstanceKey).TotalServerMemoryKB) >= 0.95*float64(latestSample(h, inv.InstanceKey).TargetServerMemoryKB)

	switch {
	case r.HARole == "SECONDARY":
		r.Category = CatHADRConstraint
		r.Recommendation = fmt.Sprintf("Secondary replica of availability group %s. Do not size from its own (expectedly idle) workload — it must absorb the primary's load after failover. Assess the replica group together.", r.HAGroup)
		r.Rationale = append(r.Rationale, "availability-group SECONDARY role detected")

	case multi && hostMB > 0 && (unlimitedCount > 0 || combinedMaxMB+osReserveMB > hostMB):
		r.Category = CatMultiInstanceRisk
		if unlimitedCount > 0 {
			r.Recommendation = fmt.Sprintf("%d of %d instances on this host leave max server memory at the unlimited default — the instances will compete and can starve the OS. Set explicit per-instance limits that sum below host RAM minus %.0f GB.", unlimitedCount, len(h.SQLInv), float64(osReserveMB)/1024)
		} else {
			r.Recommendation = fmt.Sprintf("Combined max server memory %.0f GB exceeds host RAM %.0f GB minus the OS reserve. Rebalance the per-instance limits before any host resize.", float64(combinedMaxMB)/1024, float64(hostMB)/1024)
		}
		r.Rationale = append(r.Rationale,
			fmt.Sprintf("combined configured max %.0f GB, combined SQL process memory %.1f GB, host %.0f GB",
				float64(combinedMaxMB)/1024, float64(combinedProcKB)/(1<<20), float64(hostMB)/1024))

	case r.MaxIsUnlimited:
		r.Category = CatSQLMemoryConfig
		suggested := suggestedMaxMemGB(hostReport.AllocRAMGB)
		r.Recommendation = fmt.Sprintf("max server memory is left at its unlimited default (2147483647 MB). SQL Server will grow toward all host RAM and can pressure the OS. Set an explicit limit (suggested ≈ %.0f GB for this %.0f GB host) before considering any RAM change.", suggested, hostReport.AllocRAMGB)
		r.SuggestedMaxMemGB = &CapacityRange{MinRAMGB: suggested, MaxRAMGB: suggested,
			Note: "host RAM minus max(4 GB, 10%) OS reserve; adjust for other software on the host"}
		r.Rationale = append(r.Rationale, "sys.configurations max server memory (MB) = 2147483647")

	case len(r.PressureSignals) > 0:
		r.Category = CatInsufficientHeadroom
		r.Recommendation = "Memory-pressure indicators are present. Do not reduce host RAM or max server memory; investigate the pressure first."
		r.Rationale = append(r.Rationale, r.PressureSignals...)

	case uptimeOK && !targetReached:
		r.Category = CatMonitorLonger
		r.Recommendation = "Total Server Memory has not reached Target after several days — demand may be genuinely low, but verify the engine has seen its full workload cycle (including maintenance and month-end) before concluding."
		r.Rationale = append(r.Rationale,
			fmt.Sprintf("Total %.1f GB vs Target %.1f GB at %.1f days uptime", r.TotalServerMemGB, r.TargetServerMemGB, r.EngineUptimeDays))

	case !uptimeOK:
		r.Category = CatMonitorLonger
		r.Recommendation = fmt.Sprintf("Engine uptime is only %.1f days; Total vs Target Server Memory and cache sizing are not yet meaningful.", r.EngineUptimeDays)

	case r.SQLCPUP95 < 40 && float64(r.PLEP5) > 3600 && r.GrantsPendingMax == 0 && r.SQLMemPctOfMax > 95:
		r.Category = CatPossibleRightsizing
		peakTotal := maxTotalServerMemGB(h, inv.InstanceKey)
		lo := roundGB(peakTotal * 1.2)
		r.SuggestedMaxMemGB = &CapacityRange{MinRAMGB: lo, MaxRAMGB: roundGB(lo * 1.25),
			Note: "based on peak Total Server Memory + 20% cache headroom; PLE must be re-checked after any reduction"}
		r.Recommendation = fmt.Sprintf("The buffer cache is full (as designed) but PLE stays high (P5 %ds) with no pending grants and low SQL CPU — memory demand appears comfortably met. A staged max-server-memory reduction toward %.0f–%.0f GB may be possible. High memory usage alone is NOT proof of overprovisioning; validate after each step.", r.PLEP5, r.SuggestedMaxMemGB.MinRAMGB, r.SuggestedMaxMemGB.MaxRAMGB)
		r.Rationale = append(r.Rationale,
			fmt.Sprintf("SQL CPU P95 %.0f%%, PLE P5 %ds, grants pending max %d", r.SQLCPUP95, r.PLEP5, r.GrantsPendingMax))

	default:
		r.Category = CatNoChange
		r.Recommendation = "SQL memory configuration and observed pressure give no safe reduction opportunity."
		r.Rationale = append(r.Rationale,
			fmt.Sprintf("Total %.1f GB / Target %.1f GB, PLE P5 %ds, SQL CPU P95 %.0f%%",
				r.TotalServerMemGB, r.TargetServerMemGB, r.PLEP5, r.SQLCPUP95))
	}
}

func suggestedMaxMemGB(hostGB float64) float64 {
	reserve := math.Max(osReserveMinGB, hostGB*0.10)
	return math.Max(2, math.Floor(hostGB-reserve))
}

func latestSample(h *HostData, key string) *model.SQLSample {
	ss := h.SQLSamples[key]
	if len(ss) == 0 {
		return nil
	}
	return &ss[len(ss)-1]
}

func maxTotalServerMemGB(h *HostData, key string) float64 {
	var maxKB uint64
	for _, s := range h.SQLSamples[key] {
		if s.TotalServerMemoryKB > maxKB {
			maxKB = s.TotalServerMemoryKB
		}
	}
	return float64(maxKB) / (1 << 20)
}

func dedupStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
