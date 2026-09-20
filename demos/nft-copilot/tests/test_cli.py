"""CLI 测试：version 冒烟 + generate/audit/explain（mock LLM 与审批输入）。"""

from unittest.mock import patch

from typer.testing import CliRunner

from nft_copilot.cli import app
from nft_copilot.ir import Policy, Rule, TimeWindow
from nft_copilot.validator import ValidationResult

runner = CliRunner()


def test_version_command() -> None:
    result = runner.invoke(app, ["version"])
    assert result.exit_code == 0
    assert "nft-copilot" in result.stdout


# --- generate/audit/explain 命令测试（mock LLM 与审批输入）---


def _policy() -> Policy:
    return Policy(
        summary="仅允许内网 HTTPS",
        default_input="drop",
        rules=[Rule(source="10.0.0.0/24", port=443, protocol="tcp")],
    )


def test_generate_审批后写盘并提交(tmp_path) -> None:
    policy = _policy()
    with (
        patch("nft_copilot.llm.generate_policy", return_value=policy),
        patch("nft_copilot.cli.generate_policy", return_value=policy),
    ):
        result = runner.invoke(app, [
            "generate", "只允许 10.0.0.0/24 访问 443",
            "--name", "intranet-https", "--yes",
        ], env={"NFT_COPILOT_ROOT": str(tmp_path)})
    assert result.exit_code == 0, result.stdout
    assert (tmp_path / "policies" / "intranet-https.nft").exists()
    assert (tmp_path / "policies" / "intranet-https.json").exists()
    assert (tmp_path / ".git").exists()
    assert "仅允许内网 HTTPS" in result.stdout
    assert "风险" in result.stdout


def test_generate_拒绝审批_不写盘(tmp_path) -> None:
    policy = _policy()
    with (
        patch("nft_copilot.llm.generate_policy", return_value=policy),
        patch("nft_copilot.cli.generate_policy", return_value=policy),
    ):
        result = runner.invoke(app, ["generate", "描述", "--name", "x1"],
                               input="n\n",
                               env={"NFT_COPILOT_ROOT": str(tmp_path)})
    assert result.exit_code == 0
    assert not (tmp_path / "policies" / "x1.nft").exists()


def test_generate_语法失败_中止(tmp_path) -> None:
    policy = _policy()
    with (
        patch("nft_copilot.cli.generate_policy", return_value=policy),
        patch("nft_copilot.validator.validate_syntax",
              return_value=ValidationResult(ok=False, error="语法错误")),
    ):
        result = runner.invoke(app, [
            "generate", "描述", "--name", "bad", "--yes",
        ], env={"NFT_COPILOT_ROOT": str(tmp_path)})
    assert result.exit_code != 0
    assert not (tmp_path / "policies" / "bad.nft").exists()


def test_generate_夜间版语法失败_中止不写盘(tmp_path) -> None:
    """restricted_nft 必须同样过 nft -c：夜间版失败 → 退出码 2、零写盘。"""
    policy = Policy(
        summary="夜间禁出网",
        default_output="accept",
        rules=[Rule(direction="output", action="drop", protocol="any",
                    time_window=TimeWindow(start="22:00", end="06:00"))],
    )
    # 第一次调用验证基础版（通过），第二次验证夜间版（失败）
    with (
        patch("nft_copilot.cli.generate_policy", return_value=policy),
        patch("nft_copilot.validator.validate_syntax", side_effect=[
            ValidationResult(ok=True),
            ValidationResult(ok=False, error="语法错误"),
        ]),
    ):
        result = runner.invoke(app, [
            "generate", "夜间禁出网", "--name", "night", "--yes",
        ], env={"NFT_COPILOT_ROOT": str(tmp_path)})
    assert result.exit_code == 2
    assert "夜间版" in result.stdout
    assert not (tmp_path / "policies" / "night.nft").exists()
    assert not (tmp_path / "policies" / "night.restricted.nft").exists()


def test_audit_报告并按严重度退出(tmp_path) -> None:
    from nft_copilot.renderer import render_policy
    from nft_copilot.store import save_policy, git_commit

    risky = Policy(summary="全放行", default_input="accept",
                   rules=[Rule(action="accept")])
    save_policy(tmp_path, "risky", render_policy(risky), risky, "全放行")
    git_commit(tmp_path, "feat: 添加 risky 策略")

    with patch("nft_copilot.validator.validate_syntax") as mock_v:
        mock_v.return_value = ValidationResult(ok=True)
        result = runner.invoke(app, ["audit", "--path", str(tmp_path / "policies")])
    assert result.exit_code == 1
    assert "critical" in result.stdout.lower() or "DEFAULT_INPUT_ACCEPT" in result.stdout


def test_explain_输出解释(tmp_path) -> None:
    nft_file = tmp_path / "demo.nft"
    nft_file.write_text("table inet filter { }", "utf-8")
    with patch("nft_copilot.cli.explain_config", return_value="这是解释") as m:
        result = runner.invoke(app, ["explain", str(nft_file)])
    assert result.exit_code == 0
    assert "这是解释" in result.stdout
    m.assert_called_once()
