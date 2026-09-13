#!/usr/bin/env bash
# 场景 5：Prometheus 自定义指标
# 验证：Pod 被标记后，/metrics 端点暴露 memoryguard_marked_pods{policy,namespace} 指标且值正确
set -uo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

echo -e "${BLUE}=== 场景 5: Prometheus 自定义指标 ===${NC}"

NS="default"
POLICY="mem-policy-default"
POD="stress-pod"
METRIC="memoryguard_marked_pods"
METRICS_SVC="operator-demo-controller-manager-metrics-service"
OP_NS="operator-demo-system"
SA="operator-demo-controller-manager"

# 准备：清理残留 + 创建标记 Pod
kubectl delete pod "$POD" --ignore-not-found -n "$NS" >/dev/null 2>&1
kubectl delete memorypolicy "$POLICY" --ignore-not-found -n "$NS" >/dev/null 2>&1

ctx "5.1 创建 stress Pod + MemoryPolicy，等待 Pod 被标记"
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

# 等待 Pod 被标记（确认有标记才查指标）
if ! wait_until "Pod 被标记" \
  "[ \"\$(kubectl get pod $POD -n $NS -o jsonpath='{.metadata.labels.memory-overload}' 2>/dev/null)\" = 'true' ]" \
  180; then
  bad "前置失败：Pod 未被标记，无法验证指标"
  summary; exit 1
fi
ok "Pod 已被标记"

ctx "5.2 port-forward metrics 服务并查询 /metrics"
PF_PORT=8443
kubectl port-forward -n "$OP_NS" "svc/$METRICS_SVC" ${PF_PORT}:${PF_PORT} >/dev/null 2>&1 &
PF_PID=$!
trap 'kill $PF_PID 2>/dev/null' EXIT
sleep 3

# 用 operator SA token 认证（已通过 metrics-reader-rolebinding 授权）
SA_TOKEN=$(kubectl create token -n "$OP_NS" "$SA" 2>/dev/null)
METRICS_OUT=$(curl -sk -H "Authorization: Bearer $SA_TOKEN" "https://localhost:${PF_PORT}/metrics" 2>/dev/null)

if echo "$METRICS_OUT" | grep -q "^# HELP ${METRIC}"; then
  ok "/metrics 含 ${METRIC} 指标定义"
else
  bad "/metrics 未含 ${METRIC} 指标定义"
  warn "metrics 输出片段: $(echo "$METRICS_OUT" | head -3)"
fi

# 验证指标值：policy=mem-policy-default, namespace=default 应为 1
METRIC_LINE=$(echo "$METRICS_OUT" | grep "^${METRIC}{" | grep "policy=\"${POLICY}\"" | grep "namespace=\"${NS}\"" || true)
if [ -n "$METRIC_LINE" ]; then
  val=$(echo "$METRIC_LINE" | awk -F' ' '{print $2}')
  if [ "$val" = "1" ]; then
    ok "指标值正确：${METRIC_LINE}"
  else
    bad "指标值应为 1，实际: $val（$METRIC_LINE）"
  fi
else
  bad "未找到 policy=${POLICY},namespace=${NS} 的指标行"
fi

# 清理
kubectl delete memorypolicy "$POLICY" --ignore-not-found -n "$NS" >/dev/null 2>&1
kubectl delete pod "$POD" --ignore-not-found -n "$NS" >/dev/null 2>&1

summary
