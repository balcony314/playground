// Package datasearch 提供多数据源智能查询的编排入口。
// 由 service 层持有并调用，负责把各依赖（模型、RAG、数据源客户端）
// 注入 eino 状态图并驱动执行，返回 Markdown 查询报告。
package datasearch

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"
	"github.com/elastic/go-elasticsearch/v8"

	"datasearch/internal/callback"
	"datasearch/internal/client/db"
	"datasearch/internal/orch/datasearch/graph"
	"datasearch/internal/store"
)

// Agent 是多数据源查询编排（datasearch）的对外入口
type Agent struct {
	chatModel   model.ToolCallingChatModel // 所有 LLM 节点共用的对话模型
	vectorStore *store.VectorStore         // Milvus 向量库，RAG 召回的底层实现
	ch          *db.ClickHouseClient       // ClickHouse 客户端，searcher 子图执行 SQL 用
	esCli       *elasticsearch.TypedClient // ES 客户端，searcher 子图执行 DSL 用
	maxRetry    int                        // 每个数据源的最大尝试次数（<=0 用默认值）
}

// NewAgent 创建 datasearch 编排 agent。不做重初始化 IO（图在每次查询时构建），仅持有依赖
func NewAgent(chatModel model.ToolCallingChatModel, vectorStore *store.VectorStore, ch *db.ClickHouseClient, esCli *elasticsearch.TypedClient, maxRetry int) *Agent {
	return &Agent{
		chatModel:   chatModel,
		vectorStore: vectorStore,
		ch:          ch,
		esCli:       esCli,
		maxRetry:    maxRetry,
	}
}

// DataSearch 执行一次多数据源检索，返回 Markdown 格式报告。整体流程：
//  1. genFunc 定义本次请求的初始 State：写入原始问题，并把四个 RAG 召回方法
//     以闭包形式注入 State.UserInputGenFunc，使 graph 包与 store 解耦；
//  2. 现场构建并编译状态图（每请求重建，State 相互隔离），
//     以 InputProcess 节点名作为图输入启动（该值透传给 load 节点用于加载 prompt）；
//  3. 挂载 LoggerCallback 记录全链路 LLM/节点调用日志；
//  4. 图执行至 OutputSummary 节点产出报告，作为 Runnable 的返回值带回。
func (a *Agent) DataSearch(ctx context.Context, question string) (string, error) {
	genFunc := func(ctx context.Context) *graph.State {
		return &graph.State{
			UserInput: graph.UserInput{
				OldQuestion: question,
				Question:    question,
			},
			UserInputGenFunc: graph.UserInputGenFunc{
				DDLGenWithRerankFunc:     a.GetRelatedDDLWithRerank,
				MappingGenWithRerankFunc: a.GetRelatedMappingWithRerank,
				SQLGenFunc:               a.GetRelatedQuestionSQL,
				DSLGenFunc:               a.GetRelatedQuestionDSL,
			},
			Goto:        graph.InputProcess,
			DataSources: make([]string, 0),
			RespMap:     make(map[string]string),
			Query:       make(map[string][]graph.Attempt),
			MaxRetry:    a.maxRetry,
			CH:          a.ch,
			ESCli:       a.esCli,
		}
	}

	r, err := graph.Builder(ctx, genFunc, a.chatModel)
	if err != nil {
		return "", fmt.Errorf("graph builder: %w", err)
	}

	report, err := r.Invoke(ctx, graph.InputProcess,
		compose.WithCallbacks(&callback.LoggerCallback{ParentCtx: ctx}))
	if err != nil {
		return "", fmt.Errorf("graph invoke: %w", err)
	}

	return report, nil
}

// GetRelatedDDLWithRerank 召回相关 ClickHouse DDL 并经 rerank 精排，
// 被 graph.State.DDLGenWithRerankFunc 引用，在 input_process 的 router 中调用
func (a *Agent) GetRelatedDDLWithRerank(ctx context.Context, question string) ([]string, error) {
	relatedDDL, err := a.vectorStore.GetRelatedDDLWithRerank(ctx, question)
	if err != nil {
		return []string{}, fmt.Errorf("get related ddl with rerank: %w", err)
	}

	return relatedDDL, nil
}

// GetRelatedMappingWithRerank 召回相关 ES Index Mapping 并精排
func (a *Agent) GetRelatedMappingWithRerank(ctx context.Context, question string) ([]string, error) {
	relatedMapping, err := a.vectorStore.GetRelatedMappingWithRerank(ctx, question)
	if err != nil {
		return []string{}, fmt.Errorf("get related mapping with rerank: %w", err)
	}

	return relatedMapping, nil
}

// GetRelatedQuestionSQL 召回"问题→SQL"相似语料，
// 为 clickhouse_searcher 提供同义问题的历史 SQL 写法参考
func (a *Agent) GetRelatedQuestionSQL(ctx context.Context, question string) ([]string, error) {
	relatedSQL, err := a.vectorStore.GetRelatedQuestionSQL(ctx, question)
	if err != nil {
		return []string{}, fmt.Errorf("get related question sql: %w", err)
	}

	return relatedSQL, nil
}

// GetRelatedQuestionDSL 召回"问题→DSL"相似语料，
// 为 elasticsearch_searcher 提供同义问题的历史 DSL 写法参考
func (a *Agent) GetRelatedQuestionDSL(ctx context.Context, question string) ([]string, error) {
	relatedDSL, err := a.vectorStore.GetRelatedQuestionDSL(ctx, question)
	if err != nil {
		return []string{}, fmt.Errorf("get related question dsl: %w", err)
	}

	return relatedDSL, nil
}
