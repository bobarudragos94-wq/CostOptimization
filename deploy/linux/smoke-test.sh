#!/bin/bash
# Linux smoke test of the URA agent (mirror of deploy/windows/smoke-test.ps1).
# Run as a user who can execute the agent; work dir is disposable.
#
#   ./smoke-test.sh /path/to/ura-agent /path/to/ura-analyzer [minutes]
#
# Performs: placeholder-rejection check, real keygen+config, collection with
# controlled CPU/memory/disk spikes (memory threshold derived from the
# machine's actual baseline), forced SIGKILL + restart recovery, socket
# checks, encrypted export, analyzer report with spike assertions.
# Evidence: ./smoke-evidence/
set -u
AGENT="${1:?usage: smoke-test.sh <ura-agent> <ura-analyzer> [minutes]}"
ANALYZER="${2:?usage: smoke-test.sh <ura-agent> <ura-analyzer> [minutes]}"
MINUTES="${3:-30}"
WORK="$(pwd)/smoke-evidence"
rm -rf "$WORK"; mkdir -p "$WORK"
EV="$WORK/smoke.log"
log(){ echo "[$(date -u +%H:%M:%S)] $*" | tee -a "$EV"; }
fail(){ log "FAIL: $*"; exit 1; }

"$ANALYZER" keygen --out "$WORK/keys" >>"$EV" 2>&1
REC=$(cat "$WORK/keys/recipient.txt")

# Dynamic memory threshold: baseline used% + 12; allocation ~= 25-30% of RAM.
read -r MEMTOTAL MEMAVAIL < <(awk '/MemTotal/{t=$2}/MemAvailable/{a=$2}END{print t, a}' /proc/meminfo)
USEDPCT=$(( (MEMTOTAL - MEMAVAIL) * 100 / MEMTOTAL ))
MEMTHRESH=$(( USEDPCT + 12 )); [ $MEMTHRESH -gt 90 ] && MEMTHRESH=90
ALLOC_GB=$(( MEMTOTAL / 1024 / 1024 * 3 / 10 )); [ $ALLOC_GB -lt 2 ] && ALLOC_GB=2; [ $ALLOC_GB -gt 6 ] && ALLOC_GB=6
log "baseline memory ${USEDPCT}% -> threshold ${MEMTHRESH}%, allocation ${ALLOC_GB} GiB"

cat > "$WORK/agent.yaml" <<EOF
agent:
  data_dir: $WORK/data
  recipient: "$REC"
sampling: { host_interval: 5s, proc_interval: 15s, proc_persist_interval: 60s, health_interval: 60s }
spikes:
  cooldown: 3m
  pre_buffer: 2m
  post_capture: 1m
  rules:
    - { resource: cpu,     static_threshold: 60, sustained: 60s, baseline_k: 0 }
    - { resource: memory,  static_threshold: $MEMTHRESH, sustained: 60s, baseline_k: 0 }
    - { resource: disk_io, static_threshold: 40, sustained: 60s, baseline_k: 0 }
sql: { enabled: true }
export: { auto_daily: false, export_dir: $WORK/export }
EOF

log "step 1: placeholder rejection"
sed 's/recipient: .*/recipient: "age1REPLACE_ME"/' "$WORK/agent.yaml" > "$WORK/bad.yaml"
"$AGENT" check-config --config "$WORK/bad.yaml" >>"$EV" 2>&1 && fail "placeholder accepted"
log "PASS placeholder rejected"
"$AGENT" check-config --config "$WORK/agent.yaml" >>"$EV" 2>&1 || fail "valid config rejected"

log "step 2: start agent"
"$AGENT" run --config "$WORK/agent.yaml" >>"$WORK/agent.log" 2>&1 &
APID=$!
sleep 5; kill -0 $APID || fail "agent did not start"
sockets(){ local n; n=$(ls -l /proc/$APID/fd 2>/dev/null | grep -c socket || true); log "socket-check pid=$APID: $n socket fds"; [ "$n" -gt 0 ] && ss -tpn | grep "pid=$APID," | tee -a "$EV"; }
sockets

BASE=$(( MINUTES * 60 / 4 )); [ $BASE -lt 120 ] && BASE=120
log "step 3: baseline ${BASE}s"; sleep $BASE

log "step 3a: CPU spike (per-core busy loops, 150s)"
for i in $(seq 1 "$(nproc)"); do (timeout 150 bash -c 'while :; do :; done') & done
sleep 170; sockets

log "step 3b: memory spike (${ALLOC_GB} GiB held 120s)"
python3 -c "
import time
b = bytearray($ALLOC_GB * 1024**3)
for i in range(0, len(b), 4096): b[i] = 1
time.sleep(120)" &
sleep 150

log "step 3c: disk spike (sync writes, 120s)"
timeout 120 bash -c "while :; do dd if=/dev/zero of=$WORK/dd-spike bs=4M count=64 oflag=dsync 2>/dev/null; done"
rm -f "$WORK/dd-spike"; sleep 130

log "step 4: SIGKILL + restart"
kill -9 $APID; sleep 3
"$AGENT" run --config "$WORK/agent.yaml" >>"$WORK/agent.log" 2>&1 &
APID=$!
sleep 10; kill -0 $APID || fail "agent did not restart"
log "restarted pid=$APID"; sockets

REMAIN=$(( MINUTES * 60 - BASE - 620 )); [ $REMAIN -lt 60 ] && REMAIN=60
log "step 5: remaining collection ${REMAIN}s"; sleep $REMAIN; sockets

log "step 6: stop, export, analyze"
kill -TERM $APID; wait $APID 2>/dev/null
"$AGENT" export --config "$WORK/agent.yaml" >>"$EV" 2>&1 || fail "export failed"
"$ANALYZER" analyze --key "$WORK/keys/identity.txt" --in "$WORK/export" --out "$WORK/reports" | tee -a "$EV"

log "step 7: assertions"
python3 - "$WORK/reports/report.json" <<'PY' | tee -a "$EV"
import json, sys
r = json.load(open(sys.argv[1]))
res = {s['resource'] for s in r['spikes']}
for s in r['spikes']:
    print(f"  {s['resource']:8} peak={s['peak']:8.1f} dur={s['duration_s']:6.0f}s proc={s.get('process','')!r} job={s.get('service_or_job','')!r} conf={s['confidence']}")
ok = 'cpu' in res and 'memory' in res
bogus = [s for s in r['spikes'] if s.get('service_or_job','').startswith('cron:[')]
if bogus: print("FAIL: mangled cron correlation present"); ok = False
print("RESULT:", "PASS" if ok else "FAIL")
PY
grep -q "RESULT: PASS" "$EV" || exit 1
log "SMOKE COMPLETE"
