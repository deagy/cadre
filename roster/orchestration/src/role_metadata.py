"""Shared primitives for the frontmatter-based role-metadata format.

This module is the parsing/rendering layer only -- it knows how to detect
whether an `AGENT.md` file carries `---`-delimited frontmatter, how to parse
and render that frontmatter's flat `key: value` scalar shape, and how to
strip it back out to recover the file's prose body byte-identically. It does
not know about `catalog.yaml`, `routing.json`, or the validation rules that
turn every role's frontmatter into the generated files -- that orchestration
lives in `generate_role_metadata.py`, which imports this module. Kept
dependency-free (stdlib only), matching every other module in this package
(see `routing.py`).

Frontmatter schema (all scalar, flat, no nesting): `id`, `phase`,
`capability`, `model`, `codex_model`, `reasoning_effort`, `knowledge_focus`.
`definition` is deliberately never stored here -- it is always derived from
the `AGENT.md` file's own path relative to `roster/`.
"""

from __future__ import annotations

import json
import re
from typing import Iterable

FRONTMATTER_FIELDS = (
    "id",
    "phase",
    "capability",
    "model",
    "codex_model",
    "reasoning_effort",
    "knowledge_focus",
)

# Characters that would change a plain YAML scalar's meaning (flow
# indicators, comment/anchor/alias/tag markers, quote characters) if they
# appeared as the first character of an otherwise-unquoted value. This is
# deliberately a small, conservative subset sufficient for this repo's own
# generated content -- not a general YAML plain-scalar grammar.
_YAML_INDICATOR_CHARS = set("!&*-?|>'\"%@`{}[],#:")

# YAML 1.1 (PyYAML's default `safe_load` resolver) type-coerces an unquoted
# plain scalar equal to one of these tokens, in any letter-case, to a bool or
# null instead of leaving it a string -- this module's own `read_scalar`
# would still read the same unquoted text back as the literal string, which
# is exactly the round-trip divergence this set exists to prevent. See
# https://yaml.org/type/bool.html and https://yaml.org/type/null.html.
_YAML_11_RESERVED_WORDS = frozenset(
    {
        "true", "false",
        "yes", "no",
        "on", "off",
        "null", "~",
    }
)

# A bare (unquoted) plain scalar that looks like an integer or float literal
# is resolved by a real YAML parser to a number, not a string, with the same
# `read_scalar`-vs-`yaml.safe_load` divergence risk as the reserved words
# above. Deliberately narrow, matching `_YAML_INDICATOR_CHARS`'s own
# "small, conservative subset" caveat: this only covers plain decimal
# int/float, not every literal form PyYAML's implicit resolver treats as
# numeric (underscore-grouped digits like `1_000`, `0x`/`0b` prefixes,
# sexagesimal `1:30`, or the `.inf`/`.nan` tokens). None of those forms are
# plausible values for this repo's current frontmatter fields (enums or
# prose), so this is not treated as an active gap -- widen it if a future
# field's legitimate values could plausibly collide.
_INT_RE = re.compile(r"^[-+]?[0-9]+$")
_FLOAT_RE = re.compile(r"^[-+]?(\.[0-9]+|[0-9]+(\.[0-9]*)?)([eE][-+]?[0-9]+)?$")


def _looks_like_bare_number(value: str) -> bool:
    if _INT_RE.match(value):
        return True
    if "." in value or "e" in value or "E" in value:
        return bool(_FLOAT_RE.match(value))
    return False


def is_migrated(text: str) -> bool:
    """A role's `AGENT.md` is "migrated" iff it starts with exactly `---\\n`
    or `---\\r\\n` -- `generate_role_metadata.py` requires this of every
    `AGENT.md` it discovers, raising a `RoleMetadataError` for any file that
    is not migrated. No other heuristic (file length, presence of `id:` text
    elsewhere, etc.) counts.
    """
    return text.startswith("---\n") or text.startswith("---\r\n")


def _needs_quoting(value: str) -> bool:
    if not value:
        return True
    if value != value.strip():
        return True
    if ": " in value or " #" in value:
        return True
    if "\n" in value or "\r" in value:
        return True
    if value[0] in _YAML_INDICATOR_CHARS:
        return True
    if value.lower() in _YAML_11_RESERVED_WORDS:
        return True
    if _looks_like_bare_number(value):
        return True
    return False


def emit_scalar(value: str) -> str:
    """Render `value` as the text that follows `key: ` in a frontmatter line.

    Deterministic rule: emit the value unquoted when it is non-empty, has no
    leading/trailing whitespace, contains neither `": "` nor `" #"` (both of
    which would otherwise be mistaken for a mapping separator or a comment
    by a naive line-oriented reader), contains no embedded `\n`/`\r` (which
    would otherwise split into extra lines that `parse_frontmatter`'s flat
    `key: value`-per-line grammar cannot read back), does not start with a
    YAML flow/comment/anchor/alias/tag/quote indicator character, is not a
    YAML 1.1 reserved bool/null word (`true`/`false`/`yes`/`no`/`on`/`off`/
    `null`/`~`, in any letter-case) that a real YAML parser would type-coerce
    away from a string, and does not look like a bare integer/float literal
    for the same reason. Every other value (including the empty string) is
    rendered as a JSON string literal -- JSON string syntax is a strict
    subset of YAML flow-scalar syntax, so this stays valid to read back with
    the same rule in reverse.
    """
    if _needs_quoting(value):
        return json.dumps(value)
    return value


def read_scalar(raw: str) -> str:
    """Invert `emit_scalar`: parse the text that followed `key:` on a
    frontmatter line (already separated from the key) back into the
    original string value.
    """
    stripped = raw.strip()
    if stripped.startswith('"'):
        return json.loads(stripped)
    return stripped


_FIELD_LINE = re.compile(r"^([a-z_]+):(.*)$")


def parse_frontmatter(text: str) -> tuple[dict[str, str], str] | None:
    """Return `(fields, body)` when `text` is migrated (see `is_migrated`),
    else `None`.

    `fields` contains every `key: value` line found between the opening and
    closing `---` delimiters, decoded with `read_scalar` -- callers are
    responsible for checking which of `FRONTMATTER_FIELDS` are actually
    present (a missing required field is a validation error, not a parse
    error, so unknown/missing keys are not rejected here). `body` is
    everything after the closing delimiter line, unmodified.

    Raises `ValueError` if the opening delimiter has no matching closing
    delimiter, or a non-blank line inside the block does not match the flat
    `key: value` shape.
    """
    if not is_migrated(text):
        return None
    newline = "\r\n" if text.startswith("---\r\n") else "\n"
    lines = text.split(newline)
    closing_index = None
    for index in range(1, len(lines)):
        if lines[index] == "---":
            closing_index = index
            break
    if closing_index is None:
        raise ValueError("frontmatter opening '---' has no matching closing '---' line")

    fields: dict[str, str] = {}
    for line in lines[1:closing_index]:
        if not line.strip():
            continue
        match = _FIELD_LINE.match(line)
        if not match:
            raise ValueError(f"unrecognized frontmatter line: {line!r}")
        key, raw_value = match.group(1), match.group(2)
        fields[key] = read_scalar(raw_value)

    body = newline.join(lines[closing_index + 1 :])
    return fields, body


def frontmatter_closing_delimiter_end(text: str) -> int | None:
    """Return the character offset immediately after the closing
    frontmatter delimiter line's `---` (not including that line's trailing
    newline), or `None` when `text` is not migrated (see `is_migrated`).

    Uses the same exact-line-match delimiter detection as
    `parse_frontmatter`/`strip_frontmatter` (a line consisting of exactly
    `---`, found by splitting on whichever newline convention the opening
    delimiter used) rather than a raw substring search -- a raw
    `text.find("---", 3)` would false-match on a `---` substring embedded
    inside a field value that appears before the real closing delimiter
    line. Callers that need to insert content immediately after the closing
    delimiter (e.g. `generate_global_plugin.py`'s packaged-suite-copy
    marker insertion) should use this offset instead of a raw search.

    Raises `ValueError` under the same condition as `parse_frontmatter`
    when no closing delimiter line is found.
    """
    if not is_migrated(text):
        return None
    newline = "\r\n" if text.startswith("---\r\n") else "\n"
    lines = text.split(newline)
    offset = len(lines[0]) + len(newline)
    for index in range(1, len(lines)):
        line = lines[index]
        if line == "---":
            return offset + len(line)
        offset += len(line) + len(newline)
    raise ValueError("frontmatter opening '---' has no matching closing '---' line")


def strip_frontmatter(text: str) -> str:
    """Return `text` with any leading frontmatter block removed, leaving the
    body byte-identical to what it would be without the block. A no-op when
    `text` is not migrated.
    """
    parsed = parse_frontmatter(text)
    if parsed is None:
        return text
    _fields, body = parsed
    return body


def render_frontmatter(fields: dict[str, str]) -> str:
    """Render a complete `---`-delimited frontmatter block (including both
    delimiter lines and a trailing newline after the closing delimiter) for
    `fields`, which must contain exactly `FRONTMATTER_FIELDS`.

    Field order is fixed to `FRONTMATTER_FIELDS` regardless of the input
    dict's iteration order, so output is deterministic across a `dict` built
    from any source ordering.
    """
    missing = [field for field in FRONTMATTER_FIELDS if field not in fields]
    if missing:
        raise ValueError(f"render_frontmatter: missing field(s): {', '.join(missing)}")
    extra = sorted(set(fields) - set(FRONTMATTER_FIELDS))
    if extra:
        raise ValueError(f"render_frontmatter: unknown field(s): {', '.join(extra)}")
    lines = ["---"]
    for field in FRONTMATTER_FIELDS:
        lines.append(f"{field}: {emit_scalar(fields[field])}")
    lines.append("---")
    lines.append("")
    return "\n".join(lines)


def parse_order_file(content: str) -> list[str]:
    """Parse `catalog-order.txt`'s one-id-per-line format: `#` starts a
    full-line or trailing comment, blank lines are ignored, and every
    remaining line must be a single role id. Raises on a duplicate id or a
    malformed line.
    """
    ids: list[str] = []
    seen: set[str] = set()
    for line_number, raw_line in enumerate(content.splitlines(), start=1):
        line = raw_line.split("#", 1)[0].strip()
        if not line:
            continue
        if not re.fullmatch(r"[a-z0-9-]+", line):
            raise ValueError(f"catalog-order.txt line {line_number}: invalid role id {line!r}")
        if line in seen:
            raise ValueError(f"catalog-order.txt line {line_number}: duplicate role id {line!r}")
        seen.add(line)
        ids.append(line)
    return ids


def iter_field_lines(fields: dict[str, str], field_order: Iterable[str]) -> Iterable[str]:
    """Yield `    <field>: <value>` lines (4-space indented, catalog.yaml's
    field style) for `field_order`, in that order. A thin shared helper
    available for any caller that wants to format fields in catalog.yaml's
    style identically to this module's own conventions; note that
    `generate_role_metadata.py`'s `render_catalog()` currently reimplements
    this formatting inline rather than calling this function.
    """
    for field in field_order:
        yield f"    {field}: {fields[field]}"
