// Copyright 2026 balcony314.
// SPDX-License-Identifier: MIT

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// Action 枚举值：Spec.Action 只允许取以下二者之一（与 +kubebuilder:validation:Enum 对应）。
const (
	// ActionAddLabel 表示超阈值时添加 label
	ActionAddLabel = "add-label"
	// ActionAddAnnotation 表示超阈值时添加 annotation
	ActionAddAnnotation = "add-annotation"
)

// MemoryPolicySpec defines the desired state of MemoryPolicy
type MemoryPolicySpec struct {
	// Important: Run "make" to regenerate code after modifying this file

	// Namespace 是目标命名空间，不填则监控所有命名空间
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// Threshold 是内存阈值百分比（如 80 表示 80%），取值范围 0-100
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	Threshold int `json:"threshold"`

	// Action 是触发动作：add-label（添加标签）或 add-annotation（添加注解）
	// +kubebuilder:validation:Enum=add-label;add-annotation
	Action string `json:"action"`

	// Marker 是触发时要添加的标签或注解键值对
	Marker Marker `json:"marker"`
}

// Marker 定义要添加的标签或注解的键值对
// +kubebuilder:object:generate=true
type Marker struct {
	// Key 是标签或注解的键
	Key string `json:"key"`

	// Value 是标签或注解的值
	Value string `json:"value"`
}

// MemoryPolicyStatus defines the observed state of MemoryPolicy.
type MemoryPolicyStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// conditions represent the current state of the MemoryPolicy resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// MemoryPolicy is the Schema for the memorypolicies API
type MemoryPolicy struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of MemoryPolicy
	// +required
	Spec MemoryPolicySpec `json:"spec"`

	// status defines the observed state of MemoryPolicy
	// +optional
	Status MemoryPolicyStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// MemoryPolicyList contains a list of MemoryPolicy
type MemoryPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []MemoryPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &MemoryPolicy{}, &MemoryPolicyList{})
		return nil
	})
}
