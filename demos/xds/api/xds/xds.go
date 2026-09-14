// Package xds 基于go-control-plane封装的xDS控制面服务：
// 维护服务级配置快照(Snapshot)、响应服务变更重新生成快照、
// 并在下发前按调用方位置改写EDS实现就近路由(见callback.go)
package xds

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"github.com/envoyproxy/go-control-plane/pkg/resource/v3"
	"github.com/envoyproxy/go-control-plane/pkg/server/v3"
	test "github.com/envoyproxy/go-control-plane/pkg/test"
	"google.golang.org/grpc"

	"github.com/balcony314/xds/internal/entity"
	"github.com/balcony314/xds/internal/errs"
	"github.com/balcony314/xds/internal/service"
	"github.com/balcony314/xds/internal/xds"
	"github.com/balcony314/xds/internal/xds/utils"
	"github.com/balcony314/xds/pkg/config"
	"github.com/balcony314/xds/pkg/logging"
	"github.com/balcony314/xds/pkg/probe"
)

// XDSServer xDS 控制面服务,基于 Envoy 官方的 go-control-plane 库封装。
//
// 【xDS 是什么】Envoy sidecar(或 proxyless SDK,如 gRPC)与控制面之间的配置发现
// 协议统称,"x" 代指具体资源类型,本项目用到四类:
//   - LDS(Listener)  : 监听器,哪个端口接流量、按什么规则分流、挂哪些过滤器
//   - RDS(Route)      : HTTP 路由规则,按 path/header/参数匹配,决定请求发往哪个 cluster
//   - CDS(Cluster)    : 上游服务抽象,负载均衡策略、熔断阈值、协议协商等
//   - EDS(Endpoint)   : cluster 的实例列表,IP:Port + region/zone 归属 + 故障转移优先级
//
// 【工作模型】控制面并不直接"推"配置,而是按节点维护一份 Snapshot(该节点可见的
// 全量四类资源);节点订阅时、或 Snapshot 更新时,由 go-control-plane 把配置组装成
// DiscoveryResponse 写回 gRPC 流,Envoy 收到后 ACK/NACK 完成版本协商。
//
// 本结构体的职责只有两件:
//  1. 服务数据变化时重新生成 Snapshot 并写入缓存(HandleServiceChange -> updateSnapshot)
//  2. 响应下发前按调用方位置改写 EDS(见 callback.go 的 MatrixMeshCallback)
type XDSServer struct {
	// cache 配置快照缓存,key 为"服务名@模式"(如 service-a@proxy),
	// 与 Envoy 连接时上报的 node.id 一一对应(由 utils.ServiceHash 负责映射),
	// 同一服务的 proxy / proxyless 两套配置以此天然隔离。
	// 调用 SetSnapshot 写入新快照时,库会立刻向已订阅该 key 的节点推送
	cache cache.SnapshotCache
	// versionNum 快照版本号全局自增计数器(标准库原子类型,零值即可用)。
	// xDS 协议靠版本号感知变化:对同一节点,新版本号必须严格递增,否则 Envoy
	// 认为配置无变化而直接忽略。generateSnapshot 用"RFC3339时间戳/自增序号"
	// 组合出版本号,保证同秒内多次更新也不重复
	versionNum atomic.Uint64
	// server gRPC server 实例(当前由 go-control-plane 的 test 包托管启动,此字段预留)
	server *grpc.Server
}

// Initialize 初始化 xDS server:创建快照缓存。
//
// NewSnapshotCache 三个参数:
//   - ads=false:不启用严格的 ADS 顺序下发(各资源类型独立 watch);
//     配置自洽性改由 updateSnapshot 里的 snapshot.Consistent() 校验兜底
//   - utils.ServiceHash{}:节点哈希函数,把 Envoy 上报的 node 映射为缓存 key
//     (node.id@mode,详见 ServiceHash.ID 的注释)
//   - logging.GetLogger():库内部日志输出。
//     logging.Logger 含 Debugf/Infof/Warnf/Errorf,满足 go-control-plane
//     log.Logger 接口约束,无需额外适配器
func (s *XDSServer) Initialize(ctx context.Context) error {
	s.cache = cache.NewSnapshotCache(false, utils.ServiceHash{}, logging.GetLogger())
	return nil
}

// generateSnapshot 为单个服务生成一份完整的 xDS 配置快照(含 CDS/RDS/LDS/EDS 四类资源)。
//
// 【视角】me 是配置的主人(即"谁订阅这份快照"),microServices 是 me 有权限访问的
// 全部下游服务(基于可见性白名单过滤)。快照回答的问题是:"me 作为调用方,调任何人
// 分别需要什么配置" + "流量打到 me 自己时怎么处理"。上游(谁调用 me)不需要出现在
// me 的配置里——那是写在对方快照里的。
//
// 【版本号】每份快照必须带版本字符串,且对同一节点严格递增,否则 Envoy 不感知更新。
// 用时间戳+自增序号组合,避免同秒内多次更新时版本号重复导致 Envoy 丢弃配置。
func (s *XDSServer) generateSnapshot(me *entity.MicroService) (*cache.Snapshot, error) {
	// 基于可见性白名单计算"我可以访问谁",可见的下游才会出现在配置里——
	// 访问控制直接体现在下发的配置中:看不到的服务,配置里根本没有它
	downStreamServices, err := utils.MeCanAccess(me.Name)
	if err != nil {
		return nil, fmt.Errorf("计算 %s 可访问的下游服务失败: %w", me.Name, err)
	}

	// 版本号必须严格递增且全局唯一,用时间戳+自增序号组合,
	// 避免同秒内多次更新时版本号重复导致 Envoy 不感知
	versionLocal := time.Now().Format(time.RFC3339) + "/" + strconv.FormatUint(s.versionNum.Add(1), 10)

	// 依次生成四类资源(实现在 internal/xds/ 下,均为纯函数):
	// EDS:每个下游 cluster 的实例列表(带 locality/priority,就近路由的数据基础)
	eds, err := xds.CreateEndpoints(me, downStreamServices)
	if err != nil {
		return nil, fmt.Errorf("生成 %s 的 endpoints 失败: %w", me.Name, err)
	}

	// LDS:listener 集合。proxy 模式含 virtualOutbound(15001)/virtualInbound(15006)/
	// Admin(15020) + 每个下游一个 outbound listener;proxyless 模式为 ApiListener 形态
	lds, err := xds.CreateListeners(me, downStreamServices)
	if err != nil {
		return nil, fmt.Errorf("生成 %s 的 listeners 失败: %w", me.Name, err)
	}
	// CDS:每个下游一个 cluster(命名 outbound|port|group|service)+ 自己的 inbound cluster
	cds := xds.CreateCluster(me, downStreamServices)
	// RDS:每个下游一份路由配置(命名 service:port,由 matcher 按协议生成匹配规则)
	rds := xds.CreateRoute(me, downStreamServices)

	// 把四类资源打包成一份快照。四者之间靠字符串名字互相引用串联:
	// listener 的 HCM 写着 route_config_name -> RouteConfiguration;
	// route 写着 cluster 名 -> Cluster;Cluster 是 EDS 类型 -> ClusterLoadAssignment
	snap, err := cache.NewSnapshot(versionLocal,
		map[resource.Type][]types.Resource{
			resource.ClusterType:  cds,
			resource.RouteType:    rds,
			resource.ListenerType: lds,
			resource.EndpointType: eds,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("打包 %s 的快照失败: %w", me.Name, err)
	}
	return snap, nil
}

// HandleServiceChange 处理一个服务的变更事件(实例增减、配置修改等),
// 是整个控制面数据流的汇合点:Nacos 订阅回调 -> workqueue -> worker -> 本函数。
//
// 【broadcast 语义】broadcast=true 表示"本次是服务自身的变化"。此时不只是本服务的
// 配置要刷新,它的所有上游调用者的 EDS 里也包含本服务的 endpoint,同样需要刷新。
// 做法:CanAccessMe 找出"谁可以访问我",逐个入队让它们各自重新生成快照;
// 入队时 broadcast=false,即级联只扩散一层,避免变更沿依赖链无限传播。
// (服务被删除属于正常情况如注销,直接忽略)
func (s *XDSServer) HandleServiceChange(broadcast bool, serviceName string) error {
	t1 := time.Now()
	logging.With("broadcast", broadcast, "service", serviceName).Infof("handle service change")
	defer func() {
		logging.With("broadcast", broadcast, "service", serviceName, "time_taken", time.Since(t1).String()).Infof("handle service change end")
	}()

	// 从本地缓存读取服务元数据(缓存由 Nacos 配置中心订阅回调维护)
	microService, err := service.Storage().GetServiceInfo(serviceName)
	if err != nil {
		// 服务已被删除属于正常情况(如注销),直接忽略本次变更。
		// 存储层以 fmt.Errorf("%w") 包装哨兵错误,须用 errors.Is 匹配
		if errors.Is(err, errs.ErrServiceNotFound) {
			logging.With("broadcast", broadcast, "service", serviceName).Warnf("service not found")
			return nil
		}
		return fmt.Errorf("读取服务 %s 信息失败: %w", serviceName, err)
	}
	// broadcast语义:某服务自身配置/实例发生变化时,其下游调用者的配置也会受影响
	//(调用者的EDS里包含该服务的endpoint)。因此找出"谁可以访问我"的所有上游服务,
	// 将它们逐个入队重新生成快照,但入队时broadcast=false,避免变更沿依赖链无限扩散
	if broadcast {
		upstreamServices, err := utils.CanAccessMe(microService.ServiceVisibility)
		if err != nil {
			return fmt.Errorf("计算可访问 %s 的上游服务失败: %w", serviceName, err)
		}
		for _, upStreamServiceName := range upstreamServices {
			service.Storage().Enqueue(false, upStreamServiceName.Name)
		}
	}

	// 按服务的接入模式生成快照:
	//   - Mode 明确为 proxy 或 proxyless:只生成一份
	//   - Mode 为空(兼容模式):复制两份,分别按 proxy / proxyless 各生成一份。
	//     proxy 与 proxyless 的 listener/cluster 配置差异较大,且节点 id 带模式后缀,
	//     两份快照 key 不同(service@proxy / service@proxyless),互不干扰
	switch microService.Mode {
	case entity.ServiceModeProxy, entity.ServiceModeProxyless:
		//TODO: 经过团队沟通讨论，目前所有服务都使用兼容模式，生成2份配置，mode字段默认为空
		err = s.updateSnapshot(microService)
		if err != nil {
			return fmt.Errorf("更新服务 %s 快照失败: %w", serviceName, err)
		}
	default:
		//proxy=all或者为空时，使用兼容模式，即同时生成2份配置
		//生成proxy模式配置
		svcProxy := microService.Copy()
		svcProxy.Mode = entity.ServiceModeProxy
		err = s.updateSnapshot(svcProxy)
		if err != nil {
			return fmt.Errorf("更新服务 %s(proxy) 快照失败: %w", serviceName, err)
		}

		//生成proxyless模式配置
		svcProxyless := microService.Copy()
		svcProxyless.Mode = entity.ServiceModeProxyless
		err = s.updateSnapshot(svcProxyless)
		if err != nil {
			return fmt.Errorf("更新服务 %s(proxyless) 快照失败: %w", serviceName, err)
		}
	}

	return nil
}

// updateSnapshot 为指定服务生成新快照并写入缓存,触发向订阅方推送。
//
// svc.Mode 必须是 proxy 或 proxyless 之一;兼容模式(空 Mode)由上层
// HandleServiceChange 拆成两份后再分别调用本函数。
func (s *XDSServer) updateSnapshot(svc *entity.MicroService) error {
	//校验mode是否配置正确，到了这一步，已经不存在mode等于兼容模式的的了，已经在上层做了转换
	if svc.Mode != entity.ServiceModeProxy && svc.Mode != entity.ServiceModeProxyless {
		return fmt.Errorf("service xds output mode invalid: %s", svc.Mode)
	}
	snapshot, err := s.generateSnapshot(svc)
	if err != nil {
		return err
	}
	//一致性校验，确保快照内部引用的cluster/route/listener等资源自洽，避免下发残缺配置。
	//四类资源靠名字互相引用(见 generateSnapshot 注释),任何一环名字对不上,
	//Envoy 侧会因找不到被引用的资源而 NACK 或路由失败,所以必须在写入前拦下
	if err := snapshot.Consistent(); err != nil {
		logging.Error(err)
		return fmt.Errorf("快照一致性校验失败: %w", err)
	}
	//快照key为"服务名@模式"，与ServiceHash生成的node id对应；
	//SetSnapshot会触发cache向已订阅该key的Envoy推送增量更新
	if err := s.cache.SetSnapshot(context.Background(), fmt.Sprintf("%s@%s", svc.Name, svc.Mode), snapshot); err != nil {
		logging.Errorf("error pushing snapshot: %v", err)
		return fmt.Errorf("写入 %s@%s 快照失败: %w", svc.Name, svc.Mode, err)
	}
	logging.Debugf("updated snapshot for %s", fmt.Sprintf("%s@%s", svc.Name, svc.Mode))
	return nil
}

// Run 启动 xDS gRPC 服务并驱动配置同步,是本服务的主循环入口(由 main 的 goroutine 调用)。
//
// 启动顺序及理由:
//  1. 先起 gRPC/REST服务——"先开门":Envoy 可以随时连入挂起 watch,
//     即使快照尚未生成也不丢连接,SetSnapshot 之后会立刻响应;
//     端口从配置读取(xds.grpcPort / xds.restPort),不与web端口冲突
//  2. Storage().Init 全量初始化——"再上菜":拉取全部服务建缓存,并为每个服务
//     执行一次 HandleServiceChange(broadcast=false),建立所有节点的快照基线
//  3. 就绪标记——全量完成后才标记,避免 K8s 在配置未就绪时切流量
//  4. Storage().Watch——注册增量回调并启动 worker 消费队列,此后 Nacos 侧的
//     任何变更事件都会驱动 HandleServiceChange 增量更新快照
//  5. <-ctx.Done() 阻塞常驻(当前传入 context.Background,即永不主动退出)
func (s *XDSServer) Run() error {
	ctx := context.Background()
	cb := &MatrixMeshCallback{}
	// NewServer 把"缓存 + 回调"包装成实现了全部 xDS gRPC 服务接口的对象
	// (ADS + 独立的 CDS/EDS/RDS/SRDS/LDS/SDS/RTDS,同一个对象)
	srv := server.NewServer(ctx, s.cache, cb)
	//端口从配置读取:grpcPort为gRPC discovery端口（Envoy直连），
	//restPort为REST gateway端口（便于调试）
	grpcPort := config.GetInt("xds.grpcPort")
	restPort := config.GetInt("xds.restPort")
	go test.RunManagementServer(ctx, srv, uint(grpcPort))
	go test.RunManagementGateway(ctx, srv, uint(restPort))

	// 首次全量初始化
	err := service.Storage().Init(s.HandleServiceChange)
	if err != nil {
		logging.Fatalf("%v", err)
	}

	// 全量初始化完成后标记健康与就绪（接回 Task 11 留下的 TODO）：
	// 探针路由已挂在 admin server 的 /-/ready 与 /-/healthy 上，
	// 此处置位后编排系统才会把流量切过来，避免配置未就绪时提前接流
	probe.Healthy()
	probe.Ready()
	logging.Infof("xds server finished initializing")

	// 增量部分，注册回调函数
	//启动worker消费队列，后续Nacos侧服务/实例变更事件会触发HandleServiceChange增量更新快照
	err = service.Storage().Watch(s.HandleServiceChange)
	if err != nil {
		logging.Fatalf("%v", err)
	}
	//阻塞直到context取消（当前使用context.Background，即常驻运行）
	<-ctx.Done()
	return nil
}
