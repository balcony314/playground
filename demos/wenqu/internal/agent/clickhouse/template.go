package clickhouse

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/cloudwego/eino/components/prompt"
	"github.com/cloudwego/eino/schema"
)

//go:embed prompt.md
var systemPromptStr string

// createTemplate 构造对话模板（FString 占位符格式）：
// 系统消息来自 prompt.md（go:embed 编译进二进制），用户消息承载具体问题。
// 占位符 {question} 在 Format 时填充。
func createTemplate() prompt.ChatTemplate {
	return prompt.FromMessages(schema.FString,
		schema.SystemMessage(systemPromptStr),
		schema.UserMessage("{question}"),
	)
}

// createMessagesFromTemplate 用用户问题填充模板，生成发给 react agent 的消息列表
func createMessagesFromTemplate(ctx context.Context, question string) ([]*schema.Message, error) {
	messages, err := createTemplate().Format(ctx, map[string]any{
		"question": question,
	})
	if err != nil {
		return nil, fmt.Errorf("format template: %w", err)
	}

	return messages, nil
}
