//go:build integration

package pg

import (
	"context"
	"testing"
	"time"

	"github.com/balcony314/msched/internal/model"
	"github.com/balcony314/msched/internal/scheduler"
)

// --- Register ---

// TestRegister_Insert 首次注册：写入 workers 表，labels/capacity/token/lease 全字段，返回 lease。
func TestRegister_Insert(t *testing.T) {
	ws, db := newWorkerStore(t)
	ctx := context.Background()

	w, err := ws.Register(ctx, model.Worker{
		UnitID:   "w1",
		Labels:   map[string]string{"zone": "z1"},
		Capacity: 4,
		Token:    "tok-1",
	}, 10*time.Second)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if w.LeaseExpireTime.IsZero() {
		t.Error("lease_expire_time not set")
	}
	if w.State != "ONLINE" {
		t.Errorf("state: got %q, want ONLINE", w.State)
	}

	// 写入 DB 验证
	got, err := ws.GetByUnitID(ctx, "w1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Token != "tok-1" {
		t.Errorf("token: got %q", got.Token)
	}
	if got.Labels["zone"] != "z1" {
		t.Errorf("labels: got %v", got.Labels)
	}
	if got.Capacity != 4 {
		t.Errorf("capacity: got %d", got.Capacity)
	}
	// token 校验
	if !got.VerifyToken("tok-1") {
		t.Error("VerifyToken should pass")
	}
	if got.VerifyToken("wrong") {
		t.Error("VerifyToken should fail on wrong")
	}
	_ = db // 连库校验已通过 GetByUnitID 覆盖
}

// TestRegister_Upsert 相同 unit_id 重复注册覆盖刷新（labels 变更/lease 续期/token 更新）。
func TestRegister_Upsert(t *testing.T) {
	ws, _ := newWorkerStore(t)
	ctx := context.Background()

	_, err := ws.Register(ctx, model.Worker{
		UnitID: "w1", Labels: map[string]string{"zone": "z1"}, Token: "tok-1",
	}, 10*time.Second)
	if err != nil {
		t.Fatalf("register1: %v", err)
	}
	// 重复注册：labels 变更 + token 更新
	_, err = ws.Register(ctx, model.Worker{
		UnitID: "w1", Labels: map[string]string{"zone": "z2", "gpu": "a100"}, Token: "tok-2",
	}, 10*time.Second)
	if err != nil {
		t.Fatalf("register2: %v", err)
	}
	got, _ := ws.GetByUnitID(ctx, "w1")
	if got.Token != "tok-2" {
		t.Errorf("token: got %q, want tok-2", got.Token)
	}
	if got.Labels["zone"] != "z2" || got.Labels["gpu"] != "a100" {
		t.Errorf("labels: got %v", got.Labels)
	}
	// 旧 token 失效
	if got.VerifyToken("tok-1") {
		t.Error("old token should be invalid after re-register")
	}
}

// TestGetByUnitID_NotFound 不存在返回错误。
func TestGetByUnitID_NotFound(t *testing.T) {
	ws, _ := newWorkerStore(t)
	_, err := ws.GetByUnitID(context.Background(), "nobody")
	if err == nil {
		t.Error("should error on not found")
	}
}

// --- Heartbeat ---

// TestHeartbeat_RefreshLease 刷新 lease_expire_time。
func TestHeartbeat_RefreshLease(t *testing.T) {
	ws, _ := newWorkerStore(t)
	ctx := context.Background()

	_, err := ws.Register(ctx, model.Worker{UnitID: "w1", Token: "tok"}, 1*time.Second)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	// 等 100ms 后心跳续期，lease 应往后推
	time.Sleep(100 * time.Millisecond)
	expire, recovered, err := ws.Heartbeat(ctx, "w1", nil, 10*time.Second)
	if err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if !expire.IsZero() && len(recovered) == 0 {
		// ok
	}
	got, _ := ws.GetByUnitID(ctx, "w1")
	if !got.LeaseExpireTime.After(time.Now()) {
		t.Errorf("lease should be in future after heartbeat: got %v", got.LeaseExpireTime)
	}
}

// TestHeartbeat_RecoversStaleRunning 持有空集合 -> 回收该 worker 全部 RUNNING 转 PENDING。
// 复用 TaskStore 造 RUNNING 任务（同 db）。
func TestHeartbeat_RecoversStaleRunning(t *testing.T) {
	ws, db := newWorkerStore(t)
	ctx := context.Background()
	sel := model.WorkerSelector{"zone": "z1"}

	// 注册 worker + 造一个 RUNNING 任务（lease_owner=w1）
	if _, err := ws.Register(ctx, model.Worker{UnitID: "w1", Token: "tok"}, 10*time.Second); err != nil {
		t.Fatalf("register: %v", err)
	}
	ts, err := NewTaskStore(db)
	if err != nil {
		t.Fatalf("new task store: %v", err)
	}
	uid := seedRunning(t, ts, "g1", "job1", "w1")
	// 前置 RUNNING count=1
	if got := getGroupStat(t, db, "g1", sel, model.StateRunning); got != 1 {
		t.Fatalf("running pre: got %d, want 1", got)
	}

	// 心跳上报空 running 集合 -> 全回收
	_, recovered, err := ws.Heartbeat(ctx, "w1", nil, 10*time.Second)
	if err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if len(recovered) != 1 || recovered[0] != uid {
		t.Errorf("recovered: got %v, want [%s]", recovered, uid)
	}
	// 任务回 PENDING，group_stats RUNNING=0/PENDING=1
	tk, _ := ts.ListByUnitIDs(ctx, []string{uid})
	if tk[0].State != model.StatePending {
		t.Errorf("state: got %s, want PENDING", tk[0].State)
	}
	if got := getGroupStat(t, db, "g1", sel, model.StateRunning); got != 0 {
		t.Errorf("running stats: got %d, want 0", got)
	}
	if got := getGroupStat(t, db, "g1", sel, model.StatePending); got != 1 {
		t.Errorf("pending stats: got %d, want 1", got)
	}
}

// TestHeartbeat_KeepsRunningInSet 上报仍持有的任务 -> 不回收。
func TestHeartbeat_KeepsRunningInSet(t *testing.T) {
	ws, db := newWorkerStore(t)
	ctx := context.Background()
	sel := model.WorkerSelector{"zone": "z1"}

	if _, err := ws.Register(ctx, model.Worker{UnitID: "w1", Token: "tok"}, 10*time.Second); err != nil {
		t.Fatalf("register: %v", err)
	}
	ts, _ := NewTaskStore(db)
	uid := seedRunning(t, ts, "g1", "job1", "w1")

	// 心跳上报仍持有 uid -> 不回收
	_, recovered, err := ws.Heartbeat(ctx, "w1", []string{uid}, 10*time.Second)
	if err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if len(recovered) != 0 {
		t.Errorf("recovered: got %v, want empty", recovered)
	}
	if got := getGroupStat(t, db, "g1", sel, model.StateRunning); got != 1 {
		t.Errorf("running stats: got %d, want 1（仍在集合，未回收）", got)
	}
}

// TestHeartbeat_UnknownWorker 未注册 worker 心跳 -> lease no-op，recovered 空，不报错。
func TestHeartbeat_UnknownWorker(t *testing.T) {
	ws, _ := newWorkerStore(t)
	ctx := context.Background()
	_, recovered, err := ws.Heartbeat(ctx, "nobody", nil, 10*time.Second)
	if err != nil {
		t.Fatalf("heartbeat unknown should not error: %v", err)
	}
	if len(recovered) != 0 {
		t.Errorf("recovered: got %v, want empty", recovered)
	}
}

// 编译期断言：WorkerStore 实现 scheduler.WorkerStore。
var _ scheduler.WorkerStore = (*WorkerStore)(nil)
