// Package main 实现 RAG 知识库灌库工具：读取语料目录下的表结构与
// "问题→查询语句"语料，向量化后写入 Milvus，供多源查询链路召回。
// 主键为内容 hash，重复执行天然幂等（upsert 语义）。
package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"datasearch/internal/client/db"
	"datasearch/internal/embedding"
	"datasearch/internal/store"
)

//go:embed rag/schema/ddl rag/schema/mapping rag/questionsql.json rag/questiondsl.json
var ragFS embed.FS

func main() {
	if err := run(); err != nil {
		slog.Error("ragload exited with error", slog.Any("err", err))
		os.Exit(1)
	}
}

func run() error {
	var (
		milvusAddr       = flag.String("milvus-addr", envOr("MILVUS_ADDR", "127.0.0.1:19530"), "Milvus 地址")
		milvusDatabase   = flag.String("milvus-database", envOr("MILVUS_DATABASE", "default"), "Milvus database")
		embeddingBaseURL = flag.String("embedding-base-url", envOr("EMBEDDING_BASE_URL", ""), "Embedding 服务地址")
		embeddingModel   = flag.String("embedding-model", envOr("EMBEDDING_MODEL", "BAAI/bge-m3"), "Embedding 模型名")
	)
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	ctx := context.Background()

	if *embeddingBaseURL == "" {
		return fmt.Errorf("embedding base url is required: set -embedding-base-url or EMBEDDING_BASE_URL env")
	}

	milvusCli, err := db.NewMilvus(db.MilvusConfig{Address: *milvusAddr}, *milvusDatabase)
	if err != nil {
		return fmt.Errorf("connect milvus: %w", err)
	}
	defer func() { _ = milvusCli.Close(ctx) }()

	embedder, err := embedding.NewCustomEmbedder(&embedding.CustomEmbedderConfig{
		BaseURL:      *embeddingBaseURL,
		DefaultModel: *embeddingModel,
	})
	if err != nil {
		return fmt.Errorf("new embedder: %w", err)
	}

	dim, err := embedding.GetModelDim(embedder)
	if err != nil {
		return fmt.Errorf("detect embedding dim: %w", err)
	}

	vectorStore, err := store.NewVectorStore(milvusCli, embedder, dim, nil)
	if err != nil {
		return fmt.Errorf("new vector store: %w", err)
	}

	// ---- 表结构双路召回之一：ClickHouse DDL ----
	ddlCount, err := loadDir(ctx, vectorStore, "rag/schema/ddl", func(vs *store.VectorStore, content string) error {
		return vs.AddDDL(ctx, content)
	})
	if err != nil {
		return err
	}

	// ---- 表结构双路召回之二：ES Mapping ----
	mappingCount, err := loadDir(ctx, vectorStore, "rag/schema/mapping", func(vs *store.VectorStore, content string) error {
		return vs.AddMapping(ctx, content)
	})
	if err != nil {
		return err
	}

	// ---- "问题 → SQL" few-shot 语料 ----
	sqlCount, err := loadQuestionQuery(ctx, vectorStore, "rag/questionsql.json", vectorStore.AddQuestionSQL)
	if err != nil {
		return err
	}

	// ---- "问题 → DSL" few-shot 语料 ----
	dslCount, err := loadQuestionQuery(ctx, vectorStore, "rag/questiondsl.json", vectorStore.AddQuestionDSL)
	if err != nil {
		return err
	}

	slog.Info("rag load done",
		slog.Int("ddl", ddlCount),
		slog.Int("mapping", mappingCount),
		slog.Int("question_sql", sqlCount),
		slog.Int("question_dsl", dslCount),
	)

	return nil
}

// loadDir 遍历 embed 目录下的语料文件逐条灌库
func loadDir(ctx context.Context, vs *store.VectorStore, dir string, add func(*store.VectorStore, string) error) (int, error) {
	entries, err := fs.ReadDir(ragFS, dir)
	if err != nil {
		return 0, fmt.Errorf("read dir %s: %w", dir, err)
	}

	count := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		content, err := fs.ReadFile(ragFS, filepath.ToSlash(filepath.Join(dir, entry.Name())))
		if err != nil {
			return count, fmt.Errorf("read %s: %w", entry.Name(), err)
		}

		if err := add(vs, string(content)); err != nil {
			return count, fmt.Errorf("add %s: %w", entry.Name(), err)
		}
		count++
		slog.Info("loaded", slog.String("file", entry.Name()), slog.String("kind", dir))
	}

	return count, nil
}

// questionQueryItem "问题→查询语句"语料的文件结构
type questionQueryItem struct {
	Question string `json:"question"`
	Query    string `json:"sql,omitempty"`
	DSL      string `json:"dsl,omitempty"`
}

// loadQuestionQuery 解析 JSON 语料并逐条灌库。
// sqlFile 与 dslFile 共用同一结构：SQL 文件取 "sql" 字段，DSL 文件取 "dsl" 字段。
func loadQuestionQuery(ctx context.Context, vs *store.VectorStore, path string, add func(context.Context, string, string) error) (int, error) {
	raw, err := fs.ReadFile(ragFS, path)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", path, err)
	}

	var items []questionQueryItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return 0, fmt.Errorf("unmarshal %s: %w", path, err)
	}

	count := 0
	for _, item := range items {
		query := item.Query
		if query == "" {
			query = item.DSL
		}
		if item.Question == "" || query == "" {
			slog.Warn("skip invalid question query item", slog.String("file", path))
			continue
		}

		if err := add(ctx, item.Question, query); err != nil {
			return count, fmt.Errorf("add question query %q: %w", item.Question, err)
		}
		count++
	}
	slog.Info("loaded", slog.String("file", path), slog.Int("count", count))

	return count, nil
}

// envOr 读取环境变量，缺省回落到默认值
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
