package storage

import (
	"sync"
	"time"

	"github.com/balcony314/xds/internal/entity"
	"github.com/balcony314/xds/internal/queue"
	repostorage "github.com/balcony314/xds/internal/repo/storage"
	"github.com/balcony314/xds/pkg/logging"
)

// Service 存储业务层接口，维护服务/实例两级缓存，是xDS数据面的统一数据入口
type Service interface {
	// RegisterInstance 注册实例，注册前根据服务配置决定是否关闭服务端健康检查
	RegisterInstance(serviceName string, instance *entity.Instance) error
	// DeregisterInstance 按IP+端口下线指定实例
	DeregisterInstance(serviceName, ip string, port int) error
	// UpdateInstance 更新实例，更新前根据服务配置决定是否关闭服务端健康检查
	UpdateInstance(serviceName string, instance *entity.Instance) error
	// CleanInstances 批量清理服务实例，includeHealthy控制是否连同健康实例一起删除
	CleanInstances(serviceName string, includeHealthy bool) error
	// ListInstances 从SDK侧列出实例（走nacos sdk缓存，可用/全量可选，支持过滤）
	ListInstances(serviceName string, onlyAvailable bool, filter *entity.InstanceFilter) (entity.InstanceList, error)
	// ListInstancesFromWeb 从web端列出实例（web与SDK视图不同，管理操作场景使用）
	ListInstancesFromWeb(service string, onlyAvailable bool, filter *entity.InstanceFilter) (entity.InstanceList, error)
	// GetServiceInfo 从本地缓存读取服务元数据，缓存未命中返回ErrServiceNotFound
	GetServiceInfo(serviceName string) (*entity.MicroService, error)
	// Init 全量初始化：加载全部服务到缓存，并对每个服务执行一次callback（增量驱动由此建立基线）
	Init(callback entity.WatchCallback) error
	// Watch 增量监听：注册变更回调并启动worker消费队列，只处理后续变动
	Watch(callback entity.WatchCallback) error
	// Enqueue 将服务变更事件入队，broadcast=true表示自身变化需级联通知上游，false表示被动跟新
	Enqueue(broadcast bool, services ...string)
	// ListAllMicroServices 列出缓存中的全部服务元数据
	ListAllMicroServices() ([]entity.MicroService, error)
}
type service struct {
	// storageRepo 底层Nacos存储仓库
	storageRepo repostorage.StorageRepository

	// serviceCache 服务元数据缓存，key为服务名，由Nacos配置中心变更回调驱动同步
	serviceCache map[string]*entity.MicroService
	// instanceCache 实例列表缓存，key为服务名（当前直连SDK读取，保留此结构以备后续启用）
	instanceCache map[string]entity.InstanceList
	// cacheLock 两级缓存共用的读写锁
	cacheLock sync.RWMutex

	// watchCallback 服务变更回调，由xDS层注册，用于触发配置重新下发
	watchCallback entity.WatchCallback
	// queue 服务变更事件队列，带限速重试能力（指数退避500ms~30min）
	// 原k8s workqueue的全局令牌桶限流（10qps）在自建队列中省略，由退避随机抖动近似替代
	queue *queue.Queue[entity.ServiceEvent]
	// stopCh worker退出信号，close后panic重启循环终止
	stopCh chan struct{}
}

// NewService 构造存储服务并建立Nacos订阅。
// 订阅建立失败时返回error（原实现为logger.Fatal，改由调用方决定进程去留）
func NewService(storageRepo repostorage.StorageRepository) (*service, error) {
	s := &service{
		storageRepo: storageRepo,
		// 队列限速：单条目指数退避（500ms起步、上限30min），叠加随机抖动防止惊群
		// （原全局令牌桶10qps限流由抖动近似替代）
		queue:         queue.New[entity.ServiceEvent](500*time.Millisecond, 30*time.Minute),
		serviceCache:  map[string]*entity.MicroService{},
		instanceCache: map[string]entity.InstanceList{},
		cacheLock:     sync.RWMutex{},
	}
	// 启动服务订阅
	err := storageRepo.InitSubscribe(s.Enqueue, s.SyncInstanceCache, s.SyncServiceInfoCache)
	if err != nil {
		return nil, err
	}

	return s, nil
}

// applyHealthyCheckSetting 按服务配置决定是否为实例关闭服务端健康检查。
// 未配置健康检查设置时不作处理（原实现依赖错误文本"not found"判断，改为按设置类型预检，
// 避免对普通error做字符串匹配；仓库层的ErrServiceNotFound哨兵错误由GetServiceInfo直接返回）
func (s *service) applyHealthyCheckSetting(serviceName string, instance *entity.Instance) error {
	svc, err := s.GetServiceInfo(serviceName)
	if err != nil {
		return err
	}

	// 预检设置项是否存在：Settings.Get未配置时返回普通error（非哨兵），两者分开判断
	var configured bool
	for _, setting := range svc.Settings {
		if setting.Type == entity.HealthyCheckType {
			configured = true
			break
		}
	}
	if !configured {
		// 无相关配置时默认不作处理
		logging.With("service", serviceName).Warnf("no healthyCheck setting, use default")
		return nil
	}

	var hc entity.HealthyCheckSetting
	if err := svc.Settings.Get(entity.HealthyCheckType, &hc); err != nil {
		return err
	}
	// 设置关闭服务端健康检查
	if hc.DisableServerSideHealthyCheck {
		instance.DisableServerSideHealthyCheck = true
	}
	return nil
}

// RegisterInstance 注册实例：先读取服务配置，若服务关闭了服务端健康检查，则为实例打上标记后再落库
func (s *service) RegisterInstance(serviceName string, instance *entity.Instance) error {
	if err := s.applyHealthyCheckSetting(serviceName, instance); err != nil {
		return err
	}
	return s.storageRepo.RegisterInstance(serviceName, instance)
}

// UpdateInstance 更新实例：与注册相同，先按服务配置决定是否关闭服务端健康检查，再写入仓库
func (s *service) UpdateInstance(serviceName string, instance *entity.Instance) error {
	if err := s.applyHealthyCheckSetting(serviceName, instance); err != nil {
		return err
	}
	return s.storageRepo.UpdateInstance(serviceName, instance)
}

// DeregisterInstance 按IP+端口下线实例，直接透传仓库
func (s *service) DeregisterInstance(serviceName, ip string, port int) error {
	return s.storageRepo.DeregisterInstance(serviceName, ip, port)
}

// CleanInstances 批量清理服务实例，includeHealthy控制是否连同健康实例一起删除
func (s *service) CleanInstances(serviceName string, includeHealthy bool) error {
	return s.storageRepo.CleanInstances(serviceName, includeHealthy)
}

// ListInstancesFromWeb 从web端视图列出实例，供管理页面等需要完整数据的场景使用
func (s *service) ListInstancesFromWeb(service string, onlyAvailable bool, filter *entity.InstanceFilter) (entity.InstanceList, error) {
	return s.storageRepo.ListInstancesFromWeb(service, onlyAvailable, filter)
}
