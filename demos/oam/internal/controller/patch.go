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
	"encoding/json"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/jsonmergepatch"
	"k8s.io/apimachinery/pkg/util/mergepatch"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// builtinScheme 只注册 Kubernetes 内置类型，用于判断对象是否支持
// StrategicMergePatch（仅内置类型支持，自定义资源不支持）。
var builtinScheme = runtime.NewScheme()

func init() {
	_ = clientgoscheme.AddToScheme(builtinScheme)
}

// threeWayMergePatch 基于（lastApplied, desired, existing）三元组计算补丁，
// 语义与 kubectl apply 一致：
//   - 内置资源使用 StrategicMergePatch，保留 merge key 的列表语义；
//   - 自定义资源使用 JSONMergePatch；
//   - lastApplied 与 desired 的差异视为本次变更，
//     existing 相对 lastApplied 的差异（他人改动）在无冲突时保留。
func threeWayMergePatch(desired, lastApplied, existing client.Object) (client.Patch, error) {
	current, err := json.Marshal(existing)
	if err != nil {
		return nil, err
	}
	original, err := json.Marshal(lastApplied)
	if err != nil {
		return nil, err
	}
	modified, err := json.Marshal(desired)
	if err != nil {
		return nil, err
	}

	versionedObject, err := builtinScheme.New(existing.GetObjectKind().GroupVersionKind())
	switch {
	case runtime.IsNotRegisteredError(err):
		// 自定义资源：JSONMergePatch，并校验 apiVersion/kind/name 未变
		patchData, err := jsonmergepatch.CreateThreeWayJSONMergePatch(original, modified, current,
			mergepatch.RequireKeyUnchanged("apiVersion"),
			mergepatch.RequireKeyUnchanged("kind"),
			mergepatch.RequireMetadataKeyUnchanged("name"),
		)
		if err != nil {
			return nil, err
		}
		return client.RawPatch(types.MergePatchType, patchData), nil

	case err != nil:
		return nil, err

	default:
		// 内置资源：StrategicMergePatch
		patchMeta, err := strategicpatch.NewPatchMetaFromStruct(versionedObject)
		if err != nil {
			return nil, err
		}
		patchData, err := strategicpatch.CreateThreeWayMergePatch(original, modified, current, patchMeta, true)
		if err != nil {
			return nil, err
		}
		return client.RawPatch(types.StrategicMergePatchType, patchData), nil
	}
}
