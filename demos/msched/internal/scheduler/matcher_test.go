package scheduler_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/balcony314/msched/internal/model"
	"github.com/balcony314/msched/internal/scheduler"
	"github.com/balcony314/msched/internal/scheduler/fake"
)

// pendingTask 构造一个 PENDING 任务，selector 默认空（通配，匹配任意 worker）。
func pendingTask(id int64, group string, selector model.WorkerSelector) model.Task {
	// UnitID 与 Priority 均由 id 派生：UnitID 作唯一标识，Priority 作 FIFO 排序键（id 递增）。
	return model.Task{UnitID: fmt.Sprintf("unit-%d", id), Priority: id, GroupUID: group, State: model.StatePending, WorkerSelector: selector}
}

// allMatchWorker 构造一个空 labels worker，匹配任意空 selector 任务。
func allMatchWorker(id string) model.Worker {
	return model.Worker{UnitID: id, Capacity: 8}
}

// TestMatcher_GroupCursorRoundRobin 验证撮合端 group 游标轮询防饿死（DESIGN §9.1）：
// g1 大户 100 任务、g2 小户 5 任务、单 worker 全匹配。
// 第 1 轮撮合 g1，第 2 轮推进到 g2——小户 g2 不被大户 g1 阻塞，得到撮合机会。
func TestMatcher_GroupCursorRoundRobin(t *testing.T) {
	store := fake.New()
	for i := int64(0); i < 100; i++ {
		store.AddTask(pendingTask(1000+i, "g1", nil))
	}
	for i := int64(0); i < 5; i++ {
		store.AddTask(pendingTask(2000+i, "g2", nil))
	}
	store.AddWorker(allMatchWorker("w1"))

	m := scheduler.New(store, store, store, 10)
	ctx := context.Background()

	// 第 1 轮：游标在 g1，撮合 g1 前 10 个。
	n1, err := m.MatchOnce(ctx)
	if err != nil {
		t.Fatalf("match once 1: %v", err)
	}
	if n1 != 10 {
		t.Fatalf("round 1 dispatched = %d, want 10", n1)
	}
	if got := store.BucketLen("w1", "g1"); got != 10 {
		t.Fatalf("g1 bucket len after round 1 = %d, want 10", got)
	}
	if got := store.BucketLen("w1", "g2"); got != 0 {
		t.Fatalf("g2 bucket len after round 1 = %d, want 0 (cursor 未到 g2)", got)
	}

	// 第 2 轮：游标推进到 g2，撮合 g2 前 5 个——防饿死关键断言。
	n2, err := m.MatchOnce(ctx)
	if err != nil {
		t.Fatalf("match once 2: %v", err)
	}
	if n2 != 5 {
		t.Fatalf("round 2 dispatched = %d, want 5", n2)
	}
	if got := store.BucketLen("w1", "g2"); got != 5 {
		t.Fatalf("g2 bucket len after round 2 = %d, want 5 (小户未被饿死)", got)
	}
}

// TestMatcher_RoundRobinAllGroups 验证游标环形轮询遍历所有活跃 group（DESIGN §9.1）。
// g1/g2/g3 各 3 任务，连续 3 轮各处理一个 group，顺序 g1→g2→g3。
func TestMatcher_RoundRobinAllGroups(t *testing.T) {
	store := fake.New()
	// g1: 1,2,3  g2: 4,5,6  g3: 7,8,9
	for _, g := range []string{"g1", "g2", "g3"} {
		base := map[string]int64{"g1": 1, "g2": 4, "g3": 7}[g]
		for i := int64(0); i < 3; i++ {
			store.AddTask(pendingTask(base+i, g, nil))
		}
	}
	store.AddWorker(allMatchWorker("w1"))

	m := scheduler.New(store, store, store, 3)
	ctx := context.Background()

	want := []string{"g1", "g2", "g3"}
	for i, g := range want {
		n, err := m.MatchOnce(ctx)
		if err != nil {
			t.Fatalf("round %d: %v", i+1, err)
		}
		if n != 3 {
			t.Fatalf("round %d dispatched = %d, want 3", i+1, n)
		}
		if got := store.BucketLen("w1", g); got != 3 {
			t.Fatalf("round %d: %s bucket len = %d, want 3", i+1, g, got)
		}
	}
}

// TestMatcher_NoMatchBackoff 验证无匹配 worker 的任务转 BACKOFF 退避（DESIGN §8.6）。
// task 应进入 BACKOFF 态且 NextRetryTime 已设（now + NextBackoff(0) = 30s 后）。
func TestMatcher_NoMatchBackoff(t *testing.T) {
	store := fake.New()
	store.AddTask(pendingTask(1, "g1", model.WorkerSelector{"gpu": "A100"}))
	store.AddWorker(model.Worker{UnitID: "w1", Capacity: 8}) // 无 gpu label，不匹配

	m := scheduler.New(store, store, store, 10)
	ctx := context.Background()

	n, err := m.MatchOnce(ctx)
	if err != nil {
		t.Fatalf("match once: %v", err)
	}
	if n != 0 {
		t.Fatalf("dispatched = %d, want 0 (无匹配 worker)", n)
	}
	t1, ok := store.Task("unit-1")
	if !ok {
		t.Fatal("task 1 not found")
	}
	if t1.State != model.StateBackoff {
		t.Fatalf("task state = %s, want BACKOFF（无匹配退避）", t1.State)
	}
	if t1.NextRetryTime == nil {
		t.Fatal("NextRetryTime 未设，want 30s 后")
	}
	if t1.FailCount != 1 {
		t.Fatalf("FailCount = %d, want 1", t1.FailCount)
	}
}

// TestMatcher_NoActiveGroups 验证无活跃 group 时 MatchOnce 不报错、零派发。
func TestMatcher_NoActiveGroups(t *testing.T) {
	store := fake.New()
	store.AddWorker(allMatchWorker("w1"))

	m := scheduler.New(store, store, store, 10)
	n, err := m.MatchOnce(context.Background())
	if err != nil {
		t.Fatalf("match once: %v", err)
	}
	if n != 0 {
		t.Fatalf("dispatched = %d, want 0", n)
	}
}

// TestMatcher_CASConflictSkip 验证 CAS 失败（他节点已抢）时跳过、不影响其他任务。
func TestMatcher_CASConflictSkip(t *testing.T) {
	store := fake.New()
	store.AddTask(pendingTask(1, "g1", nil))
	store.AddTask(pendingTask(2, "g1", nil))
	store.AddWorker(allMatchWorker("w1"))

	// 手动把 task 1 抢走（模拟他节点已 CAS），撮合器再取到应 CAS 失败跳过。
	ok, err := store.CASSchedule(context.Background(), "unit-1", "other")
	if err != nil || !ok {
		t.Fatalf("preset CAS failed: ok=%v err=%v", ok, err)
	}

	m := scheduler.New(store, store, store, 10)
	n, err := m.MatchOnce(context.Background())
	if err != nil {
		t.Fatalf("match once: %v", err)
	}
	if n != 1 {
		t.Fatalf("dispatched = %d, want 1 (task 1 已被抢，仅 task 2)", n)
	}
	if got := store.BucketLen("w1", "g1"); got != 1 {
		t.Fatalf("g1 bucket len = %d, want 1", got)
	}
}

// TestMatcher_ShardFiltersGroups 验证软分片：节点只撮合一致性哈希环归属自己的 group（DESIGN §5）。
// 两节点环下，归属他节点的 group 即使有任务也不被本节点撮合。
func TestMatcher_ShardFiltersGroups(t *testing.T) {
	store := fake.New()
	store.AddTask(pendingTask(1, "g1", nil))
	store.AddTask(pendingTask(2, "g2", nil))
	store.AddWorker(allMatchWorker("w1"))

	// 两节点环，确定 g1/g2 归属。
	ring := scheduler.NewRing(64)
	ring.Add("nodeA", "nodeB")
	ownerG1 := ring.Owner("g1")
	other := "nodeA"
	if ownerG1 == "nodeA" {
		other = "nodeB"
	}
	// 以「不拥有 g1 的节点」身份撮合：g1 不应被处理（仍 PENDING）。
	m := scheduler.NewWithShard(store, store, store, 10, ring, other)
	n, err := m.MatchOnce(context.Background())
	if err != nil {
		t.Fatalf("match once: %v", err)
	}
	// other 至少拥有 g2，应撮合 g2（n>=1）；g1 不归 other，应保持 PENDING。
	if n == 0 {
		t.Fatalf("dispatched = 0, want >=1 (other 应拥有某 group)")
	}
	t1, _ := store.Task("unit-1")
	if t1.State != model.StatePending {
		t.Fatalf("g1 不归本节点，应保持 PENDING，got %s", t1.State)
	}
}

// TestMatcher_ShardOwnerProcesses 验证归属自己的 group 正常撮合（DESIGN §5）。
func TestMatcher_ShardOwnerProcesses(t *testing.T) {
	store := fake.New()
	store.AddTask(pendingTask(1, "g1", nil))
	store.AddWorker(allMatchWorker("w1"))

	ring := scheduler.NewRing(64)
	ring.Add("nodeA")
	owner := ring.Owner("g1")
	if owner != "nodeA" {
		t.Fatalf("单节点环 g1 应归 nodeA，got %s", owner)
	}
	m := scheduler.NewWithShard(store, store, store, 10, ring, "nodeA")
	n, err := m.MatchOnce(context.Background())
	if err != nil {
		t.Fatalf("match once: %v", err)
	}
	if n != 1 {
		t.Fatalf("dispatched = %d, want 1 (归属自己的 group 应撮合)", n)
	}
}
