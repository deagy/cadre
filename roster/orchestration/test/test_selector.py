"""Regression tests for the Python selector and its lifecycle contract inputs."""

from __future__ import annotations

import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

ROOT = Path(__file__).resolve().parent.parent
AGENTS_ROOT = ROOT.parent
sys.path.insert(0, str(ROOT / "src"))

import agentic_sdlc_contracts  # noqa: E402
from build_dispatch_plan import build_dispatch_plan  # noqa: E402
from routing import glob_to_regex, load_catalog, load_routing, match_rule  # noqa: E402
from select_agents import (  # noqa: E402
    _origin_slug,
    discover_changed_files,
    explicit_files,
    resolve_knowledge_source,
)

CONFIG = load_routing(ROOT / "routing.yaml")
CATALOG = load_catalog(AGENTS_ROOT / "catalog.yaml")
AGENTIC_SDLC_AVAILABLE = bool(os.environ.get("AGENTIC_SDLC_BIN") or shutil.which("agentic-sdlc"))


def catalog_definitions() -> dict[str, str]:
    definitions: dict[str, str] = {}
    current_agent: str | None = None
    for line in (AGENTS_ROOT / "catalog.yaml").read_text(encoding="utf-8").splitlines():
        agent_match = line.startswith("  ") and not line.startswith("    ") and line.rstrip().endswith(":")
        if agent_match:
            current_agent = line.strip()[:-1]
        elif current_agent and line.strip().startswith("definition:"):
            definitions[current_agent] = line.split(":", 1)[1].strip()
    return definitions


def plan(**overrides: object) -> dict[str, object]:
    values = {
        "task": "Change the application",
        "changed_files": [],
        "changed_file_source": "test",
        "repository_root": str(AGENTS_ROOT.parent),
        "source": "example/repository",
        **overrides,
    }
    return build_dispatch_plan(CONFIG, CATALOG, values)


class RouteMatchReasonTests(unittest.TestCase):
    """`matched_routes` must explain every route match in the plan itself.

    Route reasons were computed and then discarded, so answering "why did this
    route fire?" meant reading `routing.yaml` and `routing.py` instead of the
    plan. Each entry now carries its own `reasons`, in the same shape
    `matched_risks` uses.
    """

    def test_every_entry_carries_an_id_and_reasons(self) -> None:
        result = plan(
            task="Update the deployment pipeline and the API docs",
            changed_files=["docs/api.md", ".github/workflows/release.yml"],
        )
        self.assertNotEqual(result["matched_routes"], [])
        for match in result["matched_routes"]:
            self.assertEqual(sorted(match), ["id", "reasons"])

    def test_keyword_match_names_the_keyword_that_fired(self) -> None:
        # The diagnostic this field exists for: the plan must say which
        # keyword fired without a source read.
        result = plan(task="the deployment runner failed unexpectedly", changed_files=[])
        reasons = {match["id"]: match["reasons"] for match in result["matched_routes"]}
        self.assertIn("pipeline", reasons)
        self.assertEqual(reasons["pipeline"]["keywords"], ["runner"])
        self.assertEqual(reasons["pipeline"]["paths"], [])

    def test_keyword_boundary_excludes_hyphenated_compounds(self) -> None:
        # Regression pin for the bug this behaviour once had: `pipeline`'s
        # "runner" keyword must not substring-match inside a hyphenated
        # compound like "cross-runner" -- a hyphen is a word character here,
        # not a boundary, so "cross-runner" is one token, not "cross" plus
        # the keyword "runner".
        result = plan(task="improve cross-runner UX documentation", changed_files=[])
        reasons = {match["id"]: match["reasons"] for match in result["matched_routes"]}
        self.assertNotIn("pipeline", reasons)

    def test_keyword_boundary_still_matches_the_word_on_its_own(self) -> None:
        # Same keyword, real word boundaries either side -- must still fire.
        result = plan(task="the shared runner keeps failing", changed_files=[])
        reasons = {match["id"]: match["reasons"] for match in result["matched_routes"]}
        self.assertIn("pipeline", reasons)
        self.assertEqual(reasons["pipeline"]["keywords"], ["runner"])

    def test_keyword_boundary_excludes_other_hyphenated_short_keywords(self) -> None:
        # The same latent fault sat under other short keywords too: "cd",
        # "index", "lock", "alert", "token" all substring-matched inside a
        # hyphenated compound before the boundary fix. Pin one -- "index"
        # inside "re-index-lock" -- as a true negative for its route
        # (database-reliability), and the plain word as a true positive.
        negative = plan(task="schedule a re-index-lock maintenance window", changed_files=[])
        negative_reasons = {match["id"]: match["reasons"] for match in negative["matched_routes"]}
        self.assertNotIn("database-reliability", negative_reasons)

        positive = plan(task="the index needs a maintenance lock", changed_files=[])
        positive_reasons = {match["id"]: match["reasons"] for match in positive["matched_routes"]}
        self.assertIn("database-reliability", positive_reasons)
        self.assertEqual(
            sorted(positive_reasons["database-reliability"]["keywords"]), ["index", "lock"]
        )

    def test_path_match_names_the_pattern_and_the_file(self) -> None:
        result = plan(task="Revise the operator guide", changed_files=["docs/runbook.md"])
        reasons = {match["id"]: match["reasons"] for match in result["matched_routes"]}
        self.assertIn("documentation", reasons)
        self.assertIn(
            {"pattern": "docs/**", "file": "docs/runbook.md"},
            reasons["documentation"]["paths"],
        )

    def test_empty_when_nothing_matches(self) -> None:
        result = plan(task="Rotate the session token used for authorization", changed_files=[])
        self.assertEqual(result["matched_routes"], [])

    def test_reasons_are_deterministic_across_identical_calls(self) -> None:
        # Reasons ride inside the fingerprinted payload, so unstable ordering
        # here would turn `dispatch_fingerprint` into a coin flip.
        arguments = {
            "task": "Update the deployment pipeline and the API docs",
            "changed_files": ["docs/api.md", ".github/workflows/release.yml"],
            "task_id": "DETERMINISM-1",
        }
        first, second = plan(**arguments), plan(**arguments)
        self.assertEqual(
            json.dumps(first["matched_routes"], sort_keys=True),
            json.dumps(second["matched_routes"], sort_keys=True),
        )
        self.assertEqual(first["dispatch_fingerprint"], second["dispatch_fingerprint"])

    def test_route_and_risk_reasons_share_one_shape(self) -> None:
        # Both fields resolve to `$defs/idWithReasonsArray` in the schema; if
        # one grows a key the other doesn't, that ref has been split apart.
        result = plan(
            task="Deploy the API to production",
            changed_files=["services/api/main.go"],
        )
        self.assertNotEqual(result["matched_routes"], [])
        self.assertNotEqual(result["matched_risks"], [])
        for match in [*result["matched_routes"], *result["matched_risks"]]:
            self.assertEqual(sorted(match), ["id", "reasons"])
            self.assertEqual(sorted(match["reasons"]), ["keyword_groups", "keywords", "paths"])


class ExcludePathsTests(unittest.TestCase):
    """`exclude_paths` subtracts at the file level, not the rule level.

    Without it the only fix for a broad glob's false positive was to narrow
    the glob, trading it for false negatives -- #154 narrowed
    `**/architecture/**` to stop this repository's own roster/architecture/
    matching, and lost every nested consuming-project path in the process
    (#156).
    """

    RULE = {"id": "t", "paths": ["**/architecture/**"], "exclude_paths": ["roster/**"]}

    def test_excluded_file_does_not_match(self) -> None:
        result = match_rule(self.RULE, "", ["roster/architecture/interaction-designer/AGENT.md"])
        self.assertFalse(result["matched"])
        self.assertEqual(result["paths"], [])

    def test_non_excluded_file_still_matches(self) -> None:
        for path in ("docs/architecture/adr.md", "services/pay/architecture/topology.md"):
            with self.subTest(path=path):
                self.assertTrue(match_rule(self.RULE, "", [path])["matched"])

    def test_exclusion_is_per_file_not_per_rule(self) -> None:
        # One excluded file must not suppress a genuine match in the same
        # change set -- the rule still fires, and reports only the file that
        # actually matched.
        result = match_rule(
            self.RULE,
            "",
            ["roster/architecture/interaction-designer/AGENT.md", "docs/architecture/adr.md"],
        )
        self.assertTrue(result["matched"])
        self.assertEqual([entry["file"] for entry in result["paths"]], ["docs/architecture/adr.md"])

    def test_every_pattern_in_a_multi_entry_exclude_list_is_applied(self) -> None:
        # The single-pattern cases above pass even if `any()` only ever
        # consulted the first excluder; this pins that each entry is live.
        rule = {
            "id": "t",
            "paths": ["**/architecture/**"],
            "exclude_paths": ["roster/**", "vendor/**", "third_party/**"],
        }
        for path in (
            "roster/architecture/x.md",
            "vendor/architecture/x.md",
            "third_party/architecture/x.md",
        ):
            with self.subTest(excluded=path):
                self.assertFalse(match_rule(rule, "", [path])["matched"])
        self.assertTrue(match_rule(rule, "", ["services/pay/architecture/x.md"])["matched"])

    def test_an_exclude_pattern_matching_nothing_is_a_no_op(self) -> None:
        rule = {"id": "t", "paths": ["**/architecture/**"], "exclude_paths": ["never/matches/**"]}
        self.assertTrue(match_rule(rule, "", ["roster/architecture/x.md"])["matched"])

    def test_an_exclude_that_shadows_its_whole_include_matches_nothing(self) -> None:
        # Documents current behavior rather than endorsing it: a rule whose
        # exclusions swallow its own include set silently falls back to
        # keyword-only matching, with no health-check or schema complaint.
        # Tracked separately as #162.
        rule = {"id": "t", "paths": ["foo/**"], "exclude_paths": ["foo/**"], "keywords": []}
        self.assertFalse(match_rule(rule, "", ["foo/bar.py"])["matched"])

    def test_absent_exclude_paths_changes_nothing(self) -> None:
        rule = {"id": "t", "paths": ["**/architecture/**"]}
        self.assertTrue(match_rule(rule, "", ["roster/architecture/x.md"])["matched"])


class SelectorTests(unittest.TestCase):
    @staticmethod
    def quality_gate_ids(result: dict[str, object]) -> list[str]:
        return [gate["id"] for gate in result["required_quality_gates"]]

    def test_catalog_definition_paths_exist(self) -> None:
        definitions = catalog_definitions()
        self.assertEqual(set(CATALOG), set(definitions))
        for agent, relative_path in definitions.items():
            with self.subTest(agent=agent):
                self.assertTrue((AGENTS_ROOT / relative_path).is_file(), relative_path)

    def test_glob_matching_supports_root_and_nested_paths(self) -> None:
        self.assertIsNotNone(glob_to_regex("**/*.go").search("main.go"))
        self.assertIsNotNone(glob_to_regex("**/*.go").search("services/api/main.go"))
        self.assertIsNotNone(glob_to_regex("terraform/**").search("terraform/modules/vm/main.tf"))
        self.assertIsNotNone(glob_to_regex(".gitlab-ci.yml").search(".gitlab-ci.yml"))
        self.assertIsNone(glob_to_regex("**/*.go").search("main.ts"))

    def test_plugin_packaging_routes_to_agent_suite_governance(self) -> None:
        result = plan(
            task="Package the Secure Cloud Agentic SDLC provider",
            changed_files=[
                "provider/provider.json",
                "provider/agent-catalog.json",
            ],
            classification="internal",
            task_id="PLUGIN-1",
        )
        self.assertEqual(result["workflow"], "agent-suite-maintenance")
        self.assertIn("application-engineer", result["agents"]["primary"])
        self.assertIn("debugging-engineer", result["agents"]["primary"])
        self.assertIn("test-engineer", result["agents"]["reviewers"])

    def test_selects_frontend_and_backend_with_cross_stack_coordination(self) -> None:
        result = plan(
            task="Add a React upload form backed by a PostgreSQL API",
            changed_files=["frontend/src/Upload.tsx", "services/upload/main.go"],
            classification="internal",
            task_id="APP-42",
        )
        self.assertEqual(result["status"], "ready")
        self.assertEqual(result["workflow"], "new-service")
        self.assertEqual(result["agents"]["primary"], ["frontend-engineer", "backend-engineer"])
        self.assertIn("test-engineer", result["agents"]["reviewers"])
        self.assertIn("code-reviewer", result["agents"]["reviewers"])
        # cross_stack.support is [frontend-engineer, backend-engineer], but
        # both are already primary here, so the de-dup in build_dispatch_plan
        # filters them back out of support -- application-engineer is no
        # longer part of this cross-stack path at all (see AGENT.md: it's
        # scoped to this suite's own tooling, not a target project's app).
        # interaction-designer remains, contributed by the frontend route's
        # own support list, independent of cross_stack.
        self.assertEqual(result["agents"]["support"], ["interaction-designer"])
        self.assertEqual(result["knowledge_context"]["status"], "planned")
        requests = result["knowledge_context"]["requests"]
        self.assertTrue(any(request["agent"] == "frontend-engineer" for request in requests))
        self.assertTrue(all("APP-42" in request["invocation"]["args"] for request in requests))
        self.assertTrue(all("\n" not in request["query"] and "\r" not in request["query"] for request in requests))
        expected_launcher = {
            "runtime": "python",
            "minimum_version": "3.10",
            "resolution": "runner-probed",
        }
        self.assertTrue(all(request["invocation"]["launcher"] == expected_launcher for request in requests))
        self.assertTrue(all(Path(request["invocation"]["args"][0]).is_absolute() for request in requests))
        self.assertTrue(all(request["invocation"]["args"][1] == "context" for request in requests))

    def test_selects_interaction_designer_as_frontend_support(self) -> None:
        result = plan(
            task="Design the upload flow interaction states for the new UI",
            changed_files=["frontend/src/Upload.tsx"],
            classification="internal",
        )
        self.assertIn("frontend-engineer", result["agents"]["primary"])
        self.assertIn("interaction-designer", result["agents"]["support"])
        self.assertIn("accessibility-reviewer", result["agents"]["reviewers"])

    def test_selects_decommission_engineer_for_service_retirement(self) -> None:
        result = plan(
            task="Plan the decommission and retirement of the legacy upload service",
            changed_files=["decommission/upload-service.json"],
            classification="internal",
        )
        self.assertEqual(result["agents"]["primary"], ["decommission-engineer"])
        self.assertIn("security-reviewer", result["agents"]["reviewers"])
        self.assertIn("compliance-reviewer", result["agents"]["reviewers"])

    def test_selects_halt_authority_for_halt_determination(self) -> None:
        result = plan(
            task="Issue a halt determination for this workstream",
            changed_files=[],
            classification="internal",
        )
        self.assertEqual(result["agents"]["primary"], ['halt-authority'])
        # Absolute-force finding: structurally backed by a human_gate, not
        # just asserted in AGENT.md prose.
        self.assertEqual([g["id"] for g in result["human_gates"]], ["halt-authority-determination"])

    def test_selects_approval_router_for_approval_routing(self) -> None:
        result = plan(
            task="Run approval routing to see who must approve this artifact",
            changed_files=[],
            classification="internal",
        )
        self.assertEqual(result["agents"]["primary"], ['approval-router'])

    def test_selects_doctrine_conformance_for_doctrine_check(self) -> None:
        result = plan(
            task="Run a doctrine conformance check on this narrative before release",
            changed_files=[],
            classification="internal",
        )
        self.assertEqual(result["agents"]["primary"], ['doctrine-conformance'])

    def test_selects_architecture_authority_for_abstraction_layer_review(self) -> None:
        result = plan(
            task="Verify this change passes through the approved abstraction layer boundary",
            changed_files=[],
            classification="internal",
        )
        self.assertEqual(result["agents"]["primary"], ['architecture-authority'])
        self.assertEqual(result["agents"]["reviewers"], ["infrastructure-reviewer"])
        self.assertEqual([g["id"] for g in result["human_gates"]], ["architecture-boundary-violation"])

    def test_selects_scope_boundary_for_backlog_check(self) -> None:
        result = plan(
            task="Run a scope boundary check on this new backlog item",
            changed_files=[],
            classification="internal",
        )
        self.assertEqual(result["agents"]["primary"], ['scope-boundary'])

    def test_selects_phase_gate_for_phase_transition(self) -> None:
        result = plan(
            task="Run the phase gate check before the next phase transition",
            changed_files=[],
            classification="internal",
        )
        self.assertEqual(result["agents"]["primary"], ['phase-gate'])
        self.assertEqual(result["agents"]["reviewers"], ["compliance-reviewer"])

    def test_selects_assumption_register_for_recorded_premise(self) -> None:
        result = plan(
            task="Add an entry to the assumption register for this premise",
            changed_files=[],
            classification="internal",
        )
        self.assertEqual(result["agents"]["primary"], ['assumption-register'])

    def test_selects_decision_record_for_captured_decision(self) -> None:
        result = plan(
            task="Record who decided this and why in a decision record",
            changed_files=[],
            classification="internal",
        )
        self.assertEqual(result["agents"]["primary"], ['decision-record'])

    def test_selects_red_team_for_adversarial_assessment(self) -> None:
        result = plan(
            task="Run a red team assessment of the deployed system",
            changed_files=[],
            classification="internal",
        )
        self.assertEqual(result["agents"]["primary"], ['red-team'])
        self.assertEqual(result["agents"]["reviewers"], ['security-reviewer'])

    def test_selects_premortem_for_planned_commitment(self) -> None:
        result = plan(
            task="Run a premortem on this planned commitment",
            changed_files=[],
            classification="internal",
        )
        self.assertEqual(result["agents"]["primary"], ['premortem'])

    def test_selects_first_principles_challenger_for_constraint_challenge(self) -> None:
        result = plan(
            task="Run a first principles challenge on this design constraint",
            changed_files=[],
            classification="internal",
        )
        self.assertEqual(result["agents"]["primary"], ['first-principles-challenger'])

    def test_selects_subtraction_agent_for_scope_increase(self) -> None:
        result = plan(
            task="Run a subtraction review of this scope increase",
            changed_files=[],
            classification="internal",
        )
        self.assertEqual(result["agents"]["primary"], ['subtraction-agent'])

    def test_selects_falsification_agent_for_correctness_claim(self) -> None:
        result = plan(
            task="Run a falsification test on this correctness claim",
            changed_files=[],
            classification="internal",
        )
        self.assertEqual(result["agents"]["primary"], ['falsification-agent'])

    def test_selects_deployment_realist_for_pilot_review(self) -> None:
        result = plan(
            task="Run a deployment realism review of this pilot",
            changed_files=[],
            classification="internal",
        )
        self.assertEqual(result["agents"]["primary"], ['deployment-realist'])

    def test_selects_classification_and_marking_gate_for_boundary_crossing(self) -> None:
        result = plan(
            task="Run the classification and marking gate before this artifact leaves the environment",
            changed_files=[],
            classification="internal",
        )
        self.assertEqual(result["agents"]["primary"], ['classification-and-marking-gate'])
        self.assertEqual(result["agents"]["reviewers"], ["compliance-reviewer"])
        self.assertEqual([g["id"] for g in result["human_gates"]], ["classification-and-marking"])

    def test_selects_claim_conformance_for_external_artifact(self) -> None:
        result = plan(
            task="Run a claim conformance check on this floor script review",
            changed_files=[],
            classification="internal",
        )
        self.assertEqual(result["agents"]["primary"], ['claim-conformance'])
        self.assertEqual(result["agents"]["reviewers"], ["compliance-reviewer"])

    def test_selects_vendor_register_steward_for_tooling_drift(self) -> None:
        result = plan(
            task="Run a vendor register review for drift in tooling assessments",
            changed_files=[],
            classification="internal",
        )
        self.assertEqual(result["agents"]["primary"], ['vendor-register-steward'])

    def test_selects_knowledge_store_steward_for_recorded_findings_and_learnings(self) -> None:
        result = plan(
            task=(
                "Record findings, learnings, decisions, lessons learned, and "
                "operational knowledge from the latest review into the agent knowledge base"
            ),
            changed_files=[],
            classification="internal",
        )
        self.assertEqual(result["agents"]["primary"], ["knowledge-store-steward"])
        self.assertIn("security-reviewer", result["agents"]["reviewers"])
        self.assertIn("compliance-reviewer", result["agents"]["reviewers"])
        reasons = {match["id"]: match["reasons"] for match in result["matched_routes"]}
        self.assertEqual(
            reasons["knowledge-store"]["keyword_groups"],
            [
                ["record"],
                [
                    "lessons learned",
                    "operational knowledge",
                    "agent knowledge",
                    "knowledge base",
                ],
            ],
        )

    def test_selects_knowledge_store_steward_for_curated_lessons_learned(self) -> None:
        result = plan(
            task=(
                "Curate the lessons learned from this quarter's incidents into "
                "the agent knowledge base for reuse by future agents"
            ),
            changed_files=[],
            classification="internal",
        )
        self.assertEqual(result["agents"]["primary"], ["knowledge-store-steward"])
        self.assertEqual([match["id"] for match in result["matched_routes"]], ["knowledge-store"])

    def test_record_this_decision_stays_on_decision_record_route(self) -> None:
        result = plan(
            task="Record this decision",
            changed_files=[],
            classification="internal",
        )
        self.assertEqual(result["agents"]["primary"], ["decision-record"])
        self.assertEqual([match["id"] for match in result["matched_routes"]], ["decision-record-capture"])

    def test_adding_findings_to_a_report_does_not_select_knowledge_store(self) -> None:
        result = plan(
            task="Add findings from the incident review to the report",
            changed_files=[],
            classification="internal",
        )
        route_ids = [match["id"] for match in result["matched_routes"]]
        self.assertNotIn("knowledge-store", route_ids)
        self.assertNotIn("knowledge-store-steward", result["agents"]["primary"])

    def test_archiving_a_learnings_folder_does_not_select_knowledge_store(self) -> None:
        result = plan(
            task="Archive the old learnings folder",
            changed_files=[],
            classification="internal",
        )
        self.assertEqual(result["matched_routes"], [])
        self.assertEqual(result["agents"]["primary"], [])

    def test_preserving_an_approved_patterns_document_does_not_select_knowledge_store(self) -> None:
        result = plan(
            task="Preserve the approved patterns document for the release",
            changed_files=[],
            classification="internal",
        )
        self.assertEqual(result["matched_routes"], [])
        self.assertEqual(result["agents"]["primary"], [])

    def test_selects_retention_and_deletion_executor_for_obligation(self) -> None:
        result = plan(
            task="Execute the retention and deletion obligation for this data",
            changed_files=[],
            classification="internal",
        )
        self.assertEqual(result["agents"]["primary"], ['retention-and-deletion-executor'])
        self.assertEqual(result["agents"]["reviewers"], ["security-reviewer", "compliance-reviewer"])
        self.assertEqual([g["id"] for g in result["human_gates"]], ["retention-deletion-execution"])

    def test_selects_agent_performance_evaluator_for_output_review(self) -> None:
        result = plan(
            task="Run an agent performance evaluation on recent outputs",
            changed_files=[],
            classification="internal",
        )
        self.assertEqual(result["agents"]["primary"], ['agent-performance-evaluator'])

    def test_selects_agent_version_control_for_provenance_check(self) -> None:
        result = plan(
            task="Check agent version control for which version produced this artifact",
            changed_files=[],
            classification="internal",
        )
        self.assertEqual(result["agents"]["primary"], ['agent-version-control'])

    def test_selects_ip_provenance_agent_for_ip_determination(self) -> None:
        result = plan(
            task="Produce an ip determination for this artifact",
            changed_files=[],
            classification="internal",
        )
        self.assertEqual(result["agents"]["primary"], ['ip-provenance-agent'])

    def test_two_route_task_forms_cross_stack_build_team_only(self) -> None:
        result = plan(
            task="Add a React upload form backed by a PostgreSQL API",
            changed_files=["frontend/src/Upload.tsx", "services/upload/main.go"],
            classification="internal",
            task_id="APP-42",
        )
        team_ids = [team["id"] for team in result["teams"]]
        self.assertEqual(team_ids, ["cross-stack-build"])
        team = result["teams"][0]
        self.assertEqual(team["type"], "fixed")
        self.assertEqual(set(team["members"]), {"frontend-engineer", "backend-engineer"})
        self.assertEqual(team["communication_mode"], "peer")
        self.assertEqual(team["fallback"], "orchestrator-relayed")

    def test_three_stack_task_also_forms_parallel_review_team(self) -> None:
        result = plan(
            task="Add a React upload form backed by a PostgreSQL API with Terraform infra",
            changed_files=["frontend/src/Upload.tsx", "services/upload/main.go", "terraform/main.tf"],
            classification="internal",
            task_id="APP-43",
        )
        team_ids = {team["id"] for team in result["teams"]}
        self.assertEqual(team_ids, {"cross-stack-build", "parallel-review"})
        review_team = next(team for team in result["teams"] if team["id"] == "parallel-review")
        self.assertEqual(set(review_team["members"]), {"code-reviewer", "infrastructure-reviewer"})

    def test_intermittent_debugging_task_forms_dynamic_team(self) -> None:
        result = plan(
            task="Debug an intermittent panic that has not converged after several fixes",
            changed_files=["services/internal/repository/regression/panic_test.go"],
            classification="internal",
            task_id="DBG-TEAM-1",
        )
        team = next(team for team in result["teams"] if team["id"] == "competing-hypotheses-debugging")
        self.assertEqual(team["type"], "dynamic")
        self.assertEqual(team["role"], "debugging-engineer")
        self.assertEqual(team["instances"], {"min": 2, "max": 4})
        self.assertIn("intermittent", team["trigger_reason"]["keywords"])

    def test_ordinary_debugging_task_does_not_form_dynamic_team(self) -> None:
        result = plan(
            task="Debug a panic and identify the root cause from the stack trace",
            changed_files=["services/internal/repository/regression/panic_test.go"],
            classification="internal",
            task_id="DBG-1",
        )
        self.assertNotIn("competing-hypotheses-debugging", [team["id"] for team in result["teams"]])

    def test_single_route_task_has_no_teams(self) -> None:
        result = plan(task="Update Terraform", changed_files=["main.tf"])
        self.assertEqual(result["teams"], [])

    def test_team_members_are_always_a_subset_of_selected_agents(self) -> None:
        cases = [
            (
                "Add a React upload form backed by a PostgreSQL API with Terraform infra",
                ["frontend/src/Upload.tsx", "services/upload/main.go", "terraform/main.tf"],
            ),
            (
                "Review dependency SBOM and container image provenance for the pipeline and infra change",
                ["services/go.mod", ".gitlab-ci.yml", "terraform/main.tf"],
            ),
        ]
        for task, changed_files in cases:
            with self.subTest(task=task):
                result = plan(task=task, changed_files=changed_files)
                selected = {*result["agents"]["primary"], *result["agents"]["reviewers"], *result["agents"]["support"]}
                for team in result["teams"]:
                    if team["type"] == "fixed":
                        self.assertTrue(set(team["members"]).issubset(selected))
                    else:
                        self.assertIn(team["role"], selected)

    def test_knowledge_invocation_uses_resolved_repository_source(self) -> None:
        from build_dispatch_plan import KNOWLEDGE_STORE_ROOT

        result = plan(
            task="Add a React upload form backed by a PostgreSQL API",
            changed_files=["frontend/src/Upload.tsx"],
            classification="internal",
            task_id="NO-SOURCE-1",
        )
        requests = result["knowledge_context"]["requests"]
        self.assertTrue(requests)
        for request in requests:
            args = request["invocation"]["args"]
            self.assertIn("--source", args)
            self.assertEqual("example/repository", args[args.index("--source") + 1])
            self.assertEqual(str(KNOWLEDGE_STORE_ROOT / "src" / "cli.py"), args[0])
            self.assertNotIn("--config", args)
            self.assertNotIn("cwd", request["invocation"])

    @unittest.skipUnless(AGENTIC_SDLC_AVAILABLE, "Agentic SDLC executable is required")
    def test_emits_schema_v3_quality_gates_separately_from_human_gates(self) -> None:
        result = plan(
            task="Deploy to production with Terraform",
            changed_files=["terraform/service/main.tf"],
        )
        self.assertEqual(result["schema_version"], 4)
        self.assertEqual(result["workflow"], "production-release")
        self.assertEqual(self.quality_gate_ids(result), ["G1", "G2", "G3", "G4", "G5", "G6", "G7", "G8", "G9"])
        production_gate = next(
            gate for gate in result["required_quality_gates"] if gate["id"] == "G9"
        )
        self.assertEqual(production_gate["contributing_routes"], ["production"])
        self.assertEqual([gate["id"] for gate in result["human_gates"]], ["production-change"])
        # kernel_mutation_gate_id cross-references the Agentic SDLC kernel's
        # own contracts/mutation-gates.json id -- cadre's "production-change"
        # maps to the kernel's "production-deployment", not a duplicate
        # definition (build_dispatch_plan.py's KERNEL_MUTATION_GATE_IDS).
        self.assertEqual(result["human_gates"][0]["kernel_mutation_gate_id"], "production-deployment")

    @unittest.skipUnless(AGENTIC_SDLC_AVAILABLE, "Agentic SDLC executable is required")
    def test_kernel_mutation_gate_ids_reconcile_against_live_kernel_contract(self) -> None:
        # KERNEL_MUTATION_GATE_IDS is a static, hand-authored cross-reference
        # to the Agentic SDLC kernel's own contracts/mutation-gates.json ids
        # -- accurate at the time it was written, but nothing previously
        # caught the kernel silently renaming/removing an id out from under
        # it. This pulls the real contract from whatever kernel is on PATH
        # (the same resolution AGENTIC_SDLC_AVAILABLE/try_lifecycle_contract
        # already use) and asserts every non-None mapped value still exists.
        from build_dispatch_plan import KERNEL_MUTATION_GATE_IDS

        executable = os.environ.get("AGENTIC_SDLC_BIN") or shutil.which("agentic-sdlc")
        result = subprocess.run(
            [executable, "show-contract", "mutation-gates"],
            check=True,
            capture_output=True,
            text=True,
            encoding="utf-8",
        )
        contract = json.loads(result.stdout)
        kernel_ids = {entry["id"] for entry in contract["human_only"]}
        for cadre_id, kernel_id in KERNEL_MUTATION_GATE_IDS.items():
            if kernel_id is None:
                continue
            with self.subTest(cadre_id=cadre_id, kernel_id=kernel_id):
                self.assertIn(kernel_id, kernel_ids)

    def test_dispatch_disposition_is_staffed_when_a_primary_or_reviewer_is_selected(self) -> None:
        result = plan(
            task="Deploy to production with Terraform",
            changed_files=["terraform/service/main.tf"],
        )
        self.assertEqual(result["dispatch_disposition"]["status"], "staffed")

    def test_dispatch_disposition_is_no_agents_selected_when_nothing_matches(self) -> None:
        result = plan(task="", changed_files=[])
        self.assertEqual(result["status"], "needs-triage")
        self.assertEqual(result["dispatch_disposition"], {
            "status": "no-agents-selected",
            "reason": "No route or risk rule matched this task; there is nothing to dispatch.",
        })

    def test_dispatch_disposition_flags_advisory_only_destructive_but_reviewable_workflow(self) -> None:
        # Regression for issue #45: exporting a local backlog artifact and then
        # deleting the source GitLab issues only ever matched change_intake's
        # generic "delete" keyword, which lands product-intent-agent,
        # requirements-agent, and code-reviewer in `support` with no primary
        # or reviewer role selected. Without an explicit disposition field,
        # that support-only selection was indistinguishable in the plan from a
        # fully-staffed one, so an orchestrator could silently perform the
        # destructive step itself with no structured reason surfaced.
        result = plan(
            task="Export the GitLab issues to a local backlog artifact, then delete the GitLab issues",
            changed_files=[],
        )
        self.assertEqual(result["agents"]["primary"], [])
        self.assertEqual(result["agents"]["reviewers"], [])
        self.assertTrue(result["agents"]["support"])
        self.assertEqual(result["dispatch_disposition"]["status"], "advisory-only")
        for agent in result["agents"]["support"]:
            self.assertIn(agent, result["dispatch_disposition"]["reason"])

    def test_dispatch_disposition_is_staffed_via_reviewers_only_with_empty_primary(self) -> None:
        # The existing "staffed" test only reaches that status via a route
        # that also has a primary (infrastructure-provisioner). Pin the other
        # half of `if groups["primary"] or groups["reviewers"]` independently:
        # a risk rule with reviewers but no primary (authentication-authorization)
        # must also be staffed on its own.
        result = plan(
            task="Rotate the session token used for authorization",
            changed_files=[],
        )
        self.assertEqual(result["agents"]["primary"], [])
        self.assertIn("security-reviewer", result["agents"]["reviewers"])
        self.assertEqual(result["dispatch_disposition"]["status"], "staffed")

    def test_risk_only_match_with_no_build_shaped_route_is_unclassified(self) -> None:
        # A lone risk-rule match with no build-shaped route (frontend/backend/
        # infrastructure/pipeline) and no architecture-change risk must not be
        # silently mislabeled "new-service" -- it doesn't look like building
        # anything. Same task as the staffed-via-reviewers-only case above.
        result = plan(
            task="Rotate the session token used for authorization",
            changed_files=[],
        )
        self.assertEqual(result["matched_routes"], [])
        self.assertEqual(result["workflow"], "unclassified")

    @unittest.skipUnless(AGENTIC_SDLC_AVAILABLE, "Agentic SDLC executable is required")
    def test_dispatch_disposition_stays_advisory_only_when_support_is_gate_agents_only(self) -> None:
        # In lifecycle-integrated mode, `_gate_agents` can add a default
        # review agent (e.g. code-reviewer) to `support` for a route that has
        # no primary or reviewers of its own (architecture-change: support
        # only, quality_gates: [G3]). That must not get conflated with a real
        # `reviewers`-group role -- this is the same "support looks staffed
        # but isn't" risk issue #45 raised, via a second code path.
        result = plan(
            task="Discuss the new component's platform topology",
            changed_files=[],
        )
        self.assertEqual(result["agents"]["primary"], [])
        self.assertEqual(result["agents"]["reviewers"], [])
        self.assertIn("code-reviewer", result["agents"]["support"])
        self.assertEqual(result["dispatch_disposition"]["status"], "advisory-only")

    @unittest.skipUnless(AGENTIC_SDLC_AVAILABLE, "Agentic SDLC executable is required")
    def test_selects_product_intake_agents_and_gates_for_intent_only(self) -> None:
        result = plan(task="Capture product intent and requirements decomposition", changed_files=[])
        self.assertEqual(result["workflow"], "product-intake")
        self.assertIn("product-intent-agent", result["agents"]["primary"])
        self.assertIn("requirements-agent", result["agents"]["primary"])
        self.assertEqual(self.quality_gate_ids(result), ["G1", "G2"])

    @unittest.skipUnless(AGENTIC_SDLC_AVAILABLE, "Agentic SDLC executable is required")
    def test_change_work_always_adds_intent_and_requirements_gates(self) -> None:
        result = plan(task="Implement a GitHub approval integration", changed_files=[])
        self.assertEqual(self.quality_gate_ids(result), ["G1", "G2"])
        self.assertIn("product-intent-agent", result["agents"]["support"])
        self.assertIn("requirements-agent", result["agents"]["support"])

    @unittest.skipUnless(AGENTIC_SDLC_AVAILABLE, "Agentic SDLC executable is required")
    def test_combined_product_intent_and_architecture_uses_new_service(self) -> None:
        result = plan(task="Capture product intent and define the service architecture", changed_files=[])
        self.assertEqual(result["workflow"], "new-service")
        self.assertIn("product-intent-agent", result["agents"]["primary"])
        self.assertIn("cloud-architect", result["agents"]["support"])
        self.assertEqual(self.quality_gate_ids(result), ["G1", "G2", "G3"])

    def test_selects_governance_data_and_crypto_specialists_narrowly(self) -> None:
        governance = plan(task="Assess governance impact and prepare an accreditation plan", changed_files=[])
        data = plan(task="Define non-egress and data residency controls", changed_files=[])
        crypto = plan(task="Assess PQC crypto agility and downgrade risk", changed_files=[])

        self.assertIn("governance-planner", governance["agents"]["primary"])
        self.assertIn("compliance-reviewer", governance["agents"]["reviewers"])
        self.assertIn("data-governance-engineer", data["agents"]["primary"])
        self.assertIn("security-reviewer", data["agents"]["reviewers"])
        self.assertIn("compliance-reviewer", data["agents"]["reviewers"])
        self.assertIn("cryptographic-assurance-engineer", crypto["agents"]["primary"])
        self.assertIn("security-reviewer", crypto["agents"]["reviewers"])
        self.assertIn("threat-modeler", crypto["agents"]["support"])
        self.assertTrue(
            set(crypto["agents"]["primary"]).isdisjoint(crypto["agents"]["reviewers"])
        )

    def test_selects_quantum_timing_assurance_specialist_narrowly(self) -> None:
        result = plan(task="Assess entanglement fidelity and timing stratum thresholds for physical trust", changed_files=[])

        self.assertIn("quantum-timing-assurance-engineer", result["agents"]["primary"])
        self.assertIn("cryptographic-assurance-engineer", result["agents"]["reviewers"])
        self.assertIn("threat-modeler", result["agents"]["support"])
        self.assertTrue(
            set(result["agents"]["primary"]).isdisjoint(result["agents"]["reviewers"])
        )

    def test_selects_authority_aides_narrowly_per_role(self) -> None:
        # `routing.yaml`'s declared quality_gates per route (standalone mode:
        # Agentic SDLC unavailable, gates pass through as declared). When
        # AGENTIC_SDLC_BIN/agentic-sdlc *is* available (integrated mode, as in
        # CI's python-contracts job), the kernel enriches required_quality_gates
        # to the full cumulative G1..max(declared) sequence, since reaching a
        # later gate implies every earlier one was also required — see
        # test_emits_schema_v3_quality_gates_separately_from_human_gates above
        # for the same cumulative pattern on an unrelated route.
        cases = [
            ("product owner decision package", "product-owner-aide", ["G1", "G2", "G6"]),
            ("engineering lead decision package", "engineering-lead-aide", ["G2", "G6"]),
            ("system architect decision package", "system-architect-aide", ["G3"]),
            ("governance lead decision package", "governance-lead-aide", ["G4"]),
            ("security lead decision package", "security-lead-aide", ["G5"]),
            ("release owner decision package", "release-owner-aide", ["G7", "G8"]),
            ("release authority decision package", "release-authority-aide", ["G9"]),
            ("service owner decision package", "service-owner-aide", ["G10"]),
        ]
        for task, expected_agent, declared_gates in cases:
            with self.subTest(agent=expected_agent):
                result = plan(task=task, changed_files=[])
                self.assertEqual(result["agents"]["primary"], [expected_agent])
                if AGENTIC_SDLC_AVAILABLE:
                    max_gate = max(int(gate[1:]) for gate in declared_gates)
                    expected_gates = [f"G{n}" for n in range(1, max_gate + 1)]
                else:
                    expected_gates = declared_gates
                self.assertEqual(self.quality_gate_ids(result), expected_gates)
                # Aides are read-only preparers, never reviewers or approvers.
                self.assertNotIn(expected_agent, result["agents"]["reviewers"])

    @unittest.skipUnless(AGENTIC_SDLC_AVAILABLE, "Agentic SDLC executable is required")
    def test_selects_runtime_assurance_without_production_release(self) -> None:
        result = plan(task="Observe production runtime for deployed behavior conformance", changed_files=[])
        self.assertEqual(result["workflow"], "runtime-assurance")
        self.assertEqual(result["agents"]["primary"], ["observability-sre"])
        self.assertIn("security-reviewer", result["agents"]["reviewers"])
        self.assertIn("compliance-reviewer", result["agents"]["reviewers"])
        self.assertIn("support-triage-agent", result["agents"]["support"])
        self.assertEqual(self.quality_gate_ids(result), ["G1", "G2", "G3", "G4", "G5", "G6", "G7", "G8", "G9", "G10"])
        self.assertNotIn("production-change", [gate["id"] for gate in result["human_gates"]])

    def test_workflow_precedence_keeps_support_ahead_of_runtime_assurance(self) -> None:
        result = plan(
            task="Triage a customer incident during runtime assurance",
            changed_files=["incidents/INC-9.md"],
        )
        self.assertEqual(result["workflow"], "support-escalation")

    def test_runtime_failure_still_uses_debugging_workflow(self) -> None:
        result = plan(task="Debug a production runtime failure", changed_files=["diagnostics/error.log"])
        self.assertEqual(result["workflow"], "debugging")
        self.assertIn("debugging-engineer", result["agents"]["primary"])

    @unittest.skipUnless(AGENTIC_SDLC_AVAILABLE, "Agentic SDLC executable is required")
    def test_narrow_lifecycle_routes_avoid_generic_collisions(self) -> None:
        cases = [
            ("Update README requirements", ["README.md"]),
            ("Review package dependencies", ["services/go.mod"]),
            ("Configure TLS", ["services/api/config.go"]),
            ("Review database data retention", ["database/postgres/backup.md"]),
            ("Fix ordinary runtime behavior", ["services/api/main.go"]),
        ]
        specialist_agents = {
            "governance-planner",
            "data-governance-engineer",
            "cryptographic-assurance-engineer",
        }
        for task, changed_files in cases:
            with self.subTest(task=task):
                result = plan(task=task, changed_files=changed_files)
                selected = {
                    *result["agents"]["primary"],
                    *result["agents"]["reviewers"],
                    *result["agents"]["support"],
                }
                # _gate_agents always contributes "code-reviewer" to support
                # whenever any required_quality_gates entry applies (see
                # test_dispatch_disposition_stays_advisory_only_when_support_is_gate_agents_only) --
                # confirm that invariant holds without a stray unselected gap.
                if any(gate["required"] for gate in result["required_quality_gates"]):
                    self.assertIn("code-reviewer", selected)
                self.assertNotEqual(result["workflow"], "runtime-assurance")

    def test_knowledge_invocation_preserves_argv_and_output_contract(self) -> None:
        result = plan(
            task="Update the React navigation",
            changed_files=["frontend/src/Nav.tsx"],
            classification="confidential",
            source="approved-decisions",
            top=3,
            task_id="UI-8",
        )
        request = next(
            request
            for request in result["knowledge_context"]["requests"]
            if request["agent"] == "frontend-engineer"
        )
        self.assertEqual(
            request,
            {
                "agent": "frontend-engineer",
                "query": (
                    "Task: Update the React navigation. Retrieve frontend implementation "
                    "patterns, UX decisions, accessibility behavior, API contracts, "
                    "browser security, and approved React or TypeScript conventions."
                ),
                "invocation": {
                    "launcher": {
                        "runtime": "python",
                        "minimum_version": "3.10",
                        "resolution": "runner-probed",
                    },
                    "args": [
                        str(AGENTS_ROOT / "knowledge-store" / "src" / "cli.py"),
                        "context",
                        "--agent",
                        "frontend-engineer",
                        "--task-id",
                        "UI-8",
                        "--query",
                        (
                            "Task: Update the React navigation. Retrieve frontend implementation "
                            "patterns, UX decisions, accessibility behavior, API contracts, "
                            "browser security, and approved React or TypeScript conventions."
                        ),
                        "--classification",
                        "confidential",
                        "--top",
                        "3",
                        "--source",
                        "approved-decisions",
                    ],
                },
            },
        )

    def test_adds_security_roles_for_authentication_work(self) -> None:
        result = plan(
            task="Add OIDC authentication and session handling to the React frontend",
            changed_files=["frontend/src/auth/session.ts"],
        )
        self.assertIn("frontend-engineer", result["agents"]["primary"])
        self.assertIn("threat-modeler", result["agents"]["support"])
        self.assertIn("security-reviewer", result["agents"]["reviewers"])
        self.assertEqual(result["knowledge_context"]["status"], "authorization-required")

    def test_selects_infrastructure_workflow_and_independent_review(self) -> None:
        result = plan(
            task="Update Terraform for a Proxmox worker VM",
            changed_files=["terraform/modules/worker/main.tf"],
        )
        self.assertEqual(result["workflow"], "infrastructure-change")
        self.assertEqual(result["agents"]["primary"], ["infrastructure-provisioner"])
        self.assertEqual(result["agents"]["reviewers"], ["infrastructure-reviewer"])

    def test_routes_compose_runtime_changes_to_infrastructure_review(self) -> None:
        result = plan(
            task="Fix Podman Compose named volume behavior for PostgreSQL",
            changed_files=["deploy/compose/compose.yaml"],
        )
        self.assertEqual(result["workflow"], "new-service")
        self.assertIn("backend-engineer", result["agents"]["primary"])
        self.assertIn("infrastructure-provisioner", result["agents"]["primary"])
        self.assertIn("infrastructure-reviewer", result["agents"]["reviewers"])

    def test_selects_black_box_tester_for_external_behavior(self) -> None:
        result = plan(
            task="Create black-box end-to-end tests for public API upload behavior",
            changed_files=["tests/features/upload.feature"],
            classification="internal",
            task_id="QA-1",
        )
        self.assertIn("black-box-tester", result["agents"]["primary"])
        self.assertIn("test-engineer", result["agents"]["primary"])
        self.assertEqual(result["knowledge_context"]["status"], "planned")
        self.assertTrue(
            any(request["agent"] == "black-box-tester" for request in result["knowledge_context"]["requests"])
        )

    def test_selects_debugging_engineer_for_root_cause_work(self) -> None:
        result = plan(
            task="Debug a panic and identify the root cause from the stack trace",
            changed_files=["services/internal/repository/regression/panic_test.go"],
            classification="internal",
            task_id="DBG-1",
        )
        self.assertIn("debugging-engineer", result["agents"]["primary"])
        self.assertEqual(result["workflow"], "debugging")
        self.assertIn("test-engineer", result["agents"]["primary"])
        self.assertIn("code-reviewer", result["agents"]["reviewers"])
        self.assertEqual(result["knowledge_context"]["status"], "planned")
        self.assertTrue(
            any(request["agent"] == "debugging-engineer" for request in result["knowledge_context"]["requests"])
        )

    def test_selects_debugging_engineer_for_agent_tune_up(self) -> None:
        result = plan(
            task="Inspect agents, find routing issues, and tune agent definitions",
            changed_files=["roster/orchestration/routing.yaml", "roster/engineering/debugging-engineer/AGENT.md"],
            classification="internal",
            task_id="AGENT-DBG-1",
        )
        self.assertIn("debugging-engineer", result["agents"]["primary"])
        self.assertEqual(result["workflow"], "debugging")
        self.assertIn("application-engineer", result["agents"]["primary"])
        self.assertIn("code-reviewer", result["agents"]["reviewers"])

    def test_selects_debugging_engineer_for_agent_definition_path_only(self) -> None:
        result = plan(
            task="Update role guidance",
            changed_files=["roster/engineering/frontend-engineer/AGENT.md"],
            classification="internal",
            task_id="AGENT-PATH-1",
        )
        # Path-only match (no debugging keyword in the task text) on a
        # roster AGENT.md is routine roster maintenance, not a defect --
        # see _select_workflow()'s debugging_by_keyword check.
        self.assertEqual(result["workflow"], "agent-suite-maintenance")
        self.assertIn("debugging-engineer", result["agents"]["primary"])
        self.assertIn("technical-writer", result["agents"]["primary"])

    def test_selects_governance_roles_for_agent_suite_review(self) -> None:
        result = plan(
            task="Review project agents skills and structure",
            changed_files=["README.md", "AGENTS.md", ".agents/skills/agent-authoring/SKILL.md"],
            classification="internal",
            task_id="GOV-1",
        )
        self.assertEqual(result["workflow"], "agent-suite-maintenance")
        self.assertIn("application-engineer", result["agents"]["primary"])
        self.assertIn("debugging-engineer", result["agents"]["primary"])
        self.assertIn("test-engineer", result["agents"]["reviewers"])
        self.assertIn("code-reviewer", result["agents"]["reviewers"])
        self.assertTrue(
            "technical-writer" in result["agents"]["primary"]
            or "technical-writer" in result["agents"]["support"]
        )
        self.assertNotEqual(result["agents"]["primary"], ["technical-writer"])

    def test_selects_governance_roles_for_publishable_skill_audit(self) -> None:
        result = plan(
            task="Audit publishable skills for packaging and stale references",
            changed_files=[".agents/skills/run-agent-orchestration/SKILL.md"],
            classification="internal",
            task_id="GOV-2",
        )
        self.assertIn("application-engineer", result["agents"]["primary"])
        self.assertIn("debugging-engineer", result["agents"]["primary"])
        self.assertTrue(
            "technical-writer" in result["agents"]["primary"]
            or "technical-writer" in result["agents"]["support"]
        )

    def test_selects_end_user_and_support_for_uat(self) -> None:
        result = plan(
            task="Run UAT for end-user document upload journeys and supportability",
            changed_files=["docs/uat/document-upload.md"],
            classification="internal",
            task_id="UAT-1",
        )
        self.assertIn("end-user-tester", result["agents"]["primary"])
        self.assertIn("technical-writer", result["agents"]["primary"])
        self.assertIn("support-triage-agent", result["agents"]["support"])

    def test_selects_support_triage_and_escalation_manager_with_human_gate(self) -> None:
        result = plan(
            task="Triage a customer report and escalate to human support owner",
            changed_files=["support/tickets/TICKET-123.md"],
            classification="confidential",
            task_id="SUP-123",
        )
        self.assertEqual(result["workflow"], "support-escalation")
        self.assertIn("support-triage-agent", result["agents"]["primary"])
        self.assertIn("escalation-manager", result["agents"]["support"])
        self.assertEqual(
            [gate["id"] for gate in result["human_gates"]],
            ["accountable-human-escalation"],
        )

    def test_selects_observability_sre_for_alerting_and_slos(self) -> None:
        result = plan(
            task="Define SLO alerts and Grafana dashboards for document upload",
            changed_files=["observability/alerts/document-upload.yaml"],
            classification="internal",
            task_id="OBS-1",
        )
        self.assertIn("observability-sre", result["agents"]["primary"])
        self.assertIn("technical-writer", result["agents"]["reviewers"])
        self.assertEqual(result["knowledge_context"]["status"], "planned")

    def test_selects_secrets_identity_with_privileged_human_gate(self) -> None:
        result = plan(
            task="Rotate a production secret for a Kubernetes service account",
            changed_files=["identity/rbac/serviceaccount-api.yaml"],
            classification="restricted",
            task_id="ID-1",
        )
        self.assertIn("secrets-identity-engineer", result["agents"]["primary"])
        self.assertIn("security-reviewer", result["agents"]["reviewers"])
        self.assertIn("privileged-identity-change", [gate["id"] for gate in result["human_gates"]])

    def test_selects_database_reliability_for_postgres_recovery(self) -> None:
        result = plan(
            task="Review PostgreSQL PITR backup and restore readiness",
            changed_files=["database/postgres/backup.md"],
            classification="confidential",
            task_id="DBRE-1",
        )
        self.assertIn("database-reliability-engineer", result["agents"]["primary"])
        self.assertIn("infrastructure-reviewer", result["agents"]["reviewers"])

    def test_selects_policy_as_code_for_admission_controls(self) -> None:
        result = plan(
            task="Add Kyverno policy for restricted security contexts",
            changed_files=["policy/kyverno/restricted.yaml"],
            classification="internal",
            task_id="POL-1",
        )
        self.assertIn("policy-as-code-engineer", result["agents"]["primary"])
        self.assertIn("security-reviewer", result["agents"]["reviewers"])

    def test_selects_supply_chain_reviewer_for_dependency_evidence(self) -> None:
        result = plan(
            task="Review dependency SBOM and container image provenance",
            changed_files=["services/go.mod"],
            classification="internal",
            task_id="SC-1",
        )
        self.assertIn("supply-chain-security-reviewer", result["agents"]["primary"])
        self.assertIn("security-reviewer", result["agents"]["reviewers"])
        self.assertIn("release-engineer", result["agents"]["support"])

    def test_lockfile_guard_test_matches_supply_chain_by_path_alone(self) -> None:
        # #189 regression: plugin/tools/test_cline_git_plugin_packaging.py is
        # the sole check tying the root lockfile to cline-plugins/, so it
        # must route to supply-chain by path regardless of task wording --
        # before the fix this depended entirely on whether the task text
        # happened to contain a supply-chain keyword like "dependency".
        result = plan(
            task="Adjust the Cline git plugin packaging checker's tolerance",
            changed_files=["plugin/tools/test_cline_git_plugin_packaging.py"],
            classification="internal",
            task_id="PKG-189-1",
        )
        route_ids = {match["id"] for match in result["matched_routes"]}
        self.assertIn("supply-chain", route_ids)
        self.assertIn("packaging", route_ids)
        self.assertIn("supply-chain-security-reviewer", result["agents"]["primary"])
        self.assertIn("security-reviewer", result["agents"]["reviewers"])

    def test_other_plugin_tools_file_routes_to_packaging_not_security_reviewers(self) -> None:
        # An ordinary plugin/tools/ file (not the lockfile guard) gets
        # application-engineer/debugging-engineer, not a security reviewer
        # chain -- the single-file supply-chain addition must not leak.
        result = plan(
            task="Tighten the plugin manifest health guard's field checks",
            changed_files=["plugin/tools/test_manifest_health.py"],
            classification="internal",
            task_id="PKG-189-2",
        )
        route_ids = {match["id"] for match in result["matched_routes"]}
        self.assertEqual(route_ids, {"packaging"})
        self.assertEqual(
            result["agents"]["primary"], ["application-engineer", "debugging-engineer"]
        )
        self.assertNotIn("supply-chain-security-reviewer", result["agents"]["primary"])
        self.assertNotIn("security-reviewer", result["agents"]["reviewers"])

    def test_root_pyproject_toml_alone_does_not_route_to_packaging(self) -> None:
        # #189 review finding 2 (code-reviewer, MEDIUM): routing.yaml ships as
        # the BASE ruleset to every consuming project (routing_overlay.py only
        # lets a consumer widen a base route, never narrow it), and root
        # pyproject.toml is a generic file present in arbitrary downstream
        # Python projects. Claiming it under a route named "packaging" whose
        # keywords are about this repo's own Cline ports and plugin manifests
        # is wrong for every consumer, so pyproject.toml was removed from the
        # route's paths. A pyproject.toml-only change with no packaging
        # keyword in the task text now falls through to needs-triage.
        result = plan(
            task="Look at pyproject.toml",
            changed_files=["pyproject.toml"],
            classification="internal",
            task_id="PKG-189-3",
        )
        route_ids = {match["id"] for match in result["matched_routes"]}
        self.assertNotIn("packaging", route_ids)
        self.assertEqual(result["status"], "needs-triage")

    def test_plugin_tools_file_never_matches_supply_chain_glob_wide(self) -> None:
        # Negative: proves decision 2 added exactly one file to supply-chain's
        # paths, not the whole plugin/tools/** glob.
        result = plan(
            task="Harden the workspace mutation guard's detection logic",
            changed_files=["plugin/tools/test_guard_workspace_mutation.py"],
            classification="internal",
            task_id="PKG-189-4",
        )
        route_ids = {match["id"] for match in result["matched_routes"]}
        self.assertNotIn("supply-chain", route_ids)

    def test_no_plugin_tools_file_matches_agent_suite_governance_by_path(self) -> None:
        for relative_path in (
            "plugin/tools/test_cline_git_plugin_packaging.py",
            "plugin/tools/test_manifest_health.py",
            "plugin/tools/port_cline_agents.py",
            "plugin/tools/test_guard_workspace_mutation.py",
        ):
            with self.subTest(path=relative_path):
                result = plan(
                    task="Adjust a plugin/tools/ script",
                    changed_files=[relative_path],
                    classification="internal",
                    task_id="PKG-189-5",
                )
                route_ids = {match["id"] for match in result["matched_routes"]}
                self.assertNotIn("agent-suite-governance", route_ids)

    def test_packaging_route_readme_routes_only_through_agent_suite_governance(self) -> None:
        # #189 re-review finding 1 (code-reviewer, MEDIUM; revision 2): the new
        # packaging route no longer carries packaging/** at all -- a copy of
        # agent-suite-governance's pre-existing imprecise glob was not a
        # license to duplicate that imprecision onto a second route and
        # double its blast radius, and routing.yaml is a base ruleset a
        # consumer can only widen, never narrow. packaging/plugin-README.md
        # therefore keeps exactly the routing it had before this change --
        # agent-suite-governance only -- and the packaging route (now scoped
        # to plugin/tools/** only) must not also match it.
        result = plan(
            task="Reword the plugin README's install instructions",
            changed_files=["packaging/plugin-README.md"],
            classification="internal",
            task_id="PKG-189-6",
        )
        route_ids = {match["id"] for match in result["matched_routes"]}
        self.assertIn("agent-suite-governance", route_ids)
        self.assertNotIn("packaging", route_ids)
        # agent-suite-governance's own primary/reviewers are byte-identical
        # to the packaging route's, so the removal is a routing-visibility
        # change only -- staffing for this file is unaffected.
        self.assertEqual(
            result["agents"]["primary"],
            ["application-engineer", "debugging-engineer", "technical-writer"],
        )
        self.assertEqual(
            result["agents"]["reviewers"], ["test-engineer", "code-reviewer"]
        )

    def test_packaging_keywords_do_not_leak_onto_adjacent_tasks(self) -> None:
        # Negative: realistic adjacent tasks (docs change, dependency bump,
        # roster/catalog change) must not pick up the new packaging route
        # via its keywords.
        adjacent = [
            ("Update the operator guide for the new runbook section", ["docs/runbook.md"]),
            ("Bump the pinned linter dependency version", ["go.mod"]),
            ("Add a new specialist role to the roster", ["roster/catalog.yaml"]),
        ]
        for task_text, files in adjacent:
            with self.subTest(task=task_text):
                result = plan(
                    task=task_text,
                    changed_files=files,
                    classification="internal",
                    task_id="PKG-189-7",
                )
                route_ids = {match["id"] for match in result["matched_routes"]}
                self.assertNotIn("packaging", route_ids)

    def test_packaging_keywords_do_not_leak_reported_high_finding_repros(self) -> None:
        # #189 review finding 1 (test-engineer, HIGH, request-changes):
        # reproduces all four reported leaks verbatim. Before the fix, the
        # route's own generic keyword strings ("version bump", "changelog
        # entry", "install script", "bootstrap sdlc") fired live through
        # build_dispatch_plan() on tasks with no plugin/tools or packaging/
        # file involved, dragging application-engineer/debugging-engineer
        # (plus test-engineer/code-reviewer) onto unrelated infra/docs work.
        # The narrowed keywords ("plugin version bump", "plugin changelog
        # entry", "plugin install script", "bootstrap_sdlc.py") must not
        # match any of these task strings.
        leak_repros = [
            (
                "Do a version bump of the terraform provider pin",
                ["infra/main.tf"],
            ),
            (
                "Add a changelog entry for the new API endpoint",
                ["docs/CHANGELOG.md"],
            ),
            (
                "Write an install script for the onboarding docs",
                ["docs/onboarding.md"],
            ),
            (
                "Bootstrap SDLC gates for the new project",
                ["docs/onboarding.md"],
            ),
        ]
        for task_text, files in leak_repros:
            with self.subTest(task=task_text):
                result = plan(
                    task=task_text,
                    changed_files=files,
                    classification="internal",
                    task_id="PKG-189-8",
                )
                route_ids = {match["id"] for match in result["matched_routes"]}
                self.assertNotIn("packaging", route_ids)
                self.assertNotIn("application-engineer", result["agents"]["primary"])
                self.assertNotIn("debugging-engineer", result["agents"]["primary"])

    def test_every_packaging_keyword_is_audited_against_generic_phrasing(self) -> None:
        # #189 review finding 1 (test-engineer, HIGH): "re-audit ALL eleven
        # keywords the same way, not just the four named. Any keyword that is
        # a generic English phrase is suspect." For each of the packaging
        # route's own keyword strings, this pairs it with a realistic
        # adjacent-domain task built from the SAME underlying words/theme but
        # NOT the exact narrowed phrase (mirroring the four originally
        # reported leaks: e.g. "version bump" leaked on a Terraform pin task,
        # so "plugin version bump" is checked against an unrelated
        # application-release "version bump" sentence that lacks the word
        # "plugin"). Each sentence is asserted to NOT contain the keyword's
        # exact substring (otherwise the assertion below would be
        # tautological) and to NOT trigger the packaging route.
        #
        # "cline port" was restored in the #189 re-review (finding 2, found
        # independently by both test-engineer and code-reviewer): it is not
        # redundant with "port cline agents" because _keyword_matches does
        # literal, ordered, whole-word substring matching with no
        # synonym/word-order handling -- "cline port" is not a substring of
        # "port cline agents", and dropping it left a real task wording
        # ("Do the cline port for the security agents") unstaffed.
        packaging_route = next(
            route for route in CONFIG["routes"] if route["id"] == "packaging"
        )
        adjacent_sentences = {
            "plugin packaging": "The Chrome plugin has a packaging step before publishing to the store",
            "plugin manifest health": "Update the plugin manifest for the browser extension",
            "plugin packaging tool": "We need a new packaging tool for our npm library",
            "packaging guard": "Add a guard rail around packaging the release",
            "plugin changelog entry": "Add a changelog entry for the new API endpoint",
            "plugin install script": "Write an install script for the onboarding docs",
            "plugin version bump": "Announce the next application version bump in the release notes",
            "port cline agents": "Configure the port for cline agents in dev",
            "cline port": "Reconfigure the cline extension's local network port for development",
            "bootstrap_sdlc.py": "Bootstrap SDLC gates for the new project",
            "plugin distribution packaging": "The npm distribution packaging pipeline needs a fix",
        }
        self.assertEqual(
            set(adjacent_sentences), set(packaging_route["keywords"]),
            "every packaging keyword must have an audited adjacent-domain sentence "
            "(and vice versa) so a keyword addition/removal cannot silently skip this audit",
        )
        for keyword, task_text in adjacent_sentences.items():
            with self.subTest(keyword=keyword):
                normalized = re.sub(r"\s+", " ", task_text.lower())
                self.assertNotIn(
                    re.sub(r"\s+", " ", keyword.lower()),
                    normalized,
                    f"audit sentence for {keyword!r} accidentally contains the exact "
                    "keyword phrase, making the negative assertion below tautological",
                )
                result = plan(
                    task=task_text,
                    changed_files=["unrelated/file.xyz"],
                    classification="internal",
                    task_id="PKG-189-9",
                )
                route_ids = {match["id"] for match in result["matched_routes"]}
                self.assertNotIn(
                    "packaging",
                    route_ids,
                    f"keyword {keyword!r} is too generic: it fired (or a same-theme "
                    f"variant fires) on an out-of-route sentence: {task_text!r}",
                )

    def test_cline_port_keyword_covers_wording_that_port_cline_agents_misses(self) -> None:
        # #189 re-review finding 2 (MEDIUM, found independently by both
        # test-engineer and code-reviewer): "cline port" was dropped in
        # revision 2 as "redundant with 'port cline agents'". It is not --
        # _keyword_matches does literal, ordered, whole-word substring
        # matching with no synonym/word-order handling, so "cline port" is
        # not a substring of "port cline agents". Restored; this pins the
        # exact live repro from the review (a task on a frontend-shaped file
        # with no plugin/tools or packaging content) so a future edit cannot
        # silently drop the keyword again without a test failing.
        result = plan(
            task="Do the cline port for the security agents",
            changed_files=["cline-plugins/cline-agents/index.ts"],
            classification="internal",
            task_id="PKG-189-10",
        )
        route_ids = {match["id"] for match in result["matched_routes"]}
        self.assertIn("packaging", route_ids)
        self.assertIn("application-engineer", result["agents"]["primary"])
        self.assertIn("debugging-engineer", result["agents"]["primary"])
        self.assertIn("test-engineer", result["agents"]["reviewers"])
        self.assertIn("code-reviewer", result["agents"]["reviewers"])

    def test_bootstrap_sdlc_keyword_matches_embedded_in_a_longer_token(self) -> None:
        # #189 re-review finding 3 (LOW, code-reviewer by reading, reproduced
        # live by test-engineer): bootstrap_sdlc.py is the only
        # underscore-containing keyword in routing.yaml, and
        # _keyword_matches's boundary class ([a-z0-9-]) excludes hyphens but
        # not underscores or dots -- so it fires embedded in a longer token.
        # This is a documented, accepted quirk of this one keyword (see the
        # _keyword_matches docstring), pinned here rather than left as an
        # undocumented accident. _keyword_matches itself is deliberately
        # unchanged: its boundary class is global matcher semantics shared by
        # ~90 keyword arrays, far outside this change's blast radius.
        leak_repros = [
            (
                "Review the archived legacy_bootstrap_sdlc.py_old script for removal",
                "docs/legacy_bootstrap_sdlc.py_old",
            ),
            (
                "Rename my_bootstrap_sdlc.py_v2 helper during cleanup",
                "scripts/my_bootstrap_sdlc.py_v2",
            ),
        ]
        for task_text, changed_file in leak_repros:
            with self.subTest(task=task_text):
                result = plan(
                    task=task_text,
                    changed_files=[changed_file],
                    classification="internal",
                    task_id="PKG-189-11",
                )
                route_ids = {match["id"] for match in result["matched_routes"]}
                self.assertIn(
                    "packaging",
                    route_ids,
                    "bootstrap_sdlc.py is expected to match embedded in a longer "
                    "token per _keyword_matches's documented underscore/dot gap; "
                    "if this now fails, either the matcher's boundary class "
                    "changed (out of scope for #189) or the keyword's shape "
                    "changed and this test's premise is stale",
                )

    def test_selects_incident_commander_for_major_incident(self) -> None:
        result = plan(
            task="Coordinate a SEV1 major incident and postmortem",
            changed_files=["incidents/SEV1-document-upload.md"],
            classification="confidential",
            task_id="INC-1",
        )
        self.assertEqual(result["workflow"], "support-escalation")
        self.assertIn("incident-commander", result["agents"]["primary"])
        self.assertIn("observability-sre", result["agents"]["support"])

    def test_selects_cost_capacity_planner_for_sizing(self) -> None:
        result = plan(
            task="Estimate Kubernetes resource limits and storage growth headroom",
            changed_files=["capacity/document-upload-sizing.md"],
            classification="internal",
            task_id="CAP-1",
        )
        self.assertIn("cost-capacity-planner", result["agents"]["primary"])
        self.assertIn("observability-sre", result["agents"]["support"])

    def test_selects_finops_engineer_for_cost_drift(self) -> None:
        result = plan(
            task="Investigate a spend anomaly and quota exhaustion drift observed in production",
            changed_files=["reports/anomaly-2026-07.txt"],
        )
        self.assertIn("finops-engineer", result["agents"]["primary"])
        self.assertIn("observability-sre", result["agents"]["support"])
        # The cost-capacity route's bare "quota" keyword also matches this task
        # text, so cost-capacity-planner co-selects as primary alongside
        # finops-engineer. This is deliberate (the two roles hand off to each
        # other per their AGENT.md), so assert it explicitly rather than
        # leaving the overlap unverified.
        self.assertIn("cost-capacity-planner", result["agents"]["primary"])

    def test_selects_api_contract_engineer_for_openapi_changes(self) -> None:
        result = plan(
            task="Add a versioned breaking change to the checkout API contract",
            changed_files=["contracts/checkout/openapi.yaml"],
        )
        self.assertIn("api-contract-engineer", result["agents"]["primary"])
        self.assertIn("cloud-architect", result["agents"]["support"])
        self.assertIn("frontend-engineer", result["agents"]["support"])
        self.assertIn("code-reviewer", result["agents"]["reviewers"])
        # The pre-existing backend route's bare "api" keyword also matches any
        # task text mentioning "API", so backend-engineer co-selects as
        # primary here too. Assert it explicitly (rather than leaving it
        # unverified) since a contract change realistically does need backend
        # implementation awareness; a contracts/** path with no "api" wording
        # in the task text would not pull backend-engineer in.
        self.assertIn("backend-engineer", result["agents"]["primary"])

    def test_frontend_route_includes_accessibility_reviewer(self) -> None:
        result = plan(
            task="Update the React navigation for keyboard accessibility",
            changed_files=["frontend/src/Nav.tsx"],
        )
        self.assertIn("frontend-engineer", result["agents"]["primary"])
        self.assertIn("accessibility-reviewer", result["agents"]["reviewers"])

    def test_github_actions_workflow_selects_the_same_agents_as_gitlab_ci(self) -> None:
        # Before the forge-neutrality repair the pipeline route carried only
        # GitLab paths, so a GitHub Actions change matched no build-shaped
        # route at all and was dispatched with no primary agent -- while the
        # identical task on .gitlab-ci.yml selected cicd-engineer. The two
        # forges are both supported, so they must staff the same way.
        github = plan(
            task="Update the release workflow to sign tags",
            changed_files=[".github/workflows/release.yml"],
        )
        gitlab = plan(
            task="Update the release workflow to sign tags",
            changed_files=[".gitlab-ci.yml"],
        )
        self.assertIn("cicd-engineer", github["agents"]["primary"])
        self.assertIn("pipeline-security-reviewer", github["agents"]["reviewers"])
        self.assertEqual(github["agents"]["primary"], gitlab["agents"]["primary"])
        self.assertEqual(github["agents"]["reviewers"], gitlab["agents"]["reviewers"])

    def test_python_service_work_selects_the_backend_engineer(self) -> None:
        # `**/*.py` is deliberately NOT a path glob on the backend route: this
        # repository is itself Python, and a bare glob cross-matched its own
        # orchestration source (already routed to application-engineer),
        # adding backend-engineer as a spurious second primary. The `python`
        # keyword covers a consuming project's Python service without that.
        result = plan(
            task="Implement a Python data transformation service",
            changed_files=["src/etl/transform.py"],
        )
        self.assertIn("backend-engineer", result["agents"]["primary"])

        own_source = plan(
            task="Fix the selector ordering",
            changed_files=["roster/orchestration/src/select_agents.py"],
        )
        self.assertNotIn("backend-engineer", own_source["agents"]["primary"])

    def test_ai_feature_route_selects_the_ai_engineer_not_the_cicd_engineer(self) -> None:
        # "pipeline" as a bare pipeline-route keyword matched any pipeline --
        # data, ETL, or RAG -- so an AI feature was dispatched to the CI/CD
        # engineer. The route keywords are compounds now.
        result = plan(
            task="Build a RAG pipeline with an llm provider and evaluate prompt quality",
            changed_files=["src/ai/rag.py"],
        )
        self.assertIn("ai-engineer", result["agents"]["primary"])
        self.assertNotIn("cicd-engineer", result["agents"]["primary"])
        self.assertIn("security-reviewer", result["agents"]["reviewers"])

    def test_visual_system_route_selects_the_visual_designer(self) -> None:
        result = plan(
            task="Define the design system tokens and component library",
            changed_files=["design-system/tokens.json"],
        )
        self.assertIn("visual-designer", result["agents"]["primary"])
        self.assertIn("accessibility-reviewer", result["agents"]["reviewers"])
        self.assertIn("interaction-designer", result["agents"]["support"])

    def test_delivery_sequencing_route_selects_the_delivery_sequencer(self) -> None:
        result = plan(
            task="Produce the dependency map and critical path for this initiative",
            changed_files=[],
        )
        self.assertIn("delivery-sequencer", result["agents"]["primary"])

    def test_selects_engineering_and_review_for_orchestration_config_only(self) -> None:
        result = plan(
            task="Adjust configuration behavior",
            changed_files=["roster/orchestration/routing.yaml"],
        )
        self.assertEqual(result["agents"]["primary"], ["application-engineer", "debugging-engineer"])
        self.assertEqual(result["agents"]["reviewers"], ["test-engineer", "code-reviewer"])
        self.assertEqual(result["workflow"], "agent-suite-maintenance")

    def test_adding_a_role_routes_to_agent_suite_maintenance_not_debugging(self) -> None:
        # Proposal 08, Bug 1: routine roster self-maintenance (adding a
        # role) has no debugging keyword and must not fall through to the
        # "debugging" workflow just because it shares paths with the
        # debugging route's agent-tune-up coverage.
        result = plan(
            task="Add a new specialist role to the roster",
            changed_files=["roster/catalog.yaml", "roster/planning/new-role/AGENT.md"],
        )
        self.assertEqual(result["workflow"], "agent-suite-maintenance")
        self.assertNotEqual(result["workflow"], "debugging")

    def test_orchestration_only_route_routes_to_agent_suite_maintenance(self) -> None:
        result = plan(
            task="Adjust the agent selector agent routing dispatch plan",
            changed_files=["notes.txt"],
        )
        self.assertEqual([route["id"] for route in result["matched_routes"]], ["orchestration"])
        self.assertEqual(result["workflow"], "agent-suite-maintenance")

    def test_adds_human_gates_for_production_database_migrations(self) -> None:
        result = plan(
            task="Run a production database migration that alters the users table",
            changed_files=["services/users/migrations/0042_users.sql"],
        )
        self.assertEqual(result["workflow"], "production-release")
        self.assertIn("backend-engineer", result["agents"]["primary"])
        self.assertIn("release-engineer", result["agents"]["support"])
        self.assertEqual(
            [gate["id"] for gate in result["human_gates"]],
            ["persistent-database-migration", "production-change"],
        )

    def test_selects_performance_testing_engineer_for_load_tests(self) -> None:
        result = plan(
            task="Add a load test to measure checkout throughput and latency under peak traffic",
            changed_files=["perf/checkout-load-test.js"],
        )
        self.assertIn("performance-testing-engineer", result["agents"]["primary"])
        self.assertIn("infrastructure-reviewer", result["agents"]["reviewers"])
        self.assertIn("cost-capacity-planner", result["agents"]["support"])

    def test_selects_chaos_resilience_engineer_for_fault_injection(self) -> None:
        result = plan(
            task="Run a game day exercise to inject node failure and verify automated recovery",
            changed_files=["chaos/node-failure-scenario.yaml"],
        )
        self.assertIn("chaos-resilience-engineer", result["agents"]["primary"])
        self.assertIn("infrastructure-reviewer", result["agents"]["reviewers"])
        self.assertIn("cloud-architect", result["agents"]["support"])
        self.assertIn("observability-sre", result["agents"]["support"])

    def test_selects_cloud_architect_as_primary_for_architecture_design(self) -> None:
        result = plan(
            task="Design the architecture for a new document-ingestion service",
            changed_files=["architecture/document-ingestion/adr-0001.md"],
        )
        self.assertIn("cloud-architect", result["agents"]["primary"])
        self.assertIn("threat-modeler", result["agents"]["reviewers"])
        self.assertNotIn("threat-modeler", result["agents"]["support"])

    def test_consuming_project_docs_architecture_path_still_matches(self) -> None:
        # Proposal 08, Bug 2 true-positive proof: a genuine consuming-
        # project system-architecture path under docs/ must still summon
        # the architecture-review roles after the "**/architecture/**"
        # glob was narrowed.
        result = plan(
            task="Record the new ADR for the ingestion service",
            changed_files=["docs/architecture/adr-001.md"],
        )
        route_ids = [route["id"] for route in result["matched_routes"]]
        self.assertIn("architecture-design", route_ids)
        self.assertIn("cloud-architect", result["agents"]["primary"])
        self.assertIn("threat-modeler", result["agents"]["reviewers"])

    def test_own_roster_architecture_directory_does_not_trigger_architecture_review(self) -> None:
        # Proposal 08, Bug 2: this repository's own roster/architecture/
        # phase directory (role definitions like cloud-architect/
        # threat-modeler themselves) must not be misread as a consuming
        # project's system-architecture work just because it shares the
        # literal path segment "architecture".
        result = plan(
            task="Tweak the interaction-designer role's escalation wording",
            changed_files=["roster/architecture/interaction-designer/AGENT.md"],
        )
        route_ids = [route["id"] for route in result["matched_routes"]]
        self.assertNotIn("architecture-design", route_ids)
        risk_ids = [risk["id"] for risk in result["matched_risks"]]
        self.assertNotIn("architecture-change", risk_ids)
        self.assertNotIn("cloud-architect", result["agents"]["primary"])
        self.assertNotIn("threat-modeler", result["agents"]["primary"])
        self.assertNotIn("cloud-architect", result["agents"]["support"])
        self.assertNotIn("threat-modeler", result["agents"]["support"])

    def test_greenfield_service_brief_pulls_in_design_and_threat_modeling_support(self) -> None:
        result = plan(
            task=(
                "Design and build a new greenfield policy-exception-register service. "
                "Requires new API/schema design coordinated with the shared governance "
                "model, and threat modeling for exception forgery and replay."
            ),
            changed_files=[
                "policy-exception-register/go.mod",
                "policy-exception-register/internal/api/handlers.go",
            ],
        )
        self.assertIn("api-contract-engineer", result["agents"]["primary"])
        self.assertIn("cloud-architect", result["agents"]["support"])
        self.assertIn("threat-modeler", result["agents"]["support"])
        matched_risks = {risk["id"]: risk for risk in result["matched_risks"]}
        self.assertIn("architecture-change", matched_risks)
        self.assertIn("greenfield", matched_risks["architecture-change"]["reasons"]["keywords"])
        self.assertIn("threat modeling", matched_risks["architecture-change"]["reasons"]["keywords"])

    def test_matched_risks_include_populated_reasons(self) -> None:
        result = plan(
            task="Run a production database migration that alters the users table",
            changed_files=["services/users/migrations/0042_users.sql"],
        )
        matched_risks = {risk["id"]: risk for risk in result["matched_risks"]}
        self.assertIn("database-migration", matched_risks)
        reasons = matched_risks["database-migration"]["reasons"]
        self.assertIsNotNone(reasons)
        self.assertTrue(reasons["keywords"] or reasons["paths"])
        self.assertNotIn("matched", reasons)

    def test_feature_refinement_reaches_change_intake(self) -> None:
        result = plan(task="Refine the existing exception approval feature based on user feedback")
        self.assertEqual(result["dispatch_disposition"]["status"], "advisory-only")
        self.assertIn("product-intent-agent", result["agents"]["support"])
        self.assertIn("requirements-agent", result["agents"]["support"])

    def test_code_review_request_selects_code_reviewer_as_primary_without_an_implementer(self) -> None:
        result = plan(task="Review this pull request for correctness and security")
        self.assertEqual(result["agents"]["primary"], ["code-reviewer"])
        self.assertIn("security-reviewer", result["agents"]["support"])
        self.assertNotIn("backend-engineer", result["agents"]["primary"])
        self.assertNotIn("frontend-engineer", result["agents"]["primary"])

    def test_returns_needs_triage_instead_of_guessing(self) -> None:
        result = plan(task="Investigate an unexplained issue", changed_files=["unknown/file.xyz"])
        self.assertEqual(result["status"], "needs-triage")
        self.assertEqual(result["workflow"], "needs-triage")
        self.assertEqual(result["agents"], {"primary": [], "reviewers": [], "support": []})

    def test_generates_stable_task_id(self) -> None:
        first = plan(task="Update Terraform", changed_files=["main.tf"])
        second = plan(task="Update Terraform", changed_files=["main.tf"])
        self.assertEqual(first["task_id"], second["task_id"])
        self.assertEqual(first["task_id"], "local-c4361ed30b71")

    def test_routes_orchestrator_source_to_application_engineering(self) -> None:
        result = plan(
            task="Refactor the local agent selector",
            changed_files=["roster/orchestration/src/select_agents.py"],
        )
        self.assertEqual(result["agents"]["primary"], ["application-engineer", "debugging-engineer"])
        self.assertIn("test-engineer", result["agents"]["reviewers"])
        self.assertIn("code-reviewer", result["agents"]["reviewers"])

    def test_orchestration_example_architecture_does_not_select_agent_suite_debugging(self) -> None:
        result = plan(
            task="Resolve architecture decisions for OIDC and PostgreSQL recovery",
            changed_files=["roster/orchestration/examples/example/architecture.md"],
            classification="internal",
            task_id="EXAMPLE-ARCH",
        )
        self.assertEqual(result["workflow"], "new-service")
        self.assertNotIn("debugging-engineer", result["agents"]["primary"])

    def test_explicit_files_support_repeat_comma_and_stable_deduplication(self) -> None:
        self.assertEqual(
            explicit_files(["frontend/a.ts, services/a.go", "frontend/a.ts", "main.tf"]),
            ["frontend/a.ts", "services/a.go", "main.tf"],
        )

    @patch("select_agents._run_git")
    def test_git_status_discovery_preserves_order_and_rename_destination(self, run_git) -> None:
        run_git.return_value = " M frontend/a.ts\0R  infra/new.tf\0old.tf\0?? tests/new.feature\0"
        self.assertEqual(
            discover_changed_files(None),
            {
                "source": "git-status",
                "files": ["frontend/a.ts", "infra/new.tf", "tests/new.feature"],
            },
        )
        run_git.assert_called_once_with(
            ["status", "--short", "-z", "--untracked-files=all"],
            AGENTS_ROOT.parent.resolve(),
        )

    @patch("select_agents._run_git")
    def test_git_status_discovery_preserves_quoted_paths_verbatim(self, run_git) -> None:
        # -z output is never quoted/escaped (unlike plain --short, which
        # octal-escapes non-ASCII/special-character paths under the default
        # core.quotePath) — a path containing a literal " -> " substring or
        # non-ASCII characters must survive intact.
        run_git.return_value = "A  frontend/café -> menu.ts\0?? weird dir/file with spaces.txt\0"
        self.assertEqual(
            discover_changed_files(None),
            {
                "source": "git-status",
                "files": ["frontend/café -> menu.ts", "weird dir/file with spaces.txt"],
            },
        )

    @patch("select_agents._run_git")
    def test_git_base_discovery_uses_three_dot_diff(self, run_git) -> None:
        run_git.return_value = "services/a.go\ninfra/main.tf\n"
        self.assertEqual(
            discover_changed_files("main"),
            {
                "source": "git-diff:main...HEAD",
                "files": ["services/a.go", "infra/main.tf"],
            },
        )
        run_git.assert_called_once_with(["diff", "--name-only", "main...HEAD"], AGENTS_ROOT.parent.resolve())

    def test_origin_slug_supports_common_git_url_forms(self) -> None:
        origins = {
            "https://github.com/Owner/Repository.git": "owner/repository",
            "ssh://git@github.com/Owner/Repository.git": "owner/repository",
            "git@github.com:Owner/Repository.git": "owner/repository",
        }
        for origin, expected in origins.items():
            with self.subTest(origin=origin), patch("select_agents._run_git", return_value=origin):
                self.assertEqual(expected, _origin_slug(AGENTS_ROOT.parent))

    def test_repository_source_falls_back_to_canonical_path_hash(self) -> None:
        with tempfile.TemporaryDirectory(prefix="Source Repo ") as temporary_directory:
            root = Path(temporary_directory).resolve()
            expected_hash = hashlib.sha256(str(root).encode("utf-8")).hexdigest()[:12]
            expected_name = re.sub(r"[^a-z0-9._-]+", "-", root.name.lower()).strip("-")
            with patch("select_agents._run_git", side_effect=RuntimeError("no origin")):
                self.assertEqual(
                    f"local-{expected_name}-{expected_hash}",
                    resolve_knowledge_source(root),
                )

    def test_cli_root_targets_unrelated_git_repository_for_status_and_base(self) -> None:
        selector = ROOT / "src" / "select_agents.py"
        with tempfile.TemporaryDirectory() as temporary_directory:
            target = Path(temporary_directory) / "target"
            caller = Path(temporary_directory) / "caller"
            target.mkdir()
            caller.mkdir()
            subprocess.run(["git", "init", "-q", str(target)], check=True)
            subprocess.run(["git", "-C", str(target), "config", "user.email", "test@example.invalid"], check=True)
            subprocess.run(["git", "-C", str(target), "config", "user.name", "Test"], check=True)
            subprocess.run(
                ["git", "-C", str(target), "remote", "add", "origin", "https://github.com/Example/TargetRepo.git"],
                check=True,
            )
            (target / "frontend").mkdir()
            (target / "frontend" / "base.tsx").write_text("base\n", encoding="utf-8")
            subprocess.run(["git", "-C", str(target), "add", "."], check=True)
            subprocess.run(["git", "-C", str(target), "commit", "-qm", "base"], check=True)
            base = subprocess.run(
                ["git", "-C", str(target), "rev-parse", "HEAD"],
                check=True, capture_output=True, text=True, encoding="utf-8",
            ).stdout.strip()
            (target / "services").mkdir()
            (target / "services" / "api.go").write_text("package services\n", encoding="utf-8")
            subprocess.run(["git", "-C", str(target), "add", "."], check=True)
            subprocess.run(["git", "-C", str(target), "commit", "-qm", "service"], check=True)
            (target / "frontend" / "base.tsx").write_text("dirty\n", encoding="utf-8")

            common = [sys.executable, str(selector), "--root", str(target), "--task", "Update React and API"]
            status = subprocess.run(common, cwd=caller, check=True, capture_output=True, text=True)
            status_plan = json.loads(status.stdout)
            self.assertEqual(str(target.resolve()), status_plan["inputs"]["repository_root"])
            self.assertEqual("example/targetrepo", status_plan["inputs"]["source_filter"])
            self.assertEqual(["frontend/base.tsx"], status_plan["inputs"]["changed_files"])

            diff = subprocess.run([*common, "--base", base], cwd=caller, check=True, capture_output=True, text=True)
            diff_plan = json.loads(diff.stdout)
            self.assertEqual(str(target.resolve()), diff_plan["inputs"]["repository_root"])
            self.assertEqual("example/targetrepo", diff_plan["inputs"]["source_filter"])
            self.assertEqual(["services/api.go"], diff_plan["inputs"]["changed_files"])

    def test_non_git_root_requires_explicit_files(self) -> None:
        selector = ROOT / "src" / "select_agents.py"
        with tempfile.TemporaryDirectory() as temporary_directory:
            root = Path(temporary_directory)
            implicit = subprocess.run(
                [sys.executable, str(selector), "--root", str(root), "--task", "Update React"],
                check=False, capture_output=True, text=True,
            )
            self.assertNotEqual(0, implicit.returncode)
            explicit = subprocess.run(
                [sys.executable, str(selector), "--root", str(root), "--task", "Update React", "--files", "frontend/App.tsx"],
                check=True, capture_output=True, text=True,
            )
            self.assertEqual("explicit", json.loads(explicit.stdout)["inputs"]["changed_file_source"])

    def test_conjunctive_production_and_destructive_gates(self) -> None:
        production_phrases = [
            "Apply the Helm chart in production",
            "Rotate credentials in the live environment",
            "Restart the prod service",
        ]
        destructive_phrases = [
            "Delete the Kubernetes namespace",
            "Drop the customer database",
            "Truncate the audit table",
            "Run terraform destroy",
            "Destroy the environment",
            "Wipe the disk",
            "Destroy the VM",
            "wipe the cache",
        ]
        for phrase in production_phrases:
            with self.subTest(phrase=phrase):
                self.assertIn("production-change", [gate["id"] for gate in plan(task=phrase)["human_gates"]])
        for phrase in destructive_phrases:
            with self.subTest(phrase=phrase):
                self.assertIn("destructive-action", [gate["id"] for gate in plan(task=phrase)["human_gates"]])
        for phrase in (
            "Observe production runtime health",
            "Read the production dashboard",
            "Delete a local variable",
            "Evaluate a destroy command example",
            "Inspect a wipe warning",
            "Delete a local variable and rename it",
            "Evaluate a destroy command example, does it work?",
            "Please remove it from the README",
            "Please destroy it",
            "Just delete it",
            "The bug will destroy it eventually",
        ):
            with self.subTest(benign=phrase):
                self.assertNotIn(
                    "production-change",
                    [gate["id"] for gate in plan(task=phrase)["human_gates"]],
                )
                self.assertNotIn(
                    "destructive-action",
                    [gate["id"] for gate in plan(task=phrase)["human_gates"]],
                )

    def test_load_routing_rejects_malformed_keyword_groups(self) -> None:
        for keyword_groups in ("destroy delete", [[]], [["destroy", ""]], [["destroy", 42]]):
            with self.subTest(keyword_groups=keyword_groups):
                config = json.loads((ROOT / "routing.yaml").read_text(encoding="utf-8"))
                config["risk_rules"][-1]["keyword_groups"] = keyword_groups
                with tempfile.TemporaryDirectory() as temporary_directory:
                    path = Path(temporary_directory) / "routing.json"
                    path.write_text(json.dumps(config), encoding="utf-8")
                    with self.assertRaisesRegex(ValueError, "keyword_groups"):
                        load_routing(path)

    def test_load_routing_rejects_inverted_dynamic_team_range(self) -> None:
        config = json.loads((ROOT / "routing.yaml").read_text(encoding="utf-8"))
        config["team_recipes"][-1]["instances"] = {"min": 4, "max": 2}
        with tempfile.TemporaryDirectory() as temporary_directory:
            path = Path(temporary_directory) / "routing.json"
            path.write_text(json.dumps(config), encoding="utf-8")
            with self.assertRaisesRegex(ValueError, "1 <= min <= max"):
                load_routing(path)

    def test_selection_schema_rejects_malformed_closed_contracts(self) -> None:
        import jsonschema

        schema = json.loads((ROOT / "selection.schema.json").read_text(encoding="utf-8"))
        validator = jsonschema.Draft202012Validator(schema)
        valid = plan(
            task="Deploy the API to production",
            changed_files=["services/api/main.go"],
            classification="internal",
            task_id="SCHEMA-1",
        )
        validator.validate(valid)

        malformed = []
        value = json.loads(json.dumps(valid))
        value["inputs"]["unknown"] = True
        malformed.append(value)
        value = json.loads(json.dumps(valid))
        value["matched_risks"][0]["reasons"]["keywords"] = "deploy"
        malformed.append(value)
        value = json.loads(json.dumps(valid))
        value["matched_routes"][0]["reasons"]["keywords"] = "deploy"
        malformed.append(value)
        value = json.loads(json.dumps(valid))
        del value["matched_routes"][0]["reasons"]
        malformed.append(value)
        value = json.loads(json.dumps(valid))
        value["agents"]["unknown"] = []
        malformed.append(value)
        value = json.loads(json.dumps(valid))
        value["lifecycle_tracking"] = {"status": "integrated", "reason": "not allowed"}
        malformed.append(value)
        value = json.loads(json.dumps(valid))
        value["knowledge_context"]["requests"][0]["invocation"]["unknown"] = True
        malformed.append(value)
        for index, candidate in enumerate(malformed):
            with self.subTest(index=index):
                self.assertTrue(list(validator.iter_errors(candidate)))

    def test_rejects_invalid_classification_for_selected_agents(self) -> None:
        with self.assertRaisesRegex(ValueError, "Invalid classification: secret"):
            plan(
                task="Update Terraform",
                changed_files=["main.tf"],
                classification="secret",
            )

    def test_rejects_knowledge_top_outside_orchestration_policy(self) -> None:
        for top in (0, 21, "many"):
            with self.subTest(top=top), self.assertRaisesRegex(
                ValueError, "Knowledge top must be an integer from 1 through 20"
            ):
                plan(
                    task="Update Terraform",
                    changed_files=["main.tf"],
                    classification="internal",
                    top=top,
                )

    def test_cli_emits_a_valid_plan_for_explicit_files(self) -> None:
        result = subprocess.run(
            [
                sys.executable,
                str(ROOT / "src" / "select_agents.py"),
                "--task",
                "Change the GitLab pipeline runner configuration",
                "--files",
                ".gitlab-ci.yml",
                "--classification",
                "internal",
                "--task-id",
                "CI-7",
            ],
            cwd=AGENTS_ROOT.parent,
            check=False,
            capture_output=True,
            text=True,
            encoding="utf-8",
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        output = json.loads(result.stdout)
        self.assertEqual(output["task_id"], "CI-7")
        self.assertEqual(output["workflow"], "pipeline-change")
        self.assertIn("cicd-engineer", output["agents"]["primary"])
        self.assertIn("pipeline-security-reviewer", output["agents"]["reviewers"])

    def test_cli_emits_utf8_and_writes_output_relative_to_callers_directory(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            result = subprocess.run(
                [
                    sys.executable,
                    str(ROOT / "src" / "select_agents.py"),
                    "--task",
                    "Añadir navegación React – café",
                    "--files",
                    "frontend/src/Nav.tsx",
                    "--task-id",
                    "UI-UTF8",
                    "--output",
                    "plans/selección.json",
                ],
                cwd=temporary_directory,
                check=False,
                capture_output=True,
            )
            self.assertEqual(result.returncode, 0, result.stderr.decode("utf-8"))
            self.assertEqual(result.stdout, b"")
            output_path = Path(temporary_directory) / "plans" / "selección.json"
            raw_output = output_path.read_bytes()
            self.assertIn("Añadir navegación React – café".encode("utf-8"), raw_output)
            self.assertTrue(raw_output.endswith(b"\n"))
            self.assertEqual(json.loads(raw_output.decode("utf-8"))["task_id"], "UI-UTF8")

    def test_cli_stdout_is_utf8(self) -> None:
        result = subprocess.run(
            [
                sys.executable,
                str(ROOT / "src" / "select_agents.py"),
                "--task",
                "Añadir navegación React – café",
                "--files",
                "frontend/src/Café.tsx",
                "--task-id",
                "UI-STDOUT-UTF8",
            ],
            cwd=AGENTS_ROOT.parent,
            check=False,
            capture_output=True,
        )
        self.assertEqual(result.returncode, 0, result.stderr.decode("utf-8"))
        decoded = result.stdout.decode("utf-8", errors="strict")
        self.assertIn("Añadir navegación React – café", decoded)
        self.assertIn("frontend/src/Café.tsx", decoded)
        self.assertTrue(result.stdout.endswith(b"\n"))

    @patch("build_dispatch_plan.try_lifecycle_contract", return_value=None)
    def test_standalone_mode_still_dispatches_teams_without_agentic_sdlc(self, _mock) -> None:
        result = plan(
            task="Add a React upload form backed by a PostgreSQL API",
            changed_files=["frontend/src/Upload.tsx", "services/upload/main.go"],
            classification="internal",
            task_id="STANDALONE-1",
        )
        from build_dispatch_plan import STANDALONE_REASON

        self.assertEqual(result["lifecycle_tracking"], {"status": "standalone", "reason": STANDALONE_REASON})
        self.assertEqual(result["agents"]["primary"], ["frontend-engineer", "backend-engineer"])
        self.assertIn("test-engineer", result["agents"]["reviewers"])

    @patch("build_dispatch_plan.try_lifecycle_contract", return_value=None)
    def test_standalone_mode_still_reports_needs_triage(self, _mock) -> None:
        result = plan(task="Investigate an unexplained issue", changed_files=["unknown/file.xyz"])
        self.assertEqual(result["status"], "needs-triage")
        self.assertEqual(result["lifecycle_tracking"]["status"], "standalone")

    @patch(
        "build_dispatch_plan.require_lifecycle_contract",
        side_effect=RuntimeError(agentic_sdlc_contracts.install_message()),
    )
    def test_require_sdlc_fails_fast_without_agentic_sdlc(self, _mock) -> None:
        with self.assertRaisesRegex(RuntimeError, r"Agentic SDLC v[\d.]+ or newer .* is required"):
            build_dispatch_plan(
                CONFIG,
                CATALOG,
                {
                    "task": "Update Terraform",
                    "changed_files": ["main.tf"],
                    "changed_file_source": "test",
                    "repository_root": str(AGENTS_ROOT.parent),
                    "source": "example/repository",
                },
                require_sdlc=True,
            )

    @patch("agentic_sdlc_contracts._resolve_executable", return_value=None)
    def test_agentic_sdlc_contracts_try_returns_none_when_unresolved(self, _mock) -> None:
        agentic_sdlc_contracts._fetch_contract.cache_clear()
        self.assertIsNone(agentic_sdlc_contracts.try_lifecycle_contract())
        with self.assertRaisesRegex(RuntimeError, r"Agentic SDLC v[\d.]+ or newer .* is required"):
            agentic_sdlc_contracts.require_lifecycle_contract()

    def test_resolved_lifecycle_executable_failures_never_degrade(self) -> None:
        cases = [
            (
                subprocess.CompletedProcess(["kernel"], 2, "", "contract unavailable"),
                "contract lookup failed",
            ),
            (
                subprocess.CompletedProcess(["kernel"], 0, "not json", ""),
                "malformed JSON",
            ),
            (
                subprocess.CompletedProcess(["kernel"], 0, '{"version": 1, "gates": []}', ""),
                "incompatible",
            ),
        ]
        for completed, message in cases:
            with self.subTest(message=message):
                agentic_sdlc_contracts._fetch_contract.cache_clear()
                with (
                    patch("agentic_sdlc_contracts._resolve_executable", return_value="/fake/kernel"),
                    patch("agentic_sdlc_contracts.subprocess.run", return_value=completed),
                    self.assertRaisesRegex(RuntimeError, message),
                ):
                    agentic_sdlc_contracts.try_lifecycle_contract()

    def test_resolved_lifecycle_timeout_is_actionable(self) -> None:
        agentic_sdlc_contracts._fetch_contract.cache_clear()
        with (
            patch("agentic_sdlc_contracts._resolve_executable", return_value="/fake/kernel"),
            patch(
                "agentic_sdlc_contracts.subprocess.run",
                side_effect=subprocess.TimeoutExpired(["/fake/kernel"], 10),
            ),
            self.assertRaisesRegex(RuntimeError, "timed out"),
        ):
            agentic_sdlc_contracts.try_lifecycle_contract()


if __name__ == "__main__":
    unittest.main()
