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

// context key 必须用自定义未导出类型而非裸字符串：
// context.Value 按 (类型, 值) 二元组匹配，私有类型保证与其他包
// 写入同一 ctx 的 key 永不冲突（Go 惯用法）。
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

// EmbedStrings 把文本批量向量化，实现 eino embedding.Embedder 接口。
//
// 回调时序（LoggerCallback 依赖这三个钩子打印调用审计日志）：
//
//	OnStart(输入 texts) → HTTP 请求 → OnEnd(输出 embeddings)
//	                                   └─ 任一步失败 → OnError(err)
func (e *CustomEmbedder) EmbedStrings(ctx context.Context, texts []string, opts ...embedding.Option) ([][]float64, error) {
	// 命名返回值 err 供 defer 捕获：无论从哪个 return 退出，异常路径都触发 OnError
	var err error
	// 出错回调在 defer 中触发，保证异常路径也有审计记录
	defer func() {
		if err != nil {
			_ = callbacks.OnError(ctx, err)
		}
	}()

	// opts 选项当前实现未消费，保留参数以对齐 eino Embedder 接口签名
	_ = opts

	// OnStart 返回追加了回调信息的新 ctx，后续 OnEnd/OnError 必须用返回值而非入参 ctx
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
// 用法：检索侧调用方先用它包一层 ctx，再传入 EmbedStrings；
// 灌库侧（ragload）不标记，走 document 路径。
func (e *CustomEmbedder) WithIsQuestionContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, isQuestionKey, struct{}{})
}

func (e *CustomEmbedder) getIsQuestionFromContext(ctx context.Context) bool {
	return ctx.Value(isQuestionKey) != nil
}

// embeddingReq 自研服务请求体；isQuestion 决定服务端套用 query/document 前缀
type embeddingReq struct {
	IsQuestion bool     `json:"isQuestion"`
	Contents   []string `json:"contents"`
}

// embeddingResp 自研服务响应体：与 contents 等长按序对应的向量数组
type embeddingResp struct {
	Embeddings [][]float64 `json:"embeddings"`
}

// getEmbeddings 单次 HTTP 调用：POST {baseURL}/embedding/{model}。
// isQuestion 标记由调用方经 WithIsQuestionContext 注入 ctx，在此提取进请求体。
func (e *CustomEmbedder) getEmbeddings(ctx context.Context, contents []string) ([][]float64, error) {
	url := fmt.Sprintf("%s/embedding/%s", e.baseURL, e.model)

	contentsBytes, err := json.Marshal(embeddingReq{
		IsQuestion: e.getIsQuestionFromContext(ctx),
		Contents:   contents,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal embedding request: %w", err)
	}

	// 携带 ctx 创建请求：上层超时/取消会随 ctx 传导中断本次 HTTP 调用
	//（http.DefaultClient 本身无超时，靠 ctx 兜底）
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
		// 报错带响应体，便于排查服务端返回的具体错误信息
		return nil, fmt.Errorf("embedding api failed with status: %s, response: %s", resp.Status, string(body))
	}

	var respVal embeddingResp
	if err := json.Unmarshal(body, &respVal); err != nil {
		return nil, fmt.Errorf("unmarshal embedding response: %w", err)
	}

	return respVal.Embeddings, nil
}
