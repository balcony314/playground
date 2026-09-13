#ifndef __EVENTS_H
#define __EVENTS_H

// 最大参数数量
#define MAX_ARGS 6
// 最大参数数据大小 (减少以适应 eBPF 栈限制 512 字节)
// 原来 64 字节 -> 结构体 528 字节 -> 超出栈限制
// 现在 16 字节 -> 结构体 192 字节 -> 安全
#define MAX_ARG_SIZE 16

// 参数类型枚举
enum arg_type {
    ARG_TYPE_INT = 0,
    ARG_TYPE_UINT,
    ARG_TYPE_FLOAT64,
    ARG_TYPE_STRING,
    ARG_TYPE_SLICE,
    ARG_TYPE_STRUCT,
    ARG_TYPE_MAP,
    ARG_TYPE_CHANNEL,
    ARG_TYPE_POINTER,
};

// 参数数据
struct arg_data {
    __u64 type;           // enum arg_type
    __u64 size;           // 数据大小
    __u8  data[MAX_ARG_SIZE]; // 原始数据
};

// 函数调用事件
struct func_event {
    __u64 timestamp;      // 时间戳 (ns)
    __u32 pid;            // 进程 ID
    __u32 tid;            // 线程 ID
    __u64 goroutine_id;   // goroutine ID (如果可用)
    __u32 func_id;        // 函数 ID (用户态映射)
    __u32 is_return;      // 是否是返回事件
    __u64 duration_ns;    // 调用耗时 (仅返回事件有效)
    __u64 return_value;   // 返回值 (整数)
    struct arg_data args[MAX_ARGS]; // 参数数据
};

// BPF Map 定义

// perf event array 用于发送事件到用户态
struct {
    __uint(type, BPF_MAP_TYPE_PERF_EVENT_ARRAY);
    __uint(key_size, sizeof(__u32));
    __uint(value_size, sizeof(__u32));
} events SEC(".maps");

// per-cpu array 用于存储临时事件数据
struct {
    __uint(type, BPF_MAP_TYPE_PERCPU_ARRAY);
    __uint(max_entries, 1);
    __type(key, __u32);
    __type(value, struct func_event);
} event_heap SEC(".maps");

// hash map 用于存储函数入口时间戳
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, 10240);
    __type(key, __u64);   // tid
    __type(value, __u64); // 入口时间戳
} start_times SEC(".maps");

#endif /* __EVENTS_H */
