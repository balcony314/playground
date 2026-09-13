// Package model 定义 msched 的核心数据模型（task/worker）与匹配语义。
//
// 详见 docs/DESIGN.md §3（数据模型）与 §4（匹配语义）。
package model

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"time"
)

// State 任务状态机枚举（DESIGN §8.1）。
type State string

const (
	// StatePending 待撮合，正常参与撮合查询。
	StatePending State = "PENDING"
	// StateBackoff 无匹配 worker 退避中，到 NextRetryTime 才查（DESIGN §8.6）。
	StateBackoff State = "BACKOFF"
	// StateScheduled 已调度，待 worker 拉取（未下发，没在执行）。
	StateScheduled State = "SCHEDULED"
	// StateRunning worker 已拉走下发，执行中。
	StateRunning State = "RUNNING"
	// StateCompleted 执行成功终态，写 Result（DESIGN §8.7）。
	StateCompleted State = "COMPLETED"
	// StateDiscarded 超过重试上限，放弃（DESIGN §8.5）。
	StateDiscarded State = "DISCARDED"
)

// WorkerSelector 任务对 worker 的单向强约束（DESIGN §4）。
// selector 维度开放：业务可自定义任意 k/v，未声明的维度即通配（匹配任意 worker）。
// 匹配语义：S_t ⊆ L_w -- selector 每对 k=v 必须在 worker Labels 中存在且相等。
//
// 同时作 Worker.Labels 的存储类型：实现 driver.Valuer + sql.Scanner（selector_codec.go），
// JSONB 双向映射，GORM 读路径与 raw 写路径（tx.ExecContext）共用同一套序列化。
type WorkerSelector map[string]string

// Task 任务实体（DESIGN §3）。
//
// ID 为 DB 物理主键（SERIAL，CRDB unique_rowid），上层一律用 UnitID 定位，不直接读 ID。
// UnitID 为全系统上层唯一标识（UNIQUE 约束 + CAS 定位 + Redis 派发队列 member + RPC 字段），
// = hash(group+Args)，相同 group+Args 产生相同 UnitID，支持创建去重与幂等。
// Priority 为撮合 FIFO 排序键，默认 = created_at（Unix 微秒），业务可调升优先级。
// 不支持定时：创建即 PENDING 即可派，无调度时间字段。
//
// 作为 GORM 模型直接映射 tasks 表（schema.sql）。WorkerSelector = JSONB（复用
// selector_codec.go 的 Valuer/Scanner，GORM 读与 raw 写路径共用）。MaxExecDuration
// 底层 int64，GORM 按 bigint 存取；写路径 INSERT/UPDATE 显式 int64() 转换。
// 可空时间（DispatchTime/PullTime/Timeout）用 time.Time 非指针：写路径全 raw，INSERT
// 不列这些列（DB DEFAULT NULL），GORM Find 读 NULL -> 零值，与原 toModel 语义等价。
// NextRetryTime 用 *time.Time 指针，nil <-> NULL。未声明 CreatedAt/UpdatedAt：
// DB 侧 DEFAULT now()，GORM Find 不读。
type Task struct {
	ID              int64          `gorm:"column:id;primaryKey"`              // DB 物理主键（SERIAL，上层不直接用，DESIGN §3）
	UnitID          string         `gorm:"column:unit_id;uniqueIndex"`        // 上层唯一标识 + CAS 定位键（= hash(group+Args)，UNIQUE 约束，DESIGN §3）
	GroupUID        string         `gorm:"column:group_uid"`                  // 租户隔离 ID（多租户公平作用域，DESIGN §9）
	Priority        int64          `gorm:"column:priority"`                   // 撮合 ORDER BY 排序键，默认 = created_at 微秒；业务可调（DESIGN §3）
	State           State          `gorm:"column:state;type:text"`            //nolint:revive // 状态机枚举，命名见 §8.1
	WorkerSelector  WorkerSelector `gorm:"column:worker_selector;type:jsonb"` // 单向匹配约束 S_t ⊆ L_w（JSONB，复用 selector_codec）
	SelectorHash    string         `gorm:"column:selector_hash"`              // = ComputeSelectorHash(WorkerSelector)，group_stats 维度键（DESIGN §3）
	MaxExecDuration time.Duration  `gorm:"column:max_exec_duration"`          // 业务给定执行时长上限，SCHEDULED->RUNNING 时算 Timeout
	Args            []byte         `gorm:"column:args"`                       // 任务参数（大字段留 PostgreSQL，不进 Redis）
	//
	Result        []byte     `gorm:"column:result"`          // 执行结果
	DispatchTime  time.Time  `gorm:"column:dispatch_time"`   // CAS PENDING->SCHEDULED 时设；派发超时判定依据
	PullTime      time.Time  `gorm:"column:pull_time"`       // worker 拉走时设；执行硬截止起算点
	Timeout       time.Time  `gorm:"column:timeout"`         // = PullTime + MaxExecDuration，仅 RUNNING 阶段生效
	NextRetryTime *time.Time `gorm:"column:next_retry_time"` // BACKOFF 退避的下次可撮合时间（PENDING 时 nil）
	FailCount     int        `gorm:"column:fail_count"`      // 连续无匹配次数，决定退避时长
	DispatchCount int        `gorm:"column:dispatch_count"`  // 派发重试计数，超限转 DISCARDED
	LeaseOwner    string     `gorm:"column:lease_owner"`     // 派发租约持有 worker ID
	CreatedAt     time.Time  `gorm:"column:created_at"`      // 创建时间（DB DEFAULT now()）
	UpdatedAt     time.Time  `gorm:"column:updated_at"`      // 修改时间，每次 UPDATE now() 维护（DESIGN §3）
}

// TableName tasks 表名（GORM 约定）。
func (Task) TableName() string { return "tasks" }

// SelectorSubset 判断单向强约束 S_t ⊆ L_w（DESIGN §4）。
//
// selector 中每对 k=v 必须在 labels 中存在且相等；selector 未声明的维度即通配，
// 匹配任意 worker。空 selector 匹配所有 worker。
//
// 注意：撮合器进程内遍历 worker 视图调本函数匹配（DESIGN §4.1，开放 k/v selector
// 亦成立）；本函数为通用子集判断，供 WorkerRegistry 实现与校验使用。
func SelectorSubset(selector WorkerSelector, labels map[string]string) bool {
	for k, v := range selector {
		lv, ok := labels[k]
		if !ok || lv != v {
			return false
		}
	}
	return true
}

// ComputeUnitID 计算任务单元 ID = hash(group+Args)（DESIGN §3）。
//
// 用于基于业务字段标识同一"单元"：相同 group+Args 产生相同 UnitID，
// 支持创建去重与幂等。用长度前缀拼接避免字段边界歧义（"a"+"bc" ≠ "ab"+"c"），
// SHA-256 抗碰撞，hex 编码（64 字符）。
func ComputeUnitID(group string, args []byte) string {
	h := sha256.New()
	fmt.Fprintf(h, "%d:%s:%d:", len(group), group, len(args))
	h.Write(args)
	return hex.EncodeToString(h.Sum(nil))
}

// ComputeSelectorPairs 返回 selector 按 key 字典序排列的 "k=v" 键值对切片（DESIGN §3）。
//
// WorkerSelector 是 map[string]string，遍历顺序不确定；先排序 key 保证输出稳定，
// 相同 selector 无论 map 插入序如何都产出相同切片。空 selector 返回空切片（通配维度）。
// 用途：GroupStats.Selectors，作为 selector_hash 的可读伴随表示，观测查询时直接看懂
// 聚合维度，无需反查 hash。
func ComputeSelectorPairs(selector WorkerSelector) []string {
	keys := make([]string, 0, len(selector))
	for k := range selector {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, len(keys))
	for i, k := range keys {
		pairs[i] = k + "=" + selector[k]
	}
	return pairs
}

// ComputeSelectorHash 计算 WorkerSelector 的稳定 hash（DESIGN §3）。
//
// 基于 ComputeSelectorPairs 产出的有序 "k=v" 对，对每个 pair 加长度前缀再 SHA-256 hex
// 编码。长度前缀消除拼接歧义（pair 整体边界清楚）。与 ComputeSelectorPairs 共享同一份
// 有序数据：pairs 是可读投影，hash 是 pairs 的指纹。空 selector 产出一个有效 hash
// （空输入的 SHA-256），是一个独立维度。用于 group_stats 按 (group_uid, selector_hash)
// 聚合任务计数（DESIGN §3）。
func ComputeSelectorHash(selector WorkerSelector) string {
	h := sha256.New()
	for _, p := range ComputeSelectorPairs(selector) {
		fmt.Fprintf(h, "%d:%s:", len(p), p)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// GroupStats 按 (group_uid, selector_hash, state) 聚合的任务计数（DESIGN §3）。
//
// group_stats 表行式存储：主键 (group_uid, selector_hash, state)，每态一行。
// 实时维护：任务状态转换时同事务内增减对应行 count。
// 观测/撮合决策查询用，不进 Redis（PostgreSQL 派生数据）。
// Selectors 为 selector_hash 的可读伴随（"k=v" 对按 key 排序），查询时直接看懂
// 聚合维度，无需反查 hash；同 hash 行冗余相同值（规模小可忽略）。
type GroupStats struct {
	GroupUID     string
	SelectorHash string
	Selectors    []string // = ComputeSelectorPairs(WorkerSelector)，selector_hash 的可读伴随
	State        State
	Count        int64
}
