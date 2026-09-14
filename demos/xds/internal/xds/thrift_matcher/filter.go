package thrift_matcher

import (
	"fmt"

	"github.com/balcony314/xds/internal/entity"
	"github.com/balcony314/xds/internal/xds/utils"
	thrift_proxy "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/thrift_proxy/v3"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// GenerateThriftFilter 生成thrift服务的envoy.filters.network.thrift_proxy网络过滤器配置。
//
// 【背景】与dubbo_proxy同构：thrift是TCP之上的RPC协议，由listener上的网络
// 过滤器解析thrift帧并做应用层路由，自带的RouteConfiguration概念上与HTTP RDS
// 同构（虚拟主机+路由匹配）但proto类型独立，且本项目中路由规则内联在过滤器
// 配置中下发，不走RDS发现。
// Transport/Protocol均设为AUTO，由Envoy运行时自动探测实际的传输（framed/
// unframed等）与协议（binary/compact等）组合，无需人工指定。
// s为下游thrift服务；序列化失败时返回nil（调用方跳过该filter）
func GenerateThriftFilter(s *entity.MicroService) *anypb.Any {
	thriftProxy := &thrift_proxy.ThriftProxy{
		Transport:      thrift_proxy.TransportType_AUTO_TRANSPORT,
		StatPrefix:     fmt.Sprintf("outbound|%d|%s", s.Port, s.Name),
		Protocol:       thrift_proxy.ProtocolType_AUTO_PROTOCOL,
		ThriftFilters:  []*thrift_proxy.ThriftFilter{},
		RouteConfig:    generateRouters(s),
	}
	ret, err := anypb.New(thriftProxy)
	if err != nil {
		return nil
	}
	return ret
}

// generateRouters 由服务Routers规则生成thrift RouteConfiguration（含全部路由+默认路由）。
// 与dubbo侧不同：这里不按CallerService过滤调用方，该服务配置的路由规则全量生效。
// 末尾追加一条方法名通配（空串匹配任意方法）的默认路由，兜底到服务的默认cluster，
// 避免业务规则全部未命中时请求无路可走
func generateRouters(s *entity.MicroService) *thrift_proxy.RouteConfiguration {
	ret := &thrift_proxy.RouteConfiguration{
		Name: fmt.Sprintf("outbound|%d|%s", s.Port, s.Name),
	}
	for _, router := range s.Routers {
		ret.Routes = append(ret.Routes, createRouter(s, router))
	}
	//默认路由
	ret.Routes = append(ret.Routes, &thrift_proxy.Route{
		Match: &thrift_proxy.RouteMatch{
			MatchSpecifier: &thrift_proxy.RouteMatch_MethodName{
				MethodName: "", // empty string matches any request method name
			},
		},
		Route: &thrift_proxy.RouteAction{
			ClusterSpecifier: &thrift_proxy.RouteAction_Cluster{
				Cluster: utils.GetOutboundTrafficClusterName(s.Name, s.Port, ""),
			},
		},
	})
	return ret
}

// createRouter 将单条路由规则转为thrift Route：转发目标为DestInstanceGroups生成的
// 加权cluster（每个实例组一个outbound cluster，按Weight加权）。
// 注意：当前实现未消费router.MatchConfig——方法名固定空串（匹配任意方法），包内
// 预留的GenerateThriftHeaderMatcher/GeneraThriftMethodMatch/GeneraThriftServiceMatch
// 均未被调用，即thrift路由现阶段只做加权分流，不做按请求特征（方法/头/服务名）的匹配；
// DestInstanceGroups的权重也未参与排序
func createRouter(downstream *entity.MicroService, router entity.Router) *thrift_proxy.Route {
	// 目标实例组转为加权cluster列表，每个实例组对应一个outbound cluster
	generateWeightedCluster := func() []*thrift_proxy.WeightedCluster_ClusterWeight {
		var wdc []*thrift_proxy.WeightedCluster_ClusterWeight
		for _, cluster := range router.DestInstanceGroups {
			name := utils.GetOutboundTrafficClusterName(downstream.Name, downstream.Port, cluster.InstanceGroupName)
			wdc = append(wdc, &thrift_proxy.WeightedCluster_ClusterWeight{
				Name: name, Weight: wrapperspb.UInt32(uint32(cluster.Weight))})
		}
		return wdc
	}
	rou := &thrift_proxy.Route{
		Match: &thrift_proxy.RouteMatch{
			MatchSpecifier: &thrift_proxy.RouteMatch_MethodName{
				MethodName: "", // empty string matches any request method name
			},
		},
		Route: &thrift_proxy.RouteAction{
			ClusterSpecifier: &thrift_proxy.RouteAction_WeightedClusters{
				WeightedClusters: &thrift_proxy.WeightedCluster{
					Clusters: generateWeightedCluster(),
				},
			},
		},
	}
	return rou
}
