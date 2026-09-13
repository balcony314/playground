package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/balcony314/msched/internal/model"
	"github.com/balcony314/msched/internal/scheduler"
	"github.com/balcony314/msched/internal/scheduler/fake"
)

// matchLoopIntegrationTest 是本文件的核心：验证 main.go 装配抽出的 runMatchLoop goroutine
// 驱动 Matcher 持续撮合的端到端装配契约（contracts/matcher-loop.md）。
//
// 用 fake Store 同时作 TaskStore/WorkerRegistry/DispatchQueue（scheduler.New(store,store,store)），
// 避免依赖真实 CockroachDB/Redis。Redis 不可达时此路径仍可 `go test` 跑（路径 A）。
// 涉及真实 PG CAS + 心跳扫描器的回收场景（T017）留 -tags=integration。

// pendTask 构造 PENDING 任务，selector 空=通配。
func pendTask(id int64, group string, selector model.WorkerSelector) model.Task {
	return model.Task{UnitID: idStr(id), Priority: id, GroupUID: group, State: model.StatePending, WorkerSelector: selector}
}

func idStr(id int64) string {
	if id < 0 {
		return ""
	}
	// 简单十进制转字符串，避免引入 strconv 增复杂度（测试内足够）。
	if id == 0 {
		return "unit-0"
	}
	var buf [20]byte
	i := len(buf)
	for id > 0 {
		i--
		buf[i] = byte('0' + id%10)
		id /= 10
	}
	return "unit-" + string(buf[i:])
}

// allWorker 空 labels，匹配任意空 selector 任务。
func allWorker(id string) model.Worker { return model.Worker{UnitID: id, Capacity: 8} }

// drainMatchOnce 启动 runMatchLoop，等至少一轮 tick 后取消并等 goroutine 退出。
// 返回的 cancel 在内部已调用；调用方只需等待返回。
func runOneTick(t *testing.T, m *scheduler.Matcher, interval time.Duration) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runMatchLoop(ctx, m, interval)
		close(done)
	}()
	// 等约 2 个 tick 确保至少执行一轮撮合。
	time.Sleep(interval * 3)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runMatchLoop 未在 2s 内退出（goroutine 泄漏）")
	}
}

// TestMatchLoop_PendingToScheduled 验证 US1/SC-001：PENDING task 匹配在线 worker 后
// 经 runMatchLoop 撮合转 SCHEDULED 且派发队列有 member。
func TestMatchLoop_PendingToScheduled(t *testing.T) {
	store := fake.New()
	store.AddTask(pendTask(1, "g1", nil))
	store.AddWorker(allWorker("w1"))

	m := scheduler.New(store, store, store, 10)
	runOneTick(t, m, 20*time.Millisecond)

	t1, ok := store.Task("unit-1")
	if !ok {
		t.Fatal("task not found")
	}
	if t1.State != model.StateScheduled {
		t.Fatalf("state = %s, want SCHEDULED（已撮合入队）", t1.State)
	}
	if got := store.BucketLen("w1", "g1"); got != 1 {
		t.Fatalf("dispatch bucket len = %d, want 1（派发队列有 member）", got)
	}
}

// TestMatchLoop_NoMatchBackoffNeverDiscard 验证 US1/SC-006：无匹配在线 worker
// 转 BACKOFF，重复撮合永不放弃（不转 DISCARDED）。
func TestMatchLoop_NoMatchBackoffNeverDiscard(t *testing.T) {
	store := fake.New()
	// selector 要求 gpu=A100，但无 worker 带 gpu label -> 无匹配。
	store.AddTask(pendTask(1, "g1", model.WorkerSelector{"gpu": "A100"}))
	store.AddWorker(allWorker("w1"))

	m := scheduler.New(store, store, store, 10)
	// 多轮撮合（间隔短，覆盖多次退避到期也不 DISCARDED）。
	runOneTick(t, m, 20*time.Millisecond)
	runOneTick(t, m, 20*time.Millisecond)

	t1, ok := store.Task("unit-1")
	if !ok {
		t.Fatal("task not found")
	}
	if t1.State != model.StateBackoff {
		t.Fatalf("state = %s, want BACKOFF（无匹配退避）", t1.State)
	}
	if t1.State == model.StateDiscarded {
		t.Fatal("永不放弃：无匹配 worker 不应转 DISCARDED")
	}
}

// TestMatchLoop_PriorityOrder 验证 FR-007：高优先级先入队，同优先级按 unit_id 序。
func TestMatchLoop_PriorityOrder(t *testing.T) {
	store := fake.New()
	// 三个任务 priority 不同：3 > 2 > 1，撮合应按 priority 降序入队（高先派）。
	// fake ListPendByGroup ORDER BY priority, unit_id（priority 大者先）。
	store.AddTask(pendTask(1, "g1", nil)) // priority=1
	store.AddTask(pendTask(3, "g1", nil)) // priority=3
	store.AddTask(pendTask(2, "g1", nil)) // priority=2
	store.AddWorker(allWorker("w1"))

	m := scheduler.New(store, store, store, 3)
	runOneTick(t, m, 20*time.Millisecond)

	// 三个均应入 w1/g1 桶（撮合批=3）。
	if got := store.BucketLen("w1", "g1"); got != 3 {
		t.Fatalf("bucket len = %d, want 3", got)
	}
	// 验证全部转 SCHEDULED。
	for _, id := range []string{"unit-1", "unit-2", "unit-3"} {
		tt, _ := store.Task(id)
		if tt.State != model.StateScheduled {
			t.Fatalf("%s state = %s, want SCHEDULED", id, tt.State)
		}
	}
}

// TestMatchLoop_MultiGroupFairness 验证 US2/SC-004：两 group（A 大 B 小）共享 worker，
// 持续撮合两组均推进，B 不被 A 饿死。
func TestMatchLoop_MultiGroupFairness(t *testing.T) {
	store := fake.New()
	for i := int64(0); i < 50; i++ {
		store.AddTask(pendTask(1000+i, "gA", nil)) // 大户 50
	}
	for i := int64(0); i < 3; i++ {
		store.AddTask(pendTask(2000+i, "gB", nil)) // 小户 3
	}
	store.AddWorker(allWorker("w1"))

	m := scheduler.New(store, store, store, 5)
	// 跑多轮 tick 让游标在 gA/gB 间轮询（每轮 1 group）。
	runOneTick(t, m, 20*time.Millisecond)
	runOneTick(t, m, 20*time.Millisecond)

	aLen := store.BucketLen("w1", "gA")
	bLen := store.BucketLen("w1", "gB")
	if aLen == 0 {
		t.Fatal("gA 未推进（大户应有 SCHEDULED）")
	}
	if bLen == 0 {
		t.Fatal("gB 未推进（小户被饿死，违反 SC-004）")
	}
}

// TestMatchLoop_EmptyGroupSkipped 验证 FR-004：某 group 无匹配 worker 时
// 轮询跳过该组不阻塞整体推进（空桶跳过）。
func TestMatchLoop_EmptyGroupSkipped(t *testing.T) {
	store := fake.New()
	// g1 有可匹配任务，g2 的任务要求 gpu=A100 无匹配。
	store.AddTask(pendTask(1, "g1", nil))
	store.AddTask(pendTask(2, "g2", model.WorkerSelector{"gpu": "A100"}))
	store.AddWorker(allWorker("w1"))

	m := scheduler.New(store, store, store, 5)
	runOneTick(t, m, 20*time.Millisecond)
	runOneTick(t, m, 20*time.Millisecond)

	// g1 应推进（SCHEDULED），g2 任务转 BACKOFF 不阻塞。
	t1, _ := store.Task("unit-1")
	if t1.State != model.StateScheduled {
		t.Fatalf("g1 state = %s, want SCHEDULED（空桶不阻塞推进）", t1.State)
	}
	t2, _ := store.Task("unit-2")
	if t2.State != model.StateBackoff {
		t.Fatalf("g2 state = %s, want BACKOFF（无匹配退避）", t2.State)
	}
}

// TestMatchLoop_DualMatcherNoDoubleDispatch 验证 US3/SC-003：两个 Matcher（不同 nodeID）
// 并发撮合同一 PENDING task，仅一个 CAS 成功，零重复下发。
func TestMatchLoop_DualMatcherNoDoubleDispatch(t *testing.T) {
	store := fake.New()
	store.AddTask(pendTask(1, "g1", nil))
	store.AddWorker(allWorker("w1"))

	// 两个 matcher 共享同一 store + dispatch 队列，无软分片（都撮合 g1）。
	m1 := scheduler.New(store, store, store, 10)
	m2 := scheduler.New(store, store, store, 10)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	for _, m := range []*scheduler.Matcher{m1, m2} {
		wg.Add(1)
		go func(mm *scheduler.Matcher) {
			defer wg.Done()
			// 并发各撮合一次。
			_, _ = mm.MatchOnce(ctx)
		}(m)
	}
	wg.Wait()

	// 仅一个应成功（dispatch_count=1，桶 member=1）。
	t1, _ := store.Task("unit-1")
	if t1.State != model.StateScheduled {
		t.Fatalf("state = %s, want SCHEDULED（应被撮合一次）", t1.State)
	}
	if t1.DispatchCount != 1 {
		t.Fatalf("dispatch_count = %d, want 1（零重复下发，SC-003）", t1.DispatchCount)
	}
	if got := store.BucketLen("w1", "g1"); got != 1 {
		t.Fatalf("bucket len = %d, want 1（派发队列无重复 member）", got)
	}
}

// TestMatchLoop_RunLoopGracefulExit 验证装配不变量（contracts/matcher-loop.md）：
// runMatchLoop 随 ctx 取消优雅退出，不泄漏 goroutine。
func TestMatchLoop_RunLoopGracefulExit(t *testing.T) {
	store := fake.New()
	store.AddWorker(allWorker("w1"))
	m := scheduler.New(store, store, store, 10)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runMatchLoop(ctx, m, 50*time.Millisecond)
		close(done)
	}()

	cancel()
	select {
	case <-done:
		// 优雅退出，无泄漏。
	case <-time.After(2 * time.Second):
		t.Fatal("runMatchLoop 未在 ctx 取消后 2s 内退出（goroutine 泄漏）")
	}
}

// TestMatchLoop_IntervalFallback 验证 interval<=0 时兜底 100ms，不忙轮询。
func TestMatchLoop_IntervalFallback(t *testing.T) {
	store := fake.New()
	store.AddWorker(allWorker("w1"))
	m := scheduler.New(store, store, store, 10)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runMatchLoop(ctx, m, 0) // 0 兜底 100ms
		close(done)
	}()

	// 100ms 兜底，50ms 内不应频繁 tick（间接验证未忙轮询）。
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("兜底 interval 下未优雅退出")
	}
}

// TestMatchLoop_DispatchExceedMaxAttempt 验证 US3/FR-009：task 反复派发（dispatch_count 累计）
// 超 TaskMaxAttempt 后转 DISCARDED，不再参与撮合。
//
// 链路：撮合 PENDING->SCHEDULED（dispatch_count=1）-> worker Pull SCHEDULED->RUNNING
// -> ReportFail 未超限 reschedule 回 PENDING（dispatch_count 不变）-> 再次撮合 dispatch_count=2
// -> ... 超限转 DISCARDED。fake ReportFail 在 RUNNING 且 dispatch_count>=max 时转 DISCARDED。
func TestMatchLoop_DispatchExceedMaxAttempt(t *testing.T) {
	store := fake.New()
	store.AddTask(pendTask(1, "g1", nil))
	store.AddWorker(allWorker("w1"))

	const maxAttempt = 2
	m := scheduler.New(store, store, store, 10)
	d := scheduler.NewDispatcher(store, store) // fake 同时作 TaskStore + DispatchQueue
	ctx := context.Background()

	// 循环：撮合 -> 拉取转 RUNNING -> ReportFail（未超限回 PENDING），直到 dispatch_count 达上限转 DISCARDED。
	for i := 0; i < maxAttempt+2; i++ {
		// 1. 撮合 PENDING->SCHEDULED（dispatch_count++）。
		if _, err := m.MatchOnce(ctx); err != nil {
			t.Fatalf("match once iter %d: %v", i, err)
		}
		t1, _ := store.Task("unit-1")
		if t1.State == model.StateDiscarded {
			break // 已超限放弃
		}
		// 2. worker Pull 转 RUNNING。
		pulled, err := d.Pull(ctx, "w1", 10)
		if err != nil {
			t.Fatalf("pull iter %d: %v", i, err)
		}
		if len(pulled) != 1 {
			t.Fatalf("iter %d: pulled %d, want 1", i, len(pulled))
		}
		// 3. ReportFail：未超限回 PENDING（可再撮合），超限转 DISCARDED。
		_, discarded, err := store.ReportFail(ctx, "unit-1", "w1", maxAttempt)
		if err != nil {
			t.Fatalf("report fail iter %d: %v", i, err)
		}
		if discarded {
			break // 超限转 DISCARDED
		}
	}

	t1, _ := store.Task("unit-1")
	if t1.State != model.StateDiscarded {
		t.Fatalf("state = %s, want DISCARDED（派发超限放弃，FR-009）", t1.State)
	}
}
