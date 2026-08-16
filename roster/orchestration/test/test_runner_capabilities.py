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

# The manifest's descriptive runner-divergence facts. No code may branch on
# these (idea #8 OD-2). Distinct from capability_tiers/model_tiers/
# allowed_reasoning_efforts, which code legitimately reads at both build and
# dispatch time -- see MANIFEST_READERS below.
RUNNER_DIVERGENCE_FIELDS = (
    "native_workspace_isolation",
    "prompt_hook_support",
    "prompt_hook_mechanism",
    "tool_gate_support",
    "tool_gate_mechanism",
)

# Every module under roster/orchestration/ permitted to reference the manifest
# at all, each with the reason. A new entry is the review checkpoint that the
# field scan cannot be: loading the manifest by path and iterating a runner's
# keys reaches the divergence facts without ever naming one, which is the only
# way past RUNNER_DIVERGENCE_FIELDS.
MANIFEST_READERS = {
    "generate_global_plugin.py": "build time: capability_tiers/model_tiers/allowed_reasoning_efforts",
    "generate_role_metadata.py": "build time: TIER_MAP derivation",
    "validate_runner_capabilities.py": "the manifest's own schema check",
    "role_fidelity.py": "model_tiers, for the packaged-vs-checkout fidelity report",
    "provenance.py": "a comment recording that it deliberately reads nothing",
    "dispatch_core.py": "dispatch time: model_tiers inversion",
    "api_runner.py": "dispatch time: capability_tiers -> offered tools, fail-closed",
}


def _orchestration_modules() -> list[Path]:
    """Every module under roster/orchestration/ that could run at dispatch time.

    Walked rather than enumerated. The tuple this replaced named
    dispatch_core.py, build_dispatch_plan.py and select_agents.py, and so could
    not see mcp/api_runner.py -- which already reads this manifest at dispatch
    time and is the likeliest place a future `tool_gate_support` branch would
    land. Test modules are excluded because this one names the fields in order
    to assert on them; nothing else is excluded, since no consumer of the
    divergence facts is legitimate in either direction.
    """
    modules: list[Path] = []
    for directory in ("src", "mcp"):
        modules.extend(sorted((REPOSITORY_ROOT / "roster" / "orchestration" / directory).rglob("*.py")))
    return modules


def _strip_whole_line_comments(source: str) -> str:
    """Drop lines that are entirely a comment, keeping every code line intact.

    Deliberately not a TypeScript parser, but it does track `/* */` nesting
    depth, because the obvious shortcut is unsound: treating any line that
    starts with `*` as a comment drops real code, since `*` also begins a
    wrapped multiplication and can begin a line inside a template literal.
    A guard that silently discards code lines is worse than the false
    positive it was narrowed to avoid -- it fails open.

    Anything executable survives, so a check over the output cannot be
    evaded by appending a trailing comment to a real statement.
    """
    kept: list[str] = []
    in_block = False
    for line in source.splitlines():
        stripped = line.strip()
        if in_block:
            # Inside /* */: this line is comment text whatever it starts with.
            if "*/" in stripped:
                in_block = False
                # Keep anything after the terminator -- it is code again.
                tail = stripped.split("*/", 1)[1]
                if tail.strip():
                    kept.append(tail)
            continue
        if stripped.startswith("//"):
            continue
        if stripped.startswith("/*"):
            if "*/" not in stripped:
                in_block = True
            continue
        kept.append(line)
    return "\n".join(kept)


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

    # test_manifest_capability_tiers_match_generator_constants_verbatim and
    # test_manifest_model_tiers_match_tier_map_verbatim moved to
    # internal/generators/runner_capabilities_test.go. They compared the
    # *Python* generators' derived constants against this manifest, and those
    # generators were replaced by the Go CLI. The Go versions check what the
    # constants are derived from, one layer closer to the file.

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


# GeneratedFromManifestTests moved to
# internal/generators/runner_capabilities_test.go
# (TestACapabilityManifestThatCannotBeTrustedFailsClosed and
# TestWriteCapableTiersAreDerivedFromSandboxModeRatherThanNamed). It drove the
# Python generator's manifest loader directly; the Go loader is the one that
# runs now, and its fail-closed paths are falsified there.

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

    # Fact 10: per-prompt hook surface (can something run before the model
    # sees the prompt, and can its output reach the model).
    def test_fact_10_prompt_hook_support(self) -> None:
        self.assertEqual("context_injection", self.runners["claude-code"]["prompt_hook_support"])
        self.assertEqual("context_injection", self.runners["codex"]["prompt_hook_support"])
        # The distinction this value exists to record: Cline's UserPromptSubmit
        # hook runs and is then ignored. "dispatch_only", never "none" (it does
        # fire) and never "context_injection" (its stdout is discarded) --
        # cline-plugins/cline/hook-surface.test.mts checks both halves against
        # the real dispatcher: a sentinel file proves it ran, and `onEvent`
        # returning nothing proves no output reaches the caller.
        self.assertEqual("dispatch_only", self.runners["cline"]["prompt_hook_support"])
        self.assertEqual("none", self.runners["api"]["prompt_hook_support"])

        # A mechanism string is required exactly when there is a surface to
        # describe, so "none" can never hide behind an explanatory sentence and
        # a real surface can never ship undocumented.
        for runner, values in self.runners.items():
            with self.subTest(runner=runner):
                if values["prompt_hook_support"] == "none":
                    self.assertIsNone(values["prompt_hook_mechanism"])
                else:
                    self.assertTrue((values["prompt_hook_mechanism"] or "").strip())

    # Fact 11: host-session tool gate (can a hook refuse a tool call in the
    # user's own session, as opposed to a subagent session the suite starts).
    def test_fact_11_tool_gate_support(self) -> None:
        for runner in ("claude-code", "codex", "cline"):
            with self.subTest(runner=runner):
                self.assertEqual("blocking", self.runners[runner]["tool_gate_support"])
        self.assertEqual("none", self.runners["api"]["tool_gate_support"])

        # The same paired-field invariant fact 10 enforces, over every runner
        # rather than the four named above. Without the loop a fifth runner
        # could ship `tool_gate_support: "none"` beside a leftover mechanism
        # string, or a real gate with none -- and this field names an
        # enforcement boundary, so it is the worse of the two to leave
        # undocumented.
        for runner, values in self.runners.items():
            with self.subTest(runner=runner):
                if values["tool_gate_support"] == "none":
                    self.assertIsNone(values["tool_gate_mechanism"])
                else:
                    self.assertTrue((values["tool_gate_mechanism"] or "").strip())

    def test_prompt_and_tool_gate_facts_are_grounded_in_the_prose(self) -> None:
        """AC-5's rule applied to facts 10-11: the manifest owns the value,
        runner-adapters.md owns the why, and neither may exist alone.
        """
        for marker in ("UserPromptSubmit", "PreToolUse", "prompt_submit", "tool_call"):
            with self.subTest(marker=marker):
                self.assertIn(marker, self.prose)

    def test_runner_divergence_facts_have_no_code_consumer(self) -> None:
        """OD-2, applied to every descriptive runner-divergence fact at once:
        these describe a runner at build time and must not become a
        dispatch-time branch. Especially load-bearing for `tool_gate_support`,
        which names an enforcement boundary -- code that silently skipped a
        check because a manifest string said "none" would be a security
        control decided by a data file.

        Note what OD-2 does NOT say. The manifest as a whole has two
        deliberate dispatch-time readers (dispatch_core.py's model_tiers
        inversion, api_runner.py's capability_tiers -> offered tools); it is
        the divergence facts specifically that no code may touch.
        """
        modules = _orchestration_modules()
        # Guards the walk itself: a broken rglob would otherwise pass vacuously.
        # The floor was 20 while the selector was Python. select_agents.py,
        # build_dispatch_plan.py, risk_classifier.py and team_recipe_dryrun.py
        # were deleted once internal/selector carried their behaviour, and this
        # guard refused to pass rather than let the walk quietly shrink -- which
        # is the whole reason it counts. Lowered deliberately, not to whatever
        # the walk happens to find today.
        self.assertGreater(
            len(modules), 12, f"module walk found only {len(modules)} files -- the walk is broken, not the code"
        )
        for path in modules:
            source = path.read_text(encoding="utf-8")
            for field in RUNNER_DIVERGENCE_FIELDS:
                with self.subTest(module=path.name, field=field):
                    self.assertNotIn(
                        field,
                        source,
                        f"{path.name} references a build-time-only manifest field (idea #8 OD-2)",
                    )

    def test_no_undeclared_module_reads_the_manifest(self) -> None:
        """The field scan above is a substring check, so a module that loads
        the manifest by path and iterates a runner's keys reaches the
        divergence facts without naming one. This closes that route the only
        way a test can: every module referencing the manifest at all must be
        declared in MANIFEST_READERS, which makes a new consumer a visible
        diff in review rather than a silent addition.
        """
        for path in _orchestration_modules():
            source = path.read_text(encoding="utf-8")
            if "runner-capabilities.json" not in source and "RUNNER_CAPABILITIES_PATH" not in source:
                continue
            with self.subTest(module=path.name):
                self.assertIn(
                    path.name,
                    MANIFEST_READERS,
                    f"{path.name} reads runner-capabilities.json but is not declared in MANIFEST_READERS. "
                    "If this is a deliberate new consumer, add it there with its reason -- and confirm it "
                    "does not branch on the runner-divergence facts (idea #8 OD-2).",
                )

    # `native_workspace_isolation` had its own three-file OD-2 test here. It is
    # now covered by test_runner_divergence_facts_have_no_code_consumer, which
    # checks the same field across every orchestration module instead of three.


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
    shipped, drift-guarded artifacts in this repository (the 159 committed
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
        must not read the manifest at runtime. It is a standalone
        distributable and cannot reach into the generating repository.

        Scoped to code, not prose. It began as a substring check over the
        whole file, which also fired on a comment explaining that a *test*
        pins index.ts's tier vocabulary against the manifest (deagy/cadre#234
        follow-up) -- true, useful, and not a runtime dependency.

        Only *whole-line* comments are excluded, which is what keeps the
        narrowing honest: an import, a `readFileSync`, or a bare string
        literal holding the manifest path all live on code lines and are
        still caught, and a trailing `// ...` cannot hide a read because the
        code before it remains. Verified against both directions below.
        """
        source = CLINE_AGENTS_INDEX_TS.read_text(encoding="utf-8")
        self.assertNotIn("runner-capabilities.json", _strip_whole_line_comments(source))

        # Both directions, so a future simplification of the helper cannot
        # quietly turn this into a check that passes on anything: a code line
        # must survive stripping (and so still be caught above), a whole-line
        # comment must not.
        self.assertIn(
            "runner-capabilities.json",
            _strip_whole_line_comments('const p = "roster/runner-capabilities.json";'),
        )
        self.assertIn(
            "runner-capabilities.json",
            _strip_whole_line_comments('const p = load(); // roster/runner-capabilities.json'),
        )
        self.assertNotIn(
            "runner-capabilities.json",
            _strip_whole_line_comments("  // see roster/runner-capabilities.json for the map"),
        )
        # A bare `*` line is only comment text when a block is actually open.
        # Treating it as one unconditionally is how this helper would fail
        # open: a wrapped expression or template-literal line starting with
        # `*` would be discarded along with any read it carried.
        self.assertNotIn(
            "runner-capabilities.json",
            _strip_whole_line_comments('/**\n * roster/runner-capabilities.json\n */'),
        )
        self.assertIn(
            "runner-capabilities.json",
            _strip_whole_line_comments('const n = base\n  * load("roster/runner-capabilities.json");'),
        )


# PackagingAllowlistParityTests moved to
# internal/generators/runner_capabilities_test.go
# (TestThePackagedPluginCarriesTheCapabilityManifest). It drove the *Python*
# generator into a fixture git repository to prove the manifest and its schema
# get packaged; the Go version checks the committed distribution -- which is
# what people install, and which `cadre generate-plugin --check` already
# guards against drifting from what the generator would emit.

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
