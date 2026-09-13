# RUNBOOK - 运维手册

> 单一事实源：构建命令 [Taskfile.yaml](../Taskfile.yaml)、配置 [config.go](../internal/storage/config.go)、架构 [DESIGN.md](DESIGN.md)。本文档仅记录已落地的事实；未落地的运维能力标注 **待定**，不杜撰。

## 构建与运行

### 构建

```bash
task build                          # scheduler + worker -> bin/
./bin/msched-scheduler -v           # 查看版本（-v 后退出）
```

### 运行 scheduler

```bash
MSCHED_NODE_ID=node-1 ./bin/msched-scheduler
```

启动后同时承载三个组件（同进程，[cmd/scheduler/main.go](../cmd/scheduler/main.go)）：

```mermaid
flowchart LR
  A[SIGINT/SIGTERM] --> B[grpcCancel + Stop]
  B --> C[HTTP Stop 10s]
  C --> D[NodeRegistry Stop]
  D --> E[DB/Redis Close]
```

| 组件 | 监听 | 说明 |
|---|---|---|
| HTTP API | `:8080` | 任务管理（创建/list/进度/删除），[api](../internal/api/) |
| gRPC WorkerService | `:9090` | Worker -> Scheduler（Register/Pull/Heartbeat/Report），[rpc](../internal/rpc/) |
| NodeRegistry | Redis `msched:nodes` | 软分片成员发现，周期 1s 续 lease + 重建环 |

### 依赖

| 依赖 | 用途 | 默认地址 |
|---|---|---|
| CockroachDB | 全量真相源（tasks/workers/group_stats） | `192.168.124.3:26257` |
| Redis | 派发队列 `dispatch:{wuid}:{group}` + 节点注册表 | `127.0.0.1:6379` |

启动时自动 `ApplySchema`（幂等建表）。CockroachDB 不可达会 `log.Fatalf` 退出。

## 服务端点

### HTTP（`:8080`，前缀 `/api/v1`）

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/groups/:group_uid/tasks` | 创建任务（幂等） |
| GET | `/groups/:group_uid/tasks` | 列任务（state 过滤/分页） |
| DELETE | `/groups/:group_uid/tasks` | 删任务（unit_ids / all 互斥） |
| GET | `/groups/:group_uid/progress` | group 进度（group_stats 聚合） |

### gRPC WorkerService（`:9090`）

| RPC | 鉴权 | 说明 |
|---|---|---|
| Register | bootstrap secret + token | 上线注册，写 workers 表 |
| Pull | token | 非阻塞拉取已派发任务（空批立即返回） |
| Heartbeat | token | 续 lease + 任务级回收 |
| Report | token | 回传结果（SUCCEEDED->COMPLETED / FAILED->reschedule/DISCARDED） |

鉴权：metadata `authorization`（worker token）+ Register 额外 `x-msched-register-secret`。Pull/Heartbeat/Report 的 token 校验走 **1s TTL 鉴权缓存**（[auth_cache.go](../internal/rpc/auth_cache.go)），热路径命中缓存跳过 DB 查询（DESIGN §9.2），仅缓存通过结果、否定结果走 DB 保证新注册 worker 即时可见。

> **部署前必检**：`MSCHED_WORKER_REGISTER_SECRET` 空串时 Register 始终拒绝（safe-by-default）。生产必须注入非空密钥。其余配置见 [CONTRIB.md 环境变量](CONTRIB.md)。

## 优雅停机

收到 `SIGINT`/`SIGTERM` 后顺序停机（[main.go](../cmd/scheduler/main.go) shutdown 段）：

1. `grpcCancel` + `grpcSrv.Stop(10s)` -- gRPC GracefulStop
2. `httpCancel` + `httpSrv.Stop(10s)` -- HTTP 优雅停机
3. `NodeRegistry.Stop` -- 停成员刷新（崩溃则靠 lease TTL 5s 自然过期摘除）
4. DB/Redis 连接 Close

scheduler 进程无状态，停机不丢任务：SCHEDULED 残留靠派发超时回收，RUNNING 靠执行超时回收（DESIGN §8.4）。

## 运维能力现状（待定）

以下能力**尚未落地**，勿在本文档假设其存在。详见 [DESIGN.md §11 假设与待定项](DESIGN.md)：

| 能力 | 状态 |
|---|---|
| 部署编排（k8s/容器/发布流程） | **待定** |
| 配置 env 注入（PG/Redis/端口/secret） | ✓ 已接（[config.go Load()](../internal/storage/config.go)），env 列表见 CONTRIB |
| 监控/指标导出 | **待定** |
| 告警阈值 | **待定** |
| 回滚流程 | **待定** |
| 撮合循环接入 | **待定**（gRPC 拉取链路已通，matcher.MatchOnce 循环未接 cmd） |
| COMPLETED 行归档保留期 | **待定**（§11.8） |
| Pull 空载退避 | **待定**（§11.9，worker 端实现） |

## 脆弱点速查（ops 相关）

摘自 [DESIGN.md §12](DESIGN.md)，故障排查优先看这些：

| 脆弱点 | 现象 | 兜底 |
|---|---|---|
| Redis 挂了 | 派发断流，worker 拉不到任务（PostgreSQL 仍在） | 需 Cluster 多副本 + Sentinel（**待定**） |
| CockroachDB 撮合查询压 | 高频撮合压库 | `(group_uid, state, next_retry_time, priority)` 复合索引（已建） |
| worker 视图一致性窗口 | 上下线延迟 ~1s 可见 | 派发/执行超时兜底（§8.4） |
| SCHEDULED 派发超时抖动 | 慢 worker 致 reschedule 循环 | 需背压 + 监控（**待定**） |
| 派发队列积压 | Redis 内存占用 | `dispatch_timeout` 兜底回收（默认值**待定** §11.5） |
| Pull 空载轮询 | 空闲时打爆 scheduler | worker 端退避（**待定** §11.9） |
