//go:build !windows

package config

func defaultDataDir() string { return "/var/lib/ura-agent" }

// Linux has no ambient integrated auth for SQL Server; a dedicated read-only
// SQL login is the default there.
func defaultSQLAuth() string { return "sqllogin" }
