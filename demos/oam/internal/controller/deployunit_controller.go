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

// Package controller 实现 DeployUnit 的 reconcile 主循环。
package controller

import (
	"context"
	"os"
	"reflect"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	oamv1 "github.com/balcony314/oam/api/v1"

	// 触发内置 Component / Trait 的 init() 自注册。
	_ "github.com/balcony314/oam/internal/render/component/components"
	_ "github.com/balcony314/oam/internal/render/trait/traits"
)

// controllerVersionDefault 是未设置 CONTROLLER_VERSION 环境变量时
// 写入 controller-version 注解的默认值。
const controllerVersionDefault = "dev"

// DeployUnitReconciler 把 DeployUnit 声明同步为集群资源。
type DeployUnitReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// SetupWithManager 将控制器注册到 Manager。
//
// 仅在 spec / 标签 / 注解变化时触发 reconcile；忽略删除事件，
// 下游资源通过属主引用由 Kubernetes 垃圾回收级联删除。
func (r *DeployUnitReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&oamv1.DeployUnit{}, builder.WithPredicates(predicate.Funcs{
			UpdateFunc: specOrMetadataChanged,
			DeleteFunc: func(event.DeleteEvent) bool {
				return false
			},
		})).
		Complete(r)
}

// specOrMetadataChanged 判断更新事件是否需要处理：
// generation 变化（spec 更新）、spec 内容变化或元数据变化均需处理。
func specOrMetadataChanged(e event.UpdateEvent) bool {
	if e.ObjectNew.GetGeneration() != e.ObjectOld.GetGeneration() {
		return true
	}
	oldObj, newObj := e.ObjectOld, e.ObjectNew
	if oldDu, ok := oldObj.(*oamv1.DeployUnit); ok {
		if newDu, ok := newObj.(*oamv1.DeployUnit); ok {
			return !reflect.DeepEqual(oldDu.Spec, newDu.Spec) ||
				!reflect.DeepEqual(oldDu.GetAnnotations(), newDu.GetAnnotations()) ||
				!reflect.DeepEqual(oldDu.GetLabels(), newDu.GetLabels())
		}
	}
	// 非预期类型时保守放行。
	return true
}

// +kubebuilder:rbac:groups=oam.example.com,resources=deployunits,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=oam.example.com,resources=deployunits/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=oam.example.com,resources=deployunits/finalizers,verbs=update
// +kubebuilder:rbac:groups=oam.example.com,resources=deployunithistories,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=oam.example.com,resources=deployunithistories/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=oam.example.com,resources=deployunithistories/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=statefulsets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete

// Reconcile 执行一次同步：渲染期望与上次应用的资源集合，三路合并到集群。
func (r *DeployUnitReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := ctrl.LoggerFrom(ctx)

	var deployUnit oamv1.DeployUnit
	if err := r.Get(ctx, req.NamespacedName, &deployUnit); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if v, ok := deployUnit.Annotations[oamv1.AnnotationReconciled]; ok && v == "true" {
		logger.V(1).Info("deployUnit already reconciled, skip")
		return ctrl.Result{}, nil
	}
	if v, ok := deployUnit.Annotations[oamv1.AnnotationPause]; ok && v == "true" {
		logger.Info("deployUnit paused, skip reconcile")
		return ctrl.Result{}, nil
	}

	var lastApplied oamv1.DeployUnit
	if err := r.getLastAppliedDeployUnit(ctx, &deployUnit, &lastApplied); err != nil {
		logger.Error(err, "failed to get last applied deployUnit")
		return ctrl.Result{}, err
	}

	manifests, err := r.renderManifests(ctx, &deployUnit, &lastApplied)
	if err != nil {
		logger.Error(err, "failed to render manifests")
		return ctrl.Result{}, err
	}

	if v, ok := deployUnit.Annotations[oamv1.AnnotationPreventRebuild]; ok && v == "true" {
		r.preventRebuild(ctx, manifests)
	}

	if err := r.apply(ctx, manifests...); err != nil {
		logger.Error(err, "failed to apply manifests")
		return ctrl.Result{}, err
	}

	if deployUnit.Annotations == nil {
		deployUnit.Annotations = map[string]string{}
	}
	deployUnit.Annotations[oamv1.AnnotationReconciled] = "true"
	deployUnit.Annotations[oamv1.AnnotationControllerVersion] = controllerVersion()
	if err := r.Update(ctx, &deployUnit); err != nil {
		logger.Error(err, "failed to mark deployUnit reconciled")
		return ctrl.Result{}, err
	}

	if err := r.updateLastAppliedDeployUnit(ctx, &deployUnit); err != nil {
		logger.Error(err, "failed to update last applied deployUnit")
		return ctrl.Result{}, err
	}

	logger.Info("reconcile deployUnit success")
	return ctrl.Result{}, nil
}

// controllerVersion 返回控制器版本标识。
func controllerVersion() string {
	if v := os.Getenv("CONTROLLER_VERSION"); v != "" {
		return v
	}
	return controllerVersionDefault
}

// preventRebuild 在 prevent-rebuild 注解生效时，用集群中现存工作负载的
// Pod 模板回填 desired 与 lastApplied，避免重建 Pod。
func (r *DeployUnitReconciler) preventRebuild(ctx context.Context, manifests []Manifest) {
	for i := range manifests {
		m := manifests[i]
		if m.Desired == nil || m.LastApplied == nil {
			continue
		}

		switch desired := m.Desired.(type) {
		case *appsv1.Deployment:
			var live appsv1.Deployment
			if err := r.Get(ctx, client.ObjectKeyFromObject(desired), &live); err != nil {
				continue
			}
			desired.Spec.Template = *live.Spec.Template.DeepCopy()
			m.LastApplied.(*appsv1.Deployment).Spec.Template = *live.Spec.Template.DeepCopy()
		case *appsv1.StatefulSet:
			var live appsv1.StatefulSet
			if err := r.Get(ctx, client.ObjectKeyFromObject(desired), &live); err != nil {
				continue
			}
			desired.Spec.Template = *live.Spec.Template.DeepCopy()
			m.LastApplied.(*appsv1.StatefulSet).Spec.Template = *live.Spec.Template.DeepCopy()
		default:
			continue
		}
		manifests[i] = m
	}
}

// getLastAppliedDeployUnit 从同名 DeployUnitHistory 读取上次应用的快照。
// 历史不存在时返回零值（首次部署场景）。
func (r *DeployUnitReconciler) getLastAppliedDeployUnit(ctx context.Context, deployUnit *oamv1.DeployUnit, lastApplied *oamv1.DeployUnit) error {
	var history oamv1.DeployUnitHistory
	if err := r.Get(ctx, client.ObjectKeyFromObject(deployUnit), &history); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	*lastApplied = history.Spec.DeployUnit
	lastApplied.Name = history.Name
	lastApplied.Namespace = history.Namespace
	lastApplied.Labels = history.Labels
	lastApplied.Annotations = history.Annotations
	return nil
}

// updateLastAppliedDeployUnit 把成功应用的 DeployUnit 快照写入同名
// DeployUnitHistory，并建立属主引用。
func (r *DeployUnitReconciler) updateLastAppliedDeployUnit(ctx context.Context, deployUnit *oamv1.DeployUnit) error {
	var history oamv1.DeployUnitHistory
	history.Name = deployUnit.Name
	history.Namespace = deployUnit.Namespace
	_, err := ctrl.CreateOrUpdate(ctx, r.Client, &history, func() error {
		history.Spec.DeployUnit = *deployUnit.DeepCopy()
		history.Labels = deployUnit.Labels
		history.Annotations = deployUnit.Annotations
		return ctrl.SetControllerReference(deployUnit, &history, r.Scheme)
	})
	return err
}
