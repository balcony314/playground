"""PolicyIR 模型校验测试。"""

import pytest
from pydantic import ValidationError

from nft_copilot.ir import Policy, Rule, TimeWindow


def test_最小合法策略() -> None:
    policy = Policy(summary="测试", rules=[])
    assert policy.default_input == "drop"


def test_完整规则() -> None:
    rule = Rule(
        direction="input",
        action="accept",
        protocol="tcp",
        source="10.0.0.0/24",
        port=[443, 8443],
    )
    assert rule.port == [443, 8443]


def test_非法_cidr_被拒绝() -> None:
    with pytest.raises(ValidationError):
        Rule(source="999.0.0.0/24")


def test_非规范主机位_cidr_被接受() -> None:
    # strict=False：10.0.0.1/24 归一化为 10.0.0.0/24
    rule = Rule(source="10.0.0.1/24")
    assert rule.source == "10.0.0.0/24"


def test_非法端口_被拒绝() -> None:
    with pytest.raises(ValidationError):
        Rule(port=0)
    with pytest.raises(ValidationError):
        Rule(port=70000)


def test_非法时间格式_被拒绝() -> None:
    with pytest.raises(ValidationError):
        TimeWindow(start="25:00", end="06:00")
    with pytest.raises(ValidationError):
        TimeWindow(start="6点", end="22:00")


def test_时间带尾换行_被拒绝() -> None:
    # 旧实现 ^...$ 的 match 会被尾换行骗过，fullmatch 修复
    with pytest.raises(ValidationError):
        TimeWindow(start="22:00\n", end="06:00")


def test_全角数字时间_被拒绝() -> None:
    # re.ASCII：\d 只匹配 ASCII 数字，全角数字必须被拒
    with pytest.raises(ValidationError):
        TimeWindow(start="２２:３０", end="06:00")


def test_协议为any时指定端口_被拒绝() -> None:
    # port 依赖具体协议才可渲染；any+port 会被静默放宽为放行一切
    with pytest.raises(ValidationError, match="protocol 为 any 时不能指定 port"):
        Rule(action="accept", protocol="any", port=443)
    with pytest.raises(ValidationError):
        Rule(action="drop", protocol="any", port=[53, 123])


def test_跨午夜时间窗口_合法() -> None:
    tw = TimeWindow(start="22:00", end="06:00")
    assert tw.start == "22:00"
