package middleware

import (
	"runtime/debug"

	"github.com/balcony314/xds/internal/errs"
	"github.com/balcony314/xds/pkg/logging"
	"github.com/gin-gonic/gin"
)

// PanicHandleMiddleWare panic恢复中间件，捕获handler执行链中的panic，
// 防止单个请求异常导致整个进程退出。恢复后记录堆栈日志，
// 并向客户端返回500内部错误响应
func PanicHandleMiddleWare(c *gin.Context) {
	defer func() {
		if r := recover(); r != nil {
			defaultError := errs.ErrInternalServerError
			logging.With("stack", debug.Stack()).Errorf("xds-server panic:%v", r)
			c.JSON(defaultError.HttpCode, defaultError.Response)
			c.Abort()
		}
	}()

	c.Next()
}
