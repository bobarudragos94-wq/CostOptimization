// ura-agent: the on-server collector of the Utilization & Rightsizing
// Analyzer. Runs as a Windows Service or under systemd on Linux; opens no
// listening ports and performs no network I/O (SQL connections are loopback
// to local instances only).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/bobarudragos94-wq/costoptimization/internal/agent"
	"github.com/bobarudragos94-wq/costoptimization/internal/config"
	"github.com/bobarudragos94-wq/costoptimization/internal/inventory"
	"github.com/bobarudragos94-wq/costoptimization/internal/model"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	cfgPath := fs.String("config", defaultConfigPath(), "path to agent.yaml")
	fs.Parse(os.Args[2:])

	switch cmd {
	case "version":
		fmt.Printf("ura-agent %s (schema %s)\n", model.AgentVersion, model.SchemaVersion)
	case "run":
		runService(*cfgPath) // service-aware on Windows, plain loop elsewhere
	case "export":
		withAgent(*cfgPath, func(a *agent.Agent) error {
			path, err := a.Export()
			if err != nil {
				return err
			}
			fmt.Println(path)
			return nil
		})
	case "inventory":
		cfg := mustConfig(*cfgPath)
		inv, issues := inventory.Collect("(preview)", cfg.Hash(), cfg.Agent.PrivacyMode)
		out, _ := json.MarshalIndent(inv, "", "  ")
		fmt.Println(string(out))
		for _, is := range issues {
			fmt.Fprintln(os.Stderr, "warn:", is)
		}
	case "check-config":
		cfg := mustConfig(*cfgPath)
		fmt.Printf("config OK (hash %s)\n", cfg.Hash()[:16])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: ura-agent <command> [--config path]

commands:
  run           start collection (foreground; service-aware)
  export        export an encrypted bundle now
  inventory     print the host inventory snapshot (diagnostics)
  check-config  validate the configuration file
  version       print version`)
}

func mustConfig(path string) *config.Config {
	cfg, err := config.Load(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	return cfg
}

func withAgent(cfgPath string, fn func(*agent.Agent) error) {
	cfg := mustConfig(cfgPath)
	a, err := agent.New(cfg, logger())
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if err := fn(a); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// runForeground runs the collection loop until SIGINT/SIGTERM.
func runForeground(cfgPath string) {
	withAgent(cfgPath, func(a *agent.Agent) error {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return a.Run(ctx)
	})
}

func logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
}
