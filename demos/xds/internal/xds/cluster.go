package xds

import (
	"time"

	"github.com/balcony314/xds/internal/entity"
	"github.com/balcony314/xds/internal/xds/utils"
	"github.com/balcony314/xds/pkg/config"
	"github.com/balcony314/xds/pkg/logging"

	cluster "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	core "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpoint "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	hpo "github.com/envoyproxy/go-control-plane/envoy/extensions/upstreams/http/v3"

	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	xdstype "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// maxUint32Nums uint32最大值的一半（约21亿），用作熔断器各项阈值的默认值。
// Envoy的熔断阈值是具体数值语义，未配置时填极大值等于事实不限流，
// 避免落进Envoy内置默认值（如最大并发1024）误伤大流量服务
const maxUint32Nums = ^uint32(0) >> 1

// CreateCluster 生成服务Sidecar的全部CDS cluster资源。
//
// 【Cluster是什么】Envoy里的cluster是"上游服务"的抽象——路由的终点，
// 承载负载均衡策略、熔断阈值、协议协商、异常实例摘除等"怎么连上游"的全部配置。
// Cluster本身不含实例列表：EDS类型cluster的实例由同名的EDS资源（ClusterLoadAssignment，
// 见endpoint.go）动态下发，两者靠cluster名关联。
//
// me为当前服务（本节点），services为me可见的所有下游服务列表。
// 返回内容包含：2个透传cluster、本服务inbound cluster、admin/tracing辅助cluster，
// 以及每个下游服务按实例组拆分的outbound cluster。
//
// 【命名约定】业务cluster名遵循 outbound|port|group|service 与 inbound|port|protocol|service
// （见internal/xds/utils的GetOutboundTrafficClusterName/GetInboundTrafficClusterName），
// 第4段固定是服务名——api/xds/callback.go的就近路由靠strings.Split(clusterName,"|")[3]
// 反查服务配置，生成方与解析方的这一约定必须保持一致
func CreateCluster(me *entity.MicroService, services []entity.MicroService) []types.Resource {
	var clusters []types.Resource
	// 默认 passthrough cluster
	// 类型为ORIGINAL_DST：不经过服务发现，直接使用请求的原始目的地址转发，
	// 用于兜底路由未匹配到任何服务规则的流量（如访问网格外的服务）
	// LB策略CLUSTER_PROVIDED：ORIGINAL_DST类cluster由Envoy内部实现负载均衡（即"连原始目的地址"本身）
	passthroughCluster := &cluster.Cluster{
		Name:                 "PassthroughCluster",
		ConnectTimeout:       durationpb.New(5 * time.Second),
		ClusterDiscoveryType: &cluster.Cluster_Type{Type: cluster.Cluster_ORIGINAL_DST},
		LbPolicy:             cluster.Cluster_CLUSTER_PROVIDED,
		CircuitBreakers: &cluster.CircuitBreakers{
			// 熔断四项阈值（最大连接/最大排队/最大并发请求/最大并发重试）全部取极大值：
			// 透传是兜底链路，不人为设限，避免误伤网格外服务的正常调用
			Thresholds: []*cluster.CircuitBreakers_Thresholds{
				{
					MaxConnections:     wrapperspb.UInt32(maxUint32Nums),
					MaxPendingRequests: wrapperspb.UInt32(maxUint32Nums),
					MaxRequests:        wrapperspb.UInt32(maxUint32Nums),
					MaxRetries:         wrapperspb.UInt32(maxUint32Nums),
				},
			},
		},
	}
	// 上游HTTP协议协商选项：UseDownstreamProtocolConfig表示上游协议跟随下游——
	// 下游进来的请求是HTTP/1.1就用HTTP/1.1转发，是HTTP/2就用HTTP/2，
	// 控制面无需为每个服务显式指定协议。序列化成Any后填入cluster的
	// TypedExtensionProtocolOptions（key为Envoy扩展类型名），localInboundCluster会复用
	httpProtocolOption := &hpo.HttpProtocolOptions{
		UpstreamProtocolOptions: &hpo.HttpProtocolOptions_UseDownstreamProtocolConfig{
			UseDownstreamProtocolConfig: &hpo.HttpProtocolOptions_UseDownstreamHttpConfig{},
		},
	}
	httpProtocolOptionAny, err := anypb.New(httpProtocolOption)
	if err != nil {
		// 入站cluster协议配置构造失败属于不可恢复的程序性错误，直接panic终止
		panic(err)
	}
	// 入站透传cluster（Istio风格）：virtualInbound中非业务端口的流量通过该cluster原样转发给本机业务进程
	// bind 127.0.0.6：入站流量经Envoy转回本机时，用127.0.0.6作为源地址与业务进程直接的请求区分开，
	// 避免Envoy再次拦截形成回环，同时保留原始目的地址（ORIGINAL_DST）供业务进程获取
	inboundPassthroughClusterIpv4 := &cluster.Cluster{
		Name:                 "InboundPassthroughClusterIpv4",
		ConnectTimeout:       durationpb.New(5 * time.Second),
		ClusterDiscoveryType: &cluster.Cluster_Type{Type: cluster.Cluster_ORIGINAL_DST},
		LbPolicy:             cluster.Cluster_CLUSTER_PROVIDED,
		CircuitBreakers: &cluster.CircuitBreakers{
			Thresholds: []*cluster.CircuitBreakers_Thresholds{
				{
					MaxConnections:     wrapperspb.UInt32(maxUint32Nums),
					MaxPendingRequests: wrapperspb.UInt32(maxUint32Nums),
					MaxRequests:        wrapperspb.UInt32(maxUint32Nums),
					MaxRetries:         wrapperspb.UInt32(maxUint32Nums),
				},
			},
		},
		UpstreamBindConfig: &core.BindConfig{
			SourceAddress: &core.SocketAddress{
				Address: "127.0.0.6",
				// 源端口0表示由操作系统随机分配
				PortSpecifier: &core.SocketAddress_PortValue{
					PortValue: 0,
				},
			},
		},
	}

	// local本身 cluster
	// 本服务自身的inbound cluster（命名 inbound|port|protocol|service）：
	// virtualInbound收到入站请求后最终路由到这里，LoadAssignment固定指向本机业务端口。
	// 类型为STATIC：实例地址直接内嵌在LoadAssignment里（不走EDS）——入站终点永远是本机，
	// 无需动态服务发现。地址0.0.0.0是Envoy的特殊上游地址，建连时会改用下游请求的
	// 原始目的地址（即业务进程实际监听的地址），因此不依赖业务绑定哪个网卡IP
	localInboundCluster := &cluster.Cluster{
		Name:                 utils.GetInboundTrafficClusterName(me.Name, me.Port, me.Protocol),
		ConnectTimeout:       durationpb.New(10 * time.Second),
		ClusterDiscoveryType: &cluster.Cluster_Type{Type: cluster.Cluster_STATIC},
		CircuitBreakers: &cluster.CircuitBreakers{
			Thresholds: []*cluster.CircuitBreakers_Thresholds{
				{
					MaxConnections:     wrapperspb.UInt32(maxUint32Nums),
					MaxPendingRequests: wrapperspb.UInt32(maxUint32Nums),
					MaxRequests:        wrapperspb.UInt32(maxUint32Nums),
					MaxRetries:         wrapperspb.UInt32(maxUint32Nums),
				},
			},
		},
		TypedExtensionProtocolOptions: map[string]*anypb.Any{
			"envoy.extensions.upstreams.http.v3.HttpProtocolOptions": httpProtocolOptionAny,
		},
		LoadAssignment: &endpoint.ClusterLoadAssignment{
			ClusterName: utils.GetInboundTrafficClusterName(me.Name, me.Port, me.Protocol),
			Endpoints: []*endpoint.LocalityLbEndpoints{
				{
					LbEndpoints: []*endpoint.LbEndpoint{
						{
							HostIdentifier: &endpoint.LbEndpoint_Endpoint{
								Endpoint: &endpoint.Endpoint{
									Address: &core.Address{
										Address: &core.Address_SocketAddress{
											SocketAddress: &core.SocketAddress{
												Address: "0.0.0.0",
												PortSpecifier: &core.SocketAddress_PortValue{
													PortValue: uint32(me.Port),
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	// admin 转发
	// 指向Envoy自身15000 admin端口，配合AdminPortal listener暴露/stats/prometheus与/ready接口
	localAdminCluster := &cluster.Cluster{
		Name:                 "admin",
		ConnectTimeout:       durationpb.New(time.Second),
		ClusterDiscoveryType: &cluster.Cluster_Type{Type: cluster.Cluster_STATIC},
		LoadAssignment: &endpoint.ClusterLoadAssignment{
			ClusterName: "admin",
			Endpoints: []*endpoint.LocalityLbEndpoints{
				{
					LbEndpoints: []*endpoint.LbEndpoint{
						{
							HostIdentifier: &endpoint.LbEndpoint_Endpoint{
								Endpoint: &endpoint.Endpoint{
									Address: &core.Address{
										Address: &core.Address_SocketAddress{
											SocketAddress: &core.SocketAddress{
												Address: "0.0.0.0",
												PortSpecifier: &core.SocketAddress_PortValue{
													PortValue: 15000,
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	// tracing cluster
	// 指向OpenTelemetry Collector，用于链路追踪数据上报，地址取自tracing.domain/tracing.port配置。
	// 类型为STRICT_DNS：地址是域名，由Envoy周期性解析成IP再转发（业务cluster用EDS，
	// 这类基础设施地址则写死在LoadAssignment里）。
	// domain为空（未配置collector）时不下发该cluster：空地址cluster会被Envoy校验拒绝，
	// 有导致整份CDS NACK的风险；未启用tracing的服务（HCM.Tracing为nil）本就不引用它
	tracingDomain := config.GetString("tracing.domain")
	tracingCluster := &cluster.Cluster{
		Name:                 "opentelemetry_collector",
		ConnectTimeout:       durationpb.New(time.Second),
		ClusterDiscoveryType: &cluster.Cluster_Type{Type: cluster.Cluster_STRICT_DNS},
		DnsLookupFamily:      cluster.Cluster_V4_ONLY,
		LbPolicy:             cluster.Cluster_ROUND_ROBIN,
		TypedExtensionProtocolOptions: map[string]*anypb.Any{
			// 与业务cluster"跟随下游协议"不同，这里显式强制HTTP/2：
			// OTLP数据走gRPC上报，上游必须是HTTP/2
			"envoy.extensions.upstreams.http.v3.HttpProtocolOptions": func() *anypb.Any {
				httpProtocolOption := &hpo.HttpProtocolOptions{
					UpstreamProtocolOptions: &hpo.HttpProtocolOptions_ExplicitHttpConfig_{
						ExplicitHttpConfig: &hpo.HttpProtocolOptions_ExplicitHttpConfig{
							ProtocolConfig: &hpo.HttpProtocolOptions_ExplicitHttpConfig_Http2ProtocolOptions{},
						},
					},
				}
				httpProtocolOptionAny, _ := anypb.New(httpProtocolOption)
				return httpProtocolOptionAny
			}(),
		},
		LoadAssignment: &endpoint.ClusterLoadAssignment{
			ClusterName: "opentelemetry_collector",
			Endpoints: []*endpoint.LocalityLbEndpoints{
				{
					LbEndpoints: []*endpoint.LbEndpoint{
						{
							HostIdentifier: &endpoint.LbEndpoint_Endpoint{
								Endpoint: &endpoint.Endpoint{
									Address: &core.Address{
										Address: &core.Address_SocketAddress{
											SocketAddress: &core.SocketAddress{
												Address: tracingDomain,
												PortSpecifier: &core.SocketAddress_PortValue{
													PortValue: config.GetUint32("tracing.port"),
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	// 先汇总基础设施cluster（透传/入站透传/入站/admin），它们与具体业务无关，每个sidecar固定都有；
	// tracing cluster仅在配置了collector地址（domain非空）时随之下发
	clusters = append(clusters, passthroughCluster, inboundPassthroughClusterIpv4,
		localInboundCluster, localAdminCluster)
	if tracingDomain != "" {
		clusters = append(clusters, tracingCluster)
	}

	// 业务相关的clusters：每个可访问的下游服务单独构建
	// 单个服务构建失败只记日志跳过（局部降级），不影响其余服务的配置下发
	for _, svc := range services {
		result, err := buildClusterForService(&svc, me.Mode)
		if err != nil {
			logging.Error(err)
			continue
		}
		clusters = append(clusters, result...)
	}
	return clusters
}

// buildClusterForService 为单个下游服务生成CDS cluster：每个实例组一个outbound cluster + 一个包含全部实例的默认cluster。
//
// 【实例组是什么】InstanceGroup通过标签选择器从服务的全部实例中圈定一批实例，
// 可独立配置熔断与异常摘除策略，并被路由规则指定为流量目标（如灰度组）。
// 因此"每个组一个cluster"意味着：路由到不同组名，就能拿到不同的实例集合与策略。
//
// cluster命名格式 outbound|port|group|service（group为空即默认cluster）。
// cluster均为EDS类型：实例列表由endpoint.go生成同名的ClusterLoadAssignment下发
// （cluster名 = EDS的ServiceName/ClusterName，两侧必须一致）。
// 熔断器阈值来自实例组的CircuitBreakersConfig，异常实例摘除配置来自OutlierDetectionConfig，
// LB策略来自服务Settings；mode区分proxy/proxyless，对LB策略做差异化处理
func buildClusterForService(svc *entity.MicroService, mode string) ([]types.Resource, error) {
	var clusters []types.Resource

	// 上游协议跟随下游（含义见CreateCluster中的同名注释），所有业务cluster共用一份
	httpProtocolOption := &hpo.HttpProtocolOptions{
		UpstreamProtocolOptions: &hpo.HttpProtocolOptions_UseDownstreamProtocolConfig{
			UseDownstreamProtocolConfig: &hpo.HttpProtocolOptions_UseDownstreamHttpConfig{},
		},
	}
	httpProtocolOptionAny, err := anypb.New(httpProtocolOption)
	if err != nil {
		return nil, err
	}
	// 从服务Settings中解析LB策略设置（如一致性哈希ring hash）
	var lbPolicy entity.LbPolicy
	for _, s := range svc.Settings {
		if s.Type == entity.LbPolicyType {
			err := s.GetProperties(&lbPolicy)
			if err != nil {
				logging.Error(err)
			}
			break
		}
	}
	// 若配置了ring hash策略则覆盖默认LB策略，并设置哈希环参数（XX_HASH，最小哈希环1024）。
	// ring hash即一致性哈希：同一请求特征（路由里配置的hash key，如header里的用户ID）
	// 稳定映射到同一实例，实例增减时只有部分key重新分配，适合有本地缓存/会话亲和的场景
	applyLbPolicy := func(c *cluster.Cluster) {
		if lbPolicy.Policy == entity.RingHash {
			c.LbPolicy = cluster.Cluster_RING_HASH
			c.LbConfig = &cluster.Cluster_RingHashLbConfig_{
				RingHashLbConfig: &cluster.Cluster_RingHashLbConfig{
					HashFunction:    cluster.Cluster_RingHashLbConfig_XX_HASH,
					MinimumRingSize: wrapperspb.UInt64(1024),
				},
			}
		}
	}

	// 遍历实例组，每个实例组对应一个独立的outbound cluster（EDS发现，cluster名与EDS的ServiceName一致）
	for _, g := range svc.InstanceGroups {
		c := &cluster.Cluster{
			Name:           utils.GetOutboundTrafficClusterName(svc.Name, svc.Port, g.Name),
			ConnectTimeout: durationpb.New(10 * time.Second),
			// EDS类型：本cluster的实例列表不写死在配置里，而是通过订阅EDS动态获取
			ClusterDiscoveryType: &cluster.Cluster_Type{Type: cluster.Cluster_EDS},
			// 默认LB策略为最小并发请求优先：把请求分给当前活跃请求数最少的实例，
			// 比纯轮询更均衡；proxyless模式与ring hash配置可覆盖（见下方）
			LbPolicy: cluster.Cluster_LEAST_REQUEST,
			// EDS发现的具体配置：实例从哪订阅、按什么名字匹配
			EdsClusterConfig: &cluster.Cluster_EdsClusterConfig{
				EdsConfig: &core.ConfigSource{
					// 走ADS（聚合发现）：EDS与其他xDS资源共用同一条gRPC流按序下发
					ConfigSourceSpecifier: &core.ConfigSource_Ads{
						Ads: &core.AggregatedConfigSource{},
					},
					//必须显示的指定为0，防止超时出现配置不完整的问题
					InitialFetchTimeout: &durationpb.Duration{Seconds: 0},
					ResourceApiVersion:  core.ApiVersion_V3,
				},
				// 订阅用的资源名，必须与endpoint.go生成的ClusterLoadAssignment.ClusterName完全一致，
				// 否则Envoy找不到该cluster的实例列表，路由会因无可用上游而失败
				ServiceName: utils.GetOutboundTrafficClusterName(svc.Name, svc.Port, g.Name),
			},
			CircuitBreakers: &cluster.CircuitBreakers{
				// 熔断阈值取自实例组配置CircuitBreakersConfig（最大连接/排队/并发请求/并发重试四项），
				// 未配置的项默认不限制（maxUint32Nums），同时生成DEFAULT与HIGH两档阈值：
				// Envoy按请求优先级取用对应档位，HIGH档服务高优先级请求（如gRPC）
				Thresholds: []*cluster.CircuitBreakers_Thresholds{
					{
						// DEFAULT档（未指定Priority时默认DEFAULT），即普通HTTP请求的额度
						MaxConnections:     wrapperspb.UInt32(utils.GetOrDefault(g.CircuitBreakersConfig.MaxConnections, maxUint32Nums)),
						MaxPendingRequests: wrapperspb.UInt32(utils.GetOrDefault(g.CircuitBreakersConfig.MaxPendingRequests, maxUint32Nums)),
						MaxRequests:        wrapperspb.UInt32(utils.GetOrDefault(g.CircuitBreakersConfig.MaxRequests, maxUint32Nums)),
						MaxRetries:         wrapperspb.UInt32(utils.GetOrDefault(g.CircuitBreakersConfig.MaxRetries, maxUint32Nums)),
						// 在实例的runtime/metrics里暴露各阈值剩余额度，便于观测熔断水位
						TrackRemaining: true,
					},
					{
						// HIGH档：阈值与DEFAULT相同，仅档位不同
						MaxConnections:     wrapperspb.UInt32(utils.GetOrDefault(g.CircuitBreakersConfig.MaxConnections, maxUint32Nums)),
						MaxPendingRequests: wrapperspb.UInt32(utils.GetOrDefault(g.CircuitBreakersConfig.MaxPendingRequests, maxUint32Nums)),
						MaxRequests:        wrapperspb.UInt32(utils.GetOrDefault(g.CircuitBreakersConfig.MaxRequests, maxUint32Nums)),
						MaxRetries:         wrapperspb.UInt32(utils.GetOrDefault(g.CircuitBreakersConfig.MaxRetries, maxUint32Nums)),
						TrackRemaining:     true,
						Priority:           core.RoutingPriority_HIGH,
					},
				},
			},
			// 异常实例摘除（被动健康检查）：连续5xx/网关错误的实例会被临时摘除，见getOutlierDetection
			OutlierDetection: getOutlierDetection(g.OutlierDetectionConfig),
			CommonLbConfig: &cluster.Cluster_CommonLbConfig{
				// HealthyPanicThreshold=0：关闭panic mode。Envoy默认在健康实例占比过低时会把
				// 不健康实例也拉回来服务（宁可不健康也不报503），设0即关闭该回退，
				// 实例全被摘除时直接拒绝请求，配合主动摘除让故障实例真正隔离
				HealthyPanicThreshold: &xdstype.Percent{Value: 0},
				// 启用locality加权LB：同一优先级内按各locality的LoadBalancingWeight
				// （endpoint.go中设为该locality实例数）分配流量，与就近路由配合（见callback.go）
				LocalityConfigSpecifier: &cluster.Cluster_CommonLbConfig_LocalityWeightedLbConfig_{
					LocalityWeightedLbConfig: &cluster.Cluster_CommonLbConfig_LocalityWeightedLbConfig{},
				},
			},
			TypedExtensionProtocolOptions: map[string]*anypb.Any{
				"envoy.extensions.upstreams.http.v3.HttpProtocolOptions": httpProtocolOptionAny,
			},
		}

		//上面的配置默认给proxy使用的，proxyless需要做一定修改：
		//proxyless SDK（如gRPC客户端）只支持基础的轮询LB，不支持LEAST_REQUEST，
		//因此回退到ROUND_ROBIN；若用户显式配置了ring hash，下方applyLbPolicy仍会覆盖
		if mode == entity.ServiceModeProxyless {
			c.LbPolicy = cluster.Cluster_ROUND_ROBIN
		}
		applyLbPolicy(c)
		clusters = append(clusters, c)

	}
	// 默认cluster（group为空，命名 outbound|port||service）：包含服务全部实例，
	// 未命中任何路由规则时的兜底转发目标。
	// 与实例组cluster的差异：熔断四项全部不限流（极大值）、不配置OutlierDetection——
	// 精细的熔断/摘除策略挂在各实例组cluster上，兜底链路只保证连通
	global := &cluster.Cluster{
		Name:                 utils.GetOutboundTrafficClusterName(svc.Name, svc.Port, ""),
		ConnectTimeout:       durationpb.New(10 * time.Second),
		ClusterDiscoveryType: &cluster.Cluster_Type{Type: cluster.Cluster_EDS},
		LbPolicy:             cluster.Cluster_LEAST_REQUEST,
		EdsClusterConfig: &cluster.Cluster_EdsClusterConfig{
			EdsConfig: &core.ConfigSource{
				ConfigSourceSpecifier: &core.ConfigSource_Ads{
					Ads: &core.AggregatedConfigSource{},
				},
				InitialFetchTimeout: &durationpb.Duration{Seconds: 0},
				ResourceApiVersion:  core.ApiVersion_V3,
			},
			// 与endpoint.go默认CLA（group为空）的ClusterName一致，实例为全部健康实例
			ServiceName: utils.GetOutboundTrafficClusterName(svc.Name, svc.Port, ""),
		},
		CircuitBreakers: &cluster.CircuitBreakers{
			Thresholds: []*cluster.CircuitBreakers_Thresholds{
				{
					MaxConnections:     wrapperspb.UInt32(maxUint32Nums),
					MaxPendingRequests: wrapperspb.UInt32(maxUint32Nums),
					MaxRequests:        wrapperspb.UInt32(maxUint32Nums),
					MaxRetries:         wrapperspb.UInt32(maxUint32Nums),
					TrackRemaining:     true,
				},
				{
					MaxConnections:     wrapperspb.UInt32(maxUint32Nums),
					MaxPendingRequests: wrapperspb.UInt32(maxUint32Nums),
					MaxRequests:        wrapperspb.UInt32(maxUint32Nums),
					MaxRetries:         wrapperspb.UInt32(maxUint32Nums),
					TrackRemaining:     true,
					Priority:           core.RoutingPriority_HIGH,
				},
			},
		},
		CommonLbConfig: &cluster.Cluster_CommonLbConfig{
			LocalityConfigSpecifier: &cluster.Cluster_CommonLbConfig_LocalityWeightedLbConfig_{
				LocalityWeightedLbConfig: &cluster.Cluster_CommonLbConfig_LocalityWeightedLbConfig{},
			},
			HealthyPanicThreshold: &xdstype.Percent{Value: 0},
		},
		TypedExtensionProtocolOptions: map[string]*anypb.Any{
			"envoy.extensions.upstreams.http.v3.HttpProtocolOptions": httpProtocolOptionAny,
		},
	}

	//上面的配置默认给proxy使用的，proxyless需要做一定修改（原因见实例组循环内的同名注释）
	if mode == entity.ServiceModeProxyless {
		global.LbPolicy = cluster.Cluster_ROUND_ROBIN
	}
	applyLbPolicy(global)
	clusters = append(clusters, global)
	return clusters, nil
}

// getOutlierDetection 将实例组的异常实例摘除配置转为Envoy OutlierDetection。
//
// 【OutlierDetection是什么】被动健康检查：Envoy统计已转发请求的真实结果，
// 把连续出错或错误率超标的实例临时"弹出"（摘除，不再参与LB），到期自动恢复，
// 与主动探测式健康检查互补。其中enforcing字段是弹出执行概率：0=检测到也不弹出
// （等于关闭该项），100=100%执行弹出。
//
// config为空时默认关闭所有弹出机制（各项enforcing置0）；某项配置>0才启用对应检测并将enforcing置为100（全部生效）
func getOutlierDetection(config entity.OutlierDetectionConfig) *cluster.OutlierDetection {
	//默认关闭所有异常值检测弹出机制
	result := &cluster.OutlierDetection{
		EnforcingConsecutive_5Xx:               wrapperspb.UInt32(0),
		EnforcingSuccessRate:                   wrapperspb.UInt32(0),
		EnforcingConsecutiveGatewayFailure:     wrapperspb.UInt32(0),
		EnforcingConsecutiveLocalOriginFailure: wrapperspb.UInt32(0),
		EnforcingLocalOriginSuccessRate:        wrapperspb.UInt32(0),
		EnforcingFailurePercentage:             wrapperspb.UInt32(0),
		EnforcingFailurePercentageLocalOrigin:  wrapperspb.UInt32(0),
	}

	// 连续N次5xx即弹出该实例
	if config.Consecutive5xx > 0 {
		result.Consecutive_5Xx = wrapperspb.UInt32(uint32(config.Consecutive5xx))
		result.EnforcingConsecutive_5Xx = wrapperspb.UInt32(100)
	}
	// 连续N次网关错误（502/503/504等）即弹出，与5xx分开统计、单独开关
	if config.ConsecutiveGatewayFailure > 0 {
		result.ConsecutiveGatewayFailure = wrapperspb.UInt32(uint32(config.ConsecutiveGatewayFailure))
		result.EnforcingConsecutiveGatewayFailure = wrapperspb.UInt32(100)
	}
	// 集群最大可摘除实例比例，防止大面积故障时实例被全量摘除引发雪崩
	if config.MaxEjectionPercent > 0 {
		result.MaxEjectionPercent = wrapperspb.UInt32(uint32(config.MaxEjectionPercent))
	}
	// 统计分析周期：每隔多久做一次弹出/恢复判定
	if config.Interval > 0 {
		result.Interval = durationpb.New(time.Duration(config.Interval) * time.Second)
	}
	// 基础弹出时长：实例被弹出后隔离多久，重复弹出时时长按次数指数增长
	if config.BaseEjectionTime > 0 {
		result.BaseEjectionTime = durationpb.New(time.Duration(config.BaseEjectionTime) * time.Second)
	}
	// 错误率摘除：实例错误率超过该百分比阈值时弹出（按周期统计）
	if config.FailurePercentageThreshold > 0 {
		result.FailurePercentageThreshold = wrapperspb.UInt32(uint32(config.FailurePercentageThreshold))
		result.EnforcingFailurePercentage = wrapperspb.UInt32(100)
	}
	// 错误率统计的最小请求样本数：样本不足时不判定，避免小流量实例被误摘
	if config.FailurePercentageRequestVolume > 0 {
		result.FailurePercentageRequestVolume = wrapperspb.UInt32(uint32(config.FailurePercentageRequestVolume))
	}
	// 错误率摘除生效的集群最小实例数：实例太少时跳过该策略
	if config.FailurePercentageMinimumHosts > 0 {
		result.FailurePercentageMinimumHosts = wrapperspb.UInt32(uint32(config.FailurePercentageMinimumHosts))
	}

	return result
}
