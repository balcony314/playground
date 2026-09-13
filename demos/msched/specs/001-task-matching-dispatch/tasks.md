# Tasks: 任务撮合调度

**Input**: Design documents from `/specs/001-task-matching-dispatch/`

**Prerequisites**: plan.md (required), spec.md (required for user stories), research.md, data-model.md, contracts/, quickstart.md

**Tests**: 本 feature 撮合/派发/分片/退避的单组件单测已存在（matcher_test/dispatcher_test/ring_test/backoff_test）。本 tasks
仅生成**装配级集成测试**（验证 main.go 装配接通后的端到端链路），不重测单组件。

**Organization**: Tasks 按 user story 组织（spec.md 3 个 story），实现独立可测。核心组件已实现，本 feature 实质是
**装配接通**：把已实现的 `Matcher` + `WorkerRegistry` 接入 `cmd/scheduler/main.go` 持续撮合循环 + 补可配参数 + 集成验证。

## Format: `[ID] [P?] [Story] Description`

- **[P]**: 可并行（不同文件、无依赖）
- **[Story]**: 所属 user story（US1/US2/US3）
- 每个任务含确切文件路径

## Path Conventions

- 单 Go module 布局：`cmd/`（装配）+ `internal/scheduler`（撮合）+ `internal/storage`（PG/Redis）+ `internal/model`（实体）
- 路径相对仓库根

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: 撮合链路可配参数接入（无新基础设施，仅 config 扩展）

- [X] T001 在 internal/storage/config.go 的 Config struct 增 MatchInterval 与 MatchBatchSize 字段（含 godoc 引用 DESIGN §7/§11 与 research R2/R3）
- [X] T002 在 internal/storage/config.go 的 DefaultConfig() 设 MatchInterval=100ms、MatchBatchSize=100（depends T001，同文件连续编辑）
- [X] T003 在 internal/storage/config.go 的 Load() 增 MSCHED_MATCH_INTERVAL 与 MSCHED_MATCH_BATCH 环境变量加载（duration / int 解析，失败保留默认），并更新 Load 顶部 env 覆盖项注释（depends T001，与 T002 同文件但不同函数）

**Checkpoint**: config 扩展完成，`task vet` 通过

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: 撮合循环装配接通--本 feature 的核心，MUST 在所有 user story 验证前完成

**⚠️ CRITICAL**: 无撮合循环则 PENDING 任务永不被撮合，三个 user story 均无法验证

- [X] T004 在 cmd/scheduler/main.go 构造 pg.WorkerRegistry（注入 db + cfg.WorkerRefresh + workerHB zset），并在 NodeRegistry.Start 之后调用 Start(ctx)（同步首次刷新避免 Match 见空缓存，contracts/matcher-loop.md 装配不变量）
- [X] T005 在 cmd/scheduler/main.go 构造 scheduler.NewWithShard(taskStore, registry, dq, cfg.MatchBatchSize, ring, nodeID)，注入与 NodeRegistry 共享的同一 *Ring 引用（matcher 持同引用自动看到新成员，node_registry.go:9）（depends T004）
- [X] T006 在 cmd/scheduler/main.go 实现撮合循环 goroutine：ticker 周期（cfg.MatchInterval）调用 matcher.MatchOnce(ctx)，select { case <-ctx.Done(): return; case <-ticker.C: ... }；MatchOnce 返回 error 仅 log 不退出循环（弱一致，contracts/matcher-loop.md 不变量）（depends T005）
- [X] T007 在 cmd/scheduler/main.go 将 WorkerRegistry.Stop() 加入现有优雅停机序列（grpcSrv.Stop / nr.Stop 之间），确保 goroutine 随 ctx 退出不泄漏（depends T004-T006）
- [X] T008 在 cmd/scheduler/main.go 启动日志增"撮合循环已启动（interval %s, batch %d）"行（quickstart B2 预期日志）（depends T006）

**Checkpoint**: 撮合循环装配完成，`task build` 通过，scheduler 启动后周期执行 MatchOnce

---

## Phase 3: User Story 1 - 待处理任务匹配下发 (Priority: P1) 🎯 MVP

**Goal**: PENDING 任务匹配在线 worker 并入派发队列（SC-001），无匹配转 BACKOFF（SC-006）

**Independent Test**: 创建 PENDING task + 匹配 worker，启动撮合循环，验证 task 数秒内转 SCHEDULED 且 dispatch 队列有 member

### 装配级集成测试 (US1)

> 单组件单测已存在（matcher_test），此处验证装配接通后的端到端链路

- [X] T009 [P] [US1] 在 cmd/scheduler/match_loop_integration_test.go 增集成测试：构造真实 pg.TaskStore + miniredis DispatchQueue + fake/真实 WorkerRegistry，跑撮合循环数轮，断言 PENDING task -> SCHEDULED + dispatch 队列有 member（SC-001）
- [X] T010 [P] [US1] 在 cmd/scheduler/match_loop_integration_test.go 增测试：task 的 worker_selector 无匹配在线 worker -> 断言转 BACKOFF + next_retry_time 设值 + fail_count=1，且重复撮合不 DISCARDED（永不放弃，SC-006）
- [X] T011 [P] [US1] 在 cmd/scheduler/match_loop_integration_test.go 增测试：多 priority task 撮合 -> 断言高优先级先入队，同优先级按 unit_id 序（FR-007）

### 装配验证 (US1)

- [X] T012 [US1] 验证 quickstart 路径 A2/A3：`go test ./cmd/scheduler/ -run MatchLoop -v` 全绿（depends T009-T011）

**Checkpoint**: US1 端到端撮合链路验证通过，核心价值达成

---

## Phase 4: User Story 2 - 多租户公平防饿死 (Priority: P2)

**Goal**: 按 group 轮询撮合，大户不饿死小户（SC-004）

**Independent Test**: 两组（A 大 B 小）共享 worker 池，持续撮合，验证两组均有推进

### 装配级集成测试 (US2)

- [X] T013 [P] [US2] 在 cmd/scheduler/match_loop_integration_test.go 增测试：group A 100 task + group B 10 task 共享 worker 池，跑撮合循环若干轮，断言两组均有 task 转 SCHEDULED（推进量 >0，SC-004）
- [X] T014 [P] [US2] 在 cmd/scheduler/match_loop_integration_test.go 增测试：某 group 当前无匹配在线 worker -> 断言轮询跳过该组不阻塞整体推进（FR-004，空桶跳过）

### 装配验证 (US2)

- [X] T015 [US2] 验证 quickstart 路径 B3 场景 2（真实 PG + Redis 双 group 公平）：手工或脚本验证两组推进（depends T013）

**Checkpoint**: US2 多租户公平验证通过

---

## Phase 5: User Story 3 - 防双发与节点容错 (Priority: P3)

**Goal**: 多节点并发零重复下发（SC-003），节点离线后任务由他节点接管（SC-005）

**Independent Test**: 两个 Matcher 实例并发撮合同一 task，验证仅一个 CAS 成功

### 装配级集成测试 (US3)

- [X] T016 [P] [US3] 在 cmd/scheduler/match_loop_integration_test.go 增测试：两个 Matcher（不同 nodeID，共享同一 TaskStore + Dispatch 队列）并发 MatchOnce 同一 PENDING task，断言仅一个 dispatched=1、dispatch_count=1、零重复（SC-003，CAS 兜底）
- [ ] T017 [P] [US3] 在 cmd/scheduler/match_loop_integration_test.go 增测试：worker 心跳 zset 过期（worker 离线）-> 断言其名下 SCHEDULED/RUNNING task 被扫描器回收转 PENDING，可被重新撮合派发给其他 worker（SC-005 + FR-010，对齐 spec Edge Cases:80 worker 下线重派）
- [X] T018 [P] [US3] 在 cmd/scheduler/match_loop_integration_test.go 增测试：task 反复派发（dispatch_count 累计）超 TaskMaxAttempt -> 断言转 DISCARDED 不再参与撮合（FR-009，补 analyze E1 缺口）

### 装配验证 (US3)

- [X] T019 [US3] 验证 quickstart 路径 B3 场景 3（双 scheduler 实例防双发）+ 场景 4（worker 离线回收重派）（depends T016-T018）

**Checkpoint**: US3 防双发与容错验证通过

---

## Phase 6: Polish & Cross-Cutting Concerns

**Purpose**: 跨 story 的质量门与文档同步

- [X] T020 [P] 运行 `task fmt && task vet && task test` 全过（宪法 IV 质量门）
- [ ] T021 [P] 运行 `task test-integration`（需 CockroachDB，验证 quickstart 路径 B4 端到端集成套件）
- [X] T022 [P] 更新 docs/DESIGN.md §7 撮合章节标注"撮合循环已装配"（如设计文档仍标"待接入"）
- [X] T023 [P] 更新 CLAUDE.md「待定项」标注 MatchInterval/MatchBatchSize 已定默认值（100ms/100，待压测校准）

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: T001 先（增字段）-> T002/T003 依赖 T001（同文件 config.go 不同函数，建议连续编辑非并行）
- **Foundational (Phase 2)**: 依赖 Phase 1（需 cfg.MatchInterval/MatchBatchSize）；T004 -> T005 -> T006 串行装配，T007/T008 依赖 T004-T006 完成；**BLOCKS 所有 user story 验证**
- **User Stories (Phase 3-5)**: 均依赖 Phase 2 撮合循环装配完成
  - 集成测试文件（T009-T011 / T013-T014 / T016-T018）各 [P] 可并行（同文件不同测试函数，无文件冲突）
  - 但建议按 P1->P2->P3 顺序推进验证
- **Polish (Phase 6)**: 依赖所有 user story 完成

### User Story Dependencies

- **US1 (P1)**: 依赖 Foundational；无其他 story 依赖（核心撮合）
- **US2 (P2)**: 依赖 Foundational；复用 US1 的撮合循环装配，验证多 group 轮询（matcher.go group 游标已实现）
- **US3 (P3)**: 依赖 Foundational；验证 CAS 防双发（CASSchedule 已实现）+ 心跳扫描器回收（已实现）+ DISCARDED 终态（ReportFail 已实现）

### Within Each User Story

- 集成测试 [P] 并行编写
- 装配验证任务（T012/T015/T019）在该 story 集成测试之后

### Parallel Opportunities

- Phase 3-5: 所有 [P] 集成测试任务跨 story 可并行（同测试文件不同函数）
- Phase 6: T020-T023 独立检查/文档，可并行

---

## Parallel Example: 装配级集成测试

```bash
# US1/US2/US3 集成测试可并行编写（同测试文件不同函数）：
Task: "PENDING->SCHEDULED 端到端 in cmd/scheduler/match_loop_integration_test.go"   # T009
Task: "多 group 公平 in cmd/scheduler/match_loop_integration_test.go"               # T013
Task: "双 Matcher 防双发 in cmd/scheduler/match_loop_integration_test.go"           # T016
```

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. 完成 Phase 1: Setup（config 扩展）
2. 完成 Phase 2: Foundational（撮合循环装配，CRITICAL 阻塞全部）
3. 完成 Phase 3: User Story 1（端到端撮合 + 退避）
4. **STOP and VALIDATE**: `task test ./cmd/scheduler/` + quickstart A2/A3
5. 确认核心撮合链路可达后再推进 US2/US3

### Incremental Delivery

1. Setup + Foundational -> 撮合循环可运行
2. + US1 -> 核心撮合验证（MVP）
3. + US2 -> 多租户公平验证
4. + US3 -> 防双发与容错验证
5. Polish -> 质量门 + 文档同步

---

## Notes

- [P] 任务 = 不同文件或同文件不同函数，无依赖（Phase 1 config.go 同文件任务显式标 depends，非 [P]）
- [Story] 标签映射到 spec.md 的 user story
- 本 feature 复用已实现组件，装配接通为主，不引入新源码目录（YAGNI）
- 集成测试用 miniredis（路径 A，无需外部依赖）+ 真实 CockroachDB（路径 B，-tags=integration）
- 验证未覆盖单组件（matcher/dispatcher/ring/backoff 单测已存在，不重测）
- 本版修正 analyze 发现：T002/T003 去误标 [P] 补 depends（F1）、T007/T008/T012/T015/T019 补依赖标注（F2）、新增 T018 补 FR-009 DISCARDED 测试（E1）
