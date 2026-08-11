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
import shlex
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


def wrap_in_bash_c(script: str) -> str:
    """Wrap `script` in one more layer of `bash -c '<script>'`, using
    `shlex.quote` so nesting composes correctly regardless of depth --
    mirrors the TS test suite's `wrapInBashC` helper in
    `cline-plugins/cline-agents/test/presets.test.mts`.
    """
    return "bash -c " + shlex.quote(script)


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
# worktree remove / prune / move / add (issue #215)
# ---------------------------------------------------------------------------


class WorktreeTests(GuardTestCase):
    """`git worktree` handler.

    `workspace-isolation.md`'s "Never remove or prune a worktree yourself"
    reaches all 159 role wrappers since #211 but had no structural
    enforcement before #215. These cases pin both what the handler blocks
    and, explicitly, what it does not.
    """

    def add_worktree(self, name: str, branch: str | None = None) -> Path:
        path = Path(self._tmp) / name
        args = ["worktree", "add", "-q", str(path)]
        if branch:
            args += ["-b", branch]
        else:
            args.append("--detach")
        _git(self.repo, *args)
        return path

    # -- remove ------------------------------------------------------------

    def test_blocks_worktree_remove(self) -> None:
        self.commit_file("a.txt", "one")
        wt = self.add_worktree("wt1", branch="wt1")
        decision = self.assert_blocked(f"git worktree remove {wt}")
        self.assertIn("deregisters a worktree", decision["reason"])

    def test_blocks_worktree_remove_force(self) -> None:
        self.commit_file("a.txt", "one")
        wt = self.add_worktree("wt1", branch="wt1")
        self.assert_blocked(f"git worktree remove --force {wt}")
        self.assert_blocked(f"git worktree remove -f {wt}")

    def test_blocks_worktree_remove_by_bare_name(self) -> None:
        # Verified against git 2.53.0 that `git worktree remove wt1`
        # (basename, not path) really does remove it -- which is why the
        # handler refuses flat instead of matching the target against
        # `git worktree list`.
        self.commit_file("a.txt", "one")
        self.add_worktree("wt1", branch="wt1")
        self.assert_blocked("git worktree remove wt1")

    def test_blocks_worktree_remove_of_unregistered_path(self) -> None:
        # Costs nothing to block: git itself exits 128 on this input.
        self.commit_file("a.txt", "one")
        self.assert_blocked("git worktree remove /path/that/is/not/a/worktree")

    def test_blocks_worktree_remove_of_a_worktree_the_session_created(self) -> None:
        # The policy is absolute, not scoped to "someone else's" worktree:
        # a worktree that holds work is the deliverable location until a
        # human decides otherwise, so tidying up your own is still blocked.
        self.commit_file("a.txt", "one")
        wt = self.add_worktree("mine", branch="mine")
        decision = self.assert_blocked(f"git worktree remove {wt}")
        self.assertIn("including one you created", decision["reason"])

    # -- move --------------------------------------------------------------

    def test_blocks_worktree_move(self) -> None:
        self.commit_file("a.txt", "one")
        wt = self.add_worktree("wt1", branch="wt1")
        dest = Path(self._tmp) / "wt1-moved"
        decision = self.assert_blocked(f"git worktree move {wt} {dest}")
        self.assertIn("relocates the registered worktree", decision["reason"])

    # -- prune -------------------------------------------------------------

    def test_blocks_prune_when_something_would_be_deregistered(self) -> None:
        self.commit_file("a.txt", "one")
        wt = self.add_worktree("wt1", branch="wt1")
        # Make the worktree unreachable without touching git metadata --
        # this is the "teammate's worktree on a momentarily unavailable
        # path" case the prune policy exists for.
        shutil.move(str(wt), str(Path(self._tmp) / "wt1-relocated"))
        decision = self.assert_blocked("git worktree prune")
        self.assertIn("deregister", decision["reason"])
        self.assertIn("names no target", decision["reason"])

    def test_allows_prune_when_nothing_would_be_deregistered(self) -> None:
        # The rejected stricter policy ("block whenever any worktree this
        # session did not create is registered") would block this. A prune
        # that removes nothing removes nothing; blocking it is pure
        # friction, and friction is what gets a guard disabled.
        self.commit_file("a.txt", "one")
        self.add_worktree("wt1", branch="wt1")
        self.assert_allowed("git worktree prune")

    def test_allows_prune_with_no_worktrees_at_all(self) -> None:
        self.commit_file("a.txt", "one")
        self.assert_allowed("git worktree prune")

    def test_allows_explicit_prune_dry_run(self) -> None:
        self.commit_file("a.txt", "one")
        wt = self.add_worktree("wt1", branch="wt1")
        shutil.move(str(wt), str(Path(self._tmp) / "wt1-relocated"))
        self.assert_allowed("git worktree prune -n")
        self.assert_allowed("git worktree prune --dry-run")
        self.assert_allowed("git worktree prune -n -v")
        self.assert_allowed("git worktree prune -nv")

    def test_prune_dry_run_report_is_read_from_stderr(self) -> None:
        # Regression pin for a behaviour verified against git 2.53.0:
        # `git worktree prune -n` writes its report to STDERR, not stdout
        # (unlike `git clean -n`, whose report the sibling handler reads
        # from stdout). A handler that only inspected stdout would see an
        # empty report and allow every prune.
        self.commit_file("a.txt", "one")
        wt = self.add_worktree("wt1", branch="wt1")
        shutil.move(str(wt), str(Path(self._tmp) / "wt1-relocated"))
        rc, out, err = guard.run_git(["worktree", "prune", "-n", "-v"], str(self.repo))
        self.assertEqual(0, rc)
        self.assertEqual("", out.strip(), "if this now has content, git changed streams")
        self.assertIn("wt1", err)

    def test_allows_prune_when_expire_never_suppresses_it(self) -> None:
        # `--expire` is passed through to the dry run, so a prune the
        # caller has explicitly scoped to remove nothing is not blocked.
        self.commit_file("a.txt", "one")
        wt = self.add_worktree("wt1", branch="wt1")
        shutil.move(str(wt), str(Path(self._tmp) / "wt1-relocated"))
        self.assert_allowed("git worktree prune --expire never")
        self.assert_allowed("git worktree prune --expire=never")

    # -- add ---------------------------------------------------------------

    def test_allows_plain_worktree_add(self) -> None:
        # `create_local_branch_or_worktree: allowed` -- the ordinary,
        # policy-endorsed isolation step must never be blocked.
        self.commit_file("a.txt", "one")
        dest = Path(self._tmp) / "new-wt"
        self.assert_allowed(f"git worktree add {dest}")
        self.assert_allowed(f"git worktree add -b agent/task/role {dest}")
        self.assert_allowed(f"git worktree add --detach {dest} HEAD")

    def test_allows_dash_capital_b_on_a_branch_that_does_not_exist(self) -> None:
        self.commit_file("a.txt", "one")
        dest = Path(self._tmp) / "new-wt"
        self.assert_allowed(f"git worktree add -B brand-new {dest}")

    def test_allows_dash_capital_b_when_branch_already_points_at_start_point(self) -> None:
        self.commit_file("a.txt", "one")
        _git(self.repo, "branch", "same")
        dest = Path(self._tmp) / "new-wt"
        self.assert_allowed(f"git worktree add -B same {dest}")

    def test_blocks_dash_capital_b_that_would_move_an_existing_branch(self) -> None:
        # Verified against git 2.53.0: this really does reset the branch,
        # reported only as "Preparing worktree (resetting branch 'x'; was
        # at <sha>)".
        self.commit_file("a.txt", "one")
        _git(self.repo, "branch", "existing")
        self.commit_file("a.txt", "two", message="second commit")
        dest = Path(self._tmp) / "new-wt"
        decision = self.assert_blocked(f"git worktree add -B existing {dest}")
        self.assertIn("force-resets the existing branch", decision["reason"])

    def test_blocks_attached_short_flag_spelling_dash_bbranch(self) -> None:
        # `-Bexisting` (no space) is accepted by git's parse-options and
        # resets the branch identically; missing it would leave the same
        # destructive operation unguarded behind a one-space difference.
        self.commit_file("a.txt", "one")
        _git(self.repo, "branch", "existing")
        self.commit_file("a.txt", "two", message="second commit")
        dest = Path(self._tmp) / "new-wt"
        self.assert_blocked(f"git worktree add -Bexisting {dest}")

    def test_blocks_dash_capital_b_with_explicit_start_point(self) -> None:
        self.commit_file("a.txt", "one")
        self.commit_file("a.txt", "two", message="second commit")
        _git(self.repo, "branch", "existing")
        dest = Path(self._tmp) / "new-wt"
        # `existing` is at HEAD; resetting it to HEAD~1 moves it.
        self.assert_blocked(f"git worktree add -B existing {dest} HEAD~1")

    # -- verbs with no opinion ---------------------------------------------

    def test_allows_read_only_and_non_removing_verbs(self) -> None:
        self.commit_file("a.txt", "one")
        wt = self.add_worktree("wt1", branch="wt1")
        self.assert_allowed("git worktree list")
        self.assert_allowed("git worktree list --porcelain")
        self.assert_allowed("git worktree")
        self.assert_allowed(f"git worktree lock {wt}")
        self.assert_allowed(f"git worktree unlock {wt}")
        self.assert_allowed("git worktree repair")

    # -- composition with the rest of the guard ----------------------------

    def test_blocks_worktree_remove_through_bash_dash_c_and_chaining(self) -> None:
        self.commit_file("a.txt", "one")
        wt = self.add_worktree("wt1", branch="wt1")
        self.assert_blocked(f"cd /tmp && git worktree remove {wt}")
        self.assert_blocked(f'bash -c "git worktree remove {wt}"')
        self.assert_blocked(f"env git worktree remove {wt}")

    def test_blocks_worktree_remove_with_global_dash_capital_c(self) -> None:
        self.commit_file("a.txt", "one")
        wt = self.add_worktree("wt1", branch="wt1")
        self.assert_blocked(f"git -C {self.repo} worktree remove {wt}")


class WorktreeDocumentedGapTests(GuardTestCase):
    """Spellings this handler deliberately does NOT cover.

    Each asserts the actual (gap) behaviour so closing or widening a gap is
    a reviewed decision rather than a silent regression in either
    direction -- same standard as the alias and recursion-bound gaps
    already pinned in this module.
    """

    def test_rm_of_a_worktree_directory_is_a_documented_gap(self) -> None:
        # `workspace-isolation.md` forbids deleting a worktree directory
        # directly in the same sentence as the git verbs, but this hook
        # only inspects `git` invocations -- `rm` is not a git subcommand.
        self.commit_file("a.txt", "one")
        wt = Path(self._tmp) / "wt1"
        _git(self.repo, "worktree", "add", "-q", str(wt), "-b", "wt1")
        self.assert_allowed(f"rm -rf {wt}")

    def test_git_gc_is_a_documented_gap(self) -> None:
        # `git gc` prunes worktrees as part of its own housekeeping and
        # has no handler, so this reaches the same effect unguarded.
        self.commit_file("a.txt", "one")
        self.assert_allowed("git gc")
        self.assert_allowed("git gc --prune=now")

    def test_aliased_worktree_remove_is_a_documented_gap(self) -> None:
        # `_HANDLERS` matches literal git subcommand names only; an alias
        # (`git wtr` for `worktree remove`) is invisible to it. Same gap
        # already recorded for the other handlers.
        self.commit_file("a.txt", "one")
        _git(self.repo, "config", "alias.wtr", "worktree remove")
        self.assert_allowed("git wtr wt1")

    def test_worktree_remove_nested_beyond_recursion_bound_is_a_documented_gap(self) -> None:
        self.commit_file("a.txt", "one")
        wt = Path(self._tmp) / "wt1"
        _git(self.repo, "worktree", "add", "-q", str(wt), "-b", "wt1")
        nested = wrap_in_bash_c(
            wrap_in_bash_c(wrap_in_bash_c(wrap_in_bash_c(f"git worktree remove {wt}")))
        )
        self.assert_allowed(nested)


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

    def test_unexpected_exception_from_evaluate_command_fails_open(self) -> None:
        # Wave 6 regression (issue #129): every other test_*_does_not_crash
        # case above is satisfied by an earlier `if`-guard returning early
        # before main()'s own try/except around `evaluate_command` is ever
        # reached. This test forces an exception past all of the earlier
        # input-validation guards so the outer `except Exception as exc:`
        # catch-all (main()'s own fail-open safety net) actually fires, and
        # asserts it still exits 0 with no stdout that would deny the
        # command -- i.e. it fails open rather than crashing or denying.
        from unittest import mock

        payload = json.dumps(
            {
                "hook_event_name": "PreToolUse",
                "tool_name": "Bash",
                "tool_input": {"command": "git status"},
                "cwd": str(self.repo),
            }
        )
        with mock.patch.object(
            guard, "evaluate_command", side_effect=RuntimeError("boom")
        ):
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


# ---------------------------------------------------------------------------
# Finding 1 (Wave 3 review, issue #129): `env` wrapper bypass
# ---------------------------------------------------------------------------


class EnvWrapperTests(GuardTestCase):
    def test_blocks_bare_env_wrapping_destructive_git(self) -> None:
        self.commit_file("a.txt", "one")
        self.dirty_file("a.txt", "uncommitted-edit")
        decision = self.assert_blocked("env git reset --hard")
        self.assertIn("uncommitted changes", decision["reason"])

    def test_blocks_env_with_var_assignment_before_command(self) -> None:
        self.commit_file("a.txt", "one")
        self.dirty_file("a.txt", "uncommitted-edit")
        self.assert_blocked("env FOO=bar git reset --hard")

    def test_blocks_env_dash_i_before_command(self) -> None:
        self.commit_file("a.txt", "one")
        self.dirty_file("a.txt", "uncommitted-edit")
        self.assert_blocked("env -i git reset --hard")

    def test_blocks_env_dash_i_with_var_assignment_before_command(self) -> None:
        self.commit_file("a.txt", "one")
        self.dirty_file("a.txt", "uncommitted-edit")
        self.assert_blocked("env -i FOO=bar git reset --hard")

    def test_blocks_env_dash_u_name_before_command(self) -> None:
        self.commit_file("a.txt", "one")
        self.dirty_file("a.txt", "uncommitted-edit")
        self.assert_blocked("env -u FOO git reset --hard")

    def test_allows_env_wrapping_non_destructive_git(self) -> None:
        self.commit_file("a.txt", "one")
        self.assert_allowed("env git status")

    # -- Wave 6 regression: env -C/--chdir/-S also take a value token, same
    # as -u/--unset. Missing them let `strip_leading_wrappers` mistake the
    # flag's value for the start of the real command and stop skipping too
    # early, so `parse_git_invocation` never saw `git` as tokens[0] and the
    # destructive command passed through unblocked. See issue #129.

    def test_blocks_env_dash_capital_c_before_command(self) -> None:
        self.commit_file("a.txt", "one")
        self.dirty_file("a.txt", "uncommitted-edit")
        decision = self.assert_blocked("env -C . git reset --hard")
        self.assertIn("uncommitted changes", decision["reason"])

    def test_blocks_env_dash_dash_chdir_before_command(self) -> None:
        self.commit_file("a.txt", "one")
        self.dirty_file("a.txt", "uncommitted-edit")
        self.assert_blocked("env --chdir . git reset --hard")

    def test_env_dash_capital_s_value_is_skipped_so_next_token_is_reached(self) -> None:
        # `-S`/`--split-string` takes a single following token as its value
        # (the string `env` itself would later shell-split into argv); this
        # guard works on already-tokenized command lines rather than
        # re-implementing that shell-split, so the faithful thing to assert
        # here is that the `-S` value token is correctly skipped and a
        # subsequent, genuinely separate destructive `git` token is still
        # reached and blocked -- not a full simulation of -S's runtime
        # split-and-exec semantics.
        self.commit_file("a.txt", "one")
        self.dirty_file("a.txt", "uncommitted-edit")
        decision = self.assert_blocked("env -S 'echo hi' git reset --hard")
        self.assertIn("uncommitted changes", decision["reason"])


# ---------------------------------------------------------------------------
# Finding 2 (Wave 3 review, issue #129): `bash -c "..."` / `sh -c "..."`
# inline indirection
# ---------------------------------------------------------------------------


class ShellDashCRecursionTests(GuardTestCase):
    def test_blocks_bash_dash_c_destructive_git(self) -> None:
        self.commit_file("a.txt", "one")
        self.dirty_file("a.txt", "uncommitted-edit")
        decision = self.assert_blocked('bash -c "git reset --hard"')
        self.assertIn("uncommitted changes", decision["reason"])

    def test_blocks_sh_dash_c_with_chained_segments(self) -> None:
        self.commit_file("a.txt", "one")
        self.dirty_file("a.txt", "uncommitted-edit")
        decision = self.assert_blocked('sh -c "cd /tmp && git reset --hard"')
        self.assertIn("uncommitted changes", decision["reason"])

    def test_blocks_combined_short_flags_bash_dash_lc(self) -> None:
        self.commit_file("a.txt", "one")
        self.dirty_file("a.txt", "uncommitted-edit")
        self.assert_blocked('bash -lc "git reset --hard"')

    def test_allows_bash_dash_c_non_destructive(self) -> None:
        self.commit_file("a.txt", "one")
        self.assert_allowed('bash -c "git status"')

    def test_nested_shell_dash_c_second_level_is_blocked(self) -> None:
        # A second level of `bash -c` inside the first is well within the
        # recursion bound (_MAX_SHELL_RECURSION_DEPTH == 3), so this is
        # blocked, not a documented gap.
        self.commit_file("a.txt", "one")
        self.dirty_file("a.txt", "uncommitted-edit")
        decision = self.assert_blocked("bash -c \"bash -c 'git reset --hard'\"")
        self.assertIn("uncommitted changes", decision["reason"])

    def test_blocks_shell_dash_c_nested_exactly_at_recursion_bound(self) -> None:
        # Mirrors the Cline guard's "blocks a destructive command nested
        # exactly at the recursion bound (3 levels)" test in
        # cline-plugins/cline-agents/test/presets.test.mts.
        self.commit_file("a.txt", "one")
        self.dirty_file("a.txt", "uncommitted-edit")
        nested = wrap_in_bash_c(wrap_in_bash_c(wrap_in_bash_c("git reset --hard")))
        decision = self.assert_blocked(nested)
        self.assertIn("uncommitted changes", decision["reason"])

    def test_nested_shell_dash_c_beyond_recursion_bound_is_a_documented_gap(self) -> None:
        # This is the known, documented limit of the recursion bound
        # (_MAX_SHELL_RECURSION_DEPTH == 3): a fourth level of `bash -c`
        # inside the first three is NOT recursed into, so this is allowed
        # even though the innermost command is destructive on a dirty tree.
        # Mirrors the Cline guard's "documented known gap: nesting one level
        # deeper than the recursion bound is not covered" test in
        # cline-plugins/cline-agents/test/presets.test.mts. This test
        # asserts the actual (gap) behavior so a future change to the bound
        # is a deliberate, reviewed decision rather than a silent regression
        # in either direction.
        self.commit_file("a.txt", "one")
        self.dirty_file("a.txt", "uncommitted-edit")
        nested = wrap_in_bash_c(wrap_in_bash_c(wrap_in_bash_c(wrap_in_bash_c("git reset --hard"))))
        self.assert_allowed(nested)


# ---------------------------------------------------------------------------
# Opt-out env var (Wave 3 review, issue #129)
# ---------------------------------------------------------------------------


class DisableEnvVarTests(GuardTestCase):
    def run_main_with_env(self, stdin_text: str, env: dict):
        import io
        from unittest import mock

        stdin = io.StringIO(stdin_text)
        stdout = io.StringIO()
        with mock.patch.object(sys, "stdin", stdin), mock.patch.object(
            sys, "stdout", stdout
        ), mock.patch.dict("os.environ", env, clear=False):
            exit_code = guard.main()
        return exit_code, stdout.getvalue()

    def test_disable_env_var_set_to_1_allows_destructive_command(self) -> None:
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
        exit_code, out = self.run_main_with_env(
            payload, {"CADRE_DISABLE_WORKSPACE_MUTATION_GUARD": "1"}
        )
        self.assertEqual(0, exit_code)
        self.assertEqual("", out.strip())

    def test_disable_env_var_set_to_true_case_insensitive(self) -> None:
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
        exit_code, out = self.run_main_with_env(
            payload, {"CADRE_DISABLE_WORKSPACE_MUTATION_GUARD": "TRUE"}
        )
        self.assertEqual(0, exit_code)
        self.assertEqual("", out.strip())

    def test_disable_env_var_unset_still_blocks(self) -> None:
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
        exit_code, out = self.run_main_with_env(payload, {})
        self.assertEqual(0, exit_code)
        parsed = json.loads(out)
        self.assertEqual("deny", parsed["hookSpecificOutput"]["permissionDecision"])

    def test_disable_env_var_set_to_0_still_blocks(self) -> None:
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
        exit_code, out = self.run_main_with_env(
            payload, {"CADRE_DISABLE_WORKSPACE_MUTATION_GUARD": "0"}
        )
        self.assertEqual(0, exit_code)
        parsed = json.loads(out)
        self.assertEqual("deny", parsed["hookSpecificOutput"]["permissionDecision"])


if __name__ == "__main__":
    unittest.main()
