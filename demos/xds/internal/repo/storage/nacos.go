package storage

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nacos-group/nacos-sdk-go/v2/clients"
	"github.com/nacos-group/nacos-sdk-go/v2/clients/config_client"
	"github.com/nacos-group/nacos-sdk-go/v2/clients/naming_client"
	"github.com/nacos-group/nacos-sdk-go/v2/common/constant"
	"github.com/nacos-group/nacos-sdk-go/v2/model"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"

	"github.com/balcony314/xds/internal/entity"
	"github.com/balcony314/xds/internal/errs"
	"github.com/balcony314/xds/pkg/config"
	"github.com/balcony314/xds/pkg/logging"
)

// nacosRepository 基于Nacos的存储实现：
// 服务元数据（MicroService序列化为JSON）存于Nacos配置中心（dataId=服务名，group=配置项nacos.group）；
// 服务实例存于Nacos服务发现（以服务名注册，不同来源实例划入不同cluster逻辑分组）
type nacosRepository struct {
	// namingClient 服务发现客户端，负责实例的注册/查询/订阅
	namingClient naming_client.INamingClient
	// configClient 配置中心客户端，负责服务元数据的读写与监听
	configClient config_client.IConfigClient
	// namespace Nacos命名空间（原硬编码matrix_mesh，现由配置项nacos.namespace驱动）
	namespace string
	// group 服务元数据与实例注册共同使用的Nacos分组，
	// 注册/查询/订阅必须使用同一group，否则订阅收不到实例事件
	group string
	// syncInterval 订阅集合diff轮询周期（新服务补订阅、消失服务退订）
	syncInterval time.Duration
	// subscribed 已建立订阅的服务名集合（naming+config一体：实例订阅与配置监听同时建立/撤销）。
	// 值持有实例订阅建立时的原始param：SDK按回调指针相等移除，退订必须传回原指针，
	// 新建param会导致旧回调残留在SDK内部、重订阅时事件被重复处理
	subscribed map[string]*vo.SubscribeParam
	mu         sync.Mutex
}

// NewNacosRepository 创建Nacos存储仓库，初始化服务发现与配置中心两类客户端；
// 客户端连接失败返回error（由调用方决定生死，不再Fatal）
func NewNacosRepository() (StorageRepository, error) {
	// namespace/group从配置读取，默认xds/default_group
	namespace := config.GetString("nacos.namespace")
	if namespace == "" {
		namespace = "xds"
	}
	group := config.GetString("nacos.group")
	if group == "" {
		group = "default_group"
	}
	syncInterval := config.GetDuration("nacos.syncInterval")
	if syncInterval <= 0 {
		syncInterval = 30 * time.Second
	}

	// 创建clientConfig
	clientConfig := constant.ClientConfig{
		NamespaceId:         namespace,
		TimeoutMs:           5000,
		NotLoadCacheAtStart: true,
		Username:            config.GetString("nacos.username"),
		Password:            config.GetString("nacos.password"),
		LogDir:              config.GetString("nacos.logDir"),
		// 修复原代码bug：CacheDir与LogLevel两个配置键曾互相写反
		CacheDir: config.GetString("nacos.cacheDir"),
		LogLevel: config.GetString("nacos.logLevel"),
	}

	// 至少一个ServerConfig
	serverConfigs := []constant.ServerConfig{
		{
			IpAddr: config.GetString("nacos.address"),
			Port:   config.GetUint64("nacos.port"),
		},
	}

	logging.Debugf("starting with nacos config %s:%d", config.GetString("nacos.address"), config.GetUint64("nacos.port"))

	// 创建服务发现客户端的另一种方式 (推荐)
	namingClient, err := clients.NewNamingClient(
		vo.NacosClientParam{
			ClientConfig:  &clientConfig,
			ServerConfigs: serverConfigs,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("创建nacos naming客户端失败: %w", err)
	}

	// 创建动态配置客户端的另一种方式 (推荐)
	configClient, err := clients.NewConfigClient(
		vo.NacosClientParam{
			ClientConfig:  &clientConfig,
			ServerConfigs: serverConfigs,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("创建nacos config客户端失败: %w", err)
	}

	r := nacosRepository{
		namingClient: namingClient,
		configClient: configClient,
		namespace:    namespace,
		group:        group,
		syncInterval: syncInterval,
		subscribed:   make(map[string]*vo.SubscribeParam),
	}
	return &r, nil
}

// SaveMicroService 保存服务元数据：整体序列化为JSON后发布到Nacos配置中心（dataId=服务名）
func (r *nacosRepository) SaveMicroService(svc *entity.MicroService) error {
	ret, err := json.Marshal(svc)
	if err != nil {
		return err
	}
	configInstance := vo.ConfigParam{
		DataId:  svc.Name,
		Group:   r.group,
		Content: string(ret),
	}
	// 注册配置实例
	success, err := r.configClient.PublishConfig(configInstance)
	if !success {
		err = fmt.Errorf("config publish not success, err: %w", err)
	}
	return err
}

// DeleteMicroService 删除Nacos配置中心中对应服务名的配置
func (r *nacosRepository) DeleteMicroService(serviceName string) error {
	configInstance := vo.ConfigParam{
		DataId: serviceName,
		Group:  r.group,
	}
	// 删除配置实例
	success, err := r.configClient.DeleteConfig(configInstance)
	if !success {
		err = fmt.Errorf("config delete not success, err: %w", err)
	}
	return err
}

// GetMicroService 从Nacos配置中心读取服务元数据并反序列化，配置不存在时返回ErrServiceNotFound
func (r *nacosRepository) GetMicroService(serviceName string) (*entity.MicroService, error) {
	configInstance := vo.ConfigParam{
		DataId: serviceName,
		Group:  r.group,
	}
	// 获取配置实例
	data, err := r.configClient.GetConfig(configInstance)
	if err != nil {
		return nil, fmt.Errorf("get config failed: %w", err)
	}

	// nacos获取不存在的config时，会返回空，因此需要特殊判断一下
	if len(data) == 0 {
		return nil, errs.ErrServiceNotFound
	}

	var svc entity.MicroService
	err = json.Unmarshal([]byte(data), &svc)
	if err != nil {
		return nil, fmt.Errorf("unmarshal config failed: %w", err)
	}
	return &svc, nil
}

// ListAllMicroServices 分页搜索配置中心全量配置，逐一反序列化为服务元数据
func (r *nacosRepository) ListAllMicroServices() ([]entity.MicroService, error) {
	pageNo := 1
	pageSize := 100
	result := make([]entity.MicroService, 0)
	searchPage, err := r.listConfig(pageNo, pageSize)
	if err != nil {
		return nil, err
	}
	svcs, err := PageItemsToServices(searchPage.PageItems)
	if err != nil {
		return nil, err
	}
	result = append(result, svcs...)
	for searchPage.TotalCount > pageSize*pageNo {
		pageNo++
		searchPage, err = r.listConfig(pageNo, pageSize)
		if err != nil {
			return nil, err
		}
		svcs, err := PageItemsToServices(searchPage.PageItems)
		if err != nil {
			return nil, err
		}
		result = append(result, svcs...)
	}
	return result, err
}

// listConfig 按页精确搜索配置中心，未按dataId过滤即获取本group全量配置。
// group限定为服务元数据所在分组，避免同一Nacos实例上其他业务的配置被误当作服务元数据
func (r *nacosRepository) listConfig(pageNo int, pageSize int) (*model.ConfigPage, error) {
	// 开源SDK参数类型名为SearchConfigParam（私有版为SearchConfigParm），字段一致
	param := vo.SearchConfigParam{
		Search:   "accurate",
		DataId:   "",
		Group:    r.group,
		PageNo:   pageNo,
		PageSize: pageSize,
	}
	searchPage, err := r.configClient.SearchConfig(param)
	if err != nil {
		return nil, err
	}
	return searchPage, err
}

// register 注册单个实例的底层实现：补全标签、校验合法性后写入Nacos服务发现，
// Nacos没有独立更新接口，更新实例也通过重新注册覆盖实现
func (r *nacosRepository) register(service string, instance *entity.Instance) error {
	//先将部分字段设置到标签里，方便后续list时还原
	instance.EnsureDefaultLabels()

	//提交前先校验，避免出现脏数据
	err := instance.Validate()
	if err != nil {
		return fmt.Errorf("instance validate failed: %w", errs.ErrBadRequest.SetError(err))
	}

	params := vo.RegisterInstanceParam{
		Ip:          instance.IP,
		Port:        uint64(instance.Port),
		ServiceName: service,
		Weight:      100,
		Enable:      true,
		Healthy:     true,
		Ephemeral:   false,
		//不同来源的实例放到不同的逻辑分组里
		ClusterName: instance.GetNacosCluster(),
		//group必须与订阅/查询一致（见nacosRepository.group说明），否则实例事件无法被订阅捕获
		GroupName: r.group,
		Metadata:  instance.Labels,
	}
	// 如果有值，则覆盖默认值
	if instance.Isolate != nil {
		params.Enable = !*instance.Isolate
	}
	// 对于物理机自主上报的实例使用默认值，对于平台同步过来的实例使用请求值
	if instance.Healthy != nil {
		params.Healthy = *instance.Healthy
	}
	success, err := r.namingClient.RegisterInstance(params)
	if err != nil || !success {
		err := fmt.Errorf("instance register failed, status: %v, err: %w", success, err)
		logging.With("success", success, "params", params).Error(err)
		return err
	}

	logging.With("success", success, "params", params).Debugf("instance registered")
	return err
}

// RegisterInstance 注册实例。
// 原私有版会在matrix来源实例首次注册后异步禁用其所在cluster的服务端健康检查
// （updateNacosClusterHealthyCheck）；开源nacos-sdk-go无UpdateClusterMetadata API，
// 且开源Nacos对实例注册自动创建的服务默认不开启服务端健康探测（NONE），
// 健康状态由来源方维护的语义天然成立，故不再需要该补偿逻辑
func (r *nacosRepository) RegisterInstance(service string, instance *entity.Instance) error {
	err := r.register(service, instance)
	if err != nil {
		return fmt.Errorf("register failed: %w", err)
	}
	return nil
}

// UpdateInstance 更新实例，通过重新注册覆盖旧数据实现
func (r *nacosRepository) UpdateInstance(service string, instance *entity.Instance) error {
	// 成功路径显式返回nil：fmt.Errorf的%w包装nil会得到非nil的"update failed: %!w(<nil>)"错误
	if err := r.register(service, instance); err != nil {
		return fmt.Errorf("update failed: %w", err)
	}
	return nil
}

// DeregisterInstance 从Nacos服务发现中下线指定IP+端口的实例
func (r *nacosRepository) DeregisterInstance(service, ip string, port int) error {
	// 注册侧按实例来源划入不同cluster（见register的ClusterName取值），注销必须带同一个
	// cluster：服务端按cluster精确匹配，缺省时按DEFAULT匹配，非DEFAULT cluster的实例会删除失败。
	// 接口无cluster参数，先查询实例注册时实际归属的cluster（与注册侧取值对称）
	clusterName, err := r.lookupClusterName(service, ip, port)
	if err != nil {
		return fmt.Errorf("deregister instance failed, lookup cluster error: %w", err)
	}

	success, err := r.namingClient.DeregisterInstance(vo.DeregisterInstanceParam{
		Ip:          ip,
		Port:        uint64(port),
		ServiceName: service,
		// 与注册侧保持一致：同group、实例实际cluster、持久实例（Ephemeral=false为参数零值）
		Cluster:   clusterName,
		GroupName: r.group,
	})

	entry := logging.With("service", service, "ip", ip, "port", port, "cluster", clusterName, "success", success, "error", err)
	if err != nil || !success {
		err = fmt.Errorf("deregister instance failed, status: %v, err: %w", success, err)
		entry.Error(err)
		return err
	}

	entry.Debugf("deregister instance")
	return err
}

// lookupClusterName 按IP+端口在服务实例列表中定位目标实例，还原其注册时所在的cluster名；
// 未匹配到时返回空串（实例已不在服务端，按缺省cluster继续注销，由服务端报not found兜底）
func (r *nacosRepository) lookupClusterName(service, ip string, port int) (string, error) {
	instances, err := r.namingClient.SelectAllInstances(vo.SelectAllInstancesParam{
		ServiceName: service,
		GroupName:   r.group,
	})
	if err != nil {
		return "", err
	}
	for _, obj := range instances {
		if obj.Ip == ip && obj.Port == uint64(port) {
			return obj.ClusterName, nil
		}
	}
	return "", nil
}

// CleanInstances 批量删除所有实例
// includeHealthy参数可以控制是否清理健康的实例
func (r *nacosRepository) CleanInstances(service string, includeHealthy bool) error {
	// 删除实例时，必须使用web端的数据，否则下线实例等无法被删除
	instances, err := r.ListInstancesFromWeb(service, false, nil)
	if err != nil {
		return err
	}

	logging.Infof("clean instances for service %s", service)
	for _, instance := range instances {
		if !includeHealthy && *instance.Healthy {
			logging.With("ip", instance.IP, "port", instance.Port, "healthy", *instance.Healthy).Infof("skip healthy instance")
			continue
		}
		err = r.DeregisterInstance(service, instance.IP, instance.Port)
		if err != nil {
			return err
		}
		logging.With("ip", instance.IP, "port", instance.Port, "healthy", *instance.Healthy).Infof("instance deregister")
	}

	return nil
}

// ListInstancesFromWeb 为了nacos而特殊存在的函数，因为nacos web端显示的和实际sdk拿到的实例是不同的，sdk拿到的会做修改和过滤。
// 开源SDK无web视图查询API，此处与SDK视图合并为SelectAllInstances；
// 反注册后的短暂缓存滞后可接受
func (r *nacosRepository) ListInstancesFromWeb(service string, onlyAvailable bool, filter *entity.InstanceFilter) (entity.InstanceList, error) {
	instances, err := r.namingClient.SelectAllInstances(vo.SelectAllInstancesParam{
		ServiceName: service,
		GroupName:   r.group,
	})
	if err != nil {
		//TODO: 临时忽略
		logging.Error(fmt.Errorf("request nacos web server error: %w", err))
		return nil, nil
	}
	if filter == nil {
		filter = &entity.InstanceFilter{}
	}

	if onlyAvailable {
		filter.Isolate = boolPtr(false)
		filter.Healthy = boolPtr(true)
	}

	logging.With("service", service, "only_available", onlyAvailable, "filter", filter).Debugf("instances list result from web server: %+v", instances)
	return ToInstance(instances).ApplyFilter(filter), nil
}

// ListInstances 列出实例列表（注意：nacos注册和反注册实例后并不能立刻获取到最新结果，会有延迟）
func (r *nacosRepository) ListInstances(service string, onlyAvailable bool, filter *entity.InstanceFilter) (entity.InstanceList, error) {
	var instances []model.Instance
	var err error
	if onlyAvailable {
		instances, err = r.namingClient.SelectInstances(vo.SelectInstancesParam{
			ServiceName: service,
			GroupName:   r.group,
			HealthyOnly: true,
		})
	} else {
		// SelectAllInstance可以返回全部实例列表,包括healthy=false,enable=false,weight<=0
		instances, err = r.namingClient.SelectAllInstances(vo.SelectAllInstancesParam{
			ServiceName: service,
			GroupName:   r.group,
		})
	}
	//忽略sdk报出的实例为空错误
	if err != nil && !strings.Contains(err.Error(), "instance list is empty") {
		return nil, fmt.Errorf("request nacos sdk error: %w", err)
	}
	logging.With("service", service, "only_available", onlyAvailable, "filter", filter).Debugf("instances list result from sdk: %+v", instances)
	return ToInstance(instances).ApplyFilter(filter), nil
}

// InitSubscribe 建立订阅与发现循环。
//
// 开源nacos-sdk-go没有私有版的全命名空间一次性订阅/全量配置监听能力，
// 替代方案：以配置中心的服务元数据集合为权威来源，
// 对每个服务建立实例订阅（naming.Subscribe）与配置监听（config.ListenConfig），
// 并由后台goroutine定时diff：新服务补订阅、消失服务退订，两者都触发变更事件
func (r *nacosRepository) InitSubscribe(
	enqueue func(broadcast bool, services ...string),
	syncInstance func(service string) error,
	syncServiceInfo func(service string) error,
) error {
	if err := r.syncSubscriptions(enqueue, syncInstance, syncServiceInfo); err != nil {
		return fmt.Errorf("初始订阅建立失败: %w", err)
	}
	go func() {
		ticker := time.NewTicker(r.syncInterval)
		defer ticker.Stop()
		for range ticker.C {
			if err := r.syncSubscriptions(enqueue, syncInstance, syncServiceInfo); err != nil {
				logging.Errorf("同步订阅集合失败: %v", err)
			}
		}
	}()
	return nil
}

// syncSubscriptions 全量拉取服务元数据，diff已订阅集合，补建/退订并触发变更
func (r *nacosRepository) syncSubscriptions(
	enqueue func(broadcast bool, services ...string),
	syncInstance func(service string) error,
	syncServiceInfo func(service string) error,
) error {
	services, err := r.ListAllMicroServices()
	if err != nil {
		return err
	}
	current := make(map[string]struct{}, len(services))
	for _, svc := range services {
		current[svc.Name] = struct{}{}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// 新服务：补建实例订阅 + 配置监听
	for name := range current {
		if _, ok := r.subscribed[name]; ok {
			continue
		}
		svcName := name // 闭包捕获
		// 开源版实例订阅回调参数无serviceKey（私有版第一个参数为group@@service），直接用闭包中的服务名
		subscribeParam := &vo.SubscribeParam{
			ServiceName: svcName,
			GroupName:   r.group,
			SubscribeCallback: func(services []model.Instance, err error) {
				if err != nil {
					logging.With("service", svcName).Errorf("实例订阅回调出错: %v", err)
					return
				}
				logging.With("service", svcName).Debugf("received instance callback event")
				_ = syncInstance(svcName)
				enqueue(true, svcName)
			},
		}
		err := r.namingClient.Subscribe(subscribeParam)
		if err != nil {
			logging.With("service", svcName).Errorf("建立实例订阅失败: %v", err)
			continue
		}
		// 开源SDK的配置监听方法为ListenConfig（私有版为ListenListener），且OnDelete字段
		// 不存在：配置删除同样以OnChange回调（data为空），上层按ErrServiceNotFound清理
		err = r.configClient.ListenConfig(vo.ConfigParam{
			DataId: svcName,
			Group:  r.group,
			OnChange: func(namespace, group, dataId, data string) {
				logging.With("service", dataId).Debugf("received config callback event")
				_ = syncServiceInfo(dataId)
				enqueue(true, dataId)
			},
		})
		if err != nil {
			logging.With("service", svcName).Errorf("建立配置监听失败: %v", err)
			continue
		}
		r.subscribed[svcName] = subscribeParam
		// 新发现的服务立即触发一次配置下发，建立快照基线
		enqueue(true, svcName)
	}

	// 消失的服务：退订并触发变更（让上层清理对应快照）
	for name := range r.subscribed {
		if _, ok := current[name]; ok {
			continue
		}
		// 传回建立订阅时的原param（指针匹配才能命中SDK内部按指针移除的回调），随后清理记录
		_ = r.namingClient.Unsubscribe(r.subscribed[name])
		_ = r.configClient.CancelListenConfig(vo.ConfigParam{DataId: name, Group: r.group})
		delete(r.subscribed, name)
		enqueue(true, name)
	}
	return nil
}
