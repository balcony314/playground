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

// Package render 实现 DeployUnit 的渲染管线：把声明的 Component + Trait
// 组合转换为一组 Kubernetes 原生资源。
package render

import (
	"context"
	"maps"

	oamv1 "github.com/balcony314/oam/api/v1"
)

// 渲染产物上使用的标签，用于把资源关联回 DeployUnit 与组件。
const (
	// LabelDeployUnit 标记资源所属的 DeployUnit 名称。
	LabelDeployUnit = "oam.example.com/deploy-unit"
	// LabelComponent 标记资源所属的组件名称。
	LabelComponent = "oam.example.com/component"
)

// contextKey 是渲染上下文的 context 键类型，避免与其他包的键冲突。
type contextKey struct{}

// RenderContext 携带渲染一个 Component 所需的 DeployUnit 元信息，
// 在渲染管线入口注入，供各 Component/Trait 实现读取。
type RenderContext struct {
	// DeployUnitName / Namespace 是目标资源所在的名称与命名空间。
	DeployUnitName string
	Namespace      string

	// ComponentName 是当前正在渲染的组件名称。
	ComponentName string

	// Labels / Annotations 是从 DeployUnit 透传到下游资源的元数据
	//（已过滤控制器内部注解，见 NewContext）。
	Labels      map[string]string
	Annotations map[string]string
}

// NewContext 基于 DeployUnit 与组件名构造携带 RenderContext 的 context。
// 每个 Component 渲染前调用一次。
func NewContext(ctx context.Context, du *oamv1.DeployUnit, componentName string) context.Context {
	return context.WithValue(ctx, contextKey{}, RenderContext{
		DeployUnitName: du.Name,
		Namespace:      du.Namespace,
		ComponentName:  componentName,
		Labels:         passthroughLabels(du),
		Annotations:    passthroughAnnotations(du),
	})
}

// FromContext 取出渲染上下文；若不存在则返回零值。
func FromContext(ctx context.Context) RenderContext {
	rc, ok := ctx.Value(contextKey{}).(RenderContext)
	if !ok {
		return RenderContext{}
	}
	return rc
}

// passthroughAnnotations 返回可透传给下游资源的注解，
// 排除 pause/reconciled 等控制器内部控制注解。
func passthroughAnnotations(du *oamv1.DeployUnit) map[string]string {
	annotations := make(map[string]string, len(du.Annotations))
	for key, value := range du.Annotations {
		switch key {
		case oamv1.AnnotationPause,
			oamv1.AnnotationReconciled,
			oamv1.AnnotationPreventRebuild,
			oamv1.AnnotationControllerVersion:
			continue
		}
		annotations[key] = value
	}
	return annotations
}

// passthroughLabels 返回可透传给下游资源的标签（当前全量透传）。
func passthroughLabels(du *oamv1.DeployUnit) map[string]string {
	labels := make(map[string]string, len(du.Labels))
	maps.Copy(labels, du.Labels)
	return labels
}
