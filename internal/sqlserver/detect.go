// Package sqlserver detects Microsoft SQL Server instances through read-only
// OS discovery and, when permitted, collects deep telemetry via strictly
// read-only DMV queries.
//
// Detection never fails host collection: an instance without SQL access is
// reported at collection level "process_only" with the note
// "SQL Server detected; deep SQL telemetry unavailable."
package sqlserver

import (
	"strings"

	"github.com/shirou/gopsutil/v4/process"
)

// Instance describes a detected SQL Server instance at the OS level.
type Instance struct {
	Name string // MSSQLSERVER for the default instance
	PID  int32
	Port int    // 0 = unknown (default instance assumed 1433)
	OS   string // linux | windows
	// LinuxMemoryLimitMB from mssql.conf when present (Linux only).
	LinuxMemoryLimitMB uint64
}

// DetectInstances discovers running SQL Server instances by process scan plus
// platform-specific discovery (registry on Windows, mssql.conf on Linux).
func DetectInstances() []Instance {
	byPID := map[int32]Instance{}
	procs, err := process.Processes()
	if err == nil {
		for _, p := range procs {
			name, err := p.Name()
			if err != nil {
				continue
			}
			base := strings.ToLower(strings.TrimSuffix(name, ".exe"))
			if base != "sqlservr" {
				continue
			}
			byPID[p.Pid] = Instance{Name: "MSSQLSERVER", PID: p.Pid}
		}
	}
	return platformResolve(byPID)
}
