"""Anthropic LLM 集成：自然语言 ⇄ PolicyIR / nftables 解释。

模型默认 claude-opus-4-8（可用 NFT_COPILOT_MODEL 覆盖）；
凭证从环境解析（ANTHROPIC_API_KEY 等），绝不硬编码。
"""

import os
from typing import Any

import anthropic
from pydantic import ValidationError

from nft_copilot.ir import Policy, PolicyGenerationError

DEFAULT_MODEL = "claude-opus-4-8"

SYSTEM_PROMPT = """\
你是一名严谨的 Linux 防火墙策略翻译器，把用户的自然语言需求转换为 \
Policy JSON（结构见 output_format schema）。

必须遵守：
1. 保守原则：需求含糊时选择更严格（更小放行面）的规则，并把你的假设\
写进 summary，例如「办公网未给网段，假设为 10.0.0.0/8」。
2. IP/网段只能来自用户描述或明确的私网假设（10.0.0.0/8、172.16.0.0/12、\
192.168.0.0/16），不得编造具体地址。
3. summary 用简体中文，1–3 句，说明策略做什么、做了哪些假设。
4. 不支持的能力（应用层过滤、域名过滤、每用户限速等）不要伪造，\
在 summary 中说明未实现。
5. 用户明确要求「其他全拒/默认拒绝」时，default_input 设为 drop；
未提及时优先 drop。
6. 端口只用用户提到的；协议未说明且涉及端口时默认 tcp。
"""


def _resolve(model: str | None) -> str:
    return model or os.environ.get("NFT_COPILOT_MODEL", DEFAULT_MODEL)


def _client_or_default(client: anthropic.Anthropic | None) -> anthropic.Anthropic:
    return client if client is not None else anthropic.Anthropic()


def _wrap_api_error(exc: Exception) -> PolicyGenerationError:
    """把 SDK 异常转成带中文说明的策略生成错误。"""
    if isinstance(exc, anthropic.AuthenticationError):
        return PolicyGenerationError(
            f"Anthropic API 认证失败：请检查 ANTHROPIC_API_KEY（{exc}）"
        )
    if isinstance(exc, anthropic.RateLimitError):
        return PolicyGenerationError(f"API 限流，请稍后重试（{exc}）")
    if isinstance(exc, anthropic.APIConnectionError):
        return PolicyGenerationError(f"无法连接 Anthropic API（{exc}）")
    return PolicyGenerationError(f"调用 Anthropic API 失败：{exc}")


def generate_policy(
    description: str,
    client: anthropic.Anthropic | None = None,
    model: str | None = None,
) -> Policy:
    """自然语言 → 经 pydantic 校验的 Policy。"""
    try:
        response = _client_or_default(client).messages.parse(
            model=_resolve(model),
            max_tokens=16000,
            thinking={"type": "adaptive"},
            system=SYSTEM_PROMPT,
            messages=[{"role": "user", "content": description}],
            output_format=Policy,
        )
    except ValidationError as exc:
        # SDK 在 structured output 内容非纯 JSON（如 markdown 代码块包裹）时抛
        # pydantic ValidationError，必须转成 PolicyGenerationError 才能被 CLI 捕获。
        raise PolicyGenerationError(
            "模型输出未通过 Policy schema 校验（可能返回了非纯 JSON 内容）"
        ) from exc
    except anthropic.APIError as exc:
        raise _wrap_api_error(exc) from exc
    if response.stop_reason == "refusal":
        raise PolicyGenerationError("模型拒绝了该请求（安全策略）")
    parsed: Policy | None = response.parsed_output
    if parsed is None:
        raise PolicyGenerationError("模型输出未通过 Policy schema 校验")
    return parsed


def explain_config(
    nft_text: str,
    client: anthropic.Anthropic | None = None,
    model: str | None = None,
) -> str:
    """用中文解释一份 nftables 配置的作用与潜在风险。"""
    prompt = (
        "请用简体中文解释以下 nftables 配置：逐链说明默认策略与每条规则\
的作用，并指出潜在风险（过宽放行、ANY-ANY、敏感端口等）。配置如下：\n"
        f"```\n{nft_text}\n```"
    )
    try:
        response = _client_or_default(client).messages.create(
            model=_resolve(model),
            max_tokens=8000,
            thinking={"type": "adaptive"},
            messages=[{"role": "user", "content": prompt}],
        )
    except anthropic.APIError as exc:
        raise _wrap_api_error(exc) from exc
    parts = [b.text for b in response.content if b.type == "text"]
    return "\n".join(parts)
