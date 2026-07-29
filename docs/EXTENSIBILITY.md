# Future Extensions (design notes — deliberately NOT implemented in v1)

The offline MVP keeps every integration out of scope (specification §3) but
the architecture reserves clean seams for them. This document records where
each future integration plugs in, so v2 work does not disturb the v1 core.

## General principles

* The **record schema (`internal/model`) is the contract**. External sources
  are adapters that emit the same kinds (`host_minute`, `spike_event`, …);
  the spike logic, encryption, analyzer and reports stay source-agnostic.
* New telemetry kinds are additive minor-version changes; the analyzer skips
  unknown kinds by design.
* Nothing in the agent may ever *require* a network; integrations that need
  one belong to separate opt-in binaries/modules with their own review.

## Azure / Azure Monitor

Adapter shape: an **importer** on the analyzer side (not the agent) that
reads exported Azure Monitor metrics (diagnostic-settings JSON blobs or
`az monitor metrics list` dumps) and converts them into `HostData.Minutes`.
Plug-in point: `internal/analyzer.LoadBundles` → introduce a
`SourceImporter interface { Load(path string) (*HostData, error) }` registry;
`.urab` is simply the first registered importer. Azure VM SKU metadata maps
onto `Inventory.AllocatedVCPU/TotalRAMBytes`.

## AWS (CloudWatch)

Same importer seam; CloudWatch `GetMetricData` JSON exports → minutes.
EC2 instance-type catalog (static file, shipped offline) supplies allocation.
Recommendation ladder unchanged; a v2 cost layer would attach price sheets to
`CapacityRange` (the spec's future cost version).

## Foglight / SCOM / Dynatrace

These already store long-horizon metrics. Adapter = export-file importer per
product (Foglight PerformanceIQ CSV, SCOM data-warehouse SQL extract,
Dynatrace metrics v2 JSON). Their alarm histories can seed `spike_event`
records with `attribution_evidence: ["imported from <tool>"]` and reduced
confidence caps.

## Prometheus

Importer for OpenMetrics dumps / `promtool tsdb dump` output with a mapping
table (node_exporter metric → model fields). Conversely, a v2 agent flag
could expose a *disabled-by-default* localhost-only read endpoint; any such
listener contradicts v1's zero-port guarantee and must remain a separate
build tag (`//go:build promexport`) so security review of the default binary
is unaffected.

## VMware / hypervisor truth

The biggest analytical upgrade: per-VM entitlement, ready time, ballooning
from vCenter perf exports. Importer merges by hostname/UUID into the same
`HostData`, letting the ladder replace the in-guest `AllocatedVCPU`
approximation and steal-time heuristic with real host-side contention data.

## Kubernetes

Different granularity (pods vs hosts). Reserve a new record kind
(`container_minute`) and a `WorkloadData` sibling of `HostData`; do not
shoehorn containers into host records. The agent's cgroup reader
(`proctop.UnitFromCgroup`) already distinguishes container scopes, which
becomes the detection hook.

## Cloud billing / savings

Attach a price catalog (offline file) at the analyzer: category outputs
already carry `CapacityRange`; a pricing layer converts deltas into ranges of
monetary savings with the same confidence/validation framing. Keep it a
separate report section so technical findings stay auditable independently.

## SQL Server siblings (PostgreSQL, MySQL, Oracle)

`sqlserver.Querier` + fixture-test pattern generalizes: one package per
engine implementing detection + read-only collectors that emit
`sql_inventory`-shaped records (rename of the kind namespace to `db_*` is the
one planned breaking schema change, scheduled for the 2.0.0 schema).
