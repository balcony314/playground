# Research: 任务撮合调度

**Feature**: 001-task-matching-dispatch | **Phase**: 0 | **Date**: 2026-07-14

## 概述

本 feature 的 Technical Context 无 NEEDS CLARIFICATION--msched 的架构决策已在
[DESIGN.md](../../docs/DESIGN.md) §1/§4/§7/§8/§9 定稿，[宪法](../../.specify/memory/constitution.md)
将其固化为不可违反的硬约束。撮合/派发/分片/退避/心跳扫描/存储接口的**代码均已实现**
（见 [plan.md Project Structure](plan.md)）。

因此 Phase 0 研究聚焦于**装配决策与参数取舍**，而非技术选型：
1. 已实现组件如何接通成持续运行的撮合链路
2. 撮合循环周期、批大小、worker 选择策略等可配参数的默认值
3. 退避抖动（CLAUDE.md 待定项）是否在本 feature 范围补齐

---

## R1. 撮合循环装配方式

**Decision**: 在 `cmd/scheduler/main.go` 构造 `Matcher`（`NewWithShard`，注入共享 `*Ring`）+
`WorkerRegistry`（注入 `WorkerHeartbeat` zset），二者后台 goroutine 持续运行：
- `WorkerRegistry.Start` 后台周期刷新进程内 worker 视图（已实现，1s 默认）
- 撮合循环 goroutine：`for { select { case <-ctx.Done(): return; case <-ticker.C: matcher.MatchOnce(ctx) } }`
- 两者与现有 gRPC / HTTP / node_registry / 心跳扫描器同生命周期，随 `ctx` 优雅启停

**Rationale**:
- `Matcher` 与 `Dispatcher` 都是无状态多实例（宪法 II），`Ring` 已由 `NodeRegistry` 后台 mutate 共享引用
  （[node_registry.go:9](../../internal/storage/redis/node_registry.go#L9)），matcher 持同引用自动看到新成员
- `WorkerRegistry` 在线判定已切到 zset（[worker_registry.go:22](../../internal/storage/pg/worker_registry.go#L22)），
  必须注入 `workerHB` 并 `Start`，否则 `Match` 返回空（撮合全部转 BACKOFF）
- main.go 现有注释明确标注此缺口："撮合循环（matcher.MatchOnce）随 gRPC server 落地后接入"
  （[cmd/scheduler/main.go:5](../../cmd/scheduler/main.go#L5)）

**Alternatives considered**:
- 事件驱动撮合（task 创建即触发）：违反"无就绪池、无扫描器"与无状态约束（宪法 I），且 10 亿规模下事件风暴不可控，否决
- 单次撮合由 Pull 触发：违反 Pull 热路径零撮合（DESIGN §9.2），且无 Pull 时任务永不被撮合，否决
- 周期轮询撮合（本方案）：无状态、可重建、与心跳扫描器范式一致，CAS 兜底防多节点重复，采纳

---

## R2. 撮合循环周期默认值

**Decision**: 撮合循环周期默认 `100ms`（`MatchInterval`，可配，env `MSCHED_MATCH_INTERVAL`）。

**Rationale**:
- SC-001 要求"数秒内入派发队列"，100ms 周期 + 单轮 `ListPendByGroup` 直查索引 = 远低于秒级
- 过短（如 10ms）压 PG 撮合查询 QPS，且软分片后每 group 单节点无竞争，无需极快
- 过长（如 1s）违背 SC-001"数秒"体感（边界场景多个 group 轮询周期累积）
- 与 `WorkerRefresh=1s`、`NodeRefresh=1s` 解耦：撮合循环可更勤（候选查得多），worker 视图刷新慢一点无伤（弱一致，靠派发/执行超时兜底）

**Alternatives considered**:
- 无间隔忙轮询（CPU 100%、压 PG）：否决
- 自适应速率（按积压量调速）：YAGNI，待压测后按 CLAUDE.md 待定项校准，本 feature 不引入

---

## R3. 撮合批大小 N 默认值

**Decision**: 单轮单 group 候选批大小 `N=100`（`MatchBatchSize`，可配，env `MSCHED_MATCH_BATCH`）。

**Rationale**:
- `MatchOnce` 单轮只处理游标当前 group 一批（[matcher.go:85](../../internal/scheduler/matcher.go#L85)），
  N 太小则大户 group 每轮推进少、游标轮询全 group 一圈耗时拉长；太大则单轮持锁 PG 查询久、饿死其他 group
- 100 是经验值：单轮 100 候选 × 逐个 CAS，单 group 既快速推进又不独占撮合时间片
- 10 亿规模下具体值待压测校准（CLAUDE.md 待定项），先给可配默认

**Alternatives considered**:
- N=1000：单轮过重，长事务压 PG，否决
- N=10：大户 group 推进太慢，游标全圈耗时高，否决

---

## R4. Worker 选择策略（pickWorker）

**Decision**: 本 feature 维持现状取列表首个（`wuids[0]`，[matcher.go:143](../../internal/scheduler/matcher.go#L143)），
但 `Match` 返回已排序（`sort.Strings`），结果稳定可测。容量模型与"最闲 worker 轮询"留作后续优化
（DESIGN §11 待定项），不阻塞本 feature。

**Rationale**:
- 容量模型未定（CLAUDE.md 待定项），现在引入加权/最闲策略缺乏数据支撑，违反 YAGNI（宪法 V）
- 取首个 + 排序稳定，行为可测、可复现
- 派发端 `PullRoundRobin` 已按 group 分桶轮询保证 worker 间拉取公平，单 worker 选择策略对多租户公平无影响

**Alternatives considered**:
- 随机选（`wuids[rand]`）：`Math.random` 在 workflow 脚本受限（非本场景），且不可测，否决
- 最闲 worker（按各 worker 派发队列水位选）：需额外读 Redis 各桶 ZCARD，热路径开销，且容量模型待定，否决留后

---

## R5. 退避抖动（CLAUDE.md 待定项）

**Decision**: 本 feature **不补**退避抖动。`backoff.go` 现有序列 `30s,1m,2m,5m,10m,30m,1h` 封顶
（[backoff.go:14](../../internal/scheduler/backoff.go#L14)）已满足"永不放弃"（宪法 V、FR-006）。
抖动待补但不阻塞撮合核心链路，作为独立后续项。

**Rationale**:
- 抖动用于避免退避任务到期同步重试风暴，但本设计退避靠 `next_retry_time` 到期被查询自然取到
  （DESIGN §8.6 不主动唤醒），不同 task 的 fail_count 不同导致退避时长天然分散，同步风暴风险低
- 抖动需在 `NextBackoff` 引入随机性，涉及可测性处理，属独立小项，不应混入撮合装配 feature
- 宪法与 CLAUDE.md 均将抖动列为"待补"，非阻塞

**Alternatives considered**:
- 本 feature 顺带加抖动：扩大 scope、降低内聚，违反"一次一逻辑变更"，否决

---

## R6. 装配级集成验证范围

**Decision**: Phase 1 quickstart 覆盖两条端到端验证路径（无需真实 CockroachDB 即可跑的 miniredis + fake 路径，
与需 CockroachDB 的真实 PG 路径），验证 SC-001/SC-003/SC-004/SC-006。多节点并发零重复（SC-003）
与节点接管（SC-005）由 `Matcher` 的 CAS + 共享 `Ring` 保证，纳入集成断言。

**Rationale**:
- 宪法 IV 要求集成测试覆盖状态机转换、CAS 并发竞争、Redis 派发队列、gRPC 契约、跨存储端到端
- 撮合装配是"把已测组件接通"，集成测试重点验证**接通后的端到端链路**而非重测单组件

**Alternatives considered**:
- 仅单测不集成：违反宪法 IV，无法验证装配正确性，否决

---

## NEEDS CLARIFICATION 状态

**无**。所有 Technical Context 项已明确，未标记 NEEDS CLARIFICATION。待回填项（性能数值、容量模型、
抖动）均属 CLAUDE.md 明示的"实现时再定/压测后回填"，不阻塞 plan 推进，已在各 Decision 标注。

## 待压测回填项（不阻塞，记录追踪）

- 撮合循环 QPS vs PG 负载（定 `MatchInterval` 生产值）
- 撮合批大小 N 在 10 亿规模下的最优值
- worker 容量模型与派发队列水位关系（定 `pickWorker` 策略）
- 撮合器整体吞吐（SC-002 定量值）
