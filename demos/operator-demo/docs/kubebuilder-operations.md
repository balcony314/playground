# Kubebuilder 操作记录

记录本项目使用 kubebuilder 脚手架时的 CLI 命令、scaffold 注入点与 controller-gen markers，便于回溯生成物来源、后续扩展同类资源或排查「为何某文件被 CLI 覆盖」时参考。

## 版本与插件

| 项 | 值 | 来源 |
|---|---|---|
| CLI 版本 | 4.15.0 | [PROJECT](../PROJECT) `cliVersion` |
| Layout / 插件 | `go.kubebuilder.io/v4` | [PROJECT](../PROJECT) `layout` |
| 域名 / repo | `example.com` / `github.com/balcony314/operator-demo` | [PROJECT](../PROJECT) |
| Go | 1.26.2 | [Makefile](../Makefile) / CI |

> 说明：[CLAUDE.md](../CLAUDE.md) 中描述的脚手架版本为 4.1.1，与 [PROJECT](../PROJECT) 记录的 4.15.0 存在出入，以 PROJECT 为准（CLI 实际写入值）。

## 一、CLI 脚手架命令

本项目仅用到三条 kubebuilder 命令，均发生在初始脚手架阶段（提交 `d6b3147`）。以下为还原的真实参数：

### 1. `kubebuilder init` - 初始化项目骨架

```bash
kubebuilder init \
  --domain example.com \
  --repo github.com/balcony314/operator-demo \
  --license apache2
```

| 参数 | 值 | 说明 |
|---|---|---|
| `--domain` | `example.com` | API 域名，写入 [PROJECT](../PROJECT) `domain`，决定 API group `memory.example.com` |
| `--repo` | `github.com/balcony314/operator-demo` | Go module 路径，写入 `go.mod` |
| `--license` | `apache2` | 生成 Apache 2.0 LICENSE（提交 `246cc3d` 后统一改为 MIT，见提交 `7c6c7da`） |
| `--layout` | *(默认 go.kubebuilder.io/v4)* | 4.x 默认即 go-v4，未显式指定，见 [PROJECT:7-8](../PROJECT) |
| `--project-name` | *(默认取目录名 operator-demo)* | 见 [PROJECT:9](../PROJECT) `projectName` |

生成物：`cmd/main.go`、`Makefile`、`Dockerfile`、`PROJECT`、`config/` 基础清单、`.golangci.yml`、`AGENTS.md` 等。

### 2. `kubebuilder create api` - 创建 CRD 类型 + Controller

```bash
kubebuilder create api \
  --group memory \
  --version v1 \
  --kind MemoryPolicy \
  --resource \
  --controller
```

| 参数 | 值 | 说明 |
|---|---|---|
| `--group` | `memory` | 组名，拼接为 `memory.example.com` |
| `--version` | `v1` | API 版本（非 v1alpha1，已直接稳定到 v1） |
| `--kind` | `MemoryPolicy` | CRD kind |
| `--resource` | *(flag 存在即 true)* | 生成 CRD 类型 `api/v1/memorypolicy_types.go` |
| `--controller` | *(flag 存在即 true)* | 生成 Controller `internal/controller/memorypolicy_controller.go` |
| `--namespaced` | *(默认 true，未传)* | 见 [PROJECT:14](../PROJECT) `namespaced: true`，namespaced 资源是默认值，无需显式指定 |

生成物：`api/v1/memorypolicy_types.go`、`api/v1/groupversion_info.go`、`internal/controller/memorypolicy_controller.go`、`config/crd/bases/memory.example.com_memorypolicies.yaml`、`config/rbac/role.yaml`。

### 3. `kubebuilder create webhook` - 创建 Validating Webhook

```bash
kubebuilder create webhook \
  --group memory \
  --version v1 \
  --kind MemoryPolicy \
  --validation
```

| 参数 | 值 | 说明 |
|---|---|---|
| `--group` / `--version` / `--kind` | `memory` / `v1` / `MemoryPolicy` | 复用上一步的 API 定义 |
| `--validation` | *(flag 存在即 true)* | 生成 ValidatingWebhook，见 [PROJECT:22](../PROJECT) `validation: true` |
| `--defaulting` | *(未传)* | 未生成 MutatingWebhook（无 defaulting 逻辑） |

生成物：`internal/webhook/v1/memorypolicy_webhook.go`、`config/webhook/`、`config/certmanager/`、`config/default/manager_webhook_patch.yaml`。

**未使用的命令**：

- `kubebuilder edit --multigroup=true` - 项目为单 group，无需多组布局
- `kubebuilder edit --plugins=helm/v2-alpha` - 未生成 Helm Chart
- `--plugins=deploy-image.go.kubebuilder.io/v1-alpha` - 未使用 deploy-image 插件，Reconciliation 逻辑为手写

## 二、Scaffold 注入标记

kubebuilder 在生成文件时留下 `// +kubebuilder:scaffold:*` 注释，CLI 后续追加代码会定位到这些标记。**禁止删除**这些注释，否则再次执行 CLI 命令会丢失注入点。

| 标记 | 位置 | 用途 |
|---|---|---|
| `+kubebuilder:scaffold:imports` | [cmd/main.go:29](../cmd/main.go#L29)、[internal/controller/suite_test.go:24](../internal/controller/suite_test.go#L24)、[internal/webhook/v1/webhook_suite_test.go:30](../internal/webhook/v1/webhook_suite_test.go#L30) | 注入 import 块 |
| `+kubebuilder:scaffold:scheme` | [cmd/main.go:41](../cmd/main.go#L41)、[internal/controller/suite_test.go:53](../internal/controller/suite_test.go#L53)、[internal/webhook/v1/webhook_suite_test.go:59](../internal/webhook/v1/webhook_suite_test.go#L59) | 注入 scheme 注册 |
| `+kubebuilder:scaffold:builder` | [cmd/main.go:194](../cmd/main.go#L194) | 注入 controller / webhook 到 manager |
| `+kubebuilder:scaffold:webhook` | [internal/webhook/v1/webhook_suite_test.go:102](../internal/webhook/v1/webhook_suite_test.go#L102) | 注入 webhook 测试 |

## 三、controller-gen Markers

由 `make manifests` 调用 controller-gen 解析，生成 CRD / RBAC / webhook 配置。

### 3.1 CRD 类型（[api/v1/memorypolicy_types.go](../api/v1/memorypolicy_types.go)）

```go
// +kubebuilder:object:root=true          // 生成 MemoryPolicy / MemoryPolicyList 的 DeepCopy
// +kubebuilder:subresource:status        // 暴露 /status 子资源
// +kubebuilder:object:generate=true      // 生成 deepcopy（groupversion_info.go 中）
// +kubebuilder:validation:Minimum=0       // threshold 下界
// +kubebuilder:validation:Maximum=100    // threshold 上界
// +kubebuilder:validation:Enum=add-label;add-annotation  // action 枚举
```

效果：`config/crd/bases/memory.example.com_memorypolicies.yaml` 中生成 `minimum: 0`、`maximum: 100`、`enum: [add-label, add-annotation]` 及 status 子资源。

### 3.2 RBAC（[internal/controller/memorypolicy_controller.go:47-52](../internal/controller/memorypolicy_controller.go#L47-L52)）

```go
// +kubebuilder:rbac:groups=memory.example.com,resources=memorypolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=memory.example.com,resources=memorypolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=memory.example.com,resources=memorypolicies/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=metrics.k8s.io,resources=pods,verbs=get;list
```

效果：`config/rbac/role.yaml` 中聚合为 ClusterRole，覆盖 MemoryPolicy（含 status/finalizers）、core Pod、events、metrics.k8s.io PodMetrics。

### 3.3 Webhook（[internal/webhook/v1/memorypolicy_webhook.go:30](../internal/webhook/v1/memorypolicy_webhook.go#L30)）

```go
// +kubebuilder:webhook:path=/validate-memory-example-com-v1-memorypolicy,mutating=false,failurePolicy=fail,sideEffects=None,groups=memory.example.com,resources=memorypolicies,verbs=create;update,versions=v1,name=vmemorypolicy-v1.kb.io,admissionReviewVersions=v1
```

另在 [memorypolicy_webhook.go:35](../internal/webhook/v1/memorypolicy_webhook.go#L35) 用 `+kubebuilder:object:generate=false` 阻止 controller-gen 为 webhook 类型生成 DeepCopy（由 defaulter/validator 接口实现）。

## 四、生成 vs 手写

明确区分哪些是 CLI 生成、哪些是手写实现，避免误判：

| 文件 | 来源 |
|---|---|
| `api/v1/memorypolicy_types.go` 的结构体骨架 | CLI 生成，Spec/Status 字段与 markers 手写 |
| `api/v1/zz_generated.deepcopy.go` | controller-gen 生成（`make generate`） |
| `config/crd/bases/*.yaml`、`config/rbac/role.yaml`、`config/webhook/*.yaml` | controller-gen 生成（`make manifests`） |
| `internal/controller/memorypolicy_controller.go` 的 Reconcile 闭环 | 手写（finalizer / 标记增删 / RequeueAfter / 降级） |
| `internal/controller/metrics.go` Prometheus 指标 | 手写 |
| `internal/webhook/v1/memorypolicy_webhook.go` 校验逻辑 | 手写（骨架由 CLI 生成） |
| `cmd/main.go` 中 controller / webhook 注册 | 部分由 scaffold 注入，自定义配置手写 |

## 五、扩展同类资源的标准流程

若需新增一组 API + Controller + Webhook（参考 [AGENTS.md](../AGENTS.md)）：

```bash
# 1. 新增 API + Controller
kubebuilder create api --group memory --version v1 --kind <Kind> --resource --controller

# 2. 新增 Webhook（按需）
kubebuilder create webhook --group memory --version v1 --kind <Kind> --defaulting --validation

# 3. 重新生成清单与 deepcopy
make manifests generate

# 4. 验证
make build test
```

执行后 CLI 会自动在 `+kubebuilder:scaffold:*` 标记处注入新类型的 import、scheme 注册与 builder 注册。
