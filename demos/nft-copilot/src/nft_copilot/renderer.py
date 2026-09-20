"""确定性渲染器：PolicyIR → nftables 配置。

纯函数，无 LLM 参与。时间窗口规则通过 systemd timer
在「夜间版 / 常规版」两份配置间定时切换实现。
"""

from dataclasses import dataclass

from nft_copilot.ir import Policy, Rule


@dataclass
class PolicyBundle:
    """一份策略渲染出的全部产物。"""

    base_nft: str  # 常规版（不含时间窗口规则）
    restricted_nft: str | None = None  # 夜间版（含时间窗口规则）
    timers: str | None = None  # systemd 单元（成对 timer+service）


_HEADER = "#!/usr/sbin/nft -f\nflush ruleset\n\n"

_FIXED_INPUT = ['iifname "lo" accept', "ct state established,related accept"]
_FIXED_OUTPUT = ['oifname "lo" accept', "ct state established,related accept"]


def _render_rule(rule: Rule) -> str:
    """单条 Rule → nft 规则语句（无换行）。"""
    parts: list[str] = []
    if rule.source:
        parts.append(f"ip saddr {rule.source}")
    if rule.protocol != "any":
        if rule.port is not None:
            ports = rule.port if isinstance(rule.port, list) else [rule.port]
            port_str = (
                f"{{ {', '.join(str(p) for p in ports)} }}"
                if len(ports) > 1
                else str(ports[0])
            )
            parts.append(f"{rule.protocol} dport {port_str}")
        else:
            # 有协议无端口：用 meta l4proto 匹配协议，绝不静默丢弃协议限定
            parts.append(f"meta l4proto {rule.protocol}")
    parts.append(rule.action)
    return " ".join(parts)


def _render_nft(policy: Policy, rules: list[Rule]) -> str:
    """渲染完整 nftables 配置文本。"""
    input_rules = [r for r in rules if r.direction == "input"]
    output_rules = [r for r in rules if r.direction == "output"]
    lines = [_HEADER.rstrip(), "", "table inet filter {"]
    lines.append("    chain input {")
    lines.append("        type filter hook input priority filter; "
                 f"policy {policy.default_input};")
    for fixed in _FIXED_INPUT:
        lines.append(f"        {fixed}")
    for r in input_rules:
        lines.append(f"        {_render_rule(r)}")
    lines.append("    }")
    lines.append("    chain output {")
    lines.append("        type filter hook output priority filter; "
                 f"policy {policy.default_output};")
    for fixed in _FIXED_OUTPUT:
        lines.append(f"        {fixed}")
    for r in output_rules:
        lines.append(f"        {_render_rule(r)}")
    lines.append("    }")
    lines.append("}")
    return "\n".join(lines) + "\n"


_TIMER_TEMPLATE = """\
# systemd 单元说明：本文件含 4 个单元（2 个 oneshot service + 2 个 timer），
# 安装前必须拆分为 4 个独立文件放入 /etc/systemd/system/：
#   nft-copilot-restrict.service、nft-copilot-base.service、
#   nft-copilot-restrict.timer、nft-copilot-base.timer
#（各单元以空行分隔，按段归属拆分），然后
# systemctl daemon-reload && systemctl enable --now nft-copilot-restrict.timer nft-copilot-base.timer

[Unit]
Description=nft-copilot 夜间加载（含时间限制规则）

[Service]
Type=oneshot
ExecStart=/usr/sbin/nft -f /etc/nftables/nft-copilot.restricted.nft

[Unit]
Description=nft-copilot 日间恢复（常规规则）

[Service]
Type=oneshot
ExecStart=/usr/sbin/nft -f /etc/nftables/nft-copilot.base.nft

[Unit]
Description=nft-copilot 夜间切换定时器

[Timer]
OnCalendar={start}
Persistent=true

[Install]
WantedBy=timers.target

[Unit]
Description=nft-copilot 日间切换定时器

[Timer]
OnCalendar={end}
Persistent=true

[Install]
WantedBy=timers.target
"""


def render_policy(policy: Policy) -> PolicyBundle:
    """渲染策略：常规规则入 base，时间窗口规则只入夜间版。"""
    timed = [r for r in policy.rules if r.time_window is not None]
    untimed = [r for r in policy.rules if r.time_window is None]
    bundle = PolicyBundle(base_nft=_render_nft(policy, untimed))
    if timed:
        bundle.restricted_nft = _render_nft(policy, untimed + timed)
        window = timed[0].time_window
        timers = _TIMER_TEMPLATE.format(
            start=f"*-*-* {window.start}:00",
            end=f"*-*-* {window.end}:00",
        )
        distinct_windows = {(r.time_window.start, r.time_window.end) for r in timed}
        if len(distinct_windows) > 1:
            timers = (
                f"# ⚠️ 警告：本策略含多个不同时间窗口，"
                f"仅第一个窗口（{window.start}–{window.end}）生效，"
                "其余窗口的规则将始终随夜间版加载\n\n" + timers
            )
        bundle.timers = timers
    return bundle
