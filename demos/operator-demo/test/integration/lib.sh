#!/usr/bin/env bash
# 集成测试公共函数：颜色、断言、等待工具
# 用法: source test/integration/lib.sh
set -uo pipefail

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; BLUE='\033[0;34m'; NC='\033[0m'
PASS=0; FAIL=0

ctx() { echo -e "${BLUE}[$(date +%H:%M:%S)]${NC} $*"; }
ok()   { echo -e "${GREEN}  ✓ PASS${NC}: $*"; PASS=$((PASS+1)); }
bad()  { echo -e "${RED}  ✗ FAIL${NC}: $*"; FAIL=$((FAIL+1)); }
warn() { echo -e "${YELLOW}  ! ${NC}: $*"; }

# 断言：命令应成功
assert_ok() {
  local desc="$1"; shift
  if "$@" >/dev/null 2>&1; then ok "$desc"
  else bad "$desc（命令失败: $*）"; fi
}

# 断言：命令应失败
assert_fail() {
  local desc="$1"; shift
  if "$@" >/dev/null 2>&1; then bad "$desc（本应失败但成功了: $*）"
  else ok "$desc"; fi
}

# 断言输出包含子串: assert_contains "desc" "cmd..." "substring"
assert_contains() {
  local desc="$1" cmd="$2" sub="$3"
  local out; out=$(bash -c "$cmd" 2>&1 || true)
  if echo "$out" | grep -qF "$sub"; then ok "$desc"
  else bad "$desc（输出未含 '$sub'，实际: $out）"; fi
}

# 断言输出不含子串
assert_not_contains() {
  local desc="$1" cmd="$2" sub="$3"
  local out; out=$(bash -c "$cmd" 2>&1 || true)
  if echo "$out" | grep -qF "$sub"; then bad "$desc（输出含 '$sub'，实际: $out）"
  else ok "$desc"; fi
}

# 等待条件成立: wait_until "描述" "条件命令" "超时秒"
wait_until() {
  local desc="$1" cond="$2" timeout="${3:-120}"
  local i=0
  while [ $i -lt $timeout ]; do
    if bash -c "$cond" >/dev/null 2>&1; then return 0; fi
    sleep 5; i=$((i+5))
  done
  bad "$desc 超时（${timeout}s 未满足）"
  return 1
}

summary() {
  echo
  echo -e "${BLUE}==================== 测试结果 ====================${NC}"
  echo -e "  ${GREEN}通过: $PASS${NC}    ${RED}失败: $FAIL${NC}"
  if [ "$FAIL" -gt 0 ]; then
    echo -e "${RED}存在失败用例${NC}"
    exit 1
  fi
  echo -e "${GREEN}全部通过${NC}"
}
