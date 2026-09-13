📋 作业题目：MemoryGuard Operator 开发
📌 作业背景
你需要开发一个 Kubernetes Operator，用于自动监控和管理 Pod 的内存使用情况。当 Pod 的内存使用超过指定阈值时，Operator 自动添加标签或注解，便于其他系统（如HPA、监控告警）识别和处理。

🎯 学习目标
理解 Operator 的核心工作原理（CRD + Controller + Reconciliation）
掌握使用 Kubebuilder 或 Operator SDK 搭建项目
实现基本的资源 Watch 和 Reconciliation 逻辑
学会编写和测试自定义资源（CR）

✅ 作业要求（功能清单）
阶段一：项目初始化与 CRD 定义（30分钟）
使用 Kubebuilder 或 Operator SDK 初始化项目
设计并定义一个自定义资源 MemoryPolicy，包含以下字段：yamlapiVersion: memory.example.com/v1
kind: MemoryPolicy
metadata:
  name: example-policy
spec:

# 目标命名空间（可选，不填则监控所有命名空间）

  namespace: "default"

# 内存阈值百分比（如：80表示80%）

  threshold: 80

# 触发动作：add-label（添加标签）或 add-annotation（添加注解）

  action: "add-label"

# 标签/注解的键值对

  marker:
    key: "memory-overload"
    value: "true"

生成 CRD 并验证是否成功注册到 Kubernetes 集群
阶段二：Controller 逻辑实现（60分钟）
实现 Reconciliation 逻辑：
Watch MemoryPolicy 资源的变化
根据 MemoryPolicy 的配置，列出目标命名空间下的所有 Pod
获取每个 Pod 的内存使用情况（可通过 Metrics API 或读取 cgroup）
判断内存使用是否超过阈值
如果超过且未标记，则根据 action 字段添加对应的 label 或 annotation
如果内存恢复正常且有标记，则移除标记

错误处理：
当无法获取 Pod 内存指标时，记录 Event 并重试
当 MemoryPolicy 被删除时，清理所有由该 Policy 添加的标记

阶段三：部署与测试（30分钟）
将 Operator 部署到本地或远程 Kubernetes 集群
创建一个 MemoryPolicy CR 实例
创建一个测试 Pod 并通过 stress 工具模拟内存压力
验证：
Pod 内存超过阈值后，是否被正确标记
内存恢复后，标记是否被移除
删除 MemoryPolicy 后，标记是否被清理

🧪 进阶挑战（可选，加分项）
支持多命名空间：允许 MemoryPolicy 同时监控多个命名空间
Webhook 验证：实现 Admission Webhook，确保 threshold 字段在 0-100 之间
Prometheus 指标：为 Operator 添加自定义 Metrics，暴露被标记 Pod 的数量
优雅降级：当 Metrics API 不可用时，降级为基于 Pod 的 limits 和 requests 计算使用率

📊 评分标准
表格评分项分值评分细则CRD 设计20分结构清晰、字段合理、注释完整Controller 逻辑40分Reconciliation 闭环正确、错误处理完善部署与测试20分Operator 成功运行、测试用例通过代码质量15分代码规范、注释清晰、模块化良好进阶挑战5分每完成一项加5分，上限15分

📚 参考资料
Kubebuilder 官方文档
Operator SDK 文档
Kubernetes API Conventions
Custom Resource Basics

💡 作业提交要求
提交完整的 Operator 项目代码（GitHub 链接或压缩包）
提供测试步骤和截图/录屏
提交一份简短的 README，说明：
项目结构
如何部署和测试
遇到的挑战和解决方案