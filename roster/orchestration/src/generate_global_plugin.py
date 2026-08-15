#!/usr/bin/env python3
"""Regenerate the self-contained Cadre plugin.

The generated package carries full skill content, embedded role instructions,
the tracked runtime suite, and a package-relative Agentic SDLC provider catalog.
It does not depend on this source checkout after installation.

Agent-role wrappers are NOT symmetric, because the two runners differ here:
- Claude Code supports plugin-bundled subagents, auto-discovered from the
  plugin's default agents/ directory (do NOT also declare an "agents" field in
  plugin.json for this — that field expects individual file paths, not a
  directory, and a bare directory string fails manifest validation), so the
  role wrappers go under the package's agents/*.md and become
  global automatically when the plugin is installed at user scope.
- Codex CLI has no such mechanism — custom agents are only discovered from
  .codex/agents/ (project) or ~/.codex/agents/ (global) on disk, never from a
  plugin manifest. The *.toml wrappers are generated to this repository's
  tracked staging directory provider/codex-agents/ instead (by `cadre
  generate-role-metadata`), and copied into the package from there. The
  separate `cadre bootstrap-codex` command safely installs their namespaced
  IDs under ~/.codex/agents/ without overwriting bare roles or unowned files;
  this generator itself never writes outside the repository.

A generated bin/cadre wrapper is included too: Claude Code auto-discovers a
plugin's bin/ directory onto the Bash tool's PATH for the duration of a session
(convention-based, no plugin.json field required), so an orchestrating Claude
Code agent gets `cadre <subcommand>` for free once this plugin is installed,
without the human's own shell PATH being touched (that part stays manual — see
README.md "Put `cadre` on `PATH`"; no plugin can modify a user's shell profile).
Codex CLI has no equivalent bin/ auto-discovery, so this is a Claude-Code-only
convenience layered on top of the manual PATH setup, not a replacement for it.

A generated package-relative agent-catalog.json is loaded by the standalone
kernel through provider.json.

The package itself is committed in this same repository under plugin/, which
is almost entirely this script's output: only the plugin manifests carrying
the release version and a few hand-authored files are maintained there
directly (see PACKAGE_ASSETS). ``--output <directory>`` is still required
rather than defaulting, so a run can never silently create a stray directory;
in this repository it is always ``--output plugin``.

Regenerate after adding/removing a role in roster/catalog.yaml or a skill under
.agents/skills/:

    cadre generate-plugin --output plugin

Validate deterministically without changing the working tree:

    cadre generate-plugin --check --output plugin

An ``--output`` target that already has a ``.codex-plugin/plugin.json`` (i.e.
it is already an initialized, hand-authored downstream package rather than a
fresh distribution target) never has its own README.md overwritten by this
command, even though README.md is otherwise part of the generated set (see
GENERATED_TOP_LEVEL) -- a downstream repository may describe a merged or
otherwise different identity than this generator's own template (deagy/cadre#97).
Pass ``--force-readme`` to write the register's own README.md there anyway
(the actual `deagy/cadre-plugin`-style distribution target, if one exists).

(bin/cadre at the repository root; or `python3 roster/orchestration/src/generate_global_plugin.py`
directly if bin/cadre isn't set up yet).
"""

from __future__ import annotations

import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Any

sys.path.insert(0, str(Path(__file__).resolve().parent))
from role_metadata import frontmatter_closing_delimiter_end, is_migrated, strip_frontmatter  # noqa: E402
from routing import parse_catalog_entries  # noqa: E402
import agentic_sdlc_contracts  # noqa: E402  (reuses its single _resolve_executable() implementation)

REPOSITORY_ROOT = Path(__file__).resolve().parents[3]
AGENTS_ROOT = REPOSITORY_ROOT / "roster"
SKILLS_ROOT = REPOSITORY_ROOT / ".agents" / "skills"
# This repository's Agentic SDLC provider bundle. Register-owned, tracked here,
# and copied verbatim into the package by generate_provider_copy(): the
# pip/pipx distribution vendors this directory (see pyproject.toml) so `cadre
# sdlc` and `cadre bootstrap-codex` keep working from an install without a
# plugin checkout. provider.json/profiles/extensions are hand-authored;
# agent-catalog.json and codex-agents/ are generated from roster/catalog.yaml
# by `cadre generate-role-metadata`, which also drift-checks them.
PROVIDER_ROOT = REPOSITORY_ROOT / "provider"
# The packaged plugin's own README. Register-owned like the provider bundle:
# the generator renders it to both <package>/README.md and
# <package>/suite/README.md, so the package has no hand-authored input this
# script must read back out of the plugin repository -- `--output` can point
# at an empty directory and still produce a complete package.
PACKAGING_README = REPOSITORY_ROOT / "packaging" / "plugin-README.md"
# Used to rewrite packaged links whose targets exist only in the register.
REGISTER_URL = "https://github.com/deagy/cadre"
PROVIDER_BUNDLE = ("provider.json", "agent-catalog.json", "profiles", "extensions", "codex-agents")
# Register-only member of provider/: verbatim copies of every role's AGENT.md.
# The kernel resolves agent-catalog.json's `definition` values relative to the
# directory holding that file and rejects anything escaping it
# (agentic_sdlc.load_agent_catalog), so role content reachable from
# provider/agent-catalog.json has to live *under* provider/ -- a relative path
# back out to roster/ would raise. Without it, `cadre sdlc init --profile
# secure-cloud` silently falls back to one-line generic role instructions
# (rich_agent_content() returns None for a missing file), which is what the pip
# distribution has always done and what a register checkout would otherwise
# start doing after the plugin split.
#
# NOT copied into the package: the package reaches the same content through
# suite/roster/, and a package-root roles/ would be dead weight. Hence
# PROVIDER_DEFINITION_PREFIX below.
PROVIDER_ROLES_DIRNAME = "roles"
# agent-catalog.json's `definition` values are relative to whichever copy of
# the file is being read, and the two copies sit in differently shaped trees:
# provider/roles/... in the register, suite/roster/... in the package. The
# register spelling is authoritative and generate_provider_copy() rewrites it
# for the package.
PROVIDER_DEFINITION_PREFIX = f"{PROVIDER_ROLES_DIRNAME}/"
PACKAGE_DEFINITION_PREFIX = "suite/roster/"
# The only files in the plugin package this script does NOT produce. Both
# carry the package's release version, which is deliberately hand-set in the
# plugin repository (see its tools/plugin_version.py) so a release stays a
# separate, reviewed act from a content regeneration. GENERATED_TOP_LEVEL
# below is the complement: everything reset_generated_content() removes and
# files_equal() compares.
PACKAGE_ASSETS = (".claude-plugin", ".codex-plugin")
# Some generated content lives *inside* an otherwise hand-authored top-level
# directory. plugins/lifecycle/ is a hand-authored sub-plugin package (its own
# manifests, tools/ -- see the plugin repository's own split docs), but
# plugins/lifecycle/skills/ is populated from this register's .agents/skills/
# like any other packaged skill (see SKILL_PACKAGE_TARGETS below). Listed as
# full relative paths, not top-level names, so reset_generated_content() and
# files_equal() can treat exactly this subtree as generated without also
# claiming the rest of plugins/lifecycle/ -- or plugins/lifecycle-github/,
# plugins/lifecycle-gitlab/, which this generator never writes to at all -- is
# generated too.
# The lifecycle plugins that ship a kernel bootstrap and therefore need the
# compatibility window travelling with them (see KERNEL_COMPATIBILITY_TARGETS).
LIFECYCLE_PLUGIN_DIRS = (
    "plugins/lifecycle",
    "plugins/lifecycle-github",
    "plugins/lifecycle-gitlab",
)
# `bootstrap_sdlc.py` needs the supported kernel range, and an installed
# plugin has no repository around it -- provider.json is a package-root file,
# while these plugins are packaged from their own subdirectories, so it is
# simply not present inside them. Emitting a small derived file into each
# plugin's own tools/ is what makes the packaged code path reachable at all;
# without it the bootstrap dies with "no kernel compatibility data" for every
# plugin user, at any path.
KERNEL_COMPATIBILITY_TARGETS = tuple(
    f"{directory}/tools/kernel-compatibility.json" for directory in LIFECYCLE_PLUGIN_DIRS
)
# The bootstrap itself has to travel with each lifecycle plugin too. Its one
# hand-authored source lives at plugin/tools/, which belongs to the `cadre`
# plugin -- an installed cadre-lifecycle-core would otherwise carry the
# compatibility window and no script to read it. Fanned out at build time so
# there is still exactly one copy to edit, with the generated-content CI job
# catching any hand-edit to the copies. Before the merge this was three
# hand-maintained duplicates policed by a dedicated test.
BOOTSTRAP_SOURCE = REPOSITORY_ROOT / "plugin" / "tools" / "bootstrap_sdlc.py"
BOOTSTRAP_TARGETS = tuple(
    f"{directory}/tools/bootstrap_sdlc.py" for directory in LIFECYCLE_PLUGIN_DIRS
)
# Claude Code puts a plugin's bin/ on the Bash tool's PATH while the plugin is
# enabled, which is what lets the kernel be reachable without touching the
# user's shell profile -- and is why the pipx "ensurepath, restart your shell"
# dead end disappears.
LIFECYCLE_BIN_TARGETS = tuple(
    f"{directory}/bin/{name}"
    for directory in LIFECYCLE_PLUGIN_DIRS
    for name in ("agentic-sdlc", "cadre-install-kernel")
)
# Every lifecycle plugin needs the install skill and the detection hook, and
# each is packaged independently, so both are fanned out rather than shared.
LIFECYCLE_HOOK_TARGETS = tuple(
    f"{directory}/hooks/hooks.json" for directory in LIFECYCLE_PLUGIN_DIRS
)
# plugins/lifecycle/ gets this skill through SKILL_PACKAGE_TARGETS like the
# other lifecycle skills; only the two forge plugins, whose skills/ are
# hand-authored, need it fanned out.
LIFECYCLE_INSTALL_SKILL_TARGETS = tuple(
    f"{directory}/skills/cadre-install-kernel/SKILL.md"
    for directory in LIFECYCLE_PLUGIN_DIRS
    if directory != "plugins/lifecycle"
)

# deagy/cadre#129: the destructive-git `PreToolUse` guard this repository
# runs on itself (`.claude/settings.json` -> `.claude/hooks/
# guard_workspace_mutation.py`) protects only sessions running against this
# checkout. Packaging it into the main `cadre` plugin's own hooks/hooks.json
# is what lets any *consuming* project that installs the plugin get the same
# structural protection, without depending on the separately-installed,
# optional cadre-lifecycle-* plugins. This repository's own copy stays the
# single hand-authored source; the packaged copy is fanned out from it here
# so there is exactly one script to review and the generated-content CI job
# catches any hand-edit to the packaged copy, mirroring BOOTSTRAP_SOURCE
# above for the lifecycle plugins' kernel-bootstrap script.
GUARD_HOOK_SOURCE = REPOSITORY_ROOT / ".claude" / "hooks" / "guard_workspace_mutation.py"
GUARD_HOOK_TARGET = "hooks/guard_workspace_mutation.py"
# Command line mirrors this repository's own `.claude/settings.json` wiring
# exactly (`python3 "${CLAUDE_PROJECT_DIR}/.claude/hooks/
# guard_workspace_mutation.py"`), substituting `${CLAUDE_PLUGIN_ROOT}` for
# `${CLAUDE_PROJECT_DIR}` -- the plugin-relative equivalent Claude Code
# exposes to a plugin's own hook commands. See the hook module's own
# docstring for the fail-open design stance and exactly what it does and
# does not block; that reasoning applies unchanged once packaged, since the
# script itself makes no assumption specific to this repository (no
# hardcoded paths, no assumption about a checkout named "cadre" -- it reads
# `cwd`/`tool_input` from the hook's own stdin JSON and shells out to `git`
# generically).
MAIN_PLUGIN_HOOKS = {
    "hooks": {
        "PreToolUse": [
            {
                "matcher": "Bash",
                "hooks": [
                    {
                        "type": "command",
                        "command": f'python3 "${{CLAUDE_PLUGIN_ROOT}}/{GUARD_HOOK_TARGET}"',
                    }
                ],
            }
        ]
    }
}

# Detect and report only. This runs before the human has asked for anything,
# on every session start, so it must not install, must not write, and must
# not fail -- `--check` is built to those constraints and is silent when a
# compatible kernel is already resolvable. Installing here instead would mean
# a plugin fetching and executing code from the network unprompted, which is
# a supply-chain objection rather than a convenience.
LIFECYCLE_HOOKS = {
    "hooks": {
        "SessionStart": [
            {
                "hooks": [
                    {
                        "type": "command",
                        "command": '"${CLAUDE_PLUGIN_ROOT}/bin/cadre-install-kernel" --check',
                        # A hook that stalls degrades every session start for
                        # a problem that is at worst "one optional feature is
                        # not set up yet".
                        "timeout": 15,
                    }
                ]
            }
        ]
    }
}
GENERATED_NESTED_PATHS = (
    ("plugins/lifecycle/skills",)
    + KERNEL_COMPATIBILITY_TARGETS
    + BOOTSTRAP_TARGETS
    + LIFECYCLE_BIN_TARGETS
    + LIFECYCLE_HOOK_TARGETS
    + LIFECYCLE_INSTALL_SKILL_TARGETS
)

# Resolution order matches bootstrap_sdlc.py's: an explicitly named binary,
# then the plugin-managed copy, then whatever the operator installed.
#
# The PATH search deliberately removes this script's own directory first.
# This shim *is* `agentic-sdlc` on the PATH while the plugin is enabled, so a
# plain `command -v agentic-sdlc` finds itself and re-execs forever.
AGENTIC_SDLC_SHIM = """\
#!/bin/sh
set -eu
BIN_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

if [ -n "${AGENTIC_SDLC_BIN:-}" ]; then
  exec "$AGENTIC_SDLC_BIN" "$@"
fi

if [ -n "${CLAUDE_PLUGIN_DATA:-}" ]; then
  for subdir in bin Scripts; do
    candidate="$CLAUDE_PLUGIN_DATA/kernel/$subdir/agentic-sdlc"
    if [ -x "$candidate" ]; then
      exec "$candidate" "$@"
    fi
  done
fi

# Strip this shim's own directory from PATH before looking, or `command -v`
# resolves back to this file and loops.
CLEANED_PATH=$(printf '%s' "$PATH" | tr ':' '\\n' | grep -vxF "$BIN_DIR" | paste -sd: -)
FOUND=$(PATH="$CLEANED_PATH" command -v agentic-sdlc 2>/dev/null || true)
if [ -n "$FOUND" ]; then
  exec "$FOUND" "$@"
fi

echo "agentic-sdlc: no lifecycle kernel available. Run cadre-install-kernel (or the /cadre-install-kernel skill) to set one up." >&2
exit 1
"""

# The install entry point a human invokes. Never called from the hook -- see
# bootstrap_sdlc.py's --check mode for the detect-and-report half.
INSTALL_KERNEL_SHIM = """\
#!/bin/sh
set -eu
BIN_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
PLUGIN_ROOT=$(CDPATH= cd -- "$BIN_DIR/.." && pwd)

AGENT_PYTHON=
for candidate in python3 python; do
  command -v "$candidate" >/dev/null 2>&1 || continue
  if "$candidate" -c 'import sys; raise SystemExit(0 if sys.version_info >= (3, 10) else 1)' 2>/dev/null; then
    AGENT_PYTHON="$candidate"
    break
  fi
done
[ -n "$AGENT_PYTHON" ] || { echo "cadre-install-kernel: Python 3.10+ is required" >&2; exit 1; }

exec "$AGENT_PYTHON" "$PLUGIN_ROOT/tools/bootstrap_sdlc.py" "$@"
"""
# Skills whose packaged home is a sub-plugin directory (see
# GENERATED_NESTED_PATHS), not the package root skills/. Anything not listed
# here keeps generating to skills/<name>/ as always -- the core role-
# selection plugin's own skills.
SKILL_PACKAGE_TARGETS = {
    "lifecycle-onboarding": "plugins/lifecycle/skills",
    "lifecycle-review": "plugins/lifecycle/skills",
    "brief-pending-gates": "plugins/lifecycle/skills",
    "cadre-install-kernel": "plugins/lifecycle/skills",
}
SHARED_POLICIES = [
    "roster/shared/operating-principles.md",
    "roster/shared/team-profile.yaml",
    "roster/shared/technology-standards.md",
    "roster/shared/library-standards.yaml",
    "roster/shared/knowledge-use-policy.md",
    # Beside knowledge-use-policy.md, and for the same reason: any role may be
    # handed a context handle in a handoff, so the rules for reading one --
    # untrusted on the way out, `untrusted_inputs` means hostile input, a
    # handle never substitutes for a required contract field -- bind every
    # tier, not only the roles that write entries.
    "roster/shared/context-use-policy.md",
    "roster/shared/agent-autonomy.yaml",
    "roster/shared/documentation-style.md",
    # Every role, not just write-capable ones: its "Never mutate a working
    # tree you did not create" section binds every tier. Destroying
    # uncommitted work with `git reset --hard`/`stash` needs no file-write
    # tool and produces no edit, so gating that rule behind a *write*-capable
    # tier coupled it to the wrong thing. The file opens with an
    # applicability header telling each tier which of its sections apply.
    "roster/shared/workspace-isolation.md",
]
ASK_HUMAN_RULE = (
    "You are a dispatched subagent: you cannot ask the human directly. If you "
    "reach a decision only a human can make, stop and return a clearly labeled "
    "blocking question in your result instead of guessing or proceeding."
)
SHARED_OVERRIDE_NOTE = (
    "The shared policy content above is this package's global defaults, "
    "embedded at packaging time. The project you are dispatched into may "
    "extend or override them under its own `.agents/shared/`; run `cadre "
    "resolve-shared <filename>` from that project's directory for each "
    "shared file's effective content instead of trusting the embedded text "
    "alone (see roster/shared/README.md in the source suite)."
)

RUNNER_CAPABILITIES_PATH = REPOSITORY_ROOT / "roster" / "runner-capabilities.json"


class ManifestError(ValueError):
    """Raised when `roster/runner-capabilities.json` is missing or does not
    carry the required structure. Fails closed rather than silently falling
    back to a stale hardcoded copy -- see idea #8
    (REQ-CADRE-BACKLOG-8, CM-NFR-5): `CAPABILITY_PROFILES`/`ALLOWED_MODELS`/
    `ALLOWED_CODEX_MODELS`/`ALLOWED_REASONING_EFFORTS` below are derived
    directly from this file at import time (stdlib `json` only, no new
    dependency -- see CM-NFR-4), so there is no separate committed copy that
    could independently drift from it. `roster/runner-capabilities.schema.json`
    additionally validates this file's own shape via a jsonschema-guarded
    standalone check (see `roster/orchestration/test/test_runner_capabilities.py`),
    matching `roster/catalog.schema.json`'s idea #10 precedent, but that
    schema is a supplementary shape check, not the mechanism the fields below
    rely on to stay in sync.
    """


def _load_runner_capabilities(path: Path = RUNNER_CAPABILITIES_PATH) -> dict[str, Any]:
    try:
        text = path.read_text(encoding="utf-8")
    except FileNotFoundError as error:
        raise ManifestError(f"{path}: runner capability manifest not found") from error
    try:
        manifest = json.loads(text)
    except json.JSONDecodeError as error:
        raise ManifestError(f"{path}: invalid JSON: {error}") from error
    if not isinstance(manifest, dict):
        raise ManifestError(f"{path}: top-level content must be a JSON object")
    for key in ("capability_tiers", "model_tiers", "allowed_reasoning_efforts"):
        if key not in manifest:
            raise ManifestError(f"{path}: missing required top-level key {key!r}")
    return manifest


def _capability_profiles_from_manifest(manifest: dict[str, Any], path: Path) -> dict[str, dict[str, Any]]:
    profiles: dict[str, dict[str, Any]] = {}
    for tier, data in manifest["capability_tiers"].items():
        if not isinstance(data, dict) or "tools" not in data or "sandbox_mode" not in data:
            raise ManifestError(f"{path}: capability_tiers[{tier!r}] must declare 'tools' and 'sandbox_mode'")
        profiles[tier] = {"tools": list(data["tools"]), "sandbox_mode": data["sandbox_mode"]}
    return profiles


def _model_tiers_from_manifest(manifest: dict[str, Any], path: Path) -> dict[str, dict[str, str]]:
    tiers: dict[str, dict[str, str]] = {}
    for tier, data in manifest["model_tiers"].items():
        if not isinstance(data, dict) or "codex_model" not in data or "reasoning_effort" not in data:
            raise ManifestError(f"{path}: model_tiers[{tier!r}] must declare 'codex_model' and 'reasoning_effort'")
        tiers[tier] = {"codex_model": data["codex_model"], "reasoning_effort": data["reasoning_effort"]}
    return tiers


_RUNNER_CAPABILITIES = _load_runner_capabilities()

# Single source of truth: `roster/runner-capabilities.json` (idea #8,
# REQ-CADRE-BACKLOG-8). Every constant below is derived from that file at
# import time, not hand-duplicated -- editing the manifest and re-running is
# the only edit location (CM-FR-2), and drift between this module and the
# manifest is structurally impossible (CM-NFR-5) because there is no second
# copy to fall out of sync.
CAPABILITY_PROFILES: dict[str, dict[str, Any]] = _capability_profiles_from_manifest(
    _RUNNER_CAPABILITIES, RUNNER_CAPABILITIES_PATH
)
MODEL_TIERS: dict[str, dict[str, str]] = _model_tiers_from_manifest(_RUNNER_CAPABILITIES, RUNNER_CAPABILITIES_PATH)
ALLOWED_MODELS = set(MODEL_TIERS)
ALLOWED_CODEX_MODELS = {data["codex_model"] for data in MODEL_TIERS.values()}
# Shared between both wrappers (Claude Code's `effort:` frontmatter and
# Codex's `model_reasoning_effort` TOML key) -- restricted to the subset
# both runners accept, so a single catalog.yaml value is always valid on
# either side. See catalog.yaml's `reasoning_effort` comment for the source
# of this list.
ALLOWED_REASONING_EFFORTS = set(_RUNNER_CAPABILITIES["allowed_reasoning_efforts"])

# Tiers whose sandbox_mode is not "read-only" -- i.e. every capability tier
# that can make a repository edit. Derived from the manifest rather than
# hardcoded tier names, so a future tier is picked up automatically (idea:
# "write-capable Cadre roles work in a git worktree by default").
#
# The single derivation of "which tiers can write": UNIVERSAL_POLICY_SECTIONS
# below excerpts for its complement, workspace-isolation.md names it
# explicitly when scoping which of its sections apply to whom, and
# `roster/orchestration/test/test_repository_health.py` reads it.
WRITE_CAPABLE_TIERS = frozenset(
    name for name, profile in CAPABILITY_PROFILES.items() if profile["sandbox_mode"] != "read-only"
)
# Shared-policy files embedded only into some capability tiers' wrapper
# instructions, keyed by the same repository-relative path convention as
# SHARED_POLICIES. A read-only role can still read any such file directly
# (or via `cadre resolve-shared`), so a file here must open with its own
# applicability header rather than relying on this tier gate for enforcement.
#
# Currently empty: workspace-isolation.md was the only entry and moved to
# SHARED_POLICIES once part of it came to bind every tier. The mechanism is
# kept because the tier-gating question recurs, and an empty dict is a
# clearer answer than a deleted concept.
TIER_SCOPED_POLICIES: dict[str, frozenset[str]] = {}

# Section-granular excerpting, deliberately a *separate* mechanism from
# TIER_SCOPED_POLICIES above rather than an extension of it (deagy/cadre#211).
#
# TIER_SCOPED_POLICIES answers "which tiers get this file at all", and that
# file-granularity is exactly what made the earlier attempt at
# workspace-isolation.md wrong: gating the whole file behind a write-capable
# tier also dropped its "Never mutate a working tree you did not create"
# section, which binds every tier. Overloading one dict with both meanings
# would leave that mistake re-expressible by a one-line edit. This dict
# instead answers a different question -- "when a file's own applicability
# header excludes a tier, which of its sections still bind that tier" -- and
# cannot express "drop the whole file", because an empty section tuple raises.
#
# Keys use the same repository-relative path convention as SHARED_POLICIES;
# values are the exact `## ` headings (heading text, without the `## `) that
# bind *every* tier. A role whose capability is not in WRITE_CAPABLE_TIERS
# receives the file's preamble (everything above its first `## ` heading,
# which is where the applicability header lives) plus these sections, in file
# order. Every other role receives the file verbatim, byte for byte.
#
# This is not a general policy-envelope generator, and must not grow into one
# -- see docs/investigations/policy-envelope-ceiling-2026-08.md, which
# measured the general case and recommends against it. A file belongs here
# only when it states its own tier applicability rule in its own text, so the
# excerpt encodes the file's stated rule rather than a judgment call about
# what some role might need.
# The membership rule is NOT "can this role write files" (deagy/cadre#211
# review). A read-only role still *creates* worktrees -- the never-mutate
# section instructs it to make a `--detach` inspection worktree rather than
# checking out a ref in someone else's tree -- so every rule about a worktree
# a role creates, removes, or resolves configuration from inside binds it
# too. Scoping by "has edits to isolate" alone once de-bound the
# never-remove-or-prune rule from exactly the roles the excerpt tells to
# create a worktree, which is how a reviewer ends up tidying up with
# `git worktree prune` and deregistering a teammate's tree.
UNIVERSAL_POLICY_SECTIONS: dict[str, tuple[str, ...]] = {
    "roster/shared/workspace-isolation.md": (
        "Never mutate a working tree you did not create",
        "The security-relevant-resolver rule",
        "Never remove or prune a worktree yourself",
        "No runner names as behavioral conditions",
    ),
}


class PolicyExcerptError(ValueError):
    """Raised when a UNIVERSAL_POLICY_SECTIONS file cannot be excerpted as
    declared.

    Fails the build closed on purpose: the alternative is silently shipping
    a read-only wrapper whose universally binding rule was renamed out from
    under it, which looks like nothing happened.

    What this raises on: a named `## ` heading missing; a named section whose
    body is empty; a file with no preamble to carry its applicability header;
    a preamble that does not name every universally binding section (so the
    header and this dict cannot drift apart); an unbalanced code fence; and
    an empty section tuple.

    What it does NOT check, so do not read it as total protection: the
    *content* of a kept section. A section gutted to a stub sentence still
    excerpts cleanly here. `plugin/tools/test_workspace_isolation_excerpt.py`
    asserts specific rule prose survives into the committed wrappers; that is
    where body-level protection actually lives.
    """


def split_policy_sections(body: str) -> tuple[str, list[tuple[str, str]]]:
    """Split a Markdown policy file into (preamble, [(heading, section)]).

    Sections break on level-2 (`## `) headings only, and headings inside
    fenced code blocks are ignored -- these files contain shell samples.
    Both ``` and ~~~ fences count; an unbalanced fence raises rather than
    silently swallowing every heading after it (which would truncate the
    excerpt with no other signal).
    """
    preamble: list[str] = []
    sections: list[tuple[str, list[str]]] = []
    in_fence = False
    fence_marker = ""
    for line in body.splitlines():
        stripped = line.lstrip()
        for marker in ("```", "~~~"):
            if stripped.startswith(marker) and (not in_fence or fence_marker == marker):
                in_fence = not in_fence
                fence_marker = marker if in_fence else ""
                break
        if not in_fence and line.startswith("## "):
            sections.append((line[3:].strip(), [line]))
        elif sections:
            sections[-1][1].append(line)
        else:
            preamble.append(line)
    if in_fence:
        raise PolicyExcerptError(
            f"unbalanced {fence_marker!r} code fence: every heading after it would be "
            "swallowed into the preceding section, silently truncating the excerpt"
        )
    return (
        "\n".join(preamble).strip(),
        [(heading, "\n".join(lines).strip()) for heading, lines in sections],
    )


def excerpt_universal_sections(relative: str, body: str) -> str:
    """Return `body` reduced to its preamble plus the sections that bind
    every tier, for a role that the file's own applicability header excludes.
    """
    required = UNIVERSAL_POLICY_SECTIONS[relative]
    if not required:
        raise PolicyExcerptError(
            f"{relative}: UNIVERSAL_POLICY_SECTIONS declares no universally binding "
            "section; this mechanism cannot be used to drop a whole file (use "
            "TIER_SCOPED_POLICIES if that is genuinely what is wanted)"
        )
    preamble, sections = split_policy_sections(body)
    if not preamble:
        raise PolicyExcerptError(
            f"{relative}: no preamble above the first '## ' heading, so the excerpt "
            "would carry no applicability header"
        )
    headings = [heading for heading, _ in sections]
    # A *balanced* stray fence pair needs no parser bug to leak policy: it
    # deletes the section boundaries between its markers, and whatever
    # headings it swallows are absorbed into the preceding section. When that
    # section is one of the kept universal ones, write-capable-only text
    # ships to every read-only wrapper with no other signal -- the leak is
    # silent in exactly the direction that matters, because swallowing a
    # *dropped* section instead trips the missing-heading check above.
    #
    # Deliberately checked here and not in split_policy_sections(): that
    # function is a general splitter whose documented behavior is to ignore
    # fenced headings, and callers legitimately rely on it. The strict
    # equality belongs on the path that ships policy text.
    raw_heading_count = sum(1 for line in body.splitlines() if line.startswith("## "))
    if raw_heading_count != len(sections):
        raise PolicyExcerptError(
            f"{relative}: parsed {len(sections)} section(s) but the file has "
            f"{raw_heading_count} '## ' line(s). A code fence is swallowing a section "
            "boundary, which silently merges sections and can leak write-capable-only "
            "text into the read-only excerpt. If a fenced '## ' line is genuinely "
            "needed here, add an explicit allow-list rather than removing this check."
        )
    missing = [heading for heading in required if heading not in headings]
    if missing:
        raise PolicyExcerptError(
            f"{relative}: required universally binding section(s) not found: "
            f"{', '.join(repr(heading) for heading in missing)}. Found: "
            f"{', '.join(repr(heading) for heading in headings)}. A heading listed in "
            "UNIVERSAL_POLICY_SECTIONS was renamed or removed -- update both together."
        )
    # The file's own applicability header must enumerate exactly the sections
    # this dict names, so a reader of either can check it against the other.
    # Symmetric on purpose: one-directional (`required` is named somewhere in
    # the preamble) lets the header promise a section binds every tier while
    # the dict silently drops it, which is the original #211 bug reached from
    # the other side.
    #
    # Matched against the header's bullet list, not the whole preamble text:
    # substring matching over prose both false-positives (the header names
    # `Isolating your own edits (write-capable tiers)` in a sentence
    # explaining what is excluded) and false-negatives on short headings
    # (heading "A" is satisfied by any word containing "A").
    promised = {
        line[2:].strip()
        for line in preamble.splitlines()
        if line.startswith("- ")
    } & set(headings)
    if promised != set(required):
        unlisted = sorted(set(required) - promised)
        overpromised = sorted(promised - set(required))
        detail = []
        if unlisted:
            detail.append(
                "registered but not named in the header's list: "
                + ", ".join(repr(heading) for heading in unlisted)
            )
        if overpromised:
            detail.append(
                "named in the header's list but not registered, so it would be "
                "dropped from every read-only wrapper despite the header saying it "
                "binds every tier: " + ", ".join(repr(heading) for heading in overpromised)
            )
        raise PolicyExcerptError(
            f"{relative}: the applicability header and UNIVERSAL_POLICY_SECTIONS "
            "disagree about which sections bind every tier (" + "; ".join(detail) + ")."
        )
    kept = []
    for heading, text in sections:
        if heading not in set(required):
            continue
        if not text.partition("\n")[2].strip():
            raise PolicyExcerptError(
                f"{relative}: universally binding section {heading!r} has an empty body; "
                "the excerpt would ship the heading alone"
            )
        kept.append(text)
    return "\n\n".join([preamble, *kept])


_unreachable_excerpts = sorted(set(UNIVERSAL_POLICY_SECTIONS) - set(SHARED_POLICIES))
if _unreachable_excerpts:
    # A key naming a file no wrapper embeds is dead configuration that reads
    # as protection. Fail at import rather than let it sit there looking
    # load-bearing.
    raise PolicyExcerptError(
        "UNIVERSAL_POLICY_SECTIONS names file(s) absent from SHARED_POLICIES, so the "
        f"excerpt would never be applied: {', '.join(_unreachable_excerpts)}"
    )

GENERATED_MARKER = "<!-- GENERATED FILE: edit the canonical source and regenerate; do not edit this copy. -->"
GENERATED_TOP_LEVEL = {
    "skills", "agents", "suite", "bin",
    # The provider bundle, copied verbatim from this repository's provider/
    # by generate_provider_copy(). Generated *for the package* even though
    # some members are hand-authored in the register -- inside the package
    # they are output, and drift against the register must fail the check.
    *PROVIDER_BUNDLE,
    "README.md",
    # The main plugin's own PreToolUse guard (deagy/cadre#129), fanned out
    # from GUARD_HOOK_SOURCE by generate_main_plugin_hook(). A top-level
    # member (not nested like the lifecycle plugins' hooks/) because it
    # belongs to the `cadre` package root itself, not a sub-plugin under
    # plugins/.
    "hooks",
}


def load_catalog(path: Path) -> dict[str, dict[str, Any]]:
    agents: dict[str, dict[str, Any]] = parse_catalog_entries(path.read_text(encoding="utf-8"))
    if not agents:
        raise ValueError("No agents found in catalog.yaml")
    for agent_id, metadata in agents.items():
        capability = metadata.get("capability")
        if capability not in CAPABILITY_PROFILES:
            raise ValueError(
                f"Agent {agent_id} must declare one of: {', '.join(sorted(CAPABILITY_PROFILES))}"
            )
        model = metadata.get("model")
        if model is not None and model not in ALLOWED_MODELS:
            raise ValueError(
                f"Agent {agent_id} declares an unsupported model tier {model!r}; "
                f"must be one of: {', '.join(sorted(ALLOWED_MODELS))}"
            )
        codex_model = metadata.get("codex_model")
        if codex_model is not None and codex_model not in ALLOWED_CODEX_MODELS:
            raise ValueError(
                f"Agent {agent_id} declares an unsupported codex_model {codex_model!r}; "
                f"must be one of: {', '.join(sorted(ALLOWED_CODEX_MODELS))}"
            )
        reasoning_effort = metadata.get("reasoning_effort")
        if reasoning_effort is not None and reasoning_effort not in ALLOWED_REASONING_EFFORTS:
            raise ValueError(
                f"Agent {agent_id} declares an unsupported reasoning_effort {reasoning_effort!r}; "
                f"must be one of: {', '.join(sorted(ALLOWED_REASONING_EFFORTS))}"
            )
    return agents


def load_skill_frontmatter(skill_file: Path) -> dict[str, str]:
    content = skill_file.read_text(encoding="utf-8")
    block = content.split("---", 2)[1]
    fields: dict[str, str] = {}
    for line in block.splitlines():
        if re.match(r"^[a-z_]+:", line):
            key, value = line.split(":", 1)
            fields[key.strip()] = value.strip()
    return fields


def toml_string(value: str) -> str:
    return json.dumps(value)


def write(path: Path, content: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding="utf-8")


def reset_generated_content(plugin_root: Path, *, remove_readme: bool = True) -> None:
    # Derived from GENERATED_TOP_LEVEL rather than re-listed, so a new member
    # can never be generated-but-not-reset (which would leave stale files that
    # files_equal() then reports as drift forever, with no way to regenerate
    # out of it). bin/ is the one entry not removed wholesale: only bin/cadre
    # is generated, and the directory may legitimately hold nothing else.
    # README.md is the one entry that may need to survive too, when
    # `remove_readme=False` (see generate_package()'s write_readme parameter
    # and main()'s downstream-identity guard, deagy/cadre#97): a downstream
    # repository with its own hand-authored README.md must never have it
    # deleted here even transiently, since generate_suite_copy() -- called
    # after this function -- won't rewrite it either in that case.
    for name in sorted(GENERATED_TOP_LEVEL - {"bin"}):
        if name == "README.md" and not remove_readme:
            continue
        path = plugin_root / name
        if path.is_dir():
            shutil.rmtree(path)
        elif path.exists():
            path.unlink()
    cadre_wrapper = plugin_root / "bin" / "cadre"
    if cadre_wrapper.exists():
        cadre_wrapper.unlink()
    # Nested generated content (see GENERATED_NESTED_PATHS): reset only the
    # generated subtree itself, never its hand-authored siblings -- e.g.
    # plugins/lifecycle/skills/ is removed and regenerated, but
    # plugins/lifecycle/.claude-plugin/, .codex-plugin/, and tools/ (the
    # plugin's own manifests and bootstrap tooling) must survive.
    for relative in GENERATED_NESTED_PATHS:
        path = plugin_root / relative
        if path.is_dir():
            shutil.rmtree(path)
        elif path.exists():
            path.unlink()


def generate_provider_copy(catalog: dict[str, dict[str, Any]], plugin_root: Path) -> list[Path]:
    """Copy this repository's provider/ bundle into the package root.

    The bundle is register-owned (see PROVIDER_ROOT): the package receives a
    verbatim copy so an installed plugin carries its own provider contracts,
    and files_equal() then fails the drift check if the package's copy ever
    diverges from the register's.
    """
    # The generated members of provider/ are produced by
    # `cadre generate-role-metadata`, not here, so this generator can only copy
    # whatever the register last committed. Editing a role's AGENT.md and
    # running `generate-plugin` alone would refresh the package's Claude Code
    # wrappers (built live from the catalog) while silently packaging stale
    # Codex wrappers and a stale catalog export -- and a following --check,
    # which compares package against the same stale register content, would
    # call it current. Fail loudly instead.
    stale = [
        str(PROVIDER_ROOT / relative)
        for relative, expected in {
            "agent-catalog.json": agent_catalog_export_content(catalog),
            **{f"codex-agents/{name}": body for name, body in codex_wrapper_contents(catalog).items()},
        }.items()
        if not (PROVIDER_ROOT / relative).is_file()
        or (PROVIDER_ROOT / relative).read_text(encoding="utf-8") != expected
    ]
    if stale:
        raise SystemExit(
            "provider/ is stale; run `cadre generate-role-metadata` before regenerating the "
            "package: " + ", ".join(sorted(stale)[:5]) + (" ..." if len(stale) > 5 else "")
        )
    written: list[Path] = []
    for name in PROVIDER_BUNDLE:
        source = PROVIDER_ROOT / name
        if not source.exists():
            raise SystemExit(
                f"{source}: missing from the provider bundle. Run "
                "`cadre generate-role-metadata` if it is generated content."
            )
        target = plugin_root / name
        if source.is_dir():
            # symlinks=False would dereference, silently vendoring out-of-tree
            # content into a published package; refuse instead, matching
            # generate_skill_copies()'s stance.
            for path in sorted(source.rglob("*")):
                if path.is_symlink():
                    raise SystemExit(f"{path}: symlinks are not packaged; replace it with a regular file")
            shutil.copytree(source, target)
            written.extend(path for path in sorted(target.rglob("*")) if path.is_file())
        elif name == "agent-catalog.json":
            # Re-point `definition` from the register's provider/roles/ tree to
            # the package's own suite/roster/ copy of the same files. The
            # kernel resolves these relative to whichever copy it reads and
            # rejects escapes, so the two trees genuinely need different
            # spellings -- see PROVIDER_DEFINITION_PREFIX.
            catalog = json.loads(source.read_text(encoding="utf-8"))
            for metadata in catalog["agents"].values():
                definition = metadata["definition"]
                if not definition.startswith(PROVIDER_DEFINITION_PREFIX):
                    raise SystemExit(
                        f"{source}: definition {definition!r} does not start with "
                        f"{PROVIDER_DEFINITION_PREFIX!r}; run `cadre generate-role-metadata`"
                    )
                metadata["definition"] = (
                    PACKAGE_DEFINITION_PREFIX + definition[len(PROVIDER_DEFINITION_PREFIX):]
                )
            write(target, json.dumps(catalog, indent=2) + "\n")
            written.append(target)
        else:
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(source, target)
            written.append(target)
    return written


def generate_skill_copies(plugin_root: Path) -> list[Path]:
    written = []
    tracked = {
        relative
        for relative in subprocess.run(
            ["git", "ls-files", ".agents/skills"], cwd=REPOSITORY_ROOT,
            check=True, capture_output=True, text=True, encoding="utf-8",
        ).stdout.splitlines()
        if (REPOSITORY_ROOT / relative).is_file()
    }
    for skill_dir in sorted(p for p in SKILLS_ROOT.iterdir() if p.is_dir()):
        skill_file = skill_dir / "SKILL.md"
        if not skill_file.is_file():
            continue
        package_root = Path(SKILL_PACKAGE_TARGETS.get(skill_dir.name, "skills"))
        target_dir = plugin_root / package_root / skill_dir.name
        for relative_text in sorted(path for path in tracked if path.startswith(f".agents/skills/{skill_dir.name}/")):
            source = REPOSITORY_ROOT / relative_text
            if source.is_symlink():
                raise ValueError(f"Symlinks are not allowed in packaged skills: {relative_text}")
            target = target_dir / Path(relative_text).relative_to(skill_dir.relative_to(REPOSITORY_ROOT))
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(source, target)
        target = target_dir / "SKILL.md"
        content = target.read_text(encoding="utf-8")
        frontmatter_end = content.find("---", 3) + 3
        # target_dir sits package_root's depth + 1 (the skill's own directory)
        # below plugin_root -- e.g. "skills/<name>/" is 2 levels down (the
        # original, hardcoded "../.."), "plugins/lifecycle/skills/<name>/" is
        # 4. Computed rather than hardcoded so a SKILL_PACKAGE_TARGETS entry
        # can never ship a wrong hint.
        up_levels = "/".join([".."] * (len(package_root.parts) + 1))
        package_note = (
            "\n\n> Packaged suite note: when the current project has no local `roster/` "
            f"tree, resolve suite files under `{up_levels}/suite/roster/` relative to this "
            "`SKILL.md`. The packaged plugin is self-contained; do not look for the "
            "source checkout.\n"
        )
        target.write_text(content[:frontmatter_end] + package_note + content[frontmatter_end:], encoding="utf-8")
        written.extend(path for path in target_dir.rglob("*") if path.is_file())
    return written


def role_wrapper_inputs(agent_id: str, metadata: dict[str, Any]) -> dict[str, Any]:
    """The wrapper content both runners derive from one role.

    Shared by generate_agent_wrappers() (Claude Code, package-only) and
    generate_codex_wrappers() (Codex, register-side under provider/), which
    write to different repositories but must embed byte-identical role and
    shared-policy instructions.
    """
    definition = metadata["definition"]
    phase = metadata.get("phase", "unknown")
    capability = metadata["capability"]
    profile = CAPABILITY_PROFILES[capability]

    def load_policy_sections(relatives: list[str]) -> list[str]:
        # Shared policy files are optional: a project (or this repository)
        # may not have every one of them, or may have emptied one, and
        # neither state is a defect. Skip missing or emptied files rather
        # than failing, and don't leave a stray blank section behind.
        sections = []
        for relative in relatives:
            path = REPOSITORY_ROOT / relative
            if not path.is_file():
                continue
            body = path.read_text(encoding="utf-8").strip()
            if not body:
                continue
            # Section-granular excerpt for a tier the file's own applicability
            # header excludes; raises rather than truncating silently.
            if relative in UNIVERSAL_POLICY_SECTIONS and capability not in WRITE_CAPABLE_TIERS:
                body = excerpt_universal_sections(relative, body)
            sections.append(f"# Shared policy: {relative}\n\n{body}")
        return sections

    shared_sections = load_policy_sections(SHARED_POLICIES)
    tier_scoped_relatives = [
        relative for relative, tiers in TIER_SCOPED_POLICIES.items() if capability in tiers
    ]
    shared_sections.extend(load_policy_sections(tier_scoped_relatives))
    shared_content = "\n\n".join(shared_sections)
    # A migrated role's AGENT.md carries `---`-delimited frontmatter
    # ahead of its prose body (see role_metadata.py); that frontmatter
    # is generated-file bookkeeping for catalog.yaml/routing.json, not
    # role instructions, so it must never be embedded into the wrapper.
    role_body = strip_frontmatter((AGENTS_ROOT / definition).read_text(encoding="utf-8")).strip()
    description = f"Secure cloud agent suite role for the {phase} phase ({agent_id})."
    instructions_parts = [f"# Role: {agent_id}\n\n{role_body}"]
    if shared_content:
        instructions_parts.append(shared_content)
    instructions_parts.append(SHARED_OVERRIDE_NOTE)
    instructions_parts.append(ASK_HUMAN_RULE)
    return {
        "definition": definition,
        "description": description,
        "profile": profile,
        "model": metadata.get("model"),
        "codex_model": metadata.get("codex_model"),
        "reasoning_effort": metadata.get("reasoning_effort"),
        "instructions": "\n\n".join(instructions_parts),
    }


def generate_agent_wrappers(catalog: dict[str, dict[str, Any]], plugin_root: Path) -> list[Path]:
    """Claude Code plugin-bundled subagent wrappers. Package-only: Claude Code
    auto-discovers these from the installed plugin's agents/ directory, so they
    have no meaning outside it (unlike the Codex wrappers below).
    """
    written = []
    for agent_id, metadata in sorted(catalog.items()):
        inputs = role_wrapper_inputs(agent_id, metadata)
        definition = inputs["definition"]
        description = inputs["description"]
        profile = inputs["profile"]
        model = inputs["model"]
        reasoning_effort = inputs["reasoning_effort"]
        instructions = inputs["instructions"]

        md_target = plugin_root / "agents" / f"{agent_id}.md"
        md_lines = [
            "---",
            f"name: {agent_id}",
            f"description: {description}",
            f"tools: {', '.join(profile['tools'])}",
        ]
        if model:
            md_lines.append(f"model: {model}")
        if reasoning_effort:
            md_lines.append(f"effort: {reasoning_effort}")
        md_lines += [
            "generated: true",
            f"canonical_source: roster/{definition}",
            "---",
            "",
            instructions,
            "",
        ]
        write(md_target, "\n".join(md_lines))
        written.append(md_target)
    return written


def codex_wrapper_contents(catalog: dict[str, dict[str, Any]]) -> dict[str, str]:
    """Codex role wrappers as {filename: content}, for provider/codex-agents/.

    Codex has no plugin-bundled-agent mechanism: it discovers custom agents
    only from ~/.codex/agents/ or a project's .codex/agents/, never from a
    plugin manifest. These are therefore a tracked staging copy that `cadre
    bootstrap-codex` installs from -- which is why they live here, in the
    register, rather than only in the plugin package: the pip/pipx
    distribution vendors provider/ and must be able to serve bootstrap-codex
    without a plugin install. `cadre generate-role-metadata` writes and
    drift-checks them there; generate_provider_copy() then copies the same
    files into the package.

    Returns content rather than writing, so generate_role_metadata.py can fold
    these into the same rendered-content map it uses for catalog.yaml and
    routing.json and get --check for free.
    """
    contents: dict[str, str] = {}
    for agent_id, metadata in sorted(catalog.items()):
        inputs = role_wrapper_inputs(agent_id, metadata)
        definition = inputs["definition"]
        description = inputs["description"]
        profile = inputs["profile"]
        codex_model = inputs["codex_model"]
        reasoning_effort = inputs["reasoning_effort"]
        instructions = inputs["instructions"]

        # `model` uses catalog.yaml's separate `codex_model` OpenAI identifier, not
        # the Claude Code wrapper's haiku/sonnet/opus tier name -- the two
        # runners don't share a model-naming space. Re-verify these identifiers
        # against current Codex CLI docs before relying on them in automation.
        codex_agent_id = f"agents-{agent_id}"
        toml_lines = [
            f"# GENERATED FILE: canonical source is roster/{definition}",
            f"name = {toml_string(codex_agent_id)}",
            f"description = {toml_string(description)}",
            f"sandbox_mode = {toml_string(profile['sandbox_mode'])}",
        ]
        if codex_model:
            toml_lines.append(f"model = {toml_string(codex_model)}")
        if reasoning_effort:
            toml_lines.append(f"model_reasoning_effort = {toml_string(reasoning_effort)}")
        toml_lines += [
            f"developer_instructions = {toml_string(instructions)}",
            "",
        ]
        contents[f"{codex_agent_id}.toml"] = "\n".join(toml_lines)
    return contents


def derive_kind(definition: str) -> str:
    if definition.startswith("review/") or definition == "engineering/test-engineer/AGENT.md":
        return "reviewer"
    if definition.startswith("support/"):
        return "specialist"
    if definition in {"documentation/evidence-curator/AGENT.md", "knowledge-store/AGENT.md"}:
        return "specialist"
    return "author"


def agent_catalog_export_content(catalog: dict[str, dict[str, Any]]) -> str:
    """Package-relative catalog export consumed through provider.json.

    Written into this repository's own provider/ bundle for the same reason as
    generate_codex_wrappers() above: `cadre sdlc` must work from the pip/pipx
    distribution, which vendors provider/ but not the plugin package. The
    `definition` values stay package-relative (suite/roster/...) because
    provider.json resolves them inside an installed plugin; that is unchanged
    from before the register/plugin split.
    """
    agents = {
        agent_id: {
            "phase": metadata.get("phase", "unknown"),
            "kind": derive_kind(metadata["definition"]),
            "capabilities": (
                ["reviewer"]
                if derive_kind(metadata["definition"]) == "reviewer"
                else ["author", "dispatch"]
            ),
            "definition": f"{PROVIDER_DEFINITION_PREFIX}{metadata['definition']}",
        }
        for agent_id, metadata in sorted(catalog.items())
    }
    return json.dumps({"schema_version": 1, "agents": agents}, indent=2) + "\n"


# Subcommands from bin/subcommands.tsv that manage this source repository
# itself (regenerating/inspecting the packaged plugin) and therefore have no
# meaning once shipped inside the plugin they regenerate.
PACKAGED_SUBCOMMAND_EXCLUSIONS = {"generate-plugin", "generate-authority-aides", "generate-role-metadata", "version"}

# Extra argv this packaged wrapper must inject ahead of the caller's own
# "$@" for a subcommand whose packaged invocation needs plugin-relative
# context bin/subcommands.tsv has no column for (bootstrap-codex's wrapper
# source lives under the packaged plugin, not this source repository).
PACKAGED_SUBCOMMAND_EXTRA_ARGS = {
    "bootstrap-codex": '--source "$PLUGIN_ROOT/codex-agents"',
}


def load_subcommand_table(repository_root: Path) -> list[tuple[str, str]]:
    """`name\tdescription` rows.

    The script column is gone. It named the Python implementation the packaged
    wrapper exec'd when it could not resolve the Go binary; that fallback is
    removed and the suite no longer ships those scripts, so a column naming
    files the distribution does not contain would be stale by construction.
    """
    table = repository_root / "bin" / "subcommands.tsv"
    rows = []
    for line in table.read_text(encoding="utf-8").splitlines():
        if not line:
            continue
        name, description = line.split("\t")
        rows.append((name, description))
    return rows


def packaged_subcommands(repository_root: Path) -> list[str]:
    """The subcommand names the packaged plugin serves.

    No script path any more: the wrapper execs the Go binary and lets it
    dispatch, instead of emitting a case arm per subcommand.
    """
    return [
        name
        for name, _description in load_subcommand_table(repository_root)
        if name not in PACKAGED_SUBCOMMAND_EXCLUSIONS
    ]


def _is_kernel_doc(path: Path) -> bool:
    """`docs/kernel/` documents the lifecycle kernel, not the role suite.

    Before the monorepo merge these lived in a separate repository and could
    not have been packaged here even by accident. They describe G1-G10 gate
    semantics and the LangGraph engine -- neither of which the packaged role
    suite contains -- and their relative links point at `kernel/` and
    `engine/` siblings that the package does not ship, so copying them in
    produces a plugin full of dangling links.
    """
    return "kernel" in path.relative_to(REPOSITORY_ROOT).parts


def kernel_requirement_text() -> str:
    """Interpolate the declared kernel range into the generated launcher.

    The generated `bin/cadre` is POSIX sh with no JSON parser, so it cannot
    read `provider.json` at runtime the way `bin/cadre.py` does. Resolve it
    here instead: this generator already owns provider.json, and the
    launcher is fully regenerated whenever it changes, so the two cannot
    drift. Do not hardcode a version -- provider.json's own `version` and
    its `kernel_compatibility` are different version lines.
    """
    try:
        manifest = json.loads((PROVIDER_ROOT / "provider.json").read_text(encoding="utf-8"))
        return f"v{manifest['kernel_compatibility']['minimum']}+"
    except (OSError, ValueError, KeyError, TypeError):
        return "a compatible version"


def generate_kernel_compatibility(plugin_root: Path) -> list[Path]:
    """Ship the kernel compatibility window into each lifecycle plugin.

    Derived from provider.json rather than copied wholesale: the bootstrap
    only needs `{minimum, maximum_exclusive}`, and provider.json also carries
    an unrelated `version` field that has been mistaken for the kernel range
    before. Emitting just the window removes that ambiguity from the packaged
    artifact entirely.
    """
    manifest = json.loads((PROVIDER_ROOT / "provider.json").read_text(encoding="utf-8"))
    compatibility = manifest["kernel_compatibility"]
    payload = {
        "_comment": (
            "Generated from provider.json's kernel_compatibility by "
            "cadre generate-plugin. Do not hand-edit; edit provider/provider.json "
            "and regenerate."
        ),
        "minimum": compatibility["minimum"],
        "maximum_exclusive": compatibility["maximum_exclusive"],
    }
    written: list[Path] = []
    for relative in KERNEL_COMPATIBILITY_TARGETS:
        target = plugin_root / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(json.dumps(payload, indent=2) + "\n", encoding="utf-8")
        written.append(target)

    bootstrap = BOOTSTRAP_SOURCE.read_text(encoding="utf-8")
    for relative in BOOTSTRAP_TARGETS:
        target = plugin_root / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(bootstrap, encoding="utf-8")
        written.append(target)

    hooks_json = json.dumps(LIFECYCLE_HOOKS, indent=2) + "\n"
    for relative in LIFECYCLE_HOOK_TARGETS:
        target = plugin_root / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(hooks_json, encoding="utf-8")
        written.append(target)

    install_skill = (SKILLS_ROOT / "cadre-install-kernel" / "SKILL.md").read_text(encoding="utf-8")
    for relative in LIFECYCLE_INSTALL_SKILL_TARGETS:
        target = plugin_root / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(install_skill, encoding="utf-8")
        written.append(target)

    for relative in LIFECYCLE_BIN_TARGETS:
        target = plugin_root / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        body = AGENTIC_SDLC_SHIM if target.name == "agentic-sdlc" else INSTALL_KERNEL_SHIM
        target.write_text(body, encoding="utf-8")
        # Useless without the executable bit: Claude Code adds bin/ to PATH
        # but does not chmod anything.
        target.chmod(0o755)
        written.append(target)
    return written


def generate_main_plugin_hook(plugin_root: Path) -> list[Path]:
    """Package this repository's own destructive-git `PreToolUse` guard
    (deagy/cadre#129) into the main `cadre` plugin, so any project that
    installs the plugin gets the same structural protection this repository
    runs on itself via `.claude/settings.json`, without needing the separate
    cadre-lifecycle-* plugins. See GUARD_HOOK_SOURCE/MAIN_PLUGIN_HOOKS above
    for the design rationale and command-line derivation.
    """
    written: list[Path] = []
    guard_target = plugin_root / GUARD_HOOK_TARGET
    guard_target.parent.mkdir(parents=True, exist_ok=True)
    shutil.copy2(GUARD_HOOK_SOURCE, guard_target)
    written.append(guard_target)

    hooks_target = plugin_root / "hooks" / "hooks.json"
    hooks_target.write_text(json.dumps(MAIN_PLUGIN_HOOKS, indent=2) + "\n", encoding="utf-8")
    written.append(hooks_target)
    return written


def generate_bin_wrapper(plugin_root: Path) -> Path:
    """Write a wrapper that execs the Go binary, or fails saying why.

    `internal/generators/plugin_generation.go` owns the real one -- the
    hardened launcher with binary resolution, checksum verification and a
    sidecar-verified cache. This exists so a package generated through the
    Python path is still runnable, and deliberately does not reimplement any
    of that.

    What it no longer does is exec Python. The packaged wrapper used to fall
    back to `roster/**/*.py` whenever the binary could not be resolved, and
    that fallback is what made the suite carry ~24,500 lines of Python. Both
    it and those files are gone.
    """
    target = plugin_root / "bin" / "cadre"
    names = "|".join([*packaged_subcommands(REPOSITORY_ROOT), "sdlc"])
    body = "\n".join(
        [
            "#!/bin/sh",
            "set -eu",
            'BIN_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)',
            'PLUGIN_ROOT=$(CDPATH= cd -- "$BIN_DIR/.." && pwd)',
            'SUITE_ROOT="$PLUGIN_ROOT/suite"',
            'if [ "${1:-}" = "--interactive" ]; then',
            "  CADRE_INTERACTIVE=1",
            "  export CADRE_INTERACTIVE",
            "  shift",
            "fi",
            'if [ -n "${CADRE_BINARY:-}" ] && [ -x "${CADRE_BINARY:-}" ]; then',
            '  CADRE_REPO_ROOT="$SUITE_ROOT" exec "$CADRE_BINARY" "$@"',
            "fi",
            'echo "cadre: no cadre binary available." >&2',
            f'echo "  subcommands: {names}" >&2',
            'echo "" >&2',
            'echo "This package was generated through the Python generator, which does" >&2',
            'echo "not emit the hardened downloading launcher. Set CADRE_BINARY to a" >&2',
            'echo "cadre binary, or regenerate with ./bin/cadre generate-plugin." >&2',
            "exit 1",
            "",
        ]
    )
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(body, encoding="utf-8")
    target.chmod(0o755)
    return target


def generate_suite_copy(
    catalog: dict[str, dict[str, Any]], plugin_root: Path, *, write_readme: bool = True
) -> list[Path]:
    tracked = {
        relative
        for relative in subprocess.run(
        ["git", "ls-files", "roster"],
        cwd=REPOSITORY_ROOT,
        check=True,
        capture_output=True,
        text=True,
        encoding="utf-8",
        ).stdout.splitlines()
        if (REPOSITORY_ROOT / relative).is_file()
    }
    # Three carve-outs used to sit here, forcing agentic_sdlc_contracts.py,
    # sync_codex_agents.py and selection_telemetry.py into the packaged suite
    # even when untracked, because the packaged wrapper exec'd them. The
    # wrapper execs the Go binary now and the suite ships no Python at all,
    # so a carve-out for a Python helper has nothing to carve out.
    role_paths = {f"roster/{metadata['definition']}" for metadata in catalog.values()}
    # `catalog` was parsed straight off the worktree copy of catalog.yaml, but
    # `tracked` only reflects git's index -- an uncommitted new role's
    # AGENT.md would otherwise pass this function silently, then still get a
    # wrapper and an agent-catalog.json entry from generate_agent_wrappers()/
    # generate_agent_catalog_export() (which read `catalog` directly, not
    # `tracked`), producing a package that references a suite file that was
    # never copied. Fail loudly here instead.
    untracked_role_paths = role_paths - tracked
    if untracked_role_paths:
        raise ValueError(
            "roster/catalog.yaml references role definition file(s) not tracked in git; "
            "commit them (git add) before regenerating the plugin: "
            + ", ".join(sorted(untracked_role_paths))
        )
    # The guard that used to sit here required every script named in
    # bin/subcommands.tsv to be tracked in git, because the wrapper emitted a
    # case arm exec'ing each one. The table has no script column now and the
    # wrapper execs no scripts, so there is nothing left for it to check.

    documentation_paths = {
        "AGENTS.md",
        "CONTRIBUTING.md",
        "IDENTITY.md",
        *(
            str(path.relative_to(REPOSITORY_ROOT))
            for path in (REPOSITORY_ROOT / "docs").rglob("*")
            if path.is_file() and not _is_kernel_doc(path)
        ),
    }
    selected: list[str] = []
    for relative in sorted(tracked):
        if relative in documentation_paths:
            selected.append(relative)
        elif relative in role_paths or relative in {
            "roster/catalog.yaml",
            "roster/catalog.schema.json",
            # PP-FR-2. Like PROVIDER_BUNDLE at :101, this is a CLOSED allowlist
            # rather than a directory walk, so an unlisted roster-root file is
            # silently skipped -- and a packaged suite whose roster.json is
            # missing is not a valid roster package, failing in the installed
            # plugin and nowhere in CI. The requirements baseline recorded that
            # trap for provider/; this is the same trap one directory over, and
            # it fired the moment roster.json existed.
            "roster/roster.json",
            "roster/catalog-order.txt",
            "roster/context-pack-order.txt",
            "roster/_catalog_header.yaml.tmpl",
            "roster/runner-capabilities.json",
            "roster/runner-capabilities.schema.json",
            "roster/authority/aides.yaml",
            "roster/authority/_template.md.tmpl",
            "roster/README.md",
            "roster/RUNBOOK.md",
        }:
            selected.append(relative)
        elif relative.startswith("roster/context-packs/"):
            selected.append(relative)
        elif relative.startswith(("roster/shared/", "roster/workflows/")):
            selected.append(relative)
        elif (
            relative.startswith("roster/orchestration/")
            and "/runs/" not in relative
            and "/test/" not in relative
            and "/examples/" not in relative
            and not relative.endswith("generate_global_plugin.py")
            and not relative.endswith("migrate_execution_summary.py")
            and not relative.endswith("plugin_version.py")
            # These two scripts import generate_global_plugin.py (excluded
            # above) and their subcommands are already excluded from the
            # packaged bin/cadre wrapper via PACKAGED_SUBCOMMAND_EXCLUSIONS
            # -- packaging them anyway would ship a non-functional entry
            # point (ModuleNotFoundError on generate_global_plugin) that
            # looks runnable but isn't.
            and not relative.endswith("generate_role_metadata.py")
            and not relative.endswith("generate_authority_aides.py")
        ):
            selected.append(relative)
        elif relative.startswith("roster/knowledge-store/src/") or relative in {
            "roster/knowledge-store/README.md",
            "roster/knowledge-store/SECURITY.md",
            # staged_records.py's findings cite this schema by path as the
            # contract a malformed record violated. Packaging the module
            # without it would ship error messages pointing at a file the
            # reader cannot open.
            "roster/knowledge-store/proposed-knowledge.schema.json",
        }:
            selected.append(relative)
        elif relative in {
            "roster/context-store/README.md",
            "roster/context-store/SECURITY.md",
        }:
            # The context store's documentation travels with the distribution;
            # its implementation no longer does. roster/context-store/src/ was
            # deleted once `cadre context` was served entirely by the Go
            # binary, and the packaged `bin/cadre` execs that binary for every
            # subcommand with no Python path at all. SECURITY.md is packaged
            # for the same reason as the knowledge store's: the CLI's own error
            # text points readers at it.
            selected.append(relative)
    selected.extend(
        relative
        for relative in sorted(documentation_paths)
        if relative not in selected and (REPOSITORY_ROOT / relative).is_file()
    )
    written: list[Path] = []
    # The packaged README is register-owned (PACKAGING_README) and rendered to
    # two places: the package root, where it is the repository's front page,
    # and suite/README.md, where the packaged docs cross-reference it.
    #
    # The package-root copy is skipped when write_readme=False (see
    # generate_package()'s parameter and main()'s downstream-identity guard,
    # deagy/cadre#97): a target repository that already has its own
    # `.codex-plugin/plugin.json` -- meaning it is itself a hand-authored
    # downstream package, not a fresh distribution target -- keeps its own
    # README.md untouched. suite/README.md is unaffected either way: it
    # always carries GENERATED_MARKER and is always register-owned content,
    # even inside a downstream repository's own package tree.
    package_readme_content = PACKAGING_README.read_text(encoding="utf-8")
    if write_readme:
        write(plugin_root / "README.md", package_readme_content)
        written.append(plugin_root / "README.md")
    write(plugin_root / "suite" / "README.md", f"{GENERATED_MARKER}\n\n{package_readme_content}")
    written.append(plugin_root / "suite" / "README.md")
    for relative in selected:
        source = REPOSITORY_ROOT / relative
        target = plugin_root / "suite" / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        if target.suffix.lower() == ".md":
            content = source.read_text(encoding="utf-8")
            content = content.replace("../bin/cadre", "../../bin/cadre")
            # Links to the register's top-level README point at the packaged
            # README under suite/. Depth-aware: a plain
            # `"../README.md" -> "README.md"` substring replace also matches
            # inside `../../README.md` (written by files one level deeper),
            # silently retargeting them to suite/roster/README.md -- a file
            # that exists, so no link checker catches it.
            source_depth = len(relative.split("/")) - 1
            if source_depth:
                root_readme = "../" * source_depth + "README.md"
                suite_readme = os.path.relpath(
                    plugin_root / "suite" / "README.md", target.parent
                ).replace(os.sep, "/")
                content = content.replace(root_readme, suite_readme)
            # The register's source for the packaged README (PACKAGING_README)
            # has no counterpart inside the package; point at the packaged
            # copy of it instead. Every file carrying this link sits one level
            # under suite/, so `../README.md` resolves to suite/README.md.
            content = content.replace("../packaging/plugin-README.md", "../README.md")
            # Skills are packaged at the package root (skills/<name>/) by
            # default, not under suite/, so a source link into .agents/skills/
            # would dangle. Computed per file rather than hardcoded:
            # suite/docs/x.md and suite/AGENTS.md sit at different depths and
            # need different prefixes for the same source text. Skill-name-
            # aware (not a flat prefix replace) because SKILL_PACKAGE_TARGETS
            # retargets some skills (lifecycle-onboarding, lifecycle-review)
            # into a sub-plugin directory instead -- a link to one of those
            # must land on its actual packaged location, not skills/.
            to_package_root = os.path.relpath(plugin_root, target.parent).replace(os.sep, "/")

            def _rewrite_skill_link(match: "re.Match[str]") -> str:
                skill_name = match.group("skill")
                package_subdir = SKILL_PACKAGE_TARGETS.get(skill_name, "skills")
                return f"{to_package_root}/{package_subdir}/{skill_name}"

            content = re.sub(r"\.\./\.agents/skills/(?P<skill>[^/]+)", _rewrite_skill_link, content)
            content = re.sub(r"\.\./\.claude/skills/(?P<skill>[^/]+)", _rewrite_skill_link, content)
            # Not packaged at all (the package ships no changelog, and tests
            # are excluded), so point at the register instead of dangling.
            content = content.replace("](../CHANGELOG.md)", f"]({REGISTER_URL}/blob/main/CHANGELOG.md)")
            content = content.replace(
                "](../roster/orchestration/test/",
                f"]({REGISTER_URL}/blob/main/roster/orchestration/test/",
            )
            # A migrated role's AGENT.md starts with `---`-delimited
            # frontmatter (see role_metadata.py); inserting the marker at
            # byte 0 would land it inside that frontmatter block instead of
            # before it, corrupting the block. Insert after the closing
            # delimiter instead, mirroring generate_skill_copies()'s
            # SKILL.md package-note placement above. No-op today (no
            # AGENT.md has frontmatter yet), but no source file in the
            # copied suite happens to start with "---" today either, so
            # this only ever takes the plain byte-0 path currently. Use
            # role_metadata's exact-line delimiter detection rather than a
            # raw substring search: a raw search would false-match a
            # literal "---" embedded inside a frontmatter value's text
            # before the real closing delimiter line.
            if is_migrated(content):
                frontmatter_end = frontmatter_closing_delimiter_end(content)
                content = content[:frontmatter_end] + f"\n\n{GENERATED_MARKER}" + content[frontmatter_end:]
            else:
                content = f"{GENERATED_MARKER}\n\n{content}"
            write(target, content)
        else:
            shutil.copy2(source, target)
        written.append(target)
    return written


def generate_package(
    catalog: dict[str, dict[str, Any]], plugin_root: Path, *, write_readme: bool = True
) -> list[Path]:
    # write_readme=False is main()'s downstream-identity guard (deagy/cadre#97):
    # a target that already carries its own `.codex-plugin/plugin.json` keeps
    # its own README.md through both the reset and the regeneration below.
    reset_generated_content(plugin_root, remove_readme=write_readme)
    return (
        generate_skill_copies(plugin_root)
        + generate_suite_copy(catalog, plugin_root, write_readme=write_readme)
        + generate_agent_wrappers(catalog, plugin_root)
        + generate_provider_copy(catalog, plugin_root)
        + generate_kernel_compatibility(plugin_root)
        + generate_main_plugin_hook(plugin_root)
        + [generate_bin_wrapper(plugin_root)]
    )


def files_equal(left: Path, right: Path, *, compare_readme: bool = True) -> bool:
    nested_generated = tuple(Path(relative) for relative in GENERATED_NESTED_PATHS)
    readme_path = Path("README.md")

    def generated_files(root: Path) -> set[Path]:
        return {
            path.relative_to(root)
            for path in root.rglob("*")
            if path.is_file()
            and (
                path.relative_to(root).parts[0] in GENERATED_TOP_LEVEL
                or any(path.relative_to(root).is_relative_to(nested) for nested in nested_generated)
            )
            and "__pycache__" not in path.relative_to(root).parts
            and path.suffix not in (".pyc", ".pyo")
            # Excluded when the `right`-side target owns its own README.md
            # (deagy/cadre#97) -- see main()'s --check handling, which passes
            # compare_readme=False exactly when that's the case. `left` is
            # always a freshly generated, marker-less candidate, so it always
            # has a package-root README.md of its own to filter out here too.
            and (compare_readme or path.relative_to(root) != readme_path)
        }

    left_files = generated_files(left)
    right_files = generated_files(right)
    if left_files != right_files:
        return False
    return all((left / relative).read_bytes() == (right / relative).read_bytes() for relative in left_files)


def main() -> int:
    catalog = load_catalog(AGENTS_ROOT / "catalog.yaml")
    arguments = sys.argv[1:]
    # --output is required rather than defaulting anywhere -- a default would
    # silently create a stray directory. In this repository it is always
    # `--output plugin`.
    if "--output" not in arguments:
        raise SystemExit(
            "cadre generate-plugin: --output is required. The packaged plugin lives in "
            "this repository's plugin/ directory, e.g.\n"
            "    cadre generate-plugin --output plugin"
        )
    output_index = arguments.index("--output")
    try:
        output_root = Path(arguments[output_index + 1]).resolve()
    except IndexError as error:
        raise SystemExit("--output requires a directory") from error
    marker = output_root / ".codex-plugin" / "plugin.json"
    # An existing `.codex-plugin/plugin.json` at the target means it is
    # already an initialized, hand-authored downstream package -- not a
    # fresh distribution target -- so its own README.md is downstream-owned
    # and must survive regeneration untouched (deagy/cadre#97: the earlier
    # non-empty-directory guard below only checked the marker's *presence*,
    # never that its declared identity matched what this generator would
    # produce, so it passed trivially for exactly this case and did nothing
    # to stop a README.md clobber). `--force-readme` is the escape hatch for
    # the one target that legitimately wants the register's own README.md
    # written over an existing marker: the actual `deagy/cadre-plugin`-style
    # distribution checkout, if one is ever re-created.
    force_readme = "--force-readme" in arguments
    preexisting_marker = marker.is_file()
    write_readme = force_readme or not preexisting_marker
    if "--check" not in arguments and output_root.exists():
        if any(output_root.iterdir()) and not preexisting_marker:
            raise SystemExit("--output must be a new directory or an existing generated plugin")
    if "--check" in arguments:
        with tempfile.TemporaryDirectory(prefix="cadre-plugin-") as temporary_directory:
            candidate = Path(temporary_directory) / "cadre"
            generate_package(catalog, candidate)
            if not output_root.exists() or not files_equal(candidate, output_root, compare_readme=write_readme):
                print("Generated plugin is stale or non-deterministic; run cadre generate-plugin", file=sys.stderr)
                return 1
        kernel = agentic_sdlc_contracts._resolve_executable()
        if kernel:
            checked = subprocess.run(
                [kernel, "--provider", str(PROVIDER_ROOT / "provider.json"), "provider", "list"],
                cwd=REPOSITORY_ROOT, check=False, capture_output=True, text=True, encoding="utf-8",
            )
            if checked.returncode != 0:
                print(f"Provider validation failed: {checked.stderr.strip() or checked.stdout.strip()}", file=sys.stderr)
                return 1
        print(f"Generated plugin is current under {output_root}")
        return 0
    written = generate_package(catalog, output_root, write_readme=write_readme)
    print(f"Generated {len(written)} self-contained files under {output_root}")
    if not write_readme:
        print(
            f"README.md left untouched: {marker} already exists, so {output_root} is treated as an "
            "already-initialized downstream package that owns its own README.md. Pass --force-readme "
            "to overwrite it with the register's own template instead.",
            file=sys.stderr,
        )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
