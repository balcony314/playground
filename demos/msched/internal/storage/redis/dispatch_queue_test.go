package redis

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/balcony314/msched/internal/scheduler"
	"github.com/redis/go-redis/v9"
)

// newMiniQueue 启动 miniredis 并返回连它的 DispatchQueue + 清理函数。
func newMiniQueue(t *testing.T) (*DispatchQueue, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return NewDispatchQueue(client), mr
}

// TestPushAndPullSingleBucket 单 group 桶：Push 后 Pull 按 score 升序取（ZPOPMIN）。
func TestPushAndPullSingleBucket(t *testing.T) {
	q, _ := newMiniQueue(t)
	ctx := context.Background()

	if err := q.Push(ctx, "w1", "g1", "u1", 2000); err != nil {
		t.Fatalf("push u1: %v", err)
	}
	if err := q.Push(ctx, "w1", "g1", "u2", 1000); err != nil {
		t.Fatalf("push u2: %v", err)
	}
	// 非阻塞，batch=10，应取全部 2 个（按 score 升序：u2 在前）
	got, err := q.PullRoundRobin(ctx, "w1", 10)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d tasks, want 2: %+v", len(got), got)
	}
	if got[0].UnitID != "u2" || got[1].UnitID != "u1" {
		t.Errorf("order by score asc: got %+v", got)
	}
	if got[0].Group != "g1" || got[0].Wuid != "w1" {
		t.Errorf("ScheduledTask fields wrong: %+v", got[0])
	}
}

// TestPullRoundRobinFairness 多 group 桶轮询各取一个，凑满 batch。验证 group 公平（§9.2）。
func TestPullRoundRobinFairness(t *testing.T) {
	q, _ := newMiniQueue(t)
	ctx := context.Background()

	// w1 有 g1（2 个）、g2（2 个）、g3（1 个），batch=2 一轮各取一个。
	for _, item := range []struct {
		wuid, group, unit string
		score             int64
	}{
		{"w1", "g1", "u11", 1},
		{"w1", "g1", "u12", 2},
		{"w1", "g2", "u21", 1},
		{"w1", "g2", "u22", 2},
		{"w1", "g3", "u31", 1},
	} {
		if err := q.Push(ctx, item.wuid, item.group, item.unit, item.score); err != nil {
			t.Fatalf("push %s: %v", item.unit, err)
		}
	}

	got, err := q.PullRoundRobin(ctx, "w1", 2)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
	// 第一轮应来自两个不同 group（g1, g2，按 key 排序稳定序）。
	groups := map[string]bool{}
	for _, t := range got {
		groups[t.Group] = true
	}
	if len(groups) != 2 {
		t.Errorf("first round should span 2 groups, got %d: %+v", len(groups), got)
	}
}

// TestPullEmptyNonBlocking 空队列立即返回空切片，非阻塞（DESIGN §1.12/REDIS.md §1.1）。
func TestPullEmptyNonBlocking(t *testing.T) {
	q, _ := newMiniQueue(t)
	ctx := context.Background()

	done := make(chan struct{})
	go func() {
		got, err := q.PullRoundRobin(ctx, "w1", 10)
		if err != nil {
			t.Errorf("pull empty: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %d, want 0", len(got))
		}
		close(done)
	}()

	select {
	case <-done:
		// 非阻塞立即返回
	case <-time.After(time.Second):
		t.Fatal("PullRoundRobin blocked on empty queue; should be non-blocking")
	}
}

// TestPullAcrossWorkersIsolation 不同 worker 桶隔离：w1 拉不到 w2 的任务。
func TestPullAcrossWorkersIsolation(t *testing.T) {
	q, _ := newMiniQueue(t)
	ctx := context.Background()

	if err := q.Push(ctx, "w1", "g1", "u1", 1); err != nil {
		t.Fatalf("push: %v", err)
	}
	if err := q.Push(ctx, "w2", "g1", "u2", 1); err != nil {
		t.Fatalf("push: %v", err)
	}

	got, err := q.PullRoundRobin(ctx, "w1", 10)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if len(got) != 1 || got[0].UnitID != "u1" || got[0].Wuid != "w1" {
		t.Errorf("w1 should only get its own task, got %+v", got)
	}
}

// TestPullBatchLimit batch 上限：多个任务但只取 batch 数。
func TestPullBatchLimit(t *testing.T) {
	q, _ := newMiniQueue(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if err := q.Push(ctx, "w1", "g1", "u"+string(rune('0'+i)), int64(i)); err != nil {
			t.Fatalf("push: %v", err)
		}
	}
	got, err := q.PullRoundRobin(ctx, "w1", 3)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("got %d, want 3 (batch limit)", len(got))
	}
}

// 确保 scheduler 包被引用（ScheduledTask 类型）。
var _ []scheduler.ScheduledTask

// TestPullMultiRound 桶数 < batch 且各桶多任务时，Lua 多轮凑满 batch。
func TestPullMultiRound(t *testing.T) {
	q, _ := newMiniQueue(t)
	ctx := context.Background()

	// g1 3 个，g2 3 个，batch=5 -> 第一轮各取 1（g1,g2），第二轮各取 1，第三轮 g1 取 1 凑满 5
	for i, u := range []string{"u11", "u12", "u13"} {
		if err := q.Push(ctx, "w1", "g1", u, int64(i)); err != nil {
			t.Fatalf("push: %v", err)
		}
	}
	for i, u := range []string{"u21", "u22", "u23"} {
		if err := q.Push(ctx, "w1", "g2", u, int64(i)); err != nil {
			t.Fatalf("push: %v", err)
		}
	}

	got, err := q.PullRoundRobin(ctx, "w1", 5)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("got %d, want 5: %+v", len(got), got)
	}
	// 验证轮询公平：前 2 个应分属 g1/g2，3-4 分属 g1/g2，第 5 个属 g1 或 g2
	if len(got) < 2 || got[0].Group == got[1].Group {
		t.Errorf("前两个应来自不同 group: %+v", got[:2])
	}
	// 桶应清空（共 6 取 5，剩 1）
	got2, _ := q.PullRoundRobin(ctx, "w1", 10)
	if len(got2) != 1 {
		t.Errorf("残余应 1, got %d: %+v", len(got2), got2)
	}
}

// TestPullMixedEmptyBuckets 部分桶空、部分有，只取非空桶，跨过空桶。
func TestPullMixedEmptyBuckets(t *testing.T) {
	q, _ := newMiniQueue(t)
	ctx := context.Background()

	// g1 有 2 个，g2 空（无 Push），g3 有 1 个；SCAN 会扫到 g1/g3（g2 无桶不建 key）
	if err := q.Push(ctx, "w1", "g1", "u1", 1); err != nil {
		t.Fatalf("push g1: %v", err)
	}
	if err := q.Push(ctx, "w1", "g1", "u2", 2); err != nil {
		t.Fatalf("push g1: %v", err)
	}
	if err := q.Push(ctx, "w1", "g3", "u3", 1); err != nil {
		t.Fatalf("push g3: %v", err)
	}

	got, err := q.PullRoundRobin(ctx, "w1", 10)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d, want 3: %+v", len(got), got)
	}
	groups := map[string]int{}
	for _, tk := range got {
		groups[tk.Group]++
	}
	if groups["g1"] != 2 || groups["g3"] != 1 {
		t.Errorf("group 分布: %+v, want g1=2 g3=1", groups)
	}
}

// TestPullNoBuckets SCAN 无桶直接返回空（省 EVAL 快路径）。
func TestPullNoBuckets(t *testing.T) {
	q, _ := newMiniQueue(t)
	ctx := context.Background()

	got, err := q.PullRoundRobin(ctx, "w1", 10)
	if err != nil {
		t.Fatalf("pull empty worker: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d, want 0: %+v", len(got), got)
	}
}
