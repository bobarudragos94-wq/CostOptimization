// Package sampler reads host telemetry at high resolution via gopsutil and
// platform files, computes rates from counter deltas, aggregates minutes for
// long-term storage and feeds the spike detector.
//
// Every probe degrades independently; failures are reported through the
// Issues callback exactly once per collector (no log spam, surfaced in
// collection health).
package sampler

import (
	"math"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	gnet "github.com/shirou/gopsutil/v4/net"

	"github.com/bobarudragos94-wq/costoptimization/internal/model"
	"github.com/bobarudragos94-wq/costoptimization/internal/spike"
)

// HighRes is one high-resolution host sample with rates already computed.
type HighRes struct {
	TS time.Time

	CPUTotalPct  float64
	CPUUserPct   float64
	CPUSystemPct float64
	CPUIOWaitPct float64
	CPUStealPct  float64
	PerCorePct   []float64
	Load1, Load5 float64
	CtxSwitchPS  float64

	MemUsedPct     float64
	MemUsedBytes   uint64
	MemAvailBytes  uint64
	MemCommitted   uint64
	SwapUsedBytes  uint64
	PageInPS       float64
	PageOutPS      float64
	MajorFaultPS   float64

	Disks map[string]DiskRates
	Net   map[string]NetRates

	PSI *PSISample // nil when unsupported
}

type DiskRates struct {
	ReadIOPS, WriteIOPS       float64
	ReadBytesPS, WriteBytesPS float64
	ReadLatMS, WriteLatMS     float64
	QueueDepth                float64
	BusyPct                   float64
}

type NetRates struct {
	RxBytesPS, TxBytesPS float64
	RxPktsPS, TxPktsPS   float64
	ErrsPS, DropsPS      float64
	SpeedMbps            int64
}

// Issues receives "collector: error" strings, deduplicated by the caller.
type Issues func(collector, err string)

type Sampler struct {
	issues Issues

	prevTS       time.Time
	prevCPU      []cpu.TimesStat
	prevCPUCores []cpu.TimesStat
	prevDisk     map[string]disk.IOCountersStat
	prevNet      map[string]gnet.IOCountersStat
	prevVM       vmCounters
	prevVMOK     bool
	nicSpeeds    map[string]int64
}

func New(issues Issues) *Sampler {
	if issues == nil {
		issues = func(string, string) {}
	}
	return &Sampler{issues: issues, nicSpeeds: map[string]int64{}}
}

// Sample takes one reading. The first call primes counters and returns nil.
func (s *Sampler) Sample() *HighRes {
	now := time.Now()
	hr := &HighRes{TS: now.UTC(), Disks: map[string]DiskRates{}, Net: map[string]NetRates{}}
	elapsed := now.Sub(s.prevTS).Seconds()
	first := s.prevTS.IsZero()

	// CPU
	if total, err := cpu.Times(false); err == nil && len(total) == 1 {
		if !first && len(s.prevCPU) == 1 {
			hr.CPUTotalPct, hr.CPUUserPct, hr.CPUSystemPct, hr.CPUIOWaitPct, hr.CPUStealPct =
				cpuDeltaPct(s.prevCPU[0], total[0])
		}
		s.prevCPU = total
	} else if err != nil {
		s.issues("cpu.times", err.Error())
	}
	if cores, err := cpu.Times(true); err == nil {
		if !first && len(s.prevCPUCores) == len(cores) {
			hr.PerCorePct = make([]float64, len(cores))
			for i := range cores {
				pct, _, _, _, _ := cpuDeltaPct(s.prevCPUCores[i], cores[i])
				hr.PerCorePct[i] = pct
			}
		}
		s.prevCPUCores = cores
	}
	if la, err := load.Avg(); err == nil {
		hr.Load1, hr.Load5 = la.Load1, la.Load5
	}

	// Memory
	if vm, err := mem.VirtualMemory(); err == nil {
		hr.MemUsedPct = vm.UsedPercent
		hr.MemUsedBytes = vm.Used
		hr.MemAvailBytes = vm.Available
		hr.MemCommitted = vm.CommittedAS
	} else {
		s.issues("mem.virtual", err.Error())
	}
	if sw, err := mem.SwapMemory(); err == nil {
		hr.SwapUsedBytes = sw.Used
	}

	// Paging / context switches (platform counters)
	if vc, err := readVMCounters(); err == nil {
		if !first && s.prevVMOK && elapsed > 0 {
			hr.PageInPS = rate(vc.pageIn, s.prevVM.pageIn, elapsed)
			hr.PageOutPS = rate(vc.pageOut, s.prevVM.pageOut, elapsed)
			hr.MajorFaultPS = rate(vc.majFault, s.prevVM.majFault, elapsed)
			hr.CtxSwitchPS = rate(vc.ctxSwitch, s.prevVM.ctxSwitch, elapsed)
		}
		s.prevVM, s.prevVMOK = vc, true
	} else if err != errUnsupported {
		s.issues("vmstat", err.Error())
	}

	// Disk I/O
	if io, err := disk.IOCounters(); err == nil {
		if !first && elapsed > 0 {
			for dev, cur := range io {
				if skipDevice(dev) {
					continue
				}
				prev, ok := s.prevDisk[dev]
				if !ok {
					continue
				}
				// Counter reset (reboot, device re-attach): uint64 deltas
				// would wrap to huge positives — skip this interval entirely.
				if cur.ReadCount < prev.ReadCount || cur.WriteCount < prev.WriteCount ||
					cur.ReadTime < prev.ReadTime || cur.WriteTime < prev.WriteTime ||
					cur.IoTime < prev.IoTime {
					continue
				}
				dr := DiskRates{
					ReadIOPS:     rate(cur.ReadCount, prev.ReadCount, elapsed),
					WriteIOPS:    rate(cur.WriteCount, prev.WriteCount, elapsed),
					ReadBytesPS:  rate(cur.ReadBytes, prev.ReadBytes, elapsed),
					WriteBytesPS: rate(cur.WriteBytes, prev.WriteBytes, elapsed),
					BusyPct:      clampPct(rate(cur.IoTime, prev.IoTime, elapsed) / 10), // ms busy per s / 10 = %
					QueueDepth:   rate(cur.WeightedIO, prev.WeightedIO, elapsed) / 1000, // avg requests in flight
				}
				if dc := cur.ReadCount - prev.ReadCount; dc > 0 {
					dr.ReadLatMS = float64(cur.ReadTime-prev.ReadTime) / float64(dc)
				}
				if dc := cur.WriteCount - prev.WriteCount; dc > 0 {
					dr.WriteLatMS = float64(cur.WriteTime-prev.WriteTime) / float64(dc)
				}
				hr.Disks[dev] = dr
			}
		}
		s.prevDisk = io
	} else {
		s.issues("disk.io", err.Error())
	}

	// Network
	if nics, err := gnet.IOCounters(true); err == nil {
		cur := map[string]gnet.IOCountersStat{}
		for _, n := range nics {
			if n.Name == "lo" || strings.HasPrefix(n.Name, "Loopback") {
				continue
			}
			cur[n.Name] = n
			if !first && elapsed > 0 {
				if prev, ok := s.prevNet[n.Name]; ok {
					sp, cached := s.nicSpeeds[n.Name]
					if !cached {
						sp = nicSpeedMbps(n.Name)
						s.nicSpeeds[n.Name] = sp
					}
					hr.Net[n.Name] = NetRates{
						RxBytesPS: rate(n.BytesRecv, prev.BytesRecv, elapsed),
						TxBytesPS: rate(n.BytesSent, prev.BytesSent, elapsed),
						RxPktsPS:  rate(n.PacketsRecv, prev.PacketsRecv, elapsed),
						TxPktsPS:  rate(n.PacketsSent, prev.PacketsSent, elapsed),
						ErrsPS:    rate(n.Errin+n.Errout, prev.Errin+prev.Errout, elapsed),
						DropsPS:   rate(n.Dropin+n.Dropout, prev.Dropin+prev.Dropout, elapsed),
						SpeedMbps: sp,
					}
				}
			}
		}
		s.prevNet = cur
	} else {
		s.issues("net.io", err.Error())
	}

	// PSI (Linux)
	if psi, err := readPSI(); err == nil {
		hr.PSI = psi
	} else if err != errUnsupported {
		s.issues("psi", err.Error())
	}

	s.prevTS = now
	if first {
		return nil
	}
	return hr
}

// SpikePoint converts a sample into detector inputs.
func (hr *HighRes) SpikePoint() spike.Point {
	vals := map[spike.Key]float64{
		{Resource: model.ResCPU}:    hr.CPUTotalPct,
		{Resource: model.ResMemory}: hr.MemUsedPct,
		{Resource: model.ResPaging}: hr.PageInPS + hr.PageOutPS + hr.MajorFaultPS,
	}
	for dev, d := range hr.Disks {
		lat := math.Max(d.ReadLatMS, d.WriteLatMS)
		vals[spike.Key{Resource: model.ResDiskLatency, Device: dev}] = lat
		vals[spike.Key{Resource: model.ResDiskQueue, Device: dev}] = d.QueueDepth
		vals[spike.Key{Resource: model.ResDiskIO, Device: dev}] = d.BusyPct
	}
	// Network: utilization percent per NIC. Only NICs with a known link speed
	// can feed the net spike rule — without capacity, a percent threshold is
	// meaningless (documented in docs/CONFIGURATION.md).
	for name, n := range hr.Net {
		if n.SpeedMbps > 0 {
			util := clampPct((n.RxBytesPS + n.TxBytesPS) * 8 / 1e6 / float64(n.SpeedMbps) * 100)
			vals[spike.Key{Resource: model.ResNet, Device: name}] = util
		}
	}
	return spike.Point{TS: hr.TS, Values: vals}
}

// ---------------------------------------------------------------------------
// Minute aggregation
// ---------------------------------------------------------------------------

type MinuteAgg struct {
	minute  time.Time
	samples []*HighRes
}

// Add accumulates a sample; when the UTC minute rolls over it returns the
// completed aggregate for the previous minute (else nil).
func (m *MinuteAgg) Add(hr *HighRes) *model.HostMinute {
	min := hr.TS.Truncate(time.Minute)
	var out *model.HostMinute
	if !m.minute.IsZero() && !min.Equal(m.minute) && len(m.samples) > 0 {
		out = m.emit()
	}
	if m.minute.IsZero() || !min.Equal(m.minute) {
		m.minute = min
		m.samples = m.samples[:0]
	}
	m.samples = append(m.samples, hr)
	return out
}

// FlushPartial emits whatever is accumulated (shutdown path).
func (m *MinuteAgg) FlushPartial() *model.HostMinute {
	if len(m.samples) == 0 {
		return nil
	}
	out := m.emit()
	m.samples = m.samples[:0]
	return out
}

func (m *MinuteAgg) emit() *model.HostMinute {
	n := float64(len(m.samples))
	hm := &model.HostMinute{
		Meta:    model.NewMeta(model.KindHostMinute, m.minute),
		Samples: len(m.samples),
	}
	var psiCount float64
	psi := model.PSIAgg{}
	diskAcc := map[string]*model.DiskAgg{}
	diskCount := map[string]float64{}
	netAcc := map[string]*model.NetAgg{}
	netCount := map[string]float64{}

	for _, s := range m.samples {
		hm.CPU.AvgPct += s.CPUTotalPct / n
		hm.CPU.MaxPct = math.Max(hm.CPU.MaxPct, s.CPUTotalPct)
		hm.CPU.UserPct += s.CPUUserPct / n
		hm.CPU.SystemPct += s.CPUSystemPct / n
		hm.CPU.IOWaitPct += s.CPUIOWaitPct / n
		hm.CPU.StealPct += s.CPUStealPct / n
		hm.CPU.Load1 += s.Load1 / n
		hm.CPU.Load5 += s.Load5 / n
		hm.CPU.CtxSwitchPS += s.CtxSwitchPS / n
		if len(s.PerCorePct) > 0 {
			if hm.CPU.PerCoreMax == nil {
				hm.CPU.PerCoreMax = make([]float64, len(s.PerCorePct))
			}
			for i, v := range s.PerCorePct {
				if i < len(hm.CPU.PerCoreMax) {
					hm.CPU.PerCoreMax[i] = math.Max(hm.CPU.PerCoreMax[i], v)
				}
			}
		}

		hm.Mem.UsedPctAvg += s.MemUsedPct / n
		hm.Mem.UsedPctMax = math.Max(hm.Mem.UsedPctMax, s.MemUsedPct)
		hm.Mem.UsedBytesAvg += uint64(float64(s.MemUsedBytes) / n)
		if hm.Mem.AvailBytesMin == 0 || s.MemAvailBytes < hm.Mem.AvailBytesMin {
			hm.Mem.AvailBytesMin = s.MemAvailBytes
		}
		if s.MemCommitted > hm.Mem.CommittedBytes {
			hm.Mem.CommittedBytes = s.MemCommitted
		}
		if s.SwapUsedBytes > hm.Mem.SwapUsedBytes {
			hm.Mem.SwapUsedBytes = s.SwapUsedBytes
		}
		hm.Mem.PageInPS += s.PageInPS / n
		hm.Mem.PageOutPS += s.PageOutPS / n
		hm.Mem.MajorFaultPS += s.MajorFaultPS / n

		for dev, d := range s.Disks {
			a := diskAcc[dev]
			if a == nil {
				a = &model.DiskAgg{Device: dev}
				diskAcc[dev] = a
			}
			diskCount[dev]++
			a.ReadIOPS += d.ReadIOPS
			a.WriteIOPS += d.WriteIOPS
			a.ReadBytesPS += d.ReadBytesPS
			a.WriteBytesPS += d.WriteBytesPS
			a.ReadLatMS += d.ReadLatMS
			a.WriteLatMS += d.WriteLatMS
			a.QueueDepth += d.QueueDepth
			a.BusyPct += d.BusyPct
		}
		for name, nn := range s.Net {
			a := netAcc[name]
			if a == nil {
				a = &model.NetAgg{Name: name}
				netAcc[name] = a
			}
			netCount[name]++
			a.RxBytesPS += nn.RxBytesPS
			a.TxBytesPS += nn.TxBytesPS
			a.RxPktsPS += nn.RxPktsPS
			a.TxPktsPS += nn.TxPktsPS
			a.ErrsPS += nn.ErrsPS
			a.DropsPS += nn.DropsPS
			if nn.SpeedMbps > 0 {
				a.UtilPct = math.Max(a.UtilPct, clampPct((nn.RxBytesPS+nn.TxBytesPS)*8/1e6/float64(nn.SpeedMbps)*100))
			}
		}
		if s.PSI != nil {
			psiCount++
			psi.CPUSomeAvg += s.PSI.CPUSome
			psi.MemSomeAvg += s.PSI.MemSome
			psi.MemFullAvg += s.PSI.MemFull
			psi.IOSomeAvg += s.PSI.IOSome
			psi.IOFullAvg += s.PSI.IOFull
			psi.MemSomeMax = math.Max(psi.MemSomeMax, s.PSI.MemSome)
			psi.IOSomeMax = math.Max(psi.IOSomeMax, s.PSI.IOSome)
		}
	}
	for dev, a := range diskAcc {
		c := diskCount[dev]
		a.ReadIOPS = round2(a.ReadIOPS / c)
		a.WriteIOPS = round2(a.WriteIOPS / c)
		a.ReadBytesPS = round2(a.ReadBytesPS / c)
		a.WriteBytesPS = round2(a.WriteBytesPS / c)
		a.ReadLatMS = round2(a.ReadLatMS / c)
		a.WriteLatMS = round2(a.WriteLatMS / c)
		a.QueueDepth = round2(a.QueueDepth / c)
		a.BusyPct = round2(a.BusyPct / c)
		hm.Disks = append(hm.Disks, *a)
	}
	for name, a := range netAcc {
		c := netCount[name]
		a.RxBytesPS = round2(a.RxBytesPS / c)
		a.TxBytesPS = round2(a.TxBytesPS / c)
		a.RxPktsPS = round2(a.RxPktsPS / c)
		a.TxPktsPS = round2(a.TxPktsPS / c)
		a.ErrsPS = round2(a.ErrsPS / c)
		a.DropsPS = round2(a.DropsPS / c)
		hm.Net = append(hm.Net, *a)
	}
	if psiCount > 0 {
		psi.CPUSomeAvg = round2(psi.CPUSomeAvg / psiCount)
		psi.MemSomeAvg = round2(psi.MemSomeAvg / psiCount)
		psi.MemFullAvg = round2(psi.MemFullAvg / psiCount)
		psi.IOSomeAvg = round2(psi.IOSomeAvg / psiCount)
		psi.IOFullAvg = round2(psi.IOFullAvg / psiCount)
		hm.PSI = &psi
	}
	roundAggs(hm)
	return hm
}

// AttachFS adds filesystem usage (collected once per minute, cheap statfs).
func AttachFS(hm *model.HostMinute) {
	parts, err := disk.Partitions(false)
	if err != nil {
		return
	}
	for _, p := range parts {
		if isPseudoFS(p.Fstype) {
			continue
		}
		if u, err := disk.Usage(p.Mountpoint); err == nil {
			hm.FS = append(hm.FS, model.FSUsage{Mount: p.Mountpoint, TotalBytes: u.Total, AvailBytes: u.Free})
		}
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func cpuDeltaPct(prev, cur cpu.TimesStat) (total, user, system, iowait, steal float64) {
	dUser := cur.User - prev.User
	dSys := cur.System - prev.System
	dIdle := cur.Idle - prev.Idle
	dIowait := cur.Iowait - prev.Iowait
	dSteal := cur.Steal - prev.Steal
	dOther := (cur.Nice - prev.Nice) + (cur.Irq - prev.Irq) + (cur.Softirq - prev.Softirq)
	busy := dUser + dSys + dSteal + dOther
	all := busy + dIdle + dIowait
	if all <= 0 {
		return 0, 0, 0, 0, 0
	}
	pct := func(v float64) float64 { return clampPct(v / all * 100) }
	return pct(busy), pct(dUser), pct(dSys), pct(dIowait), pct(dSteal)
}

func rate[T uint64 | int64](cur, prev T, elapsed float64) float64 {
	if cur < prev || elapsed <= 0 { // counter reset
		return 0
	}
	return float64(cur-prev) / elapsed
}

func clampPct(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func skipDevice(dev string) bool {
	return strings.HasPrefix(dev, "loop") || strings.HasPrefix(dev, "ram") ||
		strings.HasPrefix(dev, "zram") || strings.HasPrefix(dev, "sr")
}

func isPseudoFS(fstype string) bool {
	switch strings.ToLower(fstype) {
	case "tmpfs", "devtmpfs", "proc", "sysfs", "cgroup", "cgroup2", "overlay",
		"squashfs", "ramfs", "devfs", "autofs", "debugfs", "tracefs", "fusectl",
		"configfs", "securityfs", "pstore", "bpf", "hugetlbfs", "mqueue", "efivarfs":
		return true
	}
	return false
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

func roundAggs(hm *model.HostMinute) {
	c := &hm.CPU
	c.AvgPct, c.MaxPct = round2(c.AvgPct), round2(c.MaxPct)
	c.UserPct, c.SystemPct = round2(c.UserPct), round2(c.SystemPct)
	c.IOWaitPct, c.StealPct = round2(c.IOWaitPct), round2(c.StealPct)
	c.Load1, c.Load5, c.CtxSwitchPS = round2(c.Load1), round2(c.Load5), round2(c.CtxSwitchPS)
	for i := range c.PerCoreMax {
		c.PerCoreMax[i] = round2(c.PerCoreMax[i])
	}
	m := &hm.Mem
	m.UsedPctAvg, m.UsedPctMax = round2(m.UsedPctAvg), round2(m.UsedPctMax)
	m.PageInPS, m.PageOutPS, m.MajorFaultPS = round2(m.PageInPS), round2(m.PageOutPS), round2(m.MajorFaultPS)
}
