package types

// ═══════════════════════════════════════════════════════════════
// types.go — Agent 核心类型定义
// ═══════════════════════════════════════════════════════════════
//
// AI Agent = LLM + Tools + Loop
//
// 本文件定义了 Agent 运行所需的所有数据结构。
// 理解这些类型是理解整个 Agent 的基础。

import (
	"encoding/json"
	"fmt"
)

// ─── 消息类型 ───────────────────────────────────────────────────

// Role 表示消息在对话中的角色
type Role string

const (
	RoleSystem    Role = "system"    // 系统提示词：定义 Agent 的行为
	RoleUser      Role = "user"      // 用户输入
	RoleAssistant Role = "assistant" // LLM 的回复（可能包含工具调用）
	RoleTool      Role = "tool"      // 工具执行结果
)

// Message 是一条对话消息
type Message struct {
	Role       Role       `json:"role"`
	Content    string     `json:"content,omitempty"`    // 文本内容
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"` // LLM 请求调用的工具
	ToolCallID string     `json:"tool_call_id,omitempty"` // 对应哪个工具调用的 ID
}

// ─── 工具调用类型 ──────────────────────────────────────────────

// ToolCall 表示 LLM 决定调用的一个工具
type ToolCall struct {
	ID       string       `json:"id"`       // 唯一标识，用于匹配结果
	Type     string       `json:"type"`     // 固定为 "function"
	Function FunctionCall `json:"function"` // 要调用的函数
}

// FunctionCall 包含函数名和参数
type FunctionCall struct {
	Name      string `json:"name"`      // 函数名
	Arguments string `json:"arguments"` // JSON 字符串格式的参数
}

// ToolDefinition 是发送给 LLM 的工具描述（让 LLM 知道有哪些工具可用）
type ToolDefinition struct {
	Type     string         `json:"type"`     // 固定为 "function"
	Function FunctionSchema `json:"function"` // 函数 schema
}

// FunctionSchema 描述一个函数的签名
// 这些信息会发给 LLM，LLM 根据 Description 判断什么时候该用这个工具
// Parameters 是标准的 JSON Schema 格式，告诉 LLM 每个参数的类型和含义
type FunctionSchema struct {
	Name        string          `json:"name"`        // 函数名，LLM 通过这个名字调用工具
	Description string          `json:"description"` // 功能描述，LLM 靠这个决定用哪个工具
	Parameters  json.RawMessage `json:"parameters"`  // JSON Schema 格式的参数定义
}

// ToolFunc 是工具的执行函数签名
// 输入：JSON 格式的参数（由 LLM 生成）
// 输出：执行结果的文本（会作为 RoleTool 消息喂回 LLM）
type ToolFunc func(args json.RawMessage) (string, error)

// Tool 是一个完整的工具定义，包含两部分：
//   - Definition: 描述工具的 schema，发给 LLM 让它知道有这个工具可用
//   - Execute: 实际执行逻辑，当 LLM 决定调用时由 Agent 框架执行
type Tool struct {
	Definition ToolDefinition
	Execute    ToolFunc
}

// ─── 计划类型 ────────────────────────────────────────────────────

// Plan 表示一个执行计划
type Plan struct {
    Goal  string `json:"goal"`  // 任务目标
    Steps []Step `json:"steps"` // 步骤列表
}

// Step 表示计划中的一个步骤
type Step struct {
    ID          int    `json:"id"`          // 步骤编号
    Description string `json:"description"` // 步骤描述
    Status      string `json:"status"`      // 状态: pending/done/failed/skipped
}

// ─── Agent 配置 ──────────────────────────────────────────────────

// Config 是 Agent 的配置
type Config struct {
	SystemPrompt string  // 系统提示词
	MaxTurns     int     // 最大推理轮数（防止无限循环）
	Tools        []Tool  // 可用工具列表
}

// DefaultConfig 返回默认配置
func DefaultConfig() Config {
	return Config{
		SystemPrompt: `你是一个有用的 AI 助手，采用 Plan + ReAct 模式工作。

工作流程：
1. 当用户提出任务时，首先分析任务复杂度
2. 对于简单任务（单步可完成），直接使用工具回答
3. 对于复杂任务（需要多步骤），使用 create_plan 工具创建执行计划
4. 然后按计划逐步执行，每个步骤使用 ReAct 循环（推理-行动-观察）

你可以使用工具来帮助完成任务。
当你不确定时，请使用工具来获取信息，而不是编造答案。
请用中文回复。`,
		MaxTurns: 10,
	}
}

// ConfigWithModel 返回带模型信息的配置
func ConfigWithModel(model string) Config {
	config := DefaultConfig()
	if model != "" {
		config.SystemPrompt += fmt.Sprintf("\n\n你当前使用的模型是 %s。", model)
	}
	return config
}
