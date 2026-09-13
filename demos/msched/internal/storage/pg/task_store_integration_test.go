//go:build integration

package pg

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/balcony314/msched/internal/model"
	"gorm.io/gorm"
)

// insertTask 直接 INSERT 一个 task（测试造数据用，绕过 store）。
// SelectorHash 自动算；WorkerSelector 靠 GORM 模型 Value() 序列化为 JSONB。
func insertTask(t *testing.T, db *gorm.DB, task model.Task) {
	t.Helper()
	if task.SelectorHash == "" {
		task.SelectorHash = model.ComputeSelectorHash(task.WorkerSelector)
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("insert task %s: %v", task.UnitID, err)
	}
}

// insertWorker 直接 INSERT 一个 worker（测试造数据用，绕过 store）。
// Labels 靠 GORM 模型 Value() 序列化为 JSONB。
func insertWorker(t *testing.T, db *gorm.DB, unitID string, labels map[string]string, leaseExpire time.Time) {
	t.Helper()
	w := model.Worker{
		UnitID:          unitID,
		Labels:          model.WorkerSelector(labels),
		LeaseExpireTime: leaseExpire,
		State:           "ONLINE",
	}
	if err := db.Create(&w).Error; err != nil {
		t.Fatalf("insert worker %s: %v", unitID, err)
	}
}

// getGroupStat 直接查 group_stats 某行 count（断言用）。
func getGroupStat(t *testing.T, db *gorm.DB, groupUID string, selector model.WorkerSelector, state model.State) int64 {
	t.Helper()
	hash := model.ComputeSelectorHash(selector)
	var count int64
	err := db.Raw(
		`SELECT count FROM group_stats WHERE group_uid=? AND selector_hash=? AND state=?`,
		groupUID, hash, string(state),
	).Scan(&count).Error
	if err != nil {
		t.Fatalf("query group_stats: %v", err)
	}
	return count
}

// newTaskStore 连库建 schema + 清表，返回 TaskStore。
func newTaskStore(t *testing.T) (*TaskStore, *gorm.DB) {
	t.Helper()
	db := connectTestDB(t)
	cleanTables(t, db)
	ts, err := NewTaskStore(db)
	if err != nil {
		t.Fatalf("new task store: %v", err)
	}
	return ts, db
}

// newWorkerStore 连库建 schema + 清表，返回 WorkerStore（与 TaskStore 共享同一 db）。
func newWorkerStore(t *testing.T) (*WorkerStore, *gorm.DB) {
	t.Helper()
	db := connectTestDB(t)
	cleanTables(t, db)
	ws, err := NewWorkerStore(db)
	if err != nil {
		t.Fatalf("new worker store: %v", err)
	}
	return ws, db
}

// nowUTC 测试用固定 now 基准（避免本地时区干扰）。
func nowUTC() time.Time { return time.Now().UTC() }

// sampleTask 构造测试用 task（PENDING）。
func sampleTask(unitID, group string, priority int64, sel model.WorkerSelector) model.Task {
	return model.Task{
		UnitID:         unitID,
		GroupUID:       group,
		Priority:       priority,
		State:          model.StatePending,
		WorkerSelector: sel,
		Args:           []byte("{}"),
	}
}

// --- ListActiveGroups / ListPendByGroup ---

func TestListActiveGroupsAndPendByGroup(t *testing.T) {
	ts, db := newTaskStore(t)
	ctx := context.Background()

	sel := model.WorkerSelector{"zone": "z1"}
	// g1 有 2 个 PENDING；g2 有 1 个 PENDING；g3 有 1 个 BACKOFF 未到期；g4 有 1 个到期 BACKOFF。
	insertTask(t, db, sampleTask("u1", "g1", 200, sel))
	insertTask(t, db, sampleTask("u2", "g1", 100, sel)) // g1 priority 100 在前
	insertTask(t, db, sampleTask("u3", "g2", 50, sel))

	backoffUnexpired := sampleTask("u4", "g3", 50, sel)
	backoffUnexpired.State = model.StateBackoff
	future := nowUTC().Add(time.Hour)
	backoffUnexpired.NextRetryTime = &future
	insertTask(t, db, backoffUnexpired)

	backoffExpired := sampleTask("u5", "g4", 50, sel)
	backoffExpired.State = model.StateBackoff
	past := nowUTC().Add(-time.Minute)
	backoffExpired.NextRetryTime = &past
	insertTask(t, db, backoffExpired)

	groups, err := ts.ListActiveGroups(ctx)
	if err != nil {
		t.Fatalf("list active groups: %v", err)
	}
	wantGroups := []string{"g1", "g2", "g4"} // g3 未到期不算
	if fmt.Sprint(groups) != fmt.Sprint(wantGroups) {
		t.Errorf("active groups: got %v, want %v", groups, wantGroups)
	}

	// g1 按 priority 升序取 1 个 -> u2（priority 100）
	pend, err := ts.ListPendByGroup(ctx, "g1", 1)
	if err != nil {
		t.Fatalf("list pend: %v", err)
	}
	if len(pend) != 1 || pend[0].UnitID != "u2" {
		t.Errorf("g1 top by priority: got %+v, want u2", pend)
	}

	// g1 取全部（n=10）-> 2 个
	pend, err = ts.ListPendByGroup(ctx, "g1", 10)
	if err != nil {
		t.Fatalf("list pend all: %v", err)
	}
	if len(pend) != 2 {
		t.Errorf("g1 all: got %d, want 2", len(pend))
	}
}

// --- CASSchedule + group_stats ---

func TestCASScheduleAndGroupStats(t *testing.T) {
	ts, db := newTaskStore(t)
	ctx := context.Background()

	sel := model.WorkerSelector{"zone": "z1"}
	insertTask(t, db, sampleTask("u1", "g1", 100, sel))

	// 初始 group_stats 应为空（未初始化）。
	// CAS 抢到后：PENDING count-1（到 -1，因为没初始化为 1）、SCHEDULED count+1（新建=1）。
	ok, err := ts.CASSchedule(ctx, "u1", "w1")
	if err != nil {
		t.Fatalf("cas: %v", err)
	}
	if !ok {
		t.Fatal("CAS should succeed on PENDING task")
	}

	scheduledCount := getGroupStat(t, db, "g1", sel, model.StateScheduled)
	if scheduledCount != 1 {
		t.Errorf("SCHEDULED count after CAS: got %d, want 1", scheduledCount)
	}

	// 再次 CAS 应失败（已 SCHEDULED，不在 PENDING/BACKOFF）。
	ok, err = ts.CASSchedule(ctx, "u1", "w2")
	if err != nil {
		t.Fatalf("cas 2nd: %v", err)
	}
	if ok {
		t.Error("CAS should fail on already-SCHEDULED task")
	}

	// 不存在的 task CAS 返回 false。
	ok, err = ts.CASSchedule(ctx, "nonexistent", "w1")
	if err != nil {
		t.Fatalf("cas nonexistent: %v", err)
	}
	if ok {
		t.Error("CAS on nonexistent should return false")
	}
}

// TestCASScheduleFromBackoff 从 BACKOFF（到期）CAS 抢到。
func TestCASScheduleFromBackoff(t *testing.T) {
	ts, db := newTaskStore(t)
	ctx := context.Background()

	sel := model.WorkerSelector{"zone": "z1"}
	task := sampleTask("u1", "g1", 100, sel)
	task.State = model.StateBackoff
	past := nowUTC().Add(-time.Minute)
	task.NextRetryTime = &past
	insertTask(t, db, task)

	// 先 SetBackoff 会把它从 BACKOFF->BACKOFF（old=new 不调整 count）。
	// 这里直接测 CAS 从 BACKOFF -> SCHEDULED。
	ok, err := ts.CASSchedule(ctx, "u1", "w1")
	if err != nil {
		t.Fatalf("cas from backoff: %v", err)
	}
	if !ok {
		t.Fatal("CAS should succeed from BACKOFF")
	}
	scheduledCount := getGroupStat(t, db, "g1", sel, model.StateScheduled)
	if scheduledCount != 1 {
		t.Errorf("SCHEDULED count: got %d, want 1", scheduledCount)
	}
}

// TestSetBackoffAndGroupStats 退避：PENDING -> BACKOFF，group_stats 调整。
func TestSetBackoffAndGroupStats(t *testing.T) {
	ts, db := newTaskStore(t)
	ctx := context.Background()

	sel := model.WorkerSelector{"zone": "z1"}
	insertTask(t, db, sampleTask("u1", "g1", 100, sel))

	nextRetry := nowUTC().Add(30 * time.Second)
	if err := ts.SetBackoff(ctx, "u1", nextRetry); err != nil {
		t.Fatalf("set backoff: %v", err)
	}

	backoffCount := getGroupStat(t, db, "g1", sel, model.StateBackoff)
	if backoffCount != 1 {
		t.Errorf("BACKOFF count: got %d, want 1", backoffCount)
	}

	// task 状态应为 BACKOFF，fail_count=1。
	var row model.Task
	if err := db.Where("unit_id=?", "u1").First(&row).Error; err != nil {
		t.Fatalf("query task: %v", err)
	}
	if row.State != model.StateBackoff {
		t.Errorf("state: got %s, want BACKOFF", row.State)
	}
	if row.FailCount != 1 {
		t.Errorf("fail_count: got %d, want 1", row.FailCount)
	}

	// 再次 SetBackoff（BACKOFF->BACKOFF）：count 不变（old=new），fail_count++。
	if err := ts.SetBackoff(ctx, "u1", nextRetry.Add(time.Minute)); err != nil {
		t.Fatalf("set backoff 2nd: %v", err)
	}
	if getGroupStat(t, db, "g1", sel, model.StateBackoff) != 1 {
		t.Error("BACKOFF count should stay 1 on re-backoff")
	}
	if err := db.Where("unit_id=?", "u1").First(&row).Error; err != nil {
		t.Fatalf("query task: %v", err)
	}
	if row.FailCount != 2 {
		t.Errorf("fail_count: got %d, want 2", row.FailCount)
	}
}

// --- BatchSetRunning + 所有权校验 ---

func TestBatchSetRunningOwnership(t *testing.T) {
	ts, db := newTaskStore(t)
	ctx := context.Background()

	sel := model.WorkerSelector{"zone": "z1"}
	// u1 lease=w1 SCHEDULED；u2 lease=w2 SCHEDULED；u3 lease=w1 但 state=PENDING（不该转）。
	t1 := sampleTask("u1", "g1", 100, sel)
	t1.State = model.StateScheduled
	t1.LeaseOwner = "w1"
	insertTask(t, db, t1)

	t2 := sampleTask("u2", "g1", 100, sel)
	t2.State = model.StateScheduled
	t2.LeaseOwner = "w2"
	insertTask(t, db, t2)

	t3 := sampleTask("u3", "g1", 100, sel)
	t3.State = model.StatePending
	t3.LeaseOwner = "w1"
	insertTask(t, db, t3)

	// w1 拉 [u1, u2, u3]：只 u1 命中（lease=w1 AND state=SCHEDULED）。
	running, err := ts.BatchSetRunning(ctx, "w1", []string{"u1", "u2", "u3"})
	if err != nil {
		t.Fatalf("batch set running: %v", err)
	}
	if len(running) != 1 || running[0] != "u1" {
		t.Errorf("running: got %v, want [u1]", running)
	}

	// group_stats: SCHEDULED count（g1）应从初始 2 减到 1（u1 转 RUNNING）。
	// 注意：insertTask 没初始化 group_stats，但 CAS 会。这里直接验 RUNNING count=1。
	runningCount := getGroupStat(t, db, "g1", sel, model.StateRunning)
	if runningCount != 1 {
		t.Errorf("RUNNING count: got %d, want 1", runningCount)
	}

	// u1 应有 pull_time + timeout（非零）。
	var row model.Task
	if err := db.Where("unit_id=?", "u1").First(&row).Error; err != nil {
		t.Fatalf("query u1: %v", err)
	}
	if row.PullTime.IsZero() {
		t.Error("u1 pull_time should be set")
	}
	if row.Timeout.IsZero() {
		t.Error("u1 timeout should be set")
	}
	// updated_at 应被 UPDATE 维护（非零，且 >= created_at，证明 INSERT 后被 BatchSetRunning 刷新）。
	if row.UpdatedAt.IsZero() {
		t.Error("u1 updated_at should be maintained on UPDATE")
	}
	if row.UpdatedAt.Before(row.CreatedAt) {
		t.Errorf("updated_at %v before created_at %v", row.UpdatedAt, row.CreatedAt)
	}
}

// --- ListByUnitIDs / ListGroupStats ---

func TestListByUnitIDsAndGroupStats(t *testing.T) {
	ts, db := newTaskStore(t)
	ctx := context.Background()

	sel := model.WorkerSelector{"zone": "z1"}
	insertTask(t, db, sampleTask("u1", "g1", 100, sel))
	insertTask(t, db, sampleTask("u2", "g1", 200, sel))

	// CAS 两个到 SCHEDULED。
	_, _ = ts.CASSchedule(ctx, "u1", "w1")
	_, _ = ts.CASSchedule(ctx, "u2", "w1")

	// ListByUnitIDs 读全列。
	tasks, err := ts.ListByUnitIDs(ctx, []string{"u1", "u2"})
	if err != nil {
		t.Fatalf("list by unit ids: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("got %d, want 2", len(tasks))
	}
	if string(tasks[0].Args) != "{}" {
		t.Errorf("args should be read: got %q", tasks[0].Args)
	}

	// ListGroupStats: g1 有 2 个 SCHEDULED（u1, u2）。
	stats, err := ts.ListGroupStats(ctx, "g1")
	if err != nil {
		t.Fatalf("list group stats: %v", err)
	}
	var scheduledCount int64
	for _, s := range stats {
		if s.State == model.StateScheduled {
			scheduledCount = s.Count
		}
		if len(s.Selectors) == 0 {
			t.Errorf("Selectors should be non-empty for hash %s", s.SelectorHash)
		}
		// Selectors 应含 "zone=z1"。
		found := false
		for _, p := range s.Selectors {
			if p == "zone=z1" {
				found = true
			}
		}
		if !found {
			t.Errorf("Selectors should contain zone=z1: got %v", s.Selectors)
		}
	}
	if scheduledCount != 2 {
		t.Errorf("SCHEDULED count in group stats: got %d, want 2", scheduledCount)
	}
}

// --- WorkerRegistry ---

func TestWorkerRegistryMatch(t *testing.T) {
	_, db := newTaskStore(t)
	ctx := context.Background()

	future := nowUTC().Add(time.Hour)
	past := nowUTC().Add(-time.Minute)

	// w1 labels={zone:z1, gpu:a100} 在线；w2 labels={zone:z1} 在线；
	// w3 labels={zone:z1} 离线（lease 过期）。
	insertWorker(t, db, "w1", map[string]string{"zone": "z1", "gpu": "a100"}, future)
	insertWorker(t, db, "w2", map[string]string{"zone": "z1"}, future)
	insertWorker(t, db, "w3", map[string]string{"zone": "z1"}, past)

	rg := NewWorkerRegistry(db, 50*time.Millisecond, nil)
	if err := rg.Start(ctx); err != nil {
		t.Fatalf("start registry: %v", err)
	}
	defer rg.Stop()

	// selector={zone:z1} -> 匹配 w1, w2（w3 离线不算）。
	wuids, err := rg.Match(ctx, model.WorkerSelector{"zone": "z1"})
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if len(wuids) != 2 {
		t.Errorf("match zone=z1: got %v, want [w1 w2]", wuids)
	}

	// selector={zone:z1, gpu:a100} -> 只匹配 w1。
	wuids, err = rg.Match(ctx, model.WorkerSelector{"zone": "z1", "gpu": "a100"})
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if len(wuids) != 1 || wuids[0] != "w1" {
		t.Errorf("match gpu: got %v, want [w1]", wuids)
	}

	// 空 selector -> 匹配所有在线（w1, w2）。
	wuids, err = rg.Match(ctx, model.WorkerSelector{})
	if err != nil {
		t.Fatalf("match empty: %v", err)
	}
	if len(wuids) != 2 {
		t.Errorf("match empty: got %v, want [w1 w2]", wuids)
	}
}

// TestWorkerRegistryRefresh worker 上下线后缓存刷新可见。
func TestWorkerRegistryRefresh(t *testing.T) {
	_, db := newTaskStore(t)
	ctx := context.Background()

	rg := NewWorkerRegistry(db, 50*time.Millisecond, nil)
	if err := rg.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer rg.Stop()

	// 初始无 worker。
	wuids, _ := rg.Match(ctx, model.WorkerSelector{})
	if len(wuids) != 0 {
		t.Errorf("initial should be empty: got %v", wuids)
	}

	// 插入一个 worker，等刷新后可见。
	future := nowUTC().Add(time.Hour)
	insertWorker(t, db, "w1", map[string]string{"zone": "z1"}, future)
	time.Sleep(150 * time.Millisecond) // 等至少一轮刷新

	wuids, _ = rg.Match(ctx, model.WorkerSelector{})
	if len(wuids) != 1 || wuids[0] != "w1" {
		t.Errorf("after refresh should see w1: got %v", wuids)
	}
}

// TestWorkerRegistryDoubleStart 双重 Start 不 panic：第二次 no-op，Stop 正常退出。
// 回归缺陷：修复前第二次 Start 会再 spawn loop，Stop 时双 close(doneCh) panic。
func TestWorkerRegistryDoubleStart(t *testing.T) {
	_, db := newTaskStore(t)
	ctx := context.Background()

	rg := NewWorkerRegistry(db, 50*time.Millisecond, nil)
	if err := rg.Start(ctx); err != nil {
		t.Fatalf("first start: %v", err)
	}
	// 第二次 Start 应 no-op，不 panic、不 spawn 第二个 loop。
	if err := rg.Start(ctx); err != nil {
		t.Fatalf("second start: %v", err)
	}
	rg.Stop() // 不应 panic（避免 close of closed channel）
}

// TestWorkerRegistryStopBeforeStart 未 Start 即 Stop 不阻塞。
// 回归缺陷：修复前 Stop 会 <-doneCh 永久阻塞（无 loop 生产者关闭 doneCh）。
func TestWorkerRegistryStopBeforeStart(t *testing.T) {
	_, db := newTaskStore(t)
	rg := NewWorkerRegistry(db, 50*time.Millisecond, nil)

	done := make(chan struct{})
	go func() {
		rg.Stop() // 无 loop 时 doneCh 无生产者，不应阻塞
		close(done)
	}()
	select {
	case <-done:
		// 通过：未阻塞
	case <-time.After(2 * time.Second):
		t.Fatal("Stop before Start blocked forever")
	}
}

// --- Create / ListByGroup ---

// TestCreate_Initial 首次创建：补算 unit_id/selector_hash/priority，state=PENDING，group_stats +1。
func TestCreate_Initial(t *testing.T) {
	ts, db := newTaskStore(t)
	ctx := context.Background()

	created, err := ts.Create(ctx, model.Task{
		GroupUID:        "g1",
		Args:            []byte(`{"job":"x"}`),
		WorkerSelector:  model.WorkerSelector{"zone": "z1"},
		MaxExecDuration: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.UnitID == "" {
		t.Error("unit_id not computed")
	}
	if created.SelectorHash == "" {
		t.Error("selector_hash not computed")
	}
	if created.Priority <= 0 {
		t.Error("priority not computed")
	}
	if created.State != model.StatePending {
		t.Errorf("state: got %s, want PENDING", created.State)
	}
	// group_stats PENDING=1
	if got := getGroupStat(t, db, "g1", model.WorkerSelector{"zone": "z1"}, model.StatePending); got != 1 {
		t.Errorf("group_stats pending: got %d, want 1", got)
	}
}

// TestCreate_Idempotent 相同 group+args 重复创建返回同 unit_id，group_stats 不重复计数。
func TestCreate_Idempotent(t *testing.T) {
	ts, db := newTaskStore(t)
	ctx := context.Background()

	t1, err := ts.Create(ctx, model.Task{GroupUID: "g1", Args: []byte(`{"id":1}`)})
	if err != nil {
		t.Fatalf("create1: %v", err)
	}
	t2, err := ts.Create(ctx, model.Task{GroupUID: "g1", Args: []byte(`{"id":1}`)})
	if err != nil {
		t.Fatalf("create2: %v", err)
	}
	if t1.UnitID != t2.UnitID {
		t.Errorf("idempotent unit_id: %s vs %s", t1.UnitID, t2.UnitID)
	}
	// 仍只 +1 一次
	if got := getGroupStat(t, db, "g1", nil, model.StatePending); got != 1 {
		t.Errorf("group_stats after dup: got %d, want 1", got)
	}
}

// TestListByGroup_FiltersPagingSorting 状态过滤 + 分页 + 排序。
func TestListByGroup_FiltersPagingSorting(t *testing.T) {
	ts, db := newTaskStore(t)
	ctx := context.Background()
	sel := model.WorkerSelector{"zone": "z1"}

	// g1: 3 PENDING（priority 300/100/200）+ 1 SCHEDULED
	insertTask(t, db, sampleTask("u1", "g1", 300, sel))
	insertTask(t, db, sampleTask("u2", "g1", 100, sel))
	insertTask(t, db, sampleTask("u3", "g1", 200, sel))
	sched := sampleTask("u4", "g1", 50, sel)
	sched.State = model.StateScheduled
	insertTask(t, db, sched)
	// g2: 1 个，验证跨 group 隔离
	insertTask(t, db, sampleTask("u5", "g2", 1, sel))

	// 不过滤：g1 全 4 条
	all, err := ts.ListByGroup(ctx, "g1", nil, 100, 0)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 4 {
		t.Errorf("all g1: got %d, want 4", len(all))
	}

	// 过滤 PENDING：3 条，按 priority 升序 -> u2(100) u3(200) u1(300)
	pend, err := ts.ListByGroup(ctx, "g1", []model.State{model.StatePending}, 100, 0)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(pend) != 3 {
		t.Fatalf("pending: got %d, want 3", len(pend))
	}
	want := []string{"u2", "u3", "u1"}
	for i, w := range want {
		if pend[i].UnitID != w {
			t.Errorf("sort[%d]: got %s, want %s", i, pend[i].UnitID, w)
		}
	}

	// 分页：limit=2 offset=0 -> 前 2（u2,u3）；offset=2 -> 后 1（u1）
	p1, _ := ts.ListByGroup(ctx, "g1", []model.State{model.StatePending}, 2, 0)
	if len(p1) != 2 || p1[0].UnitID != "u2" || p1[1].UnitID != "u3" {
		t.Errorf("page1: got %+v", p1)
	}
	p2, _ := ts.ListByGroup(ctx, "g1", []model.State{model.StatePending}, 2, 2)
	if len(p2) != 1 || p2[0].UnitID != "u1" {
		t.Errorf("page2: got %+v", p2)
	}

	// 过滤 SCHEDULED：1 条
	sc, _ := ts.ListByGroup(ctx, "g1", []model.State{model.StateScheduled}, 100, 0)
	if len(sc) != 1 || sc[0].UnitID != "u4" {
		t.Errorf("scheduled: got %+v", sc)
	}

	// 跨 group 隔离：g2 仅 1 条
	g2, _ := ts.ListByGroup(ctx, "g2", nil, 100, 0)
	if len(g2) != 1 {
		t.Errorf("g2: got %d, want 1", len(g2))
	}
}

// --- Delete ---

// TestDelete_ByUnitIDs 删指定 unitID：仅删命中的，group_stats 聚合减 count，未命中不动。
// 用 ts.Create 建任务以初始化 group_stats 行（符合生产行为，insertTask 不建 stats 行）。
func TestDelete_ByUnitIDs(t *testing.T) {
	ts, db := newTaskStore(t)
	ctx := context.Background()
	sel := model.WorkerSelector{"zone": "z1"}

	t1, _ := ts.Create(ctx, model.Task{GroupUID: "g1", Args: []byte("a"), WorkerSelector: sel})
	t2, _ := ts.Create(ctx, model.Task{GroupUID: "g1", Args: []byte("b"), WorkerSelector: sel})
	t3, _ := ts.Create(ctx, model.Task{GroupUID: "g1", Args: []byte("c"), WorkerSelector: sel})
	// g2 任务验证跨 group 隔离
	_, _ = ts.Create(ctx, model.Task{GroupUID: "g2", Args: []byte("x"), WorkerSelector: sel})

	// g1 初始 PENDING=3
	if got := getGroupStat(t, db, "g1", sel, model.StatePending); got != 3 {
		t.Fatalf("g1 pre stats: got %d, want 3", got)
	}

	// 删 t1 + t2（同时含不存在的 fake，应忽略）
	n, err := ts.Delete(ctx, "g1", []string{t1.UnitID, t2.UnitID, "u_fake"})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if n != 2 {
		t.Errorf("deleted: got %d, want 2", n)
	}
	// g1 剩 t3
	left, _ := ts.ListByGroup(ctx, "g1", nil, 100, 0)
	if len(left) != 1 || left[0].UnitID != t3.UnitID {
		t.Errorf("g1 remaining: got %+v", left)
	}
	// group_stats PENDING=1（3 减 2）
	if got := getGroupStat(t, db, "g1", sel, model.StatePending); got != 1 {
		t.Errorf("g1 stats: got %d, want 1", got)
	}
	// g2 不受影响
	if got := getGroupStat(t, db, "g2", sel, model.StatePending); got != 1 {
		t.Errorf("g2 stats: got %d, want 1", got)
	}
	// g2 的 unit 传 g1 不命中（跨 group 隔离）
	tg2, _ := ts.Create(ctx, model.Task{GroupUID: "g2", Args: []byte("y"), WorkerSelector: sel})
	n, _ = ts.Delete(ctx, "g1", []string{tg2.UnitID})
	if n != 0 {
		t.Errorf("cross-group delete: got %d, want 0", n)
	}
}

// TestDelete_All 删整个 group：删全部任务 + group_stats 该 group 行清空，跨 group 隔离。
func TestDelete_All(t *testing.T) {
	ts, db := newTaskStore(t)
	ctx := context.Background()
	sel := model.WorkerSelector{"zone": "z1"}

	_, _ = ts.Create(ctx, model.Task{GroupUID: "g1", Args: []byte("a"), WorkerSelector: sel})
	_, _ = ts.Create(ctx, model.Task{GroupUID: "g1", Args: []byte("b"), WorkerSelector: sel})
	_, _ = ts.Create(ctx, model.Task{GroupUID: "g2", Args: []byte("x"), WorkerSelector: sel})
	// g1 进度应有 PENDING=2
	if got := getGroupStat(t, db, "g1", sel, model.StatePending); got != 2 {
		t.Fatalf("g1 pre stats: got %d, want 2", got)
	}

	n, err := ts.Delete(ctx, "g1", nil) // unitIDs 空 = 删整个 group
	if err != nil {
		t.Fatalf("delete all: %v", err)
	}
	if n != 2 {
		t.Errorf("deleted: got %d, want 2", n)
	}
	// g1 空
	left, _ := ts.ListByGroup(ctx, "g1", nil, 100, 0)
	if len(left) != 0 {
		t.Errorf("g1 remaining: got %d", len(left))
	}
	// group_stats g1 PENDING 行已删（0 行）
	if got := getGroupStat(t, db, "g1", sel, model.StatePending); got != 0 {
		t.Errorf("g1 stats after all-delete: got %d, want 0", got)
	}
	// g2 不受影响
	if got := getGroupStat(t, db, "g2", sel, model.StatePending); got != 1 {
		t.Errorf("g2 stats: got %d, want 1", got)
	}
}

// TestDelete_MultiState 不同状态任务的 group_stats 聚合减 count 正确（PENDING + SCHEDULED 混合）。
func TestDelete_MultiState(t *testing.T) {
	ts, db := newTaskStore(t)
	ctx := context.Background()
	sel := model.WorkerSelector{"zone": "z1"}

	// g1: u1 PENDING, u2 SCHEDULED（不同状态，group_stats 不同行）
	u1, _ := ts.Create(ctx, model.Task{GroupUID: "g1", Args: []byte("a"), WorkerSelector: sel})
	u2, _ := ts.Create(ctx, model.Task{GroupUID: "g1", Args: []byte("b"), WorkerSelector: sel})
	// u2 转 SCHEDULED
	if ok, _ := ts.CASSchedule(ctx, u2.UnitID, "w1"); !ok {
		t.Fatal("CAS u2 should succeed")
	}
	// 此时 g1: PENDING=1, SCHEDULED=1
	if got := getGroupStat(t, db, "g1", sel, model.StatePending); got != 1 {
		t.Fatalf("pending pre: got %d, want 1", got)
	}
	if got := getGroupStat(t, db, "g1", sel, model.StateScheduled); got != 1 {
		t.Fatalf("scheduled pre: got %d, want 1", got)
	}

	n, err := ts.Delete(ctx, "g1", []string{u1.UnitID, u2.UnitID})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if n != 2 {
		t.Errorf("deleted: got %d, want 2", n)
	}
	// PENDING / SCHEDULED 行 count 均降到 0
	if got := getGroupStat(t, db, "g1", sel, model.StatePending); got != 0 {
		t.Errorf("pending stats: got %d, want 0", got)
	}
	if got := getGroupStat(t, db, "g1", sel, model.StateScheduled); got != 0 {
		t.Errorf("scheduled stats: got %d, want 0", got)
	}
}

// --- Complete / ReportFail ---

// seedRunning 把一个 task 推到 RUNNING（lease_owner=wuid），返回 unit_id。
// 用 ts.Create 建 group_stats 行 -> CAS SCHEDULED -> BatchSetRunning。
func seedRunning(t *testing.T, ts *TaskStore, group, args, wuid string) string {
	t.Helper()
	ctx := context.Background()
	tk, err := ts.Create(ctx, model.Task{
		GroupUID: group, Args: []byte(args),
		WorkerSelector:  model.WorkerSelector{"zone": "z1"},
		MaxExecDuration: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if ok, err := ts.CASSchedule(ctx, tk.UnitID, wuid); err != nil || !ok {
		t.Fatalf("cas: ok=%v err=%v", ok, err)
	}
	if _, err := ts.BatchSetRunning(ctx, wuid, []string{tk.UnitID}); err != nil {
		t.Fatalf("batch set running: %v", err)
	}
	return tk.UnitID
}

// TestComplete_Success RUNNING->COMPLETED，写 Result，group_stats RUNNING-1/COMPLETED+1。
func TestComplete_Success(t *testing.T) {
	ts, db := newTaskStore(t)
	ctx := context.Background()
	sel := model.WorkerSelector{"zone": "z1"}

	uid := seedRunning(t, ts, "g1", "job1", "w1")
	// 前置：RUNNING count=1
	if got := getGroupStat(t, db, "g1", sel, model.StateRunning); got != 1 {
		t.Fatalf("running pre: got %d, want 1", got)
	}

	ok, err := ts.Complete(ctx, uid, "w1", []byte("done"))
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if !ok {
		t.Fatal("complete should hit")
	}
	// 状态 COMPLETED + Result 写入
	tk, err := ts.ListByUnitIDs(ctx, []string{uid})
	if err != nil || len(tk) != 1 {
		t.Fatalf("list: err=%v len=%d", err, len(tk))
	}
	if tk[0].State != model.StateCompleted {
		t.Errorf("state: got %s, want COMPLETED", tk[0].State)
	}
	if string(tk[0].Result) != "done" {
		t.Errorf("result: got %q", tk[0].Result)
	}
	// group_stats: RUNNING=0, COMPLETED=1
	if got := getGroupStat(t, db, "g1", sel, model.StateRunning); got != 0 {
		t.Errorf("running stats: got %d, want 0", got)
	}
	if got := getGroupStat(t, db, "g1", sel, model.StateCompleted); got != 1 {
		t.Errorf("completed stats: got %d, want 1", got)
	}
}

// TestComplete_OwnershipMismatch 所有权不符（lease_owner≠wuid）-> 返回 false，状态不变（§8.7）。
func TestComplete_OwnershipMismatch(t *testing.T) {
	ts, db := newTaskStore(t)
	ctx := context.Background()
	sel := model.WorkerSelector{"zone": "z1"}

	uid := seedRunning(t, ts, "g1", "job1", "w1")
	// w2 上报 w1 持有的任务
	ok, err := ts.Complete(ctx, uid, "w2", []byte("x"))
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if ok {
		t.Error("complete should miss on ownership mismatch")
	}
	// 状态仍 RUNNING，RUNNING count 不变
	tk, _ := ts.ListByUnitIDs(ctx, []string{uid})
	if tk[0].State != model.StateRunning {
		t.Errorf("state: got %s, want RUNNING（忽略）", tk[0].State)
	}
	if got := getGroupStat(t, db, "g1", sel, model.StateRunning); got != 1 {
		t.Errorf("running stats: got %d, want 1（未变）", got)
	}
}

// TestComplete_AlreadyTerminal 非 RUNNING（已 COMPLETED）-> 返回 false（忽略）。
func TestComplete_AlreadyTerminal(t *testing.T) {
	ts, _ := newTaskStore(t)
	ctx := context.Background()
	uid := seedRunning(t, ts, "g1", "job1", "w1")
	if _, err := ts.Complete(ctx, uid, "w1", []byte("done")); err != nil {
		t.Fatalf("complete1: %v", err)
	}
	// 再次 Complete -> false
	ok, err := ts.Complete(ctx, uid, "w1", []byte("dup"))
	if err != nil {
		t.Fatalf("complete2: %v", err)
	}
	if ok {
		t.Error("second complete should miss")
	}
}

// TestReportFail_Retry FAILED 且 dispatch_count<maxAttempt -> 回 PENDING（不入 BACKOFF，§8.6）。
func TestReportFail_Retry(t *testing.T) {
	ts, db := newTaskStore(t)
	ctx := context.Background()
	sel := model.WorkerSelector{"zone": "z1"}

	uid := seedRunning(t, ts, "g1", "job1", "w1")
	reported, discarded, err := ts.ReportFail(ctx, uid, "w1", 5)
	if err != nil {
		t.Fatalf("report fail: %v", err)
	}
	if !reported || discarded {
		t.Errorf("reported=%v discarded=%v, want true/false", reported, discarded)
	}
	tk, _ := ts.ListByUnitIDs(ctx, []string{uid})
	if tk[0].State != model.StatePending {
		t.Errorf("state: got %s, want PENDING", tk[0].State)
	}
	// group_stats: RUNNING=0, PENDING=1
	if got := getGroupStat(t, db, "g1", sel, model.StateRunning); got != 0 {
		t.Errorf("running stats: got %d, want 0", got)
	}
	if got := getGroupStat(t, db, "g1", sel, model.StatePending); got != 1 {
		t.Errorf("pending stats: got %d, want 1", got)
	}
}

// TestReportFail_Discard dispatch_count>=maxAttempt -> DISCARDED（§8.5）。
func TestReportFail_Discard(t *testing.T) {
	ts, db := newTaskStore(t)
	ctx := context.Background()
	sel := model.WorkerSelector{"zone": "z1"}

	uid := seedRunning(t, ts, "g1", "job1", "w1")
	// dispatch_count 经 CAS 已=1，maxAttempt=1 -> 丢弃
	reported, discarded, err := ts.ReportFail(ctx, uid, "w1", 1)
	if err != nil {
		t.Fatalf("report fail: %v", err)
	}
	if !reported || !discarded {
		t.Errorf("reported=%v discarded=%v, want true/true", reported, discarded)
	}
	tk, _ := ts.ListByUnitIDs(ctx, []string{uid})
	if tk[0].State != model.StateDiscarded {
		t.Errorf("state: got %s, want DISCARDED", tk[0].State)
	}
	if got := getGroupStat(t, db, "g1", sel, model.StateDiscarded); got != 1 {
		t.Errorf("discarded stats: got %d, want 1", got)
	}
}

// TestReportFail_OwnershipMismatch 所有权不符 -> reported=false，状态不变。
func TestReportFail_OwnershipMismatch(t *testing.T) {
	ts, db := newTaskStore(t)
	ctx := context.Background()
	sel := model.WorkerSelector{"zone": "z1"}

	uid := seedRunning(t, ts, "g1", "job1", "w1")
	reported, _, err := ts.ReportFail(ctx, uid, "w2", 5)
	if err != nil {
		t.Fatalf("report fail: %v", err)
	}
	if reported {
		t.Error("should miss on ownership mismatch")
	}
	if got := getGroupStat(t, db, "g1", sel, model.StateRunning); got != 1 {
		t.Errorf("running stats: got %d, want 1（未变）", got)
	}
}
