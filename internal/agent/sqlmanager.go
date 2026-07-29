package agent

import (
	"context"
	"log/slog"
	"sync"

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
}

func newSQLManager(cfg *config.Config, hostID string, health *healthTracker, log *slog.Logger) *sqlManager {
	return &sqlManager{
		cfg: cfg, hostID: hostID, health: health, log: log,
		queriers: map[string]sqlserver.Querier{},
		states:   map[string]*sqlserver.SampleState{},
		invCache: map[string]*model.SQLInstanceInventory{},
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

// sample collects the periodic light SQL sample for each connected instance,
// filling SQL process CPU from OS-side process telemetry (cheaper and
// version-independent versus parsing the scheduler-monitor ring buffer).
func (m *sqlManager) sample(ctx context.Context, st *store.Store, procs *proctop.Collector, numCPU int) {
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
		if s != nil {
			if inst.PID != 0 {
				s.SQLProcessCPUPct = processCPUPct(procs, inst.PID)
			}
			st.Append(model.KindSQLSample, s)
		}
	}
}

// spikeContext collects SQL-level evidence for a SQL-correlated host spike.
func (m *sqlManager) spikeContext(ctx context.Context, ev *model.SpikeEvent) *model.SQLSpikeContext {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Prefer the instance identified by PID; otherwise the sole connection.
	name := ""
	if ev.SQLInstance != "" {
		name = ev.SQLInstance
	} else if len(m.queriers) == 1 {
		for n := range m.queriers {
			name = n
		}
	}
	q, ok := m.queriers[name]
	if !ok {
		return nil
	}
	return sqlserver.CollectSpikeContext(ctx, q, m.hostID+"|"+name, ev.EventID,
		m.cfg.Agent.PrivacyMode, m.issueFn(name))
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
