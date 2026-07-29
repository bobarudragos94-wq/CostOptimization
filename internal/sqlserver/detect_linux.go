//go:build linux

package sqlserver

import (
	"os"
	"strconv"
	"strings"
)

// platformResolve on Linux: SQL Server on Linux runs one instance per host
// installation (default instance). Reads /var/opt/mssql/mssql.conf (read-only)
// for the configured memory limit where accessible.
func platformResolve(byPID map[int32]Instance) []Instance {
	var out []Instance
	memLimit := linuxMemoryLimitMB()
	for _, inst := range byPID {
		inst.OS = "linux"
		inst.Port = 1433
		inst.LinuxMemoryLimitMB = memLimit
		out = append(out, inst)
	}
	return out
}

func linuxMemoryLimitMB() uint64 {
	b, err := os.ReadFile("/var/opt/mssql/mssql.conf")
	if err != nil {
		return 0
	}
	inMemory := false
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			inMemory = line == "[memory]"
			continue
		}
		if inMemory && strings.HasPrefix(line, "memorylimitmb") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				if v, err := strconv.ParseUint(strings.TrimSpace(parts[1]), 10, 64); err == nil {
					return v
				}
			}
		}
	}
	return 0
}
