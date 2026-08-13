import hashlib
import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

PLUGIN_ROOT = Path(__file__).resolve().parents[1]
REPOSITORY_ROOT = PLUGIN_ROOT.parent
DEFAULT_PROVIDER = REPOSITORY_ROOT / "providers" / "agentic-sdlc-defaults" / "provider.json"
sys.path.insert(0, str(PLUGIN_ROOT))
import agentic_sdlc  # type: ignore

# Every subprocess invocation below exercises the checked-out package via
# dev_entrypoint.py -- the same entry point bin/agentic-sdlc uses, and
# deliberately not `python3 -m agentic_sdlc`/an installed `agentic-sdlc`
# distribution, since `-m` would put this test process's cwd (not
# PLUGIN_ROOT) at sys.path[0] in the subprocess; dev_entrypoint.py sets its
# own sys.path from its own file location instead, so no PYTHONPATH
# threading is needed here (see that file's docstring).
CLI_COMMAND = [sys.executable, str(PLUGIN_ROOT / "dev_entrypoint.py")]


def cli_env(overrides: dict[str, str] | None = None) -> dict[str, str]:
    return {**os.environ, **(overrides or {})}


def tree_hash(root: Path) -> str:
    """Deterministic content hash of every file under root, keyed by relative
    path, so a dry-run invocation can be proven to write zero bytes."""
    hasher = hashlib.sha256()
    for path in sorted(p for p in root.rglob("*") if p.is_file()):
        hasher.update(str(path.relative_to(root)).encode("utf-8"))
        hasher.update(path.read_bytes())
    return hasher.hexdigest()


class V03MigrationTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary.name)

    def tearDown(self):
        self.temporary.cleanup()

    def run_cli(self, *arguments, provider=False, expected=0, env=None):
        command = list(CLI_COMMAND)
        if provider:
            command += ["--provider", str(DEFAULT_PROVIDER)]
        result = subprocess.run(
            command + list(arguments) + ["--root", str(self.root)],
            text=True,
            capture_output=True,
            check=False,
            env=cli_env(env),
        )
        self.assertEqual(expected, result.returncode, result.stderr or result.stdout)
        return json.loads(result.stdout or result.stderr)

    def load(self, relative):
        return json.loads((self.root / relative).read_text(encoding="utf-8"))

    def test_kernel_only_init_is_non_destructive_and_has_contract_digest(self):
        first = self.run_cli("init")
        self.assertIsNone(first["profile"])
        project_path = self.root / ".agentic-sdlc" / "project.json"
        project = self.load(".agentic-sdlc/project.json")
        project["human_note"] = "preserve"
        project_path.write_text(json.dumps(project), encoding="utf-8")
        second = self.run_cli("init", "--force")
        self.assertEqual([], second["created"])
        self.assertEqual("preserve", self.load(".agentic-sdlc/project.json")["human_note"])
        lock = self.load(".agentic-sdlc/version.lock")
        self.assertEqual(agentic_sdlc.VERSION, lock["kernel_version"])
        self.assertTrue(lock["contract_digest"].startswith("sha256:"))
        self.assertEqual([], self.run_cli("provider", "list"))

    def test_bundled_agent_resources_are_not_in_kernel(self):
        self.assertFalse((PLUGIN_ROOT / "contracts" / "agent-catalog.json").exists())
        self.assertFalse(list((PLUGIN_ROOT / "profiles").glob("*/profile.json")))
        contract = json.loads((PLUGIN_ROOT / "contracts" / "lifecycle-gates.json").read_text(encoding="utf-8"))
        self.assertEqual([f"G{i}" for i in range(1, 11)], [gate["id"] for gate in contract["gates"]])
        for gate in contract["gates"]:
            self.assertNotIn("author_agents", gate)
            self.assertNotIn("review_agents", gate)

    def test_provider_backed_profile_binds_dispatch_and_digests(self):
        result = self.run_cli("init", "--profile", "generic", provider=True, expected=0)
        self.assertEqual("generic", result["profile"])
        plan = self.run_cli("plan", "--task-id", "MIGRATE-1", "--task", "Create the service architecture", provider=True)
        self.assertEqual(["G1", "G2", "G3"], [gate["id"] for gate in plan["required_quality_gates"]])
        gate = next(item for item in plan["gate_dispatch"] if item["gate_id"] == "G3")
        self.assertEqual(["cloud-architect"], gate["agents"])
        self.assertEqual(["define-architecture"], gate["tasks"])
        record = self.load(".agentic-sdlc/runs/MIGRATE-1/run-record.json")
        self.assertEqual(agentic_sdlc.VERSION, record["kernel_version"])
        self.assertTrue(record["dispatch_binding_digest"].startswith("sha256:"))
        self.assertEqual("agentic-sdlc-defaults", record["provider_bindings"][0]["id"])

    def test_planned_project_validates_clean_against_selection_schema(self):
        # Every other `validate` call in this suite injects a defect and
        # asserts it is caught (expected=1); none exercised the clean path.
        # That left the kernel's own plan producer and
        # contracts/selection.schema.json free to disagree silently -- doubly
        # so because schema validation is skipped outright when jsonschema is
        # absent. A freshly planned project must validate with no errors.
        self.run_cli("init", "--profile", "generic", provider=True)
        self.run_cli("plan", "--task-id", "VALID-1", "--task", "Create the service architecture", provider=True)
        # `validate` exits non-zero on readiness blockers (unresolved
        # authorities on a bare fixture project), which are not schema errors
        # -- assert on `errors`/`valid`, which is what the schema feeds.
        result = self.run_cli("validate", provider=True, expected=2)
        self.assertEqual([], result["errors"])
        self.assertTrue(result["valid"])

    def test_kernel_dispatch_plan_emits_exactly_its_schema_required_keys(self):
        # Pins the producer/contract agreement directly, so a field added to
        # one side without the other fails here with a readable diff rather
        # than through a schema message -- and still fails when jsonschema is
        # not installed, unlike the validation path above.
        self.run_cli("init", "--profile", "generic", provider=True)
        self.run_cli("plan", "--task-id", "KEYS-1", "--task", "Create the service architecture", provider=True)
        emitted = self.load(".agentic-sdlc/runs/KEYS-1/dispatch-plan.json")
        schema = json.loads(
            (PLUGIN_ROOT / "contracts" / "selection.schema.json").read_text(encoding="utf-8")
        )
        self.assertEqual(sorted(schema["required"]), sorted(emitted))
        # additionalProperties is false, so `properties` must also cover the
        # emitted set exactly -- a required-list-only check would miss a key
        # the schema declares optional but the producer never sends.
        self.assertEqual(sorted(schema["properties"]), sorted(emitted))
        self.assertEqual(schema["properties"]["schema_version"]["const"], emitted["schema_version"])

    def test_profile_requires_explicit_provider(self):
        result = self.run_cli("init", "--profile", "generic", expected=1)
        self.assertIn("unknown profile", result["error"])

    def test_provider_rejects_reviewer_with_author_capability(self):
        provider = json.loads(DEFAULT_PROVIDER.read_text(encoding="utf-8"))
        root = self.root / "bad-provider"
        (root / "profiles" / "p").mkdir(parents=True)
        (root / "catalog.json").write_text(json.dumps({"schema_version": 1, "agents": {"review": {"kind": "reviewer", "capabilities": ["reviewer", "author"]}}}), encoding="utf-8")
        (root / "profiles" / "p" / "profile.json").write_text(json.dumps({"id": "p", "version": "0.3.0", "gate_bindings": {}}), encoding="utf-8")
        provider.update({"id": "bad-provider", "agent_catalog": "catalog.json", "profile_roots": ["profiles"]})
        manifest = root / "provider.json"
        manifest.write_text(json.dumps(provider), encoding="utf-8")
        result = subprocess.run(
            CLI_COMMAND + ["--provider", str(manifest), "provider", "list"],
            text=True,
            capture_output=True,
            check=False,
            env=cli_env(),
        )
        self.assertEqual(1, result.returncode)
        self.assertIn("reviewer", result.stderr)

    def test_upgrade_preserves_project_decisions(self):
        self.run_cli("init")
        lock_path = self.root / ".agentic-sdlc" / "version.lock"
        lock = self.load(".agentic-sdlc/version.lock")
        lock["kernel_version"] = "0.2.0"
        lock_path.write_text(json.dumps(lock), encoding="utf-8")
        check = self.run_cli("upgrade", "--check")
        self.assertEqual("changes-available", check["status"])
        project_path = self.root / ".agentic-sdlc" / "project.json"
        project = self.load(".agentic-sdlc/project.json")
        project["decision"] = "keep"
        project_path.write_text(json.dumps(project), encoding="utf-8")
        applied = self.run_cli("upgrade", "--apply")
        self.assertTrue(applied["mutation"])
        self.assertEqual("keep", self.load(".agentic-sdlc/project.json")["decision"])
        self.assertEqual(agentic_sdlc.VERSION, self.load(".agentic-sdlc/version.lock")["kernel_version"])

    def test_repair_recreates_only_missing_baseline_and_is_idempotent(self):
        self.run_cli("init", "--profile", "generic", provider=True)
        authorities_path = self.root / ".agentic-sdlc" / "authorities.json"
        authorities = self.load(".agentic-sdlc/authorities.json")
        authorities["product_owner"]["assignee"] = "preserve-this-decision"
        authorities_path.write_text(json.dumps(authorities), encoding="utf-8")
        commands_path = self.root / ".agentic-sdlc" / "commands.json"
        commands_path.unlink()

        before = tree_hash(self.root)
        check = self.run_cli("repair", provider=True)
        self.assertEqual("repair-available", check["status"])
        self.assertFalse(check["mutation"])
        self.assertIn(
            {"path": ".agentic-sdlc/commands.json", "action": "recreate_missing_baseline"},
            check["actions"],
        )
        self.assertEqual(before, tree_hash(self.root))

        applied = self.run_cli("repair", "--apply", provider=True)
        self.assertEqual("repaired", applied["status"])
        self.assertTrue(applied["mutation"])
        self.assertTrue(commands_path.is_file())
        self.assertEqual("preserve-this-decision", self.load(".agentic-sdlc/authorities.json")["product_owner"]["assignee"])
        second = self.run_cli("repair", provider=True)
        self.assertEqual("current", second["status"])
        self.assertEqual([], second["actions"])

    def test_repair_upgrades_only_stale_lock_metadata(self):
        self.run_cli("init", "--profile", "generic", provider=True)
        lock_path = self.root / ".agentic-sdlc" / "version.lock"
        lock = self.load(".agentic-sdlc/version.lock")
        lock["kernel_version"] = "0.2.0"
        lock["operator_note"] = "preserve"
        lock_path.write_text(json.dumps(lock), encoding="utf-8")
        project_before = (self.root / ".agentic-sdlc" / "project.json").read_bytes()

        check = self.run_cli("repair", provider=True)
        self.assertEqual("repair-available", check["status"])
        self.assertIn("upgrade_lock:kernel_version", [item["action"] for item in check["actions"]])
        applied = self.run_cli("repair", "--apply", provider=True)
        self.assertTrue(applied["mutation"])
        repaired_lock = self.load(".agentic-sdlc/version.lock")
        self.assertEqual(agentic_sdlc.VERSION, repaired_lock["kernel_version"])
        self.assertEqual("preserve", repaired_lock["operator_note"])
        self.assertEqual(project_before, (self.root / ".agentic-sdlc" / "project.json").read_bytes())

    def test_repair_fails_closed_before_writing_on_unsafe_existing_state(self):
        self.run_cli("init", "--profile", "generic", provider=True)
        (self.root / ".agentic-sdlc" / "commands.json").unlink()
        agents_path = self.root / "AGENTS.md"
        agents_path.write_text(agents_path.read_text(encoding="utf-8").replace(agentic_sdlc.MANAGED_END, ""), encoding="utf-8")
        before = tree_hash(self.root)
        blocked = self.run_cli("repair", "--apply", provider=True, expected=1)
        self.assertEqual("blocked", blocked["status"])
        self.assertFalse(blocked["mutation"])
        self.assertEqual(before, tree_hash(self.root))

    def test_repair_refuses_unreviewed_provider_profile_drift(self):
        self.run_cli("init", "--profile", "generic", provider=True)
        project_path = self.root / ".agentic-sdlc" / "project.json"
        project = self.load(".agentic-sdlc/project.json")
        project["profile_digest"] = "sha256:old-profile"
        project_path.write_text(json.dumps(project), encoding="utf-8")
        before = tree_hash(self.root)
        blocked = self.run_cli("repair", "--apply", provider=True, expected=1)
        self.assertEqual("blocked", blocked["status"])
        self.assertTrue(any("provider profile has changed" in item["reason"] for item in blocked["blockers"]))
        self.assertEqual(before, tree_hash(self.root))

    def test_init_repair_alias_is_read_only_until_apply(self):
        self.run_cli("init", "--profile", "generic", provider=True)
        (self.root / ".agentic-sdlc" / "commands.json").unlink()
        check = self.run_cli("init", "--repair", provider=True)
        self.assertEqual("repair-available", check["status"])
        self.assertFalse((self.root / ".agentic-sdlc" / "commands.json").exists())
        self.run_cli("init", "--repair", "--apply", provider=True)
        self.assertTrue((self.root / ".agentic-sdlc" / "commands.json").exists())

    def test_invalidation_and_reentry_preserve_history_but_clear_stale_bindings(self):
        self.run_cli("init", "--profile", "generic", provider=True)
        self.run_cli("plan", "--task-id", "REENTRY-1", "--task", "Create the service architecture", provider=True)
        self.run_cli("invalidate", "--task-id", "REENTRY-1", "--earliest-gate", "G2", "--reason", "requirements changed", "--actor", "test-owner")
        result = self.run_cli("reenter", "--task-id", "REENTRY-1", "--earliest-gate", "G2", "--reason", "prepare revised baseline", "--actor", "test-owner")
        self.assertEqual("reentered", result["status"])
        record = self.load(".agentic-sdlc/runs/REENTRY-1/run-record.json")
        self.assertEqual(2, len(record["re_entry_history"]))
        self.assertEqual("pending", record["lifecycle_gates"][1]["status"])
        self.assertEqual([], record["lifecycle_gates"][1]["human_approvals"])

    def test_github_latest_change_request_invalidates_older_approval(self):
        reviews = [
            {"id": 1, "state": "APPROVED", "submitted_at": "2030-01-01T00:00:00Z", "commit_id": "abc", "user": {"login": "reviewer"}},
            {"id": 2, "state": "CHANGES_REQUESTED", "submitted_at": "2030-01-02T00:00:00Z", "commit_id": "abc", "user": {"login": "reviewer"}},
        ]
        with self.assertRaises(ValueError):
            agentic_sdlc.select_github_review(reviews, "reviewer", "abc")

    # -- RG-4: init --dry-run -------------------------------------------------

    def test_init_dry_run_on_fresh_root_writes_nothing(self):
        before = tree_hash(self.root)
        result = self.run_cli("init", "--profile", "generic", "--dry-run", provider=True)
        self.assertEqual("dry-run", result["status"])
        self.assertFalse(result["mutation"])
        self.assertEqual("generic", result["profile"])
        self.assertIn(".agentic-sdlc/project.json", result["would_create"])
        self.assertEqual([], result["existing_unchanged"])
        self.assertTrue(result["agent_wrappers_would_create"])
        self.assertEqual([], result["agent_wrappers_existing"])
        self.assertIn("detected", result)
        self.assertEqual("would_create", result["agents_md"])
        after = tree_hash(self.root)
        self.assertEqual(before, after)
        self.assertFalse((self.root / ".agentic-sdlc").exists())
        self.assertFalse((self.root / ".codex").exists())
        self.assertFalse((self.root / ".claude").exists())
        self.assertFalse((self.root / "AGENTS.md").exists())

    def test_init_dry_run_after_real_init_reports_existing_unchanged_and_writes_nothing(self):
        self.run_cli("init", "--profile", "generic", provider=True)
        before = tree_hash(self.root)
        result = self.run_cli("init", "--profile", "generic", "--dry-run", provider=True)
        self.assertEqual("dry-run", result["status"])
        self.assertEqual([], result["would_create"])
        self.assertIn(".agentic-sdlc/project.json", result["existing_unchanged"])
        self.assertIn(".agentic-sdlc/version.lock", result["existing_unchanged"])
        self.assertEqual([], result["agent_wrappers_would_create"])
        self.assertTrue(result["agent_wrappers_existing"])
        # update_agents_md() always rewrites the managed block on a real init,
        # even when AGENTS.md already exists -- dry-run must not claim it's
        # unchanged just because the file is present.
        self.assertEqual("would_update_managed_block", result["agents_md"])
        after = tree_hash(self.root)
        self.assertEqual(before, after)

    def test_init_real_run_reports_agents_md_created_then_updated(self):
        first = self.run_cli("init", "--profile", "generic", provider=True)
        self.assertEqual("created", first["agents_md"])
        second = self.run_cli("init", "--profile", "generic", provider=True)
        self.assertEqual("updated_managed_block", second["agents_md"])

    def test_init_dry_run_rejects_combination_with_force(self):
        result = subprocess.run(
            CLI_COMMAND + ["init", "--dry-run", "--force", "--root", str(self.root)],
            text=True,
            capture_output=True,
            check=False,
            env=cli_env(),
        )
        self.assertNotEqual(0, result.returncode)
        self.assertIn("not allowed with argument", result.stderr)

    # -- RG-1: GitLab MR approval-evidence adapter ----------------------------

    def test_gitlab_username_and_authority_helpers(self):
        self.assertEqual("alice", agentic_sdlc.gitlab_username_from_identity("gitlab.com/alice"))
        self.assertIsNone(agentic_sdlc.gitlab_username_from_identity("github.com/alice"))
        self.assertIsNone(agentic_sdlc.gitlab_username_from_identity(None))
        self.assertEqual(
            "explicit-alice",
            agentic_sdlc.authority_gitlab_username({"gitlab_username": "explicit-alice", "assignee": "gitlab.com/alice"}),
        )
        self.assertEqual("alice", agentic_sdlc.authority_gitlab_username({"assignee": "gitlab.com/alice"}))
        self.assertIsNone(agentic_sdlc.authority_gitlab_username({"assignee": "not-an-identity"}))

    def test_parse_gitlab_mr_uri(self):
        parsed = agentic_sdlc.parse_gitlab_mr_uri(
            "gitlab-mr:group/project:merge_requests/42:approval/7:approver/alice"
        )
        self.assertEqual(
            {"project_path": "group/project", "iid": "42", "approval_id": "7", "username": "alice"},
            parsed,
        )
        self.assertIsNone(agentic_sdlc.parse_gitlab_mr_uri("gitlab-mr:missing-fields"))

    def test_gitlab_approval_records_from_api_response_drops_name_email_and_avatar(self):
        raw = {
            "approved": True,
            "updated_at": "2030-01-01T00:00:00Z",
            "sha": "abc123",
            "approved_by": [
                {
                    "user": {
                        "id": 9,
                        "username": "alice",
                        "name": "Alice Example",
                        "email": "alice@example.com",
                        "avatar_url": "https://example.com/avatar.png",
                    }
                }
            ],
        }
        records = agentic_sdlc.gitlab_approval_records_from_api_response(raw)
        self.assertEqual(
            [{
                "approval_id": "9",
                "username": "alice",
                "state": "approved",
                "decided_at": "2030-01-01T00:00:00Z",
                "commit_sha": "abc123",
            }],
            records,
        )
        self.assertNotIn("name", records[0])
        self.assertNotIn("email", records[0])
        self.assertNotIn("avatar_url", records[0])

    def test_gitlab_latest_pending_state_invalidates_older_approval(self):
        approvals = [
            {"approval_id": "1", "username": "alice", "state": "approved", "decided_at": "2030-01-01T00:00:00Z", "commit_sha": "abc"},
            {"approval_id": "2", "username": "alice", "state": "pending", "decided_at": "2030-01-02T00:00:00Z", "commit_sha": "abc"},
        ]
        with self.assertRaises(ValueError):
            agentic_sdlc.select_gitlab_approval(approvals, "alice", "abc")

    def test_gitlab_approval_records_from_api_response_uses_approved_by_presence_not_mr_threshold(self):
        # GitLab's `approved_by` lists users who have individually already
        # approved, independent of whether the MR-level approval-rule
        # threshold (`approved`) has been satisfied. A partial-progress
        # response -- one approver in, threshold not yet met -- must still
        # surface that approver's own approval as "approved".
        raw = {
            "approved": False,
            "updated_at": "2030-01-01T00:00:00Z",
            "sha": "abc123",
            "approved_by": [
                {
                    "user": {
                        "id": 9,
                        "username": "alice",
                        "name": "Alice Example",
                        "email": "alice@example.com",
                        "avatar_url": "https://example.com/avatar.png",
                    }
                }
            ],
        }
        records = agentic_sdlc.gitlab_approval_records_from_api_response(raw)
        selected = agentic_sdlc.select_gitlab_approval(records, "alice", "abc123")
        self.assertEqual("approved", selected["state"])
        self.assertEqual("9", selected["approval_id"])

    def test_approval_source_policy_accepts_gitlab_mr_additively(self):
        self.assertEqual(
            {"human_gate_default": "gitlab-mr", "allow_manual_fallback": True},
            agentic_sdlc.approval_source_policy({"approval_sources": {"human_gate_default": "gitlab-mr"}}),
        )
        self.assertEqual(
            {"human_gate_default": "github-review", "allow_manual_fallback": True},
            agentic_sdlc.approval_source_policy({"approval_sources": {"human_gate_default": "github-review"}}),
        )
        with self.assertRaises(ValueError):
            agentic_sdlc.approval_source_policy({"approval_sources": {"human_gate_default": "bogus"}})

    def _assign_gitlab_authority(self, role="product_owner", username="alice"):
        authorities_path = self.root / ".agentic-sdlc" / "authorities.json"
        authorities = self.load(".agentic-sdlc/authorities.json")
        authorities[role].update({"status": "assigned", "assignee": f"gitlab.com/{username}", "applicability": "applicable"})
        authorities_path.write_text(json.dumps(authorities), encoding="utf-8")

    def test_approve_from_gitlab_records_username_only_evidence(self):
        self.run_cli("init", "--profile", "generic", provider=True)
        self._assign_gitlab_authority()
        self.run_cli("plan", "--task-id", "GL-1", "--task", "Create the service architecture", provider=True)
        result = self.run_cli(
            "approve-from-gitlab",
            "--task-id", "GL-1",
            "--gate", "G1",
            "--role", "product_owner",
            "--project-path", "group/project",
            "--mr-iid", "42",
            "--approval-id", "7",
            "--approver-username", "alice",
            "--commit-sha", "DEADBEEF",
            "--decided-at", "2030-01-01T00:00:00Z",
        )
        expected_uri = "gitlab-mr:group/project:merge_requests/42:approval/7:approver/alice"
        self.assertEqual(expected_uri, result["approval_uri"])
        record = self.load(".agentic-sdlc/runs/GL-1/run-record.json")
        gate = next(item for item in record["lifecycle_gates"] if item["gate_id"] == "G1")
        approval = gate["human_approvals"][0]
        self.assertEqual("gitlab.com/alice", approval["approver"]["id"])
        evidence = approval["evidence_refs"][0]
        self.assertEqual(expected_uri, evidence["uri"])
        self.assertEqual("sha256", evidence["hash_algorithm"])
        # Amendment B: only the pseudonymous username is ever persisted -- the
        # serialized run record must not contain any email/name/avatar text.
        serialized = json.dumps(record)
        self.assertNotIn("@", serialized)
        self.assertNotIn("avatar", serialized.lower())

    def test_approve_from_gitlab_rejects_username_mismatch_with_assigned_authority(self):
        self.run_cli("init", "--profile", "generic", provider=True)
        self._assign_gitlab_authority(username="alice")
        self.run_cli("plan", "--task-id", "GL-2", "--task", "Create the service architecture", provider=True)
        result = self.run_cli(
            "approve-from-gitlab",
            "--task-id", "GL-2",
            "--gate", "G1",
            "--role", "product_owner",
            "--project-path", "group/project",
            "--mr-iid", "42",
            "--approval-id", "7",
            "--approver-username", "mallory",
            "--commit-sha", "DEADBEEF",
            expected=1,
        )
        self.assertIn("does not match assigned authority username", result["error"])

    def test_approve_from_gitlab_rejects_role_not_required_by_gate(self):
        self.run_cli("init", "--profile", "generic", provider=True)
        self._assign_gitlab_authority(role="engineering_lead", username="alice")
        self.run_cli("plan", "--task-id", "GL-3", "--task", "Create the service architecture", provider=True)
        result = self.run_cli(
            "approve-from-gitlab",
            "--task-id", "GL-3",
            "--gate", "G1",
            "--role", "engineering_lead",
            "--project-path", "group/project",
            "--mr-iid", "42",
            "--approval-id", "7",
            "--approver-username", "alice",
            "--commit-sha", "DEADBEEF",
            expected=1,
        )
        self.assertIn("does not require authority role", result["error"])

    def test_approve_from_gitlab_mr_fetches_and_filters_by_commit(self):
        self.run_cli("init", "--profile", "generic", provider=True)
        self._assign_gitlab_authority(username="alice")
        self.run_cli("plan", "--task-id", "GL-4", "--task", "Create the service architecture", provider=True)
        mock_path = self.root / "gitlab-approvals-mock.json"
        # This mocks the *raw*, unnormalized `glab api
        # projects/:id/merge_requests/:iid/approvals` response shape (a
        # single MR-level object, with `name`/`email`/`avatar_url` on the
        # `user` objects exactly as GitLab actually returns them) rather
        # than a pre-normalized record list, so this test exercises the
        # real `fetch_gitlab_mr_approvals` -> normalizer wiring instead of
        # bypassing it.
        mock_path.write_text(json.dumps({
            "approved": False,
            "updated_at": "2030-01-02T00:00:00Z",
            "sha": "def",
            "approved_by": [
                {
                    "user": {
                        "id": 2,
                        "username": "alice",
                        "name": "Alice Example",
                        "email": "alice@example.com",
                        "avatar_url": "https://example.com/avatar.png",
                    }
                }
            ],
        }), encoding="utf-8")
        result = self.run_cli(
            "approve-from-gitlab-mr",
            "--task-id", "GL-4",
            "--gate", "G1",
            "--role", "product_owner",
            "--project-path", "group/project",
            "--mr-iid", "42",
            "--commit-sha", "def",
            env={"AGENTIC_SDLC_TEST_GITLAB_APPROVALS_FILE": str(mock_path)},
        )
        self.assertEqual("2", result["selected_approval_id"])
        self.assertEqual("def", result["selected_commit_sha"])
        # Amendment B: the normalizer must have been exercised for real --
        # no name/email/avatar text leaks into the CLI result or the
        # persisted run record.
        serialized_result = json.dumps(result)
        self.assertNotIn("@", serialized_result)
        self.assertNotIn("avatar", serialized_result.lower())
        record = self.load(".agentic-sdlc/runs/GL-4/run-record.json")
        serialized_record = json.dumps(record)
        self.assertNotIn("@", serialized_record)
        self.assertNotIn("avatar", serialized_record.lower())
        self.assertEqual(
            "gitlab-mr:group/project:merge_requests/42:approval/2:approver/alice",
            result["approval_uri"],
        )

    def test_validate_flags_malformed_gitlab_mr_uri(self):
        self.run_cli("init", "--profile", "generic", provider=True)
        self._assign_gitlab_authority(username="alice")
        self.run_cli("plan", "--task-id", "GL-5", "--task", "Create the service architecture", provider=True)
        self.run_cli(
            "approve-from-gitlab",
            "--task-id", "GL-5",
            "--gate", "G1",
            "--role", "product_owner",
            "--project-path", "group/project",
            "--mr-iid", "42",
            "--approval-id", "7",
            "--approver-username", "alice",
            "--commit-sha", "DEADBEEF",
            "--decided-at", "2030-01-01T00:00:00Z",
        )
        record_relative = ".agentic-sdlc/runs/GL-5/run-record.json"
        record_path = self.root / record_relative
        record = self.load(record_relative)
        gate = next(item for item in record["lifecycle_gates"] if item["gate_id"] == "G1")
        gate["human_approvals"][0]["evidence_refs"][0]["uri"] = "gitlab-mr:malformed-uri-missing-fields"
        # The URI-shape check only runs on an approved gate's evidence; force
        # gate status here (independent of the other approval-completeness
        # checks, which are exercised elsewhere) so this test isolates the
        # specific non-regression fix: a malformed gitlab-mr: URI must not
        # pass through validation unvalidated, exactly like github-review:.
        gate["status"] = "approved"
        record_path.write_text(json.dumps(record), encoding="utf-8")
        result = self.run_cli("validate", provider=True, expected=1)
        self.assertTrue(
            any("invalid GitLab MR approval URI" in error for error in result["errors"]),
            result["errors"],
        )

    def test_decide_approves_gate_and_records_evidence(self):
        self.run_cli("init", "--profile", "generic", provider=True)
        self._assign_authority("product_owner", assignee="alice")
        self.run_cli("plan", "--task-id", "DEC-1", "--task", "Create the service architecture", provider=True)
        result = self.run_cli(
            "decide",
            "--task-id", "DEC-1",
            "--gate", "G1",
            "--role", "product_owner",
            "--decision", "approved",
            "--actor-id", "alice",
            "--evidence-uri", "doc:decision-record-1",
            "--note", "Looks good",
            "--decided-at", "2030-01-01T00:00:00Z",
        )
        self.assertEqual("approved", result["decision"])
        record = self.load(".agentic-sdlc/runs/DEC-1/run-record.json")
        gate = next(item for item in record["lifecycle_gates"] if item["gate_id"] == "G1")
        approval = gate["human_approvals"][0]
        self.assertEqual("approved", approval["status"])
        self.assertEqual("alice", approval["approver"]["id"])
        self.assertEqual("Looks good", approval["note"])
        evidence = approval["evidence_refs"][0]
        self.assertEqual("doc:decision-record-1", evidence["uri"])
        self.assertEqual("sha256", evidence["hash_algorithm"])

    def test_decide_rejected_leaves_gate_unapproved(self):
        self.run_cli("init", "--profile", "generic", provider=True)
        self._assign_authority("product_owner", assignee="alice")
        self.run_cli("plan", "--task-id", "DEC-2", "--task", "Create the service architecture", provider=True)
        result = self.run_cli(
            "decide",
            "--task-id", "DEC-2",
            "--gate", "G1",
            "--role", "product_owner",
            "--decision", "rejected",
            "--actor-id", "alice",
            "--evidence-uri", "doc:decision-record-2",
        )
        self.assertNotEqual("approved", result["gate_status"])
        record = self.load(".agentic-sdlc/runs/DEC-2/run-record.json")
        gate = next(item for item in record["lifecycle_gates"] if item["gate_id"] == "G1")
        self.assertEqual("rejected", gate["human_approvals"][0]["status"])

    def test_decide_request_changes_sets_gate_status(self):
        self.run_cli("init", "--profile", "generic", provider=True)
        self._assign_authority("product_owner", assignee="alice")
        self.run_cli("plan", "--task-id", "DEC-3", "--task", "Create the service architecture", provider=True)
        result = self.run_cli(
            "decide",
            "--task-id", "DEC-3",
            "--gate", "G1",
            "--role", "product_owner",
            "--decision", "request-changes",
            "--actor-id", "alice",
            "--evidence-uri", "doc:decision-record-3",
        )
        self.assertEqual("request-changes", result["gate_status"])
        record = self.load(".agentic-sdlc/runs/DEC-3/run-record.json")
        gate = next(item for item in record["lifecycle_gates"] if item["gate_id"] == "G1")
        self.assertEqual("request-changes", gate["status"])

    def test_decide_refuses_actor_mismatch(self):
        self.run_cli("init", "--profile", "generic", provider=True)
        self._assign_authority("product_owner", assignee="alice")
        self.run_cli("plan", "--task-id", "DEC-4", "--task", "Create the service architecture", provider=True)
        result = self.run_cli(
            "decide",
            "--task-id", "DEC-4",
            "--gate", "G1",
            "--role", "product_owner",
            "--decision", "approved",
            "--actor-id", "mallory",
            "--evidence-uri", "doc:decision-record-4",
            expected=1,
        )
        self.assertIn("does not match assigned authority", result["error"])

    def test_decide_refuses_self_decision_as_preparer(self):
        self.run_cli("init", "--profile", "generic", provider=True)
        self._assign_authority("product_owner", assignee="alice")
        self.run_cli("plan", "--task-id", "DEC-5", "--task", "Create the service architecture", provider=True)
        record_relative = ".agentic-sdlc/runs/DEC-5/run-record.json"
        record_path = self.root / record_relative
        record = self.load(record_relative)
        gate = next(item for item in record["lifecycle_gates"] if item["gate_id"] == "G1")
        gate["preparers"] = [{"id": "alice", "role": "Product Owner", "kind": "human"}]
        record_path.write_text(json.dumps(record), encoding="utf-8")
        result = self.run_cli(
            "decide",
            "--task-id", "DEC-5",
            "--gate", "G1",
            "--role", "product_owner",
            "--decision", "approved",
            "--actor-id", "alice",
            "--evidence-uri", "doc:decision-record-5",
            expected=1,
        )
        self.assertIn("is a preparer", result["error"])

    def test_decide_refuses_self_decision_as_verifier(self):
        self.run_cli("init", "--profile", "generic", provider=True)
        self._assign_authority("product_owner", assignee="alice")
        self.run_cli("plan", "--task-id", "DEC-6", "--task", "Create the service architecture", provider=True)
        record_relative = ".agentic-sdlc/runs/DEC-6/run-record.json"
        record_path = self.root / record_relative
        record = self.load(record_relative)
        gate = next(item for item in record["lifecycle_gates"] if item["gate_id"] == "G1")
        gate["independent_verifier"] = {"id": "alice", "role": "Independent Verifier", "kind": "human"}
        record_path.write_text(json.dumps(record), encoding="utf-8")
        result = self.run_cli(
            "decide",
            "--task-id", "DEC-6",
            "--gate", "G1",
            "--role", "product_owner",
            "--decision", "approved",
            "--actor-id", "alice",
            "--evidence-uri", "doc:decision-record-6",
            expected=1,
        )
        self.assertIn("is the independent verifier", result["error"])

    def test_decide_downgrades_previously_approved_gate_on_rejection(self):
        self.run_cli("init", "--profile", "generic", provider=True)
        self._assign_authority("product_owner", assignee="alice")
        self.run_cli("plan", "--task-id", "DEC-7", "--task", "Create the service architecture", provider=True)
        record_relative = ".agentic-sdlc/runs/DEC-7/run-record.json"
        record_path = self.root / record_relative
        record = self.load(record_relative)
        gate = next(item for item in record["lifecycle_gates"] if item["gate_id"] == "G1")
        gate["status"] = "approved"
        gate["human_approvals"] = [{
            "status": "approved",
            "approver": {"id": "alice", "role": "Product Owner", "kind": "human"},
            "decided_at": "2030-01-01T00:00:00Z",
            "evidence_refs": [{"evidence_id": "seed", "uri": "doc:seed", "hash_algorithm": "sha256", "hash": "0" * 64, "classification": "internal"}],
        }]
        record_path.write_text(json.dumps(record), encoding="utf-8")
        result = self.run_cli(
            "decide",
            "--task-id", "DEC-7",
            "--gate", "G1",
            "--role", "product_owner",
            "--decision", "rejected",
            "--actor-id", "alice",
            "--evidence-uri", "doc:decision-record-7",
        )
        self.assertEqual("pending", result["gate_status"])
        record = self.load(record_relative)
        gate = next(item for item in record["lifecycle_gates"] if item["gate_id"] == "G1")
        self.assertEqual("pending", gate["status"])
        self.assertEqual("rejected", gate["human_approvals"][0]["status"])

    def test_decide_preserves_prior_rejection_when_later_approved(self):
        # Regression: the dedup filter used to drop *any* prior entry by the
        # same actor+role regardless of status, silently discarding a prior
        # rejection's own evidence/rationale. It must now only replace a
        # prior *approved* entry, matching record_github_approval/
        # record_gitlab_approval's existing semantics.
        self.run_cli("init", "--profile", "generic", provider=True)
        self._assign_authority("product_owner", assignee="alice")
        self.run_cli("plan", "--task-id", "DEC-8", "--task", "Create the service architecture", provider=True)
        self.run_cli(
            "decide",
            "--task-id", "DEC-8",
            "--gate", "G1",
            "--role", "product_owner",
            "--decision", "rejected",
            "--actor-id", "alice",
            "--evidence-uri", "doc:first-pass-rejection",
            "--note", "needs more detail",
        )
        self.run_cli(
            "decide",
            "--task-id", "DEC-8",
            "--gate", "G1",
            "--role", "product_owner",
            "--decision", "approved",
            "--actor-id", "alice",
            "--evidence-uri", "doc:second-pass-approval",
        )
        record = self.load(".agentic-sdlc/runs/DEC-8/run-record.json")
        gate = next(item for item in record["lifecycle_gates"] if item["gate_id"] == "G1")
        statuses = [approval["status"] for approval in gate["human_approvals"]]
        self.assertEqual(["rejected", "approved"], statuses)
        self.assertEqual("doc:first-pass-rejection", gate["human_approvals"][0]["evidence_refs"][0]["uri"])

    def test_decide_rejects_unknown_decision_value(self):
        # argparse's --decision choices already block this at the CLI layer;
        # this exercises record_gate_decision directly since it is also a
        # public library function other callers could invoke with an
        # unvalidated string.
        self.run_cli("init", "--profile", "generic", provider=True)
        self._assign_authority("product_owner", assignee="alice")
        self.run_cli("plan", "--task-id", "DEC-9", "--task", "Create the service architecture", provider=True)
        with self.assertRaisesRegex(ValueError, "unknown decision"):
            agentic_sdlc.record_gate_decision(
                self.root, "DEC-9", "G1", "product_owner", "aproved", "alice", "doc:decision-record-9", None, None,
            )

    def test_decide_refuses_manual_evidence_when_policy_requires_github_review(self):
        self.run_cli("init", "--profile", "generic", provider=True)
        self._assign_authority("product_owner", assignee="alice")
        project_path = self.root / ".agentic-sdlc" / "project.json"
        project = self.load(".agentic-sdlc/project.json")
        project["approval_sources"] = {"human_gate_default": "github-review", "allow_manual_fallback": False}
        project_path.write_text(json.dumps(project), encoding="utf-8")
        self.run_cli("plan", "--task-id", "DEC-10", "--task", "Create the service architecture", provider=True)
        result = self.run_cli(
            "decide",
            "--task-id", "DEC-10",
            "--gate", "G1",
            "--role", "product_owner",
            "--decision", "approved",
            "--actor-id", "alice",
            "--evidence-uri", "doc:i-said-so",
            expected=1,
        )
        self.assertIn("must be backed by a GitHub review", result["error"])
        record = self.load(".agentic-sdlc/runs/DEC-10/run-record.json")
        gate = next(item for item in record["lifecycle_gates"] if item["gate_id"] == "G1")
        self.assertEqual([], gate["human_approvals"])

    def test_decide_accepts_manual_evidence_when_policy_allows_fallback(self):
        self.run_cli("init", "--profile", "generic", provider=True)
        self._assign_authority("product_owner", assignee="alice")
        project_path = self.root / ".agentic-sdlc" / "project.json"
        project = self.load(".agentic-sdlc/project.json")
        project["approval_sources"] = {"human_gate_default": "github-review", "allow_manual_fallback": True}
        project_path.write_text(json.dumps(project), encoding="utf-8")
        self.run_cli("plan", "--task-id", "DEC-11", "--task", "Create the service architecture", provider=True)
        result = self.run_cli(
            "decide",
            "--task-id", "DEC-11",
            "--gate", "G1",
            "--role", "product_owner",
            "--decision", "approved",
            "--actor-id", "alice",
            "--evidence-uri", "doc:manual-fallback-ok",
        )
        self.assertEqual("approved", result["decision"])

    def _approved_g1_gate(self, task_id, verifier):
        # Shared setup for the three regression tests below (issue #9):
        # a gate that has already cleared every *other* approved-gate
        # invariant (applicability, artifact binding, evidence, authority
        # requirements, human approval) so only the independent-verifier
        # check under test can produce an error. This deliberately mirrors
        # test_validate_flags_malformed_gitlab_mr_uri's "force gate status"
        # pattern: hand-editing the persisted run record to isolate one
        # check, not exercising the full dispatch/execution completeness
        # path (covered elsewhere), so other, unrelated errors (missing
        # dispatched-agent/task-completion records, missing decision
        # timestamp) are expected to remain and are not asserted on here.
        self.run_cli("init", "--profile", "generic", provider=True)
        self._assign_gitlab_authority(username="alice")
        self.run_cli("plan", "--task-id", task_id, "--task", "Create the service architecture", provider=True)
        self.run_cli(
            "approve-from-gitlab",
            "--task-id", task_id,
            "--gate", "G1",
            "--role", "product_owner",
            "--project-path", "group/project",
            "--mr-iid", "42",
            "--approval-id", "7",
            "--approver-username", "alice",
            "--commit-sha", "DEADBEEF",
            "--decided-at", "2030-01-01T00:00:00Z",
        )
        record_relative = f".agentic-sdlc/runs/{task_id}/run-record.json"
        record_path = self.root / record_relative
        record = self.load(record_relative)
        gate = next(item for item in record["lifecycle_gates"] if item["gate_id"] == "G1")
        gate["status"] = "approved"
        gate["artifact_bindings"] = [{"artifact_id": "a", "revision": "1", "digest": "sha256:00"}]
        gate["evidence_refs"] = [{
            "evidence_id": "g1-source",
            "uri": "gitlab-issue:group/project:issues/1",
            "hash_algorithm": "sha256",
            "hash": "00",
            "classification": "internal",
        }]
        gate["independent_verifier"] = verifier
        gate["independence_declaration"] = {
            "verifier_confirmed_not_preparer": True,
            "verifier_made_material_correction": False,
        }
        record_path.write_text(json.dumps(record), encoding="utf-8")
        return self.run_cli("validate", provider=True, expected=1)

    def test_validate_accepts_agent_verifier_that_is_a_catalog_reviewer(self):
        # Regression for issue #9: `required_reviewers` used to be a
        # hardcoded empty set, so `verifier_role not in required_reviewers`
        # was unconditionally true and every approved gate was rejected with
        # "lacks required reviewer role []" regardless of who verified it.
        result = self._approved_g1_gate("REV-1", {"id": "code-reviewer", "role": "code-reviewer", "kind": "agent"})
        self.assertFalse(any("lacks required reviewer role" in error for error in result["errors"]), result["errors"])
        self.assertFalse(any("is not a catalog reviewer" in error for error in result["errors"]), result["errors"])

    def test_validate_rejects_agent_verifier_that_is_not_a_catalog_reviewer(self):
        result = self._approved_g1_gate("REV-2", {"id": "product-intent-agent", "role": "product-intent-agent", "kind": "agent"})
        self.assertTrue(
            any("verifier agent is not a catalog reviewer" in error for error in result["errors"]),
            result["errors"],
        )
        # The old, always-firing dead-code message must never come back.
        self.assertFalse(any("lacks required reviewer role" in error for error in result["errors"]), result["errors"])

    def test_validate_accepts_human_verifier_without_consulting_the_agent_catalog(self):
        # A human (or service) verifier's `role` is a free-text label, not an
        # agent-catalog id -- the agent-catalog reviewer check must not be
        # applied to it (see the identity schema's `kind` enum: human/agent/
        # service is a different axis than the agent catalog's own
        # author/reviewer/specialist `kind`).
        result = self._approved_g1_gate("REV-3", {"id": "reviewer", "role": "Reviewer", "kind": "human"})
        self.assertFalse(any("lacks required reviewer role" in error for error in result["errors"]), result["errors"])
        self.assertFalse(any("is not a catalog reviewer" in error for error in result["errors"]), result["errors"])

    def test_validate_accepts_service_verifier_without_consulting_the_agent_catalog(self):
        # `service` is the third identity `kind` (run-record.schema.json's
        # `identity` $def enum: human/agent/service) -- exercise it
        # independently of the human-kind case above so all three enum
        # values are pinned, not just two of three.
        result = self._approved_g1_gate("REV-4", {"id": "ci-bot", "role": "CI Bot", "kind": "service"})
        self.assertFalse(any("lacks required reviewer role" in error for error in result["errors"]), result["errors"])
        self.assertFalse(any("is not a catalog reviewer" in error for error in result["errors"]), result["errors"])

    def test_validate_rejects_non_string_agent_verifier_role_instead_of_crashing(self):
        # A schema-invalid `role` (e.g. a list) is reported as a validation
        # error, not an uncaught TypeError from `agent_catalog.get(role, {})`
        # -- schema violations are appended to `errors` but do not stop this
        # function's per-gate checks (see validator.iter_errors() above),
        # so this line is reachable even for a hand-edited/malformed record.
        result = self._approved_g1_gate("REV-5", {"id": "x", "role": ["oops"], "kind": "agent"})
        self.assertTrue(
            any("verifier role must be a string" in error for error in result["errors"]),
            result["errors"],
        )

    def test_parse_gitlab_issue_uri(self):
        parsed = agentic_sdlc.parse_gitlab_issue_uri("gitlab-issue:group/project:issues/42")
        self.assertEqual({"project_path": "group/project", "iid": "42"}, parsed)
        self.assertIsNone(agentic_sdlc.parse_gitlab_issue_uri("gitlab-issue:missing-fields"))
        self.assertIsNone(agentic_sdlc.parse_gitlab_issue_uri("gitlab-mr:group/project:merge_requests/42:approval/1:approver/alice"))

    def test_fetch_gitlab_issue_via_mock(self):
        mock_path = self.root / "gitlab-issue-mock.json"
        mock_path.write_text(json.dumps({
            "iid": 42,
            "title": "Support SSO login for enterprise customers",
            "state": "opened",
            "web_url": "https://gitlab.example.com/group/project/-/issues/42",
            "updated_at": "2030-01-01T00:00:00Z",
        }), encoding="utf-8")
        with mock.patch.dict(os.environ, {"AGENTIC_SDLC_TEST_GITLAB_ISSUE_FILE": str(mock_path)}):
            issue = agentic_sdlc.fetch_gitlab_issue("group/project", 42)
        self.assertEqual(
            {
                "iid": 42,
                "title": "Support SSO login for enterprise customers",
                "state": "opened",
                "web_url": "https://gitlab.example.com/group/project/-/issues/42",
                "updated_at": "2030-01-01T00:00:00Z",
            },
            issue,
        )

    def test_fetch_gitlab_issue_rejects_missing_title(self):
        mock_path = self.root / "gitlab-issue-bad.json"
        mock_path.write_text(json.dumps({"iid": 99, "state": "opened"}), encoding="utf-8")
        with mock.patch.dict(os.environ, {"AGENTIC_SDLC_TEST_GITLAB_ISSUE_FILE": str(mock_path)}):
            with self.assertRaises(ValueError):
                agentic_sdlc.fetch_gitlab_issue("group/project", 99)

    def _assign_authority(self, role, assignee="github.com/owner"):
        authorities_path = self.root / ".agentic-sdlc" / "authorities.json"
        authorities = self.load(".agentic-sdlc/authorities.json")
        authorities[role].update({"status": "assigned", "assignee": assignee, "applicability": "applicable"})
        authorities_path.write_text(json.dumps(authorities), encoding="utf-8")

    def _link_from_gitlab_issue(self, command, **overrides):
        mock_path = self.root / "gitlab-issue-link-mock.json"
        mock_path.write_text(json.dumps({
            "iid": overrides.get("issue_iid", 42),
            "title": overrides.get("title", "Support SSO login for enterprise customers"),
            "state": overrides.get("state", "opened"),
            "web_url": "https://gitlab.example.com/group/project/-/issues/42",
            "updated_at": "2030-01-01T00:00:00Z",
        }), encoding="utf-8")
        return self.run_cli(
            command,
            "--task-id", overrides["task_id"],
            "--role", overrides["role"],
            "--project-path", overrides.get("project_path", "group/project"),
            "--issue-iid", str(overrides.get("issue_iid", 42)),
            expected=overrides.get("expected", 0),
            env={"AGENTIC_SDLC_TEST_GITLAB_ISSUE_FILE": str(mock_path)},
        )

    def test_link_intent_from_gitlab_issue_records_evidence_and_sets_intent_record_id(self):
        self.run_cli("init", "--profile", "generic", provider=True)
        self._assign_authority("product_owner")
        self.run_cli("plan", "--task-id", "GI-1", "--task", "Create the service architecture", provider=True)
        result = self._link_from_gitlab_issue(
            "link-intent-from-gitlab-issue", task_id="GI-1", role="product_owner"
        )
        expected_uri = "gitlab-issue:group/project:issues/42"
        self.assertEqual(expected_uri, result["issue_uri"])
        self.assertEqual("intent_record_id", result["record_field"])
        record = self.load(".agentic-sdlc/runs/GI-1/run-record.json")
        self.assertEqual(expected_uri, record["intent_record_id"])
        self.assertIsNone(record["requirements_baseline_id"])
        gate = next(item for item in record["lifecycle_gates"] if item["gate_id"] == "G1")
        evidence = gate["evidence_refs"][0]
        self.assertEqual(expected_uri, evidence["uri"])
        self.assertEqual("sha256", evidence["hash_algorithm"])
        self.assertEqual("g1-source-gitlab-issue-42", evidence["evidence_id"])

    def test_link_requirements_from_gitlab_issue_records_evidence_and_sets_requirements_baseline_id(self):
        self.run_cli("init", "--profile", "generic", provider=True)
        self._assign_authority("engineering_lead")
        self.run_cli("plan", "--task-id", "GI-2", "--task", "Create the service architecture", provider=True)
        result = self._link_from_gitlab_issue(
            "link-requirements-from-gitlab-issue", task_id="GI-2", role="engineering_lead"
        )
        expected_uri = "gitlab-issue:group/project:issues/42"
        self.assertEqual("requirements_baseline_id", result["record_field"])
        record = self.load(".agentic-sdlc/runs/GI-2/run-record.json")
        self.assertEqual(expected_uri, record["requirements_baseline_id"])
        self.assertIsNone(record["intent_record_id"])
        gate = next(item for item in record["lifecycle_gates"] if item["gate_id"] == "G2")
        self.assertEqual(expected_uri, gate["evidence_refs"][0]["uri"])

    def test_link_intent_rejects_unassigned_authority(self):
        # An authority never assigned before `plan` has its gate snapshot's
        # applicability computed as "unknown" at plan time (make_gate_record),
        # so that check fires before reaching the separate "is not assigned"
        # check further down -- both are rejections, just at different points
        # for a never-assigned vs. assigned-then-unassigned-after-plan authority.
        self.run_cli("init", "--profile", "generic", provider=True)
        self.run_cli("plan", "--task-id", "GI-3", "--task", "Create the service architecture", provider=True)
        result = self._link_from_gitlab_issue(
            "link-intent-from-gitlab-issue", task_id="GI-3", role="product_owner", expected=1
        )
        self.assertIn("is not applicable", result["error"])

    def test_link_intent_rejects_authority_unassigned_after_plan(self):
        self.run_cli("init", "--profile", "generic", provider=True)
        self._assign_authority("product_owner")
        self.run_cli("plan", "--task-id", "GI-3B", "--task", "Create the service architecture", provider=True)
        authorities_path = self.root / ".agentic-sdlc" / "authorities.json"
        authorities = self.load(".agentic-sdlc/authorities.json")
        authorities["product_owner"].update({"status": "unassigned", "assignee": None})
        authorities_path.write_text(json.dumps(authorities), encoding="utf-8")
        result = self._link_from_gitlab_issue(
            "link-intent-from-gitlab-issue", task_id="GI-3B", role="product_owner", expected=1
        )
        self.assertIn("is not assigned", result["error"])

    def test_link_intent_rejects_role_not_required_by_gate(self):
        self.run_cli("init", "--profile", "generic", provider=True)
        self._assign_authority("engineering_lead")
        self.run_cli("plan", "--task-id", "GI-4", "--task", "Create the service architecture", provider=True)
        result = self._link_from_gitlab_issue(
            "link-intent-from-gitlab-issue", task_id="GI-4", role="engineering_lead", expected=1
        )
        self.assertIn("does not require authority role", result["error"])

    def test_link_intent_is_idempotent_on_relink(self):
        self.run_cli("init", "--profile", "generic", provider=True)
        self._assign_authority("product_owner")
        self.run_cli("plan", "--task-id", "GI-5", "--task", "Create the service architecture", provider=True)
        self._link_from_gitlab_issue("link-intent-from-gitlab-issue", task_id="GI-5", role="product_owner")
        self._link_from_gitlab_issue("link-intent-from-gitlab-issue", task_id="GI-5", role="product_owner")
        record = self.load(".agentic-sdlc/runs/GI-5/run-record.json")
        gate = next(item for item in record["lifecycle_gates"] if item["gate_id"] == "G1")
        self.assertEqual(1, len(gate["evidence_refs"]))

    def test_link_intent_does_not_affect_gate_approval_status(self):
        self.run_cli("init", "--profile", "generic", provider=True)
        self._assign_authority("product_owner")
        self.run_cli("plan", "--task-id", "GI-6", "--task", "Create the service architecture", provider=True)
        before = self.load(".agentic-sdlc/runs/GI-6/run-record.json")
        gate_before = next(item for item in before["lifecycle_gates"] if item["gate_id"] == "G1")
        self._link_from_gitlab_issue("link-intent-from-gitlab-issue", task_id="GI-6", role="product_owner")
        after = self.load(".agentic-sdlc/runs/GI-6/run-record.json")
        gate_after = next(item for item in after["lifecycle_gates"] if item["gate_id"] == "G1")
        self.assertEqual(gate_before["status"], gate_after["status"])
        self.assertEqual([], gate_after["human_approvals"])

    def test_source_link_evidence_alone_can_never_satisfy_can_mark_gate_approved(self):
        # Regression pin for a design risk raised in review: can_mark_gate_approved
        # requires non-empty artifact_bindings/evidence_refs *and*
        # has_all_required_human_approvals(...). record_gitlab_issue_link never
        # touches human_approvals, so even in a hypothetical future where some
        # other code path populates artifact_bindings for G1/G2 (it never does
        # today), a gate whose only evidence is a source link -- no real human
        # approval -- must still be unapprovable. Constructs the gate directly
        # (not via the full CLI flow) specifically to isolate this from today's
        # incidental protection (artifact_bindings staying perpetually empty).
        authorities = {"product_owner": {"status": "assigned", "assignee": "github.com/owner"}}
        gate = {
            "gate_id": "G1",
            "status": "ready",
            "applicability": "applicable",
            "artifact_bindings": [{"artifact_id": "a", "revision": "1", "digest": "sha256:00"}],
            "evidence_refs": [{
                "evidence_id": "g1-source-gitlab-issue-42",
                "uri": "gitlab-issue:group/project:issues/42",
                "hash_algorithm": "sha256",
                "hash": "00",
                "classification": "internal",
            }],
            "independent_verifier": {"id": "reviewer", "role": "Reviewer", "kind": "human"},
            "independence_declaration": {"verifier_confirmed_not_preparer": True, "verifier_made_material_correction": False},
            "authority_requirements": [
                {"authority_id": "product_owner", "authority_type": "human-approver", "role": "Product Owner", "applicability": "applicable", "rationale": None}
            ],
            "human_approvals": [],
        }
        record = {"lifecycle_gates": [gate]}
        self.assertFalse(agentic_sdlc.can_mark_gate_approved(record, gate, authorities))

    def test_relinking_a_different_issue_replaces_rather_than_accumulates_evidence(self):
        self.run_cli("init", "--profile", "generic", provider=True)
        self._assign_authority("product_owner")
        self.run_cli("plan", "--task-id", "GI-8", "--task", "Create the service architecture", provider=True)
        self._link_from_gitlab_issue(
            "link-intent-from-gitlab-issue", task_id="GI-8", role="product_owner", issue_iid=42
        )
        self._link_from_gitlab_issue(
            "link-intent-from-gitlab-issue", task_id="GI-8", role="product_owner", issue_iid=99
        )
        record = self.load(".agentic-sdlc/runs/GI-8/run-record.json")
        self.assertEqual("gitlab-issue:group/project:issues/99", record["intent_record_id"])
        gate = next(item for item in record["lifecycle_gates"] if item["gate_id"] == "G1")
        self.assertEqual(1, len(gate["evidence_refs"]))
        self.assertEqual("gitlab-issue:group/project:issues/99", gate["evidence_refs"][0]["uri"])

    def test_reenter_clears_the_paired_source_link_field_along_with_its_evidence(self):
        self.run_cli("init", "--profile", "generic", provider=True)
        self._assign_authority("product_owner")
        self.run_cli("plan", "--task-id", "GI-9", "--task", "Create the service architecture", provider=True)
        self._link_from_gitlab_issue("link-intent-from-gitlab-issue", task_id="GI-9", role="product_owner")
        record = self.load(".agentic-sdlc/runs/GI-9/run-record.json")
        self.assertIsNotNone(record["intent_record_id"])
        self.run_cli(
            "invalidate", "--task-id", "GI-9", "--earliest-gate", "G1",
            "--reason", "Intent changed", "--actor", "product-owner",
        )
        self.run_cli(
            "reenter", "--task-id", "GI-9", "--earliest-gate", "G1",
            "--reason", "Intent changed", "--actor", "product-owner",
        )
        record = self.load(".agentic-sdlc/runs/GI-9/run-record.json")
        gate = next(item for item in record["lifecycle_gates"] if item["gate_id"] == "G1")
        self.assertEqual([], gate["evidence_refs"])
        self.assertIsNone(record["intent_record_id"])

    def test_validate_flags_malformed_gitlab_issue_uri(self):
        self.run_cli("init", "--profile", "generic", provider=True)
        self._assign_authority("product_owner")
        self.run_cli("plan", "--task-id", "GI-7", "--task", "Create the service architecture", provider=True)
        self._link_from_gitlab_issue("link-intent-from-gitlab-issue", task_id="GI-7", role="product_owner")
        record_relative = ".agentic-sdlc/runs/GI-7/run-record.json"
        record_path = self.root / record_relative
        record = self.load(record_relative)
        gate = next(item for item in record["lifecycle_gates"] if item["gate_id"] == "G1")
        gate["evidence_refs"][0]["uri"] = "gitlab-issue:malformed-uri-missing-fields"
        record_path.write_text(json.dumps(record), encoding="utf-8")
        result = self.run_cli("validate", provider=True, expected=1)
        self.assertTrue(
            any("invalid GitLab issue URI" in error for error in result["errors"]),
            result["errors"],
        )

    def test_parse_github_issue_uri(self):
        parsed = agentic_sdlc.parse_github_issue_uri("github-issue:owner/project:issues/42")
        self.assertEqual({"owner": "owner", "repo": "project", "number": "42"}, parsed)
        self.assertIsNone(agentic_sdlc.parse_github_issue_uri("github-issue:missing-fields"))
        self.assertIsNone(agentic_sdlc.parse_github_issue_uri("gitlab-issue:group/project:issues/42"))

    def test_fetch_github_issue_via_mock(self):
        mock_path = self.root / "github-issue-mock.json"
        mock_path.write_text(json.dumps({
            "number": 42,
            "title": "Support SSO login for enterprise customers",
            "state": "open",
            "html_url": "https://github.com/owner/project/issues/42",
            "updated_at": "2030-01-01T00:00:00Z",
        }), encoding="utf-8")
        with mock.patch.dict(os.environ, {"AGENTIC_SDLC_TEST_GITHUB_ISSUE_FILE": str(mock_path)}):
            issue = agentic_sdlc.fetch_github_issue("owner/project", 42)
        self.assertEqual(
            {
                "iid": 42,
                "title": "Support SSO login for enterprise customers",
                "state": "open",
                "web_url": "https://github.com/owner/project/issues/42",
                "updated_at": "2030-01-01T00:00:00Z",
            },
            issue,
        )

    def test_fetch_github_issue_rejects_missing_title(self):
        mock_path = self.root / "github-issue-bad.json"
        mock_path.write_text(json.dumps({"number": 99, "state": "open"}), encoding="utf-8")
        with mock.patch.dict(os.environ, {"AGENTIC_SDLC_TEST_GITHUB_ISSUE_FILE": str(mock_path)}):
            with self.assertRaises(ValueError):
                agentic_sdlc.fetch_github_issue("owner/project", 99)

    def test_fetch_github_issue_surfaces_gh_api_failure(self):
        # Exercises the real (non-mock) subprocess path: a nonzero `gh api`
        # exit code must raise a ValueError naming the repo/issue, not
        # propagate a raw CalledProcessError or crash on invalid JSON.
        failed = subprocess.CompletedProcess(args=["gh"], returncode=1, stdout="", stderr="404 Not Found")
        environ_without_mock_file = dict(os.environ)
        environ_without_mock_file.pop("AGENTIC_SDLC_TEST_GITHUB_ISSUE_FILE", None)
        with mock.patch.dict(os.environ, environ_without_mock_file, clear=True):
            with mock.patch("agentic_sdlc.subprocess.run", return_value=failed) as run_mock:
                with self.assertRaises(ValueError) as raised:
                    agentic_sdlc.fetch_github_issue("owner/project", 404)
        run_mock.assert_called_once_with(
            ["gh", "api", "repos/owner/project/issues/404"],
            text=True,
            capture_output=True,
            check=False,
        )
        self.assertIn("unable to fetch GitHub issue for owner/project issue 404", str(raised.exception))
        self.assertIn("404 Not Found", str(raised.exception))

    def _link_from_github_issue(self, command, **overrides):
        mock_path = self.root / "github-issue-link-mock.json"
        mock_path.write_text(json.dumps({
            "number": overrides.get("issue_number", 42),
            "title": overrides.get("title", "Support SSO login for enterprise customers"),
            "state": overrides.get("state", "open"),
            "html_url": "https://github.com/owner/project/issues/42",
            "updated_at": "2030-01-01T00:00:00Z",
        }), encoding="utf-8")
        return self.run_cli(
            command,
            "--task-id", overrides["task_id"],
            "--role", overrides["role"],
            "--repo", overrides.get("repo", "owner/project"),
            "--issue-number", str(overrides.get("issue_number", 42)),
            expected=overrides.get("expected", 0),
            env={"AGENTIC_SDLC_TEST_GITHUB_ISSUE_FILE": str(mock_path)},
        )

    def test_link_intent_from_github_issue_records_evidence_and_sets_intent_record_id(self):
        self.run_cli("init", "--profile", "generic", provider=True)
        self._assign_authority("product_owner")
        self.run_cli("plan", "--task-id", "GHI-1", "--task", "Create the service architecture", provider=True)
        result = self._link_from_github_issue(
            "link-intent-from-github-issue", task_id="GHI-1", role="product_owner"
        )
        expected_uri = "github-issue:owner/project:issues/42"
        self.assertEqual(expected_uri, result["issue_uri"])
        self.assertEqual("intent_record_id", result["record_field"])
        record = self.load(".agentic-sdlc/runs/GHI-1/run-record.json")
        self.assertEqual(expected_uri, record["intent_record_id"])
        self.assertIsNone(record["requirements_baseline_id"])
        gate = next(item for item in record["lifecycle_gates"] if item["gate_id"] == "G1")
        evidence = gate["evidence_refs"][0]
        self.assertEqual(expected_uri, evidence["uri"])
        self.assertEqual("sha256", evidence["hash_algorithm"])
        self.assertEqual("g1-source-github-issue-42", evidence["evidence_id"])

    def test_link_requirements_from_github_issue_records_evidence_and_sets_requirements_baseline_id(self):
        self.run_cli("init", "--profile", "generic", provider=True)
        self._assign_authority("engineering_lead")
        self.run_cli("plan", "--task-id", "GHI-2", "--task", "Create the service architecture", provider=True)
        result = self._link_from_github_issue(
            "link-requirements-from-github-issue", task_id="GHI-2", role="engineering_lead"
        )
        expected_uri = "github-issue:owner/project:issues/42"
        self.assertEqual("requirements_baseline_id", result["record_field"])
        record = self.load(".agentic-sdlc/runs/GHI-2/run-record.json")
        self.assertEqual(expected_uri, record["requirements_baseline_id"])
        self.assertIsNone(record["intent_record_id"])
        gate = next(item for item in record["lifecycle_gates"] if item["gate_id"] == "G2")
        self.assertEqual(expected_uri, gate["evidence_refs"][0]["uri"])

    def test_link_intent_from_github_issue_rejects_unassigned_authority(self):
        self.run_cli("init", "--profile", "generic", provider=True)
        self.run_cli("plan", "--task-id", "GHI-3", "--task", "Create the service architecture", provider=True)
        result = self._link_from_github_issue(
            "link-intent-from-github-issue", task_id="GHI-3", role="product_owner", expected=1
        )
        self.assertIn("is not applicable", result["error"])

    def test_link_intent_from_github_issue_rejects_role_not_required_by_gate(self):
        self.run_cli("init", "--profile", "generic", provider=True)
        self._assign_authority("engineering_lead")
        self.run_cli("plan", "--task-id", "GHI-4", "--task", "Create the service architecture", provider=True)
        result = self._link_from_github_issue(
            "link-intent-from-github-issue", task_id="GHI-4", role="engineering_lead", expected=1
        )
        self.assertIn("does not require authority role", result["error"])

    def test_record_github_issue_link_rejects_gates_other_than_g1_g2(self):
        # The CLI subcommands hardcode G1/G2 (mirroring the GitLab ones), so
        # this rejection is only reachable by calling the underlying
        # recording function directly with an out-of-range gate id.
        self.run_cli("init", "--profile", "generic", provider=True)
        self._assign_authority("product_owner")
        self.run_cli("plan", "--task-id", "GHI-5", "--task", "Create the service architecture", provider=True)
        with self.assertRaises(ValueError) as raised:
            agentic_sdlc.record_github_issue_link(
                self.root, "GHI-5", "G3", "product_owner", "owner/project",
                {"iid": 42, "title": "T", "state": "open", "web_url": None},
            )
        self.assertIn("does not accept a GitHub issue source link", str(raised.exception))

    def test_link_intent_from_github_issue_is_idempotent_on_relink(self):
        self.run_cli("init", "--profile", "generic", provider=True)
        self._assign_authority("product_owner")
        self.run_cli("plan", "--task-id", "GHI-6", "--task", "Create the service architecture", provider=True)
        self._link_from_github_issue("link-intent-from-github-issue", task_id="GHI-6", role="product_owner")
        self._link_from_github_issue("link-intent-from-github-issue", task_id="GHI-6", role="product_owner")
        record = self.load(".agentic-sdlc/runs/GHI-6/run-record.json")
        gate = next(item for item in record["lifecycle_gates"] if item["gate_id"] == "G1")
        self.assertEqual(1, len(gate["evidence_refs"]))

    def test_link_intent_from_github_issue_does_not_affect_gate_approval_status(self):
        # Regression pin for the confirmed invariant: linking a source issue
        # must not touch human_approvals or gate.status. Compares the full
        # gate object before/after (not just status), so any incidental
        # field this adapter should never touch is caught, not only status.
        self.run_cli("init", "--profile", "generic", provider=True)
        self._assign_authority("product_owner")
        self.run_cli("plan", "--task-id", "GHI-7", "--task", "Create the service architecture", provider=True)
        before = self.load(".agentic-sdlc/runs/GHI-7/run-record.json")
        gate_before = next(item for item in before["lifecycle_gates"] if item["gate_id"] == "G1")
        self._link_from_github_issue("link-intent-from-github-issue", task_id="GHI-7", role="product_owner")
        after = self.load(".agentic-sdlc/runs/GHI-7/run-record.json")
        gate_after = next(item for item in after["lifecycle_gates"] if item["gate_id"] == "G1")
        self.assertEqual(gate_before["status"], gate_after["status"])
        self.assertEqual([], gate_after["human_approvals"])
        self.assertIsNone(gate_before.get("decided_at"))
        self.assertIsNone(gate_after.get("decided_at"))
        # Every other gate (G2-G10) must be completely untouched.
        for gate_id in ("G2", "G3", "G4", "G5", "G6", "G7", "G8", "G9", "G10"):
            gate_before_other = next(item for item in before["lifecycle_gates"] if item["gate_id"] == gate_id)
            gate_after_other = next(item for item in after["lifecycle_gates"] if item["gate_id"] == gate_id)
            self.assertEqual(gate_before_other, gate_after_other, gate_id)

    def test_relinking_a_different_github_issue_replaces_rather_than_accumulates_evidence(self):
        self.run_cli("init", "--profile", "generic", provider=True)
        self._assign_authority("product_owner")
        self.run_cli("plan", "--task-id", "GHI-8", "--task", "Create the service architecture", provider=True)
        self._link_from_github_issue(
            "link-intent-from-github-issue", task_id="GHI-8", role="product_owner", issue_number=42
        )
        self._link_from_github_issue(
            "link-intent-from-github-issue", task_id="GHI-8", role="product_owner", issue_number=99
        )
        record = self.load(".agentic-sdlc/runs/GHI-8/run-record.json")
        self.assertEqual("github-issue:owner/project:issues/99", record["intent_record_id"])
        gate = next(item for item in record["lifecycle_gates"] if item["gate_id"] == "G1")
        self.assertEqual(1, len(gate["evidence_refs"]))
        self.assertEqual("github-issue:owner/project:issues/99", gate["evidence_refs"][0]["uri"])

    def test_reenter_clears_the_paired_github_source_link_field_along_with_its_evidence(self):
        self.run_cli("init", "--profile", "generic", provider=True)
        self._assign_authority("product_owner")
        self.run_cli("plan", "--task-id", "GHI-9", "--task", "Create the service architecture", provider=True)
        self._link_from_github_issue("link-intent-from-github-issue", task_id="GHI-9", role="product_owner")
        record = self.load(".agentic-sdlc/runs/GHI-9/run-record.json")
        self.assertIsNotNone(record["intent_record_id"])
        self.run_cli(
            "invalidate", "--task-id", "GHI-9", "--earliest-gate", "G1",
            "--reason", "Intent changed", "--actor", "product-owner",
        )
        self.run_cli(
            "reenter", "--task-id", "GHI-9", "--earliest-gate", "G1",
            "--reason", "Intent changed", "--actor", "product-owner",
        )
        record = self.load(".agentic-sdlc/runs/GHI-9/run-record.json")
        gate = next(item for item in record["lifecycle_gates"] if item["gate_id"] == "G1")
        self.assertEqual([], gate["evidence_refs"])
        self.assertIsNone(record["intent_record_id"])

    def test_validate_flags_malformed_github_issue_uri(self):
        self.run_cli("init", "--profile", "generic", provider=True)
        self._assign_authority("product_owner")
        self.run_cli("plan", "--task-id", "GHI-10", "--task", "Create the service architecture", provider=True)
        self._link_from_github_issue("link-intent-from-github-issue", task_id="GHI-10", role="product_owner")
        record_relative = ".agentic-sdlc/runs/GHI-10/run-record.json"
        record_path = self.root / record_relative
        record = self.load(record_relative)
        gate = next(item for item in record["lifecycle_gates"] if item["gate_id"] == "G1")
        gate["evidence_refs"][0]["uri"] = "github-issue:malformed-uri-missing-fields"
        record_path.write_text(json.dumps(record), encoding="utf-8")
        result = self.run_cli("validate", provider=True, expected=1)
        self.assertTrue(
            any("invalid GitHub issue URI" in error for error in result["errors"]),
            result["errors"],
        )

    def test_link_intent_from_github_and_gitlab_issue_are_mutually_exclusive_cli_subcommands(self):
        # CLI wiring pin: both new subcommands are registered independently
        # and each carries its own required flags (--repo/--issue-number for
        # GitHub vs --project-path/--issue-iid for GitLab) -- one command's
        # flags must not be silently accepted by the other.
        result = subprocess.run(
            CLI_COMMAND + ["link-intent-from-github-issue", "--task-id", "X", "--role", "product_owner",
                           "--project-path", "group/project", "--issue-iid", "1", "--root", str(self.root)],
            text=True, capture_output=True, check=False, env=cli_env(),
        )
        self.assertNotEqual(0, result.returncode)
        self.assertIn("--repo", result.stderr)


class DevEntrypointCwdIsolationTests(unittest.TestCase):
    """dev_entrypoint.py must resolve the real kernel package regardless of
    what the caller's own current working directory contains -- this is
    exactly the failure mode a `python3 -m agentic_sdlc` invocation would
    have (its cwd lands at sys.path[0], ahead of any PYTHONPATH-prepended
    entry), which dev_entrypoint.py avoids by putting its own file location
    at sys.path[0] instead (see that file's docstring)."""

    def test_a_colliding_top_level_module_in_the_callers_cwd_is_not_shadowed(self):
        with tempfile.TemporaryDirectory() as temporary:
            cwd = Path(temporary)
            # A decoy that would break everything if it were ever imported
            # instead of the real kernel package.
            (cwd / "agentic_sdlc.py").write_text(
                "raise RuntimeError('the decoy module was imported instead of the real package')\n",
                encoding="utf-8",
            )
            result = subprocess.run(
                CLI_COMMAND + ["--version"],
                cwd=cwd,
                text=True,
                capture_output=True,
                check=False,
                env=cli_env(),
            )
            self.assertEqual(0, result.returncode, result.stderr)
            self.assertEqual(agentic_sdlc.VERSION, result.stdout.strip())


class AgentCatalogSchemaTests(unittest.TestCase):
    """Validates `agent-catalog.schema.json`'s `transport`/`endpoint`
    extension (added for A2A protocol support): both fields are optional
    (so existing catalogs, e.g. `providers/agentic-sdlc-defaults`'s, need
    no changes), but `transport: "a2a"` requires `endpoint`, and
    `additionalProperties: false` still rejects unknown fields.
    """

    @classmethod
    def setUpClass(cls):
        import jsonschema  # type: ignore

        cls.jsonschema = jsonschema
        schema_path = PLUGIN_ROOT / "contracts" / "agent-catalog.schema.json"
        cls.schema = json.loads(schema_path.read_text(encoding="utf-8"))
        cls.validator = jsonschema.Draft202012Validator(cls.schema)

    def assert_valid(self, catalog):
        errors = list(self.validator.iter_errors(catalog))
        self.assertEqual([], errors, [error.message for error in errors])

    def assert_invalid(self, catalog):
        errors = list(self.validator.iter_errors(catalog))
        self.assertNotEqual([], errors)

    def test_default_provider_catalog_is_unaffected_by_the_new_optional_fields(self):
        default_provider_catalog = (
            PLUGIN_ROOT.parent / "providers" / "agentic-sdlc-defaults" / "agent-catalog.json"
        )
        catalog = json.loads(default_provider_catalog.read_text(encoding="utf-8"))
        self.assert_valid(catalog)

    def test_local_transport_entry_without_endpoint_is_valid(self):
        self.assert_valid(
            {
                "schema_version": 1,
                "agents": {
                    "local-author": {
                        "kind": "author",
                        "capabilities": ["author"],
                        "transport": "local",
                    }
                },
            }
        )

    def test_a2a_transport_entry_with_endpoint_is_valid(self):
        self.assert_valid(
            {
                "schema_version": 1,
                "agents": {
                    "external-reviewer": {
                        "kind": "reviewer",
                        "capabilities": ["reviewer"],
                        "transport": "a2a",
                        "endpoint": "https://codex-agent.example.com",
                    }
                },
            }
        )

    def test_a2a_transport_entry_missing_endpoint_is_invalid(self):
        self.assert_invalid(
            {
                "schema_version": 1,
                "agents": {
                    "external-reviewer": {
                        "kind": "reviewer",
                        "capabilities": ["reviewer"],
                        "transport": "a2a",
                    }
                },
            }
        )

    def test_unknown_agent_field_is_still_rejected(self):
        self.assert_invalid(
            {
                "schema_version": 1,
                "agents": {
                    "typo-agent": {
                        "kind": "author",
                        "capabilities": ["author"],
                        "trasnport": "local",
                    }
                },
            }
        )


if __name__ == "__main__":
    unittest.main()
