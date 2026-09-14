package xds

import (
	"fmt"

	"github.com/balcony314/xds/internal/entity"
	"github.com/balcony314/xds/internal/service"
	"github.com/balcony314/xds/internal/xds/utils"
	"github.com/balcony314/xds/pkg/logging"
	core "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpoint "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// ptr 返回v的指针，替代原项目直接依赖protobuf旧API时的proto.Bool等辅助函数
func ptr[T any](v T) *T { return &v }

// stringMapToInterfaceMap 将map[string]string转为map[string]interface{}，
// 供structpb.NewStruct构造实例标签metadata（替代原pkg/utils的同名函数）
func stringMapToInterfaceMap(obj map[string]string) map[string]interface{} {
	dataMap := map[string]interface{}{}
	for k, v := range obj {
		dataMap[k] = v
	}
	return dataMap
}

// createClusterLoadAssignmentForService 为单个下游服务生成全部EDS ClusterLoadAssignment资源。
//
// 【CLA是什么】ClusterLoadAssignment（缩写CLA）是EDS资源的具体类型，描述"某个cluster
// 有哪些实例"。结构上Endpoints按Locality（region/zone）分组，每组一个Priority
// （Envoy按priority从小到大分层调度，高优先级全不可用才failover到低优先级），
// 组内LbEndpoints是具体的实例IP:Port+健康状态。CLA的ClusterName即CDS侧cluster的
// ServiceName（命名 outbound|port|group|service，与cluster.go的生成一一对应）。
//
// 每个实例组一份CLA（只含该组selector选中的实例），另加一份包含全部健康实例的默认CLA（group为空）。
// me为当前服务，svc为目标下游服务，instances为该下游服务的全部实例
func createClusterLoadAssignmentForService(me, svc *entity.MicroService, instances entity.InstanceList) ([]types.Resource, error) {
	var result []types.Resource

	// 遍历每个实例组，每个实例组一个cluster
	for i := range svc.InstanceGroups {
		group := svc.InstanceGroups[i]
		//根据实例组selector过滤实例
		//仅保留未隔离且健康的实例
		groupInstance := instances.ApplyFilter(&entity.InstanceFilter{
			Isolate:   ptr(false),
			Healthy:   ptr(true),
			Selectors: group.Selector,
		})
		result = append(result, createClusterLoadAssignmentForGroup(me, svc, &group, groupInstance))
	}

	// 为当前服务生成默认实例组，包含所有实例
	// 默认组不做selector过滤（只剔除不健康/已隔离实例），对应默认cluster outbound|port||service，
	// 作为未命中实例组路由规则时的兜底目标
	groupInstance := instances.ApplyFilter(&entity.InstanceFilter{
		Isolate: ptr(false),
		Healthy: ptr(true),
	})
	result = append(result, createClusterLoadAssignmentForGroup(me, svc, nil, groupInstance))

	return result, nil
}

// createClusterLoadAssignmentForGroup 为一个实例组（或默认组）生成EDS ClusterLoadAssignment
// group为nil时生成默认CLA（cluster名 outbound|port||service），否则cluster名为 outbound|port|group|service。
// 实例按region/zone的locality分组后填入不同LocalityLbEndpoints，locality权重等于该组实例数。
//
// 【就近路由的分工】此处只如实标注每个实例的locality，不设置Priority（缺省全为0，
// 即快照层面所有locality同层，按权重加权负载均衡）；"按调用方位置分层failover"
// 的priority改写发生在下发链路末端——api/xds/callback.go在EDS响应写给订阅者前，
// 按调用方region/zone重排priority（同region同zone=0/同region跨zone=1/跨region=2，
// 并压缩为从0连续）。因此生成侧保持与位置无关，同一份快照可服务所有调用方
//
// 注：me参数当前未参与生成逻辑，仅作预留
func createClusterLoadAssignmentForGroup(me, svc *entity.MicroService, group *entity.InstanceGroup, instances entity.InstanceList) *endpoint.ClusterLoadAssignment {
	var clusterLoad *endpoint.ClusterLoadAssignment
	if group != nil {
		// ClusterName即CDS侧cluster的ServiceName，Envoy以此把本CLA匹配到对应cluster
		clusterLoad = &endpoint.ClusterLoadAssignment{
			ClusterName: utils.GetOutboundTrafficClusterName(svc.Name, svc.Port, group.Name),
			Endpoints:   []*endpoint.LocalityLbEndpoints{},
		}
	} else {
		// group为空时，表示生成默认的集群负载
		clusterLoad = &endpoint.ClusterLoadAssignment{
			ClusterName: utils.GetOutboundTrafficClusterName(svc.Name, svc.Port, ""),
			Endpoints:   []*endpoint.LocalityLbEndpoints{},
		}
	}
	//先按region和zone分组，根据分组创建不同的endpoints
	//locality是Envoy failover与就近路由的基本单元：一个LocalityLbEndpoints共享一个priority
	for locality, subGroup := range instances.GroupByLocality() {
		var regionalLBS []*endpoint.LbEndpoint
		for _, i := range subGroup {
			// 实例在注册中心登记的IP:Port，直接作为Envoy的上游地址
			host := &core.Address{
				Address: &core.Address_SocketAddress{
					SocketAddress: &core.SocketAddress{
						Address:  i.IP,
						Protocol: core.SocketAddress_TCP,
						PortSpecifier: &core.SocketAddress_PortValue{
							PortValue: uint32(i.Port),
						},
					},
				},
			}

			// 实例标签转为metadata写入envoy.lb filter metadata，供LB（如ring hash）做一致性哈希
			metadata, err := structpb.NewStruct(stringMapToInterfaceMap(i.Labels))
			if err != nil {
				logging.Error(err)
			}

			regionalLBS = append(regionalLBS, &endpoint.LbEndpoint{
				HostIdentifier: &endpoint.LbEndpoint_Endpoint{
					Endpoint: &endpoint.Endpoint{
						Address:  host,
						Hostname: i.Labels["hostname"],
						// 预留的健康检查地址覆写，留空即用主地址探测
						HealthCheckConfig: &endpoint.Endpoint_HealthCheckConfig{},
					},
				},
				// 实例在进列表前已按Healthy=true过滤过，此处统一标记HEALTHY；
				// 运行期若配置了健康检查/异常摘除，Envoy会动态更新该状态
				HealthStatus: core.HealthStatus_HEALTHY,
				Metadata: &core.Metadata{
					FilterMetadata: map[string]*structpb.Struct{
						"envoy.lb": metadata,
					},
				},
				// 实例级LB权重固定为1（等权），流量倾斜只通过locality级权重体现（见下方）
				LoadBalancingWeight: wrapperspb.UInt32(1),
			})
		}
		// 同一locality的实例聚合为一个LocalityLbEndpoints，locality权重取该组实例数。
		// 配合cluster侧的LocalityWeightedLbConfig（同priority内按locality权重分配流量），
		// 实例级权重全为1、locality权重=实例数，宏观效果等价于全体实例近似均匀
		regionalEndpoints := &endpoint.LocalityLbEndpoints{
			LbEndpoints: regionalLBS,
			Locality: &core.Locality{
				Region: locality.Region,
				Zone:   locality.Zone,
			},
			LoadBalancingWeight: wrapperspb.UInt32(uint32(len(regionalLBS))),
		}
		clusterLoad.Endpoints = append(clusterLoad.Endpoints, regionalEndpoints)
	}
	return clusterLoad
}

// CreateEndpoints 生成当前服务的全部EDS资源
// downstream为me可访问的所有下游服务列表，逐个查询其实例并生成对应ClusterLoadAssignment
// 一个服务的EDS = 它可访问的所有下游服务实例列表的集合。
// 与CreateCluster一一对应：CDS侧生成的每个EDS类型cluster，这里都有同名的CLA供其订阅
func CreateEndpoints(me *entity.MicroService, downstream []entity.MicroService) ([]types.Resource, error) {
	var eds []types.Resource
	//对于某个服务来说，他的负载应该是他所能访问的所有下游服务的集合
	for i := range downstream {
		downstreamService := downstream[i]
		// 从注册中心（Nacos）拉取该下游服务的实例列表：
		// 第二个参数true=只取健康实例，第三个参数nil=不在存储层做额外过滤
		// （健康/隔离/按组过滤统一在下方生成侧进行）
		instances, err := service.Storage().ListInstances(downstreamService.Name, true, nil)
		if err != nil {
			return nil, fmt.Errorf("列出服务 %s 实例失败: %w", downstreamService.Name, err)
		}

		serviceOutbounds, err := createClusterLoadAssignmentForService(me, &downstreamService, instances)
		if err != nil {
			return nil, fmt.Errorf("生成服务 %s 的ClusterLoadAssignment失败: %w", downstreamService.Name, err)
		}
		eds = append(eds, serviceOutbounds...)
	}
	return eds, nil
}
