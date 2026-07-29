package analyzer

import (
	"fmt"
	"html"
	"strings"
	"time"
)

// RenderMarkdown produces the human-readable consolidated report.
func RenderMarkdown(r *Report) string {
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }

	w("# Infrastructure Utilization & Rightsizing Report")
	w("")
	w("Generated %s · schema %s · analyzer %s · fully offline",
		r.GeneratedAt.Format("2006-01-02 15:04 UTC"), r.SchemaVersion, r.ToolVersion)
	w("")
	w("## Fleet overview")
	w("")
	w("| Hosts | SQL instances | Allocated vCPU | Allocated RAM | Spike events |")
	w("|---|---|---|---|---|")
	w("| %d | %d | %d | %.0f GB | %d |", r.Fleet.Hosts, r.Fleet.SQLInstances,
		r.Fleet.TotalAllocVCPU, r.Fleet.TotalAllocRAMGB, r.Fleet.SpikeEvents)
	w("")
	w("Recommendation categories:")
	w("")
	for cat, n := range r.Fleet.Categories {
		w("- `%s`: %d host(s)", cat, n)
	}
	if r.Fleet.PotentialVCPU > 0 || r.Fleet.PotentialRAMGB > 0 {
		w("")
		w("Indicative upper bound of reclaimable capacity (only candidates, before validation): **%d vCPU, %.0f GB RAM**. This is a technical ceiling, not a commitment.",
			r.Fleet.PotentialVCPU, r.Fleet.PotentialRAMGB)
	}
	w("")

	w("## Host summaries")
	for i := range r.Hosts {
		h := &r.Hosts[i]
		w("")
		w("### %s (`%s`, %s)", h.Hostname, h.HostID, h.OS)
		w("")
		w("| | |")
		w("|---|---|")
		w("| OS | %s %s |", h.OS, h.OSVersion)
		w("| Virtualization | %s |", h.Virtualization)
		w("| Allocated | %d vCPU, %.0f GB RAM |", h.AllocVCPU, h.AllocRAMGB)
		w("| Observed | %d days at %.0f%% coverage |", h.ObservedDays, h.CoveragePct)
		w("| CPU %% (P50/P95/P99/max) | %.0f / %.0f / %.0f / %.0f |", h.CPU.P50, h.CPU.P95, h.CPU.P99, h.CPUPeak.Max)
		w("| Longest sustained CPU >70%% | %d min (peak %.0f%%) |", h.SustainedCPU.Minutes, h.SustainedCPU.PeakV)
		w("| Memory %% (P50/P95/P99/max) | %.0f / %.0f / %.0f / %.0f (P95 ≈ %.1f GB) |", h.Mem.P50, h.Mem.P95, h.Mem.P99, h.Mem.Max, h.MemUsedP95GB)
		w("| Memory pressure | %s |", orNone(strings.Join(h.MemPressure, "; ")))
		w("| Disk I/O | latency P95 %.1f ms, busy P95 %.0f%% |", h.DiskLatencyP95MS, h.DiskBusyP95Pct)
		w("| Network | rx P95 %.1f MB/s, tx P95 %.1f MB/s |", h.NetRxP95MBs, h.NetTxP95MBs)
		w("| Spikes | %d event(s), %d recurring pattern(s) |", h.SpikeCount, len(h.Patterns))
		if len(h.SQLInstances) > 0 {
			w("| SQL instances | %s |", strings.Join(h.SQLInstances, ", "))
		}
		w("")
		for _, p := range h.Patterns {
			w("- 🔁 %s", p.Description)
		}
		for _, c := range h.TopCorrelates {
			w("- probable correlate: %s", c)
		}
		w("")
		w("**Recommendation: `%s`** (confidence %.0f%%%s)", h.Category, h.Confidence*100,
			ternary(h.ValidationRequired, ", validation required", ""))
		w("")
		w("%s", h.Recommendation)
		if h.Suggested != nil {
			w("")
			if h.Suggested.MinVCPU > 0 {
				w("- suggested vCPU range: **%d–%d** (currently %d)", h.Suggested.MinVCPU, h.Suggested.MaxVCPU, h.AllocVCPU)
			}
			if h.Suggested.MinRAMGB > 0 {
				w("- suggested RAM range: **%.0f–%.0f GB** (currently %.0f GB)", h.Suggested.MinRAMGB, h.Suggested.MaxRAMGB, h.AllocRAMGB)
			}
			w("- %s", h.Suggested.Note)
		}
		if len(h.Rationale) > 0 {
			w("")
			w("Rationale:")
			for _, ra := range h.Rationale {
				w("- %s", ra)
			}
		}
	}

	if len(r.SQLInstances) > 0 {
		w("")
		w("## SQL Server instance summaries")
		for i := range r.SQLInstances {
			s := &r.SQLInstances[i]
			w("")
			w("### %s on %s", s.InstanceName, s.Hostname)
			w("")
			w("| | |")
			w("|---|---|")
			w("| Version / edition | %s / %s |", orNone(s.Version), orNone(s.Edition))
			w("| HA role | %s%s |", s.HARole, ternary(s.HAGroup != "", " ("+s.HAGroup+")", ""))
			w("| Collection level | %s |", s.CollectionLevel)
			w("| Host RAM | %.0f GB |", s.HostRAMGB)
			w("| min / max server memory | %d MB / %s |", s.MinServerMemMB,
				ternary(s.MaxIsUnlimited, "UNLIMITED DEFAULT (2147483647)", fmt.Sprintf("%d MB", s.MaxServerMemMB)))
			w("| SQL process memory | %.1f GB (%.0f%% of host) |", s.SQLProcessMemGB, s.SQLMemPctOfHost)
			w("| Total / Target Server Memory | %.1f / %.1f GB |", s.TotalServerMemGB, s.TargetServerMemGB)
			if s.NonBufferMemGB > 0 {
				w("| Memory outside memory manager | %.1f GB |", s.NonBufferMemGB)
			}
			w("| OS memory headroom | %.1f GB |", s.OSHeadroomGB)
			w("| Engine uptime | %.1f days |", s.EngineUptimeDays)
			w("| PLE P5 / grants pending max | %d s / %d |", s.PLEP5, s.GrantsPendingMax)
			w("| Batch req P95 / SQL CPU P95 | %.0f/s / %.0f%% |", s.BatchReqP95, s.SQLCPUP95)
			w("| Data-file read latency P95 | %.1f ms |", s.FileIOReadP95MS)
			if len(s.TopWaits) > 0 {
				w("| Top waits | %s |", strings.Join(s.TopWaits, ", "))
			}
			if len(s.PressureSignals) > 0 {
				w("| Pressure indicators | %s |", strings.Join(s.PressureSignals, "; "))
			}
			if s.SQLSpikes > 0 {
				w("| SQL-correlated spikes | %d |", s.SQLSpikes)
			}
			for _, j := range s.JobCorrelates {
				w("- probable correlation: %s", j)
			}
			w("")
			w("**Recommendation: `%s`** (confidence %.0f%%%s)", s.Category, s.Confidence*100,
				ternary(s.ValidationRequired, ", validation required", ""))
			w("")
			w("%s", s.Recommendation)
			if len(s.Rationale) > 0 {
				w("")
				for _, ra := range s.Rationale {
					w("- %s", ra)
				}
			}
		}
	}

	if len(r.HAGroups) > 0 {
		w("")
		w("## HA replica groups")
		for _, g := range r.HAGroups {
			w("")
			w("- **%s**: %s", g.AGName, strings.Join(g.Members, "; "))
			if len(g.Replicas) > 0 {
				w("  - replica servers: %s", strings.Join(g.Replicas, ", "))
			}
			w("  - %s", g.Note)
		}
	}

	if len(r.Spikes) > 0 {
		w("")
		w("## Spike report (%d events)", len(r.Spikes))
		w("")
		w("| Host | Start (UTC) | Res | Dur | Baseline→Peak | Process / job | Recurrence | Conf | Next validation step |")
		w("|---|---|---|---|---|---|---|---|---|")
		for i := range r.Spikes {
			s := &r.Spikes[i]
			job := s.Process
			if s.ServiceOrJob != "" {
				job += " / " + s.ServiceOrJob
			}
			if s.SQLEvidence != "" {
				job += " ⟨" + s.SQLEvidence + "⟩"
			}
			rec := ""
			if s.Recurrence != "" {
				rec = "recurring"
			}
			w("| %s | %s | %s%s | %s | %.0f→%.0f | %s | %s | %.0f%% | %s |",
				s.Hostname, s.StartTS.Format("01-02 15:04"), s.Resource,
				ternary(s.Device != "", ":"+s.Device, ""),
				(time.Duration(s.DurationS)*time.Second).Round(time.Second),
				s.Baseline, s.Peak, orNone(job), rec, s.Confidence*100, s.NextValidationStep)
		}
		w("")
		for i := range r.Spikes {
			s := &r.Spikes[i]
			if s.VCPUImpactNote != "" {
				w("- %s `%s` %s: %s", s.Hostname, s.Resource, s.StartTS.Format("01-02 15:04"), s.VCPUImpactNote)
			}
		}
	}

	if len(r.DataQuality) > 0 {
		w("")
		w("## Data quality")
		w("")
		for _, d := range r.DataQuality {
			w("- ⚠️ %s", d)
		}
	}
	w("")
	w("---")
	w("*Recommendations are technical capacity assessments based on the observed window only. Every downsizing action requires application-owner validation; peaks outside the monitoring window (month-end, quarterly, failover) must be considered separately.*")
	return b.String()
}

// RenderHTML wraps the report in a self-contained page (inline CSS, zero
// external assets — verifiable offline).
func RenderHTML(r *Report) string {
	md := RenderMarkdown(r)
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html><head><meta charset="utf-8">
<title>Utilization & Rightsizing Report</title>
<style>
body{font-family:-apple-system,Segoe UI,Roboto,sans-serif;max-width:1100px;margin:2rem auto;padding:0 1rem;color:#1a2233;line-height:1.5}
h1{border-bottom:3px solid #2b6cb0;padding-bottom:.4rem}
h2{color:#2b6cb0;margin-top:2.2rem}
h3{margin-top:1.6rem}
table{border-collapse:collapse;margin:.6rem 0;font-size:.92rem}
td,th{border:1px solid #cbd5e0;padding:.28rem .6rem;text-align:left}
th{background:#edf2f7}
code{background:#edf2f7;padding:.08rem .3rem;border-radius:3px}
li{margin:.15rem 0}
.small{color:#556;font-size:.85rem}
</style></head><body>
`)
	// Minimal Markdown→HTML for our own generated subset.
	inTable := false
	inList := false
	for _, line := range strings.Split(md, "\n") {
		esc := html.EscapeString(line)
		esc = mdInline(esc)
		switch {
		case strings.HasPrefix(line, "|"):
			if !inTable {
				b.WriteString("<table>\n")
				inTable = true
			}
			if strings.HasPrefix(strings.TrimSpace(strings.Trim(line, "|")), "---") {
				continue
			}
			cells := strings.Split(strings.Trim(line, "|"), "|")
			b.WriteString("<tr>")
			for _, c := range cells {
				b.WriteString("<td>" + mdInline(html.EscapeString(strings.TrimSpace(c))) + "</td>")
			}
			b.WriteString("</tr>\n")
			continue
		case inTable:
			b.WriteString("</table>\n")
			inTable = false
		}
		switch {
		case strings.HasPrefix(line, "- "):
			if !inList {
				b.WriteString("<ul>\n")
				inList = true
			}
			b.WriteString("<li>" + mdInline(html.EscapeString(line[2:])) + "</li>\n")
			continue
		case inList:
			b.WriteString("</ul>\n")
			inList = false
		}
		switch {
		case strings.HasPrefix(line, "### "):
			b.WriteString("<h3>" + esc[4:] + "</h3>\n")
		case strings.HasPrefix(line, "## "):
			b.WriteString("<h2>" + esc[3:] + "</h2>\n")
		case strings.HasPrefix(line, "# "):
			b.WriteString("<h1>" + esc[2:] + "</h1>\n")
		case strings.HasPrefix(line, "---"):
			b.WriteString("<hr>\n")
		case strings.TrimSpace(line) == "":
			// paragraph break handled by block elements
		default:
			b.WriteString("<p>" + esc + "</p>\n")
		}
	}
	if inTable {
		b.WriteString("</table>\n")
	}
	if inList {
		b.WriteString("</ul>\n")
	}
	b.WriteString("</body></html>\n")
	return b.String()
}

func mdInline(s string) string {
	for strings.Contains(s, "**") {
		s = strings.Replace(s, "**", "<strong>", 1)
		s = strings.Replace(s, "**", "</strong>", 1)
	}
	for strings.Contains(s, "`") {
		s = strings.Replace(s, "`", "<code>", 1)
		s = strings.Replace(s, "`", "</code>", 1)
	}
	return s
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

func ternary(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}
