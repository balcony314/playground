package agent

// ═══════════════════════════════════════════════════════════════
// agent.go — Agent 核心：Plan + ReAct 循环
// ═══════════════════════════════════════════════════════════════
//
// 【教学要点】Plan + ReAct 模式
//
// 本 Agent 采用两阶段执行模式：
//
//   ┌─────────────────────────────────────────────────────────┐
//   │  用户: "帮我分析这段代码的性能问题并给出优化建议"        │
//   └─────────────────────────────────────────────────────────┘
//                          ↓
//   ┌─────────────────────────────────────────────────────────┐
//   │  阶段 1: Plan（计划）                                   │
//   │  ┌──────────────────────────────────────────────────┐   │
//   │  │  LLM 分析任务 → 生成执行计划                      │   │
//   │  │  ┌────────────────────────────────────────────┐   │   │
//   │  │  │  目标: 分析代码性能问题并给出优化建议        │   │   │
//   │  │  │  步骤 1: 读取代码文件                        │   │   │
//   │  │  │  步骤 2: 分析性能瓶颈                        │   │   │
//   │  │  │  步骤 3: 生成优化建议                        │   │   │
//   │  │  └────────────────────────────────────────────┘   │   │
//   │  └──────────────────────────────────────────────────┘   │
//   └─────────────────────────────────────────────────────────┘
//                          ↓
//   ┌─────────────────────────────────────────────────────────┐
//   │  阶段 2: Execute（执行）                                │
//   │  ┌──────────────────────────────────────────────────┐   │
//   │  │  对每个步骤执行 ReAct 循环:                       │   │
//   │  │                                                   │   │
//   │  │  步骤 1: 读取代码文件                             │   │
//   │  │    └─ ReAct: THINK → ACT(read_file) → OBSERVE    │   │
//   │  │                                                   │   │
//   │  │  步骤 2: 分析性能瓶颈                             │   │
//   │  │    └─ ReAct: THINK → ACT(analyze) → OBSERVE      │   │
//   │  │                                                   │   │
//   │  │  步骤 3: 生成优化建议                             │   │
//   │  │    └─ ReAct: THINK → ACT(suggest) → OBSERVE      │   │
//   │  └──────────────────────────────────────────────────┘   │
//   └─────────────────────────────────────────────────────────┘
//                          ↓
//   ┌─────────────────────────────────────────────────────────┐
//   │  汇总所有步骤结果 → 返回最终答案                        │
//   └─────────────────────────────────────────────────────────┘
//
// 优势：
//   - 复杂任务被拆解为可管理的步骤
//   - 每个步骤独立执行，便于调试和错误处理
//   - 用户可以看到清晰的执行进度

import (
	"ai-agent-demo/agent/skills"
	"ai-agent-demo/agent/tools"
	"ai-agent-demo/agent/types"
	"encoding/json"
	"fmt"
	"strings"
)

// Agent 是一个 AI 智能体
type Agent struct {
	config       types.Config          // 配置
	llm          LLMClient            // LLM 客户端
	registry     *tools.ToolRegistry  // 工具注册表
	skillReg     *skills.SkillRegistry // 技能注册表
	currentSkill string               // 当前激活的技能名称
	history      []types.Message      // 对话历史
	currentPlan  *types.Plan          // 当前执行的计划
}

// NewAgent 创建一个新的 Agent
func NewAgent(llm LLMClient, config types.Config) *Agent {
	// 注册内置工具
	registry := tools.NewToolRegistry()
	tools.RegisterBuiltinTools(registry)

	// 注册内置技能
	skillReg := skills.NewSkillRegistry()
	skills.RegisterBuiltinSkills(skillReg)

	// 初始化对话历史（system prompt 是第一条消息）
	history := []types.Message{
		{Role: types.RoleSystem, Content: config.SystemPrompt},
	}

	agent := &Agent{
		config:       config,
		llm:          llm,
		registry:     registry,
		skillReg:     skillReg,
		currentSkill: "general", // 默认使用通用技能
		history:      history,
	}

	// 注册计划工具（需要 Agent 引用，所以在这里注册）
	agent.registerPlanTool()

	return agent
}

// ─── 核心：Plan + ReAct 循环 ──────────────────────────────────

// Run 是 Agent 的主循环，采用 Plan + ReAct 两阶段模式
//   1. 把用户消息加入对话历史
//   2. Plan 阶段：调用 LLM 生成执行计划
//   3. Execute 阶段：对计划中的每个步骤执行 ReAct 循环
//   4. 汇总所有步骤结果，返回最终答案
func (a *Agent) Run(userInput string) (string, error) {
	fmt.Printf("\n%s\n", strings.Repeat("─", 50))
	fmt.Printf("📝 用户: %s\n", userInput)
	fmt.Printf("%s\n\n", strings.Repeat("─", 50))

	// 1. 把用户消息加入历史
	a.history = append(a.history, types.Message{
		Role:    types.RoleUser,
		Content: userInput,
	})

	// 2. Plan 阶段：生成执行计划
	plan, err := a.planPhase()
	if err != nil {
		return "", fmt.Errorf("计划生成失败: %w", err)
	}

	// 简单任务无需计划，直接用 ReAct 执行
	if plan == nil {
		return a.reactLoop("直接回答用户问题")
	}

	// 3. Execute 阶段：逐步执行计划
	fmt.Printf("\n🚀 开始执行计划: %s\n", plan.Goal)
	fmt.Printf("%s\n\n", strings.Repeat("─", 50))

	stepResults := make([]string, 0, len(plan.Steps))
	for i, step := range plan.Steps {
		fmt.Printf("📌 步骤 %d/%d: %s\n", i+1, len(plan.Steps), step.Description)

		result, err := a.executeStep(step, i+1, len(plan.Steps))
		if err != nil {
			errMsg := fmt.Sprintf("步骤 %d 执行失败: %v", i+1, err)
			fmt.Printf("❌ %s\n\n", errMsg)
			stepResults = append(stepResults, errMsg)
		} else {
			stepResults = append(stepResults, result)
			fmt.Printf("✅ 步骤 %d 完成\n\n", i+1)
		}
	}

	// 4. 汇总所有步骤结果
	fmt.Printf("%s\n", strings.Repeat("─", 50))
	fmt.Printf("📊 所有步骤执行完毕，正在生成最终答案...\n\n")

	summary, err := a.summarizeResults(plan, stepResults)
	if err != nil {
		return "", fmt.Errorf("结果汇总失败: %w", err)
	}
	return summary, nil
}

// planPhase 执行计划生成阶段
// 调用 LLM 分析任务，生成执行计划
// 返回 nil 表示任务简单，无需计划
func (a *Agent) planPhase() (*types.Plan, error) {
	fmt.Printf("📋 阶段 1: 生成执行计划\n")
	fmt.Printf("%s\n", strings.Repeat("─", 40))

	// 构建计划生成的提示
	planPrompt := `请分析用户的任务，判断是否需要制定执行计划。

如果任务简单（单步可完成），直接回复 "SIMPLE"。
如果任务复杂（需要多步骤），请使用 create_plan 工具创建执行计划。

判断标准：
- 简单任务：单次查询、单次计算、简单问答
- 复杂任务：需要多步骤、多工具配合、有依赖关系的任务`

	// 临时添加计划提示到历史（不污染主对话历史）
	planHistory := make([]types.Message, len(a.history))
	copy(planHistory, a.history)
	planHistory = append(planHistory, types.Message{
		Role:    types.RoleUser,
		Content: planPrompt,
	})

	// 只提供 create_plan 工具，让 LLM 决定是否创建计划
	var planTools []types.ToolDefinition
	if tool, ok := a.registry.Get("create_plan"); ok {
		planTools = append(planTools, tool.Definition)
	}

	// 调用 LLM
	response, err := a.llm.Chat(planHistory, planTools)
	if err != nil {
		return nil, err
	}

	return a.parsePlanResponse(response)
}

// parsePlanResponse 解析 LLM 的计划阶段响应
// 如果 LLM 调用了 create_plan 工具则返回计划，否则返回 nil（简单任务）
func (a *Agent) parsePlanResponse(response *types.Message) (*types.Plan, error) {
	// 查找 create_plan 工具调用
	for _, tc := range response.ToolCalls {
		if tc.Function.Name != "create_plan" {
			continue
		}

		plan, err := a.executeCreatePlan(tc.Function.Arguments)
		if err != nil {
			return nil, err
		}

		fmt.Printf("   ✅ 计划已生成:\n")
		fmt.Printf("   目标: %s\n", plan.Goal)
		for i, step := range plan.Steps {
			fmt.Printf("   %d. %s\n", i+1, step.Description)
		}
		fmt.Println()
		return plan, nil
	}

	// 没有 create_plan 工具调用 → 任务简单，无需计划
	fmt.Printf("   → 任务简单，无需计划\n\n")
	return nil, nil
}

// executeCreatePlan 解析 create_plan 工具参数并返回计划
func (a *Agent) executeCreatePlan(argsJSON string) (*types.Plan, error) {
	var plan types.Plan
	if err := json.Unmarshal([]byte(argsJSON), &plan); err != nil {
		return nil, fmt.Errorf("计划解析失败: %w", err)
	}

	// 为每个步骤分配 ID 和初始状态
	for i := range plan.Steps {
		plan.Steps[i].ID = i + 1
		plan.Steps[i].Status = "pending"
	}

	return &plan, nil
}

// executeStep 对单个计划步骤执行 ReAct 循环
func (a *Agent) executeStep(step types.Step, stepNum, totalSteps int) (string, error) {
	stepPrompt := fmt.Sprintf("执行计划步骤 %d/%d: %s\n\n请完成这个步骤并报告结果。",
		stepNum, totalSteps, step.Description)
	return a.reactLoop(stepPrompt)
}

// reactLoop 执行 ReAct 循环，最多 MaxTurns 轮
func (a *Agent) reactLoop(taskPrompt string) (string, error) {
	a.history = append(a.history, types.Message{
		Role:    types.RoleUser,
		Content: taskPrompt,
	})

	for turn := 0; turn < a.config.MaxTurns; turn++ {
		fmt.Printf("   🔄 推理轮次 %d/%d\n", turn+1, a.config.MaxTurns)

		response, err := a.llm.Chat(a.history, a.getSkillTools())
		if err != nil {
			return "", fmt.Errorf("LLM 调用失败: %w", err)
		}

		a.history = append(a.history, *response)

		// 没有工具调用 → LLM 认为可以回答了
		if len(response.ToolCalls) == 0 {
			fmt.Printf("   ✅ 推理完成 (共 %d 轮)\n", turn+1)
			return response.Content, nil
		}

		// 有工具调用 → 逐个执行
		fmt.Printf("   🛠️  调用 %d 个工具:\n", len(response.ToolCalls))

		for _, tc := range response.ToolCalls {
			fmt.Printf("      → %s(%s)\n", tc.Function.Name, TruncStr(tc.Function.Arguments, 50))

			result, err := a.executeTool(tc.Function.Name, tc.Function.Arguments)
			if err != nil {
				fmt.Printf("      ❌ 错误: %v\n", err)
				a.history = append(a.history, types.Message{
					Role:       types.RoleTool,
					Content:    fmt.Sprintf("工具执行错误: %v", err),
					ToolCallID: tc.ID,
				})
				continue
			}

			fmt.Printf("      📋 结果: %s\n", TruncStr(result, 60))
			a.history = append(a.history, types.Message{
				Role:       types.RoleTool,
				Content:    result,
				ToolCallID: tc.ID,
			})
		}

		fmt.Println() // 空行分隔轮次
	}

	// 超过最大轮数
	return "", fmt.Errorf("达到最大推理轮数: %d", a.config.MaxTurns)
}

// summarizeResults 汇总所有步骤的结果，生成最终答案
func (a *Agent) summarizeResults(plan *types.Plan, stepResults []string) (string, error) {
	var sb strings.Builder
	sb.WriteString("所有执行步骤已完成，请根据以下结果生成最终答案。\n\n")
	sb.WriteString(fmt.Sprintf("任务目标: %s\n\n", plan.Goal))
	sb.WriteString("执行结果:\n")

	for i, result := range stepResults {
		desc := "(未知步骤)"
		if i < len(plan.Steps) {
			desc = plan.Steps[i].Description
		}
		sb.WriteString(fmt.Sprintf("\n步骤 %d: %s\n", i+1, desc))
		sb.WriteString(fmt.Sprintf("结果: %s\n", result))
	}

	sb.WriteString("\n请汇总以上结果，给出完整、清晰的最终答案。")

	a.history = append(a.history, types.Message{
		Role:    types.RoleUser,
		Content: sb.String(),
	})

	response, err := a.llm.Chat(a.history, a.getSkillTools())
	if err != nil {
		return "", fmt.Errorf("LLM 调用失败: %w", err)
	}

	a.history = append(a.history, *response)
	return response.Content, nil
}

// executeTool 执行一个工具
func (a *Agent) executeTool(name, argsJSON string) (string, error) {
	tool, ok := a.registry.Get(name)
	if !ok {
		return "", fmt.Errorf("未知工具: %s", name)
	}
	return tool.Execute(json.RawMessage(argsJSON))
}

// ─── 计划相关方法 ────────────────────────────────────────────────

// registerPlanTool 注册计划工具（闭包捕获 Agent 引用）
func (a *Agent) registerPlanTool() {
	a.registry.Register(types.Tool{
		Definition: types.ToolDefinition{
			Type: "function",
			Function: types.FunctionSchema{
				Name:        "create_plan",
				Description: "为复杂任务创建执行计划。当任务需要多步骤完成时使用此工具。",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"goal": {
							"type": "string",
							"description": "任务目标"
						},
						"steps": {
							"type": "array",
							"items": {
								"type": "object",
								"properties": {
									"description": {
										"type": "string",
										"description": "步骤描述"
									}
								},
								"required": ["description"]
							},
							"description": "执行步骤列表"
						}
					},
					"required": ["goal", "steps"]
				}`),
			},
		},
		Execute: func(args json.RawMessage) (string, error) {
			var plan types.Plan
			if err := json.Unmarshal(args, &plan); err != nil {
				return "", fmt.Errorf("计划解析失败: %w", err)
			}

			// 设置当前计划
			a.setPlan(plan)

			// 格式化计划预览
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("📋 计划已创建: %s\n\n", plan.Goal))
			for i, step := range plan.Steps {
				sb.WriteString(fmt.Sprintf("  %d. %s\n", i+1, step.Description))
			}
			sb.WriteString("\n请开始执行第 1 步。")

			return sb.String(), nil
		},
	})
}

// setPlan 设置当前计划（创建副本，不修改原始数据）
func (a *Agent) setPlan(plan types.Plan) {
	steps := make([]types.Step, len(plan.Steps))
	for i, s := range plan.Steps {
		steps[i] = types.Step{ID: i + 1, Description: s.Description, Status: "pending"}
	}
	a.currentPlan = &types.Plan{Goal: plan.Goal, Steps: steps}
}

// ─── 辅助函数 ──────────────────────────────────────────────────

// GetHistory 获取对话历史的副本（用于调试，不影响内部状态）
func (a *Agent) GetHistory() []types.Message {
	out := make([]types.Message, len(a.history))
	copy(out, a.history)
	return out
}

// ClearHistory 清空对话历史（保留 system prompt）
func (a *Agent) ClearHistory() {
	a.history = a.history[:1]
}

// ListTools 列出当前技能可用的工具
func (a *Agent) ListTools() []string {
	skill, ok := a.skillReg.Get(a.currentSkill)
	if !ok || len(skill.Tools) == 0 {
		return a.registry.Names()
	}
	return skill.Tools
}

// ─── Skill 相关方法 ────────────────────────────────────────────

// SwitchSkill 切换当前技能
func (a *Agent) SwitchSkill(name string) error {
	skill, ok := a.skillReg.Get(name)
	if !ok {
		return fmt.Errorf("未知技能: %s", name)
	}

	a.currentSkill = name

	// 更新 system prompt
	a.history[0] = types.Message{
		Role:    types.RoleSystem,
		Content: skill.SystemPrompt,
	}

	// 清空对话历史（保留新的 system prompt）
	a.ClearHistory()

	fmt.Printf("✨ 已切换到技能: %s (%s)\n", name, skill.Description)
	return nil
}

// CurrentSkill 获取当前技能名称
func (a *Agent) CurrentSkill() string {
	return a.currentSkill
}

// ListSkills 列出所有可用技能
func (a *Agent) ListSkills() []skills.Skill {
	return a.skillReg.List()
}

// FormatSkillList 格式化技能列表
func (a *Agent) FormatSkillList() string {
	return skills.FormatSkillList(a.skillReg.List(), a.currentSkill)
}

// getSkillTools 根据当前技能获取可用工具定义
func (a *Agent) getSkillTools() []types.ToolDefinition {
	skill, ok := a.skillReg.Get(a.currentSkill)
	if !ok || len(skill.Tools) == 0 {
		// 未指定工具列表 → 使用全部工具
		return a.registry.Definitions()
	}

	// 按技能配置过滤工具
	defs := make([]types.ToolDefinition, 0, len(skill.Tools))
	for _, name := range skill.Tools {
		if tool, found := a.registry.Get(name); found {
			defs = append(defs, tool.Definition)
		}
	}
	return defs
}

// TruncStr 截断字符串用于日志显示
// 工具参数和结果可能很长，日志中只显示前 maxLen 个字符
func TruncStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
