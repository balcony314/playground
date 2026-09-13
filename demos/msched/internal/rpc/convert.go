package rpc

import (
	"time"

	"github.com/balcony314/msched/internal/model"
	pb "github.com/balcony314/msched/proto/msched/worker/v1"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// registerReqToWorker 把 RegisterRequest 转 model.Worker（不含 LeaseExpireTime，由 store 算）。
// token 来自 metadata（parseInterceptor 注入 ctx），不来自 request body。
func registerReqToWorker(req *pb.RegisterRequest, token string) model.Worker {
	return model.Worker{
		UnitID:   req.GetUnitId(),
		Labels:   model.WorkerSelector(req.GetLabels()),
		Capacity: int(req.GetCapacity()),
		Token:    token,
	}
}

// taskToPulled 把 model.Task 转 PulledTask（Pull 返回，DESIGN §10）。
// max_exec_duration 为执行硬截止依据（worker 本地主动控超时 + scheduler 同步兜底，§8.4）。
func taskToPulled(t model.Task) *pb.PulledTask {
	return &pb.PulledTask{
		UnitId:          t.UnitID,
		GroupUid:        t.GroupUID,
		Args:            t.Args,
		MaxExecDuration: durationpb.New(t.MaxExecDuration),
	}
}

// tasksToPulled 批量转换。
func tasksToPulled(ts []model.Task) []*pb.PulledTask {
	out := make([]*pb.PulledTask, 0, len(ts))
	for i := range ts {
		out = append(out, taskToPulled(ts[i]))
	}
	return out
}

// tsPtr 把 time.Time 转 *timestamppb.Timestamp（零值 -> nil）。
func tsPtr(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}
