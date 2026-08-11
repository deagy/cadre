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
import re
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
        self.assertNotEqual(
            renamed,
            self.body,
            "this fixture's rename was a no-op, which means the heading it targets no "
            "longer exists upstream in workspace-isolation.md -- check for a rename "
            "there before assuming the fixture is stale",
        )
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

        Pinned rather than bounded: a fraction-of-the-file bound left 54
        lines of headroom, enough for a 22-line leak of write-capable prose
        to pass comfortably. A pin forces whoever changes the header to
        change this number on purpose.
        """
        preamble, sections = generator.split_policy_sections(self.body)
        self.assertLessEqual(
            len(preamble.splitlines()),
            40,
            "update this pin deliberately when the applicability header changes",
        )
        # Membership *and* order, against the registered tuple: the excerpt
        # keeps file order, so a reordering that separates the universal
        # sections from the top of the file is a real change to what a
        # read-only reader sees first.
        required = generator.UNIVERSAL_POLICY_SECTIONS[POLICY_RELATIVE]
        self.assertEqual(
            list(required),
            [heading for heading, _ in sections if heading in set(required)],
        )

    def test_no_code_fence_swallows_a_section_boundary(self) -> None:
        """A *balanced* stray fence pair needs no parser bug to leak policy:
        it deletes the boundaries between its markers, and a swallowed
        heading is absorbed into the preceding section. Silent when that
        section is one of the kept four -- write-capable text then ships to
        every read-only wrapper -- and caught by the missing-heading check
        only when it swallows a dropped section instead.
        """
        _, sections = generator.split_policy_sections(self.body)
        raw = [line for line in self.body.splitlines() if line.startswith("## ")]
        self.assertEqual(
            len(raw),
            len(sections),
            "a code fence is swallowing a '## ' section boundary; if a fenced "
            "heading is genuinely needed, add an explicit allow-list rather than "
            "relaxing this check",
        )

    def test_excerpt_raises_when_a_fence_swallows_a_section_boundary(self) -> None:
        """The same condition, at the generator boundary rather than here --
        a silent leak into a shipped policy wrapper should fail the build,
        not only this suite.
        """
        mutated = self.body.replace(
            "## No runner names as behavioral conditions",
            "## No runner names as behavioral conditions\n\n```",
            1,
        ).replace(
            "## Isolating your own edits (write-capable tiers)",
            "## Isolating your own edits (write-capable tiers)\n\n```",
            1,
        )
        self.assertNotEqual(mutated, self.body, "fixture did not insert the fences")
        with self.assertRaises(generator.PolicyExcerptError) as raised:
            generator.excerpt_universal_sections(POLICY_RELATIVE, mutated)
        self.assertIn("swallowing a section boundary", str(raised.exception))

    def test_every_self_declared_universal_section_is_registered(self) -> None:
        """The reverse of the drift guard, reached by *addition* rather than
        rename: write a new section whose body says it binds every tier,
        forget `UNIVERSAL_POLICY_SECTIONS`, and it is silently absent from
        every read-only wrapper. That is the original #211 bug again, and
        adding a section is exactly what a future editor of this file does.

        One-directional on purpose: a section carrying the marker phrase
        must be registered, but a registered section need not carry it --
        three of today's four do not, and forcing the phrase into them would
        be ceremony, not protection.
        """
        marker = re.compile(r"every role(,| and) every (capability )?tier", re.I)
        required = set(generator.UNIVERSAL_POLICY_SECTIONS[POLICY_RELATIVE])
        _, sections = generator.split_policy_sections(self.body)
        offenders = [
            heading for heading, text in sections if marker.search(text) and heading not in required
        ]
        self.assertEqual(
            [],
            offenders,
            "section(s) declare in their own body that they bind every role at every "
            "tier, but are not registered in UNIVERSAL_POLICY_SECTIONS, so read-only "
            "wrappers do not carry them. Register them (and name them in the file's "
            "applicability header), or reword the claim.",
        )

    def test_excerpt_raises_on_an_unbalanced_backtick_fence(self) -> None:
        with self.assertRaises(generator.PolicyExcerptError) as raised:
            generator.split_policy_sections("preamble\n\n```sh\nunclosed\n")
        self.assertIn("unbalanced", str(raised.exception))

    def test_excerpt_raises_on_an_unbalanced_tilde_fence(self) -> None:
        with self.assertRaises(generator.PolicyExcerptError) as raised:
            generator.split_policy_sections("preamble\n\n~~~sh\nunclosed\n")
        self.assertIn("unbalanced", str(raised.exception))

    def test_excerpt_raises_when_the_header_does_not_enumerate_a_section(self) -> None:
        stripped = self.body.replace(
            "- Never remove or prune a worktree yourself\n", "", 1
        )
        self.assertNotEqual(stripped, self.body, "fixture did not remove the header bullet")
        with self.assertRaises(generator.PolicyExcerptError) as raised:
            generator.excerpt_universal_sections(POLICY_RELATIVE, stripped)
        self.assertIn("disagree about which sections bind every tier", str(raised.exception))

    def test_excerpt_raises_when_the_header_promises_an_unregistered_section(self) -> None:
        """The symmetric direction: the header must not claim a section binds
        every tier while the dict drops it from every read-only wrapper.
        """
        promised = self.body.replace(
            "- No runner names as behavioral conditions\n",
            "- No runner names as behavioral conditions\n- Escalating\n",
            1,
        )
        self.assertNotEqual(promised, self.body, "fixture did not add the header bullet")
        with self.assertRaises(generator.PolicyExcerptError) as raised:
            generator.excerpt_universal_sections(POLICY_RELATIVE, promised)
        self.assertIn("Escalating", str(raised.exception))

    def test_excerpt_raises_on_an_empty_universal_section_body(self) -> None:
        gutted = self.body.replace(
            "## No runner names as behavioral conditions\n\nEvery decision in this file",
            "## No runner names as behavioral conditions\n\n## Every decision in this file",
            1,
        )
        self.assertNotEqual(gutted, self.body, "fixture did not gut the section")
        with self.assertRaises(generator.PolicyExcerptError) as raised:
            generator.excerpt_universal_sections(POLICY_RELATIVE, gutted)
        self.assertIn("empty body", str(raised.exception))


if __name__ == "__main__":
    unittest.main()
