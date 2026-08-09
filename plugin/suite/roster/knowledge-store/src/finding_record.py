"""Build a staged record from a structured finding, generating the ceremony fields.

The staged-record contract (`staged_records.py`) requires thirteen frontmatter
keys, three of which are pure bookkeeping that a human or an agent should
never have to hand-compute: `id` (must match `^KS-\\d{8}-[a-z0-9-]+$` and be
unique), `content_digest` (a sha256 of the body, under a specific
normalisation), and `status` (always `"proposed"` for a fresh submission).
Every other required field is a judgement call about the finding itself, and
this module deliberately does not touch those -- see "What this module does
NOT do" below.

The premise this module exists to test (see the task that introduced it): the
staged-record pipeline is unused not because nobody has findings, but because
producing a well-formed record is friction -- hand-written YAML frontmatter,
a sha256 the parser then re-verifies, an id whose format must be memorised.
`build_record` collapses that to "supply the fields you already know", and
`staged_store.put_generated_record` makes the resulting write safe against a
generated id colliding with something else.

What this module does NOT do
-----------------------------
It does not verify that a finding is genuine, that an agent actually observed
what it claims, or that the fields supplied are honest. Nothing here (or
anywhere in this repository) can verify that a review produced a finding
during a review, only that the finding it claims to have produced is
well-formed once handed to this module. Do not read a record built by this
module, or a passing `render-only`/`propose` call, as evidence that a review
happened. It is evidence only that *if* a finding was reported, the report is
structurally sound.

It also does not default `untrusted_instruction_risk` or
`proposed_classification`. Both are required in the finding input with no
fallback: `staged_records.py`'s own automatic-defer rule exists because that
risk assessment cannot be inferred, and a classification default would be a
permissiveness judgement this module has no basis to make on the caller's
behalf. See `FINDING_KEYS` and `build_record` for the complete required set.
"""

from __future__ import annotations

import re
from datetime import datetime, timezone
from typing import Any

from staged_records import REQUIRED_KEYS, compute_digest, validate_parsed

#: The frontmatter fields a finding must supply. This is `REQUIRED_KEYS`
#: minus the two this module generates (`id`, `content_digest`) and the one
#: that is always the same for a fresh proposal (`status`, always
#: `"proposed"`) -- kept derived from `REQUIRED_KEYS` rather than a second
#: hand-written tuple, so a change to the contract's required-key set is not
#: something this module can silently drift out of step with.
FINDING_KEYS: tuple[str, ...] = tuple(
    key for key in REQUIRED_KEYS if key not in ("id", "content_digest", "status")
)

#: Length cap on the slug portion of a generated id. Long enough to stay
#: recognisable from the title, short enough that an id stays a single
#: reasonable token in logs, filenames, and CLI arguments.
_SLUG_MAX_LENGTH = 40

#: How many hex characters of the content digest are folded into the id.
#: This is what makes two different findings that happen to slugify to the
#: same title on the same day resolve to different ids in practice, without
#: making the id itself a second, competing digest of the body -- it borrows
#: the one `compute_digest` already produces rather than computing anything
#: new. 12 hex characters is 48 bits of a well-distributed hash, which is
#: more collision resistance than this low-stakes id needs; `put_generated_record`
#: is still the actual safety net; this is only what keeps the common case
#: (different findings, same-ish title) from colliding at all.
_DIGEST_SUFFIX_LENGTH = 12

_SLUG_INVALID = re.compile(r"[^a-z0-9]+")


class FindingError(ValueError):
    """A structured finding is missing a field or is not well-formed input."""


def _slugify(title: str) -> str:
    slug = _SLUG_INVALID.sub("-", title.strip().lower()).strip("-")
    if not slug:
        slug = "finding"
    return slug[:_SLUG_MAX_LENGTH].strip("-") or "finding"


def generate_id(title: str, digest: str, *, when: datetime | None = None) -> str:
    """Return a deterministic id for `(title, digest)`.

    Deterministic given the same inputs: the same title, on the same UTC
    date, over content that hashes to the same digest, always produces the
    same id -- proposing the identical finding twice is idempotent by
    construction rather than by accident. Different content (a different
    digest) almost always produces a different id even when the title is
    reused verbatim, because the digest suffix differs. "Almost always" is
    not "always" -- a genuine collision (same title, same day, same digest
    prefix, different full content) is possible and must fail loudly rather
    than silently overwrite; that check lives in
    `staged_store.put_generated_record`, not here, because only the storage
    layer knows what (if anything) already occupies the id.
    """
    moment = when or datetime.now(timezone.utc)
    date_part = moment.strftime("%Y%m%d")
    slug = _slugify(title)
    suffix = digest[:_DIGEST_SUFFIX_LENGTH]
    return f"KS-{date_part}-{slug}-{suffix}"


def build_record(finding: dict[str, Any], *, when: datetime | None = None) -> tuple[dict[str, Any], str]:
    """Turn a structured finding into `(frontmatter, body)`, ready for validation.

    `finding` must be a JSON-object-shaped mapping with a `summary` key plus
    every key in `FINDING_KEYS`. `summary` becomes the record's markdown
    body -- that name, not `body`, because it is the name the dispatch
    contract tells agents to return, and a finding an agent produced by
    following that contract must be stageable without a translation step
    nobody defined -- i.e. the same
    fields `staged_records.REQUIRED_KEYS` demands, minus the three this
    function generates. No field is defaulted: a missing key is reported by
    name rather than silently filled in, because every field in
    `FINDING_KEYS` is either provenance (must be supplied) or a judgement
    call (`proposed_classification`, `untrusted_instruction_risk`,
    `recommended_action`, ...) this module has no authority to make up.

    The caller must still run the result through `staged_records.validate_parsed`
    (or route it through `staged_store.put_record`/`put_generated_record`,
    which do) before treating it as a valid record -- this function builds
    the shape, it does not certify it. It runs the same validator itself
    before returning purely to fail fast with a clear message; that is a
    convenience, not a substitute for the write-time check.
    """
    if not isinstance(finding, dict):
        raise FindingError(
            f"a finding must be a JSON object (mapping), got {type(finding).__name__}"
        )
    missing = [key for key in FINDING_KEYS if key not in finding]
    if missing:
        raise FindingError(
            "finding is missing required field(s): " + ", ".join(missing) + ". Every field the "
            "frontmatter contract requires (roster/knowledge-store/proposed-knowledge.schema.json) "
            "must be supplied explicitly -- this generates only 'id', 'content_digest', and "
            "'status', never a judgement call that belongs to whoever is proposing the finding."
        )
    body = finding.get("summary")
    if not isinstance(body, str) or not body.strip():
        raise FindingError(
            "finding['summary'] must be a non-empty string: it becomes the record's markdown "
            "body, everything the reader needs beyond the frontmatter's structured fields. "
            "'summary' is the name the dispatch contract tells agents to return "
            "(.agents/skills/run-agent-orchestration/references/dispatch-contract.md)"
        )

    frontmatter: dict[str, Any] = {"status": "proposed"}
    for key in FINDING_KEYS:
        frontmatter[key] = finding[key]

    digest = compute_digest(body)
    frontmatter["content_digest"] = digest
    # A malformed (non-string) title is reported by validate_parsed below,
    # not raised here as a crash: generate_id only needs *something* to
    # slugify, and _slugify already tolerates an empty string.
    title_value = frontmatter.get("title")
    frontmatter["id"] = generate_id(
        title_value if isinstance(title_value, str) else "", digest, when=when
    )

    findings = validate_parsed(frontmatter, body)
    if findings:
        raise FindingError(
            "the generated record does not satisfy the staged-record contract: " + "; ".join(findings)
        )
    return frontmatter, body


__all__ = ["FindingError", "FINDING_KEYS", "generate_id", "build_record"]
