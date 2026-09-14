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

package traits

import (
	"context"
	"encoding/json"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/balcony314/oam/internal/render/trait"
)

func init() {
	trait.Register("sidecar", NewSidecar)
}

// sidecarSpec 是 sidecar trait 的属性模式。
type sidecarSpec struct {
	// Name 是边车容器名称，同一 Pod 内需唯一。
	Name string `json:"name"`

	// Image 是边车容器镜像。
	Image string `json:"image"`

	// Command / Args 透传为容器的启动命令与参数。
	Command []string `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`

	// Env 是注入边车的环境变量。
	Env []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"env,omitempty"`
}

// Sidecar 向工作负载的 Pod 模板注入一个边车容器。
type Sidecar struct {
	spec sidecarSpec
}

// NewSidecar 从原始 JSON 属性构造 sidecar trait。
func NewSidecar(properties runtime.RawExtension) (trait.Trait, error) {
	spec := sidecarSpec{}
	if err := json.Unmarshal(properties.Raw, &spec); err != nil {
		return nil, fmt.Errorf("invalid sidecar properties: %w", err)
	}
	if spec.Name == "" || spec.Image == "" {
		return nil, fmt.Errorf("sidecar properties: name and image are required")
	}
	return &Sidecar{spec: spec}, nil
}

// GetOrder 返回执行档位：PostOrder（在 scaler 等基础改写之后执行）。
func (t *Sidecar) GetOrder() int {
	return trait.PostOrder
}

// Apply 实现 trait.Trait，向 Deployment / StatefulSet 的 Pod 模板追加容器。
// 同名容器已存在时跳过，避免重复注入。
func (t *Sidecar) Apply(_ context.Context, manifests []client.Object, _ client.Client) ([]client.Object, error) {
	for _, manifest := range manifests {
		var podSpec *corev1.PodSpec
		switch obj := manifest.(type) {
		case *appsv1.Deployment:
			podSpec = &obj.Spec.Template.Spec
		case *appsv1.StatefulSet:
			podSpec = &obj.Spec.Template.Spec
		default:
			continue
		}

		if findContainer(podSpec.Containers, t.spec.Name) != nil {
			continue
		}

		container := corev1.Container{
			Name:    t.spec.Name,
			Image:   t.spec.Image,
			Command: t.spec.Command,
			Args:    t.spec.Args,
		}
		for _, env := range t.spec.Env {
			container.Env = append(container.Env, corev1.EnvVar{Name: env.Name, Value: env.Value})
		}
		podSpec.Containers = append(podSpec.Containers, container)
	}
	return manifests, nil
}

// findContainer 按名称查找容器，不存在时返回 nil。
func findContainer(containers []corev1.Container, name string) *corev1.Container {
	for i := range containers {
		if containers[i].Name == name {
			return &containers[i]
		}
	}
	return nil
}
