"""PP-FR-5: the knowledge store stays platform-anchored, not roster-anchored.

Phase D of `roster/orchestration/runs/cadre-feature-portable-platform-2026-08-11/`.
This phase changes no behaviour and is deliberately landed *before* the phase
that can break it.

`build_dispatch_plan.py:29-30` holds two adjacent constants derived from the
same `parents[2]` walk:

    KNOWLEDGE_STORE_ROOT = Path(__file__).resolve().parents[2] / "knowledge-store"
    ROSTER_ROOT          = Path(__file__).resolve().parents[2]

PP-FR-1 makes `ROSTER_ROOT` resolver-driven. The live risk is that an
implementer takes `KNOWLEDGE_STORE_ROOT` along with it -- same shape, adjacent
line, and it would look like tidying. Do that and `:501` emits a `cli.py` path
that stops existing whenever the roster root points somewhere without a
knowledge store, in a plan whose consumer is a TypeScript file in another
package (`cline-plugins/cline-agents/index.ts:247-259`).

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
    """The structural half: KNOWLEDGE_STORE_ROOT must not be roster-derived."""

    def setUp(self) -> None:
        self.source = (SRC_DIR / "build_dispatch_plan.py").read_text(encoding="utf-8")

    def test_the_constants_this_guard_is_about_still_exist(self) -> None:
        # Self-vacuity guard, on the model of test_context_boundary.py:150-155.
        # A rename would otherwise make every assertion below pass over nothing.
        for name in ("KNOWLEDGE_STORE_ROOT", "ROSTER_ROOT"):
            self.assertIsNotNone(
                _module_level_assignment(self.source, name),
                f"{name} is no longer a module-level constant in build_dispatch_plan.py",
            )
        self.assertTrue(
            hasattr(build_dispatch_plan, "KNOWLEDGE_STORE_ROOT"),
            "build_dispatch_plan no longer exposes KNOWLEDGE_STORE_ROOT",
        )

    def test_knowledge_store_root_does_not_resolve_through_the_roster_root(self) -> None:
        """The assertion PP-FR-5 exists for.

        Fails if someone writes `KNOWLEDGE_STORE_ROOT = ROSTER_ROOT / "knowledge-store"`,
        which is the exact mistake Phase A invites and which no existing test
        would otherwise catch.
        """
        rhs = _module_level_assignment(self.source, "KNOWLEDGE_STORE_ROOT")
        names = _names_in(rhs)
        self.assertNotIn(
            "ROSTER_ROOT",
            names,
            "KNOWLEDGE_STORE_ROOT is derived from ROSTER_ROOT. The knowledge store is "
            "platform-owned (PP-FR-6) and must not follow a resolved roster: a plan's "
            "emitted cli.py path would stop existing whenever roster.root points at a "
            "directory with no knowledge store. Re-derive it from Path(__file__).",
        )
        self.assertNotIn(
            "roster_root",
            {name.lower() for name in names} - {"knowledge_store_root"},
            "KNOWLEDGE_STORE_ROOT references a roster-root-shaped name",
        )

    def test_knowledge_store_root_is_anchored_on_this_files_location(self) -> None:
        rhs = _module_level_assignment(self.source, "KNOWLEDGE_STORE_ROOT")
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
            "KNOWLEDGE_STORE_ROOT is not derived from Path(__file__); it must be "
            "anchored on the platform checkout rather than on any resolved value.",
        )

    def test_resolved_value_points_into_the_platform_checkout(self) -> None:
        self.assertEqual(
            build_dispatch_plan.KNOWLEDGE_STORE_ROOT,
            ROSTER_DIR / "knowledge-store",
            "KNOWLEDGE_STORE_ROOT no longer resolves to this checkout's knowledge store",
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
                "source": "deagy/cadre",
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

    def test_emitted_invocation_shape_is_unchanged(self) -> None:
        """`cline-plugins/cline-agents/index.ts:247-259` executes this argv.

        The shape is a cross-language published contract: the consumer is
        TypeScript in another package. Only *how* the path is computed is in
        scope for this work -- never what the emitted args look like.
        """
        plan = self._plan(REPO_ROOT)
        for request in plan["knowledge_context"]["requests"]:
            args = request["invocation"]["args"]
            with self.subTest(agent=request.get("agent")):
                self.assertTrue(args[0].endswith(str(Path("src") / "cli.py")))
                self.assertEqual(args[1], "context")
                self.assertIn("--source", args)


if __name__ == "__main__":
    unittest.main()
