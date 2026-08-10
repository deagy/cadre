#!/usr/bin/env python3
"""Guards the section-granular workspace-isolation excerpt (deagy/cadre#211).

`roster/shared/workspace-isolation.md` states its own applicability rule: the
worktree-isolation steps and the end-of-task result block bind write-capable
tiers only, while "Never mutate a working tree you did not create" binds every
role at every tier. `generate_global_plugin.UNIVERSAL_POLICY_SECTIONS` encodes
exactly that, so a read-only role's wrapper carries the applicability header
plus the never-mutate section and nothing else.

The failure mode this exists to catch is silent: rename that heading in the
source file and a naive excerpter ships 28 reviewer wrappers with no
never-mutate rule, looking for all the world like a routine regeneration. An
earlier attempt at this trim failed in exactly that shape -- it excerpted at
file granularity and dropped the universally binding section along with the
rest -- so the checks below assert the *presence* of that rule as forcefully
as they assert the absence of the write-capable-only steps.

    python3 -m unittest discover -s plugin/tools -p "test_*.py"
"""

from __future__ import annotations

import json
import sys
import unittest
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
PLUGIN_ROOT = REPO_ROOT / "plugin"
sys.path.insert(0, str(REPO_ROOT / "roster" / "orchestration" / "src"))

import generate_global_plugin as generator  # noqa: E402

POLICY_RELATIVE = "roster/shared/workspace-isolation.md"
POLICY_PATH = REPO_ROOT / POLICY_RELATIVE
POLICY_MARKER = f"# Shared policy: {POLICY_RELATIVE}"

# Text that appears only in the sections the file's own header scopes to
# write-capable tiers.
#
# Four of these ARE headings, so renaming one makes its `assertNotIn` pass
# vacuously -- they are a readable index of what must not leak, not the
# protection. The protection is
# `test_read_only_wrappers_embed_exactly_the_declared_excerpt`, which pins
# the embedded text to the generator's own output character for character,
# so any section not in UNIVERSAL_POLICY_SECTIONS is excluded by
# construction whatever it is called.
WRITE_CAPABLE_ONLY_PHRASES = (
    "## Step 0 -- Already isolated?",
    "## Step 1 -- Can I isolate?",
    "## Step 2 -- Degrade explicitly",
    "## End-of-task result block (mandatory)",
    "The dirty-scope guard, explained",
    "mode: worktree | inherited-worktree | in-place",
)

# Body prose from each section that binds every tier, at every capability.
# One heading per section for readability, plus prose that a heading rename
# cannot vacuously satisfy.
#
# The three sections after the first were added by the #211 review: a
# read-only role creates inspection worktrees, so the rules about removing
# one, resolving security-relevant config from inside one, and not branching
# on runner identity bind it too.
UNIVERSAL_PHRASES = (
    "## Never mutate a working tree you did not create",
    "Never run a `git` command that discards uncommitted work or moves a branch",
    "`git stash` in any form",
    "Applies to every role and every capability tier",
    "## The security-relevant-resolver rule",
    "falls through to the machine-global shared store instead",
    "## Never remove or prune a worktree yourself",
    "Never run `git worktree remove` or `git worktree prune`",
    "## No runner names as behavioral conditions",
    "never by which coding-agent runner you are",
)


def _catalog() -> dict[str, dict[str, object]]:
    return generator.load_catalog(REPO_ROOT / "roster" / "catalog.yaml")


def _split_by_capability() -> tuple[list[str], list[str]]:
    read_only, write_capable = [], []
    for agent_id, metadata in sorted(_catalog().items()):
        if metadata["capability"] in generator.WRITE_CAPABLE_TIERS:
            write_capable.append(agent_id)
        else:
            read_only.append(agent_id)
    return read_only, write_capable


def _claude_wrapper(agent_id: str) -> str:
    return (PLUGIN_ROOT / "agents" / f"{agent_id}.md").read_text(encoding="utf-8")


def _codex_wrapper(agent_id: str) -> str:
    """The Codex wrapper's `developer_instructions`, unescaped.

    `toml_string()` is `json.dumps`, so the value round-trips through
    `json.loads` -- comparing the decoded text keeps these assertions on the
    same footing as the Claude Code ones instead of matching escaped bytes.
    """
    path = PLUGIN_ROOT / "codex-agents" / f"agents-{agent_id}.toml"
    for line in path.read_text(encoding="utf-8").splitlines():
        if line.startswith("developer_instructions = "):
            return json.loads(line[len("developer_instructions = "):])
    raise AssertionError(f"{path}: no developer_instructions key")


class WorkspaceIsolationExcerptTestCase(unittest.TestCase):
    """Assertions (a)-(c) run against the committed distribution, which is
    what users install; (d) drives the generator directly.
    """

    def setUp(self) -> None:
        self.body = POLICY_PATH.read_text(encoding="utf-8").strip()
        self.read_only, self.write_capable = _split_by_capability()
        self.assertTrue(self.read_only, "expected at least one read-only role")
        self.assertTrue(self.write_capable, "expected at least one write-capable role")

    def test_policy_is_registered_for_section_granular_excerpting(self) -> None:
        self.assertIn(POLICY_RELATIVE, generator.UNIVERSAL_POLICY_SECTIONS)
        self.assertIn(POLICY_RELATIVE, generator.SHARED_POLICIES)
        # The file must reach every tier: it is in SHARED_POLICIES (all roles),
        # never TIER_SCOPED_POLICIES (some roles), which is the coupling that
        # was tried and reverted.
        self.assertNotIn(POLICY_RELATIVE, generator.TIER_SCOPED_POLICIES)

    def test_read_only_wrappers_keep_the_never_mutate_rule(self) -> None:
        for agent_id in self.read_only:
            for runner, text in (
                ("claude", _claude_wrapper(agent_id)),
                ("codex", _codex_wrapper(agent_id)),
            ):
                with self.subTest(agent=agent_id, runner=runner):
                    self.assertIn(POLICY_MARKER, text)
                    for phrase in UNIVERSAL_PHRASES:
                        self.assertIn(phrase, text)

    def test_read_only_wrappers_omit_the_write_capable_only_steps(self) -> None:
        for agent_id in self.read_only:
            for runner, text in (
                ("claude", _claude_wrapper(agent_id)),
                ("codex", _codex_wrapper(agent_id)),
            ):
                with self.subTest(agent=agent_id, runner=runner):
                    for phrase in WRITE_CAPABLE_ONLY_PHRASES:
                        self.assertNotIn(phrase, text)

    def test_read_only_wrappers_embed_exactly_the_declared_excerpt(self) -> None:
        excerpt = generator.excerpt_universal_sections(POLICY_RELATIVE, self.body)
        for agent_id in self.read_only:
            for runner, text in (
                ("claude", _claude_wrapper(agent_id)),
                ("codex", _codex_wrapper(agent_id)),
            ):
                with self.subTest(agent=agent_id, runner=runner):
                    self.assertIn(f"{POLICY_MARKER}\n\n{excerpt}", text)

    def test_write_capable_wrappers_embed_the_whole_file(self) -> None:
        for agent_id in self.write_capable:
            for runner, text in (
                ("claude", _claude_wrapper(agent_id)),
                ("codex", _codex_wrapper(agent_id)),
            ):
                with self.subTest(agent=agent_id, runner=runner):
                    self.assertIn(f"{POLICY_MARKER}\n\n{self.body}", text)
                    for phrase in (*UNIVERSAL_PHRASES, *WRITE_CAPABLE_ONLY_PHRASES):
                        self.assertIn(phrase, text)

    def test_excerpt_raises_when_a_required_heading_is_missing(self) -> None:
        renamed = self.body.replace(
            "## Never mutate a working tree you did not create",
            "## Never mutate someone else's working tree",
        )
        self.assertNotEqual(renamed, self.body, "fixture did not rename the heading")
        with self.assertRaises(generator.PolicyExcerptError) as raised:
            generator.excerpt_universal_sections(POLICY_RELATIVE, renamed)
        self.assertIn("Never mutate a working tree you did not create", str(raised.exception))

    def test_excerpt_raises_when_the_file_has_no_preamble(self) -> None:
        headless = "## Never mutate a working tree you did not create\n\nbody\n"
        with self.assertRaises(generator.PolicyExcerptError):
            generator.excerpt_universal_sections(POLICY_RELATIVE, headless)

    def test_excerpt_rejects_an_empty_section_tuple(self) -> None:
        """The mechanism must not be usable to drop a whole file -- that is
        the file-granularity mistake it exists to make inexpressible.
        """
        relative = "roster/shared/does-not-matter.md"
        generator.UNIVERSAL_POLICY_SECTIONS[relative] = ()
        try:
            with self.assertRaises(generator.PolicyExcerptError):
                generator.excerpt_universal_sections(relative, "preamble\n\n## A\n\nb\n")
        finally:
            del generator.UNIVERSAL_POLICY_SECTIONS[relative]

    def test_headings_inside_fenced_code_blocks_are_not_sections(self) -> None:
        body = "preamble\n\n```sh\n## not a heading\n```\n\n## Real\n\nkept\n"
        preamble, sections = generator.split_policy_sections(body)
        self.assertIn("## not a heading", preamble)
        self.assertEqual(["Real"], [heading for heading, _ in sections])

    def test_preamble_does_not_absorb_the_write_capable_content(self) -> None:
        """Guards the point of the change: the preamble is kept verbatim for
        every tier, so if a future edit moves write-capable-only bulk above
        the first `## ` heading it lands in every read-only wrapper, and
        nothing else here would notice.

        This targets the preamble rather than the excerpt's total size. The
        excerpt's size is a function of how many sections legitimately bind
        every tier -- four today, after the #211 review found that scoping by
        "has edits to isolate" had de-bound three sections that bind a
        read-only role which creates an inspection worktree. A total-size
        ratio would fight that correction instead of guarding it.
        """
        preamble, _ = generator.split_policy_sections(self.body)
        self.assertLess(
            len(preamble.splitlines()),
            len(self.body.splitlines()) / 4,
            "the applicability header has grown into a substantial share of the file",
        )
        excerpt = generator.excerpt_universal_sections(POLICY_RELATIVE, self.body)
        self.assertLess(len(excerpt.splitlines()), len(self.body.splitlines()))


if __name__ == "__main__":
    unittest.main()
