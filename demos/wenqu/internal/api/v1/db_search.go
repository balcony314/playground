package v1

import (
	"errors"
	"io"
	"net/http"

	"github.com/labstack/echo/v4"

	"datasearch/internal/service"
)

// withDBSearchRouter 注册数据查询业务域的路由，提供三个端点，
// 入参均为 query 参数 content（自然语言问题）：
//
//	GET /api/v1/db_search/clickhouse        —— 单源：react agent 经 MCP 工具直查 ClickHouse（同步）
//	GET /api/v1/db_search/clickhouse/stream —— 单源流式：SSE 打字机式输出
//	GET /api/v1/db_search/multi_datasource  —— 多源：状态图编排，planner 决定查 ClickHouse/ES，返回 Markdown 报告
func withDBSearchRouter(e *echo.Echo, dbSearchService *service.DBSearchService) *echo.Echo {
	group := e.Group("/api/v1/db_search")

	group.GET("/clickhouse", func(c echo.Context) error {
		req, err := bindDBSearchRequest(c)
		if err != nil {
			return err
		}

		answer, err := dbSearchService.SearchFromClickhouse(c.Request().Context(), req.Content)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "clickhouse search failed").WithInternal(err)
		}

		return c.String(http.StatusOK, answer)
	})

	group.GET("/clickhouse/stream", func(c echo.Context) error {
		req, err := bindDBSearchRequest(c)
		if err != nil {
			return err
		}

		reader, err := dbSearchService.StreamFromClickhouse(c.Request().Context(), req.Content)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "clickhouse stream failed").WithInternal(err)
		}
		defer reader.Close()

		// SSE 打字机式输出：逐帧推送生成的增量内容，直至流结束发送 [DONE] 终止帧
		c.Response().Header().Set(echo.HeaderContentType, "text/event-stream")
		c.Response().Header().Set(echo.HeaderCacheControl, "no-cache")
		c.Response().Header().Set("Connection", "keep-alive")
		c.Response().WriteHeader(http.StatusOK)

		for {
			msg, err := reader.Recv()
			if errors.Is(err, io.EOF) {
				_, _ = c.Response().Write([]byte("data: [DONE]\n\n")) // 终止帧写失败无需处理
				c.Response().Flush()
				return nil
			}
			if err != nil {
				return echo.NewHTTPError(http.StatusInternalServerError, "stream interrupted").WithInternal(err)
			}
			if msg.Content == "" {
				continue
			}

			if _, writeErr := io.WriteString(c.Response(), "data: "+msg.Content+"\n\n"); writeErr != nil {
				return nil // 客户端断开，正常结束
			}
			c.Response().Flush()
		}
	})

	group.GET("/multi_datasource", func(c echo.Context) error {
		req, err := bindDBSearchRequest(c)
		if err != nil {
			return err
		}

		report, err := dbSearchService.Search(c.Request().Context(), req.Content)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "multi datasource search failed").WithInternal(err)
		}

		return c.String(http.StatusOK, report)
	})

	return e
}

// DBSearchRequest 数据查询接口的统一请求结构
type DBSearchRequest struct {
	Content string `query:"content" json:"content"`
}

// bindDBSearchRequest 绑定并校验请求参数
func bindDBSearchRequest(c echo.Context) (*DBSearchRequest, error) {
	var req DBSearchRequest
	if err := c.Bind(&req); err != nil {
		return nil, echo.NewHTTPError(http.StatusBadRequest, "invalid param").WithInternal(err)
	}
	if req.Content == "" {
		return nil, echo.NewHTTPError(http.StatusBadRequest, "param content is required")
	}

	return &req, nil
}
