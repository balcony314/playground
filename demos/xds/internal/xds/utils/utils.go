package utils

import (
	"fmt"
	"slices"

	"github.com/balcony314/xds/internal/entity"
	"github.com/balcony314/xds/internal/service"
	"github.com/balcony314/xds/pkg/logging"
	core "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	stdoutAccesslog "github.com/envoyproxy/go-control-plane/envoy/extensions/access_loggers/stream/v3"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"
)

// Direction 流量方向标识，用于区分出站/入站资源
type Direction string

const (
	DirectionOutbound Direction = "outbound"
	DirectionInbound  Direction = "inbound"
)

// GetOutboundTrafficClusterName 生成出站cluster名，格式：outbound|port|group|service。
// 出站cluster代表"我作为调用方要访问的下游服务"；路由规则、EDS、tcp_proxy等各处
// 生成/引用的cluster名都统一经本函数拼出，保证全局一致。
// 第4段为服务名（可据此从cluster名反查服务）：api/xds/callback.go的就近路由改写
// 按此约定解析cluster名反查服务配置——生成方与解析方的约定必须一致，改动需两侧同步；
// group为实例组名，为空表示包含全部实例的默认cluster
func GetOutboundTrafficClusterName(service string, port int, group string) string {
	return fmt.Sprintf("outbound|%d|%s|%s", port, group, service)
}

// GetInboundTrafficClusterName 生成入站cluster名，格式：inbound|port|protocol|service。
// 入站cluster的endpoint是本机业务进程：proxy模式下Envoy的virtualInbound监听（15006）
// 收到入站连接后，最终路由到该cluster把流量转交给本机进程。
// 与出站命名格式对齐，第4段同为服务名
func GetInboundTrafficClusterName(service string, port int, protocol string) string {
	return fmt.Sprintf("inbound|%v|%s|%s", port, protocol, service)
}

// GetRouteName 生成RDS路由配置名，格式：service:port。
// 是xDS资源名字引用链的一环：listener的HCM在Rds.route_config_name里写这个名字，
// 指向router.go生成的同名RouteConfiguration；route内再写cluster名指向CDS的Cluster，
// Cluster经EDS拿实例列表——整条链路全部靠字符串名字对齐
func GetRouteName(service string, port int) string {
	return fmt.Sprintf("%v:%v", service, port)
}

// ServiceHash 实现go-control-plane的NodeHash接口，作为xDS响应缓存（snapshot）的节点维度的key。
// go-control-plane为每个接入节点维护一份独立快照，本类型决定"Envoy节点→快照缓存key"
// 的映射规则（见ID方法），在NewSnapshotCache创建缓存时注入
type ServiceHash struct{}

// ID 由Envoy节点信息生成缓存key，格式：nodeId@mode。
// node.Id即Envoy/bootstrap里配置的服务名，与updateSnapshot写入快照用的
// "服务名@模式"key一一对应，节点订阅时据此命中自己的快照（proxy与proxyless
// 两套配置因mode后缀不同而天然隔离）。
// mode根据节点metadata中的type字段判定：type=proxy则为proxy模式（Sidecar），
// 否则一律视为proxyless——proxyless的节点配置由用户自行填写、无法掌控，只能排除法判定
func (ServiceHash) ID(node *core.Node) string {
	if node == nil {
		return ""
	}

	var mode string
	//envoy统一配置的元数据里都有type字段，proxyless由于配置由用户填写，无法掌控，因此凡是不等于proxy的都视为proxyless
	if node.Metadata.AsMap()["type"] == "proxy" {
		mode = entity.ServiceModeProxy
	} else {
		mode = entity.ServiceModeProxyless
	}
	return fmt.Sprintf("%s@%s", node.Id, mode)
}

// serviceLister 服务列表读取函数，默认走 service 层缓存；测试可注入
var serviceLister = func() ([]entity.MicroService, error) {
	return service.Storage().ListAllMicroServices()
}

// SetServiceLister 替换服务列表读取函数（测试注入用）
func SetServiceLister(f func() ([]entity.MicroService, error)) { serviceLister = f }

// visibilityMatches 判断我的服务名 me 是否命中目标服务的可见性白名单。
// 白名单为空（nil）表示对全部服务可见——这是"零配置即全互通"的默认策略，
// 只有显式配置了白名单才收敛可见范围
func visibilityMatches(whitelist []string, me string) bool {
	if len(whitelist) == 0 {
		return true
	}
	return slices.Contains(whitelist, me)
}

// CanAccessMe 返回"谁可以访问我"。入参 myVisibility 是我（目标服务）的可见性白名单；
// 上游服务 svc 可访问我，当且仅当我的白名单包含 svc 或我的白名单为空（全开放）。
// 用途：服务自身变化时（HandleServiceChange 的 broadcast 分支）找出需级联刷新快照的上游调用者
func CanAccessMe(myVisibility []string) ([]entity.MicroService, error) {
	microServices, err := serviceLister()
	if err != nil {
		return nil, fmt.Errorf("列出全部服务失败: %w", err)
	}
	var result []entity.MicroService
	for _, microService := range microServices {
		if visibilityMatches(myVisibility, microService.Name) {
			result = append(result, microService)
		}
	}
	return result, nil
}

// MeCanAccess 返回"我可以访问谁"。me 是我的服务名；
// 下游服务对我可见，当且仅当它的白名单包含我、或它的白名单为空（全开放）。
// 结果作为生成 CDS/RDS/EDS/LDS 的输入——访问控制直接落在配置面：看不到的服务不出现在配置里
func MeCanAccess(me string) ([]entity.MicroService, error) {
	microServices, err := serviceLister()
	if err != nil {
		return nil, fmt.Errorf("列出全部服务失败: %w", err)
	}
	var result []entity.MicroService
	for _, microService := range microServices {
		if visibilityMatches(microService.ServiceVisibility, me) {
			result = append(result, microService)
		}
	}
	return result, nil
}

// GetOrDefault int转uint32，value<=0（未配置）时返回默认值
// 常用于熔断器阈值等配置项的兜底
func GetOrDefault(value int, defaultValue uint32) uint32 {
	if value <= 0 {
		return defaultValue
	}
	return uint32(value)
}

// GetOrDefaultInt64 int转int64，value<=0（未配置）时返回默认值
func GetOrDefaultInt64(value int, defaultValue int64) int64 {
	if value <= 0 {
		return defaultValue
	}
	return int64(value)
}

// CreateAccesslog 生成stdout JSON格式的访问日志配置（Any类型，供HCM的AccessLog字段
// 直接引用）。字段包括请求方法/路径/协议、响应码、上下游地址、耗时、路由名等，
// 供sidecar向标准输出打结构化访问日志；%XXX%为Envoy的命令操作符，运行时替换为实际值
func CreateAccesslog() *anypb.Any {
	format, _ := structpb.NewStruct(map[string]interface{}{
		"time":                             "%START_TIME(%Y/%m/%dT%H:%M:%S%z %s)%",
		"method":                           "%REQ(:METHOD)%",
		"path":                             "%REQ(X-ENVOY-ORIGINAL-PATH?:PATH)%",
		"protocol":                         "%PROTOCOL%",
		"response_code":                    "%RESPONSE_CODE%",
		"forwarded_for":                    "%REQ(X-FORWARDED-FOR)%",
		"upstream_host":                    "%UPSTREAM_HOST%",
		"upstream_local_address":           "%UPSTREAM_LOCAL_ADDRESS%",
		"upstream_remote_address":          "%UPSTREAM_REMOTE_ADDRESS%",
		"downstream_local_address":         "%DOWNSTREAM_LOCAL_ADDRESS%",
		"downstream_remote_address":        "%DOWNSTREAM_REMOTE_ADDRESS%",
		"downstream_direct_remote_address": "%DOWNSTREAM_DIRECT_REMOTE_ADDRESS%",
		"duration":                         "%DURATION%",
		"filter_chain_name":                "%FILTER_CHAIN_NAME%",
		"route_name":                       "%ROUTE_NAME%",
	})
	aLog := &stdoutAccesslog.StdoutAccessLog{
		AccessLogFormat: &stdoutAccesslog.StdoutAccessLog_LogFormat{
			LogFormat: &core.SubstitutionFormatString{
				Format: &core.SubstitutionFormatString_JsonFormat{
					JsonFormat: format,
				},
			},
		},
	}
	aLogA, err := anypb.New(aLog)
	if err != nil {
		logging.Error(err)
		return nil
	}

	return aLogA
}
