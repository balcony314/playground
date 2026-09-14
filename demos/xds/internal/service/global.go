// Package service 业务逻辑层。Services 为全局服务管理器，
// Init() 完成仓库与服务的装配（sync.Once 防止重复初始化破坏缓存）
package service

import (
	"fmt"
	"sync"

	repostorage "github.com/balcony314/xds/internal/repo/storage"
	microservice "github.com/balcony314/xds/internal/service/micro_service"
	storagesvc "github.com/balcony314/xds/internal/service/storage"
	"github.com/balcony314/xds/pkg/logging"
)

// Services 全局业务层服务集合，进程启动时初始化，各处通过它访问业务服务
var Services = &ServiceManager{}

// ServiceManager 业务层服务管理器，聚合对外提供的各业务服务
type ServiceManager struct {
	Storage      storagesvc.Service
	MicroService microservice.Service
}

// Storage 返回全局存储服务（负责服务/实例数据维护与变更通知）
func Storage() storagesvc.Service { return Services.Storage }

// MicroService 返回全局微服务元数据管理服务
func MicroService() microservice.Service { return Services.MicroService }

// once 保证 Init 只执行一次：多次初始化会导致 service 层缓存异常（与 raw 相同的 BUGFIX 语义）
var once sync.Once

// Init 按依赖顺序装配 repo 层与 service 层，进程生命周期内必须且只能成功执行一次
func Init() error {
	var err error
	once.Do(func() {
		logging.Infof("initializing services")
		defer logging.Infof("services initialized")

		// 注册中心客户端（Nacos），作为服务元数据与实例的存储来源
		repo, rerr := repostorage.NewNacosRepository()
		if rerr != nil {
			err = fmt.Errorf("初始化 Nacos 仓库失败: %w", rerr)
			return
		}
		// Storage 负责服务信息与实例缓存、事件监听；MicroService 负责服务配置的增删改查
		st, serr := storagesvc.NewService(repo)
		if serr != nil {
			err = fmt.Errorf("初始化存储服务失败: %w", serr)
			return
		}
		Services.Storage = st
		Services.MicroService = microservice.NewService(repo)
	})
	return err
}
