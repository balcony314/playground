package scheduler_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/balcony314/msched/internal/model"
	"github.com/balcony314/msched/internal/scheduler"
	"github.com/balcony314/msched/internal/scheduler/fake"
)

// scheduledTask 构造一个已入派发队列的 SCHEDULED 任务。
func scheduledTask(id int64, group string) model.Task {
	return model.Task{UnitID: fmt.Sprintf("unit-%d", id), GroupUID: group, State: model.StateScheduled}
}

// fillQueue 向 wuid 的 group 桶填入 tasks（按 id 递增作 score，模拟 dispatch_time）。
func fillQueue(store *fake.Store, wuid string, group string, ids []int64) {
	ctx := context.Background()
	for _, id := range ids {
		tk := scheduledTask(id, group)
		tk.LeaseOwner = wuid // CAS 时设 lease_owner，BatchSetRunning 所有权校验用
		store.AddTask(tk)
		if err := store.Push(ctx, wuid, group, tk.UnitID, id); err != nil {
			panic(err)
		}
	}
}

// TestDispatcher_PullRoundRobinFairness 验证派发端 key 轮询防饿死（DESIGN §9.2）：
// w1 下 g1(10)、g2(3)、g3(1) 三桶，Pull(batch=5) 应每桶至少各取一个，
// 大户 g1 不独占 batch。预期分布 g1=2, g2=2, g3=1。
func TestDispatcher_PullRoundRobinFairness(t *testing.T) {
	store := fake.New()
	fillQueue(store, "w1", "g1", seq(1, 10))  // g1: 1..10
	fillQueue(store, "w1", "g2", seq(11, 13)) // g2: 11..13
	fillQueue(store, "w1", "g3", []int64{14}) // g3: 14

	d := scheduler.NewDispatcher(store, store)
	tasks, err := d.Pull(context.Background(), "w1", 5)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if len(tasks) != 5 {
		t.Fatalf("got %d tasks, want 5", len(tasks))
	}

	counts := map[string]int{}
	for _, tk := range tasks {
		counts[tk.GroupUID]++
	}
	// 防饿死：每桶至少 1 个代表。
	if counts["g1"] == 0 || counts["g2"] == 0 || counts["g3"] == 0 {
		t.Fatalf("某 group 未被取到，分布=%v（应每桶至少 1）", counts)
	}
	// 单轮各取一个凑满 5：g1=2, g2=2, g3=1，大户 g1 未独占。
	if counts["g1"] != 2 || counts["g2"] != 2 || counts["g3"] != 1 {
		t.Fatalf("group 分布=%v，want g1=2,g2=2,g3=1", counts)
	}

	// 拉走的任务应转 RUNNING（DESIGN §8.3）。
	for _, tk := range tasks {
		snap, ok := store.Task(tk.UnitID)
		if !ok {
			t.Fatalf("task %s not found", tk.UnitID)
		}
		if snap.State != model.StateRunning {
			t.Fatalf("task %s state=%s, want RUNNING", tk.UnitID, snap.State)
		}
	}
}

// TestDispatcher_PullEmpty 验证空派发队列 Pull 返回空批、不报错（DESIGN §1.12 非阻塞）。
func TestDispatcher_PullEmpty(t *testing.T) {
	store := fake.New()
	d := scheduler.NewDispatcher(store, store)
	tasks, err := d.Pull(context.Background(), "w1", 5)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("got %d tasks, want 0", len(tasks))
	}
}

// TestDispatcher_PullBatchLargerThanTotal 验证 batch 大于总存量时返回全部，仍每桶各取一个。
func TestDispatcher_PullBatchLargerThanTotal(t *testing.T) {
	store := fake.New()
	fillQueue(store, "w1", "g1", []int64{1})
	fillQueue(store, "w1", "g2", []int64{2})

	d := scheduler.NewDispatcher(store, store)
	tasks, err := d.Pull(context.Background(), "w1", 10)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("got %d tasks, want 2", len(tasks))
	}
	counts := map[string]int{}
	for _, tk := range tasks {
		counts[tk.GroupUID]++
	}
	if counts["g1"] != 1 || counts["g2"] != 1 {
		t.Fatalf("group 分布=%v，want g1=1,g2=1", counts)
	}
}

// TestDispatcher_PullSkipsNotOwned 验证派发队列残留 member 但任务已不属本 worker 时，
// Pull 不返回该任务（防双发，DESIGN §8.4/§8.7）。
// 场景：u1 被 CAS 给 w2，但 member 残留在 w1 派发队列（模拟回收重派后 member 未清/错位）。
// Pull(w1) 应跳过 u1（BatchSetRunning lease_owner 校验），返回空批。
func TestDispatcher_PullSkipsNotOwned(t *testing.T) {
	store := fake.New()
	// u1 经 CAS 派给 w2（lease_owner=w2），但 member 被 push 进 w1 队列（错位/残留）。
	store.AddTask(model.Task{UnitID: "u1", GroupUID: "g1", State: model.StatePending})
	if _, err := store.CASSchedule(context.Background(), "u1", "w2"); err != nil {
		t.Fatalf("CAS: %v", err)
	}
	if err := store.Push(context.Background(), "w1", "g1", "u1", 1); err != nil {
		t.Fatalf("push: %v", err)
	}

	d := scheduler.NewDispatcher(store, store)
	tasks, err := d.Pull(context.Background(), "w1", 5)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("got %d tasks, want 0（不属 w1 的任务不应返回，防双发）", len(tasks))
	}
	// u1 仍 SCHEDULED 属 w2，未被 w1 误标 RUNNING。
	snap, ok := store.Task("u1")
	if !ok || snap.State != model.StateScheduled || snap.LeaseOwner != "w2" {
		t.Fatalf("u1 状态被破坏: state=%s owner=%s", snap.State, snap.LeaseOwner)
	}
}

// seq 返回 [start, end] 的 int64 切片（end 含）。
func seq(start, end int64) []int64 {
	out := make([]int64, 0, end-start+1)
	for i := start; i <= end; i++ {
		out = append(out, i)
	}
	return out
}
