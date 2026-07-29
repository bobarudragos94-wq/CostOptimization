//go:build !linux

package inventory

// nicSpeedMbps: link speed detection is not implemented off-Linux in v1
// (Windows would need an IP Helper query); network utilization percentage is
// simply omitted when speed is unknown.
func nicSpeedMbps(string) int64 { return 0 }
