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

// Package trait 定义 Trait 抽象及其工厂注册表。
//
// Trait 在 Component 已渲染出的对象列表上做增量改写（或追加新对象），
// 例如设置副本数、注入边车、挂载配置。具体实现位于 traits 子包，
// 通过 init() 自注册。
package trait

import (
	"context"
	"fmt"
	"sync"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Trait 的执行顺序档位。Order 越大越靠后执行，
// 后执行的 Trait 可以覆盖先执行的 Trait 设置的字段。
const (
	// PreOrder 供需要最先执行的 trait 使用，例如初始化类改写。
	PreOrder = 10
	// InOrder 是默认档位，例如 scaler 设置副本数。
	InOrder = 100
	// PostOrder 供依赖前置改写已完成的 trait 使用，例如注入容器。
	PostOrder = 1000
	// LastOrder 供需要最终生效的 trait 使用。
	LastOrder = 10000
)

// Trait 对 Component 渲染出的对象列表做增量改写。
//
// ctx 携带 render.RenderContext；c 可用于读取集群中的既有资源；
// 返回（可能追加过新对象的）对象列表。
type Trait interface {
	// GetOrder 返回执行顺序档位，见上方常量。
	GetOrder() int

	// Apply 依次改写 manifests 并返回新的对象列表。
	Apply(ctx context.Context, manifests []client.Object, c client.Client) ([]client.Object, error)
}

// Factory 由 trait 属性（原始 JSON）构造 Trait 实例。
type Factory func(properties runtime.RawExtension) (Trait, error)

var (
	mu       sync.RWMutex
	registry = map[string]Factory{}
)

// Register 注册一个 Trait 工厂，一般在实现包的 init() 中调用。
func Register(traitType string, factory Factory) {
	mu.Lock()
	defer mu.Unlock()
	registry[traitType] = factory
}

// Get 按 trait 类型取已注册的工厂，未注册时返回错误。
func Get(traitType string) (Factory, error) {
	mu.RLock()
	defer mu.RUnlock()
	factory, ok := registry[traitType]
	if !ok {
		return nil, fmt.Errorf("unknown trait type %q", traitType)
	}
	return factory, nil
}
