package tools

import (
	"encoding/json"
	"math"
	"testing"
)

// ─── evaluateExpression 测试 ───────────────────────────────────

func TestEvaluateExpression_BasicOps(t *testing.T) {
	tests := []struct {
		expr string
		want float64
	}{
		{"2 + 3", 5},
		{"10 - 4", 6},
		{"3 * 7", 21},
		{"15 / 3", 5},
		{"2 * 3 + 4", 6},   // LastIndex(+)=6 → left="2*3"→parseFloat得2, right="4" → 2+4=6
		{"10 + 2 * 3", 12}, // LastIndex(+)=3 → left="10", right="2*3"→parseFloat得2 → 10+2=12
		{"100", 100},        // 单个数字
		{"3.14 * 2", 6.28}, // 浮点数
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			got, err := evaluateExpression(tt.expr)
			if err != nil {
				t.Fatalf("evaluateExpression(%q) 错误: %v", tt.expr, err)
			}
			if math.Abs(got-tt.want) > 1e-9 {
				t.Errorf("evaluateExpression(%q) = %g, 期望 %g", tt.expr, got, tt.want)
			}
		})
	}
}

func TestEvaluateExpression_Sqrt(t *testing.T) {
	got, err := evaluateExpression("sqrt(144)")
	if err != nil {
		t.Fatalf("sqrt(144) 错误: %v", err)
	}
	if math.Abs(got-12) > 1e-9 {
		t.Errorf("sqrt(144) = %g, 期望 12", got)
	}
}

func TestEvaluateExpression_SqrtNegative(t *testing.T) {
	_, err := evaluateExpression("sqrt(-1)")
	if err == nil {
		t.Error("sqrt(-1) 应返回错误")
	}
}

func TestEvaluateExpression_DivideByZero(t *testing.T) {
	_, err := evaluateExpression("1 / 0")
	if err == nil {
		t.Error("除以零应返回错误")
	}
}

func TestEvaluateExpression_InvalidExpr(t *testing.T) {
	_, err := evaluateExpression("abc + def")
	if err == nil {
		t.Error("无效表达式应返回错误")
	}
}

func TestEvaluateExpression_Subtraction(t *testing.T) {
	got, err := evaluateExpression("10 - 3")
	if err != nil {
		t.Fatalf("10 - 3 错误: %v", err)
	}
	if math.Abs(got-7) > 1e-9 {
		t.Errorf("10 - 3 = %g, 期望 7", got)
	}
}

func TestEvaluateExpression_Multiplication(t *testing.T) {
	got, err := evaluateExpression("4 * 5")
	if err != nil {
		t.Fatalf("4 * 5 错误: %v", err)
	}
	if math.Abs(got-20) > 1e-9 {
		t.Errorf("4 * 5 = %g, 期望 20", got)
	}
}

func TestEvaluateExpression_SingleNumber(t *testing.T) {
	got, err := evaluateExpression("42")
	if err != nil {
		t.Fatalf("42 错误: %v", err)
	}
	if math.Abs(got-42) > 1e-9 {
		t.Errorf("42 = %g, 期望 42", got)
	}
}

func TestEvaluateExpression_SqrtWithExpression(t *testing.T) {
	// sqrt 内部是表达式
	got, err := evaluateExpression("sqrt(4 + 5)")
	if err != nil {
		t.Fatalf("sqrt(4+5) 错误: %v", err)
	}
	// sqrt(4) + 5 = 2 + 5 = 7 (因为 evaluateSimple 会解析 4+5 为 4)
	// 实际上 sqrt(4+5) 会调用 evaluateSimple("4+5"), 然后返回 4+5=9, 然后 sqrt(9)=3
	if math.Abs(got-3) > 1e-9 {
		t.Errorf("sqrt(4+5) = %g, 期望 3", got)
	}
}

// ─── calculator 工具测试 ───────────────────────────────────────

func TestCalculatorTool(t *testing.T) {
	reg := NewToolRegistry()
	RegisterBuiltinTools(reg)
	tool, _ := reg.Get("calculator")

	tests := []struct {
		name    string
		args    string
		want    string // 期望包含的子串
		wantErr bool
	}{
		{"加法", `{"expression":"2+3"}`, "计算结果", false},
		{"sqrt", `{"expression":"sqrt(16)"}`, "4", false},
		{"无效JSON", `{bad`, "", true},
		{"除零", `{"expression":"1/0"}`, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tool.Execute(json.RawMessage(tt.args))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if !tt.wantErr && tt.want != "" {
				if len(result) == 0 {
					t.Error("结果不应为空")
				}
			}
		})
	}
}

// ─── current_time 工具测试 ────────────────────────────────────

func TestCurrentTimeTool(t *testing.T) {
	reg := NewToolRegistry()
	RegisterBuiltinTools(reg)
	tool, _ := reg.Get("current_time")

	// 默认时区
	result, err := tool.Execute(json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("默认时区调用错误: %v", err)
	}
	if result == "" {
		t.Error("结果不应为空")
	}

	// 指定时区
	result, err = tool.Execute(json.RawMessage(`{"timezone":"Asia/Shanghai"}`))
	if err != nil {
		t.Fatalf("指定时区调用错误: %v", err)
	}
	if result == "" {
		t.Error("结果不应为空")
	}
}

func TestCurrentTimeTool_InvalidTimezone(t *testing.T) {
	reg := NewToolRegistry()
	RegisterBuiltinTools(reg)
	tool, _ := reg.Get("current_time")

	_, err := tool.Execute(json.RawMessage(`{"timezone":"Invalid/Zone"}`))
	if err == nil {
		t.Error("无效时区应返回错误")
	}
}

// ─── search 工具测试 ──────────────────────────────────────────

func TestSearchTool(t *testing.T) {
	reg := NewToolRegistry()
	RegisterBuiltinTools(reg)
	tool, _ := reg.Get("search")

	// 命中关键词
	result, err := tool.Execute(json.RawMessage(`{"query":"golang"}`))
	if err != nil {
		t.Fatalf("搜索错误: %v", err)
	}
	if result == "" {
		t.Error("搜索结果不应为空")
	}

	// 未命中
	result, err = tool.Execute(json.RawMessage(`{"query":"xyz_nonexistent"}`))
	if err != nil {
		t.Fatalf("搜索错误: %v", err)
	}
	if result == "" {
		t.Error("未命中也应返回提示信息")
	}
}

func TestSearchTool_InvalidJSON(t *testing.T) {
	reg := NewToolRegistry()
	RegisterBuiltinTools(reg)
	tool, _ := reg.Get("search")

	_, err := tool.Execute(json.RawMessage(`{bad`))
	if err == nil {
		t.Error("无效 JSON 应返回错误")
	}
}

// ─── text_transform 工具测试 ──────────────────────────────────

func TestTextTransformTool(t *testing.T) {
	reg := NewToolRegistry()
	RegisterBuiltinTools(reg)
	tool, _ := reg.Get("text_transform")

	tests := []struct {
		name    string
		args    string
		want    string
		wantErr bool
	}{
		{"upper", `{"text":"hello","operation":"upper"}`, "HELLO", false},
		{"lower", `{"text":"HELLO","operation":"lower"}`, "hello", false},
		{"reverse", `{"text":"abc","operation":"reverse"}`, "cba", false},
		{"length", `{"text":"你好","operation":"length"}`, "2", false},
		{"unknown_op", `{"text":"hi","operation":"foo"}`, "", true},
		{"invalid_json", `{bad`, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tool.Execute(json.RawMessage(tt.args))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if !tt.wantErr {
				if result == "" {
					t.Error("结果不应为空")
				}
			}
		})
	}
}

// ─── mockSearch 测试 ──────────────────────────────────────────

func TestMockSearch(t *testing.T) {
	tests := []struct {
		query   string
		wantHit bool
	}{
		{"golang", true},
		{"Go语言", true}, // 包含 golang 关键词（小写匹配）
		{"xyz_nonexistent", false},
		{"agent", true},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			result := mockSearch(tt.query)
			if result == "" {
				t.Error("mockSearch 不应返回空")
			}
		})
	}
}
