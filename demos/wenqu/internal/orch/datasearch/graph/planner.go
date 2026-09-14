package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"datasearch/internal/orch/datasearch/prompt"
)

// NewPlanner 构建"数据源规划"子图：load → agent(LLM) → router。
// 职责：由 LLM 判定改写后的问题需要查询哪些数据源（ClickHouse / Elasticsearch），
// 以 JSON 数组形式输出数据源节点名列表。
func NewPlanner(ctx context.Context, chatModel model.ToolCallingChatModel) (*compose.Graph[string, string], error) {
	cag := compose.NewGraph[string, string]()

	nodes := []struct {
		name string
		lam  *compose.Lambda
	}{
		{"load", compose.InvokableLambdaWithOption(loadPlannerMSG)},
		{"router", compose.InvokableLambdaWithOption(routerPlanner)},
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

// loadPlannerMSG load 节点：组装规划阶段的 LLM 消息。
// 用户消息为改写后的问题（规划仅基于问题本身，RAG 语料不参与）。
func loadPlannerMSG(ctx context.Context, input string, opts ...any) ([]*schema.Message, error) {
	output := make([]*schema.Message, 0)
	err := compose.ProcessState(ctx, func(ctx context.Context, state *State) error {
		systemPrompt, err := prompt.GetPrompt(ctx, input)
		if err != nil {
			return fmt.Errorf("get prompt: %w", err)
		}

		output = []*schema.Message{
			schema.SystemMessage(systemPrompt),
			schema.UserMessage(state.Question),
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("load planner msg: %w", err)
	}

	return output, nil
}

// routerPlanner router 节点：解析 LLM 输出的数据源列表。
// 约定 LLM 返回形如 ["clickhouse_searcher","elasticsearch_searcher"] 的 JSON 数组
// （元素值即主图节点名常量）。解析结果写入 state.DataSources 后流转到 SearchTeam。
//
// 容错策略：JSON 解析失败、结果为空或全部非法时，降级为"双源全查"——
// 宁可多查不可漏查，配合 warn 日志暴露异常。
func routerPlanner(ctx context.Context, input *schema.Message, opts ...any) (string, error) {
	output := ""
	err := compose.ProcessState(ctx, func(ctx context.Context, state *State) error {
		defer func() {
			output = state.Goto
		}()

		dataSources, err := parseDataSources(input.Content)
		if err != nil {
			slog.WarnContext(ctx, "planner output invalid, fallback to all data sources",
				slog.String("raw", input.Content), slog.Any("error", err))
			dataSources = searcherNodes
		}
		if len(dataSources) == 0 {
			dataSources = searcherNodes
		}

		state.DataSources = dataSources
		state.Goto = SearchTeam
		slog.InfoContext(ctx, "planner decided data sources", slog.Any("data_sources", dataSources))

		return nil
	})
	if err != nil {
		return "", fmt.Errorf("router planner: %w", err)
	}

	return output, nil
}

// parseDataSources 解析并过滤 LLM 输出：只保留指向合法搜索节点的非空元素。
// 兼容 LLM 输出节点名子串（如 "clickhouse"）与全文围栏（```json ... ```）。
func parseDataSources(content string) ([]string, error) {
	dataSources := make([]string, 0)
	if err := json.Unmarshal([]byte(stripJSONFence(content)), &dataSources); err != nil {
		return nil, fmt.Errorf("unmarshal data sources: %w", err)
	}

	filtered := make([]string, 0, len(dataSources))
	for _, ds := range dataSources {
		if ds == "" {
			continue
		}
		for _, node := range searcherNodes {
			// 子串匹配：允许 LLM 输出 "clickhouse" 命中 "clickhouse_searcher"
			if strings.Contains(node, ds) {
				filtered = append(filtered, node)
				break
			}
		}
	}
	// 去重（LLM 可能输出 ["clickhouse","clickhouse_searcher"] 双命中）
	dedup := make([]string, 0, len(filtered))
	seen := make(map[string]struct{}, len(filtered))
	for _, ds := range filtered {
		if _, ok := seen[ds]; ok {
			continue
		}
		seen[ds] = struct{}{}
		dedup = append(dedup, ds)
	}

	return dedup, nil
}
