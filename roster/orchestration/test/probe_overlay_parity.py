#!/usr/bin/env python3
"""Differential probe for the routing-overlay merge.

Runs a corpus of overlay documents -- legal, illegal, and malformed -- through
both implementations against this repository's real routing.json, and reports
every disagreement in either the merged result or the refusal.

    python3 roster/orchestration/test/probe_overlay_parity.py

Both halves are compared, and the refusals matter more than the merges. An
overlay's rules are what stop a project weakening a route's reviewers or
switching off a gate the base insists on, so a port that merged the legal
documents perfectly while accepting one document Python rejects would have
lost the entire point of the mechanism.

The corpus leans on the cases where "looks additive" and "is additive" come
apart -- adding an outer keyword_group narrows matching, and a superset of
exclude_paths narrows it too -- because those are where a reasonable
reimplementation goes wrong while looking right.
"""

from __future__ import annotations

import copy
import json
import subprocess
import sys
import tempfile
from pathlib import Path

REPOSITORY_ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(REPOSITORY_ROOT / "roster" / "orchestration" / "src"))
sys.path.insert(0, str(REPOSITORY_ROOT / "roster" / "shared" / "src"))

import routing_overlay  # noqa: E402
from routing import validate_routing_config  # noqa: E402

BASE_PATH = REPOSITORY_ROOT / "roster" / "orchestration" / "routing.json"
BASE = json.loads(BASE_PATH.read_text(encoding="utf-8"))


def a_route() -> dict:
    """A base route with keywords and paths, to widen and to violate."""
    for route in BASE["routes"]:
        if route.get("keywords") and route.get("paths"):
            return route
    raise SystemExit("no base route with both keywords and paths")


def a_keyword_group_route() -> dict | None:
    for route in [*BASE["routes"], *BASE["risk_rules"]]:
        if route.get("keyword_groups"):
            return route
    return None


ROUTE = a_route()
GROUP_ROUTE = a_keyword_group_route()
RECIPE = (BASE.get("team_recipes") or [None])[0]


def case(why: str, overlay: object, *, raw: str | None = None) -> dict:
    return {"why": why, "text": raw if raw is not None else json.dumps(overlay)}


def build_corpus() -> list[dict]:
    cases: list[dict] = []
    route_id = ROUTE["id"]

    # --- documents that must be accepted --------------------------------
    cases.append(case("empty overlay is a no-op", {}))
    cases.append(case("restating version is a permitted no-op", {"version": BASE.get("version")}))
    cases.append(case(
        "a brand-new route with a fresh id",
        {"routes": [{"id": "probe-new-route", "keywords": ["probe"], "paths": ["probe/**"],
                     "primary": "backend-engineer", "reviewers": ["code-reviewer"]}]},
    ))
    cases.append(case(
        "a new route with no workflow_shape: reported, not rejected",
        {"routes": [{"id": "probe-no-shape", "keywords": ["probe"], "primary": "backend-engineer"}]},
    ))
    cases.append(case(
        "widening keywords by appending",
        {"routes": [{"id": route_id, "keywords": [*ROUTE["keywords"], "probe-extra-keyword"]}]},
    ))
    cases.append(case(
        "widening paths by appending",
        {"routes": [{"id": route_id, "paths": [*ROUTE["paths"], "probe/**"]}]},
    ))
    cases.append(case(
        "restating keywords exactly is a permitted no-op",
        {"routes": [{"id": route_id, "keywords": list(ROUTE["keywords"])}]},
    ))
    cases.append(case(
        "reordering keywords while keeping every element",
        {"routes": [{"id": route_id, "keywords": list(reversed(ROUTE["keywords"]))}]},
    ))
    cases.append(case(
        "duplicate entries in a widened list are collapsed",
        {"routes": [{"id": route_id, "keywords": [*ROUTE["keywords"], "dup", "dup"]}]},
    ))
    cases.append(case(
        "restating an immutable field with its exact value",
        {"routes": [{"id": route_id, "primary": ROUTE.get("primary"),
                     "keywords": [*ROUTE["keywords"], "probe-extra"]}]},
    ))
    cases.append(case("change_intake additive keywords", {"change_intake": {"keywords": ["probe-intake"]}}))
    cases.append(case("change_intake additive agents", {"change_intake": {"agents": ["code-reviewer"]}}))
    cases.append(case(
        "change_intake addition already present is not duplicated",
        {"change_intake": {"keywords": list(BASE.get("change_intake", {}).get("keywords", [])[:1])}},
    ))
    cases.append(case("cross_stack additive route_ids", {"cross_stack": {"route_ids": ["probe-route"]}}))
    cases.append(case("cross_stack minimum_matches may decrease", {"cross_stack": {"minimum_matches": 1}}))
    cases.append(case(
        "cross_stack minimum_matches restated at the base value",
        {"cross_stack": {"minimum_matches": BASE.get("cross_stack", {}).get("minimum_matches")}},
    ))
    cases.append(case("knowledge_focus deep-merges, overlay wins", {"knowledge_focus": {"probe-agent": "probe focus"}}))
    cases.append(case(
        "knowledge_focus may overwrite an existing key: it carries no gating semantics",
        {"knowledge_focus": {(list(BASE.get("knowledge_focus", {}) or {"x": 1}))[0]: "replaced"}},
    ))
    cases.append(case("ignored_gates may shrink to empty", {"ignored_gates": []}))
    cases.append(case(
        "ignored_gates restated in full",
        {"ignored_gates": list(BASE.get("ignored_gates", []))},
    ))
    cases.append(case("explicit nulls are treated as absent", {"routes": None, "risk_rules": None}))
    cases.append(case(
        "a new team recipe with a fresh id",
        {"team_recipes": [{"id": "probe-team", "type": "fixed", "route_ids": ["probe-route"],
                           "minimum_matches": 2, "members": ["code-reviewer", "security-reviewer"],
                           "communication_mode": "peer", "fallback": "orchestrator-relayed",
                           "description": "probe"}]},
    ))

    # --- documents that must be refused ---------------------------------
    cases.append(case("unknown top-level field", {"nonsense": 1}))
    cases.append(case("two unknown top-level fields: the report is sorted", {"zebra": 1, "alpha": 2}))
    cases.append(case("version may not change", {"version": 99}))
    cases.append(case("overlay root must be an object", [1, 2, 3]))
    cases.append(case("malformed JSON", None, raw="{not json"))
    cases.append(case("routes must be a list", {"routes": {"id": "x"}}))
    cases.append(case("route entries must be objects", {"routes": ["not-an-object"]}))
    cases.append(case("route entry needs a non-empty id", {"routes": [{"keywords": ["x"]}]}))
    cases.append(case("route entry id must be a string", {"routes": [{"id": 7}]}))
    cases.append(case("route entry id must be non-empty", {"routes": [{"id": ""}]}))
    cases.append(case(
        "narrowing keywords by dropping one",
        {"routes": [{"id": route_id, "keywords": ROUTE["keywords"][:-1]}]},
    ))
    cases.append(case(
        "replacing keywords wholesale",
        {"routes": [{"id": route_id, "keywords": ["only-this"]}]},
    ))
    cases.append(case(
        "emptying keywords",
        {"routes": [{"id": route_id, "keywords": []}]},
    ))
    cases.append(case(
        "narrowing paths by dropping one",
        {"routes": [{"id": route_id, "paths": ROUTE["paths"][:-1]}]},
    ))
    cases.append(case(
        "a widen field supplied as a non-list",
        {"routes": [{"id": route_id, "keywords": "a string"}]},
    ))
    cases.append(case(
        "changing reviewers -- the field the whole mechanism protects",
        {"routes": [{"id": route_id, "reviewers": []}]},
    ))
    cases.append(case(
        "changing human_gate",
        {"routes": [{"id": route_id, "human_gate": False}]},
    ))
    cases.append(case(
        "changing primary",
        {"routes": [{"id": route_id, "primary": "probe-agent"}]},
    ))
    cases.append(case(
        "adding exclude_paths to a base entry narrows it, so it is immutable",
        {"routes": [{"id": route_id, "exclude_paths": ["**/vendor/**"]}]},
    ))
    cases.append(case(
        "an id colliding with an existing route",
        {"risk_rules": [{"id": route_id, "keywords": ["x"]}]},
    ))
    cases.append(case("ignored_gates may not grow", {"ignored_gates": [*BASE.get("ignored_gates", []), "probe-gate"]}))
    cases.append(case("ignored_gates must be a list", {"ignored_gates": "gate"}))
    cases.append(case("change_intake unknown field", {"change_intake": {"nonsense": []}}))
    cases.append(case("change_intake must be an object", {"change_intake": []}))
    cases.append(case("change_intake field must be a list", {"change_intake": {"keywords": "x"}}))
    cases.append(case("cross_stack unknown field", {"cross_stack": {"nonsense": 1}}))
    cases.append(case("cross_stack must be an object", {"cross_stack": []}))
    cases.append(case("cross_stack minimum_matches may not increase", {"cross_stack": {"minimum_matches": 99}}))
    cases.append(case("cross_stack minimum_matches must be an integer", {"cross_stack": {"minimum_matches": "2"}}))
    cases.append(case(
        "cross_stack minimum_matches as a float literal is not an integer",
        None, raw='{"cross_stack": {"minimum_matches": 1.0}}',
    ))
    cases.append(case(
        "cross_stack minimum_matches as a bool is not an integer",
        {"cross_stack": {"minimum_matches": True}},
    ))
    cases.append(case("knowledge_focus must be an object", {"knowledge_focus": []}))

    if RECIPE is not None:
        cases.append(case(
            "a base team recipe is fully immutable, even restated",
            {"team_recipes": [dict(RECIPE)]},
        ))
        cases.append(case(
            "a base team recipe may not be widened either",
            {"team_recipes": [{"id": RECIPE["id"], "description": "changed"}]},
        ))
    cases.append(case("team recipe entries must be objects", {"team_recipes": ["x"]}))
    cases.append(case("team recipe needs a non-empty id", {"team_recipes": [{"type": "fixed"}]}))
    cases.append(case(
        "a team recipe id colliding with a route id",
        {"team_recipes": [{"id": route_id, "type": "fixed"}]},
    ))

    # --- merge is legal, effective config is not -------------------------
    cases.append(case(
        "a new route with a misspelled workflow_shape",
        {"routes": [{"id": "probe-bad-shape", "keywords": ["x"], "workflow_shape": "not-a-shape"}]},
    ))
    cases.append(case(
        "a new route whose id collides with a context pack",
        {"routes": [{"id": (BASE.get("context_packs") or [{"id": "none"}])[0]["id"], "keywords": ["x"]}]},
    ))
    cases.append(case(
        "a new dynamic team recipe with min > max",
        {"team_recipes": [{"id": "probe-dynamic", "type": "dynamic", "role": "debugging-engineer",
                           "instances": {"min": 5, "max": 2}, "keywords": ["x"],
                           "communication_mode": "peer", "fallback": "f", "description": "d"}]},
    ))
    cases.append(case(
        "a new route with an empty keyword_groups inner list",
        {"routes": [{"id": "probe-empty-group", "keyword_groups": [[]]}]},
    ))

    # --- keyword_groups: the AND-of-ORs trap ----------------------------
    if GROUP_ROUTE is not None:
        groups = GROUP_ROUTE["keyword_groups"]
        gid = GROUP_ROUTE["id"]
        section = "routes" if any(r["id"] == gid for r in BASE["routes"]) else "risk_rules"

        widened = copy.deepcopy(groups)
        widened[0] = [*widened[0], "probe-extra"]
        cases.append(case(
            "adding a keyword to an EXISTING group widens the OR-clause: legal",
            {section: [{"id": gid, "keyword_groups": widened}]},
        ))
        cases.append(case(
            "restating keyword_groups exactly",
            {section: [{"id": gid, "keyword_groups": copy.deepcopy(groups)}]},
        ))
        cases.append(case(
            "appending an outer group LOOKS additive but adds a mandatory "
            "AND-condition, so it narrows matching: illegal",
            {section: [{"id": gid, "keyword_groups": [*copy.deepcopy(groups), ["probe-new-group"]]}]},
        ))
        cases.append(case(
            "dropping an outer group",
            {section: [{"id": gid, "keyword_groups": copy.deepcopy(groups[:-1])}]},
        ))
        narrowed = copy.deepcopy(groups)
        if len(narrowed[0]) > 1:
            narrowed[0] = narrowed[0][:-1]
            cases.append(case(
                "removing a keyword from an existing group's inner OR-list",
                {section: [{"id": gid, "keyword_groups": narrowed}]},
            ))
        cases.append(case(
            "an inner group supplied as a non-list",
            {section: [{"id": gid, "keyword_groups": ["not-a-list", *copy.deepcopy(groups[1:])]}]},
        ))

    return cases


def python_answer(text: str) -> dict[str, str]:
    try:
        loaded = json.loads(text)
    except json.JSONDecodeError:
        return {"merged": "", "error": "malformed overlay JSON"}
    if not isinstance(loaded, dict):
        return {"merged": "", "error": "overlay root must be a JSON object"}
    try:
        merged = routing_overlay.merge_routing(copy.deepcopy(BASE), loaded)
    except routing_overlay.RoutingOverlayError as error:
        return {"merged": "", "error": str(error)}
    try:
        validate_routing_config(merged)
    except ValueError as error:
        return {"merged": "", "error": f"effective configuration failed validation: {error}"}
    return {"merged": json.dumps(merged, sort_keys=True, separators=(",", ":")), "error": ""}


def main() -> int:
    cases = build_corpus()
    print(f"corpus: {len(cases)} overlay documents")

    with tempfile.TemporaryDirectory() as workspace:
        input_path = Path(workspace) / "overlays.json"
        output_path = Path(workspace) / "answers.json"
        input_path.write_text(json.dumps([case["text"] for case in cases]))

        result = subprocess.run(
            ["go", "test", "./internal/selector/", "-run", "TestOverlayParityProbe", "-count=1", "-v"],
            cwd=REPOSITORY_ROOT, capture_output=True, text=True,
            env={**__import__("os").environ, "CGO_ENABLED": "1",
                 "CADRE_OVERLAY_PROBE_IN": str(input_path),
                 "CADRE_OVERLAY_PROBE_OUT": str(output_path),
                 "CADRE_OVERLAY_PROBE_BASE": str(BASE_PATH)},
        )
        if result.returncode != 0:
            sys.stderr.write(result.stdout + result.stderr)
            return 1
        go_answers = json.loads(output_path.read_text())

    accepted = refused = differing = 0
    for index, this_case in enumerate(cases):
        expected = python_answer(this_case["text"])
        actual = go_answers[index]
        if expected["error"]:
            refused += 1
        else:
            accepted += 1

        if expected["merged"] == actual["merged"] and expected["error"] == actual["error"]:
            continue
        differing += 1
        print(f"\n  DIFFERS [#{index}] {this_case['why']}")
        if (expected["error"] == "") != (actual["error"] == ""):
            print("    ONE ACCEPTED WHAT THE OTHER REFUSED")
        if expected["error"] != actual["error"]:
            print(f"    error:\n      python: {expected['error']!r}\n      go:     {actual['error']!r}")
        if expected["merged"] != actual["merged"]:
            print("    merged configs differ")
            left = json.loads(expected["merged"]) if expected["merged"] else {}
            right = json.loads(actual["merged"]) if actual["merged"] else {}
            for key in sorted(set(left) | set(right)):
                if left.get(key) != right.get(key):
                    print(f"      {key}: python={str(left.get(key))[:200]}")
                    print(f"      {key}: go=    {str(right.get(key))[:200]}")

    print(f"\n  {len(cases)} documents: {accepted} accepted, {refused} refused")
    print(f"  {len(cases) - differing} identical, {differing} differing")
    if differing:
        print("\nFAIL")
        return 1
    print("\nOK")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
