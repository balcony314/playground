package agent

// ═══════════════════════════════════════════════════════════════
// llm.go — LLM 客户端
// ═══════════════════════════════════════════════════════════════
//
// 【教学要点】LLM 在 Agent 中的角色
//
// LLM 是 Agent 的"大脑"，负责：
//   1. 理解用户意图
//   2. 决定是否需要使用工具（Function Calling）
//   3. 生成最终回复
//
// 本客户端支持两种模式：
//   1. 真实 API 模式：调用 OpenAI 兼容的 API（OpenAI/Claude/本地 Ollama）
//   2. Mock 模式：用于无 API Key 时的教学演示
//
// Function Calling 工作原理：
//   发送请求时带上工具 schema → LLM 返回 tool_calls（而不是直接回复文本）
//   → Agent 执行工具 → 把结果放回对话 → LLM 看到结果后继续推理

import (
	"ai-agent-demo/agent/types"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ─── LLM 客户端接口 ─────────────────────────────────────────────

// LLMClient 是 LLM 的统一接口
type LLMClient interface {
	// Chat 发送消息列表，返回 LLM 的回复
	// 如果 LLM 决定使用工具，回复中会包含 ToolCalls
	Chat(messages []types.Message, tools []types.ToolDefinition) (*types.Message, error)
}

// ─── OpenAI 兼容 API 客户端 ─────────────────────────────────────

// OpenAIClient 实现了 LLMClient，调用 OpenAI 兼容 API
type OpenAIClient struct {
	APIKey  string // API Key
	BaseURL string // API 地址（可以是 OpenAI、Ollama、vLLM 等）
	Model   string // 模型名称
}

// NewOpenAIClient 创建一个新的 OpenAI 客户端
func NewOpenAIClient(apiKey, baseURL, model string) *OpenAIClient {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	if model == "" {
		model = "gpt-4o"
	}
	return &OpenAIClient{
		APIKey:  apiKey,
		BaseURL: strings.TrimRight(baseURL, "/"),
		Model:   model,
	}
}

// Ping 检查 API 是否可达（启动时调用）
func (c *OpenAIClient) Ping() error {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest("GET", c.BaseURL+"/models", nil)
	if err != nil {
		return fmt.Errorf("创建请求失败: %w", err)
	}
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("无法连接到 %s: %w", c.BaseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("API 返回异常状态码 %d", resp.StatusCode)
	}
	return nil
}

// Chat 实现 LLMClient 接口
// 请求流程：构建请求体 → 发 HTTP POST → 解析响应 → 返回 Message
// 请求体包含：model（模型名）、messages（对话历史）、tools（工具定义）
// 响应中的 message 可能包含 content（文本回复）或 tool_calls（工具调用请求）
func (c *OpenAIClient) Chat(messages []types.Message, tools []types.ToolDefinition) (*types.Message, error) {
	// 1. 构建请求体：model + messages + tools
	body := map[string]interface{}{
		"model":    c.Model,
		"messages": messages,
	}
	if len(tools) > 0 {
		body["tools"] = tools
	}

	reqBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("序列化请求失败: %w", err)
	}

	// 2. 发送 HTTP POST 请求到 /chat/completions 端点
	//    这是 OpenAI API 的标准端点，Ollama/vLLM 等也兼容这个格式
	url := c.BaseURL + "/chat/completions"
	req, err := http.NewRequest("POST", url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// Bearer token 认证，Ollama 等本地服务可以忽略这个 header
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API 调用失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API 返回错误 (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	// 3. 解析响应
	//    OpenAI API 返回格式：{ "choices": [{ "message": { "role": "assistant", "content": "...", "tool_calls": [...] } }] }
	//    如果 LLM 决定用工具，message 中会有 tool_calls 而不是 content
	var apiResp struct {
		Choices []struct {
			Message types.Message `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	if len(apiResp.Choices) == 0 {
		return nil, fmt.Errorf("API 返回空响应")
	}

	return &apiResp.Choices[0].Message, nil
}

// ─── Mock LLM 客户端（教学演示用）──────────────────────────────

// MockClient 是一个模拟的 LLM 客户端
// 用于在没有 API Key 时演示 Agent 的工作流程
type MockClient struct {
	callCount int
}

func NewMockClient() *MockClient {
	return &MockClient{}
}

// Mock 模式的工作流程演示：
//   第 1 轮：收到 "你好" → 直接回复文本
//   第 2 轮：收到带工具的请求 → 返回 tool_calls（模拟 LLM 决定用工具）
//   第 3 轮：收到工具结果 → 用自然语言总结结果
//
// 这演示了 Agent ReAct 循环的一个完整周期

func (c *MockClient) Chat(messages []types.Message, tools []types.ToolDefinition) (*types.Message, error) {
	c.callCount++

	// 根据最后一条消息的角色判断当前处于 ReAct 循环的哪个阶段：
	//   - RoleTool → 刚执行完工具，LLM 需要总结结果（OBSERVE 阶段）
	//   - RoleUser → 用户刚输入，LLM 需要决定是否用工具（THINK 阶段）
	lastMsg := messages[len(messages)-1]

	// 如果最后一条是工具结果（RoleTool），LLM 应该总结结果
	if lastMsg.Role == types.RoleTool {
		return c.summarizeToolResult(messages)
	}

	// 如果有工具可用，模拟 LLM 决定使用工具
	if len(tools) > 0 && c.callCount <= 3 {
		return c.mockToolCall(tools)
	}

	// 否则直接回复文本
	return c.mockTextReply(messages)
}

// mockToolCall 模拟 LLM 返回 tool_calls
// 真实 LLM 会根据用户意图和工具描述自主决定调用哪个工具
// Mock 模式按轮次依次调用不同工具，展示多种工具的效果
func (c *MockClient) mockToolCall(tools []types.ToolDefinition) (*types.Message, error) {
	var toolCall types.ToolCall

	switch c.callCount {
	case 1:
		// 第一次：模拟调用 search 工具
		toolCall = types.ToolCall{
			ID:   fmt.Sprintf("call_%d", c.callCount),
			Type: "function",
			Function: types.FunctionCall{
				Name:      "search",
				Arguments: `{"query": "AI Agent"}`,
			},
		}
	case 2:
		// 第二次：模拟调用 calculator
		toolCall = types.ToolCall{
			ID:   fmt.Sprintf("call_%d", c.callCount),
			Type: "function",
			Function: types.FunctionCall{
				Name:      "calculator",
				Arguments: `{"expression": "42 * 3.14"}`,
			},
		}
	case 3:
		// 第三次：模拟调用 current_time
		toolCall = types.ToolCall{
			ID:   fmt.Sprintf("call_%d", c.callCount),
			Type: "function",
			Function: types.FunctionCall{
				Name:      "current_time",
				Arguments: `{"timezone": "Asia/Shanghai"}`,
			},
		}
	}

	return &types.Message{
		Role:      types.RoleAssistant,
		Content:   "",
		ToolCalls: []types.ToolCall{toolCall},
	}, nil
}

func (c *MockClient) mockTextReply(messages []types.Message) (*types.Message, error) {
	// 从后向前找最后一条用户消息
	userMsg := ""
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == types.RoleUser {
			userMsg = messages[i].Content
			break
		}
	}

	var reply string
	switch {
	case strings.Contains(userMsg, "你好") || strings.Contains(userMsg, "hello"):
		reply = "你好！👋 我是一个 AI Agent，可以使用工具来帮助你。比如：\n- 🔢 calculator: 数学计算\n- ⏰ current_time: 获取时间\n- 🔍 search: 搜索信息\n- ✂️ text_transform: 文本处理\n\n试试问我一些需要工具才能回答的问题吧！"
	case strings.Contains(userMsg, "帮助") || strings.Contains(userMsg, "help"):
		reply = "我可以帮你做这些事情：\n\n1. **数学计算** - 例如：\"帮我算一下 sqrt(144) + 25\"\n2. **获取时间** - 例如：\"现在几点了？\"\n3. **搜索信息** - 例如：\"什么是 AI Agent？\"\n4. **文本处理** - 例如：\"把 hello world 转大写\"\n\n直接用自然语言告诉我你想做什么！"
	default:
		reply = fmt.Sprintf("收到你的消息：「%s」\n\n这是一个 Mock 模式的回复。要体验真正的 AI Agent，请设置 API Key 后运行。", userMsg)
	}
	return &types.Message{
		Role:    types.RoleAssistant,
		Content: reply,
	}, nil
}

func (c *MockClient) summarizeToolResult(messages []types.Message) (*types.Message, error) {
	// 调用方已确认最后一条是 RoleTool，直接取用
	toolResult := messages[len(messages)-1].Content

	// 简单的总结逻辑（教学用）
	reply := fmt.Sprintf("根据工具返回的结果：\n\n%s\n\n以上是工具的原始输出。在真实 Agent 中，LLM 会用自然语言重新组织这些信息来回答用户。", toolResult)

	return &types.Message{
		Role:    types.RoleAssistant,
		Content: reply,
	}, nil
}
