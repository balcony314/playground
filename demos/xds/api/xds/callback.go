package xds

import (
	"context"
	"os"
	"sort"
	"strings"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpoint "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	discovery "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	"github.com/envoyproxy/go-control-plane/pkg/resource/v3"
	"github.com/envoyproxy/go-control-plane/pkg/server/v3"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/balcony314/xds/internal/entity"
	"github.com/balcony314/xds/internal/errs"
	"github.com/balcony314/xds/internal/service"
	"github.com/balcony314/xds/pkg/logging"
)

// MatrixMeshCallback 实现 go-control-plane 的 server.Callbacks 接口。
//
// 【Callbacks 是什么】go-control-plane 在 gRPC 流生命周期的各个时刻留下的钩子:
// 连接建立/关闭、请求到达、响应即将写出等。控制面借此在"库行为"之外插入自定义逻辑。
// 本类型只用到了其中两个——响应下发时刻:
//   - OnStreamResponse:gRPC 流式(Envoy 常规订阅方式)响应写给 Envoy 前
//   - OnFetchResponse :REST gateway(8091,调试用)单次查询返回前
//
// 两者都在 EDS 响应下发前按调用方位置改写 endpoint,实现就近路由
// (同zone优先 -> 同region次之 -> 跨region兜底)。
//
// 【为什么必须在响应时刻改写】快照缓存按"服务名@模式"共享——同一份 EDS 会被
// 该服务的所有上游调用方订阅,生成快照时无从知道"这次是给谁的下发";
// 只有响应时刻才能从 DiscoveryRequest.Node 读到当前订阅者的 locality。
// 效果:同一份快照,不同调用方收到不同视图,且不需要按调用方数量放大快照。
type MatrixMeshCallback struct{}

// 编译期断言:确保 MatrixMeshCallback 完整实现 go-control-plane 的 Callbacks 接口
// (rest + sotw + delta 三组回调,漏实现任意一个方法都会在此暴露)
var _ server.Callbacks = (*MatrixMeshCallback)(nil)

// OnStreamResponse gRPC流式响应下发前的回调，按需改写EDS中的locality优先级
func (cb *MatrixMeshCallback) OnStreamResponse(ctx context.Context, streamId int64, request *discovery.DiscoveryRequest, response *discovery.DiscoveryResponse) {
	cb.MutateLocality(request, response)
}

// OnFetchResponse REST gateway单次fetch响应下发前的回调，处理逻辑与流式一致
func (cb *MatrixMeshCallback) OnFetchResponse(request *discovery.DiscoveryRequest, response *discovery.DiscoveryResponse) {
	cb.MutateLocality(request, response)
}

// MutateLocality 按请求方所在region/zone改写EDS响应，实现按调用方位置的就近路由。
//
// 一个EDS响应的Resources里是若干个ClusterLoadAssignment(每个对应一个cluster),
// 逐个反序列化->按需改写->重新序列化写回。改写规则由目标服务(被调用方)的配置决定:
//  1. 若目标服务开启了nearest：按"同region同zone=0，同region跨zone=1，跨region=2"重排endpoint优先级，
//     并将原始层级值压缩为从0开始的连续值（Envoy要求priority连续）
//  2. 若目标服务配置了fallback：Region表示只保留同region的endpoint，Zone表示只保留同zone的endpoint
func (cb *MatrixMeshCallback) MutateLocality(request *discovery.DiscoveryRequest, response *discovery.DiscoveryResponse) {
	//DiscoveryRequest.Node携带订阅方(Envoy/proxyless SDK)上报的节点标识与元数据;
	//没有node信息时无法得知调用方位置，不做事
	if request.Node == nil {
		return
	}
	//就近路由只影响EDS（endpoint），其他类型的xDS资源无需处理
	if request.TypeUrl != resource.EndpointType {
		return
	}
	for i := range response.Resources {
		r := response.Resources[i]
		var clusterLoad endpoint.ClusterLoadAssignment
		//Resources是序列化的proto Any,必须先反序列化成具体类型才能修改字段
		err := anypb.UnmarshalTo(r, &clusterLoad, proto.UnmarshalOptions{})
		if err != nil {
			logging.Error(err)
			continue
		}
		//clusterName格式为outbound|port|group|serviceName，据此反查目标服务的就近路由配置
		nearest := getNearestByClusterName(clusterLoad.ClusterName)
		currentRegion, currentZone := getCurrentRegionAndZone(request)
		//开启就近路由时，按调用方位置重算各locality的优先级
		if nearest.Enable {
			setPriorityByNearest(&clusterLoad, currentRegion, currentZone)
		}
		//fallback过滤：兜底策略限制跨区域访问时，直接裁掉不满足条件的endpoint
		if nearest.FallBackType == entity.Region {
			clusterLoad.Endpoints = filterEndpointsByRegion(clusterLoad.Endpoints, currentRegion)
		} else if nearest.FallBackType == entity.Zone {
			clusterLoad.Endpoints = filterEndpointsByZone(clusterLoad.Endpoints, currentZone)
		}
		ret, err := anypb.New(&clusterLoad)
		if err != nil {
			logging.Error(err)
			continue
		}
		//改写后的CLA重新序列化,替换响应中原来的资源
		response.Resources[i] = ret
	}
}

// serviceInfoReader 服务元数据读取函数，默认走全局存储缓存；测试可注入替身。
// 全局存储在单测环境下可能尚未初始化（接口为nil），此时按"查无此服务"处理，
// 与 GetServiceInfo 返回 ErrServiceNotFound 走同一条返回零值配置的路径
var serviceInfoReader = func(serviceName string) (*entity.MicroService, error) {
	if service.Storage() == nil {
		return nil, errs.ErrServiceNotFound
	}
	return service.Storage().GetServiceInfo(serviceName)
}

// SetServiceInfoReader 替换服务元数据读取函数（测试注入用）
func SetServiceInfoReader(f func(serviceName string) (*entity.MicroService, error)) { serviceInfoReader = f }

// getNearestByClusterName 从clusterName解析出目标服务名，并查询该服务的nearest路由配置
// clusterName格式：outbound|port|group|serviceName，第4段为服务名
// (命名约定与internal/xds/utils.GetOutboundTrafficClusterName对应,生成方与解析方必须一致)
func getNearestByClusterName(clusterName string) entity.Nearest {
	var nearest entity.Nearest
	splits := strings.Split(clusterName, "|")
	if len(splits) < 4 {
		return nearest
	}
	serviceName := splits[3]
	//从本地缓存读取服务元数据(由Nacos配置中心订阅维护)
	me, err := serviceInfoReader(serviceName)
	if err != nil {
		//查不到服务(未注册/已注销/存储未初始化)时按零值配置处理：不开启就近路由
		logging.Error(err)
		return nearest
	}
	//从服务的settings列表中找到nearest类型的配置并反序列化
	for _, s := range me.Settings {
		if s.Type == entity.NearestType {
			err := s.GetProperties(&nearest)
			if err != nil {
				logging.Error(err)
			}
			break
		}
	}
	return nearest
}

// getCurrentRegionAndZone 获取请求方（调用方服务）所在的region和zone
// 优先从Envoy node的locality元数据读取，缺失时降级用本进程环境变量兜底
// (降级链路意味着:sidecar未上报locality且控制面未配环境变量时,就近路由退化为全局负载均衡)
func getCurrentRegionAndZone(request *discovery.DiscoveryRequest) (string, string) {
	var region, zone string
	locality := request.Node.Locality
	if locality != nil {
		region = request.Node.Locality.Region
		zone = request.Node.Locality.Zone
	} else {
		region = os.Getenv("REGION")
		zone = os.Getenv("ZONE")
	}
	return region, zone
}

// setPriorityByNearest 按调用方位置重算cluster下各locality的优先级，实现就近路由：
// 同region同zone优先级最高（0），同region跨zone次之（1），跨region最低（2）
// Envoy按priority从小到大访问endpoint，高优先级全部不可用时才failover到低优先级
// ——即"就近"不是硬约束,同zone实例全挂时会自动降级到跨zone/跨region实例
func setPriorityByNearest(clusterLoad *endpoint.ClusterLoadAssignment, region string, zone string) {
	//priorityMap记录每个层级值对应的endpoint下标列表
	//(endpoints按locality分组,一组一个priority,组内实例共享该值)
	priorityMap := map[int][]int{}
	for i, localityEndpoint := range clusterLoad.Endpoints {
		p := getPriority(localityEndpoint.Locality.Region, localityEndpoint.Locality.Zone, region, zone)
		priorityMap[p] = append(priorityMap[p], i)
	}
	var priorities []int
	for priority := range priorityMap {
		priorities = append(priorities, priority)
	}
	sort.Ints(priorities)
	//Envoy要求priority值从0开始且连续，因此按排序后的层级顺序重新编号，
	//例如原始层级为{1,2}时会重写为{0,1}，层级间的相对顺序保持不变
	for i, priority := range priorities {
		for _, index := range priorityMap[priority] {
			clusterLoad.Endpoints[index].Priority = uint32(i)
		}
	}
}

// getPriority 计算某个endpoint相对于调用方的就近层级：
// 同region同zone返回0（最近），同region跨zone返回1，跨region返回2（最远）
func getPriority(region string, zone string, currentRegion string, currentZone string) int {
	if region == currentRegion {
		if zone == currentZone {
			return 0
		}
		return 1
	}
	return 2
}

// filterEndpointsByRegion 只保留与调用方同region的endpoint，用于Region级别的fallback兜底
// 该过滤是不可降级的：跨region的endpoint被直接裁掉，不会在故障时回源访问
func filterEndpointsByRegion(endpoints []*endpoint.LocalityLbEndpoints, region string) []*endpoint.LocalityLbEndpoints {
	var newEndpoints []*endpoint.LocalityLbEndpoints
	for _, ep := range endpoints {
		if ep.Locality.Region == region {
			newEndpoints = append(newEndpoints, ep)
		}
	}
	return newEndpoints
}

// filterEndpointsByZone 只保留与调用方同zone的endpoint，用于Zone级别的fallback兜底，
// 约束比Region更严格，访问范围被限制在单个可用区内
func filterEndpointsByZone(endpoints []*endpoint.LocalityLbEndpoints, zone string) []*endpoint.LocalityLbEndpoints {
	var newEndpoints []*endpoint.LocalityLbEndpoints
	for _, ep := range endpoints {
		if ep.Locality.Zone == zone {
			newEndpoints = append(newEndpoints, ep)
		}
	}
	return newEndpoints
}

// 以下为go-control-plane要求的回调接口实现，本系统暂不使用，仅保留空实现以满足接口约束

// OnStreamOpen 流式连接建立回调
func (cb *MatrixMeshCallback) OnStreamOpen(_ context.Context, id int64, typ string) error {
	return nil
}

// OnStreamClosed 流式连接关闭回调
func (cb *MatrixMeshCallback) OnStreamClosed(id int64, node *corev3.Node) {
}

// OnDeltaStreamOpen 增量(aggregated delta)流连接建立回调
func (cb *MatrixMeshCallback) OnDeltaStreamOpen(_ context.Context, id int64, typ string) error {
	return nil
}

// OnDeltaStreamClosed 增量流连接关闭回调
func (cb *MatrixMeshCallback) OnDeltaStreamClosed(id int64, node *corev3.Node) {

}

// OnStreamRequest 收到流式请求回调
func (cb *MatrixMeshCallback) OnStreamRequest(int64, *discovery.DiscoveryRequest) error {
	return nil
}

// OnStreamDeltaResponse 增量流响应下发回调（增量协议下暂不处理locality改写）
func (cb *MatrixMeshCallback) OnStreamDeltaResponse(id int64, req *discovery.DeltaDiscoveryRequest, res *discovery.DeltaDiscoveryResponse) {

}

// OnStreamDeltaRequest 收到增量流请求回调
func (cb *MatrixMeshCallback) OnStreamDeltaRequest(id int64, req *discovery.DeltaDiscoveryRequest) error {
	return nil
}

// OnFetchRequest 收到REST fetch请求回调
func (cb *MatrixMeshCallback) OnFetchRequest(_ context.Context, req *discovery.DiscoveryRequest) error {
	return nil
}
