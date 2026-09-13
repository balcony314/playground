#!/usr/bin/env bash
# 集成测试环境准备：metrics-server + cert-manager + operator 部署
# 支持两种集群后端：minikube（默认）与 kind
# 用法:
#   minikube（默认）: bash test/integration/00-setup.sh
#   kind:            CLUSTER=kind KIND_CLUSTER=<name> bash test/integration/00-setup.sh
#                    （需先 kind create cluster --name <name>）
#   kind + 国内镜像: CLUSTER=kind MIRROR=1 bash test/integration/00-setup.sh
# 幂等：可重复执行，已存在的资源会跳过或更新
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
cd "$REPO_ROOT"

# ===== 配置 =====
CLUSTER="${CLUSTER:-minikube}"        # minikube | kind
KIND_CLUSTER="${KIND_CLUSTER:-kind}"  # kind 集群名
MIRROR="${MIRROR:-auto}"              # auto=minikube->1/kind->0 | 1 | 0
MS_VER="v0.7.1"
MS_IMG_ALIYUN="registry.cn-hangzhou.aliyuncs.com/google_containers/metrics-server:${MS_VER}"
MS_IMG_OFFICIAL="registry.k8s.io/metrics-server/metrics-server:${MS_VER}"

# 颜色输出
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
log()  { echo -e "${GREEN}[setup]${NC} $*"; }
warn() { echo -e "${YELLOW}[setup]${NC} $*"; }
die()  { echo -e "${RED}[setup]${NC} $*" >&2; exit 1; }

# 解析 MIRROR=auto：minikube 默认走国内镜像（本地场景），kind 默认直连（CI 在国外）
if [ "$MIRROR" = "auto" ]; then
  case "$CLUSTER" in
    minikube) MIRROR=1 ;;
    kind)     MIRROR=0 ;;
    *)        die "未知 CLUSTER=$CLUSTER（应为 minikube 或 kind）" ;;
  esac
fi
log "集群后端: $CLUSTER$([ "$CLUSTER" = "kind" ] && echo "（$KIND_CLUSTER）" )，镜像加速: $([ "$MIRROR" = "1" ] && echo "开启（国内镜像）" || echo "关闭（直连）")"

# 按 MIRROR 决定镜像源拉取并 retag（mirror 为空时直连）
pull_image() {
  local official="$1" mirror="${2:-}"
  if [ "$MIRROR" = "1" ] && [ -n "$mirror" ]; then
    log "拉取 $official（经 $mirror）..."
    docker pull "$mirror" >/dev/null
    docker tag "$mirror" "$official"
  else
    log "拉取 $official（直连）..."
    docker pull "$official" >/dev/null
  fi
}

# ===== 0. 前置检查 =====
command -v kubectl >/dev/null || die "kubectl 未安装"
command -v make >/dev/null || die "make 未安装"

case "$CLUSTER" in
  minikube)
    command -v minikube >/dev/null || die "minikube 未安装"
    minikube status >/dev/null 2>&1 || die "minikube 未运行，先执行: minikube start"
    kubectl config current-context | grep -q minikube \
      || warn "当前 context 非 minikube（$(kubectl config current-context)）"
    log "minikube 运行中"
    ;;
  kind)
    command -v kind >/dev/null || die "kind 未安装"
    kind get clusters 2>/dev/null | grep -qw "$KIND_CLUSTER" \
      || die "kind 集群 '$KIND_CLUSTER' 不存在，先执行: kind create cluster --name $KIND_CLUSTER"
    kubectl config current-context | grep -q "kind-$KIND_CLUSTER" \
      || warn "当前 context 非 kind-$KIND_CLUSTER（$(kubectl config current-context)），建议: kubectl config use-context kind-$KIND_CLUSTER"
    log "kind 集群 '$KIND_CLUSTER' 运行中"
    ;;
  *)
    die "未知 CLUSTER=$CLUSTER（应为 minikube 或 kind）"
    ;;
esac

# ===== 1. metrics-server =====
if kubectl get deployment -n kube-system metrics-server >/dev/null 2>&1; then
  log "metrics-server 已存在，跳过安装"
else
  case "$CLUSTER" in
    minikube)
      log "启用 metrics-server addon ..."
      minikube addons enable metrics-server
      ;;
    kind)
      log "安装 metrics-server $MS_VER（官方 components.yaml）..."
      kubectl apply -f "https://github.com/kubernetes-sigs/metrics-server/releases/download/${MS_VER}/components.yaml"
      ;;
  esac
fi

# 镜像 patch：minikube addon 带国内不可达的 sha；kind 默认 registry.k8s.io 在 MIRROR=1 时换阿里云
MS_TARGET=$([ "$MIRROR" = "1" ] && echo "$MS_IMG_ALIYUN" || echo "$MS_IMG_OFFICIAL")
CURRENT_MS_IMG=$(kubectl get deployment -n kube-system metrics-server \
  -o jsonpath='{.spec.template.spec.containers[0].image}' 2>/dev/null || echo "")
if [ "$CURRENT_MS_IMG" != "$MS_TARGET" ]; then
  warn "patch metrics-server 镜像 -> $MS_TARGET"
  kubectl patch deployment -n kube-system metrics-server --type='json' \
    -p="[{\"op\":\"replace\",\"path\":\"/spec/template/spec/containers/0/image\",\"value\":\"$MS_TARGET\"}]" >/dev/null
fi

# kind 必须 --kubelet-insecure-tls（节点 kubelet 自签证书，否则 metrics 取不到）
if [ "$CLUSTER" = "kind" ]; then
  MS_ARGS=$(kubectl get deployment -n kube-system metrics-server \
    -o jsonpath='{.spec.template.spec.containers[0].args}' 2>/dev/null || echo "")
  if ! echo "$MS_ARGS" | grep -q -- "--kubelet-insecure-tls"; then
    warn "kind: 追加 metrics-server --kubelet-insecure-tls"
    kubectl patch deployment -n kube-system metrics-server --type='json' \
      -p='[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--kubelet-insecure-tls"}]' >/dev/null
  fi
fi

# 重启 metrics-server pod 使 patch 生效（label k8s-app=metrics-server 两后端通用）
kubectl delete pod -n kube-system -l k8s-app=metrics-server --grace-period=0 --force >/dev/null 2>&1 || true
log "等待 metrics-server ready ..."
kubectl wait deployment -n kube-system metrics-server --for=condition=available --timeout=240s

# ===== 2. cert-manager =====
CERT_MGR_VER="v1.16.0"
if kubectl get namespace cert-manager >/dev/null 2>&1; then
  log "cert-manager namespace 已存在，跳过安装（如需重装: kubectl delete namespace cert-manager）"
else
  log "安装 cert-manager $CERT_MGR_VER ..."
  kubectl apply -f "https://github.com/cert-manager/cert-manager/releases/download/${CERT_MGR_VER}/cert-manager.yaml"
fi
log "等待 cert-manager ready ..."
kubectl wait deployment -n cert-manager --for=condition=available --all --timeout=240s

# ===== 3. 构建并加载 manager 镜像（+ busybox，场景脚本用 imagePullPolicy: Never）=====
case "$CLUSTER" in
  minikube)
    log "构建 manager 镜像到 minikube daemon ..."
    eval "$(minikube docker-env)"
    pull_image "golang:1.26" "docker.m.daocloud.io/library/golang:1.26"
    pull_image "gcr.io/distroless/static:nonroot" "docker.m.daocloud.io/library/alpine:3.20"
    pull_image "busybox:latest" "docker.m.daocloud.io/library/busybox:latest"
    GOPROXY="https://goproxy.cn,direct" make docker-build
    log "manager 镜像构建完成: $(docker images controller:latest --format '{{.Repository}}:{{.Tag}} {{.Size}}')"
    ;;
  kind)
    log "构建 manager 镜像（宿主机 docker）并加载到 kind 集群 ..."
    pull_image "golang:1.26" "docker.m.daocloud.io/library/golang:1.26"
    pull_image "gcr.io/distroless/static:nonroot" "docker.m.daocloud.io/library/alpine:3.20"
    pull_image "busybox:latest" "docker.m.daocloud.io/library/busybox:latest"
    if [ "$MIRROR" = "1" ]; then
      GOPROXY="https://goproxy.cn,direct" make docker-build
    else
      make docker-build
    fi
    # 场景脚本用 imagePullPolicy: Never，节点必须已存在 busybox，故一并 load
    log "加载 manager + busybox 镜像到 kind 集群 ..."
    kind load docker-image controller:latest --name "$KIND_CLUSTER"
    kind load docker-image busybox:latest --name "$KIND_CLUSTER"
    log "镜像加载完成"
    ;;
esac

# ===== 4. 部署 operator =====
log "部署 operator（make deploy）..."
make deploy
log "等待 operator ready ..."
kubectl wait deployment -n operator-demo-system operator-demo-controller-manager \
  --for=condition=available --timeout=120s

# ===== 5. 验证 webhook 证书已注入 =====
log "验证 webhook caBundle 已注入 ..."
if [ -n "$(kubectl get validatingwebhookconfiguration operator-demo-validating-webhook-configuration \
  -o jsonpath='{.webhooks[0].clientConfig.caBundle}' 2>/dev/null)" ]; then
  log "webhook caBundle 已注入（cert-manager 签发完成）"
else
  die "webhook caBundle 为空，cert-manager 可能未就绪"
fi

# ===== 6. 等待 metrics API 可用 =====
log "等待 metrics API 可用 ..."
for i in $(seq 1 30); do
  if kubectl top nodes >/dev/null 2>&1; then
    log "metrics API 就绪"
    kubectl top nodes
    break
  fi
  sleep 5
done

echo
log "环境准备完成。可运行全部测试场景: bash test/integration/run-all.sh"
