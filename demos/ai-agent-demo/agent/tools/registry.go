package tools

// ═══════════════════════════════════════════════════════════════
// registry.go — 工具注册表
// ═══════════════════════════════════════════════════════════════
//
// 【教学要点】什么是工具（Tool）？
//
// 工具是 Agent 与外部世界交互的接口。
// LLM 本身只能生成文本，但通过工具它可以：
//   - 执行计算（calculator）
//   - 获取时间（current_time）
//   - 搜索信息（search）→ 这就是 RAG 的基础
//   - 操作文件、数据库、API...
//
// 工具系统的工作流程：
//   1. 开发者定义工具的 schema（名字、描述、参数）
//   2. Schema 发送给 LLM，LLM 决定什么时候用哪个工具
//   3. LLM 返回工具调用请求（name + arguments）
//   4. Agent 执行工具，拿到结果
//   5. 结果放回对话，LLM 继续推理
//
// 这就是 Function Calling 的核心机制！

import (
	"ai-agent-demo/agent/types"
)

// ToolRegistry 管理所有可用工具
type ToolRegistry struct {
	tools map[string]types.Tool
}

// NewToolRegistry 创建一个新的工具注册表
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]types.Tool),
	}
}

// Register 注册一个工具
func (r *ToolRegistry) Register(tool types.Tool) {
	r.tools[tool.Definition.Function.Name] = tool
}

// Get 根据名称获取工具
func (r *ToolRegistry) Get(name string) (types.Tool, bool) {
	tool, ok := r.tools[name]
	return tool, ok
}

// Definitions 获取所有工具的 schema（发给 LLM 用）
func (r *ToolRegistry) Definitions() []types.ToolDefinition {
	defs := make([]types.ToolDefinition, 0, len(r.tools))
	for _, tool := range r.tools {
		defs = append(defs, tool.Definition)
	}
	return defs
}

// Names 获取所有工具名（日志用）
func (r *ToolRegistry) Names() []string {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	return names
}
