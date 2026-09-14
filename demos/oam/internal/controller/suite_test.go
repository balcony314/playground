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
	"fmt"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	oamv1 "github.com/balcony314/oam/api/v1"
	"github.com/balcony314/oam/internal/render"
)

// These tests use envtest（本地 kube-apiserver + etcd）验证完整 reconcile 链路。
// 未设置 KUBEBUILDER_ASSETS 时跳过；通过 `task test-envtest` 自动准备环境。

var (
	k8sClient client.Client
	testEnv   *envtest.Environment
	ctx       context.Context
	cancel    context.CancelFunc
)

func TestAPIs(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS not set; run `task test-envtest` to run integration tests")
	}
	RegisterFailHandler(Fail)
	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.TODO())

	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	restConfig, err := testEnv.Start()
	Expect(err).NotTo(HaveOccurred())

	scheme := runtime.NewScheme()
	Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
	Expect(oamv1.AddToScheme(scheme)).To(Succeed())

	k8sClient, err = client.New(restConfig, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())

	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme:  scheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	Expect(err).NotTo(HaveOccurred())

	Expect((&DeployUnitReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr)).To(Succeed())

	go func() {
		defer GinkgoRecover()
		Expect(mgr.Start(ctx)).To(Succeed())
	}()
})

var _ = AfterSuite(func() {
	cancel()
	if testEnv != nil {
		Expect(testEnv.Stop()).To(Succeed())
	}
})

// deployUnitFixture 构造一个带 stateless 组件 + scaler/mount trait 的 DeployUnit。
func deployUnitFixture(name string, replicas int32) *oamv1.DeployUnit {
	return &oamv1.DeployUnit{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: oamv1.DeployUnitSpec{
			Components: []oamv1.DeployUnitComponent{
				{
					Name: "web",
					Type: "stateless",
					Properties: rawExtension(
						`{"image":"nginx:1.0","ports":[{"port":8080,"protocol":"TCP"}]}`),
					Traits: []oamv1.DeployUnitTrait{
						{
							Type:       "scaler",
							Properties: rawExtension(fmt.Sprintf(`{"replicas":%d}`, replicas)),
						},
						{
							Type: "mount",
							Properties: rawExtension(
								`{"mountPath":"/etc/app","files":[{"name":"app.conf","content":"key=value"}]}`),
						},
					},
				},
			},
		},
	}
}

func rawExtension(s string) runtime.RawExtension {
	return runtime.RawExtension{Raw: []byte(s)}
}

var _ = Describe("DeployUnit reconciliation", func() {
	const deployUnitName = "demo-app"

	It("should render, apply and snapshot a deployUnit", func() {
		By("creating a deployUnit")
		deployUnit := deployUnitFixture(deployUnitName, 3)
		Expect(k8sClient.Create(ctx, deployUnit)).To(Succeed())

		By("waiting for the workload to be created with trait effects")
		deploymentKey := types.NamespacedName{Namespace: "default", Name: deployUnitName + "-web"}
		Eventually(func(g Gomega) {
			var deployment appsv1.Deployment
			g.Expect(k8sClient.Get(ctx, deploymentKey, &deployment)).To(Succeed())
			g.Expect(deployment.Spec.Replicas).NotTo(BeNil())
			g.Expect(*deployment.Spec.Replicas).To(Equal(int32(3)))
			g.Expect(deployment.Spec.Template.Spec.Containers[0].Image).To(Equal("nginx:1.0"))
			// mount trait：主容器挂载点 + 卷
			g.Expect(deployment.Spec.Template.Spec.Containers[0].VolumeMounts).NotTo(BeEmpty())
			g.Expect(deployment.Spec.Template.Spec.Volumes).NotTo(BeEmpty())
		}).Should(Succeed())

		By("waiting for the service and configmap to be created")
		var service corev1.Service
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, deploymentKey, &service)).To(Succeed())
		}).Should(Succeed())
		Expect(service.Spec.Ports).To(HaveLen(1))

		var configMaps corev1.ConfigMapList
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.List(ctx, &configMaps,
				client.InNamespace("default"),
				client.MatchingLabels{render.LabelComponent: "web", render.LabelDeployUnit: deployUnitName},
			)).To(Succeed())
			g.Expect(configMaps.Items).To(HaveLen(1))
		}).Should(Succeed())
		Expect(configMaps.Items[0].Data).To(HaveKeyWithValue("app.conf", "key=value"))

		By("waiting for the deployUnit to be marked reconciled and snapshotted")
		Eventually(func(g Gomega) {
			var du oamv1.DeployUnit
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: deployUnitName}, &du)).To(Succeed())
			g.Expect(du.Annotations).To(HaveKeyWithValue(oamv1.AnnotationReconciled, "true"))
		}).Should(Succeed())

		var history oamv1.DeployUnitHistory
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: deployUnitName}, &history)).To(Succeed())
			g.Expect(history.Spec.DeployUnit.Spec.Components).To(HaveLen(1))
		}).Should(Succeed())
	})

	It("should apply updates and garbage-collect removed resources", func() {
		const name = "update-app"

		By("creating a deployUnit with two components")
		deployUnit := deployUnitFixture(name, 1)
		deployUnit.Spec.Components = append(deployUnit.Spec.Components, oamv1.DeployUnitComponent{
			Name:       "worker",
			Type:       "stateless",
			Properties: rawExtension(`{"image":"busybox:1.36"}`),
		})
		Expect(k8sClient.Create(ctx, deployUnit)).To(Succeed())

		workerKey := types.NamespacedName{Namespace: "default", Name: name + "-worker"}
		Eventually(func(g Gomega) {
			var deployment appsv1.Deployment
			g.Expect(k8sClient.Get(ctx, workerKey, &deployment)).To(Succeed())
		}).Should(Succeed())

		By("updating the deployUnit: remove worker, scale web")
		var du oamv1.DeployUnit
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: name}, &du)).To(Succeed())
			g.Expect(du.Annotations).To(HaveKeyWithValue(oamv1.AnnotationReconciled, "true"))
		}).Should(Succeed())

		du.Spec.Components = []oamv1.DeployUnitComponent{
			{
				Name:       "web",
				Type:       "stateless",
				Properties: rawExtension(`{"image":"nginx:1.0","ports":[{"port":8080,"protocol":"TCP"}]}`),
				Traits: []oamv1.DeployUnitTrait{
					{Type: "scaler", Properties: rawExtension(`{"replicas":5}`)},
				},
			},
		}
		// 重新触发同步：清除 reconciled 注解（与提交方行为一致）
		delete(du.Annotations, oamv1.AnnotationReconciled)
		Expect(k8sClient.Update(ctx, &du)).To(Succeed())

		By("worker deployment should be garbage-collected, web should be scaled")
		Eventually(func(g Gomega) {
			var deployment appsv1.Deployment
			g.Expect(k8sClient.Get(ctx, workerKey, &deployment)).To(Succeed())
		}, "10s", "250ms").ShouldNot(Succeed()) // 已删除

		Eventually(func(g Gomega) {
			var deployment appsv1.Deployment
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: name + "-web"}, &deployment)).To(Succeed())
			g.Expect(*deployment.Spec.Replicas).To(Equal(int32(5)))
		}).Should(Succeed())
	})

	It("should skip reconciliation when paused", func() {
		const name = "paused-app"

		deployUnit := deployUnitFixture(name, 2)
		deployUnit.Annotations = map[string]string{oamv1.AnnotationPause: "true"}
		Expect(k8sClient.Create(ctx, deployUnit)).To(Succeed())

		Consistently(func(g Gomega) {
			var deployment appsv1.Deployment
			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: name + "-web"}, &deployment)
			g.Expect(err).To(HaveOccurred())
		}, "2s", "250ms").Should(Succeed())
	})
})
