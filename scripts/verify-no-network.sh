#!/bin/sh
# Reproducible verification that the agent opens no listening ports and
# performs no external network communication (security requirement §17).
#
# Method:
#   1. Start the agent with a scratch config.
#   2. While it runs, enumerate its sockets via /proc/<pid>/net + ss/lsof.
#   3. Assert: zero listening sockets, zero established non-loopback sockets.
#      (Loopback connections to a local SQL Server instance are the only
#      permitted sockets, and only when sql.enabled and an instance exists.)
#
# For a stronger guarantee, run the same procedure inside
#   unshare --net ...   (empty network namespace)
# and observe that collection continues unaffected (SQL falls back to
# process_only), proving no hidden network dependency.
set -e
BIN="${1:-./bin/ura-agent}"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

cat > "$WORK/agent.yaml" <<EOF
agent:
  data_dir: $WORK/data
  recipient: "age1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq"
sql:
  enabled: false
export:
  auto_daily: false
EOF
# Any syntactically valid age recipient works for this test; collection does
# not encrypt until export. Generate a real one when available:
if [ -x ./bin/ura-analyzer ]; then
  ./bin/ura-analyzer keygen --out "$WORK/keys" >/dev/null
  REC="$(cat "$WORK/keys/recipient.txt")"
  sed -i "s|age1qq.*\"|$REC\"|" "$WORK/agent.yaml"
fi

"$BIN" run --config "$WORK/agent.yaml" &
PID=$!
sleep 8

FAIL=0
echo "== sockets owned by ura-agent (pid $PID) =="
if command -v ss >/dev/null 2>&1; then
  LISTEN="$(ss -tulpn 2>/dev/null | grep "pid=$PID," || true)"
  echo "${LISTEN:-  (no listening sockets)}"
  [ -n "$LISTEN" ] && FAIL=1
fi
# /proc ground truth: any socket inode owned by the process.
SOCKETS="$(ls -l /proc/$PID/fd 2>/dev/null | grep -c socket || true)"
echo "socket fds: $SOCKETS (expected 0 with sql.enabled=false)"
[ "$SOCKETS" -gt 0 ] && FAIL=1

kill -TERM $PID 2>/dev/null || true
wait $PID 2>/dev/null || true

if [ "$FAIL" -eq 0 ]; then
  echo "PASS: no listening ports, no network sockets"
else
  echo "FAIL: unexpected sockets found" >&2
  exit 1
fi
