// Package v1 注册 HTTP API 路由
package v1

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"datasearch/internal/service"
)

// NewWebRouter 创建 echo 实例并注册全部路由
func NewWebRouter(dbSearchService *service.DBSearchService) *echo.Echo {
	e := echo.New()
	e.HideBanner = true

	e.GET("/healthz", func(c echo.Context) error {
		return c.NoContent(http.StatusOK)
	})

	return withDBSearchRouter(e, dbSearchService)
}
