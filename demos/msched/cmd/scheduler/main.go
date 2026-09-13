// Package main 是 msched-scheduler 入口。
//
// 装配：软分片成员发现（NodeRegistry）+ gRPC WorkerService + HTTP API + 两个心跳过期
// 扫描器（worker/task，DESIGN §8.4）+ 撮合循环（matcher.MatchOnce，DESIGN §7/§9.1）。
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/balcony314/msched/internal/api"
	"github.com/balcony314/msched/internal/rpc"
	"github.com/balcony314/msched/internal/scheduler"
	"github.com/balcony314/msched/internal/storage"
	"github.com/balcony314/msched/internal/storage/pg"
	"github.com/balcony314/msched/internal/storage/redis"
	"github.com/balcony314/msched/internal/version"

	"github.com/gin-gonic/gin"
)

func main() {
	fmt.Printf("msched-scheduler %s\n", version.Version)
	if len(os.Args) > 1 && os.Args[1] == "-v" {
		return
	}

	cfg := storage.Load()
	nodeID := cfg.NodeID
	if nodeID == "" {
		h, err := os.Hostname()
		if err != nil {
			log.Fatalf("MSCHED_NODE_ID 未设置且 hostname 获取失败: %v", err)
		}
		nodeID = h
	}
	client := redis.NewRedis(cfg.Redis)
	ring := scheduler.NewRing(0) // 默认虚节点数
	nr := redis.NewNodeRegistry(client, nodeID, ring, cfg.NodeRefresh, cfg.NodeLeaseTTL)

	// PostgreSQL 真相源：建连 + 幂等建表 + TaskStore（业务侧创建/list/group_stats）。
	db, err := pg.NewDB(cfg.PG)
	if err != nil {
		log.Fatalf("pg new db: %v", err)
	}
	if err := pg.ApplySchema(db); err != nil {
		log.Fatalf("pg apply schema: %v", err)
	}
	taskStore, err := pg.NewTaskStore(db)
	if err != nil {
		log.Fatalf("new task store: %v", err)
	}

	// gRPC WorkerService（DESIGN §10）：dispatcher 复用 Redis 派发队列 + TaskStore，
	// WorkerStore 写 PostgreSQL workers 表（Register/Heartbeat），与 HTTP API 同进程并存。
	workerStore, err := pg.NewWorkerStore(db)
	if err != nil {
		log.Fatalf("new worker store: %v", err)
	}
	dq := redis.NewDispatchQueue(client)
	dispatcher := scheduler.NewDispatcher(taskStore, dq)

	// Redis 心跳 zset（worker 在线 + task 活性，DESIGN §8.4 补充）。
	workerHB := redis.NewWorkerHeartbeat(client)
	taskHB := redis.NewTaskHeartbeat(client)

	// WorkerRegistry：进程内 worker 视图缓存 + zset 在线判定（DESIGN §4.1）。
	// 注入 workerHB zset 作在线源；Start 后台周期刷新，matcher.Match 据此过滤在线 worker。
	registry := pg.NewWorkerRegistry(db, cfg.WorkerRefresh, workerHB)

	// Matcher（带软分片，DESIGN §5/§7）：注入与 NodeRegistry 共享的同一 *Ring 引用，
	// 后台 mutate 自动可见新成员（node_registry.go:9）。MatchBatchSize 为单轮单 group 候选批 N。
	matcher := scheduler.NewWithShard(taskStore, registry, dq, cfg.MatchBatchSize, ring, nodeID)

	grpcSrv := rpc.NewServer(cfg.GRPCAddr, rpc.Deps{
		Dispatcher:       dispatcher,
		TaskStore:        taskStore,
		WorkerStore:      workerStore,
		TaskHB:           taskHB,
		WorkerHB:         workerHB,
		RegisterSecret:   cfg.WorkerRegisterSecret,
		TaskMaxAttempt:   cfg.TaskMaxAttempt,
		WorkerLeaseTTL:   cfg.WorkerLeaseTTL,
		TaskHeartbeatTTL: cfg.TaskHeartbeatTTL,
	})
	grpcCtx, grpcCancel := context.WithCancel(context.Background())
	go func() {
		log.Printf("gRPC WorkerService 监听 %s", cfg.GRPCAddr)
		if err := grpcSrv.Start(grpcCtx); err != nil {
			log.Printf("grpc server: %v", err)
		}
	}()

	// HTTP API（创建/list/进度），随软分片一起启停。
	gin.SetMode(gin.ReleaseMode)
	httpSrv := api.NewServer(taskStore, cfg.HTTPAddr)
	httpCtx, httpCancel := context.WithCancel(context.Background())
	go func() {
		log.Printf("HTTP API 监听 %s", cfg.HTTPAddr)
		if err := httpSrv.Start(httpCtx); err != nil {
			log.Printf("http server: %v", err)
		}
	}()

	// 两个心跳过期扫描器（所有对等节点都跑，CAS 兜底防重复回收，DESIGN §8.4）。
	workerScanner := redis.NewWorkerHeartbeatScanner(workerHB, taskHB, taskStore, dq, cfg.WorkerScanInterval)
	taskScanner := redis.NewTaskHeartbeatScanner(taskHB, taskStore, cfg.TaskScanInterval)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := nr.Start(ctx); err != nil {
		log.Fatalf("node registry start: %v", err)
	}
	log.Printf("scheduler %s 已注册软分片，当前环成员: %v", nodeID, ring.Members())

	// WorkerRegistry 必须先于撮合循环 Start 且注入 workerHB（contracts/matcher-loop.md）：
	// 同步首次刷新避免 Match 见空缓存 -> 全部转 BACKOFF。
	if err := registry.Start(ctx); err != nil {
		log.Fatalf("worker registry start: %v", err)
	}

	// 撮合循环 goroutine（DESIGN §7/§9.1）：周期 MatchOnce，随 ctx 优雅退出。
	// MatchOnce 返回 error 仅 log 不退出循环（弱一致，下轮重试，contracts/matcher-loop.md 不变量）。
	matchCtx, matchCancel := context.WithCancel(context.Background())
	go func() {
		runMatchLoop(matchCtx, matcher, cfg.MatchInterval)
		matchCancel()
	}()
	log.Printf("撮合循环已启动（interval %s, batch %d）", cfg.MatchInterval, cfg.MatchBatchSize)

	if err := workerScanner.Start(ctx); err != nil {
		log.Fatalf("worker heartbeat scanner start: %v", err)
	}
	if err := taskScanner.Start(ctx); err != nil {
		log.Fatalf("task heartbeat scanner start: %v", err)
	}
	log.Printf("心跳扫描器已启动（worker %s / task %s）", cfg.WorkerScanInterval, cfg.TaskScanInterval)

	<-ctx.Done()
	log.Printf("收到退出信号，优雅停止...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	grpcCancel()
	grpcSrv.Stop(10 * time.Second)
	httpCancel()
	matchCancel() // 撮合循环随停机退出（goroutine 内 select <-ctx.Done()）
	if err := httpSrv.Stop(10 * time.Second); err != nil {
		log.Printf("http stop: %v", err)
	}
	workerScanner.Stop()
	taskScanner.Stop()
	registry.Stop() // WorkerRegistry 后台刷新 goroutine 退出
	if err := nr.Stop(shutdownCtx); err != nil {
		log.Printf("node registry stop: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
	_ = client.Close()
	log.Printf("scheduler %s 已退出", nodeID)
}

// runMatchLoop 撮合循环（DESIGN §7/§9.1）：按 interval 周期调用 matcher.MatchOnce。
//
// 抽出为独立函数便于装配级集成测试（match_loop_integration_test.go）直接驱动。
// ctx 取消即退出；MatchOnce 返回 error 仅 log 不退出循环（弱一致，下轮重试，
// contracts/matcher-loop.md 不变量）。interval <=0 时取 100ms 兜底（防配置误用致忙轮询）。
func runMatchLoop(ctx context.Context, matcher *scheduler.Matcher, interval time.Duration) {
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := matcher.MatchOnce(ctx); err != nil && ctx.Err() == nil {
				log.Printf("match once: %v", err)
			}
		}
	}
}
