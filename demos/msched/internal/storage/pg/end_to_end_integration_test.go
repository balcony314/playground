//go:build integration

package pg

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/balcony314/msched/internal/model"
	"github.com/balcony314/msched/internal/scheduler"
	"github.com/balcony314/msched/internal/storage"
	redisstore "github.com/balcony314/msched/internal/storage/redis"
	"github.com/redis/go-redis/v9"
)

// TestEndToEndMatchOnce 端到端撮合一轮：真实 PG TaskStore + WorkerRegistry + miniredis
// DispatchQueue，装配 scheduler.Matcher 跑 MatchOnce，验证 CAS + 派发队列 + group_stats。
func TestEndToEndMatchOnce(t *testing.T) {
	ts, db := newTaskStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// miniredis 派发队列。
	mr := miniredis.RunT(t)
	rclient := redisstore.NewRedis(storage.RedisConfig{Addr: mr.Addr()})
	dq := redisstore.NewDispatchQueue(rclient)

	// WorkerRegistry：插入一个在线 worker，启动刷新。
	future := nowUTC().Add(time.Hour)
	insertWorker(t, db, "w1", map[string]string{"zone": "z1"}, future)
	rg := NewWorkerRegistry(db, 50*time.Millisecond, nil)
	if err := rg.Start(ctx); err != nil {
		t.Fatalf("start registry: %v", err)
	}
	defer rg.Stop()

	// 插入 2 个 PENDING 任务（g1，selector={zone:z1}）。
	sel := model.WorkerSelector{"zone": "z1"}
	insertTask(t, db, sampleTask("u1", "g1", 100, sel))
	insertTask(t, db, sampleTask("u2", "g1", 200, sel))

	// 装配 matcher（无软分片）。
	m := scheduler.New(ts, rg, dq, 10)

	dispatched, err := m.MatchOnce(ctx)
	if err != nil {
		t.Fatalf("match once: %v", err)
	}
	if dispatched != 2 {
		t.Errorf("dispatched: got %d, want 2", dispatched)
	}

	// 验证：两个 task 应在 w1 的派发队列 g1 桶里。
	got, err := dq.PullRoundRobin(ctx, "w1", 10)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("dispatch queue: got %d, want 2", len(got))
	}
	// 两个都应属 g1，w1。
	for _, st := range got {
		if st.Group != "g1" || st.Wuid != "w1" {
			t.Errorf("ScheduledTask: %+v", st)
		}
	}

	// group_stats：g1 应有 2 个 SCHEDULED（u1, u2 各 CAS 一次）。
	stats, err := ts.ListGroupStats(ctx, "g1")
	if err != nil {
		t.Fatalf("list group stats: %v", err)
	}
	var scheduledCount int64
	for _, s := range stats {
		if s.State == model.StateScheduled {
			scheduledCount = s.Count
		}
	}
	if scheduledCount != 2 {
		t.Errorf("group_stats SCHEDULED count: got %d, want 2", scheduledCount)
	}
}

// TestEndToEndBackoff 端到端退避：无匹配 worker 的 task 转 BACKOFF。
func TestEndToEndBackoff(t *testing.T) {
	ts, db := newTaskStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mr := miniredis.RunT(t)
	dq := redisstore.NewDispatchQueue(redisstore.NewRedis(storage.RedisConfig{Addr: mr.Addr()}))

	// 无在线 worker。
	rg := NewWorkerRegistry(db, 50*time.Millisecond, nil)
	_ = rg.Start(ctx)
	defer rg.Stop()

	sel := model.WorkerSelector{"zone": "nonexistent"}
	insertTask(t, db, sampleTask("u1", "g1", 100, sel))

	m := scheduler.New(ts, rg, dq, 10)
	if _, err := m.MatchOnce(ctx); err != nil {
		t.Fatalf("match once: %v", err)
	}

	// u1 应转 BACKOFF，fail_count=1，next_retry_time 非 nil。
	var row model.Task
	if err := db.Where("unit_id=?", "u1").First(&row).Error; err != nil {
		t.Fatalf("query: %v", err)
	}
	if row.State != model.StateBackoff {
		t.Errorf("state: got %s, want BACKOFF", row.State)
	}
	if row.FailCount != 1 {
		t.Errorf("fail_count: got %d, want 1", row.FailCount)
	}
	if row.NextRetryTime == nil {
		t.Error("next_retry_time should be set")
	}

	// group_stats：g1 应有 1 个 BACKOFF。
	if got := getGroupStat(t, db, "g1", sel, model.StateBackoff); got != 1 {
		t.Errorf("BACKOFF count: got %d, want 1", got)
	}

	// 派发队列应为空（无 CAS 成功）。
	pulled, err := dq.PullRoundRobin(ctx, "w1", 10)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if len(pulled) != 0 {
		t.Errorf("dispatch queue should be empty: got %v", pulled)
	}
}

// TestEndToEndDispatchThenPull 端到端：撮合 -> Pull -> BatchSetRunning。
func TestEndToEndDispatchThenPull(t *testing.T) {
	ts, db := newTaskStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mr := miniredis.RunT(t)
	dq := redisstore.NewDispatchQueue(redisstore.NewRedis(storage.RedisConfig{Addr: mr.Addr()}))

	future := nowUTC().Add(time.Hour)
	insertWorker(t, db, "w1", map[string]string{"zone": "z1"}, future)
	rg := NewWorkerRegistry(db, 50*time.Millisecond, nil)
	_ = rg.Start(ctx)
	defer rg.Stop()

	sel := model.WorkerSelector{"zone": "z1"}
	insertTask(t, db, sampleTask("u1", "g1", 100, sel))
	insertTask(t, db, sampleTask("u2", "g1", 200, sel))

	// 撮合。
	m := scheduler.New(ts, rg, dq, 10)
	if _, err := m.MatchOnce(ctx); err != nil {
		t.Fatalf("match once: %v", err)
	}

	// 派发器 Pull：SCHEDULED -> RUNNING。
	d := scheduler.NewDispatcher(ts, dq)
	tasks, err := d.Pull(ctx, "w1", 10)
	if err != nil {
		t.Fatalf("dispatch pull: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("pulled tasks: got %d, want 2", len(tasks))
	}
	for _, tk := range tasks {
		if tk.State != model.StateRunning {
			t.Errorf("task %s state: got %s, want RUNNING", tk.UnitID, tk.State)
		}
	}

	// group_stats：2 个 RUNNING。
	if got := getGroupStat(t, db, "g1", sel, model.StateRunning); got != 2 {
		t.Errorf("RUNNING count: got %d, want 2", got)
	}
}

// 避免 unused import 警告（go-redis 仅用于类型引用断言）。
var _ = redis.NewClient
var _ scheduler.ScheduledTask
