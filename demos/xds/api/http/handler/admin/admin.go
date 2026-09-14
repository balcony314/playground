// Package admin 提供本地admin server的HTTP handler，
// 包括就绪/健康探针和缓存查询接口
package admin

import (
	"net/http"

	"github.com/balcony314/xds/internal/service"
	"github.com/balcony314/xds/pkg/probe"
	"github.com/balcony314/xds/pkg/web"
	"github.com/gin-gonic/gin"
)

// Register 注册admin server的路由，包括就绪/健康探针和缓存查询接口
func Register(router *gin.RouterGroup) {
	//就绪探针与存活探针，供k8s等编排系统探测服务状态
	router.GET("/-/ready", probe.HandleReadyProbe)
	router.GET("/-/healthy", probe.HandleHealthyProbe)
	//dump内存缓存中某个服务的详情与实例列表，用于本地调试
	router.GET("/-/dump", cacheDump)
}

// cacheDump 查询内存缓存中指定服务的服务详情与全部实例列表，用于排查缓存数据问题
func cacheDump(c *gin.Context) {
	svc, err := service.Storage().GetServiceInfo(c.Query("service"))
	if err != nil {
		c.Error(err)
		return
	}
	ins, err := service.Storage().ListInstances(c.Query("service"), true, nil)
	if err != nil {
		c.Error(err)
		return
	}

	c.JSON(http.StatusOK, web.NewWebSuccess(gin.H{
		"service":   svc,
		"instances": ins,
	}))
}
