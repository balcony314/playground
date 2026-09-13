# CLAUDE.md

本文件为 Claude Code (claude.ai/code) 在本仓库中工作提供指引。

## 项目概述

用 Go 实现的教学级 AI Agent，演示 **ReAct (Reasoning + Acting)** 模式。通过 OpenAI 兼容的 function calling 实现带工具调用（计算器、时间、搜索、文本处理、计划创建）的对话式 Agent。使用 cobra 管理 CLI 命令。

支持 **ReAct + Plan 机制**：对于复杂任务，Agent 可以先创建执行计划，再按步骤执行。

## 常用命令

```bash
go run .                                          # 交互式 REPL（Mock 模式）
go run . --api-key sk-xxx --model gpt-4o          # 接 OpenAI
go run . --api-key dummy -u http://localhost:11434/v1 -m qwen2  # 接 Ollama
go run . chat "你好"                              # 单次提问
go run . skill list                               # 查看技能列表
go run . version                                  # 版本信息
go build -o ai-agent .                            # 编译
go test ./...                                     # 运行测试
go test ./... -coverprofile=coverage.out          # 测试+覆盖率
go vet ./...                                      # 静态检查
```

全局 flags：`--api-key`/`-k`、`--base-url`/`-u`、`--model`/`-m`（默认 `gpt-4o`）、`--mock`

## 架构

采用 **Plan + ReAct 两阶段执行模式**：

```
用户消息
   ↓
阶段 1: Plan（planPhase）
   LLM 分析任务复杂度 → 简单任务直接 ReAct，复杂任务生成执行计划
   ↓
阶段 2: Execute
   对计划中的每个步骤执行 ReAct 循环（reactLoop）
   ↓
汇总结果（summarizeResults）→ 返回最终答案
```

```
main.go           → 入口：调用 cmd.Execute()
cmd/
  root.go         → Root 命令 + 全局 flags + 交互式 REPL + 启动连接检查
  chat.go         → chat 子命令：单次提问（非交互式）
  version.go      → version 子命令：版本信息
agent/
  agent.go        → Agent 核心：Plan + ReAct 两阶段编排 + Skill 切换
  llm.go          → OpenAIClient（Ping + /v1/chat/completions）+ MockClient（演示用）
  doc.go          → 包文档
  types/
    types.go      → 核心类型：Message, ToolCall, ToolDefinition, Config, Role, Plan, Step
  tools/
    registry.go   → ToolRegistry（map 实现）
    builtin.go    → RegisterBuiltinTools() 注册内置工具 + 辅助函数
    file_utils.go → 文件工具通用函数（路径验证、类型判断）
    file_read.go  → 5 个只读文件工具（read_file, list_dir, file_info, search_files, search_content）
    file_write.go → 3 个写入文件工具（write_file, edit_file, delete_file）
    exec.go       → 命令执行工具注册（桥接 exec 包）
    exec/
      config.go   → 执行配置（环境变量读取、默认值）
      security.go → 安全防护模块（黑名单、路径控制、敏感操作检测）
      executor.go → 命令执行核心（同步/异步、超时、输出捕获）
      audit.go    → 审计日志模块
      process.go  → 后台进程管理
  skills/
    registry.go   → SkillRegistry + RegisterBuiltinSkills() 注册 5 个内置技能
```

### agent.go 核心方法

| 方法 | 职责 |
|------|------|
| `Run(userInput)` | 主入口：Plan 阶段 → Execute 阶段 → 汇总结果 |
| `planPhase()` | 阶段 1：调用 LLM 判断任务复杂度，复杂任务生成 Plan，简单任务返回 nil |
| `executeStep(step, stepNum, totalSteps)` | 对单个计划步骤构建提示并调用 reactLoop |
| `reactLoop(taskPrompt)` | ReAct 核心循环：LLM 生成 → 工具执行 → 结果回传，最多 MaxTurns 轮 |
| `summarizeResults(plan, stepResults)` | 汇总所有步骤结果，调用 LLM 生成最终答案 |
| `registerPlanTool()` | 注册 `create_plan` 工具（闭包捕获 Agent 引用） |
| `setPlan(plan)` | 设置当前计划（创建副本，不修改原始数据） |

关键抽象：
- **`LLMClient` 接口**（`Chat` 方法）—— 可在 `OpenAIClient` 和 `MockClient` 之间切换
- **`OpenAIClient.Ping()`** —— 启动时检查 API 可达性，带 Authorization header，失败则退出
- **`ConfigWithModel(model)`** —— 将模型名注入系统提示词，让 LLM 知道自己使用的模型
- **`ToolRegistry`** —— `map[string]Tool`，提供 `Register`/`Get`/`Definitions`；每个 `Tool` 由 JSON Schema 的 `ToolDefinition` + `Execute` 函数组成
- **`Message`** —— role + content + 可选的 tool_calls/tool_call_id，对齐 OpenAI chat 格式

## 添加新工具

在 `agent/tools/builtin.go` 的 `RegisterBuiltinTools()` 中注册。提供带 JSON Schema 参数的 `ToolDefinition` 和一个 `Execute` 函数。LLM 根据 description 决定何时调用。

## 文件操作工具

8 个文件操作工具在 `agent/tools/` 目录下实现，所有操作限制在当前工作目录内。

内置文件工具：
- `read_file` - 读取文件内容（最大 1MB）
- `write_file` - 写入文件（创建/覆盖）
- `edit_file` - 编辑文件（替换/追加/插入/删除）
- `list_dir` - 列出目录内容
- `file_info` - 获取文件信息
- `delete_file` - 删除文件或目录
- `search_files` - 按名称模式搜索文件
- `search_content` - 按内容搜索（支持正则）

安全机制：
- `validatePath()` - 路径安全验证（禁止 `../`，限制在工作目录内）
- `isTextFile()` - 仅支持文本文件
- 文件大小限制（读取时最大 1MB）
- 搜索结果数量限制（100/50 条）

## 命令执行工具

2 个命令执行工具在 `agent/tools/exec.go` 中注册，核心逻辑在 `agent/tools/exec/` 包中。

内置执行工具：
- `execute_command` - 执行 shell 命令（支持同步/后台执行）
- `manage_process` - 管理后台进程（list/status/kill）

安全防护（三层机制）：
1. **命令黑名单** - 阻止危险命令（rm -rf /、dd、mkfs、shutdown 等）
2. **路径访问控制** - 限制访问系统关键路径（/etc、/usr、/bin 等）
3. **敏感操作检测** - 需要用户确认（git push、rm、chmod 等）

特性：
- 超时控制（默认 30 秒，可通过 EXEC_TIMEOUT 环境变量配置）
- 输出限制（默认 1MB，可通过 EXEC_MAX_OUTPUT 配置）
- 实时进度反馈（每 5 秒报告执行状态）
- 审计日志（通过 EXEC_AUDIT_LOG 配置日志路径）
- 错误分析（自动分析退出码和错误信息，提供修复建议）

配置环境变量：
- `EXEC_TIMEOUT` - 命令超时时间（秒）
- `EXEC_MAX_OUTPUT` - 最大输出字节数
- `EXEC_AUDIT_LOG` - 审计日志文件路径

代码结构：
```
agent/tools/exec/
  config.go      - 执行配置（环境变量读取、默认值）
  security.go    - 安全防护模块（黑名单、路径控制、敏感操作检测）
  executor.go    - 命令执行核心（同步/异步、超时、输出捕获）
  audit.go       - 审计日志模块
  process.go     - 后台进程管理
```

## Skill 系统

Skill 是预定义的 Agent 角色配置，可让同一个 Agent 切换不同"人格"。

内置技能：
- `general` - 通用助手（默认）
- `coder` - 代码助手
- `translator` - 翻译官
- `analyst` - 数据分析师
- `storyteller` - 故事大王

交互命令：
- `skills` - 列出所有可用技能
- `skill <名称>` - 切换到指定技能

添加新技能：在 `agent/skills/registry.go` 的 `RegisterBuiltinSkills()` 中注册。每个 Skill 包含 Name、Description、SystemPrompt 和可选的 Tools 列表。

## Plan 系统

Plan + ReAct 两阶段执行模式，让 Agent 能够处理复杂任务。

核心类型：
- `Plan` — 执行计划，包含 Goal（目标）和 Steps（步骤列表）
- `Step` — 单个步骤，包含 ID、Description、Status

执行流程（`Run` 方法驱动）：

```
1. planPhase()
   ├─ LLM 分析任务复杂度
   ├─ 简单任务 → 返回 nil → 直接进入 reactLoop
   └─ 复杂任务 → 调用 create_plan 工具 → 返回 Plan

2. 逐步骤执行
   └─ 对每个 Step 调用 executeStep() → reactLoop()

3. summarizeResults()
   └─ 汇总所有步骤结果 → LLM 生成最终答案
```

关键设计：
- `planPhase()` 使用临时历史副本，不污染主对话历史
- 简单任务（单步可完成）跳过计划，直接 ReAct 执行
- 步骤执行失败不中断后续步骤，错误信息记入汇总
- `reactLoop` 最多执行 `MaxTurns` 轮（默认 10 轮）

## 代码风格

- 注释和文档为中文（教学项目）
- 标准 Go 错误处理：`fmt.Errorf` + `%w` 包装
- 全程使用 OpenAI API 兼容的请求/响应格式
- 控制流规范：
  - 优先使用卫语句（if + return）提前返回，避免深层 if-else 嵌套
  - 多分支条件判断（if-else if-else）强制使用 switch 语句
- 函数设计原则：单一职责，确保一个函数只做一件事
- 参考规范：
  - [Uber Go 语言风格指南（中文）](https://github.com/xxjwxc/uber_go_guide_cn)
  - [Go 标准项目布局](https://github.com/golang-standards/project-layout)
