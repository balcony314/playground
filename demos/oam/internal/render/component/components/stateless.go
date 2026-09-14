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

package components

import (
	"context"
	"encoding/json"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/balcony314/oam/internal/render"
	"github.com/balcony314/oam/internal/render/component"
)

func init() {
	component.Register(ComponentTypeStateless, NewStateless)
}

// 内置组件类型。
const (
	ComponentTypeStateless = "stateless"
	ComponentTypeStateful  = "stateful"
)

// Stateless 把无状态组件渲染为 Deployment（及可选的 Service）。
type Stateless struct {
	spec WorkloadSpec
}

// NewStateless 从原始 JSON 属性构造 Stateless 组件。
func NewStateless(properties runtime.RawExtension) (component.Component, error) {
	spec := WorkloadSpec{}
	if err := json.Unmarshal(properties.Raw, &spec); err != nil {
		return nil, fmt.Errorf("invalid stateless properties: %w", err)
	}
	if spec.Image == "" {
		return nil, fmt.Errorf("stateless properties: image is required")
	}
	return &Stateless{spec: spec}, nil
}

// Render 实现 component.Component，产出 Deployment；声明了端口时追加 Service。
func (s *Stateless) Render(ctx context.Context, _ string) ([]client.Object, error) {
	rc := render.FromContext(ctx)

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        workloadName(rc),
			Namespace:   rc.Namespace,
			Labels:      podLabels(rc, rc.Labels),
			Annotations: rc.Annotations,
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: podLabels(rc, nil),
			},
			Template: buildPodTemplate(rc, s.spec),
		},
	}

	objects := []client.Object{deployment}
	if len(s.spec.Ports) > 0 {
		objects = append(objects, buildService(rc, s.spec))
	}
	return objects, nil
}

// buildService 为声明了端口的组件渲染一个 ClusterIP Service。
func buildService(rc render.RenderContext, spec WorkloadSpec) *corev1.Service {
	var ports []corev1.ServicePort
	for _, p := range spec.Ports {
		ports = append(ports, corev1.ServicePort{
			Name:       fmt.Sprintf("%d", p.Port),
			Port:       p.Port,
			TargetPort: intstr.FromInt(int(p.Port)),
		})
	}
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:        workloadName(rc),
			Namespace:   rc.Namespace,
			Labels:      podLabels(rc, rc.Labels),
			Annotations: rc.Annotations,
		},
		Spec: corev1.ServiceSpec{
			Selector: podLabels(rc, nil),
			Ports:    ports,
			Type:     corev1.ServiceTypeClusterIP,
		},
	}
}
