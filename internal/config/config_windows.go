//go:build windows

package config

func defaultDataDir() string { return `C:\ProgramData\ura-agent` }

// On Windows the agent service account authenticates to SQL Server with
// integrated authentication — no stored credentials at all.
func defaultSQLAuth() string { return "integrated" }
