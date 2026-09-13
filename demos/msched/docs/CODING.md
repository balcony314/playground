# msched 代码风格规范

本仓库遵循 [Uber Go 规范](https://github.com/xxjwxc/uber_go_guide_cn) 与 [Go 项目标准布局](https://github.com/golang-standards/project-layout)。以下为关键要点摘录与 msched 落地约定，写代码前必读。

## 1. 项目布局

参考 golang-standards/project-layout，msched 实际采用：

```
cmd/                    各 binary 入口，main.go 只做装配
  scheduler/main.go     msched-scheduler 入口
  worker/main.go        msched-worker 入口
proto/                  gRPC proto 源 + 生成代码（顶级，外部可 import，buf 管理，见下方）
internal/               私有代码，不对外暴露（关键：业务逻辑全在此）
  version/              版本信息
  scheduler/            调度器/撮合器逻辑（待实现）
  worker/               worker 逻辑（待实现）
  storage/              PostgreSQL + Redis 访问层（待实现）
docs/                   设计文档（DESIGN.md / REDIS.md）
buf.yaml                buf 模块配置（proto 依赖/lint/breaking）
buf.gen.yaml            buf 代码生成配置（go + go-grpc 插件）
buf.lock               buf 依赖锁
Taskfile.yaml           构建任务
```

**约定**：
- `cmd/` 只做依赖装配与启动，不含业务逻辑。main.go 越薄越好。
- `internal/` 放所有私有业务代码——这是 Go 编译器强制的私有边界，外部模块无法 import。
- **不盲目建目录**：`pkg/`（对外公共库）、`api/`（OpenAPI/proto 定义）、`configs/`、`scripts/`、`build/`、`deployments/` 等按需创建，不为"标准"而建空目录。当前只有 docs + 代码，不预创建。
- proto 源与生成代码放顶级 `proto/`（非 internal/），使外部项目可 import 生成代码。`internal/` 仅放私有业务代码。

**proto 约定（buf 管理）**：
- proto 源与生成代码均在顶级 `proto/` 下（移出 internal/，外部项目可 import），目录结构匹配 proto `package`（buf STANDARD 规范）：`proto/msched/worker/v1/worker.proto`。生成代码 import path = `github.com/balcony314/msched/proto/msched/worker/v1`（Go 包名 `workerv1`）。
- `go_package` 由 [buf.gen.yaml](buf.gen.yaml) `managed.go_package_prefix` 统一注入（= `github.com/balcony314/msched/proto/`，使 managed 推导的 go_package = prefix + 相对 module 路径 与物理 import path 一致），proto 源不写 `go_package` option；Go 包名按 package 推导（`msched.worker.v1` -> `workerv1`）。
- well-known types（`google/protobuf/{duration,timestamp}.proto`）经 [buf.yaml](buf.yaml) `deps: buf.build/protocolbuffers/wellknowntypes` 引用，不依赖 protoc 系统 include。
- 生成命令：`task proto`（= `buf lint` + `buf generate`）；同步依赖 `task proto-deps`（= `buf dep update`）。

## 2. 命名

- **包名**：小写、单词、无下划线无连字符。包名应能描述其内容，如 `scheduler`、`storage`，非 `sched`、`util`。
- **避免 `util`/`common`/`helpers` 包**：按功能命名，如 `strings`、`redis`、`pg`。
- **导出标识符**：首字母大写，命名应能自解释，避免冗余前缀如 `Manager`/`Helper` 当类型已明确时。
- **接口**：单方法接口用方法名 + `er` 后缀，如 `type Matcher interface { Match(...) }`；多方法接口用描述性名词。
- **变量**：驼峰，首字母大小写控制可见性。局部变量短名（`i`、`t`），包级变量长名。
- **常量**：不用全大写下划线（`MAX_RETRY` ❌），用驼峰（`MaxRetry` ✓）。
- **包内同类型避免无意义前缀**：`storage.Client` 而非 `storage.StorageClient`。

## 3. 错误处理

- **错误须显式处理**，不用 `_` 吞掉除非有明确理由。
- **error 作为最后一个返回值**。
- **包装错误加上下文**：`fmt.Errorf("match task %d: %w", id, err)`，保留 `%w` 让 `errors.Is/As` 可用。
- **错误类型**：优先用 `errors.New` / `fmt.Errorf`；自定义 error 类型仅在需区分错误类别时（如 `type NotFoundError struct{}`）。
- **panic**：仅用于不可恢复的程序员错误（如 init 失败、invariant 被破坏）。可预期错误用 error 返回，不 panic。
- **recover**：仅用于 server 顶层 goroutine 防崩溃，不滥用。

## 4. interface

- **在消费者侧定义接口**，不在实现侧。撮合器需要的 `WorkerRegistry` 接口定义在撮合器包，storage 包实现，而非反过来。
- **接口尽量小**：单方法接口优先。Go 的隐式实现让小接口组合自然。
- **不要为可测性提前抽接口**：若只有一个实现且无测试需求，不要造接口（YAGNI）。
- 避免在接口定义里暴露具体类型（如 `*sql.DB`），用接口或更抽象类型。

## 5. context

- **所有可能阻塞的函数**（DB/Redis/RPC/IO）首参接 `ctx context.Context`。
- **不要把 context 存进 struct**：显式传递。例外：长生命周期对象（如 worker）可在启动时持 ctx。
- **ctx.Done()**：长循环内 `select { case <-ctx.Done(): return ctx.Err() ... }`。
- 不用 `context.Background()` 在请求链路中，只在 main/顶层用。

## 6. goroutine 并发

- **知道 goroutine 何时退出**：每个 goroutine 要有明确生命周期，用 ctx 或 done channel 控制。
- **避免泄漏**：goroutine 持有的资源（DB 连接、锁）要随 goroutine 退出释放。
- **sync 优先于 channel 当只是同步**：互斥用 `sync.Mutex`，不用 channel 模拟锁。
- **sync.WaitGroup**：等待一组 goroutine 完成。
- **errgroup**：一组 goroutine 任一失败则全部取消，用 `golang.org/x/sync/errgroup`。
- **不要在持有锁时做 IO/阻塞调用**：缩小临界区。
- **atomic 优先于锁**当只是原子读写一个值。

## 7. 测试

- **测试文件与被测文件同包同目录**：`foo.go` + `foo_test.go`。
- **表驱动测试**：`tests := []struct{ name string; ... }{...}`，`t.Run(tt.name, ...)`。
- **t.Cleanup**：清理资源优于 defer 链。
- **不依赖测试执行顺序**，每个测试独立。
- **benchmark**：`BenchmarkFoo`，`b.ReportAllocs()` 关注分配。
- **mock**：消费者侧接口 mock，不 mock 具体类型。

## 8. 包结构与依赖

- **避免循环依赖**：包 A 依赖 B，B 不能再依赖 A。internal 下按领域拆，依赖单向。
- **import 顺序**：标准库空行 + 第三方空行 + 本项目，分组。
- **包级注释**：每个包有 `// Package xxx ...` 注释，导出类型/函数有注释（golint 要求）。
- **不导出不必要的东西**：内部类型用小写，仅导出外部需要的。

## 9. 代码风格细节

- **gofmt + goimports**：`task fmt` 已配置，提交前跑。
- **go vet**：`task vet`，必过。
- **不残留 `console.log` 等调试代码**（Go 里是 `fmt.Println` 调试残留）。
- **常量优于魔法数字**：`const MaxDispatchBatch = 100` 优于散落的 `100`。
- **及早 return**：错误处理 `if err != nil { return err }` 后继续主逻辑，避免深嵌套。
- **结构体初始化用字段名**：`Task{ID: 1, Group: g}` 不用 `Task{1, g}`。
- **零值可用**：`sync.Mutex{}`、`bytes.Buffer{}` 无需 `New`，优先零值。
- **避免全局可变状态**：全局 var 改依赖注入，除配置常量外。

## 10. msched 特定约定

- **状态机**：task `state` 枚举 `PENDING/BACKOFF/SCHEDULED/RUNNING/DISCARDED`，转换集中在状态机模块，不散落 `UPDATE` 语句。见 [DESIGN.md](DESIGN.md) §8。
- **无状态组件**：撮合器/调度器无状态多实例，状态全在 PostgreSQL + Redis，不在进程内存。代码勿引入进程级缓存与状态。
- **PostgreSQL(CockroachDB) CAS**：所有状态转换用 `WHERE state='X'` 乐观锁，影响行数=1 才算成功。
- **Redis 操作**：派发队列 `dispatch:{wuid}:{group}` 用 `{wuid}` hash tag；SCAN + ZPOPMIN 轮询；非阻塞。见 [REDIS.md](REDIS.md)。
- **错误可恢复 vs 不可恢复**：worker 挂了（无匹配）→ BACKOFF 退避；派发/执行超时 → reschedule 回 PENDING；执行反复失败超限 → DISCARDED。

## 11. 构建与检查

```
task build    # 编译 scheduler + worker
task test     # go test ./...
task vet      # go vet ./...
task fmt      # gofmt -s -w .
task tidy     # go mod tidy
```

提交前：`task fmt && task vet && task test` 全过。
