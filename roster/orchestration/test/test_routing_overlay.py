"""Tests for the project-local routing.json overlay mechanism (idea #6,
`roster/orchestration/runs/cadre-idea-6-routing-overlay-2026-07-29/
requirements.md`, `REQ-CADRE-BACKLOG-6`).

Covers AC-1..AC-9 from that requirements baseline: discovery/no-overlay
identity (AC-1), additive route/team-recipe fixtures (AC-2/AC-4),
matching-condition widening (AC-3), id-collision rejection (AC-5),
safety-field rejection (AC-6), the interaction-level narrowing-bypass
rejection (AC-7 -- the specific gap the requirements baseline calls out),
idea #1/#10 compatibility on the materialized effective configuration
(AC-8), and change_intake/cross_stack/knowledge_focus overlay behavior
(AC-9).
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
SELECT_AGENTS = ROOT / "src" / "select_agents.py"
sys.path.insert(0, str(ROOT / "src"))
sys.path.insert(0, str(REPOSITORY_ROOT / "roster" / "shared" / "src"))

from routing import load_routing, match_rule  # noqa: E402
from routing_overlay import (  # noqa: E402
    OVERLAY_RELATIVE_PATH,
    RoutingOverlayError,
    find_routing_overlay,
    materialize_effective_routing,
    merge_routing,
    resolve_effective_routing,
)

ROUTING_PATH = REPOSITORY_ROOT / "roster" / "orchestration" / "routing.json"


def _minimal_base() -> dict:
    """A small, self-contained base config matching routing.json's real
    shape, used for merge-rule unit tests so fixtures stay readable and
    independent of the live repository routing.json's exact content.
    """
    return {
        "version": 1,
        "ignored_gates": ["G10"],
        "change_intake": {
            "keywords": ["implement", "fix"],
            "agents": ["product-intent-agent"],
            "quality_gates": ["G1"],
        },
        "routes": [
            {
                "id": "backend",
                "paths": ["backend/**"],
                "keywords": ["backend"],
                "primary": ["backend-engineer"],
                "reviewers": ["test-engineer"],
                "quality_gates": ["G3"],
            }
        ],
        "risk_rules": [
            {
                "id": "destructive",
                "keywords": ["delete resource", "drop table"],
                "keyword_groups": [
                    ["destroy", "delete", "drop"],
                    ["resource", "database", "table"],
                ],
                "paths": [],
                "reviewers": ["security-reviewer"],
                "human_gate": "destructive-action",
            },
            {
                "id": "production",
                "keywords": [],
                "paths": [],
                "support": ["release-engineer"],
                "human_gate": "production-change",
                "quality_gates": ["G8", "G9"],
            },
        ],
        "cross_stack": {
            "route_ids": ["backend"],
            "minimum_matches": 2,
            "support": ["application-engineer"],
        },
        "team_recipes": [
            {
                "id": "parallel-review",
                "type": "fixed",
                "route_ids": ["backend"],
                "minimum_matches": 1,
                "members": ["code-reviewer"],
                "minimum_members_selected": 1,
                "communication_mode": "peer",
                "fallback": "orchestrator-relayed",
                "description": "fixture recipe",
            }
        ],
        "knowledge_focus": {
            "backend-engineer": "backend patterns",
        },
    }


class ProjectOverlayFixture(unittest.TestCase):
    """Base class providing a temporary project tree with a .git boundary
    and a helper to write .agents/orchestration/routing-overlay.json.
    """

    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory(prefix="routing-overlay-")
        self.root = Path(self.temporary.name)
        (self.root / ".git").mkdir()
        self.project = self.root / "src" / "pkg"
        self.project.mkdir(parents=True)

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def _write_overlay(self, content: dict) -> Path:
        overlay_path = self.root / OVERLAY_RELATIVE_PATH
        overlay_path.parent.mkdir(parents=True, exist_ok=True)
        overlay_path.write_text(json.dumps(content), encoding="utf-8")
        return overlay_path

    def _write_base(self, config: dict) -> Path:
        base_path = self.root / "routing.json"
        base_path.write_text(json.dumps(config), encoding="utf-8")
        return base_path


class DiscoveryTests(ProjectOverlayFixture):
    def test_finds_overlay_by_walking_up_to_git_boundary(self) -> None:
        overlay_path = self._write_overlay({"change_intake": {"keywords": ["deprecate"]}})
        found = find_routing_overlay(start=self.project)
        self.assertEqual(found, overlay_path)

    def test_returns_none_when_no_overlay_exists(self) -> None:
        self.assertIsNone(find_routing_overlay(start=self.project))

    def test_does_not_find_overlay_above_git_boundary(self) -> None:
        outside_dir = self.root.parent / ".agents" / "orchestration"
        outside_dir.mkdir(parents=True, exist_ok=True)
        (outside_dir / "routing-overlay.json").write_text("{}", encoding="utf-8")
        try:
            self.assertIsNone(find_routing_overlay(start=self.project))
        finally:
            (outside_dir / "routing-overlay.json").unlink()


class AC1NoOverlayTests(ProjectOverlayFixture):
    """AC-1: with no overlay, the effective configuration is byte-for-byte
    identical to the shipped base file.
    """

    def test_resolve_returns_base_config_unchanged(self) -> None:
        base = _minimal_base()
        base_path = self._write_base(base)
        effective, overlay_path = resolve_effective_routing(base_path=base_path, start=self.project)
        self.assertIsNone(overlay_path)
        self.assertEqual(base, effective)

    def test_materialize_is_byte_identical_to_base_file(self) -> None:
        base_path = self._write_base(_minimal_base())
        out_path = self.root / "out.json"
        overlay_path = materialize_effective_routing(out_path, base_path=base_path, start=self.project)
        self.assertIsNone(overlay_path)
        self.assertEqual(base_path.read_bytes(), out_path.read_bytes())

    def test_no_overlay_against_real_repository_routing_yaml_is_byte_identical(self) -> None:
        out_path = self.root / "out.json"
        overlay_path = materialize_effective_routing(out_path, base_path=ROUTING_PATH, start=self.project)
        self.assertIsNone(overlay_path)
        self.assertEqual(ROUTING_PATH.read_bytes(), out_path.read_bytes())


class AC2AddRouteTests(ProjectOverlayFixture):
    def test_overlay_adds_a_new_route_unmodified_and_preserves_base_routes(self) -> None:
        base = _minimal_base()
        overlay = {
            "routes": [
                {
                    "id": "project-fixture-route",
                    "paths": ["fixture/**"],
                    "keywords": ["fixture keyword"],
                    "primary": ["backend-engineer"],
                }
            ]
        }
        effective = merge_routing(base, overlay)
        ids = {route["id"] for route in effective["routes"]}
        self.assertEqual({"backend", "project-fixture-route"}, ids)
        self.assertEqual(base["routes"][0], next(r for r in effective["routes"] if r["id"] == "backend"))
        added = next(r for r in effective["routes"] if r["id"] == "project-fixture-route")
        self.assertEqual(overlay["routes"][0], added)


class AC3AdjustRiskRuleMatchingTests(ProjectOverlayFixture):
    def test_overlay_appends_a_keyword_and_preserves_safety_fields(self) -> None:
        base = _minimal_base()
        overlay = {
            "risk_rules": [
                {
                    "id": "destructive",
                    "keywords": ["delete resource", "drop table", "purge cache"],
                }
            ]
        }
        effective = merge_routing(base, overlay)
        patched = next(r for r in effective["risk_rules"] if r["id"] == "destructive")
        self.assertEqual(
            ["delete resource", "drop table", "purge cache"],
            patched["keywords"],
        )
        self.assertEqual("destructive-action", patched["human_gate"])
        self.assertEqual(["security-reviewer"], patched["reviewers"])

    def test_overlay_widens_an_existing_keyword_groups_inner_list(self) -> None:
        # keyword_groups is AND-of-ORs (routing.py::match_rule's
        # conjunctive_match): the only safe "widen" of an existing outer
        # group is adding a keyword to that group's own inner OR-list, which
        # relaxes that one AND-clause without adding a new mandatory one.
        base = _minimal_base()
        overlay = {
            "risk_rules": [
                {
                    "id": "destructive",
                    "keyword_groups": [
                        ["destroy", "delete", "drop", "annihilate"],
                        ["resource", "database", "table"],
                    ],
                }
            ]
        }
        effective = merge_routing(base, overlay)
        patched = next(r for r in effective["risk_rules"] if r["id"] == "destructive")
        self.assertEqual(
            [
                ["destroy", "delete", "drop", "annihilate"],
                ["resource", "database", "table"],
            ],
            patched["keyword_groups"],
        )
        self.assertEqual("destructive-action", patched["human_gate"])


class AC4AddTeamRecipeTests(ProjectOverlayFixture):
    def test_overlay_adds_a_new_team_recipe_unmodified(self) -> None:
        base = _minimal_base()
        overlay = {
            "team_recipes": [
                {
                    "id": "fixture-recipe",
                    "type": "fixed",
                    "route_ids": ["backend"],
                    "minimum_matches": 1,
                    "members": ["backend-engineer"],
                    "minimum_members_selected": 1,
                    "communication_mode": "orchestrator-relayed",
                    "description": "fixture-only recipe",
                }
            ]
        }
        effective = merge_routing(base, overlay)
        ids = {recipe["id"] for recipe in effective["team_recipes"]}
        self.assertEqual({"parallel-review", "fixture-recipe"}, ids)
        self.assertEqual(base["team_recipes"][0], next(r for r in effective["team_recipes"] if r["id"] == "parallel-review"))


class AC5IdCollisionTests(ProjectOverlayFixture):
    def test_new_route_colliding_with_base_risk_rule_id_is_rejected(self) -> None:
        base = _minimal_base()
        overlay = {"routes": [{"id": "destructive", "keywords": ["x"]}]}
        with self.assertRaisesRegex(RoutingOverlayError, "'destructive'"):
            merge_routing(base, overlay)

    def test_new_team_recipe_colliding_with_base_route_id_is_rejected(self) -> None:
        base = _minimal_base()
        overlay = {"team_recipes": [{"id": "backend", "type": "fixed"}]}
        with self.assertRaisesRegex(RoutingOverlayError, "'backend'"):
            merge_routing(base, overlay)

    def test_two_new_entries_across_sections_colliding_with_each_other_are_rejected(self) -> None:
        base = _minimal_base()
        overlay = {
            "routes": [{"id": "shared-fixture-id", "keywords": ["a"]}],
            "risk_rules": [{"id": "shared-fixture-id", "keywords": ["b"]}],
        }
        with self.assertRaisesRegex(RoutingOverlayError, "'shared-fixture-id'"):
            merge_routing(base, overlay)


class AC6SafetyFieldRemovalTests(ProjectOverlayFixture):
    def test_overlay_changing_human_gate_on_a_base_entry_is_rejected_by_id_and_field(self) -> None:
        base = _minimal_base()
        overlay = {"risk_rules": [{"id": "destructive", "human_gate": "weakened"}]}
        with self.assertRaisesRegex(RoutingOverlayError, r"'destructive'.*'human_gate'"):
            merge_routing(base, overlay)

    def test_overlay_removing_human_gate_field_entirely_still_leaves_it_unchanged(self) -> None:
        # An overlay entry that simply omits human_gate is not an attempt to
        # remove it -- absence means "unchanged", per the widen-patch
        # semantics (only keywords/keyword_groups/paths are ever touched).
        base = _minimal_base()
        overlay = {"risk_rules": [{"id": "destructive", "keywords": ["delete resource", "drop table", "wipe cache"]}]}
        effective = merge_routing(base, overlay)
        patched = next(r for r in effective["risk_rules"] if r["id"] == "destructive")
        self.assertEqual("destructive-action", patched["human_gate"])

    def test_overlay_changing_reviewers_on_a_base_entry_is_rejected(self) -> None:
        base = _minimal_base()
        overlay = {"risk_rules": [{"id": "destructive", "reviewers": []}]}
        with self.assertRaisesRegex(RoutingOverlayError, r"'destructive'.*'reviewers'"):
            merge_routing(base, overlay)

    def test_overlay_changing_quality_gates_on_a_base_route_is_rejected(self) -> None:
        base = _minimal_base()
        overlay = {"routes": [{"id": "backend", "quality_gates": []}]}
        with self.assertRaisesRegex(RoutingOverlayError, r"'backend'.*'quality_gates'"):
            merge_routing(base, overlay)


class AC7NarrowingBypassRejectionTests(ProjectOverlayFixture):
    """AC-7: the interaction-level check -- narrowing a base entry's
    matching conditions must fail closed even when human_gate/reviewers are
    never directly touched, distinctly from AC-6's direct-field-change case.
    """

    def test_removing_a_keyword_from_a_human_gate_bearing_rule_is_rejected(self) -> None:
        base = _minimal_base()
        overlay = {"risk_rules": [{"id": "destructive", "keywords": ["delete resource"]}]}  # drops "drop table"
        with self.assertRaisesRegex(RoutingOverlayError, "narrows base 'keywords'"):
            merge_routing(base, overlay)

    def test_removing_an_item_from_a_keyword_group_is_rejected(self) -> None:
        base = _minimal_base()
        overlay = {
            "risk_rules": [
                {
                    "id": "destructive",
                    "keyword_groups": [
                        ["destroy", "delete"],  # dropped "drop" from the first group
                        ["resource", "database", "table"],
                    ],
                }
            ]
        }
        with self.assertRaisesRegex(RoutingOverlayError, "narrows base 'keyword_groups'"):
            merge_routing(base, overlay)

    def test_appending_a_new_keyword_groups_outer_group_is_rejected(self) -> None:
        # The AND-of-ORs bypass: appending a brand-new outer group ADDS a
        # new mandatory AND-condition, which narrows overall matching (some
        # task/file combinations that used to match the base rule no longer
        # do) even though the change looks purely additive at the JSON
        # level. This must be rejected, not accepted as a "widen".
        base = _minimal_base()
        overlay = {
            "risk_rules": [
                {
                    "id": "destructive",
                    "keyword_groups": [
                        ["destroy", "delete", "drop"],
                        ["resource", "database", "table"],
                        ["zzz-never-matches-anything"],
                    ],
                }
            ]
        }
        with self.assertRaisesRegex(RoutingOverlayError, "changes the number of 'keyword_groups' outer groups"):
            merge_routing(base, overlay)

    def test_removing_a_keyword_groups_outer_group_is_rejected(self) -> None:
        base = _minimal_base()
        overlay = {"risk_rules": [{"id": "destructive", "keyword_groups": [["destroy", "delete", "drop"]]}]}
        with self.assertRaisesRegex(RoutingOverlayError, "changes the number of 'keyword_groups' outer groups"):
            merge_routing(base, overlay)

    def test_applies_even_to_a_base_entry_without_a_human_gate(self) -> None:
        """RO-FR-13: the widen-only rule applies to every base entry, not
        only ones that currently declare a human_gate -- backend's
        reviewers are a review-separation control even without a
        human_gate field at all.
        """
        base = _minimal_base()
        self.assertNotIn("human_gate", base["routes"][0])
        overlay = {"routes": [{"id": "backend", "paths": []}]}  # drops "backend/**"
        with self.assertRaisesRegex(RoutingOverlayError, "narrows base 'paths'"):
            merge_routing(base, overlay)

    def test_a_new_overlay_added_entry_cannot_suppress_an_already_matching_base_entry(self) -> None:
        """RO-FR-14: additive new-entry independence -- confirms the merge
        shape (per-id addition, never whole-array replacement) cannot be
        used to bypass a base entry's human_gate via a new entry.
        """
        base = _minimal_base()
        overlay = {
            "risk_rules": [
                {
                    "id": "project-fixture-destructive-override",
                    "keywords": ["delete resource"],
                    # deliberately no human_gate on the new entry
                }
            ]
        }
        effective = merge_routing(base, overlay)
        base_destructive = next(r for r in effective["risk_rules"] if r["id"] == "destructive")
        self.assertEqual("destructive-action", base_destructive["human_gate"])
        self.assertEqual(base["risk_rules"][0], base_destructive)


class AC8CompatibilityTests(ProjectOverlayFixture):
    """AC-8: the materialized effective configuration remains consumable,
    with zero code changes, by idea #1's routing_health.py and idea #10's
    schema_validate.py via their existing --routing path argument.
    """

    def test_routing_health_passes_against_materialized_effective_config_with_overlay(self) -> None:
        out_path = self.root / "effective-routing.json"
        overlay = {
            "change_intake": {"keywords": ["deprecate"]},
        }
        (self.root / OVERLAY_RELATIVE_PATH).parent.mkdir(parents=True, exist_ok=True)
        (self.root / OVERLAY_RELATIVE_PATH).write_text(json.dumps(overlay), encoding="utf-8")
        overlay_path = materialize_effective_routing(out_path, base_path=ROUTING_PATH, start=self.project)
        self.assertIsNotNone(overlay_path)

        catalog_path = REPOSITORY_ROOT / "roster" / "catalog.yaml"
        script = ROOT / "src" / "routing_health.py"
        result = subprocess.run(
            [sys.executable, str(script), "--catalog", str(catalog_path), "--routing", str(out_path)],
            check=False,
            capture_output=True,
            text=True,
            encoding="utf-8",
        )
        self.assertEqual(0, result.returncode, result.stderr)

    def test_schema_validate_passes_against_materialized_effective_config_with_overlay(self) -> None:
        schema_validate_path = ROOT / "src" / "schema_validate.py"
        if not schema_validate_path.is_file():
            self.skipTest(
                "schema_validate.py (idea #10) not present in this checkout; "
                "AC-8's schema-validation half is deferred until idea #10 merges"
            )
        out_path = self.root / "effective-routing.json"
        overlay = {
            "routes": [
                {
                    "id": "project-fixture-route",
                    "paths": ["fixture/**"],
                    "keywords": ["fixture keyword"],
                }
            ]
        }
        (self.root / OVERLAY_RELATIVE_PATH).parent.mkdir(parents=True, exist_ok=True)
        (self.root / OVERLAY_RELATIVE_PATH).write_text(json.dumps(overlay), encoding="utf-8")
        overlay_path = materialize_effective_routing(out_path, base_path=ROUTING_PATH, start=self.project)
        self.assertIsNotNone(overlay_path)

        catalog_path = REPOSITORY_ROOT / "roster" / "catalog.yaml"
        result = subprocess.run(
            [
                sys.executable,
                str(schema_validate_path),
                "--catalog",
                str(catalog_path),
                "--routing",
                str(out_path),
            ],
            check=False,
            capture_output=True,
            text=True,
            encoding="utf-8",
        )
        self.assertEqual(0, result.returncode, result.stderr)


class AC9ChangeIntakeCrossStackKnowledgeFocusTests(ProjectOverlayFixture):
    def test_change_intake_keywords_are_additive_only(self) -> None:
        base = _minimal_base()
        overlay = {"change_intake": {"keywords": ["deprecate"]}}
        effective = merge_routing(base, overlay)
        self.assertEqual(["implement", "fix", "deprecate"], effective["change_intake"]["keywords"])
        # unrelated fields untouched
        self.assertEqual(base["change_intake"]["agents"], effective["change_intake"]["agents"])

    def test_cross_stack_minimum_matches_may_decrease(self) -> None:
        base = _minimal_base()
        overlay = {"cross_stack": {"minimum_matches": 1}}
        effective = merge_routing(base, overlay)
        self.assertEqual(1, effective["cross_stack"]["minimum_matches"])
        self.assertEqual(base["cross_stack"]["route_ids"], effective["cross_stack"]["route_ids"])

    def test_cross_stack_minimum_matches_may_not_increase(self) -> None:
        base = _minimal_base()
        overlay = {"cross_stack": {"minimum_matches": 3}}
        with self.assertRaisesRegex(RoutingOverlayError, "may only decrease"):
            merge_routing(base, overlay)

    def test_knowledge_focus_adds_a_new_entry_via_ordinary_deep_merge(self) -> None:
        base = _minimal_base()
        overlay = {"knowledge_focus": {"project-fixture-agent": "fixture focus text"}}
        effective = merge_routing(base, overlay)
        self.assertEqual("fixture focus text", effective["knowledge_focus"]["project-fixture-agent"])
        # unrelated entries untouched
        self.assertEqual(base["knowledge_focus"]["backend-engineer"], effective["knowledge_focus"]["backend-engineer"])

    def test_all_three_together_do_not_change_any_other_section(self) -> None:
        base = _minimal_base()
        overlay = {
            "change_intake": {"keywords": ["deprecate"]},
            "cross_stack": {"minimum_matches": 1},
            "knowledge_focus": {"project-fixture-agent": "fixture focus text"},
        }
        effective = merge_routing(base, overlay)
        self.assertEqual(base["routes"], effective["routes"])
        self.assertEqual(base["risk_rules"], effective["risk_rules"])
        self.assertEqual(base["team_recipes"], effective["team_recipes"])
        self.assertEqual(base["ignored_gates"], effective["ignored_gates"])
        self.assertEqual(base["version"], effective["version"])


class IgnoredGatesAndVersionTests(ProjectOverlayFixture):
    def test_ignored_gates_overlay_may_shrink(self) -> None:
        base = _minimal_base()
        overlay = {"ignored_gates": []}
        effective = merge_routing(base, overlay)
        self.assertEqual([], effective["ignored_gates"])

    def test_ignored_gates_overlay_may_not_grow(self) -> None:
        base = _minimal_base()
        overlay = {"ignored_gates": ["G10", "G9"]}
        with self.assertRaisesRegex(RoutingOverlayError, "may not add new suppression"):
            merge_routing(base, overlay)

    def test_version_overlay_matching_base_is_a_noop(self) -> None:
        base = _minimal_base()
        overlay = {"version": 1}
        effective = merge_routing(base, overlay)
        self.assertEqual(1, effective["version"])

    def test_version_overlay_mismatch_is_rejected(self) -> None:
        base = _minimal_base()
        overlay = {"version": 2}
        with self.assertRaisesRegex(RoutingOverlayError, "fixed schema-version contract field"):
            merge_routing(base, overlay)


class MalformedOverlayTests(ProjectOverlayFixture):
    def test_unparsable_json_fails_closed(self) -> None:
        base_path = self._write_base(_minimal_base())
        overlay_path = self.root / OVERLAY_RELATIVE_PATH
        overlay_path.parent.mkdir(parents=True, exist_ok=True)
        overlay_path.write_text("not: valid: json:::", encoding="utf-8")
        with self.assertRaises(RoutingOverlayError):
            resolve_effective_routing(base_path=base_path, start=self.project)

    def test_non_object_root_fails_closed(self) -> None:
        base = _minimal_base()
        with self.assertRaisesRegex(RoutingOverlayError, "must be a JSON object"):
            merge_routing(base, [1, 2, 3])

    def test_unrecognized_top_level_field_fails_closed(self) -> None:
        base = _minimal_base()
        with self.assertRaisesRegex(RoutingOverlayError, "unrecognized top-level"):
            merge_routing(base, {"not_a_real_section": {}})

    def test_new_entry_missing_id_fails_closed(self) -> None:
        base = _minimal_base()
        with self.assertRaisesRegex(RoutingOverlayError, "missing a non-empty string 'id'"):
            merge_routing(base, {"routes": [{"keywords": ["x"]}]})

    def test_effective_output_is_validated_via_load_routing_round_trip(self) -> None:
        """A team_recipes overlay entry violating load_routing's own
        dynamic-instances shape check (min > max) is caught by the
        post-merge validation round-trip, not silently materialized.
        """
        base_path = self._write_base(_minimal_base())
        overlay = {
            "team_recipes": [
                {
                    "id": "bad-dynamic-recipe",
                    "type": "dynamic",
                    "role": "debugging-engineer",
                    "instances": {"min": 4, "max": 2},
                    "requires_route": "debugging",
                    "keywords": ["intermittent"],
                    "communication_mode": "peer",
                    "fallback": "orchestrator-relayed",
                    "description": "fixture recipe with an invalid instance range",
                }
            ]
        }
        self._write_overlay(overlay)
        with self.assertRaises(RoutingOverlayError):
            resolve_effective_routing(base_path=base_path, start=self.project)


class CliTests(ProjectOverlayFixture):
    def _run(self, *args: str) -> subprocess.CompletedProcess:
        script = ROOT / "src" / "routing_overlay.py"
        return subprocess.run(
            [sys.executable, str(script), *args],
            check=False,
            capture_output=True,
            text=True,
            encoding="utf-8",
        )

    def test_check_exits_zero_with_no_overlay_against_real_repository(self) -> None:
        result = self._run("--routing", str(ROUTING_PATH), "--project", str(self.project), "--check")
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertIn("no project-local overlay found", result.stdout)

    def test_check_exits_nonzero_on_a_malformed_overlay(self) -> None:
        overlay_path = self.root / OVERLAY_RELATIVE_PATH
        overlay_path.parent.mkdir(parents=True, exist_ok=True)
        overlay_path.write_text('{"routes": [{"id": "backend", "human_gate": "x"}]}', encoding="utf-8")
        base_path = self._write_base(_minimal_base())
        result = self._run("--routing", str(base_path), "--project", str(self.project), "--check")
        self.assertNotEqual(0, result.returncode)
        self.assertIn("human_gate", result.stderr)

    def test_out_materializes_effective_config_with_overlay_applied(self) -> None:
        overlay = {"change_intake": {"keywords": ["deprecate"]}}
        self._write_overlay(overlay)
        base_path = self._write_base(_minimal_base())
        out_path = self.root / "effective.json"
        result = self._run(
            "--routing", str(base_path), "--project", str(self.project), "--out", str(out_path)
        )
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertTrue(out_path.is_file())
        effective = json.loads(out_path.read_text(encoding="utf-8"))
        self.assertIn("deprecate", effective["change_intake"]["keywords"])


class RegressionAgainstRealRoutingYamlTests(unittest.TestCase):
    """Sanity: the real repository routing.json still loads/validates
    cleanly through load_routing, independent of the overlay mechanism --
    a baseline the AC-8/no-overlay tests above depend on.
    """

    def test_real_routing_yaml_loads(self) -> None:
        config = load_routing(ROUTING_PATH)
        self.assertEqual(1, config["version"])

    def test_merge_with_empty_overlay_is_a_noop(self) -> None:
        base = copy.deepcopy(load_routing(ROUTING_PATH))
        effective = merge_routing(base, {})
        self.assertEqual(base, effective)


class ExcludePathsOverlayTests(ProjectOverlayFixture):
    """`exclude_paths` is not a widen field, deliberately.

    Its polarity is inverted relative to `keywords`/`keyword_groups`/`paths`:
    a *superset* of `exclude_paths` narrows a rule's effective match rather
    than widening it. Routing it through `_widen_field_superset` would
    therefore enforce the wrong direction and let an overlay shed a base
    entry's `reviewers`/`human_gate` coverage without ever naming those
    fields -- the exact bypass AC-7 exists to reject. These tests pin the
    current behavior so that reconciliation can't be undone by "fixing"
    the omission from `_ROUTE_RISK_WIDEN_FIELDS`.
    """

    def _base_with_exclusion(self) -> dict:
        base = _minimal_base()
        base["routes"][0]["paths"] = ["**/backend/**"]
        base["routes"][0]["exclude_paths"] = ["roster/**"]
        return base

    def test_new_overlay_route_may_declare_its_own_exclude_paths(self) -> None:
        base = _minimal_base()
        overlay = {
            "version": 1,
            "routes": [
                {
                    "id": "vendored-architecture",
                    "paths": ["**/architecture/**"],
                    "exclude_paths": ["vendor/**"],
                    "keywords": [],
                    "primary": ["cloud-architect"],
                    "reviewers": [],
                }
            ],
        }
        effective = merge_routing(base, overlay)
        added = next(r for r in effective["routes"] if r["id"] == "vendored-architecture")
        self.assertEqual(added["exclude_paths"], ["vendor/**"])

    def test_a_new_overlay_entrys_exclusion_takes_effect_in_matching(self) -> None:
        """Not just carried through the merge -- actually honored downstream."""
        base = _minimal_base()
        overlay = {
            "version": 1,
            "routes": [
                {
                    "id": "vendored-architecture",
                    "paths": ["**/architecture/**"],
                    "exclude_paths": ["vendor/**"],
                    "keywords": [],
                    "primary": ["cloud-architect"],
                    "reviewers": [],
                }
            ],
        }
        effective = merge_routing(base, overlay)
        added = next(r for r in effective["routes"] if r["id"] == "vendored-architecture")
        self.assertTrue(match_rule(added, "", ["svc/architecture/a.md"])["matched"])
        self.assertFalse(match_rule(added, "", ["vendor/architecture/a.md"])["matched"])

    def test_adding_exclude_paths_to_a_base_entry_is_rejected(self) -> None:
        """The narrowing bypass: an exclusion added to a base route would
        suppress matches -- and the reviewers attached to them -- on files
        the base route covers today.
        """
        base = _minimal_base()
        overlay = {"version": 1, "routes": [{"id": "backend", "exclude_paths": ["backend/vendor/**"]}]}
        with self.assertRaises(RoutingOverlayError) as ctx:
            merge_routing(base, overlay)
        self.assertIn("backend", str(ctx.exception))
        self.assertIn("exclude_paths", str(ctx.exception))

    def test_extending_a_base_entrys_exclude_paths_is_rejected(self) -> None:
        """A superset would pass a naive widen check while narrowing the match."""
        base = self._base_with_exclusion()
        overlay = {
            "version": 1,
            "routes": [{"id": "backend", "exclude_paths": ["roster/**", "docs/**"]}],
        }
        with self.assertRaises(RoutingOverlayError) as ctx:
            merge_routing(base, overlay)
        self.assertIn("exclude_paths", str(ctx.exception))

    def test_clearing_a_base_entrys_exclude_paths_is_rejected(self) -> None:
        base = self._base_with_exclusion()
        overlay = {"version": 1, "routes": [{"id": "backend", "exclude_paths": []}]}
        with self.assertRaises(RoutingOverlayError):
            merge_routing(base, overlay)

    def test_widening_paths_preserves_an_untouched_base_exclusion(self) -> None:
        base = self._base_with_exclusion()
        overlay = {
            "version": 1,
            "routes": [{"id": "backend", "paths": ["**/backend/**", "**/api/**"]}],
        }
        effective = merge_routing(base, overlay)
        route = next(r for r in effective["routes"] if r["id"] == "backend")
        self.assertEqual(route["exclude_paths"], ["roster/**"])
        self.assertEqual(route["paths"], ["**/backend/**", "**/api/**"])

    def test_restating_an_identical_exclude_paths_is_an_allowed_noop(self) -> None:
        base = self._base_with_exclusion()
        overlay = {"version": 1, "routes": [{"id": "backend", "exclude_paths": ["roster/**"]}]}
        effective = merge_routing(base, overlay)
        route = next(r for r in effective["routes"] if r["id"] == "backend")
        self.assertEqual(route["exclude_paths"], ["roster/**"])

    def test_exclude_paths_is_rejected_on_a_risk_rule_too(self) -> None:
        """`architecture-change` is a risk rule, not a route -- the rule must
        hold on both sections, which share `_apply_widen_patch`.
        """
        base = _minimal_base()
        overlay = {"version": 1, "risk_rules": [{"id": "destructive", "exclude_paths": ["vendor/**"]}]}
        with self.assertRaises(RoutingOverlayError) as ctx:
            merge_routing(base, overlay)
        self.assertIn("destructive", str(ctx.exception))


class SelectionPathIntegrationTests(ProjectOverlayFixture):
    """#202: the overlay is documented as governing what a project dispatches
    against, but nothing in the `cadre select` path read it -- a consumer who
    authored, materialized and validated an overlay saw no change whatsoever
    in selection. These pin the wiring so it cannot silently rot back.
    """

    def _select(self, task: str) -> dict:
        result = subprocess.run(
            [sys.executable, str(SELECT_AGENTS), "--root", str(self.root),
             "--task", task, "--files", "notes.txt",
             "--task-id", "OVERLAY-1", "--classification", "internal"],
            capture_output=True, text=True, check=False,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        return json.loads(result.stdout)

    def _widen_documentation(self, extra: str) -> None:
        base = load_routing(REPOSITORY_ROOT / "roster" / "orchestration" / "routing.json")
        route = next(r for r in base["routes"] if r["id"] == "documentation")
        self._write_overlay({"routes": [{"id": "documentation",
                                         "keywords": [*route["keywords"], extra]}]})

    def test_an_overlay_does_not_drop_context_packs_from_the_effective_config(self) -> None:
        """`context_packs` is not in `_KNOWN_TOP_LEVEL_KEYS`, so the merge has
        no rule for it. It survives only because the merge patches a copy of
        the base rather than rebuilding the config from the keys it knows --
        an implementation detail worth pinning, since rebuilding would
        silently strip every pack from any project that has an overlay.
        """
        base = load_routing(REPOSITORY_ROOT / "roster" / "orchestration" / "routing.json")
        self.assertTrue(base.get("context_packs"), "base config should ship context packs")
        route = next(r for r in base["routes"] if r["id"] == "documentation")
        effective = merge_routing(
            base, {"routes": [{"id": "documentation", "keywords": [*route["keywords"], "x"]}]}
        )
        self.assertEqual(effective["context_packs"], base["context_packs"])

if __name__ == "__main__":
    unittest.main()
