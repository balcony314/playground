# eBPF 内部实现详解

## 概述

本文档详细描述 gprobe 中 eBPF 程序的内部实现，包括数据结构、探针逻辑和 BPF Map 使用。

## 文件结构

```
internal/bpf/
├── uprobe.c        # eBPF 程序源码
├── events.h        # 事件数据结构定义
├── vmlinux.h       # 内核类型定义（自动生成）
├── generate.go     # go generate 指令
└── bpf_x86_bpfel.go # 编译后的 eBPF 对象（自动生成）
```

## 数据结构 (`events.h`)

### 参数类型枚举

```c
enum arg_type {
    ARG_TYPE_INT = 0,      // 整数类型
    ARG_TYPE_UINT,         // 无符号整数
    ARG_TYPE_FLOAT64,      // 浮点数
    ARG_TYPE_STRING,       // 字符串（待实现）
    ARG_TYPE_SLICE,        // 切片（待实现）
    ARG_TYPE_STRUCT,       // 结构体（待实现）
    ARG_TYPE_MAP,          // Map（待实现）
    ARG_TYPE_CHANNEL,      // Channel（待实现）
    ARG_TYPE_POINTER,      // 指针
};
```

### 参数数据结构

```c
#define MAX_ARGS 6          // 最大参数数量
#define MAX_ARG_SIZE 64     // 单个参数最大字节数

struct arg_data {
    __u64 type;             // 参数类型 (enum arg_type)
    __u64 size;             // 实际数据大小
    __u8  data[MAX_ARG_SIZE]; // 原始数据
};
```

**内存布局：**
```
┌────────────────────────────────────┐
│ type (8 bytes)                     │
├────────────────────────────────────┤
│ size (8 bytes)                     │
├────────────────────────────────────┤
│ data (64 bytes)                    │
│ ...                                │
└────────────────────────────────────┘
总计: 80 bytes per arg_data
```

### 函数事件结构

```c
struct func_event {
    __u64 timestamp;        // 时间戳 (纳秒)
    __u32 pid;              // 进程 ID
    __u32 tid;              // 线程 ID
    __u64 goroutine_id;     // Goroutine ID（暂未实现）
    __u32 func_id;          // 函数 ID（用户态映射）
    __u32 is_return;        // 是否为返回事件
    __u64 duration_ns;      // 调用耗时（仅返回事件）
    __u64 return_value;     // 返回值
    struct arg_data args[MAX_ARGS]; // 参数数组
};
```

**内存布局：**
```
┌────────────────────────────────────┐
│ timestamp (8 bytes)                │
├────────────────────────────────────┤
│ pid (4 bytes) | tid (4 bytes)      │
├────────────────────────────────────┤
│ goroutine_id (8 bytes)             │
├────────────────────────────────────┤
│ func_id (4 bytes) | is_return (4B) │
├────────────────────────────────────┤
│ duration_ns (8 bytes)              │
├────────────────────────────────────┤
│ return_value (8 bytes)             │
├────────────────────────────────────┤
│ args[0] (80 bytes)                 │
├────────────────────────────────────┤
│ args[1] (80 bytes)                 │
├────────────────────────────────────┤
│ ...                                │
├────────────────────────────────────┤
│ args[5] (80 bytes)                 │
└────────────────────────────────────┘
总计: ~504 bytes per func_event
```

## BPF Maps

### 1. events (PERF_EVENT_ARRAY)

```c
struct {
    __uint(type, BPF_MAP_TYPE_PERF_EVENT_ARRAY);
    __uint(key_size, sizeof(__u32));
    __uint(value_size, sizeof(__u32));
} events SEC(".maps");
```

**用途：** 高效的内核到用户态数据传输

**特点：**
- 每个 CPU 核心一个环形缓冲区
- 支持批量读取
- 自动处理 CPU 迁移

### 2. event_heap (PERCPU_ARRAY)

```c
struct {
    __uint(type, BPF_MAP_TYPE_PERCPU_ARRAY);
    __uint(max_entries, 1);
    __type(key, __u32);
    __type(value, struct func_event);
} event_heap SEC(".maps");
```

**用途：** 临时事件数据缓冲

**为什么用 PERCPU_ARRAY：**
- 避免锁竞争
- 每个 CPU 独立的缓冲区
- 固定内存占用

### 3. start_times (HASH)

```c
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, 10240);
    __type(key, __u64);   // tid
    __type(value, __u64); // 入口时间戳
} start_times SEC(".maps");
```

**用途：** 存储函数入口时间戳，用于计算调用耗时

**键值设计：**
- Key: 线程 ID (tid)
- Value: 入口时间戳 (bpf_ktime_get_ns)

## eBPF 程序实现

### 函数入口探针

```c
SEC("uprobe/func_entry")
int uprobe_func_entry(struct pt_regs *ctx) {
    // 1. 获取进程/线程 ID
    __u64 pid_tgid = bpf_get_current_pid_tgid();
    __u32 pid = pid_tgid >> 32;
    __u32 tid = (__u32)pid_tgid;

    // 2. 过滤目标进程
    if (target_pid != 0 && pid != target_pid) {
        return 0;
    }

    // 3. 记录入口时间
    __u64 ts = bpf_ktime_get_ns();
    bpf_map_update_elem(&start_times, &tid, &ts, BPF_ANY);

    // 4. 获取事件缓冲区
    __u32 key = 0;
    struct func_event *event = bpf_map_lookup_elem(&event_heap, &key);
    if (!event) return 0;

    // 5. 填充事件数据
    __builtin_memset(event, 0, sizeof(*event));
    event->timestamp = ts;
    event->pid = pid;
    event->tid = tid;
    event->func_id = target_func_id;
    event->is_return = 0;

    // 6. 读取寄存器参数
    #pragma unroll
    for (int i = 0; i < MAX_ARGS; i++) {
        event->args[i].type = ARG_TYPE_UINT;
        event->args[i].size = sizeof(__u64);
        __u64 val = get_arg_reg(ctx, i);
        __builtin_memcpy(event->args[i].data, &val, sizeof(__u64));
    }

    // 7. 发送事件到用户态
    bpf_perf_event_output(ctx, &events, BPF_F_CURRENT_CPU, event, sizeof(*event));

    return 0;
}
```

### 函数返回探针

```c
SEC("uprobe/func_return")
int uprobe_func_return(struct pt_regs *ctx) {
    // 1-2. 同入口探针

    // 3. 计算耗时
    __u64 *start_ts = bpf_map_lookup_elem(&start_times, &tid);
    __u64 duration = 0;
    if (start_ts) {
        duration = bpf_ktime_get_ns() - *start_ts;
        bpf_map_delete_elem(&start_times, &tid);
    }

    // 4-5. 填充返回事件
    event->is_return = 1;
    event->duration_ns = duration;
    event->return_value = PT_REGS_RC(ctx);  // 读取 RAX

    // 6-7. 发送事件

    return 0;
}
```

### 寄存器参数读取

```c
static __always_inline __u64 get_arg_reg(struct pt_regs *ctx, int index) {
    switch (index) {
    case 0: return PT_REGS_PARM1(ctx);  // RAX
    case 1: return PT_REGS_PARM2(ctx);  // RBX
    case 2: return PT_REGS_PARM3(ctx);  // RCX
    case 3: return PT_REGS_PARM4(ctx);  // RDI
    case 4: return PT_REGS_PARM5(ctx);  // RSI
    case 5: return ctx->r8;             // R8
    default: return 0;
    }
}
```

## Go ABI 寄存器映射

### 参数寄存器 (x86_64)

| 参数位置 | 寄存器 | 说明 |
|----------|--------|------|
| arg 0 | RAX | 第一个参数 |
| arg 1 | RBX | 第二个参数 |
| arg 2 | RCX | 第三个参数 |
| arg 3 | RDI | 第四个参数 |
| arg 4 | RSI | 第五个参数 |
| arg 5 | R8 | 第六个参数 |
| arg 6-10 | 栈传递 | 更多参数 |

### 返回值寄存器

| 类型 | 寄存器 |
|------|--------|
| 整数 | RAX |
| 浮点 | X0 |

## 用户态 Go 结构映射

### ArgData

```go
type ArgData struct {
    Type ArgType
    Size uint64
    Data []byte
}
```

### FuncEvent

```go
type FuncEvent struct {
    Timestamp   uint64
    PID         uint32
    TID         uint32
    GoroutineID uint64
    FuncID      uint32
    IsReturn    bool
    DurationNS  uint64
    ReturnValue uint64
    Args        [6]ArgData
}
```

## 编译流程

```bash
# 1. 生成 vmlinux.h（内核类型定义）
bpftool btf dump file /sys/kernel/btf/vmlinux format c > internal/bpf/vmlinux.h

# 2. 编译 eBPF 程序
go generate ./internal/bpf/...

# 编译过程：
#   clang -target bpf -D__TARGET_ARCH_x86 \
#         -I internal/bpf -g -O2 -c internal/bpf/uprobe.c \
#         -o internal/bpf/uprobe.o
#
#   bpf2go -type func_event -type arg_data \
#          -type arg_type bpf internal/bpf/uprobe.o
```

## 调试技巧

### 1. 查看 BPF 程序加载状态

```bash
sudo bpftool prog list
```

### 2. 查看 BPF Map 内容

```bash
sudo bpftool map dump name events
sudo bpftool map dump name start_times
```

### 3. 查看 uprobe 挂载点

```bash
sudo cat /sys/kernel/debug/tracing/uprobe_events
```

### 4. 追踪 BPF 程序执行

```bash
sudo cat /sys/kernel/debug/tracing/trace_pipe
```

## 已知限制

1. **参数数量**: 最多 6 个（受寄存器数量限制）
2. **参数大小**: 单个参数最大 64 字节
3. **并发**: event_heap 使用 PERCPU，避免锁但增加内存
4. **时间精度**: bpf_ktime_get_ns() 精度为纳秒，但受系统时钟影响

## 扩展点

### 添加新参数类型

1. 在 `events.h` 中添加类型枚举
2. 在 `uprobe.c` 中添加读取逻辑
3. 在 `loader.go` 中添加解析逻辑
4. 在 `output/` 中添加格式化逻辑

### 增加参数数量

1. 修改 `events.h` 中的 `MAX_ARGS`
2. 更新 `get_arg_reg` 支持更多寄存器
3. 更新 Go 结构体中的 `Args` 数组大小

### 支持新架构

1. 生成对应架构的 `vmlinux.h`
2. 修改 `get_arg_reg` 适配新 ABI
3. 更新 `generate.go` 添加新架构支持
