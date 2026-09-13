package scheduler_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/balcony314/msched/internal/model"
	"github.com/balcony314/msched/internal/scheduler/fake"
)

// future 返回一个未来时间（退避用，精确值不影响断言）。
func future() time.Time { return time.Now().Add(time.Minute) }

// TestGroupStats_CreatePending 验证 AddTask 创建 PENDING 任务后 group_stats PENDING 计数 +1（DESIGN §3）。
func TestGroupStats_CreatePending(t *testing.T) {
	store := fake.New()
	store.AddTask(model.Task{UnitID: "u1", GroupUID: "g1", State: model.StatePending})

	if got := store.GroupStat("g1", nil, model.StatePending); got != 1 {
		t.Fatalf("g1 pending = %d, want 1", got)
	}
	// 不同 group 不受影响。
	if got := store.GroupStat("g2", nil, model.StatePending); got != 0 {
		t.Fatalf("g2 pending = %d, want 0", got)
	}
}

// TestGroupStats_SelectorDimension 验证不同 selector 分别计数（DESIGN §3 维度隔离）。
func TestGroupStats_SelectorDimension(t *testing.T) {
	store := fake.New()
	gpu := model.WorkerSelector{"gpu": "v100"}
	cpu := model.WorkerSelector{"cpu": "x86"}

	store.AddTask(model.Task{UnitID: "u1", GroupUID: "g1", State: model.StatePending, WorkerSelector: gpu})
	store.AddTask(model.Task{UnitID: "u2", GroupUID: "g1", State: model.StatePending, WorkerSelector: gpu})
	store.AddTask(model.Task{UnitID: "u3", GroupUID: "g1", State: model.StatePending, WorkerSelector: cpu})

	if got := store.GroupStat("g1", gpu, model.StatePending); got != 2 {
		t.Fatalf("g1/gpu pending = %d, want 2", got)
	}
	if got := store.GroupStat("g1", cpu, model.StatePending); got != 1 {
		t.Fatalf("g1/cpu pending = %d, want 1", got)
	}
}

// TestGroupStats_StateTransition 验证状态转换时计数流转（DESIGN §3 实时维护）：
// PENDING --CAS--> SCHEDULED --BatchSetRunning--> RUNNING，各态计数此消彼长。
func TestGroupStats_StateTransition(t *testing.T) {
	store := fake.New()
	store.AddTask(model.Task{UnitID: "u1", GroupUID: "g1", State: model.StatePending})
	store.AddTask(model.Task{UnitID: "u2", GroupUID: "g1", State: model.StatePending})

	// CAS 抢走 u1：PENDING -1，SCHEDULED +1。
	ok, err := store.CASSchedule(context.Background(), "u1", "w1")
	if err != nil || !ok {
		t.Fatalf("CAS u1: ok=%v err=%v", ok, err)
	}
	if got := store.GroupStat("g1", nil, model.StatePending); got != 1 {
		t.Fatalf("after CAS, pending = %d, want 1", got)
	}
	if got := store.GroupStat("g1", nil, model.StateScheduled); got != 1 {
		t.Fatalf("after CAS, scheduled = %d, want 1", got)
	}

	// u1 拉走：SCHEDULED -1，RUNNING +1（u1 已 CAS 给 w1，lease_owner=w1）。
	if _, err := store.BatchSetRunning(context.Background(), "w1", []string{"u1"}); err != nil {
		t.Fatalf("batch set running: %v", err)
	}
	if got := store.GroupStat("g1", nil, model.StateScheduled); got != 0 {
		t.Fatalf("after pull, scheduled = %d, want 0", got)
	}
	if got := store.GroupStat("g1", nil, model.StateRunning); got != 1 {
		t.Fatalf("after pull, running = %d, want 1", got)
	}
	// u2 仍 PENDING。
	if got := store.GroupStat("g1", nil, model.StatePending); got != 1 {
		t.Fatalf("u2 still pending = %d, want 1", got)
	}
}

// TestGroupStats_Backoff 验证 SetBackoff 退避计数流转（DESIGN §8.6/§3）。
// PENDING --SetBackoff--> BACKOFF；BACKOFF --CAS--> SCHEDULED。
func TestGroupStats_Backoff(t *testing.T) {
	store := fake.New()
	store.AddTask(model.Task{UnitID: "u1", GroupUID: "g1", State: model.StatePending})

	// 退避：PENDING -1，BACKOFF +1。
	if err := store.SetBackoff(context.Background(), "u1", future()); err != nil {
		t.Fatalf("set backoff: %v", err)
	}
	if got := store.GroupStat("g1", nil, model.StatePending); got != 0 {
		t.Fatalf("after backoff, pending = %d, want 0", got)
	}
	if got := store.GroupStat("g1", nil, model.StateBackoff); got != 1 {
		t.Fatalf("after backoff, backoff = %d, want 1", got)
	}

	// BACKOFF -> SCHEDULED：BACKOFF -1，SCHEDULED +1。
	ok, err := store.CASSchedule(context.Background(), "u1", "w1")
	if err != nil || !ok {
		t.Fatalf("CAS from BACKOFF: ok=%v err=%v", ok, err)
	}
	if got := store.GroupStat("g1", nil, model.StateBackoff); got != 0 {
		t.Fatalf("after CAS from BACKOFF, backoff = %d, want 0", got)
	}
	if got := store.GroupStat("g1", nil, model.StateScheduled); got != 1 {
		t.Fatalf("after CAS from BACKOFF, scheduled = %d, want 1", got)
	}
}

// TestGroupStats_ListGroupStats 验证 ListGroupStats 返回某 group 全部非零计数行。
func TestGroupStats_ListGroupStats(t *testing.T) {
	store := fake.New()
	gpu := model.WorkerSelector{"gpu": "v100"}
	store.AddTask(model.Task{UnitID: "u1", GroupUID: "g1", State: model.StatePending, WorkerSelector: gpu})
	store.AddTask(model.Task{UnitID: "u2", GroupUID: "g1", State: model.StatePending, WorkerSelector: gpu})
	store.AddTask(model.Task{UnitID: "u3", GroupUID: "g1", State: model.StatePending})
	// u1 转 SCHEDULED。
	if _, err := store.CASSchedule(context.Background(), "u1", "w1"); err != nil {
		t.Fatalf("CAS: %v", err)
	}

	stats, err := store.ListGroupStats(context.Background(), "g1")
	if err != nil {
		t.Fatalf("list group stats: %v", err)
	}
	// 期望：g1 下 gpu 维度 PENDING=1 + SCHEDULED=1，空 selector 维度 PENDING=1，共 3 行。
	want := map[string]int64{}
	gpuHash := model.ComputeSelectorHash(gpu)
	emptyHash := model.ComputeSelectorHash(nil)
	want[string(model.StatePending)+"|"+gpuHash] = 1
	want[string(model.StateScheduled)+"|"+gpuHash] = 1
	want[string(model.StatePending)+"|"+emptyHash] = 1
	// Selectors 为 selector_hash 的可读伴随（"k=v" 按 key 排序），查询时直接看懂维度。
	wantSelectors := map[string][]string{
		string(model.StatePending) + "|" + gpuHash:   {"gpu=v100"},
		string(model.StateScheduled) + "|" + gpuHash: {"gpu=v100"},
		string(model.StatePending) + "|" + emptyHash: {},
	}
	if len(stats) != len(want) {
		t.Fatalf("stats rows = %d, want %d: %+v", len(stats), len(want), stats)
	}
	for _, st := range stats {
		k := string(st.State) + "|" + st.SelectorHash
		if st.Count != want[k] {
			t.Errorf("stats %s count = %d, want %d", k, st.Count, want[k])
		}
		if ws, ok := wantSelectors[k]; ok && !slices.Equal(st.Selectors, ws) {
			t.Errorf("stats %s selectors = %v, want %v", k, st.Selectors, ws)
		}
	}
}
