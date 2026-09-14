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
	"testing"

	jsonpatch "github.com/evanphx/json-patch/v5"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	oamv1 "github.com/balcony314/oam/api/v1"
)

// deploymentFixture 构造一个带副本与镜像的 Deployment 测试对象。
func deploymentFixture(replicas int32, image string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "main", Image: image}},
				},
			},
		},
	}
}

// mustPatchData 取出补丁数据，失败即中止测试。
func mustPatchData(t *testing.T, desired, lastApplied, existing client.Object) []byte {
	t.Helper()
	patch, err := threeWayMergePatch(desired, lastApplied, existing)
	if err != nil {
		t.Fatalf("threeWayMergePatch: %v", err)
	}
	data, err := patch.Data(existing)
	if err != nil {
		t.Fatalf("patch data: %v", err)
	}
	return data
}

func TestThreeWayMergePatch_BuiltinUsesStrategicMerge(t *testing.T) {
	lastApplied := deploymentFixture(1, "nginx:1.0")
	desired := deploymentFixture(3, "nginx:1.0")
	existing := deploymentFixture(2, "nginx:1.0")

	data := mustPatchData(t, desired, lastApplied, existing)
	if got := string(data); got != `{"spec":{"replicas":3}}` {
		t.Fatalf("unexpected patch data: %s", got)
	}
}

func TestThreeWayMergePatch_PreservesOutOfBandChanges(t *testing.T) {
	// 场景：lastApplied 副本 1；他人给集群对象加了 spec 之外的 label
	//（未受管字段）并把副本改为 5（受管字段）；本次仅改镜像。
	// 语义与 kubectl apply 一致：
	//   - 未受管字段（label）不进补丁，他人改动保留；
	//   - 受管字段（replicas）漂移会被拉回声明值 1。
	lastApplied := deploymentFixture(1, "nginx:1.0")
	desired := deploymentFixture(1, "nginx:2.0")
	existing := deploymentFixture(5, "nginx:1.0")
	existing.Labels = map[string]string{"team": "platform"}

	data := mustPatchData(t, desired, lastApplied, existing)

	var patchMap map[string]any
	if err := json.Unmarshal(data, &patchMap); err != nil {
		t.Fatalf("unmarshal patch: %v", err)
	}
	if _, ok := patchMap["metadata"]; ok {
		t.Fatalf("patch should not touch out-of-band metadata: %s", data)
	}

	// 验证补丁可干净应用：label 保留、镜像更新、副本拉回声明值
	existingRaw, _ := json.Marshal(existing)
	merged, err := jsonpatch.MergePatch(existingRaw, data)
	if err != nil {
		t.Fatalf("merge patch: %v", err)
	}
	var mergedDeployment appsv1.Deployment
	if err := json.Unmarshal(merged, &mergedDeployment); err != nil {
		t.Fatalf("unmarshal merged: %v", err)
	}
	if got := mergedDeployment.Labels["team"]; got != "platform" {
		t.Fatalf("out-of-band label should be preserved, got %q", got)
	}
	if got := *mergedDeployment.Spec.Replicas; got != 1 {
		t.Fatalf("managed replicas should be reverted to 1, got %d", got)
	}
	if got := mergedDeployment.Spec.Template.Spec.Containers[0].Image; got != "nginx:2.0" {
		t.Fatalf("image not updated: %s", got)
	}
}

func TestThreeWayMergePatch_CustomResourceUsesMergePatch(t *testing.T) {
	// 自定义资源（未注册进 builtinScheme）应走 JSONMergePatch 分支。
	// 集群仍处旧状态（current == lastApplied）时，补丁应表达 spec 变更。
	lastApplied := &oamv1.DeployUnit{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default"},
	}
	lastApplied.Spec.Components = []oamv1.DeployUnitComponent{{Name: "web", Type: "stateless"}}

	desired := lastApplied.DeepCopy()
	desired.Spec.Components = append(desired.Spec.Components, oamv1.DeployUnitComponent{Name: "worker", Type: "stateful"})

	existing := lastApplied.DeepCopy()

	data := mustPatchData(t, desired, lastApplied, existing)

	var patchMap map[string]any
	if err := json.Unmarshal(data, &patchMap); err != nil {
		t.Fatalf("unmarshal patch: %v", err)
	}
	spec, ok := patchMap["spec"].(map[string]any)
	if !ok {
		t.Fatalf("patch should contain spec changes: %s", data)
	}
	if _, ok := spec["components"]; !ok {
		t.Fatalf("patch should contain components change: %s", data)
	}
}

func TestThreeWayMergePatch_NoopWhenAlreadySynced(t *testing.T) {
	// 集群已与期望一致时应产生空补丁（no-op）。
	lastApplied := deploymentFixture(1, "nginx:1.0")
	desired := deploymentFixture(2, "nginx:2.0")
	existing := desired.DeepCopy()

	data := mustPatchData(t, desired, lastApplied, existing)
	if got := string(data); got != "{}" {
		t.Fatalf("expected empty patch, got %s", got)
	}
}
