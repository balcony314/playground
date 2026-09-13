# 开发指南

## 环境准备

### 系统要求

- **操作系统**: Linux (Ubuntu 20.04+ / Debian 11+)
- **内核版本**: 5.4+ (需要 BTF 支持)
- **架构**: x86_64

### 依赖安装

```bash
# Ubuntu/Debian
sudo apt update
sudo apt install -y \
    clang \
    llvm \
    libbpf-dev \
    linux-headers-$(uname -r) \
    gcc-multilib \
    make

# 安装 Go 1.22+
wget https://go.dev/dl/go1.22.0.linux-amd64.tar.gz
sudo tar -C /usr/local -xzf go1.22.0.linux-amd64.tar.gz
export PATH=$PATH:/usr/local/go/bin

# 安装 Task (任务运行器)
sh -c "$(curl --location https://taskfile.dev/install.sh)" -- -d -b ~/.local/bin

# 安装 bpftool (用于 BTF 和调试)
sudo apt install -y linux-tools-common linux-tools-$(uname -r)
```

### 验证环境

```bash
# 检查内核版本
uname -r  # 应该 >= 5.4

# 检查 BTF 支持
ls -la /sys/kernel/btf/vmlinux

# 检查 Go 版本
go version  # 应该 >= 1.22

# 检查 clang 版本
clang --version  # 推荐 >= 12

# 检查 bpftool
bpftool version
```

## 项目构建

### 完整构建

```bash
# 编译 eBPF 程序 + 构建探针 + 构建示例
task

# 或者分步执行
task generate    # 编译 eBPF 程序
task build       # 构建探针
task examples    # 构建示例程序
```

### 构建流程详解

#### 1. 生成 vmlinux.h

```bash
# 从内核 BTF 信息生成 C 头文件
bpftool btf dump file /sys/kernel/btf/vmlinux format c > internal/bpf/vmlinux.h
```

#### 2. 编译 eBPF 程序

```bash
# 使用 clang 编译 eBPF 字节码
clang -target bpf \
      -D__TARGET_ARCH_x86 \
      -I internal/bpf \
      -g -O2 \
      -c internal/bpf/uprobe.c \
      -o internal/bpf/uprobe.o

# 使用 bpf2go 生成 Go 绑定
go run github.com/cilium/ebpf/cmd/bpf2go \
    -type func_event \
    -type arg_data \
    -type arg_type \
    bpf internal/bpf/uprobe.o
```

#### 3. 构建 Go 程序

```bash
go build -o bin/gprobe ./cmd/gprobe
```

### 清理构建产物

```bash
task clean
```

## 项目结构

```
gprobe/
├── cmd/
│   └── gprobe/
│       └── main.go           # 主程序入口
├── internal/
│   ├── bpf/
│   │   ├── uprobe.c          # eBPF 程序 (C)
│   │   ├── events.h          # 事件数据结构
│   │   ├── vmlinux.h         # 内核类型定义
│   │   ├── generate.go       # go generate 指令
│   │   ├── loader.go         # eBPF 加载器 (Go)
│   │   └── bpf_x86_bpfel.go  # 自动生成的绑定
│   ├── output/
│   │   ├── interface.go      # 输出接口
│   │   ├── json.go           # JSON 输出
│   │   └── terminal.go       # 终端输出
│   ├── collector/            # 数据采集器（预留）
│   ├── decoder/              # 参数解码器（预留）
│   └── parser/               # 函数签名解析（预留）
├── examples/
│   └── target/
│       └── main.go           # 示例目标程序
├── docs/                     # 文档
├── Taskfile.yml              # 任务配置
├── go.mod                    # Go 模块定义
└── go.sum                    # 依赖校验
```

## 开发流程

### 1. 修改 eBPF 程序

```bash
# 编辑 C 源码
vim internal/bpf/uprobe.c

# 重新生成 Go 绑定
task generate

# 验证编译通过
go build ./...
```

### 2. 添加新参数类型

#### 步骤 1: 更新 C 头文件

```c
// internal/bpf/events.h
enum arg_type {
    // ... 现有类型
    ARG_TYPE_MYTYPE = 10,  // 新类型
};
```

#### 步骤 2: 更新 eBPF 程序

```c
// internal/bpf/uprobe.c
// 在参数读取逻辑中添加新类型处理
```

#### 步骤 3: 更新 Go 代码

```go
// internal/bpf/loader.go
const (
    // ... 现有类型
    ArgTypeMyType ArgType = 10
)
```

#### 步骤 4: 更新输出格式化

```go
// internal/output/json.go 和 terminal.go
func decodeArg(arg bpf.ArgData) any {
    switch arg.Type {
    // ... 现有类型
    case bpf.ArgTypeMyType:
        // 解码逻辑
    }
}
```

### 3. 添加新的输出格式

```go
// internal/output/csv.go
package output

import (
    "encoding/csv"
    "io"
    "github.com/balcony314/gprobe/internal/bpf"
)

type CSVOutputter struct {
    writer    *csv.Writer
    funcNames map[uint32]string
}

func NewCSVOutputter(writer io.Writer) *CSVOutputter {
    return &CSVOutputter{
        writer:    csv.NewWriter(writer),
        funcNames: make(map[uint32]string),
    }
}

func (o *CSVOutputter) RegisterFunc(id uint32, name string) {
    o.funcNames[id] = name
}

func (o *CSVOutputter) Output(event *bpf.FuncEvent) error {
    // 实现 CSV 输出逻辑
    return nil
}
```

然后在 `main.go` 中注册：

```go
case "csv":
    out = output.NewCSVOutputter(os.Stdout)
```

### 4. 修改命令行参数

```go
// cmd/gprobe/main.go
var (
    // 添加新参数
    verbose bool
)

func init() {
    // 注册新参数
    rootCmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "详细输出")
}
```

## 测试

### 单元测试

```bash
# 运行所有测试
task test

# 或者直接使用 go test
go test ./...

# 运行特定包的测试
go test ./internal/bpf/...
go test ./internal/output/...

# 生成测试覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

### 集成测试

```bash
# 终端 1: 启动目标程序
go run examples/target/main.go

# 终端 2: 获取 PID 并运行探针
PID=$(pgrep -f "examples/target")
sudo ./bin/gprobe -p $PID -f "main.Add" -f "main.Greet"
```

### 调试测试

```bash
# 使用 delve 调试
go install github.com/go-delve/delve/cmd/dlv@latest
sudo dlv exec ./bin/gprobe -- -p <PID> -f "main.Add"

# 查看 eBPF 程序状态
sudo bpftool prog list
sudo bpftool map list
```

## 常见问题

### 1. 编译错误: "vmlinux.h not found"

```bash
# 重新生成 vmlinux.h
bpftool btf dump file /sys/kernel/btf/vmlinux format c > internal/bpf/vmlinux.h
```

### 2. 运行时错误: "permission denied"

```bash
# eBPF 需要 root 权限
sudo ./bin/gprobe ...
```

### 3. 运行时错误: "no such process"

```bash
# 检查目标进程是否存在
ps aux | grep target

# 确保进程有调试信息
file /proc/<PID>/exe
```

### 4. uprobe 挂载失败

```bash
# 检查目标函数是否存在
nm <binary> | grep <function_name>

# 查看内核日志
dmesg | tail
```

### 5. 性能问题

```bash
# 检查 BPF 程序统计
sudo bpftool prog show id <prog_id>

# 查看丢失的事件
sudo bpftool map dump name events | grep lost
```

## 代码规范

### Go 代码风格

```bash
# 格式化
go fmt ./...

# 静态检查
golangci-lint run ./...
```

### C 代码风格

```bash
# 使用 clang-format
clang-format -i internal/bpf/uprobe.c
```

### 提交规范

```
<type>(<scope>): <subject>

<body>

<footer>
```

类型：
- `feat`: 新功能
- `fix`: 修复
- `docs`: 文档
- `refactor`: 重构
- `test`: 测试
- `chore`: 构建/工具

示例：
```
feat(bpf): 添加 string 参数解析

- 实现 bpf_probe_read_user 读取字符串
- 支持 Go string 结构 (ptr, len)
- 更新 JSON 和终端输出格式

Closes #12
```

## 扩展开发

### 添加新架构支持

1. 生成目标架构的 `vmlinux.h`
2. 修改 `generate.go` 添加构建标签
3. 实现架构特定的寄存器读取
4. 更新 `bpf2go` 命令参数

### 添加新探针类型

1. 在 `uprobe.c` 中添加新的 SEC 函数
2. 在 `loader.go` 中添加挂载逻辑
3. 在 `main.go` 中添加命令行参数

### 集成到监控系统

```go
// 实现 Outputter 接口对接 Prometheus
type PrometheusOutputter struct {
    // ...
}

func (o *PrometheusOutputter) Output(event *bpf.FuncEvent) error {
    // 更新 Prometheus metrics
    return nil
}
```

## 参考资源

- [cilium/ebpf 文档](https://pkg.go.dev/github.com/cilium/ebpf)
- [eBPF 官方文档](https://ebpf.io/docs/)
- [BPF CO-RE 参考](https://nakryiko.com/posts/bpf-core-reference/)
- [Go ABI 规范](https://go.dev/s/regabi)
