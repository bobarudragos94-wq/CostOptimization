# Security Verification Procedures

Reproducible checks an enterprise security team can run before approving
deployment. All are offline.

## 1. No listening ports / no network sockets (automated)

`scripts/verify-no-network.sh [path-to-agent]`

Starts the agent with a scratch config (`sql.enabled=false`), then asserts
via `ss` and `/proc/<pid>/fd` that the process owns **zero sockets** of any
kind. PASS output is the acceptance criterion. With deep SQL collection
enabled, the only sockets ever created are loopback connections to local SQL
instances — verify with:

```
ss -tpn | grep "$(pidof ura-agent)"      # remote address must be 127.0.0.1/::1
```

## 2. Network-namespace proof (strongest)

```
unshare --net -- ./bin/ura-agent run --config /etc/ura-agent/agent.yaml
```

In an empty network namespace (no interfaces at all) collection proceeds
normally and SQL degrades to `process_only` — demonstrating no hidden network
dependency. The systemd unit additionally enforces `IPAddressDeny=any` /
`IPAddressAllow=localhost` at the kernel level for the production service.

## 3. Static verification

* `go.sum` pins every dependency (dependency locking). `go mod verify`
  revalidates the hashes offline.
* SBOM: `make sbom` → `sbom.json` (module list); CycloneDX command documented
  inside `scripts/sbom.sh` for signing pipelines.
* Reproducible builds: `CGO_ENABLED=0 go build -trimpath` produces
  bit-identical binaries for a given Go toolchain version; record
  `go version` with the release.
* Vulnerability scanning workflow: run `govulncheck ./...` (offline DB copy
  supported) per release; triage into docs/LIMITATIONS.md.
  **Last run** (hardening branch, Go 1.25.12, deps upgraded): *0
  vulnerabilities in called code, 0 in imported packages*; 1 module-level
  advisory remains — GO-2026-5932, the deprecation of
  `golang.org/x/crypto/openpgp`, which ships inside the x/crypto module but
  is never imported by this product (bundle encryption uses age's
  ChaCha20-Poly1305); no fix exists or is needed.
* Signed-build readiness: single static binary per OS — signable with
  standard tooling (Authenticode `signtool` for `ura-agent.exe`, GPG detached
  signatures for ELF). No plugin loading, no self-modification, no updater.

## 4. Data-content verification

* Grep the codebase: command lines are never read
  (`process.Cmdline` does not appear); SQL text is never selected (no
  `dm_exec_sql_text` anywhere in queries).
* Decrypt a bundle with your own key and inspect: `ura-analyzer analyze`
  output plus `tar tf bundle.urab` — all payload files end in `.age`;
  no plaintext telemetry leaves the data directory.
* Config hash in the manifest matches the deployed `agent.yaml`
  (`ura-agent check-config` prints it).

## 5. Tamper-evidence demonstration

Flip any byte of a `.urab` file and re-run the analyzer: the affected segment
is reported `hash_mismatch`/`decrypt_failed` and excluded, the remainder
imports, and the report's data-quality section names the damage. This is
covered by automated tests (`internal/bundle`, `test/e2e`) you can run with
`make test`.

## 6. Least privilege

* Linux: dedicated system user, hardened unit (empty capability set,
  `NoNewPrivileges`, `ProtectSystem=strict`, `MemoryDenyWriteExecute`).
  Inspect with `systemd-analyze security ura-agent`.
* Windows: virtual service account by default; ACL-restricted data directory.
* SQL: grants limited to VIEW SERVER STATE-class permissions
  (docs/SQL_PERMISSIONS.md), teardown script provided.
