#!/bin/sh
# URA agent installation for systemd Linux. Run as root from the unpacked
# release directory. Fully offline; nothing is downloaded.
set -e
BIN_SRC="${1:-./ura-agent}"
[ -f "$BIN_SRC" ] || { echo "usage: ./install.sh [path-to-ura-agent-binary]"; exit 2; }

echo "==> creating service user and directories"
id ura-agent >/dev/null 2>&1 || useradd --system --home /var/lib/ura-agent --shell /usr/sbin/nologin ura-agent
install -d -o root  -g root      -m 755 /opt/ura-agent
install -d -o root  -g ura-agent -m 750 /etc/ura-agent
install -d -o ura-agent -g ura-agent -m 750 /var/lib/ura-agent

echo "==> installing binary and unit"
install -o root -g root -m 755 "$BIN_SRC" /opt/ura-agent/ura-agent
install -o root -g root -m 644 ./ura-agent.service /etc/systemd/system/ura-agent.service

if [ ! -f /etc/ura-agent/agent.yaml ]; then
  install -o root -g ura-agent -m 640 ./agent.example.yaml /etc/ura-agent/agent.yaml
  echo "==> EDIT /etc/ura-agent/agent.yaml:"
  echo "      - set agent.recipient to the recipient.txt from 'ura-analyzer keygen'"
  echo "      - if using deep SQL telemetry with a SQL login, create the 0600"
  echo "        password file referenced by sql.password_file (owner ura-agent)"
fi

/opt/ura-agent/ura-agent check-config --config /etc/ura-agent/agent.yaml || {
  echo "!! fix the configuration, then: systemctl enable --now ura-agent"; exit 0; }

systemctl daemon-reload
systemctl enable --now ura-agent
echo "==> installed and started. Status: systemctl status ura-agent"
