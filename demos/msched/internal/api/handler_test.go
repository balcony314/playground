package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/balcony314/msched/internal/model"
	"github.com/balcony314/msched/internal/scheduler/fake"
	"github.com/gin-gonic/gin"
)

// newTestServer 构造挂 fake.Store 的测试 server。
func newTestServer(t *testing.T) (*Server, *fake.Store) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store := fake.New()
	return NewServer(store, ""), store
}

// doJSON 发请求并返回状态码 + 响应 JSON 解码结果。
func doJSON(t *testing.T, s *Server, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		bodyReader = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, bodyReader)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.engine.ServeHTTP(w, req)
	var resp map[string]any
	if w.Body.Len() > 0 {
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
	}
	return w.Code, resp
}

// TestCreateTask_Success 创建成功返回 201 + 算好的 unit_id。
func TestCreateTask_Success(t *testing.T) {
	s, store := newTestServer(t)
	args := base64.StdEncoding.EncodeToString([]byte(`{"job":"x"}`))
	code, resp := doJSON(t, s, http.MethodPost, "/api/v1/groups/g1/tasks", map[string]any{
		"args":              args,
		"worker_selector":   map[string]string{"zone": "z1"},
		"max_exec_duration": "30s",
	})
	if code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201, resp=%v", code, resp)
	}
	if resp["state"] != string(model.StatePending) {
		t.Errorf("state: got %v, want PENDING", resp["state"])
	}
	if resp["unit_id"] == "" {
		t.Error("unit_id empty")
	}
	// group_stats PENDING count=1
	if got := store.GroupStat("g1", model.WorkerSelector{"zone": "z1"}, model.StatePending); got != 1 {
		t.Errorf("group_stats pending: got %d, want 1", got)
	}
}

// TestCreateTask_Idempotent 相同 group+args 重复创建返回同 unit_id，group_stats 不重复计数。
func TestCreateTask_Idempotent(t *testing.T) {
	s, store := newTestServer(t)
	args := base64.StdEncoding.EncodeToString([]byte(`{"id":1}`))
	body := map[string]any{"args": args}
	code1, r1 := doJSON(t, s, http.MethodPost, "/api/v1/groups/g1/tasks", body)
	code2, r2 := doJSON(t, s, http.MethodPost, "/api/v1/groups/g1/tasks", body)
	if code1 != http.StatusCreated || code2 != http.StatusCreated {
		t.Fatalf("status: %d %d", code1, code2)
	}
	if r1["unit_id"] != r2["unit_id"] {
		t.Errorf("idempotent unit_id: %v vs %v", r1["unit_id"], r2["unit_id"])
	}
	if got := store.GroupStat("g1", nil, model.StatePending); got != 1 {
		t.Errorf("group_stats after dup create: got %d, want 1", got)
	}
}

// TestCreateTask_BadArgs 非 base64 args 返回 400。
func TestCreateTask_BadArgs(t *testing.T) {
	s, _ := newTestServer(t)
	code, _ := doJSON(t, s, http.MethodPost, "/api/v1/groups/g1/tasks", map[string]any{
		"args": "!!!not-base64!!!",
	})
	if code != http.StatusBadRequest {
		t.Errorf("bad args status: got %d, want 400", code)
	}
}

// TestListTasks_FiltersAndPaging 状态过滤 + 分页 + 排序（priority 升序）。
func TestListTasks_FiltersAndPaging(t *testing.T) {
	s, store := newTestServer(t)
	// 造 3 个 PENDING 不同 priority + 1 个 SCHEDULED
	ctx := context.Background()
	for _, tc := range []struct {
		args     string
		priority int64
		state    model.State
	}{
		{"a", 300, model.StatePending},
		{"b", 100, model.StatePending},
		{"c", 200, model.StatePending},
		{"d", 50, model.StateScheduled},
	} {
		in := model.Task{GroupUID: "g1", Args: []byte(tc.args), Priority: tc.priority}
		if tc.state == model.StateScheduled {
			in.State = model.StatePending
			tk, err := store.Create(ctx, in) //nolint:errcheck
			_ = tk
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			// 手动转 SCHEDULED 模拟已调度
			store.CASSchedule(ctx, tk.UnitID, "w1")
		} else {
			if _, err := store.Create(ctx, in); err != nil {
				t.Fatalf("create: %v", err)
			}
		}
	}

	// 不过滤：4 条
	code, resp := doJSON(t, s, http.MethodGet, "/api/v1/groups/g1/tasks?limit=10", nil)
	if code != http.StatusOK {
		t.Fatalf("list status: %d", code)
	}
	items := resp["items"].([]any)
	if len(items) != 4 {
		t.Errorf("all: got %d, want 4", len(items))
	}

	// 过滤 PENDING：3 条，按 priority 升序 -> b(100) c(200) a(300)
	code, resp = doJSON(t, s, http.MethodGet, "/api/v1/groups/g1/tasks?state=PENDING&limit=10", nil)
	items = resp["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("pending: got %d, want 3", len(items))
	}
	if items[0].(map[string]any)["priority"] != float64(100) {
		t.Errorf("sort: first priority got %v, want 100", items[0].(map[string]any)["priority"])
	}

	// 分页：limit=1 offset=0 -> 第一个（b, 100）
	code, resp = doJSON(t, s, http.MethodGet, "/api/v1/groups/g1/tasks?state=PENDING&limit=1&offset=0", nil)
	items = resp["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["priority"] != float64(100) {
		t.Errorf("page1: got %v", items)
	}

	// 过滤 SCHEDULED：1 条
	code, resp = doJSON(t, s, http.MethodGet, "/api/v1/groups/g1/tasks?state=SCHEDULED", nil)
	items = resp["items"].([]any)
	if len(items) != 1 {
		t.Errorf("scheduled: got %d, want 1", len(items))
	}
}

// TestListTasks_InvalidState 非法 state 返回 400。
func TestListTasks_InvalidState(t *testing.T) {
	s, _ := newTestServer(t)
	code, _ := doJSON(t, s, http.MethodGet, "/api/v1/groups/g1/tasks?state=BOGUS", nil)
	if code != http.StatusBadRequest {
		t.Errorf("invalid state: got %d, want 400", code)
	}
}

// TestGroupProgress 返回 group 下各 (selector, state) 计数。
func TestGroupProgress(t *testing.T) {
	s, store := newTestServer(t)
	ctx := context.Background()
	// g1: 2 PENDING（空 selector）+ 1 PENDING（zone=z1）
	for _, args := range []string{"a", "b"} {
		if _, err := store.Create(ctx, model.Task{GroupUID: "g1", Args: []byte(args)}); err != nil {
			t.Fatalf("create: %v", err)
		}
	}
	if _, err := store.Create(ctx, model.Task{
		GroupUID: "g1", Args: []byte("c"), WorkerSelector: model.WorkerSelector{"zone": "z1"},
	}); err != nil {
		t.Fatalf("create z1: %v", err)
	}
	// 转 1 个空 selector 的 PENDING -> SCHEDULED，进度应有变化
	u, _ := store.Create(ctx, model.Task{GroupUID: "g1", Args: []byte("a")})
	store.CASSchedule(ctx, u.UnitID, "w1")

	code, resp := doJSON(t, s, http.MethodGet, "/api/v1/groups/g1/progress", nil)
	if code != http.StatusOK {
		t.Fatalf("progress status: %d", code)
	}
	if resp["group_uid"] != "g1" {
		t.Errorf("group_uid: got %v", resp["group_uid"])
	}
	stats := resp["stats"].([]any)
	// 期望：空selector PENDING=1, SCHEDULED=1, z1 PENDING=1 -> 至少 3 行
	if len(stats) < 3 {
		t.Errorf("stats rows: got %d, want >=3", len(stats))
	}
}

// TestCreateTask_ValidatorNoArgs 缺 args 返回 400（binding required）。
func TestCreateTask_ValidatorNoArgs(t *testing.T) {
	s, _ := newTestServer(t)
	code, _ := doJSON(t, s, http.MethodPost, "/api/v1/groups/g1/tasks", map[string]any{})
	if code != http.StatusBadRequest {
		t.Errorf("no args: got %d, want 400", code)
	}
}

// --- deleteTasks ---

// seedTasks 在 g1 造若干任务，返回它们的 unit_id（供删除测试定位）。
func seedTasks(t *testing.T, store *fake.Store, n int) []string {
	t.Helper()
	ctx := context.Background()
	uids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		tk, err := store.Create(ctx, model.Task{GroupUID: "g1", Args: []byte{byte('a' + i)}})
		if err != nil {
			t.Fatalf("seed create: %v", err)
		}
		uids = append(uids, tk.UnitID)
	}
	return uids
}

// TestDeleteTasks_ByUnitIDs 删指定 unitID：返回删除数 + 仅删命中的，group_stats 减 count。
func TestDeleteTasks_ByUnitIDs(t *testing.T) {
	s, store := newTestServer(t)
	uids := seedTasks(t, store, 3)

	// 删前 2 个
	code, resp := doJSON(t, s, http.MethodDelete, "/api/v1/groups/g1/tasks", map[string]any{
		"unit_ids": uids[:2],
	})
	if code != http.StatusOK {
		t.Fatalf("status: got %d, want 200, resp=%v", code, resp)
	}
	if resp["deleted"] != float64(2) {
		t.Errorf("deleted: got %v, want 2", resp["deleted"])
	}
	// list 仅剩 1
	_, resp = doJSON(t, s, http.MethodGet, "/api/v1/groups/g1/tasks?limit=10", nil)
	if len(resp["items"].([]any)) != 1 {
		t.Errorf("remaining: got %v", resp["items"])
	}
	// group_stats PENDING=1
	if got := store.GroupStat("g1", nil, model.StatePending); got != 1 {
		t.Errorf("group_stats pending after delete: got %d, want 1", got)
	}
}

// TestDeleteTasks_All 删整个 group：删全部任务 + group_stats 该 group 行清空。
func TestDeleteTasks_All(t *testing.T) {
	s, store := newTestServer(t)
	_ = seedTasks(t, store, 3)
	// 另造一个 g2 任务验证跨 group 隔离
	ctx := context.Background()
	if _, err := store.Create(ctx, model.Task{GroupUID: "g2", Args: []byte("x")}); err != nil {
		t.Fatalf("create g2: %v", err)
	}

	code, resp := doJSON(t, s, http.MethodDelete, "/api/v1/groups/g1/tasks", map[string]any{"all": true})
	if code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", code)
	}
	if resp["deleted"] != float64(3) {
		t.Errorf("deleted: got %v, want 3", resp["deleted"])
	}
	// g1 全空
	_, resp = doJSON(t, s, http.MethodGet, "/api/v1/groups/g1/tasks?limit=10", nil)
	if len(resp["items"].([]any)) != 0 {
		t.Errorf("g1 remaining: got %v", resp["items"])
	}
	// group_stats g1 PENDING=0（行被删）
	if got := store.GroupStat("g1", nil, model.StatePending); got != 0 {
		t.Errorf("g1 stats after all-delete: got %d, want 0", got)
	}
	// g2 不受影响
	if got := store.GroupStat("g2", nil, model.StatePending); got != 1 {
		t.Errorf("g2 stats: got %d, want 1（跨 group 隔离）", got)
	}
}

// TestDeleteTasks_NoMode unit_ids 与 all 都不给返回 400。
func TestDeleteTasks_NoMode(t *testing.T) {
	s, _ := newTestServer(t)
	code, _ := doJSON(t, s, http.MethodDelete, "/api/v1/groups/g1/tasks", map[string]any{})
	if code != http.StatusBadRequest {
		t.Errorf("no mode: got %d, want 400", code)
	}
}

// TestDeleteTasks_BothMode unit_ids 与 all 同时给返回 400（互斥）。
func TestDeleteTasks_BothMode(t *testing.T) {
	s, _ := newTestServer(t)
	code, _ := doJSON(t, s, http.MethodDelete, "/api/v1/groups/g1/tasks", map[string]any{
		"unit_ids": []string{"u1"},
		"all":      true,
	})
	if code != http.StatusBadRequest {
		t.Errorf("both mode: got %d, want 400", code)
	}
}

// TestDeleteTasks_NonExistent 删不存在的 unitID：deleted=0，不报错。
func TestDeleteTasks_NonExistent(t *testing.T) {
	s, _ := newTestServer(t)
	code, resp := doJSON(t, s, http.MethodDelete, "/api/v1/groups/g1/tasks", map[string]any{
		"unit_ids": []string{"nonexistent"},
	})
	if code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", code)
	}
	if resp["deleted"] != float64(0) {
		t.Errorf("deleted: got %v, want 0", resp["deleted"])
	}
}
