package web

import "fmt"

// Response 统一的 HTTP 响应结构
type Response struct {
	Code        int    `json:"code"`
	Message     string `json:"message"`
	UserMessage string `json:"userMessage"`
	Level       string `json:"level"`
	Data        any    `json:"data"`
}

// WebError 携带响应信息的业务错误，可通过链式方法按需覆盖字段
type WebError struct {
	Response
	HttpCode int
	Err      error
}

func (e WebError) Error() string {
	return fmt.Sprintf("error message: %s, error stacktrace: %+v", e.Response.Message, e.Err)
}

func (e WebError) SetError(err error) WebError {
	e.Err = err
	return e.SetMessage(err.Error())
}

func (e WebError) SetMessage(msg string) WebError {
	e.Message = msg
	e.UserMessage = msg
	return e
}

func (e WebError) SetUserMessage(msg string) WebError {
	e.UserMessage = msg
	return e
}

func (e WebError) SetLevel(level string) WebError {
	e.Level = level
	return e
}

func (e WebError) SetCode(code int) WebError {
	e.Code = code
	return e
}

// NewWebSuccess 构造成功响应，并对未初始化的空切片/空映射做兜底转换
func NewWebSuccess(data any) Response {
	return Response{
		Code:        0,
		Message:     "success",
		UserMessage: "success",
		Level:       "info",
		Data:        EnsureResultNotNull(data),
	}
}
