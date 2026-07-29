// Package model is the single source of truth for the versioned record schema
// shared by the agent, the bundle format and the analyzer.
//
// Every record persisted to a segment carries Meta{Kind, Schema, TS}. TS is
// always UTC; host timezone is preserved in the Inventory record. Breaking
// changes require bumping SchemaVersion and a note in docs/DATA_DICTIONARY.md.
package model

import "time"

// SchemaVersion identifies the record schema. Analyzer accepts equal major
// versions and reports (not fails) on unknown newer minor versions.
const SchemaVersion = "1.0.0"

// AgentVersion is stamped into inventory, health and manifests.
const AgentVersion = "0.1.0"

// Record kinds. Each segment file contains records of exactly one kind.
const (
	KindInventory    = "inventory"
	KindHostMinute   = "host_minute"
	KindProcTop      = "proc_top"
	KindSpike        = "spike_event"
	KindSched        = "sched_snapshot"
	KindSQLInventory = "sql_inventory"
	KindSQLSample    = "sql_sample"
	KindSQLSpike     = "sql_spike_context"
	KindHealth       = "health"
)

// AllKinds lists every valid segment kind.
var AllKinds = []string{
	KindInventory, KindHostMinute, KindProcTop, KindSpike, KindSched,
	KindSQLInventory, KindSQLSample, KindSQLSpike, KindHealth,
}

// Meta is embedded in every record.
type Meta struct {
	Kind   string    `json:"kind"`
	Schema string    `json:"schema"`
	TS     time.Time `json:"ts"`
}

func NewMeta(kind string, ts time.Time) Meta {
	return Meta{Kind: kind, Schema: SchemaVersion, TS: ts.UTC()}
}

// ---------------------------------------------------------------------------
// Inventory
// ---------------------------------------------------------------------------

type DiskInfo struct {
	Device    string `json:"device"`
	Model     string `json:"model,omitempty"`
	SizeBytes uint64 `json:"size_bytes,omitempty"`
}

type FSInfo struct {
	Mount      string `json:"mount"`
	Device     string `json:"device"`
	FSType     string `json:"fstype"`
	TotalBytes uint64 `json:"total_bytes"`
	AvailBytes uint64 `json:"avail_bytes"`
}

type NICInfo struct {
	Name      string `json:"name"`
	MAC       string `json:"mac,omitempty"`
	SpeedMbps int64  `json:"speed_mbps,omitempty"` // 0 = unknown
	Addrs     []string `json:"addrs,omitempty"`
}

// Inventory is the versioned host inventory snapshot.
type Inventory struct {
	Meta
	HostID         string     `json:"host_id"`
	Hostname       string     `json:"hostname"`
	OS             string     `json:"os"` // linux | windows
	OSVersion      string     `json:"os_version"`
	KernelBuild    string     `json:"kernel_build"`
	BootTime       time.Time  `json:"boot_time"`
	UptimeS        uint64     `json:"uptime_s"`
	Timezone       string     `json:"timezone"`
	TZOffsetSec    int        `json:"tz_offset_sec"`
	Virtualization string     `json:"virtualization"` // physical | vm:<hint> | unknown
	CPUModel       string     `json:"cpu_model"`
	PhysicalCores  int        `json:"physical_cores"`
	LogicalCPUs    int        `json:"logical_cpus"`
	AllocatedVCPU  int        `json:"allocated_vcpu"` // in-guest visible logical CPUs (see docs/LIMITATIONS.md)
	TotalRAMBytes  uint64     `json:"total_ram_bytes"`
	SwapTotalBytes uint64     `json:"swap_total_bytes"`
	Disks          []DiskInfo `json:"disks"`
	Filesystems    []FSInfo   `json:"filesystems"`
	NICs           []NICInfo  `json:"nics"`
	AgentVersion   string     `json:"agent_version"`
	ConfigHash     string     `json:"config_hash"`
	PrivacyMode    bool       `json:"privacy_mode"`
}

// ---------------------------------------------------------------------------
// Host minute aggregates
// ---------------------------------------------------------------------------

type CPUAgg struct {
	AvgPct       float64   `json:"avg_pct"`
	MaxPct       float64   `json:"max_pct"`
	UserPct      float64   `json:"user_pct"`
	SystemPct    float64   `json:"system_pct"`
	IOWaitPct    float64   `json:"iowait_pct"`
	StealPct     float64   `json:"steal_pct"`
	PerCoreMax   []float64 `json:"per_core_max,omitempty"`
	Load1        float64   `json:"load1"`
	Load5        float64   `json:"load5"`
	RunQueue     float64   `json:"run_queue,omitempty"` // processor queue length on Windows
	CtxSwitchPS  float64   `json:"ctx_switch_ps,omitempty"`
}

type MemAgg struct {
	UsedPctAvg     float64 `json:"used_pct_avg"`
	UsedPctMax     float64 `json:"used_pct_max"`
	UsedBytesAvg   uint64  `json:"used_bytes_avg"`
	AvailBytesMin  uint64  `json:"avail_bytes_min"`
	CommittedBytes uint64  `json:"committed_bytes,omitempty"`
	SwapUsedBytes  uint64  `json:"swap_used_bytes"`
	PageInPS       float64 `json:"page_in_ps"`  // pages/s swapped or hard-faulted in
	PageOutPS      float64 `json:"page_out_ps"` // pages/s written out
	MajorFaultPS   float64 `json:"major_fault_ps,omitempty"`
}

type DiskAgg struct {
	Device       string  `json:"device"`
	ReadIOPS     float64 `json:"read_iops"`
	WriteIOPS    float64 `json:"write_iops"`
	ReadBytesPS  float64 `json:"read_bytes_ps"`
	WriteBytesPS float64 `json:"write_bytes_ps"`
	ReadLatMS    float64 `json:"read_lat_ms"`
	WriteLatMS   float64 `json:"write_lat_ms"`
	QueueDepth   float64 `json:"queue_depth"`
	BusyPct      float64 `json:"busy_pct"`
}

type FSUsage struct {
	Mount      string `json:"mount"`
	TotalBytes uint64 `json:"total_bytes"`
	AvailBytes uint64 `json:"avail_bytes"`
}

type NetAgg struct {
	Name        string  `json:"name"`
	RxBytesPS   float64 `json:"rx_bytes_ps"`
	TxBytesPS   float64 `json:"tx_bytes_ps"`
	RxPktsPS    float64 `json:"rx_pkts_ps"`
	TxPktsPS    float64 `json:"tx_pkts_ps"`
	ErrsPS      float64 `json:"errs_ps"`
	DropsPS     float64 `json:"drops_ps"`
	UtilPct     float64 `json:"util_pct,omitempty"` // when link speed known
}

// PSIAgg carries Linux Pressure Stall Information (avg10 sampled, minute avg/max).
type PSIAgg struct {
	CPUSomeAvg  float64 `json:"cpu_some_avg"`
	MemSomeAvg  float64 `json:"mem_some_avg"`
	MemFullAvg  float64 `json:"mem_full_avg"`
	IOSomeAvg   float64 `json:"io_some_avg"`
	IOFullAvg   float64 `json:"io_full_avg"`
	MemSomeMax  float64 `json:"mem_some_max"`
	IOSomeMax   float64 `json:"io_some_max"`
}

// HostMinute is the long-term persisted aggregate of high-resolution samples.
type HostMinute struct {
	Meta
	Samples int       `json:"samples"` // high-res samples aggregated into this minute
	CPU     CPUAgg    `json:"cpu"`
	Mem     MemAgg    `json:"mem"`
	Disks   []DiskAgg `json:"disks,omitempty"`
	FS      []FSUsage `json:"fs,omitempty"`
	Net     []NetAgg  `json:"net,omitempty"`
	PSI     *PSIAgg   `json:"psi,omitempty"`
}

// ---------------------------------------------------------------------------
// Processes
// ---------------------------------------------------------------------------

type ProcSample struct {
	PID          int32     `json:"pid"`
	PPID         int32     `json:"ppid"`
	Name         string    `json:"name"`
	Exe          string    `json:"exe,omitempty"` // executable path only; never command-line args
	ExeSHA256    string    `json:"exe_sha256,omitempty"`
	ServiceUnit  string    `json:"service_unit,omitempty"` // systemd unit or Windows service name
	CPUPct       float64   `json:"cpu_pct"`                // of total host capacity
	CPUTimeS     float64   `json:"cpu_time_s"`             // cumulative process CPU seconds
	RSSBytes     uint64    `json:"rss_bytes"`
	PrivateBytes uint64    `json:"private_bytes,omitempty"`
	ReadBytesPS  float64   `json:"read_bytes_ps,omitempty"`
	WriteBytesPS float64   `json:"write_bytes_ps,omitempty"`
	StartTime    time.Time `json:"start_time,omitempty"`
}

// ProcTop is a bounded top-N process snapshot.
type ProcTop struct {
	Meta
	Procs []ProcSample `json:"procs"`
}

// ---------------------------------------------------------------------------
// Scheduled work correlation
// ---------------------------------------------------------------------------

type SchedEntry struct {
	Source   string    `json:"source"` // cron | systemd_timer | win_task | sql_agent
	Name     string    `json:"name"`
	Schedule string    `json:"schedule,omitempty"`
	Unit     string    `json:"unit,omitempty"` // triggered systemd unit / task action image name
	LastRun  time.Time `json:"last_run,omitempty"`
	NextRun  time.Time `json:"next_run,omitempty"`
}

type SchedSnapshot struct {
	Meta
	Entries []SchedEntry `json:"entries"`
}

// ---------------------------------------------------------------------------
// Spike events
// ---------------------------------------------------------------------------

type MetricPoint struct {
	TS    time.Time `json:"ts"`
	Value float64   `json:"v"`
}

// Spike resources.
const (
	ResCPU         = "cpu"
	ResMemory      = "memory"
	ResPaging      = "paging"
	ResDiskLatency = "disk_latency"
	ResDiskQueue   = "disk_queue"
	ResDiskIO      = "disk_io"
	ResNet         = "net"
)

// Correlation levels used for SQL and job attribution.
const (
	CorrProcessOnly        = "process_only"
	CorrAgentJob           = "agent_job"
	CorrActiveRequest      = "active_request"
	CorrScheduledConfirmed = "scheduled_job_confirmed"
	CorrInsufficient       = "insufficient_evidence"
)

type SpikeEvent struct {
	Meta
	EventID   string    `json:"event_id"`
	Resource  string    `json:"resource"`
	Device    string    `json:"device,omitempty"`
	StartTS   time.Time `json:"start_ts"`
	EndTS     time.Time `json:"end_ts"`
	DurationS float64   `json:"duration_s"`
	Baseline  float64   `json:"baseline"`
	Peak      float64   `json:"peak"`
	Threshold float64   `json:"threshold"`
	Trigger   string    `json:"trigger"` // static | baseline_deviation

	PreSamples    []MetricPoint `json:"pre_samples,omitempty"`
	DuringSamples []MetricPoint `json:"during_samples,omitempty"`
	PostSamples   []MetricPoint `json:"post_samples,omitempty"`

	TopProcs          []ProcSample       `json:"top_procs,omitempty"`
	CoreSecondsByProc map[string]float64 `json:"core_seconds_by_proc,omitempty"`

	ProbableCause          string   `json:"probable_cause"`
	CorrelatedProcess      string   `json:"correlated_process,omitempty"`
	CorrelatedServiceOrJob string   `json:"correlated_service_or_job,omitempty"`
	AttributionConfidence  float64  `json:"attribution_confidence"` // 0..1, capped below certainty
	AttributionEvidence    []string `json:"attribution_evidence,omitempty"`
	ValidationRequired     bool     `json:"validation_required"`

	SQLCorrelated bool   `json:"sql_correlated,omitempty"`
	SQLInstance   string `json:"sql_instance,omitempty"`
}

// ---------------------------------------------------------------------------
// SQL Server
// ---------------------------------------------------------------------------

// SQL collection levels.
const (
	SQLLevelDeep        = "deep"
	SQLLevelProcessOnly = "process_only" // "SQL Server detected; deep SQL telemetry unavailable."
)

type SQLDatabase struct {
	Name       string  `json:"name"`
	SizeMB     float64 `json:"size_mb"`
	LogSizeMB  float64 `json:"log_size_mb,omitempty"`
	State      string  `json:"state,omitempty"`
}

type AGInfo struct {
	AGName            string   `json:"ag_name"`
	Role              string   `json:"role"` // PRIMARY | SECONDARY | RESOLVING
	SyncState         string   `json:"sync_state,omitempty"`
	ReadableSecondary string   `json:"readable_secondary,omitempty"`
	PartnerReplicas   []string `json:"partner_replicas,omitempty"` // server names of other replicas
}

type HAInfo struct {
	Mode string   `json:"mode"` // standalone | fci | alwayson_ag
	AGs  []AGInfo `json:"ags,omitempty"`
}

type SQLInstanceInventory struct {
	Meta
	InstanceKey  string `json:"instance_key"` // hostid|instance-name, stable across records
	MachineName  string `json:"machine_name"`
	InstanceName string `json:"instance_name"` // MSSQLSERVER for default
	IsDefault    bool   `json:"is_default"`

	Version      string    `json:"version"`
	Build        string    `json:"build"`
	ProductLevel string    `json:"product_level,omitempty"`
	Edition      string    `json:"edition"`
	StartTime    time.Time `json:"start_time"`
	UptimeS      uint64    `json:"uptime_s"`

	CPUCountVisible  int    `json:"cpu_count_visible"`
	SchedulerCount   int    `json:"scheduler_count,omitempty"`
	PhysMemVisibleKB uint64 `json:"phys_mem_visible_kb"`
	PID              int32  `json:"pid"`
	ServiceAccount   string `json:"service_account,omitempty"`

	MaxDOP                    int    `json:"maxdop"`
	CostThreshold             int    `json:"cost_threshold,omitempty"`
	MinServerMemoryMB         uint64 `json:"min_server_memory_mb"`
	MaxServerMemoryMB         uint64 `json:"max_server_memory_mb"`
	MaxMemoryUnlimitedDefault bool   `json:"max_memory_unlimited_default"`
	LockPagesInMemory         string `json:"lock_pages_in_memory"` // detected | not_detected | unknown
	LinuxMemoryLimitMB        uint64 `json:"linux_memory_limit_mb,omitempty"`

	HA        HAInfo        `json:"ha"`
	Databases []SQLDatabase `json:"databases,omitempty"`

	CollectionLevel string `json:"collection_level"` // deep | process_only
	CollectionNote  string `json:"collection_note,omitempty"`
}

type WaitDelta struct {
	WaitMS float64 `json:"wait_ms"`
	Count  float64 `json:"count"`
}

type DBFileIO struct {
	Database  string  `json:"database"`
	FileType  string  `json:"file_type"` // data | log
	ReadLatMS float64 `json:"read_lat_ms"`
	WriteLatMS float64 `json:"write_lat_ms"`
	ReadPS    float64 `json:"read_ps"`
	WritePS   float64 `json:"write_ps"`
}

// SQLSample is the periodic light-weight memory + workload sample.
type SQLSample struct {
	Meta
	InstanceKey string `json:"instance_key"`

	// From sys.dm_os_process_memory
	PhysicalMemoryInUseKB    uint64 `json:"physical_memory_in_use_kb"`
	LockedPageAllocKB        uint64 `json:"locked_page_allocations_kb"`
	LargePageAllocKB         uint64 `json:"large_page_allocations_kb"`
	MemoryUtilizationPct     int    `json:"memory_utilization_pct"`
	ProcessPhysicalMemoryLow bool   `json:"process_physical_memory_low"`
	ProcessVirtualMemoryLow  bool   `json:"process_virtual_memory_low"`

	// From sys.dm_os_performance_counters (memory manager)
	TotalServerMemoryKB  uint64 `json:"total_server_memory_kb"`
	TargetServerMemoryKB uint64 `json:"target_server_memory_kb"`
	DatabaseCacheKB      uint64 `json:"database_cache_kb"`
	StolenServerMemoryKB uint64 `json:"stolen_server_memory_kb"`
	FreeMemoryKB         uint64 `json:"free_memory_kb"`
	MemoryGrantsPending  int64  `json:"memory_grants_pending"`
	MemoryGrantsActive   int64  `json:"memory_grants_active"`
	PLESeconds           int64  `json:"ple_seconds"`

	// Top memory clerks (KB), bounded to major categories.
	MemoryClerksTopKB map[string]uint64 `json:"memory_clerks_top_kb,omitempty"`

	// Workload counters (rates computed from counter deltas by the collector).
	BatchRequestsPS   float64 `json:"batch_requests_ps"`
	CompilationsPS    float64 `json:"compilations_ps"`
	RecompilationsPS  float64 `json:"recompilations_ps"`
	SQLProcessCPUPct  float64 `json:"sql_process_cpu_pct"`
	PageReadsPS       float64 `json:"page_reads_ps"`
	PageWritesPS      float64 `json:"page_writes_ps"`
	LazyWritesPS      float64 `json:"lazy_writes_ps"`
	CheckpointPagesPS float64 `json:"checkpoint_pages_ps"`

	WaitDeltas      map[string]WaitDelta `json:"wait_deltas,omitempty"` // top waits since previous sample
	BlockedSessions int                  `json:"blocked_sessions"`
	FileIO          []DBFileIO           `json:"file_io,omitempty"`
	TempdbUsedMB    float64              `json:"tempdb_used_mb,omitempty"`
	TempdbTotalMB   float64              `json:"tempdb_total_mb,omitempty"`
}

type SQLActiveRequest struct {
	SessionID       int    `json:"session_id"`
	RequestID       int    `json:"request_id"`
	Database        string `json:"database"`
	Command         string `json:"command"`
	Status          string `json:"status"`
	CPUTimeMS       int64  `json:"cpu_time_ms"`
	ElapsedMS       int64  `json:"elapsed_ms"`
	LogicalReads    int64  `json:"logical_reads"`
	PhysicalReads   int64  `json:"physical_reads"`
	Writes          int64  `json:"writes"`
	WaitType        string `json:"wait_type,omitempty"`
	BlockingSession int    `json:"blocking_session,omitempty"`
	ProgramName     string `json:"program_name,omitempty"`
	QueryHash       string `json:"query_hash,omitempty"`
	PlanHash        string `json:"plan_hash,omitempty"`
	// Raw SQL text is intentionally not part of the v1 schema (see threat model).
}

type SQLAgentJobRun struct {
	JobName   string    `json:"job_name"` // pseudonymized in privacy mode
	StartTime time.Time `json:"start_time"`
	DurationS float64   `json:"duration_s,omitempty"`
	Outcome   string    `json:"outcome,omitempty"` // running | succeeded | failed | unknown
	Running   bool      `json:"running"`
}

// SQLSpikeContext attaches SQL-level evidence to a host spike event.
type SQLSpikeContext struct {
	Meta
	EventID          string             `json:"event_id"`
	InstanceKey      string             `json:"instance_key"`
	ActiveRequests   []SQLActiveRequest `json:"active_requests,omitempty"`
	AgentJobs        []SQLAgentJobRun   `json:"agent_jobs,omitempty"`
	CorrelationLevel string             `json:"correlation_level"`
}

// ---------------------------------------------------------------------------
// Collection health
// ---------------------------------------------------------------------------

type CollectorError struct {
	TS        time.Time `json:"ts"`
	Collector string    `json:"collector"`
	Error     string    `json:"error"`
}

type Health struct {
	Meta
	AgentVersion     string            `json:"agent_version"`
	AgentUptimeS     uint64            `json:"agent_uptime_s"`
	SamplesCollected uint64            `json:"samples_collected"`
	SamplesDropped   uint64            `json:"samples_dropped"`
	CollectorStatus  map[string]string `json:"collector_status"` // collector -> ok|degraded:<why>|unavailable:<why>
	PermissionIssues []string          `json:"permission_issues,omitempty"`
	RecentErrors     []CollectorError  `json:"recent_errors,omitempty"` // bounded
	SpoolBytes       uint64            `json:"spool_bytes"`
	SelfRSSBytes     uint64            `json:"self_rss_bytes"`
	SelfCPUPct       float64           `json:"self_cpu_pct"`
}

// ---------------------------------------------------------------------------
// Bundle manifest
// ---------------------------------------------------------------------------

type TimeRange struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

type SegmentMeta struct {
	Name      string    `json:"name"` // path inside bundle, e.g. segments/seg-host_minute-20260701-000.ndjson.gz.age
	Kind      string    `json:"kind"`
	SHA256    string    `json:"sha256"` // hex digest of the ciphertext as stored in the bundle
	SizeBytes int64     `json:"size_bytes"`
	Records   int       `json:"records"`
	MinTS     time.Time `json:"min_ts"`
	MaxTS     time.Time `json:"max_ts"`
}

type HealthSummary struct {
	ExpectedMinutes  int         `json:"expected_minutes"`
	CollectedMinutes int         `json:"collected_minutes"`
	Gaps             []TimeRange `json:"gaps,omitempty"`
	PermissionIssues []string    `json:"permission_issues,omitempty"`
}

// Manifest describes one exported bundle. It is itself encrypted inside the
// bundle; segment hashes are over ciphertext so integrity can be checked
// before any segment decryption.
type Manifest struct {
	SchemaVersion string        `json:"schema_version"`
	AgentVersion  string        `json:"agent_version"`
	BundleID      string        `json:"bundle_id"`
	HostID        string        `json:"host_id"`
	Hostname      string        `json:"hostname"`
	Timezone      string        `json:"timezone"`
	CreatedAt     time.Time     `json:"created_at"`
	PeriodStart   time.Time     `json:"period_start"`
	PeriodEnd     time.Time     `json:"period_end"`
	ConfigHash    string        `json:"config_hash"`
	Segments      []SegmentMeta `json:"segments"`
	Health        HealthSummary `json:"health"`
}
