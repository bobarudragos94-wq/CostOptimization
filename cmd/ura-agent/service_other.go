//go:build !windows

package main

func defaultConfigPath() string { return "/etc/ura-agent/agent.yaml" }

// runService on Linux: systemd runs us as a plain foreground process
// (Type=simple); SIGTERM triggers a clean shutdown.
func runService(cfgPath string) { runForeground(cfgPath) }
