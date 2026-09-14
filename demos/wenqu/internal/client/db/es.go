package db

import (
	"crypto/tls"
	"net/http"

	"github.com/elastic/go-elasticsearch/v8"
)

// ElasticSearchConfig Elasticsearch 连接配置
type ElasticSearchConfig struct {
	Hosts    []string
	User     string
	Password string
}

// NewES 创建 ES TypedClient。
// 状态图的 elasticsearch_searcher 节点用它对指定索引执行原始 DSL（Search API，天然只读）。
func NewES(cfg ElasticSearchConfig) (*elasticsearch.TypedClient, error) {
	return elasticsearch.NewTypedClient(elasticsearch.Config{
		Addresses: cfg.Hosts,
		Username:  cfg.User,
		Password:  cfg.Password,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, //nolint:gosec // 内网自签证书场景
			},
		},
	})
}
