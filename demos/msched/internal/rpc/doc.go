// Package rpc 实现 msched 的 gRPC WorkerService（DESIGN §10）。
//
// Worker -> Scheduler 的 RPC：Register/Pull/Heartbeat/Report。Server 端 token 拦截器
// 常量时间校验（model.Worker.VerifyToken），Register 额外 bootstrap secret 校验
// （metadata x-msched-register-secret）。Pull 复用 scheduler.Dispatcher（热路径零撮合零 CAS）。
//
// 鉴权模型（DESIGN §10）：token 预共享，worker 启动配置经 metadata authorization 传入；
// Register 是写入 workers.token 的入口，需 bootstrap secret 防恶意注册。Pull/Heartbeat/Report
// 查表 GetByUnitID + VerifyToken。
package rpc
