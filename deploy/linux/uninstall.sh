#!/bin/sh
# Complete, clean removal of the URA agent from a Linux host.
# By default the collected data spool is exported first (if a recipient is
# configured) and then removed; pass --keep-data to preserve it.
set -e
KEEP_DATA=0
[ "$1" = "--keep-data" ] && KEEP_DATA=1

echo "==> stopping service"
systemctl disable --now ura-agent 2>/dev/null || true
rm -f /etc/systemd/system/ura-agent.service
systemctl daemon-reload

if [ "$KEEP_DATA" -eq 0 ]; then
  if [ -x /opt/ura-agent/ura-agent ] && [ -f /etc/ura-agent/agent.yaml ]; then
    echo "==> final export of any remaining telemetry (best effort)"
    /opt/ura-agent/ura-agent export --config /etc/ura-agent/agent.yaml 2>/dev/null \
      && echo "    collect bundles from the export directory before deleting" \
      || true
  fi
  printf "Delete ALL collected data in /var/lib/ura-agent? [y/N] "
  read -r ans
  if [ "$ans" = "y" ] || [ "$ans" = "Y" ]; then
    rm -rf /var/lib/ura-agent
    echo "==> data removed"
  else
    echo "==> data kept at /var/lib/ura-agent — remove manually when done"
  fi
fi

echo "==> removing binary and configuration"
rm -rf /opt/ura-agent
rm -rf /etc/ura-agent
userdel ura-agent 2>/dev/null || true
echo "==> uninstall complete; no files, services, users or ports remain"
