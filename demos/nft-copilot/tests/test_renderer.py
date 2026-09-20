"""渲染器测试：IR → nftables 文本。"""

from nft_copilot.ir import Policy, Rule, TimeWindow
from nft_copilot.renderer import render_policy


def test_基础策略渲染() -> None:
    policy = Policy(
        summary="仅允许内网访问 HTTPS",
        default_input="drop",
        default_output="accept",
        rules=[
            Rule(direction="input", action="accept", protocol="tcp",
                 source="10.0.0.0/24", port=443),
        ],
    )
    bundle = render_policy(policy)
    text = bundle.base_nft
    assert text.startswith("#!/usr/sbin/nft -f")
    assert "flush ruleset" in text
    assert 'table inet filter' in text
    assert "policy drop" in text  # input 默认策略
    assert 'ip saddr 10.0.0.0/24 tcp dport 443 accept' in text
    assert 'iifname "lo" accept' in text
    assert "ct state established,related accept" in text
    # 无时间规则 → 无夜间版与 timer
    assert bundle.restricted_nft is None
    assert bundle.timers is None


def test_多端口渲染为集合() -> None:
    policy = Policy(
        summary="多端口",
        rules=[Rule(action="accept", protocol="tcp", port=[80, 443])],
    )
    assert "tcp dport { 80, 443 } accept" in render_policy(policy).base_nft


def test_协议无端口渲染为_l4proto() -> None:
    # 有协议无端口：不允许退化为裸 action（协议被静默丢弃），必须保留协议限定
    policy = Policy(
        summary="仅放行 TCP",
        default_input="drop",
        rules=[Rule(direction="input", action="accept", protocol="tcp")],
    )
    assert "meta l4proto tcp accept" in render_policy(policy).base_nft


def test_output_方向与_drop() -> None:
    policy = Policy(
        summary="禁出网",
        rules=[Rule(direction="output", action="drop", protocol="any")],
    )
    assert "drop" in render_policy(policy).base_nft


def test_时间窗口生成_夜间版与_timer() -> None:
    policy = Policy(
        summary="夜间禁出网",
        default_output="accept",
        rules=[
            Rule(direction="output", action="drop", protocol="any",
                 time_window=TimeWindow(start="22:00", end="06:00")),
        ],
    )
    bundle = render_policy(policy)
    assert bundle.restricted_nft is not None
    assert bundle.timers is not None
    assert "drop" in bundle.restricted_nft
    # 常规版不含该时间规则（出网默认 accept、无 drop 规则）
    assert "OnCalendar=*-*-* 22:00:00" in bundle.timers
    assert "OnCalendar=*-*-* 06:00:00" in bundle.timers
    assert "nft -f" in bundle.timers


def test_多时间窗口生成警告() -> None:
    policy = Policy(
        summary="双窗口",
        default_output="accept",
        rules=[
            Rule(direction="output", action="drop", protocol="any",
                 time_window=TimeWindow(start="22:00", end="06:00")),
            Rule(direction="output", action="drop", protocol="udp", port=53,
                 time_window=TimeWindow(start="12:00", end="13:00")),
        ],
    )
    bundle = render_policy(policy)
    assert bundle.timers is not None
    assert "警告" in bundle.timers
    assert "仅第一个窗口（22:00–06:00）生效" in bundle.timers
