"""集中管理运行配置。

所有配置均可通过环境变量覆盖，默认值与原版脚本保持一致，
无需修改代码即可在不同环境（裸机 / 容器 / K8s）中调整过滤行为。

环境变量一览：

========================  =============================================  =========
环境变量                  含义                                           默认值
========================  =============================================  =========
FILTER_COMMANDS           精确匹配的进程名黑名单（逗号分隔）              空
FILTER_COMMAND_PREFIXES   进程名前缀黑名单（逗号分隔）                    空
IGNORE_LOOPBACK           是否忽略环回地址 127.0.0.1 的连接              1
MAX_EVENTS_PER_ADDR       每个源/目的地址最大记录事件数（基数保护）      500
LISTEN_HOST               HTTP 服务监听地址                              0.0.0.0
LISTEN_PORT               HTTP 服务监听端口                              9435
========================  =============================================  =========
"""

import os
from typing import Tuple


def _csv_env(name: str) -> Tuple[str, ...]:
    """读取逗号分隔的环境变量，返回去除空白项后的元组。

    例如 FILTER_COMMANDS="sshd,dockerd" -> ("sshd", "dockerd")
    """
    raw = os.environ.get(name, "")
    return tuple(item.strip() for item in raw.split(",") if item.strip())


def _bool_env(name: str, default: bool) -> bool:
    """读取布尔型环境变量，接受 0/false/no（不区分大小写）表示关闭。"""
    raw = os.environ.get(name)
    if raw is None:
        return default
    return raw.strip().lower() not in ("0", "false", "no")


# 进程名黑名单（精确匹配）。
# 原版脚本硬编码了一批公司内部组件名，这里改为运行时可配置，默认不过滤。
FILTER_COMMANDS: Tuple[str, ...] = _csv_env("FILTER_COMMANDS")

# 进程名前缀黑名单。
# 原版脚本用于过滤 "wrk:worker"（压测工具）和 "kube" 前缀（K8s 组件），
# 这类环境相关的噪声进程改由使用方按需配置。
FILTER_COMMAND_PREFIXES: Tuple[str, ...] = _csv_env("FILTER_COMMAND_PREFIXES")

# 是否忽略环回地址（127.0.0.1）上的连接。
# 本机内部通信通常没有观测价值，默认忽略。
IGNORE_LOOPBACK: bool = _bool_env("IGNORE_LOOPBACK", True)

# 基数保护：每个源地址 / 目的地址各自最多累计记录多少条事件。
# 防止地址数量爆炸导致 Prometheus 指标标签基数失控（原版为 500）。
MAX_EVENTS_PER_ADDR: int = int(os.environ.get("MAX_EVENTS_PER_ADDR", "500"))

# HTTP 服务监听地址与端口（/metrics 与健康检查）。
LISTEN_HOST: str = os.environ.get("LISTEN_HOST", "0.0.0.0")
LISTEN_PORT: int = int(os.environ.get("LISTEN_PORT", "9435"))
