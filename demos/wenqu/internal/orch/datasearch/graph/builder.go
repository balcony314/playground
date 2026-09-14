// Package graph 构建多数据源查询的 eino compose 有向状态图。
// 拓扑（子图之间通过 SearchTeam 后的动态分支循环流转）：
//
//	START → InputProcess → Planner → SearchTeam ─┬→ ClickhouseSearcher ──→ 回 SearchTeam
//	                                             ├→ ElasticSearchSearcher → 回 SearchTeam
//	                                             └→ OutputSummary（全部完成，生成 Markdown 报告）→ END
package graph

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"
	"github.com/elastic/go-elasticsearch/v8"

	"datasearch/internal/client/db"
)

// 主图的节点名常量，同时承担三个职责：
//  1. AddGraphNode 的节点标识；
//  2. state.Goto 的流转目标值（子图 router 写入，branch 函数读取）；
//  3. prompt md 文件名（load 节点把节点名透传给 prompt.GetPrompt）。
const (
	InputProcess          = "input_process"          // 输入改写：标准化问题 + RAG 召回上下文
	Planner               = "planner"                // 规划器：判定问题需要查哪些数据源
	SearchTeam            = "search_team"            // 调度器：按数据源完成度派发任务、判定是否结束
	ClickhouseSearcher    = "clickhouse_searcher"    // ClickHouse 搜索：生成 SQL → 只读校验 → 执行
	ElasticSearchSearcher = "elasticsearch_searcher" // ES 搜索：生成 {index,dsl} → 解析 → 执行
	OutputSummary         = "output_summary"         // 结果汇总：生成最终 Markdown 查询报告
)

// searcherNodes 调度器可派发的搜索节点全集
var searcherNodes = []string{ClickhouseSearcher, ElasticSearchSearcher}

// DefaultMaxRetry 每个数据源的默认最大尝试次数（含首次）
const DefaultMaxRetry = 2

// UserInput 贯穿整个图的用户问题及其加工产物。
// OldQuestion 保留原始问题，Question 是经 input_process 改写后供下游使用的问题，
// DDL/Mapping/SQL/DSL 是 RAG 召回的参考语料。
type UserInput struct {
	OldQuestion string `json:"old_question"`
	Question    string `json:"question,omitempty"`
	DDL         string `json:"ddl,omitempty"`
	Mapping     string `json:"mapping,omitempty"`
	SQL         string `json:"sql,omitempty"`
	DSL         string `json:"dsl,omitempty"`
}

// UserInputGenFunc RAG 召回函数集合。
// 以闭包形式由外层（orch/datasearch/agent.go）注入到 State，
// 使 graph 包不直接依赖 store 向量库——这是解耦 Milvus 实现的关键。
type UserInputGenFunc struct {
	DDLGenWithRerankFunc     func(ctx context.Context, question string) ([]string, error) // 召回相关 ClickHouse 建表语句（带 rerank）
	MappingGenWithRerankFunc func(ctx context.Context, question string) ([]string, error) // 召回相关 ES Index Mapping（带 rerank）
	SQLGenFunc               func(ctx context.Context, question string) ([]string, error) // 召回相似"问题→SQL"语料
	DSLGenFunc               func(ctx context.Context, question string) ([]string, error) // 召回相似"问题→DSL"语料
}

// Attempt 记录一次"生成 → 执行"的尝试，是自愈重试闭环的载体：
// 执行失败时把报错写入最近一次 Attempt.Err，重试派发时 load 节点
// 会把失败的语句与报错注入 Prompt，供 LLM 定向修复。
type Attempt struct {
	Statement string `json:"statement"`
	Err       string `json:"err,omitempty"`
}

// State 是整张主图的共享状态（由 GenLocalState 每次请求惰性生成，
// 同一张编译后的图可被多个请求复用而互不干扰）。
// 所有子图通过 compose.ProcessState 读写同一份 State，这是子图间传递数据的唯一通道。
type State struct {
	// 用户输入
	UserInput
	UserInputGenFunc

	// 子图共享变量
	Goto        string               `json:"goto,omitempty"`         // 下一个要流转到的节点名，由各子图的 router 写入
	DataSources []string             `json:"data_sources,omitempty"` // planner 判定出的数据源列表
	Query       map[string][]Attempt `json:"query,omitempty"`        // 每个搜索子图的尝试历史（按节点名分组）
	RespMap     map[string]string    `json:"resp_map,omitempty"`     // 每个搜索子图的最终结果；某源有值即视为该源已完成
	MaxRetry    int                  // 每源最大尝试次数（含首次），<=0 时取 DefaultMaxRetry

	// 数据源执行器
	CH    *db.ClickHouseClient       // ClickHouse 客户端，clickhouse_searcher 执行 SQL 用
	ESCli *elasticsearch.TypedClient // ES 客户端，elasticsearch_searcher 执行 DSL 用
}

// maxRetry 返回有效的重试上限
func (s *State) maxRetry() int {
	if s.MaxRetry <= 0 {
		return DefaultMaxRetry
	}

	return s.MaxRetry
}

// agentHandOff 是主图 SearchTeam 节点后的分支函数，实现子图间的动态流转：
// 上一个子图把目标节点名写入 state.Goto，本函数读取并返回给 eino 框架做路由。
func agentHandOff(ctx context.Context, input string) (next string, err error) {
	defer func() {
		slog.DebugContext(ctx, "agentHandOff", slog.String("input", input), slog.String("next", next))
	}()
	_ = compose.ProcessState[*State](ctx, func(_ context.Context, state *State) error {
		next = state.Goto
		return nil
	})

	return next, nil
}

// Builder 构建并编译 datasearch 主图，返回可执行的 Runnable[string, string]：
// 输入为起始节点名（透传给 load 节点用于加载 prompt），输出为最终 Markdown 报告。
//
// genFunc：每次请求生成初始 State 的函数（见 orch/datasearch/agent.go）；
// chatModel：所有 LLM 节点共用的对话模型。
func Builder(ctx context.Context, genFunc compose.GenLocalState[*State], chatModel model.ToolCallingChatModel) (compose.Runnable[string, string], error) {
	// 状态由函数惰性创建而非图构建时固定
	g := compose.NewGraph[string, string](
		compose.WithGenLocalState(genFunc),
	)

	// branch 的候选出口白名单：agentHandOff 返回的 next 必须在此集合内
	outMap := map[string]bool{
		ClickhouseSearcher:    true,
		ElasticSearchSearcher: true,
		OutputSummary:         true,
	}

	// 构建五个子图。每个子图内部都是"load（组装 prompt）→ agent（调 LLM）→ router（写 State 并决定流转）"的固定结构
	inputProcessGraph, err := NewInputProcess(ctx, chatModel)
	if err != nil {
		return nil, fmt.Errorf("new input process: %w", err)
	}
	plannerGraph, err := NewPlanner(ctx, chatModel)
	if err != nil {
		return nil, fmt.Errorf("new planner: %w", err)
	}
	searchTeamGraph, err := NewSearchTeam(ctx)
	if err != nil {
		return nil, fmt.Errorf("new search team: %w", err)
	}
	clickhouseSearcherGraph, err := NewClickhouseSearcher(ctx, chatModel)
	if err != nil {
		return nil, fmt.Errorf("new clickhouse searcher: %w", err)
	}
	elasticSearchSearcherGraph, err := NewElasticSearchSearcher(ctx, chatModel)
	if err != nil {
		return nil, fmt.Errorf("new elasticsearch searcher: %w", err)
	}

	// 以"图节点"方式挂载子图，子图整体作为主图的一个节点
	for _, node := range []struct {
		name string
		sub  *compose.Graph[string, string]
	}{
		{InputProcess, inputProcessGraph},
		{Planner, plannerGraph},
		{SearchTeam, searchTeamGraph},
		{ClickhouseSearcher, clickhouseSearcherGraph},
		{ElasticSearchSearcher, elasticSearchSearcherGraph},
	} {
		if err := g.AddGraphNode(node.name, node.sub, compose.WithNodeName(node.name)); err != nil {
			return nil, fmt.Errorf("add graph node %s: %w", node.name, err)
		}
	}

	// 结果汇总节点：纯状态读取，不调 LLM
	if err := g.AddLambdaNode(OutputSummary, compose.InvokableLambda(outputSummary)); err != nil {
		return nil, fmt.Errorf("add output summary node: %w", err)
	}

	// 固定的主干边：入口 → 输入改写 → 规划 → 调度
	edges := []struct{ from, to string }{
		{compose.START, InputProcess},
		{InputProcess, Planner},
		{Planner, SearchTeam},
	}
	for _, edge := range edges {
		if err := g.AddEdge(edge.from, edge.to); err != nil {
			return nil, fmt.Errorf("add edge %s→%s: %w", edge.from, edge.to, err)
		}
	}

	// SearchTeam 之后是动态分支：由 agentHandOff 读取 state.Goto 决定去向
	if err := g.AddBranch(SearchTeam, compose.NewGraphBranch(agentHandOff, outMap)); err != nil {
		return nil, fmt.Errorf("add search team branch: %w", err)
	}
	// 两个搜索子图执行完固定回到 SearchTeam，形成"调度 → 搜索 → 再调度"的循环，
	// 直到所有数据源都在 RespMap 中有结果（由 search_team 的 router 判定后 Goto=OutputSummary）
	for _, searcher := range searcherNodes {
		if err := g.AddEdge(searcher, SearchTeam); err != nil {
			return nil, fmt.Errorf("add %s→search_team edge: %w", searcher, err)
		}
	}
	if err := g.AddEdge(OutputSummary, compose.END); err != nil {
		return nil, fmt.Errorf("add output summary edge: %w", err)
	}

	// 编译为可执行 Runnable（编译后图结构不可变，可重复运行）
	r, err := g.Compile(ctx)
	if err != nil {
		return nil, fmt.Errorf("compile graph: %w", err)
	}

	return r, nil
}

// outputSummary 结果汇总节点：读取最终 State 生成 Markdown 查询报告。
// 相比把报告写入 Agent 实例上的共享 map（跨请求覆盖的风险），
// 汇总作为图节点输出，随本次请求的 Runnable 返回，天然请求隔离。
func outputSummary(ctx context.Context, input string) (string, error) {
	report := ""
	err := compose.ProcessState[*State](ctx, func(ctx context.Context, state *State) error {
		report = GenerateMarkdownReport(state)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("output summary process state: %w", err)
	}

	return report, nil
}
