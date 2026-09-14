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
)

// DeployUnitHistorySpec 定义 DeployUnitHistory 的期望状态。
type DeployUnitHistorySpec struct {
	// DeployUnit 保存上次成功应用的 DeployUnit 快照，
	// 是三路合并 patch 的 lastApplied 来源。
	// 快照内嵌完整对象（含服务端填充的 metadata），保留未知字段以免被裁剪。
	// +kubebuilder:pruning:PreserveUnknownFields
	DeployUnit DeployUnit `json:"deployUnit,omitempty"`
}

// DeployUnitHistoryStatus 定义 DeployUnitHistory 的观测状态。
type DeployUnitHistoryStatus struct{}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// DeployUnitHistory 存储同名 DeployUnit 上次成功应用的快照，
// 由控制器自动维护，与 DeployUnit 一一对应（属主引用关联）。
type DeployUnitHistory struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DeployUnitHistorySpec    `json:"spec,omitempty"`
	Status DeployUnitHistoryStatus  `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DeployUnitHistoryList 是 DeployUnitHistory 资源的列表容器。
type DeployUnitHistoryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DeployUnitHistory `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DeployUnitHistory{}, &DeployUnitHistoryList{})
}
