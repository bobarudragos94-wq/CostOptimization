# Threat Model

## Context

The agent runs temporarily (30–90 days) on servers owned by a customer being
assessed for rightsizing. The analyst is a third party. Both sides need
protection: the customer from exfiltration/tampering by the product, the
analyst from manipulated evidence.

## Assets

| Asset | Sensitivity |
|---|---|
| Utilization telemetry, process names, service names | moderate (reveals infrastructure layout) |
| SQL instance configuration, database names/sizes, Agent job names | moderate–high |
| SQL credentials (Linux sqllogin mode) | high |
| The decryption identity (private key) | high |
| Recommendation reports | high (business decisions) |

Explicitly **not collected**: command-line arguments, raw SQL text, query
results, table data, user names of interactive sessions, packet contents.
These exclusions are hard-coded, not configuration defaults (v1).

## Trust boundaries

1. **Monitored server ↔ outside world**: the agent makes no network
   connections except loopback to local SQL instances (optional). No listening
   sockets, no DNS, no update checks, no telemetry-about-telemetry.
   Verification: `scripts/verify-no-network.sh` + systemd `IPAddressDeny=any`
   + `IPAddressAllow=localhost` sandboxing.
2. **Spool ↔ bundle**: plaintext telemetry exists only inside the agent data
   directory (0750, service user). Export encrypts to the analyst-controlled
   public key; the server retains no ability to read exported bundles.
3. **Bundle transport**: bundles travel by whatever offline means the customer
   chooses. Confidentiality and integrity ride on age AEAD + manifest hashes,
   not on the transport.
4. **Analyzer machine**: holds the only private key; must be treated as the
   sensitive perimeter. Keys are files with 0600, generation refuses to
   overwrite.

## STRIDE summary

| Threat | Mitigation |
|---|---|
| **S**poofing a bundle (attacker fabricates telemetry) | Bundles are not signed in v1 (documented limitation). Mitigations: hostID + hostname + config-hash consistency checks at import, per-segment ciphertext hashes pinned by the manifest, and the operational fact that fabrication requires the recipient key *and* placement in the analyst's import directory. v2: per-agent signing keys. |
| **T**ampering with spooled data on the server | Spool is plaintext for the collecting host only (0750). Post-export tampering is detected: AEAD per chunk + manifest SHA-256; analyzer marks `hash_mismatch` / `decrypt_failed` per segment and reports it. |
| **R**epudiation | Health records + manifest embed agent version, config hash, coverage, permission issues; the analyzer surfaces gaps rather than smoothing over them. Logs contain no credentials. |
| **I**nformation disclosure | No exfiltration path (offline). Encrypted-at-export with asymmetric keys; agents cannot decrypt their own output. SQL passwords only in a root-only file, read at connect time, never logged or exported. Command lines and SQL text never collected. |
| **D**enial of service (agent overloading host) | Bounded everything: top-N process cardinality, capped capture buffers, bounded queues (drop + count under disk pressure), spool byte cap, systemd `MemoryMax`/`CPUQuota` guard rails. |
| **E**levation of privilege | Agent runs as dedicated non-root user / virtual service account; read-only interfaces; `NoNewPrivileges`, empty capability set on Linux; SQL login limited to VIEW SERVER STATE-class grants. |

## Residual risks (accepted, documented)

1. Bundle authenticity relies on integrity + operational handling, not
   signatures (v1). Planned: agent identity keys.
2. A root attacker on the monitored server can falsify anything the kernel
   reports — out of scope for any in-guest agent.
3. `process_only` SQL fallback exposes instance existence via process names —
   inherent to process listing.
4. The analyst workstation is a single point of confidentiality for all
   customers' bundles; operational guidance: per-engagement keypairs
   (see docs/KEY_MANAGEMENT.md).

## Audit logging

Agent logs are operational only (start/stop, spike captured, export path,
degradations) and contain no secrets; collection health is additionally
embedded *inside* the tamper-evident encrypted bundles, so post-hoc log
manipulation on the server cannot rewrite the exported record.
