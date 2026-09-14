#!/usr/bin/env bash
# acc TLS 冒烟：gen-certs → 启动 → 探测 1 s_client 握手 →
# 探测 2 真实 MQTT CONNECT over TLS，断言收到 CONNACK 20 02 00 00
set -euo pipefail
cd "$(dirname "$0")/.."
CONF=conf/acc.conf
BIN=build/acc
TLSPORT=$(awk '$1=="tls_port"{print $2}' $CONF)
CERT=$(awk '$1=="cert_file"{print $2}' $CONF)
KEY=$(awk '$1=="key_file"{print $2}' $CONF)

task build

# 证书缺失时生成（Taskfile gen-certs 产物路径与 conf 默认值一致）
if [ ! -f "$CERT" ] || [ ! -f "$KEY" ]; then
    task gen-certs
fi

$BIN -c $CONF & MPID=$!
trap 'kill -TERM $MPID 2>/dev/null || true' EXIT

# 等 TLS 监听就绪：证书缺失降级时端口不开，此处应直接失败
ready=0
for i in $(seq 1 50); do
    if (exec 3<>/dev/tcp/127.0.0.1/$TLSPORT) 2>/dev/null; then
        ready=1
        break
    fi
    sleep 0.1
done
if [ "$ready" != 1 ]; then
    echo "FAIL: TLS 端口 $TLSPORT 未监听（证书降级?）"
    exit 1
fi
echo "OK: TLS port $TLSPORT open"

# 探测 1：TLS 握手成功（s_client 打印出服务端证书）
timeout 5 openssl s_client -connect 127.0.0.1:$TLSPORT </dev/null 2>/dev/null \
    | grep -q "BEGIN CERTIFICATE" \
    || { echo "FAIL: TLS 握手失败"; exit 1; }
echo "OK: TLS handshake"

# 探测 2：MQTT CONNECT over TLS → 期望 CONNACK 20 02 00 00。
# 字节流为固定 client_id="tlssmoke" 的 MQTT 3.1.1 CONNECT 手工编码：
# 固定头 10 14 | 可变头 0004 "MQTT" 04 02 003C | payload 0008 "tlssmoke"
CONNECT='\x10\x14\x00\x04\x4D\x51\x54\x54\x04\x02\x00\x3C\x00\x08\x74\x6C\x73\x73\x6D\x6F\x6B\x65'
# -quiet 隐含 -ign_eof：stdin EOF 后仍等待服务端数据，由 timeout 收尾；
# pipefail 下 timeout 的非零退出码属预期，局部关闭以免 set -e 误杀
set +o pipefail
RESP=$(printf "$CONNECT" \
    | timeout 5 openssl s_client -quiet -connect 127.0.0.1:$TLSPORT 2>/dev/null \
    | od -An -tx1 | tr -d ' \n')
set -o pipefail
echo "$RESP" | grep -q "20020000" \
    || { echo "FAIL: 期望 CONNACK 20 02 00 00, 实际收到: '$RESP'"; exit 1; }
echo "OK: MQTT CONNACK over TLS"

# SIGTERM 退出并等待进程消亡，避免残留占用端口
kill -TERM $MPID
for i in $(seq 1 50); do
    kill -0 $MPID 2>/dev/null || break
    sleep 0.1
done
if kill -0 $MPID 2>/dev/null; then
    echo "FAIL: master not exited"
    exit 1
fi
trap - EXIT
echo "TLS SMOKE PASS"
