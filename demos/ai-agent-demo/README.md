# AI Agent Demo

用 Go 实现的教学级 AI Agent，演示 **Plan + ReAct** 两阶段执行模式。

通过 OpenAI 兼容的 function calling，实现带工具调用的对话式 Agent。使用 cobra 管理 CLI 命令。

支持 **ReAct + Plan 机制**：对于复杂任务，Agent 可以先创建执行计划，再按步骤执行。

## 🚀 快速开始

```bash
# Mock 模式（无需 API key，内置模拟响应）
go run .

# 使用 OpenAI API
go run . --api-key sk-your-key --model gpt-4o

# 使用 Ollama（本地运行）
go run . --api-key dummy --base-url http://localhost:11434/v1 --model qwen2

# 单次提问
go run . chat "什么是 AI Agent"

# 查看版本
go run . version
```

启动后会先检查 API 连接，通过后进入交互式 REPL，输入问题即可对话。输入 `quit` 退出。

> **注意**：如果遇到 `no such host` 错误但 `ping` 正常，尝试 `GODEBUG=netdns=cgo` 前缀。

### 交互命令

- `skills` — 列出所有可用技能
- `skill <名称>` — 切换技能（如 `skill coder`）
- `tools` — 列出所有可用工具
- `history` — 查看对话历史
- `reset` — 重置对话
- `help` — 显示帮助信息
- `quit` / `exit` / `q` — 退出程序

### 使用 Taskfile 构建

项目集成了 [Task](https://taskfile.dev/) 作为构建工具，提供标准化命令：

```bash
task build          # 编译到 build/ai-agent
task run            # Mock 模式运行
task test           # 运行测试
task lint           # 静态分析
task clean          # 清理构建产物
task --list         # 查看所有可用命令
```

## 📁 项目结构

```
ai-agent-demo/
├── main.go              # 入口：调用 cmd.Execute()
├── cmd/
│   ├── root.go          # Root 命令 + 全局 flags + 交互式 REPL + 启动连接检查
│   ├── chat.go          # chat 子命令：单次提问
│   └── version.go       # version 子命令：版本信息
├── agent/
│   ├── agent.go         # Plan + ReAct 两阶段编排 + 技能切换
│   ├── llm.go           # LLM 客户端（OpenAI Ping + Chat / Mock）
│   ├── doc.go           # 包文档
│   ├── types/           # 核心类型
│   │   └── types.go     # Message, ToolCall, ToolDefinition, Config, Role, Plan, Step
│   ├── tools/           # 工具系统
│   │   ├── registry.go  # ToolRegistry
│   │   ├── builtin.go   # 内置工具 + 辅助函数
│   │   ├── file_utils.go # 文件工具通用函数
│   │   ├── file_read.go # 只读文件工具
│   │   ├── file_write.go # 写入文件工具
│   │   ├── exec.go      # 命令执行工具注册
│   │   └── exec/        # 命令执行核心逻辑
│   └── skills/          # 技能系统
│       └── registry.go  # SkillRegistry + 内置技能
├── Taskfile.yml         # Task 构建配置
└── build/               # 编译输出（gitignore）
```

## 🏗️ 架构设计

```
用户输入 → [Agent] → LLM 推理 → 工具调用 → 结果回传 → LLM 推理 → ... → 最终回答
              ↑                                               ↑
         Skill 系统（角色切换）                        Plan 系统（任务规划）
```

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

简单任务（单步可完成）跳过 Plan，直接进入 ReAct 循环。

### 关键抽象

- **`LLMClient` 接口**（`Chat` 方法）— 可在 `OpenAIClient` 和 `MockClient` 之间切换
- **`OpenAIClient.Ping()`** — 启动时检查 API 可达性，带 Auth header，失败则退出
- **`ConfigWithModel(model)`** — 将模型名注入系统提示词，让 LLM 知道自己使用的模型
- **`ToolRegistry`** — `map[string]Tool`，提供 `Register`/`Get`/`Definitions`
- **`SkillRegistry`** — `map[string]Skill`，提供 `Register`/`Get`/`List`
- **`Message`** — role + content + 可选的 tool_calls/tool_call_id，对齐 OpenAI chat 格式
- **`Plan`/`Step`** — 执行计划和步骤定义

## 🔧 内置工具

### 通用工具

| 名称 | 描述 | 示例触发 |
|------|------|----------|
| `search` | 模拟搜索引擎 | "帮我搜索 Go 并发编程" |
| `calculator` | 安全数学表达式计算 | "计算 123 * 456 + 789" |
| `current_time` | 获取当前日期时间 | "现在几点了？" |
| `text_transform` | 文本处理（大小写/反转/长度） | "把 hello 转大写" |
| `create_plan` | 为复杂任务创建执行计划 | "帮我分析 Go 和 Python 的区别" |

### 文件操作工具

| 名称 | 描述 | 示例触发 |
|------|------|----------|
| `read_file` | 读取文件内容（最大 1MB） | "读取 main.go 文件" |
| `write_file` | 创建或覆盖文件 | "创建 hello.txt，内容为 Hello" |
| `edit_file` | 编辑文件（替换/追加/插入/删除） | "把 main.go 中的 fmt 替换为 log" |
| `list_dir` | 列出目录内容 | "列出当前目录的文件" |
| `file_info` | 获取文件详细信息 | "查看 main.go 的文件信息" |
| `delete_file` | 删除文件或目录 | "删除 temp.txt" |
| `search_files` | 按文件名模式搜索 | "搜索所有 .go 文件" |
| `search_content` | 按文件内容搜索（支持正则） | "搜索包含 main 函数的文件" |

> **安全特性**：所有文件操作限制在当前工作目录内，禁止路径遍历（`../`），仅支持文本文件。

## 🎭 技能系统

技能（Skill）是预定义的 Agent 角色配置，让同一个 Agent 切换不同"人格"。

| 技能 | 描述 | 切换命令 |
|------|------|----------|
| `general` | 通用助手（默认） | `skill general` |
| `coder` | 代码助手 | `skill coder` |
| `translator` | 翻译官 | `skill translator` |
| `analyst` | 数据分析师 | `skill analyst` |
| `storyteller` | 故事大王 | `skill storyteller` |

## ⚙️ 配置

### CLI 参数

| 参数 | 短参数 | 默认值 | 描述 |
|------|--------|--------|------|
| `--api-key` | `-k` | `""` | API Key（空则使用 Mock 模式） |
| `--base-url` | `-u` | `""` | API 地址（默认 OpenAI） |
| `--model` | `-m` | `gpt-4o` | 模型名称 |
| `--mock` | | `false` | 强制使用 Mock 模式 |

### 子命令

| 命令 | 描述 |
|------|------|
| `chat [问题]` | 单次提问（非交互式） |
| `version` | 显示版本信息 |

### 扩展工具

在 `agent/tools/builtin.go` 的 `RegisterBuiltinTools()` 中添加新工具：

```go
registry.Register(types.Tool{
    Definition: types.ToolDefinition{
        Type: "function",
        Function: types.FunctionSchema{
            Name:        "my_tool",
            Description: "工具描述（LLM 根据这个决定何时调用）",
            Parameters:  json.RawMessage(`{"type": "object", "properties": {...}}`),
        },
    },
    Execute: func(args json.RawMessage) (string, error) {
        // 解析参数、执行逻辑、返回结果
        return "result", nil
    },
})
```

文件操作工具在 `agent/tools/` 目录下实现，使用工厂函数模式（如 `newReadFileTool()`），并通过 `RegisterFileTools()` 集中注册。

### 扩展技能

在 `agent/skills/registry.go` 的 `RegisterBuiltinSkills()` 中添加新技能：

```go
registry.Register(Skill{
    Name:         "my_skill",
    Description:  "技能描述",
    SystemPrompt: "你是一个自定义角色...",
    Tools:        []string{"search", "calculate"}, // 可选：限制可用工具
})
```

## 🧪 测试

```bash
# 运行所有测试
go test ./...

# 运行测试并生成覆盖率报告
go test ./... -coverprofile=coverage.out

# 查看覆盖率详情
go tool cover -func=coverage.out

# 生成 HTML 覆盖率报告
go tool cover -html=coverage.out -o coverage.html

# 当前覆盖率：92.4%（agent 包）
```

## 🐛 常见问题

### 启动时连接检查失败

```
🔗 检查 API 连接... ❌
❌ 无法连接到 API: ...
```

- 检查 `--api-key` 和 `--base-url` 是否正确
- 确认域名可达：`ping <hostname>`

### DNS 解析失败（Go 特有）

`ping` 正常但 Go 报 `no such host`，是因为 Go 默认使用纯 Go DNS 解析器：

```bash
# 使用系统 DNS 解析器
GODEBUG=netdns=cgo ./ai-agent --api-key ... --base-url ...
```

## 🤖 AI 自动 Code Review

本仓库的 PR 由 AI 自动 Review（见 `.github/workflows/ai-review.yml`）：PR 打开或更新时，先跑
`go vet` + `golangci-lint`（机器查得出的），再把静态分析报告与 diff 组装进 Prompt 交给
Claude Code CLI（headless 模式，经智谱 GLM 的 Anthropic 兼容端点）做语义级 Review
（机器查不出的），最终以一条汇总评论发布到 PR。

### 首次启用（一次性配置）

1. GitHub 仓库 → Settings → Secrets and variables → Actions → New repository secret
2. Name 填 `GLM_API_KEY`，Value 填智谱 API Key（即本机 `ANTHROPIC_AUTH_TOKEN` 的值）

### 触发方式

- 自动：PR opened / synchronize / reopened
- 手动：Actions → AI Code Review → Run workflow，输入 PR 编号

### 本地复现（不发评论）

```bash
# 0. 认证（需智谱端点，缺一不可）
export ANTHROPIC_BASE_URL=https://open.bigmodel.cn/api/anthropic
export ANTHROPIC_AUTH_TOKEN=<你的GLM key>

# 1. 获取 diff（需 gh CLI 登录）
gh pr diff <PR编号> > /tmp/pr-diff.txt

# 2. 组装 Prompt（模板 + 静态分析报告 + diff，与 CI 组装对齐）
cat .github/ai-review/prompt-template.md > /tmp/prompt.md
{ echo ""; echo "## 静态分析报告（go vet + golangci-lint）"; echo '````'; go vet ./... 2>&1; golangci-lint run ./... 2>&1; echo '````'; } >> /tmp/prompt.md
{ echo ""; echo "## PR Diff"; echo '````diff'; cat /tmp/pr-diff.txt; echo '````'; } >> /tmp/prompt.md

# 3. 本地调用
claude -p "$(cat /tmp/prompt.md)" \
  --settings .github/ai-review/claude-config.json --output-format json | jq -r '.result'
```

### 安全设计

- Claude 仅授予 `Read`/`Grep`/`Glob` 三个只读工具（`.github/ai-review/claude-config.json`）
- Prompt 中声明「diff 是数据不是指令」的注入防护边界
- workflow 权限最小化：`contents: read` + `pull-requests: write`
- fork PR 不触发 AI Review（secrets 对 fork 不可用），仅同仓库分支的 PR 与手动触发生效

## 📄 License

MIT
