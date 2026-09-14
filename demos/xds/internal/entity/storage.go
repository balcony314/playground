package entity

// WatchCallback 服务变更回调：broadcast标识是否需要级联广播，service为变更的服务名
type WatchCallback func(broadcast bool, service string) error

// ServiceEvent 服务变更事件，即变更队列中的元素
type ServiceEvent struct {
	// Service 发生变更的服务名
	Service string `json:"service"`
	// Broadcast 是否为自身变化导致的主动变更：true需级联通知上游（重新下发全量相关配置），false表示由其他服务变化引起的被动更新
	Broadcast bool `json:"broadcast"`
}
