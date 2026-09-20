"""策略中间表示（PolicyIR）。

LLM 的唯一输出目标：结构化策略 JSON 经 pydantic 严格校验后，
才允许进入渲染管线。LLM 永不直接生成 nftables 文本。
"""

import ipaddress
import re

from pydantic import BaseModel, field_validator, model_validator

# fullmatch + re.ASCII：拒绝尾换行（如 "22:00\n"）与全角数字（如 "２２:３０"）
_TIME_RE = re.compile(r"([01]\d|2[0-3]):[0-5]\d", re.ASCII)


class PolicyGenerationError(Exception):
    """策略生成失败（LLM 拒答、输出不合法等）。"""


class TimeWindow(BaseModel):
    """时间窗口，如 22:00–06:00（允许跨午夜）。"""

    start: str
    end: str

    @field_validator("start", "end")
    @classmethod
    def _check_hhmm(cls, v: str) -> str:
        if not _TIME_RE.fullmatch(v):
            raise ValueError(f"时间必须为 HH:MM（00:00–23:59），收到: {v!r}")
        return v


class Rule(BaseModel):
    """单条过滤规则。"""

    direction: str = "input"  # "input" | "output"（Literal 见模型配置）
    action: str = "accept"  # "accept" | "drop" | "reject"
    protocol: str = "any"  # "tcp" | "udp" | "icmp" | "any"
    source: str | None = None  # CIDR；None = 任意来源
    port: int | list[int] | None = None  # 目标端口
    time_window: TimeWindow | None = None

    @field_validator("direction")
    @classmethod
    def _check_direction(cls, v: str) -> str:
        if v not in ("input", "output"):
            raise ValueError(f"direction 只能是 input/output，收到: {v!r}")
        return v

    @field_validator("action")
    @classmethod
    def _check_action(cls, v: str) -> str:
        if v not in ("accept", "drop", "reject"):
            raise ValueError(f"action 只能是 accept/drop/reject，收到: {v!r}")
        return v

    @field_validator("protocol")
    @classmethod
    def _check_protocol(cls, v: str) -> str:
        if v not in ("tcp", "udp", "icmp", "any"):
            raise ValueError(f"protocol 只能是 tcp/udp/icmp/any，收到: {v!r}")
        return v

    @field_validator("source")
    @classmethod
    def _check_cidr(cls, v: str | None) -> str | None:
        if v is None:
            return None
        try:
            return str(ipaddress.ip_network(v, strict=False))
        except ValueError as exc:
            raise ValueError(f"source 必须是合法 CIDR: {v!r}") from exc

    @field_validator("port")
    @classmethod
    def _check_port(cls, v: int | list[int] | None) -> int | list[int] | None:
        if v is None:
            return None
        ports = [v] if isinstance(v, int) else v
        for p in ports:
            if not 1 <= p <= 65535:
                raise ValueError(f"端口必须在 1–65535，收到: {p}")
        return v

    @model_validator(mode="after")
    def _check_port_requires_protocol(self) -> "Rule":
        """跨字段校验：protocol 为 any 时端口无从渲染，必须在 IR 层拒绝。

        否则「仅放行 443」这类规则会被渲染成放行一切，且风险分析全盲。
        """
        if self.port is not None and self.protocol == "any":
            raise ValueError("protocol 为 any 时不能指定 port")
        return self


class Policy(BaseModel):
    """一份完整防火墙策略（LLM 结构化输出的根对象）。"""

    summary: str  # 人类可读中文摘要
    default_input: str = "drop"  # input 链默认策略
    default_output: str = "accept"  # output 链默认策略
    rules: list[Rule] = []

    @field_validator("summary")
    @classmethod
    def _check_summary(cls, v: str) -> str:
        if not v.strip():
            raise ValueError("summary 不能为空")
        return v

    @field_validator("default_input", "default_output")
    @classmethod
    def _check_default(cls, v: str) -> str:
        if v not in ("accept", "drop"):
            raise ValueError(f"默认策略只能是 accept/drop，收到: {v!r}")
        return v
