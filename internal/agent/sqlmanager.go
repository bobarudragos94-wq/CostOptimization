package agent

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/bobarudragos94-wq/costoptimization/internal/config"
	"github.com/bobarudragos94-wq/costoptimization/internal/model"
	"github.com/bobarudragos94-wq/costoptimization/internal/proctop"
	"github.com/bobarudragos94-wq/costoptimization/internal/sqlserver"
	"github.com/bobarudragos94-wq/costoptimization/internal/store"
)

// sqlManager tracks detected SQL Server instances, their connections and
// per-instance sampling state. Deep telemetry is optional: a detected
// instance without SQL access stays in process_only mode and the note
// "SQL Server detected; deep SQL telemetry unavailable." is exported.
type sqlManager struct {
	cfg    *config.Config
	hostID string
	health *healthTracker
	log    *slog.Logger

	mu        sync.Mutex
	instances []sqlserver.Instance
	queriers  map[string]sqlserver.Querier // instance name -> live connection
	states    map[string]*sqlserver.SampleState
	invCache  map[string]*model.SQLInstanceInventory
	invDirty  bool
	// pendingSpikes holds contexts captured live at event open, keyed by
	// event ID, until the event closes and completed jobs are merged in.
	pendingSpikes map[string]*model.SQLSpikeContext
	// sampling is a single-flight guard: SQL work runs on its own goroutine
	// so a stalled instance can never block host collection; if a round is
	// still running when the next tick fires, the tick is skipped (and the
	// skip is visible via collection health once queries time out).
	sampling atomic.Bool
}

func newSQLManager(cfg *config.Config, hostID string, health *healthTracker, log *slog.Logger) *sqlManager {
	return &sqlManager{
		cfg: cfg, hostID: hostID, health: health, log: log,
		queriers:      map[string]sqlserver.Querier{},
		states:        map[string]*sqlserver.SampleState{},
		invCache:      map[string]*model.SQLInstanceInventory{},
		pendingSpikes: map[string]*model.SQLSpikeContext{},
	}
}

// rescan re-detects instances and (re)establishes connections.
func (m *sqlManager) rescan(ctx context.Context) {
	if !m.cfg.SQL.Enabled {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.instances = sqlserver.DetectInstances()
	if len(m.instances) == 0 {
		return
	}
	for _, inst := range m.instances {
		if _, ok := m.queriers[inst.Name]; ok {
			continue
		}
		q, err := sqlserver.Connect(sqlserver.ConnectOptions{
			Instance:       inst,
			Auth:           m.cfg.SQL.Auth,
			Username:       m.cfg.SQL.Username,
			PasswordFile:   m.cfg.SQL.PasswordFile,
			ConnectTimeout: m.cfg.SQL.ConnectTimeout.Duration,
			QueryTimeout:   m.cfg.SQL.QueryTimeout.Duration,
		})
		if err != nil {
			// The required exact wording for this condition:
			m.health.Issue("sql."+inst.Name,
				"SQL Server detected; deep SQL telemetry unavailable. ("+err.Error()+")")
			inv := m.processOnlyInventory(inst)
			m.invCache[inst.Name] = inv
			m.invDirty = true
			continue
		}
		m.queriers[inst.Name] = q
		m.health.OK("sql." + inst.Name)
		if inv, err := sqlserver.CollectInventory(ctx, q, inst, m.hostID, m.issueFn(inst.Name)); err == nil {
			m.invCache[inst.Name] = inv
			m.invDirty = true
		} else {
			m.health.Issue("sql."+inst.Name, err.Error())
		}
	}
}

func (m *sqlManager) processOnlyInventory(inst sqlserver.Instance) *model.SQLInstanceInventory {
	inv := &model.SQLInstanceInventory{
		Meta:            model.NewMeta(model.KindSQLInventory, timeNow()),
		InstanceKey:     m.hostID + "|" + inst.Name,
		InstanceName:    inst.Name,
		IsDefault:       inst.Name == "MSSQLSERVER",
		PID:             inst.PID,
		LinuxMemoryLimitMB: inst.LinuxMemoryLimitMB,
		CollectionLevel: model.SQLLevelProcessOnly,
		CollectionNote:  "SQL Server detected; deep SQL telemetry unavailable.",
		LockPagesInMemory: "unknown",
		HA:              model.HAInfo{Mode: "standalone"},
	}
	return inv
}

// writeInventories persists cached inventories (refreshed on rescan).
func (m *sqlManager) writeInventories(ctx context.Context, st *store.Store) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, q := range m.queriers {
		inst := m.findInstance(name)
		if inv, err := sqlserver.CollectInventory(ctx, q, inst, m.hostID, m.issueFn(name)); err == nil {
			m.invCache[name] = inv
		}
	}
	for _, inv := range m.invCache {
		st.Append(model.KindSQLInventory, inv)
	}
	m.invDirty = false
}

// sample launches one SQL collection round on its own goroutine (single
// flight). Host collection never waits on SQL; a stalled instance costs at
// most one skipped SQL round plus a query-timeout health entry.
func (m *sqlManager) sample(ctx context.Context, st *store.Store, procs *proctop.Collector, numCPU int) {
	if !m.sampling.CompareAndSwap(false, true) {
		m.health.Issue("sql", "previous SQL sampling round still running; tick skipped (stalled instance or timeout in progress)")
		return
	}
	go func() {
		defer m.sampling.Store(false)
		m.sampleSync(ctx, st, procs, numCPU)
	}()
}

// sampleSync collects the periodic light SQL sample for each connected
// instance, filling SQL process CPU from OS-side process telemetry (cheaper
// and version-independent versus parsing the scheduler-monitor ring buffer).
func (m *sqlManager) sampleSync(ctx context.Context, st *store.Store, procs *proctop.Collector, numCPU int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.invDirty {
		for _, inv := range m.invCache {
			st.Append(model.KindSQLInventory, inv)
		}
		m.invDirty = false
	}
	for name, q := range m.queriers {
		inst := m.findInstance(name)
		key := m.hostID + "|" + name
		s, newState := sqlserver.CollectSample(ctx, q, key, m.states[name], m.issueFn(name))
		m.states[name] = newState
		if newState.CoreDenied {
			// The login lacks VIEW SERVER STATE: this is not "deep telemetry
			// with gaps", it is process-only monitoring. Rewrite the cached
			// inventory so exports carry the required wording, and stop
			// emitting empty samples.
			m.demoteToProcessOnly(name, inst)
			continue
		}
		if s != nil {
			if inst.PID != 0 {
				s.SQLProcessCPUPct = processCPUPct(procs, inst.PID)
			}
			st.Append(model.KindSQLSample, s)
		}
	}
}

// demoteToProcessOnly (caller holds m.mu) marks an instance process_only.
func (m *sqlManager) demoteToProcessOnly(name string, inst sqlserver.Instance) {
	inv := m.invCache[name]
	if inv == nil {
		inv = m.processOnlyInventory(inst)
	}
	if inv.CollectionLevel != model.SQLLevelProcessOnly {
		inv.CollectionLevel = model.SQLLevelProcessOnly
		inv.CollectionNote = "SQL Server detected; deep SQL telemetry unavailable."
		m.invDirty = true
		m.health.Issue("sql."+name,
			"SQL Server detected; deep SQL telemetry unavailable. (VIEW SERVER STATE denied)")
	}
	m.invCache[name] = inv
}

// spikeContextOpen captures SQL evidence WHILE the spike is active (detector
// OnOpen). The context is parked until the event closes.
func (m *sqlManager) spikeContextOpen(ctx context.Context, ev *model.SpikeEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	name, q := m.querierForEvent(ev)
	if q == nil {
		return
	}
	sc := sqlserver.CollectSpikeContext(ctx, q, m.hostID+"|"+name, ev.EventID,
		m.cfg.Agent.PrivacyMode, m.issueFn(name))
	if sc != nil {
		if len(m.pendingSpikes) < 32 { // bounded
			m.pendingSpikes[ev.EventID] = sc
		}
	}
}

// spikeContextClose finalizes the context at event close: a fresh Agent-job
// listing is merged so jobs that COMPLETED during the event are correlated.
func (m *sqlManager) spikeContextClose(ctx context.Context, ev *model.SpikeEvent) *model.SQLSpikeContext {
	m.mu.Lock()
	defer m.mu.Unlock()
	sc := m.pendingSpikes[ev.EventID]
	delete(m.pendingSpikes, ev.EventID)
	name, q := m.querierForEvent(ev)
	if q == nil {
		return sc
	}
	if sc == nil {
		// No live capture happened (agent restarted mid-event, or SQL was
		// connected after open): fall back to a close-time capture.
		sc = sqlserver.CollectSpikeContext(ctx, q, m.hostID+"|"+name, ev.EventID,
			m.cfg.Agent.PrivacyMode, m.issueFn(name))
	}
	jobs := sqlserver.CollectAgentJobs(ctx, q, m.cfg.Agent.PrivacyMode, m.issueFn(name))
	sqlserver.MergeJobsIntoContext(sc, jobs, ev.StartTS, ev.EndTS)
	return sc
}

// hasPending reports whether a live open-time context exists for an event.
func (m *sqlManager) hasPending(eventID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.pendingSpikes[eventID]
	return ok
}

// querierForEvent (caller holds m.mu) picks the connection for an event:
// the PID-identified instance, else the sole connection.
func (m *sqlManager) querierForEvent(ev *model.SpikeEvent) (string, sqlserver.Querier) {
	name := ev.SQLInstance
	if name == "" && len(m.queriers) == 1 {
		for n := range m.queriers {
			name = n
		}
	}
	return name, m.queriers[name]
}

// pidToInstance maps sqlservr PIDs to instance names for attribution.
func (m *sqlManager) pidToInstance() map[int32]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[int32]string{}
	for _, inst := range m.instances {
		if inst.PID != 0 {
			out[inst.PID] = inst.Name
		}
	}
	return out
}

func (m *sqlManager) findInstance(name string) sqlserver.Instance {
	for _, inst := range m.instances {
		if inst.Name == name {
			return inst
		}
	}
	return sqlserver.Instance{Name: name}
}

func (m *sqlManager) issueFn(instance string) sqlserver.Issues {
	return func(name, err string) {
		m.health.Issue("sql."+instance+"."+name, err)
	}
}

func (m *sqlManager) close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, q := range m.queriers {
		q.Close()
	}
	m.queriers = map[string]sqlserver.Querier{}
}
