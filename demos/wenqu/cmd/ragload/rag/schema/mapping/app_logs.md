应用日志索引：结构化日志检索场景，按天滚动（logstash-* 风格命名），level/status 支持聚合

PUT app_logs
{
  "settings": {
    "number_of_shards": 1,
    "number_of_replicas": 0
  },
  "mappings": {
    "properties": {
      "trace_id":   { "type": "keyword" },
      "service":    { "type": "keyword" },
      "level":      { "type": "keyword" },
      "message":    { "type": "text", "analyzer": "standard" },
      "host":       { "type": "keyword" },
      "latency_ms": { "type": "integer" },
      "status":     { "type": "short" },
      "endpoint":   { "type": "keyword" },
      "created_at": { "type": "date" }
    }
  }
}
