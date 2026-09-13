# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目概述

gprobe 是一个 Go 进程动态追踪探针，基于 eBPF uprobe 技术，用于在生产环境中捕获 Go 进程的函数入参和返回值。

## 技术栈

- **Go 1.22+** - 探针本身
- **cilium/ebpf** - eBPF Go 库
- **eBPF uprobe** - 内核层 hook 技术

## 构建命令

```bash
# 编译 eBPF 程序并构建探针 (推荐)
task

# 或者分步执行
task generate    # 编译 eBPF 程序
task build       # 构建探针
task examples    # 构建示例程序

# 运行测试
task test

# 交叉编译 (Linux)
task build-linux
```

## 项目结构

```
gprobe/
├── cmd/gprobe/          # 主程序入口
├── internal/
│   ├── bpf/             # eBPF 程序 (C 代码)
│   │   ├── uprobe.c     # uprobe hook 点
│   │   └── events.h     # 事件数据结构
│   ├── collector/       # 数据采集器
│   ├── decoder/         # 参数解码器
│   ├── parser/          # 函数签名解析
│   └── output/          # 输出格式化
└── docs/                # 文档
```

## 关键技术点

### Go 1.21+ 寄存器 ABI
- 整数参数: RAX, RBX, RCX, RDI, RSI, R8-R11
- 浮点参数: X0-X15
- 返回值: RAX (整数), X0 (浮点)

### Go 类型内存布局
- string: (Data unsafe.Pointer, Len int64)
- slice: (Data unsafe.Pointer, Len int64, Cap int64)
- map: runtime.hmap 结构

## 依赖管理

```bash
# 添加依赖
go get github.com/cilium/ebpf@latest

# 整理依赖
go mod tidy
```

## 测试

```bash
# 运行所有测试
go test ./...

# 运行特定包的测试
go test ./internal/decoder/...

# 生成测试覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 注意事项

- 需要 root 权限运行 (eBPF uprobe 要求)
- 目标程序需要有调试信息 (-gcflags="-N -l")
- 支持 Go 1.21+ 版本 (寄存器 ABI)
- eBPF 程序需要 clang 编译

## 代码风格
- 控制流规范：
  - 优先使用卫语句（if + return）提前返回，避免深层 if-else 嵌套
  - 多分支条件判断（if-else if-else）强制使用 switch 语句
- 函数设计原则：单一职责，确保一个函数只做一件事
- 参考规范：
  - [Uber Go 语言风格指南（中文）](https://github.com/xxjwxc/uber_go_guide_cn)
  - [Go 标准项目布局](https://github.com/golang-standards/project-layout)

## docs
- 将 ASCII 图替换为 mermaid 格式