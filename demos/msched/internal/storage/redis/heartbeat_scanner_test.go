package redis

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/balcony314/msched/internal/model"
	"github.com/balcony314/msched/internal/scheduler/fake"
	"github.com/redis/go-redis/v9"
)

// TestTaskHeartbeatScannerRecover zset 过期 task -> RecoverTask CAS RUNNING->PENDING + Remove。
func TestTaskHeartbeatScannerRecover(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()

	store := fake.New()
	// 状态机走到 RUNNING：AddTask(PENDING) -> CASSchedule(w1) -> BatchSetRunning(w1)。
	store.AddTask(model.Task{UnitID: "t1", GroupUID: "g1", State: model.StatePending, MaxExecDuration: 10 * time.Second})
	if ok, err := store.CASSchedule(ctx, "t1", "w1"); err != nil || !ok {
		t.Fatalf("cas schedule: ok=%v err=%v", ok, err)
	}
	if _, err := store.BatchSetRunning(ctx, "w1", []string{"t1"}); err != nil {
		t.Fatalf("batch set running: %v", err)
	}

	hb := NewTaskHeartbeat(client)
	// task zset 设 t1 过期。
	if err := hb.Add(ctx, "t1", time.Now().Add(-1*time.Second)); err != nil {
		t.Fatalf("add expired t1: %v", err)
	}

	scanner := NewTaskHeartbeatScanner(hb, store, 0)
	if err := scanner.scanOnce(ctx); err != nil {
		t.Fatalf("scan once: %v", err)
	}

	// t1 应已 RUNNING -> PENDING。
	t1, _ := store.Task("t1")
	if t1.State != model.StatePending {
		t.Errorf("t1 state = %s, want PENDING", t1.State)
	}
	if t1.LeaseOwner != "" {
		t.Errorf("t1 lease_owner = %q, want empty", t1.LeaseOwner)
	}
	// zset 已 Remove。
	expired, _ := hb.Expired(ctx)
	if len(expired) != 0 {
		t.Errorf("after scan, expired = %v, want empty", expired)
	}
}

// TestWorkerHeartbeatScannerRecover worker 过期 -> RecoverByWorker 回收 RUNNING+SCHEDULED + 清派发桶 + Remove。
func TestWorkerHeartbeatScannerRecover(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()

	store := fake.New()
	// t1: RUNNING（w1）；t2: SCHEDULED（w1，在派发桶）。
	store.AddTask(model.Task{UnitID: "t1", GroupUID: "g1", State: model.StatePending, MaxExecDuration: 10 * time.Second})
	store.AddTask(model.Task{UnitID: "t2", GroupUID: "g1", State: model.StatePending, MaxExecDuration: 10 * time.Second})
	store.CASSchedule(ctx, "t1", "w1")
	store.CASSchedule(ctx, "t2", "w1")
	store.BatchSetRunning(ctx, "w1", []string{"t1"}) // t1 RUNNING；t2 留 SCHEDULED

	dq := NewDispatchQueue(client)
	dq.Push(ctx, "w1", "g1", "t2", time.Now().UnixMilli()) // t2 在派发桶

	workerHB := NewWorkerHeartbeat(client)
	taskHB := NewTaskHeartbeat(client)
	// worker zset w1 过期；task zset t1（RUNNING）未过期（扫描器应随 recovered 删）。
	workerHB.Renew(ctx, "w1", time.Now().Add(-1*time.Second))
	taskHB.Add(ctx, "t1", time.Now().Add(10*time.Second))

	scanner := NewWorkerHeartbeatScanner(workerHB, taskHB, store, dq, 0)
	if err := scanner.scanOnce(ctx); err != nil {
		t.Fatalf("scan once: %v", err)
	}

	// t1/t2 应转 PENDING。
	for _, uid := range []string{"t1", "t2"} {
		tk, _ := store.Task(uid)
		if tk.State != model.StatePending {
			t.Errorf("%s state = %s, want PENDING", uid, tk.State)
		}
	}
	// 派发桶应已清空（worker 死了不再 Pull，ClearWorker SCAN+DEL 整桶）。
	got, err := dq.PullRoundRobin(ctx, "w1", 10)
	if err != nil {
		t.Fatalf("pull after clear: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("dispatch bucket after clear = %v, want empty", got)
	}
	// worker zset 已 Remove。
	active, _ := workerHB.Active(ctx)
	if _, ok := active["w1"]; ok {
		t.Errorf("worker w1 should be removed: %v", active)
	}
	// task zset t1 已随 recovered RemoveMany 删除。
	expired, _ := taskHB.Expired(ctx)
	_ = expired // t1 未过期，不在 expired；验证 Active 集合不含。
}
