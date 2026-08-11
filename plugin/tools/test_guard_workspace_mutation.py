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
        # Kept open DELIBERATELY under deagy/cadre#217, which closed the
        # sibling `git gc` path and reasoned that this one should not be:
        # deciding whether an arbitrary `rm` target is a registered
        # worktree, for every `rm` the model runs, is a much broader policy
        # question than workspace isolation, and a guard that tries and
        # half-succeeds is worse than one that declares the boundary. This
        # rule is PROMPT-ONLY for `rm`, and `workspace-isolation.md` says so.
        self.commit_file("a.txt", "one")
        wt = Path(self._tmp) / "wt1"
        _git(self.repo, "worktree", "add", "-q", str(wt), "-b", "wt1")
        self.assert_allowed(f"rm -rf {wt}")

    def test_gc_destructive_object_surface_is_a_documented_gap(self) -> None:
        # The `gc` handler added under #217 is scoped to worktree
        # registrations. Reflog expiry and `git gc --prune=now`'s object
        # pruning are a different problem -- detecting "would this destroy
        # something otherwise recoverable" reliably is materially harder --
        # and remain uncovered, which this pins: with no prunable worktree
        # registration, even the sharpest gc spelling is allowed.
        self.commit_file("a.txt", "one")
        self.assert_allowed("git gc --prune=now")
        self.assert_allowed("git reflog expire --expire=now --all")

    def test_config_file_alias_remains_a_documented_gap(self) -> None:
        # Distinct from the `-c` spelling closed under #218: resolving THIS
        # one means reading and trusting the invoking user's git config,
        # whereas `-c alias.x=...` is already in the tokens the hook holds.
        self.commit_file("a.txt", "one")
        wt = Path(self._tmp) / "wt1"
        _git(self.repo, "worktree", "add", "-q", str(wt), "-b", "wt1")
        _git(self.repo, "config", "alias.wtr", "worktree remove")
        self.assert_allowed(f"git wtr {wt}")

    def test_wrapper_set_remains_non_exhaustive(self) -> None:
        # #219 extended `_WRAPPER_TOKENS` rather than inverting the design
        # to scan every token for a `git` invocation, because inverting
        # reintroduces exactly the false-positive class the heredoc work
        # exists to avoid. That trade means the set can always be one entry
        # short, and this pins the consequence with wrappers deliberately
        # left out.
        self.commit_file("a.txt", "one")
        wt = Path(self._tmp) / "wt1"
        _git(self.repo, "worktree", "add", "-q", str(wt), "-b", "wt1")
        for wrapper in ("firejail", "runuser -u root --", "unbuffer", "doas"):
            self.assert_allowed(f"{wrapper} git worktree remove {wt}")

    def test_worktree_add_force_over_a_registered_path_is_a_documented_gap(self) -> None:
        # Verified against git 2.53.0: plain `add` refuses to reuse the path
        # of a registered-but-missing worktree, `--force` overrides and
        # re-registers it, and `-f -f` does so even when it is locked. Not
        # guarded -- see the module docstring for why the fix would
        # re-introduce the path-matching problem `remove` avoids.
        #
        # NOTE ON WHAT THIS TEST DOES AND DOES NOT SHOW: it calls
        # `evaluate_command` only, and never runs git, so the `shutil.move`
        # below does not affect the assertion -- the guard has no handler
        # branch for `add --force` at all, so it would return None either
        # way. The setup is here to describe the scenario, not to prove it.
        # The empirical claim about git's behaviour rests on the separate
        # probe recorded in the module docstring, not on this test.
        self.commit_file("a.txt", "one")
        wt = Path(self._tmp) / "victim"
        _git(self.repo, "worktree", "add", "-q", str(wt), "-b", "victim")
        shutil.move(str(wt), str(Path(self._tmp) / "victim-elsewhere"))
        self.assert_allowed(f"git worktree add --force {wt} -b intruder")
        self.assert_allowed(f"git worktree add -f -f {wt} -b intruder")

    def test_worktree_remove_nested_beyond_recursion_bound_is_a_documented_gap(self) -> None:
        self.commit_file("a.txt", "one")
        wt = Path(self._tmp) / "wt1"
        _git(self.repo, "worktree", "add", "-q", str(wt), "-b", "wt1")
        nested = wrap_in_bash_c(
            wrap_in_bash_c(wrap_in_bash_c(wrap_in_bash_c(f"git worktree remove {wt}")))
        )
        self.assert_allowed(nested)


# ---------------------------------------------------------------------------
# Cumulative `git -C` (issue #220)
# ---------------------------------------------------------------------------


class CumulativeDashCTests(GuardTestCase):
    """Repeated `-C` is cumulative in git, not last-wins.

    Reproduced before fixing, as #220 asked: `parse_git_invocation` kept the
    last value, so `git -C .worktrees -C ../ worktree prune` resolved to
    `<base>/../` -- a different directory -- where the state probe exited
    non-zero and the handler failed open. Verified against git 2.53.0 from a
    repository root that `-C sub` reports prefix `sub/`, `-C sub -C deeper`
    reports `sub/deeper/`, `-C sub -C ..` reports nothing (back at the
    root), and `-C sub -C /tmp` lands in `/tmp` outright.

    This defeated every handler that decides by inspecting repository state
    rather than by spelling; `worktree remove`/`move` were immune only
    because they refuse on the verb alone, which is an argument for flat
    refusal wherever policy allows it.
    """

    def test_parse_accumulates_in_order(self) -> None:
        self.assertEqual(
            "a/b",
            guard.parse_git_invocation(["git", "-C", "a", "-C", "b", "status"])[2],
        )

    def test_absolute_value_resets_the_accumulation(self) -> None:
        self.assertEqual(
            "/abs",
            guard.parse_git_invocation(["git", "-C", "rel", "-C", "/abs", "status"])[2],
        )
        self.assertEqual(
            "/abs/rel",
            guard.parse_git_invocation(["git", "-C", "/abs", "-C", "rel", "status"])[2],
        )

    def test_empty_value_is_a_no_op_in_either_position(self) -> None:
        # Verified against git 2.53.0: `-C "" -C sub` and `-C sub -C ""`
        # both report prefix `sub/`.
        self.assertEqual(
            "sub", guard.parse_git_invocation(["git", "-C", "", "-C", "sub", "s"])[2]
        )
        self.assertEqual(
            "sub", guard.parse_git_invocation(["git", "-C", "sub", "-C", "", "s"])[2]
        )

    def test_state_probing_handler_reaches_the_right_repository(self) -> None:
        self.commit_file("a.txt", "one")
        wt = self.repo / ".worktrees" / "wt1"
        _git(self.repo, "worktree", "add", "-q", str(wt), "-b", "wt1")
        shutil.move(str(wt), str(Path(self._tmp) / "wt1-relocated"))
        # `.worktrees` then `..` is the repository root again, where the
        # prune is meaningful. Last-wins resolved this to `<repo>/..`.
        self.assert_blocked("git -C .worktrees -C .. worktree prune")

    def test_absolute_reset_pointing_outside_a_repository_fails_open(self) -> None:
        self.commit_file("a.txt", "one")
        self.dirty_file("a.txt", "uncommitted-edit")
        self.assert_allowed(f"git -C {self.repo} -C {self._tmp} reset --hard")


# ---------------------------------------------------------------------------
# `checkout -B` / `switch -C` (issue #221)
# ---------------------------------------------------------------------------


class ForceCreateBranchTests(GuardTestCase):
    """`-B`/`-C` force-create moves an existing branch off its commits.

    Verified against git 2.53.0 for every spelling asserted here: each
    reported only "Switched to and reset branch 'existing'" while moving the
    branch. `check_worktree` has performed this check for `worktree add -B`
    since #215; #221 closed the same hole in `checkout` and added `switch`.
    """

    def setUp(self) -> None:
        super().setUp()
        self.commit_file("a.txt", "one")
        self.commit_file("a.txt", "two", message="second commit")
        _git(self.repo, "update-ref", "refs/heads/existing", "HEAD~1")

    def test_blocks_checkout_dash_capital_b_with_implicit_head_start_point(self) -> None:
        decision = self.assert_blocked("git checkout -B existing")
        self.assertIn("force-resets the existing branch", decision["reason"])

    def test_blocks_attached_and_combined_short_flag_spellings(self) -> None:
        # `-Bexisting` is a one-space difference; `-fB existing` puts `B`
        # last in a combined group so its value is the NEXT token.
        self.assert_blocked("git checkout -Bexisting")
        self.assert_blocked("git checkout -fB existing")

    def test_blocks_checkout_dash_capital_b_with_explicit_start_point(self) -> None:
        self.assert_blocked("git checkout -B existing main")

    def test_allows_checkout_dash_capital_b_on_a_name_that_does_not_exist(self) -> None:
        self.assert_allowed("git checkout -B brand-new")

    def test_allows_checkout_dash_capital_b_when_already_at_the_start_point(self) -> None:
        _git(self.repo, "update-ref", "refs/heads/same", "HEAD")
        self.assert_allowed("git checkout -B same")

    def test_allows_plain_dash_b(self) -> None:
        # Genuinely safe: git refuses `-b` when the branch already exists.
        self.assert_allowed("git checkout -b feature/new")
        self.assert_allowed("git checkout -b existing")

    def test_combined_group_with_the_flag_not_last_takes_its_value_inline(self) -> None:
        # `git checkout -Bf existing` creates a branch named `f` and treats
        # `existing` as the START POINT -- verified against git 2.53.0. So
        # this must NOT read `existing` as the branch being forced.
        self.assert_allowed("git checkout -Bf existing")

    def test_blocks_switch_dash_capital_c_spellings(self) -> None:
        for command in (
            "git switch -C existing",
            "git switch -Cexisting",
            "git switch -fC existing",
            "git switch --force-create existing",
            "git switch --force-create=existing",
        ):
            with self.subTest(command=command):
                self.assert_blocked(command)

    def test_allows_switch_create_detach_and_new_names(self) -> None:
        self.assert_allowed("git switch -c feature/new")
        self.assert_allowed("git switch -C brand-new")
        self.assert_allowed("git switch --detach HEAD")
        self.assert_allowed("git switch --orphan fresh")

    def test_switch_branch_gets_the_same_dirty_tree_check_as_checkout(self) -> None:
        # Leaving this out would make the whole branch-switch guard
        # bypassable by choosing the other spelling of the same operation.
        _git(self.repo, "branch", "other")
        self.dirty_file("a.txt", "uncommitted-edit")
        self.assert_blocked("git switch other")
        self.assert_blocked("git checkout other")  # control

    def test_allows_switch_to_a_branch_with_a_clean_tree(self) -> None:
        _git(self.repo, "branch", "other")
        self.assert_allowed("git switch other")

    def test_global_dash_capital_c_is_not_mistaken_for_switch_force_create(self) -> None:
        # `-C <dir>` is consumed as a GLOBAL before the subcommand is read,
        # so it must never be seen as switch's own `-C`.
        _git(self.repo, "branch", "other")
        self.assert_allowed(f"git -C {self.repo} switch other")
        self.dirty_file("a.txt", "uncommitted-edit")
        self.assert_blocked(f"git -C {self.repo} switch other")


# ---------------------------------------------------------------------------
# `-c alias.<name>=<definition>` expansion (issue #218)
# ---------------------------------------------------------------------------


class CommandLineAliasTests(GuardTestCase):
    """An alias defined by `-c` on the command line is expanded and re-parsed.

    The pre-existing alias gap's justification -- "resolving aliases would
    require reading and trusting the invoking user's git config" -- does not
    apply here: the definition is in the tokens this hook already holds.
    Behaviour verified against git 2.53.0; see `expand_git_alias`.
    """

    def setUp(self) -> None:
        super().setUp()
        self.commit_file("a.txt", "one")
        self.wt = Path(self._tmp) / "wt1"
        _git(self.repo, "worktree", "add", "-q", str(self.wt), "-b", "wt1")

    def test_blocks_aliased_worktree_remove(self) -> None:
        decision = self.assert_blocked(
            f"git -c alias.wtr='worktree remove' wtr {self.wt}"
        )
        self.assertIn("deregisters a worktree", decision["reason"])

    def test_the_same_shape_defeats_every_handler_not_just_worktree(self) -> None:
        self.dirty_file("a.txt", "uncommitted-edit")
        self.assert_blocked("git -c alias.nuke='reset --hard' nuke")
        self.assert_blocked("git -c alias.yolo='push --force' yolo origin main")

    def test_handles_multiple_and_interleaved_dash_c_flags(self) -> None:
        self.dirty_file("a.txt", "uncommitted-edit")
        self.assert_blocked(
            "git -c core.pager=cat -c alias.nuke='reset --hard' -c gc.auto=0 nuke"
        )

    def test_follows_an_alias_that_names_another_alias(self) -> None:
        self.assert_blocked(
            f"git -c alias.a=worktree -c alias.b='a remove' b {self.wt}"
        )

    def test_alias_loop_terminates_and_fails_open(self) -> None:
        # Git reports "alias loop detected"; this must terminate too.
        self.assert_allowed("git -c alias.x=y -c alias.y=x x")

    def test_shell_alias_goes_through_the_bounded_shell_recursion(self) -> None:
        self.assert_blocked(
            f"git -c alias.sh='!git worktree remove' sh {self.wt}"
        )
        self.dirty_file("a.txt", "uncommitted-edit")
        self.assert_blocked("git -c alias.sh='!cd /tmp && git reset --hard' sh")

    def test_alias_defined_but_not_used_is_allowed(self) -> None:
        self.assert_allowed("git -c alias.wtr='worktree remove' status")

    def test_alias_cannot_shadow_a_real_subcommand(self) -> None:
        # Verified against git 2.53.0 that git ignores an alias named after
        # a builtin, so expanding one here would ALLOW a force push.
        self.assert_blocked("git -c alias.push=status push --force origin main")

    def test_dash_c_with_no_equals_sign_is_ignored(self) -> None:
        self.assert_allowed(f"git -c alias.wtr wtr {self.wt}")

    def test_definition_may_carry_global_flags_of_its_own(self) -> None:
        # `git <definition> <args>` is literally what git runs, so a `-C` in
        # the definition redirects the invocation. Evaluated from OUTSIDE
        # the repository so the assertion can only pass if that `-C` was
        # actually folded into the resolved cwd.
        self.dirty_file("a.txt", "uncommitted-edit")
        command = f"git -c alias.n='-C {self.repo} reset --hard' n"
        self.assertIsNone(guard.evaluate_command(command.replace(str(self.repo), "/nope"), self._tmp))
        self.assertIsNotNone(guard.evaluate_command(command, self._tmp))


# ---------------------------------------------------------------------------
# Wrapper coverage (issue #219)
# ---------------------------------------------------------------------------


class WrapperCoverageTests(GuardTestCase):
    """Prefix wrappers and `find -exec`.

    Every layout asserted here was confirmed by EXECUTION to actually run a
    following `git` command on the verification machine (GNU coreutils
    `timeout`/`nice`/`stdbuf`, util-linux `ionice`/`setsid`/`chrt`/
    `taskset`, findutils `xargs`), so the token shapes parsed are the real
    ones rather than guesses from a manual page.
    """

    def setUp(self) -> None:
        super().setUp()
        self.commit_file("a.txt", "one")
        self.wt = self.repo / ".worktrees" / "wt1"
        _git(self.repo, "worktree", "add", "-q", str(self.wt), "-b", "wt1")

    def test_blocks_prefix_wrappers(self) -> None:
        for wrapper in (
            "timeout 10",
            "timeout -s KILL -k 5 10",
            "timeout --signal=KILL 10",
            "nice",
            "nice -n 5",
            "nice -5",
            "ionice -c 3",
            "stdbuf -oL",
            "stdbuf -o L",
            "setsid",
            "chrt -f 10",
            "taskset 0x1",
            "taskset -c 0,1",
            "sudo",
            "sudo -u root",
            "setsid ionice -c 3 timeout 10",
        ):
            with self.subTest(wrapper=wrapper):
                self.assert_blocked(f"{wrapper} git worktree remove {self.wt}")

    def test_blocks_xargs_including_after_a_pipe(self) -> None:
        self.assert_blocked(f"echo {self.wt} | xargs git worktree remove")
        self.assert_blocked(f"echo {self.wt} | xargs -I{{}} git worktree remove {{}}")
        self.assert_blocked(f"echo {self.wt} | xargs -I {{}} git worktree remove {{}}")
        self.assert_blocked(f"echo {self.wt} | xargs -n 1 git worktree remove")

    def test_blocks_find_exec_in_argument_position(self) -> None:
        # Two things had to change together: `find -exec` is not a prefix,
        # and the `\;` terminator was splitting the segment before the
        # scanner learned that an unquoted backslash escapes the next char.
        self.assert_blocked(
            f"find {self.repo}/.worktrees -maxdepth 1 -exec git worktree remove {{}} \\;"
        )
        self.assert_blocked(
            f"find {self.repo}/.worktrees -maxdepth 1 -exec git worktree remove {{}} +"
        )
        self.assert_blocked(
            f"find {self.repo}/.worktrees -execdir git worktree remove {{}} ';'"
        )

    def test_wrappers_do_not_fire_on_a_literal_mention(self) -> None:
        # The prefix-stripping design is kept, rather than inverting to scan
        # all tokens, precisely so a destructive command appearing as DATA
        # stays allowed. Inverting would reintroduce that false-positive
        # class -- the one the heredoc handling exists to prevent.
        self.assert_allowed(f"echo 'timeout 10 git worktree remove {self.wt}'")
        self.assert_allowed(
            f"cat <<'EOF' > note.md\ntimeout 10 git worktree remove {self.wt}\nEOF"
        )
        self.assert_allowed(f"grep -r 'xargs git worktree remove' {self.repo}")

    def test_wrapped_non_destructive_git_is_still_allowed(self) -> None:
        self.assert_allowed("timeout 10 git status")
        self.assert_allowed("nice -n 5 git log --oneline")
        self.assert_allowed(f"find {self.repo} -exec git status \\;")


# ---------------------------------------------------------------------------
# `git gc` (issue #217)
# ---------------------------------------------------------------------------


class GcTests(GuardTestCase):
    """`git gc`'s worktree-pruning path.

    The empirical picture, verified against git 2.53.0, differs from the
    issue's framing and is what the handler is built on:

      * plain `git gc` did NOT prune a worktree whose directory had just
        been moved away, and neither did `git gc --prune=now`/`--prune=all`;
      * `git -c gc.worktreePruneExpire=now gc` DID deregister it;
      * once the worktree's administrative files were aged past the default,
        plain `git gc` deregistered it, and `git worktree prune -n -v
        --expire 3.months.ago` reported exactly that registration first.

    So `gc`'s own `--prune=<date>` governs loose-object pruning and does not
    reach worktree registrations; `gc.worktreePruneExpire` does.
    """

    def _prunable_worktree(self, aged: bool) -> None:
        self.commit_file("a.txt", "one")
        wt = self.repo / ".worktrees" / "wt1"
        _git(self.repo, "worktree", "add", "-q", str(wt), "-b", "wt1")
        shutil.move(str(wt), str(Path(self._tmp) / "wt1-relocated"))
        if aged:
            import os
            import time

            admin = self.repo / ".git" / "worktrees" / "wt1"
            old = time.time() - 365 * 24 * 3600
            for entry in sorted(admin.rglob("*"), reverse=True):
                os.utime(entry, (old, old))
            os.utime(admin, (old, old))

    def test_blocks_gc_that_would_deregister_a_worktree(self) -> None:
        self._prunable_worktree(aged=True)
        decision = self.assert_blocked("git gc")
        self.assertIn("prunes worktrees as part of its own housekeeping", decision["reason"])

    def test_allows_gc_when_the_registration_is_not_yet_expired(self) -> None:
        # Matches what real gc does. Blocking here would be friction with no
        # safety, and friction is what gets a guard disabled.
        self._prunable_worktree(aged=False)
        self.assert_allowed("git gc")
        self.assert_allowed("git gc --prune=now")
        self.assert_allowed("git gc --aggressive")

    def test_blocks_gc_with_a_command_line_expire_override(self) -> None:
        # Only visible because `parse_git_invocation` now records `-c`
        # pairs -- the same change that closed the alias gap.
        self._prunable_worktree(aged=False)
        self.assert_blocked("git -c gc.worktreePruneExpire=now gc")

    def test_honours_a_repository_config_expire(self) -> None:
        self._prunable_worktree(aged=False)
        _git(self.repo, "config", "gc.worktreePruneExpire", "now")
        self.assert_blocked("git gc")

    def test_allows_gc_in_a_repository_with_nothing_prunable(self) -> None:
        self.commit_file("a.txt", "one")
        self.assert_allowed("git gc")
        self.assert_allowed("git gc --auto")

    def test_gc_outside_a_repository_fails_open(self) -> None:
        self.assert_allowed("git -C /path/does/not/exist gc")


# ---------------------------------------------------------------------------
# Command-line shape / chaining robustness
# ---------------------------------------------------------------------------


class NewlineSeparatorTests(GuardTestCase):
    """Newline is a command separator (issue #215 review, finding F1).

    Before this, `split_top_level` split on `&&`/`||`/`;`/`|` but not on
    newlines, and `shlex.split` treats a newline as ordinary whitespace --
    so a two-line command collapsed into one token list whose `tokens[0]`
    was the first line's program, `parse_git_invocation` returned None,
    and EVERY handler was bypassed. No adversarial intent required:
    multi-line Bash tool calls are routine.
    """

    def test_newline_separated_destructive_command_is_blocked(self) -> None:
        self.commit_file("a.txt", "one")
        self.dirty_file("a.txt", "uncommitted-edit")
        # The `&&` spelling is the control: both must be blocked.
        self.assert_blocked("echo hi && git reset --hard")
        self.assert_blocked("echo hi\ngit reset --hard")

    def test_newline_bypass_is_closed_for_every_handler(self) -> None:
        # The bypass was never worktree-specific, so neither is the fix.
        self.commit_file("a.txt", "one")
        _git(self.repo, "branch", "throwaway")
        (self.repo / "scratch.tmp").write_text("junk", encoding="utf-8")
        self.dirty_file("a.txt", "uncommitted-edit")
        wt = Path(self._tmp) / "wt1"
        _git(self.repo, "worktree", "add", "-q", str(wt), "-b", "wt1")
        for command in (
            "cd /tmp\ngit reset --hard",
            "cd /tmp\ngit clean -fd",
            "cd /tmp\ngit checkout HEAD~1 -- a.txt",
            "cd /tmp\ngit restore --source=HEAD~1 a.txt",
            "cd /tmp\ngit branch -D throwaway",
            "cd /tmp\ngit push --force origin main",
            f"cd /tmp\ngit worktree remove {wt}",
        ):
            with self.subTest(command=command):
                self.assert_blocked(command)

    def test_windows_style_crlf_line_endings(self) -> None:
        self.commit_file("a.txt", "one")
        self.dirty_file("a.txt", "uncommitted-edit")
        self.assert_blocked("echo hi\r\ngit reset --hard")

    def test_blank_lines_and_leading_indentation_do_not_hide_the_command(self) -> None:
        self.commit_file("a.txt", "one")
        self.dirty_file("a.txt", "uncommitted-edit")
        self.assert_blocked("echo one\n\n    git reset --hard\n")

    def test_newline_inside_quotes_is_not_a_separator(self) -> None:
        # A quoted multi-line string stays one segment, so the quoted text
        # is an argument to `echo`, not a command.
        self.commit_file("a.txt", "one")
        self.dirty_file("a.txt", "uncommitted-edit")
        self.assert_allowed("echo 'first line\ngit reset --hard'")

    def test_command_after_a_heredoc_is_blocked(self) -> None:
        self.commit_file("a.txt", "one")
        self.dirty_file("a.txt", "uncommitted-edit")
        wt = Path(self._tmp) / "wt1"
        _git(self.repo, "worktree", "add", "-q", str(wt), "-b", "wt1")
        self.assert_blocked(
            f"cat <<'EOF' > note.md\nsome text\nEOF\ngit worktree remove {wt}"
        )
        self.assert_blocked("cat <<EOF > note.md\nsome text\nEOF\ngit reset --hard")

    def test_heredoc_body_is_not_treated_as_a_command(self) -> None:
        # The false positive that newline-splitting would otherwise
        # introduce: writing documentation that quotes a destructive
        # command is routine, especially in this repository. The body is
        # text being written to a file, not an invocation.
        self.commit_file("a.txt", "one")
        self.dirty_file("a.txt", "uncommitted-edit")
        self.assert_allowed("cat <<'EOF' > note.md\ngit reset --hard\nEOF")
        self.assert_allowed("cat <<'EOF' > note.md\ngit clean -fd\ngit push --force\nEOF")

    def test_heredoc_dash_form_allows_tab_indented_terminator(self) -> None:
        # The two spellings must DIFFER, or the `<<-` branch is dead code.
        # An earlier version of this test asserted only the `<<-` case and
        # passed identically with the non-dash spelling, because segments
        # were stripped before terminator matching -- a test that could
        # not fail for its stated reason. The pair below discriminates.
        self.commit_file("a.txt", "one")
        self.dirty_file("a.txt", "uncommitted-edit")
        # `<<-` accepts the tab-indented terminator, so the heredoc ends
        # and the trailing command is a real command.
        self.assert_blocked("cat <<-'EOF' > note.md\ntext\n\tEOF\ngit reset --hard")
        # Plain `<<` does NOT: bash requires the terminator line to be
        # exactly the delimiter, so this heredoc is unterminated and
        # swallows the trailing command, exactly as the shell would.
        self.assert_allowed("cat <<'EOF' > note.md\ntext\n\tEOF\ngit reset --hard")
        # Only TABS are stripped by `<<-`, never spaces.
        self.assert_allowed("cat <<-'EOF' > note.md\ntext\n    EOF\ngit reset --hard")

    def test_terminator_must_be_exactly_the_delimiter(self) -> None:
        self.commit_file("a.txt", "one")
        self.dirty_file("a.txt", "uncommitted-edit")
        self.assert_blocked("cat <<'EOF' > note.md\ntext\nEOF\ngit reset --hard")
        # A near-miss terminator leaves the heredoc open (fails open).
        self.assert_allowed("cat <<'EOF' > note.md\ntext\nEOFX\ngit reset --hard")

    def test_here_string_is_not_mistaken_for_a_heredoc(self) -> None:
        # `<<<` is a here-STRING: no body, no terminator line. Treating it
        # as a heredoc would swallow the rest of the command.
        self.commit_file("a.txt", "one")
        self.dirty_file("a.txt", "uncommitted-edit")
        self.assert_blocked("cat <<<somestring\ngit reset --hard")

    def test_split_top_level_splits_on_newline(self) -> None:
        self.assertEqual(
            ["echo one", "echo two", "git status"],
            guard.split_top_level("echo one\necho two\ngit status"),
        )


class HeredocContextTests(GuardTestCase):
    """Regressions F7/F8 (issue #215 re-review).

    The first newline-splitting fix searched finished segment text for a
    heredoc opener, by which point two facts the scanner knew had been
    discarded: which separator produced each break, and whether the `<<`
    was inside quotes. Both are now recorded during the scan.
    """

    def test_command_chained_onto_the_heredoc_opener_line_is_still_checked(self) -> None:
        # F7. `cat > f <<EOF && git worktree remove <path>` runs that git
        # command before a single body line is read. Consuming forward to
        # the delimiter swallowed it.
        self.commit_file("a.txt", "one")
        wt = Path(self._tmp) / "wt1"
        _git(self.repo, "worktree", "add", "-q", str(wt), "-b", "wt1")
        self.assert_blocked(f"cat > note.md <<EOF && git worktree remove {wt}")
        self.assert_blocked(f"cat > note.md <<EOF; git worktree remove {wt}")
        self.assert_blocked(f"cat > note.md <<EOF | git worktree remove {wt}")

    def test_heredoc_body_after_a_chained_opener_is_still_skipped(self) -> None:
        # The body still starts on the NEXT line, so the false positive
        # the heredoc handling exists to prevent stays prevented even when
        # the opener's line carries a second command.
        self.commit_file("a.txt", "one")
        self.dirty_file("a.txt", "uncommitted-edit")
        self.assert_allowed("cat > note.md <<EOF && echo started\ngit reset --hard\nEOF")

    def test_quoted_mention_of_a_heredoc_is_not_a_redirection(self) -> None:
        # F8. A `<<EOF` inside quotes is text, not a redirection, so it
        # must not start swallowing subsequent commands.
        self.commit_file("a.txt", "one")
        wt = Path(self._tmp) / "wt1"
        _git(self.repo, "worktree", "add", "-q", str(wt), "-b", "wt1")
        self.assert_blocked(f'echo "see <<EOF for details"; git worktree remove {wt}')
        self.assert_blocked(f"echo 'see <<EOF'; git worktree remove {wt}")
        self.dirty_file("a.txt", "uncommitted-edit")
        self.assert_blocked('echo "docs mention <<EOF"\ngit reset --hard')

    def test_left_shift_in_arithmetic_expansion_is_not_a_heredoc(self) -> None:
        self.commit_file("a.txt", "one")
        self.dirty_file("a.txt", "uncommitted-edit")
        self.assert_blocked("echo $(( 1 << 2 ))\ngit reset --hard")
        self.assert_blocked("echo $(( x << shift ))\ngit reset --hard")

    def test_fd_prefixed_heredoc_is_still_recognized(self) -> None:
        self.commit_file("a.txt", "one")
        self.dirty_file("a.txt", "uncommitted-edit")
        self.assert_allowed("cat 2<<'EOF'\ngit reset --hard\nEOF")


class LineContinuationTests(GuardTestCase):
    """Regression F9 (issue #215 re-review).

    Backslash-newline is how long commands are normally written. Once
    newline became a separator, `git push \\` / `origin main --force`
    split into two segments -- neither of which is a destructive git
    invocation -- and a force push walked through a guard that had caught
    the single-line spelling since #129.
    """

    def test_force_push_split_across_lines_is_blocked(self) -> None:
        self.assert_blocked("git push --force origin main")  # control
        self.assert_blocked("git push \\\n  origin main --force")
        self.assert_blocked("git push \\\n  --force \\\n  origin main")

    def test_continuation_inside_other_handlers(self) -> None:
        self.commit_file("a.txt", "one")
        self.dirty_file("a.txt", "uncommitted-edit")
        wt = Path(self._tmp) / "wt1"
        _git(self.repo, "worktree", "add", "-q", str(wt), "-b", "wt1")
        self.assert_blocked("git reset \\\n  --hard")
        self.assert_blocked(f"git worktree \\\n  remove {wt}")

    def test_crlf_continuation(self) -> None:
        self.assert_blocked("git push \\\r\n  origin main --force")

    def test_continuation_inside_single_quotes_stays_literal(self) -> None:
        # Inside single quotes a backslash-newline is literal text, not a
        # continuation, so this must not be joined into a command.
        self.commit_file("a.txt", "one")
        self.dirty_file("a.txt", "uncommitted-edit")
        self.assert_allowed("echo 'a\\\nb'")
        self.assert_allowed("echo 'git reset \\\n--hard'")


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
