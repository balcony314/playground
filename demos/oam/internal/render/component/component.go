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

// Package component 定义 Component 抽象及其工厂注册表。
//
// Component 负责把用户声明的组件属性渲染为一组 Kubernetes 原生资源，
// 是渲染管线的第一站；渲染结果随后交给各 Trait 增量改写。
// 具体实现位于 components 子包，通过 init() 自注册。
package component

import (
	"context"
	"fmt"
	"sync"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Component 将组件属性渲染为一组 Kubernetes 资源。
//
// ctx 携带 render.RenderContext（DeployUnit 名称、命名空间、透传元数据等）；
// name 是组件名称，用于资源命名。
type Component interface {
	Render(ctx context.Context, name string) ([]client.Object, error)
}

// Factory 由组件属性（原始 JSON）构造 Component 实例。
type Factory func(properties runtime.RawExtension) (Component, error)

var (
	mu       sync.RWMutex
	registry = map[string]Factory{}
)

// Register 注册一个 Component 工厂，一般在实现包的 init() 中调用。
func Register(componentType string, factory Factory) {
	mu.Lock()
	defer mu.Unlock()
	registry[componentType] = factory
}

// Get 按组件类型取已注册的工厂，未注册时返回错误。
func Get(componentType string) (Factory, error) {
	mu.RLock()
	defer mu.RUnlock()
	factory, ok := registry[componentType]
	if !ok {
		return nil, fmt.Errorf("unknown component type %q", componentType)
	}
	return factory, nil
}
