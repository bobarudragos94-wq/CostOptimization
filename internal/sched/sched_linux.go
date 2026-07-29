//go:build linux

package sched

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/bobarudragos94-wq/costoptimization/internal/model"
)

func platformEntries(issues func(string, string)) []model.SchedEntry {
	var out []model.SchedEntry
	out = append(out, cronEntries(issues)...)
	out = append(out, systemdTimerEntries(issues)...)
	return out
}

// cronEntries parses system crontabs (read-only). User spool crontabs need
// root; a permission failure is reported once and skipped.
func cronEntries(issues func(string, string)) []model.SchedEntry {
	var out []model.SchedEntry
	paths := []string{"/etc/crontab"}
	if files, err := filepath.Glob("/etc/cron.d/*"); err == nil {
		paths = append(paths, files...)
	}
	spool := "/var/spool/cron/crontabs"
	if files, err := os.ReadDir(spool); err == nil {
		for _, f := range files {
			paths = append(paths, filepath.Join(spool, f.Name()))
		}
	} else if os.IsPermission(err) {
		issues("sched.cron", "user crontabs unreadable (needs root); system crontabs only")
	}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") || strings.Contains(line, "=") && !strings.HasPrefix(line, "*") && !strings.HasPrefix(line, "@") && len(strings.Fields(line)) < 6 {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 6 || strings.HasPrefix(fields[0], "@") {
				continue
			}
			schedule := strings.Join(fields[:5], " ")
			// Command identity only — arguments beyond the executable are
			// dropped (may contain credentials).
			cmdIdx := 5
			if p == "/etc/crontab" || strings.HasPrefix(p, "/etc/cron.d/") {
				cmdIdx = 6 // user field present
			}
			name := ""
			if len(fields) > cmdIdx {
				name = filepath.Base(fields[cmdIdx])
			} else if len(fields) > 5 {
				name = filepath.Base(fields[5])
			}
			out = append(out, model.SchedEntry{
				Source: "cron", Name: name, Schedule: schedule,
			})
		}
	}
	return out
}

// systemdTimerEntries queries systemctl (read-only) for timers with last/next
// activation; falls back silently when systemctl is unavailable.
func systemdTimerEntries(issues func(string, string)) []model.SchedEntry {
	cmd := exec.Command("systemctl", "list-timers", "--all", "--no-pager", "--no-legend")
	b, err := cmd.Output()
	if err != nil {
		issues("sched.timers", "systemctl list-timers unavailable: "+err.Error())
		return nil
	}
	var out []model.SchedEntry
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		var timer, unit string
		var next, last time.Time
		for i, f := range fields {
			if strings.HasSuffix(f, ".timer") && timer == "" {
				timer = f
				// NEXT is at the start of the line: try to parse leading date.
				if i >= 3 {
					if ts, err := time.Parse("Mon 2006-01-02 15:04:05 MST", strings.Join(fields[0:4], " ")); err == nil {
						next = ts
					}
				}
			} else if strings.HasSuffix(f, ".service") {
				unit = f
			}
		}
		if timer == "" {
			continue
		}
		out = append(out, model.SchedEntry{
			Source: "systemd_timer", Name: timer, Unit: unit,
			NextRun: next.UTC(), LastRun: last,
		})
	}
	return out
}
