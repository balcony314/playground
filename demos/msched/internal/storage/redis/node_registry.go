// node_registry.go 软分片成员发现：Redis msched:nodes ZSET + 周期刷新重建环（DESIGN §5/§6）。
//
// 对等 Scheduler 节点各持本地 Ring，靠 Redis 共享成员表感知彼此：
//   - 启动 ZADD 注册自己（score=lease_expire_ts）
//   - 周期（1s）续 lease + ZRANGEBYSCORE 拉全量活跃 + 增量更新本地 Ring（Add 新/Remove 失联）
//   - ZREMRANGEBYSCORE 幂等清过期（任节点可做）
//   - 优雅停止 ZREM 立即摘除；崩溃靠 lease TTL（5s）自然过期
//
// Ring 是引用共享：NodeRegistry 后台 mutate 同一 *Ring 实例，Matcher 持同引用自动看到
// 新成员，无需重新装配。成员变更感知延迟 ≈ 1 个刷新周期，靠 CAS 兜底（§8.2 防双发）。
//
// 生命周期范式同 internal/storage/pg/worker_registry.go（startMu/started/stopOnce 守卫）。

package redis

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/balcony314/msched/internal/scheduler"
	"github.com/redis/go-redis/v9"
)

// nodesKey 软分片成员注册表 key（DESIGN §5/§6）。单 ZSET 全集群共享。
const nodesKey = "msched:nodes"

// NodeRegistry 实现 Scheduler 节点软分片成员发现（DESIGN §5）。
//
// 周期从 Redis msched:nodes ZSET 拉全量活跃成员，增量更新本地 Ring。崩溃无损（lease
// 过期自动摘除）、扩缩容即开即用。本节点 nodeID 配置注入，重启稳定使环不抖动。
type NodeRegistry struct {
	client   *redis.Client
	nodeID   string
	ring     *scheduler.Ring // matcher 持同引用，后台 Add/Remove mutate
	refresh  time.Duration
	leaseTTL time.Duration

	stopCh chan struct{}
	doneCh chan struct{}

	startMu  sync.Mutex // 保护 started：Start 幂等 + 失败可重试
	started  bool       // loop 是否已启动（成功 Start 后置 true）
	stopOnce sync.Once  // stopCh 仅关闭一次
}

// NewNodeRegistry 构造 NodeRegistry。ring 为外部创建、matcher 共享的一致性哈希环；
// nodeID 为本节点 ID（配置注入）；refresh/leaseTTL <=0 取默认（1s / 5s）。
func NewNodeRegistry(client *redis.Client, nodeID string, ring *scheduler.Ring, refresh, leaseTTL time.Duration) *NodeRegistry {
	if refresh <= 0 {
		refresh = time.Second
	}
	if leaseTTL <= 0 {
		leaseTTL = 5 * time.Second
	}
	return &NodeRegistry{
		client:   client,
		nodeID:   nodeID,
		ring:     ring,
		refresh:  refresh,
		leaseTTL: leaseTTL,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
}

// Start 启动后台刷新 goroutine。先同步注册自己 + 拉全量活跃成员建环（避免首次撮合空环）。
// 幂等：已启动则 no-op（startMu + started 守卫）。失败可重试：refreshOnce 出错不置 started。
// ctx 取消或 Stop 关闭后 goroutine 退出。
func (n *NodeRegistry) Start(ctx context.Context) error {
	n.startMu.Lock()
	defer n.startMu.Unlock()
	if n.started {
		return nil
	}
	if err := n.refreshOnce(ctx); err != nil {
		return fmt.Errorf("refresh once: %w", err)
	}
	n.started = true
	go n.loop(ctx)
	return nil
}

// loop 周期刷新循环。ctx 取消或 Stop 时退出（先等 ctx，Stop 用作兜底）。
func (n *NodeRegistry) loop(ctx context.Context) {
	defer close(n.doneCh)
	t := time.NewTicker(n.refresh)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-n.stopCh:
			return
		case <-t.C:
			_ = n.refreshOnce(ctx) // 弱一致：失败不致命，下次重试
		}
	}
}

// Stop 停止刷新 goroutine 并从成员表摘除自己（ZREM）。幂等：重复调用安全。
// 未启动时仅尝试摘除（可能未注册，ZREM 无害），不阻塞（无 loop 生产者时 doneCh 永不关闭）。
// 已启动时阻塞至 goroutine 退出。
func (n *NodeRegistry) Stop(ctx context.Context) error {
	// 摘除自己：优雅下线立即从环移除（崩溃靠 lease TTL 自然过期，走不到这里）。
	if err := n.client.ZRem(ctx, nodesKey, n.nodeID).Err(); err != nil {
		return fmt.Errorf("zrem self on stop: %w", err)
	}
	n.stopOnce.Do(func() { close(n.stopCh) })
	n.startMu.Lock()
	started := n.started
	n.startMu.Unlock()
	if started {
		<-n.doneCh
	}
	return nil
}

// Ring 返回共享的一致性哈希环。matcher 持此引用，后台刷新自动可见。
func (n *NodeRegistry) Ring() *scheduler.Ring { return n.ring }

// refreshOnce 同步：续自己的 lease + 清过期成员 + 拉全量活跃 + 增量更新环。
//
// 顺序：先 ZADD 续自己（保活），再 ZREMRANGEBYSCORE 清过期（含他人崩溃残留），
// 最后 ZRANGEBYSCORE 拉活跃。清过期在拉取前，保证拉到的都是未过期成员。
// 环增量更新：Add 全部活跃（幂等，已存在跳过），Remove 消失成员（diff 出来逐个删）。
func (n *NodeRegistry) refreshOnce(ctx context.Context) error {
	now := time.Now().UnixMilli()
	expire := now + n.leaseTTL.Milliseconds()

	// 1. 续自己的 lease（ZADD 覆盖 score）。
	if err := n.client.ZAdd(ctx, nodesKey, redis.Z{Score: float64(expire), Member: n.nodeID}).Err(); err != nil {
		return fmt.Errorf("zadd self lease: %w", err)
	}

	// 2. 清过期成员（幂等，任节点可做；含他人崩溃残留）。
	if err := n.client.ZRemRangeByScore(ctx, nodesKey, "-inf", fmt.Sprintf("%d", now)).Err(); err != nil {
		return fmt.Errorf("zremrangebyscore expired: %w", err)
	}

	// 3. 拉全量活跃成员（score > now，即未过期）。
	members, err := n.client.ZRangeArgs(ctx, redis.ZRangeArgs{
		Key:     nodesKey,
		Start:   fmt.Sprintf("%d", now),
		Stop:    "+inf",
		ByScore: true,
	}).Result()
	if err != nil {
		return fmt.Errorf("zrangebyscore active: %w", err)
	}

	// 4. 增量更新环：Add 全部活跃，Remove 消失成员。
	active := make(map[string]struct{}, len(members))
	for _, m := range members {
		active[m] = struct{}{}
	}
	n.ring.Add(members...) // 幂等：已存在跳过
	for _, old := range n.ring.Members() {
		if _, ok := active[old]; !ok {
			n.ring.Remove(old)
		}
	}
	return nil
}
