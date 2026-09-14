package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"

	"datasearch/internal/orch/datasearch/prompt"
	"datasearch/internal/pkg/sqlguard"
)

// NewClickhouseSearcher 构建"ClickHouse 搜索"子图：load → agent(react agent) → router。
// 职责：基于 RAG 召回的 DDL/SQL 语料，让 LLM 生成一条可执行的 ClickHouse SQL；
// router 节点先做只读校验（sqlguard），再经 State 里的客户端执行。
func NewClickhouseSearcher(ctx context.Context, chatModel model.ToolCallingChatModel) (*compose.Graph[string, string], error) {
	cag := compose.NewGraph[string, string]()

	// react agent 承载"思考 → 自我校验 → 输出"的多轮推理；
	// 不挂工具（表结构来自 RAG 注入），MaxStep 限制内部循环上限
	agent, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: chatModel,
		MaxStep:          40,
	})
	if err != nil {
		return nil, fmt.Errorf("new clickhouse react agent: %w", err)
	}
	agentLambda, err := compose.AnyLambda(agent.Generate, agent.Stream, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("compose any lambda: %w", err)
	}

	if err := cag.AddLambdaNode("load", compose.InvokableLambdaWithOption(loadClickhouseSearcherMSG)); err != nil {
		return nil, fmt.Errorf("add load lambda node: %w", err)
	}
	if err := cag.AddLambdaNode("agent", agentLambda); err != nil {
		return nil, fmt.Errorf("add agent lambda node: %w", err)
	}
	if err := cag.AddLambdaNode("router", compose.InvokableLambdaWithOption(routerClickhouseSearcher)); err != nil {
		return nil, fmt.Errorf("add router lambda node: %w", err)
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

// loadClickhouseSearcherMSG load 节点：组装 SQL 生成阶段的 react agent 输入。
// 用户消息 = 改写后的问题 + RAG 召回的 DDL/相似 SQL 参考；
// 若存在失败尝试（自愈重试场景），附带出错的 SQL 与报错，要求 LLM 定向修复。
func loadClickhouseSearcherMSG(ctx context.Context, input string, opts ...any) ([]*schema.Message, error) {
	output := make([]*schema.Message, 0)
	err := compose.ProcessState[*State](ctx, func(ctx context.Context, state *State) error {
		systemPrompt, err := prompt.GetPrompt(ctx, input)
		if err != nil {
			return fmt.Errorf("get prompt: %w", err)
		}

		output = generateSQLPrompt(systemPrompt, state.UserInput, state.Query[ClickhouseSearcher])

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("load clickhouse searcher msg: %w", err)
	}

	return output, nil
}

// routerClickhouseSearcher router 节点：校验并执行生成的 SQL，登记结果。
// 流程：
//  1. 把生成的 SQL 追加到 state.Query（尝试历史，失败报错也会回写到这里）；
//  2. sqlguard 只读校验——Prompt 层约束被绕过时在此兜底拦截；
//  3. 经 State 里的客户端执行（Raw 查询 → 通用行结构）；
//  4. 执行成功才写入 state.RespMap。失败不返回 error（那样会中断整图），
//     而是把报错记入最近一次 Attempt 后回到 SearchTeam：
//     由于 RespMap 无值，调度节点将重新派发本节点，load 节点会带上
//     出错语句与报错再次生成——形成闭环自愈重试；
//  5. 无论成败，Goto 固定回 SearchTeam 继续调度。
func routerClickhouseSearcher(ctx context.Context, input *schema.Message, opts ...any) (string, error) {
	output := ""
	err := compose.ProcessState[*State](ctx, func(ctx context.Context, state *State) error {
		defer func() {
			output = state.Goto
		}()

		sql := trimStatement(input.Content)
		state.Goto = SearchTeam
		state.Query[ClickhouseSearcher] = append(state.Query[ClickhouseSearcher], Attempt{Statement: sql})
		slog.InfoContext(ctx, "clickhouse searcher gen sql", slog.String("sql", sql))

		// 只读校验（代码层兜底）
		if err := sqlguard.ValidateReadOnly(sql); err != nil {
			markAttemptErr(state, ClickhouseSearcher, err)
			slog.WarnContext(ctx, "sqlguard rejected non-read-only sql", slog.Any("error", err))
			return nil // 回调度节点自愈重试
		}

		if state.CH == nil {
			err := fmt.Errorf("clickhouse client is nil")
			markAttemptErr(state, ClickhouseSearcher, err)
			return nil
		}

		// 执行 SQL：空结果集仍视为成功
		results, err := state.CH.QueryToMaps(ctx, sql)
		if err != nil {
			markAttemptErr(state, ClickhouseSearcher, err)
			slog.WarnContext(ctx, "clickhouse exec failed, will retry", slog.Any("error", err))
			return nil // 回调度节点自愈重试
		}
		resultBytes, err := json.Marshal(results)
		if err != nil {
			markAttemptErr(state, ClickhouseSearcher, fmt.Errorf("marshal results: %w", err))
			return nil
		}
		slog.InfoContext(ctx, "clickhouse searcher results", slog.String("results", truncateForLog(string(resultBytes))))
		state.RespMap[ClickhouseSearcher] = string(resultBytes)

		return nil
	})
	if err != nil {
		return "", fmt.Errorf("router clickhouse searcher: %w", err)
	}

	return output, nil
}

// markAttemptErr 把报错写入某源最近一次尝试，供自愈重试时回看
func markAttemptErr(state *State, node string, err error) {
	attempts := state.Query[node]
	if len(attempts) == 0 {
		return
	}
	attempts[len(attempts)-1].Err = err.Error()
}

// truncateForLog 日志截断，避免超大结果集刷屏
func truncateForLog(s string) string {
	const max = 500
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}

	return string(runes[:max]) + "..."
}
