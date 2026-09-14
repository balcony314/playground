// datasearch server：多数据源智能查询 Agent 平台的 HTTP 服务入口。
// 职责：解析命令行/环境变量配置 → 建立外部依赖连接（Milvus/ES/ClickHouse/MCP）→
// 组装 chat model 与两类 agent（react 单源 / 状态图多源）→ 启动 echo HTTP 服务。
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	v1 "datasearch/internal/api/v1"
	"datasearch/internal/chatmodel"
	"datasearch/internal/client/db"
	"datasearch/internal/embedding"
	"datasearch/internal/orch/datasearch/graph"
	"datasearch/internal/service"
	"datasearch/internal/store"
	"datasearch/internal/tool"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server exited with error", slog.Any("err", err))
		os.Exit(1)
	}
}

func run() error {
	// ---- 配置解析：全部支持 flag 与同名大写环境变量两种来源，避免硬编码凭证 ----
	cfg := loadConfig()

	if err := setupLogger(cfg.LogLevel); err != nil {
		return fmt.Errorf("setup logger: %w", err)
	}
	slog.Info("starting datasearch server",
		slog.String("listen", cfg.HTTPListen),
		slog.String("model", cfg.ModelName),
	)

	if cfg.APIKey == "" {
		return fmt.Errorf("api key is required: set -api-key or OPEN_AI_API_KEY env")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// ---- 对话模型：经 OpenRouter 统一网关 ----
	chatModel, err := chatmodel.NewOpenAIChatModel(ctx, chatmodel.Config{
		OpenRouterBaseURL: cfg.OpenRouterBaseURL,
		APIKey:            cfg.APIKey,
		ModelName:         chatmodel.ModelName(cfg.ModelName),
	})
	if err != nil {
		return fmt.Errorf("new chat model: %w", err)
	}

	// ---- Milvus 向量库 + Embedding/Rerank：多源链路的 RAG 召回 ----
	milvusCli, err := db.NewMilvus(db.MilvusConfig{Address: cfg.MilvusAddr}, cfg.MilvusDatabase)
	if err != nil {
		return fmt.Errorf("connect milvus %s: %w", cfg.MilvusAddr, err)
	}
	defer func() { _ = milvusCli.Close(ctx) }()

	embedder, err := embedding.NewCustomEmbedder(&embedding.CustomEmbedderConfig{
		BaseURL:      cfg.EmbeddingBaseURL,
		DefaultModel: cfg.EmbeddingModel,
	})
	if err != nil {
		return fmt.Errorf("new embedder: %w", err)
	}

	// 向量维度须与 embedding 模型一致且建表后不可改，启动时动态探测
	dim, err := embedding.GetModelDim(embedder)
	if err != nil {
		return fmt.Errorf("detect embedding dim: %w", err)
	}
	slog.Info("embedding model ready", slog.String("model", cfg.EmbeddingModel), slog.Int64("dim", dim))

	rerankModel := store.NewQwenRerankModel(store.QwenRerankModelConfig{
		APIURL: cfg.RerankBaseURL,
		Model:  cfg.RerankModel,
	})

	// NewVectorStore 内部幂等确保四个 collection 就绪（存在跳过、缺失创建）
	vectorStore, err := store.NewVectorStore(milvusCli, embedder, dim, rerankModel)
	if err != nil {
		return fmt.Errorf("new vector store: %w", err)
	}

	// ---- Elasticsearch / ClickHouse 真实执行客户端 ----
	esCli, err := db.NewES(db.ElasticSearchConfig{
		Hosts:    cfg.ESAddrs,
		User:     cfg.ESUsername,
		Password: cfg.ESPassword,
	})
	if err != nil {
		return fmt.Errorf("connect elasticsearch %v: %w", cfg.ESAddrs, err)
	}

	ch, err := db.NewClickHouse(db.ClickHouseConfig{
		Hosts:    []string{cfg.ClickHouseAddr},
		Username: cfg.ClickHouseUsername,
		Password: cfg.ClickHousePassword,
		Database: cfg.ClickHouseDatabase,
	})
	if err != nil {
		return fmt.Errorf("connect clickhouse %s: %w", cfg.ClickHouseAddr, err)
	}
	defer func() { _ = ch.Close() }()

	// ---- MCP 工具：ClickHouse react agent 的手脚，按白名单收窄权限 ----
	allMCPTools, err := tool.GetMCPToolsBySSE(ctx, cfg.MCPClickhouseSSEBaseURL)
	if err != nil {
		return fmt.Errorf("load mcp clickhouse tools: %w", err)
	}
	mcpTools, err := tool.FilterToolsByNames(ctx, allMCPTools, strings.Split(cfg.MCPClickhouseToolNames, ","))
	if err != nil {
		return fmt.Errorf("filter mcp tools by whitelist: %w", err)
	}
	if len(mcpTools) == 0 {
		return fmt.Errorf("no mcp tool matched whitelist %q, check mcp server and MCP_CLICKHOUSE_TOOL_NAMES", cfg.MCPClickhouseToolNames)
	}
	slog.Info("mcp clickhouse tools loaded", slog.Int("count", len(mcpTools)))

	// ---- 组装 service 并注册路由 ----
	dbSearchService, err := service.NewDBSearchService(ctx, chatModel, mcpTools, vectorStore, ch, esCli, cfg.MaxRetryPerSource)
	if err != nil {
		return fmt.Errorf("new db search service: %w", err)
	}

	e := v1.NewWebRouter(dbSearchService)

	// ---- 优雅退出 ----
	errCh := make(chan error, 1)
	go func() {
		if err := e.Start(cfg.HTTPListen); err != nil {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
		slog.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := e.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("http shutdown: %w", err)
	}

	return nil
}

// Config 服务运行配置，所有字段均可由 flag 或同名大写环境变量注入
type Config struct {
	HTTPListen        string
	LogLevel          string
	APIKey            string // OpenRouter API Key
	ModelName         string
	OpenRouterBaseURL string

	MilvusAddr       string
	MilvusDatabase   string
	EmbeddingBaseURL string
	EmbeddingModel   string
	RerankBaseURL    string
	RerankModel      string

	ESAddrs            []string
	ESUsername         string
	ESPassword         string
	ClickHouseAddr     string
	ClickHouseUsername string
	ClickHousePassword string
	ClickHouseDatabase string

	MCPClickhouseSSEBaseURL string
	MCPClickhouseToolNames  string

	MaxRetryPerSource int
}

// loadConfig 注册 flag；环境变量同名大写，优先级低于 flag 显式指定
func loadConfig() *Config {
	var cfg Config
	var esAddr string

	flag.StringVar(&cfg.HTTPListen, "http-listen", envOr("HTTP_LISTEN", "127.0.0.1:5002"), "HTTP 监听地址")
	flag.StringVar(&cfg.LogLevel, "log-level", envOr("LOG_LEVEL", "info"), "日志级别 debug/info/warn/error")
	flag.StringVar(&cfg.APIKey, "api-key", envOr("OPEN_AI_API_KEY", ""), "OpenRouter API Key")
	flag.StringVar(&cfg.ModelName, "model-name", envOr("MODEL_NAME", string(chatmodel.ModelNameGemini2)), "OpenRouter 模型名")
	flag.StringVar(&cfg.OpenRouterBaseURL, "open-router-base-url", envOr("OPEN_ROUTER_BASE_URL", chatmodel.OpenRouterBaseURL), "OpenRouter 网关地址")

	flag.StringVar(&cfg.MilvusAddr, "milvus-addr", envOr("MILVUS_ADDR", "127.0.0.1:19530"), "Milvus 地址")
	flag.StringVar(&cfg.MilvusDatabase, "milvus-database", envOr("MILVUS_DATABASE", "default"), "Milvus database")
	flag.StringVar(&cfg.EmbeddingBaseURL, "embedding-base-url", envOr("EMBEDDING_BASE_URL", ""), "Embedding 服务地址（BGE 风格 /embedding/{model}）")
	flag.StringVar(&cfg.EmbeddingModel, "embedding-model", envOr("EMBEDDING_MODEL", "BAAI/bge-m3"), "Embedding 模型名")
	flag.StringVar(&cfg.RerankBaseURL, "rerank-base-url", envOr("RERANK_BASE_URL", "https://api.siliconflow.cn/v1"), "Rerank 服务地址")
	flag.StringVar(&cfg.RerankModel, "rerank-model", envOr("RERANK_MODEL", "BAAI/bge-reranker-v2-m3"), "Rerank 模型名")

	flag.StringVar(&esAddr, "es-addr", envOr("ES_ADDR", "http://127.0.0.1:9200"), "Elasticsearch 地址")
	flag.StringVar(&cfg.ESUsername, "es-username", envOr("ES_USERNAME", ""), "Elasticsearch 用户名")
	flag.StringVar(&cfg.ESPassword, "es-password", envOr("ES_PASSWORD", ""), "Elasticsearch 密码")
	flag.StringVar(&cfg.ClickHouseAddr, "clickhouse-addr", envOr("CLICKHOUSE_ADDR", "127.0.0.1:9000"), "ClickHouse 地址（native 协议）")
	flag.StringVar(&cfg.ClickHouseUsername, "clickhouse-username", envOr("CLICKHOUSE_USERNAME", "default"), "ClickHouse 用户名")
	flag.StringVar(&cfg.ClickHousePassword, "clickhouse-password", envOr("CLICKHOUSE_PASSWORD", ""), "ClickHouse 密码")
	flag.StringVar(&cfg.ClickHouseDatabase, "clickhouse-database", envOr("CLICKHOUSE_DATABASE", "default"), "ClickHouse 默认库")

	flag.StringVar(&cfg.MCPClickhouseSSEBaseURL, "mcp-clickhouse-sse-base-url", envOr("MCP_CLICKHOUSE_SSE_BASE_URL", "http://127.0.0.1:4200/sse"), "ClickHouse MCP server SSE 端点")
	flag.StringVar(&cfg.MCPClickhouseToolNames, "mcp-clickhouse-tool-names", envOr("MCP_CLICKHOUSE_TOOL_NAMES", "list_databases,list_tables,run_select_query"), "MCP 工具白名单，逗号分隔")

	flag.IntVar(&cfg.MaxRetryPerSource, "max-retry-per-source", envOrInt("MAX_RETRY_PER_SOURCE", graph.DefaultMaxRetry), "多源链路每个数据源的自愈重试上限")

	flag.Parse()

	cfg.ESAddrs = []string{esAddr}
	return &cfg
}

// envOr 读取环境变量，缺省回落到默认值
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// envOrInt 读取整型环境变量
func envOrInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			return n
		}
	}
	return def
}

// setupLogger 按级别初始化 slog 文本日志
func setupLogger(level string) error {
	var lv slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lv = slog.LevelDebug
	case "info":
		lv = slog.LevelInfo
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		return fmt.Errorf("unknown log level: %s", level)
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lv})))
	return nil
}
