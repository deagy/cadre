"""JSON Schema validation tests for `roster/catalog.yaml` and
`roster/orchestration/routing.json`.

Distinct from, and additive to, two existing checks that this file's tests
must not weaken or duplicate:

- `test_routing_coverage.py` (idea #1's `routing_health.py`): reachability/
  orphan/dangling-reference coverage, not shape/type/enum validity.
- `test_role_metadata.py` (`generate_role_metadata.py --check`): generation-
  drift between catalog.yaml/routing.json and AGENT.md frontmatter.

This module tests `roster/orchestration/src/schema_validate.py` -- a third,
independent question ("is this file's own shape/type/enum content valid"),
answerable standalone without AGENT.md frontmatter and without invoking any
generator. See requirements.md (INTENT-CADRE-BACKLOG-10 /
REQ-CADRE-BACKLOG-10) acceptance criteria AC-1..AC-8 for the traceability
this test file follows.

`jsonschema` is an already-approved, pinned CI dependency
(`.github/requirements-validation.lock`), the same one
`test_selector.py::test_selection_schema_rejects_malformed_closed_contracts`
already relies on -- but it is not guaranteed to be installed in every local
sandbox, so every test in this module is guarded the same way
`test_selector.py` guards its `AGENTIC_SDLC_BIN`-dependent tests: a module-
level availability flag plus `@unittest.skipUnless`.
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

try:
    import jsonschema  # noqa: F401

    JSONSCHEMA_AVAILABLE = True
except ImportError:
    JSONSCHEMA_AVAILABLE = False

if JSONSCHEMA_AVAILABLE:
    import schema_validate as sv  # noqa: E402

CATALOG_PATH = REPOSITORY_ROOT / "roster" / "catalog.yaml"
ROUTING_PATH = REPOSITORY_ROOT / "roster" / "orchestration" / "routing.json"
CATALOG_SCHEMA_PATH = REPOSITORY_ROOT / "roster" / "catalog.schema.json"
ROUTING_SCHEMA_PATH = REPOSITORY_ROOT / "roster" / "orchestration" / "routing.schema.json"
VALIDATOR_SCRIPT = ROOT / "src" / "schema_validate.py"


def _load_catalog() -> tuple[str, dict]:
    import yaml

    text = CATALOG_PATH.read_text(encoding="utf-8")
    return text, yaml.safe_load(text)


def _load_routing() -> dict:
    return json.loads(ROUTING_PATH.read_text(encoding="utf-8"))


def _load_catalog_schema() -> dict:
    return json.loads(CATALOG_SCHEMA_PATH.read_text(encoding="utf-8"))


def _load_routing_schema() -> dict:
    return json.loads(ROUTING_SCHEMA_PATH.read_text(encoding="utf-8"))


@unittest.skipUnless(JSONSCHEMA_AVAILABLE, "jsonschema is required")
class CleanBaselineTests(unittest.TestCase):
    """AC-1 / SC-2: the current, unmodified repository files validate with
    zero findings -- proves the schemas do not false-positive against
    today's approved content.
    """

    def test_current_catalog_and_routing_have_zero_findings(self) -> None:
        findings = sv.run(CATALOG_PATH, ROUTING_PATH, CATALOG_SCHEMA_PATH, ROUTING_SCHEMA_PATH)
        self.assertEqual([], findings)

    def test_schemas_are_draft_2020_12(self) -> None:
        # Matches selection.schema.json's precedent draft version.
        self.assertEqual(
            "https://json-schema.org/draft/2020-12/schema", _load_catalog_schema()["$schema"]
        )
        self.assertEqual(
            "https://json-schema.org/draft/2020-12/schema", _load_routing_schema()["$schema"]
        )


@unittest.skipUnless(JSONSCHEMA_AVAILABLE, "jsonschema is required")
class CatalogNegativeFixtureTests(unittest.TestCase):
    def test_invalid_phase_enum_is_reported_with_role_id_field_and_allowed_values(self) -> None:
        # AC-2: an unrecognized `phase` value names the role id, the field,
        # the offending value, and (via the enum's own message) the allowed
        # set.
        text, catalog = _load_catalog()
        catalog["agents"]["code-reviewer"]["phase"] = "not-a-real-phase"
        findings = sv.validate_catalog(text, catalog, _load_catalog_schema(), CATALOG_PATH.parent)
        self.assertTrue(findings)
        joined = "\n".join(findings)
        self.assertIn("code-reviewer", joined)
        self.assertIn("phase", joined)
        self.assertIn("not-a-real-phase", joined)

    def test_tier_mismatch_is_reported_naming_role_and_mismatched_field(self) -> None:
        # AC-3: model: opus paired with reasoning_effort: medium is a
        # cross-field inconsistency, not just a bad enum value. A oneOf
        # failure's *base* message is jsonschema's generic "not valid under
        # any of the given schemas" against the whole role dict -- which
        # would trivially contain "reasoning_effort" as a substring of its
        # own repr regardless of whether field-level precision actually
        # works. Assert on the "(most specific: ...)" sub-error segment
        # specifically (see _format_error's best_match handling), which is
        # the part that is actually supposed to name the mismatched field.
        text, catalog = _load_catalog()
        catalog["agents"]["code-reviewer"]["model"] = "opus"
        # reasoning_effort/codex_model are left at code-reviewer's original
        # sonnet-tier values (gpt-5.6-terra/medium), so this is a genuine
        # tuple mismatch, not merely an invalid enum value.
        findings = sv.validate_catalog(text, catalog, _load_catalog_schema(), CATALOG_PATH.parent)
        self.assertTrue(findings)
        joined = "\n".join(findings)
        self.assertIn("code-reviewer", joined)
        self.assertIn("(most specific:", joined)
        most_specific_segment = joined.split("(most specific:", 1)[1]
        self.assertIn("reasoning_effort", most_specific_segment)

    def test_missing_required_field_is_reported(self) -> None:
        text, catalog = _load_catalog()
        del catalog["agents"]["code-reviewer"]["capability"]
        findings = sv.validate_catalog(text, catalog, _load_catalog_schema(), CATALOG_PATH.parent)
        joined = "\n".join(findings)
        self.assertIn("code-reviewer", joined)
        self.assertIn("capability", joined)

    def test_duplicate_role_id_is_reported_for_every_duplicate(self) -> None:
        text, _catalog = _load_catalog()
        duplicated_text = text.replace(
            "  code-reviewer:\n    definition: review/code-reviewer/AGENT.md\n",
            "  code-reviewer:\n    definition: review/code-reviewer/AGENT.md\n"
            "  code-reviewer:\n    definition: review/code-reviewer/AGENT.md\n",
            1,
        )
        findings = sv._find_duplicate_catalog_ids(duplicated_text)
        self.assertEqual(1, len(findings))
        self.assertIn("code-reviewer", findings[0])
        self.assertIn("duplicate", findings[0])

    def test_nonexistent_definition_path_is_reported(self) -> None:
        # SV-FR-2: a filesystem check, not a JSON Schema concern.
        _text, catalog = _load_catalog()
        catalog["agents"]["code-reviewer"]["definition"] = "review/does-not-exist/AGENT.md"
        findings = sv._find_missing_definitions(catalog, CATALOG_PATH.parent)
        self.assertEqual(1, len(findings))
        self.assertIn("code-reviewer", findings[0])
        self.assertIn("does-not-exist", findings[0])


@unittest.skipUnless(JSONSCHEMA_AVAILABLE, "jsonschema is required")
class RoutingNegativeFixtureTests(unittest.TestCase):
    def test_wrong_type_field_is_reported_with_route_id_and_location(self) -> None:
        # AC-4: a route's `reviewers` set to a string instead of a list.
        routing = copy.deepcopy(_load_routing())
        routing["routes"][0]["reviewers"] = "not-a-list"
        findings = sv.validate_routing(routing, _load_routing_schema())
        self.assertTrue(findings)
        joined = "\n".join(findings)
        self.assertIn("routes", joined)
        self.assertIn("reviewers", joined)
        self.assertIn("0", joined)

    def test_team_recipe_cross_contamination_is_reported(self) -> None:
        # AC-5: a `type: "fixed"` recipe that also carries a dynamic-only
        # field (`requires_route`). The fixed/dynamic split is expressed via
        # a `not`/`anyOf` exclusion (see routing.schema.json's teamRecipe
        # oneOf) rather than a `const`/`enum` mismatch, so jsonschema's
        # best_match mechanism cannot dive into a "most specific" sub-error
        # the way it does for the catalog tier-mismatch case above -- the
        # container-level message is genuinely the most precise pointer
        # available. SV-FR-23 requires the out-of-type field to be present
        # in the finding, which the container's own dict-repr message
        # satisfies (confirmed below), not a per-field pointer.
        routing = copy.deepcopy(_load_routing())
        fixed_index, fixed_recipe = next(
            (index, recipe) for index, recipe in enumerate(routing["team_recipes"]) if recipe["type"] == "fixed"
        )
        fixed_recipe["requires_route"] = "debugging"
        findings = sv.validate_routing(routing, _load_routing_schema())
        self.assertEqual(1, len(findings))
        self.assertIn(f"$['team_recipes'][{fixed_index}]", findings[0])
        self.assertIn(fixed_recipe["id"], findings[0])
        self.assertIn("requires_route", findings[0])
        self.assertIn("debugging", findings[0])

    def test_cross_stack_minimum_matches_exceeding_route_ids_is_reported(self) -> None:
        # SV-FR-20 / Gap G-5: a candidate net-new consistency check
        # implemented as a supplementary Python check, not via JSON Schema.
        routing = copy.deepcopy(_load_routing())
        routing["cross_stack"]["minimum_matches"] = len(routing["cross_stack"]["route_ids"]) + 5
        findings = sv._find_cross_stack_inconsistency(routing)
        self.assertEqual(1, len(findings))
        self.assertIn("cross_stack.minimum_matches", findings[0])

    def test_team_recipe_minimum_matches_exceeding_route_ids_is_reported(self) -> None:
        routing = copy.deepcopy(_load_routing())
        fixed_recipe = next(recipe for recipe in routing["team_recipes"] if recipe["type"] == "fixed")
        fixed_recipe["minimum_matches"] = len(fixed_recipe["route_ids"]) + 3
        findings = sv._find_team_recipe_inconsistencies(routing)
        self.assertEqual(1, len(findings))
        self.assertIn(fixed_recipe["id"], findings[0])
        self.assertIn("minimum_matches", findings[0])

    def test_team_recipe_minimum_members_selected_exceeding_members_is_reported(self) -> None:
        routing = copy.deepcopy(_load_routing())
        fixed_recipe = next(recipe for recipe in routing["team_recipes"] if recipe["type"] == "fixed")
        fixed_recipe["minimum_members_selected"] = len(fixed_recipe["members"]) + 3
        findings = sv._find_team_recipe_inconsistencies(routing)
        self.assertEqual(1, len(findings))
        self.assertIn(fixed_recipe["id"], findings[0])
        self.assertIn("minimum_members_selected", findings[0])

    def test_duplicate_route_id_is_reported(self) -> None:
        routing = copy.deepcopy(_load_routing())
        routing["routes"].append(dict(routing["routes"][0]))
        findings = sv._find_duplicate_array_ids(routing["routes"], "routes")
        self.assertEqual(1, len(findings))
        self.assertIn(routing["routes"][0]["id"], findings[0])

    def test_invalid_communication_mode_enum_is_reported(self) -> None:
        routing = copy.deepcopy(_load_routing())
        routing["team_recipes"][0]["communication_mode"] = "carrier-pigeon"
        findings = sv.validate_routing(routing, _load_routing_schema())
        joined = "\n".join(findings)
        self.assertIn("communication_mode", joined)

    def test_gate_id_shape_violation_is_reported(self) -> None:
        routing = copy.deepcopy(_load_routing())
        routing["routes"][0]["quality_gates"] = ["not-a-gate-id"]
        findings = sv.validate_routing(routing, _load_routing_schema())
        joined = "\n".join(findings)
        self.assertIn("quality_gates", joined)


@unittest.skipUnless(JSONSCHEMA_AVAILABLE, "jsonschema is required")
class MultiDefectSinglePassTests(unittest.TestCase):
    def test_two_independent_defects_are_both_reported_in_one_run(self) -> None:
        # AC-6 / SV-FR-26: two unrelated defects, one per file, both surface
        # from a single validator run rather than stopping at the first.
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            catalog_path = root / "catalog.yaml"
            routing_path = root / "routing.json"

            text, catalog = _load_catalog()
            catalog["agents"]["code-reviewer"]["phase"] = "not-a-real-phase"
            import yaml

            catalog_path.write_text(yaml.safe_dump(catalog, sort_keys=False), encoding="utf-8")

            routing = _load_routing()
            routing["routes"][0]["reviewers"] = "not-a-list"
            routing_path.write_text(json.dumps(routing), encoding="utf-8")

            findings = sv.run(catalog_path, routing_path, CATALOG_SCHEMA_PATH, ROUTING_SCHEMA_PATH, root)
            self.assertGreaterEqual(len(findings), 2)
            joined = "\n".join(findings)
            self.assertIn("phase", joined)
            self.assertIn("reviewers", joined)

    def test_two_independent_defects_within_routing_yaml_alone_are_both_reported(self) -> None:
        routing = copy.deepcopy(_load_routing())
        routing["routes"][0]["reviewers"] = "not-a-list"
        routing["cross_stack"]["minimum_matches"] = len(routing["cross_stack"]["route_ids"]) + 5
        findings = sv.validate_routing(routing, _load_routing_schema())
        self.assertGreaterEqual(len(findings), 2)


@unittest.skipUnless(JSONSCHEMA_AVAILABLE, "jsonschema is required")
class StandaloneRunnabilityTests(unittest.TestCase):
    """AC-8 / SV-NFR-1: runs to a pass/fail result standing entirely alone,
    without invoking build_dispatch_plan.py, generate_global_plugin.py, or
    generate_role_metadata.py, and without AGENTIC_SDLC_BIN/agentic-sdlc on
    PATH.
    """

    def test_script_runs_standalone_and_passes_against_current_tree(self) -> None:
        # Deliberately does not set AGENTIC_SDLC_BIN and does not invoke
        # build_dispatch_plan.py/generate_global_plugin.py/
        # generate_role_metadata.py first -- the script alone, from a clean
        # process, must pass against the current tree.
        result = subprocess.run(
            [sys.executable, str(VALIDATOR_SCRIPT)],
            cwd=REPOSITORY_ROOT,
            check=False,
            capture_output=True,
            text=True,
            encoding="utf-8",
        )
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertIn("schema validation passed", result.stdout)

    def test_script_exits_nonzero_with_findings_on_stderr_for_malformed_input(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            catalog_path = root / "catalog.yaml"
            routing_path = root / "routing.json"
            catalog_path.write_text("version: 1\nagents: {}\n", encoding="utf-8")
            routing_path.write_text(json.dumps(_load_routing()), encoding="utf-8")

            result = subprocess.run(
                [
                    sys.executable, str(VALIDATOR_SCRIPT),
                    "--catalog", str(catalog_path),
                    "--routing", str(routing_path),
                    "--catalog-schema", str(CATALOG_SCHEMA_PATH),
                    "--routing-schema", str(ROUTING_SCHEMA_PATH),
                ],
                check=False,
                capture_output=True,
                text=True,
                encoding="utf-8",
            )
            self.assertNotEqual(0, result.returncode)
            self.assertTrue(result.stderr.strip())

    def test_script_accepts_path_override_flags(self) -> None:
        result = subprocess.run(
            [
                sys.executable, str(VALIDATOR_SCRIPT),
                "--catalog", str(CATALOG_PATH),
                "--routing", str(ROUTING_PATH),
                "--catalog-schema", str(CATALOG_SCHEMA_PATH),
                "--routing-schema", str(ROUTING_SCHEMA_PATH),
            ],
            cwd=REPOSITORY_ROOT,
            check=False,
            capture_output=True,
            text=True,
            encoding="utf-8",
        )
        self.assertEqual(0, result.returncode, result.stderr)


if __name__ == "__main__":
    unittest.main()
