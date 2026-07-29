//go:build linux

package proctop

import (
	"os"
	"strconv"
	"strings"
)

// SystemdMapper resolves a PID to its systemd unit by reading the process
// cgroup (read-only, no D-Bus dependency).
type SystemdMapper struct{}

func NewPlatformMapper() ServiceMapper { return SystemdMapper{} }

func (SystemdMapper) ServiceFor(pid int32) string {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(int(pid)) + "/cgroup")
	if err != nil {
		return ""
	}
	return UnitFromCgroup(string(b))
}

// UnitFromCgroup extracts the systemd service/scope from cgroup content.
// Exported for tests.
func UnitFromCgroup(content string) string {
	for _, line := range strings.Split(content, "\n") {
		// format: hierarchy-ID:controller-list:cgroup-path
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		path := parts[2]
		for _, seg := range strings.Split(path, "/") {
			if strings.HasSuffix(seg, ".service") || strings.HasSuffix(seg, ".scope") {
				return seg
			}
		}
	}
	return ""
}
