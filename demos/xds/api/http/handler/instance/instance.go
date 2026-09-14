// Package instance 提供实例管理的HTTP handler，
// 包括实例的查询、注册、更新、注销与批量清理
package instance

import (
	"net/http"

	"github.com/balcony314/xds/internal/entity"
	"github.com/balcony314/xds/internal/errs"
	"github.com/balcony314/xds/internal/service"
	"github.com/balcony314/xds/pkg/logging"
	"github.com/balcony314/xds/pkg/web"
	"github.com/gin-gonic/gin"
)

// Register 注册实例管理相关路由，包含实例的查询、注册、更新、注销与批量清理。
// 迁移说明：raw中的middleware.TokenRequired鉴权包装已按新架构契约移除（系统无鉴权）
func Register(router *gin.RouterGroup) {
	router.POST("/instances/list", list)
	router.POST("/instances", register)
	router.PUT("/instances", update)
	router.DELETE("/instances", deregister)
	router.DELETE("/instances/clean", deregisterAll)
}

// list 查询指定服务的实例列表
func list(c *gin.Context) {
	var payload entity.InstanceListRequest
	err := c.BindJSON(&payload)
	if err != nil {
		c.Error(errs.ErrBadRequest.SetError(err))
		return
	}

	//sdk来源的请求直接读缓存即可，web端请求需要保证数据准确性
	if payload.FromSdk {
		data, err := service.Storage().ListInstances(payload.Service, payload.OnlyAvailable, &payload.InstanceFilter)
		if err != nil {
			c.Error(err)
			return
		}
		c.JSON(http.StatusOK, web.NewWebSuccess(data))
		return
	}

	//默认从web端加载，保证数据准确性。nacos sdk返回的数据是做过容灾处理的，与web端显示不断不一样
	data, err := service.Storage().ListInstancesFromWeb(payload.Service, payload.OnlyAvailable, &payload.InstanceFilter)
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, web.NewWebSuccess(data))
}

// register 向指定服务注册一个实例，供proxyless模式下的服务直连注册使用
func register(c *gin.Context) {
	// 不传source时，默认为custom实例
	payload := entity.InstanceRegisterRequest{Instance: entity.Instance{Source: entity.InstanceSourceTypeCustom}}
	err := c.BindJSON(&payload)
	if err != nil {
		c.Error(errs.ErrBadRequest.SetError(err))
		return
	}

	logging.With("params", payload).Debugf("received instance register request")
	err = service.Storage().RegisterInstance(payload.Service, &payload.Instance)
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, web.NewWebSuccess("ok"))
}

// update 更新指定服务下的实例信息，仅custom实例可通过此接口修改
func update(c *gin.Context) {
	// 不传source时，默认为custom实例
	payload := entity.InstanceRegisterRequest{Instance: entity.Instance{Source: entity.InstanceSourceTypeCustom}}
	err := c.BindJSON(&payload)
	if err != nil {
		c.Error(errs.ErrBadRequest.SetError(err))
		return
	}

	logging.With("params", payload).Debugf("received instance update request")
	err = service.Storage().UpdateInstance(payload.Service, &payload.Instance)
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, web.NewWebSuccess("ok"))
}

// deregister 注销指定服务下的单个实例（按IP+Port定位）
func deregister(c *gin.Context) {
	var payload entity.InstanceDeRegisterRequest
	err := c.BindJSON(&payload)
	if err != nil {
		c.Error(errs.ErrBadRequest.SetError(err))
		return
	}

	logging.With("params", payload).Debugf("received instance deregister request")

	err = service.Storage().DeregisterInstance(payload.Service, payload.IP, payload.Port)
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, web.NewWebSuccess("ok"))
}

// deregisterAll 批量清理指定服务下的实例，可通过IncludeHealthy控制是否连带清理健康实例
func deregisterAll(c *gin.Context) {
	var payload entity.InstanceCleanRequest
	err := c.BindJSON(&payload)
	if err != nil {
		c.Error(errs.ErrBadRequest.SetError(err))
		return
	}

	err = service.Storage().CleanInstances(payload.Service, payload.IncludeHealthy)
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, web.NewWebSuccess("ok"))
}
