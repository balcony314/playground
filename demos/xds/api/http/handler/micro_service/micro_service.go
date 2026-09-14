// Package microservice 提供服务（mesh服务）元数据管理的HTTP handler，
// 包括服务元数据的保存、删除与健康检查配置查询
package microservice

import (
	"net/http"

	"github.com/balcony314/xds/internal/entity"
	"github.com/balcony314/xds/internal/errs"
	"github.com/balcony314/xds/internal/service"
	"github.com/balcony314/xds/pkg/web"
	"github.com/gin-gonic/gin"
)

// Register 注册服务管理相关路由。
// 迁移说明：raw中的middleware.AdminTokenRequired鉴权分组已按新架构契约移除（系统无鉴权），
// 原admin/other两个路由组合并为同一路由组
func Register(router *gin.RouterGroup) {
	router.POST("/mesh", syncMicroService)
	router.DELETE("/mesh/:name", deleteMicroService)
	router.GET("/service/:service/settings/healthy-check", getTTL)
}

// syncMicroService 保存单个mesh服务的元数据（服务由上游系统如下沉平台等推送到xds-server）。
// 本接口只负责把服务元数据写入Nacos配置，不直接下发xDS配置；
// 配置变更由Nacos订阅回调感知后进入事件队列，再驱动Snapshot生成与下发
func syncMicroService(c *gin.Context) {
	var payload entity.MicroService
	err := c.BindJSON(&payload)
	if err != nil {
		c.Error(errs.ErrBadRequest.SetError(err))
		return
	}
	err = service.MicroService().SyncMicroService(&payload)
	if err != nil {
		c.Error(errs.ErrInternalServerError.SetError(err))
		return
	}
	c.JSON(http.StatusOK, web.NewWebSuccess("ok"))
}

// deleteMicroService 删除指定名称的mesh服务
func deleteMicroService(c *gin.Context) {
	name := c.Param("name")
	if err := service.MicroService().DeleteMicroService(name); err != nil {
		c.Error(errs.ErrInternalServerError.SetError(err))
		return
	}
	c.JSON(http.StatusOK, web.NewWebSuccess("ok"))
}

// getTTL 查询指定服务健康检查的TTL配置（即实例心跳超时时间），未配置时默认10秒
func getTTL(c *gin.Context) {
	svc, err := service.Storage().GetServiceInfo(c.Param("service"))
	if err != nil {
		c.Error(err)
		return
	}

	config := entity.HealthyCheckSetting{}
	svc.Settings.Get(entity.HealthyCheckType, &config)
	if config.TTL == 0 {
		//默认设置为10秒
		config.TTL = 10
	}

	c.JSON(http.StatusOK, web.NewWebSuccess(config))
}
