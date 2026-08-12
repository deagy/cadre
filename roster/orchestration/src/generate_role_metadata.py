#!/usr/bin/env python3
"""Regenerate `roster/catalog.yaml` and `roster/orchestration/routing.json`'s
`knowledge_focus` block from role metadata.

This is the generator half of the frontmatter-based role-metadata format
(the parsing/rendering primitives live in `role_metadata.py`). Every role's
`AGENT.md` must start with `---`-delimited frontmatter; every field comes
from that frontmatter, with no fallback to any other source -- a field
missing from frontmatter is a hard error, never silently inherited. An
`AGENT.md` that does not carry frontmatter (see `role_metadata.is_migrated`)
is a generator error, not a supported transitional state: `catalog.yaml` and
`routing.json` are purely generated output now, never an input for role
metadata.

`roster/catalog-order.txt` supplies the dispatch-precedence order both
generated files are built in, and is the source of truth for which role ids
exist at all -- see that file's own header comment.

Every run derives `roster/catalog.yaml` and
`roster/orchestration/routing.json`'s `knowledge_focus` block entirely from
each role's `AGENT.md` frontmatter; re-running this generator after editing
catalog-order.txt or a role's frontmatter is how those two files get updated.

Regenerate after editing catalog-order.txt or a role's frontmatter:

    python3 roster/orchestration/src/generate_role_metadata.py

Validate deterministically without changing the working tree:

    python3 roster/orchestration/src/generate_role_metadata.py --check
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from generate_global_plugin import (  # noqa: E402
    ALLOWED_CODEX_MODELS,
    ALLOWED_MODELS,
    ALLOWED_REASONING_EFFORTS,
    CAPABILITY_PROFILES,
    MODEL_TIERS,
    PROVIDER_ROLES_DIRNAME,
    PROVIDER_ROOT,
    agent_catalog_export_content,
    codex_wrapper_contents,
    load_catalog,
)
from role_metadata import (  # noqa: E402
    is_migrated,
    parse_frontmatter,
    parse_order_file,
)
from routing import load_routing  # noqa: E402

REPOSITORY_ROOT = Path(__file__).resolve().parents[3]
DEFAULT_ROSTER_ROOT = REPOSITORY_ROOT / "roster"
DEFAULT_CATALOG = DEFAULT_ROSTER_ROOT / "catalog.yaml"
DEFAULT_ROUTING = DEFAULT_ROSTER_ROOT / "orchestration" / "routing.json"
DEFAULT_ORDER = DEFAULT_ROSTER_ROOT / "catalog-order.txt"
DEFAULT_HEADER_TEMPLATE = DEFAULT_ROSTER_ROOT / "_catalog_header.yaml.tmpl"

# Closed set observed in today's catalog.yaml -- see that file's `phase:`
# values. Not derived from CAPABILITY_PROFILES/ALLOWED_* because phase is a
# dispatch/reporting label, not a runner capability grant.
ALLOWED_PHASES = frozenset(
    {
        "planning",
        "design",
        "security",
        "build",
        "verify",
        "review",
        "release",
        "operations",
        "support",
        "document",
        "evidence",
        "knowledge",
        "authority",
    }
)

# model / codex_model / reasoning_effort must always agree with this mapping
# (catalog.yaml's own header comment documents the same three tiers). Fails
# closed with no exceptions -- confirmed by the Product Owner. Verified by
# hand against today's full 47-role catalog.yaml before this check was
# introduced: no deviations found (see the Wave 0 implementation report).
#
# Derived from `roster/runner-capabilities.json` (idea #8,
# REQ-CADRE-BACKLOG-8) via generate_global_plugin.py's MODEL_TIERS, not
# hand-duplicated -- see that module's ManifestError docstring for why this
# can never independently drift from the manifest.
TIER_MAP: dict[str, tuple[str, str]] = {
    tier: (data["codex_model"], data["reasoning_effort"]) for tier, data in MODEL_TIERS.items()
}

CATALOG_FIELD_ORDER = ("definition", "phase", "capability", "model", "codex_model", "reasoning_effort")

# Historic hand-authored comment that sits directly above the first
# `phase: authority` role block in catalog.yaml, immediately before the
# `product-owner-aide:` block. It documents authority-aide policy, not any
# one role's metadata, so it does not belong in frontmatter -- it is
# reproduced here verbatim so this generator's rendered catalog.yaml stays
# byte-identical to the hand-authored original. If product-owner-aide is
# ever reordered ahead of another authority role in catalog-order.txt, this
# constant should move to prefix whichever role becomes the first
# `phase: authority` entry, or be turned into prose in a more durable
# location -- it is pinned to a specific id only because that is where it
# already lives today.
ROLE_PREFIX_COMMENTS: dict[str, str] = {
    "product-owner-aide": (
        "  # `phase: authority` roles below prepare the decision package a human\n"
        "  # lifecycle authority needs for their assigned gate(s); they never approve,\n"
        "  # recommend a disposition, or hold delegated authority themselves (see\n"
        "  # docs/proposals/human-authority-role-agents.md). All read_only/opus per the\n"
        "  # design doc's rationale: these support high-blast-radius, hard-to-reverse\n"
        "  # human judgment calls even though the aide itself only assembles evidence.\n"
    ),
}

KNOWLEDGE_FOCUS_ANCHOR = '  "knowledge_focus": {'


class RoleMetadataError(ValueError):
    """Raised for any role-metadata inconsistency; carries a message that
    names the offending role id and/or field and file, per the fail-closed
    contract this generator promises callers.
    """


def _validate_record(role_id: str, record: dict[str, str], source: str) -> None:
    phase = record.get("phase")
    if phase not in ALLOWED_PHASES:
        raise RoleMetadataError(
            f"role {role_id!r} ({source}): phase {phase!r} must be one of: "
            f"{', '.join(sorted(ALLOWED_PHASES))}"
        )
    capability = record.get("capability")
    if capability not in CAPABILITY_PROFILES:
        raise RoleMetadataError(
            f"role {role_id!r} ({source}): capability {capability!r} must be one of: "
            f"{', '.join(sorted(CAPABILITY_PROFILES))}"
        )
    model = record.get("model")
    if model not in ALLOWED_MODELS:
        raise RoleMetadataError(
            f"role {role_id!r} ({source}): model {model!r} must be one of: {', '.join(sorted(ALLOWED_MODELS))}"
        )
    codex_model = record.get("codex_model")
    if codex_model not in ALLOWED_CODEX_MODELS:
        raise RoleMetadataError(
            f"role {role_id!r} ({source}): codex_model {codex_model!r} must be one of: "
            f"{', '.join(sorted(ALLOWED_CODEX_MODELS))}"
        )
    reasoning_effort = record.get("reasoning_effort")
    if reasoning_effort not in ALLOWED_REASONING_EFFORTS:
        raise RoleMetadataError(
            f"role {role_id!r} ({source}): reasoning_effort {reasoning_effort!r} must be one of: "
            f"{', '.join(sorted(ALLOWED_REASONING_EFFORTS))}"
        )
    expected = TIER_MAP.get(model)
    if expected is not None and (codex_model, reasoning_effort) != expected:
        raise RoleMetadataError(
            f"role {role_id!r} ({source}): model {model!r} requires codex_model "
            f"{expected[0]!r} and reasoning_effort {expected[1]!r}, got codex_model "
            f"{codex_model!r} and reasoning_effort {reasoning_effort!r}"
        )
    knowledge_focus = record.get("knowledge_focus")
    if not knowledge_focus:
        raise RoleMetadataError(f"role {role_id!r} ({source}): knowledge_focus must be a non-empty string")


def load_order(order_path: Path) -> list[str]:
    return parse_order_file(order_path.read_text(encoding="utf-8"))


def build_role_model(
    agents_root: Path, order_path: Path
) -> tuple[list[str], dict[str, dict[str, str]]]:
    """Build a single `(order_ids, roles)` result from every discovered
    `AGENT.md`'s frontmatter, `roles` keyed by role id and holding
    `definition`/`phase`/`capability`/`model`/`codex_model`/
    `reasoning_effort`/`knowledge_focus`. Every cross-check is fail-closed
    and names the offending role id.

    Every discovered `AGENT.md` must carry frontmatter (see
    `role_metadata.is_migrated`) -- an unmigrated file is a
    `RoleMetadataError`, not a supported fallback path.
    """
    order_ids = load_order(order_path)
    order_set = set(order_ids)

    discovered: dict[str, str] = {}
    for path in sorted(agents_root.rglob("AGENT.md")):
        relative = path.relative_to(agents_root).as_posix()
        text = path.read_text(encoding="utf-8")
        if not is_migrated(text):
            raise RoleMetadataError(f"{relative}: AGENT.md does not carry '---'-delimited frontmatter")
        discovered[relative] = text

    id_to_path: dict[str, str] = {}
    for relative, text in discovered.items():
        fields, _body = parse_frontmatter(text)  # type: ignore[misc]
        role_id = fields.get("id")
        if not role_id:
            raise RoleMetadataError(f"{relative}: frontmatter is missing required field 'id'")
        if role_id in id_to_path:
            raise RoleMetadataError(
                f"duplicate role id {role_id!r}: {id_to_path[role_id]!r} and {relative!r}"
            )
        id_to_path[role_id] = relative

    missing_files = [role_id for role_id in order_ids if role_id not in id_to_path]
    if missing_files:
        raise RoleMetadataError(
            f"{order_path}: role id(s) with no matching AGENT.md: {', '.join(missing_files)}"
        )
    extra_ids = sorted(set(id_to_path) - order_set)
    if extra_ids:
        raise RoleMetadataError(
            f"AGENT.md discovered for role id(s) not listed in {order_path}: {', '.join(extra_ids)}"
        )

    roles: dict[str, dict[str, str]] = {}
    for role_id in order_ids:
        relative = id_to_path[role_id]
        text = discovered[relative]
        fields, _body = parse_frontmatter(text)  # type: ignore[misc]
        required = ("phase", "capability", "model", "codex_model", "reasoning_effort", "knowledge_focus")
        missing_fields = [field for field in required if field not in fields]
        if missing_fields:
            raise RoleMetadataError(
                f"role {role_id!r} ({relative}): frontmatter is missing required field(s): "
                + ", ".join(missing_fields)
            )
        record = {"definition": relative, **{field: fields[field] for field in required}}
        _validate_record(role_id, record, relative)
        roles[role_id] = record

    return order_ids, roles


def render_catalog(order_ids: list[str], roles: dict[str, dict[str, str]], header_template: str) -> str:
    parts = [header_template]
    for role_id in order_ids:
        parts.append(ROLE_PREFIX_COMMENTS.get(role_id, ""))
        record = roles[role_id]
        lines = [f"  {role_id}:"]
        lines.extend(f"    {field}: {record[field]}" for field in CATALOG_FIELD_ORDER)
        parts.append("\n".join(lines) + "\n")
    return "".join(parts)


def _find_knowledge_focus_block(original_text: str) -> tuple[int, int]:
    occurrences = [match.start() for match in re.finditer(re.escape(KNOWLEDGE_FOCUS_ANCHOR), original_text)]
    if len(occurrences) != 1:
        raise RoleMetadataError(
            f"expected exactly one {KNOWLEDGE_FOCUS_ANCHOR!r} anchor line in routing.json, "
            f"found {len(occurrences)}"
        )
    anchor_start = occurrences[0]
    open_brace_index = original_text.index("{", anchor_start)
    depth = 0
    in_string = False
    escape = False
    index = open_brace_index
    while index < len(original_text):
        character = original_text[index]
        if in_string:
            if escape:
                escape = False
            elif character == "\\":
                escape = True
            elif character == '"':
                in_string = False
        else:
            if character == '"':
                in_string = True
            elif character == "{":
                depth += 1
            elif character == "}":
                depth -= 1
                if depth == 0:
                    return open_brace_index, index
        index += 1
    raise RoleMetadataError("could not find a matching closing '}' for the knowledge_focus block")


def splice_knowledge_focus(original_text: str, order_ids: list[str], roles: dict[str, dict[str, str]]) -> str:
    """Surgically replace only the `"knowledge_focus": { ... }` region of
    `original_text` (routing.json's raw source), leaving every other byte
    untouched.

    Row order within the rebuilt block preserves each already-present role
    id's existing position (so an unchanged role set reproduces the
    original bytes exactly, even though today's routing.json key order does
    not match catalog-order.txt's dispatch-precedence order -- the two
    orders are independent and this generator does not attempt to force
    them to match); any role id newly present in `roles` that was not
    already in the block is appended in catalog-order.txt order.
    """
    open_brace_index, close_brace_index = _find_knowledge_focus_block(original_text)
    original_focus = json.loads(original_text[open_brace_index : close_brace_index + 1])

    ordered_ids = [role_id for role_id in original_focus if role_id in roles]
    ordered_ids.extend(role_id for role_id in order_ids if role_id not in original_focus and role_id in roles)

    body_lines = []
    for position, role_id in enumerate(ordered_ids):
        comma = "," if position < len(ordered_ids) - 1 else ""
        # ensure_ascii=False: today's knowledge_focus prose is all-ASCII,
        # but future non-ASCII prose (e.g. an em dash) should render as the
        # literal character rather than being escaped to \uXXXX.
        value = json.dumps(roles[role_id]["knowledge_focus"], ensure_ascii=False)
        body_lines.append(f"    {json.dumps(role_id, ensure_ascii=False)}: {value}{comma}\n")
    new_block = KNOWLEDGE_FOCUS_ANCHOR + "\n" + "".join(body_lines) + "  }"

    before_region = original_text[: original_text.rindex(KNOWLEDGE_FOCUS_ANCHOR, 0, open_brace_index + 1)]
    after_region = original_text[close_brace_index + 1 :]
    spliced = before_region + new_block + after_region

    before = json.loads(original_text)
    after = json.loads(spliced)
    for key in before:
        if key == "knowledge_focus":
            continue
        if after.get(key) != before.get(key):
            raise RoleMetadataError(f"splice unexpectedly altered routing.json key {key!r}")
    if set(after.get("knowledge_focus", {})) != set(roles):
        raise RoleMetadataError("knowledge_focus id-set mismatch after splice")

    return spliced


def generate(
    agents_root: Path = DEFAULT_ROSTER_ROOT,
    catalog_path: Path = DEFAULT_CATALOG,
    routing_path: Path = DEFAULT_ROUTING,
    order_path: Path = DEFAULT_ORDER,
    header_template_path: Path = DEFAULT_HEADER_TEMPLATE,
    provider_root: Path = PROVIDER_ROOT,
) -> dict[Path, str]:
    order_ids, roles = build_role_model(agents_root, order_path)
    header_template = header_template_path.read_text(encoding="utf-8")
    catalog_content = render_catalog(order_ids, roles, header_template)

    original_routing_text = routing_path.read_text(encoding="utf-8")
    routing_content = splice_knowledge_focus(original_routing_text, order_ids, roles)
    _validate_routing_content(routing_content)

    rendered = {catalog_path: catalog_content, routing_path: routing_content}

    # The generated members of this repository's provider/ bundle (see
    # generate_global_plugin.PROVIDER_ROOT). They derive from the same role
    # metadata as catalog.yaml, so they are rendered here and get this
    # command's --check drift guard for free -- `cadre generate-plugin` only
    # copies provider/ into the package and never regenerates it, which is
    # what lets the pip/pipx distribution ship it without a plugin checkout.
    #
    # Rendered from the freshly computed catalog content rather than the
    # committed catalog.yaml on disk, so a stale catalog can never make these
    # look current.
    # Only for this repository's own roster/ tree: the wrapper bodies embed
    # role definitions and roster/shared/ policy resolved from the real
    # REPOSITORY_ROOT, so rendering them for an arbitrary fixture root (as the
    # generator's own tests use) would reach outside that root and fail.
    if renders_provider_content(agents_root):
        catalog_entries = load_catalog_content(catalog_content)
        rendered[provider_root / "agent-catalog.json"] = agent_catalog_export_content(catalog_entries)
        for filename, content in codex_wrapper_contents(catalog_entries).items():
            rendered[provider_root / "codex-agents" / filename] = content
        # Verbatim role copies the kernel can reach from provider/, so
        # `cadre sdlc init --profile secure-cloud` renders full role content
        # instead of silently degrading to generic instructions. See
        # generate_global_plugin.PROVIDER_ROLES_DIRNAME.
        for metadata in catalog_entries.values():
            definition = metadata["definition"]
            source = agents_root / definition
            rendered[provider_root / PROVIDER_ROLES_DIRNAME / definition] = source.read_text(encoding="utf-8")

    return rendered


def renders_provider_content(agents_root: Path) -> bool:
    """True when generating against this repository's own roster/ tree.

    Resolved on both sides: argparse's `type=Path` does not normalise, so a
    relative (`--agents-root agents`) or symlinked spelling of the very same
    tree would otherwise compare unequal and silently skip every provider
    artifact while still reporting "current".
    """
    return agents_root.resolve() == DEFAULT_ROSTER_ROOT.resolve()


def load_catalog_content(catalog_content: str) -> dict[str, dict[str, str]]:
    """`load_catalog()` over in-memory catalog.yaml text rather than a path."""
    import tempfile

    with tempfile.NamedTemporaryFile("w", suffix=".yaml", delete=False, encoding="utf-8") as handle:
        handle.write(catalog_content)
        temporary_path = Path(handle.name)
    try:
        return load_catalog(temporary_path)
    finally:
        temporary_path.unlink(missing_ok=True)


def _validate_routing_content(text: str) -> None:
    """Validate spliced routing.json content with the real `load_routing()`
    before it is ever written, by round-tripping it through a temporary
    file rather than duplicating `load_routing`'s validation logic.
    """
    import tempfile

    with tempfile.NamedTemporaryFile("w", suffix=".yaml", delete=False, encoding="utf-8") as handle:
        handle.write(text)
        temporary_path = Path(handle.name)
    try:
        load_routing(temporary_path)
    finally:
        temporary_path.unlink(missing_ok=True)


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[2] if __doc__ else None)
    parser.add_argument("--agents-root", type=Path, default=DEFAULT_ROSTER_ROOT)
    parser.add_argument("--catalog", type=Path, default=DEFAULT_CATALOG)
    parser.add_argument("--routing", type=Path, default=DEFAULT_ROUTING)
    parser.add_argument("--order", type=Path, default=DEFAULT_ORDER)
    parser.add_argument("--header-template", type=Path, default=DEFAULT_HEADER_TEMPLATE)
    parser.add_argument("--provider-root", type=Path, default=PROVIDER_ROOT)
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args(argv)

    rendered = generate(args.agents_root, args.catalog, args.routing, args.order, args.header_template, args.provider_root)

    # Orphans: a removed role leaves a stale wrapper that no rendered entry
    # covers, so content comparison alone would call the tree current.
    renders_provider = renders_provider_content(args.agents_root)
    if not renders_provider and args.provider_root != PROVIDER_ROOT:
        raise SystemExit(
            "generate_role_metadata: --provider-root was supplied but provider content is not "
            "rendered for a non-default --agents-root; the flag would be silently ignored"
        )
    orphan_dirs = (
        (args.provider_root / "codex-agents", "*.toml"),
        (args.provider_root / PROVIDER_ROLES_DIRNAME, "**/AGENT.md"),
    )
    orphans = sorted(
        str(path)
        for directory, pattern in orphan_dirs
        for path in (directory.glob(pattern) if renders_provider and directory.is_dir() else [])
        if path not in rendered
    )

    if args.check:
        stale = [
            str(path)
            for path, content in rendered.items()
            if not path.is_file() or path.read_text(encoding="utf-8") != content
        ] + [f"{path} (orphaned)" for path in orphans]
        if stale:
            print(
                "Role metadata derived files are stale; run "
                "roster/orchestration/src/generate_role_metadata.py: " + ", ".join(stale),
                file=sys.stderr,
            )
            return 1
        print(f"{len(rendered)} role metadata files are current")
        return 0

    changed = 0
    for path in orphans:
        Path(path).unlink()
        changed += 1
    for path, content in rendered.items():
        if path.is_file() and path.read_text(encoding="utf-8") == content:
            continue
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content, encoding="utf-8")
        changed += 1
    print(f"Generated {len(rendered)} role metadata file(s) ({changed} changed) under {args.agents_root}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
