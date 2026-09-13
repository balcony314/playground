package redis

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/balcony314/msched/internal/scheduler"
	"github.com/redis/go-redis/v9"
)

// dispatchPrefix 派发队列 key 前缀（REDIS.md §2）。
const dispatchPrefix = "dispatch:"

// DispatchQueue 实现 scheduler.DispatchQueue（Redis ZSET 派发队列，DESIGN §6/§9.2）。
//
// key 形如 dispatch:{wuid}:{group}，{wuid} hash tag 集中同 worker 各 group 桶到同 slot
// 便于 SCAN（REDIS.md §2）。score = dispatch_time，member = unitID。非阻塞。
type DispatchQueue struct {
	client *redis.Client
}

// NewDispatchQueue 构造 DispatchQueue。
func NewDispatchQueue(client *redis.Client) *DispatchQueue {
	return &DispatchQueue{client: client}
}

// 编译期断言：实现 scheduler.DispatchQueue。
var _ scheduler.DispatchQueue = (*DispatchQueue)(nil)

// Push 入派发队列（ZADD），score=dispatch_time（DESIGN §8.2）。
func (q *DispatchQueue) Push(ctx context.Context, wuid, group, unitID string, score int64) error {
	key := dispatchKey(wuid, group)
	return q.client.ZAdd(ctx, key, redis.Z{Score: float64(score), Member: unitID}).Err()
}

// pullScript 把逐桶 N 次 ZPOPMIN 收敛为单次 Lua EVAL（DESIGN §9.2，REDIS.md §1.1）。
//
// KEYS=桶名列表（调用前已排序，保证 group 轮询稳定序），ARGV[1]=batch，ARGV[2]=wuid。
// 复刻原循环语义：每轮遍历各桶各 ZPOPMIN 1 个，凑满 batch 或一轮无产出止（非阻塞）。
// 返回扁平 [member,key,member,key,...]，Go 侧按对解析出 (unitID, group)。
// ZPOPMIN 原子取 score 最小者；Lua 脚本整体原子，相比 N 次独立 ZPOPMIN 间可被插入，更一致。
const pullScript = `
local keys = KEYS
local batch = tonumber(ARGV[1])
local result = {}
while #result / 2 < batch do
	local progressed = false
	for _, key in ipairs(keys) do
		if #result / 2 >= batch then break end
		local popped = redis.call('ZPOPMIN', key, 1)
		if #popped > 0 then
			table.insert(result, popped[1])  -- member
			table.insert(result, key)         -- 桶名，Go 侧 parseGroup
			progressed = true
		end
	end
	if not progressed then break end
end
return result
`

// PullRoundRobin 派发端 group 轮询：SCAN 该 worker 各 group 桶，Lua 内逐桶 ZPOPMIN 凑 batch，
// 凑满或全空为止（DESIGN §9.2）。非阻塞，空批返回空切片。
//
// SCAN 保留（Lua 内不可用 SCAN，KEYS O(N) 阻塞禁用），桶名作为 KEYS 传给脚本，N 次 ZPOPMIN
// 合并为一次 EVAL，降低高 QPS 拉取下的 RTT（REDIS.md §1.1）。per-worker 拉取无并发，原子性次要。
func (q *DispatchQueue) PullRoundRobin(ctx context.Context, wuid string, batch int) ([]scheduler.ScheduledTask, error) {
	if batch <= 0 {
		return nil, nil
	}
	keys, err := q.scanBuckets(ctx, wuid)
	if err != nil {
		return nil, fmt.Errorf("scan dispatch buckets: %w", err)
	}
	if len(keys) == 0 {
		return nil, nil // 无桶，省一次 EVAL
	}
	sort.Strings(keys) // 稳定 group 轮询序

	res, err := q.client.Eval(ctx, pullScript, keys, batch, wuid).Result()
	if err != nil {
		return nil, fmt.Errorf("eval pull: %w", err)
	}
	items, ok := res.([]interface{})
	if !ok {
		return nil, fmt.Errorf("eval pull: unexpected return type %T", res)
	}
	tasks := make([]scheduler.ScheduledTask, 0, len(items)/2)
	for i := 0; i+1 < len(items); i += 2 {
		unitID, _ := items[i].(string)
		key, _ := items[i+1].(string)
		tasks = append(tasks, scheduler.ScheduledTask{
			UnitID: unitID,
			Group:  parseGroup(key, wuid),
			Wuid:   wuid,
		})
	}
	return tasks, nil
}

// scanBuckets SCAN 出该 worker 的所有 dispatch:{wuid}:* group 桶 key。
func (q *DispatchQueue) scanBuckets(ctx context.Context, wuid string) ([]string, error) {
	pattern := dispatchPrefix + "{" + wuid + "}:*"
	var keys []string
	var cursor uint64
	for {
		ks, c, err := q.client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return nil, err
		}
		keys = append(keys, ks...)
		cursor = c
		if cursor == 0 {
			break
		}
	}
	return keys, nil
}

// ClearWorker 清理某 worker 全部派发桶（SCAN dispatch:{wuid}:* + DEL）。
// worker 心跳过期（离线）时由扫描器调用：worker 死了不再 Pull，其 SCHEDULED 残留
// 随 DB CAS 回 PENDING，派发桶整桶清空杜绝幽灵 member 被（理论上的）后续 ZPOPMIN 取出（§8.4）。
func (q *DispatchQueue) ClearWorker(ctx context.Context, wuid string) error {
	keys, err := q.scanBuckets(ctx, wuid)
	if err != nil {
		return fmt.Errorf("scan dispatch buckets: %w", err)
	}
	if len(keys) == 0 {
		return nil
	}
	if err := q.client.Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("del dispatch buckets: %w", err)
	}
	return nil
}

// dispatchKey 构造派发队列 key：dispatch:{wuid}:{group}（{wuid} 为 hash tag，REDIS.md §2）。
func dispatchKey(wuid, group string) string {
	return dispatchPrefix + "{" + wuid + "}:" + group
}

// parseGroup 从 key 反解析 group。key 形如 dispatch:{wuid}:group，去掉前缀与 {wuid}: 段。
func parseGroup(key, wuid string) string {
	prefix := dispatchPrefix + "{" + wuid + "}:"
	return strings.TrimPrefix(key, prefix)
}
