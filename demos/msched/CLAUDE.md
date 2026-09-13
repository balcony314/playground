# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 当前状态

本仓库已有最小脚手架（cmd/ + internal/version + Taskfile），业务代码待实现。计划用 Go + GORM + PostgreSQL（CockroachDB）+ Redis + gRPC 实现。
设计文档：[DESIGN.md](docs/DESIGN.md) / [REDIS.md](docs/REDIS.md)。
**代码风格**：遵循 [CODING.md](docs/CODING.md)（Uber Go 规范 + golang-standards 项目布局），写代码前必读。
**SQL/表结构**：遵循 [SQL.md](docs/SQL.md)（命名/字段/索引/约束规范 + msched 取舍），改 schema 前必读。
构建命令见 [Taskfile.yaml](Taskfile.yaml)：`task build / test / vet / fmt / tidy`。

## 项目定位

`msched` 是**无中心、Pull 模式、10 亿级任务**的分布式调度系统。硬约束：
- 规模：PENDING 任务总量约 10 亿。**不支持定时**——task 创建即可派发，无调度时间；PostgreSQL(CockroachDB) 是全量真相源，撮合器直查 PostgreSQL(CockroachDB) PENDING（**无就绪池**），新 group 立即可撮合不被积压阻塞
- 无中心：一组对等**无状态** Scheduler（无主）撮合并下发；Worker 只拉取执行
- Pull：Worker 主动拉，Scheduler 不 push
- 状态：Scheduler 进程无状态，Redis 只存派发队列（SCHEDULED 任务）+ worker 注册表；PostgreSQL(CockroachDB) 是唯一真相源。进程内不持有海量索引，规避 Go GC 灾难
- 多租户：按 `GroupUID` 隔离级防饿死（双重 group 轮询，非严格等量）

## 架构关键决策（详见 docs/DESIGN.md §1）

实现任何部分前必须理解这些已定决策，避免推翻已评估过的取舍：

1. **状态机**：`PENDING（待撮合）→ SCHEDULED（已调度待拉取，未下发没在执行）→ RUNNING（worker 拉走下发执行中）→ COMPLETED/DISCARDED`。**未下发的任务不是 RUNNING**（见 §8）。
2. **撮合层**：撮合器**直查 PostgreSQL(CockroachDB) PENDING**（无就绪池）→ 匹配 worker → CAS `PENDING→SCHEDULED` → 入 per-worker 派发队列；Worker Pull 时直接拉现成队列，热路径零撮合零 CAS。
3. **撮合数据结构**：Redis 只存派发队列 `dispatch:{wuid}:{group}` ZSET（SCHEDULED 任务）。**无就绪池、无扫描器、无 worker 倒排索引**（worker 视图在撮合器进程内，§4.1）。selector 维度开放（见 §4）。
4. **多租户公平**：隔离级防饿死——撮合端 SQL group 游标轮询取候选 + 派发端 `dispatch:{wuid}:{group}` 分桶 worker 轮询各取一个，双重 group 轮询。**非长期严格等量**（各 group 待派量不等/空桶跳过/worker 速度差致长期偏斜）。严格等量需中心化仲裁，代价高不采纳。
5. **节点对等（软分片）**：一致性哈希环按 `group_uid` 归属节点，各节点只撮合自己认领的 group，消除多实例对同一活跃 group 的 CAS 竞争。加删节点只影响相邻段。CAS 兜底保留：N 变更瞬间 group 可能被多节点认领，靠 CAS 天然防双发。扩缩容即开即用（详见 §5）。
6. **Worker 路由**：直连任一 Scheduler，扫 `dispatch:{wuid}:{group}` 轮询各 group 桶各取一个。
7. **撮合候选源**：撮合器 `SELECT ... WHERE state='PENDING' AND group_uid=? ORDER BY priority, unit_id LIMIT N`（按 group 游标轮询），靠 PostgreSQL(CockroachDB) `(group_uid, state, next_retry_time, priority)` 索引支撑。无就绪池同步链路。
8. **防双发与下发**：撮合器 CAS `UPDATE ... SET state='SCHEDULED' WHERE unit_id=? AND state='PENDING'`（影响行=1 才算抢到）→ 入派发队列；worker 拉走时批量 `UPDATE SCHEDULED→RUNNING`（per-worker 无竞争，非 CAS）。
9. **RPC**：gRPC + protobuf。Worker 接口：`Register` / `Pull` / `Heartbeat` / `Report`。
10. **匹配语义**：单向强约束 `Task.WorkerSelector ⊆ Worker.Labels`。**selector 维度开放**（业务可自定义任意 k/v），撮合器进程内遍历 worker 视图调 `SelectorSubset` 匹配（千级 worker × 百级 QPS），无需 Redis 倒排预建。**已删除** `Worker.TaskSelector` 反向匹配。
11. **拉取语义（Pull 定稿）**：Worker 主动 `Pull(batch=N)` 拉 `dispatch:{wuid}:{group}`，**非阻塞**——有则返回、空则立即返回空批，Worker 端退避轮询；Scheduler 不主动 push。
12. **超时（两阶段）**：派发超时（SCHEDULED 阶段，`dispatch_time+dispatch_timeout`，短）+ 执行超时（RUNNING 阶段，`pull_time+max_exec_duration`，硬截止），分别 reschedule 回 PENDING。`max_exec_duration` 为 task 字段，业务创建时给定。
13. **无匹配 worker 退避**：撮合时 selector 无在线 worker 匹配 -> task `state` 转 `BACKOFF` + 指数退避（封顶 1h），不参与撮合查询；**纯退避到期**（`next_retry_time` 到期被撮合查询自然取到），不主动唤醒。**永不放弃**（见 §8.6）。退避态合并进 state，无独立字段。

## 关键硬约束（不可违反）

- **软分片仅减竞争，不依赖正确性**：分片只为消除 CAS 竞争，N 变更瞬间 group 可能被多节点认领，必须靠 §8.2 CAS `WHERE state='PENDING'` 兜底防双发，不可移除 CAS。
- **未下发≠RUNNING**：派发队列里的任务是 SCHEDULED，worker 拉走才 RUNNING。不可在撮合 CAS 时直接置 RUNNING。

## 数据模型语义

- `tasks.State`：枚举 `PENDING / BACKOFF / SCHEDULED / RUNNING / COMPLETED / DISCARDED`（BACKOFF 见 §8.6；COMPLETED 为执行成功终态见 §8.7）。
- `tasks.DispatchCount`：**派发次数**（每次 CAS `PENDING→SCHEDULED` 累加，含派发超时未拉走），`>= TaskMaxAttempt` 转 DISCARDED（§8.5，`>=` 即允许至多 N 次派发）。非执行失败次数。
- `tasks.Result`：执行成功（COMPLETED）时写入；COMPLETED 行由回收器按保留期异步归档（§8.7），避免 10 亿级 tasks 表无限增长。
- `tasks.DispatchTime`：撮合 CAS 时设；派发超时判定依据 `DispatchTime + DispatchTimeout`。
- `tasks.PullTime`：worker 拉走时设；执行硬截止起算点。
- `tasks.MaxExecDuration`：业务给定执行时长上限，创建时设定。
- `tasks.Timeout`：仅 RUNNING 阶段生效（= `PullTime + MaxExecDuration`）。
- `tasks.UnitID`：= hash(group+Args)，**全系统上层唯一标识**（UNIQUE 约束 + CAS 定位 + Redis 派发队列 member + RPC 字段）。DB 物理主键为自增 `id`（SERIAL），上层一律用 UnitID 定位；相同 group+Args 产生相同 UnitID，支持创建去重与幂等。
- `tasks.Priority`：撮合 `ORDER BY priority, unit_id` 决定 FIFO 派发序；默认 = created_at（Unix 微秒），业务可调升优先级。
- `tasks.NextRetryTime` / `FailCount`：BACKOFF 退避调度用（独立列，进复合索引，不塞 JSON meta）。
- 无调度时间字段：task 创建即 PENDING 即可派，不支持定时。
- `tasks.Labels`：删除反向匹配后不再参与撮合，当前保留作观测/过滤（是否有消费方**待确认**，见 §11）。
- `tasks.SelectorHash`：= `ComputeSelectorHash(WorkerSelector)`（基于排序 "k=v" 对 + 长度前缀 + SHA-256 hex），group_stats 按 (group_uid, selector_hash) 聚合计数的维度键。创建时算好存入。
- `workers.Labels`：被 `Task.WorkerSelector` 子集匹配；`LeaseExpireTime` 为 worker 在线心跳。
- `group_stats`：按 `(group_uid, selector_hash, state)` 聚合的任务计数表（行式，id 主键，三元组 UNIQUE）。**实时维护**--状态转换（§8 状态机）同事务内增减对应行 count；CAS 成功者才改，多撮合器并发无重复计数。观测/撮合决策用，不进 Redis。每行冗余 `selectors`（= `ComputeSelectorPairs`，"k=v" 按 key 排序）作 selector_hash 的可读伴随，查询直接看懂维度，无需反查 hash。

## 待定项（实现时再定，勿擅自固化）

- worker 视图刷新周期默认 1s -> 可配，权衡 PG 查询频率与 worker 上下线可见延迟。
- `dispatch_timeout` 默认值 → 待定，平衡 worker 拉取延迟与 reschedule 抖动。
- 退避序列参数（起始/封顶/抖动）-> 已定 30s..1h 封顶，抖动待补（见 §8.6）。
- 撮合查询批大小 N、撮合器消费速率、worker 本地队列水位、派发队列积压阈值 → 留默认 + 可配。**撮合循环周期 `MatchInterval` 默认 100ms（env `MSCHED_MATCH_INTERVAL`）、批大小 N `MatchBatchSize` 默认 100（env `MSCHED_MATCH_BATCH`）已定**（cmd/scheduler/main.go 装配 runMatchLoop），其余待压测校准。
- worker 容量模型与派发队列水位关系（背压）→ 待定。
- OSS payload 阈值默认 64 KB（假设，可调）。
- 硬件校准：撮合查询 QPS 与 PostgreSQL(CockroachDB) 负载、派发队列规模、撮合器吞吐需压测后回填参数。
- 软分片成员发现机制 -> **已定**（DESIGN §5/§6）：Redis `msched:nodes` ZSET（member=nodeID，score=lease_expire_ts），周期 1s `ZADD` 续 lease + `ZRANGEBYSCORE` 拉全量增量重建环 + `ZREMRANGEBYSCORE` 幂等清过期；nodeID 配置注入（env `MSCHED_NODE_ID`，空兜底 hostname）；lease TTL 5s。扩缩容迁移平滑度（虚节点重排抖动、迁移期任务归属切换）-> 待压测后回填。
