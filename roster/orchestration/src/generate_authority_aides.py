#!/usr/bin/env python3
"""Regenerate the authority-aide AGENT.md files from a shared template.

The eight `roster/authority/*-aide/AGENT.md` role definitions differ only in
the human-authority title and the Agentic SDLC gate number(s) they prepare a
decision package for. Everything else — Inputs, Outputs, Required checks,
Escalate when, Completion criteria — is identical policy prose shared by the
whole family. Rather than hand-maintain eight near-duplicate files, this
script renders them from roster/authority/aides.yaml (the per-role data) and
roster/authority/_template.md.tmpl (the shared prose), the same
generate-then-check pattern used for the packaged plugin
(roster/orchestration/src/generate_global_plugin.py).

Regenerate after editing aides.yaml or the template:

    python3 roster/orchestration/src/generate_authority_aides.py

Validate deterministically without changing the working tree:

    python3 roster/orchestration/src/generate_authority_aides.py --check
"""

from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from agentic_sdlc_contracts import try_lifecycle_contract  # noqa: E402
from generate_global_plugin import GENERATED_MARKER  # noqa: E402
from role_metadata import emit_scalar, frontmatter_closing_delimiter_end  # noqa: E402
from routing import parse_keyed_entries  # noqa: E402

REPOSITORY_ROOT = Path(__file__).resolve().parents[3]
AUTHORITY_ROOT = REPOSITORY_ROOT / "roster" / "authority"
DATA_PATH = AUTHORITY_ROOT / "aides.yaml"
TEMPLATE_PATH = AUTHORITY_ROOT / "_template.md.tmpl"
REQUIRED_FIELDS = ("title", "gates", "knowledge_focus")


def _strip_inline_comment(value: str) -> str:
    # A '#' only starts a comment when preceded by whitespace (or at the start
    # of the line) so values that legitimately contain '#' (e.g. "C# Lead")
    # are left intact.
    return re.sub(r"(?:^|\s)#.*$", "", value).strip()


def _parse_gates(path: Path, aide_id: str, raw: str) -> list[int]:
    value = _strip_inline_comment(raw)
    if not (value.startswith("[") and value.endswith("]")):
        raise ValueError(
            f"{path}: aide {aide_id!r} gates must be a flow-style list like '[1, 2]', got {value!r}"
        )
    raw_items = [part.strip() for part in value[1:-1].split(",") if part.strip()]
    if not raw_items:
        raise ValueError(f"{path}: aide {aide_id!r} has an empty gates list")
    try:
        gates = [int(part) for part in raw_items]
    except ValueError as error:
        raise ValueError(f"{path}: aide {aide_id!r} has a non-integer gate in {value!r}") from error
    duplicates = sorted({gate for gate in gates if gates.count(gate) > 1})
    if duplicates:
        raise ValueError(
            f"{path}: aide {aide_id!r} has duplicate gate(s) in {value!r}: "
            + ", ".join(str(gate) for gate in duplicates)
        )
    return gates


def load_aides(path: Path) -> list[dict[str, object]]:
    """Parse aides.yaml's `<id>: {title, gates}` blocks.

    Reuses routing.py's parse_keyed_entries(), the same 2-space-id /
    4-space-field parser catalog.yaml is read with, so this table and
    catalog.yaml are never parsed by two independently hand-rolled
    implementations of the same shape. Field order within an entry does not
    matter.
    """
    raw_entries = parse_keyed_entries(path.read_text(encoding="utf-8"), REQUIRED_FIELDS)
    aides: list[dict[str, object]] = []
    for aide_id, fields in raw_entries.items():
        missing = [field for field in REQUIRED_FIELDS if field not in fields]
        if missing:
            raise ValueError(f"{path}: aide {aide_id!r} is missing required field(s): {', '.join(missing)}")
        aides.append(
            {
                "id": aide_id,
                "title": _strip_inline_comment(fields["title"]),
                "gates": _parse_gates(path, aide_id, fields["gates"]),
                "knowledge_focus": _strip_inline_comment(fields["knowledge_focus"]),
            }
        )
    return aides


def validate_gates_against_kernel_contract(aides: list[dict[str, object]]) -> None:
    """aides.yaml hardcodes each aide's gate number(s) independently of the
    Agentic SDLC kernel that owns gate numbering permanently. When the
    kernel is reachable, cross-check every gate here against its live
    lifecycle-gates contract, so a kernel-side gate renumber or removal is
    caught at generation time instead of silently shipping an authority
    aide for a gate that no longer exists. No-ops in standalone mode
    (kernel not installed/configured), matching every other lifecycle-aware
    code path in this suite -- see build_dispatch_plan.py's
    _lifecycle_gates(), which degrades the same way.
    """
    contract = try_lifecycle_contract()
    if contract is None:
        return
    known_gate_ids = {gate["id"] for gate in contract["gates"]}
    for aide in aides:
        gates = aide["gates"]
        assert isinstance(gates, list)
        for gate in gates:
            gate_id = f"G{gate}"
            if gate_id not in known_gate_ids:
                raise ValueError(
                    f"{DATA_PATH}: aide {aide['id']!r} references {gate_id}, which is not in "
                    f"the Agentic SDLC kernel's live lifecycle-gates contract ({sorted(known_gate_ids)})"
                )


def gate_phrase(gates: list[int]) -> str:
    labels = [f"G{gate}" for gate in gates]
    if len(labels) == 1:
        return f"gate {labels[0]}"
    if len(labels) == 2:
        return f"gates {labels[0]} and {labels[1]}"
    return f"gates {', '.join(labels[:-1])}, and {labels[-1]}"


def gate_list(gates: list[int]) -> str:
    return ", ".join(f"G{gate}" for gate in gates)


def render(template: str, aide: dict[str, object]) -> str:
    gates = aide["gates"]
    assert isinstance(gates, list)
    try:
        rendered = template.format(
            id=emit_scalar(str(aide["id"])),
            knowledge_focus=emit_scalar(str(aide["knowledge_focus"])),
            title=aide["title"],
            gate_phrase=gate_phrase(gates),
            gate_list=gate_list(gates),
        )
    except (KeyError, IndexError) as error:
        raise ValueError(
            f"{TEMPLATE_PATH}: failed to render aide {aide['id']!r}: {error}"
        ) from error
    # The rendered template starts with `---`-delimited frontmatter (see
    # role_metadata.py); insert the generated-file marker after the closing
    # delimiter rather than at byte 0, mirroring
    # generate_global_plugin.py's identical placement for packaged copies of
    # migrated AGENT.md files.
    frontmatter_end = frontmatter_closing_delimiter_end(rendered)
    assert frontmatter_end is not None
    return rendered[:frontmatter_end] + f"\n\n{GENERATED_MARKER}" + rendered[frontmatter_end:]


def generate(aides: list[dict[str, object]], template: str) -> dict[Path, str]:
    return {
        AUTHORITY_ROOT / str(aide["id"]) / "AGENT.md": render(template, aide)
        for aide in aides
    }


def existing_generated_files() -> set[Path]:
    return set(AUTHORITY_ROOT.glob("*/AGENT.md"))


def build_parser() -> argparse.ArgumentParser:
    """Parse argv properly rather than scanning it for `--check`.

    The scan this replaces treated *every* argv it did not recognise as "no
    flags given", which routed it to the write path: `--help` regenerated the
    eight files instead of printing help, and -- the reason this matters
    beyond tidiness -- so did a typo. A CI step running `--chek` would
    silently rewrite the tree and exit 0, reporting success while masking
    exactly the drift the check exists to catch. argparse rejects an unknown
    flag instead of guessing.
    """
    parser = argparse.ArgumentParser(
        prog="cadre generate-authority-aides",
        description="Regenerate roster/authority/*-aide/AGENT.md from aides.yaml and the shared template.",
        allow_abbrev=False,
    )
    parser.add_argument(
        "--check",
        action="store_true",
        help="Report whether the generated files are current without writing anything (exit 1 if stale).",
    )
    return parser


def main() -> int:
    args = build_parser().parse_args()

    aides = load_aides(DATA_PATH)
    validate_gates_against_kernel_contract(aides)
    template = TEMPLATE_PATH.read_text(encoding="utf-8")
    rendered = generate(aides, template)
    orphaned = existing_generated_files() - set(rendered)

    if args.check:
        stale = [
            str(path.relative_to(REPOSITORY_ROOT))
            for path, content in rendered.items()
            if not path.is_file() or path.read_text(encoding="utf-8") != content
        ]
        stale.extend(str(path.relative_to(REPOSITORY_ROOT)) for path in orphaned)
        if stale:
            print(
                "Authority-aide AGENT.md files are stale; run "
                "roster/orchestration/src/generate_authority_aides.py: " + ", ".join(sorted(stale)),
                file=sys.stderr,
            )
            return 1
        print(f"{len(rendered)} authority-aide AGENT.md files are current")
        return 0

    for path in orphaned:
        path.unlink()
        try:
            path.parent.rmdir()
        except OSError as error:
            print(f"cadre: could not remove {path.parent}: {error}", file=sys.stderr)
    for path, content in rendered.items():
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content, encoding="utf-8")
    print(f"Generated {len(rendered)} authority-aide AGENT.md files under {AUTHORITY_ROOT}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
