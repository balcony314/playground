// Package embedding 实现自研 embedding 服务的 HTTP 客户端。
// 服务接口约定：POST {baseURL}/embedding/{model}，
// 请求体 {"isQuestion":bool,"contents":[]string}，响应 {"embeddings":[][]float64}。
// 实现 eino 的 embedding.Embedder 接口，可直接接入 eino callbacks 链路。
package embedding

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/embedding"
)

type key int

var isQuestionKey key

// CustomEmbedderConfig 自研 embedding 服务配置
type CustomEmbedderConfig struct {
	BaseURL      string
	DefaultModel string
}

// CustomEmbedder 自研 embedding 客户端
type CustomEmbedder struct {
	baseURL string
	model   string
}

// NewCustomEmbedder 创建客户端（不发请求，惰性连接）
func NewCustomEmbedder(config *CustomEmbedderConfig) (*CustomEmbedder, error) {
	return &CustomEmbedder{
		baseURL: config.BaseURL,
		model:   config.DefaultModel,
	}, nil
}

// EmbedStrings 把文本批量向量化，实现 eino embedding.Embedder 接口
func (e *CustomEmbedder) EmbedStrings(ctx context.Context, texts []string, opts ...embedding.Option) ([][]float64, error) {
	var err error
	// 出错回调在 defer 中触发，保证异常路径也有审计记录
	defer func() {
		if err != nil {
			_ = callbacks.OnError(ctx, err)
		}
	}()

	// opts 选项当前实现未消费，保留参数以对齐 eino Embedder 接口签名
	_ = opts

	ctx = callbacks.OnStart(ctx, &embedding.CallbackInput{
		Texts: texts,
		Config: &embedding.Config{
			Model: e.model,
		},
	})

	embeddings, err := e.getEmbeddings(ctx, texts)
	if err != nil {
		return nil, err
	}

	ctx = callbacks.OnEnd(ctx, &embedding.CallbackOutput{
		Embeddings: embeddings,
		Config: &embedding.Config{
			Model: e.model,
		},
	})

	return embeddings, nil
}

// WithIsQuestionContext 标记当前向量化的是"查询问题"（而非"待检索文档"）。
// 部分模型（如 bge 系列）对 query/document 使用不同前缀以提升召回质量。
func (e *CustomEmbedder) WithIsQuestionContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, isQuestionKey, struct{}{})
}

func (e *CustomEmbedder) getIsQuestionFromContext(ctx context.Context) bool {
	return ctx.Value(isQuestionKey) != nil
}

type embeddingReq struct {
	IsQuestion bool     `json:"isQuestion"`
	Contents   []string `json:"contents"`
}

type embeddingResp struct {
	Embeddings [][]float64 `json:"embeddings"`
}

func (e *CustomEmbedder) getEmbeddings(ctx context.Context, contents []string) ([][]float64, error) {
	url := fmt.Sprintf("%s/embedding/%s", e.baseURL, e.model)

	contentsBytes, err := json.Marshal(embeddingReq{
		IsQuestion: e.getIsQuestionFromContext(ctx),
		Contents:   contents,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal embedding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(contentsBytes)))
	if err != nil {
		return nil, fmt.Errorf("create embedding request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send embedding request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read embedding response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedding api failed with status: %s, response: %s", resp.Status, string(body))
	}

	var respVal embeddingResp
	if err := json.Unmarshal(body, &respVal); err != nil {
		return nil, fmt.Errorf("unmarshal embedding response: %w", err)
	}

	return respVal.Embeddings, nil
}
