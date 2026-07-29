# URA — Utilization & Rightsizing Analyzer: MVP Plan

**Status: MVP complete (M1–M7). The remaining follow-up is M8 — real-Windows and live-SQL-Server validation. See the checklist at the bottom.**

This document is the durable plan of record. Any agent or engineer continuing this work
should read this file first, then `docs/ARCHITECTURE.md`.

---

## 1. Interpretation of the requirements

Build a two-part, fully offline product:

1. **Agent** (`ura-agent`) — deployed temporarily (30–90 days) on Windows and Linux
   servers. Collects low-overhead host telemetry (CPU/memory/disk/network), detects and
   captures resource spikes with process/service/job context, detects Microsoft SQL
   Server and (optionally, read-only) collects SQL memory/workload/HA telemetry. Spools
   everything locally in compressed, corruption-isolated daily segments and exports
   **encrypted bundles**. No listening ports, no network I/O, never modifies the system.
2. **Analyzer** (`ura-analyzer`) — run on the consultant's offline workstation. Imports
   many encrypted bundles at once, verifies integrity, tolerates damaged segments,
   consolidates hosts, groups SQL instances and HA replicas, computes percentiles /
   sustained utilization / recurring spike patterns, and produces machine-readable
   (JSON) and human-readable (Markdown + HTML) rightsizing reports with explicit
   recommendation categories, confidence and validation-required flags.

The customer being investigated keeps control: bundles are encrypted to a public key
whose **private half never touches the monitored servers**. The product makes *technical*
capacity recommendations only (no cost/SKU math in v1).

## 2. Assumptions

| # | Assumption | Rationale |
|---|-----------|-----------|
| A1 | Development/CI environment is Linux; Windows code is cross-compiled (`GOOS=windows`) and compile-verified but not executed here. Windows-only paths (service wrapper, registry discovery, SCM service mapping) are exercised via unit-testable seams and must get a smoke test on a real Windows host before production use. | No Windows machine in this environment. |
| B1 | No live SQL Server is available here. The SQL collector is written against a narrow `Querier` seam and tested with DMV fixture data. Queries target SQL Server 2016+ DMVs (documented per-version notes in `docs/SQL_PERMISSIONS.md`). | Offline dev environment. |
| C1 | Sampling defaults: host metrics every 15 s (aggregated to 1-minute persisted records), top-process snapshots every 60 s (top-N only), SQL light telemetry every 60 s, SQL inventory every 6 h, health records every 5 min. All configurable. | Meets ≤1 % CPU / ≤150 MB targets with margin. |
| D1 | "Allocated vCPU" for a VM = logical CPU count visible to the OS; hypervisor-side allocation is out of reach for an in-guest offline agent (documented limitation). | In-guest agent. |
| E1 | Analyzer machine has enough RAM to hold minute-level aggregates for a fleet (90 days ≈ 130 k minute-rows per host — trivial). | Enables simple, dependency-free analyzer. |
| F1 | Bundle transfer to the analyzer is manual (USB/secure copy) — the product itself never transmits anything. | Hard offline requirement. |
| G1 | Linux service manager is systemd; other init systems are a documented limitation. | Enterprise norm. |
| H1 | SQL Server credentials: Windows uses integrated auth (service account granted least-privilege login); Linux uses a dedicated SQL login whose password is stored in a root-only (0600) file outside the config, referenced by path — never in the config, never in bundles, never logged. | Simplest safe offline secret handling; see threat model. |

## 3. Architecture decisions (with justification)

* **Language: Go 1.24.** One codebase for Windows + Linux; single *static binary* per
  platform (no runtime to install on customer servers — critical for temporary
  enterprise deployment and clean uninstall); low idle memory; trivial
  cross-compilation; mature service integration (`x/sys/windows/svc`, systemd Type=simple).
* **Host telemetry: `gopsutil/v4`** (the library behind Telegraf and the Datadog agent)
  rather than reimplementing `/proc` + PDH/WMI parsing. Proven, read-only, cross-platform.
  Linux PSI (`/proc/pressure/*`) read directly (gopsutil doesn't expose it).
* **SQL Server: `microsoft/go-mssqldb`** (pure Go, Microsoft-maintained; supports
  Windows integrated auth). All queries strictly read-only DMV selects behind a
  `Querier` seam so DMV parsing is unit-testable with fixtures.
* **Storage: NDJSON records → gzip → daily segments, atomic rename, per-kind files.**
  Justification: append-only and crash-safe (a torn write loses at most the active
  segment's tail, and gzip lets us recover all complete records before the corruption
  point); corruption is isolated per segment/day; gzip on repetitive minute-aggregate
  JSON achieves ~15–20× compression; the format is portable and self-describing
  (schema_version on every record); the data volume (≤ a few hundred MB per host for
  90 days) does not justify an embedded DB dependency. Spike events are separate
  segment files from long-term aggregates, as required.
* **Encryption: `filippo.io/age` (X25519 + ChaCha20-Poly1305 streaming AEAD).**
  Established, reviewed library; recipient-based, so agents carry only the *public*
  recipient key; every segment is an independent age file → per-chunk authentication
  detects tampering *and truncation*, and one damaged segment never affects the others.
  Key rotation = generate a new identity and update the agent config recipient (multiple
  recipients supported for overlap windows). No custom crypto anywhere.
* **Bundle: a single `.urab` file = tar of `manifest.json.age` + `segments/*.age`.**
  The manifest carries SHA-256 of every ciphertext segment, record counts, time ranges,
  host ID, agent + schema versions, and collection-health summary.
* **Spike detection: static thresholds + sustained duration + robust baseline
  (median + k·MAD over trailing window) + rolling pre-event ring buffer.** High-res
  samples are preserved before/during/after the event; top processes, service/unit
  mapping, scheduled-job correlation and (when applicable) SQL context are attached with
  `probable_cause` / `attribution_confidence` / `attribution_evidence` /
  `validation_required` — never a claimed definite cause.
* **Analyzer: pure-Go CLI, no DB.** Streams NDJSON into in-memory per-host datasets;
  exact percentiles from minute aggregates; recurrence detection by time-of-day
  clustering across days; a transparent, explainable rules engine for recommendation
  categories (section 16 of the spec); JSON + Markdown + self-contained HTML reports.
* **Synthetic generator (`ura-analyzer synth`)** writes through the *same* store/bundle
  code paths as the real agent, so demo bundles exercise the production format,
  encryption and analyzer end-to-end without touching a production server.

## 4. Repository layout

```
cmd/ura-agent/          agent binary (run, export, install helpers, service wrappers)
cmd/ura-analyzer/       analyzer binary (keygen, import, report, synth, verify)
internal/model/         versioned record schema (single source of truth)
internal/config/        YAML config, validation, SHA-256 config hash
internal/hostid/        stable generated host ID
internal/inventory/     host inventory snapshot
internal/sampler/       host metric sampling + minute aggregation (+ PSI on Linux)
internal/proctop/       bounded top-N process sampling, service/unit mapping
internal/sched/         cron / systemd-timer / Task Scheduler correlation snapshots
internal/spike/         detector (static+sustained+baseline), ring buffer, capture
internal/sqlserver/     detection (process/registry/mssql-conf) + read-only DMV collectors
internal/store/         segment spool: gzip NDJSON, rotation, atomic writes, retention, recovery
internal/crypt/         age keygen/encrypt/decrypt wrappers
internal/bundle/        manifest, export to .urab, verification, import
internal/agent/         orchestration, scheduling, health, state persistence
internal/analyzer/      import, normalize, stats, recurrence, recommend, report
internal/synth/         synthetic fleet/workload generator
deploy/linux/           systemd unit, install.sh, uninstall.sh
deploy/windows/         install.ps1, uninstall.ps1 (sc.exe based)
deploy/sql/             least-privilege login setup scripts
docs/                   ARCHITECTURE, THREAT_MODEL, DATA_DICTIONARY, CONFIGURATION,
                        DEPLOY_LINUX, DEPLOY_WINDOWS, SQL_PERMISSIONS, KEY_MANAGEMENT,
                        SECURITY_VERIFICATION, BENCHMARK, LIMITATIONS, EXTENSIBILITY
scripts/                build.sh, sbom.sh, demo.sh, verify-no-network.sh
samples/                committed demo: encrypted bundles + consolidated reports
test/e2e/               synth → encrypt → import → report → assertions
```

## 5. Milestones

| M | Scope | Exit criteria |
|---|-------|--------------|
| M1 | Plan committed; module scaffold | PLAN.md pushed; `go build ./...` green |
| M2 | Schema + config + host ID + store + crypto + bundle | unit tests: atomic writes, rotation, retention, crash recovery, encrypt/decrypt roundtrip, tamper & truncation detection |
| M3 | Spike engine + stats | tests: CPU/mem/disk spike capture, sustained & baseline detection, ring buffer, repetitive pattern fixture |
| M4 | Host collection (Linux live, Windows compile-verified) + agent orchestration + export | agent runs locally, produces a valid encrypted bundle; `GOOS=windows go build` green |
| M5 | SQL detection + DMV collectors + HA awareness (fixture-tested) | fixture tests incl. permission-denied fallback, multi-instance memory math |
| M6 | Analyzer: import, consolidate, recurrence, recommendations, reports | e2e: multi-host bundles → JSON+MD+HTML reports with correct percentiles, spike patterns, categories |
| M7 | Synth generator, samples, docs, deploy scripts, SBOM, benchmark | committed sample bundles + sample report; all docs present; benchmark numbers recorded |
| M8 (follow-up) | Real-Windows smoke test, real SQL Server integration pass, signing pipeline | see "Next steps" |

## 6. Major risks

1. **Windows paths unexecuted here** — mitigated by build-tag seams, cross-compilation
   in CI, gopsutil's mature Windows support; requires M8 smoke test.
2. **SQL DMV drift across versions** — collectors degrade per-query (each query failure
   is recorded in collection health, never fatal); documented version matrix.
3. **Attribution overclaiming** — mitigated structurally: confidence scores capped,
   `validation_required` default-true for all downsizing recommendations, temporal
   correlation always labeled as probable.
4. **Overhead regressions** — sampling is O(top-N) bounded; benchmark procedure in
   `docs/BENCHMARK.md` must be re-run on target-class hardware.
5. **Key mismanagement by operators** — mitigated by `keygen` UX + `docs/KEY_MANAGEMENT.md`;
   agents cannot decrypt anything they wrote.

## 7. Exact MVP boundary

**In:** everything in milestones M1–M7, i.e. a working Linux agent (live-tested),
Windows agent (code complete, cross-compiled, not smoke-tested), fixture-tested SQL
collectors, encrypted bundle pipeline, full offline analyzer with all three report
types, recommendation engine, synthetic demo fleet, sample bundles/reports, all
documentation and deploy/uninstall scripts, SBOM + no-network verification.

**Out (v1):** all external integrations (Azure/AWS/Foglight/Dynatrace/SCOM/Prometheus/
VMware/K8s/billing/SaaS/LLM), monetary savings, pseudonymization mode (schema supports
it; not implemented), raw SQL text capture (schema field exists, hard-disabled),
command-line capture (disabled by default per spec; optional redacted mode not built),
MSI/DEB/RPM packaging (zip + scripts instead), binary signing (readiness documented).

---

## 8. Status checklist (update this section as you work)

- [x] M1 scaffold + plan committed
- [x] M2 model/config/hostid/store/crypt/bundle + tests
- [x] M3 spike engine + stats + tests
- [x] M4 agent orchestration, Linux collection, Windows cross-compile, export CLI
      (validated live on Linux: run → SIGTERM → restart recovery → export → analyze)
- [x] M5 SQL detection + collectors + HA + fixture tests (incl. permission-denied
      fallback, multi-instance memory math, pseudonymized job names)
- [x] M6 analyzer import/consolidation/recommendations/reports + e2e test
      (all 6 synthetic hosts classify into their designed categories)
- [x] M7 synth generator, committed sample bundles + reports (`samples/`),
      full docs set, deploy scripts, SBOM script, measured benchmark
      (0.08 % CPU / ~15 MB RSS vs 1 % / 150 MB budget), no-network PASS
- [ ] M8 (follow-up agent): run agent on a real Windows Server host; validate service
      install/uninstall, registry SQL discovery, SCM service mapping, integrated-auth
      SQL collection against a live instance; wire binary signing; consider MSI/DEB/RPM.

### Notes for the next agent

* Everything builds with `make all` (or `scripts/build.sh`); tests with `make test`;
  the full offline demo with `scripts/demo.sh` (generates a synthetic fleet, encrypts
  bundles, imports them, writes reports under `./demo-out/`).
* The record schema lives only in `internal/model` — bump `model.SchemaVersion`
  and add migration notes to `docs/DATA_DICTIONARY.md` for any breaking change.
* Windows-specific files are `*_windows.go`; keep the seams (interfaces in
  `proctop.ServiceMapper`, `sqlserver.Discovery`) if you extend them.
