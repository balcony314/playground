// worker_heartbeat.go worker 在线心跳 zset（DESIGN §8.4 补充）。
//
// worker 在线判定以 zset 为准（替换 DB lease）：Register/Heartbeat 续期 ZADD、过期
// ZREMRANGEBYSCORE。范式同 msched:nodes（node_registry.go）。key=msched:hb:worker，
// member=worker unitID，score=lease_expire_ts(ms)。
//
// WorkerRegistry 刷新时 Active 拉在线集合；扫描器 Expired 拉过期 worker 回收其名下任务。

package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/balcony314/msched/internal/scheduler"
	"github.com/redis/go-redis/v9"
)

// workerHeartbeatKey worker 在线心跳 zset key（单 ZSET 全集群共享）。
const workerHeartbeatKey = "msched:hb:worker"

// WorkerHeartbeat 实现 scheduler.WorkerHeartbeat（worker 在线心跳 zset，DESIGN §8.4 补充）。
type WorkerHeartbeat struct {
	client *redis.Client
}

// NewWorkerHeartbeat 构造 WorkerHeartbeat。
func NewWorkerHeartbeat(client *redis.Client) *WorkerHeartbeat {
	return &WorkerHeartbeat{client: client}
}

// 编译期断言：实现 scheduler.WorkerHeartbeat。
var _ scheduler.WorkerHeartbeat = (*WorkerHeartbeat)(nil)

// Renew 续期 worker lease（ZADD score=expire_ts，覆盖旧 score）。
func (h *WorkerHeartbeat) Renew(ctx context.Context, unitID string, expire time.Time) error {
	if err := h.client.ZAdd(ctx, workerHeartbeatKey, redis.Z{
		Score:  float64(expire.UnixMilli()),
		Member: unitID,
	}).Err(); err != nil {
		return fmt.Errorf("zadd worker heartbeat %s: %w", unitID, err)
	}
	return nil
}

// Remove 摘除 worker（ZREM，优雅下线/回收后清理）。
func (h *WorkerHeartbeat) Remove(ctx context.Context, unitID string) error {
	if err := h.client.ZRem(ctx, workerHeartbeatKey, unitID).Err(); err != nil {
		return fmt.Errorf("zrem worker heartbeat %s: %w", unitID, err)
	}
	return nil
}

// Active 返回当前在线 worker unitID 集合（score > now，即未过期）。WorkerRegistry.Match 用。
func (h *WorkerHeartbeat) Active(ctx context.Context) (map[string]struct{}, error) {
	now := time.Now().UnixMilli()
	members, err := h.client.ZRangeArgs(ctx, redis.ZRangeArgs{
		Key:     workerHeartbeatKey,
		Start:   fmt.Sprintf("%d", now),
		Stop:    "+inf",
		ByScore: true,
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("zrangebyscore active workers: %w", err)
	}
	set := make(map[string]struct{}, len(members))
	for _, m := range members {
		set[m] = struct{}{}
	}
	return set, nil
}

// Expired 返回已过期 worker unitID 列表（score <= now，扫描器回收用）。
func (h *WorkerHeartbeat) Expired(ctx context.Context) ([]string, error) {
	now := time.Now().UnixMilli()
	members, err := h.client.ZRangeArgs(ctx, redis.ZRangeArgs{
		Key:     workerHeartbeatKey,
		Start:   "-inf",
		Stop:    fmt.Sprintf("%d", now),
		ByScore: true,
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("zrangebyscore expired workers: %w", err)
	}
	return members, nil
}

// CleanExpired 幂等清过期 member（ZREMRANGEBYSCORE -inf now，任节点可做）。
func (h *WorkerHeartbeat) CleanExpired(ctx context.Context) error {
	now := time.Now().UnixMilli()
	if err := h.client.ZRemRangeByScore(ctx, workerHeartbeatKey, "-inf", fmt.Sprintf("%d", now)).Err(); err != nil {
		return fmt.Errorf("zremrangebyscore expired workers: %w", err)
	}
	return nil
}
