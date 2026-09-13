// Package storage 提供 msched 存储层：PostgreSQL(CockroachDB) 真相源 + Redis 派发队列访问。
//
// 详见 docs/DESIGN.md §3（数据模型）/§6（Redis 布局）/§8（状态机）。
// 实现 scheduler.TaskStore / scheduler.WorkerRegistry / scheduler.DispatchQueue 三接口，
// 语义忠实于 internal/scheduler/fake 的内存实现。
package storage

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config 存储层连接与运行配置。
type Config struct {
	PG                   PGConfig
	Redis                RedisConfig
	WorkerRefresh        time.Duration // WorkerRegistry 缓存刷新周期（DESIGN §4.1，默认 1s，可配）
	NodeID               string        // 本 Scheduler 节点 ID（软分片，env MSCHED_NODE_ID 注入，空兜底 hostname）
	NodeRefresh          time.Duration // NodeRegistry 成员刷新周期（DESIGN §5，默认 1s，可配）
	NodeLeaseTTL         time.Duration // 节点 lease 有效期（DESIGN §5，默认 5s，= 5×刷新容忍 4 次丢失）
	HTTPAddr             string        // HTTP API 监听地址（默认 :8080）
	GRPCAddr             string        // gRPC WorkerService 监听地址（DESIGN §10，默认 :9090）
	WorkerLeaseTTL       time.Duration // worker 在线 lease 有效期（DESIGN §3/§10，默认 10s，>刷新周期 1s）
	TaskHeartbeatTTL     time.Duration // task 心跳 zset 续期有效期（默认 5s，<WorkerLeaseTTL；worker 崩溃后此周期回收）
	WorkerScanInterval   time.Duration // worker 心跳过期扫描周期（默认 1s，扫 msched:hb:worker 过期 -> 回收其名下任务）
	TaskScanInterval     time.Duration // task 心跳过期扫描周期（默认 1s，扫 msched:hb:task 过期 -> CAS RUNNING->PENDING）
	TaskMaxAttempt       int           // 派发次数上限，超限转 DISCARDED（DESIGN §8.5，默认 5）
	WorkerRegisterSecret string        // Register bootstrap 共享密钥（DESIGN §10，metadata x-msched-register-secret 校验；生产 env 注入）
	MatchInterval        time.Duration // 撮合循环周期（DESIGN §7，默认 100ms，可配；research R2）
	MatchBatchSize       int           // 单轮单 group 候选批大小 N（DESIGN §7/§11，默认 100，可配；research R3）
}

// PGConfig CockroachDB（兼容 PostgreSQL 协议）连接配置。
type PGConfig struct {
	Host     string
	Port     int
	User     string
	Password string // 空表示无密码（insecure root）
	Database string
	SSLMode  string // insecure 模式用 "disable"
}

// DSN 构造 PostgreSQL 连接串。
func (c PGConfig) DSN() string {
	auth := c.User
	if c.Password != "" {
		auth = fmt.Sprintf("%s:%s", c.User, c.Password)
	}
	ssl := c.SSLMode
	if ssl == "" {
		ssl = "disable"
	}
	return fmt.Sprintf("postgresql://%s@%s:%d/%s?sslmode=%s&application_name=msched",
		auth, c.Host, c.Port, c.Database, ssl)
}

// RedisConfig Redis 连接配置。
type RedisConfig struct {
	Addr     string
	Password string
	DB       int
}

// DefaultConfig 返回测试环境默认配置（CockroachDB insecure，DESIGN 提供的测试实例）。
// 生产应由环境变量/配置文件覆盖。
func DefaultConfig() Config {
	return Config{
		PG: PGConfig{
			Host:     "192.168.124.3",
			Port:     26257,
			User:     "root",
			Password: "",
			Database: "defaultdb",
			SSLMode:  "disable",
		},
		Redis: RedisConfig{
			Addr: "127.0.0.1:6379",
		},
		WorkerRefresh:      time.Second,
		NodeRefresh:        time.Second,
		NodeLeaseTTL:       5 * time.Second,
		HTTPAddr:           ":8080",
		GRPCAddr:           ":9090",
		WorkerLeaseTTL:     10 * time.Second,
		TaskHeartbeatTTL:   5 * time.Second,
		WorkerScanInterval: time.Second,
		TaskScanInterval:   time.Second,
		TaskMaxAttempt:     5,
		MatchInterval:      100 * time.Millisecond,
		MatchBatchSize:     100,
	}
}

// Load 从环境变量加载配置：以 DefaultConfig 为基线，env 非空则覆盖（DESIGN §10）。
//
// 覆盖项：MSCHED_PG_HOST/PORT/USER/PASSWORD/DATABASE/SSL_MODE、MSCHED_REDIS_ADDR/PASSWORD/DB、
// MSCHED_HTTP_ADDR、MSCHED_GRPC_ADDR、MSCHED_NODE_ID、MSCHED_WORKER_LEASE_TTL、
// MSCHED_TASK_HEARTBEAT_TTL、MSCHED_WORKER_SCAN_INTERVAL、MSCHED_TASK_SCAN_INTERVAL、
// MSCHED_TASK_MAX_ATTEMPT、MSCHED_WORKER_REGISTER_SECRET、MSCHED_NODE_REFRESH、MSCHED_NODE_LEASE_TTL、
// MSCHED_WORKER_REFRESH、MSCHED_MATCH_INTERVAL、MSCHED_MATCH_BATCH。未设的保留默认值。
func Load() Config {
	c := DefaultConfig()
	if v := os.Getenv("MSCHED_PG_HOST"); v != "" {
		c.PG.Host = v
	}
	if v := os.Getenv("MSCHED_PG_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			c.PG.Port = p
		}
	}
	if v := os.Getenv("MSCHED_PG_USER"); v != "" {
		c.PG.User = v
	}
	if v := os.Getenv("MSCHED_PG_PASSWORD"); v != "" {
		c.PG.Password = v
	}
	if v := os.Getenv("MSCHED_PG_DATABASE"); v != "" {
		c.PG.Database = v
	}
	if v := os.Getenv("MSCHED_PG_SSL_MODE"); v != "" {
		c.PG.SSLMode = v
	}
	if v := os.Getenv("MSCHED_REDIS_ADDR"); v != "" {
		c.Redis.Addr = v
	}
	if v := os.Getenv("MSCHED_REDIS_PASSWORD"); v != "" {
		c.Redis.Password = v
	}
	if v := os.Getenv("MSCHED_REDIS_DB"); v != "" {
		if db, err := strconv.Atoi(v); err == nil {
			c.Redis.DB = db
		}
	}
	if v := os.Getenv("MSCHED_HTTP_ADDR"); v != "" {
		c.HTTPAddr = v
	}
	if v := os.Getenv("MSCHED_GRPC_ADDR"); v != "" {
		c.GRPCAddr = v
	}
	if v := os.Getenv("MSCHED_NODE_ID"); v != "" {
		c.NodeID = v
	}
	if v := os.Getenv("MSCHED_WORKER_LEASE_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.WorkerLeaseTTL = d
		}
	}
	if v := os.Getenv("MSCHED_TASK_HEARTBEAT_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.TaskHeartbeatTTL = d
		}
	}
	if v := os.Getenv("MSCHED_WORKER_SCAN_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.WorkerScanInterval = d
		}
	}
	if v := os.Getenv("MSCHED_TASK_SCAN_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.TaskScanInterval = d
		}
	}
	if v := os.Getenv("MSCHED_TASK_MAX_ATTEMPT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.TaskMaxAttempt = n
		}
	}
	if v := os.Getenv("MSCHED_WORKER_REGISTER_SECRET"); v != "" {
		c.WorkerRegisterSecret = v
	}
	if v := os.Getenv("MSCHED_NODE_REFRESH"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.NodeRefresh = d
		}
	}
	if v := os.Getenv("MSCHED_NODE_LEASE_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.NodeLeaseTTL = d
		}
	}
	if v := os.Getenv("MSCHED_WORKER_REFRESH"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.WorkerRefresh = d
		}
	}
	if v := os.Getenv("MSCHED_MATCH_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.MatchInterval = d
		}
	}
	if v := os.Getenv("MSCHED_MATCH_BATCH"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.MatchBatchSize = n
		}
	}
	return c
}
