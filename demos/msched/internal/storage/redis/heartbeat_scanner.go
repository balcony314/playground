// heartbeat_scanner.go 两个定时扫描器：worker 心跳过期 + task 心跳过期回收（DESIGN §8.4）。
//
// 所有对等 Scheduler 节点都跑，CAS 兜底防多节点重复回收（影响行校验）。范式同
// NodeRegistry（loop + startMu/started/stopOnce）。弱一致：单次扫描失败不致命，下次重试。

package redis

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/balcony314/msched/internal/scheduler"
)

// WorkerHeartbeatScanner 扫描 msched:hb:worker 过期 worker，回收其名下 RUNNING + SCHEDULED
// 任务（DB CAS -> PENDING）+ 清 Redis 派发桶（worker 死了不再 Pull）+ 清 task zset
// recovered member（RUNNING 的在 task zset）。worker zset member 回收后 Remove。
type WorkerHeartbeatScanner struct {
	hb       *WorkerHeartbeat
	taskHB   *TaskHeartbeat
	store    scheduler.TaskStore
	queue    *DispatchQueue // 清过期 worker 派发桶（SCHEDULED 残留）
	interval time.Duration

	stopCh chan struct{}
	doneCh chan struct{}

	startMu  sync.Mutex
	started  bool
	stopOnce sync.Once
}

// NewWorkerHeartbeatScanner 构造 WorkerHeartbeatScanner。interval <=0 取默认 1s。
func NewWorkerHeartbeatScanner(hb *WorkerHeartbeat, taskHB *TaskHeartbeat, store scheduler.TaskStore, queue *DispatchQueue, interval time.Duration) *WorkerHeartbeatScanner {
	if interval <= 0 {
		interval = time.Second
	}
	return &WorkerHeartbeatScanner{
		hb:       hb,
		taskHB:   taskHB,
		store:    store,
		queue:    queue,
		interval: interval,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
}

// Start 启动扫描 goroutine。幂等：已启动则 no-op。ctx 取消或 Stop 时退出。
func (s *WorkerHeartbeatScanner) Start(ctx context.Context) error {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if s.started {
		return nil
	}
	s.started = true
	go s.loop(ctx)
	return nil
}

func (s *WorkerHeartbeatScanner) loop(ctx context.Context) {
	defer close(s.doneCh)
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case <-t.C:
			if err := s.scanOnce(ctx); err != nil {
				// 弱一致：单次失败不致命，下次重试。
				_ = err
			}
		}
	}
}

// Stop 停止扫描 goroutine。幂等：重复调用安全。
func (s *WorkerHeartbeatScanner) Stop() {
	s.stopOnce.Do(func() { close(s.stopCh) })
	s.startMu.Lock()
	started := s.started
	s.startMu.Unlock()
	if started {
		<-s.doneCh
	}
}

// scanOnce 拉过期 worker -> RecoverByWorker（DB CAS RUNNING+SCHEDULED->PENDING）
// -> 清派发桶 -> 清 task zset recovered -> Remove worker member。
func (s *WorkerHeartbeatScanner) scanOnce(ctx context.Context) error {
	expired, err := s.hb.Expired(ctx)
	if err != nil {
		return fmt.Errorf("expired workers: %w", err)
	}
	for _, wuid := range expired {
		recovered, err := s.store.RecoverByWorker(ctx, wuid)
		if err != nil {
			return fmt.Errorf("recover by worker %s: %w", wuid, err)
		}
		// 清过期 worker 派发桶（死了不再 Pull，SCHEDULED 残留随 DB CAS 回 PENDING，桶整桶清）。
		if err := s.queue.ClearWorker(ctx, wuid); err != nil {
			return fmt.Errorf("clear dispatch buckets %s: %w", wuid, err)
		}
		// recovered 含 RUNNING（在 task zset）+ SCHEDULED（不在 task zset，ZREM 无害）。
		if err := s.taskHB.RemoveMany(ctx, recovered); err != nil {
			return fmt.Errorf("task hb remove many %s: %w", wuid, err)
		}
		// 摘除 worker zset member（已回收，清理；幂等）。
		if err := s.hb.Remove(ctx, wuid); err != nil {
			return fmt.Errorf("remove worker %s: %w", wuid, err)
		}
	}
	return nil
}

// TaskHeartbeatScanner 扫描 msched:hb:task 过期 task，CAS RUNNING -> PENDING 回收。
// 无论 CAS 是否命中（已回收/已终态），member 都 Remove 清理避免重复扫。
type TaskHeartbeatScanner struct {
	hb       *TaskHeartbeat
	store    scheduler.TaskStore
	interval time.Duration

	stopCh chan struct{}
	doneCh chan struct{}

	startMu  sync.Mutex
	started  bool
	stopOnce sync.Once
}

// NewTaskHeartbeatScanner 构造 TaskHeartbeatScanner。interval <=0 取默认 1s。
func NewTaskHeartbeatScanner(hb *TaskHeartbeat, store scheduler.TaskStore, interval time.Duration) *TaskHeartbeatScanner {
	if interval <= 0 {
		interval = time.Second
	}
	return &TaskHeartbeatScanner{
		hb:       hb,
		store:    store,
		interval: interval,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
}

// Start 启动扫描 goroutine。幂等。
func (s *TaskHeartbeatScanner) Start(ctx context.Context) error {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if s.started {
		return nil
	}
	s.started = true
	go s.loop(ctx)
	return nil
}

func (s *TaskHeartbeatScanner) loop(ctx context.Context) {
	defer close(s.doneCh)
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case <-t.C:
			if err := s.scanOnce(ctx); err != nil {
				_ = err
			}
		}
	}
}

// Stop 停止扫描 goroutine。幂等。
func (s *TaskHeartbeatScanner) Stop() {
	s.stopOnce.Do(func() { close(s.stopCh) })
	s.startMu.Lock()
	started := s.started
	s.startMu.Unlock()
	if started {
		<-s.doneCh
	}
}

// scanOnce 拉过期 task -> RecoverTask（CAS RUNNING->PENDING）-> Remove（无论命中与否）。
func (s *TaskHeartbeatScanner) scanOnce(ctx context.Context) error {
	expired, err := s.hb.Expired(ctx)
	if err != nil {
		return fmt.Errorf("expired tasks: %w", err)
	}
	for _, unitID := range expired {
		if _, err := s.store.RecoverTask(ctx, unitID); err != nil {
			return fmt.Errorf("recover task %s: %w", unitID, err)
		}
		// member 该清（CAS 命中已转 PENDING；未命中=已回收/终态，残留也清），避免重复扫。
		if err := s.hb.Remove(ctx, unitID); err != nil {
			return fmt.Errorf("remove task %s: %w", unitID, err)
		}
	}
	return nil
}
