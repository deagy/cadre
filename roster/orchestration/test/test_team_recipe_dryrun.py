"""Tests for the team-recipe dry-run visualizer.

Covers `roster/orchestration/src/team_recipe_dryrun.py`: a fixed recipe that
fires (enough matched routes and selected members), one that doesn't (too
few matched routes), a dynamic recipe that fires (role selected, required
route matched, a keyword hits), one that doesn't (no keyword hits), and a
run against the real current `routing.yaml` proving the tool doesn't crash
and produces a `fires: bool` verdict with reasoning for every real recipe.
"""

from __future__ import annotations

import sys
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
AGENTS_ROOT = ROOT.parent
sys.path.insert(0, str(ROOT / "src"))

from build_dispatch_plan import build_dispatch_plan  # noqa: E402
from routing import load_catalog, load_routing  # noqa: E402
from team_recipe_dryrun import (  # noqa: E402
    _resolve_synthetic_mode_signals,
    expand_recipe_to_members,
    explain_dynamic_recipe,
    explain_fixed_recipe,
    explain_recipes,
    main,
)

CONFIG = load_routing(ROOT / "routing.yaml")
CATALOG = load_catalog(AGENTS_ROOT / "catalog.yaml")

FIXED_RECIPE = next(recipe for recipe in CONFIG["team_recipes"] if recipe["type"] == "fixed")
DYNAMIC_RECIPE = next(recipe for recipe in CONFIG["team_recipes"] if recipe["type"] == "dynamic")


class FixedRecipeExplanationTests(unittest.TestCase):
    def test_fires_when_routes_and_members_both_satisfied(self) -> None:
        matched_route_ids = set(FIXED_RECIPE["route_ids"][: FIXED_RECIPE["minimum_matches"]])
        minimum_members = FIXED_RECIPE.get("minimum_members_selected", 2)
        selected_agents = set(FIXED_RECIPE["members"][:minimum_members])

        explanation = explain_fixed_recipe(FIXED_RECIPE, matched_route_ids, selected_agents)

        self.assertTrue(explanation["fires"])
        self.assertTrue(explanation["routes"]["satisfied"])
        self.assertTrue(explanation["members"]["satisfied"])
        self.assertEqual(explanation["routes"]["actual_matches"], len(matched_route_ids))
        self.assertEqual(sorted(explanation["members"]["selected_members"]), sorted(selected_agents))

    def test_does_not_fire_when_too_few_routes_match(self) -> None:
        # One fewer than minimum_matches -- routes condition alone must fail
        # even though every member is selected.
        matched_route_ids = set(FIXED_RECIPE["route_ids"][: FIXED_RECIPE["minimum_matches"] - 1])
        selected_agents = set(FIXED_RECIPE["members"])

        explanation = explain_fixed_recipe(FIXED_RECIPE, matched_route_ids, selected_agents)

        self.assertFalse(explanation["fires"])
        self.assertFalse(explanation["routes"]["satisfied"])
        self.assertTrue(explanation["members"]["satisfied"])
        self.assertEqual(
            explanation["routes"]["actual_matches"], FIXED_RECIPE["minimum_matches"] - 1
        )
        self.assertIn(explanation["routes"]["actual_matches"], range(FIXED_RECIPE["minimum_matches"]))

    def test_does_not_fire_when_too_few_members_selected(self) -> None:
        matched_route_ids = set(FIXED_RECIPE["route_ids"])
        selected_agents: set[str] = set()

        explanation = explain_fixed_recipe(FIXED_RECIPE, matched_route_ids, selected_agents)

        self.assertFalse(explanation["fires"])
        self.assertTrue(explanation["routes"]["satisfied"])
        self.assertFalse(explanation["members"]["satisfied"])
        self.assertEqual(explanation["members"]["selected_members"], [])
        self.assertEqual(sorted(explanation["members"]["unselected_members"]), sorted(FIXED_RECIPE["members"]))

    def test_unmatched_routes_reported_as_complement(self) -> None:
        matched_route_ids = {FIXED_RECIPE["route_ids"][0]}
        explanation = explain_fixed_recipe(FIXED_RECIPE, matched_route_ids, set())
        self.assertEqual(
            sorted(explanation["routes"]["unmatched_route_ids"]),
            sorted(set(FIXED_RECIPE["route_ids"]) - matched_route_ids),
        )


class DynamicRecipeExplanationTests(unittest.TestCase):
    def test_fires_when_role_route_and_keyword_all_satisfied(self) -> None:
        role = DYNAMIC_RECIPE["role"]
        requires_route = DYNAMIC_RECIPE.get("requires_route")
        matched_route_ids = {requires_route} if requires_route else set()
        keyword = DYNAMIC_RECIPE["keywords"][0]

        explanation = explain_dynamic_recipe(
            DYNAMIC_RECIPE, matched_route_ids, {role}, f"debugging this: {keyword} behavior"
        )

        self.assertTrue(explanation["fires"])
        self.assertTrue(explanation["role"]["selected"])
        self.assertTrue(explanation["requires_route"]["matched"])
        self.assertTrue(explanation["keywords"]["satisfied"])
        self.assertIn(keyword, explanation["keywords"]["matched_keywords"])

    def test_does_not_fire_without_a_keyword_match(self) -> None:
        role = DYNAMIC_RECIPE["role"]
        requires_route = DYNAMIC_RECIPE.get("requires_route")
        matched_route_ids = {requires_route} if requires_route else set()

        explanation = explain_dynamic_recipe(
            DYNAMIC_RECIPE, matched_route_ids, {role}, "plain unrelated task text with no signal"
        )

        self.assertFalse(explanation["fires"])
        self.assertTrue(explanation["role"]["selected"])
        self.assertTrue(explanation["requires_route"]["matched"])
        self.assertFalse(explanation["keywords"]["satisfied"])
        self.assertEqual(explanation["keywords"]["matched_keywords"], [])
        self.assertEqual(
            sorted(explanation["keywords"]["unmatched_keywords"]), sorted(DYNAMIC_RECIPE["keywords"])
        )

    def test_does_not_fire_when_role_not_selected(self) -> None:
        requires_route = DYNAMIC_RECIPE.get("requires_route")
        matched_route_ids = {requires_route} if requires_route else set()
        keyword = DYNAMIC_RECIPE["keywords"][0]

        explanation = explain_dynamic_recipe(DYNAMIC_RECIPE, matched_route_ids, set(), keyword)

        self.assertFalse(explanation["fires"])
        self.assertFalse(explanation["role"]["selected"])

    def test_does_not_fire_when_required_route_missing(self) -> None:
        role = DYNAMIC_RECIPE["role"]
        keyword = DYNAMIC_RECIPE["keywords"][0]

        explanation = explain_dynamic_recipe(DYNAMIC_RECIPE, set(), {role}, keyword)

        self.assertFalse(explanation["fires"])
        self.assertFalse(explanation["requires_route"]["matched"])

    def test_empty_keyword_list_can_never_fire(self) -> None:
        recipe = {**DYNAMIC_RECIPE, "keywords": []}
        role = recipe["role"]
        requires_route = recipe.get("requires_route")
        matched_route_ids = {requires_route} if requires_route else set()

        explanation = explain_dynamic_recipe(recipe, matched_route_ids, {role}, "anything at all")

        self.assertFalse(explanation["fires"])
        self.assertFalse(explanation["keywords"]["satisfied"])


class ExpandRecipeToMembersTests(unittest.TestCase):
    """expand_recipe_to_members(): the piece explain_fixed_recipe/
    explain_dynamic_recipe deliberately stop short of -- turning a fired
    recipe into a concrete [{role_id, brief}, ...] list ready for
    dispatch_team() (roster/orchestration/mcp/dispatch_core.py)."""

    def test_fixed_recipe_with_shared_brief(self) -> None:
        matched_route_ids = set(FIXED_RECIPE["route_ids"][: FIXED_RECIPE["minimum_matches"]])
        minimum_members = FIXED_RECIPE.get("minimum_members_selected", 2)
        selected_agents = set(FIXED_RECIPE["members"][:minimum_members])

        members = expand_recipe_to_members(
            CONFIG, FIXED_RECIPE["id"], matched_route_ids, selected_agents, shared_brief="do it"
        )

        self.assertEqual(len(members), minimum_members)
        for member in members:
            self.assertEqual(member["brief"], "do it")
            self.assertIn(member["role_id"], FIXED_RECIPE["members"])

    def test_fixed_recipe_per_member_brief_overrides_shared(self) -> None:
        matched_route_ids = set(FIXED_RECIPE["route_ids"][: FIXED_RECIPE["minimum_matches"]])
        minimum_members = FIXED_RECIPE.get("minimum_members_selected", 2)
        selected_members = FIXED_RECIPE["members"][:minimum_members]
        selected_agents = set(selected_members)
        override_role = selected_members[0]

        members = expand_recipe_to_members(
            CONFIG,
            FIXED_RECIPE["id"],
            matched_route_ids,
            selected_agents,
            shared_brief="default brief",
            member_briefs={override_role: "special brief"},
        )

        briefs = {member["role_id"]: member["brief"] for member in members}
        self.assertEqual(briefs[override_role], "special brief")
        for role_id, brief in briefs.items():
            if role_id != override_role:
                self.assertEqual(brief, "default brief")

    def test_fixed_recipe_missing_brief_for_a_member_raises(self) -> None:
        matched_route_ids = set(FIXED_RECIPE["route_ids"][: FIXED_RECIPE["minimum_matches"]])
        minimum_members = FIXED_RECIPE.get("minimum_members_selected", 2)
        selected_agents = set(FIXED_RECIPE["members"][:minimum_members])

        with self.assertRaises(ValueError):
            expand_recipe_to_members(CONFIG, FIXED_RECIPE["id"], matched_route_ids, selected_agents)

    def test_recipe_that_does_not_fire_is_refused(self) -> None:
        # One fewer than minimum_matches -- mirrors
        # FixedRecipeExplanationTests.test_does_not_fire_when_too_few_routes_match.
        matched_route_ids = set(FIXED_RECIPE["route_ids"][: FIXED_RECIPE["minimum_matches"] - 1])
        selected_agents = set(FIXED_RECIPE["members"])

        with self.assertRaises(ValueError):
            expand_recipe_to_members(
                CONFIG, FIXED_RECIPE["id"], matched_route_ids, selected_agents, shared_brief="x"
            )

    def test_unknown_recipe_id_raises(self) -> None:
        with self.assertRaises(ValueError):
            expand_recipe_to_members(CONFIG, "not-a-real-recipe", set(), set(), shared_brief="x")

    def _dynamic_signals(self) -> tuple[set[str], set[str], str]:
        role = DYNAMIC_RECIPE["role"]
        requires_route = DYNAMIC_RECIPE.get("requires_route")
        matched_route_ids = {requires_route} if requires_route else set()
        keyword = DYNAMIC_RECIPE["keywords"][0]
        return matched_route_ids, {role}, f"debugging this: {keyword} behavior"

    def test_dynamic_recipe_with_correctly_sized_instance_briefs(self) -> None:
        matched_route_ids, selected_agents, task_text = self._dynamic_signals()
        count = DYNAMIC_RECIPE["instances"]["min"]
        briefs = [f"hypothesis {i}" for i in range(count)]

        members = expand_recipe_to_members(
            CONFIG,
            DYNAMIC_RECIPE["id"],
            matched_route_ids,
            selected_agents,
            task_text,
            instance_count=count,
            instance_briefs=briefs,
        )

        self.assertEqual(len(members), count)
        self.assertTrue(all(member["role_id"] == DYNAMIC_RECIPE["role"] for member in members))
        self.assertEqual([member["brief"] for member in members], briefs)

    def test_dynamic_recipe_instance_count_below_minimum_raises(self) -> None:
        matched_route_ids, selected_agents, task_text = self._dynamic_signals()
        below_min = DYNAMIC_RECIPE["instances"]["min"] - 1

        with self.assertRaises(ValueError):
            expand_recipe_to_members(
                CONFIG,
                DYNAMIC_RECIPE["id"],
                matched_route_ids,
                selected_agents,
                task_text,
                instance_count=below_min,
                instance_briefs=[f"h{i}" for i in range(max(below_min, 0))],
            )

    def test_dynamic_recipe_instance_count_above_maximum_raises(self) -> None:
        matched_route_ids, selected_agents, task_text = self._dynamic_signals()
        above_max = DYNAMIC_RECIPE["instances"]["max"] + 1

        with self.assertRaises(ValueError):
            expand_recipe_to_members(
                CONFIG,
                DYNAMIC_RECIPE["id"],
                matched_route_ids,
                selected_agents,
                task_text,
                instance_count=above_max,
                instance_briefs=[f"h{i}" for i in range(above_max)],
            )

    def test_dynamic_recipe_missing_instance_briefs_raises(self) -> None:
        matched_route_ids, selected_agents, task_text = self._dynamic_signals()

        with self.assertRaises(ValueError):
            expand_recipe_to_members(
                CONFIG, DYNAMIC_RECIPE["id"], matched_route_ids, selected_agents, task_text
            )

    def test_dynamic_recipe_mismatched_instance_briefs_count_raises(self) -> None:
        matched_route_ids, selected_agents, task_text = self._dynamic_signals()
        count = DYNAMIC_RECIPE["instances"]["min"]

        with self.assertRaises(ValueError):
            expand_recipe_to_members(
                CONFIG,
                DYNAMIC_RECIPE["id"],
                matched_route_ids,
                selected_agents,
                task_text,
                instance_count=count,
                instance_briefs=["only one brief"],
            )

    def test_dynamic_recipe_that_does_not_fire_is_refused(self) -> None:
        role = DYNAMIC_RECIPE["role"]
        requires_route = DYNAMIC_RECIPE.get("requires_route")
        matched_route_ids = {requires_route} if requires_route else set()

        with self.assertRaises(ValueError):
            expand_recipe_to_members(
                CONFIG,
                DYNAMIC_RECIPE["id"],
                matched_route_ids,
                {role},
                "plain unrelated task text with no signal",
                instance_count=DYNAMIC_RECIPE["instances"]["min"],
                instance_briefs=[f"h{i}" for i in range(DYNAMIC_RECIPE["instances"]["min"])],
            )


class SyntheticModeSignalTests(unittest.TestCase):
    def test_rejects_unknown_route_id(self) -> None:
        with self.assertRaises(ValueError):
            _resolve_synthetic_mode_signals(CONFIG, ["not-a-real-route"], [], "")

    def test_accepts_known_route_ids_and_arbitrary_agent_ids(self) -> None:
        route_id = CONFIG["routes"][0]["id"]
        matched_route_ids, selected_agents, task_text = _resolve_synthetic_mode_signals(
            CONFIG, [route_id], ["some-agent"], "task text"
        )
        self.assertEqual(matched_route_ids, {route_id})
        self.assertEqual(selected_agents, {"some-agent"})
        self.assertEqual(task_text, "task text")


class RecipeIdFilterTests(unittest.TestCase):
    def test_unknown_recipe_id_raises(self) -> None:
        with self.assertRaises(ValueError):
            explain_recipes(CONFIG, set(), set(), "", recipe_id="not-a-real-recipe")

    def test_known_recipe_id_returns_exactly_one_explanation(self) -> None:
        explanations = explain_recipes(CONFIG, set(), set(), "", recipe_id=FIXED_RECIPE["id"])
        self.assertEqual(len(explanations), 1)
        self.assertEqual(explanations[0]["id"], FIXED_RECIPE["id"])


class RealRoutingConfigurationTests(unittest.TestCase):
    """Regression guard: every real team_recipes[] entry in this repository's
    own routing.yaml must produce a sensible, non-crashing explanation."""

    def test_every_real_recipe_produces_a_verdict_with_reasoning(self) -> None:
        explanations = explain_recipes(CONFIG, set(), set(), "")
        self.assertEqual(len(explanations), len(CONFIG["team_recipes"]))
        recipe_ids = {recipe["id"] for recipe in CONFIG["team_recipes"]}
        self.assertEqual({explanation["id"] for explanation in explanations}, recipe_ids)
        for explanation in explanations:
            self.assertIn(explanation["type"], {"fixed", "dynamic"})
            self.assertIsInstance(explanation["fires"], bool)
            if explanation["type"] == "fixed":
                self.assertIn("routes", explanation)
                self.assertIn("members", explanation)
            else:
                self.assertIn("role", explanation)
                self.assertIn("requires_route", explanation)
                self.assertIn("keywords", explanation)

    def test_no_signals_means_no_real_recipe_fires(self) -> None:
        # Every real fixed recipe requires minimum_matches >= 1 and every
        # real dynamic recipe requires a role selection and a keyword hit,
        # so an empty signal set must be a universal no-fire baseline.
        explanations = explain_recipes(CONFIG, set(), set(), "")
        for explanation in explanations:
            self.assertFalse(explanation["fires"], explanation["id"])

    def test_cli_main_runs_cleanly_over_full_real_config(self) -> None:
        exit_code = main(
            [
                "--routing",
                str(ROOT / "routing.yaml"),
                "--catalog",
                str(AGENTS_ROOT / "catalog.yaml"),
                "--matched-routes",
                "frontend,backend,infrastructure,pipeline,supply-chain,debugging",
                "--selected-agents",
                "code-reviewer,infrastructure-reviewer,pipeline-security-reviewer,"
                "supply-chain-security-reviewer,frontend-engineer,backend-engineer,"
                "infrastructure-provisioner,cicd-engineer,debugging-engineer",
                "--task",
                "intermittent flaky recurring hasn't converged elusive hard to reproduce",
                "--format",
                "json",
            ]
        )
        self.assertEqual(exit_code, 0)

    def test_fires_verdicts_match_a_real_build_dispatch_plan_teams_array(self) -> None:
        # The tool's headline claim is that its verdicts can never disagree
        # with a real dispatch. Lock that in as an automated regression,
        # not just a manual/prose demonstration: build a real plan for a
        # concrete task, then independently ask explain_recipes about the
        # same matched-routes/selected-roster/task-text signals, and assert
        # the set of recipe ids marked fires=True is exactly the set of
        # recipe ids build_dispatch_plan() actually put in plan["teams"].
        task = "Add a React upload form backed by a PostgreSQL API"
        plan = build_dispatch_plan(
            CONFIG,
            CATALOG,
            {
                "task": task,
                "task_id": None,
                "repository_root": str(AGENTS_ROOT.parent),
                "base": None,
                "changed_files": ["frontend/src/Upload.tsx", "services/upload/main.go"],
                "changed_file_source": "explicit",
                "classification": "internal",
                "source": "test-fixture",
                "top": 20,
            },
        )
        matched_route_ids = {route["id"] for route in plan.get("matched_routes", [])}
        selected_agents = {
            *plan["agents"].get("primary", []),
            *plan["agents"].get("reviewers", []),
            *plan["agents"].get("support", []),
        }
        explanations = explain_recipes(CONFIG, matched_route_ids, selected_agents, task)
        dryrun_fired_ids = {e["id"] for e in explanations if e["fires"]}
        plan_team_ids = {team["id"] for team in plan.get("teams", [])}
        self.assertEqual(plan_team_ids, dryrun_fired_ids)

    def test_cli_main_reports_task_mode_against_real_repository(self) -> None:
        exit_code = main(
            [
                "--routing",
                str(ROOT / "routing.yaml"),
                "--catalog",
                str(AGENTS_ROOT / "catalog.yaml"),
                "--task",
                "Add a React upload form backed by a PostgreSQL API",
                "--files",
                "frontend/src/Upload.tsx,services/upload/main.go",
                "--root",
                str(AGENTS_ROOT.parent),
            ]
        )
        self.assertEqual(exit_code, 0)

    def test_cli_main_requires_a_mode(self) -> None:
        with self.assertRaises(ValueError):
            main(["--routing", str(ROOT / "routing.yaml"), "--catalog", str(AGENTS_ROOT / "catalog.yaml")])

    def test_cli_main_rejects_files_combined_with_synthetic_mode(self) -> None:
        with self.assertRaises(ValueError):
            main(
                [
                    "--routing",
                    str(ROOT / "routing.yaml"),
                    "--catalog",
                    str(AGENTS_ROOT / "catalog.yaml"),
                    "--matched-routes",
                    "frontend",
                    "--files",
                    "a.tsx",
                ]
            )


if __name__ == "__main__":
    unittest.main()
