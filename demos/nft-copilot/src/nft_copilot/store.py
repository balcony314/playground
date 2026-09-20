"""策略存储：写盘 + Git 即代码。

每个策略由一组文件构成（policies/<name>.*），
sidecar JSON 保存 IR 与元数据，供 audit / 回溯使用。
"""

import json
import re
import subprocess
from datetime import datetime, timezone
from pathlib import Path

from nft_copilot.ir import Policy
from nft_copilot.renderer import PolicyBundle

_NAME_RE = re.compile(r"^[a-z0-9][a-z0-9-]*$")

# 裁决 5（binding）：git 因身份未配置而失败时，用仓库级 -c 参数
# 提供匿名身份重试一次，绝不写入/修改全局 git 配置。
# git 报错文本随 locale 变化（英文 "tell me who you are" / 中文
# 「请告诉我您是谁」「身份未知」），故同时匹配中英文措辞。
_IDENTITY_ERROR_RE = re.compile(
    r"tell\s+me\s+who\s+you\s*are|identity\s+unknown|请告诉我您是谁|身份未知",
    re.IGNORECASE,
)
_FALLBACK_IDENTITY = ["-c", "user.name=nft-copilot", "-c", "user.email=nft-copilot@localhost"]


def _git(root: Path, *args: str) -> subprocess.CompletedProcess:
    return subprocess.run(
        ["git", "-C", str(root), *args],
        capture_output=True, text=True, timeout=60,
    )


def save_policy(
    root: Path,
    name: str,
    bundle: PolicyBundle,
    policy: Policy,
    source_text: str,
) -> list[Path]:
    """把策略产物写入 <root>/policies/，返回写入路径。"""
    if not _NAME_RE.match(name):
        raise ValueError(f"策略名只允许小写字母/数字/连字符: {name!r}")
    policies_dir = root / "policies"
    policies_dir.mkdir(parents=True, exist_ok=True)

    written: list[Path] = [(policies_dir / f"{name}.nft")]
    (policies_dir / f"{name}.nft").write_text(bundle.base_nft, "utf-8")
    if bundle.restricted_nft is not None:
        path = policies_dir / f"{name}.restricted.nft"
        path.write_text(bundle.restricted_nft, "utf-8")
        written.append(path)
    if bundle.timers is not None:
        path = policies_dir / f"{name}.timers"
        path.write_text(bundle.timers, "utf-8")
        written.append(path)

    sidecar = {
        "ir": policy.model_dump(),
        "source": source_text,
        "generated_at": datetime.now(timezone.utc).isoformat(timespec="seconds"),
        "version": 1,
    }
    json_path = policies_dir / f"{name}.json"
    json_path.write_text(
        json.dumps(sidecar, ensure_ascii=False, indent=2) + "\n", "utf-8"
    )
    written.append(json_path)
    return written


def load_policy_ir(path: Path) -> Policy:
    """从 sidecar JSON 还原 Policy。"""
    data = json.loads(path.read_text("utf-8"))
    return Policy.model_validate(data["ir"])


def git_commit(root: Path, message: str) -> str | None:
    """提交 policies/ 变更；无 .git 先 init；无变更返回 None。"""
    if not (root / ".git").exists():
        _git(root, "init")
    _git(root, "add", "policies")
    status = _git(root, "status", "--porcelain", "--", "policies")
    if not status.stdout.strip():
        return None
    commit = _git(root, "commit", "-m", message)
    if commit.returncode != 0:
        # 裁决 5：身份未配置 → 注入仓库级匿名身份重试一次。
        if _IDENTITY_ERROR_RE.search(commit.stderr):
            commit = _git(root, *_FALLBACK_IDENTITY, "commit", "-m", message)
        if commit.returncode != 0:
            raise RuntimeError(f"git commit 失败: {commit.stderr.strip()}")
    sha = _git(root, "rev-parse", "--short", "HEAD")
    return sha.stdout.strip() or None
