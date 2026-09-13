package agent

import (
	"ai-agent-demo/agent/types"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ─── MockClient 测试 ─────────────────────────────────────────

func TestMockClient_NewMockClient(t *testing.T) {
	client := NewMockClient()
	if client == nil {
		t.Fatal("NewMockClient 返回 nil")
	}
	if client.callCount != 0 {
		t.Errorf("初始 callCount = %d, 期望 0", client.callCount)
	}
}

func TestMockClient_ToolCallSequence(t *testing.T) {
	client := NewMockClient()
	tools := []types.ToolDefinition{
		{Type: "function", Function: types.FunctionSchema{Name: "search"}},
		{Type: "function", Function: types.FunctionSchema{Name: "calculator"}},
	}

	// 第 1 次调用：search（用户消息）
	messages := []types.Message{
		{Role: types.RoleSystem, Content: "你是助手"},
		{Role: types.RoleUser, Content: "搜索一下"},
	}
	resp, err := client.Chat(messages, tools)
	if err != nil {
		t.Fatalf("第 1 次调用错误: %v", err)
	}
	if len(resp.ToolCalls) == 0 {
		t.Error("第 1 次应返回 tool_calls")
	}
	if resp.ToolCalls[0].Function.Name != "search" {
		t.Errorf("第 1 次工具 = %q, 期望 search", resp.ToolCalls[0].Function.Name)
	}

	// 工具结果会触发 summarizeToolResult，所以直接测试第 2 次用户消息
	// 重置客户端来测试不同轮次
	client2 := NewMockClient()
	client2.callCount = 1 // 模拟第 2 次调用
	resp2, err := client2.Chat(messages, tools)
	if err != nil {
		t.Fatalf("第 2 次调用错误: %v", err)
	}
	if len(resp2.ToolCalls) == 0 {
		t.Error("第 2 次应返回 tool_calls")
	}
	if resp2.ToolCalls[0].Function.Name != "calculator" {
		t.Errorf("第 2 次工具 = %q, 期望 calculator", resp2.ToolCalls[0].Function.Name)
	}

	// 第 3 次调用
	client3 := NewMockClient()
	client3.callCount = 2 // 模拟第 3 次调用
	resp3, err := client3.Chat(messages, tools)
	if err != nil {
		t.Fatalf("第 3 次调用错误: %v", err)
	}
	if len(resp3.ToolCalls) == 0 {
		t.Error("第 3 次应返回 tool_calls")
	}
	if resp3.ToolCalls[0].Function.Name != "current_time" {
		t.Errorf("第 3 次工具 = %q, 期望 current_time", resp3.ToolCalls[0].Function.Name)
	}
}

func TestMockClient_TextReplyAfterToolCalls(t *testing.T) {
	client := NewMockClient()
	tools := []types.ToolDefinition{
		{Type: "function", Function: types.FunctionSchema{Name: "search"}},
	}

	// 前 3 次调用工具
	messages := []types.Message{
		{Role: types.RoleSystem, Content: "你是助手"},
		{Role: types.RoleUser, Content: "测试"},
	}
	for i := 0; i < 3; i++ {
		resp, _ := client.Chat(messages, tools)
		messages = append(messages, *resp, types.Message{Role: types.RoleTool, Content: "结果"})
	}

	// 第 4 次调用：应该返回文本而不是工具调用
	resp, err := client.Chat(messages, tools)
	if err != nil {
		t.Fatalf("第 4 次调用错误: %v", err)
	}
	if len(resp.ToolCalls) != 0 {
		t.Error("第 4 次应返回文本，不应有 tool_calls")
	}
	if resp.Content == "" {
		t.Error("回复内容不应为空")
	}
}

func TestMockClient_HelpKeyword(t *testing.T) {
	client := NewMockClient()
	messages := []types.Message{
		{Role: types.RoleSystem, Content: "你是助手"},
		{Role: types.RoleUser, Content: "帮助"},
	}

	resp, err := client.Chat(messages, nil)
	if err != nil {
		t.Fatalf("Chat 错误: %v", err)
	}
	if !strings.Contains(resp.Content, "数学计算") {
		t.Errorf("帮助回复应包含 '数学计算'，实际: %q", resp.Content)
	}
}

func TestMockClient_DefaultReply(t *testing.T) {
	client := NewMockClient()
	messages := []types.Message{
		{Role: types.RoleSystem, Content: "你是助手"},
		{Role: types.RoleUser, Content: "随便说点什么"},
	}

	resp, err := client.Chat(messages, nil)
	if err != nil {
		t.Fatalf("Chat 错误: %v", err)
	}
	if !strings.Contains(resp.Content, "随便说点什么") {
		t.Errorf("默认回复应包含用户消息，实际: %q", resp.Content)
	}
}

func TestMockClient_ToolResultSummary(t *testing.T) {
	client := NewMockClient()

	// 模拟工具结果消息
	messages := []types.Message{
		{Role: types.RoleSystem, Content: "你是助手"},
		{Role: types.RoleUser, Content: "搜索"},
		{Role: types.RoleAssistant, Content: "", ToolCalls: []types.ToolCall{
			{ID: "call_1", Type: "function", Function: types.FunctionCall{Name: "search", Arguments: `{"query":"test"}`}},
		}},
		{Role: types.RoleTool, Content: "这是搜索结果内容"},
	}

	resp, err := client.Chat(messages, nil)
	if err != nil {
		t.Fatalf("Chat 错误: %v", err)
	}
	if !strings.Contains(resp.Content, "工具返回的结果") {
		t.Errorf("总结回复应包含 '工具返回的结果'，实际: %q", resp.Content)
	}
}

// ─── NewOpenAIClient 测试 ────────────────────────────────────

func TestNewOpenAIClient_DefaultValues(t *testing.T) {
	client := NewOpenAIClient("sk-test", "", "")
	if client.APIKey != "sk-test" {
		t.Errorf("APIKey = %q, 期望 %q", client.APIKey, "sk-test")
	}
	if client.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("BaseURL = %q, 期望默认值", client.BaseURL)
	}
	if client.Model != "gpt-4o" {
		t.Errorf("Model = %q, 期望默认值", client.Model)
	}
}

func TestNewOpenAIClient_CustomValues(t *testing.T) {
	client := NewOpenAIClient("sk-test", "http://localhost:11434/v1/", "qwen2")
	if client.BaseURL != "http://localhost:11434/v1" {
		t.Errorf("BaseURL = %q, 期望去除末尾斜杠", client.BaseURL)
	}
	if client.Model != "qwen2" {
		t.Errorf("Model = %q, 期望 qwen2", client.Model)
	}
}

// ─── Ping 测试 ────────────────────────────────────────────────

func TestPing_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("请求路径 = %q, 期望 /models", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("Authorization = %q, 期望 Bearer sk-test", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewOpenAIClient("sk-test", server.URL, "gpt-4o")
	if err := client.Ping(); err != nil {
		t.Errorf("Ping 应成功, 实际错误: %v", err)
	}
}

func TestPing_NoAPIKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("无 API Key 时不应发送 Authorization, 实际: %q", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewOpenAIClient("", server.URL, "gpt-4o")
	if err := client.Ping(); err != nil {
		t.Errorf("Ping 应成功, 实际错误: %v", err)
	}
}

func TestPing_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewOpenAIClient("sk-test", server.URL, "gpt-4o")
	err := client.Ping()
	if err == nil {
		t.Error("Ping 500 应返回错误")
	}
}

func TestPing_Unreachable(t *testing.T) {
	client := NewOpenAIClient("sk-test", "http://127.0.0.1:1", "gpt-4o")
	err := client.Ping()
	if err == nil {
		t.Error("Ping 不可达地址应返回错误")
	}
}

// ─── OpenAIClient.Chat 测试 ───────────────────────────────────

func TestOpenAIClient_Chat_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 验证请求
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("Authorization = %q, 期望 Bearer sk-test", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q, 期望 application/json", r.Header.Get("Content-Type"))
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"choices": [{
				"message": {
					"role": "assistant",
					"content": "你好！"
				}
			}]
		}`))
	}))
	defer server.Close()

	client := NewOpenAIClient("sk-test", server.URL, "gpt-4o")
	messages := []types.Message{
		{Role: types.RoleSystem, Content: "你是助手"},
		{Role: types.RoleUser, Content: "你好"},
	}

	resp, err := client.Chat(messages, nil)
	if err != nil {
		t.Fatalf("Chat 错误: %v", err)
	}
	if resp.Content != "你好！" {
		t.Errorf("Content = %q, 期望 %q", resp.Content, "你好！")
	}
	if resp.Role != types.RoleAssistant {
		t.Errorf("Role = %q, 期望 %q", resp.Role, types.RoleAssistant)
	}
}

func TestOpenAIClient_Chat_WithTools(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"choices": [{
				"message": {
					"role": "assistant",
					"content": "",
					"tool_calls": [{
						"id": "call_1",
						"type": "function",
						"function": {
							"name": "search",
							"arguments": "{\"query\":\"test\"}"
						}
					}]
				}
			}]
		}`))
	}))
	defer server.Close()

	client := NewOpenAIClient("sk-test", server.URL, "gpt-4o")
	messages := []types.Message{{Role: types.RoleUser, Content: "搜索一下"}}
	tools := []types.ToolDefinition{
		{Type: "function", Function: types.FunctionSchema{Name: "search", Description: "搜索"}},
	}

	resp, err := client.Chat(messages, tools)
	if err != nil {
		t.Fatalf("Chat 错误: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("ToolCalls 长度 = %d, 期望 1", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Function.Name != "search" {
		t.Errorf("工具名 = %q, 期望 search", resp.ToolCalls[0].Function.Name)
	}
}

func TestOpenAIClient_Chat_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "internal error"}`))
	}))
	defer server.Close()

	client := NewOpenAIClient("sk-test", server.URL, "gpt-4o")
	messages := []types.Message{{Role: types.RoleUser, Content: "你好"}}

	_, err := client.Chat(messages, nil)
	if err == nil {
		t.Error("HTTP 500 应返回错误")
	}
}

func TestOpenAIClient_Chat_EmptyChoices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices": []}`))
	}))
	defer server.Close()

	client := NewOpenAIClient("sk-test", server.URL, "gpt-4o")
	messages := []types.Message{{Role: types.RoleUser, Content: "你好"}}

	_, err := client.Chat(messages, nil)
	if err == nil {
		t.Error("空 choices 应返回错误")
	}
}

func TestOpenAIClient_Chat_Unreachable(t *testing.T) {
	client := NewOpenAIClient("sk-test", "http://127.0.0.1:1", "gpt-4o")
	messages := []types.Message{{Role: types.RoleUser, Content: "你好"}}

	_, err := client.Chat(messages, nil)
	if err == nil {
		t.Error("不可达地址应返回错误")
	}
}
