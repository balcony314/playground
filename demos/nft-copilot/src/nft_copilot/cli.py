"""typer CLI 入口。"""

import os
from pathlib import Path

import typer

from nft_copilot import __version__, validator
from nft_copilot.conflicts import detect_conflicts
from nft_copilot.ir import Policy, PolicyGenerationError
from nft_copilot.llm import explain_config, generate_policy
from nft_copilot.renderer import render_policy
from nft_copilot.risks import analyze_risks
from nft_copilot.store import git_commit, load_policy_ir, save_policy

app = typer.Typer(
    help="AI 防火墙策略助手：自然语言 → nftables 规则（生成/审查/解释）",
    no_args_is_help=True,
)


@app.callback()
def main() -> None:
    """AI 防火墙策略助手：以子命令模式运行（避免 typer 单命令折叠）。"""


_LEVEL_ICON = {"critical": "⛔", "high": "🔴", "medium": "🟡", "low": "🔵"}


def _root() -> Path:
    return Path(os.environ.get("NFT_COPILOT_ROOT", "."))


def _default_name() -> str:
    """确定性默认策略名：按已有 sidecar 数量递增序号（不含时间戳）。"""
    policies_dir = _root() / "policies"
    existing = list(policies_dir.glob("*.json")) if policies_dir.exists() else []
    return f"policy-{len(existing) + 1}"


def _validate_and_report(nft_text: str, label: str = "") -> bool:
    """语法验证并打印结果；返回是否失败（skipped 不算失败）。"""
    validation = validator.validate_syntax(nft_text)
    tag = f"{label} " if label else ""
    if validation.skipped:
        typer.echo(f"\n⏭️  {tag}语法验证跳过：{validation.error}")
    elif validation.ok:
        typer.echo(f"\n✅ {tag}nft -c 语法验证通过")
    else:
        typer.echo(f"\n❌ {tag}语法验证失败：{validation.error}")
        return True
    return False


def _report(policy: Policy, nft_text: str, restricted_nft: str | None = None) -> int:
    """打印摘要/配置/风险/遮蔽/语法报告；返回 critical+high 数量。"""
    typer.echo(f"\n📋 摘要：{policy.summary}")
    typer.echo("\n📄 nftables 配置：\n" + nft_text)
    risks = analyze_risks(policy)
    typer.echo("\n🛡️  风险与遮蔽：")
    found = bool(risks) or bool(detect_conflicts(policy))
    for r in risks:
        typer.echo(f"{_LEVEL_ICON[r.level]} [{r.level}] {r.code}: {r.message}")
    for c in detect_conflicts(policy):
        typer.echo(f"⚠️  [conflict] {c.message}")
    if not found:
        typer.echo("✅ 未发现风险或遮蔽")
    if _validate_and_report(nft_text):
        raise typer.Exit(code=2)
    # 夜间版同样必须通过 nft -c，绝不未验证就落盘
    if restricted_nft is not None and _validate_and_report(restricted_nft, label="夜间版"):
        raise typer.Exit(code=2)
    return sum(1 for r in risks if r.level in ("critical", "high"))


@app.command()
def generate(
    description: str = typer.Argument(..., help="自然语言策略描述"),
    name: str = typer.Option(None, "--name", help="策略名（小写/数字/连字符）"),
    yes: bool = typer.Option(False, "--yes", help="跳过人工审批（CI 用）"),
    model: str = typer.Option(None, "--model", help="覆盖模型 ID"),
) -> None:
    """自然语言 → nftables：生成、审查、审批、入库。"""
    try:
        policy = generate_policy(description, model=model)
    except PolicyGenerationError as exc:
        typer.echo(f"生成失败：{exc}", err=True)
        raise typer.Exit(code=1) from exc

    bundle = render_policy(policy)
    severe = _report(policy, bundle.base_nft, bundle.restricted_nft)

    name = name or _default_name()
    if not yes:
        hint = "（存在 critical/high 风险，请谨慎确认）" if severe else ""
        if not typer.confirm(f"写入该策略到 policies/{name}.nft？{hint}",
                             default=False):
            typer.echo("已取消，未写入任何文件。")
            return

    written = save_policy(_root(), name, bundle, policy, description)
    sha = git_commit(_root(), f"feat: 添加策略 {name}（{policy.summary}）")
    for path in written:
        typer.echo(f"✍️  已写入 {path}")
    if sha:
        typer.echo(f"📦 git 提交 {sha}")


@app.command()
def audit(
    path: str = typer.Option("policies", "--path", help="策略目录"),
) -> None:
    """审计已有策略：风险 + 遮蔽 + nft -c 语法（CI 可用退出码）。"""
    root = Path(path)
    sidecars = sorted(root.glob("*.json"))
    if not sidecars:
        typer.echo(f"{root} 下没有策略（*.json）。")
        return
    failed = False
    for sidecar in sidecars:
        policy = load_policy_ir(sidecar)
        typer.echo(f"\n=== {sidecar.stem} ===")
        nft_file = sidecar.with_suffix(".nft")
        nft_text = nft_file.read_text("utf-8") if nft_file.exists() else ""
        risks = analyze_risks(policy)
        for r in risks:
            typer.echo(f"{_LEVEL_ICON[r.level]} [{r.level}] {r.code}: {r.message}")
            if r.level in ("critical", "high"):
                failed = True
        for c in detect_conflicts(policy):
            typer.echo(f"⚠️  [conflict] {c.message}")
        if nft_text:
            if _validate_and_report(nft_text):
                failed = True
        # 夜间版产物同样纳入审计：失败（非 skipped）同样计为不通过
        restricted_file = sidecar.parent / f"{sidecar.stem}.restricted.nft"
        if restricted_file.exists():
            if _validate_and_report(
                restricted_file.read_text("utf-8"), label="夜间版"
            ):
                failed = True
    if failed:
        raise typer.Exit(code=1)
    typer.echo("\n🎉 审计通过")


@app.command()
def explain(file: str = typer.Argument(..., help="nftables 配置文件路径")) -> None:
    """用中文解释一份 nftables 配置。"""
    nft_text = Path(file).read_text("utf-8")
    try:
        typer.echo(explain_config(nft_text))
    except PolicyGenerationError as exc:
        typer.echo(f"解释失败：{exc}", err=True)
        raise typer.Exit(code=1) from exc


@app.command()
def version() -> None:
    """显示版本号。"""
    typer.echo(f"nft-copilot {__version__}")
