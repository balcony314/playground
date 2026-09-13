package rpc

import (
	"context"
	"errors"
	"strings"

	"github.com/balcony314/msched/internal/model"
	"github.com/balcony314/msched/internal/scheduler"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// metadata key 常量（DESIGN §10）。
const (
	// mdAuthorization worker 身份 token，所有方法校验。
	mdAuthorization = "authorization"
	// mdRegisterSecret Register 入口 bootstrap 共享密钥，仅 Register 校验。
	mdRegisterSecret = "x-msched-register-secret"
)

// ctxKey 用于 context 注入解析出的身份（不与 gRPC metadata key 混用）。
type ctxKey string

const (
	ctxTokenKey       ctxKey = "worker_token"
	ctxRegisterSecKey ctxKey = "register_secret"
)

// tokenFromMD 从 gRPC metadata 取 authorization token（取首个值）。缺失返回空串。
func tokenFromMD(md metadata.MD) string {
	if v := md.Get(mdAuthorization); len(v) > 0 {
		return strings.TrimSpace(v[0])
	}
	return ""
}

// registerSecretFromMD 从 gRPC metadata 取 x-msched-register-secret。
func registerSecretFromMD(md metadata.MD) string {
	if v := md.Get(mdRegisterSecret); len(v) > 0 {
		return v[0]
	}
	return ""
}

// workerTokenFrom 从 ctx 取解析出的 worker token（parseInterceptor 注入）。
func workerTokenFrom(ctx context.Context) string {
	v, _ := ctx.Value(ctxTokenKey).(string)
	return v
}

// registerSecretFrom 从 ctx 取 Register bootstrap secret（parseInterceptor 注入）。
func registerSecretFrom(ctx context.Context) string {
	v, _ := ctx.Value(ctxRegisterSecKey).(string)
	return v
}

// verifyRegisterSecret 常量时间比较 Register 入口 bootstrap secret。
// 配置 secret 为空时拒绝（未配置则不开放注册，防误开）。
func verifyRegisterSecret(provided, expected string) error {
	if expected == "" || provided == "" {
		return status.Error(codes.Unauthenticated, "register secret required")
	}
	if !model.ConstantTimeEqual(provided, expected) {
		return status.Error(codes.Unauthenticated, "invalid register secret")
	}
	return nil
}

// authWorker 取 token -> workerStore.GetByUnitID -> model.Worker.VerifyToken。
// 用于 Pull/Heartbeat/Report：unitID 来自请求（请求体内 unit_id），token 来自 metadata。
// 失败返回 Unauthenticated（不泄漏具体缺失项）。worker 不存在亦算未鉴权。
//
// 热路径优化（DESIGN §9.2）：命中缓存跳过 DB 查询。仅缓存通过的结果（见 authCache），
// 否定结果（worker 不存在/token 错）走 DB 以保证新注册 worker 即时可见。
func authWorker(ctx context.Context, ws scheduler.WorkerStore, unitID, token string) error {
	if unitID == "" || token == "" {
		return status.Error(codes.Unauthenticated, "identity required")
	}
	if c := authCacheFromCtx(ctx); c != nil {
		if ok, hit := c.get(unitID, token); hit {
			if !ok {
				return status.Error(codes.Unauthenticated, "invalid token")
			}
			return nil
		}
	}
	w, err := ws.GetByUnitID(ctx, unitID)
	if err != nil {
		return status.Error(codes.Unauthenticated, "worker not found")
	}
	passed := w.VerifyToken(token)
	if c := authCacheFromCtx(ctx); c != nil {
		c.set(unitID, token, passed)
	}
	if !passed {
		return status.Error(codes.Unauthenticated, "invalid token")
	}
	return nil
}

// errToStatus 把 store 错误统一转 gRPC status：已含 status.Status 直接用，否则 Internal。
func errToStatus(err error) error {
	if err == nil {
		return nil
	}
	var s interface{ GRPCStatus() *status.Status }
	if errors.As(err, &s) {
		return err
	}
	return status.Error(codes.Internal, err.Error())
}
