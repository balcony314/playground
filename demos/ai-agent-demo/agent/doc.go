// Package agent 实现了基于 Plan + ReAct 两阶段执行模式的 AI Agent 框架。
//
// # 核心概念
//
// Agent 采用两阶段执行模式：
//   - Plan 阶段：LLM 分析任务复杂度，复杂任务生成执行计划
//   - Execute 阶段：对计划中的每个步骤执行 ReAct 循环（推理 → 行动 → 观察）
//
// 简单任务跳过 Plan，直接进入 ReAct 循环。
//
// # 架构
//
//	用户消息
//	   ↓
//	Plan 阶段 (planPhase)
//	   LLM 判断复杂度 → 简单? 直接 ReAct / 复杂? 生成 Plan
//	   ↓
//	Execute 阶段 (executeStep → reactLoop)
//	   逐步骤执行 ReAct 循环: LLM → tool_calls → 执行工具 → 结果回传
//	   ↓
//	汇总结果 (summarizeResults) → 最终答案
//
// # 关键类型
//
//   - [Message]: 对话消息，对齐 OpenAI chat 格式
//   - [Tool]: 工具定义 = JSON Schema + Execute 函数
//   - [LLMClient]: LLM 接口，支持 OpenAIClient 和 MockClient
//   - [Skill]: 预设角色配置（SystemPrompt + 工具列表）
//   - [Plan] / [Step]: 执行计划及步骤
//   - [Agent]: 编排核心，组合 LLM + Tools + Skills
//
// # 快速开始
//
//	llm := agent.NewMockClient()
//	config := agent.DefaultConfig()
//	a := agent.NewAgent(llm, config)
//	reply, err := a.Run("你好")
//
// # 扩展
//
//   - 添加工具：在 RegisterBuiltinTools() 中注册 [Tool]
//   - 添加技能：在 RegisterBuiltinSkills() 中注册 [Skill]
//   - 替换 LLM：实现 [LLMClient] 接口
package agent
