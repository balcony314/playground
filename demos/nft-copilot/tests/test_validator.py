"""语法验证器测试（mock subprocess，不依赖真实 nft 权限）。"""

import subprocess
from types import SimpleNamespace
from unittest.mock import patch

from nft_copilot.validator import validate_syntax


def _fake_run(returncode=0, stderr="", stdout=""):
    completed = subprocess.CompletedProcess(
        args=["nft", "-c", "-f", "x"], returncode=returncode,
        stdout=stdout, stderr=stderr,
    )
    def _run(*args, **kwargs):
        return completed
    return _run


def test_语法正确() -> None:
    # mock which：无 nft 二进制的机器（CI 容器）上不应走 skipped 分支
    with (
        patch("nft_copilot.validator.shutil.which", return_value="/usr/sbin/nft"),
        patch("nft_copilot.validator.subprocess.run", _fake_run(0)),
    ):
        result = validate_syntax("...")
    assert result.ok and not result.skipped


def test_语法错误() -> None:
    with (
        patch("nft_copilot.validator.shutil.which", return_value="/usr/sbin/nft"),
        patch(
            "nft_copilot.validator.subprocess.run",
            _fake_run(1, stderr="syntax error near unexpected token"),
        ),
    ):
        result = validate_syntax("...")
    assert not result.ok and not result.skipped
    assert "syntax error" in result.error


def test_无权限时标记_skipped() -> None:
    with (
        patch("nft_copilot.validator.shutil.which", return_value="/usr/sbin/nft"),
        patch(
            "nft_copilot.validator.subprocess.run",
            _fake_run(1, stderr="netlink: Error: cache initialization failed: Operation not permitted"),
        ),
    ):
        result = validate_syntax("...")
    assert result.skipped


def test_nft_不存在时_skipped() -> None:
    def _raise(*args, **kwargs):
        raise FileNotFoundError("nft")
    with patch("nft_copilot.validator.subprocess.run", _raise):
        result = validate_syntax("...")
    assert result.skipped


def test_which_找不到nft时_skipped() -> None:
    """确定性覆盖 shutil.which 分支（区别于 OSError 分支）。"""
    with patch("nft_copilot.validator.shutil.which", return_value=None):
        result = validate_syntax("...")
    assert result.skipped is True
    assert result.ok is False
    assert "未找到 nft" in result.error
