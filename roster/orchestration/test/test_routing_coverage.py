"""Routing-coverage/orphan checks between catalog.yaml and routing.yaml.

Verifies that every roster/catalog.yaml agent is reachable from
roster/orchestration/routing.yaml (routes, risk_rules, team_recipes,
change_intake.agents, or cross_stack.support), and that every agent ID
referenced from those routing.yaml structures actually exists in
catalog.yaml. See roster/orchestration/src/routing_health.py for the
implementation; it reuses routing.py's load_routing/load_catalog rather than
parsing either file a second time.
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
from routing_health import (  # noqa: E402
    _probe_paths,
    check_exclude_path_reachability,
    check_routing_coverage,
    run,
)

CATALOG_PATH = REPOSITORY_ROOT / "roster" / "catalog.yaml"
ROUTING_PATH = REPOSITORY_ROOT / "roster" / "orchestration" / "routing.yaml"


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
            self.fail('routing.yaml no longer has an "orchestration" route to attach the fixture to')

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
            self.fail('routing.yaml no longer has an "orchestration" route to attach the fixture to')

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
            self.fail('routing.yaml no longer has an "orchestration" route to attach the fixture to')

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
            self.fail("routing.yaml no longer has a team_recipe with a 'role' field to attach the fixture to")

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
        list in a routing.yaml copy (it is heavily referenced today), then
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
             "in any routing.yaml route, risk_rule, team_recipe, change_intake.agents, or "
             "cross_stack.support entry"],
            findings,
        )

    def test_standalone_cli_exits_nonzero_and_reports_findings_on_a_broken_fixture(self) -> None:
        script = ROOT / "src" / "routing_health.py"
        with tempfile.TemporaryDirectory() as temporary_directory:
            temporary = Path(temporary_directory)
            broken_routing = temporary / "routing.yaml"
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
    any `human_gate`, but matches on keywords alone, and before this check
    nothing in CI noticed.

    The check is deliberately one-directional: it reports a glob only when
    every probe the glob *itself* matches is excluded. An under-representative
    probe set therefore costs a missed finding, never a false accusation.
    """

    def test_current_repository_has_zero_findings(self) -> None:
        self.assertEqual([], check_exclude_path_reachability(load_routing(ROUTING_PATH)))

    def test_an_exclude_identical_to_its_include_is_reported(self) -> None:
        findings = check_exclude_path_reachability(
            {"routes": [{"id": "a", "paths": ["foo/**"], "exclude_paths": ["foo/**"]}]}
        )
        self.assertEqual(1, len(findings))
        self.assertIn('routes[0] (id="a").paths[0]', findings[0])
        self.assertIn("fully shadowed", findings[0])

    def test_an_exclude_broader_than_its_include_is_reported(self) -> None:
        # The realistic failure, and the one a verbatim-equality check misses.
        findings = check_exclude_path_reachability(
            {"routes": [{"id": "b", "paths": ["foo/**"], "exclude_paths": ["**"]}]}
        )
        self.assertEqual(1, len(findings))

    def test_only_the_dead_glob_is_reported_when_a_sibling_survives(self) -> None:
        findings = check_exclude_path_reachability(
            {"routes": [{"id": "c", "paths": ["alive/**", "dead/**"], "exclude_paths": ["dead/**"]}]}
        )
        self.assertEqual(1, len(findings))
        self.assertIn(".paths[1]", findings[0])
        self.assertIn("dead/**", findings[0])

    def test_risk_rules_are_checked_as_well_as_routes(self) -> None:
        findings = check_exclude_path_reachability(
            {"risk_rules": [{"id": "g", "paths": ["foo/**"], "exclude_paths": ["foo/**"]}]}
        )
        self.assertEqual(1, len(findings))
        self.assertIn('risk_rules[0] (id="g")', findings[0])

    def test_a_partial_carve_out_is_not_reported(self) -> None:
        """The shape routing.yaml actually ships: a broad glob minus a subtree."""
        self.assertEqual(
            [],
            check_exclude_path_reachability(
                {"routes": [{"id": "d", "paths": ["**/architecture/**"], "exclude_paths": ["roster/**"]}]}
            ),
        )

    def test_a_depth_limited_exclusion_is_not_reported(self) -> None:
        """`docs/*` excludes only the top level of `docs/**`; `docs/a/b.md`
        still matches. A probe set with a single fixed depth would call this
        fully shadowed -- this is the false positive the multi-depth fillers
        exist to prevent.
        """
        self.assertEqual(
            [],
            check_exclude_path_reachability(
                {"routes": [{"id": "e", "paths": ["docs/**"], "exclude_paths": ["docs/*"]}]}
            ),
        )

    def test_a_rule_without_exclude_paths_is_not_reported(self) -> None:
        self.assertEqual([], check_exclude_path_reachability({"routes": [{"id": "f", "paths": ["foo/**"]}]}))

    def test_every_synthesized_probe_matches_the_glob_it_came_from(self) -> None:
        """The soundness property the whole check rests on."""
        from routing import glob_to_regex

        for pattern in ("**/architecture/**", "docs/**", "backend/*.go", "a/?/c", "**", "exact/path.md"):
            with self.subTest(pattern=pattern):
                matcher = glob_to_regex(pattern)
                probes = _probe_paths(pattern)
                self.assertTrue(probes, f"no probe synthesized for {pattern!r}")
                for probe in probes:
                    self.assertTrue(matcher.search(probe), f"{probe!r} does not match {pattern!r}")

    def test_probes_span_more_than_one_directory_depth(self) -> None:
        depths = {probe.count("/") for probe in _probe_paths("docs/**")}
        self.assertGreater(len(depths), 1, "single-depth probes would cause false positives")

    def test_findings_are_reported_through_the_top_level_run(self) -> None:
        """Wiring check: the new findings must reach `run()`, not just the
        function under test.
        """
        with tempfile.TemporaryDirectory() as temporary_directory:
            broken = Path(temporary_directory) / "routing.yaml"
            config = json.loads(ROUTING_PATH.read_text(encoding="utf-8"))
            for route in config["routes"]:
                if route["id"] == "architecture-design":
                    route["exclude_paths"] = ["**"]
                    break
            broken.write_text(json.dumps(config), encoding="utf-8")
            findings = run(CATALOG_PATH, broken)
            self.assertTrue(any("fully shadowed" in finding for finding in findings), findings)


if __name__ == "__main__":
    unittest.main()
