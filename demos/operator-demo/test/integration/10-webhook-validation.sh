#!/usr/bin/env bash
# 场景 1：validating webhook 校验
# 验证：非法 threshold（>100）被拒、空 marker.key 被拒、合法 CR 创建成功
set -uo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

echo -e "${BLUE}=== 场景 1: validating webhook 校验 ===${NC}"

TMPF="$(mktemp)"
trap 'rm -f "$TMPF"' EXIT

# 准备：确保无同名残留 CR
kubectl delete memorypolicy bad-policy bad-marker valid-policy --ignore-not-found -n default >/dev/null 2>&1 || true

# 1.1 非法 threshold=150 应被拒（CRD schema 校验 Maximum=100）
ctx "1.1 非法 threshold=150 应被拒"
cat > "$TMPF" <<'EOF'
apiVersion: memory.example.com/v1
kind: MemoryPolicy
metadata:
  name: bad-policy
  namespace: default
spec:
  namespace: "default"
  threshold: 150
  action: "add-label"
  marker:
    key: "memory-overload"
    value: "true"
EOF
out=$(kubectl apply -f "$TMPF" 2>&1 || true)
if echo "$out" | grep -qiE "threshold|invalid"; then
  ok "threshold=150 创建被拒"
else
  bad "threshold=150 应被拒（实际: $out）"
fi

# 1.2 空 marker.key 应被拒（webhook programmatic 校验）
ctx "1.2 空 marker.key 应被拒"
cat > "$TMPF" <<'EOF'
apiVersion: memory.example.com/v1
kind: MemoryPolicy
metadata:
  name: bad-marker
  namespace: default
spec:
  threshold: 80
  action: "add-label"
  marker:
    key: ""
    value: "true"
EOF
out=$(kubectl apply -f "$TMPF" 2>&1 || true)
if echo "$out" | grep -q "marker.key must not be empty"; then
  ok "空 marker.key 被 webhook 拒"
else
  bad "空 marker.key 应被拒（实际: $out）"
fi

# 1.3 合法 CR 应创建成功
ctx "1.3 合法 CR 应创建成功"
cat > "$TMPF" <<'EOF'
apiVersion: memory.example.com/v1
kind: MemoryPolicy
metadata:
  name: valid-policy
  namespace: default
spec:
  namespace: "default"
  threshold: 80
  action: "add-label"
  marker:
    key: "memory-overload"
    value: "true"
EOF
assert_ok "合法 CR 创建成功" kubectl apply -f "$TMPF"
assert_ok "valid-policy 存在" kubectl get memorypolicy valid-policy -n default

# 清理
kubectl delete memorypolicy valid-policy --ignore-not-found -n default >/dev/null 2>&1 || true

summary
