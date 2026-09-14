# 使用说明书

> 面向调用方：如何用自然语言查询 ClickHouse 与 Elasticsearch

## 1. 前置条件

服务已启动（`http://127.0.0.1:5002`），环境搭建与启动步骤见[部署文档](deployment.md)。验证服务存活：

```bash
curl -s http://127.0.0.1:5002/healthz   # {"status":"ok"}
```

## 2. API 总览与选型

三个端点，入参统一为 query 参数 `content`（自然语言问题，必填）：

| 端点 | 链路 | 输出形态 | 适用场景 |
|------|------|----------|----------|
| `GET /api/v1/db_search/clickhouse` | ReAct 单源（同步） | 纯文本答案 | 只查 ClickHouse，等完整结果 |
| `GET /api/v1/db_search/clickhouse/stream` | ReAct 单源（SSE） | 打字机流式 | 只查 ClickHouse，要实时反馈 |
| `GET /api/v1/db_search/multi_datasource` | 多源状态图 | Markdown 查询报告 | 不确定数据在哪个源 / 跨源查询 |

选型建议：明确是 ClickHouse 分析类问题 → 单源端点（LLM 自主探索库表，灵活）；涉及 ES 全文/日志、或不确定数据位置 → 多源端点（planner 自动判定，失败自动重试）。

## 3. 单源同步：查 ClickHouse

```bash
curl "http://127.0.0.1:5002/api/v1/db_search/clickhouse?content=demo库里订单表有多少条数据"
```

响应（`200`，`text/plain`，实测样例）：

```
demo 库的订单表（orders）共有 15 条数据。
```

行为说明：LLM 作为 ReAct Agent 自主决定调用顺序——通常先 `list_databases` 探测库、`list_tables` 看表结构、最后 `run_select_query` 执行 SQL，全程可查 server 日志审计。同步端点会阻塞到答案生成完毕（实测约 10~30s，取决于模型与问题复杂度），客户端超时建议 ≥ 60s。

## 4. 单源流式：SSE 打字机

```bash
curl -N "http://127.0.0.1:5002/api/v1/db_search/clickhouse/stream?content=统计各支付状态的订单数"
```

响应（`200`，`text/event-stream`）：

```
data: 根据

data: 查询

data: 结果

...

data: [DONE]
```

帧格式：

- 每帧 `data: <增量文本>\n\n`，拼接所有帧即完整答案；
- 终止帧固定为 `data: [DONE]\n\n`，收到后关闭连接；
- 工具调用阶段（探索库表、执行 SQL）无输出，首帧延迟 = 思考 + 工具调用耗时；
- 中途出错返回非 SSE 的 `500` JSON。

前端消费示例：

```javascript
const es = new EventSource("/api/v1/db_search/clickhouse/stream?content=统计各支付状态的订单数");
let answer = "";
es.onmessage = (e) => {
  if (e.data === "[DONE]") { es.close(); return; }
  answer += e.data;          // 增量渲染
};
es.onerror = () => es.close();
```

## 5. 多源查询：Markdown 报告

```bash
curl "http://127.0.0.1:5002/api/v1/db_search/multi_datasource?content=上个月销售额最高的类目是哪些，再看看相关的差评"
```

响应（`200`，`text/plain`，Markdown 报告）。结构大致为：改写后的问题 → 按数据源分节的查询结果（含生成的 SQL / DSL 与数据结果）→ 综合结论。示意：

````markdown
## 问题
上个月（2026-08）销售额最高的类目，及相关差评

### ClickHouse
```sql
SELECT category, sum(amount) AS total FROM demo.orders
WHERE order_date >= '2026-08-01' AND order_date < '2026-09-01'
GROUP BY category ORDER BY total DESC LIMIT 1
```
查询结果：electronics，¥26,896

### Elasticsearch
（差评评论内容，rating ≤ 2）

## 结论
上个月销售额最高的类目是 electronics（¥26,896），相关差评集中在……
````

行为说明：

- planner 自动判定需要哪些数据源（可同时命中 ClickHouse 与 ES）；
- 单源查询失败会带报错自动重试（每源最多 2 次），重试仍失败则该源输出失败占位说明，**不影响其他源**；
- 相对时间（"上个月""最近一周"）由问题改写节点解析为具体时间窗；
- 实测耗时约 20~60s（多节点多次 LLM 调用），客户端超时建议 ≥ 120s。

## 6. 提问技巧

问得好，查得准：

| 技巧 | 差 | 好 |
|------|----|----|
| 指明库/索引 | "有多少订单" | "demo 库的订单表有多少数据" |
| 时间具体化 | "最近卖得怎么样" | "2026 年 8 月各品类销售额" |
| ES 问题给关键词 | "看看评价" | "找提到'手机'且评分低于 2 的评论" |
| 复合问题拆清楚 | "销量和评价" | "上个月销售额最高的类目，以及它的差评"（多源端点自动分发） |

约束：所有查询只读（SELECT / Search API），写操作类问题会被拒绝；问题语言不限，中文英文均可。

## 7. 错误处理

| 状态码 | 含义 | 处理 |
|--------|------|------|
| `400` `param content is required` | 缺少 `content` 参数 | 补参数 |
| `500` `clickhouse search failed` | 单源链路失败（模型/网关/MCP） | 看 server 日志定位；多为网关额度或依赖未就绪 |
| `500` `multi datasource search failed` | 多源链路失败（RAG/embedding/rerank 等基础设施） | 见[部署文档](deployment.md)故障排查表 |
| SSE 中断（无 `[DONE]`） | 流式链路异常 | 重试；复现则查 server 日志 |

多源报告内单源失败**不算请求失败**——报告仍正常返回，该源章节显示失败原因。

## 8. 常见问题

- **两次问同一问题答案不一样？** 模型 temperature=0，但探索路径（ReAct 工具调用序列）可能不同；结果以数据为准，SQL/DSL 会附在多源报告中可复核。
- **能查到 Milvus 里的知识库吗？** 不能。Milvus 是内部 RAG 知识库（表结构 + 查询范式），不是对外查询对象。
- **支持并发吗？** 支持。图与 Agent 编译后复用，请求状态相互隔离。
- **怎么知道它执行了什么 SQL？** 单源看 server 日志（callback 全链路审计）；多源报告直接内嵌生成的 SQL / DSL。

相关文档：[架构文档](architecture.md) · [设计文档](design.md) · [前置知识点](prerequisites.md) · [部署文档](deployment.md)
