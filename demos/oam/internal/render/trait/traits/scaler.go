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

// Package traits 提供内置的 Trait 实现，并在 init() 中完成自注册。
package traits

import (
	"context"
	"encoding/json"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/balcony314/oam/internal/render/trait"
)

func init() {
	trait.Register("scaler", NewScaler)
}

// scalerSpec 是 scaler trait 的属性模式。
type scalerSpec struct {
	// Replicas 是期望副本数。
	Replicas int32 `json:"replicas"`

	// MaxSurge / MaxUnavailable 控制滚动更新节奏，
	// 取值为整数或百分比字符串（如 "25%"），仅对 Deployment 生效。
	MaxSurge       string `json:"maxSurge,omitempty"`
	MaxUnavailable string `json:"maxUnavailable,omitempty"`

	// Paused 为 true 时暂停滚动更新，仅对 Deployment 生效。
	Paused bool `json:"paused,omitempty"`
}

// Scaler 设置工作负载的副本数与滚动更新策略。
type Scaler struct {
	spec scalerSpec
}

// NewScaler 从原始 JSON 属性构造 scaler trait。
func NewScaler(properties runtime.RawExtension) (trait.Trait, error) {
	spec := scalerSpec{}
	if err := json.Unmarshal(properties.Raw, &spec); err != nil {
		return nil, fmt.Errorf("invalid scaler properties: %w", err)
	}
	return &Scaler{spec: spec}, nil
}

// GetOrder 返回执行档位：InOrder。
func (t *Scaler) GetOrder() int {
	return trait.InOrder
}

// Apply 实现 trait.Trait，改写 Deployment / StatefulSet 的副本与更新策略。
func (t *Scaler) Apply(_ context.Context, manifests []client.Object, _ client.Client) ([]client.Object, error) {
	for _, manifest := range manifests {
		switch obj := manifest.(type) {
		case *appsv1.Deployment:
			obj.Spec.Replicas = &t.spec.Replicas
			obj.Spec.Paused = t.spec.Paused
			if obj.Spec.Strategy.Type == "" {
				obj.Spec.Strategy.Type = appsv1.RollingUpdateDeploymentStrategyType
			}
			if obj.Spec.Strategy.RollingUpdate == nil {
				obj.Spec.Strategy.RollingUpdate = &appsv1.RollingUpdateDeployment{}
			}
			if t.spec.MaxSurge != "" {
				surge := intstr.Parse(t.spec.MaxSurge)
				obj.Spec.Strategy.RollingUpdate.MaxSurge = &surge
			}
			if t.spec.MaxUnavailable != "" {
				unavailable := intstr.Parse(t.spec.MaxUnavailable)
				obj.Spec.Strategy.RollingUpdate.MaxUnavailable = &unavailable
			}
		case *appsv1.StatefulSet:
			obj.Spec.Replicas = &t.spec.Replicas
		}
	}
	return manifests, nil
}
