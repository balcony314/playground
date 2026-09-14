package chatmodel

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino-ext/components/model/openai"
)

// NewOpenAIChatModel 创建经 OpenRouter 调用的对话模型（OpenAI 兼容协议）。
// temperature=0：查询改写/SQL 生成/路由规划均为确定性任务，不需要发散。
func NewOpenAIChatModel(ctx context.Context, config Config) (*openai.ChatModel, error) {
	var temperature float32 = 0.0
	chatModel, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		BaseURL:     config.OpenRouterBaseURL,
		APIKey:      config.APIKey,
		Model:       string(config.ModelName),
		Temperature: &temperature,
	})
	if err != nil {
		return nil, fmt.Errorf("create open ai chat model: %w", err)
	}

	return chatModel, nil
}
