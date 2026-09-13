# Data Model: 任务撮合调度

**Feature**: 001-task-matching-dispatch | **Phase**: 1 | **Date**: 2026-07-14

> 本 feature **不新增表/字段**--撮合相关实体与状态机已在 [DESIGN.md §3/§8](../../docs/DESIGN.md) 定稿、
> [SQL.md](../../docs/SQL.md) 规范化、并由 [pg/schema.go](../../internal/storage/pg/schema.go) 建表 +
> [internal/model](../../internal/model/) 实现。本文档聚焦撮合链路涉及的实体**语义与状态转换**，
> 供 tasks 阶段引用，不重复 schema DDL（见 SQL.md）。

## 实体（撮合链路相关）

### Task

业务工作单元，PostgreSQL `tasks` 表行。

| 字段 | 撮合语义 |
|------|----------|
| `unit_id` | 全系统上层唯一标识（= hash(group+Args)）；CAS 定位、派发队列 member、RPC 字段。DB 物理主键为自增 `id` |
| `group_uid` | 租户隔离 + 防饿死轮询单元；撮合 group 游标轮询的 key |
| `worker_selector` | 任务能力要求（开放 k/v）；单向强约束 `selector ⊆ Worker.Labels` 匹配依据 |
| `selector_hash` | = ComputeSelectorHash(selector)；group_stats 聚合维度键 |
| `priority` | 撮合 `ORDER BY priority, unit_id` 决定 FIFO 派发序；默认 = created_at 微秒 |
| `state` | 状态机枚举（见下） |
| `dispatch_count` | 派发次数（每次 CAS `PENDING->SCHEDULED` 累加）；`>= TaskMaxAttempt` 转 DISCARDED |
| `next_retry_time` / `fail_count` | BACKOFF 退避调度用（独立列进复合索引，不塞 JSON） |
| `max_exec_duration` | 业务给定执行时长上限（创建时设定）；RUNNING 硬截止起算 |
| `dispatch_time` | 撮合 CAS 时设；派发超时判定 `dispatch_time + dispatch_timeout` |
| `pull_time` / `timeout` | worker 拉走时设 pull_time；`timeout = pull_time + max_exec_duration`（仅 RUNNING 生效） |
| `lease_owner` | 派发租约持有 worker ID；所有权校验防后置 Report 污染他处任务 |

**无调度时间字段**：task 创建即 PENDING 即可派，不支持定时（宪法 I / DESIGN §3）。

### Worker

在线工作节点，PostgreSQL `workers` 表行 + Redis 心跳 zset `msched:hb:worker`。

| 字段 | 撮合语义 |
|------|----------|
| `unit_id` | worker 唯一标识；派发队列 key 组成、lease_owner 值 |
| `labels` | 能力标签（开放 k/v）；被 `Task.WorkerSelector` 子集匹配 |
| `lease_expire_time` | DB lease（退化模式用）；在线判定以 zset 为准 |
| `token` | Register 颁发；RPC Pull/Heartbeat/Report 鉴权 |

**在线判定**：以 `msched:hb:worker` zset 为准（Register/Heartbeat 续期 ZADD、过期 ZREMRANGEBYSCORE），
替换 DB lease。`WorkerRegistry.Match` 用 zset Active 集合过滤（[worker_registry.go:163](../../internal/storage/pg/worker_registry.go#L163)）。

### Dispatch Queue

Redis ZSET `dispatch:{wuid}:{group}`（`{wuid}` hash tag 保证同 worker 各 group 桶同 slot）。

| 属性 | 语义 |
|------|------|
| member | `unit_id`（SCHEDULED 任务） |
| score | `dispatch_time`（UnixMilli） |
| 分桶 | per-worker per-group；派发端 `PullRoundRobin` 逐桶 ZPOPMIN 各取一（多租户公平） |

**可重建**：派发队列是 PG `state='SCHEDULED'` 任务的派生态，丢失可由扫描器/重建恢复（宪法 I）。

### GroupStats

PostgreSQL `group_stats` 表，按 `(group_uid, selector_hash, state)` 聚合的任务计数。

- 行式存储（id 主键，三元组 UNIQUE），每行冗余 `selectors`（= ComputeSelectorPairs，"k=v" 按 key 排序）作可读伴随
- **实时维护**：状态转换同事务内增减对应行 count；CAS 成功者才改，多撮合器并发无重复计数
- 观测/撮合决策用，不进 Redis

## 状态机（撮合链路视角）

```
                ┌──────── Create ────────┐
                ▼                        │
           ┌─────────┐  撮合 CAS(无匹配)  ┌─────────┐
     ┌────▶│ PENDING │──────────────────▶│ BACKOFF │
     │     └─────────┘                   └─────────┘
     │           │ 撮合 CAS                │ 退避到期
     │           ▼ (匹配 worker)           │ (next_retry_time<=now)
     │      ┌──────────┐                   │
     │      │ SCHEDULED │◀──────────────────┘
     │      └──────────┘
     │            │ worker Pull
     │            ▼
     │      ┌─────────┐  Report SUCCEEDED  ┌───────────┐
     │      │ RUNNING │───────────────────▶│ COMPLETED │
     │      └─────────┘                    └───────────┘
     │            │
     │            │ Report FAILED (dispatch_count < max)
     │            └──────────────┐
     │                           ▼
     │                       (reschedule)
     │                           │
     │     超时回收(派发/执行)     │
     └───────────────────────────┘
                  │
                  │ Report FAILED (dispatch_count >= max)
                  ▼
            ┌───────────┐
            │ DISCARDED │
            └───────────┘
```

### 撮合链路状态转换（本 feature 核心）

| 转换 | 触发 | SQL（语义） | group_stats |
|------|------|-------------|-------------|
| PENDING/BACKOFF → SCHEDULED | 撮合器匹配到 worker，CAS 抢租约 | `UPDATE ... SET state='SCHEDULED', lease_owner=?, dispatch_time=now, dispatch_count=dispatch_count+1 WHERE unit_id=? AND state IN ('PENDING','BACKOFF')`（影响行=1 才算抢到） | old_state-1, SCHEDULED+1 |
| PENDING/BACKOFF → BACKOFF | 撮合无匹配在线 worker | `UPDATE ... SET state='BACKOFF', next_retry_time=now+backoff(fail_count), fail_count=fail_count+1 WHERE unit_id=? AND state IN ('PENDING','BACKOFF')` | old_state-1, BACKOFF+1 |
| SCHEDULED → RUNNING | worker Pull 批量 | `UPDATE ... SET state='RUNNING', pull_time=now, time_out=now+max_exec_duration WHERE unit_id IN (...) AND state='SCHEDULED' AND lease_owner=wuid`（所有权校验防双发） | SCHEDULED-1, RUNNING+1 |

### 防双发保证（宪法 III）

- 撮合 CAS `WHERE state IN ('PENDING','BACKOFF')`：多节点并发选同一 task，仅一条 UPDATE 命中，天然防双发
- 下发 `WHERE lease_owner=wuid`：回收重派他处的任务不命中，防误标 RUNNING
- 软分片仅 `filterOwned` 减竞争，**CAS 兜底不可移除**（N 变更瞬间 group 可能被多节点认领）

### 退避语义（宪法 V，永不放弃）

- 无匹配 worker 转 BACKOFF，序列 `30s,1m,2m,5m,10m,30m,1h` 封顶（[backoff.go](../../internal/scheduler/backoff.go)）
- **永不放弃**：与派发次数超限 DISCARDED（§8.5）不同，BACKOFF 永不转 DISCARDED
- 不主动唤醒：靠 `next_retry_time` 到期被撮合查询自然取到（DESIGN §8.6）

## 验证规则（来自需求）

- `worker_selector` 维度开放（任意 k/v），未声明维度即通配（匹配任意 worker）
- `priority <= 0` 时取 `created_at` 微秒（store 内补算，FIFO 默认）
- `dispatch_count >= TaskMaxAttempt` 转 DISCARDED（`>=` 即允许至多 N 次派发）
- 仅在线（zset Active）worker 参与匹配，离线不参与（FR-010）
