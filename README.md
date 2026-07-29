# URA — Utilization & Rightsizing Analyzer

An enterprise-grade, **fully offline** infrastructure utilization and
rightsizing product. Deploy a low-overhead agent on Windows and Linux servers
for 30–90 days, collect utilization telemetry with spike capture and
process/service/SQL attribution, export **encrypted bundles**, and produce
consolidated technical rightsizing reports with a separate offline analyzer.

* No inbound or outbound network traffic. No listening ports. No cloud.
* Agents can encrypt but never decrypt — the customer/analyst holds the only
  private key.
* First-class Microsoft SQL Server awareness: instance detection, read-only
  DMV telemetry, memory-configuration analysis, Always On topology safety.
* Explainable recommendations with confidence and mandatory validation flags
  — never "auto-downsize every idle server".

## Repository map

| Path | Content |
|---|---|
| `PLAN.md` | plan of record + milestone status (start here to continue work) |
| `cmd/ura-agent`, `cmd/ura-analyzer` | the two binaries |
| `internal/` | schema, collectors, spike engine, store, crypto, bundle, analyzer, synth |
| `deploy/` | systemd unit + install scripts, PowerShell scripts, SQL least-privilege setup |
| `docs/` | architecture, threat model, data dictionary, deployment, SQL permissions, key management, security verification, benchmark, limitations, extensibility |
| `configs/agent.example.yaml` | annotated configuration |
| `samples/` | committed demo bundles + consolidated reports (with demo key) |
| `test/e2e` | full workflow test |

## Five-minute offline demo

```sh
make build          # static binaries in ./bin (Go 1.24+)
make demo           # keygen -> synthetic 6-host fleet -> encrypted bundles -> reports
# open demo-out/reports/report.html
```

The synthetic fleet exercises every analyzer decision: a rightsizing
candidate, a nightly-ETL batch host, a SQL instance with unlimited max server
memory, an Always On primary/secondary pair, and a memory-pressured host.

## Real deployment (short version)

```sh
# analyst machine
ura-analyzer keygen --out keys              # identity stays here

# each monitored server (see docs/DEPLOY_LINUX.md / DEPLOY_WINDOWS.md)
#   set agent.recipient = keys/recipient.txt content
sudo ./deploy/linux/install.sh              # or deploy\windows\install.ps1

# ... 30-90 days later: collect /var/lib/ura-agent/export/*.urab ...

# analyst machine
ura-analyzer analyze --key keys/identity.txt --in bundles/ --out reports/
```

## Development

```sh
make test    # vet + unit + integration tests
make e2e     # synthetic fleet -> encrypted bundles -> analyzer -> assertions
make all     # linux + windows binaries
make sbom    # dependency SBOM from the locked module graph
scripts/verify-no-network.sh ./bin/ura-agent   # zero-socket proof
```

Measured overhead: **0.08 % CPU, ~15 MB RSS** (budget ≤1 % / ≤150 MB) —
see docs/BENCHMARK.md. Current limitations: docs/LIMITATIONS.md (notably:
Windows agent is cross-compiled and unit-tested but pending a real Windows
smoke test, milestone M8 in PLAN.md).
