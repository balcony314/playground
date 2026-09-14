package graph

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"datasearch/internal/orch/datasearch/prompt"
)

// NewInputProcess 构建"输入改写"子图：load → agent(LLM) → router。
// 职责：由 LLM 改写用户问题（标准化表述，便于后续检索与 SQL/DSL 生成），
// 并在 router 阶段触发四路 RAG 召回，把 DDL/Mapping/SQL/DSL 语料写入 State。
func NewInputProcess(ctx context.Context, chatModel model.ToolCallingChatModel) (*compose.Graph[string, string], error) {
	cag := compose.NewGraph[string, string]()

	nodes := []struct {
		name string
		lam  *compose.Lambda
	}{
		{"load", compose.InvokableLambdaWithOption(loadInputProcessMSG)},
		{"router", compose.InvokableLambdaWithOption(routerInputProcess)},
	}
	for _, node := range nodes {
		if err := cag.AddLambdaNode(node.name, node.lam); err != nil {
			return nil, fmt.Errorf("add %s lambda node: %w", node.name, err)
		}
	}
	if err := cag.AddChatModelNode("agent", chatModel); err != nil {
		return nil, fmt.Errorf("add agent model node: %w", err)
	}

	edges := []struct{ from, to string }{
		{compose.START, "load"},
		{"load", "agent"},
		{"agent", "router"},
		{"router", compose.END},
	}
	for _, edge := range edges {
		if err := cag.AddEdge(edge.from, edge.to); err != nil {
			return nil, fmt.Errorf("add %s→%s edge: %w", edge.from, edge.to, err)
		}
	}

	return cag, nil
}

// loadInputProcessMSG load 节点：组装输入改写阶段的 LLM 消息。
// input 为上游传入的节点名字符串，恰与 prompt md 文件名一致；
// 用户消息取 State 中尚未改写的原始问题。
func loadInputProcessMSG(ctx context.Context, input string, opts ...any) ([]*schema.Message, error) {
	output := make([]*schema.Message, 0)
	err := compose.ProcessState(ctx, func(ctx context.Context, state *State) error {
		systemPrompt, err := prompt.GetPrompt(ctx, input)
		if err != nil {
			return fmt.Errorf("get prompt: %w", err)
		}

		output = []*schema.Message{
			schema.SystemMessage(systemPrompt),
			schema.UserMessage(state.OldQuestion),
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("load input process msg: %w", err)
	}

	return output, nil
}

// routerInputProcess router 节点：接收 LLM 改写后的问题，完成本子图的核心副作用：
//  1. 用 LLM 输出覆盖 state.Question（原始问题保留在 OldQuestion）；
//  2. 依次调用四路 RAG 召回（ClickHouse DDL、ES Mapping、相似 SQL、相似 DSL），
//     各取第一条写入 State，供下游 searcher 拼 Prompt 时参考。
//     单路召回失败仅降级为空上下文（warn 日志），不中断主查询流程；
//  3. 固定流转到 Planner。
func routerInputProcess(ctx context.Context, input *schema.Message, opts ...any) (string, error) {
	output := ""
	err := compose.ProcessState(ctx, func(ctx context.Context, state *State) error {
		defer func() {
			output = state.Goto
		}()

		// 问题改写
		state.Question = input.Content
		state.Goto = Planner

		// 四路 RAG 召回（失败降级）
		state.DDL = recall(ctx, "ddl", state.DDLGenWithRerankFunc, state.Question)
		state.Mapping = recall(ctx, "mapping", state.MappingGenWithRerankFunc, state.Question)
		state.SQL = recall(ctx, "sql", state.SQLGenFunc, state.Question)
		state.DSL = recall(ctx, "dsl", state.DSLGenFunc, state.Question)
		slog.InfoContext(ctx, "rag recall done",
			slog.String("question", state.Question),
			slog.Int("ddl_len", len(state.DDL)),
			slog.Int("mapping_len", len(state.Mapping)),
			slog.Int("sql_len", len(state.SQL)),
			slog.Int("dsl_len", len(state.DSL)))

		return nil
	})
	if err != nil {
		return "", fmt.Errorf("router input process: %w", err)
	}

	return output, nil
}

// recall 执行单路召回并取第一条；函数未注入、失败或空结果均降级为空字符串
func recall(ctx context.Context, channel string, fn func(ctx context.Context, question string) ([]string, error), question string) string {
	if fn == nil {
		return ""
	}

	result, err := fn(ctx, question)
	if err != nil {
		slog.WarnContext(ctx, "rag recall degraded, continue with empty context",
			slog.String("channel", channel), slog.Any("error", err))
		return ""
	}
	if len(result) == 0 {
		return ""
	}

	return result[0]
}
