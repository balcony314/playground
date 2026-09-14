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

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// 控制器行为注解。除 pause 外，均由控制器在 reconcile 过程中读写；
// 提交方在提交新的期望状态时应清除 reconciled，以触发重新同步。
const (
	// AnnotationPause 为 true 时跳过整个 reconcile。
	AnnotationPause = "oam.example.com/pause"
	// AnnotationReconciled 为 true 表示当前声明已被成功应用，跳过 reconcile。
	// 每次成功应用后由控制器置位。
	AnnotationReconciled = "oam.example.com/reconciled"
	// AnnotationPreventRebuild 为 true 时，重建对象前保留集群中现存
	// 工作负载（Deployment/StatefulSet）的 Pod 模板，仅同步其余字段。
	AnnotationPreventRebuild = "oam.example.com/prevent-rebuild"
	// AnnotationControllerVersion 记录处理该对象的控制器版本，
	// 取值来自环境变量 CONTROLLER_VERSION。
	AnnotationControllerVersion = "oam.example.com/controller-version"
)

// DeployUnitSpec 定义 DeployUnit 的期望状态。
type DeployUnitSpec struct {
	// Components 组成该 DeployUnit 的组件列表，每项独立渲染后合并入最终资源集合。
	Components []DeployUnitComponent `json:"components"`
}

// DeployUnitComponent 描述应用的一个组件。
//
// 每个 Component 由对应工厂（internal/render/component/components，
// 通过 init() 自注册）渲染为一组 Kubernetes 原生资源，
// 例如 stateless -> Deployment、stateful -> StatefulSet。
type DeployUnitComponent struct {
	// Name 组件名称，在同一 DeployUnit 内需唯一。
	// 渲染出的工作负载主容器与资源命名均使用该名称。
	Name string `json:"name"`

	// Type 组件类型，决定使用哪个 Component 工厂渲染，
	// 当前支持 "stateless"、"stateful"。
	Type string `json:"type"`

	// Properties 组件属性，以原始 JSON 存储，由对应工厂自行
	// 反序列化为具体 spec 结构（见 components.WorkloadSpec）。
	// +kubebuilder:pruning:PreserveUnknownFields
	Properties runtime.RawExtension `json:"properties,omitempty"`

	// Traits 作用于本组件的 trait 列表。实际执行时按 trait.GetOrder()
	// 升序依次 Apply，order 越大越靠后执行，后者可覆盖前者设置的字段。
	Traits []DeployUnitTrait `json:"traits,omitempty"`
}

// DeployUnitTrait 定义作用于某个 Component 的增强能力。
//
// Trait 不直接产出资源，而是在 Component 已渲染出的对象列表上做增改，
// 例如 scaler 设置副本数、sidecar 注入边车容器、mount 挂载配置。
// 工厂见 internal/render/trait/traits，通过 init() 自注册。
type DeployUnitTrait struct {
	// Type trait 类型，决定使用哪个 Trait 工厂，例如 "scaler"。
	Type string `json:"type"`

	// Properties trait 属性，以原始 JSON 存储，由对应工厂自行反序列化。
	// +kubebuilder:pruning:PreserveUnknownFields
	Properties runtime.RawExtension `json:"properties,omitempty"`
}

// DeployUnitStatus 定义 DeployUnit 的观测状态。
//
// 当前未承载具体字段，应用结果通过注解（reconciled、controller-version）
// 而非 status 回写。
type DeployUnitStatus struct{}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName="du"
// +kubebuilder:printcolumn:name="ControllerVersion",type=string,JSONPath=`.metadata.annotations.oam\.example\.com/controller-version`
// +kubebuilder:printcolumn:name="Paused",type=string,JSONPath=`.metadata.annotations.oam\.example\.com/pause`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// DeployUnit 是 OAM 应用部署声明的入口：由若干 Component 及其 Trait 组成，
// 控制器将其渲染为 Kubernetes 原生资源并以三路合并方式持续同步。
// 上次成功应用的快照写入同名的 DeployUnitHistory，作为下次合并的 lastApplied。
type DeployUnit struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DeployUnitSpec   `json:"spec,omitempty"`
	Status DeployUnitStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DeployUnitList 是 DeployUnit 资源的列表容器。
type DeployUnitList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DeployUnit `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DeployUnit{}, &DeployUnitList{})
}
