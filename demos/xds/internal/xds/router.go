package xds

import (
	"fmt"
	"regexp"
	"time"

	"github.com/balcony314/xds/internal/entity"
	"github.com/balcony314/xds/internal/xds/http_matcher"
	"github.com/balcony314/xds/internal/xds/utils"
	"github.com/balcony314/xds/pkg/logging"
	core "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	route "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// MatrixMeshServiceHeader 跨服务传递调用方服务名的header，用于入站限流等场景识别调用者。
//
// 工作闭环：本文件在出站RouteConfiguration上配置RequestHeadersToAdd，把me.Name
// （调用方服务名）注入该header发给下游；下游sidecar的入站限流路由再按该header
// 匹配（见listener.go的限流route），从而按调用方区分限流。注入时Append=false为
// 覆盖模式，应用即使自己设置了这个header也会被sidecar改写——出站身份由mesh背书，
// 防止应用伪造自己的调用身份
const (
	MatrixMeshServiceHeader = "x-matrix-mesh-service"
)

// CreateRoute 生成全部RDS路由配置资源（打包进快照的RouteType部分），
// 为me可访问的每个下游服务生成一份RouteConfiguration（命名 service:port）。
// RouteConfiguration是Envoy HTTP路由的顶层结构，内部两级：
// VirtualHost（按host分组的路由集合）→ Route（RouteMatch匹配条件 + RouteAction转发动作），
// 详见createRoute
func CreateRoute(me *entity.MicroService, microServices []entity.MicroService) []types.Resource {
	var ret []types.Resource
	for _, microService := range microServices {
		ret = append(ret, createRoute(*me, microService))
	}
	return ret
}

// createRoute 为单个下游服务生成一份RDS RouteConfiguration
// 仅http/grpc协议服务生成路由规则（dubbo/thrift的路由内联在各自的network filter中）
// 规则路由之后始终追加一条默认路由（前缀/兜底到该服务的默认cluster）
// 并在请求/响应header中注入x-matrix-mesh-service=当前服务名，供下游识别调用方。
// 结构上：单个VirtualHost（Domains="*"匹配任意Host头）装下全部Route，
// Route = RouteMatch（怎么匹配请求）+ RouteAction（匹配后发往哪个cluster），
// RouteAction的ClusterSpecifier写的cluster名与CDS下发的Cluster按名字关联
func createRoute(me, microService entity.MicroService) types.Resource {
	var routes []*route.Route

	//仅http/grpc协议走RDS；dubbo/thrift的路由配置在对应协议filter内生成
	if microService.Protocol == "http" || microService.Protocol == "grpc" {
		for _, router := range microService.Routers {
			if r := generateRoutes(me, microService, router); r != nil {
				routes = append(routes, r)
			}
		}
	}

	// 默认路由：前缀/匹配一切请求，规则路由都不命中时兜底转发到该服务的默认cluster
	// （group传空串，即包含全部实例、不做实例组细分的cluster）
	routes = append(routes, &route.Route{
		Match: &route.RouteMatch{
			// PathSpecifier是RouteMatch的必填项，前缀/即匹配任意路径
			PathSpecifier: &route.RouteMatch_Prefix{
				Prefix: "/",
			},
		},
		Action: &route.Route_Route{
			Route: &route.RouteAction{
				// 转发目标：cluster名（outbound|port|group|service格式），
				// CDS/EDS按同一个名字提供该cluster及其实例
				ClusterSpecifier: &route.RouteAction_Cluster{
					Cluster: utils.GetOutboundTrafficClusterName(microService.Name, microService.Port, ""),
				},
				// 请求超时显式置0表示不限时：Envoy路由默认15s超时会截断长请求，
				// 未配置超时的服务需要覆盖掉这个默认值
				Timeout: &durationpb.Duration{
					Seconds: 0,
					Nanos:   0,
				},
				// 流式请求（grpc双向流、长连接推送等）的最大流时长，同样置0不限
				MaxStreamDuration: &route.RouteAction_MaxStreamDuration{
					MaxStreamDuration: &durationpb.Duration{
						Seconds: 0,
						Nanos:   0,
					},
				},
				// 会话保持：下游服务配置了ring hash LB时按指定header做一致性哈希
				HashPolicy: getHashPolicy(microService),
			},
		},
	})

	// Name为 service:port（GetRouteName）。名字引用链：listener的HCM通过
	// Rds.route_config_name引用该名字找到这份配置，route里的cluster名指向CDS的
	// Cluster，Cluster再由EDS提供实例列表——任何一环名字对不上Envoy都会NACK
	return &route.RouteConfiguration{
		Name: utils.GetRouteName(microService.Name, microService.Port),
		VirtualHosts: []*route.VirtualHost{
			{
				Name: microService.Name,
				// Domains="*"匹配任意Host头：一份RouteConfiguration只服务一个下游
				// 服务，无需再按域名分流
				Domains: []string{"*"},
				Routes:  routes,
			},
		},
		// 出站请求注入调用方服务名，Append=false为覆盖模式：应用即便自己设置了
		// 该header也会被sidecar改写，出站身份由mesh背书，防止伪造调用身份
		RequestHeadersToAdd: []*core.HeaderValueOption{
			{
				Header: &core.HeaderValue{
					Key:   MatrixMeshServiceHeader,
					Value: me.Name,
				},
				Append: wrapperspb.Bool(false),
			},
		},
		// 响应方向同理注入（响应回流时带上的仍是me.Name），供链路上识别调用方
		ResponseHeadersToAdd: []*core.HeaderValueOption{
			{
				Header: &core.HeaderValue{
					Key:   MatrixMeshServiceHeader,
					Value: me.Name,
				},
				Append: wrapperspb.Bool(false),
			},
		},
	}
}

// generateMatchConfig 将路由/限流规则的MatchConfig.Selectors转为HTTP RouteMatch
// selector按Type分发：path→路径匹配、http-header→header匹配、parameter→query参数匹配
// 仅配header/parameter时补默认前缀/的路径匹配（PathSpecifier为Envoy必填项）
// 同一规则内的多个selector叠加进同一个RouteMatch，条件之间为AND关系
func generateMatchConfig(matchConfig entity.MatchConfig) *route.RouteMatch {
	var r route.RouteMatch
	// 逐个selector解析，按类型填入RouteMatch对应字段（具体匹配语法见http_matcher包）
	for _, sc := range matchConfig.Selectors {
		switch sc.Type {
		case "path":
			http_matcher.GenerateHttpPathMatcher(&r, sc)
		case "http-header":
			if r.PathSpecifier == nil {
				r.PathSpecifier = &route.RouteMatch_Prefix{Prefix: "/"}
			}
			r.Headers = append(r.Headers, http_matcher.GenerateHttpHeaderMatcher(sc))
		case "parameter":
			if r.PathSpecifier == nil {
				r.PathSpecifier = &route.RouteMatch_Prefix{Prefix: "/"}
			}
			r.QueryParameters = append(r.QueryParameters, http_matcher.GenerateHttpParamsMatcher(sc))
		}
	}
	return &r
}

// generateRoutes 一条路由规则代表一个routes
// 将实体Router规则转为Envoy route：匹配条件来自MatchConfig（CallerService为正则，需匹配当前服务名才生效），
// 转发目标为DestInstanceGroups生成的加权cluster（每个实例组一个cluster，按Weight加权），
// 可选携带重试策略（RetryConfig）与超时（TimeoutConfig），超时禁用时显式置0表示不限时
func generateRoutes(me, downstream entity.MicroService, routerConfig entity.Router) *route.Route {
	var totalWeight int

	// 按DestInstanceGroups生成加权cluster列表：每个目标实例组对应一个outbound cluster，权重累加得到TotalWeight
	generateWeightedCluster := func() []*route.WeightedCluster_ClusterWeight {
		var wdc []*route.WeightedCluster_ClusterWeight
		for _, cluster := range routerConfig.DestInstanceGroups {
			totalWeight += cluster.Weight
			name := utils.GetOutboundTrafficClusterName(downstream.Name, downstream.Port, cluster.InstanceGroupName)
			wdc = append(wdc, &route.WeightedCluster_ClusterWeight{
				Name: name, Weight: wrapperspb.UInt32(uint32(cluster.Weight))})
		}
		return wdc
	}

	// 重试策略：未启用、退避区间非法或重试次数<=0时不生成（防止Envoy配置不完整）
	generateRetryPolicy := func() *route.RetryPolicy {
		if !routerConfig.RetryConfig.Enable || routerConfig.RetryConfig.BaseInterval > routerConfig.RetryConfig.MaxInterval || routerConfig.RetryConfig.NumRetries <= 0 {
			return nil
		}
		return &route.RetryPolicy{
			RetryOn:    routerConfig.RetryConfig.RetryOn,
			NumRetries: wrapperspb.UInt32(utils.GetOrDefault(routerConfig.RetryConfig.NumRetries, 1)),
			RetryBackOff: &route.RetryPolicy_RetryBackOff{
				BaseInterval: durationpb.New(time.Duration(routerConfig.RetryConfig.BaseInterval) * time.Millisecond),
				MaxInterval:  durationpb.New(time.Duration(routerConfig.RetryConfig.MaxInterval) * time.Millisecond),
			},
		}
	}

	// 请求超时：未启用或<=0时显式返回0（即不限时），避免Envoy使用默认15s超时截断长请求
	generateRequestTimeout := func() *durationpb.Duration {
		if !routerConfig.TimeoutConfig.Enable || routerConfig.TimeoutConfig.Timeout <= 0 {
			return &durationpb.Duration{
				Seconds: 0,
				Nanos:   0,
			}
		} else {
			return durationpb.New(time.Duration(routerConfig.TimeoutConfig.Timeout) * time.Millisecond)
		}
	}

	// CallerService为调用方服务名（支持正则），仅当当前服务名匹配时该规则才对本服务生效
	ok, err := regexp.MatchString(fmt.Sprintf("^%s$", routerConfig.MatchConfig.CallerService), me.Name)
	if err != nil || !ok {
		return nil
	}
	// 匹配条件或目标实例组为空的规则不生成
	if len(routerConfig.MatchConfig.Selectors) == 0 || len(routerConfig.DestInstanceGroups) == 0 {
		return nil
	}

	return &route.Route{
		// 匹配条件：path/header/参数，由MatchConfig.Selectors转成RouteMatch
		Match: generateMatchConfig(routerConfig.MatchConfig),
		Action: &route.Route_Route{
			Route: &route.RouteAction{
				// 与默认路由的单cluster指定相对，这里按权重把流量分到
				// 多个实例组的cluster（灰度/按组分流）
				ClusterSpecifier: &route.RouteAction_WeightedClusters{
					WeightedClusters: &route.WeightedCluster{
						Clusters:    generateWeightedCluster(),
						TotalWeight: wrapperspb.UInt32(uint32(totalWeight)),
					}},
				// 请求超时：未启用时同样显式置0，避免Envoy默认15s截断长请求
				Timeout: generateRequestTimeout(),
				// 流式请求的最大流时长，与请求超时取同一份配置
				MaxStreamDuration: &route.RouteAction_MaxStreamDuration{
					MaxStreamDuration: generateRequestTimeout(),
				},
				RetryPolicy: generateRetryPolicy(),
				HashPolicy:  getHashPolicy(downstream),
			}},
	}
}

// getHashPolicy 从服务Settings读取LB策略，配置为ring hash时生成基于指定header的HashPolicy。
// HashPolicy让Envoy对每个请求按该header的值计算哈希，配合cluster侧的RING_HASH负载均衡，
// 同一header值的请求始终落在同一后端实例上，实现会话保持；未配置ring hash则返回nil
func getHashPolicy(service entity.MicroService) []*route.RouteAction_HashPolicy {
	// 从服务Settings列表中找LbPolicy类型的配置项解析出来
	var lbPolicy entity.LbPolicy
	for _, s := range service.Settings {
		if s.Type == entity.LbPolicyType {
			err := s.GetProperties(&lbPolicy)
			if err != nil {
				logging.Error(err)
				return nil
			}
			break
		}
	}
	if lbPolicy.Policy == entity.RingHash {
		hashPolicy := []*route.RouteAction_HashPolicy{
			{
				PolicySpecifier: &route.RouteAction_HashPolicy_Header_{
					Header: &route.RouteAction_HashPolicy_Header{
						HeaderName: lbPolicy.HeaderKey,
					},
				},
			},
		}
		return hashPolicy
	}
	return nil
}
