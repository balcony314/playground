package dubbo_matcher

import (
	"fmt"
	"regexp"

	"github.com/balcony314/xds/internal/entity"
	"github.com/balcony314/xds/internal/xds/utils"
	"github.com/balcony314/xds/pkg/logging"
	route "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	dubbo_proxy "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/dubbo_proxy/v3"
	matcher "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// GenerateDubboFilter 生成dubbo服务的envoy.filters.network.dubbo_proxy网络过滤器配置。
//
// 【背景】dubbo是TCP之上的RPC协议，不走HTTP那套RDS路由：Envoy在listener上挂
// 此网络过滤器解析dubbo帧，再做应用层路由。它自带的RouteConfiguration概念上
// 与HTTP RDS同构（按方法/attachment匹配、转发到cluster），但proto类型独立，
// 且本项目中路由规则内联在过滤器配置里一起下发，不走RDS发现。
//
// 固定选项：协议Dubbo、序列化Hessian2（dubbo生态主流组合）；DubboFilters挂
// envoy.filters.dubbo.router（终端过滤器，实际执行路由转发动作）。
// me为当前服务（调用方），s为下游dubbo服务；序列化失败时返回nil（调用方跳过该filter）
func GenerateDubboFilter(me, s *entity.MicroService) *anypb.Any {
	dubboProxy := &dubbo_proxy.DubboProxy{
		StatPrefix:        "dubbo_", // 指标统计前缀，metrics按dubbo_前缀聚合
		ProtocolType:      dubbo_proxy.ProtocolType_Dubbo,
		SerializationType: dubbo_proxy.SerializationType_Hessian2,
		// envoy 1.23.0以上的版本才支持RouteSpecifier
		//RouteSpecifier: generateMultiRouters(me, s),
		// 路由规则内联下发（单RouteConfig形态，兼容低版本envoy）
		RouteConfig: generateRouters(me, s),
		DubboFilters: []*dubbo_proxy.DubboFilter{
			{
				Name: "envoy.filters.dubbo.router",
			},
		},
	}

	ret, err := anypb.New(dubboProxy)
	if err != nil {
		logging.Error("anypb.New(dubboProxy) error ", err)
		return nil
	}
	return ret
}

// generateMultiRouters 将路由配置包装为MultipleRouteConfiguration形式（当前未启用，
// 因envoy 1.23.0以下版本不支持RouteSpecifier，统一使用单RouteConfig方式）
func generateMultiRouters(me, s *entity.MicroService) *dubbo_proxy.DubboProxy_MultipleRouteConfig {
	ret := dubbo_proxy.DubboProxy_MultipleRouteConfig{
		MultipleRouteConfig: &dubbo_proxy.MultipleRouteConfiguration{
			Name:        fmt.Sprintf("outbind-%s-router", s.Name),
			RouteConfig: generateRouters(me, s),
		},
	}
	return &ret
}

// generateRouters 由下游服务的Routers规则生成dubbo RouteConfiguration列表。
//
// 每条规则以MatchConfig.CallerService做调用方过滤：将其作为正则并用^$锚定后
// 匹配当前服务名（整串匹配），未命中或正则非法的规则直接跳过——同一份路由规则
// 可以描述给不同调用方，各自生成配置时只取属于自己的子集。
// 末尾追加defaultRouter兜底，业务规则全不命中时流量仍能到默认cluster
func generateRouters(me, s *entity.MicroService) []*dubbo_proxy.RouteConfiguration {
	ret := make([]*dubbo_proxy.RouteConfiguration, 0)
	for _, router := range s.Routers {
		// CallerService按正则整串（^...$）匹配当前服务名，限定"这条规则发给谁"
		ok, err := regexp.MatchString(fmt.Sprintf("^%s$", router.MatchConfig.CallerService), me.Name)
		if err != nil || !ok {
			continue
		}
		if r := createRouter(s, router); r != nil {
			ret = append(ret, r)
		}
	}
	ret = append(ret, defaultRouter(s.Name, s.Port))
	return ret
}

// defaultRouter 生成dubbo默认路由：interface=*匹配所有接口、方法名正则.*匹配所有方法，
// 转发到服务的默认cluster（outbound|port||service，实例组名为空即默认组）。
// dubbo以interface/group/version三元组标识服务，Envoy先在RouteConfiguration
// 级别按interface收敛候选，故兜底路由必须在interface维度通配才能接住所有流量
func defaultRouter(serviceName string, servicePort int) *dubbo_proxy.RouteConfiguration {
	return &dubbo_proxy.RouteConfiguration{
		Name: fmt.Sprintf("outbind-%s-default-router", serviceName),
		// To make this work, Dubbo Interface should have been registered to the Istio service registry as a service
		Interface: "*",
		Routes: []*dubbo_proxy.Route{
			{
				Match: &dubbo_proxy.RouteMatch{
					Method: &dubbo_proxy.MethodMatch{
						Name: &matcher.StringMatcher{
							MatchPattern: &matcher.StringMatcher_SafeRegex{
								SafeRegex: &matcher.RegexMatcher{
									EngineType: &matcher.RegexMatcher_GoogleRe2{GoogleRe2: &matcher.RegexMatcher_GoogleRE2{}},
									Regex:      ".*",
								},
							},
						},
					},
				},
				Route: &dubbo_proxy.RouteAction{
					ClusterSpecifier: &dubbo_proxy.RouteAction_Cluster{
						Cluster: utils.GetOutboundTrafficClusterName(serviceName, servicePort, ""),
					},
				},
			}},
	}
}

// createRouter 将单条路由规则转为dubbo RouteConfiguration。
//
// 转发目标：DestInstanceGroups生成加权cluster（每个实例组一个outbound cluster，
// 按Weight加权，复用HTTP的route.WeightedCluster类型）。
// 匹配条件按selector.Type分流：
//   - interface/group/version → 写到RouteConfiguration顶层字段
//     （dubbo服务三元组，决定这条RouteConfiguration命中哪个服务声明）
//   - method-name/parameter/header → 写到Routes[0].Match.Method
//     （方法名/按参数位置/attachment的方法级匹配）
//
// 匹配条件或目标实例组为空时返回nil（该规则被丢弃）
func createRouter(downstream *entity.MicroService, router entity.Router) *dubbo_proxy.RouteConfiguration {
	var totalWeight int
	if len(router.MatchConfig.Selectors) == 0 || len(router.DestInstanceGroups) == 0 {
		return nil
	}

	generateWeightedCluster := func() []*route.WeightedCluster_ClusterWeight {
		var wdc []*route.WeightedCluster_ClusterWeight
		for _, cluster := range router.DestInstanceGroups {
			// 每个目标实例组对应一个outbound cluster（名字带实例组名），权重用于加权转发
			totalWeight += cluster.Weight
			name := utils.GetOutboundTrafficClusterName(downstream.Name, downstream.Port, cluster.InstanceGroupName)
			wdc = append(wdc, &route.WeightedCluster_ClusterWeight{
				Name: name, Weight: wrapperspb.UInt32(uint32(cluster.Weight))})
		}
		return wdc
	}

	// 默认RouteConfiguration：interface/group/version为通配，路由到加权cluster；
	// 后续按selector逐项覆盖interface/group/version及方法级匹配
	config := &dubbo_proxy.RouteConfiguration{
		Name:      router.Name,
		Interface: "*",
		Group:     "",
		Version:   "",
		Routes: []*dubbo_proxy.Route{
			{
				Match: &dubbo_proxy.RouteMatch{
					Method: &dubbo_proxy.MethodMatch{}, //必填字段
				},
				Route: &dubbo_proxy.RouteAction{ClusterSpecifier: &dubbo_proxy.RouteAction_WeightedClusters{
					WeightedClusters: &route.WeightedCluster{
						Clusters:    generateWeightedCluster(),
						TotalWeight: wrapperspb.UInt32(uint32(totalWeight)),
					}}},
			},
		},
	}
	// 按selector类型填充匹配条件：服务三元组（interface/group/version）或方法级（method-name/parameter/header）
	for _, matcher := range router.MatchConfig.Selectors {
		switch matcher.Type {
		case "interface":
			config.Interface = matcher.Value
		case "group":
			config.Group = matcher.Value
		case "version":
			config.Version = matcher.Value
		case "method-name":
			GenerateMethodMatcher(config.Routes[0].Match.Method, matcher)
		case "parameter":
			GenerateParamMatcher(config.Routes[0].Match.Method, matcher)
		case "header":
			// 注意：GenerateHeaderMatcher的append结果不会回写，header条件实际未写入（见其函数头"已知缺陷"）
			GenerateHeaderMatcher(config.Routes[0].Match.Headers, matcher)
		}
	}
	return config
}
