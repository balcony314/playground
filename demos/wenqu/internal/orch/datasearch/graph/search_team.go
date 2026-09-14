package graph

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/cloudwego/eino/compose"
)

// NewSearchTeam 构建"任务调度"子图：仅一个 router 节点，不调 LLM，纯状态判断。
// 它是多源查询的循环枢纽：每个搜索子图完成后都回到这里，
// 由它决定"下一个还没查完的数据源"或"全部完成、流转汇总"。
func NewSearchTeam(ctx context.Context) (*compose.Graph[string, string], error) {
	cag := compose.NewGraph[string, string]()
	if err := cag.AddLambdaNode("router", compose.InvokableLambdaWithOption(routerSearchTeam)); err != nil {
		return nil, fmt.Errorf("add router lambda node: %w", err)
	}

	edges := []struct{ from, to string }{
		{compose.START, "router"},
		{"router", compose.END},
	}
	for _, edge := range edges {
		if err := cag.AddEdge(edge.from, edge.to); err != nil {
			return nil, fmt.Errorf("add %s→%s edge: %w", edge.from, edge.to, err)
		}
	}

	return cag, nil
}

// routerSearchTeam 调度逻辑，自愈重试闭环的枢纽：
//
//	未完成 = RespMap 无结果。
//	  - 尝试次数 < MaxRetry → 派发该源（重新生成查询语句）；
//	  - 尝试次数 ≥ MaxRetry → 写入失败占位结果（视为完成，保证流程必然终止）。
//	全部完成 → 流转 OutputSummary 生成报告。
//
// 每轮只派发一个源，剩余源在下一轮循环中继续。
func routerSearchTeam(ctx context.Context, input string, opts ...any) (string, error) {
	output := ""
	err := compose.ProcessState(ctx, func(ctx context.Context, state *State) error {
		defer func() {
			output = state.Goto
		}()

		// 超限未完成的源写入失败占位（记录最后一次报错，报告中可见）
		for _, node := range pendingSearchers(state) {
			if len(state.Query[node]) >= state.maxRetry() {
				lastErr := lastAttemptErr(state, node)
				state.RespMap[node] = failurePlaceholder(node, state.maxRetry(), lastErr)
				slog.WarnContext(ctx, "searcher exhausted retries, mark as failed",
					slog.String("node", node), slog.Int("max_retry", state.maxRetry()), slog.String("last_error", lastErr))
			}
		}

		// 找第一个"未完成且未超限"的源派发
		state.Goto = OutputSummary
		for _, node := range pendingSearchers(state) {
			if len(state.Query[node]) < state.maxRetry() {
				state.Goto = node
				break
			}
		}
		slog.InfoContext(ctx, "search team dispatch", slog.String("goto", state.Goto),
			slog.Any("data_sources", state.DataSources), slog.Any("resp_map_keys", keys(state.RespMap)))

		return nil
	})
	if err != nil {
		return "", fmt.Errorf("router search team: %w", err)
	}

	return output, nil
}

// pendingSearchers 返回 planner 选定、且尚未拿到结果的搜索节点（按固定顺序）
func pendingSearchers(state *State) []string {
	pending := make([]string, 0, len(searcherNodes))
	for _, node := range searcherNodes {
		if _, done := state.RespMap[node]; done {
			continue
		}
		for _, ds := range state.DataSources {
			if ds != "" && strings.Contains(node, ds) {
				pending = append(pending, node)
				break
			}
		}
	}

	return pending
}

// lastAttemptErr 取某源最近一次尝试的报错（无尝试历史时返回空串）
func lastAttemptErr(state *State, node string) string {
	attempts := state.Query[node]
	if len(attempts) == 0 {
		return ""
	}

	return attempts[len(attempts)-1].Err
}

// failurePlaceholder 生成失败占位结果（JSON），让上层无需区分成功/失败结果的结构
func failurePlaceholder(node string, maxRetry int, lastErr string) string {
	if lastErr == "" {
		lastErr = "unknown error"
	}

	return fmt.Sprintf(`{"error":"%s exhausted %d attempts, last error: %s"}`, node, maxRetry, escapeJSON(lastErr))
}

// escapeJSON 最小化的 JSON 字符串转义（报错信息可能含引号/换行）
func escapeJSON(s string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`)

	return replacer.Replace(s)
}

// keys 取 map 的 key 列表（日志用）
func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	return out
}
