package rpc

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/balcony314/msched/proto/msched/worker/v1"
)

// 输入边界常量（防御性校验，DESIGN §11.2 假设 64KB payload 可调）。
const (
	// maxPullBatch Pull 单次拉取上限，防 worker 请求超大 batch 触发 Lua 大循环（DESIGN §9.2）。
	maxPullBatch = 500
	// maxPayloadBytes Report 成功结果 payload 上限，防大 payload 撑爆 tasks.result（DESIGN §11.2 假设 64KB）。
	maxPayloadBytes = 64 * 1024
	// maxTokenLen worker token 长度上限，防超长输入。
	maxTokenLen = 256
)

// validatePullBatch 校验并规整 Pull batch：<=0 取 1，>maxPullBatch 截断到上限。
func validatePullBatch(b int32) int {
	batch := int(b)
	if batch <= 0 {
		batch = 1
	}
	if batch > maxPullBatch {
		batch = maxPullBatch
	}
	return batch
}

// validateRegister 校验 Register 请求：unit_id 非空、capacity 非负、labels 规模合理。
func validateRegister(req *pb.RegisterRequest, token string) error {
	if req.GetUnitId() == "" {
		return status.Error(codes.InvalidArgument, "unit_id required")
	}
	if req.GetCapacity() < 0 {
		return status.Error(codes.InvalidArgument, "capacity must be >= 0")
	}
	if len(token) > maxTokenLen {
		return status.Error(codes.InvalidArgument, "token too long")
	}
	return nil
}

// validateReport 校验 Report 请求：unit_id 非空、payload 不超限。
func validateReport(req *pb.ReportRequest) error {
	if req.GetUnitId() == "" {
		return status.Error(codes.InvalidArgument, "unit_id required")
	}
	if len(req.GetPayload()) > maxPayloadBytes {
		return status.Error(codes.InvalidArgument, "payload exceeds limit")
	}
	return nil
}
