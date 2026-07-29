//go:build linux

package inventory

import (
	"os"
	"strconv"
	"strings"
)

// nicSpeedMbps reads the link speed from sysfs (read-only). -1/0 = unknown.
func nicSpeedMbps(name string) int64 {
	b, err := os.ReadFile("/sys/class/net/" + name + "/speed")
	if err != nil {
		return 0
	}
	v, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil || v < 0 {
		return 0
	}
	return v
}
