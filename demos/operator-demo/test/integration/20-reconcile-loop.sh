#!/usr/bin/env bash
# 场景 2：核心闭环——超阈值打标记 + 内存恢复移除
# 验证：内存超 threshold 后 Pod 被打上 marker label；内存恢复后 marker 被移除
set -uo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

echo -e "${BLUE}=== 场景 2: 核心闭环（超阈值打标记 / 恢复移除）===${NC}"

NS="default"
POLICY="mem-policy-default"
POD="stress-pod"
MARKER_KEY="memory-overload"

# 准备：清理残留
kubectl delete pod "$POD" --ignore-not-found -n "$NS" >/dev/null 2>&1
kubectl delete memorypolicy "$POLICY" --ignore-not-found -n "$NS" >/dev/null 2>&1

# 2.1 创建 stress Pod（memory.limit 256Mi，emptyDir tmpfs 占 220Mi -> rate 86% > 80%）
ctx "2.1 创建 stress Pod（占用 220Mi / 256Mi limit = 86%）"
cat <<'EOF' | kubectl apply -f - >/dev/null 2>&1
apiVersion: v1
kind: Pod
metadata:
  name: stress-pod
  namespace: default
  labels:
    app: stress-test
spec:
  restartPolicy: Never
  volumes:
  - name: memtmpfs
    emptyDir:
      medium: Memory
      sizeLimit: 240Mi
  containers:
  - name: stress
    image: busybox:latest
    imagePullPolicy: Never
    volumeMounts:
    - name: memtmpfs
      mountPath: /mem
    command: ["/bin/sh", "-c"]
    args:
    - |
      dd if=/dev/zero of=/mem/blob bs=1M count=220 2>/dev/null
      echo "allocated 220Mi, holding..."
      sleep 3600
    resources:
      limits:
        memory: "256Mi"
      requests:
        memory: "32Mi"
EOF
assert_ok "stress Pod 创建"
assert_ok "stress Pod Running" kubectl wait pod "$POD" -n "$NS" --for=condition=ready --timeout=60s

# 2.2 创建 MemoryPolicy
ctx "2.2 创建 MemoryPolicy（threshold=80, add-label）"
cat <<'EOF' | kubectl apply -f - >/dev/null 2>&1
apiVersion: memory.example.com/v1
kind: MemoryPolicy
metadata:
  name: mem-policy-default
  namespace: default
spec:
  namespace: "default"
  threshold: 80
  action: "add-label"
  marker:
    key: "memory-overload"
    value: "true"
EOF
assert_ok "MemoryPolicy 创建"

# 2.3 等待 metrics 采集 + operator 打标记（metrics 周期 ~60s + reconcile 30s）
ctx "2.3 等待 Pod 被打上 $MARKER_KEY=true label（metrics 采集周期约 60-90s）"
wait_until "Pod 打上 marker label" \
  "[ \"\$(kubectl get pod $POD -n $NS -o jsonpath='{.metadata.labels.$MARKER_KEY}' 2>/dev/null)\" = 'true' ]" \
  180 && ok "Pod 已打上 $MARKER_KEY=true label"
assert_ok "归属 annotation 已加" \
  bash -c "[ \"\$(kubectl get pod $POD -n $NS -o jsonpath='{.metadata.annotations.memory\.example\.com/managed-by-policy}' 2>/dev/null)\" = '$POLICY' ]"

# 2.4 验证 operator 事件
ctx "2.4 验证 operator MarkerAdded 事件"
assert_contains "事件日志含 MarkerAdded" \
  "kubectl logs -n operator-demo-system deploy/operator-demo-controller-manager --tail=50 2>&1" \
  "MarkerAdded"

# 2.5 释放内存 -> 等待标记移除
ctx "2.5 释放 Pod 内存（删 tmpfs 文件）-> 等待 marker 移除"
kubectl exec "$POD" -n "$NS" -- rm /mem/blob 2>/dev/null || true
wait_until "Pod memory-overload label 被移除" \
  "[ -z \"\$(kubectl get pod $POD -n $NS -o jsonpath='{.metadata.labels.$MARKER_KEY}' 2>/dev/null)\" ]" \
  180 && ok "内存恢复后 marker label 已移除"
assert_ok "归属 annotation 已移除" \
  bash -c "[ -z \"\$(kubectl get pod $POD -n $NS -o jsonpath='{.metadata.annotations.memory\.example\.com/managed-by-policy}' 2>/dev/null)\" ]"
assert_contains "事件日志含 MarkerRemoved" \
  "kubectl logs -n operator-demo-system deploy/operator-demo-controller-manager --tail=50 2>&1" \
  "MarkerRemoved"

# 清理（保留 Pod/Policy 供场景 3 复用）
summary
