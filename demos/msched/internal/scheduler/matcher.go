// matcher.go 撮合器：直查 PostgreSQL PENDING -> 匹配 worker -> CAS -> 入派发队列（DESIGN §7/§9.1）。
//
// 多租户公平（撮合端，§9.1）：维护活跃 group 环形游标，每轮 MatchOnce 处理游标
// 当前指向的 group，取尽一批后推进游标。大户 group 不独占撮合产出，小户每轮都
// 得到撮合机会。游标为本地近似（§9.4 软分片后每 group 单节点撮合，无跨节点竞争）。
//
// 软分片（§5）：一致性哈希环按 group_uid 归属节点，本节点只处理 Owns 的 group，
// 消除多实例对同一活跃 group 的 CAS 竞争。CAS 兜底保留：N 变更瞬间 group 可能被
// 多节点认领，靠 §8.2 CAS 天然防双发。
//
// 退避（§8.6）：无匹配在线 worker 的 task 转 BACKOFF + 指数退避（封顶 1h，永不放弃），
// 不空耗撮合器；到期被撮合查询再次取到（放弃主动唤醒，纯靠 next_retry_time 到期）。

package scheduler

import (
	"context"
	"fmt"
	"time"
)

// Matcher 撮合器（无状态多实例，DESIGN §5）。group 游标为本地近似状态。
//
// 软分片：ring 非 nil 时只撮合本节点认领的 group；nil 退化为全量撮合（单节点/测试用）。
type Matcher struct {
	store     TaskStore
	registry  WorkerRegistry
	queue     DispatchQueue
	batchSize int    // 单轮单 group 取候选批大小 N（DESIGN §11，可配）
	nextGroup string // 下次处理的 group ID（空则从列表头开始，DESIGN §9.1）
	ring      *Ring  // 一致性哈希环（软分片，§5）；nil = 不分片
	nodeID    string // 本节点 ID（ring 判定 Owns 用）
	now       func() time.Time
}

// New 构造撮合器。store/registry/queue 为消费者侧接口（CODING §4），可同源（如 fake）。
// 不带软分片：所有 group 都撮合（单节点或测试场景）。
func New(store TaskStore, registry WorkerRegistry, queue DispatchQueue, batchSize int) *Matcher {
	return &Matcher{
		store:     store,
		registry:  registry,
		queue:     queue,
		batchSize: batchSize,
		now:       time.Now,
	}
}

// NewWithShard 构造带软分片的撮合器（DESIGN §5）。ring 为成员已就绪的一致性哈希环，
// nodeID 为本节点在环上的 ID。ring 为 nil 时等价 New（退化为全量撮合）。
func NewWithShard(store TaskStore, registry WorkerRegistry, queue DispatchQueue, batchSize int, ring *Ring, nodeID string) *Matcher {
	m := New(store, registry, queue, batchSize)
	m.ring = ring
	m.nodeID = nodeID
	return m
}

// MatchOnce 执行一轮撮合（DESIGN §7/§9.1）：
//  1. 刷新活跃 group 列表，软分片过滤出本节点认领的 group
//  2. 取游标当前 group，ListPendByGroup 取一批候选（ORDER BY priority, FIFO）
//  3. 逐 task：Match 选 worker -> CASSchedule 抢租约 -> Push 入派发队列
//  4. 无匹配 worker 的 task 转 BACKOFF 退避（DESIGN §8.6，永不放弃）
//  5. 推进游标到下一 group
//
// 返回本轮撮合成功数。无活跃 group 返回 0。
func (m *Matcher) MatchOnce(ctx context.Context) (int, error) {
	groups, err := m.store.ListActiveGroups(ctx)
	if err != nil {
		return 0, fmt.Errorf("list active groups: %w", err)
	}
	owned := m.filterOwned(groups)
	if len(owned) == 0 {
		return 0, nil
	}
	// 定位本轮处理 group：从 nextGroup 起轮询，未命中则从头（DESIGN §9.1 环形）。
	idx := 0
	if m.nextGroup != "" {
		for i, g := range owned {
			if g == m.nextGroup {
				idx = i
				break
			}
		}
	}
	group := owned[idx]
	candidates, err := m.store.ListPendByGroup(ctx, group, m.batchSize)
	if err != nil {
		return 0, fmt.Errorf("list pend by group %s: %w", group, err)
	}

	dispatched := 0
	for _, t := range candidates {
		wuids, err := m.registry.Match(ctx, t.WorkerSelector)
		if err != nil {
			return dispatched, fmt.Errorf("match task %s: %w", t.UnitID, err)
		}
		if len(wuids) == 0 {
			// 无匹配在线 worker：转 BACKOFF 退避（DESIGN §8.6，永不放弃）。
			nextRetry := m.now().Add(NextBackoff(t.FailCount))
			if err := m.store.SetBackoff(ctx, t.UnitID, nextRetry); err != nil {
				return dispatched, fmt.Errorf("set backoff task %s: %w", t.UnitID, err)
			}
			continue
		}
		wuid := m.pickWorker(wuids)
		ok, err := m.store.CASSchedule(ctx, t.UnitID, wuid)
		if err != nil {
			return dispatched, fmt.Errorf("cas dispatch task %s: %w", t.UnitID, err)
		}
		if !ok {
			// CAS 失败：他节点已抢，丢弃候选（DESIGN §8.2）。
			continue
		}
		score := m.now().UnixMilli()
		if err := m.queue.Push(ctx, wuid, t.GroupUID, t.UnitID, score); err != nil {
			return dispatched, fmt.Errorf("push dispatch queue task %s: %w", t.UnitID, err)
		}
		dispatched++
	}

	// 推进游标到下一个 group（基于本轮列表环形）。
	m.nextGroup = owned[(idx+1)%len(owned)]
	return dispatched, nil
}

// filterOwned 软分片过滤：只保留本节点认领的 group（DESIGN §5）。
// ring 为 nil 时不过滤（退化为全量撮合）。
func (m *Matcher) filterOwned(groups []string) []string {
	if m.ring == nil {
		return groups
	}
	owned := make([]string, 0, len(groups))
	for _, g := range groups {
		if m.ring.Owns(m.nodeID, g) {
			owned = append(owned, g)
		}
	}
	return owned
}

// pickWorker 从匹配的 worker 中选一个派发（DESIGN §11 容量模型待定）。
// 本轮取列表首个（Match 返回已排序）；后继可改最闲/加权轮询以均衡多 worker。
func (m *Matcher) pickWorker(wuids []string) string {
	return wuids[0]
}
