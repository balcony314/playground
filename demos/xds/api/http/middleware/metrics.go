package middleware

import (
	"fmt"
	"regexp"
	"time"

	"github.com/balcony314/xds/internal/metric"
	"github.com/gin-gonic/gin"
)

// MetricsMiddleware HTTP接口指标统计中间件，请求完成后记录接口耗时与请求总量两个指标。
// 仅统计 /api/vN/ 前缀的业务接口，安全扫描等噪声请求直接跳过
func MetricsMiddleware() gin.HandlerFunc {
	re := regexp.MustCompile(`^/api/v\d+/.*`)
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		method := c.Request.Method
		startTime := time.Now()
		c.Next()

		//过滤安全部的扫描，只记录接口相关的数据
		if !re.MatchString(path) {
			return
		}

		//状态码必须在c.Next()之后读取：此时handler已执行完毕，
		//Writer上才是最终响应状态；提前读取只能拿到初始值200，导致status_code标签全量失真
		statusCode := c.Writer.Status()

		//使用匹配的route，而不是真实路径，防止监控指标基数过高
		path = c.FullPath()
		latency := time.Since(startTime)
		metric.ClientReqDur.WithLabelValues(path, fmt.Sprint(statusCode), method).Observe(float64(latency / time.Millisecond))
		metric.ClientReqTotal.WithLabelValues(path, fmt.Sprint(statusCode), method).Inc()
	}
}
