"""PP-FR-5: the knowledge store stays platform-anchored, not roster-anchored.

Phase D of `roster/orchestration/runs/cadre-feature-portable-platform-2026-08-11/`.
This phase changes no behaviour and is deliberately landed *before* the phase
that can break it.

`build_dispatch_plan.py` holds two adjacent constants that used to come from
the same `parents[2]` walk:

    KNOWLEDGE_STORE_ROOT = Path(__file__).resolve().parents[2] / "knowledge-store"
    ROSTER_ROOT          = Path(__file__).resolve().parents[2]

PP-FR-1 makes `ROSTER_ROOT` resolver-driven. The live risk is that an
implementer takes the knowledge-store constant along with it -- same shape,
adjacent line, and it would look like tidying. Do that and the emitted argv
names a path that stops existing whenever the roster root points somewhere
without a knowledge store, in a plan whose consumer is a TypeScript file in
another package (`cline-plugins/cline-agents/index.ts`).

**What the first constant is now.** The Python knowledge store was replaced by
the Go implementation behind `cadre knowledge` and `roster/knowledge-store/src/`
was deleted, so `KNOWLEDGE_STORE_ROOT / "src" / "cli.py"` became a dangling path
in every plan -- the exact failure this file exists to catch, arriving by a
route it did not anticipate. The constant carrying the emitted path is now
`KNOWLEDGE_CLI` (`<checkout>/bin/cadre`) and the assertions below moved onto it
unchanged in substance: PP-FR-5 is about *where the emitted path is anchored*,
not about which executable sits at the end of it. It is a `next(...)` walk up
`Path(__file__).resolve().parents` rather than a fixed index because the
packaged plugin relocates this tree under `suite/`, putting `bin/cadre` one
level further up than a repository checkout does.

The store's *data* location stays governed by `knowledge_store.home`
(`settings.py:673-680`), which is a different setting and is not what this pins.

Named after the requirement rather than added to
`roster/knowledge-store/test/test_scope_enforcement.py`, which the requirements
baseline originally nominated: the assertions here are about an orchestration
constant, and `roster/context-store/` carries a same-named test file, so a
distinct name is cheaper to cite unambiguously than a path is.
"""

from __future__ import annotations

import ast
import importlib
import os
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

ORCHESTRATION_ROOT = Path(__file__).resolve().parent.parent
SRC_DIR = ORCHESTRATION_ROOT / "src"
ROSTER_DIR = ORCHESTRATION_ROOT.parent
REPO_ROOT = ROSTER_DIR.parent

if str(SRC_DIR) not in sys.path:
    sys.path.insert(0, str(SRC_DIR))

import build_dispatch_plan  # noqa: E402
import select_agents  # noqa: E402


def _module_level_assignment(source: str, target: str) -> ast.expr:
    """Return the right-hand side of a module-level `target = ...` assignment."""
    tree = ast.parse(source)
    for node in tree.body:
        if isinstance(node, ast.Assign):
            for bound in node.targets:
                if isinstance(bound, ast.Name) and bound.id == target:
                    return node.value
        elif isinstance(node, ast.AnnAssign):
            if isinstance(node.target, ast.Name) and node.target.id == target and node.value:
                return node.value
    raise AssertionError(
        f"no module-level assignment to {target} found -- this guard is checking nothing"
    )


def _names_in(node: ast.expr) -> set[str]:
    return {child.id for child in ast.walk(node) if isinstance(child, ast.Name)}


class TestKnowledgeStoreRootIsPlatformAnchored(unittest.TestCase):
    """The structural half: KNOWLEDGE_CLI must not be roster-derived."""

    def setUp(self) -> None:
        self.source = (SRC_DIR / "build_dispatch_plan.py").read_text(encoding="utf-8")

    def test_the_constants_this_guard_is_about_still_exist(self) -> None:
        # Self-vacuity guard, on the model of test_context_boundary.py:150-155.
        # A rename would otherwise make every assertion below pass over nothing.
        for name in ("KNOWLEDGE_CLI", "ROSTER_ROOT"):
            self.assertIsNotNone(
                _module_level_assignment(self.source, name),
                f"{name} is no longer a module-level constant in build_dispatch_plan.py",
            )
        self.assertTrue(
            hasattr(build_dispatch_plan, "KNOWLEDGE_CLI"),
            "build_dispatch_plan no longer exposes KNOWLEDGE_CLI",
        )

    def test_knowledge_cli_does_not_resolve_through_the_roster_root(self) -> None:
        """The assertion PP-FR-5 exists for.

        Fails if someone writes `KNOWLEDGE_CLI = ROSTER_ROOT / ...`, which is
        the exact mistake Phase A invites and which no existing test would
        otherwise catch.
        """
        rhs = _module_level_assignment(self.source, "KNOWLEDGE_CLI")
        names = _names_in(rhs)
        self.assertNotIn(
            "ROSTER_ROOT",
            names,
            "KNOWLEDGE_CLI is derived from ROSTER_ROOT. The knowledge store is "
            "platform-owned (PP-FR-6) and must not follow a resolved roster: a plan's "
            "emitted CLI path would stop existing whenever roster.root points at a "
            "directory with no CLI. Re-derive it from Path(__file__).",
        )
        self.assertNotIn(
            "roster_root",
            {name.lower() for name in names},
            "KNOWLEDGE_CLI references a roster-root-shaped name",
        )

    def test_knowledge_cli_is_anchored_on_this_files_location(self) -> None:
        rhs = _module_level_assignment(self.source, "KNOWLEDGE_CLI")
        self.assertIn(
            "__file__",
            {
                child.id
                for child in ast.walk(rhs)
                if isinstance(child, ast.Name)
            }
            | {
                child.attr
                for child in ast.walk(rhs)
                if isinstance(child, ast.Attribute)
            },
            "KNOWLEDGE_CLI is not derived from Path(__file__); it must be "
            "anchored on the platform checkout rather than on any resolved value.",
        )

    def test_resolved_value_points_into_the_platform_checkout(self) -> None:
        self.assertEqual(
            build_dispatch_plan.KNOWLEDGE_CLI,
            REPO_ROOT / "bin" / "cadre",
            "KNOWLEDGE_CLI no longer resolves to this checkout's cadre wrapper",
        )


class TestEmittedKnowledgeCliPath(unittest.TestCase):
    """The behavioural half: the emitted path must exist on disk, by stat."""

    @staticmethod
    def _plan(repository_root: Path) -> dict:
        catalog = select_agents.load_catalog(select_agents.ROSTER_ROOT / "catalog.yaml")
        config, _overlay = select_agents.resolve_effective_routing(
            select_agents.ORCHESTRATION_ROOT / "routing.json", start=repository_root
        )
        select_agents.validate_routing_config(config)
        return build_dispatch_plan.build_dispatch_plan(
            config,
            catalog,
            {
                "task": "Fix a bug in the login form",
                "task_id": "TASK-PP-FR-5",
                "repository_root": str(repository_root),
                "base": None,
                "classification": "internal",
                "changed_files": ["src/login.tsx"],
                "changed_file_source": "explicit",
                "sources": ["deagy/cadre"],
                "top": 5,
            },
            require_sdlc=False,
        )

    def _cli_paths(self, plan: dict) -> list[str]:
        requests = plan.get("knowledge_context", {}).get("requests", [])
        self.assertTrue(requests, "plan emitted no knowledge_context requests to check")
        paths = []
        for request in requests:
            args = request["invocation"]["args"]
            self.assertTrue(args, "invocation.args is empty")
            paths.append(args[0])
        return paths

    def test_emitted_cli_path_exists_on_disk(self) -> None:
        # By stat, not by string shape. A path that merely *looks* right is what
        # this requirement is guarding against.
        for path in self._cli_paths(self._plan(REPO_ROOT)):
            with self.subTest(path=path):
                self.assertTrue(Path(path).is_absolute(), f"{path} is not absolute")
                self.assertTrue(
                    Path(path).is_file(),
                    f"emitted knowledge-store CLI path does not exist: {path}",
                )

    def test_emitted_cli_path_survives_a_roster_root_pointed_elsewhere(self) -> None:
        """Forward pin for PP-FR-1.

        `CADRE_ROSTER_ROOT` is read by nothing today, so this passes trivially on
        the current tree -- that is stated rather than dressed up. Its job starts
        the moment Phase A introduces the setting: point the roster root at a
        directory holding no knowledge store and the emitted path must be
        unchanged and must still exist.
        """
        with tempfile.TemporaryDirectory() as empty:
            with mock.patch.dict(os.environ, {"CADRE_ROSTER_ROOT": empty}, clear=False):
                importlib.reload(build_dispatch_plan)
                try:
                    paths = self._cli_paths(self._plan(REPO_ROOT))
                    for path in paths:
                        with self.subTest(path=path):
                            self.assertTrue(
                                Path(path).is_file(),
                                f"emitted CLI path stopped existing with CADRE_ROSTER_ROOT="
                                f"{empty}: {path}",
                            )
                            self.assertNotIn(
                                empty,
                                path,
                                "the emitted knowledge-store path followed CADRE_ROSTER_ROOT",
                            )
                finally:
                    importlib.reload(build_dispatch_plan)

    def test_emitted_invocation_shape_matches_the_go_knowledge_cli(self) -> None:
        """`cline-plugins/cline-agents/index.ts` executes this argv.

        The shape is a cross-language published contract: the consumer is
        TypeScript in another package. It changed exactly once, when the
        Python store behind `src/cli.py context` was deleted in favour of the
        Go `cadre knowledge search`; that took `schema_version` 7 -> 8 and the
        Cline consumer with it. Pinned here so it cannot change again quietly.
        """
        plan = self._plan(REPO_ROOT)
        for request in plan["knowledge_context"]["requests"]:
            args = request["invocation"]["args"]
            with self.subTest(agent=request.get("agent")):
                self.assertTrue(args[0].endswith(str(Path("bin") / "cadre")), args[0])
                self.assertEqual(args[1:3], ["knowledge", "search"])
                self.assertIn("--source", args)
                self.assertIn("--json", args)
                self.assertNotIn(
                    "--all-sources",
                    args,
                    "a planned retrieval must never widen to every source in the store",
                )
                # The query is a trailing positional under Go's flag package,
                # which stops at the first non-flag argument. If it ever moves
                # ahead of a flag, every `--source` after it silently stops
                # scoping the read -- a widening failure, so assert placement
                # rather than mere presence.
                self.assertEqual(args[-1], request["query"])
                self.assertNotIn(request["query"], args[:-1])

    def test_emitted_launcher_describes_a_directly_executed_wrapper(self) -> None:
        """args[0] is executed as-is; no interpreter is prepended to it."""
        plan = self._plan(REPO_ROOT)
        for request in plan["knowledge_context"]["requests"]:
            with self.subTest(agent=request.get("agent")):
                self.assertEqual(
                    request["invocation"]["launcher"],
                    {
                        "runtime": "cadre",
                        "minimum_version": "0.5.0",
                        "resolution": "platform-anchored",
                    },
                )

    def test_emitted_cli_path_is_executable(self) -> None:
        """Stronger than `is_file()`: the consumer execs it directly now."""
        for path in self._cli_paths(self._plan(REPO_ROOT)):
            with self.subTest(path=path):
                self.assertTrue(
                    os.access(path, os.X_OK),
                    f"emitted knowledge CLI path is not executable: {path}",
                )


if __name__ == "__main__":
    unittest.main()
