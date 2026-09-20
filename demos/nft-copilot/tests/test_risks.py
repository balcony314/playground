"""风险分析器测试。"""

from nft_copilot.ir import Policy, Rule
from nft_copilot.risks import analyze_risks


def _codes(risks):
    return [r.code for r in risks]


def test_默认放行_input_是_critical() -> None:
    risks = analyze_risks(Policy(summary="x", default_input="accept"))
    assert "DEFAULT_INPUT_ACCEPT" in _codes(risks)
    level = {r.code: r.level for r in risks}["DEFAULT_INPUT_ACCEPT"]
    assert level == "critical"


def test_任意来源任意端口放行_是_high() -> None:
    risks = analyze_risks(
        Policy(summary="x", rules=[Rule(action="accept", protocol="any")])
    )
    assert "ANY_ANY_ACCEPT" in _codes(risks)


def test_私网来源放行_无公网风险() -> None:
    risks = analyze_risks(
        Policy(summary="x",
               rules=[Rule(action="accept", source="10.0.0.0/8", port=443,
                           protocol="tcp")])
    )
    assert "PUBLIC_SOURCE_ACCEPT" not in _codes(risks)


def test_公网来源放行_是_medium() -> None:
    risks = analyze_risks(
        Policy(summary="x",
               rules=[Rule(action="accept", source="203.0.113.0/24", port=443,
                           protocol="tcp")])
    )
    assert "PUBLIC_SOURCE_ACCEPT" in _codes(risks)


def test_宽端口_是_medium() -> None:
    risks = analyze_risks(
        Policy(summary="x",
               rules=[Rule(action="accept", port=list(range(1, 13)),
                           protocol="tcp")])
    )
    assert "WIDE_PORTS" in _codes(risks)


def test_敏感端口_是_medium() -> None:
    risks = analyze_risks(
        Policy(summary="x", rules=[Rule(action="accept", port=3389,
                                        protocol="tcp")])
    )
    assert "SENSITIVE_PORT" in _codes(risks)


def test_空规则集_是_low() -> None:
    risks = analyze_risks(Policy(summary="x"))
    assert "NO_RULES" in _codes(risks)


def test_出站默认放行_是_low() -> None:
    risks = analyze_risks(Policy(summary="x", default_output="accept"))
    assert "OUTPUT_POLICY_ACCEPT" in _codes(risks)
