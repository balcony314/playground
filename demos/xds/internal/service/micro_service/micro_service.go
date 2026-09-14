// Package microservice 提供微服务元数据的管理服务
package microservice

import (
	"github.com/balcony314/xds/internal/entity"
	repostorage "github.com/balcony314/xds/internal/repo/storage"
)

// Service 微服务元数据管理接口，负责服务定义的保存与删除
type Service interface {
	// SyncMicroService 保存服务元数据（不存在则创建，存在则覆盖更新）
	SyncMicroService(body *entity.MicroService) error
	// DeleteMicroService 删除服务，删除前先清理全部实例避免实例残留
	DeleteMicroService(serviceName string) error
}

type service struct {
	storageRepo repostorage.StorageRepository
}

// NewService 构造微服务元数据管理服务
func NewService(storageRepo repostorage.StorageRepository) Service {
	return &service{storageRepo: storageRepo}
}

// SyncMicroService 保存服务元数据，透传仓库
func (s *service) SyncMicroService(body *entity.MicroService) error {
	return s.storageRepo.SaveMicroService(body)
}

// DeleteMicroService 删除服务：先清理全部实例避免残留，再删除服务元数据
func (s *service) DeleteMicroService(serviceName string) error {
	// 删除服务前，需要先清楚所有实例信息，避免实例残留
	err := s.storageRepo.CleanInstances(serviceName, true)
	if err != nil {
		return err
	}
	return s.storageRepo.DeleteMicroService(serviceName)
}
