"""PP-FR-3: a second roster exists, and is exercised.

Phase B′. The fixture at `fixtures/minimal-roster/` is the Phase 0 spike's
throwaway roster, made permanent — the spike answered "is the seam real" once;
this keeps the answer true.

**Authored fresh, not subset from Cadre's**, and that is asserted rather than
promised (`test_fixture_shares_nothing_with_cadre`). A copy would satisfy every
assumption Cadre happens to satisfy, which is exactly the blindness the parked
proposal's condition 3 names — and which the spike demonstrated is not
hypothetical: the first foreign roster this repository ever had immediately hit
an undeclared format assumption nobody knew was being made (G-12).

Acceptance cases (a)–(g). (e) is the one without which this whole file reports
"the seam is real" while blind to the only blocker that ever stood in its way.
"""

from __future__ import annotations

import importlib
import json
import os
import shutil
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

ORCHESTRATION_ROOT = Path(__file__).resolve().parent.parent
SRC_DIR = ORCHESTRATION_ROOT / "src"
ROSTER_DIR = ORCHESTRATION_ROOT.parent
REPO_ROOT = ROSTER_DIR.parent
FIXTURE = ORCHESTRATION_ROOT / "test" / "fixtures" / "minimal-roster"
IN_TREE_KERNEL = REPO_ROOT / "bin" / "agentic-sdlc"

for candidate in (SRC_DIR, ROSTER_DIR / "shared" / "src"):
    if str(candidate) not in sys.path:
        sys.path.insert(0, str(candidate))

import build_dispatch_plan  # noqa: E402
import routing as routing_mod  # noqa: E402
import select_agents  # noqa: E402
from roster_manifest import RosterManifestError, load_roster_manifest  # noqa: E402

MATCHING_TASK = "Forge a new sprocket flange assembly"
MATCHING_FILES = ["sprockets/flange.yaml"]
CADRE_ROLE_MARKERS = (
    "code-reviewer", "product-intent-agent", "requirements-agent",
    "security-reviewer", "cloud-architect", "application-engineer",
)


def _plan(roster_root: Path, task: str, files: list[str], *, require_sdlc: bool) -> dict:
    manifest = load_roster_manifest(roster_root)
    catalog = routing_mod.load_catalog(manifest.catalog)
    config, _ = select_agents.resolve_effective_routing(manifest.routing, start=roster_root)
    select_agents.validate_routing_config(config)
    return build_dispatch_plan.build_dispatch_plan(
        config, catalog,
        {
            "task": task, "task_id": "PP-FR-3", "repository_root": str(roster_root),
            "base": None, "classification": "internal", "changed_files": files,
            "changed_file_source": "explicit", "sources": ["fixture-roster"], "top": 5,
        },
        require_sdlc=require_sdlc,
    )


class _BrokenCopy:
    """A mutable copy of the fixture, for the fail-closed cases."""

    def __init__(self) -> None:
        self._tmp = tempfile.TemporaryDirectory()
        self.root = Path(self._tmp.name) / "roster"
        shutil.copytree(FIXTURE, self.root)

    def __enter__(self) -> "_BrokenCopy":
        return self

    def __exit__(self, *exc) -> None:
        self._tmp.cleanup()

    def patch_manifest(self, **changes) -> None:
        path = self.root / "roster.json"
        data = json.loads(path.read_text(encoding="utf-8"))
        for key, value in changes.items():
            if value is None:
                data.pop(key, None)
            else:
                data[key] = value
        path.write_text(json.dumps(data), encoding="utf-8")


class TestFixtureIsGenuinelyForeign(unittest.TestCase):
    def test_fixture_shares_nothing_with_cadre(self) -> None:
        """PP-FR-3's "do not subset Cadre's", asserted rather than promised."""
        cadre_manifest = load_roster_manifest(ROSTER_DIR)
        fixture_manifest = load_roster_manifest(FIXTURE)
        cadre_roles = set(routing_mod.load_catalog(cadre_manifest.catalog))
        fixture_roles = set(routing_mod.load_catalog(fixture_manifest.catalog))
        self.assertTrue(fixture_roles, "fixture catalog is empty")
        self.assertEqual(
            set(), cadre_roles & fixture_roles,
            "the fixture shares role ids with Cadre. A leaked default would then "
            "show up as a plausible name instead of a wrong one, which is the "
            "whole reason this roster is authored fresh.",
        )
        cadre_routing = json.loads(cadre_manifest.routing.read_text(encoding="utf-8"))
        fixture_routing = json.loads(fixture_manifest.routing.read_text(encoding="utf-8"))

        def keywords(config):
            return {k for route in config["routes"] for k in route.get("keywords", [])}

        self.assertEqual(set(), keywords(cadre_routing) & keywords(fixture_routing))
        self.assertEqual(
            set(),
            {r["id"] for r in cadre_routing["routes"]}
            & {r["id"] for r in fixture_routing["routes"]},
        )

    def test_role_definitions_resolve_and_exist(self) -> None:
        """A broken `role_root` passes every selection case if nothing ever
        dereferences a `definition` path."""
        manifest = load_roster_manifest(FIXTURE)
        import yaml

        catalog = yaml.safe_load(manifest.catalog.read_text(encoding="utf-8"))
        self.assertTrue(catalog["agents"])
        for role_id, entry in catalog["agents"].items():
            with self.subTest(role=role_id):
                definition = manifest.role_root / entry["definition"]
                self.assertTrue(definition.is_file(), f"{definition} does not exist")
                self.assertTrue(definition.read_text(encoding="utf-8").strip())


class TestAcceptance(unittest.TestCase):
    """(a), (b), (d) — the cases that pass without the lifecycle."""

    def test_a_plan_is_schema_valid_and_names_only_fixture_roles(self) -> None:
        import jsonschema

        plan = _plan(FIXTURE, MATCHING_TASK, MATCHING_FILES, require_sdlc=False)
        schema = json.loads(
            (ORCHESTRATION_ROOT / "selection.schema.json").read_text(encoding="utf-8")
        )
        jsonschema.validate(plan, schema)
        selected = [agent for group in plan["agents"].values() for agent in group]
        self.assertTrue(selected, "no agents were selected against the fixture")
        leaked = [agent for agent in selected if agent in CADRE_ROLE_MARKERS]
        self.assertEqual([], leaked, f"Cadre roles leaked into a foreign plan: {leaked}")

    def test_b_no_match_returns_needs_triage_not_a_guess(self) -> None:
        plan = _plan(
            FIXTURE, "Recalibrate the quantum tachyon manifold", ["unrelated/thing.txt"],
            require_sdlc=False,
        )
        self.assertEqual("needs-triage", plan["status"])
        selected = [agent for group in plan["agents"].values() for agent in group]
        self.assertEqual([], selected, "needs-triage still named agents")

    def test_d_a_matching_task_classifies_to_a_real_workflow(self) -> None:
        """Regression pin, not a falsification — it already worked.

        `_select_workflow()`'s final stage reads each matched route's declared
        `workflow_shape`, which the roster supplies. OD-8 was withdrawn on
        exactly this, and the property is most likely to be lost silently while
        the fixture is edited for some other purpose.
        """
        plan = _plan(FIXTURE, MATCHING_TASK, MATCHING_FILES, require_sdlc=False)
        self.assertNotEqual("unclassified", plan["workflow"])
        self.assertEqual("new-service", plan["workflow"])


@unittest.skipUnless(IN_TREE_KERNEL.is_file(), "in-tree kernel wrapper missing")
class TestLifecycleAwareAcceptance(unittest.TestCase):
    """(e) — the case without which (a)–(d) prove less than they appear to.

    None of (a)–(d) requires the fixture to declare `quality_gates`, so
    `_gate_agents()` never fires and the only blocker a foreign roster ever had
    is invisible. Before OD-9 this exact case raised
    `ValueError: Routing selected an unknown agent: code-reviewer`.

    Forced rather than skipped: `bin/agentic-sdlc` runs the in-tree kernel with
    no install, so this is deterministic and checkout-only rather than
    dependent on whatever happens to be on PATH.
    """

    def test_the_fixture_declares_quality_gates(self) -> None:
        # Self-vacuity: without this the case below silently degrades into (a).
        config = json.loads(
            load_roster_manifest(FIXTURE).routing.read_text(encoding="utf-8")
        )
        gated = [r["id"] for r in config["routes"] if r.get("quality_gates")]
        self.assertTrue(
            gated,
            "no fixture route declares quality_gates, so this test class cannot "
            "reach _gate_agents() and proves nothing beyond case (a).",
        )

    def test_e_a_gate_bearing_foreign_roster_still_gets_a_plan(self) -> None:
        with mock.patch.dict(os.environ, {"AGENTIC_SDLC_BIN": str(IN_TREE_KERNEL)}, clear=False):
            importlib.reload(build_dispatch_plan)
            try:
                plan = _plan(FIXTURE, MATCHING_TASK, MATCHING_FILES, require_sdlc=True)
            finally:
                importlib.reload(build_dispatch_plan)
        self.assertEqual("integrated", plan["lifecycle_tracking"]["status"])
        self.assertEqual("ready", plan["status"])
        selected = [agent for group in plan["agents"].values() for agent in group]
        leaked = [agent for agent in selected if agent in CADRE_ROLE_MARKERS]
        self.assertEqual(
            [], leaked,
            "a Cadre role reached a foreign roster's lifecycle-aware plan. "
            "OD-9 moved the gate-reviewer default into roster data precisely so "
            "a roster without that role gets an empty list rather than a "
            "ValueError from _validate_agents.",
        )


class TestFailsClosed(unittest.TestCase):
    """(c), (f), (g) — C4: name the problem, never degrade to the built-in roster."""

    def test_c_missing_catalog_fails_naming_the_file(self) -> None:
        with _BrokenCopy() as broken:
            (broken.root / "catalog.yaml").unlink()
            with self.assertRaises(RosterManifestError) as caught:
                load_roster_manifest(broken.root)
        message = str(caught.exception)
        self.assertIn("catalog", message)
        self.assertIn("catalog.yaml", message)

    def test_c_missing_routing_fails_naming_the_file(self) -> None:
        with _BrokenCopy() as broken:
            (broken.root / "orchestration" / "routing.json").unlink()
            with self.assertRaises(RosterManifestError) as caught:
                load_roster_manifest(broken.root)
        self.assertIn("routing", str(caught.exception))

    def test_f_a_path_escaping_the_package_is_rejected(self) -> None:
        with _BrokenCopy() as broken:
            broken.patch_manifest(catalog="../../../../etc/passwd")
            with self.assertRaises(RosterManifestError) as caught:
                load_roster_manifest(broken.root)
        message = str(caught.exception)
        self.assertIn("escapes", message)
        self.assertIn("catalog", message)

    def test_g_a_manifest_missing_a_key_fails_naming_the_key(self) -> None:
        with _BrokenCopy() as broken:
            broken.patch_manifest(routing=None)
            with self.assertRaises(RosterManifestError) as caught:
                load_roster_manifest(broken.root)
        self.assertIn("routing", str(caught.exception))

    def test_g_an_unknown_schema_version_is_rejected(self) -> None:
        with _BrokenCopy() as broken:
            broken.patch_manifest(schema_version=99)
            with self.assertRaises(RosterManifestError) as caught:
                load_roster_manifest(broken.root)
        self.assertIn("schema_version", str(caught.exception))

    def test_a_missing_manifest_does_not_fall_back_to_cadre(self) -> None:
        """The failure C4 exists for: silence, not a wrong answer, is the risk."""
        with _BrokenCopy() as broken:
            (broken.root / "roster.json").unlink()
            with self.assertRaises(RosterManifestError) as caught:
                load_roster_manifest(broken.root)
        self.assertIn("roster.json", str(caught.exception))


if __name__ == "__main__":
    unittest.main()
