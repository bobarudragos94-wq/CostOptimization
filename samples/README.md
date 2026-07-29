# Samples

- `bundles/*.urab` — encrypted bundles from the synthetic 6-host demo fleet
  (real production format, encryption and code paths).
- `demo-keys/` — the matching keypair. The identity is committed **only**
  because these are synthetic samples; a real engagement's identity never
  enters version control or any monitored server.
- `reports/` — the consolidated reports the analyzer produced from these
  bundles (JSON, Markdown, HTML).

Regenerate with `make demo` (writes to `demo-out/`, git-ignored).
