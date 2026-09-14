// xds-server 进程入口：配置加载 → 日志初始化 → 端口校验 → 内部服务装配
// → HTTP 管理面 + xDS 控制面启动，并注册优雅退出回调。
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"strconv"

	xdshttp "github.com/balcony314/xds/api/http"
	"github.com/balcony314/xds/api/xds"
	"github.com/balcony314/xds/internal/service"
	"github.com/balcony314/xds/pkg/buildinfo"
	"github.com/balcony314/xds/pkg/config"
	"github.com/balcony314/xds/pkg/logging"
	"github.com/balcony314/xds/pkg/proc"
)

func main() {
	showVersion := flag.Bool("version", false, "打印构建版本信息后退出")
	configPath := flag.String("config", "", "指定配置文件路径")
	flag.Parse()

	// 仅查询版本信息的场景，打印后直接退出
	if *showVersion {
		fmt.Printf("Build Version Information:\n%s\n", buildinfo.Info.LongForm())
		return
	}

	// 配置先于日志：日志自身按配置初始化（级别/格式/轮转）
	if err := config.Init(*configPath); err != nil {
		panic(err)
	}
	logging.Init(
		config.GetString("log.level"),
		config.GetString("log.format"),
		config.GetString("log.dir"),
		config.GetInt("log.maxSize"),
		config.GetInt("log.maxBackups"),
		config.GetInt("log.maxAge"),
	)

	// 端口校验先于一切装配：配置缺失或为 0 时直接报错退出，
	// 不得静默监听随机端口（Task 11 评审 MINOR 项）
	if err := validateListenConfig(); err != nil {
		logging.Fatalf("invalid listen config: %v", err)
	}

	// 装配 repo 层与 service 层（内部 sync.Once，重复调用安全；
	// 含 Nacos 客户端构建，无 Nacos 环境下在此失败退出）
	if err := service.Init(); err != nil {
		logging.Fatalf("%v", err)
	}

	// HTTP 管理面：服务/实例管理 API + 本地 admin 探针
	httpServer, err := xdshttp.NewHTTPServer()
	if err != nil {
		logging.Fatalf("http server init err: %v", err)
	}

	// xDS 控制面：向 Envoy sidecar / proxyless SDK 下发配置。
	// Run 内部完成 gRPC 服务启动 → 存储全量初始化 → probe 置位 → 增量订阅
	xdsServer := &xds.XDSServer{}
	if err := xdsServer.Initialize(context.Background()); err != nil {
		logging.Fatalf("xds server init err: %v", err)
	}

	// 注册优雅退出回调：SIGTERM 时先关 HTTP（含 admin server）再由运行时退出
	proc.AddShutdownListener(func() {
		_ = httpServer.Close()
	})

	go func() {
		logging.Info("http server started")
		if err := httpServer.Run(); err != nil {
			logging.Errorf("http server exited: %v", err)
		}
	}()

	// 主 goroutine 阻塞在 xDS server 主循环上（gRPC 服务 + 配置同步），
	// 与 raw 的 select{} 阻塞语义相同且退出路径更清晰
	logging.Info("xds server started")
	if err := xdsServer.Run(); err != nil {
		logging.Fatalf("xds server exited: %v", err)
	}
}

// validateListenConfig 校验全部监听地址配置：xds gRPC/REST 端口、admin 端口、
// 业务 HTTP 地址。任一端口为 0 或缺失即返回错误，防止静默监听随机端口
func validateListenConfig() error {
	for _, check := range []struct {
		key  string
		port int
	}{
		{"xds.grpcPort", config.GetInt("xds.grpcPort")},
		{"xds.restPort", config.GetInt("xds.restPort")},
		{"web.adminPort", config.GetInt("web.adminPort")},
	} {
		if check.port <= 0 || check.port > 65535 {
			return fmt.Errorf("config %s invalid: %d, must be a port between 1 and 65535", check.key, check.port)
		}
	}

	// web.address 为完整 host:port 形式（如 0.0.0.0:8080），单独解析校验端口段
	addr := config.GetString("web.address")
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("config web.address invalid: %q, expect host:port form: %w", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return fmt.Errorf("config web.address invalid: %q, port must be between 1 and 65535", addr)
	}
	return nil
}
