package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testRecipient is a throwaway but structurally valid age public key.
const testRecipient = "age1atpm63qwfhmtg9l5c4mvu0rv7s7p5hktzz7kydduw5t2vzf6qc2qsa55z9"

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
  recipient: age1atpm63qwfhmtg9l5c4mvu0rv7s7p5hktzz7kydduw5t2vzf6qc2qsa55z9
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
		"agent:\n  data_dir: /tmp/x\n  recipient: age1atpm63qwfhmtg9l5c4mvu0rv7s7p5hktzz7kydduw5t2vzf6qc2qsa55z9\nsampling:\n  proc_top_n: 5000\n",
		"agent:\n  data_dir: /tmp/x\n  recipient: age1atpm63qwfhmtg9l5c4mvu0rv7s7p5hktzz7kydduw5t2vzf6qc2qsa55z9\nsql:\n  auth: cleartext\n",
		"agent:\n  data_dir: /tmp/x\n  recipient: age1atpm63qwfhmtg9l5c4mvu0rv7s7p5hktzz7kydduw5t2vzf6qc2qsa55z9\nspikes:\n  rules:\n    - resource: gpu\n",
	}
	for i, body := range cases {
		if _, err := Load(writeCfg(t, body)); err == nil {
			t.Errorf("case %d should fail validation", i)
		}
	}
}

func TestRecipientValidation(t *testing.T) {
	// Placeholder from the example config must be rejected.
	if _, err := Load(writeCfg(t, "agent:\n  data_dir: /tmp/x\n  recipient: \"age1REPLACE_ME\"\n")); err == nil {
		t.Fatal("template placeholder recipient must be rejected")
	} else if !strings.Contains(err.Error(), "placeholder") {
		t.Fatalf("error should name the placeholder problem: %v", err)
	}
	// Structurally invalid keys must be rejected.
	for _, bad := range []string{"age1abc", "not-a-key", "AGE-SECRET-KEY-1SHOULDNEVERBEHERE"} {
		if _, err := Load(writeCfg(t, "agent:\n  data_dir: /tmp/x\n  recipient: \""+bad+"\"\n")); err == nil {
			t.Errorf("invalid recipient %q must be rejected", bad)
		}
	}
	// A valid key with surrounding whitespace is accepted and trimmed.
	c, err := Load(writeCfg(t, "agent:\n  data_dir: /tmp/x\n  recipient: \"  "+testRecipient+" \"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Agent.Recipient != testRecipient {
		t.Fatalf("recipient not trimmed: %q", c.Agent.Recipient)
	}
}

func TestQueryTimeoutValidation(t *testing.T) {
	if _, err := Load(writeCfg(t, "agent:\n  data_dir: /tmp/x\n  recipient: "+testRecipient+"\nsql:\n  query_timeout: 1ms\n")); err == nil {
		t.Fatal("sub-second query timeout must be rejected")
	}
	c, err := Load(writeCfg(t, "agent:\n  data_dir: /tmp/x\n  recipient: "+testRecipient+"\n"))
	if err != nil || c.SQL.QueryTimeout.Duration != 10*time.Second {
		t.Fatalf("default query timeout: %v err=%v", c.SQL.QueryTimeout, err)
	}
}

func TestHashStableAndSensitive(t *testing.T) {
	p := writeCfg(t, "agent:\n  data_dir: /tmp/x\n  recipient: age1atpm63qwfhmtg9l5c4mvu0rv7s7p5hktzz7kydduw5t2vzf6qc2qsa55z9\n")
	c1, _ := Load(p)
	c2, _ := Load(p)
	if c1.Hash() != c2.Hash() {
		t.Fatal("hash must be deterministic")
	}
	p3 := writeCfg(t, "agent:\n  data_dir: /tmp/y\n  recipient: age1atpm63qwfhmtg9l5c4mvu0rv7s7p5hktzz7kydduw5t2vzf6qc2qsa55z9\n")
	c3, _ := Load(p3)
	if c1.Hash() == c3.Hash() {
		t.Fatal("hash must change with config")
	}
}
