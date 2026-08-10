#!/usr/bin/env python3
"""Tests for `.claude/hooks/guard_workspace_mutation.py`.

Tracks deagy/cadre#129: a PreToolUse hook that structurally refuses the
destructive `git` invocations that prompt-level policy
(`roster/shared/workspace-isolation.md`, `roster/shared/agent-autonomy.yaml`)
has already failed to prevent three times over (see the hook module's own
docstring for the incidents this responds to).

These tests import the hook module directly and drive its pure functions
with synthetic `PreToolUse` JSON and disposable git repositories -- no
Claude Code runtime is needed to exercise the decision logic.

    python3 -m unittest discover -s plugin/tools -p "test_*.py"
"""

from __future__ import annotations

import json
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
HOOK_DIR = REPO_ROOT / ".claude" / "hooks"
sys.path.insert(0, str(HOOK_DIR))

import guard_workspace_mutation as guard  # noqa: E402


def _git(cwd: Path, *args: str) -> subprocess.CompletedProcess:
    return subprocess.run(
        ["git", *args],
        cwd=cwd,
        capture_output=True,
        text=True,
        check=True,
    )


def _init_repo(path: Path) -> None:
    _git(path, "init", "-q", "-b", "main")
    _git(path, "config", "user.email", "test@example.com")
    _git(path, "config", "user.name", "Test User")


class GuardTestCase(unittest.TestCase):
    def setUp(self) -> None:
        self._tmp = tempfile.mkdtemp(prefix="guard-workspace-mutation-")
        self.addCleanup(shutil.rmtree, self._tmp, ignore_errors=True)
        self.repo = Path(self._tmp) / "repo"
        self.repo.mkdir()
        _init_repo(self.repo)

    def commit_file(self, name: str, content: str, message: str = "add file") -> None:
        (self.repo / name).write_text(content, encoding="utf-8")
        _git(self.repo, "add", name)
        _git(self.repo, "commit", "-q", "-m", message)

    def dirty_file(self, name: str, content: str) -> None:
        (self.repo / name).write_text(content, encoding="utf-8")

    def evaluate(self, command: str):
        return guard.evaluate_command(command, str(self.repo))

    def assert_blocked(self, command: str) -> dict:
        decision = self.evaluate(command)
        self.assertIsNotNone(decision, f"expected {command!r} to be blocked")
        self.assertIn("reason", decision)
        return decision

    def assert_allowed(self, command: str) -> None:
        decision = self.evaluate(command)
        self.assertIsNone(decision, f"expected {command!r} to be allowed, got {decision!r}")


# ---------------------------------------------------------------------------
# reset --hard
# ---------------------------------------------------------------------------


class ResetHardTests(GuardTestCase):
    def test_blocks_hard_reset_with_dirty_tree(self) -> None:
        self.commit_file("a.txt", "one")
        self.dirty_file("a.txt", "two-uncommitted")
        decision = self.assert_blocked("git reset --hard")
        self.assertIn("uncommitted changes", decision["reason"])

    def test_blocks_hard_reset_moving_branch_even_with_clean_tree(self) -> None:
        self.commit_file("a.txt", "one")
        self.commit_file("a.txt", "two", message="second commit")
        # Clean tree, but `git reset --hard HEAD~1` would strand the tip
        # commit -- exactly the #129 incident shape.
        decision = self.assert_blocked("git reset --hard HEAD~1")
        self.assertIn("strand", decision["reason"])

    def test_allows_hard_reset_on_clean_tree_no_ref(self) -> None:
        self.commit_file("a.txt", "one")
        self.assert_allowed("git reset --hard")

    def test_allows_hard_reset_to_current_head_with_clean_tree(self) -> None:
        self.commit_file("a.txt", "one")
        self.assert_allowed("git reset --hard HEAD")

    def test_allows_non_hard_reset_regardless_of_dirty_tree(self) -> None:
        self.commit_file("a.txt", "one")
        self.commit_file("a.txt", "two", message="second commit")
        self.dirty_file("a.txt", "uncommitted-edit")
        self.assert_allowed("git reset HEAD~1")
        self.assert_allowed("git reset --soft HEAD~1")
        self.assert_allowed("git reset --mixed HEAD~1")


# ---------------------------------------------------------------------------
# checkout / restore pulling from another ref
# ---------------------------------------------------------------------------


class CheckoutRestoreTests(GuardTestCase):
    def test_blocks_checkout_ref_dashdash_path_over_dirty_file(self) -> None:
        self.commit_file("a.txt", "one")
        self.commit_file("a.txt", "two", message="second commit")
        self.dirty_file("a.txt", "uncommitted-edit")
        decision = self.assert_blocked("git checkout HEAD~1 -- a.txt")
        self.assertIn("overwrite", decision["reason"])

    def test_blocks_restore_source_over_dirty_file(self) -> None:
        self.commit_file("a.txt", "one")
        self.commit_file("a.txt", "two", message="second commit")
        self.dirty_file("a.txt", "uncommitted-edit")
        self.assert_blocked("git restore --source=HEAD~1 a.txt")
        self.assert_blocked("git restore -s HEAD~1 a.txt")

    def test_allows_checkout_ref_path_when_path_is_clean(self) -> None:
        self.commit_file("a.txt", "one")
        self.commit_file("b.txt", "unrelated")
        self.commit_file("a.txt", "two", message="second commit")
        self.dirty_file("b.txt", "unrelated dirty edit")
        # a.txt itself has no uncommitted changes, only b.txt does.
        self.assert_allowed("git checkout HEAD~1 -- a.txt")

    def test_allows_bare_checkout_dashdash_path_no_ref(self) -> None:
        self.commit_file("a.txt", "one")
        self.dirty_file("a.txt", "uncommitted-edit")
        self.assert_allowed("git checkout -- a.txt")

    def test_allows_bare_checkout_of_file_no_ref_clean_tree(self) -> None:
        self.commit_file("a.txt", "one")
        self.assert_allowed("git checkout a.txt")

    def test_allows_restore_with_no_source(self) -> None:
        self.commit_file("a.txt", "one")
        self.dirty_file("a.txt", "uncommitted-edit")
        self.assert_allowed("git restore a.txt")


# ---------------------------------------------------------------------------
# checkout <branch>
# ---------------------------------------------------------------------------


class CheckoutBranchSwitchTests(GuardTestCase):
    def test_blocks_branch_switch_with_dirty_tree(self) -> None:
        self.commit_file("a.txt", "one")
        _git(self.repo, "branch", "other")
        self.dirty_file("a.txt", "uncommitted-edit")
        decision = self.assert_blocked("git checkout other")
        self.assertIn("uncommitted changes", decision["reason"])

    def test_allows_branch_switch_with_clean_tree(self) -> None:
        self.commit_file("a.txt", "one")
        _git(self.repo, "branch", "other")
        self.assert_allowed("git checkout other")

    def test_allows_checkout_dash_b_new_branch_even_when_dirty(self) -> None:
        self.commit_file("a.txt", "one")
        self.dirty_file("a.txt", "uncommitted-edit")
        self.assert_allowed("git checkout -b feature/new")


# ---------------------------------------------------------------------------
# clean
# ---------------------------------------------------------------------------


class CleanTests(GuardTestCase):
    def test_blocks_force_clean_with_untracked_files_present(self) -> None:
        self.commit_file("a.txt", "one")
        (self.repo / "scratch.tmp").write_text("junk", encoding="utf-8")
        decision = self.assert_blocked("git clean -fd")
        self.assertIn("permanently delete", decision["reason"])

    def test_allows_force_clean_when_nothing_to_remove(self) -> None:
        self.commit_file("a.txt", "one")
        self.assert_allowed("git clean -fd")

    def test_allows_explicit_dry_run(self) -> None:
        self.commit_file("a.txt", "one")
        (self.repo / "scratch.tmp").write_text("junk", encoding="utf-8")
        self.assert_allowed("git clean -n")
        self.assert_allowed("git clean --dry-run -fd")

    def test_allows_clean_without_force_flag(self) -> None:
        self.commit_file("a.txt", "one")
        (self.repo / "scratch.tmp").write_text("junk", encoding="utf-8")
        self.assert_allowed("git clean -d")


# ---------------------------------------------------------------------------
# branch deletion
# ---------------------------------------------------------------------------


class BranchDeleteTests(GuardTestCase):
    def test_blocks_force_delete_dash_capital_d(self) -> None:
        self.commit_file("a.txt", "one")
        _git(self.repo, "checkout", "-b", "throwaway")
        _git(self.repo, "checkout", "main")
        decision = self.assert_blocked("git branch -D throwaway")
        self.assertIn("unmerged-work safety check", decision["reason"])

    def test_blocks_delete_plus_force_long_form(self) -> None:
        self.commit_file("a.txt", "one")
        _git(self.repo, "checkout", "-b", "throwaway")
        _git(self.repo, "checkout", "main")
        self.assert_blocked("git branch --delete --force throwaway")

    def test_allows_plain_safe_delete(self) -> None:
        self.commit_file("a.txt", "one")
        _git(self.repo, "checkout", "-b", "throwaway")
        _git(self.repo, "checkout", "main")
        _git(self.repo, "merge", "throwaway")
        self.assert_allowed("git branch -d throwaway")


# ---------------------------------------------------------------------------
# push
# ---------------------------------------------------------------------------


class PushTests(GuardTestCase):
    def test_blocks_plain_force_push(self) -> None:
        decision = self.assert_blocked("git push --force origin main")
        self.assertIn("force-with-lease", decision["reason"])
        self.assert_blocked("git push -f origin main")

    def test_allows_force_with_lease(self) -> None:
        self.assert_allowed("git push --force-with-lease origin main")

    def test_blocks_remote_branch_delete_flag(self) -> None:
        decision = self.assert_blocked("git push origin --delete feature/x")
        self.assertIn("remote branch", decision["reason"])

    def test_blocks_remote_branch_delete_colon_refspec(self) -> None:
        self.assert_blocked("git push origin :feature/x")

    def test_allows_plain_push(self) -> None:
        self.assert_allowed("git push origin main")


# ---------------------------------------------------------------------------
# Command-line shape / chaining robustness
# ---------------------------------------------------------------------------


class CommandShapeTests(GuardTestCase):
    def test_detects_git_after_chain_operator(self) -> None:
        self.commit_file("a.txt", "one")
        self.dirty_file("a.txt", "uncommitted-edit")
        decision = self.assert_blocked("echo hi && git reset --hard")
        self.assertIn("uncommitted changes", decision["reason"])

    def test_ignores_non_git_commands(self) -> None:
        self.assert_allowed("rm -rf /tmp/whatever")
        self.assert_allowed("echo git reset --hard")  # not an invocation, just text

    def test_ignores_unrecognized_git_subcommand(self) -> None:
        self.assert_allowed("git status")
        self.assert_allowed("git log --oneline")
        self.assert_allowed("git diff")

    def test_handles_global_dash_capital_c_flag(self) -> None:
        self.commit_file("a.txt", "one")
        self.dirty_file("a.txt", "uncommitted-edit")
        decision = self.assert_blocked(f"git -C {self.repo} reset --hard")
        self.assertIn("uncommitted changes", decision["reason"])


# ---------------------------------------------------------------------------
# main(): stdin/stdout glue, including malformed-input handling
# ---------------------------------------------------------------------------


class MainEntrypointTests(GuardTestCase):
    def run_main(self, stdin_text: str, monkeypatch_cwd: str | None = None):
        import io
        from unittest import mock

        stdin = io.StringIO(stdin_text)
        stdout = io.StringIO()
        with mock.patch.object(sys, "stdin", stdin), mock.patch.object(sys, "stdout", stdout):
            exit_code = guard.main()
        return exit_code, stdout.getvalue()

    def test_blocks_and_emits_hook_specific_output(self) -> None:
        self.commit_file("a.txt", "one")
        self.dirty_file("a.txt", "uncommitted-edit")
        payload = json.dumps(
            {
                "hook_event_name": "PreToolUse",
                "tool_name": "Bash",
                "tool_input": {"command": "git reset --hard"},
                "cwd": str(self.repo),
            }
        )
        exit_code, out = self.run_main(payload)
        self.assertEqual(0, exit_code)
        parsed = json.loads(out)
        self.assertEqual(
            "deny", parsed["hookSpecificOutput"]["permissionDecision"]
        )
        self.assertEqual("PreToolUse", parsed["hookSpecificOutput"]["hookEventName"])
        self.assertIn("reset --hard", parsed["hookSpecificOutput"]["permissionDecisionReason"])

    def test_allows_emits_nothing(self) -> None:
        self.commit_file("a.txt", "one")
        payload = json.dumps(
            {
                "hook_event_name": "PreToolUse",
                "tool_name": "Bash",
                "tool_input": {"command": "git status"},
                "cwd": str(self.repo),
            }
        )
        exit_code, out = self.run_main(payload)
        self.assertEqual(0, exit_code)
        self.assertEqual("", out.strip())

    def test_malformed_json_does_not_crash(self) -> None:
        exit_code, out = self.run_main("not valid json {{{")
        self.assertEqual(0, exit_code)
        self.assertEqual("", out.strip())

    def test_empty_stdin_does_not_crash(self) -> None:
        exit_code, out = self.run_main("")
        self.assertEqual(0, exit_code)
        self.assertEqual("", out.strip())

    def test_non_dict_json_does_not_crash(self) -> None:
        exit_code, out = self.run_main(json.dumps(["not", "a", "dict"]))
        self.assertEqual(0, exit_code)
        self.assertEqual("", out.strip())

    def test_wrong_event_name_ignored(self) -> None:
        payload = json.dumps(
            {
                "hook_event_name": "PostToolUse",
                "tool_name": "Bash",
                "tool_input": {"command": "git reset --hard"},
                "cwd": str(self.repo),
            }
        )
        exit_code, out = self.run_main(payload)
        self.assertEqual(0, exit_code)
        self.assertEqual("", out.strip())

    def test_non_bash_tool_ignored(self) -> None:
        payload = json.dumps(
            {
                "hook_event_name": "PreToolUse",
                "tool_name": "Write",
                "tool_input": {"file_path": "/tmp/x", "content": "git reset --hard"},
                "cwd": str(self.repo),
            }
        )
        exit_code, out = self.run_main(payload)
        self.assertEqual(0, exit_code)
        self.assertEqual("", out.strip())

    def test_missing_tool_input_does_not_crash(self) -> None:
        payload = json.dumps({"hook_event_name": "PreToolUse", "tool_name": "Bash"})
        exit_code, out = self.run_main(payload)
        self.assertEqual(0, exit_code)
        self.assertEqual("", out.strip())

    def test_non_string_command_does_not_crash(self) -> None:
        payload = json.dumps(
            {
                "hook_event_name": "PreToolUse",
                "tool_name": "Bash",
                "tool_input": {"command": 12345},
            }
        )
        exit_code, out = self.run_main(payload)
        self.assertEqual(0, exit_code)
        self.assertEqual("", out.strip())

    def test_missing_cwd_falls_back_without_crashing(self) -> None:
        payload = json.dumps(
            {
                "hook_event_name": "PreToolUse",
                "tool_name": "Bash",
                "tool_input": {"command": "git status"},
            }
        )
        exit_code, out = self.run_main(payload)
        self.assertEqual(0, exit_code)
        self.assertEqual("", out.strip())


# ---------------------------------------------------------------------------
# Unparseable command segments are skipped, not treated as destructive
# ---------------------------------------------------------------------------


class MalformedCommandTests(GuardTestCase):
    def test_unbalanced_quotes_do_not_crash_and_do_not_block(self) -> None:
        self.assert_allowed("git reset --hard 'unterminated")

    def test_git_in_unresolvable_directory_fails_open(self) -> None:
        self.assert_allowed("git -C /path/does/not/exist reset --hard")


if __name__ == "__main__":
    unittest.main()
