// Package scheduler 实现撮合器与派发器（无状态对等多实例，DESIGN §5/§7/§9）。
//
// 本包定义存储访问接口（消费者侧，CODING §4）：接口在此定义，真实实现
// （PostgreSQL/Redis）在 internal/storage，测试用 fake 实现在 internal/scheduler/fake。
package scheduler

import (
	"context"
	"time"

	"github.com/balcony314/msched/internal/model"
)

// ScheduledTask 已入派发队列的任务引用（SCHEDULED 状态，待 worker 拉走）。
//
// 由派发队列 PullRoundRobin 返回，携带 group 供监控/调试；UnitID 是任务唯一标识
// （= hash(group+Args)，DESIGN §3），对应 PostgreSQL tasks.UnitID。
type ScheduledTask struct {
	UnitID string
	Group  string
	Wuid   string
}

// TaskStore 是 PostgreSQL 任务真相源访问接口（DESIGN §3/§7/§8）。
//
// 撮合器直查 PostgreSQL PENDING（无就绪池），靠 (group_uid, state, next_retry_time, priority)
// 复合索引支撑。CAS 语义：影响行=1 才算抢到租约。全系统以 UnitID 为唯一标识/定位键。
type TaskStore interface {
	// Create 创建任务（DESIGN §3）。幂等：相同 group+args 产生相同 UnitID，重复创建
	// 返回已存在任务（tasks.unit_id UNIQUE 冲突 no-op）。store 内补算 unit_id / selector_hash /
	// priority（<=0 时取 created_at 微秒，DESIGN §3），初始 state=PENDING，同事务
	// group_stats PENDING count+1。入参 in 仅读 GroupUID / Args / WorkerSelector /
	// MaxExecDuration / Priority，其余字段由 store 置位。返回含算好字段的完整任务。
	Create(ctx context.Context, in model.Task) (model.Task, error)

	// ListByGroup 按 group list 任务（面向 API 的泛读，全状态可选，DESIGN §3）。
	// states 为空表示不过滤；按 priority, unit_id 升序（与撮合序一致）；limit/offset 分页。
	// 不读 args/result 等大字段（list 不拉大 payload）。
	ListByGroup(ctx context.Context, groupUID string, states []model.State, limit, offset int) ([]model.Task, error)

	// ListActiveGroups 返回有 PENDING/到期 BACKOFF 任务的活跃 group 列表（DESIGN §9.5）。
	// 撮合器据此环形轮询，大户 group 不独占撮合产出。
	ListActiveGroups(ctx context.Context) ([]string, error)

	// ListPendByGroup 按 group 游标取一批候选任务（DESIGN §9.1/§7）。
	// SQL: WHERE group_uid=? AND (state='PENDING' OR (state='BACKOFF' AND next_retry_time<=now))
	//      ORDER BY priority, unit_id LIMIT n
	ListPendByGroup(ctx context.Context, group string, n int) ([]model.Task, error)

	// CASSchedule 撮合 CAS：PENDING/BACKOFF → SCHEDULED（DESIGN §8.2）。
	// unitID 为任务唯一标识，owner 为派发租约持有 worker ID。返回 true 表示抢到（影响行=1）。
	// 实时维护 group_stats：抢到时 old_state count-1、SCHEDULED count+1（DESIGN §3）。
	CASSchedule(ctx context.Context, unitID string, owner string) (bool, error)

	// SetBackoff 无匹配 worker 退避：PENDING/BACKOFF -> BACKOFF（DESIGN §8.6）。
	// nextRetry 为退避到期时间（= now + NextBackoff(failCount)，由撮合器算好传入），
	// store 内 fail_count++。永不放弃（封顶 1h）。状态已变（他节点 CAS 抢走）时 no-op。
	// 实时维护 group_stats：退避时 old_state count-1、BACKOFF count+1（DESIGN §3）。
	SetBackoff(ctx context.Context, unitID string, nextRetry time.Time) error

	// BatchSetRunning worker 拉走后批量 SCHEDULED → RUNNING（DESIGN §8.3）。
	// 带 lease_owner=wuid 所有权校验：只转属于该 worker 的 SCHEDULED 任务，
	// 防止回收重派他处后被误标 RUNNING（防双发，§8.4/§8.7）。
	// 返回实际转为 RUNNING 的 unitID 集合，调用方据此只返回这些任务（dispatcher.Pull）。
	// 设 pull_time + timeout。实时维护 group_stats：每个 SCHEDULED count-1、RUNNING count+1（§3）。
	BatchSetRunning(ctx context.Context, wuid string, unitIDs []string) ([]string, error)

	// ListByUnitIDs 按 UnitID 批量读任务详情（worker Pull 返回 []Task，DESIGN §10）。
	ListByUnitIDs(ctx context.Context, unitIDs []string) ([]model.Task, error)

	// Complete 执行成功回传：CAS RUNNING -> COMPLETED，写 Result（DESIGN §8.7）。
	// 所有权校验 lease_owner==wuid（防后置 Report 污染他处执行的任务，§8.4/§8.7）：
	// 仅 state='RUNNING' AND lease_owner=wuid 才命中（影响行=1）。
	// 返回 true 表示命中并转 COMPLETED；false 表示状态已变/所有权不符，Report 忽略。
	// 实时维护 group_stats：RUNNING count-1、COMPLETED count+1。
	Complete(ctx context.Context, unitID, wuid string, result []byte) (bool, error)

	// ReportFail 执行失败回传：CAS 所有权校验后按 dispatch_count 兜底（DESIGN §8.5/§8.6）。
	// 仅 state='RUNNING' AND lease_owner=wuid 才命中。命中时：
	//   dispatch_count >= maxAttempt -> DISCARDED（放弃）
	//   否则 -> PENDING（reschedule，重新可撮合；不入 BACKOFF，因之前匹配过 worker，§8.6）
	// 返回 reported（是否命中；false 则忽略）+ discarded（命中且转 DISCARDED）。
	// 实时维护 group_stats：RUNNING count-1、目标态 count+1。
	// dispatch_count 在 CAS PENDING->SCHEDULED 时已累加（§8.2），此处不重复 +1。
	ReportFail(ctx context.Context, unitID, wuid string, maxAttempt int) (reported bool, discarded bool, err error)

	// ListGroupStats 返回某 group 下各 (selector_hash, state) 的任务计数（DESIGN §3）。
	// group_stats 表行式存储，实时维护。观测/撮合决策查询用。
	// 每行 Selectors 为 selector_hash 的可读伴随（"k=v" 按 key 排序），无需反查 hash。
	ListGroupStats(ctx context.Context, groupUID string) ([]model.GroupStats, error)

	// Delete 物理删除任务，返回实际删除行数。
	//
	// 两种模式（互斥）：
	//   - unitIDs 非空：仅删属于 groupUID 且 unit_id 在列表中的任务
	//   - unitIDs 为空：删 groupUID 下全部任务
	//
	// 同事务维护 group_stats：删指定时按 (selector_hash, state) 聚合减 count（行保留，
	// count 可降至 0，与实时维护一致）；删整个 group 时连 group_stats 该 group 所有行
	// 一起删（group 不再存在）。
	//
	// 仅删 PostgreSQL 真相源，不主动清 Redis 派发队列：SCHEDULED 任务删除后 dispatch 桶
	// 可能有残留 member，worker 拉走时 BatchSetRunning 因行不存在 no-op（无害，靠现有
	// 防双发所有权校验兜底）。物理删除绕过状态机，适用管理/运维清理（SQL.md §4.2）。
	Delete(ctx context.Context, groupUID string, unitIDs []string) (int64, error)

	// RecoverByWorker 回收某 worker 名下全部 RUNNING + SCHEDULED 任务 -> PENDING（DESIGN §8.4）。
	// worker 心跳 zset 过期（worker 离线）时由扫描器调用。RUNNING/SCHEDULED 均 group_stats
	// old_state-1/PENDING+1。返回实际回收的 unitID 列表。SCHEDULED 残留的 Redis 派发桶清理
	// 由调用方另做。CAS 天然防多节点重复回收（影响行校验）。同事务维护 group_stats。
	RecoverByWorker(ctx context.Context, wuid string) ([]string, error)

	// RecoverTask 单 task 执行超时回收：CAS RUNNING -> PENDING（DESIGN §8.4）。
	// task 心跳 zset 过期（worker 崩溃不续期 / 硬截止 timeout 到）时由扫描器调用。
	// 不校验 lease_owner（zset member 只存 unitID，过期即回收任意 RUNNING）。影响行=1 才算回收。
	// group_stats RUNNING-1/PENDING+1。返回是否命中（false=状态已变/已回收，忽略）。
	RecoverTask(ctx context.Context, unitID string) (bool, error)
}

// WorkerRegistry 是 worker 视图访问接口（DESIGN §4/§6）。
//
// Match 遍历在线 worker，按单向强约束 S_t ⊆ L_w 返回匹配的 worker ID 列表。
// 实现为进程内 worker 视图缓存（千级 worker × 百级 QPS，开放 k/v selector 亦成立，
// DESIGN §4），周期从 PostgreSQL 刷新（默认 1s，可配）。缓存可重建、弱一致：
// 崩溃无损、扩缩容即开即用；worker 上下线延迟可见靠派发/执行超时兜底（§8.4）。
type WorkerRegistry interface {
	// Match 返回满足 selector 单向强约束 S_t ⊆ L_w 的在线 worker ID 列表。
	// selector 未声明维度即通配，匹配任意 worker。
	Match(ctx context.Context, selector model.WorkerSelector) ([]string, error)
}

// DispatchQueue 是 Redis 派发队列访问接口（DESIGN §6/§9.2）。
//
// 队列按 dispatch:{wuid}:{group} 分桶，group 公平靠 key 结构 + 拉取轮询（§9.2）。
// member 为 UnitID（全系统统一标识）。
type DispatchQueue interface {
	// Push 撮合 CAS 成功后入派发队列（ZADD），score 为 dispatch_time（DESIGN §8.2）。
	Push(ctx context.Context, wuid, group string, unitID string, score int64) error

	// PullRoundRobin 派发端 group 轮询：SCAN 该 worker 各 group 桶，逐桶 ZPOPMIN 1
	// 各取一个，凑满 batch 或所有桶空为止（DESIGN §9.2）。非阻塞，空批返回空切片。
	PullRoundRobin(ctx context.Context, wuid string, batch int) ([]ScheduledTask, error)
}

// WorkerStore 是 worker 写入与查询接口（DESIGN §10 Register/Heartbeat + token 校验）。
//
// Register/Heartbeat 直接写 PostgreSQL workers 表；WorkerRegistry（读视图缓存，§4.1）
// 下次刷新时可见。GetByUnitID 供 RPC 入口 token 校验（model.Worker.VerifyToken）。
// 二者职责分离：WorkerRegistry.Match 是撮合读视图，WorkerStore 是 RPC 写入与鉴权查询。
type WorkerStore interface {
	// Register 上线注册（DESIGN §10）。upsert workers 表：labels/capacity/lease_expire_time/
	// token/state。lease_expire_time = now + leaseTTL。相同 unit_id 重复注册覆盖刷新
	// （labels 变更/lease 续期）。返回含算好 lease_expire_time 的完整 worker。
	Register(ctx context.Context, w model.Worker, leaseTTL time.Duration) (model.Worker, error)

	// Heartbeat 刷新 lease + 任务级活性心跳回收（DESIGN §10/§8.4 补充）。
	//  1. UPDATE workers.lease_expire_time = now + leaseTTL
	//  2. 回收：lease_owner=unitID AND state='RUNNING' AND unit_id NOT IN runningUnitIDs 的任务
	//     批量转 PENDING（提前 reschedule，不必等执行硬截止 Timeout，§8.4 补充）
	//  返回新 lease_expire_time + recovered（被回收的 unitID 列表，告知 worker 丢弃本地状态）。
	//  runningUnitIDs 为空时回收该 worker 全部 RUNNING（worker 持有空集即全丢）。
	Heartbeat(ctx context.Context, unitID string, runningUnitIDs []string, leaseTTL time.Duration) (leaseExpire time.Time, recovered []string, err error)

	// GetByUnitID 按 unit_id 查 worker（token 校验用，DESIGN §10）。未找到返回错误。
	GetByUnitID(ctx context.Context, unitID string) (model.Worker, error)
}

// WorkerHeartbeat 是 worker 在线心跳 zset（msched:hb:worker）访问接口（DESIGN §8.4 补充）。
//
// worker 在线判定以 zset 为准（替换 DB lease）：Register/Heartbeat 续期 ZADD、过期
// ZREMRANGEBYSCORE。WorkerRegistry 刷新时 Active 拉在线集合；扫描器 Expired 拉过期
// worker 回收其名下任务。
type WorkerHeartbeat interface {
	// Renew 续期 worker lease（ZADD score=expire_ts）。
	Renew(ctx context.Context, unitID string, expire time.Time) error
	// Remove 摘除 worker（ZREM，优雅下线/回收后清理）。
	Remove(ctx context.Context, unitID string) error
	// Active 返回当前在线 worker unitID 集合（ZRANGEBYSCORE now +inf，WorkerRegistry.Match 用）。
	Active(ctx context.Context) (map[string]struct{}, error)
	// Expired 返回已过期 worker unitID 列表（ZRANGEBYSCORE -inf now，扫描器回收用）。
	Expired(ctx context.Context) ([]string, error)
	// CleanExpired 幂等清过期 member（ZREMRANGEBYSCORE -inf now，任节点可做）。
	CleanExpired(ctx context.Context) error
}

// TaskHeartbeatItem 续期单项：UnitID + 到期时间（= min(now+ttl, 硬截止 timeout)）。
type TaskHeartbeatItem struct {
	UnitID string
	Expire time.Time
}

// TaskHeartbeat 是 task 活性心跳 zset（msched:hb:task）访问接口（DESIGN §8.4 补充）。
//
// task 心跳复用 worker Heartbeat 的 runningUnitIds（不改 worker SDK）：Pull 转 RUNNING
// 时 Add、Heartbeat 续期 RenewMany、Report 完成 Remove。expire cap 到硬截止 timeout
// （不可续期，CLAUDE.md §8.12）。扫描器 Expired 拉过期 task 回收。
type TaskHeartbeat interface {
	// Add 新 task 入 zset（ZADD，Pull 转 RUNNING 时）。expire cap 硬截止。
	Add(ctx context.Context, unitID string, expire time.Time) error
	// RenewMany 批量续期（ZADD 覆盖 score，Heartbeat runningUnitIds 时）。expire cap 硬截止。
	RenewMany(ctx context.Context, items []TaskHeartbeatItem) error
	// Remove 删 task（ZREM，Report 完成/失败时）。
	Remove(ctx context.Context, unitID string) error
	// RemoveMany 批量删（ZREM，Heartbeat recovered 时）。
	RemoveMany(ctx context.Context, unitIDs []string) error
	// Expired 返回已过期 task unitID 列表（ZRANGEBYSCORE -inf now，扫描器回收用）。
	Expired(ctx context.Context) ([]string, error)
	// CleanExpired 幂等清过期 member（ZREMRANGEBYSCORE -inf now，任节点可做）。
	CleanExpired(ctx context.Context) error
}
