package rpc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	pb "github.com/balcony314/msched/proto/msched/worker/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// Server 是 msched gRPC WorkerService 服务，封装 *grpc.Server。
//
// 生命周期：Start 阻塞至 ctx 取消或监听错误；Stop 优雅 GracefulStop（等待在途 RPC）。
// 鉴权：parseInterceptor 解析 metadata 注入 ctx，各 handler 按需校验（Register secret /
// 其余 token + GetByUnitID + VerifyToken）。
type Server struct {
	addr   string
	server *grpc.Server
}

// NewServer 构造 Server。注册 WorkerServiceServer + parseInterceptor。
// parseInterceptor 同时注入 authCache（Pull/Heartbeat/Report 热路径鉴权缓存，DESIGN §9.2）。
func NewServer(addr string, deps Deps) *Server {
	cache := newAuthCache()
	srv := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		// parseInterceptor 解析 metadata + 注入 authCache。
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			ctx = context.WithValue(ctx, ctxTokenKey, tokenFromMD(md))
			ctx = context.WithValue(ctx, ctxRegisterSecKey, registerSecretFromMD(md))
		}
		ctx = withAuthCache(ctx, cache)
		return handler(ctx, req)
	}))
	pb.RegisterWorkerServiceServer(srv, newWorkerService(deps))
	return &Server{addr: addr, server: srv}
}

// Start 启动 gRPC 服务。阻塞至 ctx 取消或监听出错。Serve 返回 grpc.ErrServerClosed
// 视为正常关闭。
func (s *Server) Start(ctx context.Context) error {
	lis, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.addr, err)
	}
	errCh := make(chan error, 1)
	go func() {
		if err := s.server.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			errCh <- err
			return
		}
		errCh <- nil
	}()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return nil
	}
}

// Stop 优雅关闭，GracefulStop 等待在途 RPC 完成。
func (s *Server) Stop(timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		s.server.GracefulStop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		s.server.Stop() // 超时强制停
	}
}

// Server 暴露 *grpc.Server（测试用 bufconn / 注册自定义服务用）。
func (s *Server) GRPCServer() *grpc.Server { return s.server }
