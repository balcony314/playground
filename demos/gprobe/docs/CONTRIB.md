# 贡献指南

## 概述

欢迎贡献！本文档涵盖开发环境搭建、构建流程、测试规范以及提交 PR 的完整流程。

## 环境准备

### 系统要求

| 组件 | 最低版本 | 说明 |
|------|---------|------|
| 操作系统 | Linux (Ubuntu 20.04+ / Debian 11+) | eBPF 依赖 Linux 内核 |
| 内核 | 5.4+ | 需要 BTF 支持 |
| 架构 | x86_64 | 暂不支持 ARM64 |
| Go | 1.22+ | 运行时依赖 |
| clang | 12+ | 编译 eBPF C 代码 |

### 一键安装依赖

```bash
# Ubuntu/Debian
sudo apt update
sudo apt install -y clang llvm libbpf-dev \
    linux-headers-$(uname -r) gcc-multilib \
    linux-tools-common linux-tools-$(uname -r)

# 安装 Task 运行器
sh -c "$(curl --location https://taskfile.dev/install.sh)" -- -d -b ~/.local/bin
```

### 环境验证

```bash
uname -r                    # >= 5.4
ls /sys/kernel/btf/vmlinux  # BTF 支持
go version                  # >= 1.22
clang --version             # >= 12
bpftool version             # 调试工具
```

## Task 脚本参考

所有构建命令通过 [Task](https://taskfile.dev) 管理。

| 命令 | 说明 | 前置依赖 |
|------|------|---------|
| `task` | 完整构建 (generate + build + examples) | - |
| `task generate` | 编译 eBPF C 程序并生成 Go 绑定 | clang, bpf2go |
| `task build` | 构建探针二进制 `bin/gprobe` | generate |
| `task examples` | 构建示例目标程序 `bin/target` | - |
| `task test` | 运行所有 Go 测试 | - |
| `task clean` | 清理构建产物和自动生成文件 | - |
| `task fmt` | 格式化 Go 代码 | - |
| `task lint` | golangci-lint 静态检查 | golangci-lint |
| `task build-linux` | Linux amd64 交叉编译 | - |
| `task install` | 安装到 `$GOPATH/bin` | - |
| `task run-example` | 端到端运行示例 (需 root) | build, examples |
| `task help` | 显示所有可用任务 | - |

## 开发工作流

### 1. 克隆并构建

```bash
git clone https://github.com/balcony314/gprobe.git
cd gprobe
task          # 完整构建
```

### 2. 修改 eBPF 程序

编辑 `internal/bpf/uprobe.c` 后：

```bash
task generate   # 重新编译 eBPF + 重新生成 Go 绑定
go build ./...  # 验证 Go 编译通过
```

### 3. 添加新参数类型

按以下文件顺序修改：

1. **`internal/bpf/events.h`** — 添加 `ARG_TYPE_XXX` 枚举
2. **`internal/bpf/uprobe.c`** — 添加参数读取逻辑
3. **`internal/bpf/loader.go`** — Go 侧类型常量 + 解析逻辑
4. **`internal/output/decode.go`** — 解码为可读值
5. **`internal/output/json.go` / `terminal.go`** — 格式化输出

### 4. 添加新输出格式

实现 `Outputter` 接口：

```go
type Outputter interface {
    RegisterFunc(id uint32, name string)
    Output(event *bpf.FuncEvent) error
}
```

然后在 `cmd/gprobe/main.go` 的 `createOutputter()` 中注册。

### 5. 编写测试

```bash
# TDD 流程: 先写测试，确保失败，再写实现
task test

# 特定包
go test ./internal/bpf/...
go test ./internal/output/...

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码规范

### Go

- 遵循 [Uber Go 风格指南](https://github.com/xxjwxc/uber_go_guide_cn)
- 优先使用卫语句 (`if` + `return`) 减少嵌套
- 多分支条件使用 `switch`
- 函数单一职责，保持短小 (< 50 行)
- 文件控制在 800 行以内

```bash
task fmt   # 自动格式化
task lint  # 静态检查
```

### C (eBPF)

```bash
clang-format -i internal/bpf/uprobe.c
```

## 提交规范

遵循 [Conventional Commits](https://www.conventionalcommits.org/)：

```
<type>(<scope>): <简要描述>

<详细说明>

<关联 issue>
```

**类型：** `feat` | `fix` | `docs` | `refactor` | `test` | `chore`

**示例：**

```
feat(bpf): 添加 string 参数解析

- 实现 bpf_probe_read_user 读取字符串
- 支持 Go string 结构 (ptr, len)
- 更新 JSON 和终端输出格式

Closes #12
```

## PR 流程

1. 从 `main` 分支创建功能分支
2. 遵循 TDD 编写代码（测试先行）
3. 确保 `task test` 全部通过
4. 确保 `task lint` 无警告
5. 运行 `task run-example` 做端到端验证
6. 提交 PR 并填写完整描述

## 调试技巧

```bash
# 查看已加载的 BPF 程序
sudo bpftool prog list

# 查看 BPF Map 内容
sudo bpftool map dump name events

# 查看 uprobe 挂载点
sudo cat /sys/kernel/debug/tracing/uprobe_events

# 追踪 BPF 执行
sudo cat /sys/kernel/debug/tracing/trace_pipe

# 内核日志
sudo dmesg | tail -20

# delve 调试探针
sudo dlv exec ./bin/gprobe -- -p <PID> -f "main.Add"
```

## 目录结构

```
gprobe/
├── cmd/gprobe/main.go       # 主程序入口
├── internal/
│   ├── bpf/                 # eBPF 程序 (C) + Go 加载器
│   │   ├── uprobe.c         # eBPF 探针源码
│   │   ├── events.h         # 事件数据结构
│   │   ├── vmlinux.h        # 内核类型 (自动生成)
│   │   ├── generate.go      # go generate 指令
│   │   └── loader.go        # eBPF 加载/卸载/事件解析
│   └── output/              # 输出格式化
│       ├── interface.go     # Outputter 接口
│       ├── json.go          # JSON 输出
│       ├── decode.go        # 参数解码
│       └── terminal.go      # 终端彩色输出
├── examples/target/         # 示例目标程序
├── docs/                    # 项目文档
├── Taskfile.yml             # 构建任务
├── go.mod / go.sum          # Go 依赖
└── README.md
```

## 参考资源

- [cilium/ebpf 文档](https://pkg.go.dev/github.com/cilium/ebpf)
- [eBPF 官方文档](https://ebpf.io/docs/)
- [Go ABI 规范](https://go.dev/s/regabi)
- [BPF CO-RE 参考](https://nakryiko.com/posts/bpf-core-reference/)
