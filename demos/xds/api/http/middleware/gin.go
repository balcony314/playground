package middleware

import (
	"fmt"
	"time"

	"github.com/balcony314/xds/pkg/logging"
	"github.com/gin-gonic/gin"
)

// GinAccessLogger 访问日志中间件，请求完成后记录一条结构化访问日志，
// 内容包括状态码、耗时、客户端IP、请求方法、路径、匹配路由、请求用户等，
// 并按状态码分级输出：5XX记为Error、4XX记为Warn、其余记为Info
func GinAccessLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		raw := c.Request.URL.RawQuery
		if raw != "" {
			path = path + "?" + raw
		}

		c.Next()

		latency := time.Now().Sub(start)
		statusCode := c.Writer.Status()
		clientIP := c.ClientIP()

		entry := logging.With(
			"status_code", statusCode,
			"latency", latency.String(),
			"client_ip", clientIP,
			"method", c.Request.Method,
			"user_agent", c.GetHeader("User-Agent"),
			"path", path,
			"route", c.FullPath(),
			"user", c.GetString("user"),
		)

		if len(c.Errors) > 0 {
			//请求处理链上有错误时，将错误一并记录，并额外输出到标准输出
			err := c.Errors.Last()
			entry = entry.With("error", err)
			defer fmt.Printf("%s error: %+v", time.Now().Format(time.RFC3339), err)
		}

		if statusCode >= 500 {
			entry.Error("gin access")
		} else if statusCode >= 400 {
			entry.Warn("gin access")
		} else {
			entry.Info("gin access")
		}

	}
}
