# CONTRIB - 开发贡献指南

> 单一事实源：[Taskfile.yaml](../Taskfile.yaml)（构建命令）、[internal/storage/config.go](../internal/storage/config.go)（配置默认值）、代码中的 env 引用。本文档随代码同步。

## 技术栈

| 项 | 值 |
|---|---|
| 语言 | Go 1.26 |
| 模块路径 | `github.com/balcony314/msched` |
| ORM | GORM |
| 真相源 | CockroachDB（兼容 PostgreSQL 协议） |
| 派发队列/成员发现 | Redis |
| RPC | gRPC + protobuf（buf 生成） |
| HTTP | gin |

设计依据见 [DESIGN.md](DESIGN.md)；编码规范 [CODING.md](CODING.md)；SQL/表结构 [SQL.md](SQL.md)；Redis 布局 [REDIS.md](REDIS.md)。

## Task 命令

> 来源 [Taskfile.yaml](../Taskfile.yaml)，需安装 [Task](https://taskfile.dev)。

| 命令 | 说明 |
|---|---|
| `task build` | 构建全部二进制（scheduler + worker）→ `bin/` |
| `task scheduler` | 构建 `bin/msched-scheduler` |
| `task worker` | 构建 `bin/msched-worker` |
| `task test` | 运行单元测试 `go test ./...`（无需外部依赖） |
| `task test-integration` | 集成测试 `go test -tags=integration ./...`（需 CockroachDB） |
| `task vet` | `go vet ./...` |
| `task fmt` | `gofmt -s -w .` |
| `task tidy` | `go mod tidy` |
| `task proto` | `buf lint && buf generate`（重新生成 gRPC 代码） |
| `task proto-deps` | `buf dep update`（同步 buf 依赖） |
| `task clean` | 清理 `bin/ dist/` |

## 环境变量

> 来源 [config.go](../internal/storage/config.go) `Load()`，以 `DefaultConfig` 为基线，env 非空则覆盖。生产化前必须注入敏感项（尤其 `MSCHED_WORKER_REGISTER_SECRET`）。

| 变量 | 用途 | 默认 |
|---|---|---|
| `MSCHED_NODE_ID` | Scheduler 节点 ID（软分片环归属，DESIGN §5）。空则兜底 `hostname` | hostname |
| `MSCHED_PG_HOST`/`PORT`/`USER`/`PASSWORD`/`DATABASE`/`SSL_MODE` | CockroachDB 连接 | `192.168.124.3:26257`/root/`defaultdb`/disable |
| `MSCHED_REDIS_ADDR`/`PASSWORD`/`DB` | Redis 连接 | `127.0.0.1:6379` |
| `MSCHED_HTTP_ADDR` | HTTP API 监听 | `:8080` |
| `MSCHED_GRPC_ADDR` | gRPC WorkerService 监听 | `:9090` |
| `MSCHED_WORKER_LEASE_TTL` | worker lease 有效期 | `10s` |
| `MSCHED_TASK_MAX_ATTEMPT` | 派发次数上限（`>=` 转 DISCARDED，DESIGN §8.5） | `5` |
| `MSCHED_WORKER_REGISTER_SECRET` | Register bootstrap 密钥（空则 Register 始终拒绝） | `""` |
| `MSCHED_NODE_REFRESH`/`NODE_LEASE_TTL` | 软分片刷新/lease | `1s`/`5s` |
| `MSCHED_WORKER_REFRESH` | worker 视图刷新 | `1s` |

> 集成测试连接仍是 `DefaultConfig().PG` 硬编码（[conn_integration_test.go:22](../internal/storage/pg/conn_integration_test.go#L22)），`Load()` 不影响测试库定位。

## 默认配置

来源 [config.go](../internal/storage/config.go) `DefaultConfig`：

| 配置 | 默认值 |
|---|---|
| CockroachDB | `192.168.124.3:26257`，DB `defaultdb`，user `root`，sslmode `disable` |
| Redis | `127.0.0.1:6379` |
| HTTP API | `:8080` |
| gRPC WorkerService | `:9090` |
| Worker lease TTL | `10s` |
| TaskMaxAttempt | `5` |
| WorkerRegisterSecret | `""`（空，需生产注入） |
| NodeRegistry 刷新 | `1s`，lease TTL `5s` |
| WorkerRegistry 刷新 | `1s` |

## 测试流程

### 单元测试（无需外部依赖）

```bash
task test        # 或 go test ./...
```

覆盖：model 语义、scheduler 撮合/派发、redis 派发队列（miniredis）、rpc 四方法（bufconn + fake）、api（httptest）。

### 集成测试（需 CockroachDB）

```bash
task test-integration   # 或 go test -tags=integration ./...
```

`//go:build integration` 标记的测试需真实 CockroachDB（默认连 `192.168.124.3:26257`）。schema 首次 DROP+重建，后续复用；各测试 `cleanTables` 清数据隔离。Redis 集成测试用 miniredis（无需真实 Redis）。

无 CockroachDB 时集成测试会 `t.Fatalf`，单元测试不受影响。

### proto 代码生成

改 `.proto` 后：

```bash
task proto    # buf lint + buf generate
```

生成产物在 [proto/msched/worker/v1/](../proto/msched/worker/v1/)，提交进仓库（非生成时勿手改）。

## 开发约定

- **代码风格**：[CODING.md](CODING.md)（Uber Go 规范 + golang-standards 布局），写代码前必读
- **SQL/schema**：[SQL.md](SQL.md)，改 schema 前必读
- **设计决策**：[DESIGN.md](DESIGN.md) §1 关键决策，勿擅自推翻已评估取舍
- **错误处理**：`return err` 必须带上下文（`fmt.Errorf("xxx: %w", err)`），不裸返回
- **不可变性**：创建新对象，不原地修改（见全局 coding-style）
- **提交**：conventional commits（`feat`/`fix`/`refactor`/`perf`/`test`/`docs`），无 Co-Authored-By
