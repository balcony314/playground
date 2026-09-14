package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"

	"datasearch/internal/orch/datasearch/prompt"
)

// NewElasticSearchSearcher 构建"Elasticsearch 搜索"子图：load → agent(react agent) → router。
// 与 ClickhouseSearcher 结构对称：基于 RAG 召回的 Mapping/DSL 语料生成 ES 查询 DSL，
// LLM 输出结构化 {"index":..,"dsl":{..}}，router 节点解析执行。
func NewElasticSearchSearcher(ctx context.Context, chatModel model.ToolCallingChatModel) (*compose.Graph[string, string], error) {
	cag := compose.NewGraph[string, string]()

	agent, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: chatModel,
		MaxStep:          40,
	})
	if err != nil {
		return nil, fmt.Errorf("new elasticsearch react agent: %w", err)
	}
	agentLambda, err := compose.AnyLambda(agent.Generate, agent.Stream, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("compose any lambda: %w", err)
	}

	if err := cag.AddLambdaNode("load", compose.InvokableLambdaWithOption(loadElasticSearchSearcherMSG)); err != nil {
		return nil, fmt.Errorf("add load lambda node: %w", err)
	}
	if err := cag.AddLambdaNode("agent", agentLambda); err != nil {
		return nil, fmt.Errorf("add agent lambda node: %w", err)
	}
	if err := cag.AddLambdaNode("router", compose.InvokableLambdaWithOption(routerElasticSearchSearcher)); err != nil {
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

// loadElasticSearchSearcherMSG load 节点：组装 DSL 生成阶段的 react agent 输入。
// 用户消息 = 改写后的问题 + RAG 召回的 Index Mapping/相似 DSL 参考；
// 若存在失败尝试（自愈重试场景），附带出错的 DSL 与报错。
func loadElasticSearchSearcherMSG(ctx context.Context, input string, opts ...any) ([]*schema.Message, error) {
	output := make([]*schema.Message, 0)
	err := compose.ProcessState[*State](ctx, func(ctx context.Context, state *State) error {
		systemPrompt, err := prompt.GetPrompt(ctx, input)
		if err != nil {
			return fmt.Errorf("get prompt: %w", err)
		}

		output = generateDSLPrompt(systemPrompt, state.UserInput, state.Query[ElasticSearchSearcher])

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("load elasticsearch searcher msg: %w", err)
	}

	return output, nil
}

// dslResp 是约定给 LLM 输出的结构：目标索引名 + 查询 DSL。
// Dsl 用 json.RawMessage 以同时兼容对象形式（"dsl":{...}）与
// 字符串形式（"dsl":"{...}"，LLM 常见的转义习惯），执行前统一展开。
type dslResp struct {
	Index string          `json:"index"`
	Dsl   json.RawMessage `json:"dsl"`
}

// routerElasticSearchSearcher router 节点：解析并执行生成的 DSL。
// 解析或执行失败时与 ClickHouse 侧相同：不中断图，报错记入 Attempt，
// 回 SearchTeam 重新派发，由 load 节点携带出错上下文再生——闭环自愈。
func routerElasticSearchSearcher(ctx context.Context, input *schema.Message, opts ...any) (string, error) {
	output := ""
	err := compose.ProcessState[*State](ctx, func(ctx context.Context, state *State) error {
		defer func() {
			output = state.Goto
		}()

		statement := trimStatement(stripJSONFence(input.Content))
		state.Goto = SearchTeam
		state.Query[ElasticSearchSearcher] = append(state.Query[ElasticSearchSearcher], Attempt{Statement: statement})
		slog.InfoContext(ctx, "es searcher gen dsl", slog.String("dsl", statement))

		var llmResp dslResp
		if err := json.Unmarshal([]byte(statement), &llmResp); err != nil {
			markAttemptErr(state, ElasticSearchSearcher, fmt.Errorf("unmarshal dsl response: %w", err))
			slog.WarnContext(ctx, "es dsl unmarshal failed, will retry", slog.Any("error", err))
			return nil
		}
		if llmResp.Index == "" {
			err := errors.New("es dsl response missing index")
			markAttemptErr(state, ElasticSearchSearcher, err)
			return nil
		}
		dsl, err := unwrapDSL(llmResp.Dsl)
		if err != nil {
			markAttemptErr(state, ElasticSearchSearcher, err)
			return nil
		}

		if state.ESCli == nil {
			markAttemptErr(state, ElasticSearchSearcher, errors.New("elasticsearch client is nil"))
			return nil
		}

		// 执行 DSL（Search API，天然只读）
		resp, err := state.ESCli.Search().Index(llmResp.Index).Raw(strings.NewReader(dsl)).Do(ctx)
		if err != nil {
			markAttemptErr(state, ElasticSearchSearcher, fmt.Errorf("es search exec: %w", err))
			slog.WarnContext(ctx, "es exec failed, will retry", slog.Any("error", err))
			return nil
		}
		resultBytes, err := json.Marshal(resp.Hits.Hits)
		if err != nil {
			markAttemptErr(state, ElasticSearchSearcher, fmt.Errorf("marshal hits: %w", err))
			return nil
		}
		slog.InfoContext(ctx, "es searcher results", slog.String("results", truncateForLog(string(resultBytes))))
		state.RespMap[ElasticSearchSearcher] = string(resultBytes)

		return nil
	})
	if err != nil {
		return "", fmt.Errorf("router elasticsearch searcher: %w", err)
	}

	return output, nil
}

// stripJSONFence 剥离 LLM 输出可能携带的 Markdown 代码围栏
// （```json ... ``` 或 ``` ... ```），返回内层内容。
func stripJSONFence(content string) string {
	out := strings.TrimSpace(content)
	if !strings.HasPrefix(out, "```") {
		return out
	}

	// 去掉开头围栏行（可能带 json/lang 标记）
	if idx := strings.Index(out, "\n"); idx >= 0 {
		out = out[idx+1:]
	}
	// 去掉结尾围栏
	out = strings.TrimSuffix(strings.TrimSpace(out), "```")

	return strings.TrimSpace(out)
}

// unwrapDSL 把 RawMessage 形式的 DSL 展开为可执行的字符串：
// 字符串形式先反序列化取内层，对象形式原样返回。
func unwrapDSL(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", errors.New("es dsl response missing dsl")
	}

	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}

	return string(raw), nil
}
