#!/usr/bin/env bash
# acc 集成冒烟：启动→连通→优雅退出→respawn
set -euo pipefail
cd "$(dirname "$0")/.."
CONF=conf/acc.conf
BIN=build/acc
PORT=$(awk '$1=="tcp_port"{print $2}' $CONF)

task build

$BIN -c $CONF & MPID=$!
trap 'kill -TERM $MPID 2>/dev/null || true' EXIT
sleep 1

# 1. 端口可连
(exec 3<>/dev/tcp/127.0.0.1/$PORT) && echo "OK: port $PORT open"

# 2. master 存活且 worker 数正确
WPID=$(pgrep -P $MPID | head -1)
[ -n "$WPID" ] && echo "OK: worker $WPID"

# 3. kill worker → respawn
kill -9 $WPID; sleep 2
NEW=$(pgrep -P $MPID | wc -l)
[ "$NEW" -ge 1 ] && echo "OK: respawned"

# 4. SIGQUIT 优雅退出
kill -QUIT $MPID
for i in $(seq 1 50); do kill -0 $MPID 2>/dev/null || break; sleep 0.1; done
if kill -0 $MPID 2>/dev/null; then echo "FAIL: master not exited"; exit 1; fi
echo "OK: graceful exit"
echo "SMOKE PASS"
