# Contract: 撮合循环与装配

**Feature**: 001-task-matching-dispatch | **Phase**: 1 | **Date**: 2026-07-14

> msched 对外 RPC 契约（gRPC WorkerService `Register/Pull/Heartbeat/Report`）已由
> [proto/msched/worker/v1/worker.proto](../../../proto/msched/worker/v1/worker.proto) 定义并实现
> （commit ab49c6b）。本 feature 不改对外 RPC 契约。
>
> 本契约定义**本 feature 新增的内部装配契约**：撮合循环 goroutine 的行为契约与 `main.go` 装配约束。
> 供 tasks 阶段实现与集成测试验证。

## 撮合循环契约（Matcher Loop）

### 行为

撮合循环是 `cmd/scheduler/main.go` 启动的后台 goroutine，周期调用 `Matcher.MatchOnce(ctx)`：

```
启动顺序（main.go）：
  1. NodeRegistry.Start（同步建环，已存在）
  2. WorkerRegistry.Start（同步首次刷新，注入 workerHB zset）  ← 本 feature 接入
  3. gRPC / HTTP server（已存在）
  4. 心跳扫描器 Start（已存在）
  5. 撮合循环 goroutine：for { select { case <-ctx.Done(): return; case <-ticker.C: matcher.MatchOnce(ctx) } }  ← 本 feature 新增

退出顺序（ctx 取消）：
  撮合循环随 ctx 退出（goroutine select <-ctx.Done()）
  其余组件按现有优雅停机流程
```

### 不变量

| 不变量 | 保证方式 |
|--------|----------|
| 撮合循环随 ctx 优雅退出 | `select { case <-ctx.Done(): return }`，不裸 `for` |
| WorkerRegistry 必须先于撮合循环 Start 且注入 workerHB | 否则 Match 全返空 → 全部转 BACKOFF（撮合空转） |
| Matcher 持与 NodeRegistry 共享的同一 `*Ring` 引用 | 后台 mutate 自动可见新成员（node_registry.go:9） |
| 撮合循环错误不致命 | `MatchOnce` 返回 error 仅 log，不退出循环（弱一致，下轮重试） |
| 多节点并发撮合零重复 | CAS `WHERE state IN ('PENDING','BACKOFF')` 兜底（宪法 III） |

## 装配契约（main.go）

### 新增构造

```go
// WorkerRegistry（进程内 worker 视图 + zset 在线判定，DESIGN §4.1）
registry := pg.NewWorkerRegistry(db, cfg.WorkerRefresh, workerHB)

// Matcher（带软分片，注入共享 ring + nodeID，DESIGN §5/§7）
matcher := scheduler.NewWithShard(taskStore, registry, dq, cfg.MatchBatchSize, ring, nodeID)
```

### 新增可配参数（config.go）

| 参数 | 默认 | env | 语义 |
|------|------|-----|------|
| `MatchInterval` | `100ms` | `MSCHED_MATCH_INTERVAL` | 撮合循环周期（research R2） |
| `MatchBatchSize` | `100` | `MSCHED_MATCH_BATCH` | 单轮单 group 候选批大小 N（research R3） |

### 生命周期集成

`WorkerRegistry` 与撮合循环加入现有优雅停机：
- `WorkerRegistry.Start(ctx)` 在 `NodeRegistry.Start` 之后、server 启动之前
- 撮合循环 goroutine 在心跳扫描器 Start 之后启动，与 `ctx` 绑定
- 停机时 `WorkerRegistry.Stop()` 加入现有 `grpcSrv.Stop` / `nr.Stop` 序列

## 不改动的契约（本 feature 范围外）

- gRPC WorkerService proto 与实现（已实现，commit ab49c6b）
- HTTP API（创建/list/进度，已有脚手架）
- TaskStore / DispatchQueue / 心跳 zset / 扫描器接口（已实现，本 feature 仅消费）
- worker SDK / client（cmd/worker）

## 验证契约（集成测试）

集成测试须验证（对应 spec SC）：

| 场景 | 验证 | 对应 SC |
|------|------|---------|
| PENDING task + 匹配在线 worker → MatchOnce 后入 dispatch 队列且 state=SCHEDULED | 派发队列有 member + PG state 转换 | SC-001 |
| 无匹配在线 worker → state=BACKOFF + next_retry_time 设值 | PG state + fail_count++ | SC-006 |
| 多 Matcher 并发 MatchOnce 同一 task → 仅一个 CAS 成功 | 派发队列仅 1 member + dispatch_count=1 | SC-003 |
| 两 group（A 大 B 小）持续撮合 → A/B 均有推进 | 两组 dispatched 均 >0 | SC-004 |
| 高优先级先派发 | 按 priority, unit_id 序入队 | FR-007 |
| worker 离线（zset 过期）→ 其名下 task 被回收重派 | 回收后 state=PENDING 可再撮合 | SC-005（含扫描器） |
