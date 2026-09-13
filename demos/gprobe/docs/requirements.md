# gprobe 需求文档

## 项目概述

gprobe 是一个 Go 进程动态追踪探针，用于在生产环境中**不修改、不重启目标进程**的前提下，通过 eBPF uprobe 技术自动获取函数的入参和返回值。

## 核心需求

### 1. 探针用途
- 捕获目标 Go 进程的函数入参和返回值
- 支持深度 Go 运行时特性（goroutine、interface、栈增长等）

### 2. 技术方案
- **eBPF uprobe** - 在内核层 hook 函数入口/出口
- **Go + cilium/ebpf** - 探针本身用 Go 开发
- **主机独立进程** - 部署在物理机/虚拟机上

### 3. 使用场景
- 生产环境长期监控
- 有 root 权限部署

### 4. 目标程序要求
- Go 1.21+ 版本
- 支持寄存器 ABI（Go 1.17+）
- 需要调试信息（-gcflags="-N -l"）

## 功能需求

### 1. 函数指定方式
- 通过命令行参数手动指定要 hook 的函数名
- 支持完整函数路径，如 `main.TargetFunction`、`net/http.(*Server).ServeHTTP`

### 2. 参数解析能力

#### 已实现
- int/uint 系列 (8, 16, 32, 64)
- float32/float64
- 指针类型

#### 待实现
- string (ptr, len) - 需要读取目标进程内存
- []byte (ptr, len, cap) - 需要读取目标进程内存
- struct 字段解析（需要 DWARF 信息）
- map 解析（runtime.hmap 结构）
- channel 解析（runtime.hchan 结构）

### 3. 输出格式
- **结构化日志 (JSON)** - 函数名、参数值、返回值、耗时、时间戳
- **实时终端输出** - 彩色显示调用流

## 性能要求

- 每次 hook 延迟 1-3μs（uprobe 标准开销）
- 可接受的性能影响，不能明显拖慢业务

## 技术约束

### Go ABI 寄存器分配（Go 1.17+）
- 整数参数: RAX, RBX, RCX, RDI, RSI, R8-R11
- 浮点参数: X0-X15
- 返回值: RAX (整数), X0 (浮点)

### Go 类型内存布局
```go
// string 结构
type GoString struct {
    Data unsafe.Pointer
    Len  int64
}

// slice 结构
type GoSlice struct {
    Data unsafe.Pointer
    Len  int64
    Cap  int64
}

// map 结构 (runtime.hmap)
type GoMap struct {
    Count     int32
    Flags     uint8
    B         uint8
    Noverflow uint16
    Hash0     uint32
    Buckets   unsafe.Pointer
}
```

## 实现阶段

### Phase 1: 最小可用版本 ✅
- hook 一个简单 Go 函数
- 读取整数参数
- 输出 JSON 格式日志
- 终端实时显示

### Phase 2: 完整参数解析
- string/slice 解析（需要 bpf_probe_read_user）
- struct 字段解析
- map 内容解析
- channel 解析

### Phase 3: 生产就绪
- 性能优化（批量处理、采样模式）
- 稳定性（优雅退出、错误恢复）
- goroutine ID 获取

## 验证标准

1. ✅ 能 hook 一个简单 Go 函数
2. ✅ 能读取整数参数
3. ⏳ 能读取 string 参数（待实现）
4. ⏳ 能读取 struct 字段（待实现）
5. ⏳ 能读取 map 内容（待实现）
6. ✅ 输出 JSON 格式日志
7. ✅ 终端实时显示
