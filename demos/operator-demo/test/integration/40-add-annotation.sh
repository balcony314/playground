#!/usr/bin/env bash
# 场景 4：add-annotation 动作
# 验证：action=add-annotation 时，超阈值 Pod 被加上 marker 注解（而非 label）
set -uo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

echo -e "${BLUE}=== 场景 4: add-annotation 动作 ===${NC}"

NS="default"
POLICY="mem-policy-annotation"
POD="stress-anno-pod"
MARKER_KEY="memory-overload-anno"

# 准备：清理残留
kubectl delete pod "$POD" --ignore-not-found -n "$NS" >/dev/null 2>&1
kubectl delete memorypolicy "$POLICY" --ignore-not-found -n "$NS" >/dev/null 2>&1

ctx "4.1 创建 stress Pod（256Mi limit，占 220Mi）"
cat <<'EOF' | kubectl apply -f - >/dev/null 2>&1
apiVersion: v1
kind: Pod
metadata:
  name: stress-anno-pod
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
assert_ok "stress Pod 创建"
assert_ok "stress Pod Running" kubectl wait pod "$POD" -n "$NS" --for=condition=ready --timeout=60s

ctx "4.2 创建 MemoryPolicy（action=add-annotation）"
cat <<'EOF' | kubectl apply -f - >/dev/null 2>&1
apiVersion: memory.example.com/v1
kind: MemoryPolicy
metadata:
  name: mem-policy-annotation
  namespace: default
spec:
  namespace: "default"
  threshold: 80
  action: "add-annotation"
  marker:
    key: "memory-overload-anno"
    value: "true"
EOF
assert_ok "MemoryPolicy（add-annotation）创建"

ctx "4.3 等待 Pod 被打上 $MARKER_KEY annotation（而非 label）"
if wait_until "Pod 打上 marker annotation" \
  "[ \"\$(kubectl get pod $POD -n $NS -o jsonpath='{.metadata.annotations.$MARKER_KEY}' 2>/dev/null)\" = 'true' ]" \
  180; then
  ok "Pod 已打上 $MARKER_KEY=true annotation"
else
  bad "Pod 未被打上 annotation"
fi
# 确认没被打上 label（action 是 annotation，不应有同名 label）
assert_ok "Pod 未被打上同名 label" \
  bash -c "[ -z \"\$(kubectl get pod $POD -n $NS -o jsonpath='{.metadata.labels.$MARKER_KEY}' 2>/dev/null)\" ]"

# 清理
kubectl delete memorypolicy "$POLICY" --ignore-not-found -n "$NS" >/dev/null 2>&1
kubectl delete pod "$POD" --ignore-not-found -n "$NS" >/dev/null 2>&1

summary
