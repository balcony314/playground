package store

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	// limitRerank rerank 后保留的条数
	limitRerank = 1
)

// RerankModel 精排模型接口：按与 query 的相关度重排候选文档
type RerankModel interface {
	Rerank(ctx context.Context, query string, docs []string) ([]RerankResult, error)
}

// RerankResult 单条精排结果（Index 指向候选列表原下标，用于回查原始内容）
type RerankResult struct {
	Index int
	Score float64
	Text  string
}

type rerankRequest struct {
	Documents []string `json:"documents,omitempty"`
	Model     *string  `json:"model,omitempty"`
	Query     *string  `json:"query,omitempty"`
	TopN      *int64   `json:"top_n,omitempty"`
}

type rerankResponse struct {
	Results []rerankResultItem `json:"results,omitempty"`
}

type rerankResultItem struct {
	Document struct {
		Text string `json:"text,omitempty"`
	} `json:"document,omitempty"`
	Index          int     `json:"index,omitempty"`
	RelevanceScore float64 `json:"relevance_score,omitempty"`
}

// QwenRerankModelConfig 自研 Qwen rerank 服务配置
type QwenRerankModelConfig struct {
	APIURL string
	Model  string
}

// QwenRerankModel Qwen reranker 的 HTTP 客户端。
// 服务接口约定：POST {apiURL}/reranker/{model}，兼容 SiliconFlow 风格响应结构。
type QwenRerankModel struct {
	apiURL string
	model  string
	client *http.Client
}

// NewQwenRerankModel 创建精排客户端
func NewQwenRerankModel(config QwenRerankModelConfig) *QwenRerankModel {
	return &QwenRerankModel{
		apiURL: config.APIURL,
		model:  config.Model,
		client: &http.Client{},
	}
}

// Rerank 调用精排服务，返回按相关度降序的候选子集
func (r *QwenRerankModel) Rerank(ctx context.Context, query string, docs []string) ([]RerankResult, error) {
	topN := int64(limitRerank)

	reqBody := rerankRequest{
		Documents: docs,
		Model:     &r.model,
		Query:     &query,
		TopN:      &topN,
	}

	contentsBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal rerank request: %w", err)
	}

	requestURL := r.apiURL + "/reranker/" + r.model
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, strings.NewReader(string(contentsBytes)))
	if err != nil {
		return nil, fmt.Errorf("create rerank request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send rerank request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read rerank response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rerank api failed with status %s: %s", resp.Status, string(body))
	}

	var rResp rerankResponse
	if err := json.Unmarshal(body, &rResp); err != nil {
		return nil, fmt.Errorf("unmarshal rerank response: %w", err)
	}

	results := make([]RerankResult, 0, len(rResp.Results))
	for _, item := range rResp.Results {
		results = append(results, RerankResult{
			Text:  item.Document.Text,
			Index: item.Index,
			Score: item.RelevanceScore,
		})
	}

	return results, nil
}
