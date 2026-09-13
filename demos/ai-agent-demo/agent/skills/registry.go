package skills

// ═══════════════════════════════════════════════════════════════
// registry.go — Skill 系统：预定义的 Agent 角色
// ═══════════════════════════════════════════════════════════════
//
// 【教学要点】什么是 Skill？
//
// Skill 是预定义的 Agent 角色配置，包含：
//   - Name: 技能名称（用户通过 /skill 命令切换）
//   - Description: 技能描述（帮助用户理解用途）
//   - SystemPrompt: 专用的系统提示词
//   - Tools: 该技能可用的工具列表（可选，为空则用全部工具）
//
// 使用场景：
//   - 代码助手：专注代码生成和解释
//   - 翻译官：专注多语言翻译
//   - 数据分析师：专注数据处理和计算
//
// 这种设计让同一个 Agent 框架可以灵活切换不同"人格"！

import (
	"fmt"
	"sort"
)

// Skill 定义一个预设的 Agent 角色
type Skill struct {
	Name         string   // 技能名称
	Description  string   // 技能描述
	SystemPrompt string   // 专用的系统提示词
	Tools        []string // 可用工具名称列表（空 = 全部）
}

// SkillRegistry 管理所有技能
type SkillRegistry struct {
	skills map[string]Skill
}

// NewSkillRegistry 创建技能注册表
func NewSkillRegistry() *SkillRegistry {
	return &SkillRegistry{
		skills: make(map[string]Skill),
	}
}

// Register 注册一个技能
func (r *SkillRegistry) Register(skill Skill) {
	r.skills[skill.Name] = skill
}

// Get 根据名称获取技能
func (r *SkillRegistry) Get(name string) (Skill, bool) {
	skill, ok := r.skills[name]
	return skill, ok
}

// List 列出所有技能（按名称排序）
func (r *SkillRegistry) List() []Skill {
	list := make([]Skill, 0, len(r.skills))
	for _, s := range r.skills {
		list = append(list, s)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].Name < list[j].Name
	})
	return list
}

// RegisterBuiltinSkills 注册所有内置技能
func RegisterBuiltinSkills(registry *SkillRegistry) {

	// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
	// 技能 1: 通用助手（默认）
	// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
	registry.Register(Skill{
		Name:        "general",
		Description: "通用 AI 助手，可使用所有工具",
		SystemPrompt: `你是一个有用的 AI 助手。
你可以使用工具来帮助回答问题。
当你不确定时，请使用工具来获取信息，而不是编造答案。

## 计划功能
对于复杂任务（需要 3 个以上步骤），请先使用 create_plan 工具制定执行计划，然后按步骤执行。
简单任务直接使用工具即可，无需创建计划。

请用中文回复。`,
		Tools: []string{}, // 空 = 全部工具
	})

	// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
	// 技能 2: 代码助手
	// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
	registry.Register(Skill{
		Name:        "coder",
		Description: "代码助手：专注代码生成、解释和调试",
		SystemPrompt: `你是一个专业的编程助手。
你的专长是代码相关任务：编写代码、解释代码、调试问题、优化性能。
回复时：
- 代码块使用正确的语法高亮标记
- 解释要简洁清晰
- 主动指出潜在的 bug 和改进点
请用中文回复。`,
		Tools: []string{"calculator", "text_transform"}, // 只用计算和文本工具
	})

	// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
	// 技能 3: 翻译官
	// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
	registry.Register(Skill{
		Name:        "translator",
		Description: "翻译官：专注中英日韩多语言互译",
		SystemPrompt: `你是一个专业的多语言翻译助手。
支持中文、英文、日文、韩文之间的互译。
翻译原则：
- 保持原文的语气和风格
- 专业术语使用行业通用译法
- 必要时提供翻译说明和备选方案
回复格式：先给出翻译结果，再补充必要的说明。`,
		Tools: []string{"text_transform"}, // 只用文本工具
	})

	// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
	// 技能 4: 数据分析师
	// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
	registry.Register(Skill{
		Name:        "analyst",
		Description: "数据分析：专注数据处理、计算和洞察",
		SystemPrompt: `你是一个专业的数据分析师。
你的专长是数据分析相关任务：数据处理、统计计算、趋势分析、洞察提炼。
回复时：
- 使用具体数字和百分比
- 提供可视化建议（图表类型等）
- 给出可执行的建议
请用中文回复。`,
		Tools: []string{"calculator", "search"}, // 用计算和搜索工具
	})

	// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
	// 技能 5: 故事大王
	// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
	registry.Register(Skill{
		Name:        "storyteller",
		Description: "故事大王：创作有趣的故事和创意内容",
		SystemPrompt: `你是一个富有创造力的故事大王。
你擅长创作各种类型的故事：童话、科幻、悬疑、历史故事等。
创作原则：
- 情节引人入胜，节奏感强
- 人物形象鲜明
- 语言生动有趣
可以根据用户的要求调整风格和长度。请用中文创作。`,
		Tools: []string{}, // 不需要工具
	})
}

// FormatSkillList 格式化技能列表（用于显示）
func FormatSkillList(skills []Skill, currentSkill string) string {
	result := "可用技能:\n"
	for _, s := range skills {
		marker := "  "
		if s.Name == currentSkill {
			marker = "▶ "
		}
		result += fmt.Sprintf("%s%-12s %s\n", marker, s.Name, s.Description)
	}
	return result
}
