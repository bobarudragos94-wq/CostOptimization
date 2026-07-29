//go:build linux

package proctop

import "testing"

func TestUnitFromCgroup(t *testing.T) {
	cases := []struct {
		content, want string
	}{
		{"0::/system.slice/cron.service\n", "cron.service"},
		{"0::/system.slice/system-getty.slice/getty@tty1.service\n", "getty@tty1.service"},
		{"12:cpu,cpuacct:/system.slice/mssql-server.service\n0::/init.scope\n", "mssql-server.service"},
		{"0::/user.slice/user-1000.slice/session-3.scope\n", "session-3.scope"},
		{"0::/\n", ""},
	}
	for _, c := range cases {
		if got := UnitFromCgroup(c.content); got != c.want {
			t.Errorf("UnitFromCgroup(%q) = %q, want %q", c.content, got, c.want)
		}
	}
}
