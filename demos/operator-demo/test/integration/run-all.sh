#!/usr/bin/env bash
# 一键运行全部集成测试场景
# 用法: bash test/integration/run-all.sh
# 前置: 已执行 00-setup.sh（环境已准备）
set -uo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

GREEN='\033[0;32m'; BLUE='\033[0;34m'; NC='\033[0m'
echo -e "${BLUE}############ 集成测试：全部场景 ############${NC}"

for scenario in "$SCRIPT_DIR"/10-webhook-validation.sh \
                 "$SCRIPT_DIR"/20-reconcile-loop.sh \
                 "$SCRIPT_DIR"/30-cleanup-on-delete.sh \
                 "$SCRIPT_DIR"/40-add-annotation.sh \
                 "$SCRIPT_DIR"/50-prometheus-metrics.sh \
                 "$SCRIPT_DIR"/60-graceful-degradation.sh; do
  echo
  echo -e "${GREEN}>>> 运行 $(basename "$scenario")${NC}"
  bash "$scenario" || { echo "场景 $(basename "$scenario") 失败，终止"; exit 1; }
done

echo
echo -e "${GREEN}############ 全部场景通过 ############${NC}"
