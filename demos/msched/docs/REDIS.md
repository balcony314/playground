# msched Redis 用法参考

本文汇总 msched 设计中用到的全部 Redis 功能与指令，按数据结构分组，附用途、所属角色与对应 DESIGN 章节。配套阅读 [DESIGN.md](DESIGN.md) §6/§7/§8。

> **无就绪池、无 worker 倒排**：撮合候选来自 PostgreSQL 直查，worker 视图在撮合器进程内（§4.1，周期从 PostgreSQL 刷新，不经 Redis）。Redis 只存**派发队列**（SCHEDULED 任务），规模 GB 级。

## 角色与职责

| 角色 | 来源 | Redis 操作 |
|---|---|---|
| 撮合器 | PostgreSQL PENDING -> 派发队列 | 写 ZSET（入派发队列）|
| 调度器 | 派发队列 -> Worker Pull | 读 ZSET（SCAN + ZPOPMIN）|

撮合器/调度器均可无状态多实例，状态全在 PostgreSQL + Redis，崩溃可重建。worker 视图在撮合器进程内，不进 Redis。

---

## 1. ZSET（有序集合）-- 主力结构

### 1.1 派发队列 `dispatch:{wuid}:{group}`
- **用途**：装已 CAS 抢到、待 worker 拉走的 **SCHEDULED** 任务（撮合后）。score = `dispatch_time`，member = `unitID`。**per-worker × per-group 分桶**--派发端 group 公平靠 key 结构 + 拉取轮询（撮合端另有 SQL group 游标轮询，DESIGN §9 双重公平）。group 桶按需创建（ZADD 自动建、ZREM 到空自动消）。
- **hash tag**：实际 key 形如 `dispatch:{w7}:g3`，`{wuid}` 集中同 worker 各 group 桶到同 slot，便于 SCAN。

| 指令 | 角色 | 说明 |
|---|---|---|
| `ZADD` | 撮合器 | CAS `PENDING->SCHEDULED` 成功后入 `dispatch:{wuid}:{group}`，score=dispatch_time |
| `SCAN dispatch:{wuid}:*` | 调度器 | Worker Pull 时列出该 worker 的各 group 桶 |
| `ZPOPMIN` 1 | 调度器 | 逐 group 桶各取一个，轮询凑 batch；拉走后批量 PostgreSQL `UPDATE SCHEDULED->RUNNING` |
| `ZCARD` | 监控/背压 | 某 group 桶积压水位 |
| `ZREMRANGEBYSCORE` | 回收器 | 派发超时 reschedule 时清该队列超时成员（**必须**，§8.4 防双发） |

示例（worker w7，unitID "unit-1001" 属 group g3，dispatch_time 时间戳）：
```redis
# 撮合器 CAS 成功后入派发队列（group 从 task 行读得）
ZADD dispatch:{w7}:g3 1719408005000 "unit-1001"

# Worker Pull：先列出 w7 的各 group 桶
SCAN 0 MATCH dispatch:{w7}:* COUNT 100
#   -> 命中 dispatch:{w7}:g1 / dispatch:{w7}:g3 / dispatch:{w7}:g7

# 一次 Lua EVAL 跨各桶 ZPOPMIN 凑 batch（轮询公平，非阻塞，原子）
# KEYS = [dispatch:{w7}:g1, dispatch:{w7}:g3, dispatch:{w7}:g7]（已排序），ARGV=[batch, w7]
EVAL <pullScript> 3 dispatch:{w7}:g1 dispatch:{w7}:g3 dispatch:{w7}:g7 <batch> w7
# 脚本内每轮遍历各桶 ZPOPMIN 1 个，凑满 batch 或一轮无产出止；空桶返回空即跳过
# 拉走后 scheduler 批量 UPDATE tasks SET state='RUNNING' WHERE unit_id IN (...) AND state='SCHEDULED'

# 某 group 桶积压水位
ZCARD dispatch:{w7}:g3

# 派发超时 reschedule 必须清该队列里超时（score < cutoff）的成员，防幽灵 member 被重复 ZPOPMIN（§8.4）
ZREMRANGEBYSCORE dispatch:{w7}:g3 -inf 1719407900000
```

> **非阻塞**：Pull 不用 `BZPOPMIN`，空批立即返回，Worker 端退避轮询。避免 gRPC server goroutine 钉死在 Redis 阻塞调用上。（DESIGN §1.12/§10）
>
> **轮询公平**：per-worker 拉取无并发，逐桶 ZPOPMIN 已收敛为**单次 Lua EVAL**（SCAN 出桶名作 KEYS，脚本内循环 ZPOPMIN 凑 batch），把 N 次 RTT 合为 1 次，且脚本整体原子（相比 N 次独立 ZPOPMIN 间可被插入更一致）。SCAN 保留（Lua 内不可用 SCAN）。group 桶按需建空不留。

---

## 2. Key 设计与运维要点

- **Key 前缀命名空间**：`dispatch:`。
- **TTL**：`dispatch:{wuid}:{group}` 可用 `EXPIRE` 设兜底过期，防任务终态后残留。
- **Cluster 路由**：`dispatch:{wuid}:{group}` 以 `{wuid}` 为 hash tag（实际 key 形如 `dispatch:{w7}:g3`），保证同一 worker 的各 group 派发桶落同一 slot，便于 SCAN 跨 key 操作。
- **内存**：Redis 只装派发队列（SCHEDULED 任务，已派未拉，远小于 10 亿），GB 级，无需分片分摊全量。撮合候选在 PostgreSQL，worker 视图在进程内，不占 Redis。
- **持久化**：Redis 为可重建缓存（真相源在 PostgreSQL），RDB/AOF 可弱化；派发队列丢失后可由 PostgreSQL SCHEDULED 状态任务重建。

---

## 3. 指令速查表

| 指令 | 结构 | 角色 | 用途 |
|---|---|---|---|
| `ZADD` | ZSET | 撮合器 | 入 `dispatch:{wuid}:{group}` |
| `SCAN dispatch:{wuid}:*` | 通用 | 调度器 | 列 worker 的各 group 派发桶 |
| `ZPOPMIN` 1 | ZSET | 调度器 | 逐 group 桶各取一个（轮询公平，非阻塞） |
| `ZREM` | ZSET | 回收器 | 清派发桶残影 |
| `ZCARD` | ZSET | 监控 | 派发桶水位 |
| `ZRANGEBYSCORE` | ZSET | 回收器 | 找过期成员 |
| `ZREMRANGEBYSCORE` | ZSET | 回收器 | 清派发桶超时成员（**必须**，§8.4） |
| `EXPIRE` | 通用 | 运维 | 兜底过期防残留 |
