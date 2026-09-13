package rpc

import (
	"context"
	"time"

	"github.com/balcony314/msched/internal/model"
	"github.com/balcony314/msched/internal/scheduler"
	pb "github.com/balcony314/msched/proto/msched/worker/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// workerServiceServer 实现 pb.WorkerServiceServer（DESIGN §10）。
//
// 持有 dispatcher（Pull 热路径）/taskStore（Report Complete/ReportFail + Heartbeat 续期查 timeout）/
// workerStore（Register/Heartbeat/GetByUnitID）+ taskHB/workerHB（Redis 心跳 zset 维护）。
// Register 额外 bootstrap secret 校验；Pull/Heartbeat/Report 走 authWorker。
//
// zset 维护在 RPC 层组合（保持 pg/redis 包纯净）：Pull 转 RUNNING 时 taskHB.RenewMany、
// Heartbeat 续期 runningUnitIds + 删 recovered、Report 删 task、Register/Heartbeat 续 worker。
// zset 失败忽略（Redis 抖动偶发），靠 DB CAS + zset TTL 过期扫描器兜底（误回收重派无害）。
type workerServiceServer struct {
	pb.UnimplementedWorkerServiceServer

	dispatcher       *scheduler.Dispatcher
	taskStore        scheduler.TaskStore
	workerStore      scheduler.WorkerStore
	taskHB           scheduler.TaskHeartbeat   // task 活性 zset（Pull/Heartbeat/Report 维护）
	workerHB         scheduler.WorkerHeartbeat // worker 在线 zset（Register/Heartbeat 续期）
	registerSecret   string                    // Register bootstrap secret（metadata 校验）
	taskMaxAttempt   int                       // Report FAILED 兜底上限（§8.5）
	workerLeaseTTL   time.Duration             // Register/Heartbeat lease 有效期
	taskHeartbeatTTL time.Duration             // task 心跳续期有效期（cap 硬截止 timeout）
}

// Deps 构造 workerServiceServer 的依赖（避免长参数列表）。
type Deps struct {
	Dispatcher       *scheduler.Dispatcher
	TaskStore        scheduler.TaskStore
	WorkerStore      scheduler.WorkerStore
	TaskHB           scheduler.TaskHeartbeat
	WorkerHB         scheduler.WorkerHeartbeat
	RegisterSecret   string
	TaskMaxAttempt   int
	WorkerLeaseTTL   time.Duration
	TaskHeartbeatTTL time.Duration
}

// newWorkerService 构造 workerServiceServer。
func newWorkerService(d Deps) *workerServiceServer {
	return &workerServiceServer{
		dispatcher:       d.Dispatcher,
		taskStore:        d.TaskStore,
		workerStore:      d.WorkerStore,
		taskHB:           d.TaskHB,
		workerHB:         d.WorkerHB,
		registerSecret:   d.RegisterSecret,
		taskMaxAttempt:   d.TaskMaxAttempt,
		workerLeaseTTL:   d.WorkerLeaseTTL,
		taskHeartbeatTTL: d.TaskHeartbeatTTL,
	}
}

// 编译期断言：实现 pb.WorkerServiceServer。
var _ pb.WorkerServiceServer = (*workerServiceServer)(nil)

// renewExpire 算 task 心跳 zset expire：min(now+ttl, 硬截止 timeout)。
// 续期不超硬截止（CLAUDE.md §8.12 不可续期）；Timeout 零值（理论上不会，RUNNING 必设）退化用 now+ttl。
func (s *workerServiceServer) renewExpire(t model.Task) time.Time {
	expire := time.Now().Add(s.taskHeartbeatTTL)
	if !t.Timeout.IsZero() && t.Timeout.Before(expire) {
		return t.Timeout
	}
	return expire
}

// Register 上线注册（DESIGN §10）。
// 校验 bootstrap secret -> 取 token -> workerStore.Register upsert workers 表 + workerHB.Renew 入 zset。
// 不主动唤醒 BACKOFF 任务（纯退避到期，§8.6）。返回 lease_expire_time。
func (s *workerServiceServer) Register(ctx context.Context, req *pb.RegisterRequest) (*pb.RegisterResponse, error) {
	if err := verifyRegisterSecret(registerSecretFrom(ctx), s.registerSecret); err != nil {
		return nil, err
	}
	token := workerTokenFrom(ctx)
	if err := validateRegister(req, token); err != nil {
		return nil, err
	}
	if token == "" {
		return nil, status.Error(codes.Unauthenticated, "authorization token required")
	}

	w, err := s.workerStore.Register(ctx, registerReqToWorker(req, token), s.workerLeaseTTL)
	if err != nil {
		return nil, errToStatus(err)
	}
	if s.workerHB != nil {
		_ = s.workerHB.Renew(ctx, w.UnitID, w.LeaseExpireTime) // zset 续期，失败忽略（下次 Heartbeat 补）
	}
	return &pb.RegisterResponse{LeaseExpireTime: tsPtr(w.LeaseExpireTime)}, nil
}

// Pull 批量拉取已派发任务（DESIGN §9.2/§8.3/§10）。非阻塞，空批返回空切片。
// authWorker 鉴权 -> dispatcher.Pull（PullRoundRobin + BatchSetRunning + ListByUnitIDs）
// -> taskHB.RenewMany 入 task 心跳 zset（expire cap 硬截止）。
func (s *workerServiceServer) Pull(ctx context.Context, req *pb.PullRequest) (*pb.PullResponse, error) {
	if err := authWorker(ctx, s.workerStore, req.GetUnitId(), workerTokenFrom(ctx)); err != nil {
		return nil, err
	}
	batch := validatePullBatch(req.GetBatch())
	tasks, err := s.dispatcher.Pull(ctx, req.GetUnitId(), batch)
	if err != nil {
		return nil, errToStatus(err)
	}
	if s.taskHB != nil && len(tasks) > 0 {
		items := make([]scheduler.TaskHeartbeatItem, 0, len(tasks))
		for _, t := range tasks {
			items = append(items, scheduler.TaskHeartbeatItem{UnitID: t.UnitID, Expire: s.renewExpire(t)})
		}
		_ = s.taskHB.RenewMany(ctx, items) // zset 维护，失败忽略（TTL 过期扫描器兜底重派）
	}
	return &pb.PullResponse{Tasks: tasksToPulled(tasks)}, nil
}

// Heartbeat 刷新 lease + 任务级活性心跳回收（DESIGN §10/§8.4 补充）。
// authWorker 鉴权 -> workerStore.Heartbeat（refresh lease + recovered）
// -> workerHB.Renew 续 worker zset + taskHB.RenewMany 续 runningUnitIds（task 心跳，复用本 RPC 不改 SDK）
// + taskHB.RemoveMany 删 recovered（已转 PENDING）。
func (s *workerServiceServer) Heartbeat(ctx context.Context, req *pb.HeartbeatRequest) (*pb.HeartbeatResponse, error) {
	if err := authWorker(ctx, s.workerStore, req.GetUnitId(), workerTokenFrom(ctx)); err != nil {
		return nil, err
	}
	expire, recovered, err := s.workerStore.Heartbeat(ctx, req.GetUnitId(), req.GetRunningUnitIds(), s.workerLeaseTTL)
	if err != nil {
		return nil, errToStatus(err)
	}
	if s.workerHB != nil {
		_ = s.workerHB.Renew(ctx, req.GetUnitId(), expire)
	}
	if s.taskHB != nil {
		// 续期 runningUnitIds（task 心跳，复用 worker Heartbeat，不改 SDK）。
		if rids := req.GetRunningUnitIds(); len(rids) > 0 {
			tasks, err := s.taskStore.ListByUnitIDs(ctx, rids)
			if err == nil {
				items := make([]scheduler.TaskHeartbeatItem, 0, len(tasks))
				for _, t := range tasks {
					if t.State != model.StateRunning {
						continue // 非 RUNNING（已回收/终态）不续期
					}
					items = append(items, scheduler.TaskHeartbeatItem{UnitID: t.UnitID, Expire: s.renewExpire(t)})
				}
				_ = s.taskHB.RenewMany(ctx, items)
			}
		}
		// recovered（已转 PENDING）的 task zset member 该删。
		_ = s.taskHB.RemoveMany(ctx, recovered)
	}
	return &pb.HeartbeatResponse{
		LeaseExpireTime:  tsPtr(expire),
		RecoveredUnitIds: recovered,
	}, nil
}

// Report 回传执行结果（DESIGN §10/§8.5/§8.7）。
// authWorker 鉴权 -> SUCCEEDED: Complete（RUNNING->COMPLETED 写 Result）；
// FAILED: ReportFail（按 dispatch_count 兜底 PENDING/DISCARDED）。
// 所有权不符（已被回收重派）-> 忽略，仍返回 OK。完成后 taskHB.Remove 删 task zset。
func (s *workerServiceServer) Report(ctx context.Context, req *pb.ReportRequest) (*pb.ReportResponse, error) {
	if err := authWorker(ctx, s.workerStore, req.GetWorkerUnitId(), workerTokenFrom(ctx)); err != nil {
		return nil, err
	}
	if err := validateReport(req); err != nil {
		return nil, err
	}

	switch req.GetResult() {
	case pb.ExecResult_EXEC_RESULT_SUCCEEDED:
		_, err := s.taskStore.Complete(ctx, req.GetUnitId(), req.GetWorkerUnitId(), req.GetPayload())
		if err != nil {
			return nil, errToStatus(err)
		}
		// reported=false（所有权不符/状态已变）亦返回 OK：Report 忽略，不报错（§8.7）。
	case pb.ExecResult_EXEC_RESULT_FAILED:
		_, _, err := s.taskStore.ReportFail(ctx, req.GetUnitId(), req.GetWorkerUnitId(), s.taskMaxAttempt)
		if err != nil {
			return nil, errToStatus(err)
		}
	default:
		return nil, status.Error(codes.InvalidArgument, "result unspecified")
	}
	if s.taskHB != nil {
		_ = s.taskHB.Remove(ctx, req.GetUnitId()) // task 终态/转 PENDING，删 zset（幂等）
	}
	return &pb.ReportResponse{}, nil
}
