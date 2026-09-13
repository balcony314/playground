package skills

import (
	"strings"
	"testing"
)

// ─── SkillRegistry 测试 ───────────────────────────────────────

func TestSkillRegistry_RegisterAndGet(t *testing.T) {
	reg := NewSkillRegistry()
	skill := Skill{
		Name:         "test",
		Description:  "测试技能",
		SystemPrompt: "你是一个测试助手",
	}
	reg.Register(skill)

	got, ok := reg.Get("test")
	if !ok {
		t.Fatal("Get 返回 false，期望找到已注册的技能")
	}
	if got.Name != "test" {
		t.Errorf("技能名 = %q, 期望 %q", got.Name, "test")
	}
}

func TestSkillRegistry_GetNotFound(t *testing.T) {
	reg := NewSkillRegistry()
	_, ok := reg.Get("nonexistent")
	if ok {
		t.Error("Get 返回 true，期望找不到未注册的技能")
	}
}

func TestSkillRegistry_List(t *testing.T) {
	reg := NewSkillRegistry()
	reg.Register(Skill{Name: "beta", Description: "B"})
	reg.Register(Skill{Name: "alpha", Description: "A"})
	reg.Register(Skill{Name: "gamma", Description: "G"})

	list := reg.List()
	if len(list) != 3 {
		t.Fatalf("List 长度 = %d, 期望 3", len(list))
	}

	// 应按名称排序
	if list[0].Name != "alpha" || list[1].Name != "beta" || list[2].Name != "gamma" {
		t.Errorf("List 未按名称排序: %v", []string{list[0].Name, list[1].Name, list[2].Name})
	}
}

func TestRegisterBuiltinSkills(t *testing.T) {
	reg := NewSkillRegistry()
	RegisterBuiltinSkills(reg)

	expected := []string{"general", "coder", "translator", "analyst", "storyteller"}
	for _, name := range expected {
		if _, ok := reg.Get(name); !ok {
			t.Errorf("内置技能 %q 未注册", name)
		}
	}
}

func TestBuiltinSkills_ToolFiltering(t *testing.T) {
	reg := NewSkillRegistry()
	RegisterBuiltinSkills(reg)

	tests := []struct {
		skillName string
		wantTools int // 0 表示用全部工具
	}{
		{"general", 0},
		{"coder", 2},
		{"translator", 1},
		{"analyst", 2},
		{"storyteller", 0},
	}
	for _, tt := range tests {
		t.Run(tt.skillName, func(t *testing.T) {
			skill, _ := reg.Get(tt.skillName)
			if len(skill.Tools) != tt.wantTools {
				t.Errorf("技能 %q 工具数 = %d, 期望 %d", tt.skillName, len(skill.Tools), tt.wantTools)
			}
		})
	}
}

// ─── FormatSkillList 测试 ─────────────────────────────────────

func TestFormatSkillList(t *testing.T) {
	skills := []Skill{
		{Name: "alpha", Description: "Alpha 技能"},
		{Name: "beta", Description: "Beta 技能"},
	}

	result := FormatSkillList(skills, "alpha")

	if !strings.Contains(result, "alpha") {
		t.Error("输出应包含 alpha")
	}
	if !strings.Contains(result, "beta") {
		t.Error("输出应包含 beta")
	}
	// 当前技能应有 ▶ 标记
	if !strings.Contains(result, "▶") {
		t.Error("当前技能应有 ▶ 标记")
	}
}

func TestFormatSkillList_NoCurrent(t *testing.T) {
	skills := []Skill{
		{Name: "alpha", Description: "Alpha"},
	}

	result := FormatSkillList(skills, "")
	// 没有当前技能时不应有 ▶
	if strings.Contains(result, "▶") {
		t.Error("无当前技能时不应有 ▶ 标记")
	}
}
