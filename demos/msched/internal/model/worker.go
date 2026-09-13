package model

import (
	"crypto/subtle"
	"time"
)

// Worker 执行节点实体（DESIGN §3）。
//
// UnitID 为全系统唯一标识（与 Task.UnitID 统一，DESIGN §3）：业务侧生成，
// 用于派发队列 key dispatch:{wuid}:{group} 的 wuid、worker 视图匹配返回的标识、
// RPC 请求 unit_id（worker.proto）。业务逻辑一律用 UnitID。
// ID 保留作未来 DB 主键（workers 表 schema 待定，DESIGN §3），当前无业务消费方。
// Labels 被 Task.WorkerSelector 子集匹配（S_t ⊆ L_w）。
// LeaseExpireTime 为 worker 在线心跳 lease，过期即离线（DESIGN §8.4）。
// Token 为身份校验凭证，RPC 入口（Register/Pull/Heartbeat/Report）比对校验。
//
// 作为 GORM 模型直接映射 workers 表（schema.sql）。Labels = WorkerSelector
// 复用 JSONB Valuer/Scanner（selector_codec.go），GORM 读与 raw 写路径（tx.ExecContext）
// 共用同一套序列化。未声明 CreatedAt/UpdatedAt：DB 侧 DEFAULT now() 维护，GORM Find 不读。
type Worker struct {
	ID              int64          `gorm:"column:id;primaryKey"`       // DB 物理主键（SERIAL，上层不直接用，DESIGN §3）
	UnitID          string         `gorm:"column:unit_id;uniqueIndex"` // 全系统上层唯一标识（与 Task.UnitID 统一）：派发队列 wuid + worker 视图匹配标识 + RPC unit_id
	Labels          WorkerSelector `gorm:"column:labels;type:jsonb"`   // 被 Task.WorkerSelector 子集匹配（JSONB，复用 selector_codec）
	Capacity        int            `gorm:"column:capacity"`            // 并发槽位（容量模型待定，DESIGN §11）
	LeaseExpireTime time.Time      `gorm:"column:lease_expire_time"`   // 在线心跳 lease，过期即离线（DESIGN §8.4）
	State           string         `gorm:"column:state"`
	Token           string         `gorm:"column:token"`      // 身份校验凭证，RPC 入口比对（DESIGN §10）
	CreatedAt       time.Time      `gorm:"column:created_at"` // 创建时间（DB DEFAULT now()）
	UpdatedAt       time.Time      `gorm:"column:updated_at"` // 修改时间，每次 UPDATE now() 维护
}

// TableName workers 表名（GORM 约定）。
func (Worker) TableName() string { return "workers" }

// VerifyToken 常量时间比较校验提供的 token 是否匹配该 worker 存储的 token，
// 防时序泄漏（DESIGN §10 RPC 入口校验）。stored 或 provided 为空时拒绝
// （未设置 token 的 worker 不允许通过校验）。
func (w Worker) VerifyToken(provided string) bool {
	if w.Token == "" || provided == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(w.Token), []byte(provided)) == 1
}

// ConstantTimeEqual 常量时间比较两个字符串是否相等，防时序泄漏（DESIGN §10）。
// 任一为空返回 false。供 RPC 层 Register bootstrap secret 等校验复用。
func ConstantTimeEqual(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
