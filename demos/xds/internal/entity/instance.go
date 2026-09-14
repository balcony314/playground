package entity

import (
	"encoding/json"
	"fmt"
	"net"
	"regexp"
)

const (
	// InstanceSourceTypeMatrix 实例来源：由Matrix平台同步
	InstanceSourceTypeMatrix = "matrix"
	// InstanceSourceTypeCustom 实例来源：物理机等自定义注册
	InstanceSourceTypeCustom = "custom"

	// InstanceClusterName 关闭服务端健康检查的实例所在的Nacos逻辑cluster名
	InstanceClusterName = "custom-client-side-healthy-check"

	// 标签选择器操作符
	SelectorOperatorEqual    = "="  // 等于
	SelectorOperatorRegex    = "~"  // 正则匹配
	SelectorOperatorNotEqual = "!=" // 不等于
	SelectorOperatorNotRegex = "!~" // 正则不匹配
)

var (
	// InstanceAvailableFilter 预置可用实例过滤器：健康且未被隔离
	InstanceAvailableFilter = InstanceFilter{
		Healthy: ptr(true),
		Isolate: ptr(false),
	}
	ipValidator       = regexp.MustCompile(`^(([0-9]|[1-9][0-9]|1[0-9]{2}|2[0-4][0-9]|25[0-5])\.){3}([0-9]|[1-9][0-9]|1[0-9]{2}|2[0-4][0-9]|25[0-5])$`)
	hostnameValidator = regexp.MustCompile(`^(([a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9\-]*[a-zA-Z0-9])\.)*([A-Za-z0-9]|[A-Za-z0-9][A-Za-z0-9\-]*[A-Za-z0-9])$`)
)

// Instance 服务实例实体，对应Nacos中注册的一个实例
type Instance struct {
	// IP 实例地址，允许填IP或主机名（主机名会在注册前解析为真实IP）
	IP     string `json:"ip" binding:"required"`
	Port   int    `json:"port" binding:"required"`
	Region string `json:"region" binding:"required"`
	Zone   string `json:"zone" binding:"required"`
	// 为空时默认为true，有值时按值处理
	Healthy *bool `json:"healthy"`
	// 为空时默认为false，有值时按值处理
	Isolate *bool `json:"isolate"`
	// Source 实例来源：matrix（Matrix同步）或custom（自主上报）
	Source string            `json:"source" binding:"required,oneof=matrix custom"`
	Labels map[string]string `json:"labels"`

	// DisableServerSideHealthyCheck 是否关闭服务端健康检查（健康状态由实例自身/来源方维护）
	DisableServerSideHealthyCheck bool `json:"disableServerSideHealthyCheck"`
}

func (i Instance) String() string {
	data, _ := json.Marshal(i)
	return string(data)
}

// GetNacosCluster 返回实例应归属的Nacos逻辑cluster：
// 关闭服务端健康检查的实例单独成组，其余按来源分组，便于按cluster批量控制健康检查策略
func (i Instance) GetNacosCluster() string {
	if i.DisableServerSideHealthyCheck {
		return InstanceClusterName
	} else {
		return i.Source
	}
}

// EnsureDefaultLabels 将部分字段设置到标签中
func (i *Instance) EnsureDefaultLabels() {
	//防止字段为空
	if i.Labels == nil {
		i.Labels = map[string]string{}
	}
	i.Labels["region"] = i.Region
	i.Labels["zone"] = i.Zone
	i.Labels["source"] = i.Source
	if i.Port != 0 {
		i.Labels["port"] = fmt.Sprint(i.Port)
	}
	ip := net.ParseIP(i.IP)
	if ip == nil {
		//传入的不是IP，尝试解析
		ips, _ := net.LookupIP(i.IP)
		if len(ips) > 0 {
			//如果IP字段为主机名，则通过dns解析真实IP
			i.Labels["hostname"] = i.IP
			i.IP = ips[0].String()
		}
	} else {
		// 传入的是IP，根据来源确定是否需要反向查询hostname
		// matrix直接使用pod名作为hostname, custom的才尝试反查hostname
		if i.Source == InstanceSourceTypeCustom {
			names, _ := net.LookupAddr(i.IP)
			if len(names) > 0 {
				i.Labels["hostname"] = names[0]
			}
		}
	}
}

// RemoveDefaultLabels 从标签中移除默认标签（当前默认标签需保留，暂为空实现）
func (i *Instance) RemoveDefaultLabels() {
	//delete(i.Labels, "region")
	//delete(i.Labels, "zone")
	//delete(i.Labels, "source")
	//delete(i.Labels, "port")
}

// Validate 注册前校验实例合法性：地址（IP或可解析的主机名）、端口、region、zone
func (i *Instance) Validate() error {
	//校验地址是否合法，先通过正则确定是IP还是hostname，然后分别校验
	if ipValidator.MatchString(i.IP) {
		if ip := net.ParseIP(i.IP); ip == nil {
			return fmt.Errorf("invalid ip: %s", i.IP)
		}
	} else if !hostnameValidator.MatchString(i.IP) {
		return fmt.Errorf("invalid hostname: %s", i.IP)
	} else {
		ips, _ := net.LookupIP(i.IP)
		if len(ips) == 0 {
			return fmt.Errorf("can not resovle hostname: %s", i.IP)
		}
	}

	//校验端口是否合法
	if i.Port <= 0 || i.Port >= 65535 {
		return fmt.Errorf("invalid port: %v", i.Port)
	}

	//校验region是否合法
	//TODO: 暂时只考虑是否为空
	if i.Region == "" {
		return fmt.Errorf("invalid region: %s", i.Region)
	}

	//校验zone是否合法
	if i.Zone == "" {
		return fmt.Errorf("invalid zone: %s", i.Zone)
	}
	return nil
}

// Locality 实例地理位置，用于就近路由调度
type Locality struct {
	Region string
	Zone   string
}

// InstanceList 实例列表，附带的过滤、分组方法供路由计算使用
type InstanceList []Instance

// GroupByLocality 按region+zone将实例分组，供就近路由按地域挑选实例
func (i InstanceList) GroupByLocality() map[Locality]InstanceList {
	result := map[Locality]InstanceList{}
	for _, obj := range i {
		result[Locality{
			Region: obj.Region,
			Zone:   obj.Zone,
		}] = i.ApplyFilter(&InstanceFilter{Region: obj.Region, Zone: obj.Zone})
	}
	return result
}

// ApplyFilter 按条件过滤实例列表，filter为nil时原样返回
func (i InstanceList) ApplyFilter(filter *InstanceFilter) InstanceList {
	// 防止filter为nil时报错
	if filter == nil {
		return i
	}
	result := InstanceList{}
	for _, obj := range i {
		if i.match(obj, filter) {
			result = append(result, obj)
		}
	}
	return result
}

// 按条件过滤实例，多个条件时必须同时满足
func (i InstanceList) match(obj Instance, filter *InstanceFilter) bool {
	if filter.IP != "" && obj.IP != filter.IP {
		return false
	}
	if filter.Port != 0 && obj.Port != filter.Port {
		return false
	}
	if filter.Region != "" && obj.Region != filter.Region {
		return false
	}
	if filter.Zone != "" && obj.Zone != filter.Zone {
		return false
	}
	if filter.Healthy != nil && obj.Healthy != nil && *obj.Healthy != *filter.Healthy {
		return false
	}
	if filter.Isolate != nil && obj.Isolate != nil && *obj.Isolate != *filter.Isolate {
		return false
	}
	if filter.Source != "" && obj.Source != filter.Source {
		return false
	}
	if len(filter.Selectors) > 0 {
		for _, s := range filter.Selectors {
			if !s.Match(obj.Labels) {
				return false
			}
		}
	}
	return true
}

// InstanceFilter 实例过滤条件，多条件需同时满足；零值字段视为不限制
type InstanceFilter struct {
	IP      string `json:"ip,omitempty"`
	Port    int    `json:"port,omitempty"`
	Region  string `json:"region,omitempty"`
	Zone    string `json:"zone,omitempty"`
	Healthy *bool  `json:"healthy,omitempty"`
	Isolate *bool  `json:"isolate,omitempty"`
	Source  string `json:"source,omitempty"`
	// Selectors 标签选择器列表，需全部命中
	Selectors []Selector `json:"selectors,omitempty"`
}

func (i InstanceFilter) String() string {
	data, _ := json.Marshal(i)
	return string(data)
}

// Selector 单个标签选择器，支持=/!=/~(正则)/!~四种操作符
type Selector struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	Operator string `json:"operator"`
}

// Match 判断标签集是否命中该选择器；key不存在时not类操作符视为匹配
func (i Selector) Match(data map[string]string) bool {
	if v, ok := data[i.Key]; ok {
		switch i.Operator {
		case SelectorOperatorEqual:
			return v == i.Value
		case SelectorOperatorNotEqual:
			return v != i.Value
		case SelectorOperatorRegex:
			ok, err := regexp.MatchString(fmt.Sprintf("^%s$", i.Value), v)
			return ok && err == nil
		case SelectorOperatorNotRegex:
			ok, err := regexp.MatchString(fmt.Sprintf("^%s$", i.Value), v)
			return !ok && err == nil
		default:
			return false
		}
	} else {
		//key不存在的情况下，需要分类讨论
		switch i.Operator {
		case SelectorOperatorEqual:
			return false
		case SelectorOperatorNotEqual:
			// not类匹配，不存在对应的键按匹配处理
			return true
		case SelectorOperatorRegex:
			return false
		case SelectorOperatorNotRegex:
			return true
		default:
			return false
		}
	}
}

// InstanceListRequest 列出服务实例
type InstanceListRequest struct {
	Service       string `json:"service" binding:"required"`
	OnlyAvailable bool   `json:"onlyAvailable"`
	// FromSdk 是否从sdk侧查询（默认web端，两者实例视图不同）
	FromSdk bool `json:"fromSdk"`
	InstanceFilter
}

// InstanceRegisterRequest 注册服务请求
type InstanceRegisterRequest struct {
	Service string `json:"service"`
	Instance
}

// InstanceDeRegisterRequest 反注册服务请求
type InstanceDeRegisterRequest struct {
	Service string `json:"service" binding:"required"`
	IP      string `json:"ip"`
	Port    int    `json:"port"`
}

type InstanceCleanRequest struct {
	Service string `json:"service" binding:"required"`
	// 可选，是否清理健康的实例
	IncludeHealthy bool `json:"includeHealthy"`
}

// ptr 返回 v 的指针，替代原 golang/protobuf 的 proto.Bool 等辅助函数
func ptr[T any](v T) *T { return &v }
