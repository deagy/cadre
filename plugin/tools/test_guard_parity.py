#!/usr/bin/env python3
"""Parity between the two workspace-mutation guards (deagy/cadre#222).

`.claude/hooks/guard_workspace_mutation.py` and
`cline-plugins/cline-agents/index.ts` implement the same guard for two
runners. Until now the only thing asserting they stayed in step was a code
comment stating that #215 landed `check_worktree` in both files in one
change -- prose about intent, not a check. That is precisely the failure
`roster/shared/operating-principles.md` names: *"When a claim about how
something works can be checked against the thing itself ... check it there
before repeating it."*

Two independent checks live here, because they fail in different ways:

  1. STRUCTURAL (`HandlerTableParityTests`). Parses both files and asserts
     the handler key sets, the wrapper-token set, and the global-flag set
     are equal. Cheap, and catches the stated failure -- a handler added to
     one file and not the other, which would silently cost Cline users
     enforcement with no signal.

  2. BEHAVIOURAL (`SharedFixtureParityTests`). Runs every case in
     `guard_parity_fixture.json` through BOTH implementations against the
     same prepared repository state and asserts the decisions agree with
     each other AND with the fixture's declared expectation. This is what
     catches the divergences #222 lists that structure cannot see: a
     `split("=", 2)` that truncates in JS where Python's `split("=", 1)`
     keeps the remainder, a `??` where the other file means `or`, a missing
     bounds check. Each of those left both files with identical handler
     tables and different meanings.

Structure alone would not have caught any of the three. Behaviour alone
would not catch a handler that exists in both files but is never reached.
Hence both.

The TypeScript half runs through `guard_parity_runner.mjs`, which needs
node plus a TypeScript transform. When either is missing the behavioural
tests SKIP rather than fail -- an unavailable tool must not read as a
red build -- but `test_node_side_is_actually_exercised_when_available`
makes that skip visible instead of silent.

    python3 -m unittest discover -s plugin/tools -p "test_guard_parity.py"
"""

from __future__ import annotations

import json
import os
import re
import shlex
import shutil
import subprocess
import sys
import tempfile
import time
import unittest
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
HOOK_PATH = REPO_ROOT / ".claude" / "hooks" / "guard_workspace_mutation.py"
CLINE_PATH = REPO_ROOT / "cline-plugins" / "cline-agents" / "index.ts"
FIXTURE_PATH = Path(__file__).resolve().parent / "guard_parity_fixture.json"
RUNNER_PATH = Path(__file__).resolve().parent / "guard_parity_runner.mjs"

sys.path.insert(0, str(HOOK_PATH.parent))

import guard_workspace_mutation as guard  # noqa: E402

# `guard_parity_runner.mjs` exits with this when node cannot transpile the
# TypeScript region on this machine. Kept in sync with the runner.
EXIT_UNSUPPORTED = 3


# ---------------------------------------------------------------------------
# Structural parity
# ---------------------------------------------------------------------------


def _typescript_object_keys(source: str, declaration: str) -> set[str]:
    """Top-level keys of a `const <declaration> ... = { ... }` object literal.

    Deliberately a small brace-matched scan rather than a regex over the
    whole file: the values are function references and arrow functions, and
    a regex that tried to skip those would be the kind of thing that quietly
    stops matching and then asserts nothing.
    """
    match = re.search(rf"const {re.escape(declaration)}\b[^=]*=\s*\{{", source)
    if match is None:
        raise AssertionError(f"{CLINE_PATH}: no `const {declaration} = {{` declaration found")
    start = match.end() - 1
    depth = 0
    for index in range(start, len(source)):
        if source[index] == "{":
            depth += 1
        elif source[index] == "}":
            depth -= 1
            if depth == 0:
                body = source[start + 1 : index]
                break
    else:
        raise AssertionError(f"{CLINE_PATH}: unbalanced braces in {declaration}")

    keys: set[str] = set()
    depth = 0
    for line in body.splitlines():
        stripped = line.strip()
        if depth == 0:
            key = re.match(r'^["\']?([A-Za-z_][A-Za-z0-9_]*)["\']?\s*:', stripped)
            if key:
                keys.add(key.group(1))
        depth += line.count("{") + line.count("(") + line.count("[")
        depth -= line.count("}") + line.count(")") + line.count("]")
    return keys


def _typescript_string_list(source: str, declaration: str) -> set[str]:
    """String literals in a `const <declaration> = new Set([...])` or
    `= [...]` initializer."""
    match = re.search(
        rf"const {re.escape(declaration)}\b[^=]*=\s*(?:new Set\()?\[(.*?)\]", source, re.S
    )
    if match is None:
        raise AssertionError(f"{CLINE_PATH}: no `const {declaration} = [...]` declaration found")
    return set(re.findall(r'"([^"]*)"', match.group(1)))


class HandlerTableParityTests(unittest.TestCase):
    def setUp(self) -> None:
        self.typescript = CLINE_PATH.read_text(encoding="utf-8")

    def test_handler_key_sets_are_equal(self) -> None:
        self.assertEqual(
            set(guard._HANDLERS),
            _typescript_object_keys(self.typescript, "GIT_GUARD_HANDLERS"),
            "the Python hook and the Cline guard dispatch on different git "
            "subcommands; add the handler to both files in the same change",
        )

    def test_wrapper_token_sets_are_equal(self) -> None:
        self.assertEqual(
            guard._WRAPPER_TOKENS,
            _typescript_object_keys(self.typescript, "WRAPPER_FLAGS_WITH_VALUE"),
            "one guard strips a leading wrapper the other does not, so the "
            "same wrapped command is blocked on one runner and allowed on the other",
        )

    def test_global_flag_sets_are_equal(self) -> None:
        self.assertEqual(
            guard._GIT_GLOBAL_FLAGS_WITH_VALUE,
            _typescript_string_list(self.typescript, "GIT_GLOBAL_FLAGS_WITH_VALUE"),
        )

    def test_recursion_bounds_match(self) -> None:
        for python_value, declaration in (
            (guard._MAX_SHELL_RECURSION_DEPTH, "MAX_SHELL_C_RECURSION_DEPTH"),
            (guard._MAX_ALIAS_EXPANSION_DEPTH, "MAX_ALIAS_EXPANSION_DEPTH"),
        ):
            with self.subTest(declaration=declaration):
                match = re.search(rf"const {declaration} = (\d+);", self.typescript)
                self.assertIsNotNone(match, f"{CLINE_PATH}: no {declaration} constant")
                self.assertEqual(python_value, int(match.group(1)))

    def test_guard_region_markers_are_present_and_ordered(self) -> None:
        # The behavioural half slices this region out; without the markers it
        # can check nothing, and a silently-skipping parity check is worse
        # than none.
        begin = self.typescript.find("// cadre:guard-region:begin")
        end = self.typescript.find("// cadre:guard-region:end")
        self.assertNotEqual(-1, begin, "guard region begin marker missing")
        self.assertNotEqual(-1, end, "guard region end marker missing")
        self.assertLess(begin, end)

    def test_python_handlers_all_accept_the_config_argument(self) -> None:
        # `evaluate_command` passes three arguments to every handler; a
        # handler ported without the third would raise, and this module's
        # catch-all would turn that into a silent fail-open.
        for name, handler in guard._HANDLERS.items():
            with self.subTest(subcommand=name):
                self.assertIsNone(handler([], str(REPO_ROOT), {}))


# ---------------------------------------------------------------------------
# Behavioural parity: the shared fixture
# ---------------------------------------------------------------------------


def _git(cwd: Path, *args: str) -> None:
    subprocess.run(["git", *args], cwd=cwd, capture_output=True, text=True, check=True)


class FixtureWorld:
    """One disposable repository, built from a fixture case's `setup` steps."""

    def __init__(self, root: Path) -> None:
        self.root = root
        self.repo = root / "repo"
        self.repo.mkdir(parents=True)
        self.worktrees: dict[str, Path] = {}
        _git(self.repo, "init", "-q", "-b", "main")
        _git(self.repo, "config", "user.email", "test@example.com")
        _git(self.repo, "config", "user.name", "Test User")

    def apply(self, step: dict) -> None:
        op = step["op"]
        if op == "commit":
            (self.repo / step["path"]).write_text(step["content"], encoding="utf-8")
            _git(self.repo, "add", step["path"])
            _git(self.repo, "commit", "-q", "-m", f"write {step['path']}")
        elif op == "dirty":
            (self.repo / step["path"]).write_text(step["content"], encoding="utf-8")
        elif op == "untracked":
            (self.repo / step["path"]).write_text(step["content"], encoding="utf-8")
        elif op == "mkdir":
            (self.repo / step["path"]).mkdir(parents=True, exist_ok=True)
        elif op == "branch":
            _git(self.repo, "branch", step["name"])
        elif op == "branch-at":
            # `update-ref`, not `branch -f`: this suite runs under the very
            # guard it tests when a developer drives it by hand.
            _git(self.repo, "update-ref", f"refs/heads/{step['name']}", step["ref"])
        elif op == "worktree":
            path = self.repo / ".worktrees" / step["name"]
            _git(self.repo, "worktree", "add", "-q", str(path), "-b", step["branch"])
            self.worktrees[step["name"]] = path
        elif op == "detach-worktree":
            path = self.worktrees[step["name"]]
            shutil.move(str(path), str(self.root / f"{step['name']}-relocated"))
        elif op == "age-worktree":
            # `git gc` prunes a worktree registration only once its admin
            # files are older than gc.worktreePruneExpire (3.months.ago by
            # default) -- verified against git 2.53.0, and the reason the
            # `gc` handler probes at that expiry rather than prune's own.
            admin = self.repo / ".git" / "worktrees" / step["name"]
            old = time.time() - 365 * 24 * 3600
            for entry in sorted(admin.rglob("*"), reverse=True):
                os.utime(entry, (old, old))
            os.utime(admin, (old, old))
        elif op == "config-alias":
            _git(self.repo, "config", f"alias.{step['name']}", step["definition"])
        else:  # pragma: no cover - a typo in the fixture must not pass quietly
            raise AssertionError(f"unknown fixture setup op: {op!r}")

    def resolve(self, text: str) -> str:
        text = text.replace("{repo}", str(self.repo)).replace("{tmp}", str(self.root))
        for name, path in self.worktrees.items():
            text = text.replace(f"{{wt:{name}}}", str(path))
        return text


def _load_fixture() -> list[dict]:
    data = json.loads(FIXTURE_PATH.read_text(encoding="utf-8"))
    return data["cases"]


def _build_command(case: dict, world: FixtureWorld) -> str:
    command = world.resolve(case["command"])
    for _ in range(case.get("wrap_in_bash_c", 0)):
        command = "bash -c " + shlex.quote(command)
    return command


class SharedFixtureParityTests(unittest.TestCase):
    """Both implementations, same repository state, same expected decision.

    The repositories are built ONCE per case and reused by both halves --
    two separate builds would let a setup difference masquerade as a guard
    difference, which is the failure this whole file exists to rule out.
    """

    @classmethod
    def setUpClass(cls) -> None:
        cls.cases = _load_fixture()
        cls.tmp = tempfile.mkdtemp(prefix="guard-parity-")
        cls.worlds: dict[str, FixtureWorld] = {}
        cls.commands: dict[str, str] = {}
        for case in cls.cases:
            world = FixtureWorld(Path(cls.tmp) / case["id"])
            for step in case.get("setup", []):
                world.apply(step)
            cls.worlds[case["id"]] = world
            cls.commands[case["id"]] = _build_command(case, world)
        cls.node_results, cls.node_skip_reason = cls._run_node_side()

    @classmethod
    def tearDownClass(cls) -> None:
        shutil.rmtree(cls.tmp, ignore_errors=True)

    @classmethod
    def _run_node_side(cls):
        node = shutil.which("node")
        if node is None:
            return None, "node is not installed"
        plan = {
            "cases": [
                {
                    "id": case["id"],
                    "command": cls.commands[case["id"]],
                    "cwd": cls.worlds[case["id"]].resolve(case.get("cwd", "{repo}")),
                }
                for case in cls.cases
            ]
        }
        plan_path = Path(cls.tmp) / "plan.json"
        plan_path.write_text(json.dumps(plan), encoding="utf-8")
        proc = subprocess.run(
            [node, str(RUNNER_PATH), str(plan_path)],
            capture_output=True,
            text=True,
            timeout=300,
        )
        if proc.returncode == EXIT_UNSUPPORTED:
            return None, proc.stderr.strip() or "no TypeScript transform available"
        if proc.returncode != 0:
            raise AssertionError(
                "guard_parity_runner.mjs failed (this is a real failure, not an "
                f"unavailable tool):\n{proc.stderr}"
            )
        return json.loads(proc.stdout)["results"], None

    def _python_decision(self, case: dict) -> tuple[str, str]:
        world = self.worlds[case["id"]]
        cwd = world.resolve(case.get("cwd", "{repo}"))
        decision = guard.evaluate_command(self.commands[case["id"]], cwd)
        return ("blocked", decision["reason"]) if decision else ("allowed", "")

    def test_fixture_is_non_trivial(self) -> None:
        # A fixture that quietly emptied itself would make every parity
        # assertion below vacuous.
        self.assertGreaterEqual(len(self.cases), 40)
        self.assertEqual(
            len(self.cases),
            len({case["id"] for case in self.cases}),
            "duplicate fixture case ids",
        )
        issues = {case.get("issue") for case in self.cases}
        for issue in ("217", "218", "219", "220", "221", "222"):
            self.assertIn(issue, issues, f"no fixture case covers deagy/cadre#{issue}")
        self.assertTrue(
            any(case["expected"] == "allowed" for case in self.cases),
            "an all-blocked fixture would pass against a guard that blocks everything",
        )

    def test_python_guard_matches_the_fixture(self) -> None:
        for case in self.cases:
            with self.subTest(case=case["id"]):
                decision, reason = self._python_decision(case)
                self.assertEqual(case["expected"], decision, case.get("why", ""))
                if case.get("reason_contains"):
                    self.assertIn(case["reason_contains"], reason)

    def test_typescript_guard_matches_the_fixture(self) -> None:
        if self.node_results is None:
            self.skipTest(f"TypeScript half not exercised: {self.node_skip_reason}")
        for case in self.cases:
            with self.subTest(case=case["id"]):
                result = self.node_results.get(case["id"])
                self.assertIsNotNone(result, "runner returned no result for this case")
                self.assertEqual(case["expected"], result["decision"], case.get("why", ""))
                if case.get("reason_contains"):
                    self.assertIn(case["reason_contains"], result["reason"])

    def test_both_guards_agree_case_by_case(self) -> None:
        # Deliberately separate from the two assertions above: those compare
        # each guard to the fixture, this compares the guards to EACH OTHER,
        # so a fixture expectation edited to match a newly-divergent
        # implementation still fails here.
        if self.node_results is None:
            self.skipTest(f"TypeScript half not exercised: {self.node_skip_reason}")
        for case in self.cases:
            with self.subTest(case=case["id"]):
                python_decision, python_reason = self._python_decision(case)
                node_result = self.node_results[case["id"]]
                self.assertEqual(
                    python_decision,
                    node_result["decision"],
                    "the two guards disagree on this command; whichever is right, "
                    "they must be changed together",
                )
                self.assertEqual(python_reason, node_result["reason"])

    def test_node_side_is_actually_exercised_when_available(self) -> None:
        # Guards the guard: if node exists and the transform works, a skip
        # would mean the TypeScript half silently stopped being checked.
        if shutil.which("node") is None:
            self.skipTest("node is not installed")
        if self.node_results is None:
            self.skipTest(f"no TypeScript transform available: {self.node_skip_reason}")
        self.assertEqual(len(self.cases), len(self.node_results))


if __name__ == "__main__":
    unittest.main()
