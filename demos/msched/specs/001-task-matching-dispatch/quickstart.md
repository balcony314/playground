# Quickstart: 任务撮合调度验证

**Feature**: 001-task-matching-dispatch | **Phase**: 1 | **Date**: 2026-07-14

> 端到端验证指南，证明撮合链路接通后核心 user story 可达。两条路径：
> **A. 纯单测路径**（miniredis + fake，无需外部依赖，`go test` 即跑）
> **B. 真实集成路径**（需 CockroachDB + Redis，`-tags=integration`）。
>
> 实现细节见 [tasks.md](tasks.md)（Phase 2 产出，本命令不创建）；数据模型见
> [data-model.md](data-model.md)；装配契约见 [contracts/matcher-loop.md](contracts/matcher-loop.md)。

## 前置条件

| 项 | 路径 A（单测） | 路径 B（集成） |
|----|----------------|----------------|
| Go | 1.26+ | 1.26+ |
| CockroachDB | 不需要 | 需要（`MSCHED_PG_DSN` 覆盖，默认 192.168.124.3:26257） |
| Redis | 不需要（miniredis） | 需要（`MSCHED_REDIS_ADDR`，默认 127.0.0.1:6379） |
| 工具 | `task`（[Taskfile](../../Taskfile.yaml)） | 同左 + `buf`（proto 已生成，通常不需重跑） |

## 路径 A：单测验证（无需外部依赖）

### A1. 构建与基础检查

```bash
task vet      # go vet ./... 必过
task fmt      # 格式化
go test ./internal/scheduler/...   # matcher/dispatcher/ring/backoff 单测
```

**预期**：全绿。覆盖撮合一轮（匹配/CAS/入队/退避）、派发轮询、环归属、退避序列。

### A2. 撮合链路单测（fake + miniredis）

验证 US1 核心流程（对应 SC-001/SC-003/SC-006）：

```bash
go test ./internal/scheduler/ -run Matcher -v
```

**预期场景**：
1. PENDING task + 匹配 worker → MatchOnce 返回 dispatched=1，派发队列（fake）有 member，CAS 成功
2. 无匹配 worker → task 转 BACKOFF，next_retry_time 设值，永不放弃（重复 MatchOnce 不 DISCARDED）
3. 两个 Matcher（不同 nodeID）并发 MatchOnce 同一 task → 仅一个 dispatched=1（CAS 防双发）

### A3. 装配级单测（main 装配契约）

验证 [contracts/matcher-loop.md](contracts/matcher-loop.md) 装配不变量：

```bash
go test ./cmd/scheduler/... -run MatchLoop -v   # 待 tasks 阶段实现
```

**预期**：撮合循环 goroutine 随 ctx 优雅退出（不泄漏）；WorkerRegistry 未 Start 时 MatchOnce 行为可预测。

## 路径 B：真实集成验证（需 CockroachDB + Redis）

### B1. 启动依赖

```bash
# CockroachDB（insecure，测试实例）
cockroach start-single-node --insecure --listen-addr=192.168.124.3:26257 --http-addr=192.168.124.3:8080

# Redis
redis-server --port 6379
```

### B2. 启动 Scheduler（撮合循环接入后）

```bash
# 覆盖默认连接（按实际环境）
export MSCHED_PG_HOST=192.168.124.3
export MSCHED_NODE_ID=node-1
export MSCHED_MATCH_INTERVAL=100ms   # 本 feature 新增参数
export MSCHED_MATCH_BATCH=100        # 本 feature 新增参数
task scheduler
```

**预期日志**：
```
msched-scheduler dev
scheduler node-1 已注册软分片，当前环成员: [node-1]
心跳扫描器已启动（worker 1s / task 1s）
撮合循环已启动（interval 100ms, batch 100）   ← 本 feature 新增日志
```

### B3. 端到端撮合验证

**场景 1 - 单 task 匹配下发（US1 / SC-001）**：

```bash
# 1. 注册一个 worker（labels 满足某 selector）
# 2. 创建一个 PENDING task（worker_selector 能被该 worker labels 满足）
# 3. 等待数秒（< 撮合周期若干倍）
# 4. 查 PG：task.state 应为 SCHEDULED（已撮合入队）
#    查 Redis：dispatch:{wuid}:{group} 应有该 unit_id member
```

**预期**：task 在数秒内从 PENDING → SCHEDULED，派发队列出现 member。

**场景 2 - 多租户公平（US2 / SC-004）**：

```bash
# 1. 创建 group A 100 个 task、group B 10 个 task，共享一批 worker
# 2. 持续撮合（等待 ~撮合周期 × group 数 × 若干轮）
# 3. 查 group_stats 或 dispatch 队列：A 和 B 均有任务被派发（两组推进量均 >0）
```

**预期**：B 组不被 A 组饿死，两组均有 SCHEDULED 任务。

**场景 3 - 防双发（US3 / SC-003）**：

```bash
# 1. 启动两个 scheduler 实例（node-1, node-2），共享同一 PG + Redis
# 2. 创建一批 PENDING task
# 3. 等待撮合
# 4. 查 PG：每个 task 的 dispatch_count 应为 1（未被重复 CAS）
#    查 Redis：每个 dispatch 桶内无重复 member
```

**预期**：零重复下发（dispatch_count=1，重复下发率=0）。

**场景 4 - 退避永不放弃（SC-006）**：

```bash
# 1. 创建一个 worker_selector 无任何在线 worker 满足的 task
# 2. 等待撮合
# 3. 查 PG：task.state=BACKOFF，next_retry_time 已设，fail_count=1
# 4. 注册一个 labels 满足的 worker
# 5. 等待 next_retry_time 到期 + 若干撮合周期
# 6. 查 PG：task.state 应转为 SCHEDULED（退避到期被重新撮合）
```

**预期**：曾退避的任务在 worker 恢复后被重新撮合派发，不永久丢失。

### B4. 运行集成测试套件

```bash
task test-integration   # go test -tags=integration ./...
```

**预期**：包含撮合装配端到端断言（B3 场景的自动化版本）全绿。

## 验证结果判定

| SC | 验证路径 | 通过判据 |
|----|----------|----------|
| SC-001 | A2 / B3-场景1 | 数秒内 PENDING → SCHEDULED + 派发队列有 member |
| SC-002 | B（压测，待回填） | 10 亿规模下撮合不停滞（待硬件压测校准） |
| SC-003 | A2 / B3-场景3 | 并发撮合 dispatch_count=1，零重复 |
| SC-004 | B3-场景2 | 多 group 各组推进量均 >0 |
| SC-005 | B3-场景4 | worker 离线后其 task 被回收重派 |
| SC-006 | A2 / B3-场景4 | 退避 task 在 worker 恢复后被重新撮合 |

## 不在本 quickstart 范围

- 完整实现代码 / migrations / 全量测试套件（属 tasks.md 与实现阶段）
- worker SDK / client 使用（属 cmd/worker 范围）
- 性能压测脚本（待 SC-002 校准，CLAUDE.md 待定项）
