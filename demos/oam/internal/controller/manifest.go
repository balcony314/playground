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

package controller

import (
	"context"
	"errors"
	"fmt"
	"sort"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"

	oamv1 "github.com/balcony314/oam/api/v1"
	"github.com/balcony314/oam/internal/render"
	"github.com/balcony314/oam/internal/render/component"
	"github.com/balcony314/oam/internal/render/trait"
)

// Manifest 是一个待同步资源的期望态与上次应用态的配对。
// Desired 为 nil 表示资源需要删除（垃圾回收）；
// LastApplied 为 nil 表示资源需要创建。
type Manifest struct {
	Desired     client.Object
	LastApplied client.Object
}

// renderManifests 分别渲染 desired 与 lastApplied 两个 DeployUnit，
// 并按资源唯一键做差集，得到需要创建、更新、删除的清单。
func (r *DeployUnitReconciler) renderManifests(ctx context.Context, desired, lastApplied *oamv1.DeployUnit) ([]Manifest, error) {
	desiredList, err := r.renderObjects(ctx, desired)
	if err != nil {
		return nil, err
	}
	lastAppliedList, err := r.renderObjects(ctx, lastApplied)
	if err != nil {
		return nil, err
	}

	desiredMap, err := r.objectListToMap(desiredList)
	if err != nil {
		return nil, err
	}
	lastAppliedMap, err := r.objectListToMap(lastAppliedList)
	if err != nil {
		return nil, err
	}

	var manifests []Manifest
	for _, obj := range desiredList {
		key, err := r.uniqueKey(obj)
		if err != nil {
			return nil, err
		}
		if lastObj, ok := lastAppliedMap[key]; ok {
			// 需要更新的
			manifests = append(manifests, Manifest{Desired: obj, LastApplied: lastObj})
		} else {
			// 需要创建的
			manifests = append(manifests, Manifest{Desired: obj})
		}
	}
	for _, obj := range lastAppliedList {
		key, err := r.uniqueKey(obj)
		if err != nil {
			return nil, err
		}
		if _, ok := desiredMap[key]; !ok {
			// 需要删除的
			manifests = append(manifests, Manifest{LastApplied: obj})
		}
	}
	return manifests, nil
}

// renderObjects 把一个 DeployUnit 的所有 Component + Trait 渲染为资源列表。
func (r *DeployUnitReconciler) renderObjects(ctx context.Context, deployUnit *oamv1.DeployUnit) ([]client.Object, error) {
	result := make([]client.Object, 0)
	if deployUnit == nil {
		return result, nil
	}

	for _, c := range deployUnit.Spec.Components {
		ctx = render.NewContext(ctx, deployUnit, c.Name)

		comp, err := buildComponent(&c)
		if err != nil {
			return nil, err
		}
		manifests, err := comp.Render(ctx, c.Name)
		if err != nil {
			return nil, err
		}

		// Trait 按 GetOrder() 升序依次 Apply，
		// 后执行的 Trait 可覆盖先执行的设置的字段。
		traits, err := buildTraits(c.Traits)
		if err != nil {
			return nil, err
		}
		for _, t := range traits {
			manifests, err = t.Apply(ctx, manifests, r.Client)
			if err != nil {
				return nil, err
			}
		}
		result = append(result, manifests...)
	}

	// 统一建立属主引用，DeployUnit 删除时级联清理。
	for _, obj := range result {
		if err := ctrl.SetControllerReference(deployUnit, obj, r.Scheme); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// buildComponent 按类型取工厂并构造 Component 实例。
func buildComponent(componentDef *oamv1.DeployUnitComponent) (component.Component, error) {
	if componentDef.Properties.Raw == nil {
		return nil, errors.New("deployUnit component properties is null")
	}
	factory, err := component.Get(componentDef.Type)
	if err != nil {
		return nil, err
	}
	return factory(componentDef.Properties)
}

// buildTraits 构造 Trait 实例并按执行档位排序。
// 未注册类型或无属性的 trait 静默跳过，保持声明容错。
func buildTraits(traitDefs []oamv1.DeployUnitTrait) ([]trait.Trait, error) {
	var result []trait.Trait
	for _, t := range traitDefs {
		if t.Properties.Raw == nil {
			continue
		}
		factory, err := trait.Get(t.Type)
		if err != nil {
			continue
		}
		item, err := factory(t.Properties)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	sort.SliceStable(result, func(i, j int) bool {
		return result[i].GetOrder() < result[j].GetOrder()
	})
	return result, nil
}

// uniqueKey 返回资源的唯一键：group/kind|namespace/name。
// 仅按 kind 而非 version 区分，避免 version 变化导致误删重建。
func (r *DeployUnitReconciler) uniqueKey(obj client.Object) (string, error) {
	gvk, err := apiutil.GVKForObject(obj, r.Scheme)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s/%s|%s/%s", gvk.Group, gvk.Kind, obj.GetNamespace(), obj.GetName()), nil
}

// objectListToMap 把资源列表按唯一键转为映射，供差集计算。
func (r *DeployUnitReconciler) objectListToMap(list []client.Object) (map[string]client.Object, error) {
	result := make(map[string]client.Object, len(list))
	for _, obj := range list {
		key, err := r.uniqueKey(obj)
		if err != nil {
			return nil, err
		}
		result[key] = obj
	}
	return result, nil
}
