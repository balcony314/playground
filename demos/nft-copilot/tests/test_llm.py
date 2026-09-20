"""LLM 模块测试：全程 mock，不真实调用 API。"""

from types import SimpleNamespace
from unittest.mock import MagicMock

import pytest

import nft_copilot.llm as llm
from nft_copilot.ir import Policy, PolicyGenerationError, Rule


def _fake_client(parsed=None, stop_reason="end_turn", text="解释文本",
                 create_error=None):
    client = MagicMock()
    parse_result = SimpleNamespace(parsed_output=parsed, stop_reason=stop_reason)
    client.messages.parse = MagicMock(return_value=parse_result)
    if create_error is not None:
        client.messages.create = MagicMock(side_effect=create_error)
    else:
        client.messages.create = MagicMock(return_value=SimpleNamespace(
            content=[SimpleNamespace(type="text", text=text)]
        ))
    return client


def test_generate_policy_返回校验后的_policy() -> None:
    policy = Policy(summary="测试", rules=[Rule(source="10.0.0.0/24", port=443,
                                                protocol="tcp")])
    client = _fake_client(parsed=policy)
    result = llm.generate_policy("只允许内网访问 443", client=client)
    assert result.rules[0].port == 443
    client.messages.parse.assert_called_once()
    kwargs = client.messages.parse.call_args.kwargs
    assert kwargs["output_format"] is Policy
    assert kwargs["model"] == "claude-opus-4-8"


def test_generate_policy_模型可被环境变量覆盖(monkeypatch) -> None:
    monkeypatch.setenv("NFT_COPILOT_MODEL", "claude-sonnet-5")
    client = _fake_client(parsed=Policy(summary="x"))
    llm.generate_policy("描述", client=client)
    kwargs = client.messages.parse.call_args.kwargs
    assert kwargs["model"] == "claude-sonnet-5"


def test_generate_policy_拒答抛异常() -> None:
    client = _fake_client(parsed=None, stop_reason="refusal")
    with pytest.raises(PolicyGenerationError, match="拒绝"):
        llm.generate_policy("描述", client=client)


def test_generate_policy_解析失败抛异常() -> None:
    client = _fake_client(parsed=None, stop_reason="end_turn")
    with pytest.raises(PolicyGenerationError):
        llm.generate_policy("描述", client=client)


def test_generate_policy_SDK校验异常转策略生成错误() -> None:
    from pydantic import ValidationError

    client = MagicMock()
    client.messages.parse = MagicMock(
        side_effect=ValidationError.from_exception_data("Policy", [])
    )
    with pytest.raises(PolicyGenerationError, match="schema"):
        llm.generate_policy("描述", client=client)


def test_explain_config_返回文本() -> None:
    client = _fake_client()
    text = llm.explain_config("table inet filter { }", client=client)
    assert text == "解释文本"


def test_explain_config_认证错误转中文异常() -> None:
    import anthropic
    client = _fake_client(
        create_error=anthropic.AuthenticationError(
            message="invalid x-api-key", response=MagicMock(status_code=401),
            body=None,
        )
    )
    with pytest.raises(PolicyGenerationError, match="认证"):
        llm.explain_config("...", client=client)


def test_系统提示词包含保守原则() -> None:
    assert "不得编造" in llm.SYSTEM_PROMPT
    assert "summary" in llm.SYSTEM_PROMPT
