"""The plan-level signal for a matched route that declared no `workflow_shape`.

Covers issue #214, which finishes #210 at the project-local overlay boundary.

#210 gave every route in this repository's own `routing.yaml` an explicit
`workflow_shape`, enforced by `test_selector.py::WorkflowShapeDeclarationTests`.
That build-time guard reaches this repository's routes and nothing else:

- `routing.schema.json`'s `route` definition requires only `id`, so
  `workflow_shape` is optional and an overlay predating #210 still validates.
- `routing.py::validate_routing_config` checks the *value* against
  `WORKFLOW_SHAPES`, never its presence.

Both are deliberate -- requiring presence would break every existing overlay
that adds a route -- so an overlay-added route with no shape reproduced the
exact defect #210 was filed about: it matches, contributes no delivery shape,
and the plan falls back to `unclassified` by omission with nothing to notice.

The fix reports rather than rejects. `build_dispatch_plan` emits an optional
top-level `undeclared_workflow_shape_routes` array naming those routes. It is
emitted only when non-empty and is absent from `selection.schema.json`'s
top-level `required` list -- the same additive pattern `provenance` already
uses -- so `schema_version` stays 5 and a consumer pinned to a v5 copy of the
schema is unaffected.

These tests live in their own file rather than in `test_selector.py` so the
#214 behavior is reviewable independently of the #210 declaration tests it
completes.
"""

from __future__ import annotations

import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

ROOT = Path(__file__).resolve().parent.parent
AGENTS_ROOT = ROOT.parent
REPOSITORY_ROOT = AGENTS_ROOT.parent
sys.path.insert(0, str(ROOT / "src"))
sys.path.insert(0, str(REPOSITORY_ROOT / "roster" / "shared" / "src"))

import build_dispatch_plan as build_dispatch_plan_module  # noqa: E402
from build_dispatch_plan import build_dispatch_plan  # noqa: E402
from routing import load_catalog, load_routing  # noqa: E402
from routing_overlay import OVERLAY_RELATIVE_PATH  # noqa: E402

try:
    import jsonschema

    JSONSCHEMA_AVAILABLE = True
except ImportError:  # pragma: no cover - exercised only where jsonschema is absent
    JSONSCHEMA_AVAILABLE = False

SELECT_AGENTS = ROOT / "src" / "select_agents.py"
CATALOG_PATH = AGENTS_ROOT / "catalog.yaml"
ROUTING_PATH = ROOT / "routing.yaml"
SCHEMA_PATH = ROOT / "selection.schema.json"
SIGNAL = "undeclared_workflow_shape_routes"

# A route id and keyword pair chosen to be inert against the live
# routing.yaml: nothing in the base configuration claims either, so a plan
# built with this overlay matches this route and nothing else, which is what
# isolates the shape fallback under test.
OVERLAY_ROUTE_ID = "widget-cabinet-fabrication"
OVERLAY_KEYWORD = "widget cabinet fabrication"
OVERLAY_TASK = "plan the widget cabinet fabrication run"


def _values(**overrides: object) -> dict[str, object]:
    values = {
        "task": OVERLAY_TASK,
        "changed_files": ["notes.txt"],
        "changed_file_source": "test",
        "repository_root": str(REPOSITORY_ROOT),
        "source": "example/repository",
        "classification": "internal",
        "task_id": "ISSUE-214-1",
        **overrides,
    }
    return values


def _plan(config: dict, **overrides: object) -> dict:
    # Lifecycle-contract lookup is stubbed out so these assertions do not
    # depend on whether a kernel happens to be resolvable in the environment
    # running the suite; the signal under test is independent of it.
    with patch.object(build_dispatch_plan_module, "try_lifecycle_contract", return_value=None):
        return build_dispatch_plan(config, load_catalog(CATALOG_PATH), _values(**overrides))


def _config_with_overlay_route(**route_fields: object) -> dict:
    """The live routing.yaml plus one added route, mimicking what
    `routing_overlay.merge_routing` produces for an overlay-added entry (it
    appends `dict(overlay_entry)` verbatim -- see
    `_merge_route_or_risk_rule_section`), without re-testing the merge itself.
    """
    config = load_routing(ROUTING_PATH)
    config["routes"] = [
        *config["routes"],
        {
            "id": OVERLAY_ROUTE_ID,
            "keywords": [OVERLAY_KEYWORD],
            "primary": ["backend-engineer"],
            **route_fields,
        },
    ]
    return config


class SignalFiresForAnUndeclaredShapeTests(unittest.TestCase):
    def test_route_without_a_shape_is_named_and_the_workflow_falls_back(self) -> None:
        plan = _plan(_config_with_overlay_route())
        self.assertEqual([match["id"] for match in plan["matched_routes"]], [OVERLAY_ROUTE_ID])
        self.assertEqual(plan[SIGNAL], [OVERLAY_ROUTE_ID])
        # The point of the signal: this `unclassified` is the by-omission
        # fallback, and the plan now says so instead of leaving it silent.
        self.assertEqual(plan["workflow"], "unclassified")

    def test_null_and_empty_shapes_count_as_undeclared(self) -> None:
        for shape in (None, ""):
            with self.subTest(workflow_shape=shape):
                plan = _plan(_config_with_overlay_route(workflow_shape=shape))
                self.assertEqual(plan[SIGNAL], [OVERLAY_ROUTE_ID])

    def test_only_the_undeclared_route_is_named_when_a_declared_route_co_matches(self) -> None:
        config = _config_with_overlay_route()
        plan = _plan(config, task=f"{OVERLAY_TASK} and update the runbook documentation")
        matched = [match["id"] for match in plan["matched_routes"]]
        self.assertIn(OVERLAY_ROUTE_ID, matched)
        self.assertIn("documentation", matched)
        self.assertEqual(plan[SIGNAL], [OVERLAY_ROUTE_ID])


class SignalStaysSilentWhenShapesAreDeclaredTests(unittest.TestCase):
    def test_declared_delivery_shape_produces_no_signal(self) -> None:
        plan = _plan(_config_with_overlay_route(workflow_shape="new-service"))
        self.assertEqual([match["id"] for match in plan["matched_routes"]], [OVERLAY_ROUTE_ID])
        self.assertNotIn(SIGNAL, plan)
        self.assertEqual(plan["workflow"], "new-service")

    def test_unclassified_is_a_declaration_not_an_omission(self) -> None:
        """The distinction the whole signal rests on: a route that says it
        claims no delivery shape is doing the right thing, and must not be
        reported alongside one that simply left the field off. Both produce
        `workflow: unclassified`; only one is a defect.
        """
        plan = _plan(_config_with_overlay_route(workflow_shape="unclassified"))
        self.assertNotIn(SIGNAL, plan)
        self.assertEqual(plan["workflow"], "unclassified")

    def test_no_matched_routes_produces_no_signal(self) -> None:
        plan = _plan(load_routing(ROUTING_PATH), task="qqzzx nothing matches this at all")
        self.assertEqual(plan["matched_routes"], [])
        self.assertNotIn(SIGNAL, plan)

    def test_this_repositorys_own_routing_never_triggers_the_signal(self) -> None:
        """#210's declaration test guarantees the input; this pins the
        consequence, so a base route that ever slipped past it would surface
        here too -- the signal is not overlay-specific.
        """
        config = load_routing(ROUTING_PATH)
        self.assertNotIn(SIGNAL, _plan(config, task="update the terraform module", changed_files=["main.tf"]))
        self.assertNotIn(SIGNAL, _plan(config, task="update the runbook documentation"))


class SchemaCompatibilityTests(unittest.TestCase):
    def setUp(self) -> None:
        self.schema = json.loads(SCHEMA_PATH.read_text(encoding="utf-8"))

    def test_schema_version_is_unchanged_at_5(self) -> None:
        """A closed schema (`additionalProperties: false`) forces a version
        bump for any *required* new field, and a bump breaks every consumer
        validating against a pinned copy. This field is optional precisely to
        avoid that, so the constant must not move with it.
        """
        self.assertEqual(self.schema["properties"]["schema_version"]["const"], 5)
        self.assertEqual(_plan(load_routing(ROUTING_PATH))["schema_version"], 5)

    def test_signal_is_declared_optional_like_provenance(self) -> None:
        self.assertIn(SIGNAL, self.schema["properties"])
        self.assertNotIn(SIGNAL, self.schema["required"])
        self.assertNotIn("provenance", self.schema["required"])

    @unittest.skipUnless(JSONSCHEMA_AVAILABLE, "jsonschema is not installed")
    def test_plans_validate_with_and_without_the_signal(self) -> None:
        for label, config in (
            ("undeclared", _config_with_overlay_route()),
            ("declared", _config_with_overlay_route(workflow_shape="new-service")),
        ):
            with self.subTest(case=label):
                jsonschema.validate(instance=_plan(config), schema=self.schema)


class FingerprintTests(unittest.TestCase):
    def test_the_signal_is_part_of_the_fingerprinted_payload(self) -> None:
        """Unlike `provenance`, this field is computed from the plan's own
        matched routes rather than generation-time environment state, so it
        belongs inside the determinism check rather than excluded from it.
        """
        undeclared = _plan(_config_with_overlay_route())
        declared = _plan(_config_with_overlay_route(workflow_shape="unclassified"))
        # Same routes, same agents, same workflow label -- the signal is the
        # only difference, so an unchanged fingerprint would mean it was
        # excluded from the hashed payload.
        self.assertEqual(
            [match["id"] for match in undeclared["matched_routes"]],
            [match["id"] for match in declared["matched_routes"]],
        )
        self.assertEqual(undeclared["workflow"], declared["workflow"])
        self.assertNotEqual(undeclared["dispatch_fingerprint"], declared["dispatch_fingerprint"])

    def test_the_fingerprint_is_deterministic_for_a_plan_carrying_the_signal(self) -> None:
        config = _config_with_overlay_route()
        self.assertEqual(_plan(config)["dispatch_fingerprint"], _plan(config)["dispatch_fingerprint"])


class SelectionPathIntegrationTests(unittest.TestCase):
    """End to end through `cadre select` with a real project-local overlay
    file, so the signal is proven against the path a consumer actually runs --
    not only against an in-process config dict.
    """

    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory(prefix="undeclared-shape-")
        self.root = Path(self.temporary.name)
        (self.root / ".git").mkdir()

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def _write_overlay(self, **route_fields: object) -> None:
        overlay_path = self.root / OVERLAY_RELATIVE_PATH
        overlay_path.parent.mkdir(parents=True, exist_ok=True)
        overlay_path.write_text(
            json.dumps(
                {
                    "routes": [
                        {
                            "id": OVERLAY_ROUTE_ID,
                            "keywords": [OVERLAY_KEYWORD],
                            "primary": ["backend-engineer"],
                            **route_fields,
                        }
                    ]
                }
            ),
            encoding="utf-8",
        )

    def _select(self) -> dict:
        result = subprocess.run(
            [
                sys.executable,
                str(SELECT_AGENTS),
                "--root",
                str(self.root),
                "--task",
                OVERLAY_TASK,
                "--files",
                "notes.txt",
                "--task-id",
                "ISSUE-214-E2E",
                "--classification",
                "internal",
            ],
            capture_output=True,
            text=True,
            check=False,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        return json.loads(result.stdout)

    def test_overlay_route_without_a_shape_is_reported_by_cadre_select(self) -> None:
        self._write_overlay()
        plan = self._select()
        self.assertEqual([match["id"] for match in plan["matched_routes"]], [OVERLAY_ROUTE_ID])
        self.assertEqual(plan[SIGNAL], [OVERLAY_ROUTE_ID])
        self.assertEqual(plan["workflow"], "unclassified")

    def test_overlay_route_with_a_shape_is_not_reported_by_cadre_select(self) -> None:
        self._write_overlay(workflow_shape="new-service")
        plan = self._select()
        self.assertEqual([match["id"] for match in plan["matched_routes"]], [OVERLAY_ROUTE_ID])
        self.assertNotIn(SIGNAL, plan)
        self.assertEqual(plan["workflow"], "new-service")

    def test_no_overlay_leaves_the_plan_shape_unchanged(self) -> None:
        result = subprocess.run(
            [
                sys.executable,
                str(SELECT_AGENTS),
                "--root",
                str(self.root),
                "--task",
                "update the terraform module",
                "--files",
                "main.tf",
                "--task-id",
                "ISSUE-214-E2E-BASE",
                "--classification",
                "internal",
            ],
            capture_output=True,
            text=True,
            check=False,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn(SIGNAL, json.loads(result.stdout))


if __name__ == "__main__":
    unittest.main()
