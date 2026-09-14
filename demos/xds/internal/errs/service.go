package errs

import (
	"errors"
)

var (
	// ErrServiceNotFound 服务不存在，一般在查询/操作一个已被删除或未创建的服务时返回
	ErrServiceNotFound = errors.New("service not found")
)
