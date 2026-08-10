"""Tests for the declarative runner-capability manifest (idea #8,
REQ-CADRE-BACKLOG-8, `roster/orchestration/runs/cadre-idea-8-capability-manifest-2026-07-29/`).

Traces to requirements.md's acceptance criteria AC-1..AC-9. Each test method
below names the AC(s) it covers in its docstring.
"""

from __future__ import annotations

import copy
import importlib.util
import json
import re
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

ROOT = Path(__file__).resolve().parent.parent
REPOSITORY_ROOT = ROOT.parent.parent
sys.path.insert(0, str(ROOT / "src"))

import generate_global_plugin as ggp  # noqa: E402
import generate_role_metadata as grm  # noqa: E402

try:
    import jsonschema  # noqa: F401

    JSONSCHEMA_AVAILABLE = True
except ImportError:
    JSONSCHEMA_AVAILABLE = False

if JSONSCHEMA_AVAILABLE:
    import validate_runner_capabilities as vrc  # noqa: E402

MANIFEST_PATH = REPOSITORY_ROOT / "roster" / "runner-capabilities.json"
SCHEMA_PATH = REPOSITORY_ROOT / "roster" / "runner-capabilities.schema.json"
CATALOG_SCHEMA_PATH = REPOSITORY_ROOT / "roster" / "catalog.schema.json"
RUNNER_ADAPTERS_PATH = (
    REPOSITORY_ROOT / ".agents" / "skills" / "run-agent-orchestration" / "references" / "runner-adapters.md"
)
CLINE_AGENTS_DIR = REPOSITORY_ROOT / "cline-plugins" / "cline-agents"
CLINE_AGENTS_PRESETS_DIR = CLINE_AGENTS_DIR / "agents"
CLINE_AGENTS_INDEX_TS = CLINE_AGENTS_DIR / "index.ts"


def _load_manifest() -> dict:
    return json.loads(MANIFEST_PATH.read_text(encoding="utf-8"))


def _load_catalog() -> dict:
    import yaml  # local import: only this helper needs it

    return yaml.safe_load((REPOSITORY_ROOT / "roster" / "catalog.yaml").read_text(encoding="utf-8"))


def _catalog_role_ids(catalog: dict) -> list[str]:
    return list(catalog["agents"])


class ManifestExistenceAndContentTests(unittest.TestCase):
    """AC-1: a single named artifact exists that declares, for all 5
    capability tiers, tools/sandbox_mode matching CAPABILITY_PROFILES
    verbatim, and for all 3 model tiers, codex_model/reasoning_effort
    matching TIER_MAP verbatim.
    """

    def test_manifest_file_and_schema_exist(self) -> None:
        self.assertTrue(MANIFEST_PATH.is_file())
        self.assertTrue(SCHEMA_PATH.is_file())

    def test_manifest_capability_tiers_match_generator_constants_verbatim(self) -> None:
        manifest = _load_manifest()
        expected = {
            tier: {"tools": data["tools"], "sandbox_mode": data["sandbox_mode"]}
            for tier, data in manifest["capability_tiers"].items()
        }
        self.assertEqual(expected, ggp.CAPABILITY_PROFILES)
        self.assertEqual(
            {"read_only", "document_author", "code_author", "test_author", "environment_operator"},
            set(ggp.CAPABILITY_PROFILES),
        )

    def test_manifest_model_tiers_match_tier_map_verbatim(self) -> None:
        manifest = _load_manifest()
        expected = {
            tier: (data["codex_model"], data["reasoning_effort"]) for tier, data in manifest["model_tiers"].items()
        }
        self.assertEqual(expected, grm.TIER_MAP)
        self.assertEqual({"opus", "sonnet", "haiku"}, set(grm.TIER_MAP))

    def test_manifest_reproduces_catalog_schema_enums_without_hand_copying(self) -> None:
        """CM-FR-4 / gap G-3: roster/catalog.schema.json's capability/model/
        codex_model/reasoning_effort enum lists must be checked against this
        manifest's own data, not an independent fifth hand-copied location.
        """
        manifest = _load_manifest()
        catalog_schema = json.loads(CATALOG_SCHEMA_PATH.read_text(encoding="utf-8"))
        role_defs = catalog_schema["$defs"]["role"]["properties"]

        self.assertEqual(set(role_defs["capability"]["enum"]), set(manifest["capability_tiers"]))
        self.assertEqual(set(role_defs["model"]["enum"]), set(manifest["model_tiers"]))
        self.assertEqual(
            {data["codex_model"] for data in manifest["model_tiers"].values()},
            set(role_defs["codex_model"]["enum"]),
        )
        self.assertEqual(set(manifest["allowed_reasoning_efforts"]), set(role_defs["reasoning_effort"]["enum"]))


class GeneratedFromManifestTests(unittest.TestCase):
    """AC-2 / CM-NFR-5: generate_global_plugin.py's/generate_role_metadata.py's
    capability/model-tier constants are *generated from* the manifest (not
    merely checked against a second hand-authored copy) -- demonstrated by
    varying the manifest content fed to the loader and observing the derived
    constants change accordingly, and by showing a malformed/incomplete
    manifest fails closed rather than silently falling back to stale values.
    """

    def test_capability_profiles_reflect_a_fixture_manifest(self) -> None:
        fixture = {
            "capability_tiers": {
                "throwaway_tier": {"tools": ["Read"], "sandbox_mode": "read-only"},
            },
            "model_tiers": {},
            "allowed_reasoning_efforts": [],
        }
        profiles = ggp._capability_profiles_from_manifest(fixture, Path("fixture.json"))
        self.assertEqual(
            {"throwaway_tier": {"tools": ["Read"], "sandbox_mode": "read-only"}},
            profiles,
        )

    def test_changing_one_tiers_sandbox_mode_in_a_fixture_changes_only_that_tier(self) -> None:
        """AC-3: adding/changing one tier requires editing exactly the
        manifest -- demonstrated here by mutating a single field on a
        fixture copy and confirming the derived profile reflects exactly
        that one change, with no other Python file touched.
        """
        manifest = copy.deepcopy(_load_manifest())
        manifest["capability_tiers"]["read_only"]["sandbox_mode"] = "danger-full-access"
        profiles = ggp._capability_profiles_from_manifest(manifest, MANIFEST_PATH)
        self.assertEqual("danger-full-access", profiles["read_only"]["sandbox_mode"])
        for tier in ("document_author", "code_author", "test_author", "environment_operator"):
            self.assertEqual("workspace-write", profiles[tier]["sandbox_mode"])

    def test_manifest_missing_capability_tiers_key_fails_closed(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            broken = Path(directory) / "runner-capabilities.json"
            broken.write_text(json.dumps({"model_tiers": {}, "allowed_reasoning_efforts": []}), encoding="utf-8")
            with self.assertRaisesRegex(ggp.ManifestError, "capability_tiers"):
                ggp._load_runner_capabilities(broken)

    def test_manifest_tier_missing_sandbox_mode_fails_closed(self) -> None:
        fixture = {"capability_tiers": {"read_only": {"tools": ["Read"]}}}
        with self.assertRaisesRegex(ggp.ManifestError, "read_only"):
            ggp._capability_profiles_from_manifest(fixture, Path("fixture.json"))

    def test_missing_manifest_file_fails_closed(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            missing = Path(directory) / "does-not-exist.json"
            with self.assertRaisesRegex(ggp.ManifestError, "not found"):
                ggp._load_runner_capabilities(missing)

    def test_invalid_json_fails_closed(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            broken = Path(directory) / "runner-capabilities.json"
            broken.write_text("{not valid json", encoding="utf-8")
            with self.assertRaisesRegex(ggp.ManifestError, "invalid JSON"):
                ggp._load_runner_capabilities(broken)

    @unittest.skipUnless(JSONSCHEMA_AVAILABLE, "jsonschema is not installed in this environment")
    def test_scratch_manifest_divergence_from_schema_is_a_failing_check(self) -> None:
        """AC-8: a scratch/fixture divergence between the manifest and its
        contract produces a non-zero-exit-equivalent failure, not a silent
        pass, runnable under this same `unittest discover` invocation.
        """
        manifest = copy.deepcopy(_load_manifest())
        manifest["capability_tiers"]["read_only"]["sandbox_mode"] = 12345  # wrong type
        schema = json.loads(SCHEMA_PATH.read_text(encoding="utf-8"))
        findings = vrc.validate(manifest, schema)
        self.assertTrue(findings, "expected schema validation to report the injected type mismatch")

    @unittest.skipUnless(JSONSCHEMA_AVAILABLE, "jsonschema is not installed in this environment")
    def test_real_manifest_is_schema_clean(self) -> None:
        findings = vrc.run()
        self.assertEqual([], findings)


class RunnerAdaptersStructuralFactCoverageTests(unittest.TestCase):
    """AC-4: all 8 structural facts enumerated in requirements.md's table
    are present in the manifest with correct current values, verified
    side-by-side against runner-adapters.md's current prose.
    """

    def setUp(self) -> None:
        self.manifest = _load_manifest()
        self.runners = self.manifest["runners"]
        self.prose = RUNNER_ADAPTERS_PATH.read_text(encoding="utf-8")

    # Fact 1/2: generated wrapper existence + dispatch naming.
    def test_fact_1_and_2_generated_wrapper_and_dispatch_naming(self) -> None:
        self.assertTrue(self.runners["claude-code"]["has_generated_wrapper"])
        self.assertTrue(self.runners["codex"]["has_generated_wrapper"])
        self.assertTrue(self.runners["cline"]["has_generated_wrapper"])
        self.assertIn("agents:<role-id>", self.runners["claude-code"]["dispatch_naming"])
        self.assertIn(".codex/agents/<role-id>.toml", self.runners["codex"]["dispatch_naming"])
        self.assertIn("start_subagent", self.runners["cline"]["dispatch_naming"])
        self.assertIn("preset", self.runners["cline"]["dispatch_naming"])

    def test_cline_generated_wrapper_claim_is_grounded_in_a_real_committed_preset_per_role(self) -> None:
        """The manifest's claim of a generated wrapper for Cline is only as
        good as the committed artifact backing it -- assert against the
        actual preset directory `port_cline_agents.py` produces
        (drift-guarded byte-for-byte by
        `plugin/tools/test_port_cline_agents.py`), rather than restating the
        manifest's own boolean back at itself.
        """
        self.assertTrue(CLINE_AGENTS_PRESETS_DIR.is_dir())
        preset_files = sorted(p.stem for p in CLINE_AGENTS_PRESETS_DIR.glob("*.md"))
        self.assertTrue(preset_files, "expected at least one bundled Cline agent preset")

        catalog = _load_catalog()
        catalog_role_ids = sorted(_catalog_role_ids(catalog))
        self.assertEqual(
            catalog_role_ids,
            preset_files,
            "cline-plugins/cline-agents/agents/*.md must carry one preset per catalog role",
        )

    def test_cline_dispatch_naming_claim_is_grounded_in_index_ts(self) -> None:
        """Assert the `preset` argument name and the tool that consumes it
        are real identifiers in `cline-plugins/cline-agents/index.ts`, not
        just prose repeated in the manifest.
        """
        source = CLINE_AGENTS_INDEX_TS.read_text(encoding="utf-8")
        self.assertIn('name: "start_subagent"', source)
        self.assertIn("preset: NonEmptyText", source)

    # Fact 3/4/5: peer communication_mode support, gate, nested teams, team size.
    def test_fact_3_4_5_communication_mode_and_team_shape(self) -> None:
        self.assertEqual("gated", self.runners["claude-code"]["communication_mode_peer_support"])
        self.assertEqual("CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1", self.runners["claude-code"]["peer_support_gate"])
        self.assertIn("CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1", self.prose)
        self.assertFalse(self.runners["claude-code"]["nested_teams_supported"])
        self.assertEqual(3, self.runners["claude-code"]["team_size_guidance"]["minimum"])
        self.assertEqual(5, self.runners["claude-code"]["team_size_guidance"]["maximum"])

        self.assertEqual("no", self.runners["codex"]["communication_mode_peer_support"])
        self.assertEqual("not_applicable", self.runners["codex"]["nested_teams_supported"])

        self.assertEqual("best_effort", self.runners["cline"]["communication_mode_peer_support"])
        self.assertEqual("not_applicable", self.runners["cline"]["nested_teams_supported"])

    # Fact 6/7: named-agent dispatch support and workaround.
    def test_fact_6_7_named_dispatch_and_workaround(self) -> None:
        self.assertTrue(self.runners["claude-code"]["named_agent_dispatch_supported"])
        self.assertIsNone(self.runners["claude-code"]["named_agent_dispatch_workaround"])

        self.assertFalse(self.runners["codex"]["named_agent_dispatch_supported"])
        self.assertIn("dispatch_secure_cloud_role", self.runners["codex"]["named_agent_dispatch_workaround"])
        self.assertIn("dispatch_secure_cloud_role", self.prose)

        self.assertTrue(self.runners["cline"]["named_agent_dispatch_supported"])
        self.assertIsNone(self.runners["cline"]["named_agent_dispatch_workaround"])

    def test_cline_named_dispatch_claim_is_grounded_in_index_ts_tool_registrations(self) -> None:
        """Both MCP tools the manifest's dispatch_naming/named_agent_dispatch
        claims rely on -- `start_subagent` (direct named-preset dispatch)
        and `dispatch_selected_roles` (fan-out across a `cadre select`
        plan's staffed roles) -- must actually be registered tools in the
        shipped plugin, not just described in the manifest.
        """
        source = CLINE_AGENTS_INDEX_TS.read_text(encoding="utf-8")
        self.assertIn('name: "start_subagent"', source)
        self.assertIn('name: "dispatch_selected_roles"', source)
        self.assertIn("Unknown agent preset", source)  # named dispatch fails closed on an unknown preset

    # Fact 8: concurrency bound config key.
    def test_fact_8_concurrency_bound(self) -> None:
        codex_bound = self.runners["codex"]["concurrency_bound_config_key"]
        self.assertIn("agents.max_concurrent_threads_per_session", codex_bound)
        self.assertIn("MAX_CONCURRENT_CHILDREN", codex_bound)
        self.assertIn("agents.max_concurrent_threads_per_session", self.prose)
        self.assertIn("MAX_CONCURRENT_CHILDREN", self.prose)
        self.assertIsNone(self.runners["claude-code"]["concurrency_bound_config_key"])
        self.assertIsNone(self.runners["cline"]["concurrency_bound_config_key"])

    # Fact 9: native workspace isolation (roster/shared/workspace-isolation.md).
    def test_fact_9_native_workspace_isolation(self) -> None:
        self.assertEqual("worktree", self.runners["claude-code"]["native_workspace_isolation"])
        self.assertIsNone(self.runners["codex"]["native_workspace_isolation"])
        self.assertIsNone(self.runners["cline"]["native_workspace_isolation"])
        self.assertIn("native_workspace_isolation", self.prose)

    def test_native_workspace_isolation_has_no_runtime_consumer(self) -> None:
        """This field is build-time descriptive data only (see idea #8's
        OD-2 disposition, matching every other field on this manifest) --
        no dispatch-time code path may branch on it.

        Covers all three plausible runtime consumers, not just one: the MCP
        dispatch server, the dispatch-plan builder, and the selector CLI.
        Grepping only dispatch_core.py would let a future branch in
        build_dispatch_plan.py or select_agents.py regress OD-2 silently.
        """
        runtime_consumers = (
            REPOSITORY_ROOT / "roster" / "orchestration" / "mcp" / "dispatch_core.py",
            REPOSITORY_ROOT / "roster" / "orchestration" / "src" / "build_dispatch_plan.py",
            REPOSITORY_ROOT / "roster" / "orchestration" / "src" / "select_agents.py",
        )
        for path in runtime_consumers:
            self.assertTrue(path.is_file(), f"expected runtime-consumer file is missing: {path}")
            self.assertNotIn(
                "native_workspace_isolation",
                path.read_text(encoding="utf-8"),
                f"{path.name} branches on a build-time-only manifest field (idea #8 OD-2)",
            )


class NarrativeContentUndisturbedTests(unittest.TestCase):
    """AC-5: runner-adapters.md's narrative/investigative paragraphs remain
    present, unmodified/undeleted, after the manifest ships.
    """

    def test_narrative_paragraphs_still_present(self) -> None:
        prose = RUNNER_ADAPTERS_PATH.read_text(encoding="utf-8")
        expected_markers = [
            "openai/codex#15250",
            "ChatGPT-authenticated Codex session can reject",
            "A2A was evaluated as a fix for this exact limitation and rejected",
            "AgentExtensionApi",
            "cline/cline#11435",
            "will go stale",
            "pip install -r roster/orchestration/mcp/requirements-mcp.txt",
        ]
        for marker in expected_markers:
            with self.subTest(marker=marker):
                self.assertIn(marker, prose)


class ClineScopeRespectedTests(unittest.TestCase):
    """AC-6: the manifest declares only capability facts actually backed by
    shipped, drift-guarded artifacts in this repository (the 86 committed
    `cline-plugins/cline-agents/agents/*.md` presets and the `start_subagent`/
    `dispatch_selected_roles` MCP tools in `cline-plugins/cline-agents/
    index.ts`), and does not fabricate a `tools`/`sandbox_mode` grant --
    per-tool policy enforcement for Cline is real (see
    `resolveToolPolicyConfig` in `index.ts`) but is derived per-preset from
    each preset's own `allowedTools` frontmatter, not from this manifest, so
    this manifest correctly carries no `tools`/`sandbox_mode` key for Cline.
    """

    def test_cline_has_no_tools_or_sandbox_mode_grant(self) -> None:
        manifest = _load_manifest()
        cline = manifest["runners"]["cline"]
        self.assertNotIn("tools", cline)
        self.assertNotIn("sandbox_mode", cline)
        self.assertTrue(cline["has_generated_wrapper"])
        self.assertTrue(cline["named_agent_dispatch_supported"])
        self.assertIsNone(cline["native_workspace_isolation"])

    def test_cline_index_ts_does_not_read_the_capability_manifest(self) -> None:
        """Companion grounding check for the class docstring's claim that
        per-preset tool policy comes from each preset's own `allowedTools`
        frontmatter, not from `roster/runner-capabilities.json`: the plugin
        source must not reference the manifest file at all.
        """
        source = CLINE_AGENTS_INDEX_TS.read_text(encoding="utf-8")
        self.assertNotIn("runner-capabilities.json", source)


class PackagingAllowlistParityTests(unittest.TestCase):
    """AC-7 (CM-NFR-6): the manifest and its schema are present in
    generate_global_plugin.py::generate_suite_copy's file-selection
    allowlist, demonstrated both positively (against a fixture repository)
    and negatively (a scratch removal of the allowlist entries produces a
    packaged copy missing the files -- the exact idea #10 gap class).
    """

    def test_source_declares_both_files_in_the_allowlist(self) -> None:
        source = (ROOT / "src" / "generate_global_plugin.py").read_text(encoding="utf-8")
        self.assertIn('"roster/runner-capabilities.json"', source)
        self.assertIn('"roster/runner-capabilities.schema.json"', source)

    def _init_git_repo(self, root: Path) -> None:
        subprocess.run(["git", "init", "-q", str(root)], check=True)
        subprocess.run(["git", "-C", str(root), "config", "user.email", "test@example.invalid"], check=True)
        subprocess.run(["git", "-C", str(root), "config", "user.name", "Test"], check=True)

    def test_real_generator_packages_the_manifest_and_schema(self) -> None:
        """Uses a fixture git repository (same pattern as
        test_generate_global_plugin.py) rather than this checkout's own
        REPOSITORY_ROOT, because generate_suite_copy() only ever selects
        `git ls-files`-tracked paths, and this task's own instructions
        prohibit `git add`ing the newly-created manifest files into this
        checkout's real index -- see the negative (scratch-removal)
        demonstration below for the other half of this same proof.
        """
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self._init_git_repo(root)
            (root / "roster").mkdir()
            (root / "roster" / "runner-capabilities.json").write_text(
                MANIFEST_PATH.read_text(encoding="utf-8"), encoding="utf-8"
            )
            (root / "roster" / "runner-capabilities.schema.json").write_text(
                SCHEMA_PATH.read_text(encoding="utf-8"), encoding="utf-8"
            )
            subprocess.run(["git", "-C", str(root), "add", "."], check=True)
            subprocess.run(["git", "-C", str(root), "commit", "-qm", "base"], check=True)
            plugin_root = root / "plugins" / "cadre"
            plugin_root.mkdir(parents=True)
            packaging_readme = root / "packaging" / "plugin-README.md"
            packaging_readme.parent.mkdir(parents=True)
            packaging_readme.write_text("template readme\n", encoding="utf-8")

            with mock.patch.object(ggp, "REPOSITORY_ROOT", root), mock.patch.object(
                ggp, "PACKAGING_README", packaging_readme
            ):
                ggp.generate_suite_copy({}, plugin_root)

            self.assertTrue((plugin_root / "suite" / "roster" / "runner-capabilities.json").is_file())
            self.assertTrue((plugin_root / "suite" / "roster" / "runner-capabilities.schema.json").is_file())
            packaged = json.loads(
                (plugin_root / "suite" / "roster" / "runner-capabilities.json").read_text(encoding="utf-8")
            )
            self.assertEqual(_load_manifest(), packaged)

    def _load_module_from_source(self, name: str, source: str, module_path: Path) -> object:
        module_path.parent.mkdir(parents=True, exist_ok=True)
        module_path.write_text(source, encoding="utf-8")
        spec = importlib.util.spec_from_file_location(name, module_path)
        module = importlib.util.module_from_spec(spec)
        assert spec.loader is not None
        spec.loader.exec_module(module)
        return module

    def test_scratch_removal_from_allowlist_breaks_the_packaged_copy(self) -> None:
        """Mirrors the exact idea #10 review-caught gap: patch a scratch
        copy of generate_suite_copy's source to omit the manifest allowlist
        entries, run it against a fixture repository that *does* carry
        roster/runner-capabilities.json, and confirm the packaged output
        silently lacks the file -- proving the real allowlist entry is
        load-bearing, not decorative.
        """
        real_source = (ROOT / "src" / "generate_global_plugin.py").read_text(encoding="utf-8")
        marker = (
            '"roster/runner-capabilities.json",\n'
            '            "roster/runner-capabilities.schema.json",\n'
        )
        self.assertIn(marker, real_source)
        broken_source = real_source.replace(marker, "", 1)
        self.assertNotIn('"roster/runner-capabilities.json"', broken_source)

        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self._init_git_repo(root)
            (root / "roster").mkdir()
            (root / "roster" / "runner-capabilities.json").write_text(
                MANIFEST_PATH.read_text(encoding="utf-8"), encoding="utf-8"
            )
            (root / "roster" / "runner-capabilities.schema.json").write_text(
                SCHEMA_PATH.read_text(encoding="utf-8"), encoding="utf-8"
            )
            subprocess.run(["git", "-C", str(root), "add", "."], check=True)
            subprocess.run(["git", "-C", str(root), "commit", "-qm", "base"], check=True)
            plugin_root = root / "plugins" / "cadre"
            plugin_root.mkdir(parents=True)
            packaging_readme = root / "packaging" / "plugin-README.md"
            packaging_readme.parent.mkdir(parents=True)
            packaging_readme.write_text("template readme\n", encoding="utf-8")

            # Place the scratch module at the same relative depth as the
            # real one (<root>/roster/orchestration/src/generate_global_plugin.py)
            # so its own `Path(__file__).resolve().parents[3]` self-resolves
            # to this fixture repo without needing any REPOSITORY_ROOT patch.
            module_path = root / "roster" / "orchestration" / "src" / "generate_global_plugin.py"
            broken_module = self._load_module_from_source(
                "broken_generate_global_plugin", broken_source, module_path
            )
            with mock.patch.object(broken_module, "PACKAGING_README", packaging_readme):
                broken_module.generate_suite_copy({}, plugin_root)
            self.assertFalse((plugin_root / "suite" / "roster" / "runner-capabilities.json").is_file())

            # Contrast: the real, unmodified module packages the same
            # fixture repository correctly.
            correct_plugin_root = root / "plugins" / "cadre-correct"
            correct_plugin_root.mkdir(parents=True)
            with mock.patch.object(ggp, "REPOSITORY_ROOT", root), mock.patch.object(
                ggp, "PACKAGING_README", packaging_readme
            ):
                ggp.generate_suite_copy({}, correct_plugin_root)
            self.assertTrue((correct_plugin_root / "suite" / "roster" / "runner-capabilities.json").is_file())


class NoFabricatedTargetTests(unittest.TestCase):
    """AC-9: no specific maintenance-time, defect-rate, or onboarding-time
    percentage/number is asserted anywhere in the shipped artifact's
    documentation beyond the grounded current-state counts.
    """

    def test_manifest_and_schema_do_not_assert_a_percentage_target(self) -> None:
        percentage_pattern = re.compile(r"\d+%")
        for path in (MANIFEST_PATH, SCHEMA_PATH):
            content = path.read_text(encoding="utf-8")
            self.assertFalse(percentage_pattern.search(content), str(path))


if __name__ == "__main__":
    unittest.main()
