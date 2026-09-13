# Image URL to use all building/pushing image targets
IMG ?= controller:latest
# KIND cluster name used for e2e tests
KIND_CLUSTER ?= operator-demo-test-e2e
# Platforms to build for multi-arch images
PLATFORMS ?= linux/arm64,linux/amd64,linux/s390x,linux/ppc64le
# Ignore not-found errors during uninstall (IGNORE_NOT_FOUND=true)
IGNORE_NOT_FOUND ?= false
# GOPROXY for docker build (e.g. GOPROXY=https://goproxy.cn,direct make docker-build)
GOPROXY ?=

# Container tool & CLIs
CONTAINER_TOOL ?= docker
KUBECTL ?= kubectl
KIND ?= kind

# Project directory & local bin
PROJECT_DIR := $(CURDIR)
LOCALBIN ?= $(PROJECT_DIR)/bin

# Tool binaries
KUSTOMIZE ?= $(LOCALBIN)/kustomize
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest
GOLANGCI_LINT ?= $(LOCALBIN)/golangci-lint

# Tool versions
KUSTOMIZE_VERSION ?= v5.8.1
CONTROLLER_TOOLS_VERSION ?= v0.21.0
GOLANGCI_LINT_VERSION ?= v2.12.2

# ENVTEST_VERSION: from controller-runtime in go.mod (replace-aware)
ENVTEST_VERSION := $(shell go list -m sigs.k8s.io/controller-runtime 2>/dev/null | awk '{print $$2}')
# ENVTEST_K8S_VERSION: from k8s.io/api in go.mod, converted to 1.x (e.g. v0.36.0 -> 1.36)
ENVTEST_K8S_VERSION := $(shell echo $$(go list -m k8s.io/api 2>/dev/null | awk '{print $$2}') | sed -E 's/^v?[0-9]+\.([0-9]+).*/1.\1/')

# Year for generated file headers
YEAR := $(shell date +%Y)

# Use bash so that $$(...) command substitution and case/esac behave as expected.
SHELL := /usr/bin/env bash

# ============================================================
# 通用
# ============================================================

.PHONY: all
all: build ## 构建 manager 二进制文件

# ============================================================
# 开发
# ============================================================

.PHONY: manifests
manifests: controller-gen ## 生成 WebhookConfiguration、ClusterRole 与 CRD 清单
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases

.PHONY: generate
generate: controller-gen ## 生成 DeepCopy 方法
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt",year=$(YEAR) paths="./..."

.PHONY: fmt
fmt: ## 执行 go fmt
	go fmt ./...

.PHONY: vet
vet: generate ## 执行 go vet
	go vet ./...

.PHONY: test
test: manifests generate fmt vet setup-envtest ## 运行单元测试（基于 envtest）
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" \
	    go test $$(go list ./... | grep -v /e2e) -coverprofile cover.out

.PHONY: setup-test-e2e
setup-test-e2e: ## 若 e2e 测试用的 kind 集群不存在则创建
	@command -v $(KIND) >/dev/null 2>&1 || { echo "Kind is not installed. Please install Kind manually."; exit 1; }
	@case "$$($(KIND) get clusters)" in \
	    *"$(KIND_CLUSTER)"*) echo "Kind cluster '$(KIND_CLUSTER)' already exists. Skipping creation." ;; \
	    *) echo "Creating Kind cluster '$(KIND_CLUSTER)'..."; $(KIND) create cluster --name $(KIND_CLUSTER) ;; \
	esac

.PHONY: test-e2e
test-e2e: setup-test-e2e manifests generate fmt vet ## 在隔离的 kind 环境中运行 e2e 测试
	KIND=$(KIND) KIND_CLUSTER=$(KIND_CLUSTER) go test -tags=e2e ./test/e2e/ -v -ginkgo.v
	$(MAKE) cleanup-test-e2e

.PHONY: cleanup-test-e2e
cleanup-test-e2e: ## 销毁 e2e 测试使用的 kind 集群
	$(KIND) delete cluster --name $(KIND_CLUSTER)

.PHONY: integration-setup
integration-setup: ## 集成测试环境准备（minikube + cert-manager + metrics-server + operator）
	bash test/integration/00-setup.sh

.PHONY: integration-test
integration-test: ## 运行全部集成测试场景（前置：make integration-setup）
	bash test/integration/run-all.sh

.PHONY: integration-cleanup
integration-cleanup: ## 清理集成测试资源（--all 同时卸载 operator）
	bash test/integration/99-cleanup.sh $(ARGS)

.PHONY: lint
lint: golangci-lint ## 运行 golangci-lint
	"$(GOLANGCI_LINT)" run

.PHONY: lint-fix
lint-fix: golangci-lint ## 运行 golangci-lint 并自动修复
	"$(GOLANGCI_LINT)" run --fix

.PHONY: lint-config
lint-config: golangci-lint ## 校验 golangci-lint 配置
	"$(GOLANGCI_LINT)" config verify

# ============================================================
# 构建
# ============================================================

.PHONY: build
build: manifests generate fmt vet ## 构建 manager 二进制文件
	go build -o bin/manager cmd/main.go

.PHONY: run
run: manifests generate fmt vet ## 本地运行 controller
	go run ./cmd/main.go

.PHONY: docker-build
docker-build: ## 构建 manager docker 镜像（如 GOPROXY=https://goproxy.cn,direct make docker-build）
	$(CONTAINER_TOOL) build --build-arg GOPROXY=$(GOPROXY) -t $(IMG) .

.PHONY: docker-push
docker-push: ## 推送 manager docker 镜像
	$(CONTAINER_TOOL) push $(IMG)

.PHONY: docker-buildx
docker-buildx: ## 构建并推送多平台 manager 镜像
	sed -e '1 s/\(^FROM\)/FROM --platform=$${BUILDPLATFORM}/; t' -e ' 1,// s//FROM --platform=$${BUILDPLATFORM}/' Dockerfile > Dockerfile.cross
	-$(CONTAINER_TOOL) buildx create --name operator-demo-builder
	$(CONTAINER_TOOL) buildx use operator-demo-builder
	-$(CONTAINER_TOOL) buildx build --push --platform=$(PLATFORMS) --tag $(IMG) -f Dockerfile.cross .
	-$(CONTAINER_TOOL) buildx rm operator-demo-builder
	rm -f Dockerfile.cross

.PHONY: build-installer
build-installer: manifests generate kustomize ## 生成包含 CRD 与部署的合并 YAML
	mkdir -p dist
	cd config/manager && $(KUSTOMIZE) edit set image controller=$(IMG)
	$(KUSTOMIZE) build config/default > dist/install.yaml

# ============================================================
# 部署
# ============================================================

.PHONY: install
install: manifests kustomize ## 将 CRD 安装到集群
	@out="$$($(KUSTOMIZE) build config/crd 2>/dev/null || true)"; \
	if [ -n "$$out" ]; then echo "$$out" | $(KUBECTL) apply -f -; \
	else echo "No CRDs to install; skipping."; fi

.PHONY: uninstall
uninstall: manifests kustomize ## 卸载 CRD（先清理 MemoryPolicy CR 以避免 finalizer 死锁）
	@echo "Cleaning up MemoryPolicy CR instances (if any) to avoid finalizer deadlock..."
	$(KUBECTL) delete memorypolicy.memory.example.com -A --all --ignore-not-found=$(IGNORE_NOT_FOUND) 2>/dev/null || true
	@out="$$($(KUSTOMIZE) build config/crd 2>/dev/null || true)"; \
	if [ -n "$$out" ]; then echo "$$out" | $(KUBECTL) delete --ignore-not-found=$(IGNORE_NOT_FOUND) -f -; \
	else echo "No CRDs to delete; skipping."; fi

.PHONY: deploy
deploy: manifests kustomize ## 部署 controller 到集群
	cd config/manager && $(KUSTOMIZE) edit set image controller=$(IMG)
	$(KUSTOMIZE) build config/default | $(KUBECTL) apply -f -

.PHONY: undeploy
undeploy: kustomize ## 从集群卸载 controller
	$(KUSTOMIZE) build config/default | $(KUBECTL) delete --ignore-not-found=$(IGNORE_NOT_FOUND) -f -

.PHONY: uninstall-all
uninstall-all: ## 彻底卸载：undeploy + uninstall + 删除命名空间
	$(MAKE) undeploy
	$(MAKE) uninstall
	$(KUBECTL) delete namespace operator-demo-system --ignore-not-found=$(IGNORE_NOT_FOUND)

# ============================================================
# 依赖工具安装
# ============================================================

.PHONY: controller-gen
controller-gen: ## 按需在本地下载 controller-gen
	@test -s $(CONTROLLER_GEN) || { \
	    echo "Downloading controller-gen@$(CONTROLLER_TOOLS_VERSION)"; \
	    GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION); \
	}

.PHONY: kustomize
kustomize: ## 按需在本地下载 kustomize
	@test -s $(KUSTOMIZE) || { \
	    echo "Downloading kustomize@$(KUSTOMIZE_VERSION)"; \
	    GOBIN=$(LOCALBIN) go install sigs.k8s.io/kustomize/kustomize/v5@$(KUSTOMIZE_VERSION); \
	}

.PHONY: envtest
envtest: ## 按需在本地下载 setup-envtest
	@test -s $(ENVTEST) || { \
	    echo "Downloading setup-envtest@$(ENVTEST_VERSION)"; \
	    GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-runtime/tools/setup-envtest@$(ENVTEST_VERSION); \
	}

.PHONY: setup-envtest
setup-envtest: envtest ## 为配置的 Kubernetes 版本准备 envtest 二进制
	@echo "Setting up envtest binaries for Kubernetes version $(ENVTEST_K8S_VERSION)..."
	"$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path

.PHONY: golangci-lint
golangci-lint: ## 按需在本地下载 golangci-lint
	@test -s $(GOLANGCI_LINT) || { \
	    echo "Downloading golangci-lint@$(GOLANGCI_LINT_VERSION)"; \
	    GOBIN=$(LOCALBIN) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION); \
	}
	-@if [ -f .custom-gcl.yml ]; then \
	    echo "Building custom golangci-lint with plugins..."; \
	    "$(GOLANGCI_LINT)" custom --destination $(LOCALBIN) --name golangci-lint-custom; \
	    mv -f $(LOCALBIN)/golangci-lint-custom $(GOLANGCI_LINT); \
	fi

# ============================================================
# Help
# ============================================================

.PHONY: help
help: ## 显示本帮助
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make <target> [VAR=value]\n\nTargets:\n"} \
	/^[a-zA-Z_0-9-]+:.*##/ { printf "  %-20s %s\n", $$1, $$2 }' $(MAKEFILE_LIST)
