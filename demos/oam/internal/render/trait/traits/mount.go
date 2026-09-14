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

package traits

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/balcony314/oam/internal/render"
	"github.com/balcony314/oam/internal/render/trait"
)

func init() {
	trait.Register("mount", NewMount)
}

// mountSpec 是 mount trait 的属性模式。
type mountSpec struct {
	// MountPath 是容器内挂载点。
	MountPath string `json:"mountPath"`

	// Container 是目标容器名，默认为组件主容器。
	Container string `json:"container,omitempty"`

	// VolumeName 是卷名，默认按挂载路径哈希生成。
	VolumeName string `json:"volumeName,omitempty"`

	// Files 是写入 ConfigMap 的文件集合。
	Files []struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	} `json:"files"`
}

// Mount 把配置内容渲染为 ConfigMap，并挂载到工作负载容器。
//
// 该 trait 展示了 trait 追加新对象的能力：ConfigMap 会被追加进
// 渲染结果，随其余资源一起参与三路合并与垃圾回收。
type Mount struct {
	spec mountSpec
}

// NewMount 从原始 JSON 属性构造 mount trait。
func NewMount(properties runtime.RawExtension) (trait.Trait, error) {
	spec := mountSpec{}
	if err := json.Unmarshal(properties.Raw, &spec); err != nil {
		return nil, fmt.Errorf("invalid mount properties: %w", err)
	}
	if spec.MountPath == "" {
		return nil, fmt.Errorf("mount properties: mountPath is required")
	}
	if len(spec.Files) == 0 {
		return nil, fmt.Errorf("mount properties: at least one file is required")
	}
	return &Mount{spec: spec}, nil
}

// GetOrder 返回执行档位：PostOrder。
func (t *Mount) GetOrder() int {
	return trait.PostOrder
}

// Apply 实现 trait.Trait，向对象列表追加 ConfigMap，
// 并在目标容器上挂载对应的卷。
func (t *Mount) Apply(ctx context.Context, manifests []client.Object, _ client.Client) ([]client.Object, error) {
	rc := render.FromContext(ctx)

	volumeName := t.spec.VolumeName
	if volumeName == "" {
		volumeName = "vol-" + hash(t.spec.MountPath)
	}

	for _, manifest := range manifests {
		var podSpec *corev1.PodSpec
		switch obj := manifest.(type) {
		case *appsv1.Deployment:
			podSpec = &obj.Spec.Template.Spec
		case *appsv1.StatefulSet:
			podSpec = &obj.Spec.Template.Spec
		default:
			continue
		}

		containerName := t.spec.Container
		if containerName == "" {
			containerName = rc.ComponentName
		}
		container := findContainer(podSpec.Containers, containerName)
		if container == nil {
			continue
		}

		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name:      volumeName,
			MountPath: t.spec.MountPath,
		})
		podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{
			Name: volumeName,
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: configMapName(rc, volumeName)},
				},
			},
		})
	}

	return append(manifests, t.buildConfigMap(rc, volumeName)), nil
}

// buildConfigMap 依据 files 声明构造 ConfigMap。
func (t *Mount) buildConfigMap(rc render.RenderContext, volumeName string) *corev1.ConfigMap {
	data := make(map[string]string, len(t.spec.Files))
	for _, file := range t.spec.Files {
		data[file.Name] = file.Content
	}
	labels := make(map[string]string, len(rc.Labels)+2)
	maps.Copy(labels, rc.Labels)
	labels[render.LabelDeployUnit] = rc.DeployUnitName
	labels[render.LabelComponent] = rc.ComponentName
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName(rc, volumeName),
			Namespace: rc.Namespace,
			Labels:    labels,
		},
		Data: data,
	}
}

// configMapName 生成确定性名称：<deployUnit>-<component>-<volume>。
func configMapName(rc render.RenderContext, volumeName string) string {
	return fmt.Sprintf("%s-%s-%s", rc.DeployUnitName, rc.ComponentName, volumeName)
}

// hash 返回字符串的十六进制 MD5 前 8 位，用于生成稳定短标识。
func hash(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])[:8]
}
