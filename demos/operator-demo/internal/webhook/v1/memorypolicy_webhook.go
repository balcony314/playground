// Copyright 2026 balcony314.
// SPDX-License-Identifier: MIT

package v1

import (
	"context"

	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	memoryv1 "github.com/balcony314/operator-demo/api/v1"
)

// nolint:unused
// log is for logging in this package.
var memorypolicylog = logf.Log.WithName("memorypolicy-resource")

// SetupMemoryPolicyWebhookWithManager registers the webhook for MemoryPolicy in the manager.
func SetupMemoryPolicyWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &memoryv1.MemoryPolicy{}).
		WithValidator(&MemoryPolicyCustomValidator{}).
		Complete()
}

// TODO(user): change verbs to "verbs=create;update;delete" if you want to enable deletion validation.
// NOTE: If you want to customise the 'path', use the flags '--defaulting-path' or '--validation-path'.
// +kubebuilder:webhook:path=/validate-memory-example-com-v1-memorypolicy,mutating=false,failurePolicy=fail,sideEffects=None,groups=memory.example.com,resources=memorypolicies,verbs=create;update,versions=v1,name=vmemorypolicy-v1.kb.io,admissionReviewVersions=v1

// MemoryPolicyCustomValidator struct is responsible for validating the MemoryPolicy resource
// when it is created, updated, or deleted.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as this struct is used only for temporary operations and does not need to be deeply copied.
type MemoryPolicyCustomValidator struct{}

// validateMemoryPolicy 校验 MemoryPolicy 字段：
// - threshold 必须在 0-100 之间
// - action 必须是 add-label 或 add-annotation
// - marker.key 不能为空
func (v *MemoryPolicyCustomValidator) validateMemoryPolicy(obj *memoryv1.MemoryPolicy) (admission.Warnings, error) {
	var allErrs field.ErrorList
	spec := &obj.Spec

	// threshold 必须在 0-100 之间
	if spec.Threshold < 0 || spec.Threshold > 100 {
		allErrs = append(allErrs, field.Invalid(
			field.NewPath("spec", "threshold"), spec.Threshold, "must be between 0 and 100",
		))
	}

	// action 必须是允许的枚举值
	switch spec.Action {
	case memoryv1.ActionAddLabel, memoryv1.ActionAddAnnotation:
	default:
		allErrs = append(allErrs, field.NotSupported(
			field.NewPath("spec", "action"), spec.Action, []string{memoryv1.ActionAddLabel, memoryv1.ActionAddAnnotation},
		))
	}

	// marker.key 不能为空
	if spec.Marker.Key == "" {
		allErrs = append(allErrs, field.Required(
			field.NewPath("spec", "marker", "key"), "marker.key must not be empty",
		))
	}

	if len(allErrs) > 0 {
		return nil, allErrs.ToAggregate()
	}
	return nil, nil
}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type MemoryPolicy.
func (v *MemoryPolicyCustomValidator) ValidateCreate(_ context.Context, obj *memoryv1.MemoryPolicy) (admission.Warnings, error) {
	memorypolicylog.Info("Validation for MemoryPolicy upon creation", "name", obj.GetName())

	return v.validateMemoryPolicy(obj)
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type MemoryPolicy.
func (v *MemoryPolicyCustomValidator) ValidateUpdate(_ context.Context, oldObj, newObj *memoryv1.MemoryPolicy) (admission.Warnings, error) {
	memorypolicylog.Info("Validation for MemoryPolicy upon update", "name", newObj.GetName())

	return v.validateMemoryPolicy(newObj)
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type MemoryPolicy.
func (v *MemoryPolicyCustomValidator) ValidateDelete(_ context.Context, obj *memoryv1.MemoryPolicy) (admission.Warnings, error) {
	memorypolicylog.Info("Validation for MemoryPolicy upon deletion", "name", obj.GetName())

	return nil, nil
}
