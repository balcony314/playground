package agent

import (
	"ai-agent-demo/agent/types"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// ─── Mock LLMClient（用于测试 Agent.Run）──────────────────────

// scriptedClient 按预设脚本返回响应的 mock LLM
type scriptedClient struct {
	responses []*types.Message // 预设的响应队列
	callIdx   int             // 当前调用索引
}

func (c *scriptedClient) Chat(messages []types.Message, tools []types.ToolDefinition) (*types.Message, error) {
	if c.callIdx >= len(c.responses) {
		return nil, fmt.Errorf("scriptedClient: 预设响应已用完 (callIdx=%d)", c.callIdx)
	}
	resp := c.responses[c.callIdx]
	c.callIdx++
	return resp, nil
}

// errorClient 总是返回错误的 mock LLM
type errorClient struct{}

func (c *errorClient) Chat(messages []types.Message, tools []types.ToolDefinition) (*types.Message, error) {
	return nil, fmt.Errorf("模拟 LLM 错误")
}

// ─── Agent.Run 测试 ───────────────────────────────────────────

func TestAgentRun_DirectReply(t *testing.T) {
	// LLM 直接回复文本，不调用工具
	// 需要两个响应：1. planPhase 返回 SIMPLE 2. reactLoop 直接回复
	client := &scriptedClient{
		responses: []*types.Message{
			// planPhase: 判断为简单任务
			{Role: types.RoleAssistant, Content: "SIMPLE"},
			// reactLoop: 直接回复
			{Role: types.RoleAssistant, Content: "你好！"},
		},
	}

	agent := NewAgent(client, types.DefaultConfig())
	result, err := agent.Run("你好")
	if err != nil {
		t.Fatalf("Run 错误: %v", err)
	}
	if result != "你好！" {
		t.Errorf("结果 = %q, 期望 %q", result, "你好！")
	}
}

func TestAgentRun_ToolCallThenReply(t *testing.T) {
	// planPhase 判断为简单任务，reactLoop 中 LLM 调用 search 工具后回复
	client := &scriptedClient{
		responses: []*types.Message{
			// planPhase: 判断为简单任务
			{Role: types.RoleAssistant, Content: "SIMPLE"},
			// reactLoop 第一轮：LLM 决定调用 search
			{
				Role:    types.RoleAssistant,
				Content: "",
				ToolCalls: []types.ToolCall{
					{
						ID:   "call_1",
						Type: "function",
						Function: types.FunctionCall{
							Name:      "search",
							Arguments: `{"query":"golang"}`,
						},
					},
				},
			},
			// reactLoop 第二轮：LLM 看到工具结果后回复
			{Role: types.RoleAssistant, Content: "Go 是 Google 开发的编程语言。"},
		},
	}

	agent := NewAgent(client, types.DefaultConfig())
	result, err := agent.Run("什么是 Go 语言？")
	if err != nil {
		t.Fatalf("Run 错误: %v", err)
	}
	if result != "Go 是 Google 开发的编程语言。" {
		t.Errorf("结果 = %q, 期望 %q", result, "Go 是 Google 开发的编程语言。")
	}

	// 验证对话历史：system + user(原始输入) + user(reactLoop任务提示) + assistant(tool_call) + tool_result + assistant(reply)
	// 注意：planPhase 使用临时历史，不会修改 a.history
	history := agent.GetHistory()
	if len(history) != 6 {
		t.Errorf("历史长度 = %d, 期望 6", len(history))
	}
	// 第4条应是 tool result
	if history[4].Role != types.RoleTool {
		t.Errorf("历史[4] role = %q, 期望 %q", history[4].Role, types.RoleTool)
	}
	if history[4].ToolCallID != "call_1" {
		t.Errorf("历史[4] ToolCallID = %q, 期望 %q", history[4].ToolCallID, "call_1")
	}
}

func TestAgentRun_MultipleToolCalls(t *testing.T) {
	// LLM 连续调用多个工具
	client := &scriptedClient{
		responses: []*types.Message{
			// 第一轮：调用 calculator
			{
				Role: types.RoleAssistant,
				ToolCalls: []types.ToolCall{
					{
						ID:   "call_calc",
						Type: "function",
						Function: types.FunctionCall{
							Name:      "calculator",
							Arguments: `{"expression":"2+3"}`,
						},
					},
				},
			},
			// 第二轮：调用 search
			{
				Role: types.RoleAssistant,
				ToolCalls: []types.ToolCall{
					{
						ID:   "call_search",
						Type: "function",
						Function: types.FunctionCall{
							Name:      "search",
							Arguments: `{"query":"agent"}`,
						},
					},
				},
			},
			// 第三轮：最终回复
			{Role: types.RoleAssistant, Content: "计算和搜索都完成了。"},
		},
	}

	agent := NewAgent(client, types.DefaultConfig())
	result, err := agent.Run("帮我算 2+3 并搜索 agent")
	if err != nil {
		t.Fatalf("Run 错误: %v", err)
	}
	if result != "计算和搜索都完成了。" {
		t.Errorf("结果 = %q", result)
	}
}

func TestAgentRun_LLMError(t *testing.T) {
	client := &errorClient{}
	agent := NewAgent(client, types.DefaultConfig())

	_, err := agent.Run("你好")
	if err == nil {
		t.Fatal("LLM 错误时 Run 应返回错误")
	}
	// 新流程中，错误发生在 planPhase 阶段
	if !strings.Contains(err.Error(), "计划生成失败") {
		t.Errorf("错误信息 = %q, 期望包含 '计划生成失败'", err.Error())
	}
}

func TestAgentRun_UnknownTool(t *testing.T) {
	// LLM 调用一个不存在的工具
	client := &scriptedClient{
		responses: []*types.Message{
			// planPhase: 判断为简单任务
			{Role: types.RoleAssistant, Content: "SIMPLE"},
			// reactLoop: LLM 调用不存在的工具
			{
				Role: types.RoleAssistant,
				ToolCalls: []types.ToolCall{
					{
						ID:   "call_bad",
						Type: "function",
						Function: types.FunctionCall{
							Name:      "nonexistent_tool",
							Arguments: `{}`,
						},
					},
				},
			},
			// reactLoop: Agent 会把工具错误喂回 LLM，LLM 再回复
			{Role: types.RoleAssistant, Content: "抱歉，工具调用失败了。"},
		},
	}

	agent := NewAgent(client, types.DefaultConfig())
	result, err := agent.Run("测试")
	if err != nil {
		t.Fatalf("Run 错误: %v", err)
	}

	// 工具错误应被记录到历史中，LLM 收到后给出最终回复
	// 注意：planPhase 使用临时历史，不会修改 a.history
	history := agent.GetHistory()
	toolMsg := history[4] // system + user(原始输入) + user(reactLoop任务提示) + assistant(tool_call) + tool_result
	if toolMsg.Role != types.RoleTool {
		t.Errorf("工具结果角色 = %q, 期望 %q", toolMsg.Role, types.RoleTool)
	}
	if !strings.Contains(toolMsg.Content, "未知工具") {
		t.Errorf("工具错误信息 = %q, 期望包含 '未知工具'", toolMsg.Content)
	}
	_ = result
}

func TestAgentRun_MaxTurnsExceeded(t *testing.T) {
	// LLM 每轮都调用工具，永不给出最终回复
	// 需要先通过 planPhase
	responses := []*types.Message{
		// planPhase: 判断为简单任务
		{Role: types.RoleAssistant, Content: "SIMPLE"},
	}
	// 添加足够多的工具调用响应
	for i := 0; i < 20; i++ {
		responses = append(responses, &types.Message{
			Role: types.RoleAssistant,
			ToolCalls: []types.ToolCall{
				{
					ID:   fmt.Sprintf("call_%d", i),
					Type: "function",
					Function: types.FunctionCall{
						Name:      "calculator",
						Arguments: `{"expression":"1+1"}`,
					},
				},
			},
		})
	}

	client := &scriptedClient{
		responses: responses,
	}

	config := types.DefaultConfig()
	config.MaxTurns = 3
	agent := NewAgent(client, config)

	result, err := agent.Run("无限循环测试")
	if err == nil {
		t.Fatal("超过最大轮数应返回错误")
	}
	if !strings.Contains(err.Error(), "最大推理轮数") {
		t.Errorf("错误 = %q, 期望包含 '最大推理轮数'", err.Error())
	}
	if result != "" {
		t.Errorf("结果 = %q, 期望空字符串", result)
	}
}

// ─── Agent.executeTool 测试 ───────────────────────────────────

func TestAgentExecuteTool(t *testing.T) {
	client := &scriptedClient{}
	agent := NewAgent(client, types.DefaultConfig())

	// 正常执行
	result, err := agent.executeTool("calculator", `{"expression":"2+3"}`)
	if err != nil {
		t.Fatalf("executeTool 错误: %v", err)
	}
	if result == "" {
		t.Error("结果不应为空")
	}

	// 未知工具
	_, err = agent.executeTool("nonexistent", `{}`)
	if err == nil {
		t.Error("未知工具应返回错误")
	}
}

// ─── Agent.SwitchSkill 测试 ───────────────────────────────────

func TestAgentSwitchSkill(t *testing.T) {
	client := &scriptedClient{}
	agent := NewAgent(client, types.DefaultConfig())

	if agent.CurrentSkill() != "general" {
		t.Fatalf("默认技能 = %q, 期望 %q", agent.CurrentSkill(), "general")
	}

	// 切换到 coder
	err := agent.SwitchSkill("coder")
	if err != nil {
		t.Fatalf("SwitchSkill 错误: %v", err)
	}
	if agent.CurrentSkill() != "coder" {
		t.Errorf("当前技能 = %q, 期望 %q", agent.CurrentSkill(), "coder")
	}

	// 未知技能
	err = agent.SwitchSkill("nonexistent")
	if err == nil {
		t.Error("切换到未知技能应返回错误")
	}
}

func TestAgentSwitchSkill_ClearsHistory(t *testing.T) {
	client := &scriptedClient{
		responses: []*types.Message{
			// planPhase: 判断为简单任务
			{Role: types.RoleAssistant, Content: "SIMPLE"},
			// reactLoop: 回复
			{Role: types.RoleAssistant, Content: "回复"},
		},
	}
	agent := NewAgent(client, types.DefaultConfig())

	// 先对话一轮
	agent.Run("你好")
	before := len(agent.GetHistory())
	if before < 2 {
		t.Fatalf("对话后历史长度 = %d, 期望 >= 2", before)
	}

	// 切换技能应清空历史
	agent.SwitchSkill("coder")
	after := len(agent.GetHistory())
	if after != 1 {
		t.Errorf("切换技能后历史长度 = %d, 期望 1 (仅 system prompt)", after)
	}
}

// ─── Agent.getSkillTools 测试 ─────────────────────────────────

func TestAgentGetSkillTools_AllTools(t *testing.T) {
	client := &scriptedClient{}
	agent := NewAgent(client, types.DefaultConfig())

	// general 技能：使用全部工具（calculator, current_time, search, text_transform, create_plan, 8个文件工具, 2个执行工具）
	defs := agent.getSkillTools()
	if len(defs) != 15 {
		t.Errorf("general 技能工具数 = %d, 期望 15", len(defs))
	}
}

func TestAgentGetSkillTools_FilteredTools(t *testing.T) {
	client := &scriptedClient{}
	agent := NewAgent(client, types.DefaultConfig())

	agent.SwitchSkill("coder") // 只有 calculator, text_transform
	defs := agent.getSkillTools()
	if len(defs) != 2 {
		t.Errorf("coder 技能工具数 = %d, 期望 2", len(defs))
	}

	names := map[string]bool{}
	for _, d := range defs {
		names[d.Function.Name] = true
	}
	if !names["calculator"] || !names["text_transform"] {
		t.Errorf("coder 工具 = %v, 期望 calculator 和 text_transform", names)
	}
}

// ─── Agent.ClearHistory 测试 ──────────────────────────────────

func TestAgentClearHistory(t *testing.T) {
	client := &scriptedClient{
		responses: []*types.Message{
			// planPhase: 判断为简单任务
			{Role: types.RoleAssistant, Content: "SIMPLE"},
			// reactLoop: 回复
			{Role: types.RoleAssistant, Content: "回复"},
		},
	}
	agent := NewAgent(client, types.DefaultConfig())
	agent.Run("你好")

	if len(agent.GetHistory()) < 2 {
		t.Fatal("对话后历史应有多条消息")
	}

	agent.ClearHistory()
	if len(agent.GetHistory()) != 1 {
		t.Errorf("清空后历史长度 = %d, 期望 1", len(agent.GetHistory()))
	}
	if agent.GetHistory()[0].Role != types.RoleSystem {
		t.Errorf("清空后第一条消息角色 = %q, 期望 %q", agent.GetHistory()[0].Role, types.RoleSystem)
	}
}

// ─── Agent.ListTools 测试 ─────────────────────────────────────

func TestAgentListTools(t *testing.T) {
	client := &scriptedClient{}
	agent := NewAgent(client, types.DefaultConfig())

	// general 技能：Tools 为空，返回全部工具
	tools := agent.ListTools()
	if len(tools) == 0 {
		t.Error("ListTools 不应返回空")
	}

	// 切换到 coder 技能：返回指定工具
	agent.SwitchSkill("coder")
	tools = agent.ListTools()
	if len(tools) != 2 {
		t.Errorf("coder 技能工具数 = %d, 期望 2", len(tools))
	}
}

// ─── Agent.ListSkills 测试 ────────────────────────────────────

func TestAgentListSkills(t *testing.T) {
	client := &scriptedClient{}
	agent := NewAgent(client, types.DefaultConfig())

	skillsList := agent.ListSkills()
	if len(skillsList) != 5 {
		t.Errorf("ListSkills 长度 = %d, 期望 5", len(skillsList))
	}
}

// ─── MockClient 测试 ─────────────────────────────────────────

func TestMockClient_DirectReply(t *testing.T) {
	client := NewMockClient()
	messages := []types.Message{
		{Role: types.RoleSystem, Content: "你是助手"},
		{Role: types.RoleUser, Content: "你好"},
	}

	resp, err := client.Chat(messages, nil)
	if err != nil {
		t.Fatalf("Chat 错误: %v", err)
	}
	if resp.Role != types.RoleAssistant {
		t.Errorf("角色 = %q, 期望 %q", resp.Role, types.RoleAssistant)
	}
	if resp.Content == "" {
		t.Error("回复内容不应为空")
	}
}

func TestMockClient_ToolCall(t *testing.T) {
	client := NewMockClient()
	messages := []types.Message{
		{Role: types.RoleSystem, Content: "你是助手"},
		{Role: types.RoleUser, Content: "搜索一下"},
	}
	tools := []types.ToolDefinition{
		{Type: "function", Function: types.FunctionSchema{Name: "search"}},
	}

	resp, err := client.Chat(messages, tools)
	if err != nil {
		t.Fatalf("Chat 错误: %v", err)
	}
	if len(resp.ToolCalls) == 0 {
		t.Error("有工具时应返回 tool_calls")
	}
}

func TestMockClient_SummarizeToolResult(t *testing.T) {
	client := NewMockClient()
	messages := []types.Message{
		{Role: types.RoleSystem, Content: "你是助手"},
		{Role: types.RoleUser, Content: "搜索"},
		{Role: types.RoleTool, Content: "搜索结果内容"},
	}

	resp, err := client.Chat(messages, nil)
	if err != nil {
		t.Fatalf("Chat 错误: %v", err)
	}
	if !strings.Contains(resp.Content, "工具返回的结果") {
		t.Errorf("总结回复 = %q, 期望包含 '工具返回的结果'", resp.Content)
	}
}

// ─── NewAgent 默认配置测试 ────────────────────────────────────

func TestNewAgent_DefaultConfig(t *testing.T) {
	client := &scriptedClient{}
	config := types.DefaultConfig()
	agent := NewAgent(client, config)

	if agent.CurrentSkill() != "general" {
		t.Errorf("默认技能 = %q, 期望 %q", agent.CurrentSkill(), "general")
	}

	history := agent.GetHistory()
	if len(history) != 1 {
		t.Errorf("初始历史长度 = %d, 期望 1", len(history))
	}
	if history[0].Role != types.RoleSystem {
		t.Errorf("初始消息角色 = %q, 期望 %q", history[0].Role, types.RoleSystem)
	}
}

// ─── 边界情况：空工具调用参数 ─────────────────────────────────

func TestAgentRun_EmptyToolArgs(t *testing.T) {
	client := &scriptedClient{
		responses: []*types.Message{
			// planPhase: 判断为简单任务
			{Role: types.RoleAssistant, Content: "SIMPLE"},
			// reactLoop: 调用 current_time 工具
			{
				Role: types.RoleAssistant,
				ToolCalls: []types.ToolCall{
					{
						ID:       "call_1",
						Type:     "function",
						Function: types.FunctionCall{Name: "current_time", Arguments: `{}`},
					},
				},
			},
			// reactLoop: 回复
			{Role: types.RoleAssistant, Content: "现在时间是..."},
		},
	}

	agent := NewAgent(client, types.DefaultConfig())
	result, err := agent.Run("几点了")
	if err != nil {
		t.Fatalf("Run 错误: %v", err)
	}
	if result == "" {
		t.Error("结果不应为空")
	}
}

// ─── 边界情况：多个并行工具调用 ──────────────────────────────

func TestAgentRun_ParallelToolCalls(t *testing.T) {
	client := &scriptedClient{
		responses: []*types.Message{
			// planPhase: 判断为简单任务
			{Role: types.RoleAssistant, Content: "SIMPLE"},
			// reactLoop: LLM 一次返回多个 tool_calls
			{
				Role: types.RoleAssistant,
				ToolCalls: []types.ToolCall{
					{
						ID: "call_1", Type: "function",
						Function: types.FunctionCall{Name: "calculator", Arguments: `{"expression":"1+1"}`},
					},
					{
						ID: "call_2", Type: "function",
						Function: types.FunctionCall{Name: "calculator", Arguments: `{"expression":"2+2"}`},
					},
				},
			},
			// reactLoop: 回复
			{Role: types.RoleAssistant, Content: "1+1=2, 2+2=4"},
		},
	}

	agent := NewAgent(client, types.DefaultConfig())
	result, err := agent.Run("算一下 1+1 和 2+2")
	if err != nil {
		t.Fatalf("Run 错误: %v", err)
	}

	// 历史中应有 2 条 tool result
	history := agent.GetHistory()
	toolResults := 0
	for _, m := range history {
		if m.Role == types.RoleTool {
			toolResults++
		}
	}
	if toolResults != 2 {
		t.Errorf("工具结果数 = %d, 期望 2", toolResults)
	}
	_ = result
}

// ─── DefaultConfig 测试 ──────────────────────────────────────

func TestDefaultConfig(t *testing.T) {
	config := types.DefaultConfig()
	if config.MaxTurns != 10 {
		t.Errorf("MaxTurns = %d, 期望 10", config.MaxTurns)
	}
	if config.SystemPrompt == "" {
		t.Error("SystemPrompt 不应为空")
	}
}

// ─── 计划功能测试 ──────────────────────────────────────────────

func TestSetPlan(t *testing.T) {
	client := &scriptedClient{}
	agent := NewAgent(client, types.DefaultConfig())

	plan := types.Plan{
		Goal: "测试目标",
		Steps: []types.Step{
			{Description: "步骤一"},
			{Description: "步骤二"},
			{Description: "步骤三"},
		},
	}

	agent.setPlan(plan)

	if agent.currentPlan == nil {
		t.Fatal("setPlan 后 currentPlan 不应为 nil")
	}
	if agent.currentPlan.Goal != "测试目标" {
		t.Errorf("Goal = %q, 期望 %q", agent.currentPlan.Goal, "测试目标")
	}
	if len(agent.currentPlan.Steps) != 3 {
		t.Errorf("Steps 长度 = %d, 期望 3", len(agent.currentPlan.Steps))
	}
	// 验证 ID 和 Status 被正确设置
	for i, step := range agent.currentPlan.Steps {
		if step.ID != i+1 {
			t.Errorf("Step[%d].ID = %d, 期望 %d", i, step.ID, i+1)
		}
		if step.Status != "pending" {
			t.Errorf("Step[%d].Status = %q, 期望 %q", i, step.Status, "pending")
		}
	}
}

func TestCreatePlanTool(t *testing.T) {
	client := &scriptedClient{}
	agent := NewAgent(client, types.DefaultConfig())

	// 验证 create_plan 工具已注册
	tool, ok := agent.registry.Get("create_plan")
	if !ok {
		t.Fatal("create_plan 工具应已注册")
	}
	if tool.Definition.Function.Name != "create_plan" {
		t.Errorf("工具名 = %q, 期望 %q", tool.Definition.Function.Name, "create_plan")
	}

	// 执行 create_plan 工具
	planJSON := `{
		"goal": "测试计划",
		"steps": [
			{"description": "第一步"},
			{"description": "第二步"}
		]
	}`
	result, err := tool.Execute(json.RawMessage(planJSON))
	if err != nil {
		t.Fatalf("执行 create_plan 错误: %v", err)
	}

	// 验证结果包含计划信息
	if !strings.Contains(result, "测试计划") {
		t.Errorf("结果应包含目标名，实际: %q", result)
	}
	if !strings.Contains(result, "第一步") {
		t.Errorf("结果应包含步骤描述，实际: %q", result)
	}

	// 验证计划已被设置
	if agent.currentPlan == nil {
		t.Fatal("create_plan 后 currentPlan 不应为 nil")
	}
	if agent.currentPlan.Goal != "测试计划" {
		t.Errorf("计划目标 = %q, 期望 %q", agent.currentPlan.Goal, "测试计划")
	}
}

func TestCreatePlanTool_InvalidJSON(t *testing.T) {
	client := &scriptedClient{}
	agent := NewAgent(client, types.DefaultConfig())

	tool, _ := agent.registry.Get("create_plan")

	// 无效的 JSON
	_, err := tool.Execute(json.RawMessage(`{invalid json`))
	if err == nil {
		t.Error("无效 JSON 应返回错误")
	}
	if !strings.Contains(err.Error(), "计划解析失败") {
		t.Errorf("错误信息 = %q, 期望包含 '计划解析失败'", err.Error())
	}
}

// ─── executeCreatePlan 测试 ─────────────────────────────────────

func TestExecuteCreatePlan_ValidJSON(t *testing.T) {
	client := &scriptedClient{}
	agent := NewAgent(client, types.DefaultConfig())

	argsJSON := `{
		"goal": "分析代码",
		"steps": [
			{"description": "读取文件"},
			{"description": "分析逻辑"},
			{"description": "给出建议"}
		]
	}`

	plan, err := agent.executeCreatePlan(argsJSON)
	if err != nil {
		t.Fatalf("executeCreatePlan 错误: %v", err)
	}
	if plan.Goal != "分析代码" {
		t.Errorf("Goal = %q, 期望 %q", plan.Goal, "分析代码")
	}
	if len(plan.Steps) != 3 {
		t.Fatalf("Steps 长度 = %d, 期望 3", len(plan.Steps))
	}
	// 验证 ID 和 Status 被正确设置
	for i, step := range plan.Steps {
		if step.ID != i+1 {
			t.Errorf("Step[%d].ID = %d, 期望 %d", i, step.ID, i+1)
		}
		if step.Status != "pending" {
			t.Errorf("Step[%d].Status = %q, 期望 %q", i, step.Status, "pending")
		}
		if step.Description == "" {
			t.Errorf("Step[%d].Description 不应为空", i)
		}
	}
}

func TestExecuteCreatePlan_InvalidJSON(t *testing.T) {
	client := &scriptedClient{}
	agent := NewAgent(client, types.DefaultConfig())

	_, err := agent.executeCreatePlan(`{invalid json}`)
	if err == nil {
		t.Error("无效 JSON 应返回错误")
	}
	if !strings.Contains(err.Error(), "计划解析失败") {
		t.Errorf("错误 = %q, 期望包含 '计划解析失败'", err.Error())
	}
}

func TestExecuteCreatePlan_EmptySteps(t *testing.T) {
	client := &scriptedClient{}
	agent := NewAgent(client, types.DefaultConfig())

	argsJSON := `{"goal": "空计划", "steps": []}`
	plan, err := agent.executeCreatePlan(argsJSON)
	if err != nil {
		t.Fatalf("executeCreatePlan 错误: %v", err)
	}
	if len(plan.Steps) != 0 {
		t.Errorf("Steps 长度 = %d, 期望 0", len(plan.Steps))
	}
}

// ─── executeStep 测试 ───────────────────────────────────────────

func TestExecuteStep(t *testing.T) {
	client := &scriptedClient{
		responses: []*types.Message{
			// reactLoop: 直接回复
			{Role: types.RoleAssistant, Content: "步骤完成"},
		},
	}
	agent := NewAgent(client, types.DefaultConfig())

	step := types.Step{ID: 1, Description: "读取文件", Status: "pending"}
	result, err := agent.executeStep(step, 1, 3)
	if err != nil {
		t.Fatalf("executeStep 错误: %v", err)
	}
	if result != "步骤完成" {
		t.Errorf("结果 = %q, 期望 %q", result, "步骤完成")
	}
}

func TestExecuteStep_WithToolCall(t *testing.T) {
	client := &scriptedClient{
		responses: []*types.Message{
			// reactLoop: 调用工具
			{
				Role: types.RoleAssistant,
				ToolCalls: []types.ToolCall{
					{
						ID:       "call_1",
						Type:     "function",
						Function: types.FunctionCall{Name: "calculator", Arguments: `{"expression":"1+1"}`},
					},
				},
			},
			// reactLoop: 工具结果后回复
			{Role: types.RoleAssistant, Content: "计算结果是 2"},
		},
	}
	agent := NewAgent(client, types.DefaultConfig())

	step := types.Step{ID: 1, Description: "计算表达式", Status: "pending"}
	result, err := agent.executeStep(step, 1, 2)
	if err != nil {
		t.Fatalf("executeStep 错误: %v", err)
	}
	if result != "计算结果是 2" {
		t.Errorf("结果 = %q, 期望 %q", result, "计算结果是 2")
	}
}

func TestExecuteStep_LLMError(t *testing.T) {
	client := &errorClient{}
	agent := NewAgent(client, types.DefaultConfig())

	step := types.Step{ID: 1, Description: "会失败的步骤", Status: "pending"}
	_, err := agent.executeStep(step, 1, 1)
	if err == nil {
		t.Error("LLM 错误时应返回错误")
	}
}

// ─── summarizeResults 测试 ──────────────────────────────────────

func TestSummarizeResults(t *testing.T) {
	client := &scriptedClient{
		responses: []*types.Message{
			// summarizeResults 的 LLM 调用
			{Role: types.RoleAssistant, Content: "最终汇总答案"},
		},
	}
	agent := NewAgent(client, types.DefaultConfig())

	plan := &types.Plan{
		Goal: "测试目标",
		Steps: []types.Step{
			{ID: 1, Description: "步骤一"},
			{ID: 2, Description: "步骤二"},
		},
	}
	stepResults := []string{"结果一", "结果二"}

	result, err := agent.summarizeResults(plan, stepResults)
	if err != nil {
		t.Fatalf("summarizeResults 错误: %v", err)
	}
	if result != "最终汇总答案" {
		t.Errorf("结果 = %q, 期望 %q", result, "最终汇总答案")
	}
}

func TestSummarizeResults_LLMError(t *testing.T) {
	client := &errorClient{}
	agent := NewAgent(client, types.DefaultConfig())

	plan := &types.Plan{
		Goal: "测试目标",
		Steps: []types.Step{
			{ID: 1, Description: "步骤一"},
		},
	}
	stepResults := []string{"结果一"}

	_, err := agent.summarizeResults(plan, stepResults)
	if err == nil {
		t.Error("LLM 错误时应返回错误")
	}
	if !strings.Contains(err.Error(), "LLM 调用失败") {
		t.Errorf("错误 = %q, 期望包含 'LLM 调用失败'", err.Error())
	}
}

// ─── Run 带计划的完整流程测试 ───────────────────────────────────

func TestRun_WithPlan(t *testing.T) {
	// 复杂任务：planPhase 返回 create_plan，然后逐步执行
	client := &scriptedClient{
		responses: []*types.Message{
			// planPhase: LLM 调用 create_plan
			{
				Role: types.RoleAssistant,
				ToolCalls: []types.ToolCall{
					{
						ID:   "call_plan",
						Type: "function",
						Function: types.FunctionCall{
							Name:      "create_plan",
							Arguments: `{"goal":"分析代码","steps":[{"description":"读取文件"},{"description":"分析逻辑"}]}`,
						},
					},
				},
			},
			// 步骤 1 的 reactLoop: 直接回复
			{Role: types.RoleAssistant, Content: "文件内容已读取"},
			// 步骤 2 的 reactLoop: 直接回复
			{Role: types.RoleAssistant, Content: "逻辑分析完成"},
			// summarizeResults 的 LLM 调用
			{Role: types.RoleAssistant, Content: "最终分析报告"},
		},
	}

	agent := NewAgent(client, types.DefaultConfig())
	result, err := agent.Run("帮我分析这段代码")
	if err != nil {
		t.Fatalf("Run 错误: %v", err)
	}
	if result != "最终分析报告" {
		t.Errorf("结果 = %q, 期望 %q", result, "最终分析报告")
	}
}

func TestRun_WithPlanStepError(t *testing.T) {
	// 计划中某个步骤超过最大轮数，但后续步骤继续执行
	responses := []*types.Message{
		// planPhase: LLM 调用 create_plan
		{
			Role: types.RoleAssistant,
			ToolCalls: []types.ToolCall{
				{
					ID:   "call_plan",
					Type: "function",
					Function: types.FunctionCall{
						Name:      "create_plan",
						Arguments: `{"goal":"测试","steps":[{"description":"会失败的步骤"},{"description":"正常步骤"}]}`,
					},
				},
			},
		},
	}
	// 步骤 1: reactLoop 每轮都调用工具，超过 MaxTurns=3
	for i := 0; i < 4; i++ {
		responses = append(responses, &types.Message{
			Role: types.RoleAssistant,
			ToolCalls: []types.ToolCall{
				{ID: fmt.Sprintf("c%d", i), Type: "function", Function: types.FunctionCall{Name: "calculator", Arguments: `{"expression":"1"}`}},
			},
		})
	}
	// 步骤 2 + summarizeResults
	responses = append(responses,
		&types.Message{Role: types.RoleAssistant, Content: "步骤 2 完成"},
		&types.Message{Role: types.RoleAssistant, Content: "汇总结果"},
	)

	client := &scriptedClient{responses: responses}

	config := types.DefaultConfig()
	config.MaxTurns = 3
	agent := NewAgent(client, config)

	result, err := agent.Run("测试")
	if err != nil {
		t.Fatalf("Run 错误: %v", err)
	}
	if result != "汇总结果" {
		t.Errorf("结果 = %q, 期望 %q", result, "汇总结果")
	}
}

func TestRun_SummaryError(t *testing.T) {
	// planPhase 成功，但 summarizeResults 时预设响应已用完
	client := &scriptedClient{
		responses: []*types.Message{
			// planPhase: LLM 调用 create_plan
			{
				Role: types.RoleAssistant,
				ToolCalls: []types.ToolCall{
					{
						ID:   "call_plan",
						Type: "function",
						Function: types.FunctionCall{
							Name:      "create_plan",
							Arguments: `{"goal":"测试","steps":[{"description":"步骤一"}]}`,
						},
					},
				},
			},
			// 步骤 1: reactLoop 完成
			{Role: types.RoleAssistant, Content: "完成"},
			// 这之后 summarizeResults 会调用 LLM，但预设响应已用完 → 报错
		},
	}

	agent := NewAgent(client, types.DefaultConfig())
	_, err := agent.Run("测试")
	// summarizeResults 调用 LLM 时预设响应已用完，应报错
	if err == nil {
		t.Error("summarize 失败时 Run 应返回错误")
	}
}

func TestRun_PlanPhaseError(t *testing.T) {
	// planPhase 阶段 LLM 报错
	client := &errorClient{}
	agent := NewAgent(client, types.DefaultConfig())

	_, err := agent.Run("你好")
	if err == nil {
		t.Error("planPhase 失败时 Run 应返回错误")
	}
	if !strings.Contains(err.Error(), "计划生成失败") {
		t.Errorf("错误 = %q, 期望包含 '计划生成失败'", err.Error())
	}
}

// ─── planPhase 测试 ─────────────────────────────────────────────

func TestPlanPhase_WithCreatePlanToolCall(t *testing.T) {
	// planPhase 中 LLM 调用 create_plan 工具
	client := &scriptedClient{
		responses: []*types.Message{
			{
				Role: types.RoleAssistant,
				ToolCalls: []types.ToolCall{
					{
						ID:   "call_plan",
						Type: "function",
						Function: types.FunctionCall{
							Name:      "create_plan",
							Arguments: `{"goal":"多步任务","steps":[{"description":"步骤A"},{"description":"步骤B"}]}`,
						},
					},
				},
			},
		},
	}

	agent := NewAgent(client, types.DefaultConfig())
	// 需要先添加用户消息到历史（模拟 Run 的行为）
	agent.history = append(agent.history, types.Message{Role: types.RoleUser, Content: "复杂任务"})

	plan, err := agent.planPhase()
	if err != nil {
		t.Fatalf("planPhase 错误: %v", err)
	}
	if plan == nil {
		t.Fatal("plan 不应为 nil")
	}
	if plan.Goal != "多步任务" {
		t.Errorf("Goal = %q, 期望 %q", plan.Goal, "多步任务")
	}
	if len(plan.Steps) != 2 {
		t.Errorf("Steps 长度 = %d, 期望 2", len(plan.Steps))
	}
}

func TestPlanPhase_SimpleTask(t *testing.T) {
	// planPhase 中 LLM 回复 SIMPLE
	client := &scriptedClient{
		responses: []*types.Message{
			{Role: types.RoleAssistant, Content: "SIMPLE"},
		},
	}

	agent := NewAgent(client, types.DefaultConfig())
	agent.history = append(agent.history, types.Message{Role: types.RoleUser, Content: "简单问题"})

	plan, err := agent.planPhase()
	if err != nil {
		t.Fatalf("planPhase 错误: %v", err)
	}
	if plan != nil {
		t.Error("简单任务 plan 应为 nil")
	}
}

func TestPlanPhase_NonSimpleText(t *testing.T) {
	// planPhase 中 LLM 回复非 "SIMPLE" 的文本（也视为简单任务）
	client := &scriptedClient{
		responses: []*types.Message{
			{Role: types.RoleAssistant, Content: "好的，我来回答"},
		},
	}

	agent := NewAgent(client, types.DefaultConfig())
	agent.history = append(agent.history, types.Message{Role: types.RoleUser, Content: "你好"})

	plan, err := agent.planPhase()
	if err != nil {
		t.Fatalf("planPhase 错误: %v", err)
	}
	if plan != nil {
		t.Error("非 SIMPLE 文本也应返回 nil plan")
	}
}

func TestPlanPhase_LLMError(t *testing.T) {
	client := &errorClient{}
	agent := NewAgent(client, types.DefaultConfig())
	agent.history = append(agent.history, types.Message{Role: types.RoleUser, Content: "测试"})

	_, err := agent.planPhase()
	if err == nil {
		t.Error("LLM 错误时 planPhase 应返回错误")
	}
}

func TestPlanPhase_UnknownToolCall(t *testing.T) {
	// planPhase 中 LLM 调用了非 create_plan 的工具
	client := &scriptedClient{
		responses: []*types.Message{
			{
				Role: types.RoleAssistant,
				ToolCalls: []types.ToolCall{
					{
						ID:       "call_other",
						Type:     "function",
						Function: types.FunctionCall{Name: "calculator", Arguments: `{"expression":"1+1"}`},
					},
				},
			},
		},
	}

	agent := NewAgent(client, types.DefaultConfig())
	agent.history = append(agent.history, types.Message{Role: types.RoleUser, Content: "测试"})

	plan, err := agent.planPhase()
	if err != nil {
		t.Fatalf("planPhase 错误: %v", err)
	}
	// 不是 create_plan 工具调用，应返回 nil
	if plan != nil {
		t.Error("非 create_plan 工具调用应返回 nil plan")
	}
}

func TestPlanPhase_InvalidPlanJSON(t *testing.T) {
	// create_plan 工具调用但参数无效
	client := &scriptedClient{
		responses: []*types.Message{
			{
				Role: types.RoleAssistant,
				ToolCalls: []types.ToolCall{
					{
						ID:       "call_plan",
						Type:     "function",
						Function: types.FunctionCall{Name: "create_plan", Arguments: `{invalid}`},
					},
				},
			},
		},
	}

	agent := NewAgent(client, types.DefaultConfig())
	agent.history = append(agent.history, types.Message{Role: types.RoleUser, Content: "测试"})

	_, err := agent.planPhase()
	if err == nil {
		t.Error("无效计划 JSON 应返回错误")
	}
}

func TestAgentFormatSkillList(t *testing.T) {
	client := &scriptedClient{}
	agent := NewAgent(client, types.DefaultConfig())

	result := agent.FormatSkillList()

	// 验证包含所有技能
	expectedSkills := []string{"general", "coder", "translator", "analyst", "storyteller"}
	for _, name := range expectedSkills {
		if !strings.Contains(result, name) {
			t.Errorf("FormatSkillList 结果应包含 %q", name)
		}
	}

	// 验证当前技能有标记
	if !strings.Contains(result, "▶ general") {
		t.Errorf("当前技能应有 ▶ 标记，实际: %q", result)
	}

	// 切换技能后验证
	agent.SwitchSkill("coder")
	result = agent.FormatSkillList()
	if !strings.Contains(result, "▶ coder") {
		t.Errorf("切换后当前技能应为 coder，实际: %q", result)
	}
}
