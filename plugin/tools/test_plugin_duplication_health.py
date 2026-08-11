"""Fail loudly when the deliberately duplicated lifecycle-plugin content
drifts out of sync across the three plugins.

`cadre-lifecycle-core`, `-github`, and `-gitlab` are each self-sufficient:
none may depend on another being installed (see plugin/AGENTS.md's
plugin-split rationale), so three skills and the kernel bootstrap script
exist as full copies rather than shared imports. Nothing about that
arrangement makes the copies stay equal.

This test exists because they demonstrably didn't. A change correcting
"clone the archived deagy/agentic-sdlc" to "the in-tree kernel/" landed in
`lifecycle-onboarding` only; both forge copies kept telling users to clone a
repository that no longer exists. Each of the three copies carried a
"Duplication note" asserting `tools/test_plugin_duplication_health.py`
enforced their sync -- a file that had been deleted, so the claim was false
and nothing caught the divergence. This is that file, restored with teeth.

## What "in sync" means here

The copies are not byte-identical and are not supposed to be. Frontmatter
`name`/`description` differ per plugin, and forge-specific vocabulary
(GitHub/GitLab, PR/MR, gh/glab, and each forge's own write-skill name)
differs throughout the body by design. So the comparison normalizes both
forges' vocabulary onto shared placeholders and compares the result
section by section.

A handful of sections genuinely differ in substance, not just vocabulary --
GitHub's login lookup is exact-match while GitLab's can return multiple
matches, so only the GitLab copy has an ambiguous-match case to explain.
Those are listed in KNOWN_DIVERGENT_SECTIONS with a reason. Adding an entry
there is deliberate and reviewable; silently drifting a shared paragraph is
what this test stops.
"""

from __future__ import annotations

import re
import unittest
from pathlib import Path

PLUGINS_ROOT = Path(__file__).resolve().parent.parent / "plugins"

# Each triple: (core skill dir, github skill dir, gitlab skill dir). These are
# exactly the skills carrying a "Duplication note" callout in their forge
# copies; SKILL_TRIPLES_MATCH_DUPLICATION_NOTES below asserts that stays true,
# so a newly duplicated skill cannot be added without landing here too.
SKILL_TRIPLES: dict[str, tuple[str, str, str]] = {
    "brief-pending-gates": (
        "lifecycle/skills/brief-pending-gates",
        "lifecycle-github/skills/brief-pending-gates-github",
        "lifecycle-gitlab/skills/brief-pending-gates-gitlab",
    ),
    "lifecycle-onboarding": (
        "lifecycle/skills/lifecycle-onboarding",
        "lifecycle-github/skills/lifecycle-onboarding-github",
        "lifecycle-gitlab/skills/lifecycle-onboarding-gitlab",
    ),
    "lifecycle-review": (
        "lifecycle/skills/lifecycle-review",
        "lifecycle-github/skills/lifecycle-review-generic-github",
        "lifecycle-gitlab/skills/lifecycle-review-generic-gitlab",
    ),
}

# (triple id, section heading) -> why this section legitimately differs.
# Anything not listed here must normalize to identical text across all three
# copies. Keep the reason specific enough that a reviewer can tell whether it
# still holds.
KNOWN_DIVERGENT_SECTIONS: dict[tuple[str, str], str] = {
    (
        "lifecycle-onboarding",
        "## Step 4 — Authorities: resolve what's needed now, defer the rest",
    ): (
        "GitHub's login lookup is exact-match; GitLab's can return multiple "
        "matches. Only the GitLab copy has a `gitlab-user-ambiguous` case to "
        "explain, and each copy names its own forge-write skill "
        "(create-github-gate-issues vs gitlab-gate-tracking) as the consumer "
        "of that reason-code vocabulary."
    ),
    (
        "lifecycle-onboarding",
        "## Resolving a deferred authority later",
    ): (
        "The forge plugins ship two review skills -- a generic one and a "
        "PR/MR-backed one -- so their copies name both and say which to reach "
        "for; the core plugin ships only lifecycle-review, so it names one. "
        "Suffix normalization collapses both forge names onto the same token, "
        "which is why the extra clause is what diverges rather than the name."
    ),
}

# The kernel bootstrap script is a different case from the skills above: it
# has exactly one hand-maintained copy, `plugin/tools/bootstrap_sdlc.py`, which
# `generate_global_plugin.py` fans out verbatim to each lifecycle plugin at
# build time (its BOOTSTRAP_SOURCE -> BOOTSTRAP_TARGETS). So the invariant is
# not "the copies agree with each other" but "each copy still equals the source
# it was generated from" -- which also catches a hand-edit to a copy, the thing
# the fan-out exists to make unnecessary.
#
# `cadre generate-plugin --check` covers this too, by regenerating everything.
# This check is here because it is nearly free, names the specific file, and
# fails with a message pointing at the source rather than at a diff of 384
# regenerated files.
BOOTSTRAP_SOURCE = Path(__file__).resolve().parent / "bootstrap_sdlc.py"
BOOTSTRAP_GENERATED_COPIES = (
    "lifecycle/tools/bootstrap_sdlc.py",
    "lifecycle-github/tools/bootstrap_sdlc.py",
    "lifecycle-gitlab/tools/bootstrap_sdlc.py",
)

_FRONTMATTER_RE = re.compile(r"\A---\n.*?\n---\n", re.DOTALL)
_CALLOUT_RE = re.compile(r"(?m)^> (?:Duplication|Packaged suite) note:.*$")
_HEADING_RE = re.compile(r"(?m)^##\s+.*$")


def normalize(text: str) -> str:
    """Strip per-copy boilerplate and map both forges' vocabulary onto shared
    placeholders, so only substantive differences survive.

    Both forges normalize to the *same* token deliberately: a copy that says
    "GitHub" where its sibling says "GitLab" is expected, but a copy that says
    "GitHub" where its sibling says something else entirely is drift.
    """
    text = _FRONTMATTER_RE.sub("", text, count=1)
    text = _CALLOUT_RE.sub("", text)
    # Forge-specific skill names, before the bare-token rules below chew them up.
    text = text.replace("create-github-gate-issues", "FORGE_GATE_ISSUES_SKILL")
    text = text.replace("gitlab-gate-tracking", "FORGE_GATE_ISSUES_SKILL")
    # Skill-name suffixes: `lifecycle-onboarding-github` -> `lifecycle-onboarding`.
    text = re.sub(r"-generic-(?:github|gitlab)\b", "", text)
    text = re.sub(r"-(?:github|gitlab)\b", "", text)
    text = re.sub(r"\bGitHub\b|\bGitLab\b", "FORGE", text)
    text = re.sub(r"\bgithub\b|\bgitlab\b", "forge", text)
    text = re.sub(r"`gh`|`glab`", "`FORGECLI`", text)
    text = re.sub(r"\bpull request\b|\bmerge request\b", "review request", text)
    text = re.sub(r"\bPR\b|\bMR\b", "RR", text)
    return text


def split_sections(text: str) -> dict[str, str]:
    """Split normalized text into `## heading -> body`, with everything before
    the first heading under the key `<preamble>`.

    Section bodies get their whitespace collapsed here rather than in
    `normalize`, which must leave line structure intact for the heading regex
    to see. Reflowing a paragraph is not drift.
    """
    headings = list(_HEADING_RE.finditer(text))
    raw = {"<preamble>": text[: headings[0].start()] if headings else text}
    for index, match in enumerate(headings):
        end = headings[index + 1].start() if index + 1 < len(headings) else len(text)
        raw[match.group().strip()] = text[match.end() : end]
    return {heading: re.sub(r"\s+", " ", body).strip() for heading, body in raw.items()}


class SkillDuplicationTests(unittest.TestCase):
    def _read(self, relative: str) -> str:
        path = PLUGINS_ROOT / relative / "SKILL.md"
        self.assertTrue(path.is_file(), f"missing duplicated skill copy: {path}")
        return path.read_text(encoding="utf-8")

    def test_duplicated_skill_bodies_stay_in_sync(self) -> None:
        for triple_id, (core, github, gitlab) in SKILL_TRIPLES.items():
            with self.subTest(skill=triple_id):
                copies = {
                    name: split_sections(normalize(self._read(relative)))
                    for name, relative in (
                        ("core", core),
                        ("github", github),
                        ("gitlab", gitlab),
                    )
                }
                reference_name, reference = next(iter(copies.items()))
                for name, sections in list(copies.items())[1:]:
                    self.assertEqual(
                        sorted(reference),
                        sorted(sections),
                        f"{triple_id}: {reference_name} and {name} copies have "
                        f"different section headings -- a section was added, "
                        f"removed, or renamed in one copy only",
                    )
                    for heading in reference:
                        if (triple_id, heading) in KNOWN_DIVERGENT_SECTIONS:
                            continue
                        self.assertEqual(
                            reference[heading],
                            sections[heading],
                            f"{triple_id}: section {heading!r} differs between "
                            f"the {reference_name} and {name} copies after "
                            f"forge-vocabulary normalization. Propagate the "
                            f"change to every copy, or -- if the difference is "
                            f"genuinely forge-specific -- add it to "
                            f"KNOWN_DIVERGENT_SECTIONS with a reason.",
                        )

    def test_known_divergent_sections_still_exist(self) -> None:
        """An exemption for a section that no longer exists is dead weight that
        would silently keep exempting a *future* section of the same name."""
        for (triple_id, heading), reason in KNOWN_DIVERGENT_SECTIONS.items():
            with self.subTest(skill=triple_id, section=heading):
                self.assertIn(triple_id, SKILL_TRIPLES, "exemption names an unknown skill triple")
                self.assertTrue(reason.strip(), "exemption must carry a reason")
                core_sections = split_sections(normalize(self._read(SKILL_TRIPLES[triple_id][0])))
                self.assertIn(
                    heading,
                    core_sections,
                    f"KNOWN_DIVERGENT_SECTIONS exempts {heading!r} from "
                    f"{triple_id}, but no such section exists -- remove the "
                    f"stale exemption",
                )

    def test_known_divergent_sections_actually_diverge(self) -> None:
        """An exemption that is no longer needed silently disables the check for
        that section. If the copies have converged, delete the entry."""
        for (triple_id, heading) in KNOWN_DIVERGENT_SECTIONS:
            with self.subTest(skill=triple_id, section=heading):
                bodies = {
                    split_sections(normalize(self._read(relative)))[heading]
                    for relative in SKILL_TRIPLES[triple_id]
                }
                self.assertGreater(
                    len(bodies),
                    1,
                    f"{triple_id}: section {heading!r} is listed in "
                    f"KNOWN_DIVERGENT_SECTIONS but all copies now agree -- "
                    f"remove the exemption so the section is checked again",
                )

    def test_every_duplicated_skill_is_covered_by_a_triple(self) -> None:
        """The 'Duplication note' callout is the marker a skill copy is meant to
        stay in sync. A copy carrying that note but absent from SKILL_TRIPLES
        would repeat exactly the failure this file exists to prevent."""
        noted: set[str] = set()
        for path in PLUGINS_ROOT.glob("*/skills/*/SKILL.md"):
            if "Duplication note:" in path.read_text(encoding="utf-8"):
                noted.add(path.parent.relative_to(PLUGINS_ROOT).as_posix())
        covered = {relative for triple in SKILL_TRIPLES.values() for relative in triple}
        self.assertEqual(
            set(),
            noted - covered,
            "skill copies carry a Duplication note but are not listed in "
            "SKILL_TRIPLES, so nothing checks them for drift",
        )

    def test_duplication_notes_do_not_claim_unenforced_checks(self) -> None:
        """The notes previously cited this file while it did not exist. Now that
        it does, the citation is accurate -- assert it stays that way rather
        than reverting to a bare 'keep these in sync' with no mechanism."""
        for relative in (r for triple in SKILL_TRIPLES.values() for r in triple[1:]):
            with self.subTest(skill=relative):
                body = self._read(relative)
                self.assertIn(
                    "Duplication note:",
                    body,
                    "forge copy lost its Duplication note callout",
                )
                self.assertIn(
                    "test_plugin_duplication_health.py",
                    body,
                    "Duplication note should name the test that enforces it",
                )


class BootstrapDuplicationTests(unittest.TestCase):
    def test_generated_bootstrap_copies_match_their_source(self) -> None:
        """Unlike the skills, bootstrap_sdlc.py carries no forge-specific
        content and is not hand-maintained per plugin -- it is generated from a
        single source, so any difference at all is drift."""
        self.assertTrue(
            BOOTSTRAP_SOURCE.is_file(),
            f"missing bootstrap source: {BOOTSTRAP_SOURCE}",
        )
        source = BOOTSTRAP_SOURCE.read_text(encoding="utf-8")
        for relative in BOOTSTRAP_GENERATED_COPIES:
            with self.subTest(copy=relative):
                path = PLUGINS_ROOT / relative
                self.assertTrue(path.is_file(), f"missing bootstrap copy: {path}")
                self.assertEqual(
                    source,
                    path.read_text(encoding="utf-8"),
                    f"plugins/{relative} has drifted from its source "
                    f"{BOOTSTRAP_SOURCE.name}. It is generated -- edit the "
                    f"source and re-run `cadre generate-plugin --output plugin` "
                    f"rather than editing the copy.",
                )


if __name__ == "__main__":
    unittest.main()
