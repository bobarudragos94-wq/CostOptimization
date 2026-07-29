// Package config loads and validates the agent configuration.
//
// Secrets never live in the config file: SQL passwords are referenced by path
// to a root-only file. The SHA-256 config hash stamped into inventory excludes
// nothing — the config itself must therefore never contain secret material.
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Duration struct{ time.Duration }

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	dd, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	d.Duration = dd
	return nil
}

func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(d.String()) }

type SpikeRule struct {
	Resource        string   `yaml:"resource" json:"resource"`
	StaticThreshold float64  `yaml:"static_threshold" json:"static_threshold"`
	Sustained       Duration `yaml:"sustained" json:"sustained"`
	BaselineK       float64  `yaml:"baseline_k" json:"baseline_k"` // 0 disables baseline trigger
	BaselineFloor   float64  `yaml:"baseline_floor" json:"baseline_floor"`
}

type Config struct {
	Agent struct {
		DataDir      string `yaml:"data_dir" json:"data_dir"`
		DurationDays int    `yaml:"duration_days" json:"duration_days"`
		Recipient    string `yaml:"recipient" json:"recipient"` // age public key; agents never hold private keys
		PrivacyMode  bool   `yaml:"privacy_mode" json:"privacy_mode"`
	} `yaml:"agent" json:"agent"`

	Sampling struct {
		HostInterval        Duration `yaml:"host_interval" json:"host_interval"`
		ProcInterval        Duration `yaml:"proc_interval" json:"proc_interval"`
		ProcTopN            int      `yaml:"proc_top_n" json:"proc_top_n"`
		ProcPersistInterval Duration `yaml:"proc_persist_interval" json:"proc_persist_interval"`
		SQLSampleInterval   Duration `yaml:"sql_sample_interval" json:"sql_sample_interval"`
		SQLInventoryInterval Duration `yaml:"sql_inventory_interval" json:"sql_inventory_interval"`
		HealthInterval      Duration `yaml:"health_interval" json:"health_interval"`
		SchedInterval       Duration `yaml:"sched_interval" json:"sched_interval"`
	} `yaml:"sampling" json:"sampling"`

	Retention struct {
		MaxSpoolMB      int64 `yaml:"max_spool_mb" json:"max_spool_mb"`
		MaxAgeDays      int   `yaml:"max_age_days" json:"max_age_days"`
		KeepAfterExport bool  `yaml:"keep_after_export" json:"keep_after_export"`
	} `yaml:"retention" json:"retention"`

	Spikes struct {
		Cooldown    Duration    `yaml:"cooldown" json:"cooldown"`
		PreBuffer   Duration    `yaml:"pre_buffer" json:"pre_buffer"`
		PostCapture Duration    `yaml:"post_capture" json:"post_capture"`
		Rules       []SpikeRule `yaml:"rules" json:"rules"`
	} `yaml:"spikes" json:"spikes"`

	SQL struct {
		Enabled        bool     `yaml:"enabled" json:"enabled"`
		Auth           string   `yaml:"auth" json:"auth"` // integrated | sqllogin
		Username       string   `yaml:"username" json:"username"`
		PasswordFile   string   `yaml:"password_file" json:"password_file"` // 0600 file; contents never logged/exported
		ConnectTimeout Duration `yaml:"connect_timeout" json:"connect_timeout"`
		// CollectSQLText exists for forward compatibility but is hard-disabled
		// in v1: the collector never selects sql text regardless of its value.
		CollectSQLText bool `yaml:"collect_sql_text" json:"collect_sql_text"`
	} `yaml:"sql" json:"sql"`

	Export struct {
		AutoDaily bool   `yaml:"auto_daily" json:"auto_daily"`
		ExportDir string `yaml:"export_dir" json:"export_dir"`
	} `yaml:"export" json:"export"`
}

// Default returns the documented low-overhead defaults.
func Default() *Config {
	c := &Config{}
	c.Agent.DataDir = defaultDataDir()
	c.Agent.DurationDays = 30
	c.Sampling.HostInterval = Duration{15 * time.Second}
	c.Sampling.ProcInterval = Duration{60 * time.Second}
	c.Sampling.ProcTopN = 10
	c.Sampling.ProcPersistInterval = Duration{5 * time.Minute}
	c.Sampling.SQLSampleInterval = Duration{60 * time.Second}
	c.Sampling.SQLInventoryInterval = Duration{6 * time.Hour}
	c.Sampling.HealthInterval = Duration{5 * time.Minute}
	c.Sampling.SchedInterval = Duration{6 * time.Hour}
	c.Retention.MaxSpoolMB = 2048
	c.Retention.MaxAgeDays = 100
	c.Retention.KeepAfterExport = false
	c.Spikes.Cooldown = Duration{10 * time.Minute}
	c.Spikes.PreBuffer = Duration{5 * time.Minute}
	c.Spikes.PostCapture = Duration{3 * time.Minute}
	c.Spikes.Rules = []SpikeRule{
		{Resource: "cpu", StaticThreshold: 85, Sustained: Duration{2 * time.Minute}, BaselineK: 4, BaselineFloor: 40},
		{Resource: "memory", StaticThreshold: 90, Sustained: Duration{2 * time.Minute}, BaselineK: 5, BaselineFloor: 60},
		{Resource: "paging", StaticThreshold: 500, Sustained: Duration{1 * time.Minute}, BaselineK: 6, BaselineFloor: 100},
		{Resource: "disk_latency", StaticThreshold: 50, Sustained: Duration{1 * time.Minute}, BaselineK: 5, BaselineFloor: 10},
		{Resource: "disk_queue", StaticThreshold: 8, Sustained: Duration{1 * time.Minute}, BaselineK: 5, BaselineFloor: 2},
		{Resource: "disk_io", StaticThreshold: 95, Sustained: Duration{2 * time.Minute}, BaselineK: 4, BaselineFloor: 50},
	}
	c.SQL.Enabled = true
	c.SQL.Auth = defaultSQLAuth()
	c.SQL.ConnectTimeout = Duration{5 * time.Second}
	c.Export.AutoDaily = true
	return c
}

// Load reads YAML over the defaults and validates.
func Load(path string) (*Config, error) {
	c := Default()
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	if err := yaml.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Config) Validate() error {
	if c.Agent.DataDir == "" {
		return fmt.Errorf("agent.data_dir is required")
	}
	if c.Agent.DurationDays < 1 || c.Agent.DurationDays > 365 {
		return fmt.Errorf("agent.duration_days must be 1..365 (default 30, typical max 90)")
	}
	if c.Agent.Recipient == "" {
		return fmt.Errorf("agent.recipient (age public key) is required; run 'ura-analyzer keygen' on the analyzer machine and copy only the recipient here")
	}
	if c.Sampling.HostInterval.Duration < time.Second {
		return fmt.Errorf("sampling.host_interval must be >= 1s")
	}
	if c.Sampling.ProcTopN < 1 || c.Sampling.ProcTopN > 100 {
		return fmt.Errorf("sampling.proc_top_n must be 1..100 (bounded process cardinality)")
	}
	if c.Retention.MaxSpoolMB < 64 {
		return fmt.Errorf("retention.max_spool_mb must be >= 64")
	}
	if c.SQL.Auth != "integrated" && c.SQL.Auth != "sqllogin" {
		return fmt.Errorf("sql.auth must be integrated or sqllogin")
	}
	if c.Export.ExportDir == "" {
		c.Export.ExportDir = c.Agent.DataDir + "/export"
	}
	for _, r := range c.Spikes.Rules {
		switch r.Resource {
		case "cpu", "memory", "paging", "disk_latency", "disk_queue", "disk_io", "net":
		default:
			return fmt.Errorf("unknown spike rule resource %q", r.Resource)
		}
	}
	return nil
}

// Hash returns the SHA-256 of the canonical JSON encoding of the effective
// configuration. Stamped into inventory and manifests so the analyzer can
// prove which settings produced the data.
func (c *Config) Hash() string {
	b, _ := json.Marshal(c)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
