//go:build linux

package sampler

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// vmCounters carries cumulative paging and context-switch counters.
type vmCounters struct {
	pageIn, pageOut uint64 // pages swapped/paged in/out (pswpin/pswpout)
	majFault        uint64
	ctxSwitch       uint64
}

// readVMCounters parses /proc/vmstat and /proc/stat (read-only).
func readVMCounters() (vmCounters, error) {
	var vc vmCounters
	f, err := os.Open("/proc/vmstat")
	if err != nil {
		return vc, err
	}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) != 2 {
			continue
		}
		v, _ := strconv.ParseUint(fields[1], 10, 64)
		switch fields[0] {
		case "pswpin":
			vc.pageIn = v
		case "pswpout":
			vc.pageOut = v
		case "pgmajfault":
			vc.majFault = v
		}
	}
	f.Close()
	if f, err := os.Open("/proc/stat"); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "ctxt ") {
				fields := strings.Fields(sc.Text())
				if len(fields) == 2 {
					vc.ctxSwitch, _ = strconv.ParseUint(fields[1], 10, 64)
				}
				break
			}
		}
		f.Close()
	}
	return vc, nil
}

// PSISample carries Linux Pressure Stall Information avg10 values.
type PSISample struct {
	CPUSome, MemSome, MemFull, IOSome, IOFull float64
}

// readPSI reads /proc/pressure/{cpu,memory,io}; errUnsupported on kernels
// without PSI (pre-4.20 or psi=0).
func readPSI() (*PSISample, error) {
	cpuSome, _, err := psiFile("/proc/pressure/cpu")
	if err != nil {
		return nil, errUnsupported
	}
	memSome, memFull, _ := psiFile("/proc/pressure/memory")
	ioSome, ioFull, _ := psiFile("/proc/pressure/io")
	return &PSISample{CPUSome: cpuSome, MemSome: memSome, MemFull: memFull, IOSome: ioSome, IOFull: ioFull}, nil
}

func psiFile(path string) (some, full float64, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		var v float64
		for _, f := range fields[1:] {
			if strings.HasPrefix(f, "avg10=") {
				v, _ = strconv.ParseFloat(strings.TrimPrefix(f, "avg10="), 64)
			}
		}
		switch fields[0] {
		case "some":
			some = v
		case "full":
			full = v
		}
	}
	return some, full, nil
}

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
