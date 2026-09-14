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

package render_test

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	oamv1 "github.com/balcony314/oam/api/v1"
	"github.com/balcony314/oam/internal/render"
	"github.com/balcony314/oam/internal/render/component"
	"github.com/balcony314/oam/internal/render/trait"
	// 触发内置实现注册。
	_ "github.com/balcony314/oam/internal/render/component/components"
	_ "github.com/balcony314/oam/internal/render/trait/traits"
)

func TestComponentRegistry(t *testing.T) {
	for _, componentType := range []string{"stateless", "stateful"} {
		if _, err := component.Get(componentType); err != nil {
			t.Errorf("builtin component %q should be registered: %v", componentType, err)
		}
	}
	if _, err := component.Get("unknown"); err == nil {
		t.Error("unknown component type should return an error")
	}
}

func TestTraitRegistry(t *testing.T) {
	for _, traitType := range []string{"scaler", "sidecar", "mount"} {
		if _, err := trait.Get(traitType); err != nil {
			t.Errorf("builtin trait %q should be registered: %v", traitType, err)
		}
	}
	if _, err := trait.Get("unknown"); err == nil {
		t.Error("unknown trait type should return an error")
	}
}

func TestNewContextFiltersInternalAnnotations(t *testing.T) {
	du := &oamv1.DeployUnit{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "app",
			Namespace: "default",
			Labels:      map[string]string{"team": "platform"},
			Annotations: map[string]string{
				oamv1.AnnotationReconciled:       "true",
				oamv1.AnnotationPause:            "true",
				oamv1.AnnotationPreventRebuild:   "true",
				oamv1.AnnotationControllerVersion: "dev",
				"example.com/owner":              "alice",
			},
		},
	}

	rc := render.FromContext(render.NewContext(context.Background(), du, "web"))

	if rc.DeployUnitName != "app" || rc.Namespace != "default" || rc.ComponentName != "web" {
		t.Fatalf("unexpected render context: %+v", rc)
	}
	if _, ok := rc.Annotations["example.com/owner"]; !ok {
		t.Error("external annotation should be passed through")
	}
	for _, key := range []string{
		oamv1.AnnotationReconciled,
		oamv1.AnnotationPause,
		oamv1.AnnotationPreventRebuild,
		oamv1.AnnotationControllerVersion,
	} {
		if _, ok := rc.Annotations[key]; ok {
			t.Errorf("internal annotation %q should be filtered out", key)
		}
	}
	if rc.Labels["team"] != "platform" {
		t.Error("labels should be passed through")
	}
}

func TestFromContextMissing(t *testing.T) {
	if rc := render.FromContext(context.Background()); rc.DeployUnitName != "" {
		t.Fatalf("expected zero render context, got %+v", rc)
	}
}

// 确认 RawExtension 反序列化失败的工厂会返回错误而不是 panic。
func TestStatelessFactoryRejectsInvalidProperties(t *testing.T) {
	factory, err := component.Get("stateless")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := factory(runtime.RawExtension{Raw: []byte(`{"image":}`)}); err == nil {
		t.Error("invalid JSON should return an error")
	}
	if _, err := factory(runtime.RawExtension{Raw: []byte(`{}`)}); err == nil {
		t.Error("missing image should return an error")
	}
}
