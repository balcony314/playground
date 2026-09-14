// Package service 封装业务域与 Agent 的交互，对 api 层暴露语义化方法，
// 屏蔽"单源 ReAct 查询"与"多源状态图编排"两种实现的选择细节。
package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/elastic/go-elasticsearch/v8"

	"datasearch/internal/agent/clickhouse"
	"datasearch/internal/client/db"
	"datasearch/internal/orch/datasearch"
	"datasearch/internal/store"
)

// DBSearchService 数据查询业务域的 service 层封装
type DBSearchService struct {
	ckSearchAgent   *clickhouse.Agent // ClickHouse 单源查询：react agent（MCP 工具自主调用）
	dataSearchAgent *datasearch.Agent // 多数据源查询：eino 状态图编排
}

// NewDBSearchService 组装数据查询服务，依赖全部由 main.go 注入：
//   - defaultChatModel：经 OpenRouter 的对话模型，两个 agent 共用；
//   - mcpClickhouseTools：ClickHouse MCP 工具（已按白名单收窄），给 react agent；
//   - vectorStore：Milvus 向量库，用于多源查询的 RAG 召回；
//   - ch/esCli：真实执行查询的客户端；
//   - maxRetry：多源链路每个数据源的自愈重试上限。
func NewDBSearchService(ctx context.Context, defaultChatModel model.ToolCallingChatModel, mcpClickhouseTools []tool.BaseTool, vectorStore *store.VectorStore, ch *db.ClickHouseClient, esCli *elasticsearch.TypedClient, maxRetry int) (*DBSearchService, error) {
	ckSearchAgent, err := clickhouse.NewAgent(ctx, defaultChatModel, mcpClickhouseTools)
	if err != nil {
		return nil, fmt.Errorf("new clickhouse agent: %w", err)
	}

	dataSearchAgent := datasearch.NewAgent(defaultChatModel, vectorStore, ch, esCli, maxRetry)

	return &DBSearchService{
		ckSearchAgent:   ckSearchAgent,
		dataSearchAgent: dataSearchAgent,
	}, nil
}

// SearchFromClickhouse 单源查询：交给 react agent，
// 由 LLM 自主调用 ClickHouse MCP 工具完成"问题 → SQL → 执行 → 总结"
func (s *DBSearchService) SearchFromClickhouse(ctx context.Context, content string) (string, error) {
	msg, err := s.ckSearchAgent.Generate(ctx, content)
	if err != nil {
		return "", fmt.Errorf("search from clickhouse: %w", err)
	}
	slog.InfoContext(ctx, "clickhouse search done", slog.Int("resp_len", len(msg.Content)))

	return msg.Content, nil
}

// StreamFromClickhouse 单源查询的流式版本：打字机式增量返回
func (s *DBSearchService) StreamFromClickhouse(ctx context.Context, content string) (*schema.StreamReader[*schema.Message], error) {
	reader, err := s.ckSearchAgent.Stream(ctx, content)
	if err != nil {
		return nil, fmt.Errorf("stream from clickhouse: %w", err)
	}

	return reader, nil
}

// Search 多源查询：交给 datasearch 状态图编排
// （输入改写 → 规划选数据源 → 依次查 ClickHouse/ES（失败自愈重试）→ 汇总 Markdown 报告）
func (s *DBSearchService) Search(ctx context.Context, content string) (string, error) {
	report, err := s.dataSearchAgent.DataSearch(ctx, content)
	if err != nil {
		return "", fmt.Errorf("multi datasource search: %w", err)
	}
	slog.InfoContext(ctx, "multi datasource search done", slog.Int("report_len", len(report)))

	return report, nil
}
