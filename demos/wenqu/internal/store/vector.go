// Package store 提供 RAG 知识库的向量存储与检索。
// Milvus 中维护四类 collection，对应"表结构"与"问题→查询语句"双路召回：
//
//	DDL        —— ClickHouse 建表语句（Text-to-SQL 的 Schema 参考）
//	Mapping    —— ES 索引 Mapping（Text-to-DSL 的 Schema 参考）
//	QuestionSQL —— "问题 → SQL"历史语料（few-shot 参考）
//	QuestionDSL —— "问题 → DSL"历史语料（few-shot 参考）
package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/milvus-io/milvus/client/v2/entity"
	"github.com/milvus-io/milvus/client/v2/index"
	"github.com/milvus-io/milvus/client/v2/milvusclient"

	"datasearch/internal/pkg/convert"
	"datasearch/internal/pkg/hash"
)

const (
	collectionNameDDL         = "demo_ddl"
	collectionNameMapping     = "demo_mapping"
	collectionNameQuestionSQL = "demo_question_sql"
	collectionNameQuestionDSL = "demo_question_dsl"

	// limitDDL 向量检索的 topK
	limitDDL = 10

	idField       = "id"
	vectorField   = "vector"
	schemaField   = "schema"
	questionField = "question"
	queryField    = "query"
	idLen         = 64
	schemaLen     = 65535
	questionLen   = 65535
	queryLen      = 65535

	// questionPrefix bge 系列 embedding 模型的 query 侧指令前缀，
	// 与文档侧区分以提升非对称检索召回质量
	questionPrefix = "为这个句子生成表示以用于检索相关文章："
)

// VectorStore RAG 向量知识库
type VectorStore struct {
	db             *milvusclient.Client
	customEmbedder embedding.Embedder
	rerankModel    RerankModel
	dim            int64
}

// NewVectorStore 创建向量库并确保四个 collection 就绪（存在则跳过，缺失则创建）
func NewVectorStore(db *milvusclient.Client, customEmbedder embedding.Embedder, dim int64, rerankModel RerankModel) (*VectorStore, error) {
	ctx := context.Background()

	s := &VectorStore{
		db:             db,
		customEmbedder: customEmbedder,
		rerankModel:    rerankModel,
		dim:            dim,
	}

	for _, collectionName := range []string{collectionNameDDL, collectionNameMapping} {
		if err := s.ensureDDLOrMappingCollection(ctx, collectionName); err != nil {
			return nil, fmt.Errorf("ensure ddl or mapping collection %s: %w", collectionName, err)
		}
	}

	for _, collectionName := range []string{collectionNameQuestionSQL, collectionNameQuestionDSL} {
		if err := s.ensureQuestionCollection(ctx, collectionName); err != nil {
			return nil, fmt.Errorf("ensure question collection %s: %w", collectionName, err)
		}
	}

	if err := s.loadCollections(ctx); err != nil {
		return nil, err
	}

	return s, nil
}

// loadCollections 显式加载全部 collection（幂等）。
// Milvus 重启或内存压力释放后 collection 会回到未加载态，未加载的 collection 无法搜索；
// server 启动与 ragload 灌库均经 NewVectorStore 进入，此处加载以自愈该状态。
func (s *VectorStore) loadCollections(ctx context.Context) error {
	for _, collectionName := range []string{collectionNameDDL, collectionNameMapping, collectionNameQuestionSQL, collectionNameQuestionDSL} {
		loadTask, err := s.db.LoadCollection(ctx, milvusclient.NewLoadCollectionOption(collectionName))
		if err != nil {
			return fmt.Errorf("load collection %s: %w", collectionName, err)
		}
		// 等待加载完成，避免启动即搜的首次请求失败
		if err := loadTask.Await(ctx); err != nil {
			return fmt.Errorf("await load collection %s: %w", collectionName, err)
		}
		slog.InfoContext(ctx, "collection loaded", slog.String("collection", collectionName))
	}

	return nil
}

// ensureDDLOrMappingCollection 幂等创建 schema 类 collection（id + vector + schema 三列）
func (s *VectorStore) ensureDDLOrMappingCollection(ctx context.Context, collectionName string) error {
	has, err := s.db.HasCollection(ctx, milvusclient.NewHasCollectionOption(collectionName))
	if err != nil {
		return fmt.Errorf("has collection: %w", err)
	}
	if has {
		return nil
	}

	schema := entity.NewSchema().
		WithName(collectionName).
		WithDescription("RAG schema knowledge base (DDL or ES Mapping)").
		WithField(entity.NewField().WithName(idField).WithDataType(entity.FieldTypeVarChar).WithMaxLength(idLen).WithIsPrimaryKey(true).WithIsAutoID(false)). // 主键 = 内容 hash，重复灌库幂等
		WithField(entity.NewField().WithName(vectorField).WithDataType(entity.FieldTypeFloatVector).WithDim(s.dim)).
		WithField(entity.NewField().WithName(schemaField).WithDataType(entity.FieldTypeVarChar).WithMaxLength(schemaLen))

	indexOption := milvusclient.NewCreateIndexOption(collectionName, vectorField, index.NewAutoIndex(entity.IP)).WithIndexName(vectorField)
	if err := s.db.CreateCollection(ctx,
		milvusclient.NewCreateCollectionOption(collectionName, schema).
			WithIndexOptions(indexOption)); err != nil {
		return fmt.Errorf("create collection: %w", err)
	}
	slog.InfoContext(ctx, "created ddl/mapping collection", slog.String("collection", collectionName))

	return nil
}

// ensureQuestionCollection 幂等创建"问题→查询语句"类 collection（id + vector + question + query 四列）
func (s *VectorStore) ensureQuestionCollection(ctx context.Context, collectionName string) error {
	has, err := s.db.HasCollection(ctx, milvusclient.NewHasCollectionOption(collectionName))
	if err != nil {
		return fmt.Errorf("has collection: %w", err)
	}
	if has {
		return nil
	}

	schema := entity.NewSchema().
		WithName(collectionName).
		WithDescription("RAG question-to-query knowledge base").
		WithField(entity.NewField().WithName(idField).WithDataType(entity.FieldTypeVarChar).WithMaxLength(idLen).WithIsPrimaryKey(true).WithIsAutoID(false)).
		WithField(entity.NewField().WithName(vectorField).WithDataType(entity.FieldTypeFloatVector).WithDim(s.dim)).
		WithField(entity.NewField().WithName(questionField).WithDataType(entity.FieldTypeVarChar).WithMaxLength(questionLen)).
		WithField(entity.NewField().WithName(queryField).WithDataType(entity.FieldTypeVarChar).WithMaxLength(queryLen))

	indexOption := milvusclient.NewCreateIndexOption(collectionName, vectorField, index.NewAutoIndex(entity.IP)).WithIndexName(vectorField)
	if err := s.db.CreateCollection(ctx,
		milvusclient.NewCreateCollectionOption(collectionName, schema).
			WithIndexOptions(indexOption)); err != nil {
		return fmt.Errorf("create collection: %w", err)
	}
	slog.InfoContext(ctx, "created question collection", slog.String("collection", collectionName))

	return nil
}

// ---- 灌库接口（cmd/ragload 调用） ----

// AddDDL 写入一条 ClickHouse 建表语句
func (s *VectorStore) AddDDL(ctx context.Context, ddl string) error {
	return s.addSchema(ctx, collectionNameDDL, ddl)
}

// AddMapping 写入一条 ES 索引 Mapping
func (s *VectorStore) AddMapping(ctx context.Context, mapping string) error {
	return s.addSchema(ctx, collectionNameMapping, mapping)
}

// AddQuestionSQL 写入一条"问题 → SQL"语料
func (s *VectorStore) AddQuestionSQL(ctx context.Context, question, sql string) error {
	return s.addQuestionQuery(ctx, collectionNameQuestionSQL, question, sql)
}

// AddQuestionDSL 写入一条"问题 → DSL"语料
func (s *VectorStore) AddQuestionDSL(ctx context.Context, question, dsl string) error {
	return s.addQuestionQuery(ctx, collectionNameQuestionDSL, question, dsl)
}

// ---- 召回接口（状态图 input_process 节点调用） ----

// GetRelatedDDLWithRerank 召回相关 DDL 并精排，返回 top1（多条件拼接）
func (s *VectorStore) GetRelatedDDLWithRerank(ctx context.Context, question string) ([]string, error) {
	result, err := s.getSchemaWithRerank(ctx, collectionNameDDL, question)
	if err != nil {
		return nil, fmt.Errorf("get related ddl with rerank: %w", err)
	}

	return result, nil
}

// GetRelatedMappingWithRerank 召回相关 ES Mapping 并精排
func (s *VectorStore) GetRelatedMappingWithRerank(ctx context.Context, question string) ([]string, error) {
	result, err := s.getSchemaWithRerank(ctx, collectionNameMapping, question)
	if err != nil {
		return nil, fmt.Errorf("get related mapping with rerank: %w", err)
	}

	return result, nil
}

// GetRelatedQuestionSQL 召回相似问题的历史 SQL（取 top1）
func (s *VectorStore) GetRelatedQuestionSQL(ctx context.Context, question string) ([]string, error) {
	result, err := s.getRelatedQuestionQuery(ctx, collectionNameQuestionSQL, question)
	if err != nil {
		return nil, fmt.Errorf("get related question sql: %w", err)
	}

	return result, nil
}

// GetRelatedQuestionDSL 召回相似问题的历史 DSL（取 top1）
func (s *VectorStore) GetRelatedQuestionDSL(ctx context.Context, question string) ([]string, error) {
	result, err := s.getRelatedQuestionQuery(ctx, collectionNameQuestionDSL, question)
	if err != nil {
		return nil, fmt.Errorf("get related question dsl: %w", err)
	}

	return result, nil
}

// ---- 内部实现 ----

func (s *VectorStore) addSchema(ctx context.Context, collectionName, schema string) error {
	vectors, err := s.embed(ctx, schema)
	if err != nil {
		return fmt.Errorf("add schema, embedding: %w", err)
	}

	_, err = s.db.Insert(ctx, milvusclient.NewColumnBasedInsertOption(collectionName).
		WithVarcharColumn(idField, []string{hash.GetStringSha256(schema)}).
		WithVarcharColumn(schemaField, []string{schema}).
		WithFloatVectorColumn(vectorField, len(vectors), [][]float32{vectors}))
	if err != nil {
		return fmt.Errorf("add schema, insert: %w", err)
	}

	return nil
}

func (s *VectorStore) addQuestionQuery(ctx context.Context, collectionName, question, query string) error {
	vectors, err := s.embed(ctx, question)
	if err != nil {
		return fmt.Errorf("add question query, embedding: %w", err)
	}

	_, err = s.db.Insert(ctx, milvusclient.NewColumnBasedInsertOption(collectionName).
		WithVarcharColumn(idField, []string{hash.GetStringSha256(question)}).
		WithVarcharColumn(questionField, []string{question}).
		WithVarcharColumn(queryField, []string{query}).
		WithFloatVectorColumn(vectorField, len(vectors), [][]float32{vectors}))
	if err != nil {
		return fmt.Errorf("add question query, insert: %w", err)
	}

	return nil
}

// embed 文本向量化并转为 []float32（Milvus 浮点向量列要求）
func (s *VectorStore) embed(ctx context.Context, text string) ([]float32, error) {
	vectors, err := s.customEmbedder.EmbedStrings(ctx, []string{text})
	if err != nil {
		return nil, fmt.Errorf("embed: %w", err)
	}
	if len(vectors) == 0 {
		return nil, errors.New("embedding result is empty")
	}

	float32Vectors := make([]float32, 0, len(vectors[0]))
	for _, v := range vectors[0] {
		f32, err := convert.Float64To32(v)
		if err != nil {
			return nil, fmt.Errorf("convert to float32: %w", err)
		}
		float32Vectors = append(float32Vectors, f32)
	}

	return float32Vectors, nil
}

// getSchemaWithRerank 向量粗排取 topK 候选 → rerank 精排取 top1
func (s *VectorStore) getSchemaWithRerank(ctx context.Context, collectionName, question string) ([]string, error) {
	resultSets, err := s.search(ctx, collectionName, question, schemaField)
	if err != nil {
		return nil, err
	}

	candidates := make([]string, 0)
	schemaCol := resultSets[0].GetColumn(schemaField)
	for i := 0; i < schemaCol.Len(); i++ {
		text, _ := schemaCol.GetAsString(i) // 空值容忍
		candidates = append(candidates, text)
	}

	rerankResults, err := s.rerankModel.Rerank(ctx, question, candidates)
	if err != nil {
		return nil, fmt.Errorf("rerank: %w", err)
	}

	var sb strings.Builder
	for i := 0; i < len(rerankResults) && i < limitRerank; i++ {
		sb.WriteString(rerankResults[i].Text)
		sb.WriteString("\n")
		slog.InfoContext(ctx, "schema rerank hit", slog.String("text", truncate(rerankResults[i].Text)), slog.Any("score", rerankResults[i].Score))
	}

	return []string{sb.String()}, nil
}

// getRelatedQuestionQuery 按"问题向量"召回相似问题，返回对应查询语句（top1）
func (s *VectorStore) getRelatedQuestionQuery(ctx context.Context, collectionName, question string) ([]string, error) {
	resultSets, err := s.search(ctx, collectionName, question, queryField)
	if err != nil {
		return nil, err
	}

	queryCol := resultSets[0].GetColumn(queryField)
	if queryCol.Len() == 0 {
		return nil, errors.New("no related question query found")
	}

	text, _ := queryCol.GetAsString(0) // 空值容忍

	return []string{text}, nil
}

// search 向量近邻检索。查询侧拼接 bge 指令前缀以区分 query/document 编码
func (s *VectorStore) search(ctx context.Context, collectionName, question string, outputFields ...string) ([]milvusclient.ResultSet, error) {
	questionVectors, err := s.embed(ctx, questionPrefix+question)
	if err != nil {
		return nil, fmt.Errorf("search, embedding: %w", err)
	}

	resultSets, err := s.db.Search(ctx, milvusclient.NewSearchOption(collectionName, limitDDL,
		[]entity.Vector{entity.FloatVector(questionVectors)}).
		WithANNSField(vectorField).
		WithSearchParam("nprobe", "128").
		WithOutputFields(outputFields...))
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	if len(resultSets) == 0 || resultSets[0].ResultCount == 0 {
		return nil, errors.New("search result is empty")
	}

	return resultSets, nil
}

// truncate 日志截断，避免超长 DDL 刷屏
func truncate(s string) string {
	const max = 120
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}

	return string(runes[:max]) + "..."
}
