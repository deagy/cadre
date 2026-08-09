"""Repository health checks for the agent suite itself."""

from __future__ import annotations

import atexit
import contextlib
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import time
import unittest
from unittest import mock
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
REPOSITORY_ROOT = ROOT.parent

# Single source of truth for this repository's current role count. Cross-
# checked directly against the AGENT.md files on disk below, so a role
# add/remove without updating this constant fails immediately instead of
# leaving the other assertions below silently pinned to a stale number.
EXPECTED_ROLE_COUNT = 74

# This repository's register-owned Agentic SDLC provider bundle. Copied
# verbatim into the plugin package by `cadre generate-plugin`; the pip/pipx
# distribution vendors it directly (see pyproject.toml).
PROVIDER_ROOT = REPOSITORY_ROOT / "provider"

_GENERATED_PACKAGE: Path | None = None


def _tracked_files() -> list[str]:
    """`git ls-files`, so untracked scratch files and gitignored build output
    never trip a scan -- only what's actually committed."""
    return subprocess.run(
        ["git", "ls-files"],
        cwd=REPOSITORY_ROOT,
        check=True,
        capture_output=True,
        text=True,
        encoding="utf-8",
    ).stdout.splitlines()


def _catalog_role_count() -> int:
    """The live role count, parsed from catalog.yaml the same way
    test_catalog_declares_capabilities_and_reviewers_are_read_only does --
    deliberately not EXPECTED_ROLE_COUNT above, since that constant is itself
    a hardcoded number these doc-drift checks exist to stop prose from
    silently diverging from."""
    current_agent: str | None = None
    agents: set[str] = set()
    for line in (ROOT / "catalog.yaml").read_text(encoding="utf-8").splitlines():
        if line.startswith("  ") and not line.startswith("    ") and line.rstrip().endswith(":"):
            current_agent = line.strip()[:-1]
            agents.add(current_agent)
    return len(agents)


def _secure_cloud_role_count() -> int:
    """The live `secure-cloud` profile role count, read directly from its
    `agents` array rather than hardcoded."""
    profile = json.loads(
        (PROVIDER_ROOT / "profiles" / "secure-cloud" / "profile.json").read_text(encoding="utf-8")
    )
    return len(profile["agents"])


def generated_package() -> Path:
    """A freshly generated plugin package, built once and reused.

    The committed package lives in its own repository (deagy/cadre-lifecycle,
    successor to the now-archived deagy/cadre-plugin) since the
    register/plugin split, so tests about what the *generator*
    produces build their own copy here instead of reading a checked-in tree.
    Drift between this generator and that repository's committed content is
    guarded there, by its validate.yml `generated-content` job, which runs
    `generate-plugin --check --output` against the register revision pinned in
    its cadre-ref.txt.
    """
    global _GENERATED_PACKAGE
    if _GENERATED_PACKAGE is None:
        directory = Path(tempfile.mkdtemp(prefix="cadre-health-package-"))
        atexit.register(shutil.rmtree, directory, True)
        target = directory / "cadre-plugin"
        generated = subprocess.run(
            [
                sys.executable,
                str(REPOSITORY_ROOT / "roster" / "orchestration" / "src" / "generate_global_plugin.py"),
                "--output",
                str(target),
            ],
            cwd=REPOSITORY_ROOT, check=False, capture_output=True, text=True, encoding="utf-8",
        )
        if generated.returncode != 0:  # pragma: no cover - defensive
            # Surfaces inside whichever test called this first, so name the
            # helper explicitly rather than letting it read as that test's own
            # unrelated failure.
            raise AssertionError(
                "generated_package(): `generate-plugin --output` failed, so every test "
                f"depending on a generated package will fail too:\n{generated.stderr}"
            )
        _GENERATED_PACKAGE = target
    return _GENERATED_PACKAGE


class RepositoryHealthTests(unittest.TestCase):
    @staticmethod
    def _require_agentic_sdlc() -> None:
        if os.environ.get("AGENTIC_SDLC_BIN") or shutil.which("agentic-sdlc"):
            return
        raise unittest.SkipTest("Agentic SDLC executable is not configured")

    def test_catalog_definitions_and_agent_files_stay_in_sync(self) -> None:
        catalog_agents: dict[str, str] = {}
        current_agent: str | None = None
        for line in (ROOT / "catalog.yaml").read_text(encoding="utf-8").splitlines():
            if line.startswith("  ") and not line.startswith("    ") and line.rstrip().endswith(":"):
                current_agent = line.strip()[:-1]
            elif current_agent and line.strip().startswith("definition:"):
                catalog_agents[current_agent] = line.split(":", 1)[1].strip()

        agent_files = {
            str(path.relative_to(ROOT)).replace("\\", "/")
            for path in ROOT.rglob("AGENT.md")
        }
        self.assertEqual(set(catalog_agents.values()), agent_files)
        for relative_path in catalog_agents.values():
            self.assertTrue((ROOT / relative_path).is_file(), relative_path)

    def test_catalog_declares_capabilities_and_reviewers_are_read_only(self) -> None:
        catalog = (ROOT / "catalog.yaml").read_text(encoding="utf-8").splitlines()
        current_agent: str | None = None
        metadata: dict[str, dict[str, str]] = {}
        for line in catalog:
            if line.startswith("  ") and not line.startswith("    ") and line.rstrip().endswith(":"):
                current_agent = line.strip()[:-1]
                metadata[current_agent] = {}
            elif current_agent and line.strip().startswith(("definition:", "phase:", "capability:")):
                key, value = line.strip().split(":", 1)
                metadata[current_agent][key] = value.strip()

        self.assertEqual(EXPECTED_ROLE_COUNT, len(metadata))
        self.assertEqual(EXPECTED_ROLE_COUNT, len(list(ROOT.rglob("AGENT.md"))))
        allowed = {"read_only", "document_author", "code_author", "test_author", "environment_operator"}
        for agent_id, values in metadata.items():
            with self.subTest(agent=agent_id):
                self.assertIn(values.get("capability"), allowed)
                if values.get("definition", "").startswith("authority/"):
                    # Authority aides prepare a human decision package and
                    # must never author/mutate the artifacts they report on;
                    # document_author here would violate their own
                    # independence clause (docs/proposals/human-authority-
                    # role-agents.md §8.2).
                    self.assertEqual("read_only", values["capability"])
                if values.get("definition", "").startswith("review/"):
                    self.assertEqual("read_only", values["capability"])

    def test_workflow_values_match_schema_and_files(self) -> None:
        schema = json.loads((ROOT / "orchestration" / "selection.schema.json").read_text(encoding="utf-8"))
        workflow_values = set(schema["properties"]["workflow"]["enum"])
        workflow_files = {
            path.stem
            for path in (ROOT / "workflows").glob("*.md")
        }
        self.assertEqual(workflow_values - {"needs-triage"}, workflow_files)

    def test_publishable_skill_folders_are_tracked_and_not_ignored(self) -> None:
        skills_root = REPOSITORY_ROOT / ".agents" / "skills"
        for skill_file in skills_root.glob("*/SKILL.md"):
            skill_dir = skill_file.parent
            openai_yaml = skill_dir / "agents" / "openai.yaml"
            self.assertTrue(openai_yaml.is_file(), str(openai_yaml))

            tracked = subprocess.run(
                ["git", "ls-files", "--error-unmatch", str(skill_file.relative_to(REPOSITORY_ROOT))],
                cwd=REPOSITORY_ROOT,
                check=False,
                capture_output=True,
                text=True,
                encoding="utf-8",
            )
            self.assertEqual(tracked.returncode, 0, tracked.stderr)

            ignored = subprocess.run(
                ["git", "check-ignore", "-q", str(skill_dir.relative_to(REPOSITORY_ROOT))],
                cwd=REPOSITORY_ROOT,
                check=False,
            )
            self.assertNotEqual(ignored.returncode, 0, f"{skill_dir} is ignored")

    def test_hand_maintained_skill_count_matches_agents_skills(self) -> None:
        """Pins the hand-typed "N skills" prose in README.md/RUNBOOK.md to the
        actual `.agents/skills/` count, so a skill add/remove can't silently
        leave stale prose behind (as happened when it drifted to "6 skills"
        with 7 actually present).
        """
        skills_root = REPOSITORY_ROOT / ".agents" / "skills"
        actual_count = len(list(skills_root.glob("*/SKILL.md")))
        for doc_path in (REPOSITORY_ROOT / "README.md", REPOSITORY_ROOT / "roster" / "RUNBOOK.md"):
            text = doc_path.read_text(encoding="utf-8")
            self.assertIn(
                f"{actual_count} skills",
                text,
                f"{doc_path} does not say '{actual_count} skills' (actual count under {skills_root})",
            )

    def test_claude_skill_pointers_match_the_canonical_codex_skill(self) -> None:
        skills_root = REPOSITORY_ROOT / ".agents" / "skills"
        claude_skills_root = REPOSITORY_ROOT / ".claude" / "skills"
        for skill_file in skills_root.glob("*/SKILL.md"):
            skill_name = skill_file.parent.name
            with self.subTest(skill=skill_name):
                pointer_file = claude_skills_root / skill_name / "SKILL.md"
                self.assertTrue(pointer_file.is_file(), str(pointer_file))

                def frontmatter(path: Path) -> dict[str, str]:
                    content = path.read_text(encoding="utf-8")
                    block = content.split("---", 2)[1]
                    return dict(
                        (part.strip() for part in line.split(":", 1))
                        for line in block.splitlines()
                        if line.strip()
                    )

                canonical = frontmatter(skill_file)
                pointer = frontmatter(pointer_file)
                self.assertEqual(canonical["name"], pointer["name"])
                self.assertEqual(canonical["description"], pointer["description"])
                self.assertIn(
                    f".agents/skills/{skill_name}/SKILL.md",
                    pointer_file.read_text(encoding="utf-8"),
                )

                tracked = subprocess.run(
                    ["git", "ls-files", "--error-unmatch", str(pointer_file.relative_to(REPOSITORY_ROOT))],
                    cwd=REPOSITORY_ROOT,
                    check=False,
                    capture_output=True,
                    text=True,
                    encoding="utf-8",
                )
                self.assertEqual(tracked.returncode, 0, tracked.stderr)

    def test_clinerules_pointer_is_tracked_and_points_at_canonical_sources(self) -> None:
        pointer_file = REPOSITORY_ROOT / ".clinerules" / "agents-repository.md"
        self.assertTrue(pointer_file.is_file(), str(pointer_file))

        content = pointer_file.read_text(encoding="utf-8")
        self.assertIn("AGENTS.md", content)
        self.assertIn("roster/RUNBOOK.md", content)

        tracked = subprocess.run(
            ["git", "ls-files", "--error-unmatch", str(pointer_file.relative_to(REPOSITORY_ROOT))],
            cwd=REPOSITORY_ROOT,
            check=False,
            capture_output=True,
            text=True,
            encoding="utf-8",
        )
        self.assertEqual(tracked.returncode, 0, tracked.stderr)

        ignored = subprocess.run(
            ["git", "check-ignore", "-q", str(pointer_file.parent.relative_to(REPOSITORY_ROOT))],
            cwd=REPOSITORY_ROOT,
            check=False,
        )
        self.assertNotEqual(ignored.returncode, 0, f"{pointer_file.parent} is ignored")

    def test_sample_references_are_limited_to_allowed_archives(self) -> None:
        allowed_prefixes = (
            ".gitignore",
            "roster/orchestration/examples/SAMPLE-001",
            "roster/orchestration/examples/SAMPLE-001-report.md",
            "roster/orchestration/examples/sample-plan.json",
            "roster/orchestration/runs/.gitignore",
            "roster/orchestration/runs/SAMPLE-001-IMPLEMENT",
            "roster/orchestration/test/test_repository_health.py",
            "roster/orchestration/test/test_run_record_validation.py",
            "sample-001/",
        )
        tracked_files = subprocess.run(
            ["git", "ls-files"],
            cwd=REPOSITORY_ROOT,
            check=True,
            capture_output=True,
            text=True,
            encoding="utf-8",
        ).stdout.splitlines()

        offenders: list[str] = []
        for relative_path in tracked_files:
            normalized = relative_path.replace("\\", "/")
            if normalized.startswith(allowed_prefixes):
                continue
            path = REPOSITORY_ROOT / normalized
            if not path.is_file():
                continue
            try:
                text = path.read_text(encoding="utf-8")
            except UnicodeDecodeError:
                continue
            if "SAMPLE-001" in text or "sample-001" in text or "Sample-001" in text:
                offenders.append(normalized)

        self.assertEqual(offenders, [])

    def test_authority_aide_agents_are_generated_and_in_sync(self) -> None:
        generator = REPOSITORY_ROOT / "roster" / "orchestration" / "src" / "generate_authority_aides.py"
        checked = subprocess.run(
            [sys.executable, str(generator), "--check"],
            cwd=REPOSITORY_ROOT,
            check=False,
            capture_output=True,
            text=True,
            encoding="utf-8",
        )
        self.assertEqual(0, checked.returncode, checked.stderr)

    def test_role_metadata_files_are_generated_and_in_sync(self) -> None:
        generator = REPOSITORY_ROOT / "roster" / "orchestration" / "src" / "generate_role_metadata.py"
        checked = subprocess.run(
            [sys.executable, str(generator), "--check"],
            cwd=REPOSITORY_ROOT,
            check=False,
            capture_output=True,
            text=True,
            encoding="utf-8",
        )
        self.assertEqual(0, checked.returncode, checked.stderr)

    def test_secure_cloud_agents_plugin_is_generated_and_in_sync(self) -> None:
        generator = REPOSITORY_ROOT / "roster" / "orchestration" / "src" / "generate_global_plugin.py"
        with tempfile.TemporaryDirectory(prefix="agents-health-") as temporary_directory:
            output = Path(temporary_directory) / "plugin"
            generated = subprocess.run(
                [sys.executable, str(generator), "--output", str(output)],
                cwd=REPOSITORY_ROOT,
                check=False,
                capture_output=True,
                text=True,
                encoding="utf-8",
            )
            self.assertEqual(0, generated.returncode, generated.stderr)
            checked = subprocess.run(
                [sys.executable, str(generator), "--check", "--output", str(output)],
                cwd=REPOSITORY_ROOT,
                check=False,
                capture_output=True,
                text=True,
                encoding="utf-8",
            )
            self.assertEqual(0, checked.returncode, checked.stderr)

    def test_removed_lifecycle_migration_utility_cannot_ship(self) -> None:
        source_path = ROOT / "orchestration" / "src" / "migrate_execution_summary.py"
        packaged_path = (
            generated_package()
            / "suite"
            / "roster"
            / "orchestration"
            / "src"
            / "migrate_execution_summary.py"
        )
        self.assertFalse(source_path.exists(), str(source_path))
        self.assertFalse(packaged_path.exists(), str(packaged_path))

    def test_packaged_runtime_has_no_removed_lifecycle_paths(self) -> None:
        plugin_root = generated_package()
        offenders: list[str] = []
        for path in plugin_root.rglob("*"):
            # Other tests execute the packaged bin/cadre out of this shared
            # fixture, which leaves __pycache__/*.pyc behind. Filter them
            # rather than relying on this test happening to run first.
            if not path.is_file() or path.suffix in {".pyc", ".pyo"} or "__pycache__" in path.parts:
                continue
            content = path.read_text(encoding="utf-8", errors="ignore")
            if "plugins/agentic-sdlc" in content or "migrate_execution_summary" in content:
                offenders.append(str(path.relative_to(plugin_root)))
        self.assertEqual([], offenders)

    def test_suite_does_not_duplicate_lifecycle_authority(self) -> None:
        forbidden = [
            ROOT / "orchestration" / "quality-gates.md",
            ROOT / "orchestration" / "run-record.schema.json",
            ROOT / "orchestration" / "src" / "validate_run_record.py",
            ROOT / "orchestration" / "agentic-sdlc-artifact-contract.md",
        ]
        for path in forbidden:
            with self.subTest(path=path):
                self.assertFalse(path.exists(), str(path))

    def test_secure_cloud_agents_plugin_covers_every_catalog_agent_and_skill(self) -> None:
        catalog_agents: dict[str, str] = {}
        current_agent: str | None = None
        for line in (ROOT / "catalog.yaml").read_text(encoding="utf-8").splitlines():
            if line.startswith("  ") and not line.startswith("    ") and line.rstrip().endswith(":"):
                current_agent = line.strip()[:-1]
                catalog_agents[current_agent] = ""

        plugin_root = generated_package()
        for agent_id in catalog_agents:
            with self.subTest(agent=agent_id):
                md_path = plugin_root / "agents" / f"{agent_id}.md"
                codex_id = f"agents-{agent_id}"
                toml_path = plugin_root / "codex-agents" / f"{codex_id}.toml"
                self.assertTrue(md_path.is_file(), str(md_path))
                self.assertTrue(toml_path.is_file(), str(toml_path))
                self.assertIn(f"name: {agent_id}", md_path.read_text(encoding="utf-8"))
                self.assertIn(f'name = "{codex_id}"', toml_path.read_text(encoding="utf-8"))

        sys.path.insert(0, str(ROOT / "orchestration" / "src"))
        try:
            import generate_global_plugin
        finally:
            sys.path.pop(0)

        skills_root = REPOSITORY_ROOT / ".agents" / "skills"
        for skill_file in skills_root.glob("*/SKILL.md"):
            skill_name = skill_file.parent.name
            with self.subTest(skill=skill_name):
                # Most skills package to skills/<name>/; a few (see
                # SKILL_PACKAGE_TARGETS) retarget into a sub-plugin directory
                # instead, e.g. lifecycle-onboarding/lifecycle-review into
                # plugins/lifecycle/skills/ so cadre-lifecycle can ship them
                # as an optional plugin rather than bundled into the core.
                package_subdir = generate_global_plugin.SKILL_PACKAGE_TARGETS.get(skill_name, "skills")
                packaged_skill = plugin_root / package_subdir / skill_name / "SKILL.md"
                self.assertTrue(packaged_skill.is_file(), str(packaged_skill))
                self.assertIn(f"name: {skill_name}", packaged_skill.read_text(encoding="utf-8"))

    def test_secure_cloud_agents_agent_catalog_export_covers_every_role(self) -> None:
        catalog_agents: dict[str, str] = {}
        current_agent: str | None = None
        for line in (ROOT / "catalog.yaml").read_text(encoding="utf-8").splitlines():
            if line.startswith("  ") and not line.startswith("    ") and line.rstrip().endswith(":"):
                current_agent = line.strip()[:-1]
                catalog_agents[current_agent] = ""

        export_path = PROVIDER_ROOT / "agent-catalog.json"
        export = json.loads(export_path.read_text(encoding="utf-8"))["agents"]
        self.assertEqual(set(catalog_agents), set(export))
        for agent_id, metadata in export.items():
            with self.subTest(agent=agent_id):
                self.assertIn(metadata["kind"], {"author", "reviewer", "specialist"})
                self.assertTrue(metadata["phase"])
                # The kernel resolves `definition` relative to whichever copy
                # of agent-catalog.json it reads, and rejects anything escaping
                # that directory -- so the register's export must resolve
                # inside PROVIDER_ROOT (roles/...), and the package's rewritten
                # copy inside the package (suite/roster/...). Both are asserted
                # here because a path that resolves in neither is exactly the
                # regression that shipped in the register/plugin split:
                # `cadre sdlc init` silently degraded to generic role text.
                definition = metadata["definition"]
                self.assertTrue((PROVIDER_ROOT / definition).is_file(), definition)
                packaged = json.loads(
                    (generated_package() / "agent-catalog.json").read_text(encoding="utf-8")
                )["agents"][agent_id]["definition"]
                self.assertTrue((generated_package() / packaged).is_file(), packaged)

    def test_role_authority_is_equivalent_across_every_generating_runner(self) -> None:
        """Proposal 10's invariant, stated as one test: every role this
        repository's catalog defines is emitted, with runner-equivalent
        authority, by all three role-wrapper generators -- Claude Code
        (generate_global_plugin.py), Codex (generate_role_metadata.py's
        codex_wrapper_contents), and Cline (plugin/tools/port_cline_agents.py).

        Before this test, parity across the three outputs was only an
        emergent property of several separate generator tests, each checking
        its own output in isolation -- nothing stated "role X has
        runner-equivalent representation everywhere" as a single, named
        check. This test states it directly and fails with the specific
        role id, runner(s), and field(s) that diverged, rather than a bare
        assertion failure.

        Everything here is regenerated fresh into temporary output (this
        test never reads the committed plugin/, provider/, or
        cline-plugins/ trees, which can lag the source of truth between
        regenerations):

        - the role set and each role's capability/model tier come from
          `generate_role_metadata.build_role_model()`, parsed directly from
          every roster/<phase>/<role>/AGENT.md's frontmatter -- the same
          source catalog.yaml itself is generated from, not catalog.yaml on
          disk;
        - the Claude Code wrapper set is `generated_package()` above (a
          fresh `generate_global_plugin.py --output` build, cached and
          reused by every other test in this module);
        - the Codex wrapper set is computed in-memory by calling
          `codex_wrapper_contents()` on that same fresh role model -- not by
          reading the committed roster/provider/codex-agents/*.toml, which
          `cadre generate-role-metadata --check` guards separately but which
          could still be stale in a working tree mid-edit;
        - the Cline preset set is a fresh `port_cline_agents.port_agents()`
          run, sourced from the fresh Claude Code build above.

        "Authority-equivalent" means, per runner pairing:

        - Claude Code <-> Codex: the role's `capability` tier must resolve to
          the *same* `roster/runner-capabilities.json` profile on both sides
          -- Claude's `tools:` frontmatter line must equal that profile's
          tool list verbatim, and Codex's `sandbox_mode` TOML value must
          equal that profile's `sandbox_mode` verbatim. This is the only
          pairing checked on the full tool-list/sandbox_mode axis, since it
          is the only pairing where both sides carry a directly comparable
          field.
        - All three (Claude Code, Codex, Cline): whether the role is
          write-capable at all. Claude Code is write-capable when its tools
          line contains Bash/Edit/Write; Codex when its `sandbox_mode` is
          not "read-only"; Cline when its `allowedTools` contains
          `run_commands` or `editor` (the tools `port_cline_agents.TOOL_MAP`
          maps Bash/Edit/Write onto). All three must agree.
        - All three: model tier. Claude Code's `model:` frontmatter value,
          Cline's `modelTier:` frontmatter value, and Codex's `model` TOML
          value resolved back to a tier through `MODEL_TIERS` must all be
          the same tier string.

        Explicit limitation, stated rather than silently skipped: Cline
        presets carry no `sandbox_mode`-equivalent field and no verbatim
        tool-list -- only a coarser `allowedTools` action-name list with no
        read/write-per-tool distinction preserved (`Edit` and `Write` both
        collapse to `editor`; `Grep`/`Glob` both collapse to
        `search_codebase`). Cline therefore does not participate in the
        exact-tool-list comparison above; it participates only in the
        coarser write-capable-or-not comparison, which is the one property
        `allowedTools` can actually express. Forcing a false byte-level
        equivalence there would fail this test on Cline's tool-name
        *vocabulary* rather than on any real authority drift, which is
        exactly the kind of noisy assertion Proposal 10 was written against.
        """
        sys.path.insert(0, str(ROOT / "orchestration" / "src"))
        try:
            import generate_global_plugin as ggp
            import generate_role_metadata as grm
        finally:
            sys.path.pop(0)

        sys.path.insert(0, str(REPOSITORY_ROOT / "plugin" / "tools"))
        try:
            import port_cline_agents as pca
        finally:
            sys.path.pop(0)

        # 1. The full role set and each role's capability/model metadata,
        # derived fresh from AGENT.md frontmatter -- never from catalog.yaml
        # on disk, so a mid-edit catalog.yaml cannot make this test look
        # more current than the actual role definitions.
        order_ids, roles = grm.build_role_model(ROOT, ROOT / "catalog-order.txt")
        self.assertTrue(order_ids, "no roles discovered from AGENT.md frontmatter")

        header_template = (ROOT / "_catalog_header.yaml.tmpl").read_text(encoding="utf-8")
        fresh_catalog_content = grm.render_catalog(order_ids, roles, header_template)
        catalog_entries = grm.load_catalog_content(fresh_catalog_content)

        # 2. Claude Code: the shared, cached fresh plugin build.
        plugin_root = generated_package()

        # 3. Codex: computed purely in-memory from the fresh role model
        # above -- never written to (or read from) the real provider/ tree.
        codex_contents = ggp.codex_wrapper_contents(catalog_entries)

        # 4. Cline: a fresh port, sourced from the fresh Claude Code build.
        cline_root = Path(tempfile.mkdtemp(prefix="cadre-cline-conformance-"))
        self.addCleanup(shutil.rmtree, cline_root, ignore_errors=True)
        ported_cline_roles = set(pca.port_agents(cline_root, source_root=plugin_root))

        codex_model_to_tier = {
            data["codex_model"]: tier for tier, data in ggp.MODEL_TIERS.items()
        }

        def _frontmatter_field(text: str, prefix: str) -> str | None:
            for line in text.splitlines():
                if line.startswith(prefix):
                    return line[len(prefix) :].strip()
            return None

        def _toml_field(text: str, key: str) -> str | None:
            raw = _frontmatter_field(text, f"{key} = ")
            return json.loads(raw) if raw is not None else None

        WRITE_TOOLS = {"Bash", "Edit", "Write"}
        WRITE_CLINE_TOOLS = {"run_commands", "editor"}

        divergences: list[str] = []

        for role_id in order_ids:
            metadata = catalog_entries[role_id]
            capability = metadata["capability"]
            profile = ggp.CAPABILITY_PROFILES[capability]
            expected_model_tier = metadata["model"]

            # -- Claude Code --------------------------------------------------
            claude_path = plugin_root / "agents" / f"{role_id}.md"
            if not claude_path.is_file():
                divergences.append(f"{role_id}: missing from Claude Code output ({claude_path})")
                continue
            claude_text = claude_path.read_text(encoding="utf-8")
            claude_tools_line = _frontmatter_field(claude_text, "tools:")
            expected_tools_line = ", ".join(profile["tools"])
            if claude_tools_line != expected_tools_line:
                divergences.append(
                    f"{role_id}: Claude Code tools {claude_tools_line!r} != expected "
                    f"{expected_tools_line!r} for capability {capability!r}"
                )
            claude_model = _frontmatter_field(claude_text, "model:")
            if claude_model != expected_model_tier:
                divergences.append(
                    f"{role_id}: Claude Code model {claude_model!r} != catalog model "
                    f"{expected_model_tier!r}"
                )
            claude_write_capable = bool(WRITE_TOOLS & set((claude_tools_line or "").split(", ")))

            # -- Codex ----------------------------------------------------------
            codex_filename = f"agents-{role_id}.toml"
            codex_text = codex_contents.get(codex_filename)
            if codex_text is None:
                divergences.append(f"{role_id}: missing from Codex output ({codex_filename})")
                continue
            codex_sandbox_mode = _toml_field(codex_text, "sandbox_mode")
            if codex_sandbox_mode != profile["sandbox_mode"]:
                divergences.append(
                    f"{role_id}: Codex sandbox_mode {codex_sandbox_mode!r} != expected "
                    f"{profile['sandbox_mode']!r} for capability {capability!r}"
                )
            codex_model_value = _toml_field(codex_text, "model")
            codex_model_tier = codex_model_to_tier.get(codex_model_value)
            if codex_model_tier != expected_model_tier:
                divergences.append(
                    f"{role_id}: Codex model {codex_model_value!r} resolves to tier "
                    f"{codex_model_tier!r}, != catalog model {expected_model_tier!r}"
                )
            codex_write_capable = codex_sandbox_mode != "read-only"

            # -- Cline ------------------------------------------------------
            if role_id not in ported_cline_roles:
                divergences.append(f"{role_id}: missing from Cline output (port_agents did not emit it)")
                continue
            cline_path = cline_root / "cline-agents" / "agents" / f"{role_id}.md"
            cline_text = cline_path.read_text(encoding="utf-8")
            cline_model_tier = _frontmatter_field(cline_text, "modelTier:")
            if cline_model_tier != expected_model_tier:
                divergences.append(
                    f"{role_id}: Cline modelTier {cline_model_tier!r} != catalog model "
                    f"{expected_model_tier!r}"
                )
            allowed_tools_raw = _frontmatter_field(cline_text, "allowedTools:")
            allowed_tools = {
                tool.strip() for tool in (allowed_tools_raw or "").strip("[]").split(",") if tool.strip()
            }
            cline_write_capable = bool(WRITE_CLINE_TOOLS & allowed_tools)

            # -- Cross-runner write-capability agreement (all three) --------
            if not (claude_write_capable == codex_write_capable == cline_write_capable):
                divergences.append(
                    f"{role_id}: write-capability disagreement -- Claude Code="
                    f"{claude_write_capable}, Codex={codex_write_capable}, Cline="
                    f"{cline_write_capable} (capability tier {capability!r})"
                )

        self.assertEqual(
            [],
            divergences,
            "One or more roles have non-equivalent authority across generating runners "
            "(role: what diverged, listed above). Each entry names the specific role id "
            "and runner(s) that disagree.",
        )

    def test_generated_wrappers_enforce_catalog_capabilities_and_provenance(self) -> None:
        generator = REPOSITORY_ROOT / "roster" / "orchestration" / "src" / "generate_global_plugin.py"
        with tempfile.TemporaryDirectory(prefix="agents-capabilities-") as temporary_directory:
            plugin_root = Path(temporary_directory) / "plugin"
            result = subprocess.run(
                [sys.executable, str(generator), "--output", str(plugin_root)],
                cwd=REPOSITORY_ROOT,
                check=True,
                capture_output=True,
                text=True,
                encoding="utf-8",
            )
            self.assertEqual(0, result.returncode)
            for agent_id in ("code-reviewer", "security-reviewer"):
                markdown = (plugin_root / "agents" / f"{agent_id}.md").read_text(encoding="utf-8")
                toml = (plugin_root / "codex-agents" / f"agents-{agent_id}.toml").read_text(encoding="utf-8")
                self.assertIn("tools: Read, Grep, Glob", markdown)
                self.assertNotIn("tools: Read, Grep, Glob, Bash", markdown)
                self.assertIn('sandbox_mode = "read-only"', toml)
                self.assertIn("generated: true", markdown)
                self.assertIn("canonical_source:", markdown)
                self.assertIn("# GENERATED FILE:", toml)
            for agent_id in ("application-engineer", "test-engineer"):
                author = (plugin_root / "agents" / f"{agent_id}.md").read_text(encoding="utf-8")
                self.assertIn("tools: Read, Grep, Glob, Bash, Edit, Write", author)
                self.assertIn('sandbox_mode = "workspace-write"', (plugin_root / "codex-agents" / f"agents-{agent_id}.toml").read_text(encoding="utf-8"))

            # roster/shared/workspace-isolation.md must reach EVERY role, at
            # every capability tier. Its "Never mutate a working tree you did
            # not create" section binds all of them: destroying uncommitted
            # work with `git reset --hard`/`checkout`/`stash` while inspecting
            # a diff requires no file-write tool and produces no edit, so
            # neither a role's tier nor its tool allowlist is what governs.
            # The file was previously tier-scoped to write-capable roles,
            # which coupled a rule about *reading* to a tier about *writing*.
            marker = "Shared policy: roster/shared/workspace-isolation.md"
            for agent_id in (
                "code-reviewer",  # read-only
                "security-reviewer",  # read-only
                "application-engineer",  # code_author
                "test-engineer",  # test_author
                "incident-commander",  # environment_operator
                "requirements-agent",  # document_author
            ):
                markdown = (plugin_root / "agents" / f"{agent_id}.md").read_text(encoding="utf-8")
                self.assertIn(marker, markdown)

            sys.path.insert(0, str(ROOT / "orchestration" / "src"))
            try:
                import generate_global_plugin
            finally:
                sys.path.pop(0)
            manifest = json.loads(
                (REPOSITORY_ROOT / "roster" / "runner-capabilities.json").read_text(encoding="utf-8")
            )
            expected_write_capable = {
                tier for tier, data in manifest["capability_tiers"].items()
                if data["sandbox_mode"] != "read-only"
            }
            self.assertEqual(expected_write_capable, set(generate_global_plugin.WRITE_CAPABLE_TIERS))

            tracked = set(
                subprocess.run(
                    ["git", "ls-files", "roster"],
                    cwd=REPOSITORY_ROOT,
                    check=True,
                    capture_output=True,
                    text=True,
                    encoding="utf-8",
                ).stdout.splitlines()
            )
            for relative in generate_global_plugin.TIER_SCOPED_POLICIES:
                self.assertIn(
                    relative,
                    tracked,
                    f"{relative} is not git-tracked under roster/",
                )
                self.assertTrue(
                    (plugin_root / "suite" / relative).is_file(),
                    f"{relative} did not land under suite/ in the generated package",
                )

    def test_every_relative_link_in_the_generated_package_resolves(self) -> None:
        """The generator rewrites relative links as it copies docs into
        suite/, and a rewrite that lands on the wrong depth produces a link
        that still *exists* -- so nothing catches it by inspection. 22 links
        pointing at unpackaged files, plus one silently retargeted to
        suite/roster/README.md instead of suite/README.md, went unnoticed
        exactly this way.
        """
        package = generated_package()
        broken = []
        for markdown in sorted(package.rglob("*.md")):
            for match in re.finditer(r"\]\((?!https?:|#|mailto:)([^)#]+)", markdown.read_text(encoding="utf-8")):
                if not (markdown.parent / match.group(1)).resolve().exists():
                    broken.append(f"{markdown.relative_to(package)} -> {match.group(1)}")
        self.assertEqual([], broken)

    def test_advertised_role_count_in_register_owned_prose_matches_the_catalog(self) -> None:
        """`packaging/plugin-README.md` is register-owned and rendered verbatim
        into the package (and thence the marketplace listing), so a stale count
        there advertises the wrong number of roles to installers.

        A drift check cannot catch this: the generator copies the wrong number
        faithfully, so package and register agree and `--check` passes. The
        equivalent assertion over the two plugin.json manifests moved to
        deagy/cadre-lifecycle with the manifests themselves; this covers the half
        whose source lives here.
        """
        readme = (REPOSITORY_ROOT / "packaging" / "plugin-README.md").read_text(encoding="utf-8")
        advertised = {int(value) for value in re.findall(r"(\d+)\s*\n?specialist roles", readme)}
        advertised |= {int(value) for value in re.findall(r"(\d+) specialist roles", readme)}
        self.assertEqual(
            {EXPECTED_ROLE_COUNT},
            advertised,
            "packaging/plugin-README.md advertises a role count that is not EXPECTED_ROLE_COUNT",
        )

    def test_repository_profile_and_local_override_policy_stay_current(self) -> None:
        # team-profile.yaml is optional: absence is not a defect, so this test
        # only asserts something when the file is actually present. What it
        # asserts is deliberately about content *shape* (no PII), not exact
        # prose -- generate_global_plugin.py embeds this file verbatim into
        # every generated role wrapper (71+ files, including a separately
        # published public repo), so a personal name here would leak broadly.
        profile_path = ROOT / "shared" / "team-profile.yaml"
        if profile_path.is_file():
            profile = profile_path.read_text(encoding="utf-8")
            self.assertNotRegex(
                profile,
                r"[\w.+-]+@[\w-]+\.[\w.-]+|Daniel Eagy",
                "roster/shared/team-profile.yaml must not contain personal names or "
                "emails -- it is embedded verbatim into every generated role wrapper",
            )
            self.assertNotIn(
                "\nroles:",
                profile,
                "roster/shared/team-profile.yaml must not carry a 'roles' block naming "
                "individuals -- named authority belongs in a consuming project's own "
                "local/untracked config or its agentic-sdlc lifecycle records",
            )

        for local_root in (
            REPOSITORY_ROOT / ".claude" / "agents",
            REPOSITORY_ROOT / ".codex" / "agents",
        ):
            self.assertFalse(
                any(local_root.glob("*.md")) or any(local_root.glob("*.toml")),
                f"stale project-local agent overrides remain under {local_root}",
            )

        secure_cloud = json.loads(
            (PROVIDER_ROOT / "profiles" / "secure-cloud" / "profile.json").read_text(encoding="utf-8")
        )
        self.assertEqual(19, len(secure_cloud["agents"]))
        catalog = json.loads((PROVIDER_ROOT / "agent-catalog.json").read_text(encoding="utf-8"))
        self.assertEqual(EXPECTED_ROLE_COUNT, len(catalog["agents"]))

    def test_codex_bootstrap_preserves_bare_files_and_rejects_unowned_collision(self) -> None:
        script = ROOT / "orchestration" / "src" / "sync_codex_agents.py"
        with tempfile.TemporaryDirectory() as temporary_directory:
            temporary = Path(temporary_directory)
            source = temporary / "source"
            target = temporary / "home" / ".codex" / "agents"
            source.mkdir()
            target.mkdir(parents=True)
            generated = (
                "# GENERATED FILE: canonical source is roster/review/code-reviewer/AGENT.md\n"
                'name = "agents-code-reviewer"\n'
            )
            (source / "agents-code-reviewer.toml").write_text(generated, encoding="utf-8")
            bare = target / "code-reviewer.toml"
            bare.write_text("user-owned bare wrapper\n", encoding="utf-8")

            installed = subprocess.run(
                [sys.executable, str(script), "--source", str(source), "--target", str(target)],
                check=True, capture_output=True, text=True,
            )
            self.assertIn("Installed 1", installed.stdout)
            self.assertEqual("user-owned bare wrapper\n", bare.read_text(encoding="utf-8"))

            namespaced = target / "agents-code-reviewer.toml"
            namespaced.write_text("", encoding="utf-8")
            empty_rejected = subprocess.run(
                [sys.executable, str(script), "--source", str(source), "--target", str(target)],
                check=False, capture_output=True, text=True,
            )
            self.assertNotEqual(0, empty_rejected.returncode)
            self.assertIn("Refusing to overwrite unowned", empty_rejected.stderr)
            self.assertEqual("", namespaced.read_text(encoding="utf-8"))

            namespaced.write_text("user-owned collision\n", encoding="utf-8")
            rejected = subprocess.run(
                [sys.executable, str(script), "--source", str(source), "--target", str(target)],
                check=False, capture_output=True, text=True,
            )
            self.assertNotEqual(0, rejected.returncode)
            self.assertIn("Refusing to overwrite unowned", rejected.stderr)
            self.assertEqual("user-owned collision\n", namespaced.read_text(encoding="utf-8"))

    @unittest.skipIf(sys.platform == "win32", "POSIX symlink behavior is required")
    def test_codex_bootstrap_rejects_symlinked_wrappers(self) -> None:
        script = ROOT / "orchestration" / "src" / "sync_codex_agents.py"
        with tempfile.TemporaryDirectory() as temporary_directory:
            temporary = Path(temporary_directory)
            source = temporary / "source"
            target = temporary / "home" / ".codex" / "agents"
            source.mkdir()
            target.mkdir(parents=True)
            generated = (
                "# GENERATED FILE: canonical source is roster/review/code-reviewer/AGENT.md\n"
                'name = "agents-code-reviewer"\n'
            )
            real_source = source / "real.toml"
            real_source.write_text(generated, encoding="utf-8")
            symlinked_source = source / "agents-code-reviewer.toml"
            os.symlink(real_source, symlinked_source)

            source_rejected = subprocess.run(
                [sys.executable, str(script), "--source", str(source), "--target", str(target)],
                check=False, capture_output=True, text=True,
            )
            self.assertNotEqual(0, source_rejected.returncode)
            self.assertIn("Refusing non-regular source wrapper", source_rejected.stderr)

        with tempfile.TemporaryDirectory() as temporary_directory:
            temporary = Path(temporary_directory)
            source = temporary / "source"
            target = temporary / "home" / ".codex" / "agents"
            source.mkdir()
            target.mkdir(parents=True)
            (source / "agents-code-reviewer.toml").write_text(generated, encoding="utf-8")
            real_destination = target / "real-destination.toml"
            real_destination.write_text("user-owned destination\n", encoding="utf-8")
            os.symlink(real_destination, target / "agents-code-reviewer.toml")

            destination_rejected = subprocess.run(
                [sys.executable, str(script), "--source", str(source), "--target", str(target)],
                check=False, capture_output=True, text=True,
            )
            self.assertNotEqual(0, destination_rejected.returncode)
            self.assertIn("Refusing symlinked destination wrapper", destination_rejected.stderr)
            self.assertEqual("user-owned destination\n", real_destination.read_text(encoding="utf-8"))

    def test_codex_bootstrap_writes_role_index_with_resolved_paths_and_models(self) -> None:
        script = ROOT / "orchestration" / "src" / "sync_codex_agents.py"
        with tempfile.TemporaryDirectory() as temporary_directory:
            temporary = Path(temporary_directory)
            source = temporary / "source"
            target = temporary / "home" / ".codex" / "agents"
            source.mkdir()
            target.mkdir(parents=True)
            (source / "agents-code-reviewer.toml").write_text(
                "# GENERATED FILE: canonical source is roster/review/code-reviewer/AGENT.md\n"
                'name = "agents-code-reviewer"\n'
                'model = "gpt-5-mini"\n',
                encoding="utf-8",
            )
            (source / "agents-test-engineer.toml").write_text(
                "# GENERATED FILE: canonical source is roster/review/test-engineer/AGENT.md\n"
                'name = "agents-test-engineer"\n',
                encoding="utf-8",
            )

            result = subprocess.run(
                [sys.executable, str(script), "--source", str(source), "--target", str(target)],
                check=True, capture_output=True, text=True,
            )
            self.assertIn("Index installed", result.stdout)

            index_path = target / "agents-index.json"
            index = json.loads(index_path.read_text(encoding="utf-8"))
            self.assertEqual(1, index["schema_version"])
            self.assertEqual("# GENERATED FILE: canonical source is roster/", index["generated_marker"])
            self.assertEqual(
                {
                    "code-reviewer": {
                        "path": str((target / "agents-code-reviewer.toml").resolve()),
                        "model": "gpt-5-mini",
                    },
                    "test-engineer": {
                        "path": str((target / "agents-test-engineer.toml").resolve()),
                        "model": None,
                    },
                },
                index["roles"],
            )

    def test_codex_bootstrap_role_index_is_byte_identical_across_unchanged_reruns(self) -> None:
        script = ROOT / "orchestration" / "src" / "sync_codex_agents.py"
        with tempfile.TemporaryDirectory() as temporary_directory:
            temporary = Path(temporary_directory)
            source = temporary / "source"
            target = temporary / "home" / ".codex" / "agents"
            source.mkdir()
            target.mkdir(parents=True)
            (source / "agents-code-reviewer.toml").write_text(
                "# GENERATED FILE: canonical source is roster/review/code-reviewer/AGENT.md\n"
                'name = "agents-code-reviewer"\n'
                'model = "gpt-5-mini"\n',
                encoding="utf-8",
            )
            index_path = target / "agents-index.json"

            first = subprocess.run(
                [sys.executable, str(script), "--source", str(source), "--target", str(target)],
                check=True, capture_output=True, text=True,
            )
            self.assertIn("Index installed", first.stdout)
            first_bytes = index_path.read_bytes()

            second = subprocess.run(
                [sys.executable, str(script), "--source", str(source), "--target", str(target)],
                check=True, capture_output=True, text=True,
            )
            self.assertIn("Index unchanged", second.stdout)
            self.assertEqual(first_bytes, index_path.read_bytes())

    def test_codex_bootstrap_role_index_updates_when_source_model_changes(self) -> None:
        script = ROOT / "orchestration" / "src" / "sync_codex_agents.py"
        with tempfile.TemporaryDirectory() as temporary_directory:
            temporary = Path(temporary_directory)
            source = temporary / "source"
            target = temporary / "home" / ".codex" / "agents"
            source.mkdir()
            target.mkdir(parents=True)
            wrapper_source = source / "agents-code-reviewer.toml"
            wrapper_source.write_text(
                "# GENERATED FILE: canonical source is roster/review/code-reviewer/AGENT.md\n"
                'name = "agents-code-reviewer"\n'
                'model = "gpt-5-mini"\n',
                encoding="utf-8",
            )
            index_path = target / "agents-index.json"

            subprocess.run(
                [sys.executable, str(script), "--source", str(source), "--target", str(target)],
                check=True, capture_output=True, text=True,
            )
            self.assertEqual("gpt-5-mini", json.loads(index_path.read_text(encoding="utf-8"))["roles"]["code-reviewer"]["model"])

            wrapper_source.write_text(
                "# GENERATED FILE: canonical source is roster/review/code-reviewer/AGENT.md\n"
                'name = "agents-code-reviewer"\n'
                'model = "gpt-5"\n',
                encoding="utf-8",
            )
            updated = subprocess.run(
                [sys.executable, str(script), "--source", str(source), "--target", str(target)],
                check=True, capture_output=True, text=True,
            )
            self.assertIn("Index installed", updated.stdout)
            self.assertEqual("gpt-5", json.loads(index_path.read_text(encoding="utf-8"))["roles"]["code-reviewer"]["model"])

    def test_codex_bootstrap_role_index_rejects_unowned_collision(self) -> None:
        script = ROOT / "orchestration" / "src" / "sync_codex_agents.py"
        with tempfile.TemporaryDirectory() as temporary_directory:
            temporary = Path(temporary_directory)
            source = temporary / "source"
            target = temporary / "home" / ".codex" / "agents"
            source.mkdir()
            target.mkdir(parents=True)
            (source / "agents-code-reviewer.toml").write_text(
                "# GENERATED FILE: canonical source is roster/review/code-reviewer/AGENT.md\n"
                'name = "agents-code-reviewer"\n',
                encoding="utf-8",
            )
            index_path = target / "agents-index.json"
            index_path.write_text('{"unowned": true}', encoding="utf-8")

            rejected = subprocess.run(
                [sys.executable, str(script), "--source", str(source), "--target", str(target)],
                check=False, capture_output=True, text=True,
            )
            self.assertNotEqual(0, rejected.returncode)
            self.assertIn("Refusing to overwrite unowned", rejected.stderr)
            self.assertEqual('{"unowned": true}', index_path.read_text(encoding="utf-8"))

    @unittest.skipIf(sys.platform == "win32", "POSIX symlink behavior is required")
    def test_codex_bootstrap_rejects_symlinked_role_index(self) -> None:
        script = ROOT / "orchestration" / "src" / "sync_codex_agents.py"
        with tempfile.TemporaryDirectory() as temporary_directory:
            temporary = Path(temporary_directory)
            source = temporary / "source"
            target = temporary / "home" / ".codex" / "agents"
            source.mkdir()
            target.mkdir(parents=True)
            (source / "agents-code-reviewer.toml").write_text(
                "# GENERATED FILE: canonical source is roster/review/code-reviewer/AGENT.md\n"
                'name = "agents-code-reviewer"\n',
                encoding="utf-8",
            )
            real_destination = target / "real-index.json"
            real_destination.write_text("user-owned index\n", encoding="utf-8")
            os.symlink(real_destination, target / "agents-index.json")

            rejected = subprocess.run(
                [sys.executable, str(script), "--source", str(source), "--target", str(target)],
                check=False, capture_output=True, text=True,
            )
            self.assertNotEqual(0, rejected.returncode)
            self.assertIn("Refusing symlinked destination wrapper", rejected.stderr)
            self.assertEqual("user-owned index\n", real_destination.read_text(encoding="utf-8"))

    def test_codex_bootstrap_role_index_left_unchanged_when_a_wrapper_write_fails(self) -> None:
        script = ROOT / "orchestration" / "src" / "sync_codex_agents.py"
        with tempfile.TemporaryDirectory() as temporary_directory:
            temporary = Path(temporary_directory)
            source = temporary / "source"
            target = temporary / "home" / ".codex" / "agents"
            source.mkdir()
            target.mkdir(parents=True)
            # "code-reviewer" sorts before "test-engineer", so the loop writes
            # it successfully before reaching the collision below.
            (source / "agents-code-reviewer.toml").write_text(
                "# GENERATED FILE: canonical source is roster/review/code-reviewer/AGENT.md\n"
                'name = "agents-code-reviewer"\n',
                encoding="utf-8",
            )
            (source / "agents-test-engineer.toml").write_text(
                "# GENERATED FILE: canonical source is roster/review/test-engineer/AGENT.md\n"
                'name = "agents-test-engineer"\n',
                encoding="utf-8",
            )
            index_path = target / "agents-index.json"

            # First run: no collision yet, establishes an installed index.
            first = subprocess.run(
                [sys.executable, str(script), "--source", str(source), "--target", str(target)],
                check=True, capture_output=True, text=True,
            )
            self.assertIn("Index installed", first.stdout)
            established_index_bytes = index_path.read_bytes()

            # Corrupt one of the already-installed namespaced wrappers so the
            # next run fails partway through the per-wrapper loop, before the
            # index would be rebuilt.
            (target / "agents-test-engineer.toml").write_text(
                "user-owned collision\n", encoding="utf-8",
            )
            failing = subprocess.run(
                [sys.executable, str(script), "--source", str(source), "--target", str(target)],
                check=False, capture_output=True, text=True,
            )
            self.assertNotEqual(0, failing.returncode)
            self.assertIn("Refusing to overwrite unowned", failing.stderr)
            self.assertTrue(
                (target / "agents-code-reviewer.toml").read_text(encoding="utf-8").startswith(
                    "# GENERATED FILE:"
                ),
                "the wrapper preceding the collision should still have been written",
            )
            self.assertEqual(
                established_index_bytes,
                index_path.read_bytes(),
                "the index must not change when a wrapper write fails mid-loop",
            )

    def test_codex_bootstrap_role_index_prunes_roles_removed_from_source(self) -> None:
        script = ROOT / "orchestration" / "src" / "sync_codex_agents.py"
        with tempfile.TemporaryDirectory() as temporary_directory:
            temporary = Path(temporary_directory)
            source = temporary / "source"
            target = temporary / "home" / ".codex" / "agents"
            source.mkdir()
            target.mkdir(parents=True)
            code_reviewer_source = source / "agents-code-reviewer.toml"
            code_reviewer_source.write_text(
                "# GENERATED FILE: canonical source is roster/review/code-reviewer/AGENT.md\n"
                'name = "agents-code-reviewer"\n',
                encoding="utf-8",
            )
            (source / "agents-test-engineer.toml").write_text(
                "# GENERATED FILE: canonical source is roster/review/test-engineer/AGENT.md\n"
                'name = "agents-test-engineer"\n',
                encoding="utf-8",
            )
            index_path = target / "agents-index.json"

            subprocess.run(
                [sys.executable, str(script), "--source", str(source), "--target", str(target)],
                check=True, capture_output=True, text=True,
            )
            first_roles = json.loads(index_path.read_text(encoding="utf-8"))["roles"]
            self.assertEqual({"code-reviewer", "test-engineer"}, set(first_roles))

            code_reviewer_source.unlink()
            subprocess.run(
                [sys.executable, str(script), "--source", str(source), "--target", str(target)],
                check=True, capture_output=True, text=True,
            )
            second_roles = json.loads(index_path.read_text(encoding="utf-8"))["roles"]
            self.assertEqual({"test-engineer"}, set(second_roles))

    @unittest.skipUnless(hasattr(os, "O_NOFOLLOW"), "O_NOFOLLOW support is required")
    def test_codex_bootstrap_no_follow_guards_run_at_open_time(self) -> None:
        import importlib.util

        script = ROOT / "orchestration" / "src" / "sync_codex_agents.py"
        spec = importlib.util.spec_from_file_location("sync_codex_agents_under_test", script)
        self.assertIsNotNone(spec)
        self.assertIsNotNone(spec.loader)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)

        with tempfile.TemporaryDirectory() as temporary_directory:
            temporary = Path(temporary_directory)
            source_target = temporary / "source-target.toml"
            source_target.write_text("source content\n", encoding="utf-8")
            source_link = temporary / "agents-source.toml"
            os.symlink(source_target, source_link)
            with self.assertRaises(OSError):
                module._read_regular_file(source_link)

            destination_target = temporary / "destination-target.toml"
            destination_target.write_text("destination content\n", encoding="utf-8")
            destination_link = temporary / "agents-destination.toml"
            os.symlink(destination_target, destination_link)
            with mock.patch.object(Path, "is_symlink", return_value=False):
                with self.assertRaises(OSError):
                    module._write_owned_wrapper(destination_link, b"new content\n")
            self.assertEqual("destination content\n", destination_target.read_text(encoding="utf-8"))

    @unittest.skipUnless(sys.platform != "win32", "packaged wrapper is a POSIX sh script")
    def test_packaged_selector_targets_callers_git_repository(self) -> None:
        wrapper = generated_package() / "bin" / "cadre"
        with tempfile.TemporaryDirectory() as temporary_directory:
            target = Path(temporary_directory) / "unrelated"
            target.mkdir()
            subprocess.run(["git", "init", "-q", str(target)], check=True)
            subprocess.run(["git", "-C", str(target), "config", "user.email", "test@example.invalid"], check=True)
            subprocess.run(["git", "-C", str(target), "config", "user.name", "Test"], check=True)
            (target / "README.md").write_text("base\n", encoding="utf-8")
            subprocess.run(["git", "-C", str(target), "add", "."], check=True)
            subprocess.run(["git", "-C", str(target), "commit", "-qm", "base"], check=True)
            base = subprocess.run(
                ["git", "-C", str(target), "rev-parse", "HEAD"],
                check=True, capture_output=True, text=True, encoding="utf-8",
            ).stdout.strip()
            (target / "frontend").mkdir()
            (target / "frontend" / "App.tsx").write_text("export default 1\n", encoding="utf-8")

            status = subprocess.run(
                [str(wrapper), "select", "--task", "Update React"],
                cwd=target, check=True, capture_output=True, text=True,
            )
            status_plan = json.loads(status.stdout)
            self.assertEqual(str(target.resolve()), status_plan["inputs"]["repository_root"])
            self.assertEqual(["frontend/App.tsx"], status_plan["inputs"]["changed_files"])

            subprocess.run(["git", "-C", str(target), "add", "."], check=True)
            subprocess.run(["git", "-C", str(target), "commit", "-qm", "frontend"], check=True)
            diff = subprocess.run(
                [str(wrapper), "select", "--task", "Update React", "--base", base],
                cwd=target, check=True, capture_output=True, text=True,
            )
            self.assertEqual(["frontend/App.tsx"], json.loads(diff.stdout)["inputs"]["changed_files"])

    def test_generated_package_has_no_source_paths_or_unsafe_relative_documentation_paths(self) -> None:
        generator = REPOSITORY_ROOT / "roster" / "orchestration" / "src" / "generate_global_plugin.py"
        with tempfile.TemporaryDirectory(prefix="agents-packaging-") as temporary_directory:
            plugin_root = Path(temporary_directory) / "plugin"
            subprocess.run(
                [sys.executable, str(generator), "--output", str(plugin_root)],
                cwd=REPOSITORY_ROOT,
                check=True,
                capture_output=True,
                text=True,
                encoding="utf-8",
            )
            for path in plugin_root.rglob("*"):
                if not path.is_file():
                    continue
                content = path.read_text(encoding="utf-8", errors="ignore")
                self.assertNotIn(str(REPOSITORY_ROOT), content, str(path))
            for path in (plugin_root / "suite" / "roster").rglob("*.md"):
                content = path.read_text(encoding="utf-8")
                for raw_relative in re.findall(r"(?<!https:)(?<!https://)(\.\./[^\s`)'\"]+)", content):
                    relative = raw_relative.rstrip(".,")
                    target = (path.parent / relative).resolve()
                    self.assertTrue(target.is_file() or target.is_dir(), f"{path}: {relative}")

    def test_every_embedded_shared_policy_is_vendored_into_the_wheel(self) -> None:
        """pyproject.toml's [tool.hatch.build.targets.wheel.force-include]
        lists roster/shared/ files *individually* (hatchling's force-include
        does not honor exclude patterns, so the subtree cannot be included
        wholesale). That is a second, separate allowlist from
        generate_suite_copy()'s -- which prefix-matches `roster/shared/` and
        therefore needs no edit for a new file.

        Omitting a new shared policy here does not crash: role_wrapper_inputs()
        deliberately *skips* a missing shared file, so an installed
        distribution would silently generate wrappers without the policy, and
        `cadre generate-role-metadata --check` would then report drift against
        the vendored provider/ copies that do contain it. This test makes that
        silent divergence a loud one.
        """
        sys.path.insert(0, str(ROOT / "orchestration" / "src"))
        try:
            import generate_global_plugin
        finally:
            sys.path.pop(0)

        pyproject = (REPOSITORY_ROOT / "pyproject.toml").read_text(encoding="utf-8")
        embedded = list(generate_global_plugin.SHARED_POLICIES) + list(
            generate_global_plugin.TIER_SCOPED_POLICIES
        )
        missing = [
            relative
            for relative in embedded
            if f'"{relative}" = "cadre_cli/_vendor/{relative}"' not in pyproject
        ]
        self.assertEqual(
            [],
            missing,
            "shared policy files embedded into generated wrappers but not "
            "force-included into the wheel (add them to pyproject.toml's "
            "[tool.hatch.build.targets.wheel.force-include])",
        )

    def test_every_shared_policy_file_is_embedded_or_explicitly_exempted(self) -> None:
        """A shared file under roster/shared/ can silently never reach any
        agent: SHARED_POLICIES and TIER_SCOPED_POLICIES are opt-in
        allowlists, so adding a new roster/shared/*.md policy without adding
        it to either is not an error anywhere else -- it just quietly never
        gets embedded (documentation-style.md shipped exactly this way for a
        full release). This test closes that gap by requiring every
        roster/shared/*.md file to be accounted for one of three ways:
        embedded universally (SHARED_POLICIES), embedded for a subset of
        capability tiers (TIER_SCOPED_POLICIES), or named in the allowlist
        below with a reason it is deliberately not embedded.
        """
        sys.path.insert(0, str(ROOT / "orchestration" / "src"))
        try:
            import generate_global_plugin
        finally:
            sys.path.pop(0)

        # Files that deliberately do not go through automatic embedding.
        # Adding a file here is a real design decision, not a way to silence
        # this test -- explain why the file is exempt.
        not_embedded_by_design = {
            # Directory README, not agent-facing policy content.
            "roster/shared/README.md": "index/reference document, not a role policy",
            # Opt-in policy referenced explicitly by the specific roles that
            # need it (cloud-architect, etc.), not every role -- unlike
            # documentation-style.md, applying to a narrow, deliberately
            # chosen subset is the intended shape here.
            "roster/shared/cloud-guardrails.md": "opt-in, referenced explicitly by the roles it applies to",
            "roster/shared/secure-development-policy.md": "opt-in, referenced explicitly by the roles it applies to",
            # General reference material cited from RUNBOOK.md as shared
            # context for reviewers/release engineers, not per-role
            # instructions meant to be embedded into every wrapper.
            "roster/shared/definition-of-done.md": "general completion-bar reference cited from RUNBOOK.md, not per-role embedded policy",
            "roster/shared/risk-severity-model.md": "general reference cited from RUNBOOK.md, not per-role embedded policy",
        }

        embedded = set(generate_global_plugin.SHARED_POLICIES) | set(
            generate_global_plugin.TIER_SCOPED_POLICIES
        )
        shared_markdown_files = {
            f"roster/shared/{path.name}"
            for path in (REPOSITORY_ROOT / "roster" / "shared").glob("*.md")
        }

        unaccounted = sorted(
            shared_markdown_files - embedded - set(not_embedded_by_design)
        )
        self.assertEqual(
            [],
            unaccounted,
            "roster/shared/*.md file(s) are neither embedded (SHARED_POLICIES "
            "or TIER_SCOPED_POLICIES in generate_global_plugin.py) nor listed "
            "in this test's not_embedded_by_design allowlist with a reason: "
            f"{unaccounted}. Add the file to one of those, deliberately.",
        )

        stale_allowlist_entries = sorted(
            set(not_embedded_by_design) - shared_markdown_files
        )
        self.assertEqual(
            [],
            stale_allowlist_entries,
            "not_embedded_by_design names file(s) that no longer exist under "
            f"roster/shared/: {stale_allowlist_entries}. Remove the stale entry.",
        )

    def test_secure_cloud_agents_plugin_is_self_contained(self) -> None:
        plugin_root = generated_package()
        provider = json.loads((plugin_root / "provider.json").read_text(encoding="utf-8"))
        self.assertEqual("cadre", provider["id"])
        self.assertEqual("0.3.0", provider["version"])
        self.assertTrue((plugin_root / "suite" / "roster" / "catalog.yaml").is_file())
        offenders = []
        for path in plugin_root.rglob("*"):
            if (
                path.is_file()
                and path.suffix not in {".pyc", ".pyo"}
                and "__pycache__" not in path.parts
                and str(REPOSITORY_ROOT) in path.read_text(encoding="utf-8", errors="ignore")
            ):
                offenders.append(str(path.relative_to(plugin_root)))
        self.assertEqual([], offenders)

    @staticmethod
    def _semver_tuple(value: str) -> tuple[int, int, int]:
        # Verbatim copy of `semver_tuple` from `agentic-sdlc`'s
        # plugins/agentic-sdlc/scripts/agentic_sdlc.py (lines 84-88), in a
        # `deagy/agentic-sdlc` checkout. Reimplemented locally rather than imported because
        # `AGENTIC_SDLC_BIN`/`PATH` resolution does not guarantee an importable
        # layout for the standalone kernel script.
        match = re.fullmatch(r"([0-9]+)\.([0-9]+)\.([0-9]+)", value)
        if not match:
            raise ValueError(f"invalid semantic version: {value}")
        return tuple(int(part) for part in match.groups())  # type: ignore[return-value]

    @classmethod
    def _kernel_version_in_range(cls, live: str, minimum: str, maximum_exclusive: str) -> bool:
        return cls._semver_tuple(minimum) <= cls._semver_tuple(live) < cls._semver_tuple(maximum_exclusive)

    def test_kernel_version_in_range_enforces_half_open_bounds(self) -> None:
        self.assertFalse(self._kernel_version_in_range("0.2.9", "0.3.0", "0.4.0"))
        self.assertTrue(self._kernel_version_in_range("0.3.0", "0.3.0", "0.4.0"))
        self.assertFalse(self._kernel_version_in_range("0.4.0", "0.3.0", "0.4.0"))
        self.assertTrue(self._kernel_version_in_range("0.3.9", "0.3.0", "0.4.0"))

    def test_secure_cloud_agents_provider_kernel_compatibility_covers_live_sdlc_version(self) -> None:
        self._require_agentic_sdlc()
        provider = json.loads(
            (PROVIDER_ROOT / "provider.json").read_text(encoding="utf-8")
        )
        minimum = provider["kernel_compatibility"]["minimum"]
        maximum_exclusive = provider["kernel_compatibility"]["maximum_exclusive"]
        self.assertRegex(minimum, r"^\d+\.\d+\.\d+$")
        self.assertRegex(maximum_exclusive, r"^\d+\.\d+\.\d+$")

        result = subprocess.run(
            [str(REPOSITORY_ROOT / "bin" / "cadre"), "sdlc", "--version"],
            cwd=REPOSITORY_ROOT,
            check=True,
            capture_output=True,
            text=True,
            encoding="utf-8",
            env=os.environ.copy(),
        )
        live_version = result.stdout.strip()

        self.assertTrue(
            self._kernel_version_in_range(live_version, minimum, maximum_exclusive),
            f"live agentic-sdlc kernel version {live_version!r} is outside the "
            f"provider-declared range [{minimum}, {maximum_exclusive})",
        )

    def test_bin_agents_wrapper_is_executable(self) -> None:
        wrapper = REPOSITORY_ROOT / "bin" / "cadre"
        self.assertTrue(wrapper.is_file(), str(wrapper))
        self.assertTrue(os.access(wrapper, os.X_OK), f"{wrapper} is not executable")

    def test_bin_agents_delegates_sdlc_to_standalone_kernel(self) -> None:
        self._require_agentic_sdlc()
        result = subprocess.run(
            [str(REPOSITORY_ROOT / "bin" / "cadre"), "sdlc", "--version"],
            cwd=REPOSITORY_ROOT,
            check=True,
            capture_output=True,
            text=True,
            encoding="utf-8",
            env=os.environ.copy(),
        )
        # Derived, not hardcoded. This asserted the literal "0.13.0" and so
        # broke the moment the kernel was released -- the same class of bug
        # as the pinned marketplace tags and the "Agentic SDLC v0.3.x"
        # strings. It also passed locally while failing in CI, because a
        # developer machine with an older agentic-sdlc on PATH resolves that
        # instead of the in-tree kernel CI points AGENTIC_SDLC_BIN at.
        expected = re.search(
            r'^VERSION = "(\d+\.\d+\.\d+)"',
            (REPOSITORY_ROOT / "kernel" / "agentic_sdlc" / "__init__.py").read_text(encoding="utf-8"),
            re.MULTILINE,
        )
        self.assertIsNotNone(expected, "kernel VERSION not found")
        self.assertEqual(expected.group(1), result.stdout.strip())

    @unittest.skipUnless(sys.platform != "win32", "bin/cadre is a POSIX sh script")
    def test_bin_agents_wrapper_dispatches_select_matching_direct_invocation(self) -> None:
        self._require_agentic_sdlc()
        wrapper = REPOSITORY_ROOT / "bin" / "cadre"
        selector = ROOT / "orchestration" / "src" / "select_agents.py"
        arguments = [
            "--task", "Update the React navigation",
            "--files", "frontend/src/Nav.tsx",
            "--classification", "internal",
            "--task-id", "WRAPPER-HEALTH-1",
        ]
        direct = subprocess.run(
            [sys.executable, str(selector), *arguments],
            cwd=REPOSITORY_ROOT,
            check=True,
            capture_output=True,
            text=True,
            encoding="utf-8",
        )
        via_wrapper = subprocess.run(
            [str(wrapper), "select", *arguments],
            cwd=REPOSITORY_ROOT,
            check=True,
            capture_output=True,
            text=True,
            encoding="utf-8",
        )
        direct_payload = json.loads(direct.stdout)
        wrapper_payload = json.loads(via_wrapper.stdout)
        direct_payload.pop("generated_at", None)
        wrapper_payload.pop("generated_at", None)
        self.assertEqual(direct_payload, wrapper_payload)

    @unittest.skipUnless(sys.platform != "win32", "bin/cadre is a POSIX sh script")
    def test_bin_agents_wrapper_resolves_correctly_through_a_symlink(self) -> None:
        self._require_agentic_sdlc()
        wrapper = REPOSITORY_ROOT / "bin" / "cadre"
        with tempfile.TemporaryDirectory() as temporary_directory:
            link = Path(temporary_directory) / "agents"
            link.symlink_to(wrapper)
            result = subprocess.run(
                [
                    str(link), "select",
                    "--root", str(REPOSITORY_ROOT),
                    "--task", "Capture product intent",
                    "--files", "README.md",
                    "--classification", "internal",
                    "--task-id", "WRAPPER-HEALTH-2",
                ],
                cwd=temporary_directory,
                check=True,
                capture_output=True,
                text=True,
                encoding="utf-8",
            )
            self.assertEqual("ready", json.loads(result.stdout)["status"])

    @unittest.skipUnless(sys.platform != "win32", "bin/cadre is a POSIX sh script")
    def test_bin_agents_wrapper_rejects_unknown_subcommand(self) -> None:
        wrapper = REPOSITORY_ROOT / "bin" / "cadre"
        result = subprocess.run(
            [str(wrapper), "not-a-real-subcommand"],
            cwd=REPOSITORY_ROOT,
            check=False,
            capture_output=True,
            text=True,
            encoding="utf-8",
        )
        self.assertNotEqual(0, result.returncode)
        self.assertIn("unknown subcommand", result.stderr)

    def test_secure_cloud_agents_plugin_bin_wrapper_matches_direct_invocation(self) -> None:
        self._require_agentic_sdlc()
        wrapper = generated_package() / "bin" / "cadre"
        self.assertTrue(wrapper.is_file(), str(wrapper))
        self.assertTrue(os.access(wrapper, os.X_OK), f"{wrapper} is not executable")
        selector = ROOT / "orchestration" / "src" / "select_agents.py"
        arguments = [
            "--root", str(REPOSITORY_ROOT),
            "--task", "Update the React navigation",
            "--files", "frontend/src/Nav.tsx",
            "--classification", "internal",
            "--task-id", "WRAPPER-HEALTH-4",
        ]
        direct = subprocess.run(
            [sys.executable, str(selector), *arguments],
            cwd=REPOSITORY_ROOT,
            check=True,
            capture_output=True,
            text=True,
            encoding="utf-8",
        )
        with tempfile.TemporaryDirectory() as temporary_directory:
            via_plugin_wrapper = subprocess.run(
                [str(wrapper), "select", *arguments],
                cwd=temporary_directory,
                check=True,
                capture_output=True,
                text=True,
                encoding="utf-8",
            )
        direct_payload = json.loads(direct.stdout)
        wrapper_payload = json.loads(via_plugin_wrapper.stdout)
        direct_payload.pop("generated_at", None)
        wrapper_payload.pop("generated_at", None)
        for payload in (direct_payload, wrapper_payload):
            payload.pop("dispatch_fingerprint", None)
            # Resolved from the *invoking* directory, not --root, and the
            # packaged wrapper is deliberately run from a non-git temporary
            # directory here to prove it does not depend on its own location.
            payload.get("provenance", {}).pop("git_commit_sha", None)
            payload.get("provenance", {}).pop("git_dirty_paths", None)
            for request in payload.get("knowledge_context", {}).get("requests", []):
                request["invocation"]["args"][0] = "<packaged-knowledge-cli>"
        self.assertEqual(direct_payload, wrapper_payload)

    def test_packaged_bin_wrapper_sdlc_resolves_agentic_sdlc_bin_through_settings_py(self) -> None:
        """The packaged wrapper's `sdlc` branch must resolve
        agentic_sdlc.bin_path through roster/shared/src/settings.py's real
        precedence chain (env > project/global config file > shutil.which),
        not a second, hand-rolled shell-only ${AGENTIC_SDLC_BIN}/`command -v`
        resolution that would silently ignore a configured value -- this was
        a real gap flagged in review: the generated wrapper previously never
        consulted a config file at all.
        """
        wrapper = generated_package() / "bin" / "cadre"
        self.assertTrue(wrapper.is_file(), str(wrapper))
        with tempfile.TemporaryDirectory() as xdg_home:
            (Path(xdg_home) / "cadre").mkdir(parents=True)
            (Path(xdg_home) / "cadre" / "config.yaml").write_text(
                'agentic_sdlc:\n  bin_path: "/nonexistent/fake-agentic-sdlc-from-config"\n',
                encoding="utf-8",
            )
            env = os.environ.copy()
            env.pop("AGENTIC_SDLC_BIN", None)
            env["XDG_CONFIG_HOME"] = xdg_home
            with tempfile.TemporaryDirectory() as cwd:
                result = subprocess.run(
                    [str(wrapper), "sdlc", "--version"],
                    cwd=cwd,
                    check=False,
                    capture_output=True,
                    text=True,
                    encoding="utf-8",
                    env=env,
                )
        self.assertNotEqual(0, result.returncode)
        self.assertIn("fake-agentic-sdlc-from-config", result.stderr)

    def test_packaged_bin_wrapper_accepts_a_leading_interactive_flag(self) -> None:
        wrapper = generated_package() / "bin" / "cadre"
        result = subprocess.run(
            [str(wrapper), "--interactive", "help"],
            cwd=REPOSITORY_ROOT,
            check=True,
            capture_output=True,
            text=True,
            encoding="utf-8",
            env=os.environ.copy(),
        )
        self.assertIn("Usage: cadre [--interactive]", result.stdout)

    @unittest.skipUnless(sys.platform != "win32", "bin/cadre is a POSIX sh script")
    def test_packaged_bin_wrapper_sdlc_needs_no_python_when_the_binary_is_already_locatable(self) -> None:
        """`cadre sdlc ...` must keep working on a host with no Python 3.10+
        at all whenever the binary is already locatable without consulting a
        config file (AGENTIC_SDLC_BIN, or `agentic-sdlc` on PATH) -- the
        pre-existing, Python-independent property of this branch. Only a
        config-file-supplied value legitimately requires Python.
        """
        wrapper = generated_package() / "bin" / "cadre"
        with tempfile.TemporaryDirectory() as fake_bin:
            stub = Path(fake_bin) / "agentic-sdlc"
            stub.write_text('#!/bin/sh\necho "STUB-SDLC $*"\n', encoding="utf-8")
            stub.chmod(0o755)
            other = Path(fake_bin) / "other-sdlc"
            other.write_text('#!/bin/sh\necho "STUB-SDLC $*"\n', encoding="utf-8")
            other.chmod(0o755)
            # PATH must contain no python3/python, but still enough for the
            # wrapper to function -- so it is the stub directory alone, plus
            # symlinks to the few external tools the wrapper actually calls.
            # Deliberately NOT `f"{fake_bin}:/bin"`: /bin is a symlink to
            # /usr/bin on most modern distributions, which silently puts
            # python3 back on PATH and makes this test vacuous (verified by
            # mutation -- with /bin present, the assertions below pass even
            # against a wrapper that hard-requires Python, i.e. they would
            # not catch the regression this test exists for).
            for tool in ("dirname",):
                located = shutil.which(tool)
                self.assertIsNotNone(located, f"{tool} not found; cannot build the test PATH")
                os.symlink(located, Path(fake_bin) / tool)
            self.assertIsNone(
                shutil.which("python3", path=fake_bin),
                "test PATH must not contain python3, or this test proves nothing",
            )
            bare_path = fake_bin

            via_env = subprocess.run(
                [str(wrapper), "sdlc", "--version"],
                cwd=REPOSITORY_ROOT,
                check=False,
                capture_output=True,
                text=True,
                encoding="utf-8",
                env={"PATH": bare_path, "AGENTIC_SDLC_BIN": str(other), "HOME": fake_bin},
            )
            self.assertEqual(0, via_env.returncode, via_env.stderr)
            self.assertIn("STUB-SDLC", via_env.stdout)

            via_path = subprocess.run(
                [str(wrapper), "sdlc", "--version"],
                cwd=REPOSITORY_ROOT,
                check=False,
                capture_output=True,
                text=True,
                encoding="utf-8",
                env={"PATH": bare_path, "HOME": fake_bin},
            )
            self.assertEqual(0, via_path.returncode, via_path.stderr)
            self.assertIn("STUB-SDLC", via_path.stdout)

            # With nothing locatable and no Python, the failure must still be
            # the actionable install pointer, never "Python 3.10+ is required".
            stub.unlink()
            unresolvable = subprocess.run(
                [str(wrapper), "sdlc", "--version"],
                cwd=REPOSITORY_ROOT,
                check=False,
                capture_output=True,
                text=True,
                encoding="utf-8",
                env={"PATH": bare_path, "HOME": fake_bin},
            )
            self.assertNotEqual(0, unresolvable.returncode)
            self.assertIn("install Agentic SDLC", unresolvable.stderr)
            self.assertNotIn("Python 3.10+ is required", unresolvable.stderr)

    @unittest.skipUnless(sys.platform != "win32", "requires a POSIX pty/controlling terminal")
    def test_packaged_bin_wrapper_interactive_sdlc_prompts_on_tty_and_keeps_stdout_clean(self) -> None:
        """End-to-end proof that `cadre --interactive sdlc ...` can actually
        prompt, and that prompt text never contaminates the value-carrying
        stdout.

        This is the only test that exercises the real failure mode the
        --interactive half of this feature was written for: the wrapper
        resolves the binary inside `$(...)`, whose child stdout is
        unconditionally a pipe, so a naive `sys.stdout.isatty()` gate makes
        prompting permanently unreachable no matter what terminal the
        operator is really sitting at. `pty.fork()` (not a pty fd handed to
        subprocess.Popen, which does NOT confer a controlling terminal on
        Linux) plus a separate pipe dup2'd onto fd 1 reproduces that exact
        shape.
        """
        import pty

        wrapper = generated_package() / "bin" / "cadre"
        self.assertTrue(wrapper.is_file(), str(wrapper))
        with tempfile.TemporaryDirectory() as fake_bin, tempfile.TemporaryDirectory() as fake_home:
            stub = Path(fake_bin) / "agentic-sdlc-chosen"
            stub.write_text('#!/bin/sh\necho "STUB-SDLC $*"\n', encoding="utf-8")
            stub.chmod(0o755)

            stdout_r, stdout_w = os.pipe()
            pid, master_fd = pty.fork()
            if pid == 0:  # pragma: no cover - child process
                try:
                    os.close(stdout_r)
                    os.dup2(stdout_w, 1)  # stdout = pipe, exactly like $(...)
                    os.close(stdout_w)
                    os.chdir(str(REPOSITORY_ROOT))
                    env = {
                        # Deliberately no agentic-sdlc on PATH and no
                        # AGENTIC_SDLC_BIN, so the field is genuinely
                        # unresolved and prompting is the only way forward.
                        "PATH": "/usr/bin:/bin",
                        "HOME": fake_home,
                        "XDG_CONFIG_HOME": str(Path(fake_home) / ".config"),
                    }
                    os.execve(str(wrapper), [str(wrapper), "--interactive", "sdlc", "--version"], env)
                except BaseException:  # noqa: BLE001 - child must never raise into the harness
                    pass
                os._exit(127)

            os.close(stdout_w)
            transcript = b""
            answered_value = False
            answered_tier = False
            try:
                deadline = time.monotonic() + 20
                while time.monotonic() < deadline:
                    try:
                        chunk = os.read(master_fd, 4096)
                    except OSError:
                        break
                    if not chunk:
                        break
                    transcript += chunk
                    if not answered_value and b"Enter value" in transcript:
                        os.write(master_fd, str(stub).encode() + b"\n")
                        answered_value = True
                    elif answered_value and not answered_tier and b"which tier" in transcript:
                        os.write(master_fd, b"skip\n")
                        answered_tier = True
                os.waitpid(pid, 0)
            finally:
                os.close(master_fd)
            captured_stdout = b""
            os.set_blocking(stdout_r, False)
            with contextlib.suppress(BlockingIOError, OSError):
                captured_stdout = os.read(stdout_r, 65536)
            os.close(stdout_r)

        # The prompt reached the terminal...
        self.assertIn(b"agentic_sdlc.bin_path is not configured", transcript)
        self.assertTrue(answered_value, f"never saw a value prompt; transcript={transcript!r}")
        # ...the chosen binary actually ran...
        self.assertIn(b"STUB-SDLC", captured_stdout)
        # ...and no prompt text leaked into the value-carrying stdout.
        self.assertNotIn(b"Enter value", captured_stdout)
        self.assertNotIn(b"is not configured", captured_stdout)

    @unittest.skipUnless(sys.platform != "win32", "bin/cadre is a POSIX sh script")
    def test_packaged_bin_wrapper_sdlc_fails_closed_on_a_resolve_error(self) -> None:
        """A `resolve` *failure* (not merely an unset value) must abort the
        sdlc branch, never fall through to a PATH lookup.

        A SettingsScopeError here means an untrusted project-local config
        tried to set the global-only agentic_sdlc.bin_path -- a security
        event that must stay fatal. The regression this guards against is
        someone "helpfully" adding a fallback that swallows it (e.g.
        `... || sdlc_bin=$(command -v agentic-sdlc || true)`), which would
        silently exec whatever binary is first on PATH instead. Verified by
        mutation: that exact edit turns this test red, while the plain
        `set -e` abort and the explicit `|| exit 1` both keep it green.
        """
        wrapper = generated_package() / "bin" / "cadre"
        with tempfile.TemporaryDirectory() as project, tempfile.TemporaryDirectory() as fake_bin:
            project_path = Path(project)
            (project_path / ".git").mkdir()
            (project_path / ".agents").mkdir()
            (project_path / ".agents" / "cadre.yaml").write_text(
                'agentic_sdlc:\n  bin_path: "/tmp/should-never-be-honored"\n', encoding="utf-8"
            )
            # A decoy on PATH: if the scope error were swallowed, this would
            # be silently exec'd and the test would see rc=0/DECOY.
            decoy = Path(fake_bin) / "agentic-sdlc"
            decoy.write_text('#!/bin/sh\necho "DECOY-SDLC"\n', encoding="utf-8")
            decoy.chmod(0o755)
            result = subprocess.run(
                [str(wrapper), "sdlc", "--version"],
                cwd=project,
                check=False,
                capture_output=True,
                text=True,
                encoding="utf-8",
                env={
                    "PATH": f"{fake_bin}:/usr/bin:/bin",
                    "HOME": fake_bin,
                    "XDG_CONFIG_HOME": str(Path(fake_bin) / ".config"),
                },
            )
        self.assertNotEqual(0, result.returncode)
        self.assertNotIn("DECOY-SDLC", result.stdout)
        self.assertIn("agentic_sdlc.bin_path", result.stderr)
        self.assertIn("project-local", result.stderr)

    @unittest.skipUnless(sys.platform != "win32", "bin/cadre is a POSIX sh script")
    def test_packaged_bin_wrapper_generic_subcommand_still_requires_python(self) -> None:
        """`detect_agent_python()` was extracted into a shell function with
        two call sites; the sdlc branch is covered above, this pins the
        other one (every non-sdlc subcommand), which no other test reaches.
        """
        wrapper = generated_package() / "bin" / "cadre"
        with tempfile.TemporaryDirectory() as empty_bin:
            result = subprocess.run(
                [str(wrapper), "select", "--help"],
                cwd=REPOSITORY_ROOT,
                check=False,
                capture_output=True,
                text=True,
                encoding="utf-8",
                env={"PATH": empty_bin, "HOME": empty_bin},
            )
        self.assertNotEqual(0, result.returncode)
        self.assertIn("Python 3.10+ is required", result.stderr)

    def test_bin_agents_subcommand_table_is_the_single_source_of_truth(self) -> None:
        table = REPOSITORY_ROOT / "bin" / "subcommands.tsv"
        self.assertTrue(table.is_file(), str(table))
        rows = [line.split("\t") for line in table.read_text(encoding="utf-8").splitlines() if line]
        self.assertTrue(rows)
        for name, script, description in rows:
            with self.subTest(subcommand=name):
                self.assertTrue((REPOSITORY_ROOT / script).is_file(), script)
                self.assertTrue(description)

        # bin/cadre.py owns table parsing, sdlc delegation, usage text, and
        # dispatch; the per-platform shims (bin/cadre, bin/cadre.ps1) only
        # find a Python interpreter and hand off to it, so this logic exists
        # exactly once instead of being duplicated per shell language.
        dispatcher_source = (REPOSITORY_ROOT / "bin" / "cadre.py").read_text(encoding="utf-8")
        self.assertIn("subcommands.tsv", dispatcher_source)

        sh_source = (REPOSITORY_ROOT / "bin" / "cadre").read_text(encoding="utf-8")
        ps1_source = (REPOSITORY_ROOT / "bin" / "cadre.ps1").read_text(encoding="utf-8")
        for source in (sh_source, ps1_source):
            self.assertNotIn("subcommands.tsv", source, "shims must not also parse the subcommand table")
            self.assertIn("cadre.py", source, "shims must hand off to the shared dispatcher")
            for _name, script, _description in rows:
                self.assertNotIn(script, source, "subcommand table must not also be hardcoded in the shim")

    def test_packaged_wrapper_covers_every_non_excluded_subcommand_table_entry(self) -> None:
        """Extends the `select`-only parity check above to every packaged
        subcommand: a bin/subcommands.tsv script-path change must show up in
        the packaged bin/cadre wrapper, not just for `select`.
        """
        sys.path.insert(0, str(ROOT / "orchestration" / "src"))
        try:
            import generate_global_plugin
        finally:
            sys.path.pop(0)

        rows = generate_global_plugin.packaged_subcommands(REPOSITORY_ROOT)
        self.assertTrue(rows)
        wrapper_source = (generated_package() / "bin" / "cadre").read_text(encoding="utf-8")
        for name, script in rows:
            with self.subTest(subcommand=name):
                self.assertIn(name, wrapper_source)
                self.assertIn(script, wrapper_source)
        for excluded in generate_global_plugin.PACKAGED_SUBCOMMAND_EXCLUSIONS:
            with self.subTest(excluded=excluded):
                self.assertNotIn(f"{excluded})", wrapper_source)

    def _powershell_interpreter(self) -> str | None:
        return shutil.which("pwsh") or shutil.which("powershell")

    def test_bin_agents_ps1_wrapper_dispatches_select_matching_direct_invocation(self) -> None:
        interpreter = self._powershell_interpreter()
        if interpreter is None:
            self.skipTest("no PowerShell interpreter (pwsh/powershell) available")
        wrapper = REPOSITORY_ROOT / "bin" / "cadre.ps1"
        selector = ROOT / "orchestration" / "src" / "select_agents.py"
        arguments = [
            "--task", "Update the React navigation",
            "--files", "frontend/src/Nav.tsx",
            "--classification", "internal",
            "--task-id", "WRAPPER-HEALTH-3",
        ]
        direct = subprocess.run(
            [sys.executable, str(selector), *arguments],
            cwd=REPOSITORY_ROOT,
            check=True,
            capture_output=True,
            text=True,
            encoding="utf-8",
        )
        via_wrapper = subprocess.run(
            [interpreter, "-File", str(wrapper), "select", *arguments],
            cwd=REPOSITORY_ROOT,
            check=True,
            capture_output=True,
            text=True,
            encoding="utf-8",
        )
        direct_payload = json.loads(direct.stdout)
        wrapper_payload = json.loads(via_wrapper.stdout)
        direct_payload.pop("generated_at", None)
        wrapper_payload.pop("generated_at", None)
        self.assertEqual(direct_payload, wrapper_payload)

    def test_bin_agents_ps1_wrapper_rejects_unknown_subcommand(self) -> None:
        interpreter = self._powershell_interpreter()
        if interpreter is None:
            self.skipTest("no PowerShell interpreter (pwsh/powershell) available")
        wrapper = REPOSITORY_ROOT / "bin" / "cadre.ps1"
        result = subprocess.run(
            [interpreter, "-File", str(wrapper), "not-a-real-subcommand"],
            cwd=REPOSITORY_ROOT,
            check=False,
            capture_output=True,
            text=True,
            encoding="utf-8",
        )
        self.assertNotEqual(0, result.returncode)
        self.assertIn("unknown subcommand", result.stderr)

    # -- Stale doc facts: a recurring failure shape ------------------------
    #
    # Three independent review findings (total role count, secure-cloud role
    # count, a hardcoded kernel release tag) turned out to be the same shape:
    # prose asserting a fact that only the generator/source files actually
    # own, with nothing to catch the two drifting apart. These three tests
    # guard that shape going forward. They deliberately do not hardcode 74 or
    # 19 -- see `_catalog_role_count()`/`_secure_cloud_role_count()` above --
    # because a test that pins its own expected number is exactly the kind of
    # second copy that caused the drift in the first place.
    #
    # `plugin/` is excluded from all three scans below: it is `roster/`'s own
    # generated output (`cadre generate-plugin --output plugin`, verified
    # byte-for-byte by `.github/workflows/validate.yml`'s `generated-content`
    # job), so anything stale there is a faithful mirror of a source-tree
    # finding this same scan already catches at its source location. Scanning
    # it too would just report every source finding twice under a different
    # path, with no independent signal -- and it would fail this task's own
    # "do not touch plugin/" boundary the moment someone tried to fix what it
    # reported.

    # Rather than enumerate the sentence shapes docs currently use to state
    # a role count -- which only catches phrasings someone already thought
    # of, and lets "Cadre ships 71 roles" walk straight past -- invert it:
    # find *every* "N roles" in live prose and require N to be a number this
    # repository can actually justify. That is the live catalog total, the
    # live secure-cloud profile count, or an explicitly allowed local count
    # below. A new phrasing is caught automatically; a new *number* has to be
    # justified here, which is the review this check exists to force.
    # Keyed by (path prefix, count), not by count alone: a bare-value
    # allowlist would permit this number in *any* document, so a genuinely
    # wrong claim elsewhere would pass for the wrong reason.
    ALLOWED_LOCAL_ROLE_COUNTS = {
        (".agents/skills/lifecycle-onboarding/SKILL.md", 5): (
            "the conditional authority roles -- kernel-owned, and exactly "
            "the 5 keys in agentic_sdlc.CONDITIONAL_AUTHORITY_ROLES "
            "(data_control_owner, human_key_owner, uat_product_owner, "
            "implicated_security_lead, implicated_governance_lead), not a "
            "claim about the role catalog"
        ),
        ("cline-plugins/cline-agents/README.md", 4): (
            "roles needing bespoke path-rewrite handling beyond the generic "
            "lookup table -- a property of that script, not a claim about "
            "the catalog"
        ),
    }

    # Point-in-time records legitimately freeze a count that was true when
    # written and is not expected to track the live catalog forever.
    ROLE_COUNT_HISTORICAL_PREFIXES = (
        # Chronological record; earlier entries correctly cite the count at
        # that release.
        "CHANGELOG.md",
        # Point-in-time decision records, never revised once decided --
        # role-expansion-2026-08.md describes a completed 71 -> 74 migration.
        "docs/proposals/",
        # Archived task records, same rationale as the sample-archive
        # allowance in test_sample_references_are_limited_to_allowed_archives.
        "roster/orchestration/runs/",
        "roster/orchestration/examples/",
    )

    # docs/capability-index.md states a dozen per-tier subset counts, each on
    # its own heading. Those are skipped -- but by *shape*, not by heading
    # level: a bare "### " prefix check would let any heading anywhere hide a
    # real total ("### This suite ships 999 roles"), which is an escape hatch,
    # not an exemption. This matches only the breakdown form actually used: a
    # heading whose body is a single backtick-quoted lowercase tier/phase
    # token followed by a parenthesised count.
    ROLE_COUNT_BREAKDOWN_HEADING = re.compile(r"^#+\s+`[a-z][a-z_]*`\s+\(\d+ roles?\)\s*$")

    def test_role_count_claims_in_tracked_docs_are_justified(self) -> None:
        catalog_total = _catalog_role_count()
        secure_cloud_total = _secure_cloud_role_count()
        derived = {catalog_total, secure_cloud_total}
        # `[\d,]` so a thousands separator is read as one number: matching
        # bare `\d+` re-anchors after the comma, so "2,074 roles" would be
        # read as a correct claim of 74. Up to four intervening words, so
        # "74 highly specialised expert roles" is caught, not just
        # "74 specialist roles". Singular "role" counts only in "role
        # definition(s)" -- otherwise "Step 4 role table" reads as a count of
        # four roles, which it is not. "of" is excluded from the intervening
        # words so partitives ("2 of the four roles") are not read as a claim
        # that there are two roles.
        pattern = re.compile(
            r"\b(\d[\d,]*)(?:\s+(?!of\b)[A-Za-z][\w-]*){0,4}\s+(?:roles\b|role definitions?\b)"
        )
        # Markdown emphasis and code spans sit between the number and the
        # noun ("**74** roles", "`74` roles") and would otherwise break the
        # match entirely -- the claim goes unseen rather than misread.
        # Replaced with spaces, not stripped, so offsets and therefore line
        # numbers stay accurate.
        markup = str.maketrans({"*": " ", "`": " ", "_": " "})
        offenders: list[str] = []
        scanned_claims = 0
        for relative_path in _tracked_files():
            normalized = relative_path.replace("\\", "/")
            if not normalized.endswith(".md") or normalized.startswith("plugin/"):
                continue
            if normalized.startswith(self.ROLE_COUNT_HISTORICAL_PREFIXES):
                continue
            path = REPOSITORY_ROOT / normalized
            try:
                text = path.read_text(encoding="utf-8")
            except UnicodeDecodeError:
                continue
            lines = text.splitlines()
            scannable = text.translate(markup)
            # Scanned over the whole text, not line by line: this repository
            # wraps prose, so a claim routinely straddles a newline ("...71\n
            # specialist roles..."). A per-line scan silently misses those.
            for match in pattern.finditer(scannable):
                line_number = text.count("\n", 0, match.start()) + 1
                if self.ROLE_COUNT_BREAKDOWN_HEADING.match(lines[line_number - 1]):
                    continue
                claimed = int(match.group(1).replace(",", ""))
                scanned_claims += 1
                if claimed in derived:
                    continue
                if (normalized, claimed) in self.ALLOWED_LOCAL_ROLE_COUNTS:
                    continue
                offenders.append(
                    f"{normalized}:{line_number}: claims {match.group(0)!r}, which is "
                    f"neither the catalog total ({catalog_total}, roster/catalog.yaml), "
                    f"the secure-cloud profile count ({secure_cloud_total}, "
                    f"provider/profiles/secure-cloud/profile.json), nor an "
                    f"ALLOWED_LOCAL_ROLE_COUNTS entry for this file"
                )
        self.assertEqual([], offenders)
        # A scan matching nothing cannot fail. The live docs do state role
        # counts; if this ever finds none, the pattern has rotted. Counted
        # during the pass above rather than re-walking every file.
        self.assertGreater(scanned_claims, 0, "no role-count claim found to check")

    # A literal `kernel-v<major>.<minor>.<patch>` (or `plugin-v<...>`) frozen
    # into prose goes stale the moment the next tag ships, unless the prose
    # is itself a historical record of a specific past release/incident (a
    # changelog entry, a postmortem naming the exact affected tag, a
    # migration writeup, a proposal's after-the-fact release note, or the
    # release workflow's own inline comments narrating a past incident --
    # its actual tag creation is templated as `kernel-v${{ ... }}`, never a
    # literal digit string). Every current allowlist entry was checked
    # against the real tree before being added here, not assumed.
    RELEASE_TAG_ALLOWLIST = {
        "CHANGELOG.md": (
            "the release changelog is a chronological record of exactly what "
            "shipped at each tag; it must keep citing old tags verbatim"
        ),
        "SECURITY.md": (
            "documents a specific past incident (keyless tag signing) tied to "
            "the exact historical plugin tag that carries it; the postmortem "
            "is only useful if that tag stays named"
        ),
        "docs/migration/monorepo-migration.md": (
            "restates the same tag-signing incident plus a completed "
            "migration's release history -- both are historical facts about "
            "specific past tags, not live guidance"
        ),
        "docs/proposals/": (
            "proposal documents are point-in-time decision records that cite "
            "which tag a change actually shipped as, and are never revised "
            "once the decision is made"
        ),
        ".github/workflows/release.yml": (
            "the only literal digits here are inside comments narrating a "
            "specific past signing incident for engineer context; the "
            "workflow's actual tag creation is templated "
            "(`kernel-v${{ steps.version.outputs.value }}`), never a literal "
            "version string"
        ),
    }

    def test_hardcoded_release_tags_are_confined_to_historical_records(self) -> None:
        # Case-insensitive: a sentence-initial, capitalised form of the tag
        # is ordinary prose and would otherwise walk straight past this
        # check. (Deliberately not spelled out here -- this file is scanned
        # too, and a literal example would trip its own guard.)
        pattern = re.compile(r"(?:kernel|plugin)-v\d+\.\d+\.\d+", re.IGNORECASE)
        allowed_prefixes = tuple(self.RELEASE_TAG_ALLOWLIST)
        offenders: list[str] = []
        for relative_path in _tracked_files():
            normalized = relative_path.replace("\\", "/")
            if normalized.startswith("plugin/") or normalized.startswith(allowed_prefixes):
                continue
            path = REPOSITORY_ROOT / normalized
            if not path.is_file():
                continue
            try:
                text = path.read_text(encoding="utf-8")
            except UnicodeDecodeError:
                continue
            for match in pattern.finditer(text):
                line_number = text.count("\n", 0, match.start()) + 1
                offenders.append(f"{normalized}:{line_number}: hardcoded release tag '{match.group(0)}'")
        self.assertEqual([], offenders)


if __name__ == "__main__":
    unittest.main()
