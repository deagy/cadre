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
top-level `undeclared_workflow_shape_routes` array naming those routes in
match order. It is absent from `selection.schema.json`'s top-level `required`
list and omitted when empty -- but that did NOT make it additive. Adding it
took `schema_version` 5 -> 6, per RUNBOOK.md's "When `schema_version`
increments" rule: the schema is closed (`additionalProperties: false`) and is
vendored away from the producer, so any change to the emitted field set bumps
the version. That rule's carve-out covers a property the consumer never
receives; it does not reach a field emitted unconditionally to overlay
consumers, who are the very population this signal exists for.

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
from routing_overlay import (  # noqa: E402
    OVERLAY_RELATIVE_PATH,
    RoutingOverlayError,
    merge_routing,
)

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


def _overlay_route(route_id: str, keyword: str, **route_fields: object) -> dict:
    return {
        "id": route_id,
        "keywords": [keyword],
        "primary": ["backend-engineer"],
        **route_fields,
    }


def _config_with_overlay_routes(*routes: dict) -> dict:
    """The live routing.yaml plus the given added routes, mimicking what
    `routing_overlay.merge_routing` produces for overlay-added entries (it
    appends `dict(overlay_entry)` verbatim in configuration order -- see
    `_merge_route_or_risk_rule_section`), without re-testing the merge itself.
    """
    config = load_routing(ROUTING_PATH)
    config["routes"] = [*config["routes"], *routes]
    return config


def _config_with_overlay_route(**route_fields: object) -> dict:
    return _config_with_overlay_routes(
        _overlay_route(OVERLAY_ROUTE_ID, OVERLAY_KEYWORD, **route_fields)
    )


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


class MatchOrderAndScopeTests(unittest.TestCase):
    """The two properties the docstring and schema both promise but that a
    single-route fixture cannot falsify: match order, and matched-routes-only
    scoping. Both survived mutation testing before these existed -- replacing
    the comprehension with `sorted(...)`, `reversed(...)`, or a scan over
    `config["routes"]` left the suite fully green.
    """

    # Ordered so that match order and alphabetical order DISAGREE. That
    # disagreement is the whole point: with `alpha` first, a `sorted()`
    # implementation would be indistinguishable from a correct one.
    ZEBRA = ("zebra-cabinet-fabrication", "zebra cabinet fabrication")
    ALPHA = ("alpha-cabinet-fabrication", "alpha cabinet fabrication")
    MIDDLE = ("middle-cabinet-fabrication", "middle cabinet fabrication")

    def test_multiple_undeclared_routes_are_reported_in_match_order(self) -> None:
        config = _config_with_overlay_routes(
            _overlay_route(*self.ZEBRA), _overlay_route(*self.ALPHA)
        )
        plan = _plan(config, task=f"{self.ZEBRA[1]} then {self.ALPHA[1]}")
        expected = [self.ZEBRA[0], self.ALPHA[0]]
        self.assertEqual([match["id"] for match in plan["matched_routes"]], expected)
        self.assertEqual(plan[SIGNAL], expected)
        # Kills the `sorted()` mutant specifically: without this, an
        # implementation that sorts its output still satisfies the assertion
        # above only by coincidence of fixture naming, so state the
        # disagreement outright rather than relying on it.
        self.assertNotEqual(plan[SIGNAL], sorted(plan[SIGNAL]))
        # ...and the `reversed()` mutant, which the equality above already
        # catches, but only while there are exactly two entries.
        self.assertEqual(plan[SIGNAL][0], self.ZEBRA[0])

    def test_declared_and_undeclared_routes_interleave_without_reordering(self) -> None:
        config = _config_with_overlay_routes(
            _overlay_route(*self.ZEBRA),
            _overlay_route(*self.MIDDLE, workflow_shape="new-service"),
            _overlay_route(*self.ALPHA),
        )
        plan = _plan(config, task=f"{self.ZEBRA[1]} and {self.MIDDLE[1]} and {self.ALPHA[1]}")
        self.assertEqual(
            [match["id"] for match in plan["matched_routes"]],
            [self.ZEBRA[0], self.MIDDLE[0], self.ALPHA[0]],
        )
        # The declared route is skipped, and skipping it does not disturb the
        # relative order of the two that remain.
        self.assertEqual(plan[SIGNAL], [self.ZEBRA[0], self.ALPHA[0]])
        self.assertNotEqual(plan[SIGNAL], sorted(plan[SIGNAL]))

    def test_an_undeclared_route_that_does_not_match_is_not_reported(self) -> None:
        """Scoping: the signal reads `matched_routes`, never `config["routes"]`.
        An implementation scanning the whole configuration would fire on every
        plan for any project with one shapeless overlay route -- a permanently
        on signal, which is the failure a consumer would notice first.
        """
        config = _config_with_overlay_route()
        plan = _plan(config, task="update the terraform module", changed_files=["main.tf"])
        matched = [match["id"] for match in plan["matched_routes"]]
        self.assertTrue(matched, "fixture must match at least one base route")
        self.assertNotIn(OVERLAY_ROUTE_ID, matched)
        self.assertNotIn(SIGNAL, plan)


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
        # A plan with nothing matched is needs-triage, not an `unclassified`
        # delivery shape -- pinned here so the no-match case cannot silently
        # start resolving through the shape fallback this signal reports on.
        self.assertEqual(plan["workflow"], "needs-triage")
        self.assertEqual(plan["status"], "needs-triage")

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

    def _pinned_v5_schema(self) -> dict:
        """A stand-in for the copy of `selection.schema.json` a consumer
        vendored at the previous release: the current schema minus this
        change's two deltas. Reconstructed rather than read from git so the
        test does not depend on a hardcoded commit or on clone depth.
        """
        schema = json.loads(SCHEMA_PATH.read_text(encoding="utf-8"))
        schema["properties"]["schema_version"]["const"] = 5
        del schema["properties"][SIGNAL]
        return schema

    def test_schema_version_is_6(self) -> None:
        """Adding an emitted field bumps the version -- RUNBOOK.md's "When
        `schema_version` increments" rule. Optional-and-omitted-when-empty is
        not the carve-out that rule grants, because an overlay consumer
        receives this field unconditionally. Pinned so the schema constant and
        the producer cannot drift apart, in either direction.
        """
        self.assertEqual(self.schema["properties"]["schema_version"]["const"], 6)
        self.assertEqual(_plan(load_routing(ROUTING_PATH))["schema_version"], 6)
        self.assertEqual(_plan(_config_with_overlay_route())["schema_version"], 6)

    def test_signal_is_optional_but_that_did_not_make_it_additive(self) -> None:
        self.assertIn(SIGNAL, self.schema["properties"])
        self.assertNotIn(SIGNAL, self.schema["required"])
        # The closedness that makes an unversioned field addition a breaking
        # change in the first place.
        self.assertFalse(self.schema["additionalProperties"])

    @unittest.skipUnless(JSONSCHEMA_AVAILABLE, "jsonschema is not installed")
    def test_plans_validate_with_and_without_the_signal(self) -> None:
        for label, config in (
            ("undeclared", _config_with_overlay_route()),
            ("declared", _config_with_overlay_route(workflow_shape="new-service")),
            ("base", load_routing(ROUTING_PATH)),
        ):
            with self.subTest(case=label):
                jsonschema.validate(instance=_plan(config), schema=self.schema)

    @unittest.skipUnless(JSONSCHEMA_AVAILABLE, "jsonschema is not installed")
    def test_a_pinned_v5_consumer_fails_on_the_version_not_on_the_new_property(self) -> None:
        """The reason the bump is right, asserted rather than argued.

        A consumer pinned to the previous schema copy rejects this plan either
        way -- the schema is closed. What the bump changes is *which* error
        they get: at 6 the failure names `schema_version`, the real cause; at 5
        it would have named `additionalProperties` while the plan truthfully
        reported the version their copy claims to handle, sending them to
        debug the wrong thing.
        """
        plan = _plan(_config_with_overlay_route())
        self.assertIn(SIGNAL, plan)
        pinned = self._pinned_v5_schema()

        errors = list(jsonschema.Draft202012Validator(pinned).iter_errors(plan))
        self.assertTrue(
            any(
                error.validator == "const" and list(error.absolute_path) == ["schema_version"]
                for error in errors
            ),
            f"expected a schema_version const failure, got {[e.validator for e in errors]}",
        )

        # The counterfactual: the same plan mislabelled as v5, i.e. what
        # shipping this field without a bump would have produced. The only
        # complaint is about the unknown property -- an error naming a symptom
        # rather than the version mismatch that actually caused it.
        mislabelled = {**plan, "schema_version": 5}
        errors = list(jsonschema.Draft202012Validator(pinned).iter_errors(mislabelled))
        self.assertTrue(errors, "a pinned v5 copy must still reject the unknown property")
        self.assertEqual({error.validator for error in errors}, {"additionalProperties"})


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


class OverlayMergeRuleTests(unittest.TestCase):
    """Pins the `workflow_shape` bullet added to RUNBOOK.md's per-construct
    overlay merge-rule list, so the documentation and `_apply_widen_patch`
    cannot drift apart. Nothing here changes overlay behavior -- it asserts
    what the deny-by-default rule already does.
    """

    def test_an_overlay_may_not_change_a_base_routes_workflow_shape(self) -> None:
        base = load_routing(ROUTING_PATH)
        route = next(r for r in base["routes"] if r["id"] == "infrastructure")
        self.assertEqual(route["workflow_shape"], "infrastructure-change")
        with self.assertRaises(RoutingOverlayError) as raised:
            merge_routing(
                base, {"routes": [{"id": "infrastructure", "workflow_shape": "new-service"}]}
            )
        self.assertIn("workflow_shape", str(raised.exception))

    def test_an_overlay_may_restate_a_base_routes_workflow_shape_as_a_no_op(self) -> None:
        base = load_routing(ROUTING_PATH)
        effective = merge_routing(
            base, {"routes": [{"id": "infrastructure", "workflow_shape": "infrastructure-change"}]}
        )
        merged = next(r for r in effective["routes"] if r["id"] == "infrastructure")
        self.assertEqual(merged["workflow_shape"], "infrastructure-change")

    def test_a_new_overlay_route_may_declare_its_own_workflow_shape(self) -> None:
        base = load_routing(ROUTING_PATH)
        effective = merge_routing(
            base,
            {"routes": [_overlay_route(OVERLAY_ROUTE_ID, OVERLAY_KEYWORD, workflow_shape="new-service")]},
        )
        added = next(r for r in effective["routes"] if r["id"] == OVERLAY_ROUTE_ID)
        self.assertEqual(added["workflow_shape"], "new-service")

    def test_a_new_overlay_route_may_omit_workflow_shape_entirely(self) -> None:
        """The compatibility guarantee this whole issue rests on: omitting the
        field must stay legal, or every overlay written before #210 breaks.
        The signal reports it; the merge does not reject it.
        """
        base = load_routing(ROUTING_PATH)
        effective = merge_routing(
            base, {"routes": [_overlay_route(OVERLAY_ROUTE_ID, OVERLAY_KEYWORD)]}
        )
        added = next(r for r in effective["routes"] if r["id"] == OVERLAY_ROUTE_ID)
        self.assertNotIn("workflow_shape", added)


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
