"""确定性风险分析：对 PolicyIR 做静态检查。

不依赖 LLM——风险结论必须可复现、可审计。
"""

import ipaddress
from dataclasses import dataclass

from nft_copilot.ir import Policy, Rule

SENSITIVE_PORTS = {21, 23, 135, 139, 445, 3389, 5900, 6379, 9200, 27017}

# RFC1918 私网段。不直接用 net.is_private：Python 3.12 会把 TEST-NET
# 文档段（如 203.0.113.0/24）也判为 private，导致公网来源漏报。
_RFC1918 = (
    ipaddress.ip_network("10.0.0.0/8"),
    ipaddress.ip_network("172.16.0.0/12"),
    ipaddress.ip_network("192.168.0.0/16"),
)


@dataclass
class Risk:
    """一条风险发现。"""

    level: str  # critical | high | medium | low
    code: str
    message: str


def _is_public(cidr: str) -> bool:
    """是否公网地址（非回环/RFC1918 私网/链路本地/组播）。"""
    net = ipaddress.ip_network(cidr)
    return not (
        net.is_loopback or net.is_link_local or net.is_multicast
        or any(net.version == r.version and net.subnet_of(r) for r in _RFC1918)
    )


def _rule_ports(rule: Rule) -> list[int]:
    if rule.port is None:
        return []
    return rule.port if isinstance(rule.port, list) else [rule.port]


def analyze_risks(policy: Policy) -> list[Risk]:
    """对策略做全量风险扫描。"""
    risks: list[Risk] = []

    if policy.default_input == "accept":
        risks.append(Risk(
            "critical", "DEFAULT_INPUT_ACCEPT",
            "input 链默认策略为 accept：未匹配任何规则的入站流量全部放行",
        ))
    if not policy.rules:
        risks.append(Risk(
            "low", "NO_RULES", "策略不含任何规则，仅剩默认策略生效",
        ))
    if policy.default_output == "accept":
        risks.append(Risk(
            "low", "OUTPUT_POLICY_ACCEPT",
            "output 链默认策略为 accept：出站流量默认全部放行",
        ))

    for idx, rule in enumerate(policy.rules):
        if rule.source is None and rule.port is None and rule.action == "accept":
            risks.append(Risk(
                "high", "ANY_ANY_ACCEPT",
                f"规则 #{idx + 1}：任意来源、任意端口放行（ANY-ANY）",
            ))
        if rule.action == "accept" and rule.source and _is_public(rule.source):
            risks.append(Risk(
                "medium", "PUBLIC_SOURCE_ACCEPT",
                f"规则 #{idx + 1}：放行公网来源 {rule.source}",
            ))
        ports = _rule_ports(rule)
        if len(ports) > 10:
            risks.append(Risk(
                "medium", "WIDE_PORTS",
                f"规则 #{idx + 1}：单条规则放行 {len(ports)} 个端口，范围过宽",
            ))
        hit = sorted(set(ports) & SENSITIVE_PORTS)
        if hit:
            risks.append(Risk(
                "medium", "SENSITIVE_PORT",
                f"规则 #{idx + 1}：涉及敏感端口 {hit}（远程管理/数据库/文件共享）",
            ))
    return risks
