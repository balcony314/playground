"""DNS 查询捕获模块（基于 kprobe + socket filter，移植自原 dns.py）。

原理
====

单个 eBPF 程序无法同时拿到"报文内容"和"发起报文的进程"，因此采用
两个 BPF 程序配合，通过共享哈希表 ``proc_ports`` 传递进程信息：

1. kprobe 程序（挂 udp_sendmsg / tcp_sendmsg）
   在应用调用发送函数时，把 **五元组 -> 进程信息（pid/uid/comm）**
   写入 PUBLIC 哈希表 proc_ports（只处理 53 端口，即 DNS）。

2. socket filter 程序（挂到原始套接字）
   在报文经过协议栈时解析以太网/IP/TCP/UDP 头，对 53 端口的 DNS
   报文用五元组反查 proc_ports 表找回进程信息，连同报文原文一起
   经 perf buffer 送往用户态。

用户态再用 dnslib 解析报文，输出携带完整上下文的结构化日志::

    COMM=curl PID=12345 DEV=eth0 PROTO=UDP SRC=10.0.0.2 DST=10.0.0.1
    SPT=40000 DPT=53 DNS_QR=0 DNS_NAME=example.com. DNS_TYPE=A

运行（需要 root 权限加载 BPF 程序）::

    sudo python -m ebpf_exporter dns

依赖：Python 库 dnslib（pip install dnslib）。
"""

import ctypes as ct
import sys
from socket import if_indextoname
from struct import unpack

from bcc import BPF

# ---------------------------------------------------------------------------
# BPF 程序一：kprobe（捕获"谁在发 DNS 报文"）
# ---------------------------------------------------------------------------
C_BPF_KPROBE = r"""
#include <net/sock.h>

// proc_ports 表的 key：四元组 + 协议号（6=TCP 17=UDP）
struct port_key {
    u8 proto;
    u32 saddr;
    u32 daddr;
    u16 sport;
    u16 dport;
};

// proc_ports 表的 value：发起连接的进程信息
struct port_val {
    u32 ifindex;        // 报文出口网卡索引（由 socket filter 侧回填）
    u32 pid;            // 线程 ID（pid_tgid 低 32 位）
    u32 tgid;           // 进程 ID（pid_tgid 高 32 位）
    u32 uid;            // 用户 ID
    u32 gid;            // 组 ID
    char comm[64];      // 线程名
};

// PUBLIC 哈希表：跨 BPF 程序共享。
// kprobe 侧写入，socket filter 侧读取。
// 容量 20480：按四元组去重，足够容纳常见并发下的活跃 DNS 连接数。
BPF_TABLE_PUBLIC("hash", struct port_key, struct port_val, proc_ports, 20480);

// 挂 udp_sendmsg：参数一即 struct sock *
int trace_udp_sendmsg(struct pt_regs *ctx) {
    struct sock *sk = (struct sock *)PT_REGS_PARM1(ctx);

    // sk_num 是本端端口（主机字节序），sk_dport 是对端端口（网络字节序）
    u16 sport = sk->sk_num;
    u16 dport = sk->sk_dport;

    // 只处理 53 端口（DNS）。dport 是网络字节序，13568 == ntohs(53)
    if (sport == 13568 || dport == 13568) {
        u32 saddr = sk->sk_rcv_saddr;
        u32 daddr = sk->sk_daddr;
        u64 pid_tgid = bpf_get_current_pid_tgid();
        u64 uid_gid = bpf_get_current_uid_gid();

        // 组装 key：统一转为网络字节序存储
        struct port_key key = {.proto = 17};    // 17 = IPPROTO_UDP
        key.saddr = htonl(saddr);
        key.daddr = htonl(daddr);
        key.sport = sport;
        key.dport = htons(dport);

        // 组装 value：进程上下文
        struct port_val val = {};
        val.pid = pid_tgid >> 32;
        val.tgid = (u32)pid_tgid;
        val.uid = (u32)uid_gid;
        val.gid = uid_gid >> 32;
        bpf_get_current_comm(val.comm, 64);

        proc_ports.update(&key, &val);
    }
    return 0;
}

// 挂 tcp_sendmsg：参数二即 struct sock *（参数一是 iov，参数二是 sock）
int trace_tcp_sendmsg(struct pt_regs *ctx, struct sock *sk) {
    u16 sport = sk->sk_num;
    u16 dport = sk->sk_dport;

    // 同上：只处理 53 端口
    if (sport == 13568 || dport == 13568) {
        u32 saddr = sk->sk_rcv_saddr;
        u32 daddr = sk->sk_daddr;
        u64 pid_tgid = bpf_get_current_pid_tgid();
        u64 uid_gid = bpf_get_current_uid_gid();

        struct port_key key = {.proto = 6};     // 6 = IPPROTO_TCP
        key.saddr = htonl(saddr);
        key.daddr = htonl(daddr);
        key.sport = sport;
        key.dport = htons(dport);

        struct port_val val = {};
        val.pid = pid_tgid >> 32;
        val.tgid = (u32)pid_tgid;
        val.uid = (u32)uid_gid;
        val.gid = uid_gid >> 32;
        bpf_get_current_comm(val.comm, 64);

        proc_ports.update(&key, &val);
    }
    return 0;
}
"""

# ---------------------------------------------------------------------------
# BPF 程序二：socket filter（捕获 DNS 报文本体并关联进程）
# ---------------------------------------------------------------------------
BPF_SOCK_TEXT = r"""
#include <net/sock.h>
#include <bcc/proto.h>

// 与 kprobe 侧保持一致的 key/value 结构
struct port_key {
    u8 proto;
    u32 saddr;
    u32 daddr;
    u16 sport;
    u16 dport;
};

struct port_val {
    u32 ifindex;
    u32 pid;
    u32 tgid;
    u32 uid;
    u32 gid;
    char comm[64];
};

// extern 声明：引用 kprobe 侧创建的 PUBLIC 表（同一内核中的共享实例）
BPF_TABLE("extern", struct port_key, struct port_val, proc_ports, 20480);

// perf buffer：把 port_val（进程信息）+ 报文原文一起送往用户态
BPF_PERF_OUTPUT(dns_events);

// 挂在原始套接字上的过滤器：对每个经过的报文执行。
// cursor_advance 辅助函数按协议层次逐层推进"游标"完成手动解析。
int dns_matching(struct __sk_buff *skb) {
    u8 *cursor = 0;

    // 第一层：以太网头
    struct ethernet_t *ethernet = cursor_advance(cursor, sizeof(*ethernet));

    if (ethernet->type == ETH_P_IP) {
        // 第二层：IP 头
        struct ip_t *ip = cursor_advance(cursor, sizeof(*ip));

        u8 proto;
        u16 sport;
        u16 dport;

        // 第三层：传输层（UDP / TCP）
        if (ip->nextp == IPPROTO_UDP) {
            struct udp_t *udp = cursor_advance(cursor, sizeof(*udp));

            proto = 17;
            sport = udp->sport;
            dport = udp->dport;
        } else if (ip->nextp == IPPROTO_TCP) {
            struct tcp_t *tcp = cursor_advance(cursor, sizeof(*tcp));

            // TCP DNS 响应通过数据段承载；纯 ACK（无 PSH 标志）不携带数据，跳过
            if (!tcp->flag_psh) {
                return 0;
            }

            proto = 6;
            sport = tcp->src_port;
            dport = tcp->dst_port;
        } else {
            return 0;
        }

        // 只处理 DNS 报文（任一端端口为 53）
        if (dport == 53 || sport == 53) {
            // 用五元组组装 key。
            // ingress_ifindex == 0 表示本机发出的报文（出向）：
            //   ip->src 即客户端地址，直接映射；
            // 否则为收到的响应报文（入向）：
            //   服务器是源、客户端是目的，需反转后再与 kprobe 侧的 key 对齐。
            struct port_key key = {};
            key.proto = proto;
            if (skb->ingress_ifindex == 0) {
                key.saddr = ip->src;
                key.daddr = ip->dst;
                key.sport = sport;
                key.dport = dport;
            } else {
                key.saddr = ip->dst;
                key.daddr = ip->src;
                key.sport = dport;
                key.dport = sport;
            }

            // 反查共享表，找回发送时记录的进程信息
            struct port_val *p_val;
            p_val = proc_ports.lookup(&key);

            // 查不到说明该连接不在监控范围内（如监控启动前已建立），放弃
            if (!p_val) {
                return 0;
            }

            // 回填报文实际经过的网卡索引（供用户态解析网卡名）
            p_val->ifindex = skb->ifindex;

            // perf_submit_skb：把 port_val 结构 + skb->len 字节的报文
            // 原文一并送往用户态
            dns_events.perf_submit_skb(skb, skb->len, p_val,
                                       sizeof(struct port_val));
            return 0;
        }
    }

    return 0;
}
"""

# 传输层协议号 -> 名称
NET_PROTO = {6: "TCP", 17: "UDP"}

# DNS 资源记录类型（本工具只关注地址类记录）
DNS_QTYPE = {1: "A", 28: "AAAA"}


def _parse_ip_packet(ip_packet: bytes) -> dict:
    """解析 IP 头及其后的传输层头，返回地址/端口/传输层载荷偏移信息。

    :param ip_packet: 去掉以太网头（14 字节）后的 IP 报文
    :returns: dict(saddr, daddr, proto, sport, dport, dns_packet)
    :raises ValueError: 协议不受支持时抛出
    """
    # IP 头前 20 字节：版本+首部长度 / TOS / 总长 / 标识 / 标志分片 / TTL /
    # 协议 / 校验和 / 源地址 / 目的地址（! 表示网络字节序）
    (length, _, _, _, _, proto, _, saddr, daddr) = unpack(
        "!BBHLBBHLL", ip_packet[:20])
    # 首 4 比特为版本、低 4 比特为首部长度（单位：4 字节字），换算为字节数
    len_iph = (length & 15) * 4
    # u32 整数 -> 点分十进制
    saddr = ".".join(map(str, [saddr >> 24 & 0xff, saddr >> 16 & 0xff,
                               saddr >> 8 & 0xff, saddr & 0xff]))
    daddr = ".".join(map(str, [daddr >> 24 & 0xff, daddr >> 16 & 0xff,
                               daddr >> 8 & 0xff, daddr & 0xff]))

    if proto == 17:
        # UDP：头固定 8 字节，其后即 DNS 报文
        udp_packet = ip_packet[len_iph:]
        (sport, dport) = unpack("!HH", udp_packet[:4])
        dns_packet = udp_packet[8:]
    elif proto == 6:
        # TCP：头长度可变（选项字段），需从第 13 字节的高 4 位取头长
        tcp_packet = ip_packet[len_iph:]
        (sport, dport, _, length) = unpack("!HHQB", tcp_packet[:13])
        len_tcph = (length >> 4) * 4
        # 原版注释保留：原因未明的 2 字节偏移——缺少它 DNS 报文起始位置
        # 会错位（推测与 perf_submit_skb 的截断对齐有关），必须保留
        dns_packet = tcp_packet[len_tcph + 2:]
    else:
        # 其他协议（如 QUIC/DoT over 非 53 端口）不处理
        raise ValueError(f"unsupported protocol: {proto}")

    return {
        "saddr": saddr, "daddr": daddr, "proto": proto,
        "sport": sport, "dport": dport, "dns_packet": dns_packet,
    }


def print_dns(cpu, data, size) -> None:
    """perf buffer 回调：解码事件 + 报文原文，解析 DNS 并打印结构化日志。

    :param cpu:  产生事件的 CPU 编号
    :param data: 原始字节串（port_val 结构 + 报文原文）
    :param size: data 总字节数
    """
    import dnslib

    # 与内核侧 port_val 对齐的 ctypes 结构：
    # 前 5 个 u32 + 64 字节 comm 是 port_val，其后紧跟报文原文
    class SkbEvent(ct.Structure):
        _fields_ = [
            ("ifindex", ct.c_uint32),
            ("pid", ct.c_uint32),
            ("tgid", ct.c_uint32),
            ("uid", ct.c_uint32),
            ("gid", ct.c_uint32),
            ("comm", ct.c_char * 64),
            ("raw", ct.c_ubyte * (size - ct.sizeof(ct.c_uint32 * 5)
                                  - ct.sizeof(ct.c_char * 64))),
        ]

    sk = ct.cast(data, ct.POINTER(SkbEvent)).contents

    # eBPF 侧拿到的是线程名（comm），常常与进程名不同，
    # 优先用 PID 从 /proc 读取真实进程名，失败再退回 comm
    try:
        with open(f"/proc/{sk.pid}/comm", "r") as proc_comm:
            proc_name = proc_comm.read().rstrip()
    except OSError:
        proc_name = sk.comm.decode("utf-8", "replace").rstrip("\x00")

    # 网卡索引 -> 网卡名
    ifname = if_indextoname(sk.ifindex)

    # 跳过 14 字节以太网头得到 IP 报文
    ip_packet = bytes(sk.raw[14:])

    try:
        info = _parse_ip_packet(ip_packet)
    except ValueError:
        return

    # 解析 DNS 报文
    dns_data = dnslib.DNSRecord.parse(info["dns_packet"])

    # 日志公共字段：进程 / 网卡 / 五元组 / 用户
    prefix = (f"COMM={proc_name} PID={sk.pid} TGID={sk.tgid} DEV={ifname} "
              f"PROTO={NET_PROTO[info['proto']]} SRC={info['saddr']} "
              f"DST={info['daddr']} SPT={info['sport']} DPT={info['dport']} "
              f"UID={sk.uid} GID={sk.gid}")

    # QR=0：查询报文；QR=1：响应报文
    if dns_data.header.qr == 0:
        # 只输出 A / AAAA 查询
        for q in dns_data.questions:
            if q.qtype in DNS_QTYPE:
                print(f"{prefix} DNS_QR=0 DNS_NAME={q.qname} "
                      f"DNS_TYPE={DNS_QTYPE[q.qtype]}")
    elif dns_data.header.qr == 1:
        # 只输出 A / AAAA 应答
        for rr in dns_data.rr:
            if rr.rtype in DNS_QTYPE:
                print(f"{prefix} DNS_QR=1 DNS_NAME={rr.rname} "
                      f"DNS_TYPE={DNS_QTYPE[rr.rtype]} DNS_DATA={rr.rdata}")
    else:
        print("Invalid DNS query type.")


def run() -> None:
    """模块入口：加载两段 BPF 程序并阻塞轮询事件（Ctrl-C 退出）。"""
    # dnslib 仅本模块需要，延迟导入并给出清晰的安装提示
    try:
        import dnslib  # noqa: F401
    except ImportError:
        print("Error: Python dnslib module required.")
        print("Install it with:")
        print("\t$ pip3 install dnslib")
        print(" or")
        print("\t$ sudo apt install python3-dnslib (on Ubuntu 20+)")
        sys.exit(1)

    # 程序一：kprobe 捕获发送时的进程信息
    bpf_kprobe = BPF(text=C_BPF_KPROBE)
    # 程序二：socket filter 捕获报文并关联进程
    bpf_sock = BPF(text=BPF_SOCK_TEXT)

    bpf_kprobe.attach_kprobe(event="udp_sendmsg", fn_name="trace_udp_sendmsg")
    bpf_kprobe.attach_kprobe(event="tcp_sendmsg", fn_name="trace_tcp_sendmsg")

    # 以 socket filter 类型加载，并挂到任意接口的原始套接字
    # （'' 表示不限定接口，捕获所有流量）
    function_dns_matching = bpf_sock.load_func("dns_matching", BPF.SOCKET_FILTER)
    BPF.attach_raw_socket(function_dns_matching, "")

    print("The program is running. Press Ctrl-C to abort.")
    bpf_sock["dns_events"].open_perf_buffer(print_dns)

    while True:
        try:
            bpf_sock.perf_buffer_poll()
        except KeyboardInterrupt:
            sys.exit(0)
