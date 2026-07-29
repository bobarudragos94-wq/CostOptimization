//go:build windows

package proctop

import (
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// SCMMapper resolves PIDs to Windows service names by enumerating the
// Service Control Manager (read-only: SC_MANAGER_ENUMERATE_SERVICE). The
// mapping is cached and refreshed at most every 5 minutes.
type SCMMapper struct {
	mu        sync.Mutex
	byPID     map[uint32]string
	refreshed time.Time
}

func NewPlatformMapper() ServiceMapper { return &SCMMapper{byPID: map[uint32]string{}} }

func (m *SCMMapper) ServiceFor(pid int32) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if time.Since(m.refreshed) > 5*time.Minute {
		m.refresh()
		m.refreshed = time.Now()
	}
	return m.byPID[uint32(pid)]
}

func (m *SCMMapper) refresh() {
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_ENUMERATE_SERVICE)
	if err != nil {
		return
	}
	defer windows.CloseServiceHandle(scm)

	var bytesNeeded, servicesReturned, resume uint32
	// First call sizes the buffer.
	_ = windows.EnumServicesStatusEx(scm, windows.SC_ENUM_PROCESS_INFO,
		windows.SERVICE_WIN32, windows.SERVICE_STATE_ALL, nil, 0,
		&bytesNeeded, &servicesReturned, &resume, nil)
	if bytesNeeded == 0 {
		return
	}
	buf := make([]byte, bytesNeeded)
	err = windows.EnumServicesStatusEx(scm, windows.SC_ENUM_PROCESS_INFO,
		windows.SERVICE_WIN32, windows.SERVICE_STATE_ALL, &buf[0], uint32(len(buf)),
		&bytesNeeded, &servicesReturned, &resume, nil)
	if err != nil {
		return
	}
	next := map[uint32]string{}
	svcSize := unsafe.Sizeof(windows.ENUM_SERVICE_STATUS_PROCESS{})
	for i := uint32(0); i < servicesReturned; i++ {
		svc := (*windows.ENUM_SERVICE_STATUS_PROCESS)(unsafe.Pointer(&buf[uintptr(i)*svcSize]))
		pid := svc.ServiceStatusProcess.ProcessId
		if pid != 0 {
			next[pid] = windows.UTF16PtrToString(svc.ServiceName)
		}
	}
	m.byPID = next
}
