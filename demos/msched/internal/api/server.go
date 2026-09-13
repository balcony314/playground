package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/balcony314/msched/internal/scheduler"
	"github.com/gin-gonic/gin"
)

// Server 是 msched HTTP API 服务，封装 gin Engine + *http.Server。
//
// 生命周期：Start 阻塞至 ctx 取消或 Shutdown 调用；Stop 优雅关闭（等待在途请求）。
// 路由前缀 /api/v1，路由表见 handler.register。
type Server struct {
	addr   string
	engine *gin.Engine
	http   *http.Server
}

// NewServer 构造 Server。store 为 TaskStore 实现（pg/fake 均可）。
// 生产应设 gin.ReleaseMode（NewServer 不强制，由调用方按需 gin.SetMode）。
func NewServer(store scheduler.TaskStore, addr string) *Server {
	engine := gin.New()
	engine.Use(gin.Recovery())
	h := newHandler(store)
	rg := engine.Group("/api/v1")
	h.register(rg)
	return &Server{
		addr:   addr,
		engine: engine,
		http: &http.Server{
			Addr:              addr,
			Handler:           engine,
			ReadHeaderTimeout: 5 * time.Second,
		},
	}
}

// Start 启动 HTTP 服务。阻塞至 ctx 取消（调用方应 go 起协程）。ListenAndServe 返回
// ErrServerClosed 视为正常关闭。
func (s *Server) Start(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		if err := s.http.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
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

// Stop 优雅关闭，等待在途请求完成（最多 timeout）。
func (s *Server) Stop(timeout time.Duration) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := s.http.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("http shutdown: %w", err)
	}
	return nil
}

// Engine 暴露 gin Engine（测试用 httptest 直接驱动）。
func (s *Server) Engine() *gin.Engine { return s.engine }
