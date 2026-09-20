"""nft -c 语法验证（dry-run）。

只做语法/语义检查，绝不加载规则（-c 模式不应用 ruleset）。
"""

import shutil
import subprocess
import tempfile
from dataclasses import dataclass
from pathlib import Path


@dataclass
class ValidationResult:
    """语法验证结果。skipped=True 表示环境不允许验证（非语法问题）。"""

    ok: bool
    skipped: bool = False
    error: str | None = None


def validate_syntax(nft_text: str, nft_bin: str = "nft") -> ValidationResult:
    """对配置文本执行 nft -c -f dry-run。"""
    if shutil.which(nft_bin) is None:
        return ValidationResult(
            ok=False, skipped=True,
            error=f"未找到 {nft_bin}，跳过语法验证",
        )
    with tempfile.NamedTemporaryFile(
        mode="w", suffix=".nft", delete=False
    ) as tmp:
        tmp.write(nft_text)
        tmp_path = Path(tmp.name)
    try:
        proc = subprocess.run(
            [nft_bin, "-c", "-f", str(tmp_path)],
            capture_output=True, text=True, timeout=30,
        )
    except (subprocess.TimeoutExpired, OSError) as exc:
        return ValidationResult(ok=False, skipped=True, error=str(exc))
    finally:
        tmp_path.unlink(missing_ok=True)

    if proc.returncode == 0:
        return ValidationResult(ok=True)
    stderr = (proc.stderr or proc.stdout).strip()
    if "Operation not permitted" in stderr:
        return ValidationResult(
            ok=False, skipped=True,
            error="无 CAP_NET_ADMIN 权限，无法执行 nft -c（可用 sudo 重试）",
        )
    return ValidationResult(ok=False, error=stderr or "未知语法错误")
