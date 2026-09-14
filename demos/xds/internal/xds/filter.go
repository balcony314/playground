package xds

import (
	"github.com/balcony314/xds/internal/entity"
	"github.com/balcony314/xds/internal/xds/dubbo_matcher"
	"github.com/balcony314/xds/internal/xds/thrift_matcher"
	listener "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	"github.com/envoyproxy/go-control-plane/pkg/wellknown"
	"google.golang.org/protobuf/types/known/anypb"
)

// createProxyFilterChain 为proxy模式的outbound listener按目标服务协议生成filter chain列表。
//
// 【filter chain概念】Envoy的listener内可挂多条filter chain，每条带匹配条件
// （应用协议、端口等），连接按条件选中一条，由其中的network filter依次处理；
// 同一个outbound端口可能承载多种协议的流量，因此按协议给出多条chain备选。
// 调用方在listener.go：把返回的chain列表填进下游服务的outbound listener。
//
// 组合方式：
//   - http/grpc（default）：HTTP filter chain（HCM）+ 兜底TCP filter chain
//   - dubbo：dubbo_proxy filter chain（路由规则内联在DubboProxy的RouteConfig中）+ HTTP filter chain
//   - thrift：thrift_proxy filter chain（路由内联在ThriftProxy的RouteConfig中）+ HTTP filter chain
//
// httpConf/tcpConf分别为预序列化的HCM与tcp_proxy配置（Any类型，直接嵌入TypedConfig）
func createProxyFilterChain(me, s *entity.MicroService, httpConf, tcpConf *anypb.Any) []*listener.FilterChain {
	// HTTP filter chain：按嗅探到的应用协议（http/1.x、h2c）匹配。
	// HCM（HTTP Connection Manager）是Envoy七层流量的核心network filter，
	// 负责HTTP编解码、经RDS做路由决策，并串起HTTP级过滤器（访问日志/限流等）
	var httpFilterChain = &listener.FilterChain{
		FilterChainMatch: &listener.FilterChainMatch{
			ApplicationProtocols: []string{
				"http/1.0",
				"http/1.1",
				"h2c",
			},
		},
		Filters: []*listener.Filter{
			{
				Name: wellknown.HTTPConnectionManager,
				ConfigType: &listener.Filter_TypedConfig{
					TypedConfig: httpConf,
				},
			},
		}}
	// TCP filter chain：兜底四层转发（tcp_proxy不解析应用层协议），
	// 直接转发到目标服务的默认cluster，协议嗅探不出HTTP时走这条链
	var tcpFilterChain = &listener.FilterChain{
		Filters: []*listener.Filter{
			{
				Name: wellknown.TCPProxy,
				ConfigType: &listener.Filter_TypedConfig{
					TypedConfig: tcpConf,
				},
			},
		}}

	//按下游服务协议决定filter chain组合
	switch s.Protocol {
	case "dubbo":
		// dubbo走专用协议filter：dubbo_proxy是TCP之上的应用层协议代理，自带
		// RouteConfig（方法/attachment匹配→加权cluster，由dubbo_matcher包生成，
		// 概念与HTTP路由同构，只是路由规则内联在filter配置里而不走RDS）
		return []*listener.FilterChain{
			{
				Filters: []*listener.Filter{
					{
						Name:       "envoy.filters.network.dubbo_proxy",
						ConfigType: &listener.Filter_TypedConfig{TypedConfig: dubbo_matcher.GenerateDubboFilter(me, s)},
					},
				},
			},
			//必须要加，不加就会报错，router数量数目不对检查失败
			httpFilterChain,
		}
	case "thrift":
		// 同dubbo：thrift_proxy自带RouteConfig（service/method/header匹配，
		// 由thrift_matcher包生成，路由不走RDS）
		return []*listener.FilterChain{
			{
				Filters: []*listener.Filter{
					{
						Name:       "envoy.filters.network.thrift_proxy",
						ConfigType: &listener.Filter_TypedConfig{TypedConfig: thrift_matcher.GenerateThriftFilter(s)},
					},
				},
			},
			httpFilterChain,
		}
	default:
		// http/grpc及其他协议：HTTP chain优先，协议嗅探不上HTTP的裸TCP流量
		// 由tcp chain兜底四层转发
		return []*listener.FilterChain{httpFilterChain, tcpFilterChain}
	}
}
