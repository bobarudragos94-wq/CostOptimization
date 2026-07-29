# Linux Deployment & Uninstall

Supported: systemd distributions (RHEL/Rocky/Alma 8+, Ubuntu 20.04+, SLES 15+,
Debian 11+), x86-64. The binary is static — no package dependencies.

## Install

1. Unpack the release archive on the server (no internet needed):
   `ura-agent`, `ura-agent.service`, `agent.example.yaml`, `install.sh`,
   `uninstall.sh`.
2. Run `sudo ./install.sh`. It creates the `ura-agent` system user,
   `/opt/ura-agent` (binary), `/etc/ura-agent` (config, 750),
   `/var/lib/ura-agent` (spool, 750), installs the hardened systemd unit
   (no capabilities, `ProtectSystem=strict`, `IPAddressDeny=any` +
   `IPAddressAllow=localhost`, `MemoryMax=200M`, `CPUQuota=25%`).
3. Edit `/etc/ura-agent/agent.yaml`:
   * `agent.recipient` ← content of `recipient.txt` from
     `ura-analyzer keygen` (see docs/KEY_MANAGEMENT.md).
   * For deep SQL telemetry on SQL Server on Linux: keep `sql.auth: sqllogin`,
     create the login with `deploy/sql/setup-least-privilege.sql`, and store
     the password: `install -o ura-agent -g ura-agent -m 600 /dev/null
     /etc/ura-agent/sql-password && vi /etc/ura-agent/sql-password`.
4. `sudo systemctl enable --now ura-agent`

## Verify

```
systemctl status ura-agent
sudo -u ura-agent /opt/ura-agent/ura-agent inventory --config /etc/ura-agent/agent.yaml
sudo ./scripts/verify-no-network.sh /opt/ura-agent/ura-agent   # no ports, no sockets
```

Collection health (including permission degradations, e.g. unreadable user
crontabs when running unprivileged) is embedded in every exported bundle and
shown in the analyzer's data-quality section.

## Permissions note

The agent runs unprivileged by design. Consequences, all reported in
collection health rather than failing:
* per-process I/O counters of other users' processes are unreadable →
  process disk attribution is degraded (CPU/memory attribution unaffected);
* user spool crontabs are unreadable → system crontabs + systemd timers only.
Running the service as root removes these gaps but is NOT recommended; prefer
accepting the documented degradation.

## Export & collection

Bundles are written daily (and at manual `ura-agent export`) to
`/var/lib/ura-agent/export/*.urab`. Collect them by any offline means; the
agent never transmits.

## Restart / reboot behavior

State survives restarts: the spool recovers torn segments on start, host ID
and monitoring start date persist in `/var/lib/ura-agent`. `Restart=on-failure`
plus systemd ordering handles reboots; no operator action needed.

## Uninstall

`sudo ./uninstall.sh` — stops and removes the service, offers a final export,
asks before deleting the data directory, removes binary, config and the
service user. `--keep-data` preserves `/var/lib/ura-agent`.
