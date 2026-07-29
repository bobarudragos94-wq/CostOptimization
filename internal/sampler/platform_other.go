//go:build !linux

package sampler

// vmCounters: cumulative paging counters. On Windows, gopsutil does not expose
// pages/sec or context switches without PDH counters; v1 reports paging via
// swap usage trends and marks the paging collector degraded (documented in
// docs/LIMITATIONS.md, surfaced in collection health).
type vmCounters struct {
	pageIn, pageOut uint64
	majFault        uint64
	ctxSwitch       uint64
}

func readVMCounters() (vmCounters, error) { return vmCounters{}, errUnsupported }

type PSISample struct {
	CPUSome, MemSome, MemFull, IOSome, IOFull float64
}

// readPSI: PSI is Linux-only.
func readPSI() (*PSISample, error) { return nil, errUnsupported }

func nicSpeedMbps(string) int64 { return 0 }
