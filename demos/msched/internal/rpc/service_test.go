package rpc

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/balcony314/msched/internal/model"
	"github.com/balcony314/msched/internal/scheduler"
	"github.com/balcony314/msched/internal/scheduler/fake"
	pb "github.com/balcony314/msched/proto/msched/worker/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

const (
	testSecret   = "bootstrap-secret"
	testToken    = "worker-token"
	testLeaseTTL = 10 * time.Second
)

// newTestServer 起 bufconn gRPC + fake store，返回 client + store。
func newTestServer(t *testing.T, maxAttempt int) (pb.WorkerServiceClient, *fake.Store) {
	t.Helper()
	store := fake.New()
	dispatcher := scheduler.NewDispatcher(store, store)
	srv := NewServer("", Deps{
		Dispatcher:     dispatcher,
		TaskStore:      store,
		WorkerStore:    store,
		RegisterSecret: testSecret,
		TaskMaxAttempt: maxAttempt,
		WorkerLeaseTTL: testLeaseTTL,
	})
	lis := bufconn.Listen(1024 * 1024)
	go func() { _ = srv.server.Serve(lis) }()
	t.Cleanup(srv.server.Stop)
	dialer := func(context.Context, string) (net.Conn, error) { return lis.Dial() }
	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithContextDialer(dialer), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return pb.NewWorkerServiceClient(conn), store
}

// withAuth 附加 authorization metadata。secret 用于 Register。
func withAuth(token string) metadata.MD {
	return metadata.Pairs(mdAuthorization, token)
}

func withAuthSecret(token, secret string) metadata.MD {
	return metadata.Pairs(mdAuthorization, token, mdRegisterSecret, secret)
}

// callCtx 带 token 的 context。
func callCtx(token string) context.Context {
	return metadata.NewOutgoingContext(context.Background(), withAuth(token))
}

func callCtxSecret(token, secret string) context.Context {
	return metadata.NewOutgoingContext(context.Background(), withAuthSecret(token, secret))
}

// statusOK 判断 gRPC 错误是否为 nil 或 OK。
func statusIs(err error, want codes.Code) bool {
	if err == nil {
		return want == codes.OK
	}
	st, ok := status.FromError(err)
	return ok && st.Code() == want
}

// seedWorkerAndTask 注册 worker 并造一个派发到该 worker 的任务（PENDING->SCHEDULED->入队）。
// 返回 unit_id。各 Report 测试再显式 BatchSetRunning 转 RUNNING。
func seedWorkerAndTask(t *testing.T, store *fake.Store, wuid, args string) string {
	t.Helper()
	ctx := context.Background()
	// 注册 worker
	if _, err := store.Register(ctx, model.Worker{UnitID: wuid, Token: testToken}, testLeaseTTL); err != nil {
		t.Fatalf("register: %v", err)
	}
	// 造任务
	tk, err := store.Create(ctx, model.Task{GroupUID: "g1", Args: []byte(args), MaxExecDuration: 30 * time.Second})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// CAS SCHEDULED + 入派发队列
	if _, err := store.CASSchedule(ctx, tk.UnitID, wuid); err != nil {
		t.Fatalf("cas: %v", err)
	}
	if err := store.Push(ctx, wuid, "g1", tk.UnitID, 1); err != nil {
		t.Fatalf("push: %v", err)
	}
	return tk.UnitID
}

// --- Register ---

func TestRegister_Success(t *testing.T) {
	c, store := newTestServer(t, 5)
	resp, err := c.Register(callCtxSecret(testToken, testSecret), &pb.RegisterRequest{
		UnitId: "w1", Labels: map[string]string{"zone": "z1"}, Capacity: 4,
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if resp.GetLeaseExpireTime() == nil {
		t.Fatal("lease_expire_time nil")
	}
	w, err := store.GetByUnitID(context.Background(), "w1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if w.Token != testToken {
		t.Errorf("token: got %q, want %q", w.Token, testToken)
	}
	if w.Labels["zone"] != "z1" {
		t.Errorf("labels: got %v", w.Labels)
	}
}

func TestRegister_WrongSecret(t *testing.T) {
	c, _ := newTestServer(t, 5)
	_, err := c.Register(callCtxSecret(testToken, "wrong"), &pb.RegisterRequest{UnitId: "w1"})
	if !statusIs(err, codes.Unauthenticated) {
		t.Errorf("wrong secret: got %v, want Unauthenticated", err)
	}
}

func TestRegister_NoSecret(t *testing.T) {
	c, _ := newTestServer(t, 5)
	_, err := c.Register(callCtx(testToken), &pb.RegisterRequest{UnitId: "w1"})
	if !statusIs(err, codes.Unauthenticated) {
		t.Errorf("no secret: got %v, want Unauthenticated", err)
	}
}

func TestRegister_NoToken(t *testing.T) {
	c, _ := newTestServer(t, 5)
	_, err := c.Register(callCtxSecret("", testSecret), &pb.RegisterRequest{UnitId: "w1"})
	if !statusIs(err, codes.Unauthenticated) {
		t.Errorf("no token: got %v, want Unauthenticated", err)
	}
}

// --- Pull ---

func TestPull_Success(t *testing.T) {
	c, store := newTestServer(t, 5)
	uid := seedWorkerAndTask(t, store, "w1", "job1")

	resp, err := c.Pull(callCtx(testToken), &pb.PullRequest{UnitId: "w1", Batch: 10})
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if len(resp.GetTasks()) != 1 {
		t.Fatalf("tasks: got %d, want 1", len(resp.GetTasks()))
	}
	pt := resp.GetTasks()[0]
	if pt.GetUnitId() != uid {
		t.Errorf("unit_id: got %s, want %s", pt.GetUnitId(), uid)
	}
	if pt.GetGroupUid() != "g1" {
		t.Errorf("group: got %s", pt.GetGroupUid())
	}
	if pt.GetMaxExecDuration() == nil {
		t.Error("max_exec_duration nil")
	}
}

func TestPull_EmptyNonBlocking(t *testing.T) {
	c, store := newTestServer(t, 5)
	// 注册但无派发任务 -> 空批立即返回（非阻塞，DESIGN §1.12）
	if _, err := store.Register(context.Background(), model.Worker{UnitID: "w1", Token: testToken}, testLeaseTTL); err != nil {
		t.Fatalf("register: %v", err)
	}
	resp, err := c.Pull(callCtx(testToken), &pb.PullRequest{UnitId: "w1", Batch: 10})
	if err != nil {
		t.Fatalf("pull empty: %v", err)
	}
	if len(resp.GetTasks()) != 0 {
		t.Errorf("empty pull: got %d, want 0", len(resp.GetTasks()))
	}
}

func TestPull_BadToken(t *testing.T) {
	c, store := newTestServer(t, 5)
	if _, err := store.Register(context.Background(), model.Worker{UnitID: "w1", Token: testToken}, testLeaseTTL); err != nil {
		t.Fatalf("register: %v", err)
	}
	_, err := c.Pull(callCtx("wrong-token"), &pb.PullRequest{UnitId: "w1"})
	if !statusIs(err, codes.Unauthenticated) {
		t.Errorf("bad token: got %v, want Unauthenticated", err)
	}
}

func TestPull_UnknownWorker(t *testing.T) {
	c, _ := newTestServer(t, 5)
	_, err := c.Pull(callCtx(testToken), &pb.PullRequest{UnitId: "nobody"})
	if !statusIs(err, codes.Unauthenticated) {
		t.Errorf("unknown worker: got %v, want Unauthenticated", err)
	}
}

// --- Report ---

func TestReport_Succeeded(t *testing.T) {
	c, store := newTestServer(t, 5)
	uid := seedWorkerAndTask(t, store, "w1", "job1")
	// Pull 拉走转 RUNNING
	if _, err := store.BatchSetRunning(context.Background(), "w1", []string{uid}); err != nil {
		t.Fatalf("batch set running: %v", err)
	}

	_, err := c.Report(callCtx(testToken), &pb.ReportRequest{
		WorkerUnitId: "w1", UnitId: uid, Result: pb.ExecResult_EXEC_RESULT_SUCCEEDED,
		Payload: []byte("done"),
	})
	if err != nil {
		t.Fatalf("report succeeded: %v", err)
	}
	// 任务应 COMPLETED + Result 写入
	tk, ok := store.Task(uid)
	if !ok {
		t.Fatal("task gone")
	}
	if tk.State != model.StateCompleted {
		t.Errorf("state: got %s, want COMPLETED", tk.State)
	}
	if string(tk.Result) != "done" {
		t.Errorf("result: got %q", tk.Result)
	}
}

func TestReport_Failed_Retry(t *testing.T) {
	c, store := newTestServer(t, 5)
	uid := seedWorkerAndTask(t, store, "w1", "job1")
	if _, err := store.BatchSetRunning(context.Background(), "w1", []string{uid}); err != nil {
		t.Fatalf("batch set running: %v", err)
	}
	// dispatch_count=1 < maxAttempt=5 -> 回 PENDING
	_, err := c.Report(callCtx(testToken), &pb.ReportRequest{
		WorkerUnitId: "w1", UnitId: uid, Result: pb.ExecResult_EXEC_RESULT_FAILED, Error: "boom",
	})
	if err != nil {
		t.Fatalf("report failed: %v", err)
	}
	tk, _ := store.Task(uid)
	if tk.State != model.StatePending {
		t.Errorf("state: got %s, want PENDING", tk.State)
	}
}

func TestReport_Failed_DiscardOverLimit(t *testing.T) {
	c, store := newTestServer(t, 1) // maxAttempt=1，dispatch_count 已=1 -> DISCARDED
	uid := seedWorkerAndTask(t, store, "w1", "job1")
	if _, err := store.BatchSetRunning(context.Background(), "w1", []string{uid}); err != nil {
		t.Fatalf("batch set running: %v", err)
	}
	_, err := c.Report(callCtx(testToken), &pb.ReportRequest{
		WorkerUnitId: "w1", UnitId: uid, Result: pb.ExecResult_EXEC_RESULT_FAILED,
	})
	if err != nil {
		t.Fatalf("report failed: %v", err)
	}
	tk, _ := store.Task(uid)
	if tk.State != model.StateDiscarded {
		t.Errorf("state: got %s, want DISCARDED", tk.State)
	}
}

func TestReport_OwnershipMismatch_Ignored(t *testing.T) {
	c, store := newTestServer(t, 5)
	uid := seedWorkerAndTask(t, store, "w1", "job1")
	if _, err := store.BatchSetRunning(context.Background(), "w1", []string{uid}); err != nil {
		t.Fatalf("batch set running: %v", err)
	}
	// 用 w2 身份上报 w1 持有的任务 -> 所有权不符，Report 忽略（仍 OK）
	if _, err := store.Register(context.Background(), model.Worker{UnitID: "w2", Token: testToken}, testLeaseTTL); err != nil {
		t.Fatalf("register w2: %v", err)
	}
	_, err := c.Report(callCtx(testToken), &pb.ReportRequest{
		WorkerUnitId: "w2", UnitId: uid, Result: pb.ExecResult_EXEC_RESULT_SUCCEEDED,
	})
	if err != nil {
		t.Errorf("ownership mismatch should be ignored: %v", err)
	}
	// 状态不变（仍 RUNNING，未被他节点转 COMPLETED）
	tk, _ := store.Task(uid)
	if tk.State != model.StateRunning {
		t.Errorf("state: got %s, want RUNNING（所有权不符忽略）", tk.State)
	}
}

// --- Heartbeat ---

func TestHeartbeat_RefreshLease(t *testing.T) {
	c, store := newTestServer(t, 5)
	if _, err := store.Register(context.Background(), model.Worker{UnitID: "w1", Token: testToken}, testLeaseTTL); err != nil {
		t.Fatalf("register: %v", err)
	}
	resp, err := c.Heartbeat(callCtx(testToken), &pb.HeartbeatRequest{UnitId: "w1"})
	if err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if resp.GetLeaseExpireTime() == nil {
		t.Fatal("lease_expire_time nil")
	}
}

func TestHeartbeat_RecoversStaleRunning(t *testing.T) {
	c, store := newTestServer(t, 5)
	uid := seedWorkerAndTask(t, store, "w1", "job1")
	if _, err := store.BatchSetRunning(context.Background(), "w1", []string{uid}); err != nil {
		t.Fatalf("batch set running: %v", err)
	}
	// 心跳上报空 running 集合 -> 该 worker 全部 RUNNING 被回收
	resp, err := c.Heartbeat(callCtx(testToken), &pb.HeartbeatRequest{UnitId: "w1"})
	if err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if len(resp.GetRecoveredUnitIds()) != 1 || resp.GetRecoveredUnitIds()[0] != uid {
		t.Errorf("recovered: got %v, want [%s]", resp.GetRecoveredUnitIds(), uid)
	}
	// 任务回 PENDING
	tk, _ := store.Task(uid)
	if tk.State != model.StatePending {
		t.Errorf("state: got %s, want PENDING（回收）", tk.State)
	}
}

func TestHeartbeat_KeepsRunningInSet(t *testing.T) {
	c, store := newTestServer(t, 5)
	uid := seedWorkerAndTask(t, store, "w1", "job1")
	if _, err := store.BatchSetRunning(context.Background(), "w1", []string{uid}); err != nil {
		t.Fatalf("batch set running: %v", err)
	}
	// 心跳上报仍持有该任务 -> 不回收
	resp, err := c.Heartbeat(callCtx(testToken), &pb.HeartbeatRequest{UnitId: "w1", RunningUnitIds: []string{uid}})
	if err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if len(resp.GetRecoveredUnitIds()) != 0 {
		t.Errorf("recovered: got %v, want empty", resp.GetRecoveredUnitIds())
	}
	tk, _ := store.Task(uid)
	if tk.State != model.StateRunning {
		t.Errorf("state: got %s, want RUNNING（仍在集合，不回收）", tk.State)
	}
}

// --- 输入边界校验 ---

// TestPull_BatchCapped batch 超上限截断到 maxPullBatch，不报错。
func TestPull_BatchCapped(t *testing.T) {
	c, store := newTestServer(t, 5)
	uid := seedWorkerAndTask(t, store, "w1", "job1")
	// batch=1000000 应被截断，仍正常拉取
	resp, err := c.Pull(callCtx(testToken), &pb.PullRequest{UnitId: "w1", Batch: 1000000})
	if err != nil {
		t.Fatalf("pull capped: %v", err)
	}
	if len(resp.GetTasks()) != 1 || resp.GetTasks()[0].GetUnitId() != uid {
		t.Errorf("tasks: got %v, want [%s]", resp.GetTasks(), uid)
	}
}

// TestReport_PayloadTooLarge payload 超 64KB 返回 InvalidArgument。
func TestReport_PayloadTooLarge(t *testing.T) {
	c, store := newTestServer(t, 5)
	uid := seedWorkerAndTask(t, store, "w1", "job1")
	if _, err := store.BatchSetRunning(context.Background(), "w1", []string{uid}); err != nil {
		t.Fatalf("batch set running: %v", err)
	}
	big := make([]byte, 64*1024+1)
	_, err := c.Report(callCtx(testToken), &pb.ReportRequest{
		WorkerUnitId: "w1", UnitId: uid, Result: pb.ExecResult_EXEC_RESULT_SUCCEEDED, Payload: big,
	})
	if !statusIs(err, codes.InvalidArgument) {
		t.Errorf("payload too large: got %v, want InvalidArgument", err)
	}
}

// TestRegister_NegativeCapacity capacity 负值返回 InvalidArgument。
func TestRegister_NegativeCapacity(t *testing.T) {
	c, _ := newTestServer(t, 5)
	_, err := c.Register(callCtxSecret(testToken, testSecret), &pb.RegisterRequest{
		UnitId: "w1", Capacity: -1,
	})
	if !statusIs(err, codes.InvalidArgument) {
		t.Errorf("negative capacity: got %v, want InvalidArgument", err)
	}
}

// --- 鉴权缓存 ---

// TestAuthCache_HitSkipsDB 连续两次 Pull：第二次命中缓存，GetByUnitID 仅调用 1 次。
func TestAuthCache_HitSkipsDB(t *testing.T) {
	c, store := newTestServer(t, 5)
	uid := seedWorkerAndTask(t, store, "w1", "job1")
	// 第一次 Pull -> 鉴权走 DB（GetByUnitID=1）
	if _, err := c.Pull(callCtx(testToken), &pb.PullRequest{UnitId: "w1", Batch: 1}); err != nil {
		t.Fatalf("pull1: %v", err)
	}
	after1 := store.GetByUnitIDCount()
	if after1 != 1 {
		t.Fatalf("after pull1 GetByUnitID: got %d, want 1", after1)
	}
	// 第二次 Pull 同 worker+token -> 命中缓存，GetByUnitID 仍 1
	if _, err := c.Pull(callCtx(testToken), &pb.PullRequest{UnitId: "w1", Batch: 1}); err != nil {
		t.Fatalf("pull2: %v", err)
	}
	after2 := store.GetByUnitIDCount()
	if after2 != 1 {
		t.Errorf("after pull2 GetByUnitID: got %d, want 1（缓存命中跳过 DB）", after2)
	}
	// 确保缓存不影响正确性：仍能拉到任务（已在第一次拉走，第二次空批）
	_ = uid
}
