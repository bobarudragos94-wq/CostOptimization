//go:build windows

package sched

import (
	"encoding/csv"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/bobarudragos94-wq/costoptimization/internal/model"
)

// platformEntries queries the Task Scheduler via `schtasks /query /fo CSV`
// (read-only). Task actions/arguments are never captured — only the task
// name and run times needed for spike correlation.
func platformEntries(issues func(string, string)) []model.SchedEntry {
	cmd := exec.Command("schtasks", "/query", "/fo", "CSV", "/nh")
	b, err := cmd.Output()
	if err != nil {
		issues("sched.tasks", "schtasks query failed: "+err.Error())
		return nil
	}
	r := csv.NewReader(strings.NewReader(string(b)))
	r.FieldsPerRecord = -1
	var out []model.SchedEntry
	for {
		rec, err := r.Read()
		if err != nil {
			break
		}
		if len(rec) < 2 || rec[0] == "" || strings.HasPrefix(rec[0], "\\Microsoft\\") {
			continue // skip OS-internal tasks to bound cardinality
		}
		e := model.SchedEntry{Source: "win_task", Name: filepath.Base(rec[0])}
		if len(rec) >= 2 {
			for _, layout := range []string{"1/2/2006 3:04:05 PM", "2006-01-02 15:04:05"} {
				if ts, err := time.Parse(layout, rec[1]); err == nil {
					e.NextRun = ts.UTC()
					break
				}
			}
		}
		out = append(out, e)
	}
	return out
}
