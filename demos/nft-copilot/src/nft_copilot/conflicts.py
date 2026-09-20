"""规则遮蔽（shadowing）检测。

nftables 规则自上而下首匹配生效——若前面的规则完全覆盖
后面的规则且动作不同，后者永不生效。
"""

import ipaddress
from dataclasses import dataclass

from nft_copilot.ir import Policy, Rule


@dataclass
class Conflict:
    """一条遮蔽发现。序号为 1 起。"""

    earlier: int
    later: int
    message: str


def _covers_source(early: Rule, late: Rule) -> bool:
    if early.source is None:
        return True
    if late.source is None:
        return False
    early_net = ipaddress.ip_network(early.source)
    late_net = ipaddress.ip_network(late.source)
    # 跨地址族（IPv4/IPv6）必然不覆盖，subnet_of 会抛 TypeError，先短路
    if early_net.version != late_net.version:
        return False
    return late_net.subnet_of(early_net)


def _port_set(rule: Rule) -> set[int] | None:
    if rule.port is None:
        return None
    ports = rule.port if isinstance(rule.port, list) else [rule.port]
    return set(ports)


def _covers_port(early: Rule, late: Rule) -> bool:
    early_ports, late_ports = _port_set(early), _port_set(late)
    if early_ports is None:
        return True
    if late_ports is None:
        return False
    return late_ports <= early_ports


def _covers(early: Rule, late: Rule) -> bool:
    if early.protocol != "any" and early.protocol != late.protocol:
        return False
    # 裁决 6：时间窗口必须均为 None 或相等，否则生效时段不同，不判遮蔽
    if not (
        early.time_window is None and late.time_window is None
        or early.time_window == late.time_window
    ):
        return False
    return _covers_source(early, late) and _covers_port(early, late)


def detect_conflicts(policy: Policy) -> list[Conflict]:
    """报告被更早规则遮蔽、永不生效的规则。"""
    conflicts: list[Conflict] = []
    for i, later in enumerate(policy.rules):
        for j in range(i):
            earlier = policy.rules[j]
            if earlier.direction != later.direction:
                continue
            if earlier.action == later.action:
                continue
            if _covers(earlier, later):
                conflicts.append(Conflict(
                    earlier=j + 1, later=i + 1,
                    message=f"规则 #{i + 1} 被更早的规则 #{j + 1} 完全遮蔽，"
                            f"永不生效（动作 {earlier.action} 先于 {later.action}）",
                ))
    return conflicts
