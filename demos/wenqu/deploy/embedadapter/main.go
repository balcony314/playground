// embedadapter：自研 Embedding/Rerank 协议到本机 Ollama 的适配层（演示用）。
//
// 项目约定的自研协议（公共云不直接提供）：
//
//	POST /embedding/{model}  {"isQuestion":bool,"contents":[]string} → {"embeddings":[][]float64}
//	POST /reranker/{model}   {"query","documents","top_n"} → {"results":[{"index","relevance_score","document"}]}
//
// 适配策略：
//   - embedding → 转发宿主机 Ollama POST /api/embed（前置：ollama pull bge-m3）；
//   - rerank    → 恒等精排（按输入顺序返回 top_n）：演示语料仅数条，向量粗排已覆盖全部候选；
//   - GET /healthz → 同时探测自身与 Ollama 可达性，Taskfile 的 deps:wait 依赖它判定就绪。
//
// 纯标准库零依赖：compose 以 golang 公共镜像挂载本目录 `go run` 拉起（见 deploy/compose.yml），
// 故本目录是独立 module（不进主 module，go build ./... 不会触达）。
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	listen := envOr("ADAPTER_LISTEN", ":9999")
	ollama := strings.TrimRight(envOr("OLLAMA_HOST", "http://127.0.0.1:11434"), "/")
	embedModel := envOr("EMBED_MODEL", "bge-m3")

	a := &adapter{
		ollama:     ollama,
		embedModel: embedModel,
		// CPU 批量前向较慢，超时给足余量（上层还有 ctx 超时兜底）
		client: &http.Client{Timeout: 120 * time.Second},
	}

	mux := http.NewServeMux()
	// {model...} 多段通配：模型名本身含斜杠（如 BAAI/bge-m3），单段 {model} 会 404
	mux.HandleFunc("POST /embedding/{model...}", a.handleEmbedding)
	mux.HandleFunc("POST /reranker/{model...}", a.handleRerank)
	mux.HandleFunc("GET /healthz", a.handleHealthz)

	slog.Info("embedadapter listening",
		slog.String("addr", listen), slog.String("ollama", ollama))
	if err := http.ListenAndServe(listen, mux); err != nil {
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
	client     *http.Client
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
func (a *adapter) handleEmbedding(w http.ResponseWriter, r *http.Request) {
	model := ollamaName(r.PathValue("model"))

	var req embedReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, fmt.Errorf("decode request: %w", err))
		return
	}
	if len(req.Contents) == 0 {
		httpError(w, http.StatusBadRequest, errors.New("contents is empty"))
		return
	}

	var resp ollamaEmbedResp
	in := &ollamaEmbedReq{Model: model, Input: req.Contents}
	if err := a.doJSON(r, http.MethodPost, "/api/embed", in, &resp); err != nil {
		httpError(w, http.StatusBadGateway, fmt.Errorf("call ollama: %w", err))
		return
	}
	if len(resp.Embeddings) != len(req.Contents) {
		httpError(w, http.StatusBadGateway, fmt.Errorf(
			"ollama returned %d embeddings for %d contents",
			len(resp.Embeddings), len(req.Contents)))
		return
	}

	writeJSON(w, http.StatusOK, &embedResp{Embeddings: resp.Embeddings})
}

// ---- rerank：恒等实现 ----

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

// handleRerank 恒等精排：不调模型，按输入顺序返回前 top_n 条，
// 分数按位次递减占位。演示语料下向量粗排已覆盖全部候选，见 deployment.md §6.3。
func (a *adapter) handleRerank(w http.ResponseWriter, r *http.Request) {
	var req rerankReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, fmt.Errorf("decode request: %w", err))
		return
	}

	topN := len(req.Documents)
	if req.TopN != nil && *req.TopN >= 0 && *req.TopN < topN {
		topN = *req.TopN
	}

	items := make([]rerankItem, 0, topN)
	for i := range topN {
		var item rerankItem
		item.Index = i
		item.RelevanceScore = float64(len(req.Documents)-i) / float64(len(req.Documents))
		item.Document.Text = req.Documents[i]
		items = append(items, item)
	}

	writeJSON(w, http.StatusOK, &rerankResp{Results: items})
}

// ---- 健康探测 ----

// handleHealthz 探测 Ollama 可达性与模型就绪：
//   - GET /api/version 确认 Ollama 存活；
//   - POST /api/show 确认 embedModel 已拉取（首次下载约 1.2GB 期间返回 404 → 此处 502，
//     deps:wait 据此等待，不会在模型未就绪时误判依赖齐备）。
func (a *adapter) handleHealthz(w http.ResponseWriter, r *http.Request) {
	var vresp struct {
		Version string `json:"version"`
	}
	if err := a.doJSON(r, http.MethodGet, "/api/version", nil, &vresp); err != nil {
		httpError(w, http.StatusBadGateway, fmt.Errorf("ollama unreachable: %w", err))
		return
	}
	// 只关心状态码（200=已拉取 / 404=不存在），响应体不解析
	if err := a.doJSON(r, http.MethodPost, "/api/show",
		map[string]string{"model": a.embedModel}, nil); err != nil {
		httpError(w, http.StatusBadGateway,
			fmt.Errorf("model %q not ready (still pulling?): %w", a.embedModel, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "ok", "ollama": vresp.Version, "model": a.embedModel})
}

// ---- 公共工具 ----

// ollamaName 把路径里的模型名映射为 Ollama 本地模型名：
// 取最后一段（"BAAI/bge-m3" → "bge-m3"，与 ollama pull 的命名一致）
func ollamaName(pathModel string) string {
	if i := strings.LastIndex(pathModel, "/"); i >= 0 {
		return pathModel[i+1:]
	}
	return pathModel
}

// doJSON 向 Ollama 发 JSON 请求并解析 JSON 响应
func (a *adapter) doJSON(r *http.Request, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		body = strings.NewReader(string(b))
	}

	req, err := http.NewRequestWithContext(r.Context(), method, a.ollama+path, body)
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("write response", slog.Any("error", err))
	}
}

func httpError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
	slog.Error("request failed", slog.Int("status", status), slog.String("error", err.Error()))
}
