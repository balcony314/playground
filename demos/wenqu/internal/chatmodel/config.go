// Package chatmodel 集中管理对话模型配置。
// 所有模型统一经 OpenRouter 调用（OpenAI 兼容协议），
// 模型名、BaseURL 等常量只在此维护，不散落硬编码。
package chatmodel

import (
	"time"

	"github.com/cloudwego/eino-ext/components/model/openai"
)

const (
	// OpenRouterBaseURL OpenRouter 的 OpenAI 兼容入口
	OpenRouterBaseURL = "https://openrouter.ai/api/v1"

	// DefaultTimeoutMin 单次 LLM 调用超时（分钟）。
	// ReAct agent 多轮工具调用与状态图多节点均复用同一模型实例
	DefaultTimeoutMin = 10
)

// DefaultAPIKey 默认 API Key，由 SetDefaultAPIKey 从启动参数注入
var DefaultAPIKey string

// SetDefaultAPIKey 设置默认 API Key
func SetDefaultAPIKey(apiKey string) {
	DefaultAPIKey = apiKey
}

// ModelName OpenRouter 模型标识
type ModelName string

// 对话模型（按能力/成本取舍，改默认模型换这里）
const (
	ModelNameClaudeSonnet4 ModelName = "anthropic/claude-sonnet-4"
	ModelNameGemini2       ModelName = "google/gemini-2.5-pro"
	ModelNameGPT4oMini     ModelName = "openai/gpt-4o-mini"
	ModelNameQWen3Instruct ModelName = "qwen/qwen3-235b-a22b-2507"
	ModelNameDeepSeekV3    ModelName = "deepseek/deepseek-chat-v3.1"

	// ModelNameBGE 自研 embedding 服务的模型名（非 OpenRouter，服务地址见 flag）
	ModelNameBGE ModelName = "BAAI/HF/bge-large-zh-v1.5"

	// ModelNameRerankQwen 自研 rerank 服务的模型名
	ModelNameRerankQwen ModelName = "qwen3-reranker-8b"
)

// Config 对话模型配置
type Config struct {
	OpenRouterBaseURL string
	APIKey            string
	ModelName         ModelName
}

// DefaultConfig 默认模型配置（低温 + 用量统计）
func DefaultConfig() *openai.ChatModelConfig {
	return &openai.ChatModelConfig{
		APIKey:  DefaultAPIKey,
		BaseURL: OpenRouterBaseURL,
		Model:   string(ModelNameGemini2),
		Timeout: time.Minute * time.Duration(DefaultTimeoutMin),
		ExtraFields: map[string]any{
			"usage": map[string]bool{
				"include": true,
			},
		},
	}
}
