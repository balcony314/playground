商品评论索引：全文检索场景，text 字段用 ik 分词，支持关键词/短语/范围组合查询

PUT product_reviews
{
  "settings": {
    "number_of_shards": 1,
    "number_of_replicas": 0
  },
  "mappings": {
    "properties": {
      "review_id":  { "type": "keyword" },
      "product_id": { "type": "keyword" },
      "user_id":    { "type": "integer" },
      "rating":     { "type": "byte" },
      "title":      { "type": "text", "analyzer": "ik_max_word" },
      "content":    { "type": "text", "analyzer": "ik_max_word" },
      "sentiment":  { "type": "keyword" },
      "verified":   { "type": "boolean" },
      "created_at": { "type": "date" }
    }
  }
}
