package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeCfg(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "agent.yaml")
	os.WriteFile(p, []byte(body), 0o600)
	return p
}

func TestLoadDefaultsAndOverride(t *testing.T) {
	p := writeCfg(t, `
agent:
  data_dir: /tmp/x
  recipient: age1abc
sampling:
  host_interval: 30s
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Sampling.HostInterval.Duration != 30*time.Second {
		t.Fatalf("override lost: %v", c.Sampling.HostInterval)
	}
	if c.Sampling.ProcTopN != 10 || c.Agent.DurationDays != 30 {
		t.Fatalf("defaults lost: %+v", c)
	}
	if len(c.Spikes.Rules) < 5 {
		t.Fatalf("default spike rules missing")
	}
}

func TestValidationErrors(t *testing.T) {
	cases := []string{
		"agent:\n  data_dir: /tmp/x\n", // no recipient
		"agent:\n  data_dir: /tmp/x\n  recipient: age1abc\nsampling:\n  proc_top_n: 5000\n",
		"agent:\n  data_dir: /tmp/x\n  recipient: age1abc\nsql:\n  auth: cleartext\n",
		"agent:\n  data_dir: /tmp/x\n  recipient: age1abc\nspikes:\n  rules:\n    - resource: gpu\n",
	}
	for i, body := range cases {
		if _, err := Load(writeCfg(t, body)); err == nil {
			t.Errorf("case %d should fail validation", i)
		}
	}
}

func TestHashStableAndSensitive(t *testing.T) {
	p := writeCfg(t, "agent:\n  data_dir: /tmp/x\n  recipient: age1abc\n")
	c1, _ := Load(p)
	c2, _ := Load(p)
	if c1.Hash() != c2.Hash() {
		t.Fatal("hash must be deterministic")
	}
	p3 := writeCfg(t, "agent:\n  data_dir: /tmp/y\n  recipient: age1abc\n")
	c3, _ := Load(p3)
	if c1.Hash() == c3.Hash() {
		t.Fatal("hash must change with config")
	}
}
