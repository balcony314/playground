package storage

import "github.com/balcony314/xds/internal/entity"

// StorageRepository 存储仓库接口，定义服务元数据与实例在Nacos中的读写及订阅能力
type StorageRepository interface {
	// InitSubscribe 建立Nacos订阅连接，三个回调分别为：变更入队、实例缓存同步、服务元数据缓存同步
	InitSubscribe(enqueue func(broadcast bool, services ...string), syncInstance func(service string) error, syncServiceInfo func(service string) error) error
	// RegisterInstance 注册实例到Nacos服务发现
	RegisterInstance(service string, instance *entity.Instance) error
	// DeregisterInstance 按IP+端口下线指定实例
	DeregisterInstance(service, ip string, port int) error
	// UpdateInstance 更新实例信息（Nacos侧以重新注册覆盖实现）
	UpdateInstance(service string, instance *entity.Instance) error
	// CleanInstances 批量清理实例，includeHealthy控制是否连同健康实例一起删除
	CleanInstances(service string, includeHealthy bool) error
	// ListInstances 从SDK侧列出实例（走nacos sdk自身缓存，可用/全量可选，支持过滤）
	ListInstances(service string, onlyAvailable bool, filter *entity.InstanceFilter) (entity.InstanceList, error)
	// ListInstancesFromWeb 从web端列出实例（开源SDK无web视图API，实际与SDK视图合并，清理实例等场景使用）
	ListInstancesFromWeb(service string, onlyAvailable bool, filter *entity.InstanceFilter) (entity.InstanceList, error)
	// GetMicroService 从Nacos配置中心读取单个服务元数据
	GetMicroService(serviceName string) (*entity.MicroService, error)
	// DeleteMicroService 删除Nacos配置中心中的服务元数据
	DeleteMicroService(serviceName string) error
	// SaveMicroService 保存服务元数据到Nacos配置中心
	SaveMicroService(conf *entity.MicroService) error
	// ListAllMicroServices 分页遍历Nacos配置中心，列出全部服务元数据
	ListAllMicroServices() ([]entity.MicroService, error)
}
