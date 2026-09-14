#!/usr/bin/env bash
# 演示数据初始化：ES 索引 product_reviews / app_logs + 示例文档
# 幂等策略：先删后建，重复执行会重置为初始数据集
# 注意：mapping 与 cmd/ragload/rag/schema/mapping 一致，但 ik_max_word 换成 standard
#（compose 的原生 ES 8.19 镜像不含 ik 插件）
set -euo pipefail
ES_ADDR="${ES_ADDR:-http://127.0.0.1:9200}"

# ---- 商品评论索引 ----
curl -sf -X DELETE "$ES_ADDR/product_reviews" >/dev/null 2>&1 || true
curl -sf -X PUT "$ES_ADDR/product_reviews" -H 'Content-Type: application/json' -d '{
  "settings": { "number_of_shards": 1, "number_of_replicas": 0 },
  "mappings": { "properties": {
    "review_id":  { "type": "keyword" },
    "product_id": { "type": "keyword" },
    "user_id":    { "type": "integer" },
    "rating":     { "type": "byte" },
    "title":      { "type": "text", "analyzer": "standard" },
    "content":    { "type": "text", "analyzer": "standard" },
    "sentiment":  { "type": "keyword" },
    "verified":   { "type": "boolean" },
    "created_at": { "type": "date" }
  } }
}'; echo

# ---- 应用日志索引 ----
curl -sf -X DELETE "$ES_ADDR/app_logs" >/dev/null 2>&1 || true
curl -sf -X PUT "$ES_ADDR/app_logs" -H 'Content-Type: application/json' -d '{
  "settings": { "number_of_shards": 1, "number_of_replicas": 0 },
  "mappings": { "properties": {
    "trace_id":   { "type": "keyword" },
    "service":    { "type": "keyword" },
    "level":      { "type": "keyword" },
    "message":    { "type": "text", "analyzer": "standard" },
    "host":       { "type": "keyword" },
    "latency_ms": { "type": "integer" },
    "status":     { "type": "short" },
    "endpoint":   { "type": "keyword" },
    "created_at": { "type": "date" }
  } }
}'; echo

# ---- 评论文档（手机/耳机相关，便于"差评""好评"类检索；refresh=wait_for 使后续统计即时可见）----
curl -sf -X POST "$ES_ADDR/_bulk?refresh=wait_for" -H 'Content-Type: application/json' --data-binary @- <<'EOF'
{"index": {"_index": "product_reviews", "_id": "r001"}}
{"review_id": "r001", "product_id": "501", "user_id": 1001, "rating": 5, "title": "旗舰手机非常好用", "content": "屏幕清晰，续航给力，拍照效果出色，系统流畅不卡顿", "sentiment": "positive", "verified": true, "created_at": "2026-08-20T10:00:00Z"}
{"index": {"_index": "product_reviews", "_id": "r002"}}
{"review_id": "r002", "product_id": "501", "user_id": 1002, "rating": 2, "title": "发热严重", "content": "手机玩游戏半小时就发烫，电池掉电快，有点失望", "sentiment": "negative", "verified": true, "created_at": "2026-08-22T14:30:00Z"}
{"index": {"_index": "product_reviews", "_id": "r003"}}
{"review_id": "r003", "product_id": "506", "user_id": 1003, "rating": 4, "title": "降噪耳机音质不错", "content": "降噪效果明显，佩戴舒适，续航中规中矩", "sentiment": "positive", "verified": false, "created_at": "2026-08-25T09:00:00Z"}
{"index": {"_index": "product_reviews", "_id": "r004"}}
{"review_id": "r004", "product_id": "507", "user_id": 1004, "rating": 1, "title": "质量差退货了", "content": "到手就有划痕，客服处理慢，最后退货退款", "sentiment": "negative", "verified": true, "created_at": "2026-09-01T18:00:00Z"}
{"index": {"_index": "product_reviews", "_id": "r005"}}
{"review_id": "r005", "product_id": "512", "user_id": 1005, "rating": 5, "title": "笔记本性能强", "content": "编译速度快，散热好，键盘手感佳，值得推荐", "sentiment": "positive", "verified": true, "created_at": "2026-09-05T11:20:00Z"}
{"index": {"_index": "product_reviews", "_id": "r006"}}
{"review_id": "r006", "product_id": "502", "user_id": 1006, "rating": 3, "title": "外套版型一般", "content": "面料还行，版型偏大，颜色和图片有色差", "sentiment": "neutral", "verified": false, "created_at": "2026-09-08T20:10:00Z"}
EOF
echo

# ---- 日志文档（order-service 错误集中，便于"错误日志"类检索）----
curl -sf -X POST "$ES_ADDR/_bulk?refresh=wait_for" -H 'Content-Type: application/json' --data-binary @- <<'EOF'
{"index": {"_index": "app_logs", "_id": "l001"}}
{"trace_id": "t1001", "service": "order-service", "level": "ERROR", "message": "failed to deduct stock for order o20260810008", "host": "pod-1", "latency_ms": 850, "status": 500, "endpoint": "/api/order/create", "created_at": "2026-09-10T08:00:01Z"}
{"index": {"_index": "app_logs", "_id": "l002"}}
{"trace_id": "t1002", "service": "order-service", "level": "ERROR", "message": "payment gateway timeout after 3000ms", "host": "pod-1", "latency_ms": 3020, "status": 504, "endpoint": "/api/pay/callback", "created_at": "2026-09-10T08:05:30Z"}
{"index": {"_index": "app_logs", "_id": "l003"}}
{"trace_id": "t1003", "service": "search-service", "level": "WARN", "message": "es query slow threshold exceeded 500ms", "host": "pod-3", "latency_ms": 620, "status": 200, "endpoint": "/api/search", "created_at": "2026-09-11T10:00:00Z"}
{"index": {"_index": "app_logs", "_id": "l004"}}
{"trace_id": "t1004", "service": "order-service", "level": "ERROR", "message": "duplicate order id detected o20260905014", "host": "pod-2", "latency_ms": 45, "status": 409, "endpoint": "/api/order/create", "created_at": "2026-09-11T15:22:10Z"}
{"index": {"_index": "app_logs", "_id": "l005"}}
{"trace_id": "t1005", "service": "user-service", "level": "INFO", "message": "user login success from app channel", "host": "pod-5", "latency_ms": 88, "status": 200, "endpoint": "/api/user/login", "created_at": "2026-09-12T09:30:00Z"}
{"index": {"_index": "app_logs", "_id": "l006"}}
{"trace_id": "t1006", "service": "order-service", "level": "INFO", "message": "order created successfully o20260910015", "host": "pod-2", "latency_ms": 120, "status": 200, "endpoint": "/api/order/create", "created_at": "2026-09-12T13:02:00Z"}
EOF
echo

echo "== 索引统计 =="
curl -sf "$ES_ADDR/_cat/indices?v"
