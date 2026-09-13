// task_heartbeat.go task 活性心跳 zset（DESIGN §8.4 补充）。
//
// task 心跳复用 worker Heartbeat 的 runningUnitIds（不改 worker SDK/proto）：Pull 转 RUNNING
// 时 Add、Heartbeat 续期 RenewMany、Report 完成 Remove。expire 由调用方 cap 到硬截止 timeout
// （不可续期，CLAUDE.md §8.12）。key=msched:hb:task，member=task unitID，score=expire_ts(ms)。
//
// 扫描器 Expired 拉过期 task -> CAS RUNNING->PENDING 回收 -> Remove 清理。

package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/balcony314/msched/internal/scheduler"
	"github.com/redis/go-redis/v9"
)

// taskHeartbeatKey task 活性心跳 zset key（单 ZSET 全集群共享）。
const taskHeartbeatKey = "msched:hb:task"

// TaskHeartbeat 实现 scheduler.TaskHeartbeat（task 活性心跳 zset，DESIGN §8.4 补充）。
type TaskHeartbeat struct {
	client *redis.Client
}

// NewTaskHeartbeat 构造 TaskHeartbeat。
func NewTaskHeartbeat(client *redis.Client) *TaskHeartbeat {
	return &TaskHeartbeat{client: client}
}

// 编译期断言：实现 scheduler.TaskHeartbeat。
var _ scheduler.TaskHeartbeat = (*TaskHeartbeat)(nil)

// Add 新 task 入 zset（ZADD）。expire 由调用方 cap 到硬截止 timeout。
func (h *TaskHeartbeat) Add(ctx context.Context, unitID string, expire time.Time) error {
	if err := h.client.ZAdd(ctx, taskHeartbeatKey, redis.Z{
		Score:  float64(expire.UnixMilli()),
		Member: unitID,
	}).Err(); err != nil {
		return fmt.Errorf("zadd task heartbeat %s: %w", unitID, err)
	}
	return nil
}

// RenewMany 批量续期（pipeline ZADD 覆盖 score）。空切片 no-op。
func (h *TaskHeartbeat) RenewMany(ctx context.Context, items []scheduler.TaskHeartbeatItem) error {
	if len(items) == 0 {
		return nil
	}
	pipe := h.client.Pipeline()
	for _, it := range items {
		pipe.ZAdd(ctx, taskHeartbeatKey, redis.Z{
			Score:  float64(it.Expire.UnixMilli()),
			Member: it.UnitID,
		})
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("pipeline zadd task heartbeat: %w", err)
	}
	return nil
}

// Remove 删 task（ZREM，Report 完成/失败时）。
func (h *TaskHeartbeat) Remove(ctx context.Context, unitID string) error {
	if err := h.client.ZRem(ctx, taskHeartbeatKey, unitID).Err(); err != nil {
		return fmt.Errorf("zrem task heartbeat %s: %w", unitID, err)
	}
	return nil
}

// RemoveMany 批量删（ZREM 多 member，Heartbeat recovered 时）。空切片 no-op。
func (h *TaskHeartbeat) RemoveMany(ctx context.Context, unitIDs []string) error {
	if len(unitIDs) == 0 {
		return nil
	}
	args := make([]any, 0, len(unitIDs))
	for _, id := range unitIDs {
		args = append(args, id)
	}
	if err := h.client.ZRem(ctx, taskHeartbeatKey, args...).Err(); err != nil {
		return fmt.Errorf("zrem task heartbeat many: %w", err)
	}
	return nil
}

// Expired 返回已过期 task unitID 列表（score <= now，扫描器回收用）。
func (h *TaskHeartbeat) Expired(ctx context.Context) ([]string, error) {
	now := time.Now().UnixMilli()
	members, err := h.client.ZRangeArgs(ctx, redis.ZRangeArgs{
		Key:     taskHeartbeatKey,
		Start:   "-inf",
		Stop:    fmt.Sprintf("%d", now),
		ByScore: true,
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("zrangebyscore expired tasks: %w", err)
	}
	return members, nil
}

// CleanExpired 幂等清过期 member（ZREMRANGEBYSCORE -inf now，任节点可做）。
func (h *TaskHeartbeat) CleanExpired(ctx context.Context) error {
	now := time.Now().UnixMilli()
	if err := h.client.ZRemRangeByScore(ctx, taskHeartbeatKey, "-inf", fmt.Sprintf("%d", now)).Err(); err != nil {
		return fmt.Errorf("zremrangebyscore expired tasks: %w", err)
	}
	return nil
}
