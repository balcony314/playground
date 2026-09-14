package middleware

import (
	"errors"

	"github.com/balcony314/xds/internal/errs"
	"github.com/balcony314/xds/pkg/web"
	"github.com/gin-gonic/gin"
)

// unwrapAll 沿 Unwrap 链取最底层根因（替代 pkg/errors 的 errors.Cause）
func unwrapAll(err error) error {
	for {
		u := errors.Unwrap(err)
		if u == nil {
			return err
		}
		err = u
	}
}

// ErrorHandleMiddleWare 统一的错误处理中间件。
//
// handler中通过c.Error()上报的错误在此统一转换为HTTP响应：
// 取gin链上最后一个错误，并用unwrapAll沿Unwrap链剥离包装层拿到根因。
// 根因为web.WebError（service层预定义的业务错误，如参数错误、未授权等）时，
// 直接按其HttpCode返回对应的Response；否则视为未知错误，统一包装成500内部错误返回
func ErrorHandleMiddleWare() gin.HandlerFunc {
	return func(c *gin.Context) {
		//先放行后续handler执行，结束后再统一收集处理错误
		c.Next()

		var e error
		if last := c.Errors.Last(); last != nil {
			e = last.Err
		}
		if e != nil {
			// service层哨兵错误映射：包装链上任一层是ErrServiceNotFound时按404返回
			// （spec §8：注册API查询不存在的服务返回404而非500）。
			// 不能改动哨兵本身：xDS注销与缓存未命中判定依赖errors.Is(err, errs.ErrServiceNotFound)
			if errors.Is(e, errs.ErrServiceNotFound) {
				c.JSON(errs.ErrNotFound.HttpCode, errs.ErrNotFound.Response)
				return
			}
			switch err := unwrapAll(e).(type) {
			case web.WebError:
				c.JSON(err.HttpCode, err.Response)
			default:
				//非WebError的未知错误统一按500内部错误处理，原始错误信息透出到UserMessage方便排查
				defaultError := errs.ErrInternalServerError.SetError(err).SetUserMessage(err.Error())
				c.JSON(defaultError.HttpCode, defaultError.Response)
			}
		}
	}
}
