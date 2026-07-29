//go:build windows

package sqlserver

import (
	"strings"

	"golang.org/x/sys/windows/registry"

	"github.com/shirou/gopsutil/v4/process"
	"github.com/shirou/gopsutil/v4/winservices"
)

// platformResolve on Windows maps sqlservr.exe processes to instance names via
// the registry (HKLM\SOFTWARE\Microsoft\Microsoft SQL Server\Instance Names\SQL,
// read-only) and the service name of each process (MSSQLSERVER / MSSQL$NAME).
func platformResolve(byPID map[int32]Instance) []Instance {
	instNames := registryInstances()
	svcByPID := sqlServicePIDs()

	var out []Instance
	for pid, inst := range byPID {
		inst.OS = "windows"
		if name, ok := svcByPID[uint32(pid)]; ok {
			inst.Name = name
		} else if len(instNames) == 1 {
			inst.Name = instNames[0]
		}
		out = append(out, inst)
	}
	// Registry-known instances whose process was inaccessible still get a
	// process_only entry (PID 0), so "SQL Server detected" is never missed.
	for _, name := range instNames {
		found := false
		for _, inst := range out {
			if inst.Name == name {
				found = true
			}
		}
		if !found {
			out = append(out, Instance{Name: name, OS: "windows"})
		}
	}
	return out
}

func registryInstances() []string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Microsoft SQL Server\Instance Names\SQL`, registry.READ)
	if err != nil {
		return nil
	}
	defer k.Close()
	names, err := k.ReadValueNames(0)
	if err != nil {
		return nil
	}
	return names
}

// sqlServicePIDs maps running SQL Server service PIDs to instance names.
func sqlServicePIDs() map[uint32]string {
	out := map[uint32]string{}
	svcs, err := winservices.ListServices()
	if err != nil {
		return out
	}
	for _, s := range svcs {
		name := s.Name
		var inst string
		if name == "MSSQLSERVER" {
			inst = "MSSQLSERVER"
		} else if strings.HasPrefix(name, "MSSQL$") {
			inst = strings.TrimPrefix(name, "MSSQL$")
		} else {
			continue
		}
		sv, err := winservices.NewService(name)
		if err != nil {
			continue
		}
		if err := sv.GetServiceDetail(); err != nil {
			continue
		}
		if sv.Status.Pid != 0 {
			out[sv.Status.Pid] = inst
		}
	}
	return out
}

var _ = process.Processes // keep import parity with shared code
