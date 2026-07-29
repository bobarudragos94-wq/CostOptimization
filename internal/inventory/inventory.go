// Package inventory collects the versioned host inventory snapshot using
// read-only OS interfaces (gopsutil). Every field degrades independently:
// a failed probe yields an empty field plus a note, never a failed snapshot.
package inventory

import (
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
	gnet "github.com/shirou/gopsutil/v4/net"

	"github.com/bobarudragos94-wq/costoptimization/internal/model"
)

// Collect builds the inventory snapshot. Returned issues list every probe
// that failed (for collection-health reporting).
func Collect(hostID, configHash string, privacy bool) (*model.Inventory, []string) {
	var issues []string
	now := time.Now()
	inv := &model.Inventory{
		Meta:         model.NewMeta(model.KindInventory, now),
		HostID:       hostID,
		AgentVersion: model.AgentVersion,
		ConfigHash:   configHash,
		PrivacyMode:  privacy,
	}

	tzName, tzOff := now.Zone()
	inv.Timezone = tzName
	inv.TZOffsetSec = tzOff

	if hi, err := host.Info(); err == nil {
		inv.Hostname = hi.Hostname
		inv.OS = hi.OS
		inv.OSVersion = strings.TrimSpace(hi.Platform + " " + hi.PlatformVersion)
		inv.KernelBuild = hi.KernelVersion
		inv.BootTime = time.Unix(int64(hi.BootTime), 0).UTC()
		inv.UptimeS = hi.Uptime
		switch {
		case hi.VirtualizationRole == "guest" && hi.VirtualizationSystem != "":
			inv.Virtualization = "vm:" + hi.VirtualizationSystem
		case hi.VirtualizationRole == "guest":
			inv.Virtualization = "vm"
		case hi.VirtualizationRole == "host" || hi.VirtualizationSystem == "":
			inv.Virtualization = "physical_or_unknown"
		default:
			inv.Virtualization = "unknown"
		}
	} else {
		issues = append(issues, "inventory.host: "+err.Error())
	}

	if infos, err := cpu.Info(); err == nil && len(infos) > 0 {
		inv.CPUModel = infos[0].ModelName
	} else if err != nil {
		issues = append(issues, "inventory.cpuinfo: "+err.Error())
	}
	if n, err := cpu.Counts(false); err == nil {
		inv.PhysicalCores = n
	}
	if n, err := cpu.Counts(true); err == nil {
		inv.LogicalCPUs = n
		// In-guest view: allocated vCPU == logical CPUs visible to the OS.
		// Hypervisor-side overcommit is not observable offline (documented).
		inv.AllocatedVCPU = n
	} else {
		issues = append(issues, "inventory.cpucount: "+err.Error())
	}

	if vm, err := mem.VirtualMemory(); err == nil {
		inv.TotalRAMBytes = vm.Total
	} else {
		issues = append(issues, "inventory.mem: "+err.Error())
	}
	if sw, err := mem.SwapMemory(); err == nil {
		inv.SwapTotalBytes = sw.Total
	}

	if parts, err := disk.Partitions(false); err == nil {
		seen := map[string]bool{}
		for _, p := range parts {
			if isPseudoFS(p.Fstype) {
				continue
			}
			if !seen[p.Device] {
				seen[p.Device] = true
				inv.Disks = append(inv.Disks, model.DiskInfo{Device: p.Device})
			}
			if u, err := disk.Usage(p.Mountpoint); err == nil {
				inv.Filesystems = append(inv.Filesystems, model.FSInfo{
					Mount: p.Mountpoint, Device: p.Device, FSType: p.Fstype,
					TotalBytes: u.Total, AvailBytes: u.Free,
				})
			}
		}
	} else {
		issues = append(issues, "inventory.disk: "+err.Error())
	}

	if ifs, err := gnet.Interfaces(); err == nil {
		for _, ifc := range ifs {
			if isLoopback(ifc) {
				continue
			}
			ni := model.NICInfo{Name: ifc.Name, MAC: ifc.HardwareAddr, SpeedMbps: nicSpeedMbps(ifc.Name)}
			for _, a := range ifc.Addrs {
				ni.Addrs = append(ni.Addrs, a.Addr)
			}
			inv.NICs = append(inv.NICs, ni)
		}
	} else {
		issues = append(issues, "inventory.net: "+err.Error())
	}

	return inv, issues
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

func isLoopback(ifc gnet.InterfaceStat) bool {
	for _, f := range ifc.Flags {
		if f == "loopback" {
			return true
		}
	}
	return ifc.Name == "lo"
}
