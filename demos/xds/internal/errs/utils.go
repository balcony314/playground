package errs

import "github.com/balcony314/xds/pkg/web"

// newCustomWebError 构造预定义的web业务错误。
//
// httpCode为返回给客户端的HTTP状态码；code为业务错误码（4XXX客户端错误、5XXX服务端错误）；
// message为面向开发者的错误信息，userMessage为直接展示给用户的提示文案；
// level标识错误级别（warning/error），供日志分级与告警使用
func newCustomWebError(httpCode, code int, message, userMessage, level string) web.WebError {
	return web.WebError{
		Response: web.Response{
			Code:        code,
			Message:     message,
			UserMessage: userMessage,
			Level:       level,
		},
		HttpCode: httpCode,
	}
}
