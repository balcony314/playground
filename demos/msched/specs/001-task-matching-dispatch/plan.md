# Implementation Plan: 任务撮合调度

**Branch**: `001-task-matching-dispatch` | **Date**: 2026-07-14 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/001-task-matching-dispatch/spec.md`

## Summary

待处理任务自动匹配给能力满足其要求的在线工作节点并下发执行--系统的核心撮合派发链路。
撮合器直查 PostgreSQL PENDING/到期 BACKOFF 候选（按 group 游标轮询防饿死）-> 遍历进程内
worker 视图按单向强约束 `selector ⊆ labels` 匹配 -> CAS `PENDING/BACKOFF->SCHEDULED` 抢租约
（防双发）-> 入 Redis `dispatch:{wuid}:{group}` 派发队列。无匹配 worker 转 BACKOFF 退避永不放弃。

**现状**：撮合/派发/分片/退避/心跳扫描/存储接口**均已实现**（matcher/dispatcher/ring/backoff/
node_registry/worker_registry/scanners/task_store/dispatch_queue）。本 plan 的实质工作是
**装配与接通**：把已实现的 `Matcher` + `WorkerRegistry` 接入 `cmd/scheduler/main.go` 持续撮合
循环、补齐可配参数、补装配级集成验证。research 确认无 NEEDS CLARIFICATION（设计已定），
重点在装配决策与参数取舍。

## Technical Context

**Language/Version**: Go 1.26（module `github.com/balcony314/msched`）

**Primary Dependencies**:
- GORM v1.31 + `gorm.io/driver/postgres`（PostgreSQL/CockroachDB 访问）
- `github.com/redis/go-redis/v9`（Redis 派发队列 + 心跳 zset + 节点注册表）
- `github.com/lib/pq` + `github.com/cockroachdb/cockroach-go/v2`（CockroachDB 事务 `crdb.ExecuteTx`）
- `google.golang.org/grpc` + `google.golang.org/protobuf`（gRPC WorkerService，已实现）
- `github.com/alicebob/miniredis/v2`（Redis 单测）+ `github.com/google/go-cmp`（断言）

**Storage**: PostgreSQL(CockroachDB) 真相源（tasks/workers/group_stats 表，`pg/schema.go` 幂等建表）
+ Redis（派发队列 `dispatch:{wuid}:{group}` ZSET、worker/task 心跳 zset、节点注册表 `msched:nodes`）

**Testing**: `go test ./...`（单元 + miniredis）+ `go test -tags=integration ./...`（需 CockroachDB，
`MSCHED_PG_DSN` 覆盖）。表驱动 + 消费者侧接口 mock（`internal/scheduler/fake`）。命令见 Taskfile。

**Target Platform**: Linux server（对等无状态 Scheduler 多实例部署）

**Project Type**: 分布式调度系统（多 binary：`cmd/scheduler` + `cmd/worker`）

**Performance Goals**:
- PENDING 任务总量约 10 亿规模下持续撮合下发，规模增长不致撮合停滞（SC-002）
- 单 task 匹配到合适在线 worker 后数秒内入派发队列（SC-001）
- 多节点并发撮合零重复下发（SC-003，CAS 保证）
- 撮合吞吐与 QPS、PG 负载、派发队列规模的具体数值**待硬件压测后校准回填**（CLAUDE.md 待定项）

**Constraints**:
- 无状态：Scheduler 进程内不持海量任务索引（规避 GC 灾难）；状态全在 PG + Redis
- 软分片仅减竞争：N 变更瞬间 group 可能被多节点认领，靠 CAS 兜底防双发（不可移除 CAS）
- 未下发≠RUNNING：派发队列里是 SCHEDULED，worker 拉走才 RUNNING
- 单向强约束匹配：`Task.WorkerSelector ⊆ Worker.Labels`，不支持软偏好打分

**Scale/Scope**: 10 亿级 PENDING 任务；千级 worker × 百级 QPS 撮合；多租户 group 隔离防饿死

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

依据 [.specify/memory/constitution.md](../../.specify/memory/constitution.md) 五项原则核对：

| 原则 | 状态 | 核对结论 |
|------|------|----------|
| I. PostgreSQL 为唯一真相源 | ✅ PASS | 撮合直查 PG PENDING，无就绪池；Redis 仅派发队列/心跳 zset（可重建）；进程内仅 worker 视图缓存（可重建、弱一致）。`Matcher.MatchOnce` 调 `store.ListPendByGroup` 直查，不绕道 Redis |
| II. 无状态对等与 Pull 模式 (NON-NEGOTIABLE) | ✅ PASS | `Matcher`/`Dispatcher` 无状态；Worker Pull 非阻塞（`PullRoundRobin` 空批返回空）；状态机 `PENDING->SCHEDULED->RUNNING` 严格，未下发是 SCHEDULED 非 RUNNING |
| III. CAS 防双发不可移除 (NON-NEGOTIABLE) | ✅ PASS | `CASSchedule` 用 `WHERE state IN ('PENDING','BACKOFF')` 影响行=1 才算抢到；软分片 `Ring` 仅 `filterOwned` 减竞争，CAS 兜底保留 |
| IV. 测试先行与集成验证 | ✅ PASS（待补集成） | 单测已覆盖 matcher/dispatcher/ring/backoff；装配级集成（多节点撮合循环 + WorkerRegistry 接入）待 tasks 阶段补，已纳入 Phase 1 quickstart 验证场景 |
| V. 可观测性与简洁性 | ✅ PASS | `group_stats` 实时维护；退避永不放弃；YAGNI（pickWorker 取首个，容量模型待压测）；错误 `%w` 包装 |

**Gate 结论**：无宪法违规，无需复杂度豁免。设计已定决策（DESIGN §1/§7/§8/§9）均符合宪法，无取舍被推翻。

**Phase 1 复核**：data-model / contracts / quickstart 均未引入违反宪法的设计（见各产物），CAS/无状态/
真相源约束贯穿。PASS。

## Project Structure

### Documentation (this feature)

```text
specs/001-task-matching-dispatch/
├── plan.md              # 本文件
├── research.md          # Phase 0：装配决策与参数取舍
├── data-model.md        # Phase 1：撮合相关实体与状态机
├── quickstart.md        # Phase 1：端到端撮合验证指南
├── contracts/           # Phase 1：撮合循环与装配契约
│   └── matcher-loop.md
├── checklists/
│   └── requirements.md  # spec 质量清单（/speckit-specify 产出）
└── tasks.md             # Phase 2 输出（/speckit-tasks，本命令不创建）
```

### Source Code (repository root)

```text
cmd/scheduler/main.go            # 装配 Matcher + WorkerRegistry 撮合循环（本 feature 主要改动）
internal/scheduler/
  ├── matcher.go                 # 已实现：MatchOnce 撮合一轮（无状态 + 软分片过滤）
  ├── dispatcher.go              # 已实现：Pull 派发（非阻塞 + 批量 SCHEDULED->RUNNING）
  ├── ring.go                    # 已实现：一致性哈希环（软分片归属）
  ├── backoff.go                 # 已实现：退避序列 30s..1h 封顶永不放弃
  ├── store.go                   # 已实现：TaskStore/WorkerRegistry/DispatchQueue 等消费者侧接口
  └── fake/fake.go               # 已实现：单测 fake
internal/storage/pg/
  ├── task_store.go              # 已实现：CASSchedule/SetBackoff/BatchSetRunning/ListPendByGroup...
  ├── worker_registry.go         # 已实现：进程内 worker 视图缓存 + zset 在线判定 + 周期刷新
  └── schema.go                  # 已实现：幂等建表 + (group_uid,state,next_retry_time,priority) 索引
internal/storage/redis/
  ├── dispatch_queue.go          # 已实现：Push/PullRoundRobin（SCAN+ZPOPMIN 逐桶轮询）
  ├── node_registry.go           # 已实现：msched:nodes ZSET 续 lease + 增量重建环
  ├── worker_heartbeat.go        # 已实现：worker 在线 zset（Renew/Active/Expired）
  └── task_heartbeat.go          # 已实现：task 活性 zset（Add/RenewMany/Expired）
internal/model/
  ├── task.go                    # 已实现：Task/State/SelectorSubset
  ├── worker.go                  # 已实现：Worker/VerifyToken
  └── selector_codec.go          # 已实现：WorkerSelector JSONB 双向映射
internal/storage/config.go       # 配置：待补撮合循环/worker 视图刷新参数
```

**Structure Decision**: 沿用现有 `cmd/`（装配）+ `internal/scheduler`（撮合逻辑）+ `internal/storage`（PG/Redis）
+ `internal/model`（实体）单 Go module 布局（CODING.md §1）。本 feature **不新增源码目录**，
主要改动集中在 `cmd/scheduler/main.go`（接通撮合循环）与 `internal/storage/config.go`（可配参数），
辅以装配级集成测试。符合 YAGNI--不为已存在的能力造新结构。

## Complexity Tracking

> 无宪法违规，无需复杂度豁免。本 feature 复用已实现的核心组件，装配接通为主，不引入新架构复杂度。
