"""遮蔽检测测试。"""

import ipaddress

from nft_copilot.ir import Policy, Rule
from nft_copilot.conflicts import detect_conflicts


def test_宽规则遮蔽窄规则() -> None:
    policy = Policy(summary="x", rules=[
        Rule(action="accept", source="10.0.0.0/8", port=443, protocol="tcp"),
        Rule(action="drop", source="10.1.0.0/16", port=443, protocol="tcp"),
    ])
    conflicts = detect_conflicts(policy)
    assert len(conflicts) == 1
    assert conflicts[0].earlier == 1 and conflicts[0].later == 2


def test_无遮蔽返回空() -> None:
    policy = Policy(summary="x", rules=[
        Rule(action="accept", source="10.0.0.0/8", port=443, protocol="tcp"),
        Rule(action="accept", source="10.1.0.0/16", port=22, protocol="tcp"),
    ])
    assert detect_conflicts(policy) == []


def test_不同方向不比较() -> None:
    policy = Policy(summary="x", rules=[
        Rule(direction="input", action="accept"),
        Rule(direction="output", action="drop"),
    ])
    assert detect_conflicts(policy) == []


def test_同_action_不报() -> None:
    policy = Policy(summary="x", rules=[
        Rule(action="accept", source="10.0.0.0/8"),
        Rule(action="accept", source="10.1.0.0/16"),
    ])
    assert detect_conflicts(policy) == []


def test_任意来源遮蔽具体来源() -> None:
    policy = Policy(summary="x", rules=[
        Rule(action="drop", protocol="any"),
        Rule(action="accept", source="192.168.1.0/24", port=80, protocol="tcp"),
    ])
    assert len(detect_conflicts(policy)) == 1


def test_时间窗口不同不判遮蔽() -> None:
    """裁决 6：前者覆盖后者但 time_window 不同 → 不报遮蔽。"""
    policy = Policy(summary="x", rules=[
        Rule(action="accept", source="10.0.0.0/8", port=443, protocol="tcp"),
        Rule(action="drop", source="10.1.0.0/16", port=443, protocol="tcp",
             time_window={"start": "22:00", "end": "06:00"}),
    ])
    assert detect_conflicts(policy) == []


def test_跨地址族不判遮蔽() -> None:
    """early 与 late 分属 IPv4/IPv6 → 必然不覆盖，不抛 TypeError。"""
    policy = Policy(summary="x", rules=[
        Rule(action="accept", source="10.0.0.0/8", port=443, protocol="tcp"),
        Rule(action="drop", source="fd00::/8", port=443, protocol="tcp"),
    ])
    assert detect_conflicts(policy) == []
