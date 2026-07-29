# Windows Deployment & Uninstall

Supported: Windows Server 2016+ x64. Single static binary, no .NET/VC++
prerequisites.

> **MVP status**: the Windows agent is code-complete and cross-compiled, but
> this MVP was validated live on Linux only. Run the smoke checklist below on
> a non-production Windows host first (see PLAN.md, milestone M8).

## Install

1. Unpack the release on the server: `ura-agent.exe`, `agent.example.yaml`,
   `install.ps1`, `uninstall.ps1`.
2. Elevated PowerShell: `.\install.ps1`
   * installs to `C:\Program Files\ura-agent`, data in `C:\ProgramData\ura-agent`
     with restricted ACLs (Administrators/SYSTEM/service account only);
   * registers the `ura-agent` service (auto start, restart on failure) under
     the virtual account `NT SERVICE\ura-agent` (least privilege, no password).
3. Edit `C:\ProgramData\ura-agent\agent.yaml`: set `agent.recipient` from
   `ura-analyzer keygen` (docs/KEY_MANAGEMENT.md). On Windows `sql.auth`
   defaults to `integrated` — no credentials stored anywhere.
4. For deep SQL telemetry, run `deploy\sql\setup-least-privilege.sql` on each
   instance with `CREATE LOGIN [NT SERVICE\ura-agent] FROM WINDOWS;`
   uncommented.
5. `Start-Service ura-agent`

## Service account trade-off

`NT SERVICE\ura-agent` can read system counters and its own data directory.
Reading working-set/IO details of *other* accounts' processes may be limited;
those gaps appear in collection health as degraded process attribution. If the
engagement requires full process visibility, re-run `install.ps1
-ServiceAccount 'NT AUTHORITY\SYSTEM'` and document the elevated choice.

## Verify

```powershell
Get-Service ura-agent
# no listening ports owned by the agent:
Get-NetTCPConnection -State Listen | Where-Object OwningProcess -eq (Get-Process ura-agent).Id   # expect empty
# loopback-only sockets (only when SQL deep collection is enabled):
Get-NetTCPConnection | Where-Object { $_.OwningProcess -eq (Get-Process ura-agent).Id -and $_.RemoteAddress -notin '127.0.0.1','::1' }  # expect empty
& 'C:\Program Files\ura-agent\ura-agent.exe' inventory --config C:\ProgramData\ura-agent\agent.yaml
```

## Export & collection

Daily bundles land in `C:\ProgramData\ura-agent\export\*.urab`; manual export:
`ura-agent.exe export --config C:\ProgramData\ura-agent\agent.yaml`.

## Uninstall

Elevated PowerShell: `.\uninstall.ps1` — stops/deletes the service, offers a
final export, asks before deleting `C:\ProgramData\ura-agent`, removes the
Program Files directory. `-KeepData` preserves the data directory. Revoke the
SQL login with the teardown block of `setup-least-privilege.sql`.
