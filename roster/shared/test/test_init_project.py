from __future__ import annotations

import argparse
import contextlib
import hashlib
import io
import itertools
import json
import os
import shutil
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(ROOT / "src"))

import init_project as init_mod  # noqa: E402
import resolve as resolve_mod  # noqa: E402
from init_project import (  # noqa: E402
    InitError,
    build_autonomy_overlay,
    build_guardrails_overlay,
    build_prose_addendum_overlay,
    build_platform_overlay,
    build_structured_overlay,
    parse_field_decisions,
    plan_writes,
    refuse_if_self_checkout,
    require_field_decisions_cover,
    run_init,
    scan_guardrail_bullet,
    validate_autonomy_overlay_content,
    validate_platform_fragment,
)
import init_project_interactive as init_interactive_mod  # noqa: E402
from init_project_interactive import run_interactive_flow  # noqa: E402


def _make_project(root: Path) -> Path:
    (root / ".git").mkdir()
    return root


def _tree_hash(root: Path) -> str:
    digest = hashlib.sha256()
    for path in sorted(candidate for candidate in root.rglob("*") if candidate.is_file()):
        digest.update(str(path.relative_to(root)).encode("utf-8"))
        digest.update(path.read_bytes())
    return digest.hexdigest()


class SelfCheckoutGuardTests(unittest.TestCase):
    def test_refuses_this_repos_own_checkout(self) -> None:
        with self.assertRaises(InitError):
            refuse_if_self_checkout(init_mod.REPO_ROOT)

    def test_refuses_descendant_of_this_repos_checkout(self) -> None:
        with self.assertRaises(InitError):
            refuse_if_self_checkout(init_mod.REPO_ROOT / "roster" / "shared")

    def test_refuses_unrelated_clone_with_matching_markers(self) -> None:
        with tempfile.TemporaryDirectory(prefix="agents-init-selfcheckout-") as tmp:
            root = Path(tmp)
            (root / "roster" / "shared").mkdir(parents=True)
            (root / "roster" / "shared" / "team-profile.yaml").write_text("status: active\n")
            (root / "bin").mkdir()
            (root / "bin" / "subcommands.tsv").write_text("select\tx\ty\n")
            with self.assertRaises(InitError):
                refuse_if_self_checkout(root)

    def test_allows_ordinary_project(self) -> None:
        with tempfile.TemporaryDirectory(prefix="agents-init-ok-") as tmp:
            root = _make_project(Path(tmp))
            refuse_if_self_checkout(root)  # must not raise

    def test_refuses_when_markers_present_in_an_ancestor_not_just_target(self) -> None:
        # Finding 4: the marker check must walk up from the resolved target
        # through its ancestors, not just check the target directory itself,
        # so a genuine subdirectory of an unrelated clone of this same suite
        # is refused too.
        with tempfile.TemporaryDirectory(prefix="agents-init-selfcheckout-nested-") as tmp:
            root = Path(tmp)
            (root / "roster" / "shared").mkdir(parents=True)
            (root / "roster" / "shared" / "team-profile.yaml").write_text("status: active\n")
            (root / "bin").mkdir()
            (root / "bin" / "subcommands.tsv").write_text("select\tx\ty\n")
            nested = root / "some" / "nested" / "target"
            nested.mkdir(parents=True)
            with self.assertRaises(InitError):
                refuse_if_self_checkout(nested)


class FilesystemIdentityContainmentTests(unittest.TestCase):
    """Finding 4: the self-checkout/containment guard must compare paths by
    filesystem identity (os.stat device/inode), not by path string or
    Path.resolve() equality, so it isn't bypassable on a case-insensitive-
    but-case-preserving filesystem (e.g. macOS APFS/HFS+ default) where two
    differently-cased path strings can refer to the identical on-disk
    directory. This CI environment's filesystem is case-sensitive, so a
    genuine case-collision cannot be reproduced here; the test below instead
    verifies the containment decision is driven purely by
    `os.path.samestat`'s result, which is the actual property required.

    Manual verification on a real case-insensitive filesystem: create a
    project at `/Volumes/data/Project`, run
    `cadre init --target /Volumes/data/project --interactive` (note the
    lowercase `p`) against a checkout whose repo root is
    `/Volumes/data/Project`, and confirm it is refused exactly as
    `--target /Volumes/data/Project` would be.
    """

    def test_child_and_self_are_contained_unrelated_dir_is_not(self) -> None:
        with tempfile.TemporaryDirectory(prefix="agents-init-identity-") as tmp:
            root = Path(tmp)
            child = root / "sub"
            child.mkdir()
            self.assertTrue(init_mod._is_same_or_descendant(root, root))
            self.assertTrue(init_mod._is_same_or_descendant(child, root))
            outside = Path(tempfile.mkdtemp(prefix="agents-init-identity-outside-"))
            try:
                self.assertFalse(init_mod._is_same_or_descendant(outside, root))
            finally:
                shutil.rmtree(outside, ignore_errors=True)

    def test_not_yet_created_target_is_still_checked_by_identity(self) -> None:
        with tempfile.TemporaryDirectory(prefix="agents-init-identity-noexist-") as tmp:
            root = Path(tmp)
            not_yet_created = root / "sub" / "does-not-exist-yet"
            self.assertTrue(init_mod._is_same_or_descendant(not_yet_created, root))

    def test_decision_is_driven_by_filesystem_identity_not_by_path_strings(self) -> None:
        with tempfile.TemporaryDirectory(prefix="agents-init-identity-mock-") as tmp:
            a = Path(tmp) / "a"
            a.mkdir()
            b = Path(tmp) / "b"
            b.mkdir()
            # Two genuinely different, unrelated directories: real
            # filesystem identity says they are not the same location.
            self.assertFalse(init_mod._is_same_or_descendant(a, b))
            # Forcing os.path.samestat to report identity -- even though the
            # two paths' string representations are unrelated -- flips the
            # containment decision. This demonstrates the check is driven by
            # filesystem identity rather than string/path comparison, which
            # is exactly the property a case-insensitive-filesystem
            # collision needs the guard to have.
            with mock.patch("init_project.os.path.samestat", return_value=True):
                self.assertTrue(init_mod._is_same_or_descendant(a, b))


class WriteChokepointContainmentTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory(prefix="agents-init-write-")
        self.root = _make_project(Path(self.temporary.name))

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def test_writes_under_agents_shared(self) -> None:
        dest = init_mod._write_overlay(self.root, "team-profile.yaml", "status: active\n")
        self.assertEqual(dest, self.root / ".agents" / "shared" / "team-profile.yaml")
        self.assertEqual(dest.read_text(), "status: active\n")

    def test_rejects_symlink_escape_via_agents_dir(self) -> None:
        outside = Path(tempfile.mkdtemp(prefix="agents-init-outside-"))
        try:
            (self.root / ".agents").symlink_to(outside, target_is_directory=True)
            with self.assertRaises(InitError):
                init_mod._write_overlay(self.root, "team-profile.yaml", "status: active\n")
            self.assertFalse((outside / "shared").exists())
        finally:
            import shutil

            shutil.rmtree(outside, ignore_errors=True)

    def test_refuses_self_checkout_target(self) -> None:
        with self.assertRaises(InitError):
            init_mod._write_overlay(init_mod.REPO_ROOT, "team-profile.yaml", "status: active\n")


class IdempotencyTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory(prefix="agents-init-idempotent-")
        self.root = _make_project(Path(self.temporary.name))

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def test_structured_overlay_preserves_manually_edited_untouched_field(self) -> None:
        init_mod._write_overlay(
            self.root,
            "team-profile.yaml",
            "engineering:\n  primary_language: golang\nplatform:\n  hosting_model: self-hosted\n",
        )
        content, merged = build_structured_overlay(
            self.root, "team-profile.yaml", {"platform": {"hosting_model": "cloud"}}
        )
        self.assertEqual(merged["platform"]["hosting_model"], "cloud")
        # A field the current run's fragment never mentions survives untouched.
        self.assertEqual(merged["engineering"]["primary_language"], "golang")
        self.assertIn("primary_language: golang", content)

    def test_empty_fragment_is_a_pure_no_op(self) -> None:
        init_mod._write_overlay(self.root, "team-profile.yaml", "engineering:\n  primary_language: golang\n")
        result = build_structured_overlay(self.root, "team-profile.yaml", {})
        self.assertIsNotNone(result)
        _content, merged = result
        self.assertEqual(merged, {"engineering": {"primary_language": "golang"}})

    def test_platform_overlay_updates_only_touched_category(self) -> None:
        init_mod._write_overlay(
            self.root,
            "platform-impact-profile.yaml",
            "impact_categories:\n"
            "  - id: platform-phase\n"
            "    applicability: not-applicable\n"
            "    definition_reference: null\n"
            "    rationale: manually recorded\n"
            "    owner: null\n"
            "    evidence_refs: []\n",
        )
        content, merged = build_platform_overlay(
            self.root,
            {
                "impact_categories": {
                    "platform-component": {
                        "applicability": "applicable",
                        "definition_reference": "doc://x",
                        "owner": "Someone",
                    }
                }
            },
        )
        by_id = {e["id"]: e for e in merged["impact_categories"]}
        # Manually-recorded category from the prior run survives untouched.
        self.assertEqual(by_id["platform-phase"]["applicability"], "not-applicable")
        self.assertEqual(by_id["platform-phase"]["rationale"], "manually recorded")
        # Newly touched category is applied.
        self.assertEqual(by_id["platform-component"]["applicability"], "applicable")
        self.assertIn("platform-phase", content)

    def test_guardrails_managed_block_accumulates_bullets_across_runs(self) -> None:
        # Finding 1: a second run supplying a different bullet must not
        # silently discard the first run's already-accepted bullet.
        first_content, rejected_first = build_guardrails_overlay(
            self.root, ["Require MFA for all break-glass Proxmox accounts."]
        )
        self.assertFalse(rejected_first)
        init_mod._write_overlay(self.root, "cloud-guardrails.md", first_content)

        second_content, rejected_second = build_guardrails_overlay(
            self.root, ["Encrypt all backups with a project-specific key."]
        )
        self.assertFalse(rejected_second)
        self.assertIn("Require MFA for all break-glass Proxmox accounts.", second_content)
        self.assertIn("Encrypt all backups with a project-specific key.", second_content)

    def test_guardrails_managed_block_dedupes_exact_duplicate_bullet(self) -> None:
        first_content, _ = build_guardrails_overlay(self.root, ["Encrypt all backups."])
        init_mod._write_overlay(self.root, "cloud-guardrails.md", first_content)
        second_content, _ = build_guardrails_overlay(self.root, ["Encrypt all backups."])
        self.assertEqual(second_content.count("Encrypt all backups."), 1)

    def test_prose_addendum_managed_block_accumulates_entries_across_runs(self) -> None:
        # Finding 1: a second run supplying different addendum text must not
        # silently discard the first run's already-accepted addendum.
        first_content = build_prose_addendum_overlay(
            self.root, "technology-standards.md", "Adopt trunk-based development for all services."
        )
        self.assertIsNotNone(first_content)
        init_mod._write_overlay(self.root, "technology-standards.md", first_content)

        second_content = build_prose_addendum_overlay(
            self.root, "technology-standards.md", "Require signed commits on the default branch."
        )
        self.assertIn("Adopt trunk-based development for all services.", second_content)
        self.assertIn("Require signed commits on the default branch.", second_content)


class FailClosedTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory(prefix="agents-init-failclosed-")
        self.root = _make_project(Path(self.temporary.name))

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def test_invalid_autonomy_fragment_aborts_entire_run_with_no_writes(self) -> None:
        answers = {
            "schema_version": 1,
            "rg_a_stack": {"platform": {"hosting_model": "cloud"}},
            "rg_b_autonomy": {"mutations": {"production": "allowed"}},  # loosening: invalid
            "field_decisions": {
                "platform.hosting_model": {
                    "status": "overridden",
                    "category": "stack",
                    "source_value": "self-hosted",
                    "new_value": "cloud",
                },
                "mutations.production": {
                    "status": "overridden",
                    "category": "governance",
                    "source_value": "human_approval",
                    "new_value": "allowed",
                },
            },
        }
        result, errors = plan_writes(self.root, answers, ["rg-a-stack", "rg-b-governance"])
        self.assertTrue(errors)
        # No overlay may have been written to disk as a side effect of planning.
        self.assertFalse((self.root / ".agents").exists())

    def test_run_init_reports_failure_and_writes_nothing_on_disk(self) -> None:
        answers_path = self.root / "answers.yaml"
        answers_path.write_text(
            "schema_version: 1\n"
            "rg_b_autonomy:\n"
            "  mutations:\n"
            "    production: allowed\n"
            "field_decisions:\n"
            "  mutations.production:\n"
            "    status: overridden\n"
            "    category: governance\n"
            "    source_value: human_approval\n"
            "    new_value: allowed\n"
        )
        args = argparse.Namespace(
            target=self.root,
            stack=None,
            answers=answers_path,
            interactive=False,
            sections="rg-b-governance",
            dry_run=False,
            force=True,
            print_answers=False,
            set_values=None,
        )
        with tempfile.TemporaryDirectory(prefix="agents-init-audit-") as audit_dir:
            os.environ["AGENTS_INIT_AUDIT_LOG"] = str(Path(audit_dir) / "audit.jsonl")
            try:
                code = run_init(args)
            finally:
                del os.environ["AGENTS_INIT_AUDIT_LOG"]
        self.assertEqual(code, 1)
        self.assertFalse((self.root / ".agents").exists())


class FieldDecisionTrackingTests(unittest.TestCase):
    def test_accepts_all_three_outcomes(self) -> None:
        raw = {
            "a.b": {"status": "kept", "category": "stack", "source_value": 1},
            "c.d": {"status": "overridden", "category": "stack", "source_value": 1, "new_value": 2},
            "e.f": {"status": "deferred", "category": "governance", "source_value": "on_request"},
        }
        decisions = parse_field_decisions(raw)
        self.assertEqual({d.status for d in decisions.values()}, {"kept", "overridden", "deferred"})

    def test_rejects_unknown_status(self) -> None:
        with self.assertRaises(InitError):
            parse_field_decisions({"a.b": {"status": "ignored", "category": "stack"}})

    def test_rejects_unknown_category(self) -> None:
        with self.assertRaises(InitError):
            parse_field_decisions({"a.b": {"status": "kept", "category": "bogus"}})

    def test_require_field_decisions_cover_flags_missing_field(self) -> None:
        decisions = parse_field_decisions({"a.b": {"status": "kept", "category": "stack"}})
        with self.assertRaises(InitError):
            require_field_decisions_cover([("a.b", "stack"), ("c.d", "stack")], decisions)

    def test_require_field_decisions_cover_flags_category_mismatch(self) -> None:
        decisions = parse_field_decisions({"a.b": {"status": "kept", "category": "stack"}})
        with self.assertRaises(InitError):
            require_field_decisions_cover([("a.b", "governance")], decisions)

    def test_require_field_decisions_cover_passes_when_satisfied(self) -> None:
        decisions = parse_field_decisions({"a.b": {"status": "kept", "category": "stack"}})
        require_field_decisions_cover([("a.b", "stack")], decisions)  # must not raise

    def test_field_decisions_required_by_plan_writes_for_touched_fields(self) -> None:
        with tempfile.TemporaryDirectory(prefix="agents-init-a006-") as tmp:
            root = _make_project(Path(tmp))
            answers = {
                "schema_version": 1,
                "rg_a_stack": {"platform": {"hosting_model": "cloud"}},
                "field_decisions": {},  # missing entry for the touched field
            }
            _result, errors = plan_writes(root, answers, ["rg-a-stack"])
            self.assertTrue(any("A-006" in e for e in errors))


class AutonomyAllowlistTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory(prefix="agents-init-autonomy-")
        self.root = _make_project(Path(self.temporary.name))

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def test_allowed_choices_never_include_a_looser_rank(self) -> None:
        from resolve import _AUTONOMY_RESTRICTIVENESS_RANK, _autonomy_rank

        choices = init_mod.autonomy_allowed_choices("human_approval")
        default_rank = _autonomy_rank("x", "human_approval")
        for choice in choices:
            self.assertGreaterEqual(_AUTONOMY_RESTRICTIVENESS_RANK[choice], default_rank)

    def test_narrowing_within_allowlist_is_accepted(self) -> None:
        content, merged = build_autonomy_overlay(self.root, {"repository": {"commit": "human_approval"}})
        self.assertEqual(merged["repository"]["commit"], "human_approval")
        self.assertIn("human_approval", content)

    def test_loosening_outside_allowlist_is_rejected(self) -> None:
        with self.assertRaises(Exception):
            build_autonomy_overlay(self.root, {"repository": {"merge": "allowed"}})

    def test_fixed_keys_cannot_be_set(self) -> None:
        with self.assertRaises(InitError):
            build_autonomy_overlay(self.root, {"default_rule": "allow_unless_denied"})

    def test_fixed_keys_cannot_be_set_via_policy_version(self) -> None:
        with self.assertRaises(InitError):
            build_autonomy_overlay(self.root, {"policy_version": 2})

    def test_rejected_autonomy_value_message_is_redacted_to_a_hash(self) -> None:
        # Finding 2: the raw rejected value must never appear in the
        # exception's own message (which flows straight into plan_writes's
        # errors list and then stderr); only the field path and a hash.
        secret_looking_value = "token-abc123-allowed"
        with self.assertRaises(init_mod.AutonomyOverlayRejected) as ctx:
            build_autonomy_overlay(self.root, {"repository": {"merge": secret_looking_value}})
        message = str(ctx.exception)
        self.assertNotIn(secret_looking_value, message)
        self.assertIn("repository.merge", message)
        self.assertIn(ctx.exception.value_sha256, message)

    def test_rejected_autonomy_value_never_appears_verbatim_in_plan_writes_errors(self) -> None:
        secret_looking_value = "token-abc123-allowed"
        answers = {
            "schema_version": 1,
            "rg_b_autonomy": {"repository": {"merge": secret_looking_value}},
            "field_decisions": {
                "repository.merge": {
                    "status": "overridden",
                    "category": "governance",
                    "source_value": "never",
                    "new_value": secret_looking_value,
                }
            },
        }
        result, errors = plan_writes(self.root, answers, ["rg-b-governance"])
        self.assertTrue(errors)
        joined_errors = "\n".join(errors)
        self.assertNotIn(secret_looking_value, joined_errors)
        self.assertTrue(result.rejected_autonomy)
        # The raw value is only ever carried internally for hash-only audit
        # logging, never surfaced as a plain string anywhere else.
        for _field_path, value in result.rejected_autonomy:
            self.assertEqual(value, secret_looking_value)  # captured internally...
        self.assertNotIn(secret_looking_value, str(result.planned))

    def test_rejected_autonomy_value_never_printed_by_run_init(self) -> None:
        secret_looking_value = "token-abc123-allowed"
        answers_path = self.root / "answers.yaml"
        answers_path.write_text(
            "schema_version: 1\n"
            "rg_b_autonomy:\n"
            f"  repository:\n    merge: {secret_looking_value}\n"
            "field_decisions:\n"
            "  repository.merge:\n"
            "    status: overridden\n"
            "    category: governance\n"
            "    source_value: never\n"
            f"    new_value: {secret_looking_value}\n"
        )
        args = argparse.Namespace(
            target=self.root,
            stack=None,
            answers=answers_path,
            interactive=False,
            sections="rg-b-governance",
            dry_run=False,
            force=True,
            print_answers=False,
            set_values=None,
        )
        with tempfile.TemporaryDirectory(prefix="agents-init-autonomy-audit-") as audit_dir:
            audit_path = Path(audit_dir) / "audit.jsonl"
            os.environ["AGENTS_INIT_AUDIT_LOG"] = str(audit_path)
            try:
                import io
                import contextlib

                captured_stderr = io.StringIO()
                with contextlib.redirect_stderr(captured_stderr):
                    code = run_init(args)
            finally:
                del os.environ["AGENTS_INIT_AUDIT_LOG"]
            self.assertEqual(code, 1)
            self.assertNotIn(secret_looking_value, captured_stderr.getvalue())
            lines = [json.loads(line) for line in audit_path.read_text().splitlines() if line.strip()]
            rejected_entries = [e for e in lines if e["kind"] == "rejected" and "value_sha256" in e]
            self.assertTrue(rejected_entries)
            for entry in rejected_entries:
                self.assertNotIn(secret_looking_value, json.dumps(entry))


class GuardrailsDenylistTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory(prefix="agents-init-guardrails-")
        self.root = _make_project(Path(self.temporary.name))

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def test_accepts_genuinely_additive_bullet(self) -> None:
        self.assertIsNone(scan_guardrail_bullet("Require MFA for all break-glass Proxmox accounts."))

    def test_rejects_negation_phrasing_case_insensitively(self) -> None:
        for bullet in [
            "This project is exempt from the backup requirement.",
            "Our guardrail OVERRIDES THE ABOVE encryption rule.",
            "Use plaintext instead of TLS for internal traffic.",
            "This policy replaces the shared secrets baseline.",
            "The audit-logging rule does not apply here.",
        ]:
            with self.subTest(bullet=bullet):
                self.assertIsNotNone(scan_guardrail_bullet(bullet))

    def test_build_guardrails_overlay_separates_accepted_and_rejected(self) -> None:
        content, rejected = build_guardrails_overlay(
            self.root,
            ["Encrypt all backups with a project-specific key.", "This is exempt from network policy."],
        )
        self.assertEqual(len(rejected), 1)
        self.assertIn("Encrypt all backups", content)
        self.assertNotIn("exempt", content)

    def test_rejected_bullet_not_silently_dropped_surfaces_in_plan_errors(self) -> None:
        answers = {
            "schema_version": 1,
            "rg_b_guardrails_addendum": ["This rule is exempt from review."],
            "field_decisions": {},
        }
        _result, errors = plan_writes(self.root, answers, ["rg-b-governance"])
        self.assertTrue(any("rejected" in e for e in errors))


class AuditLogTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory(prefix="agents-init-audit-")
        self.root = Path(self.temporary.name) / "project"
        self.root.mkdir()
        _make_project(self.root)
        self.audit_dir = tempfile.TemporaryDirectory(prefix="agents-init-audit-log-")
        self.audit_path = Path(self.audit_dir.name) / "audit.jsonl"
        os.environ["AGENTS_INIT_AUDIT_LOG"] = str(self.audit_path)

    def tearDown(self) -> None:
        del os.environ["AGENTS_INIT_AUDIT_LOG"]
        self.temporary.cleanup()
        self.audit_dir.cleanup()

    def _run(self, answers_yaml: str, sections: str, force: bool) -> int:
        answers_path = self.root / "answers.yaml"
        answers_path.write_text(answers_yaml)
        args = argparse.Namespace(
            target=self.root,
            stack=None,
            answers=answers_path,
            interactive=False,
            sections=sections,
            dry_run=not force,
            force=force,
            print_answers=False,
            set_values=None,
        )
        return run_init(args)

    def test_audit_log_written_outside_target_tree(self) -> None:
        code = self._run(
            "schema_version: 1\n"
            "rg_a_stack:\n"
            "  platform:\n"
            "    hosting_model: cloud\n"
            "field_decisions:\n"
            "  platform.hosting_model:\n"
            "    status: overridden\n"
            "    category: stack\n"
            "    source_value: self-hosted\n"
            "    new_value: cloud\n",
            "rg-a-stack",
            force=True,
        )
        self.assertEqual(code, 0)
        self.assertTrue(self.audit_path.is_file())
        # The audit log itself must not land inside the target project tree.
        self.assertNotIn(str(self.root), str(self.audit_path.resolve()))

    def test_rejected_free_text_logged_as_hash_not_verbatim(self) -> None:
        secret_looking_bullet = "token-abc123 is exempt from rotation"
        self._run(
            "schema_version: 1\n"
            f'rg_b_guardrails_addendum: ["{secret_looking_bullet}"]\n'
            "field_decisions: {}\n",
            "rg-b-governance",
            force=True,
        )
        lines = [json.loads(line) for line in self.audit_path.read_text().splitlines() if line.strip()]
        rejected_entries = [e for e in lines if e["kind"] == "rejected" and "value_sha256" in e]
        self.assertTrue(rejected_entries)
        for entry in rejected_entries:
            self.assertNotIn(secret_looking_bullet, json.dumps(entry))
            self.assertNotIn("token-abc123", json.dumps(entry))

    def test_accepted_entries_logged_too(self) -> None:
        code = self._run(
            "schema_version: 1\n"
            "rg_a_stack:\n"
            "  platform:\n"
            "    hosting_model: cloud\n"
            "field_decisions:\n"
            "  platform.hosting_model:\n"
            "    status: overridden\n"
            "    category: stack\n"
            "    source_value: self-hosted\n"
            "    new_value: cloud\n",
            "rg-a-stack",
            force=False,
        )
        self.assertEqual(code, 0)
        lines = [json.loads(line) for line in self.audit_path.read_text().splitlines() if line.strip()]
        self.assertTrue(any(e["kind"] == "accepted" for e in lines))


class PlatformGuidedFillInTests(unittest.TestCase):
    def test_applicable_requires_definition_reference_and_owner(self) -> None:
        with self.assertRaises(InitError):
            validate_platform_fragment(
                {"impact_categories": {"platform-phase": {"applicability": "applicable", "owner": "me"}}}
            )
        with self.assertRaises(InitError):
            validate_platform_fragment(
                {
                    "impact_categories": {
                        "platform-phase": {"applicability": "applicable", "definition_reference": "doc://x"}
                    }
                }
            )

    def test_applicable_with_both_fields_is_accepted(self) -> None:
        validate_platform_fragment(
            {
                "impact_categories": {
                    "platform-phase": {
                        "applicability": "applicable",
                        "definition_reference": "doc://x",
                        "owner": "me",
                    }
                }
            }
        )  # must not raise

    def test_unknown_stays_valid_without_citation(self) -> None:
        validate_platform_fragment({"impact_categories": {"platform-phase": {"applicability": "unknown"}}})

    def test_not_applicable_stays_valid_without_citation(self) -> None:
        validate_platform_fragment({"impact_categories": {"platform-phase": {"applicability": "not-applicable"}}})

    def test_referencing_unknown_category_id_is_rejected(self) -> None:
        with tempfile.TemporaryDirectory(prefix="agents-init-platform-") as tmp:
            root = _make_project(Path(tmp))
            with self.assertRaises(InitError):
                build_platform_overlay(root, {"impact_categories": {"not-a-real-category": {"applicability": "unknown"}}})

    def test_invalid_applicability_value_is_rejected(self) -> None:
        # Finding 3: applicability must be one of exactly
        # applicable/not-applicable/unknown; a typo or "n/a" must not be
        # silently accepted.
        with self.assertRaises(InitError):
            validate_platform_fragment({"impact_categories": {"platform-phase": {"applicability": "aplicable"}}})
        with self.assertRaises(InitError):
            validate_platform_fragment({"impact_categories": {"platform-phase": {"applicability": "n/a"}}})


class TemplateImmutabilityTests(unittest.TestCase):
    def test_global_platform_template_file_is_never_modified(self) -> None:
        template_path = init_project_root = ROOT / "platform-impact-profile.yaml"
        original = template_path.read_text()
        with tempfile.TemporaryDirectory(prefix="agents-init-immutable-") as tmp:
            root = _make_project(Path(tmp))
            build_platform_overlay(
                root,
                {
                    "impact_categories": {
                        "platform-phase": {
                            "applicability": "applicable",
                            "definition_reference": "doc://x",
                            "owner": "me",
                        }
                    }
                },
            )
        self.assertEqual(template_path.read_text(), original)

    def test_overlay_never_adds_or_removes_categories(self) -> None:
        with tempfile.TemporaryDirectory(prefix="agents-init-immutable2-") as tmp:
            root = _make_project(Path(tmp))
            _content, merged = build_platform_overlay(
                root,
                {
                    "impact_categories": {
                        "platform-phase": {
                            "applicability": "applicable",
                            "definition_reference": "doc://x",
                            "owner": "me",
                        }
                    }
                },
            )
            from resolve import _load_structured

            base = _load_structured(ROOT / "platform-impact-profile.yaml")
            base_ids = [e["id"] for e in base["impact_categories"]]
            overlay_ids = [e["id"] for e in merged["impact_categories"]]
            self.assertEqual(set(overlay_ids), set(base_ids) & set(overlay_ids))
            self.assertTrue(set(overlay_ids).issubset(set(base_ids)))


class EndToEndDryRunAndForceTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory(prefix="agents-init-e2e-")
        self.root = _make_project(Path(self.temporary.name))
        self.audit_dir = tempfile.TemporaryDirectory(prefix="agents-init-e2e-audit-")
        os.environ["AGENTS_INIT_AUDIT_LOG"] = str(Path(self.audit_dir.name) / "audit.jsonl")

    def tearDown(self) -> None:
        del os.environ["AGENTS_INIT_AUDIT_LOG"]
        self.temporary.cleanup()
        self.audit_dir.cleanup()

    def _answers_path(self) -> Path:
        path = self.root / "answers.yaml"
        path.write_text(
            "schema_version: 1\n"
            "rg_a_stack:\n"
            "  platform:\n"
            "    hosting_model: cloud\n"
            "field_decisions:\n"
            "  platform.hosting_model:\n"
            "    status: overridden\n"
            "    category: stack\n"
            "    source_value: self-hosted\n"
            "    new_value: cloud\n"
        )
        return path

    def test_dry_run_default_writes_nothing(self) -> None:
        args = argparse.Namespace(
            target=self.root,
            stack=None,
            answers=self._answers_path(),
            interactive=False,
            sections=",".join(init_mod.ALL_SECTIONS),
            dry_run=False,
            force=False,
            print_answers=False,
            set_values=None,
        )
        code = run_init(args)
        self.assertEqual(code, 0)
        self.assertFalse((self.root / ".agents" / "shared" / "team-profile.yaml").exists())

    def test_force_actually_writes_and_resolves_correctly(self) -> None:
        args = argparse.Namespace(
            target=self.root,
            stack=None,
            answers=self._answers_path(),
            interactive=False,
            sections="rg-a-stack",
            dry_run=False,
            force=True,
            print_answers=False,
            set_values=None,
        )
        code = run_init(args)
        self.assertEqual(code, 0)
        written = self.root / ".agents" / "shared" / "team-profile.yaml"
        self.assertTrue(written.is_file())

        from resolve import resolve_shared_config

        resolved = resolve_shared_config("team-profile.yaml", start=self.root)
        self.assertEqual(resolved["platform"]["hosting_model"], "cloud")


class RepairModeTests(unittest.TestCase):
    """Explicit repair is an inspect-first wrapper around safe overlay merging."""

    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory(prefix="agents-init-repair-")
        self.root = _make_project(Path(self.temporary.name))
        self.audit = tempfile.TemporaryDirectory(prefix="agents-init-repair-audit-")
        os.environ["AGENTS_INIT_AUDIT_LOG"] = str(Path(self.audit.name) / "audit.jsonl")

    def tearDown(self) -> None:
        os.environ.pop("AGENTS_INIT_AUDIT_LOG", None)
        self.audit.cleanup()
        self.temporary.cleanup()

    def _args(self, **overrides: object) -> argparse.Namespace:
        base = dict(
            target=self.root,
            stack=None,
            answers=None,
            interactive=False,
            sections=",".join(init_mod.ALL_SECTIONS),
            dry_run=False,
            force=False,
            repair=True,
            apply=False,
            print_answers=False,
            set_values=None,
        )
        base.update(overrides)
        return argparse.Namespace(**base)

    def _answers(self) -> Path:
        path = self.root / "answers.yaml"
        path.write_text(
            "schema_version: 1\n"
            "rg_a_stack:\n"
            "  platform:\n"
            "    hosting_model: cloud\n"
            "field_decisions:\n"
            "  platform.hosting_model:\n"
            "    status: overridden\n"
            "    category: stack\n"
            "    source_value: self-hosted\n"
            "    new_value: cloud\n",
            encoding="utf-8",
        )
        return path

    def test_repair_check_treats_missing_sparse_overlays_as_current_defaults(self) -> None:
        stdout = io.StringIO()
        with contextlib.redirect_stdout(stdout):
            code = run_init(self._args())
        self.assertEqual(0, code)
        self.assertIn("missing_uses_shipped_default", stdout.getvalue())
        self.assertFalse((self.root / ".agents").exists())
        self.assertFalse(Path(os.environ["AGENTS_INIT_AUDIT_LOG"]).exists())

    def test_repair_never_merges_answers_or_rewrites_project_values(self) -> None:
        init_mod._write_overlay(
            self.root,
            "team-profile.yaml",
            "engineering:\n  primary_language: golang\n",
        )
        args = self._args()
        before = _tree_hash(self.root)
        self.assertEqual(0, run_init(args))
        self.assertEqual(before, _tree_hash(self.root))

        stderr = io.StringIO()
        with contextlib.redirect_stderr(stderr):
            self.assertEqual(2, run_init(self._args(answers=self._answers(), apply=True)))
        self.assertIn("does not accept", stderr.getvalue())
        merged = init_mod._load_structured(
            self.root / ".agents" / "shared" / "team-profile.yaml"
        )
        self.assertEqual("golang", merged["engineering"]["primary_language"])
        self.assertNotIn("platform", merged)

    def test_repair_fails_closed_on_an_invalid_existing_overlay(self) -> None:
        init_mod._write_overlay(
            self.root,
            "agent-autonomy.yaml",
            "mutations:\n  production: allowed\n",
        )
        before = _tree_hash(self.root)
        stderr = io.StringIO()
        with contextlib.redirect_stderr(stderr):
            code = run_init(self._args(apply=True))
        self.assertEqual(1, code)
        self.assertIn("repair blocked", stderr.getvalue())
        self.assertEqual(before, _tree_hash(self.root))

    def test_repair_rejects_dangling_symlink_and_reversed_managed_block(self) -> None:
        overlay_dir = self.root / ".agents" / "shared"
        overlay_dir.mkdir(parents=True)
        dangling = overlay_dir / "team-profile.yaml"
        dangling.symlink_to(self.root / "outside.yaml")
        stderr = io.StringIO()
        with contextlib.redirect_stderr(stderr):
            self.assertEqual(1, run_init(self._args()))
        self.assertIn("symlinked overlay", stderr.getvalue())

        dangling.unlink()
        (overlay_dir / "technology-standards.md").write_text(
            f"{init_mod.MANAGED_END}\ncontent\n{init_mod.MANAGED_START}\n", encoding="utf-8"
        )
        stderr = io.StringIO()
        with contextlib.redirect_stderr(stderr):
            self.assertEqual(1, run_init(self._args()))
        self.assertIn("incomplete or ambiguous", stderr.getvalue())

    def test_apply_requires_repair_and_is_an_inspection_only_alias(self) -> None:
        stderr = io.StringIO()
        with contextlib.redirect_stderr(stderr):
            self.assertEqual(2, run_init(self._args(repair=False, apply=True)))
        self.assertIn("valid only with --repair", stderr.getvalue())
        stderr = io.StringIO()
        with contextlib.redirect_stderr(stderr):
            self.assertEqual(2, run_init(self._args(force=True)))
        self.assertIn("does not accept", stderr.getvalue())


class PrintAnswersRedactionTests(unittest.TestCase):
    """Finding A: `--print-answers` must echo the answer set only AFTER
    plan_writes has validated it, and must redact rg_b_autonomy/
    rg_b_guardrails_addendum to their post-validation accepted/rejected
    status rather than the raw values."""

    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory(prefix="agents-init-printanswers-")
        self.root = _make_project(Path(self.temporary.name))

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def _run_with_captured_stdout(self, answers_yaml: str, sections: str) -> tuple[int, str]:
        answers_path = self.root / "answers.yaml"
        answers_path.write_text(answers_yaml)
        args = argparse.Namespace(
            target=self.root,
            stack=None,
            answers=answers_path,
            interactive=False,
            sections=sections,
            dry_run=False,
            force=True,
            print_answers=True,
            set_values=None,
        )
        with tempfile.TemporaryDirectory(prefix="agents-init-printanswers-audit-") as audit_dir:
            os.environ["AGENTS_INIT_AUDIT_LOG"] = str(Path(audit_dir) / "audit.jsonl")
            try:
                captured_stdout = io.StringIO()
                with contextlib.redirect_stdout(captured_stdout):
                    code = run_init(args)
            finally:
                del os.environ["AGENTS_INIT_AUDIT_LOG"]
        return code, captured_stdout.getvalue()

    def test_print_answers_never_shows_a_rejected_autonomy_raw_value(self) -> None:
        # This deliberately keeps the rejected value out of
        # field_decisions.new_value so this test isolates the rg_b_autonomy
        # redaction under test; the field_decisions-echo case (finding D) is
        # covered separately below.
        secret_looking_value = "token-abc123-allowed"
        code, output = self._run_with_captured_stdout(
            "schema_version: 1\n"
            "rg_b_autonomy:\n"
            f"  repository:\n    merge: {secret_looking_value}\n"
            "field_decisions:\n"
            "  repository.merge:\n"
            "    status: overridden\n"
            "    category: governance\n"
            "    source_value: never\n"
            "    new_value: (see audit log)\n",
            "rg-b-governance",
        )
        self.assertEqual(code, 1)
        self.assertNotIn(secret_looking_value, output)
        self.assertIn("repository.merge", output)
        self.assertIn("rejected", output)

    def test_print_answers_shows_accepted_autonomy_field_as_accepted_hash_only(self) -> None:
        # Even an accepted field must never be echoed as its raw value —
        # only field path, "accepted", and a hash.
        secret_looking_source = "human_approval"
        code, output = self._run_with_captured_stdout(
            "schema_version: 1\n"
            "rg_b_autonomy:\n"
            f"  repository:\n    commit: {secret_looking_source}\n"
            "field_decisions:\n"
            "  repository.commit:\n"
            "    status: overridden\n"
            "    category: governance\n"
            "    source_value: on_request\n"
            f"    new_value: {secret_looking_source}\n",
            "rg-b-governance",
        )
        self.assertEqual(code, 0)
        self.assertIn("repository.commit", output)
        self.assertIn("accepted", output)
        # The raw permission value itself never appears in the echoed
        # answer set (rg_b_autonomy or field_decisions, finding D); it
        # legitimately does appear later in the diff preview of the file
        # actually being written, which is a separate, intentional feature
        # (an accepted value is real file content, previewed for review) and
        # out of scope for this redaction. `--print-answers` echoes before
        # any write preview, so everything up to the first preview marker is
        # the echoed answer set under test.
        echoed_answers = output.split("--- ", 1)[0]
        self.assertNotIn(secret_looking_source, echoed_answers)

    def test_print_answers_redacts_rejected_guardrail_bullet_to_hash(self) -> None:
        secret_looking_bullet = "token-abc123 is exempt from rotation"
        code, output = self._run_with_captured_stdout(
            "schema_version: 1\n"
            f'rg_b_guardrails_addendum: ["{secret_looking_bullet}"]\n'
            "field_decisions: {}\n",
            "rg-b-governance",
        )
        self.assertEqual(code, 1)
        self.assertNotIn(secret_looking_bullet, output)
        self.assertNotIn("token-abc123", output)
        self.assertIn("rejected", output)

    def test_print_answers_shows_accepted_guardrail_bullet_verbatim(self) -> None:
        bullet = "Encrypt all backups with a project-specific key."
        code, output = self._run_with_captured_stdout(
            "schema_version: 1\n"
            f'rg_b_guardrails_addendum: ["{bullet}"]\n'
            "field_decisions: {}\n",
            "rg-b-governance",
        )
        self.assertEqual(code, 0)
        self.assertIn(bullet, output)

    def test_print_answers_redacts_governance_field_decision_new_value(self) -> None:
        # Finding D: an ordinary answer file naturally repeats the same raw
        # value in both rg_b_autonomy (to attempt the change) and
        # field_decisions[<path>].new_value (to satisfy A-006 rev 2's
        # decision-coverage requirement). Both copies must be redacted.
        secret_looking_value = "human_approval_bypass_token"
        code, output = self._run_with_captured_stdout(
            "schema_version: 1\n"
            "rg_b_autonomy:\n"
            f"  repository:\n    merge: {secret_looking_value}\n"
            "field_decisions:\n"
            "  repository.merge:\n"
            "    status: overridden\n"
            "    category: governance\n"
            "    source_value: never\n"
            f"    new_value: {secret_looking_value}\n",
            "rg-b-governance",
        )
        self.assertEqual(code, 1)
        self.assertNotIn(secret_looking_value, output)
        self.assertIn("repository.merge", output)
        self.assertIn("rejected", output)

    def test_print_answers_redacts_mislabeled_governance_field_decision_by_ground_truth(self) -> None:
        # Round-4 reproduction: `repository.merge` is a real agent-
        # autonomy.yaml leaf touched via `rg_b_autonomy` with an attempted
        # loosening value, but its `field_decisions[...].category` is
        # (incorrectly) declared "stack" rather than "governance".
        # `require_field_decisions_cover`'s category-mismatch check will
        # eventually flag this as an error, but that must not be what
        # prevents the raw value from leaking through `--print-answers`:
        # ground truth (this path is a real governance leaf, independent of
        # what the answer file claims about its own category) must catch it
        # regardless of the mislabel.
        secret_looking_value = "token-abc123-allowed"
        code, output = self._run_with_captured_stdout(
            "schema_version: 1\n"
            "rg_b_autonomy:\n"
            f"  repository:\n    merge: {secret_looking_value}\n"
            "field_decisions:\n"
            "  repository.merge:\n"
            "    status: overridden\n"
            "    category: stack\n"  # mislabeled: this is a governance path
            "    source_value: never\n"
            f"    new_value: {secret_looking_value}\n",
            "rg-b-governance",
        )
        self.assertEqual(code, 1)
        self.assertNotIn(secret_looking_value, output)
        self.assertIn("repository.merge", output)

    def test_print_answers_does_not_redact_stack_field_decision_new_value(self) -> None:
        # Stack-category field_decisions entries are not governance-
        # restricted and describe a legitimate operator's own stack choices,
        # so they must remain visible, unlike governance-category entries.
        code, output = self._run_with_captured_stdout(
            "schema_version: 1\n"
            "rg_a_stack:\n"
            "  platform:\n    hosting_model: cloud\n"
            "field_decisions:\n"
            "  platform.hosting_model:\n"
            "    status: overridden\n"
            "    category: stack\n"
            "    source_value: self-hosted\n"
            "    new_value: cloud\n",
            "rg-a-stack",
        )
        self.assertEqual(code, 0)
        self.assertIn("new_value: cloud", output)


class WriteChokepointTOCTOUTests(unittest.TestCase):
    """Finding B: the write chokepoint must resolve target_root exactly
    once and use that SAME resolved identity for both the self-checkout
    check and the actual write destination, closing the gap where an
    earlier check-time resolve() and a later, separate use-time resolve()
    could diverge (e.g. via a symlink swapped in between calls)."""

    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory(prefix="agents-init-toctou-")
        self.root = _make_project(Path(self.temporary.name))

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def test_diverging_resolve_calls_cannot_bypass_the_self_checkout_check(self) -> None:
        target_root = self.root
        real_resolve = Path.resolve
        call_count = {"n": 0}
        repo_root_resolved = real_resolve(init_mod.REPO_ROOT)

        def fake_resolve(self_path: Path, strict: bool = False) -> Path:
            if self_path is target_root:
                call_count["n"] += 1
                if call_count["n"] == 1:
                    # "Checked" identity: reports this suite's own checkout.
                    return repo_root_resolved
                # A hypothetical SECOND, independent resolve() of
                # target_root would see a harmless, unrelated identity —
                # exactly the divergence a TOCTOU attacker needs. This
                # branch must never be exercised by the fixed code.
                return real_resolve(self_path, strict=strict)
            return real_resolve(self_path, strict=strict)

        with mock.patch.object(Path, "resolve", fake_resolve):
            with self.assertRaises(InitError):
                init_mod._write_overlay(target_root, "team-profile.yaml", "status: active\n")

        self.assertEqual(
            call_count["n"],
            1,
            "the write chokepoint must resolve target_root exactly once, not twice "
            "(finding B): a second, independent resolve() call is exactly the TOCTOU "
            "gap that must be closed",
        )
        self.assertFalse((target_root / ".agents").exists())

    def test_ordinary_write_still_succeeds_with_only_one_resolve_call(self) -> None:
        # Sanity check alongside the divergence test above: the single-
        # resolve refactor must not break the ordinary, non-adversarial path.
        target_root = self.root
        real_resolve = Path.resolve
        call_count = {"n": 0}

        def counting_resolve(self_path: Path, strict: bool = False) -> Path:
            if self_path is target_root:
                call_count["n"] += 1
            return real_resolve(self_path, strict=strict)

        with mock.patch.object(Path, "resolve", counting_resolve):
            dest = init_mod._write_overlay(target_root, "team-profile.yaml", "status: active\n")
        self.assertEqual(call_count["n"], 1)
        self.assertEqual(dest.read_text(), "status: active\n")


class SecondAutonomyValidationRedactionTests(unittest.TestCase):
    """Finding C: the second, independent autonomy validation call site
    (`validate_overlay_content` -> `resolve_shared_config` ->
    `_check_autonomy_overlay` again) must be wrapped in the same
    AutonomyOverlayRejected redaction as the first call site, so a raw
    value can never leak through it even if it ever diverges from the
    first check and rejects content the first check already accepted."""

    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory(prefix="agents-init-secondcheck-")
        self.root = _make_project(Path(self.temporary.name))

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def test_second_check_failure_is_redacted_not_leaked(self) -> None:
        secret_looking_value = "s3cr3t-token-99"
        fragment = {"repository": {"commit": secret_looking_value}}
        merged = {"repository": {"commit": secret_looking_value}}
        content = f"repository:\n  commit: {secret_looking_value}\n"

        def always_failing_check(base: dict, overlay: dict) -> None:
            # Simulates the second, independent validation call site
            # diverging from the first and rejecting content the first
            # check already accepted.
            raise resolve_mod.OverlayError(f"diverging rejection: {secret_looking_value}")

        with mock.patch.object(resolve_mod, "_check_autonomy_overlay", always_failing_check):
            with self.assertRaises(init_mod.AutonomyOverlayRejected) as ctx:
                validate_autonomy_overlay_content(content, fragment, merged)

        message = str(ctx.exception)
        self.assertNotIn(secret_looking_value, message)
        self.assertIn(ctx.exception.value_sha256, message)

    def test_plan_writes_does_not_leak_raw_value_when_second_check_diverges(self) -> None:
        secret_looking_value = "s3cr3t-token-77"
        answers = {
            "schema_version": 1,
            # A value that legitimately passes the FIRST check (a valid
            # narrowing of the default), so only the mocked SECOND check
            # (below) is what fails, proving this call site's own
            # redaction — not the first call site's — is what's under test.
            "rg_b_autonomy": {"repository": {"commit": "human_approval"}},
            "field_decisions": {
                "repository.commit": {
                    "status": "overridden",
                    "category": "governance",
                    "source_value": "on_request",
                    "new_value": "human_approval",
                }
            },
        }

        def always_failing_check(base: dict, overlay: dict) -> None:
            raise resolve_mod.OverlayError(f"diverging rejection: {secret_looking_value}")

        with mock.patch.object(resolve_mod, "_check_autonomy_overlay", always_failing_check):
            result, errors = plan_writes(self.root, answers, ["rg-b-governance"])

        self.assertTrue(errors)
        joined_errors = "\n".join(errors)
        self.assertNotIn(secret_looking_value, joined_errors)
        self.assertTrue(result.rejected_autonomy)
        self.assertFalse((self.root / ".agents").exists())


class InteractivePresetThreadingTests(unittest.TestCase):
    """Finding 1: `--stack <preset> --interactive` must show the preset's
    values as prompt DEFAULTS (never silently discard them), let the
    operator's actual input override any single field while leaving every
    other preset-seeded field intact, and echo the real preset id in
    `stack_preset` rather than hardcoding `None`."""

    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory(prefix="agents-init-interactive-preset-")
        self.root = _make_project(Path(self.temporary.name))
        self.preset_id = "golang-postgres-k8s"
        self.preset = init_mod.load_stack_preset(self.preset_id)
        self.leaf_paths = init_mod._leaf_paths(
            init_mod._load_structured(init_mod.SHARED_DEFAULTS_DIR / "team-profile.yaml")
        )

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def _grouped_leaf_paths(self) -> dict[str, list[str]]:
        """Same grouping the collector applies, in the same order: groups in
        order of first appearance, leaves in `_leaf_paths` order within each."""
        grouped: dict[str, list[str]] = {}
        for path in self.leaf_paths:
            grouped.setdefault(path.split(".")[0], []).append(path)
        return grouped

    def _scripted_input(
        self, overrides: dict[str, str], accept_groups: bool = True
    ) -> tuple[object, io.StringIO]:
        """Returns an input_func that accepts every group gate (so each leaf is
        actually prompted), answers blank (keep shown default) for every
        leaf-field prompt except `overrides` (path -> typed answer), then one
        trailing blank for the technology-standards.md addendum prompt. Also
        captures everything written to stdout so a test can assert on the
        shown defaults."""
        answers: list[str] = []
        for _group, paths in self._grouped_leaf_paths().items():
            answers.append("y" if accept_groups else "")
            if accept_groups:
                answers.extend(overrides.get(path, "") for path in paths)
        answers.append("")  # addendum prompt
        captured = io.StringIO()

        def input_func(_prompt_text: str) -> str:
            return answers.pop(0)

        return input_func, captured

    def _run(self, overrides: dict[str, str], accept_groups: bool = True) -> tuple[dict, str]:
        input_func, _ = self._scripted_input(overrides, accept_groups=accept_groups)
        stdout = io.StringIO()
        with contextlib.redirect_stdout(stdout):
            answers = run_interactive_flow(
                target_root=self.root,
                sections=["rg-a-stack"],
                preset=self.preset,
                input_func=input_func,
                preset_id=self.preset_id,
            )
        return answers, stdout.getvalue()

    def test_preset_values_are_shown_as_defaults(self) -> None:
        _answers, output = self._run({})
        self.assertIn("backend.database [default: 'postgresql']", output)
        self.assertIn("platform.orchestration [default: 'kubernetes']", output)
        self.assertIn(f"--stack preset {self.preset_id!r}", output)

    def test_accepting_all_defaults_matches_the_preset(self) -> None:
        answers, _output = self._run({})
        self.assertEqual(answers["rg_a_stack"], self.preset["rg_a_stack"])

    def test_overriding_one_field_changes_only_that_field(self) -> None:
        answers, _output = self._run({"backend.database": "cockroachdb"})
        stack = answers["rg_a_stack"]
        self.assertEqual(stack["backend"]["database"], "cockroachdb")
        # Everything else the preset seeded must survive untouched.
        self.assertEqual(stack["backend"]["primary_language"], "golang")
        self.assertEqual(stack["platform"]["orchestration"], "kubernetes")
        self.assertEqual(stack["platform"]["package_deployment"], "helm")

    def test_stack_preset_field_reflects_actual_preset_id(self) -> None:
        answers, _output = self._run({})
        self.assertEqual(answers["stack_preset"], self.preset_id)


class GuardrailsBuilderFailClosedTests(unittest.TestCase):
    """Finding 2: `build_guardrails_overlay` itself must be wrapped in the
    same try/except pattern as every other section in `plan_writes`, so a
    corrupt/unreadable existing overlay file raises a clean error entry
    instead of an uncaught, unredacted traceback."""

    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory(prefix="agents-init-guardrails-failclosed-")
        self.root = _make_project(Path(self.temporary.name))

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def test_unreadable_existing_guardrails_overlay_is_a_clean_plan_writes_error(self) -> None:
        answers = {
            "schema_version": 1,
            "rg_b_guardrails_addendum": ["Encrypt all backups with a project-specific key."],
            "field_decisions": {},
        }
        with mock.patch.object(
            init_mod, "_read_existing_overlay_text", side_effect=OSError("simulated corrupt overlay read")
        ):
            result, errors = plan_writes(self.root, answers, ["rg-b-governance"])
        self.assertTrue(errors)
        self.assertTrue(any(init_mod.GUARDRAILS_FILENAME in e for e in errors))
        self.assertFalse(result.planned)


class PartialWriteAuditTrailTests(unittest.TestCase):
    """Finding 3: a post-write verification failure partway through a
    multi-file run must not discard the audit trail for files that were
    genuinely already written (or for the failure itself) before it."""

    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory(prefix="agents-init-partial-audit-")
        self.root = _make_project(Path(self.temporary.name))
        self.audit_dir = tempfile.TemporaryDirectory(prefix="agents-init-partial-audit-log-")
        self.audit_path = Path(self.audit_dir.name) / "audit.jsonl"
        os.environ["AGENTS_INIT_AUDIT_LOG"] = str(self.audit_path)

    def tearDown(self) -> None:
        del os.environ["AGENTS_INIT_AUDIT_LOG"]
        self.temporary.cleanup()
        self.audit_dir.cleanup()

    def test_audit_log_retains_prior_success_and_the_failure_on_mid_loop_error(self) -> None:
        answers_path = self.root / "answers.yaml"
        answers_path.write_text(
            "schema_version: 1\n"
            "rg_a_stack:\n"
            "  platform:\n"
            "    hosting_model: cloud\n"
            "rg_b_guardrails_addendum: [\"Encrypt all backups with a project-specific key.\"]\n"
            "field_decisions:\n"
            "  platform.hosting_model:\n"
            "    status: overridden\n"
            "    category: stack\n"
            "    source_value: self-hosted\n"
            "    new_value: cloud\n"
        )
        args = argparse.Namespace(
            target=self.root,
            stack=None,
            answers=answers_path,
            interactive=False,
            sections="rg-a-stack,rg-b-governance",
            dry_run=False,
            force=True,
            print_answers=False,
            set_values=None,
        )

        real_write_overlay = init_mod._write_overlay
        call_count = {"n": 0}

        def flaky_write_overlay(target_root: Path, filename: str, content: str) -> Path:
            call_count["n"] += 1
            dest = real_write_overlay(target_root, filename, content)
            if call_count["n"] == 2:
                # Simulate the second of the (at least two) planned writes
                # failing its post-write verification by corrupting what was
                # just written, out from under the caller.
                dest.write_text(content + "\ncorrupted-after-write\n", encoding="utf-8")
            return dest

        with mock.patch.object(init_mod, "_write_overlay", flaky_write_overlay):
            with self.assertRaises(InitError):
                run_init(args)

        self.assertGreaterEqual(call_count["n"], 2)
        lines = [json.loads(line) for line in self.audit_path.read_text().splitlines() if line.strip()]
        self.assertTrue(
            any(e["kind"] == "written" for e in lines),
            "the first file's successful write must still be flushed to the audit log",
        )
        self.assertTrue(
            any(e["kind"] == "rejected" and "post-write verification failed" in e.get("detail", "") for e in lines),
            "the mid-loop failure itself must be flushed to the audit log",
        )


class DefaultsModeTests(unittest.TestCase):
    """`cadre init --target X` with no answer source is a legal run that keeps
    every shipped default. Overlays are sparse, so "keep the default" means
    "write no overlay for that field" -- a defaults-only run therefore plans
    no writes at all rather than materializing the defaults into the project."""

    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory(prefix="agents-init-defaults-")
        self.root = _make_project(Path(self.temporary.name))
        self.audit = tempfile.TemporaryDirectory(prefix="agents-init-defaults-audit-")
        os.environ["AGENTS_INIT_AUDIT_LOG"] = str(Path(self.audit.name) / "audit.jsonl")

    def tearDown(self) -> None:
        os.environ.pop("AGENTS_INIT_AUDIT_LOG", None)
        self.audit.cleanup()
        self.temporary.cleanup()

    def _args(self, **overrides: object) -> argparse.Namespace:
        base = dict(
            target=self.root,
            stack=None,
            answers=None,
            interactive=False,
            set_values=None,
            sections=",".join(init_mod.ALL_SECTIONS),
            dry_run=False,
            force=True,
            print_answers=False,
        )
        base.update(overrides)
        return argparse.Namespace(**base)

    def _run(self, **overrides: object) -> tuple[int, str, str]:
        stdout, stderr = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
            code = run_init(self._args(**overrides))
        return code, stdout.getvalue(), stderr.getvalue()

    def test_no_answer_source_succeeds_and_writes_nothing(self) -> None:
        code, out, _err = self._run()
        self.assertEqual(code, 0)
        self.assertFalse((self.root / ".agents").exists())
        self.assertIn("keeps every shipped default", out)

    def test_defaults_run_still_resolves_to_the_shipped_values(self) -> None:
        self._run()
        resolved = resolve_mod.resolve_shared_config(
            init_mod.TEAM_PROFILE_FILENAME, start=self.root
        )
        shipped = init_mod._load_structured(
            init_mod.SHARED_DEFAULTS_DIR / init_mod.TEAM_PROFILE_FILENAME
        )
        self.assertEqual(resolved, shipped)

    def test_preset_alone_needs_no_answer_file(self) -> None:
        """A `--stack` preset supplies RG-A leaves that `require_field_decisions_cover`
        demands a decision for; defaults mode synthesizes those rather than
        making the operator hand-author an answer file just to use a preset."""
        code, _out, err = self._run(stack="golang-postgres-k8s")
        self.assertEqual(code, 0, err)
        written = resolve_mod.resolve_shared_config(
            init_mod.TEAM_PROFILE_FILENAME, start=self.root
        )
        self.assertEqual(written["backend"]["database"], "postgresql")
        self.assertEqual(written["platform"]["orchestration"], "kubernetes")

    def test_answers_mode_still_fails_closed_on_a_missing_decision(self) -> None:
        """Synthesis is defaults-mode only: a hand-authored answer file keeps
        the A-006 rev 2 contract that every supplied field carries a decision."""
        answers_path = self.root / "answers.yaml"
        answers_path.write_text(
            "schema_version: 1\nrg_a_stack:\n  platform:\n    hosting_model: cloud\n"
            "field_decisions: {}\n"
        )
        code, _out, err = self._run(answers=answers_path)
        self.assertEqual(code, 1)
        self.assertIn("field_decisions is missing an entry", err)


class SetOverrideTests(unittest.TestCase):
    """`--set` is the flag-driven middle ground between "all shipped defaults"
    and a full answer file / prompt flow."""

    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory(prefix="agents-init-set-")
        self.root = _make_project(Path(self.temporary.name))
        self.audit = tempfile.TemporaryDirectory(prefix="agents-init-set-audit-")
        os.environ["AGENTS_INIT_AUDIT_LOG"] = str(Path(self.audit.name) / "audit.jsonl")

    def tearDown(self) -> None:
        os.environ.pop("AGENTS_INIT_AUDIT_LOG", None)
        self.audit.cleanup()
        self.temporary.cleanup()

    def _run(self, set_values: list[str], **overrides: object) -> tuple[int, str, str]:
        base = dict(
            target=self.root,
            stack=None,
            answers=None,
            interactive=False,
            set_values=set_values,
            sections=",".join(init_mod.ALL_SECTIONS),
            dry_run=False,
            force=True,
            print_answers=False,
        )
        base.update(overrides)
        stdout, stderr = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
            code = run_init(argparse.Namespace(**base))
        return code, stdout.getvalue(), stderr.getvalue()

    # -- region derivation ------------------------------------------------

    def test_derives_region_from_the_shipped_defaults(self) -> None:
        self.assertEqual(init_mod.resolve_set_region("platform.hosting_model", None), "stack")
        self.assertEqual(init_mod.resolve_set_region("mutations.production", None), "autonomy")
        self.assertEqual(
            init_mod.resolve_set_region("impact_categories.platform-phase.applicability", None),
            "platform",
        )

    def test_rejects_a_field_no_shipped_default_defines(self) -> None:
        with self.assertRaises(InitError) as caught:
            init_mod.resolve_set_region("no.such.field", None)
        self.assertIn("no shipped default", str(caught.exception))

    def test_rejects_an_ambiguous_path_instead_of_guessing(self) -> None:
        """`policy_version` exists in both library-standards.yaml and
        agent-autonomy.yaml. Guessing could file a governance field under the
        `stack` category, so this fails closed and asks for a qualifier."""
        with self.assertRaises(InitError) as caught:
            init_mod.resolve_set_region("policy_version", None)
        self.assertIn("ambiguous", str(caught.exception))
        self.assertEqual(init_mod.resolve_set_region("policy_version", "autonomy"), "autonomy")

    def test_rejects_malformed_arguments(self) -> None:
        for raw in ("platform.hosting_model", "=cloud", "nope:x=1"):
            with self.assertRaises(InitError):
                region, path, _value = init_mod.parse_set_override(raw)
                init_mod.resolve_set_region(path, region)

    # -- application ------------------------------------------------------

    def test_sets_a_stack_field(self) -> None:
        code, _out, err = self._run(["platform.hosting_model=cloud"])
        self.assertEqual(code, 0, err)
        resolved = resolve_mod.resolve_shared_config(
            init_mod.TEAM_PROFILE_FILENAME, start=self.root
        )
        self.assertEqual(resolved["platform"]["hosting_model"], "cloud")

    def test_sets_a_platform_entry_field(self) -> None:
        code, _out, err = self._run(
            [
                "platform:impact_categories.platform-phase.applicability=not-applicable",
                "platform:impact_categories.platform-phase.rationale=No platform phase here",
            ]
        )
        self.assertEqual(code, 0, err)
        resolved = resolve_mod.resolve_shared_config(
            init_mod.PLATFORM_FILENAME, start=self.root
        )
        entry = next(e for e in resolved["impact_categories"] if e["id"] == "platform-phase")
        self.assertEqual(entry["applicability"], "not-applicable")

    def test_set_wins_over_a_stack_preset(self) -> None:
        code, _out, err = self._run(
            ["backend.database=cockroachdb"], stack="golang-postgres-k8s"
        )
        self.assertEqual(code, 0, err)
        resolved = resolve_mod.resolve_shared_config(
            init_mod.TEAM_PROFILE_FILENAME, start=self.root
        )
        self.assertEqual(resolved["backend"]["database"], "cockroachdb")
        # The rest of the preset must survive.
        self.assertEqual(resolved["platform"]["orchestration"], "kubernetes")

    # -- governance safety ------------------------------------------------

    def test_category_is_derived_not_operator_supplied(self) -> None:
        """A `--set` on an autonomy field must be filed as `governance` no
        matter how it was written, because that label drives --print-answers
        redaction (B-005)."""
        answers = init_mod.apply_set_overrides(
            {"schema_version": 1, "field_decisions": {}},
            ["mutations.production=human_approval"],
            list(init_mod.ALL_SECTIONS),
        )
        self.assertEqual(
            answers["field_decisions"]["mutations.production"]["category"], "governance"
        )

    def test_cannot_loosen_autonomy_and_never_echoes_the_value(self) -> None:
        code, out, err = self._run(["autonomy:mutations.production=allowed"])
        self.assertEqual(code, 1)
        self.assertFalse((self.root / ".agents").exists())
        self.assertNotIn("allowed", out + err)

    def test_governance_value_is_redacted_in_print_answers(self) -> None:
        code, out, err = self._run(
            ["autonomy:mutations.production=human_approval"], print_answers=True, dry_run=True
        )
        self.assertEqual(code, 0, err)
        self.assertIn("category: governance", out)
        # The raw value only ever reaches the overlay/audit log, never stdout.
        head = out.split("--- would write")[0]
        self.assertNotIn("human_approval", head)

    def test_refuses_a_set_targeting_an_excluded_section(self) -> None:
        code, _out, err = self._run(
            ["autonomy:mutations.production=human_approval"], sections="rg-a-stack"
        )
        self.assertEqual(code, 1)
        self.assertIn("excluded by --sections", err)

    def test_set_and_interactive_are_mutually_exclusive(self) -> None:
        code, _out, err = self._run(["platform.hosting_model=cloud"], interactive=True)
        self.assertEqual(code, 2)
        self.assertIn("mutually exclusive", err)


class InteractiveGroupGatingTests(unittest.TestCase):
    """The prompt flow gates each group of fields behind one question, so an
    operator answers ~1 question per group instead of one per leaf field."""

    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory(prefix="agents-init-grouping-")
        self.root = _make_project(Path(self.temporary.name))

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def test_declining_every_group_asks_one_question_per_group(self) -> None:
        base = init_mod._load_structured(
            init_mod.SHARED_DEFAULTS_DIR / init_mod.TEAM_PROFILE_FILENAME
        )
        leaves = init_mod._leaf_paths(base)
        groups = {path.split(".")[0] for path in leaves}
        asked: list[str] = []

        def input_func(_prompt_text: str) -> str:
            asked.append("")
            return ""

        with contextlib.redirect_stdout(io.StringIO()):
            answers = run_interactive_flow(
                target_root=self.root,
                sections=["rg-a-stack"],
                preset=None,
                input_func=input_func,
            )
        # One question per group, plus the trailing addendum prompt -- not one
        # per leaf field.
        self.assertEqual(len(asked), len(groups) + 1)
        self.assertLess(len(asked), len(leaves))
        self.assertEqual(answers["rg_a_stack"], {})

    def test_declined_groups_are_recorded_as_kept(self) -> None:
        """A declined group writes nothing, but still records a decision, so a
        --print-answers echo replayed through --answers reproduces the run."""
        with contextlib.redirect_stdout(io.StringIO()):
            answers = run_interactive_flow(
                target_root=self.root,
                sections=["rg-a-stack"],
                preset=None,
                input_func=lambda _prompt: "",
            )
        base = init_mod._load_structured(
            init_mod.SHARED_DEFAULTS_DIR / init_mod.TEAM_PROFILE_FILENAME
        )
        for path in init_mod._leaf_paths(base):
            self.assertEqual(answers["field_decisions"][path]["status"], "kept")

    def test_declining_a_governance_group_keeps_the_restrictive_default(self) -> None:
        with contextlib.redirect_stdout(io.StringIO()):
            answers = run_interactive_flow(
                target_root=self.root,
                sections=["rg-b-governance"],
                preset=None,
                input_func=lambda _prompt: "",
            )
        # Nothing to write means nothing can be loosened.
        self.assertEqual(answers["rg_b_autonomy"], {})
        decisions = answers["field_decisions"]
        self.assertTrue(decisions)
        for entry in decisions.values():
            self.assertEqual(entry["status"], "kept")
            self.assertEqual(entry["category"], "governance")


class PlatformOverlayCompletenessTests(unittest.TestCase):
    """`resolve.py` replaces lists wholesale rather than merging them by id, so
    a platform overlay listing only the entries a run touched deletes every
    other category/BOM from the resolved profile — including the `unknown`
    ones that are supposed to block gates."""

    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory(prefix="agents-init-platform-")
        self.root = _make_project(Path(self.temporary.name))
        self.audit = tempfile.TemporaryDirectory(prefix="agents-init-platform-audit-")
        os.environ["AGENTS_INIT_AUDIT_LOG"] = str(Path(self.audit.name) / "audit.jsonl")
        self.shipped = init_mod._load_structured(
            init_mod.SHARED_DEFAULTS_DIR / init_mod.PLATFORM_FILENAME
        )

    def tearDown(self) -> None:
        os.environ.pop("AGENTS_INIT_AUDIT_LOG", None)
        self.audit.cleanup()
        self.temporary.cleanup()

    def _run(self, set_values: list[str]) -> int:
        args = argparse.Namespace(
            target=self.root,
            stack=None,
            answers=None,
            interactive=False,
            set_values=set_values,
            sections=",".join(init_mod.ALL_SECTIONS),
            dry_run=False,
            force=True,
            print_answers=False,
        )
        with contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
            return run_init(args)

    def test_overriding_one_entry_keeps_every_other_entry(self) -> None:
        target = self.shipped["impact_categories"][0]["id"]
        self._run([f"platform:impact_categories.{target}.applicability=not-applicable"])
        resolved = resolve_mod.resolve_shared_config(
            init_mod.PLATFORM_FILENAME, start=self.root
        )
        self.assertEqual(
            [entry["id"] for entry in resolved["impact_categories"]],
            [entry["id"] for entry in self.shipped["impact_categories"]],
        )
        by_id = {entry["id"]: entry for entry in resolved["impact_categories"]}
        self.assertEqual(by_id[target]["applicability"], "not-applicable")
        # Every entry the run did not touch keeps its blocking `unknown`.
        for entry in self.shipped["impact_categories"]:
            if entry["id"] != target:
                self.assertEqual(by_id[entry["id"]]["applicability"], "unknown")

    def test_untouched_section_is_preserved_too(self) -> None:
        target = self.shipped["impact_categories"][0]["id"]
        self._run([f"platform:impact_categories.{target}.applicability=not-applicable"])
        resolved = resolve_mod.resolve_shared_config(
            init_mod.PLATFORM_FILENAME, start=self.root
        )
        self.assertEqual(
            len(resolved["specialized_boms"]), len(self.shipped["specialized_boms"])
        )

    def test_repeated_sets_on_one_entry_record_the_applicability_decision(self) -> None:
        """RG-C tracks one decision per entry, so a second `--set` on the same
        entry must not overwrite the applicability the first one recorded."""
        target = self.shipped["impact_categories"][0]["id"]
        answers = init_mod.apply_set_overrides(
            {"schema_version": 1, "field_decisions": {}},
            [
                f"platform:impact_categories.{target}.applicability=not-applicable",
                f"platform:impact_categories.{target}.rationale=Not relevant here",
            ],
            list(init_mod.ALL_SECTIONS),
        )
        decision = answers["field_decisions"][f"rg_c_platform.impact_categories.{target}"]
        self.assertEqual(decision["new_value"], "not-applicable")
        # Matches what the interactive RG-C collector records for the same key.
        self.assertEqual(decision["source_value"], "unknown")

    def test_mapping_values_are_refused(self) -> None:
        """A mapping does not override the named leaf — it grafts new leaves
        below it, which would sidestep the shipped-path check entirely."""
        with self.assertRaises(InitError) as caught:
            init_mod.apply_set_overrides(
                {"schema_version": 1, "field_decisions": {}},
                ["platform.hosting_model={a: 1}"],
                list(init_mod.ALL_SECTIONS),
            )
        self.assertIn("must be scalars or lists", str(caught.exception))


class InteractiveGroupGatingRegressionTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory(prefix="agents-init-gating-regress-")
        self.root = _make_project(Path(self.temporary.name))

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def test_fixed_policy_keys_are_never_offered_as_groups(self) -> None:
        """`_AUTONOMY_FIXED_KEYS` hold a version number and a prose rule, not
        permission words, so `autonomy_allowed_choices` raises OverlayError on
        them — and `build_autonomy_overlay` refuses them anyway. Accepting
        every group must not reach either outcome."""
        # "y" accepts every group gate; the trailing blanks are what ends the
        # guardrail-bullet loop, which only stops on an empty line.
        answers = itertools.chain(["y"] * 100, itertools.repeat(""))
        with contextlib.redirect_stdout(io.StringIO()):
            result, decisions = init_interactive_mod.collect_governance_answers(
                lambda _prompt: next(answers)
            )
        for fixed_key in resolve_mod._AUTONOMY_FIXED_KEYS:
            self.assertNotIn(fixed_key, result["rg_b_autonomy"])
            self.assertFalse(
                [path for path in decisions if path.split(".")[0] == fixed_key],
                f"{fixed_key} is fixed policy and must not be offered",
            )

    def test_declining_every_platform_group_plans_no_write(self) -> None:
        """Declining is the defaults outcome, and keeping defaults means
        writing no overlay — not writing an empty one."""
        with contextlib.redirect_stdout(io.StringIO()):
            platform, decisions = init_interactive_mod.collect_platform_answers(
                lambda _prompt: ""
            )
        self.assertEqual(platform["rg_c_platform"], {})
        answers = {"schema_version": 1, "field_decisions": decisions, **platform}
        result, errors = plan_writes(self.root, answers, ["rg-c-platform"])
        self.assertEqual([], errors)
        self.assertEqual([], result.planned)

    def test_declined_group_records_the_preset_value_it_kept(self) -> None:
        """What a declined group keeps is whatever would have been shown, and
        with a preset loaded that is the preset's value — which is also what
        reaches the overlay through the caller's merge."""
        preset_id = "golang-postgres-k8s"
        preset = init_mod.load_stack_preset(preset_id)
        with contextlib.redirect_stdout(io.StringIO()):
            _answers, decisions = init_interactive_mod.collect_stack_answers(
                lambda _prompt: "", preset=preset, preset_id=preset_id
            )
        for path, expected in init_mod._leaf_values(preset["rg_a_stack"]).items():
            self.assertEqual(decisions[path]["status"], "kept")
            self.assertEqual(decisions[path]["source_value"], expected)

    def test_accepted_group_prompts_its_fields_before_the_next_gate(self) -> None:
        """RG-B interleaves like RG-A: opting into one group must not make the
        operator answer every remaining gate question first."""
        seen: list[int] = []

        def input_func(_prompt: str) -> str:
            seen.append(len(seen))
            return "y" if len(seen) == 1 else ""

        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            init_interactive_mod.collect_governance_answers(input_func)
        text = output.getvalue()
        first_gate = text.index("Review")
        first_field = text.index("(current default:")
        second_gate = text.index("Review", first_gate + 1)
        self.assertLess(first_gate, first_field)
        self.assertLess(first_field, second_gate)


if __name__ == "__main__":
    unittest.main()
