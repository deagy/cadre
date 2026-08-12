#!/usr/bin/env python3
"""Tests for tools/port_cline_agents.py.

`port_agents()`/`port_skills()` are what regenerate.yml now runs to keep
`cline-agents/agents/*.md` and `cline-agents/skills/*.md` in sync with the
register-generated `agents/*.md`/`skills/*/SKILL.md` -- previously a static,
one-time hand port. The fail-loud safety net (any unrecognized
`roster/`-relative or `../`-relative reference stops the script rather than
shipping a leaked path) is the single most important property here: these
tests verify it actually trips, not just that the happy path works.

    python3 -m unittest discover -s tools -p "test_*.py"
"""

from __future__ import annotations

import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent))

import port_cline_agents as p  # noqa: E402

REPO_ROOT = Path(__file__).resolve().parent.parent

# The port *target* is no longer a sibling of this tool. The three Cline
# workspaces moved to a top-level cline-plugins/ because an npm workspace root
# inside plugin/ made installing the Claude Code plugin -- whose marketplace
# source is ./plugin -- fetch 263 MB of node_modules. Named explicitly rather
# than derived by counting `.parent`s, so the next move fails loudly here
# instead of silently resolving somewhere plausible.
CLINE_ROOT = REPO_ROOT.parent / "cline-plugins"

# `agents/` and `skills/` stopped being committed at the monorepo merge --
# they are generated into a build directory now. Build them on demand.
sys.path.insert(0, str(Path(__file__).resolve().parent))
from generated_package import generated_package  # noqa: E402


class ToolAndModelMappingTests(unittest.TestCase):
    def test_tools_map_to_canonical_cline_names_deduped_in_order(self) -> None:
        source_dir_content = (
            "---\n"
            "name: sample-role\n"
            "description: A sample role.\n"
            "tools: Read, Grep, Glob, Bash, Edit, Write\n"
            "model: sonnet\n"
            "effort: medium\n"
            "generated: true\n"
            "canonical_source: roster/engineering/sample-role/AGENT.md\n"
            "---\n"
            "\n"
            "# Role: sample-role\n"
            "\n"
            "# Sample Role\n"
            "\n"
            "## Role\n"
            "\n"
            "Do a sample thing.\n"
        )
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "agents").mkdir()
            (root / "cline-agents" / "agents").mkdir(parents=True)
            (root / "agents" / "sample-role.md").write_text(source_dir_content, encoding="utf-8")

            p.port_agents(root)
            content = (root / "cline-agents" / "agents" / "sample-role.md").read_text(encoding="utf-8")

        self.assertIn("allowedTools: [read_files, search_codebase, run_commands, editor]", content)
        # Presets carry the capability tier only. A provider or a
        # vendor-qualified model id here would reintroduce issue #142: it
        # selects a vendor on the operator's behalf, and where that vendor's
        # credentials happen to exist, silently routes task and
        # knowledge-store content to it.
        # ...translated to the capability-neutral vocabulary. The catalog's
        # `sonnet` is a Claude model line, which names nothing on a local or
        # open-weight rig -- the axis survives the move, the branding does not.
        self.assertIn("modelTier: mid", content)
        self.assertNotIn("modelTier: sonnet", content)
        self.assertNotIn("providerId", content)
        self.assertNotIn("anthropic", content)
        self.assertIn("convertedFrom: agents/sample-role.md", content)
        self.assertNotIn("effort:", content)
        self.assertNotIn("generated:", content)
        self.assertNotIn("# Role: sample-role", content)

    def test_read_only_tools_map_without_write_or_exec(self) -> None:
        result = [p.TOOL_MAP[t] for t in ["Read", "Grep", "Glob"]]
        deduped = list(dict.fromkeys(result))
        self.assertEqual(deduped, ["read_files", "search_codebase"])

    def test_tiers_are_capability_labels_not_vendor_model_ids(self) -> None:
        # Keys are the catalog's vocabulary (what a Claude Code preset carries);
        # values are what a Cline preset carries.
        self.assertEqual({"opus": "high", "sonnet": "mid", "haiku": "low"}, p.MODEL_TIERS)
        for tier in p.MODEL_TIERS.values():
            self.assertNotIn("/", tier, "a tier is a capability label, not a vendor-qualified id")

    def test_emitted_tier_vocabulary_names_no_vendor_model_line(self) -> None:
        """The point of the rename. Cline is driven mostly against open-weight
        and local models, where `opus`/`sonnet`/`haiku` name models the
        operator does not have -- and, via CLINE_AGENTS_MODEL_<TIER>, ask them
        to write `CLINE_AGENTS_MODEL_OPUS=<some local model>`. Nothing on the
        Cline surface may reintroduce that branding.
        """
        for tier in p.MODEL_TIERS.values():
            self.assertNotIn(
                tier,
                {"opus", "sonnet", "haiku", "gpt", "claude"},
                f"emitted tier {tier!r} names a vendor's model line",
            )

    def test_unknown_tier_is_rejected_rather_than_defaulted(self) -> None:
        source = (
            "---\nname: bad-role\ndescription: d\nmodel: gpt-9\n"
            "tools: Read\ncanonical_source: roster/x/bad-role/AGENT.md\n---\n\nBody.\n"
        )
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "agents").mkdir(parents=True)
            (root / "cline-agents" / "agents").mkdir(parents=True)
            (root / "agents" / "bad-role.md").write_text(source, encoding="utf-8")
            with self.assertRaises(SystemExit):
                p.port_agents(root)


class PathSubstitutionTests(unittest.TestCase):
    def _body(self, role: str, extra: str) -> str:
        return f"\n# Role: {role}\n\n# Title\n\n## Role\n\n{extra}\n"

    def test_shared_policy_backtick_reference_is_rewritten(self) -> None:
        body = self._body("some-role", "Follow `../../shared/team-profile.yaml`.")
        converted = p._convert_agent_body("some-role", body)
        self.assertIn("this project's team-profile documentation", converted)
        self.assertNotIn("../../shared/team-profile.yaml", converted)

    def test_shared_policy_header_prefix_is_stripped_but_filename_kept(self) -> None:
        body = self._body("some-role", "# Shared policy: roster/shared/technology-standards.md")
        converted = p._convert_agent_body("some-role", body)
        self.assertIn("# Shared policy: technology-standards.md", converted)

    def test_routing_yaml_boilerplate_across_a_line_break_is_rewritten(self) -> None:
        body = self._body(
            "some-role",
            "noted that roster/orchestration/\n    routing.json's own routing rules matter.",
        )
        converted = p._convert_agent_body("some-role", body)
        self.assertIn("this project's routing configuration's own routing rules", converted)


class FailLoudSafetyNetTests(unittest.TestCase):
    def test_unrecognized_roster_relative_reference_raises(self) -> None:
        body = "\n# Role: some-role\n\n# Title\n\n## Role\n\nSee `roster/some/brand-new/file.md` for details.\n"
        converted = p._convert_agent_body("some-role", body)
        with self.assertRaises(SystemExit):
            p._check_no_leaks("some-role", converted)

    def test_unrecognized_dotdot_reference_raises(self) -> None:
        converted = "See `../../some/new/path.md` for details."
        with self.assertRaises(SystemExit):
            p._check_no_leaks("some-role", converted)

    def test_known_substitutions_leave_no_leak(self) -> None:
        body = "\n# Role: some-role\n\n# Title\n\n## Role\n\nFollow `../../shared/team-profile.yaml`.\n"
        converted = p._convert_agent_body("some-role", body)
        p._check_no_leaks("some-role", converted)  # must not raise

    def test_application_engineer_is_exempt_from_the_leak_check(self) -> None:
        p._check_no_leaks("application-engineer", "roster/catalog.yaml is this role's whole subject.")


class HandCasedExceptionTests(unittest.TestCase):
    def test_application_engineer_gets_the_port_note_appended(self) -> None:
        body = "\n# Role: application-engineer\n\n# Title\n\n## Role\n\nOwn this suite's tooling.\n"
        converted = p._convert_agent_body("application-engineer", body)
        self.assertIn("Port note (not part of the original role authority text)", converted)

    def test_debugging_engineer_agent_md_bullet_is_reworded(self) -> None:
        body = (
            "\n# Role: debugging-engineer\n\n# Title\n\n## Role\n\n"
            "- When inspecting agents, verify `AGENT.md` authority, catalog registration, "
            "routing rules, knowledge focus, workflow alignment, selector tests, and runbook "
            "examples.\n"
        )
        converted = p._convert_agent_body("debugging-engineer", body)
        self.assertIn("the agent definition's authority, catalog/registry registration", converted)
        self.assertNotIn("verify `AGENT.md` authority", converted)

    def test_knowledge_store_steward_security_clause_gets_the_added_explanation(self) -> None:
        body = (
            "\n# Role: knowledge-store-steward\n\n# Title\n\n## Role\n\n"
            "resolves to the shared global store by default (`SECURITY.md`), so also verify.\n"
        )
        converted = p._convert_agent_body("knowledge-store-steward", body)
        self.assertIn("exact default-resolution behavior", converted)

    def test_missing_override_text_fails_loudly_rather_than_silently_skipping(self) -> None:
        body = "\n# Role: debugging-engineer\n\n# Title\n\n## Role\n\nNo AGENT.md bullet here at all.\n"
        with self.assertRaises(SystemExit):
            p._convert_agent_body("debugging-engineer", body)


class RealRepoRegressionTests(unittest.TestCase):
    """Runs the actual converter against this checkout's real agents/skills
    and diffs the result against the committed cline-agents/ content -- the
    thing that actually proves the table is complete and correct, not just
    plausible against synthetic fixtures above.
    """

    def _assert_mirror_matches_committed(self, kind: str, ported: list[str], generated_root: Path) -> None:
        """Compare a regenerated mirror against the committed one as a *set*,
        not just where the two happen to overlap.

        The earlier version of this check iterated the regenerated files and
        skipped any whose committed counterpart was missing. That made two
        real drifts invisible: deleting a committed file entirely, and leaving
        an orphan behind that no source produces. Both were verified to pass
        silently before this was tightened.
        """
        committed_dir = CLINE_ROOT / "cline-agents" / kind
        committed_names = {path.stem for path in sorted(committed_dir.glob("*.md"))}
        regenerated_names = set(ported)

        missing = sorted(regenerated_names - committed_names)
        orphaned = sorted(committed_names - regenerated_names)
        self.assertEqual(
            missing, [], f"{kind}: produced by the converter but absent from the committed mirror: {missing}"
        )
        self.assertEqual(
            orphaned, [], f"{kind}: committed but produced by no source; delete or regenerate: {orphaned}"
        )

        mismatches = []
        for name in sorted(regenerated_names):
            generated = (generated_root / f"{name}.md").read_text(encoding="utf-8")
            committed = (committed_dir / f"{name}.md").read_text(encoding="utf-8")
            if generated != committed:
                mismatches.append(name)
        self.assertEqual(mismatches, [], f"{kind}: diverged from committed content: {mismatches}")

    def test_agents_reproduce_committed_content_exactly(self) -> None:
        # The committed cline-agents/agents/*.md this test compares against
        # already reflects this converter's own output (including the 3
        # gitlab_* autonomy keys it deliberately keeps, unlike the old hand
        # port -- see the commit message / README), so this is a genuine
        # byte-for-byte equality check, not a fuzzy one: any divergence here
        # means either the table regressed or committed content drifted
        # without re-running the converter.
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "cline-agents" / "agents").mkdir(parents=True)
            import shutil

            shutil.copytree(generated_package() / "agents", root / "agents")

            ported = p.port_agents(root)
            self.assertEqual(len(ported), 159)
            self._assert_mirror_matches_committed("agents", ported, root / "cline-agents" / "agents")

    def test_skills_reproduce_committed_content_exactly(self) -> None:
        # The agents mirror had this and the skills mirror did not (issue
        # #126): a hand-edit or stale regeneration in a skill body that did
        # not happen to trip the leak patterns below went undetected, while
        # the equivalent in an agent preset failed CI.
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "cline-agents" / "skills").mkdir(parents=True)
            import shutil

            shutil.copytree(generated_package() / "skills", root / "skills")

            ported = p.port_skills(root)
            self.assertEqual(len(ported), 8)
            self._assert_mirror_matches_committed("skills", ported, root / "cline-agents" / "skills")

    def test_skills_have_no_remaining_roster_relative_leakage(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "cline-agents" / "skills").mkdir(parents=True)
            import shutil

            shutil.copytree(generated_package() / "skills", root / "skills")

            ported = p.port_skills(root)
            self.assertEqual(len(ported), 8)

            for name in ported:
                content = (root / "cline-agents" / "skills" / f"{name}.md").read_text(encoding="utf-8")
                for match in p.LEAK_RE.finditer(content):
                    self.assertTrue(
                        any(
                            allowed in match.group(0) or match.group(0) in allowed
                            for allowed in p.SKILL_LEAK_ALLOWLIST
                        ),
                        f"{name}: unexpected leaked reference {match.group(0)!r}",
                    )

    def test_skills_no_longer_reference_the_dead_suite_fallback_path(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "cline-agents" / "skills").mkdir(parents=True)
            import shutil

            shutil.copytree(generated_package() / "skills", root / "skills")
            p.port_skills(root)

            content = (root / "cline-agents" / "skills" / "run-agent-orchestration.md").read_text(encoding="utf-8")
            self.assertNotIn("../../suite/roster/", content)
            self.assertIn("Cline packaging note", content)

    def test_script_is_idempotent(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "cline-agents" / "agents").mkdir(parents=True)
            (root / "cline-agents" / "skills").mkdir(parents=True)
            import shutil

            shutil.copytree(generated_package() / "agents", root / "agents")
            shutil.copytree(generated_package() / "skills", root / "skills")

            p.port_agents(root)
            p.port_skills(root)
            first_pass = {
                f.name: f.read_text(encoding="utf-8")
                for f in (root / "cline-agents" / "agents").glob("*.md")
            }

            p.port_agents(root)
            second_pass = {
                f.name: f.read_text(encoding="utf-8")
                for f in (root / "cline-agents" / "agents").glob("*.md")
            }
            self.assertEqual(first_pass, second_pass)

    def test_cli_runs_cleanly_against_this_checkout(self) -> None:
        # --source is separate from --root since the monorepo merge: the
        # agents/ and skills/ being ported are build artifacts under
        # plugin/, while cline-agents/ (the port target) is committed under
        # cline-plugins/ -- so --root and --source genuinely differ here.
        #
        # --root points at a *copy*, never CLINE_ROOT itself. Writing into the
        # committed tree made this test silently repair drift instead of
        # letting the mirror checks report it: an edited or orphaned skill was
        # regenerated away before test_skills_reproduce_committed_content_exactly
        # ever looked, and the agents equivalent only survived because
        # unittest happens to run methods alphabetically, putting it before
        # this one. A guard whose correctness depends on method-name ordering
        # is not a guard.
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            import shutil

            shutil.copytree(CLINE_ROOT / "cline-agents", root / "cline-agents")
            result = subprocess.run(
                [
                    sys.executable,
                    str(REPO_ROOT / "tools" / "port_cline_agents.py"),
                    "--root",
                    str(root),
                    "--source",
                    str(generated_package()),
                ],
                capture_output=True,
                text=True,
            )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Ported 159 agent(s) and 8 skill(s)", result.stdout)

    def test_the_suite_leaves_the_committed_mirror_untouched(self) -> None:
        # The property the fix above restores, asserted directly rather than
        # left to be rediscovered: no test may write into the committed
        # mirror, because a suite that regenerates it cannot also detect that
        # it drifted.
        mirror = CLINE_ROOT / "cline-agents"
        before = {
            path.relative_to(mirror).as_posix(): path.read_bytes()
            for path in sorted(mirror.rglob("*.md"))
        }
        subprocess.run(
            [sys.executable, "-B", "-m", "unittest", "discover", "-s", str(REPO_ROOT / "tools"),
             "-p", "test_port_cline_agents.py", "-k", "test_cli_runs_cleanly_against_this_checkout"],
            cwd=REPO_ROOT.parent,
            capture_output=True,
            text=True,
            check=False,
        )
        after = {
            path.relative_to(mirror).as_posix(): path.read_bytes()
            for path in sorted(mirror.rglob("*.md"))
        }
        self.assertEqual(sorted(before), sorted(after), "the suite added or removed committed mirror files")
        changed = sorted(name for name in before if before[name] != after.get(name))
        self.assertEqual(changed, [], f"the suite rewrote committed mirror files: {changed}")


if __name__ == "__main__":
    unittest.main()


class PrunesStalePortsTests(unittest.TestCase):
    """The port must remove copies of skills it no longer sources.

    It only ever added, so a skill that was renamed, deleted, or (as actually
    happened with cadre-install-kernel) routed into a sub-plugin left its old
    copy behind permanently, and cline-agents kept advertising a skill nothing
    generated. The stale file then failed the bundled-skill count assertion in
    a completely different package, which is a poor way to find out.
    """

    def test_skill_no_longer_sourced_is_removed(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            source = root / "source"
            (source / "skills" / "kept").mkdir(parents=True)
            (source / "skills" / "kept" / "SKILL.md").write_text(
                "---\nname: kept\ndescription: still generated\n---\n\nBody.\n",
                encoding="utf-8",
            )
            target = root / "cline-agents" / "skills"
            target.mkdir(parents=True)
            stale = target / "gone.md"
            stale.write_text("---\nname: gone\n---\n", encoding="utf-8")

            # Stub the body converter: this test is about pruning, and the
            # real converter enforces packaging conventions the minimal
            # fixture above deliberately does not carry.
            with mock.patch.object(p, "_convert_skill_body", lambda name, body: body), \
                 mock.patch.object(p, "_check_no_skill_leaks", lambda name, body: None):
                ported = p.port_skills(root, source)

            self.assertEqual(["kept"], ported)
            self.assertFalse(stale.exists(), "a no-longer-sourced port must be removed")
            self.assertTrue((target / "kept.md").is_file())
