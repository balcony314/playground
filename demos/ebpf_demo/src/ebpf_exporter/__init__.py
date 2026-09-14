"""ebpf_exporter —— 基于 BCC 的 eBPF 网络监控导出器。

包含两类监控器：

* tcp_monitor: 基于 tracepoint 的 TCP 连接时长监控，暴露 Prometheus 指标
* dns_monitor: 基于 kprobe + socket filter 的 DNS 查询捕获，输出结构化日志

统一入口::

    sudo python -m ebpf_exporter tcp   # 启动 TCP 监控 + /metrics HTTP 服务
    sudo python -m ebpf_exporter dns   # 启动 DNS 捕获（前台打印）
"""

__version__ = "1.0.0"
