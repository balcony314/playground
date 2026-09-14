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

// Package v1 定义 oam.example.com/v1 API 组的资源模式（Schema）。
// +kubebuilder:object:generate=true
// +groupName=oam.example.com
package v1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	// GroupVersion 是注册本组资源所使用的 group/version。
	GroupVersion = schema.GroupVersion{Group: "oam.example.com", Version: "v1"}

	// SchemeBuilder 用于将 Go 类型注册到 GroupVersionKind scheme。
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

	// AddToScheme 将本组版本中的类型加入给定 scheme。
	AddToScheme = SchemeBuilder.AddToScheme
)
