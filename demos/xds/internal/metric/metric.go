package metric

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	// namespace 所有指标的统一前缀，最终指标名形如 xds_server_requests_xxx
	namespace = "xds_server"
)

var (
	// ClientReqDur 客户端请求耗时分布（直方图，单位毫秒），
	// 按路由模板、状态码、请求方法三个维度统计，桶边界为100/500/1000/5000ms
	ClientReqDur = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Subsystem: "requests",
		Name:      "duration_ms",
		Help:      "xds server requests duration(ms).",
		Buckets:   []float64{100, 500, 1000, 5000},
	}, []string{"path", "status_code", "method"})

	// ClientReqTotal 客户端请求累计总量（计数器），可用于计算QPS，
	// 与耗时指标同维度：路由模板、状态码、请求方法
	ClientReqTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "requests",
		Name:      "total",
		Help:      "xds server requests qps.",
	}, []string{"path", "status_code", "method"},
	)
)
