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
	"encoding/json"
	"fmt"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// apply 类似 kubectl apply：以三路合并补丁把期望变动同步到集群对象，
// 只发送差异部分以减少冲突；同时做垃圾回收，删除不再需要的资源。
func (r *DeployUnitReconciler) apply(ctx context.Context, manifests ...Manifest) error {
	logger := ctrl.LoggerFrom(ctx)
	for _, manifest := range manifests {
		switch {
		case manifest.Desired != nil:
			// 创建或更新
			existing, err := r.createOrGet(ctx, manifest.Desired)
			if err != nil {
				return fmt.Errorf("failed to create or get %s %s/%s: %w",
					manifest.Desired.GetObjectKind().GroupVersionKind().Kind,
					manifest.Desired.GetNamespace(), manifest.Desired.GetName(), err)
			}

			patch, err := threeWayMergePatch(manifest.Desired, manifest.LastApplied, existing)
			if err != nil {
				return fmt.Errorf("failed to build three way merge patch: %w", err)
			}
			if err := r.Patch(ctx, existing, patch); err != nil {
				r.logPatchFailure(logger, manifest, existing, patch, err)
				return fmt.Errorf("failed to apply patch: %w", err)
			}

			logger.V(1).Info("resource created or updated",
				"kind", manifest.Desired.GetObjectKind().GroupVersionKind().Kind,
				"namespace", manifest.Desired.GetNamespace(),
				"name", manifest.Desired.GetName(),
				"is_create", manifest.LastApplied == nil,
			)

		case manifest.LastApplied != nil:
			// 垃圾回收：desired 中已不存在的资源
			if err := r.Delete(ctx, manifest.LastApplied); client.IgnoreNotFound(err) != nil {
				return fmt.Errorf("failed to delete %s %s/%s: %w",
					manifest.LastApplied.GetObjectKind().GroupVersionKind().Kind,
					manifest.LastApplied.GetNamespace(), manifest.LastApplied.GetName(), err)
			}
			logger.Info("resource deleted",
				"kind", manifest.LastApplied.GetObjectKind().GroupVersionKind().Kind,
				"namespace", manifest.LastApplied.GetNamespace(),
				"name", manifest.LastApplied.GetName(),
			)
		}
	}
	return nil
}

// createOrGet 读取集群中的现存对象；不存在时按期望内容创建。
// 返回的 existing 携带服务端字段（resourceVersion 等），可直接用于 Patch。
func (r *DeployUnitReconciler) createOrGet(ctx context.Context, obj client.Object) (client.Object, error) {
	existing, ok := obj.DeepCopyObject().(client.Object)
	if !ok {
		return nil, fmt.Errorf("object %T is not a client.Object", obj)
	}

	err := r.Get(ctx, client.ObjectKeyFromObject(obj), existing)
	if client.IgnoreNotFound(err) != nil {
		return nil, err
	}
	if err != nil {
		// 不存在：existing 仍是期望内容，直接创建
		return existing, r.Create(ctx, existing)
	}
	return existing, nil
}

// logPatchFailure 打印三路合并失败时的三方现场，便于排查。
func (r *DeployUnitReconciler) logPatchFailure(logger logr.Logger, manifest Manifest, existing client.Object, patch client.Patch, applyErr error) {
	entry := logger.WithValues(
		"kind", manifest.Desired.GetObjectKind().GroupVersionKind().Kind,
		"namespace", existing.GetNamespace(),
		"name", existing.GetName(),
	)
	if data, err := json.Marshal(existing); err == nil {
		entry.Info("existing state", "data", string(data))
	}
	if data, err := json.Marshal(manifest.Desired); err == nil {
		entry.Info("desired state", "data", string(data))
	}
	if manifest.LastApplied != nil {
		if data, err := json.Marshal(manifest.LastApplied); err == nil {
			entry.Info("last applied state", "data", string(data))
		}
	}
	if data, err := patch.Data(existing); err == nil {
		entry.Error(applyErr, "failed to apply patch", "patch", string(data))
	}
}
