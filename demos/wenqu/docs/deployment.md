# 部署文档

> 本地一键部署与端到端验证手册（含实测踩坑记录）

## 0. 前置要求

- Docker ≥ 24 与 Docker Compose v2
- Go ≥ 1.22（`go run ./cmd/server`）
- [Taskfile](https://taskfile.dev) ≥ 3.3（可选但推荐，常用命令已固化，见下表）
- 可访问的 LLM 网关（任意 OpenAI 兼容端点）与 Embedding 服务

## 0.1 Taskfile 常用命令

仓库根目录的 [Taskfile.yml](../Taskfile.yml) 固化了全部常用操作（自动加载 `.env`），`task --list` 查看全量：

| 任务 | 作用 |
|------|------|
| `task deps:up` | 启动全部依赖容器并轮询等待就绪 |
| `task init` | 初始化 ClickHouse + ES 演示数据（幂等：重置为初始数据集） |
| `task ragload` | 灌 RAG 知识库到 Milvus（幂等） |
| `task server` | 前台启动查询服务（:5002） |
| `task check` | go build + go vet + go test |
| `task smoke:single / smoke:stream / smoke:multi` | 三接口冒烟验证 |
| `task e2e` | 一键端到端：起依赖 → 初始化 → 灌库 → 起服务（复用已运行实例）→ 三接口冒烟 |
| `task deps:down` / `task deps:clean` | 停止容器（保留数据卷）/ 连数据卷一起清空 |

以下各节等价于逐条执行上述任务，供需要理解细节或无 Taskfile 环境时使用。

## 1. 启动依赖环境

```bash
cp .env.example .env          # 按需修改凭证
task deps:up                  # 或：docker compose -f deploy/compose.yml up -d
```

六服务（全公共镜像，首次拉取约 3GB）：

| 服务 | 端口 | 说明 |
|------|------|------|
| etcd / minio | — | Milvus 的元数据与对象存储依赖 |
| milvus | 19530 / 9091 | RAG 向量知识库 |
| clickhouse | 8123(HTTP) / 9000(native) | 结构化数据源（账号取 `.env` 的 `CLICKHOUSE_USERNAME/PASSWORD`，默认 default/clickhouse） |
| mcp-clickhouse | 4200 | ClickHouse MCP Server（**SSE transport**，须配 `CLICKHOUSE_MCP_SERVER_TRANSPORT=sse`、`CLICKHOUSE_MCP_BIND_HOST=0.0.0.0`、`CLICKHOUSE_SECURE=false`，compose.yml 已内置） |
| elasticsearch | 9200 | 全文/日志数据源（security 已关闭） |

验证就绪（六服务 healthy）：

```bash
docker compose -f deploy/compose.yml ps
curl -sf http://127.0.0.1:9091/healthz        # Milvus
curl -sf http://127.0.0.1:9200/_cluster/health
timeout 3 curl -sN http://127.0.0.1:4200/sse | head -2   # 应输出 event: endpoint
```

## 2. 初始化演示数据（可选）

```bash
task init       # 一条命令完成下面两步
```

- ClickHouse：[deploy/init/clickhouse.sql](../deploy/init/clickhouse.sql) 重建 `demo` 库（orders 15 行 / user_visits 10 行），DDL 与 [cmd/ragload/rag/schema/ddl/](../cmd/ragload/rag/schema/ddl/) 一致；
- ES：[deploy/init/elasticsearch.sh](../deploy/init/elasticsearch.sh) 重建 `product_reviews` / `app_logs` 索引（各 6 条文档），mapping 与 [cmd/ragload/rag/schema/mapping/](../cmd/ragload/rag/schema/mapping/) 一致。

> ⚠️ **ik 分词器**：RAG 语料中的 mapping 使用 `ik_max_word` 分词器。compose 的原生 ES 镜像**不含 ik 插件**，初始化脚本已替换为 `standard` 分词；若需 ik 语义分词，先 `elasticsearch-plugin install analysis-ik` 并改回脚本中的 analyzer。

## 3. 灌 RAG 知识库

```bash
export EMBEDDING_BASE_URL=http://...     # 必填：BGE 风格 POST /embedding/{model} 服务
go run ./cmd/ragload                     # 或：task ragload（自动加载 .env）
```

预期输出：`rag load done ddl=2 mapping=2 question_sql=8 question_dsl=7`。主键为内容 hash，重复执行幂等。

## 4. 配置并启动服务

`.env` 关键项（完整列表见 [.env.example](../.env.example)，flag 与同名环境变量二选一，flag 优先）：

| 配置 | 说明 |
|------|------|
| `OPEN_AI_API_KEY` | LLM 网关 API Key（必填） |
| `OPEN_ROUTER_BASE_URL` | 任意 OpenAI 兼容端点，默认 OpenRouter |
| `MODEL_NAME` | 模型名，默认 `google/gemini-2.5-pro` |
| `EMBEDDING_BASE_URL` / `EMBEDDING_MODEL` | embedding 服务（必填） |
| `RERANK_BASE_URL` / `RERANK_MODEL` | rerank 服务（协议 `POST /reranker/{model}`，多源链路必需） |

```bash
export $(grep -v '^#' .env | grep -v '^$' | xargs)
go run ./cmd/server                      # 或：task server
```

就绪日志：`milvus client ready` → `embedding model ready` → `clickhouse client ready` → `mcp clickhouse tools loaded count=3` → `http server started on 127.0.0.1:5002`。

## 5. 验证

```bash
task smoke:single   # 等价：curl "http://127.0.0.1:5002/api/v1/db_search/clickhouse?content=demo库里订单表有多少条数据"
task smoke:stream   # 等价：curl -N ".../clickhouse/stream?content=统计各支付状态的订单数"
task smoke:multi    # 等价：curl ".../multi_datasource?content=上个月销售额最高的类目是哪些，再看看手机相关的差评"
task e2e            # 一键：依赖+数据+灌库+服务+三接口冒烟
```

单元测试：`task check`（go build + go vet + go test ./...）。

## 6. 实测踩坑记录（2026-09 全链路验证）

以下问题均在本地端到端验证中发现，对应修复已合入仓库；未合入的需部署侧注意。

### 6.1 MCP 容器默认 stdio，`:4200` 不可用

`mcp/clickhouse` 镜像默认 `stdio` transport 且默认 HTTPS 连 ClickHouse。必须显式设置（[deploy/compose.yml](../deploy/compose.yml) 已修复）：

```yaml
environment:
  CLICKHOUSE_SECURE: "false"                 # 8123 是 HTTP
  CLICKHOUSE_MCP_SERVER_TRANSPORT: sse       # 默认 stdio 无法提供 :4200 服务
  CLICKHOUSE_MCP_BIND_HOST: 0.0.0.0          # 默认 127.0.0.1 端口映射不通
  CLICKHOUSE_MCP_BIND_PORT: "4200"
```

### 6.2 GLM Coding Plan 必须用专用端点

智谱 GLM Coding Plan 套餐**只覆盖专用端点**：

| 端点 | 协议 | Coding Plan 可用性 |
|------|------|-------------------|
| `https://open.bigmodel.cn/api/coding/paas/v4` | OpenAI 兼容 | ✅ 本项目用这个 |
| `https://open.bigmodel.cn/api/anthropic` | Anthropic 兼容 | ✅（本项目协议不匹配，不用） |
| `https://open.bigmodel.cn/api/paas/v4` | OpenAI 兼容 | ❌ 走按量计费，报 `1113/429 余额不足` |

`.env` 配置：`OPEN_ROUTER_BASE_URL=https://open.bigmodel.cn/api/coding/paas/v4`、`MODEL_NAME=glm-4.6`。

### 6.3 Embedding/Rerank 本地适配层（deploy/embedadapter + compose 内 Ollama）

项目的 embedding（`POST /embedding/{model}`）与 rerank（`POST /reranker/{model}`）均为自研协议，公共云服务不直接提供。本地演示已完全编排进 compose（`task deps:up` 一并拉起，无需宿主机预装任何模型服务）：

- **Ollama 容器**（`ollama/ollama` 公共镜像）：embedding 模型运行时，CPU 推理即可；`ollama-pull` 初始化容器首次启动自动拉取 `bge-m3`（1024 维，约 1.2GB，幂等）；
- **适配层容器**（[deploy/embedadapter](../deploy/embedadapter)，纯标准库 Go 独立 module，`golang` 公共镜像 `go run` 拉起）：
  - embedding：自研协议 → Ollama `POST /api/embed`。bge-m3 为对称检索模型，`isQuestion` 字段仅透传，无需 query/document 指令前缀；
  - rerank：恒等精排（按输入顺序返回 top_n）——演示语料仅 2 条 DDL / 2 条 mapping，向量粗排已覆盖全部候选；
  - 就绪探测：`GET :9999/healthz` 校验 Ollama 存活 **且模型已拉取**，`deps:wait` 依赖它（模型下载期间不会误报就绪）。

`.env` 保持 `EMBEDDING_BASE_URL` / `RERANK_BASE_URL` 指向 `http://127.0.0.1:9999` 即可。

> 内存提示：Ollama 容器 + bge-m3 常驻约 2G；宿主机若另跑了 Ollama 服务且不再需要，可 `systemctl stop ollama` 释放（容器不依赖宿主机实例）。
> 智谱 `embedding-3` 不在 Coding Plan 套餐内（报 1113），勿用套餐 key 调它。

### 6.4 推理型模型流式断流（已修复，选型注意）

eino `react.NewAgent` 默认流式工具调用检查器在**首个含内容 chunk** 即判定"无工具调用"；glm / claude 等推理型模型先输出思考/叙述、`tool_calls` 在流末尾，会被误判导致流式断流。[internal/agent/clickhouse/agent.go](../internal/agent/clickhouse/agent.go) 已配置 `fullStreamToolCallChecker`（消费完整流再判定）。更换模型时若流式输出戛然而止，优先排查此处。

### 6.5 中文别名 SQL 语法错误（已修复，选型注意）

glm-4.6 等中文倾向强的模型可能生成 `count() AS 订单数`——裸中文标识符在 ClickHouse 中是语法错误（Code 62）。两个 SQL 生成 Prompt（[internal/agent/clickhouse/prompt.md](../internal/agent/clickhouse/prompt.md)、[internal/orch/datasearch/prompt/clickhouse_searcher.md](../internal/orch/datasearch/prompt/clickhouse_searcher.md)）已加"别名必须 ASCII"约束。

### 6.6 Milvus collection 运行时释放导致 RAG 召回静默失效（已修复）

Milvus 重启或内存压力下 collection 可能被释放回"未加载"态，此后所有向量搜索报 `collection not loaded`。项目原代码从不显式 `LoadCollection`，且召回失败会**静默降级为空上下文继续**（server 日志 `rag recall degraded, continue with empty context`）——症状是多源查询开始"瞎编"表名/索引名（如不存在的 `sales_orders` 索引、`ecommerce` 库），不报任何错误。

修复（[store/vector.go](../internal/store/vector.go)）：`NewVectorStore` 末尾幂等 `LoadCollection` 并等待加载完成，server 启动与 `ragload` 均经此入口自动自愈。若运行中再次发生，重启 server 或重跑 `task ragload` 即可恢复。

### 6.7 其他注意

- ES bulk 写入用 `--data-binary @-` + heredoc（`-` 开头的字面参数会被 curl 当作选项）+ `?refresh=wait_for`（否则刚写入的文档对统计/查询有约 1s 延迟）；建索引脚本建议"先删后建"保证幂等；
- `go run` 的 server 子进程（`/tmp/go-build*/exe/server`）在父 shell 被杀后可能残留并占用 :5002，重启前先清理；
- Milvus collection 维度创建后不可改：更换 embedding 模型（维度变化）须清空 `demo_*` collection 后重新 `ragload`。

## 7. 常见故障排查

| 现象 | 排查 |
|------|------|
| `load mcp clickhouse tools: ... EOF` | MCP 容器 transport 配置（§6.1）；容器刚重建后偶发，重试即可 |
| `mcp clickhouse tools loaded` 后 API 报 429/1113 | LLM 网关端点/额度（§6.2） |
| `embedding api failed` | `EMBEDDING_BASE_URL` 服务可用性；协议是否为 `POST /embedding/{model}` |
| 多源链路 `rerank` 报错中断 | rerank 服务不可用（协议 `POST /reranker/{model}`），多源链路无降级 |
| SSE 流在工具调用前戛然而止 | §6.4 的 checker 问题（已修复）或模型生成非法 SQL 被 sqlguard 拒绝（看 server 日志 `sqlguard rejected`） |
| `no related question query found` | Milvus 中语料为空，先执行 §3 灌库 |
| 多源报告瞎编表名/索引名（不报错） | server 日志 `rag recall degraded ... collection not loaded`：collection 被释放（§6.6，启动已自愈），重启 server 或重跑 `task ragload` |

相关文档：[使用说明书](usage.md) · [架构文档](architecture.md) · [设计文档](design.md) · [前置知识点](prerequisites.md)
