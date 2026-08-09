#!/usr/bin/env python3
"""Well-formedness validation for staged proposed-knowledge records.

A staged record is a Markdown file under
`roster/knowledge-store/proposed-knowledge/` with a YAML frontmatter block
delimited by `---` lines, followed by a Markdown body. It is the durable,
auditable destination for one item an agent listed under
`knowledge_steward_handoffs`, and the place `knowledge-store-steward`
records that item's disposition. `proposed-knowledge.schema.json` is the
declarative contract for the frontmatter mapping; this module implements it
plus the cross-field and directory-level rules a JSON Schema document over a
frontmatter-only instance cannot express.

What this module does NOT establish, and cannot
-----------------------------------------------
It does not verify that an agent *emitted* a `knowledge_steward_handoffs`
item, and no schema or parser can. A handoff is free-form agent output: there
is no emission event to observe, no counter to reconcile against, and an
agent that silently proposed nothing is indistinguishable from one that had
nothing to propose. An empty `proposed-knowledge/` directory is therefore
valid here, and its validity says nothing about whether knowledge was lost.

What is checkable, and all this module claims, is that the staged records
which *do* exist are well-formed: required fields present and correctly
typed, digest matching the body, status and disposition coherent, the
automatic-defer rule respected, no absolute local path leaked through
evidence or origin, and ids unique across the directory. Read every finding
as a statement about the records on disk, never about agent behaviour.

Reporting style
---------------
Findings are returned, not raised, matching `routing_health.py` and
`schema_validate.py`: an empty list means valid, and every independent defect
in a record surfaces in one pass rather than only the first. Each rule has
its own distinct, actionable message so a failure names the fix rather than
the symptom.

YAML dependency
---------------
None. `roster/knowledge-store/src/` is stdlib-only, and PyYAML is an optional
extra of the `cadre` distribution (`pyproject.toml`'s `yaml` extra), not a
hard dependency. This module therefore ships a minimal, deliberately
restricted frontmatter parser rather than adding a dependency to a package
that has none -- the same precedent as `routing.py`'s line-oriented
`parse_keyed_entries` for `catalog.yaml`. The parser accepts exactly the
subset the contract needs (top-level scalars, one level of nested mapping,
one level of string list) and raises `RecordFormatError` on anything else,
including block scalars, flow collections and three-level nesting, so an
unsupported construct fails loudly instead of being silently misread.

Run:

    python3 roster/knowledge-store/src/staged_records.py

Exits non-zero with every finding on stderr; exits zero with a summary line
on stdout when every staged record is well-formed.
"""

from __future__ import annotations

import argparse
import hashlib
import re
import sys
from pathlib import Path
from typing import Any

DEFAULT_DIRECTORY = Path(__file__).resolve().parents[1] / "proposed-knowledge"

# No DEFAULT_SCHEMA constant: `proposed-knowledge.schema.json` is the
# declarative contract for humans and external tooling, but this module never
# loads it, and `generate_global_plugin.py` packages only README.md,
# SECURITY.md and `src/` from this directory -- a constant pointing at the
# schema would dangle in the packaged distribution. The two are kept in step
# by test_staged_records.SchemaAgreementTests instead, which compares the
# schema document against this module's constants directly.

DELIMITER = "---"

#: Frontmatter keys every staged record must carry, in canonical order.
REQUIRED_KEYS: tuple[str, ...] = (
    "id",
    "title",
    "status",
    "evidence",
    "origin",
    "proposed_classification",
    "source_scope",
    "sensitivity_notes",
    "conflicts_or_staleness",
    "recommended_action",
    "untrusted_instruction_risk",
    "staged_by",
    "content_digest",
)

#: Keys permitted but not required. `disposition` appears only once the
#: steward has decided.
OPTIONAL_KEYS: tuple[str, ...] = ("disposition",)

STATUS_VALUES: tuple[str, ...] = ("proposed", "accepted", "rejected", "deferred")
RECOMMENDED_ACTIONS: tuple[str, ...] = ("ingest", "update", "reclassify", "defer")
DISPOSITION_ACTIONS: tuple[str, ...] = ("accepted", "rejected", "deferred")
ORIGIN_KEYS: tuple[str, ...] = ("task", "artifact", "revision")
DISPOSITION_KEYS: tuple[str, ...] = (
    "action",
    "reason",
    "classification_used",
    "diverged_from_proposal",
    "decided_by",
)

#: Required non-empty plain strings with no further constraint.
NON_EMPTY_STRING_KEYS: tuple[str, ...] = (
    "title",
    "proposed_classification",
    "source_scope",
    "staged_by",
)

#: Required strings that are allowed to be empty. The key must still be
#: present, so "nobody considered it" stays distinguishable from
#: "considered, nothing found".
POSSIBLY_EMPTY_STRING_KEYS: tuple[str, ...] = ("sensitivity_notes", "conflicts_or_staleness")

ID_PATTERN = re.compile(r"^KS-\d{8}-[a-z0-9-]+$")
DIGEST_PATTERN = re.compile(r"^[0-9a-f]{64}$")

#: Absolute-local-path shapes rejected in `evidence[]` and `origin` values.
#: The Windows form requires a single drive letter not preceded by another
#: word character, so a `file.py:12` reference and a `https://` URL do not
#: false-positive.
_ABSOLUTE_PATH_PATTERNS: tuple[tuple[re.Pattern[str], str], ...] = (
    (re.compile(r"(?<![\w.-])/home/"), "/home/"),
    (re.compile(r"(?<![\w.-])/Users/"), "/Users/"),
    (re.compile(r"(?<![\w.-])~/"), "~/"),
    (re.compile(r"(?<![A-Za-z0-9])[A-Za-z]:[\\/]"), "a Windows drive path such as C:\\"),
)

_REDACTION_RULE = (
    "staged records carry the same source_uri redaction rule as knowledge citations "
    "(roster/shared/knowledge-use-policy.md: omit or redact nested citation source_uri by "
    "default, because it may expose a local path). Use a repository-relative path or a "
    "redacted reference instead"
)

_SCHEMA_POINTER = "see roster/knowledge-store/proposed-knowledge.schema.json for the frontmatter contract"

_KEY_LINE = re.compile(r"^([A-Za-z_][A-Za-z0-9_]*):(?:[ \t]+(.*))?$")


class RecordFormatError(ValueError):
    """The text is not a parseable staged record at all.

    Raised only for structural failures that make the frontmatter
    unreadable (missing or unclosed delimiter, unsupported YAML construct,
    malformed line). Contract violations in an otherwise-parseable record
    are reported as findings, not raised.
    """


def compute_digest(body: str) -> str:
    """Return the lowercase sha256 hex of `body` under the canonical
    normalisation.

    The normalisation is defined here, once, and every producer and consumer
    of `content_digest` must go through this function so the digest can never
    be computed two ways:

    1. The body is everything after the closing `---` delimiter line. The
       delimiter line itself, the frontmatter block, and the opening
       delimiter are all excluded -- amending `status` or appending a
       `disposition` therefore never invalidates the digest of unchanged
       prose, which is the whole point: the digest pins the *claim*, while
       the frontmatter records the claim's lifecycle.
    2. `\\r\\n` and lone `\\r` are normalised to `\\n`, so a record checked
       out with CRLF line endings digests identically to the same record with
       LF endings.
    3. Leading and trailing whitespace (including the blank line that
       conventionally follows the closing delimiter, and any trailing
       newline) is stripped.
    4. The result is encoded UTF-8 and hashed with sha256.

    No other transformation is applied: interior whitespace, blank lines
    between paragraphs, and Markdown structure are all significant.
    """
    normalized = body.replace("\r\n", "\n").replace("\r", "\n").strip()
    return hashlib.sha256(normalized.encode("utf-8")).hexdigest()


def _parse_scalar(text: str, line_number: int) -> Any:
    """Parse one frontmatter scalar from the text after `key:` or `- `.

    Deliberately restricted relative to YAML: `true`/`false` become booleans,
    `null`/`~` become None (so a wrong type is reported rather than silently
    accepted as a string), quoted strings are unquoted, and every other bare
    token stays a string -- numbers and dates are NOT converted, because no
    field in the contract is numeric and a surprising coercion is worse than
    a plain string. Constructs this parser cannot represent faithfully raise
    rather than being approximated.
    """
    value = text.strip()
    if value in ("|", ">", "|-", ">-", "|+", ">+"):
        raise RecordFormatError(
            f"line {line_number}: block scalars ({value!r}) are not supported by the staged-record "
            f"frontmatter parser; put long prose in the record body and keep frontmatter values on one line"
        )
    if value.startswith(("[", "{")):
        raise RecordFormatError(
            f"line {line_number}: flow-style collections are not supported by the staged-record "
            f"frontmatter parser; use an indented block list (`- item`) or block mapping instead"
        )
    if value.startswith(("&", "*")):
        raise RecordFormatError(
            f"line {line_number}: YAML anchors/aliases are not supported by the staged-record "
            f"frontmatter parser; write the value out in full"
        )
    if len(value) >= 2 and value[0] == '"' and value[-1] == '"':
        return value[1:-1].replace('\\"', '"').replace("\\\\", "\\")
    if len(value) >= 2 and value[0] == "'" and value[-1] == "'":
        return value[1:-1].replace("''", "'")
    if value in ("true", "True", "TRUE"):
        return True
    if value in ("false", "False", "FALSE"):
        return False
    if value in ("null", "Null", "NULL", "~"):
        return None
    return value


class _Pending:
    """Marker for a key whose block body has not been seen yet."""

    __slots__ = ()

    def __repr__(self) -> str:  # pragma: no cover - debugging aid only
        return "<pending block>"


_PENDING = _Pending()


def parse_frontmatter(block: str, *, first_line_number: int = 2) -> dict[str, Any]:
    """Parse a frontmatter block into a mapping.

    `first_line_number` is the file line number of the block's first line, so
    error messages point at the real location in the record rather than at an
    offset into an extracted substring.
    """
    result: dict[str, Any] = {}
    current_key: str | None = None

    for offset, line in enumerate(block.split("\n")):
        line_number = first_line_number + offset
        stripped = line.strip()
        if not stripped or stripped.startswith("#"):
            continue

        indent_text = line[: len(line) - len(line.lstrip())]
        if "\t" in indent_text:
            raise RecordFormatError(
                f"line {line_number}: tab indentation is not valid YAML frontmatter; use spaces"
            )
        indent = len(indent_text)

        if indent == 0:
            match = _KEY_LINE.match(line.rstrip())
            if not match:
                raise RecordFormatError(
                    f"line {line_number}: expected a top-level `key: value` or `key:` line, "
                    f"got {line.strip()!r}"
                )
            key, raw_value = match.group(1), match.group(2)
            if key in result:
                raise RecordFormatError(
                    f"line {line_number}: duplicate frontmatter key {key!r}; the later value would "
                    f"silently overwrite the earlier one"
                )
            if raw_value is None or not raw_value.strip():
                result[key] = _PENDING
                current_key = key
            else:
                result[key] = _parse_scalar(raw_value, line_number)
                current_key = None
            continue

        if current_key is None:
            raise RecordFormatError(
                f"line {line_number}: indented line {stripped!r} does not belong to any preceding key"
            )

        container = result[current_key]
        if stripped == "-" or stripped.startswith("- "):
            if container is _PENDING:
                container = []
                result[current_key] = container
            if not isinstance(container, list):
                raise RecordFormatError(
                    f"line {line_number}: key {current_key!r} already has mapping entries; a key is "
                    f"either a list or a mapping, not both"
                )
            container.append(_parse_scalar(stripped[1:], line_number))
            continue

        match = _KEY_LINE.match(stripped)
        if not match:
            raise RecordFormatError(
                f"line {line_number}: expected a nested `key: value` line or a `- item` list entry "
                f"under {current_key!r}, got {stripped!r}"
            )
        sub_key, raw_value = match.group(1), match.group(2)
        if container is _PENDING:
            container = {}
            result[current_key] = container
        if not isinstance(container, dict):
            raise RecordFormatError(
                f"line {line_number}: key {current_key!r} already has list entries; a key is either "
                f"a list or a mapping, not both"
            )
        if sub_key in container:
            raise RecordFormatError(
                f"line {line_number}: duplicate key {sub_key!r} under {current_key!r}; the later "
                f"value would silently overwrite the earlier one"
            )
        if raw_value is None or not raw_value.strip():
            raise RecordFormatError(
                f"line {line_number}: {current_key}.{sub_key} has no value; the staged-record "
                f"frontmatter parser supports one level of nesting only, so a nested key must carry "
                f"a scalar on the same line"
            )
        container[sub_key] = _parse_scalar(raw_value, line_number)

    for key, value in result.items():
        if value is _PENDING:
            raise RecordFormatError(
                f"frontmatter key {key!r} opens a block but has no entries; give it a scalar on the "
                f"same line, or an indented list/mapping beneath it"
            )
    return result


def parse_record(text: str) -> tuple[dict[str, Any], str]:
    """Split a staged record into `(frontmatter_mapping, body)`.

    The returned body has newlines normalised to `\\n` but is otherwise
    unmodified; `compute_digest` applies the remaining normalisation. Raises
    `RecordFormatError` if the text is not a delimited frontmatter record.
    """
    normalized = text.lstrip("\ufeff").replace("\r\n", "\n").replace("\r", "\n")
    lines = normalized.split("\n")
    if not lines or lines[0].strip() != DELIMITER:
        raise RecordFormatError(
            f"record must begin with a {DELIMITER!r} frontmatter delimiter on its first line "
            f"({_SCHEMA_POINTER})"
        )
    closing_index: int | None = None
    for index in range(1, len(lines)):
        if lines[index].strip() == DELIMITER:
            closing_index = index
            break
    if closing_index is None:
        raise RecordFormatError(
            f"frontmatter is never closed: no {DELIMITER!r} line after the opening delimiter "
            f"({_SCHEMA_POINTER})"
        )
    frontmatter = parse_frontmatter("\n".join(lines[1:closing_index]), first_line_number=2)
    body = "\n".join(lines[closing_index + 1 :])
    return frontmatter, body


def _type_name(value: Any) -> str:
    return type(value).__name__


def _check_string(
    frontmatter: dict[str, Any], key: str, findings: list[str], *, allow_empty: bool = False
) -> str | None:
    if key not in frontmatter:
        return None
    value = frontmatter[key]
    if not isinstance(value, str):
        findings.append(
            f"frontmatter key {key!r} must be a string, got {_type_name(value)} {value!r} "
            f"({_SCHEMA_POINTER})"
        )
        return None
    if not allow_empty and not value.strip():
        findings.append(
            f"frontmatter key {key!r} must be a non-empty string, got {value!r} ({_SCHEMA_POINTER})"
        )
        return None
    return value


def _check_enum(
    frontmatter: dict[str, Any], key: str, allowed: tuple[str, ...], findings: list[str]
) -> str | None:
    if key not in frontmatter:
        return None
    value = frontmatter[key]
    if not isinstance(value, str):
        findings.append(
            f"frontmatter key {key!r} must be a string, got {_type_name(value)} {value!r}; "
            f"allowed values are {', '.join(allowed)}"
        )
        return None
    if value not in allowed:
        findings.append(
            f"frontmatter key {key!r} must be one of {', '.join(allowed)}; got {value!r}"
        )
        return None
    return value


def _check_key_presence(frontmatter: dict[str, Any]) -> list[str]:
    findings: list[str] = []
    known = set(REQUIRED_KEYS) | set(OPTIONAL_KEYS)
    for key in sorted(set(frontmatter) - known):
        findings.append(
            f"unknown top-level frontmatter key {key!r}: the staged-record frontmatter contract is "
            f"closed. Permitted keys are {', '.join(REQUIRED_KEYS)} and the optional "
            f"{', '.join(OPTIONAL_KEYS)}. Put anything else in the record body ({_SCHEMA_POINTER})"
        )
    for key in REQUIRED_KEYS:
        if key not in frontmatter:
            findings.append(f"missing required frontmatter key {key!r} ({_SCHEMA_POINTER})")
    return findings


def _check_id(frontmatter: dict[str, Any], findings: list[str]) -> None:
    value = _check_string(frontmatter, "id", findings)
    if value is None:
        return
    if not ID_PATTERN.match(value):
        findings.append(
            f"frontmatter key 'id' must match {ID_PATTERN.pattern} (KS-YYYYMMDD-slug, lowercase "
            f"slug); got {value!r}. The id is immutable once assigned, because the disposition and "
            f"any downstream ingestion reference it"
        )


def _check_evidence(frontmatter: dict[str, Any], findings: list[str]) -> None:
    if "evidence" not in frontmatter:
        return
    value = frontmatter["evidence"]
    if not isinstance(value, list):
        findings.append(
            f"frontmatter key 'evidence' must be a list of strings, got {_type_name(value)} "
            f"{value!r}; write it as an indented `- item` block"
        )
        return
    if not value:
        findings.append(
            "frontmatter key 'evidence' must be a non-empty list: a record with no citation or "
            "file:line reference is an unsupported claim, which the steward cannot triage"
        )
        return
    for index, entry in enumerate(value):
        if not isinstance(entry, str) or not entry.strip():
            findings.append(
                f"evidence[{index}] must be a non-empty string, got {_type_name(entry)} {entry!r}"
            )


def _check_origin(frontmatter: dict[str, Any], findings: list[str]) -> None:
    if "origin" not in frontmatter:
        return
    value = frontmatter["origin"]
    if not isinstance(value, dict):
        findings.append(
            f"frontmatter key 'origin' must be a mapping with keys {', '.join(ORIGIN_KEYS)}, got "
            f"{_type_name(value)} {value!r}"
        )
        return
    for key in ORIGIN_KEYS:
        if key not in value:
            findings.append(
                f"origin is missing required key {key!r}: provenance needs all of "
                f"{', '.join(ORIGIN_KEYS)} to stay point-in-time attributable"
            )
        elif not isinstance(value[key], str) or not value[key].strip():
            findings.append(
                f"origin.{key} must be a non-empty string, got {_type_name(value[key])} {value[key]!r}"
            )


def _check_untrusted_instruction_risk(frontmatter: dict[str, Any], findings: list[str]) -> Any:
    if "untrusted_instruction_risk" not in frontmatter:
        return None
    value = frontmatter["untrusted_instruction_risk"]
    if isinstance(value, bool) or value == "unknown":
        return value
    findings.append(
        f"frontmatter key 'untrusted_instruction_risk' must be the boolean true, the boolean false, "
        f"or the string 'unknown'; got {_type_name(value)} {value!r}. Quote nothing for the "
        f"booleans -- a quoted \"true\" is the string, not the boolean"
    )
    return None


def _check_recommended_action(frontmatter: dict[str, Any], findings: list[str]) -> None:
    if "recommended_action" not in frontmatter:
        return
    value = frontmatter["recommended_action"]
    if value == "delete":
        findings.append(
            "recommended_action 'delete' is not an available action: the knowledge store implements "
            "no deletion capability at all, so no staged record can request one. A required deletion "
            "escalates to knowledge-store-steward and an authorized human, with evidence-custodian "
            "coordination (roster/knowledge-store/AGENT.md, 'Escalate when'); it is never recorded "
            "as a staged-record action. Use 'defer' and state the deletion request in "
            "sensitivity_notes"
        )
        return
    _check_enum(frontmatter, "recommended_action", RECOMMENDED_ACTIONS, findings)


def _check_digest(frontmatter: dict[str, Any], body: str, findings: list[str]) -> None:
    value = _check_string(frontmatter, "content_digest", findings)
    if value is None:
        return
    if not DIGEST_PATTERN.match(value):
        findings.append(
            f"frontmatter key 'content_digest' must be 64 lowercase hex characters (a sha256 digest); "
            f"got {value!r}"
        )
        return
    expected = compute_digest(body)
    if value != expected:
        findings.append(
            f"content_digest does not match the record body: declared {value}, computed {expected}. "
            f"Recompute it with staged_records.compute_digest(body) -- the body is everything after "
            f"the closing '---' line, with CRLF normalised to LF and leading/trailing whitespace "
            f"stripped. Never compute it by hand: a second implementation of the normalisation is "
            f"how a digest silently stops meaning anything"
        )


def _check_disposition_shape(frontmatter: dict[str, Any], findings: list[str]) -> dict[str, Any] | None:
    if "disposition" not in frontmatter:
        return None
    value = frontmatter["disposition"]
    if not isinstance(value, dict):
        findings.append(
            f"frontmatter key 'disposition' must be a mapping with keys "
            f"{', '.join(DISPOSITION_KEYS)}, got {_type_name(value)} {value!r}"
        )
        return None
    for key in DISPOSITION_KEYS:
        if key not in value:
            findings.append(
                f"disposition is missing required key {key!r}: a disposition without all of "
                f"{', '.join(DISPOSITION_KEYS)} is not an audit trail"
            )
    action = value.get("action")
    if "action" in value and (not isinstance(action, str) or action not in DISPOSITION_ACTIONS):
        findings.append(
            f"disposition.action must be one of {', '.join(DISPOSITION_ACTIONS)}; got {action!r}"
        )
    reason = value.get("reason")
    if "reason" in value and (not isinstance(reason, str) or not reason.strip()):
        findings.append(
            f"disposition.reason must be a non-empty string, got {_type_name(reason)} {reason!r}: an "
            f"unexplained disposition cannot be reviewed"
        )
    classification_used = value.get("classification_used")
    if "classification_used" in value and not isinstance(classification_used, str):
        findings.append(
            f"disposition.classification_used must be a string, got "
            f"{_type_name(classification_used)} {classification_used!r}"
        )
    diverged = value.get("diverged_from_proposal")
    if "diverged_from_proposal" in value and not isinstance(diverged, bool):
        findings.append(
            f"disposition.diverged_from_proposal must be the boolean true or false, got "
            f"{_type_name(diverged)} {diverged!r}"
        )
    decided_by = value.get("decided_by")
    if "decided_by" in value and not isinstance(decided_by, str):
        findings.append(
            f"disposition.decided_by must be a string, got {_type_name(decided_by)} {decided_by!r}"
        )
    return value


def _check_status_disposition_coherence(
    status: str | None, disposition: dict[str, Any] | None, has_disposition_key: bool, findings: list[str]
) -> None:
    if status is None:
        return
    if status == "proposed":
        if has_disposition_key:
            findings.append(
                "status 'proposed' requires 'disposition' to be absent: a proposed record has not "
                "been dispositioned yet, and a disposition present alongside it makes the record's "
                "real state unreadable. Set status to the disposition's action, or remove the "
                "disposition"
            )
        return
    if not has_disposition_key:
        findings.append(
            f"status {status!r} requires a 'disposition' mapping: a dispositioned record must record "
            f"the decision (action, reason, classification_used, diverged_from_proposal, decided_by), "
            f"otherwise the proposal has an outcome with no audit linkage back to who decided it and why"
        )
        return
    if disposition is None:
        return
    action = disposition.get("action")
    if isinstance(action, str) and action in DISPOSITION_ACTIONS and action != status:
        findings.append(
            f"disposition.action {action!r} does not match status {status!r}: the two must agree, "
            f"so the record's state cannot be read two ways"
        )


def _check_automatic_defer(
    risk: Any, status: str | None, disposition: dict[str, Any] | None, findings: list[str]
) -> None:
    if risk is not True:
        return
    if status == "accepted":
        findings.append(
            "untrusted_instruction_risk is true, so status must not be 'accepted': the "
            "automatic-defer rule makes an injection-risk candidate a defer, not a discretionary "
            "approval (roster/knowledge-store/AGENT.md: 'treat injection_risk=true on "
            "handoff-originated candidates as an automatic defer'). Set status to 'deferred', or "
            "correct untrusted_instruction_risk if the assessment was wrong"
        )
    if disposition is None:
        return
    action = disposition.get("action")
    if isinstance(action, str) and action in DISPOSITION_ACTIONS and action != "deferred":
        findings.append(
            f"untrusted_instruction_risk is true, so disposition.action must be 'deferred'; got "
            f"{action!r}. This is the automatic-defer rule: an injection-risk candidate is deferred "
            f"and escalated, never accepted or rejected on the steward's discretion alone"
        )


def _absolute_path_hit(value: str) -> str | None:
    for pattern, label in _ABSOLUTE_PATH_PATTERNS:
        if pattern.search(value):
            return label
    return None


def _check_absolute_paths(frontmatter: dict[str, Any], findings: list[str]) -> None:
    evidence = frontmatter.get("evidence")
    if isinstance(evidence, list):
        for index, entry in enumerate(evidence):
            if not isinstance(entry, str):
                continue
            label = _absolute_path_hit(entry)
            if label is not None:
                findings.append(
                    f"evidence[{index}] contains an absolute local path ({label}): {entry!r}. "
                    f"{_REDACTION_RULE}"
                )
    origin = frontmatter.get("origin")
    if isinstance(origin, dict):
        for key in ORIGIN_KEYS:
            entry = origin.get(key)
            if not isinstance(entry, str):
                continue
            label = _absolute_path_hit(entry)
            if label is not None:
                findings.append(
                    f"origin.{key} contains an absolute local path ({label}): {entry!r}. "
                    f"{_REDACTION_RULE}"
                )


def validate_parsed(frontmatter: dict[str, Any], body: str) -> list[str]:
    """Validate an already-parsed record. Returns findings; empty means valid."""
    findings: list[str] = []
    findings.extend(_check_key_presence(frontmatter))

    _check_id(frontmatter, findings)
    for key in NON_EMPTY_STRING_KEYS:
        _check_string(frontmatter, key, findings)
    for key in POSSIBLY_EMPTY_STRING_KEYS:
        _check_string(frontmatter, key, findings, allow_empty=True)
    status = _check_enum(frontmatter, "status", STATUS_VALUES, findings)
    _check_evidence(frontmatter, findings)
    _check_origin(frontmatter, findings)
    _check_recommended_action(frontmatter, findings)
    risk = _check_untrusted_instruction_risk(frontmatter, findings)
    _check_digest(frontmatter, body, findings)

    disposition = _check_disposition_shape(frontmatter, findings)
    _check_status_disposition_coherence(
        status, disposition, "disposition" in frontmatter, findings
    )
    _check_automatic_defer(risk, status, disposition, findings)
    _check_absolute_paths(frontmatter, findings)
    return findings


def validate_record(text: str, *, path: Path | str | None = None) -> list[str]:
    """Validate one staged record's text. Returns findings; empty means valid.

    `path` is used only to prefix findings with a location, matching
    `schema_validate.py`'s convention.
    """
    prefix = f"{path}: " if path is not None else ""
    try:
        frontmatter, body = parse_record(text)
    except RecordFormatError as error:
        return [f"{prefix}{error}"]
    return [f"{prefix}{finding}" for finding in validate_parsed(frontmatter, body)]


def validate_directory(path: Path | str = DEFAULT_DIRECTORY) -> list[str]:
    """Validate every `*.md` staged record in a directory, plus the one rule
    that is a property of the directory rather than of any single file: `id`
    uniqueness.

    An empty directory is valid. That is a deliberate statement of the
    module's limits, not an oversight: nothing observable distinguishes "no
    agent had durable knowledge to propose" from "an agent silently dropped
    one", so this module does not pretend to.
    """
    directory = Path(path)
    if not directory.is_dir():
        return [f"{directory}: staged-record directory does not exist or is not a directory"]

    findings: list[str] = []
    seen_ids: dict[str, Path] = {}
    for record_path in sorted(
        entry
        for entry in directory.glob("*.md")
        # A directory of records may also carry a README explaining what it is
        # -- the export snapshot does, to stop anyone hand-editing generated
        # files. It is documentation, not a record, and the exporter never
        # writes a record under this name because record filenames are always
        # the record id (`KS-YYYYMMDD-slug.md`).
        if entry.is_file() and entry.name != "README.md"
    ):
        try:
            text = record_path.read_text(encoding="utf-8")
        except (OSError, UnicodeDecodeError) as error:
            findings.append(f"{record_path}: cannot be read as UTF-8 text: {error}")
            continue
        findings.extend(validate_record(text, path=record_path))
        try:
            frontmatter, _ = parse_record(text)
        except RecordFormatError:
            continue
        record_id = frontmatter.get("id")
        if not isinstance(record_id, str) or not record_id:
            continue
        if record_id in seen_ids:
            findings.append(
                f"{record_path}: duplicate record id {record_id!r}, already declared by "
                f"{seen_ids[record_id]}. Ids must be unique across the directory: a disposition, an "
                f"audit trail, and any downstream ingestion resolve a record by id alone"
            )
        else:
            seen_ids[record_id] = record_path
    return findings


def run(directory: Path = DEFAULT_DIRECTORY) -> list[str]:
    return validate_directory(directory)


def _cli_description() -> str | None:
    if not __doc__:
        return None
    paragraph: list[str] = []
    for line in __doc__.strip().splitlines():
        stripped = line.strip()
        if not stripped:
            break
        paragraph.append(stripped)
    return " ".join(paragraph) if paragraph else None


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=_cli_description())
    parser.add_argument("--directory", type=Path, default=DEFAULT_DIRECTORY)
    # Accepted as a no-op for symmetry with this repo's other drift guards,
    # mirroring routing_health.py: this tool only ever reports, so it is
    # already in "check" mode.
    parser.add_argument("--check", action="store_true", help=argparse.SUPPRESS)
    args = parser.parse_args(argv)

    findings = run(args.directory)
    if findings:
        for finding in findings:
            print(finding, file=sys.stderr)
        return 1
    print(
        f"staged-record validation passed: every record under {args.directory} is well-formed. "
        f"This does not verify that any agent emitted a knowledge_steward_handoffs item -- no "
        f"check can."
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
