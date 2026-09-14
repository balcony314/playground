# eBPF 导出器镜像
#
# 基础镜像选择 ubuntu:22.04 而非官方 python 镜像：
# BCC 运行时需要即时编译 BPF 程序，依赖内核头文件与 LLVM 工具链，
# 这些通过 apt 的 bpfcc-tools / python3-bpfcc 安装（pip 版 bcc 需源码编译，非常缓慢）。
# 容器启动时仍需挂载宿主机 /lib/modules 与内核源码目录（见 Taskfile docker-run）。
FROM ubuntu:22.04

# PYTHONUNBUFFERED：日志实时输出，便于 kubectl logs 查看
ENV PYTHONUNBUFFERED=1 \
    PYTHONPATH=/app/src

# bpfcc-tools        BCC 工具集与 LLVM（BPF 即时编译）
# python3-bpfcc      Python bcc 模块（import bcc）
# python3-pip        安装其余纯 Python 依赖
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        bpfcc-tools \
        python3-bpfcc \
        python3-pip \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# 先单独复制依赖清单，充分利用 Docker 层缓存
COPY requirements.txt .
RUN pip3 install --no-cache-dir -r requirements.txt

COPY src/ src/

# Prometheus 抓取端口
EXPOSE 9435

# 入口：python -m ebpf_exporter [tcp|dns]，默认启动 TCP 监控
ENTRYPOINT ["python3", "-m", "ebpf_exporter"]
CMD ["tcp"]
