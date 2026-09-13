#!/usr/bin/env bash
# 场景 6：优雅降级
# 验证：Metrics API 不可用时，降级为 request/limit 估算使用率，仍能标记 Pod，且 status=Degraded
# 注意：本场景会临时删除 metrics-server，测试后自动恢复
set -uo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

echo -e "${BLUE}=== 场景 6: 优雅降级（Metrics API 不可用 -> request/limit 估算）===${NC}"

NS="default"
POLICY="mem-policy-degraded"
POD="stress-degraded-pod"
MARKER_KEY="memory-overload"

# 准备：清理残留
kubectl delete pod "$POD" --ignore-not-found -n "$NS" >/dev/null 2>&1
kubectl delete memorypolicy "$POLICY" --ignore-not-found -n "$NS" >/dev/null 2>&1

ctx "6.1 临时删除 metrics-server（模拟 Metrics API 不可用）"
MS_REPLICA=$(kubectl get deployment -n kube-system metrics-server -o jsonpath='{.spec.replicas}' 2>/dev/null)
kubectl scale deployment -n kube-system metrics-server --replicas=0 2>/dev/null
warn "metrics-server 已缩容到 0 副本（原 $MS_REPLICA）"
assert_ok "metrics-server 副本为 0" bash -c "[ \"\$(kubectl get deployment -n kube-system metrics-server -o jsonpath='{.spec.replicas}' 2>/dev/null)\" = '0' ]"
# 等一会确保 API 不可用
warn "等待 20s 让 Metrics API 缓存失效 ..."
sleep 20

ctx "6.2 创建 Pod（request=210Mi/limit=256Mi=82% > 阈值 80%，降级时按 request/limit 估算）"
cat <<'EOF' | kubectl apply -f - >/dev/null 2>&1
apiVersion: v1
kind: Pod
metadata:
  name: stress-degraded-pod
  namespace: default
  labels:
    app: stress-test
spec:
  restartPolicy: Never
  containers:
  - name: app
    image: busybox:latest
    imagePullPolicy: Never
    command: ["/bin/sh", "-c"]
    args:
    - |
      dd if=/dev/zero of=/dev/shm/blob bs=1M count=200 2>/dev/null
      sleep 3600
    resources:
      limits:
        memory: "256Mi"
      requests:
        memory: "210Mi"
EOF
assert_ok "Pod 创建"
assert_ok "Pod Running" kubectl wait pod "$POD" -n "$NS" --for=condition=ready --timeout=60s

ctx "6.3 创建 MemoryPolicy 并等待降级标记"
cat <<'EOF' | kubectl apply -f - >/dev/null 2>&1
apiVersion: memory.example.com/v1
kind: MemoryPolicy
metadata:
  name: mem-policy-degraded
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

# 降级路径：reconcile 用 request/limit 估算，210/256=82% > 80% 应标记
ctx "6.4 等待 Pod 被标记（降级估算路径）"
if wait_until "Pod 降级路径标记" \
  "[ \"\$(kubectl get pod $POD -n $NS -o jsonpath='{.metadata.labels.$MARKER_KEY}' 2>/dev/null)\" = 'true' ]" \
  120; then
  ok "降级时 Pod 仍被标记（request/limit 估算生效）"
else
  bad "降级时 Pod 未被标记"
fi

ctx "6.5 验证 MemoryPolicy status=Degraded"
# status condition Available=False, reason=MetricsDegraded
REASON=$(kubectl get memorypolicy "$POLICY" -n "$NS" -o jsonpath='{.status.conditions[?(@.type=="Available")].reason}' 2>/dev/null)
STATUS=$(kubectl get memorypolicy "$POLICY" -n "$NS" -o jsonpath='{.status.conditions[?(@.type=="Available")].status}' 2>/dev/null)
if [ "$REASON" = "MetricsDegraded" ] && [ "$STATUS" = "False" ]; then
  ok "status Available=False/MetricsDegraded（降级已反映到 status）"
else
  bad "status 应为 Degraded，实际: status=$STATUS reason=$REASON"
fi

ctx "6.6 验证 operator 日志含降级提示"
assert_contains "日志含 MetricsDegraded" \
  "kubectl logs -n operator-demo-system deploy/operator-demo-controller-manager --tail=80 2>&1" \
  "MetricsDegraded"

# 清理：恢复 metrics-server
ctx "6.7 恢复 metrics-server"
kubectl scale deployment -n kube-system metrics-server --replicas="${MS_REPLICA:-1}" 2>/dev/null
warn "metrics-server 已恢复到 $MS_REPLICA 副本"
assert_ok "metrics-server 副本恢复" bash -c "[ \"\$(kubectl get deployment -n kube-system metrics-server -o jsonpath='{.spec.replicas}' 2>/dev/null)\" = '${MS_REPLICA:-1}' ]"

# 清理测试资源
kubectl delete memorypolicy "$POLICY" --ignore-not-found -n "$NS" >/dev/null 2>&1
kubectl delete pod "$POD" --ignore-not-found -n "$NS" >/dev/null 2>&1

summary
