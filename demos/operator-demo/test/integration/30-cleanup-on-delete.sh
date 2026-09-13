#!/usr/bin/env bash
# 场景 3：删除 MemoryPolicy -> finalizer 清理标记
# 验证：Policy 删除后，其添加的 marker label + 归属 annotation 被清理，且 Policy 彻底删除（非卡在 Terminating）
set -uo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

echo -e "${BLUE}=== 场景 3: 删除 MemoryPolicy 的 finalizer 清理 ===${NC}"

NS="default"
POLICY="mem-policy-default"
POD="stress-pod"
MARKER_KEY="memory-overload"

# 准备：确保 Pod 超阈值且有标记（若场景 2 已清理标记，需重新占内存）
kubectl delete pod "$POD" --ignore-not-found -n "$NS" >/dev/null 2>&1
kubectl delete memorypolicy "$POLICY" --ignore-not-found -n "$NS" >/dev/null 2>&1

ctx "3.1 创建 stress Pod + MemoryPolicy，等待 Pod 被标记"
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
      sleep 3600
    resources:
      limits:
        memory: "256Mi"
      requests:
        memory: "32Mi"
EOF
kubectl wait pod "$POD" -n "$NS" --for=condition=ready --timeout=60s >/dev/null 2>&1

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
ok "stress Pod + MemoryPolicy 创建"

# 等待 Pod 被标记（确认清理前确实有标记）
if wait_until "Pod 被打上 marker label" \
  "[ \"\$(kubectl get pod $POD -n $NS -o jsonpath='{.metadata.labels.$MARKER_KEY}' 2>/dev/null)\" = 'true' ]" \
  180; then
  ok "Pod 确实已被标记（准备测试删除清理）"
else
  bad "前置条件失败：Pod 未被标记，无法测清理"
  summary; exit 1
fi

ctx "3.2 删除 MemoryPolicy -> 等待 finalizer 清理"
kubectl delete memorypolicy "$POLICY" -n "$NS" 2>/dev/null

# finalizer 清理后 Policy 应彻底消失
if wait_until "MemoryPolicy 彻底删除（finalizer 移除）" \
  "! kubectl get memorypolicy $POLICY -n $NS >/dev/null 2>&1" \
  90; then
  ok "MemoryPolicy 已彻底删除（非卡在 Terminating）"
else
  bad "MemoryPolicy 卡在 Terminating（finalizer 清理失败）"
  warn "当前状态: $(kubectl get memorypolicy $POLICY -n $NS -o jsonpath='{.metadata.deletionTimestamp}' 2>/dev/null)"
fi

ctx "3.3 验证 Pod marker 已被清理"
assert_ok "Pod marker label 已移除" \
  bash -c "[ -z \"\$(kubectl get pod $POD -n $NS -o jsonpath='{.metadata.labels.$MARKER_KEY}' 2>/dev/null)\" ]"
assert_ok "Pod 归属 annotation 已移除" \
  bash -c "[ -z \"\$(kubectl get pod $POD -n $NS -o jsonpath='{.metadata.annotations.memory\.example\.com/managed-by-policy}' 2>/dev/null)\" ]"

summary
