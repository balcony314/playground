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

// 端到端冒烟：走真实 HTTP 端口（httptest.NewServer 真实 TCP）+ 真实 http.Client，
// 验证 gin 路由 / JSON 编解码往返 / 状态码 / 错误响应体整条链路，
// 与 handler_test.go（httptest.NewRecorder 驱动）互补。

// newE2EServer 起真实 HTTP 服务（OS 分配端口），返回 base URL + fake store。
func newE2EServer(t *testing.T) (string, *fake.Store) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store := fake.New()
	s := NewServer(store, "")
	ts := httptest.NewServer(s.engine)
	t.Cleanup(ts.Close)
	return ts.URL, store
}

// doHTTP 用真实 http.Client 打请求，返回状态码 + 解码后的 JSON + 原始响应体。
func doHTTP(t *testing.T, base, method, path string, body any) (int, map[string]any, string) {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, base+path, r)
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do req: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return resp.StatusCode, m, string(raw)
}

// TestE2E_CreateListProgressDelete 覆盖四个端点的 happy path，全链路真实 HTTP。
func TestE2E_CreateListProgressDelete(t *testing.T) {
	base, store := newE2EServer(t)
	ctx := context.Background()

	// 1. 创建任务 -> 201 + unit_id + state=PENDING
	args := base64.StdEncoding.EncodeToString([]byte(`{"job":"e2e"}`))
	code, resp, raw := doHTTP(t, base, http.MethodPost, "/api/v1/groups/g1/tasks", map[string]any{
		"args":              args,
		"worker_selector":   map[string]string{"zone": "z1"},
		"max_exec_duration": "30s",
	})
	if code != http.StatusCreated {
		t.Fatalf("create: got %d, want 201, body=%s", code, raw)
	}
	uid, _ := resp["unit_id"].(string)
	if uid == "" {
		t.Fatalf("create: empty unit_id, body=%s", raw)
	}
	if resp["state"] != string(model.StatePending) {
		t.Fatalf("create: state got %v, want PENDING", resp["state"])
	}
	t.Logf("create -> 201 unit_id=%s state=%v", uid, resp["state"])

	// 再造 2 个凑列表（不同 args -> 不同 unit_id）
	for _, a := range []string{`{"i":2}`, `{"i":3}`} {
		if _, err := store.Create(ctx, model.Task{GroupUID: "g1", Args: []byte(a)}); err != nil { //nolint:errcheck
			t.Fatalf("seed create: %v", err)
		}
	}

	// 2. 列表（过滤 PENDING） -> 200 + 3 条
	code, resp, raw = doHTTP(t, base, http.MethodGet, "/api/v1/groups/g1/tasks?state=PENDING&limit=10", nil)
	if code != http.StatusOK {
		t.Fatalf("list: got %d, want 200, body=%s", code, raw)
	}
	items, _ := resp["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("list: got %d items, want 3, body=%s", len(items), raw)
	}
	t.Logf("list -> 200 items=%d", len(items))

	// 3. group 进度 -> 200 + stats 非空
	code, resp, raw = doHTTP(t, base, http.MethodGet, "/api/v1/groups/g1/progress", nil)
	if code != http.StatusOK {
		t.Fatalf("progress: got %d, want 200, body=%s", code, raw)
	}
	if resp["group_uid"] != "g1" {
		t.Fatalf("progress: group_uid got %v, want g1", resp["group_uid"])
	}
	stats, _ := resp["stats"].([]any)
	if len(stats) == 0 {
		t.Fatalf("progress: empty stats, body=%s", raw)
	}
	t.Logf("progress -> 200 stats rows=%d", len(stats))

	// 4. 删除首个 -> 200 + deleted=1
	code, resp, raw = doHTTP(t, base, http.MethodDelete, "/api/v1/groups/g1/tasks", map[string]any{
		"unit_ids": []string{uid},
	})
	if code != http.StatusOK {
		t.Fatalf("delete: got %d, want 200, body=%s", code, raw)
	}
	if resp["deleted"] != float64(1) {
		t.Fatalf("delete: deleted got %v, want 1, body=%s", resp["deleted"], raw)
	}
	t.Logf("delete -> 200 deleted=%v", resp["deleted"])

	// 删后列表少 1
	_, resp, _ = doHTTP(t, base, http.MethodGet, "/api/v1/groups/g1/tasks?limit=10", nil)
	items, _ = resp["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("after delete: got %d items, want 2", len(items))
	}
}

// TestE2E_RoutingErrors 路由/校验错误：未知路径 404，DELETE 无模式 400。
func TestE2E_RoutingErrors(t *testing.T) {
	base, _ := newE2EServer(t)

	// 未知路径 -> 404（gin 默认 404）
	code, _, _ := doHTTP(t, base, http.MethodGet, "/api/v1/nope", nil)
	if code != http.StatusNotFound {
		t.Errorf("unknown path: got %d, want 404", code)
	}

	// DELETE 不给模式 -> 400 + error 字段
	code, resp, raw := doHTTP(t, base, http.MethodDelete, "/api/v1/groups/g1/tasks", map[string]any{})
	if code != http.StatusBadRequest {
		t.Errorf("no mode: got %d, want 400", code)
	}
	if _, ok := resp["error"]; !ok {
		t.Errorf("no mode: error field missing, body=%s", raw)
	}
}
