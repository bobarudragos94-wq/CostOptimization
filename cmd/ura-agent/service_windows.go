//go:build windows

package main

import (
	"context"
	"fmt"
	"os"

	"golang.org/x/sys/windows/svc"

	"github.com/bobarudragos94-wq/costoptimization/internal/agent"
)

func defaultConfigPath() string { return `C:\ProgramData\ura-agent\agent.yaml` }

// runService detects whether we were started by the Service Control Manager
// and runs accordingly (interactive fallback for debugging).
func runService(cfgPath string) {
	isSvc, err := svc.IsWindowsService()
	if err == nil && isSvc {
		if err := svc.Run("ura-agent", &uraService{cfgPath: cfgPath}); err != nil {
			fmt.Fprintln(os.Stderr, "service error:", err)
			os.Exit(1)
		}
		return
	}
	runForeground(cfgPath)
}

type uraService struct{ cfgPath string }

func (s *uraService) Execute(_ []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	cfg := mustConfig(s.cfgPath)
	a, err := agent.New(cfg, logger())
	if err != nil {
		return false, 1
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()

	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case c := <-req:
			switch c.Cmd {
			case svc.Interrogate:
				status <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				<-done
				return false, 0
			}
		case <-done:
			cancel()
			return false, 0
		}
	}
}
