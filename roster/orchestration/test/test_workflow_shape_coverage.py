"""Every declared `workflow_shape` must be pinned by at least one fixture.

`test_selector.py::WorkflowShapeDeclarationTests` asserts that all 146 routes
in routing.json declare a shape. That is a completeness check on the *file*,
and it measures the wrong thing on its own: a route can declare a shape that
no test would notice being changed, because every input that matches it also
matches a broad route (`frontend`/`backend`) already contributing
`new-service`. The declaration is then decorative -- freely editable to any
legal value with a fully green suite -- which is exactly the silent-editing
problem #210 set out to remove, one level up from where it was fixed.

Appearing in a fixture is not the same as being pinned by one. This module
measures the difference the only way it can be measured: substitute each
route's declared shape for every other legal value, re-run the fixtures that
match that route, and require at least one substitution to move some
fixture's `workflow`.

Scope: routes declaring a non-`unclassified` shape. A route declaring
`unclassified` contributes nothing by definition, so most substitutions of it
are unobservable, and requiring a pin for all 146 costs roughly four times
the runtime to guard declarations that carry no behavior. That is a deliberate
boundary, not an oversight -- a route moving *off* `unclassified` lands in
scope automatically.

Fixtures come from both fixture files (selection_golden_corpus.json and
workflow_fitness_table.json), so a pin may live in either: the corpus for a
regression baseline, the fitness table for an independently reasoned one.
"""

from __future__ import annotations

import copy
import json
import sys
import unittest
from pathlib import Path
from typing import Any
from unittest import mock

ROOT = Path(__file__).resolve().parent.parent
AGENTS_ROOT = ROOT.parent
FIXTURES = Path(__file__).resolve().parent / "fixtures"

sys.path.insert(0, str(ROOT / "src"))

import build_dispatch_plan as build_dispatch_plan_module  # noqa: E402
from build_dispatch_plan import build_dispatch_plan  # noqa: E402
from routing import (  # noqa: E402
    WORKFLOW_SHAPES,
    load_catalog,
    load_routing,
    match_rule,
)

CONFIG = load_routing(ROOT / "routing.json")
CATALOG = load_catalog(AGENTS_ROOT / "catalog.yaml")

# One route cannot be pinned by any input, and it is exempted here by name
# rather than by weakening the check for everyone.
#
# `prompt-artifact-execution`'s entire trigger surface is a subset of the
# `ai-feature` route's: all three of its keywords contain "prompt", an
# ai-feature keyword, and both of its path globs sit under ai-feature's
# `**/prompts/**`. So every input that matches it also matches a route
# already contributing new-service, and substituting its declared shape can
# never move any plan's `workflow`. No fixture can fix that; only a routing
# change could.
#
# The exemption is not taken on trust: SubsumedRouteExemptionTests below
# re-derives the premise from routing.json on every run. If ai-feature is
# ever narrowed the way the frontend route was in #207 -- the change that
# surfaced this whole defect class -- that test fails, and this route has to
# earn a real pin instead of keeping an exemption whose reason expired.
STRUCTURALLY_UNPINNABLE = {"prompt-artifact-execution": "ai-feature"}


def _inputs() -> list[dict[str, Any]]:
    """Every fixture input in both fixture files, deduplicated."""
    cases: list[dict[str, Any]] = []
    seen: set[tuple[str, tuple[str, ...], str]] = set()
    for path in (FIXTURES / "selection_golden_corpus.json", FIXTURES / "workflow_fitness_table.json"):
        for case in json.loads(path.read_text(encoding="utf-8"))["cases"]:
            classification = case.get("classification", "internal")
            key = (case["task"], tuple(case["changed_files"]), classification)
            if key in seen:
                continue
            seen.add(key)
            cases.append(
                {
                    "id": f"{path.stem}:{case['id']}",
                    "task": case["task"],
                    "changed_files": case["changed_files"],
                    "changed_file_source": "test",
                    "repository_root": str(AGENTS_ROOT.parent),
                    "source": "example/repository",
                    "classification": classification,
                    "task_id": case["task_id"],
                }
            )
    return cases


INPUTS = _inputs()


def _run(config: dict[str, Any], values: dict[str, Any]) -> dict[str, Any]:
    # Standalone mode, matching both fixture harnesses: without it a host with
    # the Agentic SDLC kernel resolvable pulls extra gate agents into the plan.
    with mock.patch.object(build_dispatch_plan_module, "try_lifecycle_contract", return_value=None):
        return build_dispatch_plan(config, CATALOG, values)


class WorkflowShapeDecisivenessTests(unittest.TestCase):
    def test_every_declared_delivery_shape_changes_some_fixture_when_substituted(self) -> None:
        in_scope = [
            route
            for route in CONFIG["routes"]
            if route["workflow_shape"] != "unclassified" and route["id"] not in STRUCTURALLY_UNPINNABLE
        ]
        # Substituting a route's shape can only move an input whose plan
        # matched that route, so resolve matching first and build a full
        # baseline plan only for the inputs that survive. Match with
        # `match_rule` against the in-scope routes only, not `match_routes`
        # against all 146: it is the same predicate `match_routes` applies
        # per route, so the two cannot disagree, and restricting it to the
        # routes under test is what keeps this affordable.
        covering_by_route = {
            route["id"]: [
                values
                for values in INPUTS
                if match_rule(route, values["task"], values["changed_files"])["matched"]
            ]
            for route in in_scope
        }
        in_scope_hits: dict[str, int] = {}
        for covered in covering_by_route.values():
            for values in covered:
                in_scope_hits[values["id"]] = in_scope_hits.get(values["id"], 0) + 1
        relevant = [values for values in INPUTS if values["id"] in in_scope_hits]
        baseline = {values["id"]: _run(CONFIG, values)["workflow"] for values in relevant}

        working = copy.deepcopy(CONFIG)
        routes_by_id = {route["id"]: route for route in working["routes"]}

        unpinned: list[str] = []
        for route in in_scope:
            declared = route["workflow_shape"]
            # Fewest co-matched in-scope routes first: an input where this
            # route is the only one claiming a shape is the one most likely
            # to be decisive, and finding a decisive input early
            # short-circuits the remaining substitutions. Ordering is a speed
            # heuristic only -- the verdict is identical in any order.
            covering = sorted(
                covering_by_route[route["id"]],
                key=lambda values: in_scope_hits[values["id"]],
            )
            decisive = False
            for substitute in sorted(WORKFLOW_SHAPES - {declared}):
                routes_by_id[route["id"]]["workflow_shape"] = substitute
                try:
                    for values in covering:
                        if _run(working, values)["workflow"] != baseline[values["id"]]:
                            decisive = True
                            break
                finally:
                    routes_by_id[route["id"]]["workflow_shape"] = declared
                if decisive:
                    break
            if not decisive:
                unpinned.append(route["id"])

        self.assertEqual(
            unpinned,
            [],
            "these routes declare a delivery shape that no fixture pins -- the value can be "
            "changed to anything legal with a fully green suite. Add a fixture (either file) "
            "whose matched routes make the declaration decisive, i.e. one where no co-matched "
            "route already contributes the same shape: " + ", ".join(unpinned),
        )


class SubsumedRouteExemptionTests(unittest.TestCase):
    """Re-derive every STRUCTURALLY_UNPINNABLE exemption's premise from routing.json.

    An allowlist entry whose reason has quietly expired is worse than no
    allowlist: it looks examined. Each assertion below states one half of
    "this route cannot be decisive", so the exemption survives only as long
    as the routing facts that justify it do.
    """

    def test_each_exempt_route_is_wholly_subsumed_by_its_broad_route(self) -> None:
        routes = {route["id"]: route for route in CONFIG["routes"]}
        for exempt_id, broad_id in STRUCTURALLY_UNPINNABLE.items():
            with self.subTest(route=exempt_id):
                exempt, broad = routes[exempt_id], routes[broad_id]

                # If the broad route stopped contributing a shape, the exempt
                # route's declaration would become decisive and pinnable.
                self.assertNotEqual(
                    broad["workflow_shape"],
                    "unclassified",
                    f"{broad_id} no longer contributes a shape, so {exempt_id} can now be pinned",
                )

                # Every keyword: matching the exempt route by keyword must
                # also match the broad route.
                for keyword in exempt["keywords"]:
                    self.assertTrue(
                        match_rule(broad, keyword, [])["matched"],
                        f"{exempt_id} keyword {keyword!r} no longer matches {broad_id}; "
                        f"it can now be isolated, so remove the exemption and add a pin",
                    )
                self.assertEqual(exempt.get("keyword_groups", []), [])

                # Every path glob: a representative path under it must also
                # match the broad route. Probes are derived from the route's
                # own globs, so adding a glob without a probe fails here
                # rather than silently going unchecked.
                probes = {
                    glob: glob.replace("**", "probe/nested") + ("/probe.txt" if glob.endswith("**") else "")
                    for glob in exempt["paths"]
                }
                for glob, probe in probes.items():
                    self.assertTrue(
                        match_rule(exempt, "", [probe])["matched"],
                        f"probe {probe!r} no longer matches {exempt_id}'s own glob {glob!r}",
                    )
                    self.assertTrue(
                        match_rule(broad, "", [probe])["matched"],
                        f"{exempt_id} path {glob!r} is no longer covered by {broad_id}; "
                        f"it can now be isolated, so remove the exemption and add a pin",
                    )


if __name__ == "__main__":
    unittest.main()
