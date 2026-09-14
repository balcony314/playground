/*
Copyright 2026 The OAM Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package components 提供内置的 Component 实现，并在 init() 中完成自注册。
package components

import (
	"fmt"
	"maps"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/balcony314/oam/internal/render"
)

// WorkloadSpec 是内置组件的通用属性模式，由 Component 的 Properties 反序列化而来。
type WorkloadSpec struct {
	// Image 是容器镜像完整引用（含 tag）。
	Image string `json:"image"`

	// Command / Args 透传为容器的启动命令与参数。
	Command []string `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`

	// Env 是注入容器的环境变量。
	Env []EnvVar `json:"env,omitempty"`

	// Ports 声明容器监听端口，非空时会额外渲染一个 ClusterIP Service。
	Ports []PortSpec `json:"ports,omitempty"`

	// Resources 声明容器资源请求与限制，值为 Kubernetes quantity 字符串。
	Resources ResourcesSpec `json:"resources,omitzero"`

	// LivenessProbe / ReadinessProbe 声明容器探针。
	LivenessProbe  *ProbeSpec `json:"livenessProbe,omitempty"`
	ReadinessProbe *ProbeSpec `json:"readinessProbe,omitempty"`
}

// EnvVar 是注入容器的环境变量。
type EnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// PortSpec 声明一个容器端口。
type PortSpec struct {
	// Port 是容器监听端口号。
	Port int32 `json:"port"`

	// Protocol 默认 TCP。
	Protocol string `json:"protocol,omitempty"`
}

// ResourcesSpec 声明容器的资源请求与限制。
type ResourcesSpec struct {
	Requests ResourcePair `json:"requests,omitzero"`
	Limits   ResourcePair `json:"limits,omitzero"`
}

// ResourcePair 用 Kubernetes quantity 字符串描述 CPU 与内存，例如 "500m"、"512Mi"。
type ResourcePair struct {
	CPU    string `json:"cpu,omitempty"`
	Memory string `json:"memory,omitempty"`
}

// ProbeSpec 声明容器探针。Type 取 http、tcp 或 exec。
type ProbeSpec struct {
	Type string `json:"type"`

	// Path 是 http 探针路径。
	Path string `json:"path,omitempty"`

	// Port 是 http/tcp 探针端口。
	Port int `json:"port,omitempty"`

	// Command 是 exec 探针命令。
	Command string `json:"command,omitempty"`

	// 高级配置；零值时使用内置默认值。
	PeriodSeconds    int32 `json:"periodSeconds,omitempty"`
	TimeoutSeconds   int32 `json:"timeoutSeconds,omitempty"`
	FailureThreshold int32 `json:"failureThreshold,omitempty"`
}

// 探针默认参数。
const (
	defaultProbePeriodSeconds    = 10
	defaultProbeTimeoutSeconds   = 5
	defaultProbeFailureThreshold = 3
)

// workloadName 返回组件工作负载的名称：<deployUnit>-<component>。
func workloadName(rc render.RenderContext) string {
	return fmt.Sprintf("%s-%s", rc.DeployUnitName, rc.ComponentName)
}

// podLabels 返回工作负载 Pod 模板与 Service 共用的标签集。
func podLabels(rc render.RenderContext, extra map[string]string) map[string]string {
	labels := make(map[string]string, len(extra)+2)
	maps.Copy(labels, extra)
	labels[render.LabelDeployUnit] = rc.DeployUnitName
	labels[render.LabelComponent] = rc.ComponentName
	return labels
}

// buildContainer 把 WorkloadSpec 转换为主容器定义，容器名固定为组件名。
func buildContainer(name string, spec WorkloadSpec) corev1.Container {
	container := corev1.Container{
		Name:    name,
		Image:   spec.Image,
		Command: spec.Command,
		Args:    spec.Args,
		Ports:   containerPorts(spec.Ports),
	}
	for _, env := range spec.Env {
		container.Env = append(container.Env, corev1.EnvVar{Name: env.Name, Value: env.Value})
	}
	if requests, limits := resourceList(spec.Resources); requests != nil || limits != nil {
		container.Resources = corev1.ResourceRequirements{Requests: requests, Limits: limits}
	}
	if probe := buildProbe(spec.LivenessProbe); probe != nil {
		container.LivenessProbe = probe
	}
	if probe := buildProbe(spec.ReadinessProbe); probe != nil {
		container.ReadinessProbe = probe
	}
	return container
}

// containerPorts 把端口声明转换为容器端口列表，端口名按 协议-端口 自动生成以避免冲突。
func containerPorts(ports []PortSpec) []corev1.ContainerPort {
	if len(ports) == 0 {
		return nil
	}
	result := make([]corev1.ContainerPort, 0, len(ports))
	for _, p := range ports {
		protocol := corev1.ProtocolTCP
		if p.Protocol != "" {
			protocol = corev1.Protocol(strings.ToUpper(p.Protocol))
		}
		result = append(result, corev1.ContainerPort{
			Name:          strings.ToLower(fmt.Sprintf("%s-%d", protocol, p.Port)),
			Protocol:      protocol,
			ContainerPort: p.Port,
		})
	}
	return result
}

// resourceList 把资源配置转换为 ResourceList，空值返回 nil。
func resourceList(spec ResourcesSpec) (requests, limits corev1.ResourceList) {
	toList := func(pair ResourcePair) corev1.ResourceList {
		list := corev1.ResourceList{}
		if pair.CPU != "" {
			list[corev1.ResourceCPU] = resource.MustParse(pair.CPU)
		}
		if pair.Memory != "" {
			list[corev1.ResourceMemory] = resource.MustParse(pair.Memory)
		}
		if len(list) == 0 {
			return nil
		}
		return list
	}
	return toList(spec.Requests), toList(spec.Limits)
}

// buildProbe 把探针声明转换为 Kubernetes 探针，未声明或类型未知时返回 nil。
func buildProbe(spec *ProbeSpec) *corev1.Probe {
	if spec == nil {
		return nil
	}

	probe := corev1.Probe{
		PeriodSeconds:    orDefault(spec.PeriodSeconds, defaultProbePeriodSeconds),
		TimeoutSeconds:   orDefault(spec.TimeoutSeconds, defaultProbeTimeoutSeconds),
		FailureThreshold: orDefault(spec.FailureThreshold, defaultProbeFailureThreshold),
	}
	switch strings.ToLower(spec.Type) {
	case "http":
		probe.HTTPGet = &corev1.HTTPGetAction{
			Path: spec.Path,
			Port: intstr.FromInt(spec.Port),
		}
	case "tcp":
		probe.TCPSocket = &corev1.TCPSocketAction{
			Port: intstr.FromInt(spec.Port),
		}
	case "exec":
		probe.Exec = &corev1.ExecAction{
			Command: []string{"/bin/sh", "-c", spec.Command},
		}
	default:
		return nil
	}
	return &probe
}

// buildPodTemplate 构造工作负载的 Pod 模板。
func buildPodTemplate(rc render.RenderContext, spec WorkloadSpec) corev1.PodTemplateSpec {
	return corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels:      podLabels(rc, rc.Labels),
			Annotations: rc.Annotations,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{buildContainer(rc.ComponentName, spec)},
		},
	}
}

// orDefault 返回非零值，否则返回默认值。
func orDefault(value, defaultValue int32) int32 {
	if value == 0 {
		return defaultValue
	}
	return value
}
