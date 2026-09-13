// Copyright 2026 balcony314.
// SPDX-License-Identifier: MIT

package controller

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// MemoryGuardMetrics 封装 MemoryGuard Operator 的自定义 Prometheus 指标。
type MemoryGuardMetrics struct {
	// MarkedPods 是一个 Gauge，按 MemoryPolicy 维度记录当前被该 Policy 标记的 Pod 数量。
	// 标签：policy（MemoryPolicy 名）、namespace（目标命名空间，空表示全集群）。
	MarkedPods *prometheus.GaugeVec
}

// NewMemoryGuardMetrics 创建并注册自定义指标到 controller-runtime 的 metrics registry。
func NewMemoryGuardMetrics() *MemoryGuardMetrics {
	m := &MemoryGuardMetrics{
		MarkedPods: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "memoryguard_marked_pods",
			Help: "Number of pods currently marked by each MemoryPolicy (memory usage exceeds threshold).",
		}, []string{"policy", "namespace"}),
	}
	metrics.Registry.MustRegister(m.MarkedPods)
	return m
}
