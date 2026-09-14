package xds

import (
	"strconv"
	"time"

	"github.com/balcony314/xds/internal/entity"
	"github.com/balcony314/xds/internal/xds/utils"
	accesslog "github.com/envoyproxy/go-control-plane/envoy/config/accesslog/v3"
	core "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	listener "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	route "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	trace "github.com/envoyproxy/go-control-plane/envoy/config/trace/v3"
	local_ratelimit "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/local_ratelimit/v3"
	routefilter "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/router/v3"
	http_inspector "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/listener/http_inspector/v3"
	od "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/listener/original_dst/v3"
	hcm "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	tcp "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/tcp_proxy/v3"
	tracing "github.com/envoyproxy/go-control-plane/envoy/type/tracing/v3"
	envoy_type_v3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	"github.com/envoyproxy/go-control-plane/pkg/wellknown"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// CreateListeners 生成服务Sidecar的全部LDS listener资源
// me为当前服务，microServices为me可访问的所有下游服务
// 两种模式：
//   - proxyless（无Sidecar，业务进程直连控制面）：每个下游服务一个HCM listener + 一个virtualInbound（承接本地限流）
//   - proxy（Sidecar模式）：virtualOutbound(15001)/virtualInbound(15006)/AdminPortal(15020)三个基础listener，
//     外加每个下游服务一个outbound listener（不绑定端口，接收virtualOutbound按原始目的地址转来的流量）
//
// 【Envoy Listener 模型】一个Listener = 监听地址(SocketAddress:Port) + FilterChain列表
// （按目的端口/应用协议等match条件择一命中）+ 每条链内的NetworkFilter链（HTTP流量由
// HCM(envoy.filters.network.http_connection_manager)处理，四层流量由tcp_proxy处理）。
// 理解本文件的关键是分清两种形态：proxy模式下流量真的流经Envoy进程；proxyless模式下
// 没有Envoy进程，这些listener只是SDK（如gRPC xDS客户端）消费的逻辑配置。
func CreateListeners(me *entity.MicroService, microServices []entity.MicroService) ([]types.Resource, error) {
	var result []types.Resource
	if me.Mode == entity.ServiceModeProxyless {
		//proxyless模式：每个下游服务生成一个独立listener（ApiListener形态），
		//由业务进程内置的xDS客户端拉取后在SDK内部实现寻址路由，Envoy不实际转发流量
		for _, svc := range microServices {
			l, err := createNormalProxylessListeners(&svc)
			if err != nil {
				return nil, err
			}
			result = append(result, l)
		}
		//限流需要用
		//proxyless没有统一的入站拦截点，单独生成一个绑定业务端口的virtualInbound承接本地限流
		virutalInbound, err := createProxylessVirtualInboundListeners(me)
		if err != nil {
			return nil, err
		}
		result = append(result, virutalInbound)
		//proxyless模式不需要其他配置
		return result, nil
	}

	//proxy模式
	//三个基础listener：出站流量入口、入站流量入口、admin暴露口
	virtualOutbound, err := createVirtualOutBoundListener()
	if err != nil {
		return nil, err
	}
	virutalInbound, err := createProxyVirtualInboundListeners(me)
	if err != nil {
		return nil, err
	}
	admin, err := createAdminListener()
	if err != nil {
		return nil, err
	}
	result = append(result, virtualOutbound, virutalInbound, admin)

	//每个下游服务一个outbound listener，实际流量由virtualOutbound按原始目的地址转发进来
	for _, svc := range microServices {
		l, err := createNormalProxyListeners(me, &svc)
		if err != nil {
			return nil, err
		}
		result = append(result, l)
	}
	return result, nil
}

// createNormalProxylessListeners 为proxyless模式生成单个下游服务的outbound listener
// 纯HTTP listener（ApiListener方式挂HCM），路由通过RDS动态获取（RouteConfigName为 service:port）
// 监听地址为该服务的VIP+Port，BindToPort=false表示不真正绑定端口
//
// 【ApiListener形态】proxyless下没有Envoy进程，消费这份配置的是业务进程里的xDS客户端
// （如gRPC xDS）：它只认listener的ApiListener字段（一份序列化的HCM），不支持filter chain
// match等能力。因此同一份HCM序列化同时放在ApiListener与FilterChains两处——前者供SDK读取，
// 后者保持标准Listener结构（部分客户端实现读filter chain）。
func createNormalProxylessListeners(s *entity.MicroService) (*listener.Listener, error) {
	// router HTTP过滤器：负责按路由规则把请求发往上游cluster。
	// Envoy要求HTTP过滤器链必须以router收尾，否则配置校验不通过；空配置即全默认行为
	rou := &routefilter.Router{}

	// 过滤器配置统一以Any(TypedConfig)形式内嵌，先序列化
	m, err := anypb.New(rou)
	if err != nil {
		panic(m)
	}
	// HCM：HTTP流量的总管，在TCP连接之上解析HTTP语义，串起路由/限流/追踪/访问日志
	manager := &hcm.HttpConnectionManager{
		// AUTO：自动嗅探流量是http/1.x还是h2，无需按协议拆分配置
		CodecType: hcm.HttpConnectionManager_AUTO,
		// 统计指标命名前缀（Envoy暴露给/stats/prometheus的指标以此为命名空间）
		StatPrefix: utils.GetRouteName(s.Name, s.Port),
		// 路由来源选RDS形态：不把RouteConfiguration内联在这里，
		// 而是按名字(service:port)引用CreateRoute单独生成下发的RDS资源
		RouteSpecifier: &hcm.HttpConnectionManager_Rds{
			Rds: &hcm.Rds{
				ConfigSource: &core.ConfigSource{
					// 走ADS：四类资源从同一个gRPC流按序获取，保证配置相互一致
					ConfigSourceSpecifier: &core.ConfigSource_Ads{
						Ads: &core.AggregatedConfigSource{},
					},
					InitialFetchTimeout: &durationpb.Duration{Seconds: 0},
					ResourceApiVersion:  core.ApiVersion_V3,
				},
				// 必须与RDS资源名一致，listener与route靠这个名字关联
				RouteConfigName: utils.GetRouteName(s.Name, s.Port),
			},
		},
		// HTTP过滤器链只挂router：proxyless出站不做限流/追踪
		HttpFilters: []*hcm.HttpFilter{
			{
				Name: "router",
				ConfigType: &hcm.HttpFilter_TypedConfig{
					TypedConfig: m,
				},
			},
		},
		// 访问日志，格式统一由utils.CreateAccesslog生成
		AccessLog: []*accesslog.AccessLog{
			{
				Name: "accesslog",
				ConfigType: &accesslog.AccessLog_TypedConfig{
					TypedConfig: utils.CreateAccesslog(),
				},
			},
		},
	}

	// HCM整体序列化，塞进listener
	pbst, err := anypb.New(manager)
	if err != nil {
		panic(err)
	}
	l := &listener.Listener{
		// proxyless下listener名直接用下游服务名
		Name: s.Name,
		// 此处必须增加这个配置，否则会无法重复绑定端口，此listener接收的是来自virtualOutbound的流量
		// （原注释复制自proxy版：proxyless没有Sidecar，virtualOutbound(15001)只在proxy模式生成，
		//   流量实际不经任何listener转发。此处BindToPort=false的真实作用是：该listener仅作为
		//   逻辑配置供SDK拉取消费，不真正占用端口，也避免多服务VIP:Port重叠时报端口绑定冲突）
		BindToPort: wrapperspb.Bool(false),
		// proxyless SDK读取HCM配置的入口
		ApiListener: &listener.ApiListener{
			ApiListener: pbst,
		},
		Address: &core.Address{
			// 地址=下游服务在注册中心的VIP(虚拟IP)+端口：
			// 不是本机监听地址，而是"访问该服务"的逻辑标识
			Address: &core.Address_SocketAddress{
				SocketAddress: &core.SocketAddress{
					Address: s.VIP,
					PortSpecifier: &core.SocketAddress_PortValue{
						PortValue: uint32(s.Port),
					},
				},
			},
		},
		// 与ApiListener内容相同的HCM，保持标准Listener结构
		FilterChains: []*listener.FilterChain{
			{
				Filters: []*listener.Filter{
					{
						Name: wellknown.HTTPConnectionManager,
						ConfigType: &listener.Filter_TypedConfig{
							TypedConfig: pbst,
						},
					},
				},
			},
		},
	}
	return l, nil
}

// createNormalProxyListeners 为proxy模式生成单个下游服务的outbound listener（命名 服务名_outbound）
// 与proxyless版的区别：按协议区分filter chain（http/dubbo/thrift/tcp，见createProxyFilterChain），
// 并附带http_inspector listener filter嗅探应用协议，供filter chain按ApplicationProtocols匹配分流
// BindToPort=false：所有出站流量先进virtualOutbound(15001)，再按原始目的地址转到本listener
func createNormalProxyListeners(me, s *entity.MicroService) (*listener.Listener, error) {
	// router HTTP过滤器（链尾必须），空配置即默认行为
	rou := &routefilter.Router{}
	m, err := anypb.New(rou)
	if err != nil {
		panic(m)
	}

	// HCM：出站HTTP流量的连接管理器，路由走RDS（名字service:port关联CreateRoute的产物）
	manager := &hcm.HttpConnectionManager{
		// AUTO：自动嗅探http/1.x与h2
		CodecType:  hcm.HttpConnectionManager_AUTO,
		StatPrefix: utils.GetRouteName(s.Name, s.Port),
		// 引用RDS资源（同proxyless版），路由规则由router.go统一生成
		RouteSpecifier: &hcm.HttpConnectionManager_Rds{
			Rds: &hcm.Rds{
				ConfigSource: &core.ConfigSource{
					// ADS聚合发现，保证与cluster/listener等资源一致
					ConfigSourceSpecifier: &core.ConfigSource_Ads{
						Ads: &core.AggregatedConfigSource{},
					},
					InitialFetchTimeout: &durationpb.Duration{Seconds: 0},
					ResourceApiVersion:  core.ApiVersion_V3,
				},
				RouteConfigName: utils.GetRouteName(s.Name, s.Port),
			},
		},
		// HTTP过滤器链只挂router：出站侧不做限流/追踪
		HttpFilters: []*hcm.HttpFilter{{
			Name: wellknown.Router,
			ConfigType: &hcm.HttpFilter_TypedConfig{
				TypedConfig: m,
			},
		}},
		AccessLog: []*accesslog.AccessLog{
			{
				Name: "accesslog",
				ConfigType: &accesslog.AccessLog_TypedConfig{
					TypedConfig: utils.CreateAccesslog(),
				},
			},
		},
		// 客户端地址取自请求头(XFF)而非物理连接对端地址
		UseRemoteAddress: wrapperspb.Bool(false),
		// mTLS场景把本连接的客户端证书摘要追加进x-forwarded-client-cert头转发给上游
		ForwardClientCertDetails: hcm.HttpConnectionManager_APPEND_FORWARD,
	}

	pbst, err := anypb.New(manager)
	if err != nil {
		panic(err)
	}
	// 兜底tcp代理：非HTTP流量直接转发到该服务的默认cluster
	// （cluster名outbound|port|group|service，group为空表示含全部实例的默认cluster）
	tcpConf := &tcp.TcpProxy{
		StatPrefix: utils.GetOutboundTrafficClusterName(s.Name, s.Port, ""),
		ClusterSpecifier: &tcp.TcpProxy_Cluster{
			Cluster: utils.GetOutboundTrafficClusterName(s.Name, s.Port, ""),
		},
	}

	tcpC, err := anypb.New(tcpConf)
	if err != nil {
		return nil, err
	}
	// http_inspector的空配置，挂到下方ListenerFilters做协议嗅探
	hi, err := anypb.New(&http_inspector.HttpInspector{})
	if err != nil {
		return nil, err
	}
	l := &listener.Listener{
		// 命名 服务名_outbound（proxy版专属后缀，与proxyless版的裸服务名区分）
		Name: s.Name + "_outbound",
		// 此处必须增加这个配置，否则会无法重复绑定端口，此listener接收的是来自virtualOutbound的流量
		// （出站流量先进15001的virtualOutbound，其按原始目的地址(VIP:Port)匹配到本listener；
		//   本listener不自己绑端口，仅作为逻辑转发目标，否则与virtualOutbound的端口占用冲突）
		BindToPort: wrapperspb.Bool(false),
		Address: &core.Address{
			// 地址=下游服务VIP:Port，作为UseOriginalDst转发的匹配目标
			Address: &core.Address_SocketAddress{
				SocketAddress: &core.SocketAddress{
					Address: s.VIP,
					PortSpecifier: &core.SocketAddress_PortValue{
						PortValue: uint32(s.Port),
					},
				},
			},
		},
		// 按下游服务协议组装filter chain：http(HCM+tcp兜底)/dubbo/thrift，见filter.go
		FilterChains: createProxyFilterChain(me, s, pbst, tcpC),
		// listener级过滤器在filter chain匹配之前执行：
		// http_inspector嗅探出应用协议(http/1.x、h2c)，供上面filter chain
		// 的ApplicationProtocols匹配条件使用
		ListenerFilters: []*listener.ListenerFilter{
			{
				Name: "envoy.listener.http_inspector",
				ConfigType: &listener.ListenerFilter_TypedConfig{
					TypedConfig: hi,
				},
			},
		},
	}

	return l, nil
}

// createProxylessVirtualInboundListeners 生成proxyless模式的virtualInbound listener
// 与proxy模式的区别：直接绑定本服务业务端口（业务进程自身把流量引到这里），而非15006
// 入站路由直接内联在HCM的RouteConfig中（不走RDS），每条限流规则对应一条route，
// route的Action为NonForwardingAction（仅做限流判定，不转发），默认route兜底
//
// 【定位】proxyless模式SDK自己处理流量收发，Envoy配置在这里仅作为限流规则的载体：
// HCM只负责匹配请求并执行本地限流，NonForwardingAction表示"到此为止、不转发给任何
// cluster"，请求交还进程自身处理。
func createProxylessVirtualInboundListeners(me *entity.MicroService) (*listener.Listener, error) {
	// router HTTP过滤器（链尾必须），空配置即默认行为
	rou := &routefilter.Router{}

	m, err := anypb.New(rou)
	if err != nil {
		return nil, err
	}
	//本地限流filter的全局默认配置（空token bucket），具体每条route的限流参数在TypedPerFilterConfig中按route覆盖
	//（local_ratelimit支持route级覆盖：HCM级配置作默认值，命中route的TypedPerFilterConfig优先；
	//  空token bucket意味着未挂限流配置的请求一律不限流）
	lrl := &local_ratelimit.LocalRateLimit{
		StatPrefix: "http_local_rate_limit",
	}
	lrlAny, err := anypb.New(lrl)
	if err != nil {
		return nil, err
	}

	// 根据me.Ratelimits限流规则生成入站路由：每条规则一条route
	// route匹配条件由MatchConfig.Selectors生成，限流参数（token bucket、超限响应码等）以TypedPerFilterConfig挂在route上
	generateInboundRoutes := func() ([]*route.Route, error) {
		var routes []*route.Route
		for _, rl := range me.Ratelimits {
			matchConfig := rl.MatchConfig
			// 超限响应码（如429）从字符串配置转为HTTP状态码枚举
			statusCode, err := strconv.Atoi(rl.RatelimitConfig.Status)
			if err != nil {
				return nil, err
			}
			// 本条route专属的限流配置（经TypedPerFilterConfig覆盖HCM级默认值）
			rlrl := &local_ratelimit.LocalRateLimit{
				StatPrefix: "http_local_rate_limit",
				// 令牌桶：容量MaxTokens，每FillInterval(1秒)补充TokensPerFill个令牌，
				// 桶空时的新请求被判超限
				TokenBucket: &envoy_type_v3.TokenBucket{
					MaxTokens:     uint32(rl.RatelimitConfig.MaxTokens),
					TokensPerFill: wrapperspb.UInt32(uint32(rl.RatelimitConfig.TokensPerFill)),
					FillInterval:  &durationpb.Duration{Seconds: 1},
				},
				// 限流判定100%启用（FractionalPercent可做灰度比例，这里不灰度）
				FilterEnabled: &core.RuntimeFractionalPercent{
					DefaultValue: &envoy_type_v3.FractionalPercent{
						Numerator:   100,
						Denominator: envoy_type_v3.FractionalPercent_HUNDRED,
					},
				},
				// 超限后100%实际拒绝（返回下方Status配置的状态码）
				FilterEnforced: &core.RuntimeFractionalPercent{
					DefaultValue: &envoy_type_v3.FractionalPercent{
						Numerator:   100,
						Denominator: envoy_type_v3.FractionalPercent_HUNDRED,
					},
				},
				Status: &envoy_type_v3.HttpStatus{
					Code: envoy_type_v3.StatusCode(statusCode),
				},

				// 超限响应统一附加x-local-rate-limit:true头，便于调用方识别被限流
				ResponseHeadersToAdd: []*core.HeaderValueOption{
					{
						Append: wrapperspb.Bool(false),
						Header: &core.HeaderValue{
							Key:   "x-local-rate-limit",
							Value: "true",
						},
					},
				},
			}
			rlrlAny, err := anypb.New(rlrl)
			if err != nil {
				return nil, err
			}
			// NonForwardingAction：命中本条route后不把请求转发给任何cluster。
			// proxyless模式SDK自己处理流量，route的职责只到"匹配+限流判定"为止
			act := &route.NonForwardingAction{}
			act.Reset()
			route := &route.Route{
				// 匹配条件（path/header/method等selector）由matcher按协议生成
				Match: generateMatchConfig(matchConfig),
				Action: &route.Route_NonForwardingAction{
					NonForwardingAction: act,
				},
				// route级限流配置：key为local_ratelimit过滤器名，仅对本条route生效
				TypedPerFilterConfig: map[string]*anypb.Any{"envoy.filters.http.local_ratelimit": rlrlAny},
			}
			routes = append(routes, route)
		}
		//默认兜底路由：前缀/匹配所有请求，不限流不转发
		act := &route.NonForwardingAction{}
		act.Reset()
		defaultRoute := &route.Route{
			Match: &route.RouteMatch{
				PathSpecifier: &route.RouteMatch_Prefix{
					Prefix: "/",
				},
			},
			Action: &route.Route_NonForwardingAction{
				NonForwardingAction: act,
			},
		}
		routes = append(routes, defaultRoute)
		return routes, nil
	}
	routes, err := generateInboundRoutes()
	if err != nil {
		return nil, err
	}

	// curl http://b2c.b2cop.http.server:8000/api/v1/  -H "x-client-trace-id:11111"
	// 链路追踪配置：读取服务Settings中的tracing设置，未启用则返回nil
	// span通过opentelemetry_collector cluster以OTel协议上报，附带service.type=matrixmesh标签
	openTelConfig := func() *hcm.HttpConnectionManager_Tracing {
		config := entity.TracingSetting{}
		me.Settings.Get(entity.TracingType, &config)
		if !config.Enable {
			return nil
		}

		// OTel tracer配置：gRPC上报到opentelemetry_collector cluster
		// （cluster.go中定义，STRICT_DNS指向配置的collector地址），服务名envoy.<服务名>
		opentelemetry := &trace.OpenTelemetryConfig{
			GrpcService: &core.GrpcService{
				TargetSpecifier: &core.GrpcService_EnvoyGrpc_{
					EnvoyGrpc: &core.GrpcService_EnvoyGrpc{
						ClusterName: "opentelemetry_collector",
					},
				},
				Timeout: durationpb.New(1 * time.Second),
			},
			ServiceName: "envoy." + me.Name,
		}
		ret, _ := anypb.New(opentelemetry)

		return &hcm.HttpConnectionManager_Tracing{
			// 三级采样率(%)：按上游已有采样决定、随机采样、总体上限
			ClientSampling:  &envoy_type_v3.Percent{Value: float64(config.ClientSampling)},
			RandomSampling:  &envoy_type_v3.Percent{Value: float64(config.RandomSampling)},
			OverallSampling: &envoy_type_v3.Percent{Value: float64(config.OverallSampling)},
			CustomTags: []*tracing.CustomTag{
				{
					Tag: "service.type",
					Type: &tracing.CustomTag_Literal_{
						Literal: &tracing.CustomTag_Literal{Value: "matrixmesh"},
					},
				},
			},
			Provider: &trace.Tracing_Http{
				Name:       "envoy.tracers.opentelemetry",
				ConfigType: &trace.Tracing_Http_TypedConfig{TypedConfig: ret},
			},
		}
	}

	// 入站HCM：统计前缀借用入站cluster名(inbound|port|protocol|service)；
	// 路由取内联形态（route_specifier两种形态之一）：RouteConfiguration直接嵌在HCM里，
	// 与RDS二选一——这里路由与listener强绑定且需按route挂限流，没必要单独走RDS下发
	manager := &hcm.HttpConnectionManager{
		// AUTO：自动嗅探http/1.x与h2
		CodecType:  hcm.HttpConnectionManager_AUTO,
		StatPrefix: utils.GetInboundTrafficClusterName(me.Name, me.Port, me.Protocol),
		Tracing:    openTelConfig(),
		RouteSpecifier: &hcm.HttpConnectionManager_RouteConfig{
			RouteConfig: &route.RouteConfiguration{
				Name: "local_route",
				VirtualHosts: []*route.VirtualHost{
					{
						Name: "local_route",
						// 匹配任意Host头的请求
						Domains: []string{"*"},
						Routes:  routes,
					},
				},
			},
		},
		// 客户端地址取自请求头(XFF)而非物理连接对端地址
		UseRemoteAddress: wrapperspb.Bool(false),
		// mTLS场景把客户端证书摘要追加进x-forwarded-client-cert头
		ForwardClientCertDetails: hcm.HttpConnectionManager_APPEND_FORWARD,
		AccessLog: []*accesslog.AccessLog{
			{
				Name: "accesslog",
				ConfigType: &accesslog.AccessLog_TypedConfig{
					TypedConfig: utils.CreateAccesslog(),
				},
			},
		},
	}
	// 组装HTTP过滤器链，顺序敏感：local_ratelimit在前、router必须收尾。
	// 配置了限流规则才挂载本地限流filter（带上面那份空默认配置）
	if len(me.Ratelimits) > 0 {
		manager.HttpFilters = append(manager.HttpFilters, &hcm.HttpFilter{
			Name: "envoy.filters.http.local_ratelimit",
			ConfigType: &hcm.HttpFilter_TypedConfig{
				TypedConfig: lrlAny,
			},
		})
	}
	// router收尾：限流filter放行后由它终结HTTP过滤器链
	manager.HttpFilters = append(manager.HttpFilters, &hcm.HttpFilter{
		Name: "router",
		ConfigType: &hcm.HttpFilter_TypedConfig{
			TypedConfig: m,
		},
	})
	pbst, err := anypb.New(manager)
	if err != nil {
		return nil, err
	}

	// proxyless版virtualInbound：真实绑定0.0.0.0:本服务业务端口
	// （没有iptables与15006的配合，流量由业务进程/SDK自行引到该端口做限流判定）
	virutalInbound := &listener.Listener{
		Name: "virtualInbound",
		// 标记入站方向，影响指标统计口径
		TrafficDirection: core.TrafficDirection_INBOUND,
		Address: &core.Address{
			Address: &core.Address_SocketAddress{
				SocketAddress: &core.SocketAddress{
					Protocol: core.SocketAddress_TCP,
					Address:  "0.0.0.0",
					PortSpecifier: &core.SocketAddress_PortValue{
						PortValue: uint32(me.Port),
					},
				},
			},
		},
		// 单条filter chain：全部流量交给HCM（限流判定在HTTP七层做）
		FilterChains: []*listener.FilterChain{
			{
				Filters: []*listener.Filter{
					{
						Name: wellknown.HTTPConnectionManager,
						ConfigType: &listener.Filter_TypedConfig{
							TypedConfig: pbst,
						},
					},
				},
			},
		},
	}
	return virutalInbound, nil
}

// createProxyVirtualInboundListeners 生成proxy模式（Sidecar）的virtualInbound listener，绑定15006端口
// 通过iptables拦截的入站流量先进这里，按UseOriginalDst+filter chain匹配分流：
//   - HTTP流量（http_inspector嗅探出http/1.x、h2c且目的端口为业务端口）走HCM，应用限流后路由到本服务inbound cluster
//   - 非HTTP但目的端口为业务端口的TCP流量走tcp_proxy直连本服务inbound cluster
//   - 其余流量走InboundPassthroughClusterIpv4透传（保留原始目的地址）
//
// 限流规则挂载在route级别（TypedPerFilterConfig），并通过x-matrix-mesh-service header识别调用方服务
func createProxyVirtualInboundListeners(me *entity.MicroService) (*listener.Listener, error) {

	rou := &routefilter.Router{}

	m, err := anypb.New(rou)
	if err != nil {
		return nil, err
	}
	//本地限流filter的全局默认配置（空token bucket），具体每条route的限流参数在TypedPerFilterConfig中按route覆盖
	lrl := &local_ratelimit.LocalRateLimit{
		StatPrefix: "http_local_rate_limit",
	}
	lrlAny, err := anypb.New(lrl)
	if err != nil {
		return nil, err
	}

	// 根据me.Ratelimits限流规则生成入站路由：每条规则一条route
	// 额外追加一个x-matrix-mesh-service header匹配条件，用调用方服务名区分限流对象（CallerService为正则）
	// （该header由调用方sidecar的出站路由RequestHeadersToAdd注入，Append=false覆盖模式，
	//   应用自带的同名头会被覆盖，无法伪造服务名冒充别的调用方）
	generateInboundRoutes := func() ([]*route.Route, error) {
		var routes []*route.Route
		for _, rl := range me.Ratelimits {
			matchConfig := rl.MatchConfig
			// 追加调用方识别selector：x-matrix-mesh-service的值按正则匹配CallerService，
			// 实现"本条限流规则只对指定调用方生效"（业务自身的selector全部AND组合）
			matchConfig.Selectors = append(matchConfig.Selectors, entity.RequestSelector{
				Type:     "http-header",
				Key:      MatrixMeshServiceHeader,
				Value:    matchConfig.CallerService,
				Operator: entity.RegexMatch,
			})
			statusCode, err := strconv.Atoi(rl.RatelimitConfig.Status)
			if err != nil {
				return nil, err
			}
			// 本条route专属限流配置：令牌桶(容量MaxTokens/每秒补TokensPerFill)、
			// 100%启用并执行、超限返回上面的状态码并附x-local-rate-limit:true响应头
			rlrl := &local_ratelimit.LocalRateLimit{
				StatPrefix: "http_local_rate_limit",
				TokenBucket: &envoy_type_v3.TokenBucket{
					MaxTokens:     uint32(rl.RatelimitConfig.MaxTokens),
					TokensPerFill: wrapperspb.UInt32(uint32(rl.RatelimitConfig.TokensPerFill)),
					FillInterval:  &durationpb.Duration{Seconds: 1},
				},
				FilterEnabled: &core.RuntimeFractionalPercent{
					DefaultValue: &envoy_type_v3.FractionalPercent{
						Numerator:   100,
						Denominator: envoy_type_v3.FractionalPercent_HUNDRED,
					},
				},
				FilterEnforced: &core.RuntimeFractionalPercent{
					DefaultValue: &envoy_type_v3.FractionalPercent{
						Numerator:   100,
						Denominator: envoy_type_v3.FractionalPercent_HUNDRED,
					},
				},
				Status: &envoy_type_v3.HttpStatus{
					Code: envoy_type_v3.StatusCode(statusCode),
				},

				ResponseHeadersToAdd: []*core.HeaderValueOption{
					{
						Append: wrapperspb.Bool(false),
						Header: &core.HeaderValue{
							Key:   "x-local-rate-limit",
							Value: "true",
						},
					},
				},
			}
			rlrlAny, err := anypb.New(rlrl)
			if err != nil {
				return nil, err
			}
			route := &route.Route{
				// 匹配条件=业务selector+调用方header识别（见上），由matcher生成
				Match: generateMatchConfig(matchConfig),
				// 与proxyless版NonForwardingAction的区别：proxy模式流量真的经Envoy转发，
				// 未被限流的请求路由到本服务inbound cluster（指向本机业务进程）
				Action: &route.Route_Route{
					Route: &route.RouteAction{
						ClusterSpecifier: &route.RouteAction_Cluster{
							Cluster: utils.GetInboundTrafficClusterName(me.Name, me.Port, me.Protocol),
						},
						// 超时0=禁用路由级超时：入站请求的时长控制交给业务进程自己
						Timeout: &durationpb.Duration{
							Seconds: 0,
							Nanos:   0,
						},
					},
				},
				// route级限流配置，仅对本条route生效
				TypedPerFilterConfig: map[string]*anypb.Any{"envoy.filters.http.local_ratelimit": rlrlAny},
			}
			routes = append(routes, route)
		}
		//默认兜底路由：前缀/匹配所有请求，直接路由到本服务inbound cluster，不限流
		defaultRoute := &route.Route{
			Match: &route.RouteMatch{
				PathSpecifier: &route.RouteMatch_Prefix{
					Prefix: "/",
				},
			},
			Action: &route.Route_Route{
				Route: &route.RouteAction{
					ClusterSpecifier: &route.RouteAction_Cluster{
						Cluster: utils.GetInboundTrafficClusterName(me.Name, me.Port, me.Protocol),
					},
					// 同上：禁用路由级超时
					Timeout: &durationpb.Duration{
						Seconds: 0,
						Nanos:   0,
					},
				},
			},
		}
		routes = append(routes, defaultRoute)
		return routes, nil
	}
	routes, err := generateInboundRoutes()
	if err != nil {
		return nil, err
	}

	// curl http://b2c.b2cop.http.server:8000/api/v1/  -H "x-client-trace-id:11111"
	// 链路追踪配置：读取服务Settings中的tracing设置，未启用则返回nil
	// span通过opentelemetry_collector cluster以OTel协议上报，附带service.type=matrixmesh标签
	openTelConfig := func() *hcm.HttpConnectionManager_Tracing {
		config := entity.TracingSetting{}
		me.Settings.Get(entity.TracingType, &config)
		if !config.Enable {
			return nil
		}

		// OTel tracer配置：gRPC上报到opentelemetry_collector cluster
		// （cluster.go中定义，STRICT_DNS指向配置的collector地址），服务名envoy.<服务名>
		opentelemetry := &trace.OpenTelemetryConfig{
			GrpcService: &core.GrpcService{
				TargetSpecifier: &core.GrpcService_EnvoyGrpc_{
					EnvoyGrpc: &core.GrpcService_EnvoyGrpc{
						ClusterName: "opentelemetry_collector",
					},
				},
				Timeout: durationpb.New(1 * time.Second),
			},
			ServiceName: "envoy." + me.Name,
		}
		ret, _ := anypb.New(opentelemetry)

		return &hcm.HttpConnectionManager_Tracing{
			// 三级采样率(%)：按上游已有采样决定、随机采样、总体上限
			ClientSampling:  &envoy_type_v3.Percent{Value: float64(config.ClientSampling)},
			RandomSampling:  &envoy_type_v3.Percent{Value: float64(config.RandomSampling)},
			OverallSampling: &envoy_type_v3.Percent{Value: float64(config.OverallSampling)},
			CustomTags: []*tracing.CustomTag{
				{
					Tag: "service.type",
					Type: &tracing.CustomTag_Literal_{
						Literal: &tracing.CustomTag_Literal{Value: "matrixmesh"},
					},
				},
			},
			Provider: &trace.Tracing_Http{
				Name:       "envoy.tracers.opentelemetry",
				ConfigType: &trace.Tracing_Http_TypedConfig{TypedConfig: ret},
			},
		}
	}

	//入站HTTP流量的连接管理器：路由内联（local_route），限流参数按route覆盖
	manager := &hcm.HttpConnectionManager{
		// AUTO：自动嗅探http/1.x与h2
		CodecType:  hcm.HttpConnectionManager_AUTO,
		StatPrefix: utils.GetInboundTrafficClusterName(me.Name, me.Port, me.Protocol),
		Tracing:    openTelConfig(),
		// 路由内联形态：RouteConfiguration直接嵌在HCM里（与RDS二选一），
		// 限流route与该listener强绑定，无需单独走RDS
		RouteSpecifier: &hcm.HttpConnectionManager_RouteConfig{
			RouteConfig: &route.RouteConfiguration{
				Name: "local_route",
				VirtualHosts: []*route.VirtualHost{
					{
						Name: "local_route",
						// 匹配任意Host头的请求
						Domains: []string{"*"},
						Routes:  routes,
					},
				},
			},
		},
		// 客户端地址取自请求头(XFF)而非物理连接对端地址
		UseRemoteAddress: wrapperspb.Bool(false),
		// mTLS场景把客户端证书摘要追加进x-forwarded-client-cert头
		ForwardClientCertDetails: hcm.HttpConnectionManager_APPEND_FORWARD,
		AccessLog: []*accesslog.AccessLog{
			{
				Name: "accesslog",
				ConfigType: &accesslog.AccessLog_TypedConfig{
					TypedConfig: utils.CreateAccesslog(),
				},
			},
		},
	}
	//组装HTTP过滤器链：local_ratelimit在前、router必须收尾
	//配置了限流规则才挂载本地限流filter
	if len(me.Ratelimits) > 0 {
		manager.HttpFilters = append(manager.HttpFilters, &hcm.HttpFilter{
			Name: "envoy.filters.http.local_ratelimit",
			ConfigType: &hcm.HttpFilter_TypedConfig{
				TypedConfig: lrlAny,
			},
		})
	}
	// router收尾：限流filter放行后由它按route把请求发往inbound cluster
	manager.HttpFilters = append(manager.HttpFilters, &hcm.HttpFilter{
		Name: "router",
		ConfigType: &hcm.HttpFilter_TypedConfig{
			TypedConfig: m,
		},
	})
	// HCM序列化，供下方HTTP filter chain引用
	pbst, err := anypb.New(manager)
	if err != nil {
		return nil, err
	}

	//非HTTP协议（目的端口为业务端口）的入站TCP流量直接转发到本服务inbound cluster
	inboundTcpConfig := &tcp.TcpProxy{
		StatPrefix: utils.GetInboundTrafficClusterName(me.Name, me.Port, me.Protocol),
		ClusterSpecifier: &tcp.TcpProxy_Cluster{
			Cluster: utils.GetInboundTrafficClusterName(me.Name, me.Port, me.Protocol),
		},
	}

	inboundTcpC, err := anypb.New(inboundTcpConfig)
	if err != nil {
		return nil, err
	}
	//original_dst listener filter：恢复被iptables改写前的原始目的地址，供filter chain按目的端口分流
	originalDst := &od.OriginalDst{}
	odA, err := anypb.New(originalDst)
	if err != nil {
		return nil, err
	}
	//http_inspector listener filter：嗅探流量是否为HTTP及其协议版本，供filter chain按ApplicationProtocols匹配
	hi, err := anypb.New(&http_inspector.HttpInspector{})
	if err != nil {
		return nil, err
	}
	//兜底filter chain：不匹配业务端口的流量经InboundPassthroughClusterIpv4按原始目的地址透传
	passThroughConfig := &tcp.TcpProxy{
		StatPrefix: "InboundPassthroughClusterIpv4",
		ClusterSpecifier: &tcp.TcpProxy_Cluster{
			Cluster: "InboundPassthroughClusterIpv4",
		},
	}

	passThrough, err := anypb.New(passThroughConfig)
	if err != nil {
		return nil, err
	}

	//virtualInbound：监听15006，UseOriginalDst配合original_dst filter按原始目的端口分流到各filter chain
	virutalInbound := &listener.Listener{
		Name:             "virtualInbound",
		TrafficDirection: core.TrafficDirection_INBOUND,
		Address: &core.Address{
			Address: &core.Address_SocketAddress{
				SocketAddress: &core.SocketAddress{
					Protocol: core.SocketAddress_TCP,
					Address:  "0.0.0.0",
					PortSpecifier: &core.SocketAddress_PortValue{
						PortValue: 15006,
					},
				},
			},
		},
		// iptables把入站流量重定向到15006时目的地址已被改写；UseOriginalDst=true
		// 让Envoy以original_dst filter恢复出的原始目的地址参与filter chain match
		// （如DestinationPort），流量得以按"原本要访问的业务端口"分流
		UseOriginalDst: wrapperspb.Bool(true),
		FilterChains: []*listener.FilterChain{
			{
				//filter chain 1：HTTP流量（目的端口为业务端口），走HCM处理限流/追踪后转本服务
				// （http_inspector嗅探出http/1.x或h2c才会命中ApplicationProtocols）
				FilterChainMatch: &listener.FilterChainMatch{
					ApplicationProtocols: []string{
						"http/1.0",
						"http/1.1",
						"h2c",
					},
					DestinationPort: wrapperspb.UInt32(uint32(me.Port)),
				},
				Filters: []*listener.Filter{
					{
						Name: wellknown.HTTPConnectionManager,
						ConfigType: &listener.Filter_TypedConfig{
							TypedConfig: pbst,
						},
					},
				},
			},
			{
				//filter chain 2：非HTTP但目的端口为业务端口的TCP流量，直接tcp_proxy到本服务inbound cluster
				FilterChainMatch: &listener.FilterChainMatch{
					DestinationPort: wrapperspb.UInt32(uint32(me.Port)),
				},
				Filters: []*listener.Filter{
					{
						Name: wellknown.TCPProxy,
						ConfigType: &listener.Filter_TypedConfig{
							TypedConfig: inboundTcpC,
						},
					},
				},
			},
			{
				//filter chain 3：其余流量（目的IP为任意IPv4），走InboundPassthroughClusterIpv4透传
				FilterChainMatch: &listener.FilterChainMatch{
					PrefixRanges: []*core.CidrRange{
						{
							AddressPrefix: "0.0.0.0",
							PrefixLen:     wrapperspb.UInt32(0),
						},
					},
				},
				Filters: []*listener.Filter{
					{
						Name: wellknown.TCPProxy,
						ConfigType: &listener.Filter_TypedConfig{
							TypedConfig: passThrough,
						},
					},
				},
			},
		},
		// listener级过滤器（在filter chain匹配之前执行）：
		// 先恢复原始目的地址，再嗅探应用协议，二者共同供上面三条链的match条件使用
		ListenerFilters: []*listener.ListenerFilter{
			{
				Name: "envoy.listener.original_dst",
				ConfigType: &listener.ListenerFilter_TypedConfig{
					TypedConfig: odA,
				},
			},
			{
				Name: "envoy.listener.http_inspector",
				ConfigType: &listener.ListenerFilter_TypedConfig{
					TypedConfig: hi,
				},
			},
		},
	}
	return virutalInbound, nil
}

// createVirtualOutBoundListener 生成virtualOutbound listener，监听15001（Sidecar出站流量统一入口）
// iptables把业务进程的出站流量重定向到这里，UseOriginalDst按原始目的地址（VIP+Port）
// 转给各服务对应的outbound listener处理；未匹配到任何listener的流量走PassthroughCluster透传兜底
func createVirtualOutBoundListener() (*listener.Listener, error) {
	//兜底tcp代理：没有匹配listener的出站流量直接透传到原始目的地址
	//（PassthroughCluster在cluster.go中定义为ORIGINAL_DST类型：不经服务发现、
	// 按原始目的地址直连，保证网格外服务的访问不受Sidecar接入影响）
	tcpConfig := &tcp.TcpProxy{
		StatPrefix: "PassthroughCluster",
		ClusterSpecifier: &tcp.TcpProxy_Cluster{
			Cluster: "PassthroughCluster",
		},
	}

	tcpC, err := anypb.New(tcpConfig)
	if err != nil {
		return nil, err
	}

	// 统一出口：iptables把业务进程全部出站流量重定向到0.0.0.0:15001，只有这一条
	// tcp filter chain；匹配不到任何outbound listener的流量落到PassthroughCluster
	virtualOutbound := &listener.Listener{
		Name: "virtualOutbound",
		// 此处必须增加这个配置，来重定向流量到真实的listeners上
		// （按流量原始目的地址VIP:Port匹配各BindToPort=false的 服务名_outbound listener，
		//   这些逻辑listener不绑端口、仅作为转发的匹配目标）
		UseOriginalDst:   wrapperspb.Bool(true),
		TrafficDirection: core.TrafficDirection_OUTBOUND,
		Address: &core.Address{
			Address: &core.Address_SocketAddress{
				SocketAddress: &core.SocketAddress{
					Protocol: core.SocketAddress_TCP,
					Address:  "0.0.0.0",
					PortSpecifier: &core.SocketAddress_PortValue{
						PortValue: 15001,
					},
				},
			},
		},
		FilterChains: []*listener.FilterChain{
			{
				Filters: []*listener.Filter{
					{
						Name: wellknown.TCPProxy,
						ConfigType: &listener.Filter_TypedConfig{
							TypedConfig: tcpC,
						},
					},
				},
			},
		},
		AccessLog: []*accesslog.AccessLog{
			{
				Name: "accesslog",
				ConfigType: &accesslog.AccessLog_TypedConfig{
					TypedConfig: utils.CreateAccesslog(),
				},
			},
		},
	}
	return virtualOutbound, nil
}

// createAdminListener 生成AdminPortal listener，监听15020
// 将/stats/prometheus（Prometheus指标抓取）与/ready（就绪探针）请求转发到Envoy自身的admin端口(15000)
func createAdminListener() (*listener.Listener, error) {
	// router HTTP过滤器（链尾必须）
	router, err := anypb.New(&routefilter.Router{})
	if err != nil {
		return nil, err
	}

	// HCM：两条前缀路由都指向"admin"cluster——cluster.go中定义为指向本机
	// Envoy自身15000 admin端口的静态cluster，相当于把Envoy admin接口的
	// 两个只读端点经由15020转发暴露出去（admin端口默认只绑127.0.0.1，外部不可达）
	manager := &hcm.HttpConnectionManager{
		// AUTO：自动嗅探http/1.x与h2
		CodecType:  hcm.HttpConnectionManager_AUTO,
		StatPrefix: "stats",
		// 路由内联（仅两条前缀匹配，无需单独走RDS）
		RouteSpecifier: &hcm.HttpConnectionManager_RouteConfig{
			RouteConfig: &route.RouteConfiguration{
				VirtualHosts: []*route.VirtualHost{
					{
						Name: "AdminPortal",
						// 匹配任意Host头
						Domains: []string{"*"},
						Routes: []*route.Route{
							{
								// Envoy原生指标端点，供Prometheus抓取
								Match: &route.RouteMatch{
									PathSpecifier: &route.RouteMatch_Prefix{
										Prefix: "/stats/prometheus",
									},
								},
								Action: &route.Route_Route{
									Route: &route.RouteAction{
										ClusterSpecifier: &route.RouteAction_Cluster{
											Cluster: "admin",
										},
									},
								},
							},
							{
								// 就绪探针端点，供K8s等编排系统探测Sidecar状态
								Match: &route.RouteMatch{
									PathSpecifier: &route.RouteMatch_Prefix{
										Prefix: "/ready",
									},
								},
								Action: &route.Route_Route{
									Route: &route.RouteAction{
										ClusterSpecifier: &route.RouteAction_Cluster{
											Cluster: "admin",
										},
									},
								},
							},
						},
					},
				},
			},
		},
		HttpFilters: []*hcm.HttpFilter{{
			Name: "envoy.router",
			ConfigType: &hcm.HttpFilter_TypedConfig{
				TypedConfig: router,
			},
		}},
	}

	httpManager, err := anypb.New(manager)
	if err != nil {
		return nil, err
	}

	// 15020真实绑定0.0.0.0：本listener是Sidecar上唯一面向外部
	// （Prometheus/K8s探针）的访问入口
	listener := &listener.Listener{
		Name: "AdminPortal",
		Address: &core.Address{
			Address: &core.Address_SocketAddress{
				SocketAddress: &core.SocketAddress{
					Protocol: core.SocketAddress_TCP,
					Address:  "0.0.0.0",
					PortSpecifier: &core.SocketAddress_PortValue{
						PortValue: 15020,
					},
				},
			},
		},
		FilterChains: []*listener.FilterChain{
			{
				Filters: []*listener.Filter{
					{
						Name: wellknown.HTTPConnectionManager,
						ConfigType: &listener.Filter_TypedConfig{
							TypedConfig: httpManager,
						},
					},
				},
			},
		},
	}
	return listener, nil
}
