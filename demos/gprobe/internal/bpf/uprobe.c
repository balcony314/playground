//go:build ignore

#include "vmlinux.h"
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_tracing.h>
#include <bpf/bpf_core_read.h>
#include "events.h"

// 配置 map（运行时可修改）
struct {
    __uint(type, BPF_MAP_TYPE_ARRAY);
    __uint(max_entries, 2);
    __type(key, __u32);
    __type(value, __u32);
} probe_config SEC(".maps");

// probe_config key 定义
#define CONFIG_PID 0
#define CONFIG_FUNC_ID 1

// 获取 goroutine ID
// 注意: 在 BPF 中无法直接读取 Go runtime 的 TLS
// 需要通过其他方式获取，如从栈中解析或使用 uprobe 参数
static __always_inline __u64 get_goroutine_id(void) {
    // TODO: 实现 goroutine ID 获取
    // 暂时返回 0，后续可以通过解析 Go runtime 结构实现
    return 0;
}

// 函数入口探针
SEC("uprobe/func_entry")
int uprobe_func_entry(struct pt_regs *ctx) {
    __u64 pid_tgid = bpf_get_current_pid_tgid();
    __u32 pid = pid_tgid >> 32;
    __u32 tid = (__u32)pid_tgid;

    // 从 config map 读取配置
    __u32 config_key = CONFIG_PID;
    __u32 *target_pid = bpf_map_lookup_elem(&probe_config, &config_key);
    if (target_pid && *target_pid != 0 && pid != *target_pid) {
        return 0;
    }

    // 记录入口时间
    __u64 ts = bpf_ktime_get_ns();
    __u64 key64 = tid; // 显式扩展为 64 位
    bpf_map_update_elem(&start_times, &key64, &ts, BPF_ANY);

    // 使用 per-cpu array 获取缓冲区
    __u32 key = 0;
    struct func_event *event = bpf_map_lookup_elem(&event_heap, &key);
    if (!event) {
        return 0;
    }

    // 清零
    __builtin_memset(event, 0, sizeof(*event));

    // 填充事件数据
    event->timestamp = ts;
    event->pid = pid;
    event->tid = tid;
    event->goroutine_id = 0;

    // 从 config map 读取 func_id
    __u32 func_id_key = CONFIG_FUNC_ID;
    __u32 *func_id = bpf_map_lookup_elem(&probe_config, &func_id_key);
    event->func_id = func_id ? *func_id : 0;
    event->is_return = 0;

    // 读取前 6 个参数 (寄存器 ABI)
    event->args[0].type = ARG_TYPE_UINT;
    event->args[0].size = sizeof(__u64);
    *((__u64 *)event->args[0].data) = PT_REGS_PARM1(ctx);

    event->args[1].type = ARG_TYPE_UINT;
    event->args[1].size = sizeof(__u64);
    *((__u64 *)event->args[1].data) = PT_REGS_PARM2(ctx);

    event->args[2].type = ARG_TYPE_UINT;
    event->args[2].size = sizeof(__u64);
    *((__u64 *)event->args[2].data) = PT_REGS_PARM3(ctx);

    event->args[3].type = ARG_TYPE_UINT;
    event->args[3].size = sizeof(__u64);
    *((__u64 *)event->args[3].data) = PT_REGS_PARM4(ctx);

    event->args[4].type = ARG_TYPE_UINT;
    event->args[4].size = sizeof(__u64);
    *((__u64 *)event->args[4].data) = PT_REGS_PARM5(ctx);

    event->args[5].type = ARG_TYPE_UINT;
    event->args[5].size = sizeof(__u64);
#ifdef __TARGET_ARCH_x86
    *((__u64 *)event->args[5].data) = ctx->r8;
#else
    *((__u64 *)event->args[5].data) = 0;
#endif

    // 发送事件
    bpf_perf_event_output(ctx, &events, BPF_F_CURRENT_CPU, event, sizeof(*event));

    return 0;
}

// 函数返回探针
SEC("uprobe/func_return")
int uprobe_func_return(struct pt_regs *ctx) {
    __u64 pid_tgid = bpf_get_current_pid_tgid();
    __u32 pid = pid_tgid >> 32;
    __u32 tid = (__u32)pid_tgid;

    // 从 config map 读取配置
    __u32 config_key = CONFIG_PID;
    __u32 *target_pid = bpf_map_lookup_elem(&probe_config, &config_key);
    if (target_pid && *target_pid != 0 && pid != *target_pid) {
        return 0;
    }

    // 计算耗时
    __u64 key64 = tid; // 显式扩展为 64 位
    __u64 *start_ts = bpf_map_lookup_elem(&start_times, &key64);
    __u64 duration = 0;
    if (start_ts) {
        duration = bpf_ktime_get_ns() - *start_ts;
        bpf_map_delete_elem(&start_times, &key64);
    }

    // 从 per-cpu array 获取事件缓冲区
    __u32 key = 0;
    struct func_event *event = bpf_map_lookup_elem(&event_heap, &key);
    if (!event) {
        return 0;
    }

    // 清零事件数据
    __builtin_memset(event, 0, sizeof(*event));

    // 填充返回事件
    event->timestamp = bpf_ktime_get_ns();
    event->pid = pid;
    event->tid = tid;
    event->goroutine_id = get_goroutine_id();

    // 从 config map 读取 func_id
    __u32 func_id_key = CONFIG_FUNC_ID;
    __u32 *func_id = bpf_map_lookup_elem(&probe_config, &func_id_key);
    event->func_id = func_id ? *func_id : 0;
    event->is_return = 1;
    event->duration_ns = duration;

    // 读取返回值 (RAX)
    event->return_value = PT_REGS_RC(ctx);

    // 发送事件到用户态
    bpf_perf_event_output(ctx, &events, BPF_F_CURRENT_CPU, event, sizeof(*event));

    return 0;
}

char _license[] SEC("license") = "GPL";
