package xds

// 本文件是一组构造Envoy资源示例的辅助/测试代码（endpoint/cluster/virtualHost/listener
// 各一个构造器），演示各类资源的最小配置结构与名字引用关系；当前无生产调用方
// （正式逻辑见同目录endpoint.go/cluster.go/router.go/listener.go），保留作参考

import (
	cluster "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	core "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpoint "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	listener "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	route "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	routefilter "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/router/v3"
	hcm "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	"github.com/envoyproxy/go-control-plane/pkg/wellknown"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// podEndPoint 简化的实例地址信息（IP+端口）
type podEndPoint struct {
	IP   string
	Port uint32
}

// clusterLoadAssignment 由静态IP列表构造EDS ClusterLoadAssignment资源（测试/演示用辅助函数）。
// ClusterLoadAssignment即EDS响应体：ClusterName指明归属的cluster，Endpoints按locality
// （region/zone）分组，每组带priority（故障转移优先级，越小越优先）与LB权重。
// 本演示实现把所有实例放进同一个假locality（region/zone），priority为0，locality权重1000
func clusterLoadAssignment(podEndPoint []podEndPoint, clusterName string) []types.Resource {
	var lbs []*endpoint.LbEndpoint
	for _, p := range podEndPoint {
		hst := &core.Address{
			Address: &core.Address_SocketAddress{
				SocketAddress: &core.SocketAddress{
					Address:  p.IP,
					Protocol: core.SocketAddress_TCP,
					PortSpecifier: &core.SocketAddress_PortValue{
						PortValue: p.Port,
					},
				},
			},
		}
		lbs = append(lbs, &endpoint.LbEndpoint{
			HostIdentifier: &endpoint.LbEndpoint_Endpoint{
				Endpoint: &endpoint.Endpoint{
					Address: hst,
				},
			},
			HealthStatus: core.HealthStatus_HEALTHY,
		})
	}
	return []types.Resource{
		&endpoint.ClusterLoadAssignment{
			ClusterName: clusterName,
			Endpoints: []*endpoint.LocalityLbEndpoints{
				{
					Priority:             0,
					LbEndpoints:          lbs,
					LoadBalancingWeight:  wrapperspb.UInt32(1000),
					Locality: &core.Locality{
						Region: "region",
						Zone:   "zone",
					},
				},
			},
		},
	}
}

// createCluster 生成一个简单的EDS类型cluster（测试/演示用辅助函数）。
// Type=EDS表示实例列表不写死在cluster配置里，而是经xDS（ADS）动态订阅、由控制面推送；
// 轮询负载均衡，MaxRequests=3为并发请求熔断阈值（超过则快速失败，保护上游）
func createCluster(clusterName string) []types.Resource {
	return []types.Resource{
		&cluster.Cluster{
			Name:                 clusterName,
			LbPolicy:             cluster.Cluster_ROUND_ROBIN,
			ClusterDiscoveryType: &cluster.Cluster_Type{Type: cluster.Cluster_EDS},
			EdsClusterConfig: &cluster.Cluster_EdsClusterConfig{
				EdsConfig: &core.ConfigSource{
					ConfigSourceSpecifier: &core.ConfigSource_Ads{},
				},
			},
			CircuitBreakers: &cluster.CircuitBreakers{
				Thresholds: []*cluster.CircuitBreakers_Thresholds{
					{
						MaxRequests: wrapperspb.UInt32(3),
					},
				},
			},
		},
	}
}

// createVirtualHost 构造含固定演示路由的VirtualHost（hello示例服务）。
// VirtualHost是RouteConfiguration内的host分组（Domains命中Host头后进入该组路由）。
// 两条演示路由均为grpc风格路径的精确匹配：
// /hello.Hello/SayHello路由到第一个cluster（失败按"unavailable"条件重试5次），
// /hello.Hello/SayHi路由到第二个cluster
func createVirtualHost(virtualHostName, listenerName string, clusterNames ...string) *route.VirtualHost {
	return &route.VirtualHost{
		Name:    virtualHostName,
		Domains: []string{listenerName},
		Routes: []*route.Route{{
			Match: &route.RouteMatch{
				PathSpecifier: &route.RouteMatch_Path{
					Path: "/hello.Hello/SayHello",
				},
			},
			Action: &route.Route_Route{
				Route: &route.RouteAction{
					ClusterSpecifier: &route.RouteAction_Cluster{
						Cluster: clusterNames[0],
					},
					RetryPolicy: &route.RetryPolicy{
						RetryOn:   "unavailable",
						NumRetries: wrapperspb.UInt32(5),
					},
				},
			}},
			{
				Match: &route.RouteMatch{
					PathSpecifier: &route.RouteMatch_Path{
						Path: "/hello.Hello/SayHi",
					},
				},
				Action: &route.Route_Route{
					Route: &route.RouteAction{
						ClusterSpecifier: &route.RouteAction_Cluster{
							Cluster: clusterNames[1],
						},
					},
				}}},
	}

}

//func createRoute(routeConfigName string, virtualHostName string, listenerName string, clusterNames ...string) []types.Resource {
//	vh := createVirtualHost(virtualHostName, listenerName, clusterNames...)
//	rds := []types.Resource{
//		&route.RouteConfiguration{
//			Name:         routeConfigName,
//			VirtualHosts: []*route.VirtualHost{vh},
//		},
//	}
//	return rds
//}

// createListener 生成监听10000端口的HTTP listener（测试/演示用辅助函数）。
// 核心是组装HCM（HTTP Connection Manager）：路由不静态写在listener里，
// 而是RDS引用指定名字的RouteConfiguration（与正式逻辑同构，见listener.go/router.go）；
// HTTP过滤器链仅挂一个router filter——router必须位于HTTP过滤器链末尾，
// 由它真正执行路由转发。HCM序列化后同时填入ApiListener（proxyless SDK按此读取）
// 与常规FilterChains（Envoy sidecar按此处理连接）
func createListener(listenerName string, routeConfigName string) []types.Resource {
	// RDS引用：HCM只写路由配置名，具体RouteConfiguration由控制面经RDS下发，
	// 两端靠routeConfigName字符串对上
	hcRds := &hcm.HttpConnectionManager_Rds{
		Rds: &hcm.Rds{
			RouteConfigName: routeConfigName,
			ConfigSource: &core.ConfigSource{
				ConfigSourceSpecifier: &core.ConfigSource_Ads{
					Ads: &core.AggregatedConfigSource{},
				},
			},
		},
	}
	// router filter：HTTP过滤器链的必备收尾，前面各filter（限流/日志等）处理完
	// 再由它按路由规则把请求发往上游cluster
	rou := &routefilter.Router{}

	m, err := anypb.New(rou)
	if err != nil {
		panic(m)
	}
	// 组装HCM本体：CodecType=AUTO自动识别http/1.x与h2，路由走上面的RDS引用
	manager := &hcm.HttpConnectionManager{
		CodecType:      hcm.HttpConnectionManager_AUTO,
		RouteSpecifier: hcRds,
		HttpFilters: []*hcm.HttpFilter{
			{
				Name: "router",
				ConfigType: &hcm.HttpFilter_TypedConfig{
					TypedConfig: m,
				},
			},
		},
	}
	pbst, err := anypb.New(manager)
	if err != nil {
		panic(err)
	}
	return []types.Resource{
		&listener.Listener{
			Name: listenerName,
			ApiListener: &listener.ApiListener{
				ApiListener: pbst,
			},
			Address: &core.Address{
				Address: &core.Address_SocketAddress{
					SocketAddress: &core.SocketAddress{
						Protocol: core.SocketAddress_TCP,
						Address:  "0.0.0.0",
						PortSpecifier: &core.SocketAddress_PortValue{
							PortValue: 10000,
						},
					},
				},
			},
			FilterChains: []*listener.FilterChain{{
				Filters: []*listener.Filter{{
					Name: wellknown.HTTPConnectionManager,
					ConfigType: &listener.Filter_TypedConfig{
						TypedConfig: pbst,
					},
				}},
			}},
		}}

}
