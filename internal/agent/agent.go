// Package agent orchestrates all collectors, the spike detector, the local
// spool and encrypted export. It opens no sockets, spawns no network I/O and
// only reads OS/SQL interfaces.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"

	"github.com/bobarudragos94-wq/costoptimization/internal/bundle"
	"github.com/bobarudragos94-wq/costoptimization/internal/config"
	"github.com/bobarudragos94-wq/costoptimization/internal/hostid"
	"github.com/bobarudragos94-wq/costoptimization/internal/inventory"
	"github.com/bobarudragos94-wq/costoptimization/internal/model"
	"github.com/bobarudragos94-wq/costoptimization/internal/proctop"
	"github.com/bobarudragos94-wq/costoptimization/internal/sampler"
	"github.com/bobarudragos94-wq/costoptimization/internal/sched"
	"github.com/bobarudragos94-wq/costoptimization/internal/spike"
	"github.com/bobarudragos94-wq/costoptimization/internal/store"
)

type persistedState struct {
	FirstStart    time.Time `json:"first_start"`
	LastExportDay string    `json:"last_export_day"`
	// CollectedMinutes survives restarts so exported coverage reflects the
	// whole monitoring period, not just the current process lifetime.
	CollectedMinutes int `json:"collected_minutes"`
}

type Agent struct {
	cfg      *config.Config
	log      *slog.Logger
	hostID   string
	hostname string
	tz       string
	numCPU   int

	store    *store.Store
	health   *healthTracker
	sampler  *sampler.Sampler
	agg      sampler.MinuteAgg
	detector *spike.Detector
	procs    *proctop.Collector
	sqlmgr   *sqlManager

	state     persistedState
	statePath string

	lastSched     model.SchedSnapshot
	lastProcSnap  time.Time
	expectedMins  int
	collectedMins int
}

func New(cfg *config.Config, log *slog.Logger) (*Agent, error) {
	if log == nil {
		log = slog.Default()
	}
	if err := os.MkdirAll(cfg.Agent.DataDir, 0o750); err != nil {
		return nil, err
	}
	id, err := hostid.Load(cfg.Agent.DataDir)
	if err != nil {
		return nil, err
	}
	st, err := store.Open(cfg.Agent.DataDir)
	if err != nil {
		return nil, err
	}
	// The recipient must parse before we collect anything.
	if _, err := bundleRecipientCheck(cfg.Agent.Recipient); err != nil {
		return nil, err
	}
	a := &Agent{
		cfg: cfg, log: log, hostID: id,
		store:     st,
		health:    newHealthTracker(),
		detector:  spike.NewDetector(cfg),
		statePath: filepath.Join(cfg.Agent.DataDir, "state.json"),
	}
	a.sampler = sampler.New(a.health.Issue)
	a.procs = proctop.New(cfg.Sampling.ProcTopN, proctop.NewPlatformMapper(), a.health.Issue)
	a.numCPU, _ = cpu.Counts(true)
	if a.numCPU == 0 {
		a.numCPU = 1
	}
	a.sqlmgr = newSQLManager(cfg, id, a.health, log)
	a.loadState()
	a.collectedMins = a.state.CollectedMinutes
	// Live SQL evidence: capture active requests/jobs the moment a spike
	// opens, not minutes later at close. Only immutable fields of ev are read
	// (EventID, StartTS); the copy runs on its own goroutine so a slow SQL
	// instance cannot stall the sampling loop.
	a.detector.OnOpen = func(ev *model.SpikeEvent) {
		evCopy := &model.SpikeEvent{EventID: ev.EventID, StartTS: ev.StartTS, Resource: ev.Resource}
		go a.sqlmgr.spikeContextOpen(context.Background(), evCopy)
	}
	return a, nil
}

// Run is the collection main loop; returns when ctx is cancelled.
func (a *Agent) Run(ctx context.Context) error {
	a.log.Info("agent starting", "host_id", a.hostID, "version", model.AgentVersion,
		"data_dir", a.cfg.Agent.DataDir)
	if a.state.FirstStart.IsZero() {
		a.state.FirstStart = time.Now().UTC()
		a.saveState()
	}

	a.writeInventory()
	a.lastSched = sched.Snapshot(a.health.Issue)
	a.store.Append(model.KindSched, a.lastSched)
	go a.sqlmgr.rescan(ctx) // connection attempts must not delay first samples

	hostTick := time.NewTicker(a.cfg.Sampling.HostInterval.Duration)
	procTick := time.NewTicker(a.cfg.Sampling.ProcInterval.Duration)
	procPersist := time.NewTicker(a.cfg.Sampling.ProcPersistInterval.Duration)
	sqlTick := time.NewTicker(a.cfg.Sampling.SQLSampleInterval.Duration)
	sqlInvTick := time.NewTicker(a.cfg.Sampling.SQLInventoryInterval.Duration)
	healthTick := time.NewTicker(a.cfg.Sampling.HealthInterval.Duration)
	schedTick := time.NewTicker(a.cfg.Sampling.SchedInterval.Duration)
	flushTick := time.NewTicker(30 * time.Second)
	retentionTick := time.NewTicker(time.Hour)
	inventoryTick := time.NewTicker(24 * time.Hour)
	defer func() {
		for _, t := range []*time.Ticker{hostTick, procTick, procPersist, sqlTick,
			sqlInvTick, healthTick, schedTick, flushTick, retentionTick, inventoryTick} {
			t.Stop()
		}
	}()

	// Prime counters immediately.
	a.sampler.Sample()
	a.procs.Snapshot(a.numCPU)

	for {
		select {
		case <-ctx.Done():
			return a.shutdown()

		case <-hostTick.C:
			a.onHostSample()

		case <-procTick.C:
			a.procs.Snapshot(a.numCPU)
			a.health.OK("proc")

		case <-procPersist.C:
			snap := a.procs.Snapshot(a.numCPU)
			if len(snap.Procs) > 0 {
				a.append(model.KindProcTop, snap)
			}

		case <-sqlTick.C:
			a.sqlmgr.sample(ctx, a.store, a.procs, a.numCPU)

		case <-sqlInvTick.C:
			go func() { // never block host sampling on SQL round-trips
				a.sqlmgr.rescan(ctx)
				a.sqlmgr.writeInventories(ctx, a.store)
			}()

		case <-schedTick.C:
			a.lastSched = sched.Snapshot(a.health.Issue)
			a.append(model.KindSched, a.lastSched)

		case <-healthTick.C:
			a.append(model.KindHealth, a.health.Snapshot(a.store.Dropped(), a.store.SpoolBytes()))

		case <-flushTick.C:
			if err := a.store.Flush(); err != nil {
				a.health.Issue("store", err.Error())
			}
			if a.collectedMins != a.state.CollectedMinutes {
				a.state.CollectedMinutes = a.collectedMins
				a.saveState()
			}
			a.maybeAutoExport()

		case <-retentionTick.C:
			res, err := a.store.EnforceRetention(a.cfg.Retention.MaxSpoolMB<<20, a.cfg.Retention.MaxAgeDays)
			if err != nil {
				a.health.Issue("retention", err.Error())
			} else if res.DeletedSegments > 0 {
				a.log.Warn("retention removed segments", "segments", res.DeletedSegments, "bytes", res.DeletedBytes)
			}

		case <-inventoryTick.C:
			a.writeInventory()
		}
	}
}

func (a *Agent) onHostSample() {
	hr := a.sampler.Sample()
	if hr == nil {
		return
	}
	a.health.OK("host")
	a.health.AddSamples(1)

	if hm := a.agg.Add(hr); hm != nil {
		sampler.AttachFS(hm)
		a.append(model.KindHostMinute, hm)
		a.collectedMins++
	}

	// During an active spike capture, sample processes at high resolution.
	if a.detector.CaptureActive() && time.Since(a.lastProcSnap) >= 15*time.Second {
		a.procs.Snapshot(a.numCPU)
		a.lastProcSnap = time.Now()
	}

	for _, ev := range a.detector.Offer(hr.SpikePoint()) {
		a.finishSpike(ev)
	}
}

func (a *Agent) finishSpike(ev *model.SpikeEvent) {
	window := 2 * time.Minute
	snaps := a.procs.History(ev.StartTS.Add(-window), ev.EndTS.Add(window))
	attribute(ev, snaps, a.lastSched.Entries, a.numCPU, a.sqlmgr.pidToInstance(), time.Local)
	a.append(model.KindSpike, ev)
	a.log.Info("spike captured", "resource", ev.Resource, "device", ev.Device,
		"peak", ev.Peak, "duration_s", ev.DurationS, "probable_cause", ev.ProbableCause)

	if ev.SQLCorrelated || a.sqlmgr.hasPending(ev.EventID) {
		go func() { // SQL round-trips stay off the sampling loop
			if sc := a.sqlmgr.spikeContextClose(context.Background(), ev); sc != nil {
				a.append(model.KindSQLSpike, sc)
			}
		}()
	}
}

func (a *Agent) writeInventory() {
	inv, issues := inventory.Collect(a.hostID, a.cfg.Hash(), a.cfg.Agent.PrivacyMode)
	for _, is := range issues {
		a.health.Issue("inventory", is)
	}
	a.hostname = inv.Hostname
	a.tz = inv.Timezone
	a.append(model.KindInventory, inv)
}

func (a *Agent) append(kind string, rec any) {
	if err := a.store.Append(kind, rec); err != nil {
		a.health.Issue("store."+kind, err.Error())
	}
}

// maybeAutoExport exports once per UTC day shortly after midnight.
func (a *Agent) maybeAutoExport() {
	if !a.cfg.Export.AutoDaily {
		return
	}
	now := time.Now().UTC()
	day := now.Format("20060102")
	if a.state.LastExportDay == day || now.Hour() == 0 && now.Minute() < 10 {
		return
	}
	if a.state.LastExportDay == "" { // first run: skip today, export tomorrow
		a.state.LastExportDay = day
		a.saveState()
		return
	}
	path, err := a.Export()
	if err != nil {
		a.health.Issue("export", err.Error())
		return
	}
	a.state.LastExportDay = day
	a.saveState()
	a.log.Info("daily bundle exported", "path", path)
}

// Export writes an encrypted bundle of everything spooled so far.
func (a *Agent) Export() (string, error) {
	expected := 0
	if !a.state.FirstStart.IsZero() {
		expected = int(time.Since(a.state.FirstStart).Minutes())
	}
	return bundle.Export(bundle.ExportInput{
		Store:      a.store,
		Recipient:  a.cfg.Agent.Recipient,
		HostID:     a.hostID,
		Hostname:   a.hostname,
		Timezone:   a.tz,
		ConfigHash: a.cfg.Hash(),
		OutDir:     a.cfg.Export.ExportDir,
		Health: model.HealthSummary{
			ExpectedMinutes:  expected,
			CollectedMinutes: a.collectedMins,
			PermissionIssues: a.health.PermissionIssues(),
		},
		RemoveAfter: !a.cfg.Retention.KeepAfterExport,
	})
}

func (a *Agent) shutdown() error {
	a.log.Info("agent stopping")
	if hm := a.agg.FlushPartial(); hm != nil {
		a.append(model.KindHostMinute, hm)
	}
	a.append(model.KindHealth, a.health.Snapshot(a.store.Dropped(), a.store.SpoolBytes()))
	a.sqlmgr.close()
	if err := a.store.Flush(); err != nil {
		return err
	}
	return a.store.Rotate()
}

func (a *Agent) loadState() {
	b, err := os.ReadFile(a.statePath)
	if err == nil {
		json.Unmarshal(b, &a.state)
	}
}

func (a *Agent) saveState() {
	b, _ := json.Marshal(a.state)
	tmp := a.statePath + ".tmp"
	if os.WriteFile(tmp, b, 0o640) == nil {
		os.Rename(tmp, a.statePath)
	}
}

func bundleRecipientCheck(recipient string) (string, error) {
	if recipient == "" {
		return "", fmt.Errorf("agent.recipient missing")
	}
	return recipient, nil
}

// HostID exposes the stable host identifier (CLI diagnostics).
func (a *Agent) HostID() string { return a.hostID }
