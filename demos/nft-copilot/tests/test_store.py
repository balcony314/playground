"""存储层测试：tmp_path + 真实 git。"""

import json

import pytest

from nft_copilot.ir import Policy, Rule, TimeWindow
from nft_copilot.renderer import render_policy
from nft_copilot.store import git_commit, load_policy_ir, save_policy


def _bundle():
    policy = Policy(
        summary="示例",
        rules=[
            Rule(source="10.0.0.0/24", port=443, protocol="tcp"),
            Rule(direction="output", action="drop",
                 time_window=TimeWindow(start="22:00", end="06:00")),
        ],
    )
    return policy, render_policy(policy)


def test_保存并加载_roundtrip(tmp_path) -> None:
    policy, bundle = _bundle()
    paths = save_policy(tmp_path, "demo", bundle, policy, "只允许内网访问 443")
    names = {p.name for p in paths}
    assert names == {"demo.nft", "demo.restricted.nft", "demo.timers", "demo.json"}
    restored = load_policy_ir(tmp_path / "policies" / "demo.json")
    assert restored == policy

    meta = json.loads((tmp_path / "policies" / "demo.json").read_text("utf-8"))
    assert meta["source"] == "只允许内网访问 443"
    assert "generated_at" in meta


def test_无时间规则_不写_timer_文件(tmp_path) -> None:
    policy = Policy(summary="x", rules=[Rule(port=443, protocol="tcp")])
    bundle = render_policy(policy)
    paths = save_policy(tmp_path, "simple", bundle, policy, "描述")
    names = {p.name for p in paths}
    assert "simple.timers" not in names
    assert "simple.restricted.nft" not in names


def test_非法名称被拒绝(tmp_path) -> None:
    policy, bundle = _bundle()
    with pytest.raises(ValueError):
        save_policy(tmp_path, "../evil", bundle, policy, "x")
    with pytest.raises(ValueError):
        save_policy(tmp_path, "My Policy", bundle, policy, "x")


def test_git_提交并返回_hash(tmp_path) -> None:
    policy, bundle = _bundle()
    save_policy(tmp_path, "demo", bundle, policy, "描述")
    sha = git_commit(tmp_path, "feat: 添加 demo 策略")
    assert sha is not None and len(sha) >= 7
    # 再次提交无变更 → None
    assert git_commit(tmp_path, "chore: 空") is None
