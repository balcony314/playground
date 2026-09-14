package errs

import (
	"net/http"
)

var (
	// 预定义的web层业务错误，业务码规则：4000段为4XX客户端错误，5000段为5XX服务端错误
	// 4XX 默认都是warning
	// ErrBadRequest 请求参数错误
	ErrBadRequest = newCustomWebError(http.StatusBadRequest, 4000, "bad request", "参数错误", "warning")
	// ErrUnauthorized 用户未认证（缺少或无效的认证信息）
	ErrUnauthorized = newCustomWebError(http.StatusUnauthorized, 4001, "Unauthorized", "用户未认证", "warning")
	// ErrForbidden 用户已认证但无权限执行该操作
	ErrForbidden = newCustomWebError(http.StatusForbidden, 4003, "forbidden", "无权限", "warning")
	// ErrNotFound 请求的资源不存在
	ErrNotFound = newCustomWebError(http.StatusNotFound, 4004, "not found", "请求的资源不存在", "warning")
	// ErrConflict 资源冲突（如重复创建）
	ErrConflict = newCustomWebError(http.StatusConflict, 4009, "conflict", "已存在", "warning")
	// 5XX 默认都是error
	// ErrInternalServerError 服务端内部错误
	ErrInternalServerError = newCustomWebError(http.StatusInternalServerError, 5000, "Internal Server Error", "内部错误", "error")
)
