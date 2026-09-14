// Package clickhouse 实现 ClickHouse 单数据源的 ReAct 模式自主查询 Agent。
// LLM 通过 MCP 协议调用 list_databases / list_tables / run_select_query 等工具，
// 循环"思考 → 调工具 → 观察"直至产出答案，无需外部编排。
// 提供同步（Generate）与流式（Stream）两种执行模式。
package clickhouse

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"

	"datasearch/internal/callback"
)

// Agent 是 ClickHouse 数据查询的 react agent 封装
type Agent struct {
	agent *react.Agent
}

// NewAgent 创建 ClickHouse react agent。
// chatModel：底层对话模型（经 OpenRouter 调用）；
// mcpClickhouseTools：ClickHouse MCP server 暴露的工具列表（已在加载侧
// 按工具名白名单收窄），agent 执行期可自主决定调用顺序与次数。
func NewAgent(ctx context.Context, chatModel model.ToolCallingChatModel, mcpClickhouseTools []tool.BaseTool) (*Agent, error) {
	if len(mcpClickhouseTools) == 0 {
		return nil, fmt.Errorf("no mcp clickhouse tools provided, check mcp server and tool whitelist")
	}

	agent, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: chatModel,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools: mcpClickhouseTools,
		},
		// 默认 checker 在首个含内容的 chunk 处即判定"无工具调用"，
		// 而推理型模型（glm / claude 等）先输出思考与叙述、tool_calls 位于流末尾，
		// 会被误判导致流式链路提前终止（工具不执行、流戛然而止）
		StreamToolCallChecker: fullStreamToolCallChecker,
	})
	if err != nil {
		return nil, fmt.Errorf("new react agent: %w", err)
	}

	return &Agent{agent: agent}, nil
}

// fullStreamToolCallChecker 流式工具调用判定：消费完整模型输出流，
// 任一 chunk 携带 ToolCalls 即视为工具调用轮。
// eino 默认实现在首个非空内容 chunk 处即返回"无工具调用"，
// 对"先输出思考/叙述、再输出 tool_calls"的模型会误判并提前终止 ReAct 循环。
// 调用方收到的是流的副本（tee），此处消费完整流不影响下游透出。
func fullStreamToolCallChecker(_ context.Context, sr *schema.StreamReader[*schema.Message]) (bool, error) {
	defer sr.Close()

	for {
		msg, err := sr.Recv()
		if err != nil { // io.EOF：流读完未见 ToolCalls，判定为最终答案轮
			if errors.Is(err, io.EOF) {
				return false, nil
			}
			return false, err
		}

		if len(msg.ToolCalls) > 0 {
			return true, nil
		}
		// 叙述性内容不作为终止依据，继续消费
	}
}

// Generate 同步执行：将用户问题套入 prompt 模板后交给 react agent，
// 阻塞直到拿到完整回答（非流式）。
// 挂载 LoggerCallback 记录每次模型/工具调用的输入输出日志。
func (a *Agent) Generate(ctx context.Context, question string) (*schema.Message, error) {
	messages, err := createMessagesFromTemplate(ctx, question)
	if err != nil {
		return nil, fmt.Errorf("generate create template: %w", err)
	}

	result, err := a.agent.Generate(ctx, messages, agent.WithComposeOptions(
		compose.WithCallbacks(&callback.LoggerCallback{ParentCtx: ctx})))
	if err != nil {
		return nil, fmt.Errorf("react agent generate: %w", err)
	}

	return result, nil
}

// Stream 流式执行：与 Generate 相同的输入组装逻辑，
// 以 StreamReader 增量返回生成内容（打字机式输出场景）。
func (a *Agent) Stream(ctx context.Context, question string) (*schema.StreamReader[*schema.Message], error) {
	messages, err := createMessagesFromTemplate(ctx, question)
	if err != nil {
		return nil, fmt.Errorf("stream create template: %w", err)
	}

	result, err := a.agent.Stream(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("react agent stream: %w", err)
	}

	return result, nil
}
