#!/usr/bin/env python3
"""Port `agents/*.md` and `skills/*/SKILL.md` into `cline-agents/agents/`
and `cline-agents/skills/` -- the Cline SDK preset/skill formats.

This replaces what was previously a one-time hand port (see git history for
`cline-agents/agents/*.md` and `cline-agents/skills/*.md`). The agents port
was reverse-engineered from that hand port by diffing all 71 files against
their sources: it is a fixed lookup table of literal source-repo-relative
path references rewritten into consumer-neutral prose, applied identically
everywhere, plus a small number of named per-role exceptions where the
original port made a judgment call this script reproduces exactly. The
skills port previously never rewrote any of this at all -- every
`roster/`-relative reference and the (structurally dead, `cline-agents/`
ships no `suite/` directory) "Packaged suite note" callout were carried
over byte-identical. This script fixes that.

Both functions fail loudly (raise SystemExit) if a generated body still
contains a `roster/`-relative or `../`-relative reference the table doesn't
recognize and no exception covers -- a future new role or new shared-policy
file must either match the table or stop this script before regenerate.yml
opens a PR, rather than silently shipping a leaked source-repo path. Extend
PATH_SUBSTITUTIONS (or the per-role/per-skill exception maps) when that
happens; do not loosen the check.

The port target lives in a different top-level directory from the generated
source, so --root and --source are always both required in practice:

    python3 plugin/tools/port_cline_agents.py --root cline-plugins --source plugin
"""

from __future__ import annotations

import argparse
import json
import re
import shutil
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))


def replace_path(src: Path, dst: Path) -> None:
    """Make dst match src exactly (src missing => dst removed).

    Inlined from the former apply_regeneration.py, which existed only to
    apply a cross-repo `cadre generate-plugin` diff into a separate plugin
    checkout. The monorepo merge removed that whole workflow; this one
    helper was its only reusable part.
    """
    if dst.exists():
        if dst.is_dir():
            shutil.rmtree(dst)
        else:
            dst.unlink()
    if src.exists():
        dst.parent.mkdir(parents=True, exist_ok=True)
        if src.is_dir():
            shutil.copytree(src, dst)
        else:
            shutil.copy2(src, dst)

# ---------------------------------------------------------------------------
# Agents
# ---------------------------------------------------------------------------

TOOL_MAP = {
    "Read": "read_files",
    "Grep": "search_codebase",
    "Glob": "search_codebase",
    "Bash": "run_commands",
    "Edit": "editor",
    "Write": "editor",
}
# Presets carry the capability *tier*, never a vendor-qualified model id and
# never a provider. The tier is this repository's own domain knowledge --
# catalog.yaml's header documents the heuristic that assigns it -- whereas
# which provider and which concrete model serve that tier is an operator
# decision. team-profile.yaml records `ai.model_provider: not_yet_selected`,
# so shipping one baked into 74 generated artifacts contradicted this
# repository's own stated position. The runtime resolves tier -> model id
# from operator configuration at dispatch time (see cline-agents/README.md).
#
# Derived from `roster/runner-capabilities.json` at import time rather than
# hand-listed, matching generate_global_plugin.py: that manifest is the single
# source of the tier vocabulary, and with no second copy here the two cannot
# fall out of sync. This became load-bearing when the tier stopped being an
# implementation detail of a model-id mapping table and became the published
# contract a preset carries.
RUNNER_CAPABILITIES_PATH = Path(__file__).resolve().parents[2] / "roster" / "runner-capabilities.json"


def _model_tiers_from_manifest(path: Path) -> dict[str, str]:
    try:
        manifest = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, ValueError) as error:
        raise SystemExit(f"{path}: cannot read the runner-capability manifest: {error}") from error
    tiers = manifest.get("model_tiers")
    if not isinstance(tiers, dict) or not tiers:
        raise SystemExit(f"{path}: 'model_tiers' must be a non-empty object")
    mapping: dict[str, str] = {}
    for tier, data in tiers.items():
        if not isinstance(data, dict) or not data.get("cline_tier"):
            raise SystemExit(f"{path}: model_tiers[{tier!r}] must declare 'cline_tier'")
        mapping[tier] = data["cline_tier"]
    return mapping


# Catalog tier (`opus`/`sonnet`/`haiku`) -> the tier name a Cline preset
# carries (`high`/`mid`/`low`).
#
# The catalog's vocabulary is Anthropic's brand names because the catalog was
# written against Claude Code first. Cline is overwhelmingly driven against
# open-weight and locally hosted models, where `modelTier: opus` names nothing
# the operator has and `export CLINE_AGENTS_MODEL_OPUS=qwen3-coder:30b` is an
# absurd line to ask them to write. The tier *axis* is sound and survives the
# move -- a local rig with a large, a mid and a small model tiers exactly as
# well, and a single-model rig collapses onto CLINE_AGENTS_MODEL_DEFAULT --
# so this renames the labels on the Cline surface only, and leaves the
# catalog, the Claude Code plugin and the Codex wrappers untouched.
#
# Derived from the manifest for the same reason the tier vocabulary already
# was: one edit location, no second copy to drift. `codex_model` set the
# precedent that a runner whose namespace does not share Claude's naming gets
# its own field rather than a reused value.
MODEL_TIERS = _model_tiers_from_manifest(RUNNER_CAPABILITIES_PATH)

# Applied in order, each a plain (non-regex) substring replacement, except
# ROUTING_YAML_RE below which is applied first since the source text spans
# a line break. Longer/more-specific strings are listed before shorter ones
# that could otherwise partially match inside them.
PATH_SUBSTITUTIONS: list[tuple[str, str]] = [
    (
        "files under roster/shared/ are embedded verbatim",
        "these shared-policy defaults are embedded verbatim",
    ),
    ("`../../review/halt-authority/AGENT.md`", "the halt-authority role definition"),
    (
        "`../../review/classification-and-marking-gate/AGENT.md`",
        "the classification-and-marking-gate role definition",
    ),
    (
        "`../../shared/output-schemas/finding.schema.json`",
        "this project's finding output schema",
    ),
    ("`../../shared/agent-autonomy.yaml`", "this project's agent-autonomy policy documentation"),
    ("`../../shared/team-profile.yaml`", "this project's team-profile documentation"),
    ("`../../shared/technology-standards.md`", "this project's technology-standards documentation"),
    ("`../../shared/library-standards.yaml`", "this project's library-standards documentation"),
    (
        "`../../shared/secure-development-policy.md`",
        "this project's secure-development-policy documentation",
    ),
    ("`../../shared/operating-principles.md`", "this project's operating-principles documentation"),
    ("`../../shared/knowledge-use-policy.md`", "this project's knowledge-use-policy documentation"),
    ("`../../shared/cloud-guardrails.md`", "this project's cloud-guardrails documentation"),
    ("`../../orchestration/escalation-policy.md`", "this project's escalation-policy documentation"),
    ("`../../orchestration/handoff-contracts.md`", "this project's handoff-contracts documentation"),
    # Single-`../`-nesting variants (knowledge-store-steward.md sits one
    # directory shallower than the rest of roster/<phase>/<role>/AGENT.md).
    ("`../shared/agent-autonomy.yaml`", "this project's agent-autonomy policy documentation"),
    ("`../shared/team-profile.yaml`", "this project's team-profile documentation"),
    ("`../shared/technology-standards.md`", "this project's technology-standards documentation"),
    ("`../shared/operating-principles.md`", "this project's operating-principles documentation"),
    ("`../shared/knowledge-use-policy.md`", "this project's knowledge-use-policy documentation"),
    ("`../documentation/evidence-curator/AGENT.md`", "this project's evidence-curator role definition"),
    ("`SECURITY.md`", "this project's security documentation"),
    ("`roster/knowledge-store/README.md`", "this project's knowledge-store documentation"),
    ("`../../knowledge-store/AGENT.md`", "this project's knowledge-store-steward role definition"),
    (
        "`roster/knowledge-store/proposed-knowledge.schema.json`",
        "this project's staged-knowledge-record schema",
    ),
    ("`roster/knowledge-store/proposed-knowledge/`", "this project's staged-knowledge-record directory"),
    ("`proposed-knowledge.schema.json`", "this project's staged-knowledge-record schema"),
    ("`proposed-knowledge/`", "this project's staged-knowledge-record directory"),
    ("`roster/shared/`", "this project's shared-policy directory"),
    ("`roster/shared/README.md`", "this project's shared-policy documentation"),
    ("roster/shared/README.md", "this project's shared-policy documentation"),
    # roster/shared/workspace-isolation.md is embedded verbatim into every
    # wrapper as tier-scoped shared policy, and cites this manifest by path.
    # The rule was missing because the committed cline-agents/ snapshot
    # predated that policy file: with the distribution living in a separate
    # repository, nothing re-ported it until the monorepo merge made every
    # test build from source.
    (
        "`roster/runner-capabilities.json`",
        "this project's runner-capability manifest",
    ),
    (
        "`roster/knowledge-store/src/config.py`",
        "this project's knowledge-store configuration module",
    ),
    (
        "`roster/knowledge-store/SECURITY.md`",
        "this project's knowledge-store security documentation",
    ),
    ("`roster/RUNBOOK.md`", "this project's operating runbook"),
    ("roster/RUNBOOK.md", "this project's operating runbook"),
]
ROUTING_YAML_RE = re.compile(r"roster/orchestration/\s+routing\.yaml's")
ROUTING_YAML_REPLACEMENT = "this project's routing configuration's"
SHARED_POLICY_HEADER_RE = re.compile(r"^# Shared policy: roster/shared/", re.MULTILINE)
SHARED_POLICY_HEADER_REPLACEMENT = "# Shared policy: "
ROLE_HEADING_RE = re.compile(r"\A\n# Role: [a-z0-9-]+\n\n")

# Per-role exact-substring overrides, applied before the generic table (so
# they don't get partially consumed by a broader rule). Each is a genuine,
# one-off hand judgment call from the original port -- see
# cline-agents/README.md's "Path-reference rewrites" section.
ROLE_OVERRIDES: dict[str, list[tuple[str, str]]] = {
    "knowledge-store-steward": [
        (
            "by default (`SECURITY.md`), so also verify",
            "by default (see this project's security documentation for the exact "
            "default-resolution behavior), so also verify",
        ),
    ],
    "debugging-engineer": [
        (
            "When inspecting agents, verify `AGENT.md` authority, catalog registration, "
            "routing rules, knowledge focus, workflow alignment, selector tests, and runbook "
            "examples.",
            "When inspecting agents, verify the agent definition's authority, catalog/registry "
            "registration, routing rules, knowledge focus, workflow alignment, selector tests, "
            "and usage/runbook examples.",
        ),
    ],
}

# application-engineer's whole purpose is maintaining this suite's own
# tooling -- `roster/catalog.yaml`, `roster/orchestration/routing.yaml`,
# `roster/RUNBOOK.md`, `AGENT.md`/`AGENTS.md`, etc. are the literal subject
# of the role body (outside the shared-policy boilerplate block, which
# still gets the normal table applied), not incidental cross-references.
# Exempt from the fail-loud safety net; the port note below documents why.
APPLICATION_ENGINEER_PORT_NOTE = (
    "\n\n---\n\n"
    "_Port note (not part of the original role authority text): "
    "application-engineer's role text describes maintaining THIS "
    "deagy/cadre monorepo's own tooling (roster/catalog.yaml, "
    "roster/orchestration/routing.yaml, roster/RUNBOOK.md, the packaged-plugin "
    "regeneration flow via `cadre generate-plugin`/`cadre generate-role-metadata`, "
    "plugin/). Those are the literal subject of the role, not "
    "incidental cross-references, so they were left unrewritten; this preset is "
    "only meaningful when dispatched against a checkout of the "
    "deagy/cadre register repository itself, not an arbitrary "
    "consumer project._"
)
FAIL_LOUD_EXEMPT_ROLES = {"application-engineer"}

# Includes `<`/`>` so templated placeholders like `roster/<phase>/<role>/
# AGENT.md` are caught too -- a prior version of this regex excluded them
# and both this file's own SKILL_PATH_SUBSTITUTIONS and pre-existing
# committed content had such a placeholder slip through undetected.
LEAK_RE = re.compile(r"roster/[a-zA-Z0-9_/.<>-]+|(?<![\w.])\.\./[a-zA-Z0-9_./<>-]+")


def _parse_frontmatter(text: str) -> tuple[dict[str, str], str]:
    match = re.match(r"\A---\n(.*?)\n---\n(.*)\Z", text, re.DOTALL)
    if not match:
        raise SystemExit(f"Could not parse frontmatter from:\n{text[:200]!r}")
    fields: dict[str, str] = {}
    for line in match.group(1).splitlines():
        key, _, value = line.partition(":")
        fields[key.strip()] = value.strip()
    return fields, match.group(2)


def _convert_agent_body(role: str, body: str) -> str:
    body = ROLE_HEADING_RE.sub("\n", body, count=1)
    for source, target in ROLE_OVERRIDES.get(role, []):
        if source not in body:
            raise SystemExit(f"{role}: expected override text not found: {source!r}")
        body = body.replace(source, target, 1)
    body = ROUTING_YAML_RE.sub(ROUTING_YAML_REPLACEMENT, body)
    # The full table applies uniformly to every role, including
    # application-engineer -- its shared-policy boilerplate block gets
    # rewritten exactly like every other role's. Only its own substantive
    # roster/-relative references elsewhere in the body (the literal
    # subject of that role) are left alone, and only the fail-loud leak
    # check downstream is exempted for it (see FAIL_LOUD_EXEMPT_ROLES).
    for source, target in PATH_SUBSTITUTIONS:
        body = body.replace(source, target)
    body = SHARED_POLICY_HEADER_RE.sub(SHARED_POLICY_HEADER_REPLACEMENT, body)
    if role == "application-engineer":
        body = body.rstrip("\n") + APPLICATION_ENGINEER_PORT_NOTE + "\n"
    return body


def _check_no_leaks(role: str, body: str) -> None:
    if role in FAIL_LOUD_EXEMPT_ROLES:
        return
    for match in LEAK_RE.finditer(body):
        raise SystemExit(
            f"{role}: unrecognized source-repo-relative reference left in ported body: "
            f"{match.group(0)!r}. Add a rule to PATH_SUBSTITUTIONS (or a role-specific "
            f"override) in tools/port_cline_agents.py, don't ship a leaked path."
        )


def _convert_agent_file(source_path: Path, role: str) -> str:
    fields, body = _parse_frontmatter(source_path.read_text(encoding="utf-8"))
    tools = [t.strip() for t in fields.get("tools", "").split(",") if t.strip()]
    allowed_tools = list(dict.fromkeys(TOOL_MAP[t] for t in tools))
    model = fields.get("model")
    if model not in MODEL_TIERS:
        raise SystemExit(f"{role}: unknown model tier {model!r}")

    body = _convert_agent_body(role, body)
    _check_no_leaks(role, body)

    frontmatter_lines = [
        "---",
        f"name: {fields['name']}",
        f'description: "{fields["description"]}"',
        f"modelTier: {MODEL_TIERS[model]}",
        f"allowedTools: [{', '.join(allowed_tools)}]",
        f"canonicalSource: {fields['canonical_source']}",
        f"convertedFrom: agents/{role}.md",
        "---",
        "",
    ]
    return "\n".join(frontmatter_lines) + body


def port_agents(root: Path, source_root: Path | None = None) -> list[str]:
    source_dir = (source_root or root) / "agents"
    target_dir = root / "cline-agents" / "agents"
    ported: list[str] = []
    for source_path in sorted(source_dir.glob("*.md")):
        role = source_path.stem
        content = _convert_agent_file(source_path, role)
        (target_dir / f"{role}.md").parent.mkdir(parents=True, exist_ok=True)
        (target_dir / f"{role}.md").write_text(content, encoding="utf-8")
        ported.append(role)
    return ported


# ---------------------------------------------------------------------------
# Skills
# ---------------------------------------------------------------------------

# Same idea as PATH_SUBSTITUTIONS above, but for skills/*/SKILL.md content,
# which references this suite's own CLI/data files (not the shared-policy
# doc set agents reference) -- authored fresh for this port, since the
# previous skills port never rewrote any of this.
SKILL_PATH_SUBSTITUTIONS: list[tuple[str, str]] = [
    # Same late-arriving shared policy as PATH_SUBSTITUTIONS'
    # workspace-isolation entry: skills cite it too.
    (
        "`roster/shared/workspace-isolation.md`",
        "this project's workspace-isolation policy documentation",
    ),
    (
        "roster/shared/workspace-isolation.md",
        "this project's workspace-isolation policy documentation",
    ),
    # Longest/most specific multi-line phrases must come first -- a later,
    # shorter rule (e.g. plain `roster/catalog.yaml`) would otherwise
    # partially consume this one's interior text before it gets a chance
    # to match as a whole.
    (
        "use the project-local suite when it\ncontains `roster/catalog.yaml`; otherwise use the self-contained suite under\n`../../suite/roster/` relative to this packaged skill",
        "this is entirely handled for you: this plugin's tools resolve the bundled role catalog on "
        "their own, with no config step needed before first dispatch",
    ),
    ("`roster/catalog-order.txt`", "the bundled role catalog's ordering file"),
    ("roster/catalog-order.txt", "the bundled role catalog's ordering file"),
    # Must precede the generic `roster/catalog.yaml` rule below: "the
    # current" is already a determiner, so the generic "this repository's
    # ..." phrase would otherwise double up ("the current this
    # repository's...").
    ("the current `roster/catalog.yaml`", "the current bundled role catalog"),
    ("`roster/catalog.yaml`", "this repository's bundled role catalog"),
    ("roster/catalog.yaml", "this repository's bundled role catalog"),
    (
        "`roster/orchestration/routing.yaml`",
        "this repository's bundled routing configuration",
    ),
    ("roster/orchestration/routing.yaml", "this repository's bundled routing configuration"),
    (
        "`roster/orchestration/src/select_agents.py`",
        "the bundled selector implementation",
    ),
    (
        "roster/orchestration/src/select_agents.py",
        "the bundled selector implementation",
    ),
    ("`roster/orchestration/src/build_dispatch_plan.py`", "the bundled dispatch-plan builder"),
    ("roster/orchestration/src/build_dispatch_plan.py", "the bundled dispatch-plan builder"),
    ("`roster/orchestration/escalation-policy.md`", "this repository's escalation-policy documentation"),
    ("`roster/orchestration/handoff-contracts.md`", "this repository's handoff-contracts documentation"),
    ("`roster/RUNBOOK.md`", "this repository's runbook"),
    ("roster/RUNBOOK.md", "this repository's runbook"),
    ("`roster/shared/`", "this project's shared-policy directory"),
    ("`roster/shared/README.md`", "this project's shared-policy documentation"),
    ("roster/shared/README.md", "this project's shared-policy documentation"),
    ("`roster/knowledge-store/README.md`", "this project's knowledge-store documentation"),
    ("`../../knowledge-store/AGENT.md`", "this project's knowledge-store-steward role definition"),
    (
        "`roster/knowledge-store/proposed-knowledge.schema.json`",
        "this project's staged-knowledge-record schema",
    ),
    ("`roster/knowledge-store/proposed-knowledge/`", "this project's staged-knowledge-record directory"),
    ("`proposed-knowledge.schema.json`", "this project's staged-knowledge-record schema"),
    ("`proposed-knowledge/`", "this project's staged-knowledge-record directory"),
    ("`roster/knowledge-store/SECURITY.md`", "this project's knowledge-store security documentation"),
    ("roster/knowledge-store/SECURITY.md", "this project's knowledge-store security documentation"),
    (
        "`roster/orchestration/mcp/dispatch_core.py`",
        "the bundled MCP dispatch server implementation",
    ),
    ("[`run-agent-orchestration`](../run-agent-orchestration/SKILL.md)", "the `run-agent-orchestration` skill"),
    ("`../run-agent-orchestration/SKILL.md`", "the `run-agent-orchestration` skill"),
    ("../run-agent-orchestration/SKILL.md", "the `run-agent-orchestration` skill"),
    ("`roster/engineering/backend-engineer/AGENT.md`", "its own role-definition file"),
    ("`roster/<domain>/<agent-name>/AGENT.md`", "its own role-definition file"),
    ("`roster/<phase>/<role>/AGENT.md`", "its own role-definition file"),
    ("`roster/knowledge-store/src/config.py`", "the bundled knowledge-store config-resolution logic"),
    ("roster/knowledge-store/src/config.py", "the bundled knowledge-store config-resolution logic"),
    # A literal runnable shell command, not descriptive prose -- rewriting
    # just the path argument would leave a command this plugin can't
    # actually run (it ships no roster/knowledge-store/test directory).
    # Replace the whole instruction instead of the bare path.
    (
        'Run the knowledge-store tests before ingestion: `python3 -m unittest discover -s '
        'roster/knowledge-store/test -p "test_*.py"`.',
        "Run the knowledge-store tests before ingestion if working from a checkout of the source "
        "register (this bundled plugin does not ship that test suite itself).",
    ),
    ("`roster/knowledge-store/test`", "the bundled knowledge-store test suite"),
    ("roster/knowledge-store/test", "the bundled knowledge-store test suite"),
    # Promoted out of `mcp/` in the #234 follow-up: the register now covers
    # every dispatch path, not only the MCP servers, so the replacement prose
    # can no longer call it "the MCP dispatch server's" documentation.
    (
        "`roster/orchestration/SECURITY-CONTROLS.md`",
        "the bundled security-controls register",
    ),
    (
        "roster/orchestration/SECURITY-CONTROLS.md",
        "the bundled security-controls register",
    ),
    # Two literal runnable shell commands (Codex CLI MCP-server setup
    # instructions), not descriptive prose -- these describe a register
    # checkout's own source layout that this bundled plugin doesn't ship,
    # so rewriting just the path argument would leave a broken,
    # non-runnable command. Replace the whole instruction, same treatment
    # as the knowledge-store-test case above; must come before the generic
    # bare-path rules below since those are shorter and would otherwise
    # partially consume these first.
    (
        "1. `pip install -r roster/orchestration/mcp/requirements-mcp.txt` (installs\n"
        "       the official `mcp` SDK; stdio transport only — do not add a networked\n"
        "       extra).",
        "1. Install the official `mcp` SDK (stdio transport only — do not add a networked extra) "
        "if working from a checkout of the source register (this bundled plugin does not ship "
        "the MCP dispatch server's own dependency pin file).",
    ),
    (
        "directly at `python3 <repo>/roster/orchestration/mcp/dispatch_server.py`",
        "directly at the bundled MCP dispatch server implementation, if working from a checkout "
        "of the source register (this bundled plugin does not ship that server as a standalone "
        "script)",
    ),
    ("`roster/orchestration/mcp/dispatch_server.py`", "the bundled MCP dispatch server implementation"),
    ("roster/orchestration/mcp/dispatch_server.py", "the bundled MCP dispatch server implementation"),
    (
        "`roster/orchestration/mcp/requirements-mcp.txt`",
        "the bundled MCP dispatch server's dependency pin file",
    ),
    (
        "roster/orchestration/mcp/requirements-mcp.txt",
        "the bundled MCP dispatch server's dependency pin file",
    ),
    (
        "`roster/orchestration/runs/<task-id>/`",
        "this repository's local run-artifact directory, under a `<task-id>/` subdirectory,",
    ),
    ("`roster/orchestration/runs/`", "this repository's local run-artifact directory"),
    ("roster/orchestration/runs/", "this repository's local run-artifact directory"),
    (
        "`roster/orchestration/src/generate_role_metadata.py`",
        "the bundled role-metadata generator",
    ),
    ("roster/orchestration/src/generate_role_metadata.py", "the bundled role-metadata generator"),
    ("`roster/orchestration/src/role_metadata.py`", "the bundled role-metadata module"),
    ("roster/orchestration/src/role_metadata.py", "the bundled role-metadata module"),
    ("`roster/orchestration/test/test_selector.py`", "the bundled selector's own test suite"),
    ("roster/orchestration/test/test_selector.py", "the bundled selector's own test suite"),
    (
        "`roster/orchestration/test/fixtures/selection_golden_corpus.json`",
        "the bundled selector's golden-corpus fixtures",
    ),
    (
        "`roster/orchestration/test/test_repository_health.py`",
        "the bundled repository-health test suite",
    ),
    ("`roster/orchestration/test/test_role_metadata.py`", "the bundled role-metadata test suite"),
    ("`test_routing_coverage.py`", "the bundled routing-coverage test suite"),
    ("`plugin/tools/test_port_cline_agents.py`", "the bundled Cline port's own test suite"),
    ("`roster/runner-capabilities.json`", "the bundled runner-capabilities manifest"),
    ("roster/runner-capabilities.json", "the bundled runner-capabilities manifest"),
    ("`roster/runner-capabilities.schema.json`", "the bundled runner-capabilities manifest's schema"),
    ("`roster/shared/knowledge-use-policy.md`", "this project's knowledge-use-policy documentation"),
    ("roster/shared/knowledge-use-policy.md", "this project's knowledge-use-policy documentation"),
    ("`roster/shared/operating-principles.md`", "this project's operating-principles documentation"),
    ("roster/shared/operating-principles.md", "this project's operating-principles documentation"),
    ("`roster/shared/technology-standards.md`", "this project's technology-standards documentation"),
    ("roster/shared/technology-standards.md", "this project's technology-standards documentation"),
    ("`roster/workflows/debugging.md`", "this repository's debugging workflow doc"),
    ("roster/workflows/debugging.md", "this repository's debugging workflow doc"),
    ("`roster/workflows/knowledge-ingestion.md`", "this repository's knowledge-ingestion workflow doc"),
    ("roster/workflows/knowledge-ingestion.md", "this repository's knowledge-ingestion workflow doc"),
    ("`roster/workflows/`", "this repository's worked-example workflow docs"),
    ("roster/workflows/", "this repository's worked-example workflow docs"),
]
PACKAGED_SUITE_NOTE_RE = re.compile(
    r"^> Packaged suite note: .*?do not look for the source checkout\.\n\n?",
    re.MULTILINE,
)
CLINE_PACKAGING_NOTE = (
    "> Cline packaging note: this skill's instructions describe this repository's own "
    "`roster/`-layout tooling in the abstract (the role catalog, routing configuration, and "
    "selector this plugin bundles) -- they are not literal paths to look up in an arbitrary "
    "target project. When dispatching, use `start_subagent`/`dispatch_selected_roles`/`bin/cadre "
    "select` rather than reading these files directly.\n\n"
)
# Matches both `[references/X.md](references/X.md)` and the bare sibling
# form `[X.md](X.md)` -- both are self-links where the reference target,
# once inlined, lives in the same document as a `# Reference: X.md`
# section rather than a separate file.
INLINE_REFERENCE_LINK_RE = re.compile(
    r"\[(?:references/)?([a-zA-Z0-9_-]+\.md)\]\((?:references/)?\1\)"
)


def _fix_inline_reference_links(text: str) -> str:
    def _replace(match: re.Match[str]) -> str:
        filename = match.group(1)
        return f'the "Reference: {filename}" section below'

    return INLINE_REFERENCE_LINK_RE.sub(_replace, text)


def _convert_skill_body(name: str, body: str) -> str:
    if PACKAGED_SUITE_NOTE_RE.search(body) is None:
        raise SystemExit(f"{name}: expected 'Packaged suite note' callout not found")
    body = PACKAGED_SUITE_NOTE_RE.sub(CLINE_PACKAGING_NOTE, body, count=1)
    body = _fix_inline_reference_links(body)
    for source, target in SKILL_PATH_SUBSTITUTIONS:
        body = body.replace(source, target)
    return body


# Literal substrings that match LEAK_RE's shape but are NOT references to
# this repository's own roster/ tree -- they name an external system's own
# convention (Cline's in-progress native "agent profiles" feature happens
# to also use the path segment "roster", coincidentally/by-design mirroring
# this project's terminology for an unrelated, not-yet-shipped Cline
# feature). Rewriting these would be actively misleading, not a fix.
SKILL_LEAK_ALLOWLIST = {".cline/roster/*.yml", "~/.cline/roster/", "cline-roster/"}


def _check_no_skill_leaks(name: str, body: str) -> None:
    for match in LEAK_RE.finditer(body):
        if any(allowed in match.group(0) or match.group(0) in allowed for allowed in SKILL_LEAK_ALLOWLIST):
            continue
        raise SystemExit(
            f"{name}: unrecognized source-repo-relative reference left in ported skill: "
            f"{match.group(0)!r}. Add a rule to SKILL_PATH_SUBSTITUTIONS in "
            f"tools/port_cline_agents.py, don't ship a leaked path."
        )


def port_skills(root: Path, source_root: Path | None = None) -> list[str]:
    source_dir = (source_root or root) / "skills"
    target_dir = root / "cline-agents" / "skills"
    ported: list[str] = []
    for skill_dir in sorted(p for p in source_dir.iterdir() if p.is_dir()):
        name = skill_dir.name
        skill_md = skill_dir / "SKILL.md"
        if not skill_md.is_file():
            continue
        fields, body = _parse_frontmatter(skill_md.read_text(encoding="utf-8"))
        body = _convert_skill_body(name, body.strip())

        references_dir = skill_dir / "references"
        if references_dir.is_dir():
            for ref_file in sorted(references_dir.glob("*.md")):
                ref_body = _fix_inline_reference_links(ref_file.read_text(encoding="utf-8").strip())
                for source, target in SKILL_PATH_SUBSTITUTIONS:
                    ref_body = ref_body.replace(source, target)
                body += f"\n\n# Reference: {ref_file.name}\n\n{ref_body}"

        _check_no_skill_leaks(name, body)

        frontmatter_lines = [
            "---",
            f"name: {name}",
            f"description: {fields['description']}",
            f"canonicalSource: skills/{name}/SKILL.md",
            "---",
            "",
        ]
        content = "\n".join(frontmatter_lines) + "\n" + body + "\n"
        (target_dir / f"{name}.md").parent.mkdir(parents=True, exist_ok=True)
        replace_path(skill_md, target_dir / f"{name}.md")  # ensure clean slate
        (target_dir / f"{name}.md").write_text(content, encoding="utf-8")
        ported.append(name)

    # Remove ports of skills that are no longer sourced here. Without this the
    # port only ever adds: a skill that moves to a sub-plugin (or is renamed or
    # deleted) leaves its old copy behind forever, and cline-agents keeps
    # advertising a skill nothing generates. That is exactly how
    # cadre-install-kernel.md ended up stranded here after being routed to the
    # lifecycle plugins.
    if target_dir.is_dir():
        keep = {f"{name}.md" for name in ported}
        for stale in sorted(target_dir.glob("*.md")):
            if stale.name not in keep:
                stale.unlink()

    return ported


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--root", type=Path, default=Path("cline-plugins"),
        help="Directory containing cline-agents/ (the port target).",
    )
    parser.add_argument(
        "--source", type=Path, default=None,
        help=(
            "Directory containing the generated agents/ and skills/ to port from. "
            "Defaults to --root. Since the monorepo merge these are build "
            "artifacts rather than committed files, so this normally points at a "
            "`cadre generate-plugin` output directory."
        ),
    )
    args = parser.parse_args(argv)
    root = args.root.resolve()
    source_root = args.source.resolve() if args.source else None

    agents = port_agents(root, source_root)
    skills = port_skills(root, source_root)
    print(f"Ported {len(agents)} agent(s) and {len(skills)} skill(s) into cline-agents/.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
