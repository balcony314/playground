package probe

import "github.com/balcony314/xds/pkg/logging"

// 就绪/健康探针状态标记，由服务各组件初始化完成后置位，
// 供 k8s liveness/readiness 探针（handler.go）读取
var ready = false
var healthy = false

// Ready 标记服务已就绪，可以接收流量
func Ready() {
	ready = true
	logging.With("status", "ready").Info("changing probe status")
}

// NotReady 标记服务未就绪，reason 为导致未就绪的原因
func NotReady(err error) {
	ready = false
	logging.With("status", "not-ready", "reason", err).Warn("changing probe status")
}

// Healthy 标记服务健康
func Healthy() {
	healthy = true
	logging.With("status", "healthy").Info("changing probe status")
}

// NotHealthy 标记服务不健康，reason 为导致不健康的原因
func NotHealthy(err error) {
	healthy = false
	logging.With("status", "not-healthy", "reason", err).Warn("changing probe status")
}
