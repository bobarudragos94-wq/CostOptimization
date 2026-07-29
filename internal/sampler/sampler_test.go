package sampler

import (
	"runtime"
	"testing"
	"time"

	"github.com/bobarudragos94-wq/costoptimization/internal/model"
	"github.com/bobarudragos94-wq/costoptimization/internal/spike"
)

// TestLiveSampling exercises real collection on the build host (Linux in CI;
// the same gopsutil paths serve Windows).
func TestLiveSampling(t *testing.T) {
	s := New(nil)
	if hr := s.Sample(); hr != nil {
		t.Fatal("first sample must prime and return nil")
	}
	time.Sleep(150 * time.Millisecond)
	hr := s.Sample()
	if hr == nil {
		t.Fatal("second sample must produce data")
	}
	if hr.CPUTotalPct < 0 || hr.CPUTotalPct > 100 {
		t.Fatalf("cpu out of range: %v", hr.CPUTotalPct)
	}
	if hr.MemUsedBytes == 0 || hr.MemAvailBytes == 0 {
		t.Fatalf("memory not collected: %+v", hr)
	}
	if runtime.GOOS == "linux" && hr.PSI == nil {
		t.Log("PSI unavailable on this kernel (acceptable degradation)")
	}
	pt := hr.SpikePoint()
	if _, ok := pt.Values[spike.Key{Resource: model.ResCPU}]; !ok {
		t.Fatal("spike point must include cpu")
	}
}

func TestMinuteAggregation(t *testing.T) {
	var agg MinuteAgg
	base := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	mk := func(ts time.Time, cpu, mem float64) *HighRes {
		return &HighRes{TS: ts, CPUTotalPct: cpu, MemUsedPct: mem,
			MemUsedBytes: uint64(mem) << 28, MemAvailBytes: 8 << 30,
			Disks: map[string]DiskRates{"sda": {ReadIOPS: 10, ReadLatMS: 2}},
			Net:   map[string]NetRates{"eth0": {RxBytesPS: 1e6, SpeedMbps: 1000}},
		}
	}
	if out := agg.Add(mk(base, 10, 40)); out != nil {
		t.Fatal("no emission mid-minute")
	}
	agg.Add(mk(base.Add(15*time.Second), 20, 42))
	agg.Add(mk(base.Add(30*time.Second), 90, 44))
	agg.Add(mk(base.Add(45*time.Second), 40, 46))
	out := agg.Add(mk(base.Add(60*time.Second), 5, 40)) // rollover
	if out == nil {
		t.Fatal("minute rollover must emit")
	}
	if out.Samples != 4 {
		t.Fatalf("samples: %d", out.Samples)
	}
	if out.CPU.AvgPct != 40 || out.CPU.MaxPct != 90 {
		t.Fatalf("cpu agg: %+v", out.CPU)
	}
	if out.Mem.UsedPctMax != 46 {
		t.Fatalf("mem agg: %+v", out.Mem)
	}
	if len(out.Disks) != 1 || out.Disks[0].ReadLatMS != 2 {
		t.Fatalf("disk agg: %+v", out.Disks)
	}
	if len(out.Net) != 1 || out.Net[0].UtilPct == 0 {
		t.Fatalf("net utilization must be computed when speed known: %+v", out.Net)
	}
	if p := agg.FlushPartial(); p == nil || p.Samples != 1 {
		t.Fatalf("partial flush: %+v", p)
	}
}
