"""Routing-coverage/orphan checks between catalog.yaml and routing.json.

Verifies that every roster/catalog.yaml agent is reachable from
roster/orchestration/routing.json (routes, risk_rules, team_recipes,
change_intake.agents, or cross_stack.support), and that every agent ID
referenced from those routing.json structures actually exists in
catalog.yaml, and that no routing rule's `exclude_paths` fully shadows one of
its own `paths` globs (issue #162). See roster/orchestration/src/
routing_health.py for the implementation; it reuses routing.py's
load_routing/load_catalog rather than parsing either file a second time, and
decides shadowing exactly via glob_containment.py rather than by sampling.
"""

from __future__ import annotations

import copy
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
REPOSITORY_ROOT = ROOT.parent.parent
sys.path.insert(0, str(ROOT / "src"))

from routing import load_catalog, load_routing  # noqa: E402
from routing_health import check_exclude_path_reachability, check_routing_coverage, run  # noqa: E402

CATALOG_PATH = REPOSITORY_ROOT / "roster" / "catalog.yaml"
ROUTING_PATH = REPOSITORY_ROOT / "roster" / "orchestration" / "routing.json"


class RoutingCoverageTests(unittest.TestCase):
    def test_current_repository_has_zero_findings(self) -> None:
        findings = run(CATALOG_PATH, ROUTING_PATH)
        self.assertEqual([], findings)

    def test_risk_rules_and_team_recipes_count_as_reachability_paths(self) -> None:
        """Regression guard for the routes-only false positive this check
        must avoid: release-engineer, escalation-manager, and threat-modeler
        are (also) reachable via risk_rules/team_recipes support/role fields
        today. Stripping every routes[*] reference to them and re-checking
        with only risk_rules/team_recipes/change_intake/cross_stack left
        proves those paths alone are sufficient for reachability, matching
        A-FR-5.
        """
        config = copy.deepcopy(load_routing(ROUTING_PATH))
        catalog_agent_ids = load_catalog(CATALOG_PATH)
        watched = {"release-engineer", "escalation-manager", "threat-modeler"}
        for agent_id in watched:
            self.assertIn(agent_id, catalog_agent_ids)

        non_route_reachable: set[str] = set()
        for rule in config["risk_rules"]:
            for field in ("primary", "reviewers", "support"):
                non_route_reachable.update(rule.get(field, []) or [])
        for recipe in config.get("team_recipes", []):
            non_route_reachable.update(recipe.get("members", []) or [])
            if "role" in recipe:
                non_route_reachable.add(recipe["role"])
        non_route_reachable.update(config.get("change_intake", {}).get("agents", []) or [])
        non_route_reachable.update(config.get("cross_stack", {}).get("support", []) or [])
        self.assertTrue(
            watched.issubset(non_route_reachable),
            f"test fixture assumption broken: {watched - non_route_reachable} are no longer "
            "reachable via risk_rules/team_recipes/change_intake/cross_stack alone, so this "
            "regression guard no longer proves those paths count",
        )

        for route in config["routes"]:
            for field in ("primary", "reviewers", "support"):
                if field in route:
                    route[field] = [agent_id for agent_id in route[field] if agent_id not in watched]

        findings = check_routing_coverage(config, catalog_agent_ids)
        self.assertEqual([], findings)

    def test_orphan_catalog_agent_is_named_in_the_finding(self) -> None:
        config = load_routing(ROUTING_PATH)
        catalog_agent_ids = load_catalog(CATALOG_PATH) + ["totally-unreferenced-fixture-agent"]

        findings = check_routing_coverage(config, catalog_agent_ids)

        self.assertTrue(
            any('catalog agent "totally-unreferenced-fixture-agent"' in finding for finding in findings),
            findings,
        )

    def test_dangling_route_reviewer_reference_is_named_with_route_and_field(self) -> None:
        config = copy.deepcopy(load_routing(ROUTING_PATH))
        catalog_agent_ids = load_catalog(CATALOG_PATH)

        for route in config["routes"]:
            if route["id"] == "orchestration":
                route.setdefault("reviewers", []).append("nonexistent-bogus-agent")
                break
        else:  # pragma: no cover - guards fixture assumption
            self.fail('routing.json no longer has an "orchestration" route to attach the fixture to')

        findings = check_routing_coverage(config, catalog_agent_ids)

        self.assertTrue(
            any(
                'routes[' in finding
                and 'id="orchestration"' in finding
                and ".reviewers[" in finding
                and 'agent "nonexistent-bogus-agent"' in finding
                for finding in findings
            ),
            findings,
        )

    def test_dangling_route_primary_reference_is_named_with_route_and_field(self) -> None:
        config = copy.deepcopy(load_routing(ROUTING_PATH))
        catalog_agent_ids = load_catalog(CATALOG_PATH)

        for route in config["routes"]:
            if route["id"] == "orchestration":
                route.setdefault("primary", []).append("nonexistent-bogus-agent")
                break
        else:  # pragma: no cover - guards fixture assumption
            self.fail('routing.json no longer has an "orchestration" route to attach the fixture to')

        findings = check_routing_coverage(config, catalog_agent_ids)

        self.assertTrue(
            any(
                'routes[' in finding
                and 'id="orchestration"' in finding
                and ".primary[" in finding
                and 'agent "nonexistent-bogus-agent"' in finding
                for finding in findings
            ),
            findings,
        )

    def test_dangling_route_support_reference_is_named_with_route_and_field(self) -> None:
        config = copy.deepcopy(load_routing(ROUTING_PATH))
        catalog_agent_ids = load_catalog(CATALOG_PATH)

        for route in config["routes"]:
            if route["id"] == "orchestration":
                route.setdefault("support", []).append("nonexistent-bogus-agent")
                break
        else:  # pragma: no cover - guards fixture assumption
            self.fail('routing.json no longer has an "orchestration" route to attach the fixture to')

        findings = check_routing_coverage(config, catalog_agent_ids)

        self.assertTrue(
            any(
                'routes[' in finding
                and 'id="orchestration"' in finding
                and ".support[" in finding
                and 'agent "nonexistent-bogus-agent"' in finding
                for finding in findings
            ),
            findings,
        )

    def test_dangling_risk_rule_primary_reference_is_named_with_rule_and_field(self) -> None:
        config = copy.deepcopy(load_routing(ROUTING_PATH))
        catalog_agent_ids = load_catalog(CATALOG_PATH)

        rule = config["risk_rules"][0]
        rule.setdefault("primary", []).append("nonexistent-bogus-agent")

        findings = check_routing_coverage(config, catalog_agent_ids)

        self.assertTrue(
            any(
                "risk_rules[" in finding
                and f'id="{rule["id"]}")' in finding
                and ".primary[" in finding
                and 'agent "nonexistent-bogus-agent"' in finding
                for finding in findings
            ),
            findings,
        )

    def test_dangling_risk_rule_reviewers_reference_is_named_with_rule_and_field(self) -> None:
        config = copy.deepcopy(load_routing(ROUTING_PATH))
        catalog_agent_ids = load_catalog(CATALOG_PATH)

        rule = config["risk_rules"][0]
        rule.setdefault("reviewers", []).append("nonexistent-bogus-agent")

        findings = check_routing_coverage(config, catalog_agent_ids)

        self.assertTrue(
            any(
                "risk_rules[" in finding
                and f'id="{rule["id"]}")' in finding
                and ".reviewers[" in finding
                and 'agent "nonexistent-bogus-agent"' in finding
                for finding in findings
            ),
            findings,
        )

    def test_dangling_risk_rule_support_reference_is_named_with_rule_and_field(self) -> None:
        config = copy.deepcopy(load_routing(ROUTING_PATH))
        catalog_agent_ids = load_catalog(CATALOG_PATH)

        rule = config["risk_rules"][0]
        rule.setdefault("support", []).append("nonexistent-bogus-agent")

        findings = check_routing_coverage(config, catalog_agent_ids)

        self.assertTrue(
            any(
                "risk_rules[" in finding
                and f'id="{rule["id"]}")' in finding
                and ".support[" in finding
                and 'agent "nonexistent-bogus-agent"' in finding
                for finding in findings
            ),
            findings,
        )

    def test_dangling_team_recipe_member_reference_is_named_with_recipe_and_field(self) -> None:
        config = copy.deepcopy(load_routing(ROUTING_PATH))
        catalog_agent_ids = load_catalog(CATALOG_PATH)

        recipe = config["team_recipes"][0]
        recipe.setdefault("members", []).append("nonexistent-bogus-agent")

        findings = check_routing_coverage(config, catalog_agent_ids)

        self.assertTrue(
            any(
                "team_recipes[" in finding
                and f'id="{recipe["id"]}")' in finding
                and ".members[" in finding
                and 'agent "nonexistent-bogus-agent"' in finding
                for finding in findings
            ),
            findings,
        )

    def test_dangling_team_recipe_role_reference_is_named_with_recipe_and_field(self) -> None:
        config = copy.deepcopy(load_routing(ROUTING_PATH))
        catalog_agent_ids = load_catalog(CATALOG_PATH)

        for recipe in config["team_recipes"]:
            if "role" in recipe:
                recipe["role"] = "nonexistent-bogus-agent"
                target_recipe = recipe
                break
        else:  # pragma: no cover - guards fixture assumption
            self.fail("routing.json no longer has a team_recipe with a 'role' field to attach the fixture to")

        findings = check_routing_coverage(config, catalog_agent_ids)

        self.assertTrue(
            any(
                "team_recipes[" in finding
                and f'id="{target_recipe["id"]}")' in finding
                and ".role" in finding
                and 'agent "nonexistent-bogus-agent"' in finding
                for finding in findings
            ),
            findings,
        )

    def test_dangling_change_intake_agents_reference_is_named(self) -> None:
        config = copy.deepcopy(load_routing(ROUTING_PATH))
        catalog_agent_ids = load_catalog(CATALOG_PATH)

        config["change_intake"].setdefault("agents", []).append("nonexistent-bogus-agent")

        findings = check_routing_coverage(config, catalog_agent_ids)

        self.assertTrue(
            any(
                "change_intake.agents[" in finding and 'agent "nonexistent-bogus-agent"' in finding
                for finding in findings
            ),
            findings,
        )

    def test_dangling_cross_stack_support_reference_is_named(self) -> None:
        config = copy.deepcopy(load_routing(ROUTING_PATH))
        catalog_agent_ids = load_catalog(CATALOG_PATH)

        config["cross_stack"].setdefault("support", []).append("nonexistent-bogus-agent")

        findings = check_routing_coverage(config, catalog_agent_ids)

        self.assertTrue(
            any(
                "cross_stack.support[" in finding and 'agent "nonexistent-bogus-agent"' in finding
                for finding in findings
            ),
            findings,
        )

    def test_removing_an_agent_from_every_reference_list_produces_an_orphan_finding(self) -> None:
        """End-to-end fixture: drop "technical-writer" from every reference
        list in a routing.json copy (it is heavily referenced today), then
        confirm it is reported as an orphan and nothing else regresses.
        """
        config = copy.deepcopy(load_routing(ROUTING_PATH))
        catalog_agent_ids = load_catalog(CATALOG_PATH)
        self.assertIn("technical-writer", catalog_agent_ids)

        def strip(ids: list[str]) -> list[str]:
            return [agent_id for agent_id in ids if agent_id != "technical-writer"]

        for route in config["routes"]:
            for field in ("primary", "reviewers", "support"):
                if field in route:
                    route[field] = strip(route[field])
        for rule in config["risk_rules"]:
            for field in ("primary", "reviewers", "support"):
                if field in rule:
                    rule[field] = strip(rule[field])
        for recipe in config.get("team_recipes", []):
            if "members" in recipe:
                recipe["members"] = strip(recipe["members"])
            if recipe.get("role") == "technical-writer":
                recipe["role"] = "test-engineer"
        config["change_intake"]["agents"] = strip(config["change_intake"]["agents"])
        config["cross_stack"]["support"] = strip(config["cross_stack"]["support"])

        findings = check_routing_coverage(config, catalog_agent_ids)

        self.assertEqual(
            ['catalog agent "technical-writer" is not referenced as primary/reviewers/support '
             "in any routing.json route, risk_rule, team_recipe, change_intake.agents, or "
             "cross_stack.support entry"],
            findings,
        )

    def test_standalone_cli_exits_nonzero_and_reports_findings_on_a_broken_fixture(self) -> None:
        script = ROOT / "src" / "routing_health.py"
        with tempfile.TemporaryDirectory() as temporary_directory:
            temporary = Path(temporary_directory)
            broken_routing = temporary / "routing.json"
            config = json.loads(ROUTING_PATH.read_text(encoding="utf-8"))
            for route in config["routes"]:
                if route["id"] == "orchestration":
                    route.setdefault("reviewers", []).append("nonexistent-bogus-agent")
                    break
            broken_routing.write_text(json.dumps(config), encoding="utf-8")

            result = subprocess.run(
                [sys.executable, str(script), "--catalog", str(CATALOG_PATH), "--routing", str(broken_routing)],
                check=False,
                capture_output=True,
                text=True,
                encoding="utf-8",
            )
            self.assertNotEqual(0, result.returncode)
            self.assertIn("nonexistent-bogus-agent", result.stderr)

    def test_standalone_cli_accepts_the_flag_the_pre_commit_hook_passes(self) -> None:
        """`.pre-commit-config.yaml`'s `catalog-health` hook invokes this with
        `--check`. argparse rejecting it made the hook exit 2 without ever
        running the check, so the hook silently never worked.
        """
        hook_entry = (REPOSITORY_ROOT / ".pre-commit-config.yaml").read_text(encoding="utf-8")
        self.assertIn("routing_health.py --check", hook_entry)
        result = subprocess.run(
            [
                sys.executable,
                str(ROOT / "src" / "routing_health.py"),
                "--check",
                "--catalog",
                str(CATALOG_PATH),
                "--routing",
                str(ROUTING_PATH),
            ],
            check=False,
            capture_output=True,
            text=True,
            encoding="utf-8",
        )
        self.assertEqual(0, result.returncode, result.stderr)

    def test_standalone_cli_exits_zero_on_the_current_repository(self) -> None:
        script = ROOT / "src" / "routing_health.py"
        result = subprocess.run(
            [sys.executable, str(script), "--catalog", str(CATALOG_PATH), "--routing", str(ROUTING_PATH)],
            check=False,
            capture_output=True,
            text=True,
            encoding="utf-8",
        )
        self.assertEqual(0, result.returncode, result.stderr)


class ExcludePathReachabilityTests(unittest.TestCase):
    """Issue #162: an `exclude_paths` set that swallows one of its own rule's
    `paths` globs leaves that glob dead -- the rule keeps its `reviewers` and
    any `human_gate`, but matches on keywords alone, or never at all if it has
    none.

    The verdict is exact, decided by `glob_containment.contains`, not sampled.
    An earlier sampling implementation was withdrawn because the finding is a
    *universal* claim ("every path this glob matches is excluded") and an
    incomplete sample makes a universal claim easier to satisfy -- the
    false-accusation direction. The regression cases below are the concrete
    false positives that implementation produced.
    """

    def test_current_repository_has_zero_findings(self) -> None:
        self.assertEqual([], check_exclude_path_reachability(load_routing(ROUTING_PATH)))

    def _findings(self, section: str, rule: dict) -> list[str]:
        return check_exclude_path_reachability({section: [rule]})

    # -- true positives ---------------------------------------------------

    def test_an_exclude_identical_to_its_include_is_reported(self) -> None:
        findings = self._findings("routes", {"id": "a", "paths": ["foo/**"], "exclude_paths": ["foo/**"]})
        self.assertEqual(1, len(findings))
        self.assertIn('routes[0] (id="a").paths[0]', findings[0])
        self.assertIn("fully shadowed", findings[0])

    def test_an_exclude_broader_than_its_include_is_reported(self) -> None:
        # The realistic failure, and the one a verbatim-equality check misses.
        self.assertEqual(
            1, len(self._findings("routes", {"id": "b", "paths": ["foo/**"], "exclude_paths": ["**"]}))
        )

    def test_shadowing_by_the_union_of_several_excludes_is_reported(self) -> None:
        """No single exclusion covers the include; together they do. Only an
        exact containment decision can see this.
        """
        self.assertEqual(
            1,
            len(
                self._findings(
                    "routes", {"id": "u", "paths": ["a/**"], "exclude_paths": ["a/*", "a/*/**"]}
                )
            ),
        )

    def test_only_the_dead_glob_is_reported_when_a_sibling_survives(self) -> None:
        findings = self._findings(
            "routes", {"id": "c", "paths": ["alive/**", "dead/**"], "exclude_paths": ["dead/**"]}
        )
        self.assertEqual(1, len(findings))
        self.assertIn(".paths[1]", findings[0])

    def test_risk_rules_are_checked_as_well_as_routes(self) -> None:
        findings = self._findings(
            "risk_rules", {"id": "g", "paths": ["foo/**"], "exclude_paths": ["foo/**"]}
        )
        self.assertEqual(1, len(findings))
        self.assertIn('risk_rules[0] (id="g")', findings[0])

    def test_the_finding_distinguishes_a_keywordless_rule(self) -> None:
        """A shadowed glob on a rule with no keywords is not "keyword-only" --
        it is entirely dead, and the message must not misdescribe it.
        """
        keyworded = self._findings(
            "routes", {"id": "k", "paths": ["foo/**"], "exclude_paths": ["**"], "keywords": ["x"]}
        )
        bare = self._findings("routes", {"id": "n", "paths": ["foo/**"], "exclude_paths": ["**"]})
        self.assertIn("matches on keywords alone", keyworded[0])
        self.assertIn("can never match", bare[0])

    # -- false positives the sampling implementation produced --------------

    def test_an_exclude_matching_the_old_probe_vocabulary_is_not_reported(self) -> None:
        """Regression: every synthesized probe for a `**` glob ended in
        `.txt`, so this realistic carve-out was reported as fully shadowed.
        `roster/catalog-order.txt` is a real file matching the include.
        """
        self.assertEqual(
            [], self._findings("routes", {"id": "r", "paths": ["roster/**"], "exclude_paths": ["**/*.txt"]})
        )

    def test_an_exclude_matching_the_old_star_filler_is_not_reported(self) -> None:
        """Regression: `*` expanded to the single literal filler `probe`, so
        any exclude naming that literal condemned the whole glob. `main.go`
        matches the include and no exclude.
        """
        self.assertEqual(
            [], self._findings("routes", {"id": "s", "paths": ["**/*.go"], "exclude_paths": ["**/probe.go"]})
        )

    def test_an_exclude_matching_the_old_question_filler_is_not_reported(self) -> None:
        """Regression: `?` expanded to the single filler `x`. `a/b.go` lives."""
        self.assertEqual(
            [], self._findings("routes", {"id": "q", "paths": ["a/?.go"], "exclude_paths": ["a/x.go"]})
        )

    def test_a_deeply_nested_include_is_not_reported(self) -> None:
        """Regression: enough `**/` segments overflowed the old probe budget,
        which truncated to a biased prefix and dropped the surviving deep
        paths -- reporting a live glob as dead.
        """
        self.assertEqual(
            [],
            self._findings(
                "routes",
                {
                    "id": "t",
                    "paths": ["**/a/**/b/**/c/**/d.md"],
                    "exclude_paths": ["a/**", "probe/a/**", "probe/nested/a/b/**"],
                },
            ),
        )

    def test_the_broadest_possible_glob_is_not_reported_for_a_narrow_exclude(self) -> None:
        self.assertEqual([], self._findings("routes", {"id": "w", "paths": ["**"], "exclude_paths": ["**/*.txt"]}))

    # -- ordinary non-findings --------------------------------------------

    def test_a_partial_carve_out_is_not_reported(self) -> None:
        """The shape routing.json actually ships: a broad glob minus a subtree."""
        self.assertEqual(
            [],
            self._findings(
                "routes", {"id": "d", "paths": ["**/architecture/**"], "exclude_paths": ["roster/**"]}
            ),
        )

    def test_a_depth_limited_exclusion_is_not_reported(self) -> None:
        """`docs/*` excludes only the top level of `docs/**`."""
        self.assertEqual(
            [], self._findings("routes", {"id": "e", "paths": ["docs/**"], "exclude_paths": ["docs/*"]})
        )

    def test_a_rule_without_exclude_paths_is_not_reported(self) -> None:
        self.assertEqual([], self._findings("routes", {"id": "f", "paths": ["foo/**"]}))

    def test_a_rule_without_paths_is_not_reported(self) -> None:
        for paths in ({}, {"paths": []}, {"paths": None}):
            with self.subTest(paths=paths):
                self.assertEqual([], self._findings("routes", {"id": "p", "exclude_paths": ["**"], **paths}))

    def test_a_literal_include_equal_to_a_literal_exclude_is_reported(self) -> None:
        self.assertEqual(
            1,
            len(self._findings("routes", {"id": "l", "paths": ["a/b.md"], "exclude_paths": ["a/b.md"]})),
        )

    def test_an_undetermined_verdict_is_skipped_rather_than_reported(self) -> None:
        """The entire fail-safe argument: when the decision procedure cannot
        settle a pattern within its state budget, the rule must be skipped.
        A regression that treated anything-but-NOT_CONTAINED as a finding
        would turn every budget-exhausting pattern into a false accusation.
        """
        import glob_containment

        rule = {"id": "big", "paths": ["**/a/**/b/**/c.md"], "exclude_paths": ["x/**", "y/**"]}
        original = glob_containment._MAX_PRODUCT_STATES
        try:
            glob_containment._MAX_PRODUCT_STATES = 1
            self.assertEqual(
                glob_containment.UNDETERMINED,
                glob_containment.contains(rule["paths"][0], rule["exclude_paths"]),
                "fixture assumption broken: this pattern no longer exhausts the budget",
            )
            self.assertEqual([], check_exclude_path_reachability({"routes": [rule]}))
        finally:
            glob_containment._MAX_PRODUCT_STATES = original

    # -- wiring ------------------------------------------------------------

    def test_findings_are_reported_through_the_top_level_run(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            broken = Path(temporary_directory) / "routing.json"
            config = json.loads(ROUTING_PATH.read_text(encoding="utf-8"))
            for route in config["routes"]:
                if route["id"] == "architecture-design":
                    route["exclude_paths"] = ["**"]
                    break
            broken.write_text(json.dumps(config), encoding="utf-8")
            findings = run(CATALOG_PATH, broken)
            self.assertTrue(any("fully shadowed" in finding for finding in findings), findings)

    def test_run_reports_coverage_findings_alongside_shadowing_findings(self) -> None:
        """`run()` concatenates both lists; neither may shadow the other."""
        with tempfile.TemporaryDirectory() as temporary_directory:
            broken = Path(temporary_directory) / "routing.json"
            config = json.loads(ROUTING_PATH.read_text(encoding="utf-8"))
            for route in config["routes"]:
                if route["id"] == "architecture-design":
                    route["exclude_paths"] = ["**"]
                    route.setdefault("reviewers", []).append("nonexistent-bogus-agent")
                    break
            broken.write_text(json.dumps(config), encoding="utf-8")
            findings = run(CATALOG_PATH, broken)
            self.assertTrue(any("fully shadowed" in f for f in findings), findings)
            self.assertTrue(any("nonexistent-bogus-agent" in f for f in findings), findings)


if __name__ == "__main__":
    unittest.main()
