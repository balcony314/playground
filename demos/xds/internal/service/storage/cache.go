package storage

import (
	"errors"
	"fmt"

	"github.com/balcony314/xds/internal/entity"
	"github.com/balcony314/xds/internal/errs"
	"github.com/balcony314/xds/pkg/logging"
)

// SyncServiceInfoCache 将单个服务的元数据从仓库同步到本地缓存
// 服务在仓库中不存在（已删除）时，同步动作表现为从缓存中移除该服务
func (s *service) SyncServiceInfoCache(service string) error {
	logging.With("service", service).Debugf("sync service info to cache")
	svc, err := s.storageRepo.GetMicroService(service)
	// 配置已被删除，需同步清理本地缓存，避免残留已下线服务
	if errors.Is(err, errs.ErrServiceNotFound) {
		logging.Errorf("%v: %s", errs.ErrServiceNotFound, service)
		s.cacheLock.Lock()
		defer s.cacheLock.Unlock()
		delete(s.serviceCache, service)
		return nil
	} else if err != nil {
		logging.Error(err)
		return fmt.Errorf("sync service info cache for %s: %w", service, err)
	}

	s.cacheLock.Lock()
	defer s.cacheLock.Unlock()
	s.serviceCache[service] = svc
	return nil
}

// SyncInstanceCache 将单个服务的实例列表同步到本地缓存
// 由于nacos sdk自身已维护实例缓存，这里改为空实现，保留函数签名以维持订阅回调契约
func (s *service) SyncInstanceCache(service string) error {
	//logging.With("service", service).Debugf("sync service instances to cache")
	//instances, err := s.storageRepo.ListInstances(service, false, nil)
	//if err != nil {
	//	logging.Error(err)
	//	return fmt.Errorf("sync instance cache for %s: %w", service, err)
	//}
	//s.cacheLock.Lock()
	//defer s.cacheLock.Unlock()
	////服务熔断保护
	//// TODO: 是否考虑直接使用nacos sdk的
	//// bugfix: 实例为0依然要更新缓存，否则页面更新的全部都拿不到了
	////if v := len(instances.ApplyFilter(&entity.InstanceAvailableFilter)); v == 0 {
	////	logging.With("service", service).Warnf("no available instance, skip update local cache, available:%d, total:%d", v, len(instances))
	////	return nil
	////}
	//s.instanceCache[service] = instances
	return nil
}

// ListInstances 列出服务实例，直接透传仓库（nacos sdk自带缓存，不再做二级缓存）
func (s *service) ListInstances(serviceName string, onlyAvailable bool, filter *entity.InstanceFilter) (entity.InstanceList, error) {
	// 由于nacos sdk本身已经自己做了一层缓存，再次缓存只会浪费内存，因此这里直接调用sdk即可，
	// 为了保留后续的可替代性，相应的缓存代码与调用并没有删除，只是做了注释
	return s.storageRepo.ListInstances(serviceName, onlyAvailable, filter)
}

// ListAllMicroServices 列出缓存中的全部服务元数据（快照拷贝，调用方可安全修改）
func (s *service) ListAllMicroServices() ([]entity.MicroService, error) {
	s.cacheLock.RLock()
	defer s.cacheLock.RUnlock()

	result := []entity.MicroService{}
	for _, v := range s.serviceCache {
		result = append(result, *v)
	}
	return result, nil
}

// GetServiceInfo 从本地缓存读取服务元数据，未命中返回ErrServiceNotFound
func (s *service) GetServiceInfo(service string) (*entity.MicroService, error) {
	s.cacheLock.RLock()
	defer s.cacheLock.RUnlock()

	svc, ok := s.serviceCache[service]
	if !ok {
		return nil, fmt.Errorf("%w: %s", errs.ErrServiceNotFound, service)
	}
	return svc, nil
}
