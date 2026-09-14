// Package http 提供对外的业务API server（Gin），
// 对外提供实例管理、服务管理等REST接口，以及独立的本地admin server
package http

import (
	"context"
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/balcony314/xds/api/http/handler/admin"
	"github.com/balcony314/xds/api/http/handler/instance"
	microservice "github.com/balcony314/xds/api/http/handler/micro_service"
	"github.com/balcony314/xds/api/http/middleware"
	"github.com/balcony314/xds/pkg/config"
	"github.com/balcony314/xds/pkg/logging"
	"github.com/gin-gonic/gin"
)

// HTTPServer 对外的业务API server，对外提供实例管理、服务管理等REST接口
type HTTPServer struct {
	server *http.Server
	// adminServer 本地管理server（127.0.0.1），Run 时创建并保存句柄，
	// Close 时一并关闭。修复来源：Task 13 评审 MINOR——此前 admin server
	// 在 Run 的匿名 goroutine 内临时创建，句柄未保存导致优雅退出时无法关闭
	adminServer *http.Server
}

// NewHTTPServer 创建业务API server并注册全部路由
func NewHTTPServer() (*HTTPServer, error) {
	s := HTTPServer{server: &http.Server{Addr: config.GetString("web.address"), Handler: newRouter()}}
	return &s, nil
}

// newRouter 构建业务API的gin路由：全局中间件链 + /api/v1业务路由 + /metrics
func newRouter() *gin.Engine {
	//按配置的debug开关决定gin运行模式：debug键开启时输出更详细的调试信息
	if config.GetBool("debug") {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	e := gin.New()
	//全局中间件链，按执行顺序依次为：
	//访问日志 -> 指标统计 -> 错误处理 -> panic恢复
	e.Use(middleware.GinAccessLogger(), middleware.MetricsMiddleware(),
		middleware.ErrorHandleMiddleWare(), middleware.PanicHandleMiddleWare)

	//业务路由统一挂在 /api/v1 前缀下，各业务模块自行注册
	router := e.Group("/api/v1")
	instance.Register(router.Group(""))
	microservice.Register(router.Group(""))

	//暴露Prometheus指标抓取接口
	e.GET("/metrics", gin.WrapH(promhttp.Handler()))
	return e
}

// newAdminServer 创建本地管理server，只绑定127.0.0.1，外部无法访问。
// 探针与缓存dump等运维接口均注册在此server上（区别于raw挂到业务server的写法）
func newAdminServer() *http.Server {
	e := gin.New()
	//与业务server保持一致的全局中间件链
	e.Use(middleware.GinAccessLogger(), middleware.MetricsMiddleware(),
		middleware.ErrorHandleMiddleWare(), middleware.PanicHandleMiddleWare)

	//就绪/健康探针与缓存dump等admin接口，仅供本地调试与编排系统探测
	admin.Register(e.Group(""))

	return &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", config.GetInt("web.adminPort")), Handler: e}
}

// Run 启动server：admin server在后台协程中启动，业务API server阻塞当前协程
func (s *HTTPServer) Run() error {
	// 先创建admin server并保存句柄，Close 时才能一并优雅关闭（见结构体注释）
	s.adminServer = newAdminServer()
	go func() {
		logging.Infof("starting admin server at %s", s.adminServer.Addr)
		logging.Debug(s.adminServer.ListenAndServe())
	}()
	logging.Infof("starting api server at %s", config.GetString("web.address"))
	return s.server.ListenAndServe()
}

// Close 优雅关闭业务API server与admin server
func (s *HTTPServer) Close() error {
	logging.Infof("shutting down server")
	defer logging.Infof("server exited")
	// 同时关闭admin server，避免优雅退出后admin端口残留（修复来源见结构体注释）
	if s.adminServer != nil {
		if err := s.adminServer.Shutdown(context.Background()); err != nil {
			logging.Errorf("shut down admin server err: %v", err)
		}
	}
	return s.server.Shutdown(context.Background())
}
