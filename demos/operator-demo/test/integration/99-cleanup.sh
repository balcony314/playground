#!/usr/bin/env bash
# 清理集成测试资源：删除测试 Pod / MemoryPolicy / 卸载 operator（可选）
# 用法:
#   bash test/integration/99-cleanup.sh          # 仅清理测试 CR/Pod，保留 operator
#   bash test/integration/99-cleanup.sh --all      # 额外卸载 operator（make undeploy）
set -uo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
cd "$REPO_ROOT"

GREEN='\033[0;32m'; NC='\033[0m'
log() { echo -e "${GREEN}[cleanup]${NC} $*"; }

UNDEPLOY=0
[ "${1:-}" = "--all" ] && UNDEPLOY=1

# 清理测试 CR / Pod
log "删除测试 MemoryPolicy ..."
kubectl delete memorypolicy -A --all --ignore-not-found 2>/dev/null || true
log "删除测试 Pod ..."
kubectl delete pod -n default stress-pod stress-anno-pod --ignore-not-found 2>/dev/null || true

if [ "$UNDEPLOY" -eq 1 ]; then
  log "卸载 operator（make undeploy）..."
  make undeploy || true
else
  log "保留 operator 部署（如需卸载: bash $0 --all）"
fi

log "清理完成"
