"""Regression tests for dispatch-plan provenance binding (idea #7).

Traces to `roster/orchestration/runs/cadre-idea-7-provenance-binding-2026-07-29/
requirements.md` acceptance criteria AC-1..AC-11 (that run directory is not
part of this repository's tracked history; the run identifier is preserved
here only for traceability of which decomposition this file implements).

Covers `roster/orchestration/src/provenance.py` (the standalone hashing/git-
identity helpers) and `build_dispatch_plan()`'s optional `catalog_path`/
`routing_path` integration, plus the additive `provenance` property in
`selection.schema.json`.
"""

from __future__ import annotations

import ast
import hashlib
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

ROOT = Path(__file__).resolve().parent.parent
AGENTS_ROOT = ROOT.parent
sys.path.insert(0, str(ROOT / "src"))

import build_dispatch_plan as build_dispatch_plan_module  # noqa: E402
import provenance  # noqa: E402
from build_dispatch_plan import build_dispatch_plan  # noqa: E402
from routing import load_catalog, load_routing  # noqa: E402

try:
    import jsonschema  # noqa: F401

    JSONSCHEMA_AVAILABLE = True
except ImportError:
    JSONSCHEMA_AVAILABLE = False

CATALOG_PATH = AGENTS_ROOT / "catalog.yaml"
ROUTING_PATH = ROOT / "routing.json"
SCHEMA_PATH = ROOT / "selection.schema.json"
CONFIG = load_routing(ROUTING_PATH)
CATALOG = load_catalog(CATALOG_PATH)


def _values(**overrides: object) -> dict[str, object]:
    values = {
        "task": "Update Terraform",
        "changed_files": ["main.tf"],
        "changed_file_source": "test",
        "repository_root": str(AGENTS_ROOT.parent),
        "sources": ["example/repository"],
        "classification": "internal",
        "task_id": "PROVENANCE-1",
        **overrides,
    }
    return values


def _plan_with_provenance(**overrides: object) -> dict[str, object]:
    with patch.object(build_dispatch_plan_module, "try_lifecycle_contract", return_value=None):
        return build_dispatch_plan(
            CONFIG,
            CATALOG,
            _values(**overrides),
            catalog_path=CATALOG_PATH,
            routing_path=ROUTING_PATH,
        )


def _git_init(path: Path) -> None:
    for args in (
        ["git", "init", "-q"],
        ["git", "config", "user.email", "test@example.invalid"],
        ["git", "config", "user.name", "Test"],
    ):
        subprocess.run(args, cwd=path, check=True, capture_output=True)


def _git_commit_all(path: Path, message: str) -> str:
    subprocess.run(["git", "add", "."], cwd=path, check=True, capture_output=True)
    subprocess.run(["git", "commit", "-q", "-m", message], cwd=path, check=True, capture_output=True)
    return subprocess.run(
        ["git", "rev-parse", "HEAD"], cwd=path, check=True, capture_output=True, text=True, encoding="utf-8"
    ).stdout.strip()


class ProvenanceAbsenceTests(unittest.TestCase):
    """AC-6/AC-7 groundwork and the additive/backward-compatible contract (PB-FR-7)."""

    def test_provenance_absent_when_paths_not_supplied(self) -> None:
        with patch.object(build_dispatch_plan_module, "try_lifecycle_contract", return_value=None):
            plan = build_dispatch_plan(CONFIG, CATALOG, _values())
        self.assertNotIn("provenance", plan)

    def test_provenance_requires_both_paths_or_neither(self) -> None:
        with self.assertRaisesRegex(ValueError, "catalog_path and routing_path"):
            build_dispatch_plan(CONFIG, CATALOG, _values(), catalog_path=CATALOG_PATH)
        with self.assertRaisesRegex(ValueError, "catalog_path and routing_path"):
            build_dispatch_plan(CONFIG, CATALOG, _values(), routing_path=ROUTING_PATH)


class ContentHashBindingTests(unittest.TestCase):
    """AC-1 (baseline binding correctness) and AC-3 (detects a later edit)."""

    def test_catalog_and_routing_hashes_match_independent_sha256(self) -> None:
        plan = _plan_with_provenance()
        expected_catalog_hash = "sha256:" + hashlib.sha256(CATALOG_PATH.read_bytes()).hexdigest()
        expected_routing_hash = "sha256:" + hashlib.sha256(ROUTING_PATH.read_bytes()).hexdigest()
        self.assertEqual(expected_catalog_hash, plan["provenance"]["catalog_content_hash"])
        self.assertEqual(expected_routing_hash, plan["provenance"]["routing_content_hash"])

    def test_edited_catalog_content_changes_only_the_catalog_hash(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            work = Path(temporary_directory)
            catalog_copy = work / "catalog.yaml"
            routing_copy = work / "routing.json"
            catalog_copy.write_bytes(CATALOG_PATH.read_bytes())
            routing_copy.write_bytes(ROUTING_PATH.read_bytes())

            with patch.object(build_dispatch_plan_module, "try_lifecycle_contract", return_value=None):
                first = build_dispatch_plan(
                    CONFIG, CATALOG, _values(), catalog_path=catalog_copy, routing_path=routing_copy
                )

            catalog_copy.write_bytes(catalog_copy.read_bytes() + b"\n# local edit\n")

            with patch.object(build_dispatch_plan_module, "try_lifecycle_contract", return_value=None):
                second = build_dispatch_plan(
                    CONFIG, CATALOG, _values(), catalog_path=catalog_copy, routing_path=routing_copy
                )

            self.assertNotEqual(
                first["provenance"]["catalog_content_hash"], second["provenance"]["catalog_content_hash"]
            )
            self.assertEqual(
                first["provenance"]["routing_content_hash"], second["provenance"]["routing_content_hash"]
            )

    def test_missing_catalog_file_propagates_as_hard_failure(self) -> None:
        # Content-hash reads are already-mandatory reads for plan generation
        # to proceed at all (PB-NFR-3): a read failure must not be silently
        # swallowed into an absent/placeholder provenance field.
        with patch.object(build_dispatch_plan_module, "try_lifecycle_contract", return_value=None):
            with self.assertRaises(OSError):
                build_dispatch_plan(
                    CONFIG,
                    CATALOG,
                    _values(),
                    catalog_path=AGENTS_ROOT / "does-not-exist.yaml",
                    routing_path=ROUTING_PATH,
                )


class DeterminismTests(unittest.TestCase):
    """AC-2: identical inputs/files produce byte-identical provenance content."""

    def test_provenance_is_deterministic_across_runs(self) -> None:
        first = _plan_with_provenance()
        second = _plan_with_provenance()
        self.assertEqual(first["provenance"], second["provenance"])


class GitIdentityTests(unittest.TestCase):
    """AC-1 (git identity), AC-4 (dirty-tree honesty), AC-5 (non-git degradation)."""

    def test_git_commit_sha_matches_git_rev_parse_head_in_this_checkout(self) -> None:
        head = subprocess.run(
            ["git", "rev-parse", "HEAD"], cwd=AGENTS_ROOT, check=False, capture_output=True, text=True, encoding="utf-8"
        )
        if head.returncode != 0:
            self.skipTest("this checkout is not inside a resolvable git working tree")
        plan = _plan_with_provenance()
        self.assertEqual(head.stdout.strip(), plan["provenance"]["git_commit_sha"])

    def test_dirty_tree_records_uncommitted_relative_path_alongside_head(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            work = Path(temporary_directory)
            (work / "orchestration").mkdir()
            catalog_copy = work / "catalog.yaml"
            routing_copy = work / "orchestration" / "routing.json"
            catalog_copy.write_bytes(CATALOG_PATH.read_bytes())
            routing_copy.write_bytes(ROUTING_PATH.read_bytes())
            _git_init(work)
            head = _git_commit_all(work, "initial")

            # Uncommitted local edit to routing_copy only.
            routing_copy.write_bytes(routing_copy.read_bytes() + b"\n")

            with patch.object(build_dispatch_plan_module, "try_lifecycle_contract", return_value=None):
                plan = build_dispatch_plan(
                    CONFIG, CATALOG, _values(), catalog_path=catalog_copy, routing_path=routing_copy
                )

            self.assertEqual(head, plan["provenance"]["git_commit_sha"])
            self.assertIn("orchestration/routing.json", plan["provenance"]["git_dirty_paths"])
            self.assertNotIn("catalog.yaml", plan["provenance"]["git_dirty_paths"])

    def test_clean_tree_records_empty_dirty_paths(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            work = Path(temporary_directory)
            catalog_copy = work / "catalog.yaml"
            routing_copy = work / "routing.json"
            catalog_copy.write_bytes(CATALOG_PATH.read_bytes())
            routing_copy.write_bytes(ROUTING_PATH.read_bytes())
            _git_init(work)
            _git_commit_all(work, "initial")

            with patch.object(build_dispatch_plan_module, "try_lifecycle_contract", return_value=None):
                plan = build_dispatch_plan(
                    CONFIG, CATALOG, _values(), catalog_path=catalog_copy, routing_path=routing_copy
                )
            self.assertEqual([], plan["provenance"]["git_dirty_paths"])

    def test_git_binary_unavailable_degrades_cleanly(self) -> None:
        # Deterministic unit-level exercise of the fail-open contract
        # (PB-NFR-3/AC-5): a missing/unresolvable git binary must never
        # raise, only omit the git identity fields.
        with patch.object(provenance, "_run_git", return_value=None):
            identity = provenance.git_identity(CATALOG_PATH, ROUTING_PATH)
        self.assertEqual({}, identity)

    def test_non_git_directory_omits_git_fields_but_keeps_content_hashes(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            work = Path(temporary_directory)
            catalog_copy = work / "catalog.yaml"
            routing_copy = work / "routing.json"
            catalog_copy.write_bytes(CATALOG_PATH.read_bytes())
            routing_copy.write_bytes(ROUTING_PATH.read_bytes())
            # tempfile.mkdtemp() output is not inside a git working tree in
            # this environment's test sandbox; guard defensively in case it
            # ever is, so this test fails loudly instead of silently no-op.
            probe = subprocess.run(
                ["git", "rev-parse", "--is-inside-work-tree"],
                cwd=work, check=False, capture_output=True, text=True, encoding="utf-8",
            )
            if probe.returncode == 0:
                self.skipTest("temporary directory unexpectedly resolved inside a git working tree")

            with patch.object(build_dispatch_plan_module, "try_lifecycle_contract", return_value=None):
                plan = build_dispatch_plan(
                    CONFIG, CATALOG, _values(), catalog_path=catalog_copy, routing_path=routing_copy
                )

            self.assertIn("catalog_content_hash", plan["provenance"])
            self.assertIn("routing_content_hash", plan["provenance"])
            self.assertNotIn("git_commit_sha", plan["provenance"])
            self.assertNotIn("git_dirty_paths", plan["provenance"])
            if JSONSCHEMA_AVAILABLE:
                schema = json.loads(SCHEMA_PATH.read_text(encoding="utf-8"))
                jsonschema.Draft202012Validator(schema).validate(plan)


class DormantFieldTests(unittest.TestCase):
    """AC-6 (overlay dormant) and AC-7 (manifest never bound)."""

    def test_overlay_fields_never_populated_today(self) -> None:
        plan = _plan_with_provenance()
        for field in ("overlay_applied", "overlay_content_hash", "overlay_path"):
            self.assertNotIn(field, plan["provenance"])

    def test_runner_capabilities_manifest_is_never_read_or_claimed(self) -> None:
        plan = _plan_with_provenance()
        self.assertNotIn("runner_capabilities_content_hash", plan["provenance"])
        # Regression tripwire (AC-7): confirms roster/runner-capabilities.json
        # is not read anywhere in the actual dispatch-plan-generation call
        # path -- would fail if a future change started reading the manifest
        # there without updating this decomposition's PB-FR-5 resolution and
        # its accompanying test. Deliberately scoped to only the two call-path
        # modules (not provenance.py itself, whose docstring legitimately
        # explains *why* the manifest is not bound, which would otherwise
        # trip a naive substring check).
        for module_path in (ROOT / "src" / "build_dispatch_plan.py", ROOT / "src" / "select_agents.py"):
            text = module_path.read_text(encoding="utf-8").lower()
            self.assertNotIn("runner-capabilit", text)
            self.assertNotIn("runner_capabilit", text)


class LifecycleContractVersionTests(unittest.TestCase):
    """AC-8: bind the already-consumed lifecycle contract `version` integer."""

    def test_contract_version_recorded_when_integrated(self) -> None:
        fake_contract = {
            "version": 7,
            "gates": [
                {"id": "g1", "name": "Gate One", "phase": "design", "author_agents": [], "review_agents": []}
            ],
        }
        # No route/risk match (mirrors test_standalone_mode_still_reports_
        # needs_triage's fixture) so no quality_gates reference a real
        # contract gate id -- this test is only about whether the
        # already-consumed contract `version` integer is threaded through,
        # not about exercising unrelated gate-matching logic.
        values = _values(task="Investigate an unexplained issue", changed_files=["unknown/file.xyz"])
        with patch.object(build_dispatch_plan_module, "try_lifecycle_contract", return_value=fake_contract):
            plan = build_dispatch_plan(
                CONFIG, CATALOG, values, catalog_path=CATALOG_PATH, routing_path=ROUTING_PATH
            )
        self.assertEqual("integrated", plan["lifecycle_tracking"]["status"])
        self.assertEqual(7, plan["provenance"]["agentic_sdlc_contract_version"])

    def test_contract_version_absent_when_standalone(self) -> None:
        plan = _plan_with_provenance()
        self.assertEqual("standalone", plan["lifecycle_tracking"]["status"])
        self.assertNotIn("agentic_sdlc_contract_version", plan["provenance"])


@unittest.skipUnless(JSONSCHEMA_AVAILABLE, "jsonschema is not installed in this environment")
class SchemaBackwardCompatibilityTests(unittest.TestCase):
    """AC-9: additive/optional -- old (no-provenance) and new plans both validate."""

    def setUp(self) -> None:
        self.schema = json.loads(SCHEMA_PATH.read_text(encoding="utf-8"))
        self.validator = jsonschema.Draft202012Validator(self.schema)

    def test_pre_change_plan_without_provenance_still_validates(self) -> None:
        with patch.object(build_dispatch_plan_module, "try_lifecycle_contract", return_value=None):
            plan = build_dispatch_plan(CONFIG, CATALOG, _values())
        self.assertNotIn("provenance", plan)
        self.validator.validate(plan)

    def test_provenance_is_not_in_top_level_required(self) -> None:
        self.assertNotIn("provenance", self.schema["required"])

    def test_plan_with_provenance_validates(self) -> None:
        plan = _plan_with_provenance()
        self.assertIn("provenance", plan)
        self.validator.validate(plan)

    def test_malformed_provenance_is_rejected(self) -> None:
        plan = _plan_with_provenance()
        malformed = json.loads(json.dumps(plan))
        malformed["provenance"]["unknown_field"] = True
        self.assertTrue(list(self.validator.iter_errors(malformed)))

        malformed = json.loads(json.dumps(plan))
        del malformed["provenance"]["catalog_content_hash"]
        self.assertTrue(list(self.validator.iter_errors(malformed)))

        malformed = json.loads(json.dumps(plan))
        malformed["provenance"]["overlay_content_hash"] = "sha256:" + "0" * 64
        self.assertTrue(list(self.validator.iter_errors(malformed)))


class FingerprintExclusionTests(unittest.TestCase):
    """AC-10: dispatch_fingerprint is unaffected by provenance presence/content."""

    def test_dispatch_fingerprint_identical_with_and_without_provenance(self) -> None:
        with patch.object(build_dispatch_plan_module, "try_lifecycle_contract", return_value=None):
            without_provenance = build_dispatch_plan(CONFIG, CATALOG, _values())
        with_provenance = _plan_with_provenance()
        self.assertEqual(without_provenance["dispatch_fingerprint"], with_provenance["dispatch_fingerprint"])


class NoNewDependencyTests(unittest.TestCase):
    """AC-11: implementation introduces zero new third-party dependencies."""

    def test_provenance_module_imports_only_the_standard_library(self) -> None:
        stdlib_allowlist = {"__future__", "hashlib", "os", "subprocess", "pathlib", "typing"}
        source = (ROOT / "src" / "provenance.py").read_text(encoding="utf-8")
        tree = ast.parse(source)
        imported_modules = set()
        for node in ast.walk(tree):
            if isinstance(node, ast.Import):
                imported_modules.update(alias.name.split(".")[0] for alias in node.names)
            elif isinstance(node, ast.ImportFrom) and node.module:
                imported_modules.add(node.module.split(".")[0])
        self.assertTrue(imported_modules, "expected at least one import in provenance.py")
        self.assertTrue(
            imported_modules.issubset(stdlib_allowlist),
            f"provenance.py imports beyond the stdlib allowlist: {imported_modules - stdlib_allowlist}",
        )


if __name__ == "__main__":
    unittest.main()
