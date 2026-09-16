// embedadapter：自研 Embedding/Rerank 协议到 Ollama 的适配层（演示用，gin 实现）。
//
// 项目约定的自研协议（公共云不直接提供）：
//
//	POST /embedding/{model}  {"isQuestion":bool,"contents":[]string} → {"embeddings":[][]float64}
//	POST /reranker/{model}   {"query","documents","top_n"} → {"results":[{"index","relevance_score","document"}]}
//
// 适配策略：
//   - embedding → 转发 Ollama POST /api/embed（模型 bge-m3 由 compose 的 ollama-pull 预拉）；
//   - rerank    → 配置了 RERANK_URL 则转发 llama.cpp server POST /v1/rerank
//     （模型 bge-reranker-v2-m3 GGUF 由 compose 的 reranker-pull 预下载）；
//     未配置时恒等降级（按输入顺序返回 top_n）；
//   - GET /healthz → 同时探测 Ollama 可达性与模型就绪，Taskfile 的 deps:wait 依赖它判定。
//
// 属主 module（与 server/ragload 并列的进程入口）；compose 以 golang 公共镜像
// 挂载 go.mod/go.sum/本目录 `go run` 拉起（见 deploy/compose.yml）。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

func main() {
	listen := envOr("ADAPTER_LISTEN", ":9999")
	ollama := envOr("OLLAMA_HOST", "http://127.0.0.1:11434")
	embedModel := envOr("EMBED_MODEL", "bge-m3")
	// 真 rerank 后端（llama.cpp server 的 /v1/rerank 完整地址）；留空则恒等降级
	rerankURL := envOr("RERANK_URL", "")

	a := &adapter{
		ollama:     ollama,
		embedModel: embedModel,
		rerankURL:  rerankURL,
		// CPU 批量前向较慢，超时给足余量（上层还有 ctx 超时兜底）
		client: &http.Client{Timeout: 120 * time.Second},
	}

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	// *model 多段通配：模型名本身含斜杠（如 BAAI/bge-m3），gin 单段 :model 匹配不了；
	// 取值形如 "/BAAI/bge-m3"，统一经 ollamaModel 转成 Ollama 本地模型名
	r.POST("/embedding/*model", a.handleEmbedding)
	r.POST("/reranker/*model", a.handleRerank)
	r.GET("/healthz", a.handleHealthz)

	slog.Info("embedadapter listening",
		slog.String("addr", listen), slog.String("ollama", ollama), slog.String("model", embedModel),
		slog.String("rerank", rerankRoute(rerankURL)))
	if err := r.Run(listen); err != nil {
		slog.Error("embedadapter exited", slog.Any("error", err))
		os.Exit(1)
	}
}

// envOr 读环境变量，缺省回退默认值
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

type adapter struct {
	ollama     string // Ollama 根地址，compose 网络内为 http://ollama:11434
	embedModel string // 就绪探测用的模型名（healthz 校验其已拉取）
	rerankURL  string // 真 rerank 后端完整地址（llama.cpp /v1/rerank）；空则恒等降级
	client     *http.Client
}

// rerankRoute 供启动日志展示当前精排模式
func rerankRoute(rerankURL string) string {
	if rerankURL == "" {
		return "identity(降级)"
	}
	return rerankURL
}

// ---- embedding：转发 Ollama ----

type embedReq struct {
	IsQuestion bool     `json:"isQuestion"`
	Contents   []string `json:"contents"`
}

// ollamaEmbedReq / ollamaEmbedResp 为 Ollama POST /api/embed 的报文
type ollamaEmbedReq struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type ollamaEmbedResp struct {
	Model      string      `json:"model"`
	Embeddings [][]float64 `json:"embeddings"`
}

type embedResp struct {
	Embeddings [][]float64 `json:"embeddings"`
}

// handleEmbedding 转发向量化请求到 Ollama。
// 注：bge-m3 属对称检索模型，query/document 不需要指令前缀，
// isQuestion 字段仅透传记录，不参与转换。
func (a *adapter) handleEmbedding(c *gin.Context) {
	model := ollamaModel(c)

	var req embedReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, fmt.Errorf("decode request: %w", err))
		return
	}
	if len(req.Contents) == 0 {
		fail(c, http.StatusBadRequest, fmt.Errorf("contents is empty"))
		return
	}

	var resp ollamaEmbedResp
	in := &ollamaEmbedReq{Model: model, Input: req.Contents}
	if err := a.doJSON(c.Request.Context(), http.MethodPost, "/api/embed", in, &resp); err != nil {
		fail(c, http.StatusBadGateway, fmt.Errorf("call ollama: %w", err))
		return
	}
	if len(resp.Embeddings) != len(req.Contents) {
		fail(c, http.StatusBadGateway, fmt.Errorf("ollama returned %d embeddings for %d contents", len(resp.Embeddings), len(req.Contents)))
		return
	}

	c.JSON(http.StatusOK, &embedResp{Embeddings: resp.Embeddings})
}

// ---- rerank：真模型精排（llama.cpp /v1/rerank），未配置时恒等降级 ----

type rerankReq struct {
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	TopN      *int     `json:"top_n"`
}

type rerankResp struct {
	Results []rerankItem `json:"results"`
}

type rerankItem struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
	Document       struct {
		Text string `json:"text"`
	} `json:"document"`
}

// llamaRerankReq / llamaRerankResp 为 llama.cpp server POST /v1/rerank 的
// OpenAI 风格报文（llama.cpp 以 --reranking 启动后提供该端点）。
// 响应按分数降序，document 字段视版本可能缺省，统一按 index 回填原文。
type llamaRerankReq struct {
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	TopN      int      `json:"top_n"`
}

type llamaRerankResp struct {
	Results []struct {
		Index          int     `json:"index"`
		RelevanceScore float64 `json:"relevance_score"`
	} `json:"results"`
}

// handleRerank 精排入口：配置了 RERANK_URL 走 bge-reranker-v2-m3 真打分
// （cross-encoder 逐对算 query×document 相关度，见 deployment.md §6.3）；
// 未配置或后端异常时恒等降级（按输入顺序返回 top_n），并打日志提示。
func (a *adapter) handleRerank(c *gin.Context) {
	var req rerankReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, fmt.Errorf("decode request: %w", err))
		return
	}
	if len(req.Documents) == 0 {
		fail(c, http.StatusBadRequest, fmt.Errorf("documents is empty"))
		return
	}

	topN := len(req.Documents)
	if req.TopN != nil && *req.TopN >= 0 && *req.TopN < topN {
		topN = *req.TopN
	}

	items, err := a.rerank(c.Request.Context(), req, topN)
	if err != nil {
		// 真模型失败不静默：日志留痕后降级恒等，保证演示链路可用
		slog.Warn("rerank backend failed, fallback to identity",
			slog.String("rerank_url", a.rerankURL), slog.String("error", err.Error()))
		items = identityRerank(req.Documents, topN)
	}

	c.JSON(http.StatusOK, &rerankResp{Results: items})
}

// rerank 调 llama.cpp /v1/rerank 真打分；RERANK_URL 未配置时直接走恒等。
func (a *adapter) rerank(ctx context.Context, req rerankReq, topN int) ([]rerankItem, error) {
	if a.rerankURL == "" {
		return identityRerank(req.Documents, topN), nil
	}

	var lresp llamaRerankResp
	in := &llamaRerankReq{Query: req.Query, Documents: req.Documents, TopN: topN}
	if err := a.doJSONAt(ctx, http.MethodPost, a.rerankURL, in, &lresp); err != nil {
		return nil, fmt.Errorf("call reranker: %w", err)
	}

	items := make([]rerankItem, 0, len(lresp.Results))
	for _, r := range lresp.Results {
		if r.Index < 0 || r.Index >= len(req.Documents) {
			return nil, fmt.Errorf("reranker returned out-of-range index %d for %d documents", r.Index, len(req.Documents))
		}
		var item rerankItem
		item.Index = r.Index
		// llama.cpp 返回 raw logit（可正可负），自研协议对齐 SiliconFlow 风格的
		// 0~1 相关度语义，sigmoid 归一化；单调变换不影响排序
		item.RelevanceScore = sigmoid(r.RelevanceScore)
		item.Document.Text = req.Documents[r.Index] // 按 index 回填原文，不依赖后端是否带 document 字段
		items = append(items, item)
	}
	return items, nil
}

// sigmoid 将 rerank raw logit 映射到 (0,1)：logit 0 → 0.5，越大越相关
func sigmoid(x float64) float64 {
	return 1 / (1 + math.Exp(-x))
}

// identityRerank 恒等精排：按输入顺序返回前 top_n 条，分数按位次递减占位
func identityRerank(documents []string, topN int) []rerankItem {
	items := make([]rerankItem, 0, topN)
	for i := range topN {
		var item rerankItem
		item.Index = i
		item.RelevanceScore = float64(len(documents)-i) / float64(len(documents))
		item.Document.Text = documents[i]
		items = append(items, item)
	}
	return items
}

// ---- 健康探测 ----

// handleHealthz 探测 Ollama 可达性与模型就绪：
//   - GET /api/version 确认 Ollama 存活；
//   - POST /api/show 确认 embedModel 已拉取（首次下载约 1.2GB 期间返回 404 → 此处 502，
//     deps:wait 据此等待，不会在模型未就绪时误判依赖齐备）。
func (a *adapter) handleHealthz(c *gin.Context) {
	var vresp struct {
		Version string `json:"version"`
	}
	if err := a.doJSON(c.Request.Context(), http.MethodGet, "/api/version", nil, &vresp); err != nil {
		fail(c, http.StatusBadGateway, fmt.Errorf("ollama unreachable: %w", err))
		return
	}
	// 只关心状态码（200=已拉取 / 404=不存在），响应体不解析
	if err := a.doJSON(c.Request.Context(), http.MethodPost, "/api/show",
		map[string]string{"model": a.embedModel}, nil); err != nil {
		fail(c, http.StatusBadGateway,
			fmt.Errorf("model %q not ready (still pulling?): %w", a.embedModel, err))
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "ollama": vresp.Version, "model": a.embedModel})
}

// ---- 公共工具 ----

// ollamaModel 从 gin 路由参数提取 Ollama 本地模型名，两步合并成一次显式转换：
//  1. gin 的 *model 多段通配取值带前导斜杠（"/BAAI/bge-m3"），先剥掉；
//  2. 模型名带组织前缀时取最后一段（"BAAI/bge-m3" → "bge-m3"，与 ollama pull 命名一致）。
func ollamaModel(c *gin.Context) string {
	path := strings.TrimPrefix(c.Param("model"), "/")
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}

// doJSON 向 Ollama 发 JSON 请求并解析 JSON 响应。
// URL 用 url.JoinPath 拼接：容忍 base 尾斜杠且不折叠 scheme 的 //，
// 比 path.Join（会吃掉 "http://" 的双斜杠）和裸字符串拼接安全。
func (a *adapter) doJSON(ctx context.Context, method, path string, in, out any) error {
	target, err := url.JoinPath(a.ollama, path)
	if err != nil {
		return fmt.Errorf("join url: %w", err)
	}
	return a.doJSONAt(ctx, method, target, in, out)
}

// doJSONAt 向完整 URL 发 JSON 请求并解析 JSON 响应（rerank 后端等非 Ollama 目标）
func (a *adapter) doJSONAt(ctx context.Context, method, target string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		body = strings.NewReader(string(b))
	}

	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %s: %s", resp.Status, string(respBody))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("unmarshal response: %w", err)
	}
	return nil
}

// fail 统一错误响应：JSON 返回 + 服务端日志
func fail(c *gin.Context, status int, err error) {
	c.JSON(status, gin.H{"error": err.Error()})
	slog.Error("request failed", slog.Int("status", status), slog.String("error", err.Error()))
}
