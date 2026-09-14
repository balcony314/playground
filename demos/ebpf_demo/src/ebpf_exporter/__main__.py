"""命令行入口。

用法::

    sudo python -m ebpf_exporter tcp   # TCP 连接时长监控 + /metrics HTTP 服务
    sudo python -m ebpf_exporter dns   # DNS 查询捕获（前台打印结构化日志）

监控模块按需延迟导入：运行 tcp 不需要 dnslib，运行 dns 不需要 Flask。
加载 BPF 程序需要 root 权限，普通用户运行会由 BCC 报错。
"""

import argparse


def main() -> None:
    """解析子命令并启动对应的监控器。"""
    parser = argparse.ArgumentParser(
        prog="ebpf_exporter",
        description="基于 BCC 的 eBPF 网络监控导出器",
    )
    sub = parser.add_subparsers(dest="monitor", required=True)

    sub.add_parser(
        "tcp",
        help="TCP 连接时长监控（tracepoint），暴露 Prometheus 指标",
    )
    sub.add_parser(
        "dns",
        help="DNS 查询捕获（kprobe + socket filter），前台打印日志",
    )

    args = parser.parse_args()

    # 延迟导入：只加载当前监控器所需的依赖
    if args.monitor == "tcp":
        from .tcp_monitor import run
    else:
        from .dns_monitor import run

    run()


if __name__ == "__main__":
    main()
