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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/balcony314/oam/internal/render"
	"github.com/balcony314/oam/internal/render/component"
)

func init() {
	component.Register(ComponentTypeStateful, NewStateful)
}

// Stateful 把有状态组件渲染为 StatefulSet（及可选的 Service）。
type Stateful struct {
	spec WorkloadSpec
}

// NewStateful 从原始 JSON 属性构造 Stateful 组件。
func NewStateful(properties runtime.RawExtension) (component.Component, error) {
	spec := WorkloadSpec{}
	if err := json.Unmarshal(properties.Raw, &spec); err != nil {
		return nil, fmt.Errorf("invalid stateful properties: %w", err)
	}
	if spec.Image == "" {
		return nil, fmt.Errorf("stateful properties: image is required")
	}
	return &Stateful{spec: spec}, nil
}

// Render 实现 component.Component，产出 StatefulSet；声明了端口时追加 Service。
func (s *Stateful) Render(ctx context.Context, _ string) ([]client.Object, error) {
	rc := render.FromContext(ctx)

	statefulSet := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        workloadName(rc),
			Namespace:   rc.Namespace,
			Labels:      podLabels(rc, rc.Labels),
			Annotations: rc.Annotations,
		},
		Spec: appsv1.StatefulSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: podLabels(rc, nil),
			},
			Template: buildPodTemplate(rc, s.spec),
			UpdateStrategy: appsv1.StatefulSetUpdateStrategy{
				Type: appsv1.RollingUpdateStatefulSetStrategyType,
			},
		},
	}

	objects := []client.Object{statefulSet}
	if len(s.spec.Ports) > 0 {
		objects = append(objects, buildService(rc, s.spec))
	}
	return objects, nil
}
