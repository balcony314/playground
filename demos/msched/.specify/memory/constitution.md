# msched Constitution

<!--
Sync Impact Report
- Version change: (unfilled template) -> 1.0.0
- Modified principles: N/A — initial ratification; all template placeholders replaced with concrete values.
- Added sections:
  - Core Principles I–V:
      I. PostgreSQL 为唯一真相源 (Single Source of Truth)
      II. 无状态对等与 Pull 模式 (NON-NEGOTIABLE)
      III. CAS 防双发不可移除 (NON-NEGOTIABLE)
      IV. 测试先行与集成验证 (Test-First & Integration)
      V. 可观测性与简洁性 (Observability & Simplicity)
  - 硬约束与技术栈
  - 开发流程与质量门
  - Governance
- Removed sections: N/A
- Templates requiring updates:
  - .specify/templates/plan-template.md        ✅ aligned (Constitution Check is a generic gate; no change needed)
  - .specify/templates/spec-template.md         ✅ aligned (generic)
  - .specify/templates/tasks-template.md        ✅ aligned (generic)
  - .specify/templates/checklist-template.md   ✅ aligned (generic)
- Spec Kit command files: all reference `.specify/memory/constitution.md` generically; no agent-specific
  names to generalize. speckit-converge / speckit-analyze will now enforce (previously skipped as template).
- Follow-up TODOs: none
-->

## Core Principles

### I. PostgreSQL 为唯一真相源 (Single Source of Truth)

- PostgreSQL(CockroachDB) 是全量任务状态的唯一真相源；Redis 仅存派发队列与 worker 注册表等**可重建的派生态**，MUST NOT 持有不可重建的权威状态。
- 撮合器直查 PostgreSQL PENDING 进行撮合，**无就绪池同步链路**；新 group 立即可撮合，不被就绪池积压阻塞。
- 进程内 MUST NOT 持有海量索引或任务状态（规避 Go GC 灾难）；状态转换集中于状态机模块，不散落裸 `UPDATE`。
- **理由**：10 亿级任务规模下，唯一真相源保证正确性、容灾可重建，且撮合无需额外同步链路。

### II. 无状态对等与 Pull 模式 (NON-NEGOTIABLE)

- Scheduler 进程**无状态、对等无主**；Worker **主动 Pull**，Scheduler MUST NOT push。Pull 非阻塞——有则返回、空则立即返回空批，Worker 端退避轮询。
- 状态机严格遵循：`PENDING -> SCHEDULED -> RUNNING -> COMPLETED/DISCARDED`（含 `BACKOFF` 退避态）。**未下发的任务不是 RUNNING**（派发队列里是 SCHEDULED，worker 拉走才 RUNNING）。
- 软分片（一致性哈希按 `group_uid` 归属节点）仅消除 CAS 竞争，**正确性不依赖分片归属**。
- **理由**：无中心消除单点与主从切换；无状态进程支持即开即用的水平扩缩容。

### III. CAS 防双发不可移除 (NON-NEGOTIABLE)

- 所有状态转换 MUST 用 `WHERE state='X'` 乐观锁，**影响行数=1 才算抢到**。
- 节点 N 变更瞬间 group 可能被多节点认领，MUST 靠 CAS 兜底防双发；**软分片不可替代 CAS**。
- 任何移除或弱化 CAS 的改动 MUST 先经架构评审，并在 plan.md Constitution Check 记录理由与被否决的替代方案。
- **理由**：无中心多实例并发下，CAS 是正确性的最后防线；分片只减竞争，不保正确。

### IV. 测试先行与集成验证 (Test-First & Integration)

- TDD 强制：测试先行 -> 用户确认 -> 测试失败 -> 实现；Red-Green-Refactor 严格执行。
- 测试文件与被测同包同目录；表驱动测试；消费者侧接口 mock，MUST NOT mock 具体类型。
- 集成测试 MUST 覆盖：状态机转换、CAS 并发竞争、Redis 派发队列轮询、gRPC 契约、跨存储端到端。
- 提交前质量门 MUST 全过：`task fmt && task vet && task test`。
- **理由**：分布式系统并发态与状态机复杂，测试是回归与正确性防线。

### V. 可观测性与简洁性 (Observability & Simplicity)

- 结构化日志 REQUIRED；状态转换可追踪；`group_stats` 实时维护用于观测与撮合决策（同事务内增减，CAS 成功者才改）。
- 撮合/派发热路径零阻塞（Pull 热路径零撮合零 CAS）；错误区分**可恢复**（BACKOFF 退避 / 超时 reschedule 回 PENDING）与**不可恢复**（派发超限 DISCARDED），且**永不放弃**匹配。
- YAGNI：按需建目录，不为"标准布局"预创空目录；单一实现且无测试需求时不提前抽接口。
- **理由**：10 亿级规模下可观测性是运维前提；简洁性规避过度工程与 GC 灾难。

## 硬约束与技术栈

- **语言/规范**：Go，遵循 [docs/CODING.md](../../docs/CODING.md)（Uber Go 规范 + golang-standards 项目布局）。
- **存储**：PostgreSQL(CockroachDB) 真相源 + Redis 派发队列；schema 遵循 [docs/SQL.md](../../docs/SQL.md)。Redis 派发队列 `dispatch:{wuid}:{group}` 用 `{wuid}` hash tag，SCAN + ZPOPMIN 轮询，非阻塞。
- **RPC**：gRPC + protobuf（buf 管理）；Worker 接口 `Register / Pull / Heartbeat / Report`。
- **规模与语义**：PENDING 任务总量约 10 亿；**不支持定时**（创建即 PENDING 即可派发）；多租户按 `GroupUID` 隔离级防饿死（双重 group 轮询，非严格等量）。
- **并发与错误**：`context.Context` 贯穿所有阻塞调用；错误用 `%w` 包装保留可判别性；可预期错误用 error 返回，MUST NOT panic。
- **匹配语义**：单向强约束 `Task.WorkerSelector ⊆ Worker.Labels`，selector 维度开放，撮合器进程内遍历匹配，无需 Redis 倒排预建。

## 开发流程与质量门

- 实现 feature 前 MUST 先理解 [docs/DESIGN.md](../../docs/DESIGN.md) §1 架构关键决策，MUST NOT 推翻已评估取舍。
- `cmd/` 只做依赖装配，MUST NOT 含业务逻辑；`internal/` 放所有私有业务代码（Go 编译器强制私有边界）。
- 状态机转换集中在状态机模块，MUST NOT 散落裸 `UPDATE`；状态全在 PostgreSQL + Redis，代码 MUST NOT 引入进程级缓存与状态。
- 提交前质量门 MUST 全过：`task fmt && task vet && task test`。
- 复杂度须有正当理由：plan.md Constitution Check MUST 记录违规、必要性、被否决的更简方案。
- 设计/架构文档变更 MUST 同步更新 CLAUDE.md / docs/CODING.md / docs/SQL.md 相关条目。

## Governance

- 本宪法高于一切其他实践；与宪法冲突的实现 MUST 先修宪，或在 plan.md 记录复杂度豁免并经评审。
- **修宪流程**：文档化变更 -> 评审 -> 按 SemVer 升版本号：
  - **MAJOR**：删除/重定义原则或破坏性治理变更；
  - **MINOR**：新增原则/章节或实质性扩展指引；
  - **PATCH**：澄清、措辞、笔误等非语义修订。
- **合规审查**：每个 PR 与 plan.md MUST 核对宪法；复杂度须正当化；违反宪法 MUST 的冲突自动判为 CRITICAL（由 `/speckit-analyze`、`/speckit-converge` 执行）。
- **运行时开发指引**：架构决策见 [docs/DESIGN.md](../../docs/DESIGN.md)，代码风格见 [docs/CODING.md](../../docs/CODING.md)，schema 见 [docs/SQL.md](../../docs/SQL.md)。

**Version**: 1.0.0 | **Ratified**: 2026-07-14 | **Last Amended**: 2026-07-14
