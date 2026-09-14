"""TCP 连接监控模块（基于 tracepoint，移植自原 tcp_v2.py）。

原理
====

通过内核自带的 ``sock:inet_sock_set_state`` tracepoint（Linux 4.16+）
观测每条 TCP socket 的状态机变迁。相比 kprobe 方案（原 tcp.py v1），
tracepoint 是内核提供的稳定跟踪点，函数签名不会随内核版本漂移。

eBPF 侧工作流程::

    1. 连接建立（newstate < TCP_FIN_WAIT1，含 SYN_SENT/SYN_RECV 等）
       - birth 哈希表记录 socket 指针 -> 出生时间戳
       - whoami 哈希表记录 socket 指针 -> 进程信息
         （SYN_SENT = 主动连接 typ='C'；SYN_RECV = 被动接受 typ='A'）

    2. 连接关闭（newstate == TCP_CLOSE）
       - 当前时间 - 出生时间 = 连接存活时长 span_us
       - 从 struct tcp_sock 读取该连接累计收发字节数（rx_b / tx_b）
       - 五元组 + 进程信息 + 时长打包成事件，经 perf buffer 送往用户态

用户态将时长（毫秒）写入 Prometheus Histogram，按
``type``（C=主动 / A=被动 / U=未知）、``source_addr``、``dest_addr`` 打标签。

运行（需要 root 权限加载 BPF 程序）::

    sudo python -m ebpf_exporter tcp

指标::

    ebpf_tcp_duration_millisecond_bucket / _sum / _count
"""

import threading
from socket import AF_INET, inet_ntop
from struct import pack
from typing import Dict

from bcc import BPF
from prometheus_client import Histogram

from . import config
from .server import serve

# ---------------------------------------------------------------------------
# BPF 程序（C 语法，由 BCC 即时编译并注入内核）
# ---------------------------------------------------------------------------
BPF_TEXT = r"""
#include <uapi/linux/ptrace.h>
#include <linux/tcp.h>
#include <net/sock.h>
#include <bcc/proto.h>

// socket 指针 -> 连接出生时间戳（纳秒）。
// 连接刚建立（newstate < TCP_FIN_WAIT1）时写入，关闭时用于计算存活时长。
BPF_HASH(birth, struct sock *, u64);

// 上报给用户态的事件结构（仅支持 IPv4）。
struct ipv4_data_t {
    u64 ts_us;                      // 事件时间戳（微秒）
    u32 pid;                        // 进程 ID（tgid）
    u32 saddr;                      // 源地址（网络字节序）
    u32 daddr;                      // 目的地址（网络字节序）
    u64 ports;                      // lport/dport 打包存储（用户态未使用）
    u64 rx_b;                       // 连接累计接收字节数
    u64 tx_b;                       // 连接累计发送（已确认）字节数
    u64 span_us;                    // 连接存活时长（微秒）
    char task[TASK_COMM_LEN];       // 进程名（comm）
    char typ[16];                   // 连接类型：C=主动连接 A=被动接受 U=未知
};

// perf buffer：eBPF -> 用户态的事件通道
BPF_PERF_OUTPUT(ipv4_events);

// socket 指针 -> 发起连接的进程信息。
// 用于在连接关闭时找回"是谁建立的这条连接"（关闭动作可能由内核线程执行）。
struct id_t {
    u32 pid;                        // 进程 ID
    char task[TASK_COMM_LEN];       // 进程名
    char typ[16];                   // C=主动连接 A=被动接受
};
BPF_HASH(whoami, struct sock *, struct id_t);

// sock:inet_sock_set_state 跟踪点：TCP socket 每次状态变迁都会触发。
// TRACEPOINT_PROBE 宏由 BCC 展开，自动完成挂载，args 即跟踪点参数。
TRACEPOINT_PROBE(sock, inet_sock_set_state)
{
    // 本监控只关心 TCP
    if (args->protocol != IPPROTO_TCP)
        return 0;

    u32 pid = bpf_get_current_pid_tgid() >> 32;
    struct sock *sk = (struct sock *)args->skaddr;
    u16 lport = args->sport;
    u16 dport = args->dport;

    // 状态回退到建立早期（SYN_SENT / SYN_RECV / ESTABLISHED 等），
    // 视为"连接出生"，记录时间戳。
    if (args->newstate < TCP_FIN_WAIT1) {
        u64 ts = bpf_ktime_get_ns();
        birth.update(&sk, &ts);
    }

    // 主动发起连接（客户端，发了 SYN）：记录进程信息，标记为 'C'
    if (args->newstate == TCP_SYN_SENT) {
        struct id_t me = {.pid = pid};
        me.typ[0] = 'C';
        bpf_get_current_comm(&me.task, sizeof(me.task));
        whoami.update(&sk, &me);
    }

    // 被动接受连接（服务端，收到 SYN）：记录进程信息，标记为 'A'
    if (args->newstate == TCP_SYN_RECV){
        struct id_t me = {.pid = pid};
        me.typ[0] = 'A';
        bpf_get_current_comm(&me.task, sizeof(me.task));
        whoami.update(&sk, &me);
    }

    // 只在连接彻底关闭时上报事件，其余状态变迁到此结束
    if (args->newstate != TCP_CLOSE)
        return 0;

    // 查找出生时间，计算存活时长
    u64 *tsp, delta_us;
    tsp = birth.lookup(&sk);
    if (tsp == 0) {
        whoami.delete(&sk);     // 可能不存在，删除幂等
        return 0;               // 没观察到连接建立（监控启动前已存在），放弃
    }
    delta_us = (bpf_ktime_get_ns() - *tsp) / 1000;
    birth.delete(&sk);

    // 找回建立连接的进程；找不到则 pid 用当前进程、类型标 'U'（未知）
    struct id_t *mep;
    mep = whoami.lookup(&sk);
    if (mep != 0)
        pid = mep->pid;

    // 本模块仅支持 IPv4，跳过 IPv6 连接
    // （原版未做此判断，IPv6 事件会被误读为乱码 IPv4 地址）
    if (args->family != AF_INET)
        return 0;

    // 从 tcp_sock 读取该连接生命周期内的收发字节统计
    u64 rx_b = 0, tx_b = 0;
    struct tcp_sock *tp = (struct tcp_sock *)sk;
    rx_b = tp->bytes_received;
    tx_b = tp->bytes_acked;

    // 组装事件并上报用户态
    struct ipv4_data_t data4 = {};
    data4.span_us = delta_us;
    data4.rx_b = rx_b;
    data4.tx_b = tx_b;
    data4.ts_us = bpf_ktime_get_ns() / 1000;
    __builtin_memcpy(&data4.saddr, args->saddr, sizeof(data4.saddr));
    __builtin_memcpy(&data4.daddr, args->daddr, sizeof(data4.daddr));
    // 原版写法：结构体暂不支持分离的 lport/dport 字段，暂打包进 u64
    // （当前用户态并未使用端口信息，保留以兼容原始数据结构）
    data4.ports = dport + ((0ULL + lport) << 32);
    data4.pid = pid;
    if (mep == 0) {
        // 关闭时才首次观察到（如监控启动前已建立的连接）：
        // 用当前进程信息兜底，类型标 'U'
        bpf_get_current_comm(&data4.task, sizeof(data4.task));
        data4.typ[0] = 'U';
    } else {
        bpf_probe_read_kernel(&data4.task, sizeof(data4.task), (void *)mep->task);
        data4.typ[0] = mep->typ[0];
    }
    ipv4_events.perf_submit(args, &data4, sizeof(data4));

    if (mep != 0)
        whoami.delete(&sk);
    return 0;
}
"""

# ---------------------------------------------------------------------------
# Prometheus 指标
# ---------------------------------------------------------------------------

# 直方图桶边界（毫秒）：从 5 微秒到 100 秒指数式放宽，最后 +Inf 由客户端自动补充
_DURATION_BUCKETS = (
    0.005, 0.01, 0.1, 0.5, 1.0, 10.0, 100.0, 1000.0, 10000.0, 100000.0,
    float("inf"),
)

# TCP 连接存活时长直方图（毫秒）。
TCP_DURATION = Histogram(
    "ebpf_tcp_duration_millisecond",
    "TCP 连接存活时长（毫秒）",
    ["type", "source_addr", "dest_addr"],
    buckets=_DURATION_BUCKETS,
)

# ---------------------------------------------------------------------------
# 模块级状态
# ---------------------------------------------------------------------------

# BPF 实例（_init_bpf() 中创建，事件回调中用来解码事件）
_bpf = None

# 基数保护计数器：每个源/目的地址已记录的事件数。
# 防止异常场景（端口扫描、高频短连接）撑爆 Prometheus 标签基数。
_source_event_count: Dict[str, int] = {}
_dest_event_count: Dict[str, int] = {}


# ---------------------------------------------------------------------------
# 事件处理
# ---------------------------------------------------------------------------

def _handle_event(cpu, data, size) -> None:
    """perf buffer 回调：解码内核上报的连接关闭事件并写入 Histogram。

    :param cpu:  产生事件的 CPU 编号（BCC 回调签名要求，未使用）
    :param data: 原始事件字节串
    :param size: 事件字节数
    """
    # 按 ipv4_data_t 结构解码事件
    event = _bpf["ipv4_events"].event(data)

    # 进程名 / 类型解码；rstrip("\x00") 去掉定长字符数组的 NUL 填充，
    # 保证精确匹配与前缀匹配过滤正确生效
    command = event.task.decode("utf-8", "replace").rstrip("\x00")
    typ = event.typ.decode("utf-8", "replace").rstrip("\x00")

    # u32 整数（网络字节序）转点分十进制字符串
    source_addr = inet_ntop(AF_INET, pack("I", event.saddr))
    dest_addr = inet_ntop(AF_INET, pack("I", event.daddr))

    # ---- 过滤噪声数据 ----
    # 1. 黑名单进程（精确匹配 + 前缀匹配，见 config.py 说明）
    if command in config.FILTER_COMMANDS:
        return
    if command.startswith(config.FILTER_COMMAND_PREFIXES):
        return
    # 2. 环回地址（本机内部通信）
    if config.IGNORE_LOOPBACK and "127.0.0.1" in (source_addr, dest_addr):
        return

    # 被动接受的连接（typ='A'）：内核视角的源/目的地址与业务视角相反，
    # 交换后统一为 "客户端 -> 服务端" 方向，便于跨方向聚合查询
    if typ == "A":
        source_addr, dest_addr = dest_addr, source_addr

    # ---- 基数保护 ----
    # 任一端地址的事件数达到上限后，新事件直接丢弃
    source_count = _source_event_count.get(source_addr, 0)
    dest_count = _dest_event_count.get(dest_addr, 0)
    if source_count > config.MAX_EVENTS_PER_ADDR or dest_count > config.MAX_EVENTS_PER_ADDR:
        return

    # 微秒 -> 毫秒，写入直方图
    duration_ms = float(event.span_us) / 1000
    TCP_DURATION.labels(typ, source_addr, dest_addr).observe(duration_ms)
    _source_event_count[source_addr] = source_count + 1
    _dest_event_count[dest_addr] = dest_count + 1


# ---------------------------------------------------------------------------
# 启动流程
# ---------------------------------------------------------------------------

def _init_bpf() -> None:
    """编译并加载 BPF 程序，注册事件回调。

    TRACEPOINT_PROBE 宏在 BPF() 构造时自动完成 tracepoint 挂载，
    无需（也无法）再手动 attach。
    """
    global _bpf
    _bpf = BPF(text=BPF_TEXT)
    _bpf["ipv4_events"].open_perf_buffer(_handle_event)


def _start_event_loop() -> None:
    """在后台守护线程中轮询 perf buffer。

    perf_buffer_poll() 阻塞等待内核事件并触发 _handle_event 回调；
    放入独立线程使 HTTP 服务可以并行运行。
    """

    def _loop() -> None:
        while True:
            try:
                _bpf.perf_buffer_poll()
            except KeyboardInterrupt:
                return

    threading.Thread(target=_loop, daemon=True).start()


def run() -> None:
    """模块入口：初始化 eBPF，启动事件循环与 HTTP 服务（阻塞）。"""
    _init_bpf()
    _start_event_loop()
    print("Tracing TCP connections (tracepoint: sock/inet_sock_set_state). "
          "Ctrl-C to end.")
    serve(config.LISTEN_HOST, config.LISTEN_PORT)
