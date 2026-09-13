package pg

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/balcony314/msched/internal/model"
	"github.com/balcony314/msched/internal/scheduler"
	"gorm.io/gorm"
)

// WorkerRegistry 实现 scheduler.WorkerRegistry（进程内缓存 + 周期刷新，DESIGN §4.1）。
//
// 后台 goroutine 周期（默认 1s，可配）从 PostgreSQL workers 表全量拉取 worker
// UnitID + Labels（+ lease_expire_time 兼容降级），刷新缓存。在线判定以注入的
// WorkerHeartbeat zset 为准（替换 DB lease，DESIGN §8.4 补充）：refreshOnce 同步
// Active 拉在线 unitID 集合，Match 用 zset 集合过滤。online 为 nil 时退化用 DB
// lease_expire_time（兼容单测/降级）。Match 遍历进程内缓存调 SelectorSubset 匹配
// （开放 k/v selector 亦成立，§4）。
type WorkerRegistry struct {
	db      *gorm.DB
	online  scheduler.WorkerHeartbeat // 注入，nil 退化用 DB lease
	refresh time.Duration

	mu        sync.RWMutex
	view      []workerView        // 在线 worker 快照（labels）
	onlineSet map[string]struct{} // 在线 unitID 集合（zset Active 拉取）
	stopCh    chan struct{}
	doneCh    chan struct{}

	startMu  sync.Mutex // 保护 started：Start 幂等 + 失败可重试
	started  bool       // loop 是否已启动（成功 Start 后置 true）
	stopOnce sync.Once  // stopCh 仅关闭一次
}

// workerView 缓存的在线 worker 投影（撮合匹配所需字段，DESIGN §4.1）。
type workerView struct {
	UnitID string
	Labels map[string]string
	Expire time.Time // 退化模式（online nil）用 DB lease 判在线
}

// NewWorkerRegistry 构造 WorkerRegistry。online 为在线判定源（WorkerHeartbeat zset，
// 替换 DB lease）；nil 退化用 DB lease_expire_time（兼容单测/降级）。调用 Start 后开始周期刷新。
func NewWorkerRegistry(db *gorm.DB, refresh time.Duration, online scheduler.WorkerHeartbeat) *WorkerRegistry {
	if refresh <= 0 {
		refresh = time.Second
	}
	return &WorkerRegistry{
		db:      db,
		online:  online,
		refresh: refresh,
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
	}
}

// 编译期断言：实现 scheduler.WorkerRegistry。
var _ scheduler.WorkerRegistry = (*WorkerRegistry)(nil)

// Start 启动后台刷新 goroutine。先同步刷新一次（避免首次 Match 见空缓存）。
// 幂等：已启动则 no-op（startMu + started 守卫，不会重复 spawn loop）。
// 失败可重试：refreshOnce 出错时不置 started，后续 Start 可再次尝试。
// ctx 取消或 Stop 关闭后 goroutine 退出。
func (r *WorkerRegistry) Start(ctx context.Context) error {
	r.startMu.Lock()
	defer r.startMu.Unlock()
	if r.started {
		return nil // 已启动,no-op
	}
	if err := r.refreshOnce(ctx); err != nil {
		return fmt.Errorf("refresh once: %w", err) // 失败不置 started,允许重试
	}
	r.started = true
	go r.loop(ctx)
	return nil
}

// loop 周期刷新循环。ctx 取消或 Stop 时退出（先等 ctx，Stop 用作兜底）。
func (r *WorkerRegistry) loop(ctx context.Context) {
	defer close(r.doneCh)
	t := time.NewTicker(r.refresh)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.stopCh:
			return
		case <-t.C:
			_ = r.refreshOnce(ctx) // 弱一致：失败不致命，下次重试
		}
	}
}

// Stop 停止刷新 goroutine。幂等：重复调用安全（stopOnce 保证 stopCh 仅关一次）。
// 未启动时立即返回不阻塞（无 loop 生产者时 doneCh 永不关闭）。
// 已启动时阻塞至 goroutine 退出。
func (r *WorkerRegistry) Stop() {
	r.stopOnce.Do(func() { close(r.stopCh) })
	// 仅当 loop 已启动才等其退出；未启动时 doneCh 无生产者,等待会永久阻塞。
	r.startMu.Lock()
	started := r.started
	r.startMu.Unlock()
	if started {
		<-r.doneCh
	}
}

// refreshOnce 同步从 PostgreSQL 拉取全部 worker（labels + lease），并从 zset 拉在线
// 集合（online 非 nil 时）。持锁窗口短（仅赋值切片 + map）。
func (r *WorkerRegistry) refreshOnce(ctx context.Context) error {
	var workers []model.Worker
	err := r.db.WithContext(ctx).
		Select("unit_id", "labels", "lease_expire_time").
		Find(&workers).Error
	if err != nil {
		return fmt.Errorf("query workers: %w", err)
	}
	view := make([]workerView, 0, len(workers))
	for i := range workers {
		w := workers[i]
		view = append(view, workerView{
			UnitID: w.UnitID,
			Labels: w.Labels,
			Expire: w.LeaseExpireTime,
		})
	}

	var online map[string]struct{}
	if r.online != nil {
		online, err = r.online.Active(ctx)
		if err != nil {
			return fmt.Errorf("active workers: %w", err)
		}
	}

	r.mu.Lock()
	r.view = view
	if online != nil {
		r.onlineSet = online
	}
	r.mu.Unlock()
	return nil
}

// Match 返回满足 selector 单向强约束 S_t ⊆ L_w 的在线 worker ID 列表（DESIGN §4）。
// 在线判定：online 非 nil 用 zset 在线集合；nil 退化用 DB lease_expire_time。
// 结果排序稳定（与 fake 一致）。
func (r *WorkerRegistry) Match(ctx context.Context, selector model.WorkerSelector) ([]string, error) {
	r.mu.RLock()
	view := r.view
	online := r.onlineSet
	useOnline := r.online != nil
	r.mu.RUnlock()

	now := time.Now()
	wuids := make([]string, 0, len(view))
	for _, w := range view {
		if useOnline {
			if _, ok := online[w.UnitID]; !ok {
				continue // zset 判定离线
			}
		} else if !w.Expire.IsZero() && !w.Expire.After(now) {
			continue // DB lease 过期，离线（退化模式）
		}
		if model.SelectorSubset(selector, w.Labels) {
			wuids = append(wuids, w.UnitID)
		}
	}
	sort.Strings(wuids)
	return wuids, nil
}
