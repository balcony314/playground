package entity

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

const (
	// ServiceModeProxy sidecar代理模式，流量经过envoy等代理转发
	ServiceModeProxy = "proxy"
	// ServiceModeProxyless 无代理模式，业务直连（如gRPC xDS）
	ServiceModeProxyless = "proxyless"
)

// Settings 服务级网格设置列表，按Type区分就近路由、健康检查等配置项
type Settings []MeshSetting

// Get 按设置类型取出配置并反序列化到obj，未配置时返回错误
func (s Settings) Get(name SettingType, obj interface{}) error {
	for _, setting := range s {
		if setting.Type == name {
			err := setting.GetProperties(obj)
			if err != nil {
				return err
			}
			return nil
		}
	}
	return fmt.Errorf("setting %s not found in service", name)
}

// MicroService 服务元数据，整体序列化为JSON后存于Nacos配置中心，是网格配置计算的输入源
type MicroService struct {
	// Name 服务名，同时作为Nacos配置的dataId
	Name string `json:"name"`
	// Desc 服务描述
	Desc string `json:"desc"`
	// Protocol 服务协议（如http、grpc），影响xDS listener生成
	Protocol string `json:"protocol"`
	// Port 服务端口，与Protocol共同确定监听配置
	Port int `json:"port"`
	// VIP 虚拟IP（预留字段），proxy/proxyless的outbound listener以其作为逻辑监听地址
	VIP string `json:"vip"`
	// Mode 接入模式：proxy（sidecar代理）/proxyless（无代理直连）
	Mode string `json:"mode"`
	// ServiceVisibility 服务可见性白名单：本服务对哪些服务名可见。
	// nil 或空列表表示对全部服务可见（开源场景默认全开）
	ServiceVisibility []string `json:"serviceVisibility"`
	// InstanceGroups 实例分组：按标签选择器将实例划分成多个逻辑集群，
	// 分别配置熔断、异常实例摘除等策略，也是路由规则的目标
	InstanceGroups []InstanceGroup `json:"instanceGroups"`
	// Ratelimits 限流规则列表
	Ratelimits []Ratelimit `json:"ratelimits"`
	// Routers 路由规则列表（含匹配条件、目标分组、重试、超时）
	Routers []Router `json:"routers"`
	// Settings 网格设置列表（就近路由、负载均衡、健康检查、链路追踪等）
	Settings Settings `json:"settings"`
}

// Copy 深拷贝服务元数据：经JSON序列化往返实现完全独立的副本，
// 避免调用方修改（如临时调整配置计算）影响缓存中的原始数据
func (s *MicroService) Copy() *MicroService {
	var newObj MicroService
	data, _ := json.Marshal(s)
	json.Unmarshal(data, &newObj)
	return &newObj
}

// InstanceGroup 实例分组：通过标签选择器从服务实例中圈定一组实例，
// 可单独配置熔断与异常摘除策略，并作为路由规则的目标集群
type InstanceGroup struct {
	Id                     int                       `json:"id"`                     //  id
	Name                   string                    `json:"name"`                   //  名称
	ServiceMeshId          int                       `json:"serviceMeshId"`          //  mesh id
	Desc                   string                    `json:"desc"`                   //  规则描述
	Selector               InstanceGroupSelectorList `json:"selector"`               //  标签选择器
	CircuitBreakersConfig  CircuitBreakersConfig     `json:"circuitBreakersConfig"`  //  熔断设置
	OutlierDetectionConfig OutlierDetectionConfig    `json:"outlierDetectionConfig"` //  异常值设置
	Instances              []Instance                `json:"instances"`
}

// InstanceGroupSelectorList 实例分组选择器列表，命中全部选择器的实例归属该分组
type InstanceGroupSelectorList []Selector

// CircuitBreakersConfig 熔断配置，限制集群的最大连接/排队/请求/重试数
type CircuitBreakersConfig struct {
	MaxConnections     int `json:"maxConnections"`
	MaxPendingRequests int `json:"maxPendingRequests"`
	MaxRequests        int `json:"maxRequests"`
	MaxRetries         int `json:"maxRetries"`
}

// OutlierDetectionConfig 异常实例摘除配置，连续5xx/网关错误的实例将被临时摘除
type OutlierDetectionConfig struct {
	Consecutive5xx                 int `json:"consecutive5xx"`
	ConsecutiveGatewayFailure      int `json:"consecutiveGatewayFailure"`
	Interval                       int `json:"interval"`
	BaseEjectionTime               int `json:"baseEjectionTime"`
	MaxEjectionPercent             int `json:"maxEjectionPercent"`
	FailurePercentageThreshold     int `json:"failurePercentageThreshold"`     // 故障百分比
	FailurePercentageMinimumHosts  int `json:"failurePercentageMinimumHosts"`  // 策略执行时，集群内最小主机数
	FailurePercentageRequestVolume int `json:"failurePercentageRequestVolume"` // 策略计算周期内，最小请求数
}

// Ratelimit 限流规则：按调用方及请求特征匹配，命中后应用令牌桶限流
type Ratelimit struct {
	Id              int             `json:"id"`              //  id
	Name            string          `json:"name"`            //  名称
	Desc            string          `json:"desc"`            //  规则描述
	ServiceMeshId   int             `json:"serviceMeshId"`   //  网格id
	MatchConfig     MatchConfig     `json:"matchConfig"`     //  请求类型
	RatelimitConfig RatelimitConfig `json:"ratelimitConfig"` //  限流设置
	CreateTime      time.Time       `json:"createTime"`      //  创建时间
	UpdateTime      time.Time       `json:"updateTime"`      //  更新时间
}

// RatelimitConfig 令牌桶限流参数
type RatelimitConfig struct {
	Status        string `json:"status"`
	MaxTokens     int    `json:"maxTokens"`
	TokensPerFill int    `json:"tokensPerFill"`
	Percent       int    `json:"percent"`
}

// MatchConfig 规则匹配条件：限定调用方服务及请求特征（header/参数等）
type MatchConfig struct {
	CallerService string            `json:"callerService"`
	Selectors     []RequestSelector `json:"selectors"`
}

// RequestSelector 请求级选择器，按类型（header等）匹配请求特征
type RequestSelector struct {
	Type     string `json:"type"`
	Key      string `json:"key"`
	Value    string `json:"value"`
	Operator string `json:"operator"`
}

// Router 路由规则：匹配请求后按权重/优先级转发到目标实例分组，并可附带重试与超时配置
type Router struct {
	ID                 int64              `json:"id"`                 //  id
	ServiceMeshId      int64              `json:"serviceMeshId"`     //  id
	Name               string             `json:"name"`              //  名称
	Desc               string             `json:"desc"`              //  规则描述
	MatchConfig        MatchConfig        `json:"matchConfig"`       //  请求描述
	DestInstanceGroups DestInstanceGroups `json:"destInstanceGroups"` //  路由目标集群
	RetryConfig        RetryConfig        `json:"retryConfig"`       //  重试策略
	TimeoutConfig      TimeoutConfig      `json:"timeoutConfig"`     //  超时配置
	CreateTime         time.Time          `json:"createTime"`        //  创建时间
	UpdateTime         time.Time          `json:"updateTime"`        //  更新时间
}

// TimeoutConfig 超时配置
type TimeoutConfig struct {
	Timeout int  `json:"timeout"`
	Enable  bool `json:"enable"`
}

// DestInstanceGroups 路由目标分组列表，实现sort.Interface按权重、优先级排序
type DestInstanceGroups []struct {
	InstanceGroupName string `json:"instanceGroupName"`
	Weight            int    `json:"weight"`
	Priority          int    `json:"priority"`
}

func (p DestInstanceGroups) Len() int { return len(p) }

func (p DestInstanceGroups) Less(i, j int) bool {
	if p[i].Weight < p[j].Weight {
		return true
	} else if p[i].Weight == p[j].Weight {
		return p[i].Priority < p[j].Priority
	}
	return false
}

func (p DestInstanceGroups) Swap(i, j int) { p[i], p[j] = p[j], p[i] }

// RetryConfig 重试策略配置
type RetryConfig struct {
	RetryOn      string `json:"retryOn"`
	NumRetries   int    `json:"numRetries"`
	BaseInterval int    `json:"baseInterval"`
	MaxInterval  int    `json:"maxInterval"`
	Enable       bool   `json:"enable"`
}

// JSON 原始JSON数据，延迟解析用。
// 使用类型别名（而非定义类型），使 json.RawMessage 可直接赋值给 Properties，
// 与标准库 Marshaler/Unmarshaler 行为完全一致
type JSON = json.RawMessage

// SettingType 网格设置类型
type SettingType string

// 支持的网格设置类型
var (
	// NearestType 就近路由设置
	NearestType SettingType = "nearest"
	// LbPolicyType 负载均衡策略设置
	LbPolicyType SettingType = "lbPolicy"
	// HealthyCheckType 健康检查设置
	HealthyCheckType SettingType = "healthyCheck"
	// TracingType 链路追踪采样设置
	TracingType SettingType = "tracing"
)

// MeshSetting 单项网格设置：Type区分设置种类，Properties为对应配置的原始JSON
type MeshSetting struct {
	Id            int         `json:"id"`
	ServiceMeshId int         `json:"serviceMeshId"` //  网格id
	Type          SettingType `json:"type"`
	Properties    JSON        `json:"properties"`
}

// SetProperties 将任意配置对象序列化存入Properties
func (d *MeshSetting) SetProperties(obj interface{}) {
	data, _ := json.Marshal(obj)
	d.Properties = data
}

// GetProperties 将Properties反序列化到obj
func (d *MeshSetting) GetProperties(obj interface{}) error {
	return json.Unmarshal(d.Properties, obj)
}

// Nearest 就近路由设置：开启后按region/zone就近调度，无匹配实例时按fallbackType回退
type Nearest struct {
	Enable       bool                `json:"enable"`
	FallBackType NearestFallBackType `json:"fallbackType"`
}

// NearestFallBackType 就近路由无匹配时的回退范围
type NearestFallBackType int

// 就近路由回退范围：从全部实例到仅同zone逐级收紧
const (
	All    NearestFallBackType = 0 // 无限制，回退到全部实例
	Region NearestFallBackType = 1 // 回退到同region
	Zone   NearestFallBackType = 2 // 回退到同zone
)

var toString = map[NearestFallBackType]string{
	All:    "ALL",
	Region: "REGION",
	Zone:   "ZONE",
}
var toID = map[string]NearestFallBackType{
	"ALL":    All,
	"REGION": Region,
	"ZONE":   Zone,
}

func (n NearestFallBackType) MarshalJSON() ([]byte, error) {
	buffer := bytes.NewBufferString(`"`)
	buffer.WriteString(toString[n])
	buffer.WriteString(`"`)
	return buffer.Bytes(), nil
}
func (n *NearestFallBackType) UnmarshalJSON(b []byte) error {
	var j string
	err := json.Unmarshal(b, &j)
	if err != nil {
		return err
	}
	*n = toID[j]
	return nil
}

// LbPolicy 负载均衡策略，如RINGHASH时按HeaderKey的取值做一致性哈希
type LbPolicy struct {
	Policy    string `json:"policy"`
	HeaderKey string `json:"headerKey"`
}

// RingHash 一致性哈希负载均衡策略标识
const RingHash = "RINGHASH"

// HealthyCheckSetting 健康检查设置：可关闭服务端健康检查、指定TTL
type HealthyCheckSetting struct {
	DisableServerSideHealthyCheck bool `json:"disableServerSideHealthyCheck"`
	TTL                           int  `json:"ttl"`
}

// TracingSetting 链路追踪采样设置，各采样率取值0~100
type TracingSetting struct {
	Enable          bool `json:"enable" binding:"gte=0,lte=100"`
	ClientSampling  int  `json:"client_sampling" binding:"gte=0,lte=100"`
	RandomSampling  int  `json:"random_sampling" binding:"gte=0,lte=100"`
	OverallSampling int  `json:"overall_sampling" binding:"gte=0,lte=100"`
}
