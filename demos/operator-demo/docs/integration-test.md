# 集成测试文档

MemoryGuard Operator 的集成测试方案，支持本地 minikube 与 CI 中的 kind 两种集群后端，含自动化脚本与复用说明。

## 测试环境

| 组件 | 版本 |
|---|---|
| 集群后端 | minikube（本地默认）或 kind（CI） |
| cert-manager | v1.16.0（webhook 证书签发） |
| metrics-server | v0.7.1（PodMetrics 数据源） |
| Go | 1.26.2 |
| 构建工具 | Make（Makefile） |

`00-setup.sh` 已参数化，支持 minikube 与 kind 两种后端，默认 minikube 行为不变。详见下文「kind 后端（CI）」。

## 脚本结构

所有脚本位于 `test/integration/`，幂等可重复执行：

```
test/integration/
├── lib.sh                      # 公共函数：颜色、断言、等待工具
├── 00-setup.sh                 # 环境准备（镜像源/cert-manager/metrics-server/operator 部署）
├── 10-webhook-validation.sh    # 场景1：webhook 校验
├── 20-reconcile-loop.sh        # 场景2：超阈值打标记 + 内存恢复移除
├── 30-cleanup-on-delete.sh     # 场景3：删除 Policy finalizer 清理
├── 40-add-annotation.sh        # 场景4：add-annotation 动作
├── 50-prometheus-metrics.sh    # 场景5：Prometheus 自定义指标
├── 60-graceful-degradation.sh  # 场景6：优雅降级（Metrics API 不可用）
├── run-all.sh                  # 一键运行场景 1-6
└── 99-cleanup.sh               # 清理测试资源（--all 连带卸载 operator）
```

## 快速复用

```bash
# 1. 一次性环境准备（首次或环境变更后执行）
bash test/integration/00-setup.sh

# 2. 一键运行全部测试场景
bash test/integration/run-all.sh

# 或单独运行某个场景
bash test/integration/10-webhook-validation.sh

# 3. 清理（保留 operator）
bash test/integration/99-cleanup.sh
# 3. 彻底卸载（含 operator）
bash test/integration/99-cleanup.sh --all
```

## kind 后端（CI）

`00-setup.sh` 支持以 kind 替代 minikube 跑完整 6 场景，GitHub Actions 已接入（`.github/workflows/test-integration.yml`）。场景脚本 10-60 与 99 完全集群无关（只用 `kubectl`），无需改动。

### 环境变量

| 变量 | 默认 | 取值 | 说明 |
|---|---|---|---|
| `CLUSTER` | `minikube` | `minikube` \| `kind` | 集群后端 |
| `KIND_CLUSTER` | `kind` | 集群名 | kind 集群名（需先 `kind create cluster`） |
| `MIRROR` | `auto` | `auto` \| `1` \| `0` | 镜像加速。`auto`=minikube→1/kind→0 |

### 用法

```bash
# 国外/CI 环境（直连 registry.k8s.io / docker hub，最快）
kind create cluster --name demo-int
CLUSTER=kind KIND_CLUSTER=demo-int bash test/integration/00-setup.sh
bash test/integration/run-all.sh
kind delete cluster --name demo-int

# 国内本地 kind（强制走国内镜像）
CLUSTER=kind KIND_CLUSTER=demo-int MIRROR=1 bash test/integration/00-setup.sh
```

### kind 与 minikube 的实现差异

`00-setup.sh` 按后端分支处理：

- **metrics-server**：minikube 走 `addons enable`；kind apply 官方 `components.yaml`，并**追加 `--kubelet-insecure-tls`**（kind 节点 kubelet 用自签证书，否则 `kubectl top` 取不到数据，场景 2/3/4/5/6 全挂）
- **镜像加载**：minikube `eval $(minikube docker-env)` 后构建即落位；kind 在宿主机构建后 `kind load docker-image`
- **busybox 预加载**：两边都拉。场景脚本的 stress Pod 用 `imagePullPolicy: Never`，节点必须有镜像否则 `ErrImageNeverPull`

### CI

`.github/workflows/test-integration.yml`：checkout → setup go → 装 kind → 建集群 → `00-setup.sh`（kind）→ `run-all.sh` → 失败时 dump operator 日志/events → `if: always()` 清理集群。`timeout-minutes: 30`。trigger 与 `test-e2e.yml` 对齐（push master + PR），与脚手架冒烟测试（`test-e2e.yml`）解耦、可并行。

## 测试场景

### 场景 1：validating webhook 校验

验证三层校验生效：
- `threshold=150` 被 CRD OpenAPI schema（`Maximum=100`）拒绝
- 空 `marker.key` 被 validating webhook（`vmemorypolicy-v1.kb.io`）拒绝，错误信息 `marker.key must not be empty`
- 合法 CR（threshold=80、action 枚举、marker.key 非空）创建成功

**预期**：前两个创建失败（exit 1），第三个成功。

### 场景 2：核心闭环（超阈值打标记 / 恢复移除）

```
stress Pod（memory.limit=256Mi，emptyDir tmpfs 占 220Mi = 86%）
   └─ 超过 threshold=80 -> operator 加 label memory-overload=true + 归属 annotation
      └─ 释放内存 -> metrics 回落 -> operator 移除 label + 归属 annotation
```

**预期**：
- metrics 采集周期（~60s）后 Pod 打上 `memory-overload=true` label
- operator 事件日志含 `MarkerAdded`（"memory usage 86% exceeds threshold 80%"）
- 删 tmpfs 文件后，metrics 刷新（~60s）+ operator reconcile（30s）后 label 移除
- 事件日志含 `MarkerRemoved`

### 场景 3：删除 MemoryPolicy 的 finalizer 清理

```
Pod 已被标记 -> 删除 MemoryPolicy -> finalizer 触发清理
   └─ 移除 Pod 上的 marker label + 归属 annotation
   └─ 移除 finalizer -> MemoryPolicy 彻底删除（非卡在 Terminating）
```

**预期**：
- `kubectl delete memorypolicy` 后 Policy 在 ~30s 内彻底消失（DeletionTimestamp 清除）
- Pod 的 `memory-overload` label 与 `memory.example.com/managed-by-policy` annotation 均被清理

### 场景 4：add-annotation 动作

验证 `action=add-annotation` 时标记以 annotation 形式加在 Pod 上（而非 label）。

**预期**：Pod annotations 出现 `memory-overload-anno=true`，labels 不出现同名键。

### 场景 5：Prometheus 自定义指标

验证 `/metrics` 端点暴露自定义指标 `memoryguard_marked_pods{policy,namespace}`，值=当前被该 Policy 标记的 Pod 数。

**实现**：
- 指标定义在 `internal/controller/metrics.go`（GaugeVec，标签 policy/namespace）
- main.go 通过 `NewMemoryGuardMetrics()` 注册到 controller-runtime metrics registry
- Reconcile 遍历 Pod 时统计被标记数，循环后 `WithLabelValues().Set()`

**验证方式**：port-forward metrics 服务（8443 HTTPS），用 operator SA token 认证（需 `metrics-reader-rolebinding` 授权 get /metrics），curl `/metrics`。

**预期**：Pod 被标记后，指标行 `memoryguard_marked_pods{namespace="default",policy="mem-policy-default"} 1` 出现。

**注意**：
- metrics 端点开启 secureMetrics（HTTPS + authn/authz 过滤），需 Bearer token
- `metrics-reader-rolebinding`（`config/rbac/metrics_reader_role_binding.yaml`）已纳入 `make deploy`，绑定 metrics-reader ClusterRole 到 operator SA，便于本地验证与 Prometheus 抓取
- 生产环境若用独立 SA 抓取，应改 binding 的 subject

### 场景 6：优雅降级（Metrics API 不可用）

验证 Metrics API 不可用时，降级为 `request/limit` 估算使用率，仍能标记 Pod，且 status 反映降级状态。

**降级口径**：`rate = memory.request / memory.limit × 100`（取自 Pod spec 配置值，不依赖运行时数据）。

**实现**（`reconcilePods`）：
- PodMetrics List 失败 -> `degraded=true`，记 `MetricsDegraded` Event，不中断调谐
- 降级时分子用 `sum(各容器 memory.request)` 替代实际 usage
- status condition `Available=False / reason=MetricsDegraded`
- Metrics API 恢复后自动回到正常路径

**测试方式**：`kubectl scale deployment -n kube-system metrics-server --replicas=0` 模拟 Metrics API 不可用 -> 创建 `request=210Mi/limit=256Mi`（82% > 80%）的 Pod -> 验证降级标记 + status Degraded -> 测试后自动恢复 metrics-server。

**预期**：Pod 被标记、status `Available=False/MetricsDegraded`、日志含 `MetricsDegraded`。

**注意**：降级是 request/limit **估算**，request 是调度保证量而非实际占用，语义偏保守（仅当配置的 request 占比超阈值时触发）。

## 关键设计

- **阈值口径**：`rate = Pod 内存用量 / Pod memory.limit × 100`，无 limit 的 Pod 跳过
- **归属 annotation**：`memory.example.com/managed-by-policy=<policy名>`，支持多 Policy 共存，finalizer 精确清理不误删
- **RequeueAfter 30s**：metrics 非 K8s 资源不触发 watch，必须定期轮询
- **幂等**：所有脚本开头清理同名残留资源，可重复执行

## 踩坑记录

### 1. 镜像源（国内无法直连 docker.io / gcr.io）

`00-setup.sh` 自动处理（按 `MIRROR` 决定是否走国内镜像，minikube 默认开、kind 默认关）：
- `golang:1.26`、`gcr.io/distroless/static:nonroot`、`busybox:latest` 经 daocloud 镜像拉取并 retag 为原名
- manager 镜像构建传 `GOPROXY=https://goproxy.cn,direct`（Dockerfile 已加 `ARG GOPROXY`）
- `make docker-build` 支持 `GOPROXY=... make docker-build`
- kind 后端额外 `kind load docker-image` 把 manager + busybox 灌入节点（见「kind 后端」节）

### 2. metrics-server 镜像

minikube addon 用的带 sha 镜像（`metrics-server:v0.7.1@sha256:db380008...`）在阿里云 manifest 缺失。`00-setup.sh` 自动 patch deployment 为无 sha 版本（`registry.cn-hangzhou.aliyuncs.com/google_containers/metrics-server:v0.7.1`，digest cf40d06e）。

### 3. stress Pod 内存占用方式

关键：**minikube 容器 `/dev/shm` 默认 64MB**，dd 写入受限，且进程 RSS（awk 字符串拼接）在 busybox 极慢。

**可行方案**：挂载 `emptyDir.medium: Memory` 的 tmpfs（独立配额，不受 /dev/shm 64MB 限制），dd 写 220Mi。metrics-server 会将 tmpfs 计入 container workingset，`kubectl top pod` 正确反映 221Mi。

```yaml
volumes:
- name: memtmpfs
  emptyDir:
    medium: Memory
    sizeLimit: 240Mi
```

### 4. metrics 采集延迟

metrics-server 采集周期约 60s，标记出现/移除需等待 1-3 分钟（采集 + reconcile）。脚本 `wait_until` 默认超时 180s。

## 断言库（lib.sh）

| 函数 | 用途 |
|---|---|
| `assert_ok "描述" cmd...` | 命令应成功 |
| `assert_fail "描述" cmd...` | 命令应失败 |
| `assert_contains "描述" "cmd" "子串"` | 命令输出含子串 |
| `assert_not_contains "描述" "cmd" "子串"` | 命令输出不含子串 |
| `wait_until "描述" "条件" "超时秒"` | 轮询等待条件成立 |

末尾 `summary` 打印通过/失败计数并返回退出码。
