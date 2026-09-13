package tools

import (
	"ai-agent-demo/agent/types"
	"encoding/json"
	"testing"
)

// ─── ToolRegistry 测试 ─────────────────────────────────────────

func TestToolRegistry_RegisterAndGet(t *testing.T) {
	reg := NewToolRegistry()

	tool := types.Tool{
		Definition: types.ToolDefinition{
			Type: "function",
			Function: types.FunctionSchema{
				Name:        "test_tool",
				Description: "测试工具",
			},
		},
		Execute: func(args json.RawMessage) (string, error) {
			return "ok", nil
		},
	}

	reg.Register(tool)

	got, ok := reg.Get("test_tool")
	if !ok {
		t.Fatal("Get 返回 false，期望找到已注册的工具")
	}
	if got.Definition.Function.Name != "test_tool" {
		t.Errorf("工具名 = %q, 期望 %q", got.Definition.Function.Name, "test_tool")
	}
}

func TestToolRegistry_GetNotFound(t *testing.T) {
	reg := NewToolRegistry()
	_, ok := reg.Get("nonexistent")
	if ok {
		t.Error("Get 返回 true，期望找不到未注册的工具")
	}
}

func TestToolRegistry_Definitions(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(types.Tool{
		Definition: types.ToolDefinition{
			Type: "function",
			Function: types.FunctionSchema{Name: "a", Description: "tool a"},
		},
	})
	reg.Register(types.Tool{
		Definition: types.ToolDefinition{
			Type: "function",
			Function: types.FunctionSchema{Name: "b", Description: "tool b"},
		},
	})

	defs := reg.Definitions()
	if len(defs) != 2 {
		t.Errorf("Definitions 长度 = %d, 期望 2", len(defs))
	}
}

func TestToolRegistry_Names(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(types.Tool{
		Definition: types.ToolDefinition{
			Function: types.FunctionSchema{Name: "alpha"},
		},
	})
	reg.Register(types.Tool{
		Definition: types.ToolDefinition{
			Function: types.FunctionSchema{Name: "beta"},
		},
	})

	names := reg.Names()
	if len(names) != 2 {
		t.Errorf("Names 长度 = %d, 期望 2", len(names))
	}
	nameSet := map[string]bool{}
	for _, n := range names {
		nameSet[n] = true
	}
	if !nameSet["alpha"] || !nameSet["beta"] {
		t.Errorf("Names = %v, 期望包含 alpha 和 beta", names)
	}
}

func TestRegisterBuiltinTools(t *testing.T) {
	reg := NewToolRegistry()
	RegisterBuiltinTools(reg)

	expected := []string{"calculator", "current_time", "search", "text_transform"}
	for _, name := range expected {
		if _, ok := reg.Get(name); !ok {
			t.Errorf("内置工具 %q 未注册", name)
		}
	}
}
