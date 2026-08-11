#!/usr/bin/env python3
"""Measure whether a role's *payload* survives contact with a given model.

Why this exists
---------------

This suite's 159 role definitions are dense: authority statements, required
checks, escalation rules, embedded shared policy. That payload was written
and tuned against frontier models. The Cline distribution, however, is driven
overwhelmingly against open-weight and locally hosted models -- smaller
context windows, weaker instruction-following, and a much sharper drop-off in
how much of a long system prompt actually shapes the output.

The open question that follows is not rhetorical: *does the role catalog still
work down there, or does a local-model distribution need condensed briefs and
a smaller role subset?* Nothing in this repository currently answers it, and
the answer decides whether a separate distribution is warranted or whether the
existing one-source/three-outputs arrangement is sufficient. This module is
the instrument for answering it with measurements instead of assumption.

What it does, in two independent modes
--------------------------------------

**Static** (`--mode static`, the default) needs no model and no network. It
measures each role's payload and reports how it sits against a context budget.
This alone rules a model in or out before a single token is spent: a role
whose brief does not fit is not a fidelity question, it is arithmetic.

**Probe** (`--mode probe`) sends each selected role's real system prompt plus a
probe task to an OpenAI-compatible `/chat/completions` endpoint -- which is
what Ollama, LM Studio, vLLM, llama.cpp's server and most hosted providers all
speak -- and scores the reply against deterministic, declarative checks from
`role-fidelity-probes.yaml`.

On what the probe mode is, and is not
-------------------------------------

It is a **screening instrument**, and this framing is load-bearing rather than
modest boilerplate. The checks are keyword and structure assertions over the
reply text. They catch the failure mode that actually matters at small model
sizes -- the role's constraints stop shaping the output at all -- and they will
happily miss subtler infidelity, and can be fooled by a reply that recites the
right words while doing the wrong thing. A pass is therefore evidence that the
payload still lands, never a certificate that the role behaved correctly.

Two consequences, both deliberate:

- Every reply is recorded in full in the JSON report. The score directs
  attention; the transcript is the actual finding, and a human reading a
  sample of them is a required step, not an optional one.
- No result from this harness may stand in for a human review, approve a
  gate, or accept a risk. It measures a model's grip on a prompt. That is a
  different thing from an authority decision, and this suite's separation of
  authorship from approval is not softened by a good score here.

Anti-cheat note: probes are scored per role, and a probe whose expected
keywords appear verbatim in the role's own brief is degenerate -- the model
can pass by copying. `--warn-degenerate` (on by default) flags those so a
green run is not quietly self-fulfilling.
"""

from __future__ import annotations

import argparse
import http.client
import json
import os
import re
import sys
import urllib.error
import urllib.request
from dataclasses import dataclass, field
from functools import lru_cache
from pathlib import Path
from typing import Any, Iterable, Sequence

# Chars-per-token. A crude divisor, used because a real tokenizer would mean a
# dependency this repository's Python tooling deliberately does not carry, and
# because every decision this number feeds is a coarse one ("does a 4k window
# stand a chance?"). Reported as an estimate everywhere it surfaces, and
# `--chars-per-token` overrides it for a tokenizer known to differ. Do not
# quote these token counts as exact; they are not.
DEFAULT_CHARS_PER_TOKEN = 4.0

DEFAULT_PROBES_FILENAME = "role-fidelity-probes.yaml"

# Where a preset directory tends to live, tried in order, relative to this
# file. Covers a source checkout and a packaged plugin install without the
# caller having to know which one they are in -- in a packaged plugin this
# module sits at `<plugin>/suite/roster/orchestration/src/`, two levels deeper
# than in a checkout, so both roots are tried.
_HERE = Path(__file__).resolve()
_CHECKOUT_ROOT = _HERE.parents[3]        # <repo>/roster/orchestration/src -> <repo>
_PACKAGED_ROOT = _HERE.parents[4]        # <plugin>/suite/roster/orchestration/src -> <plugin>
_PRESET_DIR_CANDIDATES = (
    _CHECKOUT_ROOT / "cline-plugins" / "cline-agents" / "agents",
    _CHECKOUT_ROOT / "plugin" / "agents",
    _PACKAGED_ROOT / "agents",
    _HERE.parents[2] / "agents",
)

# Same "checkout vs packaged plugin" shape as _PRESET_DIR_CANDIDATES above,
# for the one file that carries the tier-vocabulary mapping both preset
# families are generated from.
_RUNNER_CAPABILITIES_CANDIDATES = (
    _CHECKOUT_ROOT / "roster" / "runner-capabilities.json",
    _PACKAGED_ROOT / "suite" / "roster" / "runner-capabilities.json",
)


class FidelityError(RuntimeError):
    """A configuration or input problem the caller can act on."""


# ---------------------------------------------------------------------------
# Preset loading
# ---------------------------------------------------------------------------


# Marks where a generated brief stops being about the role and starts being
# the shared-policy block embedded verbatim into every one of them. Splitting
# on it separates the part that is unique per role from the part that is
# identical across all of them -- which is the difference between "this
# catalog is too big for a small model" and "this catalog repeats itself 159
# times", two problems with entirely different fixes.
SHARED_POLICY_MARKER_RE = re.compile(r"^# Shared policy: ", re.MULTILINE)


@dataclass(frozen=True)
class Preset:
    """One role's shipped payload: what a dispatch actually sends."""

    name: str
    path: Path
    frontmatter: dict[str, str]
    body: str

    @property
    def tier(self) -> str:
        """This role's model tier, normalized to one vocabulary.

        `plugin/agents/*.md` frontmatter carries `model: sonnet` (a Claude
        Code tier name); `cline-plugins/cline-agents/agents/*.md` frontmatter
        carries `modelTier: mid` (that same tier's Cline mirror, per
        `runner-capabilities.json`'s `model_tiers.*.cline_tier`). Both are
        legitimate `--presets-dir` targets, and `default_presets_dir()` picks
        whichever candidate exists first -- so a raw, unnormalized value here
        means a probe's `applies_to_tiers` silently matches nothing at all
        whenever it is run against the vocabulary it was not written in.
        Normalizing to the `model_tiers` key names (`opus`/`sonnet`/`haiku`)
        makes both preset families comparable under one probe file.
        """
        raw = self.frontmatter.get("modelTier") or self.frontmatter.get("model") or "unset"
        return tier_normalization_map().get(raw.strip().lower(), raw)

    @property
    def chars(self) -> int:
        return len(self.body)

    def tokens(self, chars_per_token: float = DEFAULT_CHARS_PER_TOKEN) -> int:
        return int(round(self.chars / chars_per_token))

    @property
    def role_specific_chars(self) -> int:
        """Characters before the first embedded shared-policy block.

        Falls back to the whole body when a brief carries no marker, so a
        hand-authored or differently-generated preset is counted as entirely
        role-specific rather than silently as zero.
        """
        match = SHARED_POLICY_MARKER_RE.search(self.body)
        return match.start() if match else len(self.body)

    @property
    def shared_policy_chars(self) -> int:
        return self.chars - self.role_specific_chars


def _parse_frontmatter(text: str) -> tuple[dict[str, str], str]:
    if not text.startswith("---"):
        return {}, text
    lines = text.splitlines()
    fields: dict[str, str] = {}
    for index, line in enumerate(lines[1:], start=1):
        if line.strip() == "---":
            return fields, "\n".join(lines[index + 1 :]).lstrip("\n")
        key, sep, value = line.partition(":")
        if sep:
            fields[key.strip()] = value.strip().strip('"')
    # Unterminated frontmatter: treat the whole file as body rather than
    # silently consuming it as metadata.
    return {}, text


def default_runner_capabilities_path() -> Path | None:
    """Best-effort locate `runner-capabilities.json`.

    Returns `None` rather than raising: the tier-vocabulary normalization it
    feeds is a correctness improvement over an unnormalized value, not a hard
    requirement to run the harness at all, and a missing file should degrade
    to "no normalization" rather than aborting a probe run.
    """
    for candidate in _RUNNER_CAPABILITIES_CANDIDATES:
        if candidate.is_file():
            return candidate
    return None


@lru_cache(maxsize=None)
def _load_tier_normalization_map(path: Path) -> dict[str, str]:
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, ValueError):
        return {}
    model_tiers = data.get("model_tiers")
    if not isinstance(model_tiers, dict):
        return {}
    mapping: dict[str, str] = {}
    for canonical, info in model_tiers.items():
        canonical = str(canonical).strip()
        if not canonical:
            continue
        mapping[canonical.lower()] = canonical
        if isinstance(info, dict):
            cline_tier = info.get("cline_tier")
            if cline_tier:
                mapping[str(cline_tier).strip().lower()] = canonical
    return mapping


def tier_normalization_map(path: Path | None = None) -> dict[str, str]:
    """Map every raw tier spelling in use onto one canonical name.

    Source of truth is `runner-capabilities.json`'s `model_tiers` block: its
    keys (`opus`/`sonnet`/`haiku`) are the canonical names, and each entry's
    `cline_tier` (`high`/`mid`/`low`) is that same tier's other spelling. Both
    directions map onto the canonical key, so `tier_normalization_map()["mid"]
    == tier_normalization_map()["sonnet"] == "sonnet"`. An unrecognized raw
    value (including `"unset"`) is not in the map and is left unchanged by the
    caller.
    """
    resolved = path or default_runner_capabilities_path()
    if resolved is None:
        return {}
    return _load_tier_normalization_map(resolved)


def default_presets_dir() -> Path:
    for candidate in _PRESET_DIR_CANDIDATES:
        if candidate.is_dir():
            return candidate
    raise FidelityError(
        "could not locate a presets directory; pass --presets-dir explicitly "
        f"(looked in: {', '.join(str(p) for p in _PRESET_DIR_CANDIDATES)})"
    )


def load_presets(presets_dir: Path, roles: Sequence[str] | None = None) -> list[Preset]:
    if not presets_dir.is_dir():
        raise FidelityError(f"{presets_dir}: not a directory")
    wanted = set(roles or ())
    presets: list[Preset] = []
    for path in sorted(presets_dir.glob("*.md")):
        if wanted and path.stem not in wanted:
            continue
        fields, body = _parse_frontmatter(path.read_text(encoding="utf-8"))
        presets.append(
            Preset(name=fields.get("name", path.stem), path=path, frontmatter=fields, body=body)
        )
    if wanted:
        # Selection is by filename stem, so resolution has to be checked
        # against the stem too. Checking `p.name` (the frontmatter field)
        # instead reports a preset that was found and loaded as missing
        # whenever the two differ -- which a hand-authored preset is free to
        # do, and which no error message here would explain.
        missing = sorted(wanted - {p.path.stem for p in presets})
        if missing:
            raise FidelityError(f"no preset found for role(s): {', '.join(missing)}")
    if not presets:
        raise FidelityError(f"{presets_dir}: contains no *.md presets")
    return presets


# ---------------------------------------------------------------------------
# Static analysis -- payload size against a context budget
# ---------------------------------------------------------------------------


def static_report(
    presets: Iterable[Preset],
    context_budget_tokens: int,
    reserve_tokens: int,
    chars_per_token: float = DEFAULT_CHARS_PER_TOKEN,
) -> dict[str, Any]:
    """Measure each payload against the room a model actually leaves for it.

    `reserve_tokens` is the part of the window the role brief may *not* use:
    the task, the retrieved context, the tool schemas, and the reply. Budgeting
    against the raw window is the standard way to conclude a brief fits and
    then watch it truncate in practice.
    """
    usable = context_budget_tokens - reserve_tokens
    rows: list[dict[str, Any]] = []
    for preset in sorted(presets, key=lambda p: p.chars, reverse=True):
        tokens = preset.tokens(chars_per_token)
        role_tokens = int(round(preset.role_specific_chars / chars_per_token))
        rows.append(
            {
                "role": preset.name,
                "tier": preset.tier,
                "chars": preset.chars,
                "estimated_tokens": tokens,
                "role_specific_tokens": role_tokens,
                "shared_policy_tokens": tokens - role_tokens,
                "fits": tokens <= usable,
                "role_specific_fits": role_tokens <= usable,
                "percent_of_usable": round(100.0 * tokens / usable, 1) if usable > 0 else None,
            }
        )
    over = [r for r in rows if not r["fits"]]
    token_counts = [int(r["estimated_tokens"]) for r in rows]
    role_only_counts = [int(r["role_specific_tokens"]) for r in rows]
    shared_counts = [int(r["shared_policy_tokens"]) for r in rows]

    def _median(values: list[int]) -> int:
        return sorted(values)[len(values) // 2] if values else 0

    return {
        "role_specific_over_budget_count": sum(1 for r in rows if not r["role_specific_fits"]),
        "median_role_specific_tokens": _median(role_only_counts),
        "median_shared_policy_tokens": _median(shared_counts),
        "shared_policy_share_of_total": (
            round(sum(shared_counts) / sum(token_counts), 3) if sum(token_counts) else None
        ),
        "mode": "static",
        "context_budget_tokens": context_budget_tokens,
        "reserve_tokens": reserve_tokens,
        "usable_tokens": usable,
        "chars_per_token": chars_per_token,
        "role_count": len(rows),
        "over_budget_count": len(over),
        "largest_role": rows[0]["role"] if rows else None,
        "max_estimated_tokens": max(token_counts) if token_counts else 0,
        "median_estimated_tokens": sorted(token_counts)[len(token_counts) // 2] if token_counts else 0,
        "total_estimated_tokens": sum(token_counts),
        "roles": rows,
    }


# ---------------------------------------------------------------------------
# Probes
# ---------------------------------------------------------------------------


@dataclass(frozen=True)
class Probe:
    """One declarative fidelity question and how to score the answer."""

    id: str
    prompt: str
    description: str = ""
    applies_to: tuple[str, ...] = ()          # role names; empty = all roles
    applies_to_tiers: tuple[str, ...] = ()    # tier names; empty = all tiers
    must_mention_any: tuple[str, ...] = ()
    must_mention_all: tuple[str, ...] = ()
    must_not_mention_any: tuple[str, ...] = ()
    # Regex patterns (case-insensitive), not literal phrases. Exists for the
    # class of negative check `must_not_mention_any` cannot express: "did the
    # reply do the work", checked across implementation stacks rather than one
    # model's observed output style. See `stays-in-remit` in the shipped probe
    # file for why a literal-phrase list of this shape overfits to a single
    # transcript.
    must_not_match_any: tuple[str, ...] = ()
    max_words: int | None = None
    min_words: int | None = None
    # "REFUSE" or "PROCEED". When set, this IS the pass/fail signal and every
    # must_mention_*/must_not_* field above is unused for this probe -- see
    # `score_reply` and the probe-writing guidance in
    # `role-fidelity-probes.yaml`'s header comment for why free-text scoring
    # was retired for this class of authority/remit question.
    expect_verdict: str | None = None

    def applies(self, preset: Preset) -> bool:
        if self.applies_to and preset.name not in self.applies_to:
            return False
        if self.applies_to_tiers and preset.tier not in self.applies_to_tiers:
            return False
        return True


def _as_tuple(value: Any) -> tuple[str, ...]:
    if value is None:
        return ()
    if isinstance(value, str):
        return (value,)
    return tuple(str(v) for v in value)


def parse_probes(raw: Any, source: str = "<probes>") -> list[Probe]:
    if not isinstance(raw, list) or not raw:
        raise FidelityError(f"{source}: expected a non-empty list of probes")
    probes: list[Probe] = []
    seen: set[str] = set()
    for index, entry in enumerate(raw):
        if not isinstance(entry, dict):
            raise FidelityError(f"{source}: probe #{index} is not a mapping")
        probe_id = str(entry.get("id") or "").strip()
        prompt = str(entry.get("prompt") or "").strip()
        if not probe_id:
            raise FidelityError(f"{source}: probe #{index} has no 'id'")
        if not prompt:
            raise FidelityError(f"{source}: probe {probe_id!r} has no 'prompt'")
        if probe_id in seen:
            raise FidelityError(f"{source}: duplicate probe id {probe_id!r}")
        seen.add(probe_id)
        max_words = entry.get("max_words")
        min_words = entry.get("min_words")
        must_not_match_any = _as_tuple(entry.get("must_not_match_any"))
        for pattern in must_not_match_any:
            try:
                re.compile(pattern, re.IGNORECASE)
            except re.error as error:
                raise FidelityError(
                    f"{source}: probe {probe_id!r} has an invalid must_not_match_any pattern "
                    f"{pattern!r}: {error}"
                ) from error
        expect_verdict_raw = entry.get("expect_verdict")
        expect_verdict: str | None = None
        if expect_verdict_raw is not None:
            expect_verdict = str(expect_verdict_raw).strip().upper()
            if expect_verdict not in VALID_VERDICTS:
                raise FidelityError(
                    f"{source}: probe {probe_id!r} has invalid expect_verdict "
                    f"{expect_verdict_raw!r} (must be one of: {', '.join(sorted(VALID_VERDICTS))})"
                )
        probe = Probe(
            id=probe_id,
            prompt=prompt,
            description=str(entry.get("description") or ""),
            applies_to=_as_tuple(entry.get("applies_to")),
            applies_to_tiers=_as_tuple(entry.get("applies_to_tiers")),
            must_mention_any=_as_tuple(entry.get("must_mention_any")),
            must_mention_all=_as_tuple(entry.get("must_mention_all")),
            must_not_mention_any=_as_tuple(entry.get("must_not_mention_any")),
            must_not_match_any=must_not_match_any,
            max_words=int(max_words) if max_words is not None else None,
            min_words=int(min_words) if min_words is not None else None,
            expect_verdict=expect_verdict,
        )
        if not any(
            (
                probe.must_mention_any,
                probe.must_mention_all,
                probe.must_not_mention_any,
                probe.must_not_match_any,
                probe.max_words is not None,
                probe.min_words is not None,
                probe.expect_verdict is not None,
            )
        ):
            raise FidelityError(
                f"{source}: probe {probe_id!r} declares no checks, so it can never fail"
            )
        probes.append(probe)
    return probes


def load_probes(path: Path) -> list[Probe]:
    text = path.read_text(encoding="utf-8")
    try:
        import yaml  # noqa: PLC0415 -- optional; JSON fallback below keeps this runnable without PyYAML
    except ImportError:
        try:
            raw = json.loads(text)
        except ValueError as error:
            raise FidelityError(
                f"{path}: PyYAML is not installed and the file is not valid JSON either ({error}). "
                "Install PyYAML or supply probes as JSON."
            ) from error
    else:
        raw = yaml.safe_load(text)
    return parse_probes(raw, source=str(path))


VALID_VERDICTS = ("REFUSE", "PROCEED")

# Case-insensitive on both the label and the token -- the instructed format is
# an exact literal ("VERDICT: REFUSE"), but this harness is screening small,
# often locally hosted models for whether the *decision* landed, not for
# whether they reproduced casing exactly. Anchored at both ends so trailing
# prose on the same line ("VERDICT: REFUSE, obviously") does not match: that
# is the model not following the format, which `parse_verdict` reports as
# absent/malformed rather than silently accepting a near-miss.
VERDICT_LINE_RE = re.compile(r"^VERDICT:\s*(REFUSE|PROCEED)\s*$", re.IGNORECASE)


def parse_verdict(reply: str) -> str | None:
    """Extract the declared verdict token from a verdict-scored probe's reply.

    Strict about *position*: only the first non-empty line is ever
    considered. A `VERDICT: REFUSE` line appearing after several paragraphs
    of reasoning is not the model following the instructed format -- it is
    the model reasoning its way to an answer and then affixing the label
    after the fact, which this probe cannot distinguish from a model that
    talked itself out of the refusal it started to write. Returns `None`
    for an empty reply, a first line that is not a VERDICT line at all, or
    one that carries the wrong token or extra trailing text -- callers
    report all of these as "no valid verdict" rather than trying to
    distinguish "absent" from "malformed", because the practical finding is
    the same: the model did not produce a gradeable verdict.
    """
    for line in reply.splitlines():
        stripped = line.strip()
        if not stripped:
            continue
        match = VERDICT_LINE_RE.match(stripped)
        return match.group(1).upper() if match else None
    return None


def _contains(haystack: str, needle: str) -> bool:
    """Case-insensitive whole-token-ish containment.

    Substring matching alone reports "sign" inside "design" and "gate" inside
    "delegate" -- both plausible in this domain and both wrong. Word-boundary
    matching is used wherever the needle is a bare word, and plain containment
    where it is a phrase or carries punctuation.
    """
    needle = needle.strip()
    if not needle:
        return False
    if re.fullmatch(r"[\w'-]+", needle):
        return re.search(rf"\b{re.escape(needle)}\b", haystack, re.IGNORECASE) is not None
    return needle.lower() in haystack.lower()


def score_reply(probe: Probe, reply: str) -> dict[str, Any]:
    """Score one reply. Pure: no network, no clock, no filesystem."""
    words = len(reply.split())

    if probe.expect_verdict is not None:
        # The verdict IS the pass/fail for this probe; must_mention_*/
        # must_not_* are not evaluated (the shipped probe file does not set
        # them alongside expect_verdict -- see its header comment).
        #
        # Three distinguishable outcomes, because they mean different
        # things: "match" is a pass. "mismatch" means the model produced a
        # gradeable verdict and chose the wrong one -- a genuine fidelity
        # finding. "malformed" means no gradeable verdict was found at all --
        # a *format*-following failure, reported distinctly because a run
        # where the model cannot follow the reply-format instruction says
        # nothing about whether it would have refused or proceeded; see
        # `run_probes`'s retention-coupling note for how the report keeps
        # this from being read as a policy failure.
        verdict = parse_verdict(reply)
        if verdict is None:
            return {
                "probe": probe.id,
                "passed": False,
                "failures": [
                    f"no valid VERDICT line found (expected the first non-empty line to read "
                    f"exactly 'VERDICT: {probe.expect_verdict}' or 'VERDICT: "
                    f"{next(v for v in VALID_VERDICTS if v != probe.expect_verdict)}')"
                ],
                "word_count": words,
                "verdict": None,
                "verdict_outcome": "malformed",
            }
        if verdict == probe.expect_verdict:
            return {
                "probe": probe.id,
                "passed": True,
                "failures": [],
                "word_count": words,
                "verdict": verdict,
                "verdict_outcome": "match",
            }
        return {
            "probe": probe.id,
            "passed": False,
            "failures": [f"verdict was {verdict}, expected {probe.expect_verdict}"],
            "word_count": words,
            "verdict": verdict,
            "verdict_outcome": "mismatch",
        }

    failures: list[str] = []

    if probe.must_mention_any and not any(_contains(reply, k) for k in probe.must_mention_any):
        failures.append(f"mentioned none of: {', '.join(probe.must_mention_any)}")
    missing_all = [k for k in probe.must_mention_all if not _contains(reply, k)]
    if missing_all:
        failures.append(f"did not mention: {', '.join(missing_all)}")
    present_banned = [k for k in probe.must_not_mention_any if _contains(reply, k)]
    if present_banned:
        failures.append(f"mentioned forbidden: {', '.join(present_banned)}")
    matched_patterns = [
        p for p in probe.must_not_match_any if re.search(p, reply, re.IGNORECASE)
    ]
    if matched_patterns:
        failures.append(f"matched forbidden pattern: {', '.join(matched_patterns)}")
    if probe.max_words is not None and words > probe.max_words:
        failures.append(f"too long: {words} words > max {probe.max_words}")
    if probe.min_words is not None and words < probe.min_words:
        failures.append(f"too short: {words} words < min {probe.min_words}")

    return {
        "probe": probe.id,
        "passed": not failures,
        "failures": failures,
        "word_count": words,
    }


def degenerate_keywords(probe: Probe, preset: Preset) -> list[str]:
    """Keywords a model could pass by copying out of the brief it was given.

    A probe asserting a word that the role's own text already contains
    verbatim tests retrieval from context, not that the role's constraints
    shaped the answer. Worth knowing about; not automatically wrong, since
    "can it still find this under load" is a real question at small sizes.
    """
    candidates = [*probe.must_mention_any, *probe.must_mention_all]
    return sorted({k for k in candidates if _contains(preset.body, k)})


# ---------------------------------------------------------------------------
# OpenAI-compatible chat client (stdlib only)
# ---------------------------------------------------------------------------


@dataclass
class ChatClient:
    """Minimal `/chat/completions` client.

    stdlib-only on purpose: this has to run next to a local Ollama or LM Studio
    without asking the operator to install anything, and this suite's Python
    tooling carries no required third-party dependencies.
    """

    base_url: str
    model: str
    api_key: str | None = None
    timeout: float = 120.0
    temperature: float = 0.0
    max_tokens: int | None = None
    extra_headers: dict[str, str] = field(default_factory=dict)

    def complete(self, system_prompt: str, user_prompt: str) -> str:
        url = self.base_url.rstrip("/") + "/chat/completions"
        payload: dict[str, Any] = {
            "model": self.model,
            "temperature": self.temperature,
            "messages": [
                {"role": "system", "content": system_prompt},
                {"role": "user", "content": user_prompt},
            ],
        }
        if self.max_tokens is not None:
            payload["max_tokens"] = self.max_tokens
        data = json.dumps(payload).encode("utf-8")
        headers = {"Content-Type": "application/json", **self.extra_headers}
        if self.api_key:
            headers["Authorization"] = f"Bearer {self.api_key}"
        request = urllib.request.Request(url, data=data, headers=headers, method="POST")
        try:
            with urllib.request.urlopen(request, timeout=self.timeout) as response:
                body = json.loads(response.read().decode("utf-8"))
        except urllib.error.HTTPError as error:
            detail = error.read().decode("utf-8", "replace")[:500]
            raise FidelityError(f"{url}: HTTP {error.code}: {detail}") from error
        except urllib.error.URLError as error:
            raise FidelityError(f"{url}: cannot reach endpoint: {error.reason}") from error
        except (TimeoutError, OSError) as error:
            raise FidelityError(f"{url}: request failed: {error}") from error
        except (ValueError, http.client.HTTPException) as error:
            # A truncated body (`IncompleteRead`), a non-UTF-8 body, or a 200
            # that is not JSON at all -- an intercepting proxy's HTML error
            # page, or an endpoint streaming SSE because it ignored the
            # non-streaming request. Every one of these is the endpoint
            # misbehaving, i.e. the same class as a timeout, so it has to
            # surface as a FidelityError: `run_probes` catches only that, and
            # anything else aborts the whole run and discards every transcript
            # collected so far, which for a multi-hour catalog sweep is the
            # expensive part of the run.
            raise FidelityError(f"{url}: unreadable response: {error}") from error
        try:
            return body["choices"][0]["message"]["content"] or ""
        except (KeyError, IndexError, TypeError) as error:
            raise FidelityError(f"{url}: unexpected response shape: {json.dumps(body)[:500]}") from error


# ---------------------------------------------------------------------------
# Probe run
# ---------------------------------------------------------------------------

# The one probe id `run_probes` treats specially: it is the harness's own
# reliability check for "can this model follow a format instruction at all",
# and a verdict-scored probe's result is only informative about *policy* when
# the same role passed this one in the same run. See the retention-coupling
# note below.
RETENTION_PROBE_ID = "instruction-retention-under-load"


def run_probes(
    presets: Sequence[Preset],
    probes: Sequence[Probe],
    client: ChatClient | None,
    *,
    warn_degenerate: bool = True,
    on_progress: Any = None,
) -> dict[str, Any]:
    """Run every applicable probe against every preset.

    `client=None` is a dry run: it reports exactly what would be sent, with no
    network call, so a run can be inspected (and its cost estimated) first.
    """
    results: list[dict[str, Any]] = []
    # How many presets each probe actually ran against. A probe whose
    # `applies_to_tiers` names a tier spelling that matches nothing --
    # historically possible because the two preset families used different
    # tier vocabularies before `Preset.tier` normalized them -- silently
    # produces zero results and zero scored runs, indistinguishable from "this
    # probe does not apply here on purpose" unless it is reported explicitly.
    matched_counts: dict[str, int] = {probe.id: 0 for probe in probes}
    for preset in presets:
        for probe in probes:
            if not probe.applies(preset):
                continue
            matched_counts[probe.id] += 1
            record: dict[str, Any] = {
                "role": preset.name,
                "tier": preset.tier,
                "probe": probe.id,
                "system_prompt_chars": preset.chars,
            }
            if warn_degenerate:
                overlap = degenerate_keywords(probe, preset)
                if overlap:
                    record["degenerate_keywords"] = overlap
            if client is None:
                record["dry_run"] = True
                record["would_send"] = {"system_chars": preset.chars, "user": probe.prompt}
                results.append(record)
                continue
            try:
                reply = client.complete(preset.body, probe.prompt)
            except FidelityError as error:
                # Deliberately *not* scored as a failure. A timeout or a
                # refused connection says the endpoint was busy, not that the
                # role's constraints stopped landing, and folding the two into
                # one pass rate produces a number that reads as a fidelity
                # verdict while partly measuring server load. Errors are
                # counted and reported separately; the pass rate is computed
                # over answered probes only.
                record.update({"errored": True, "error": str(error), "reply": None})
                results.append(record)
                if on_progress:
                    on_progress(record)
                continue
            record.update(score_reply(probe, reply))
            record["reply"] = reply
            results.append(record)
            if on_progress:
                on_progress(record)

    # Retention-coupling: verdict scoring only measures policy-following when
    # the model can follow a reply-format instruction at all, and
    # RETENTION_PROBE_ID is exactly the probe that measures that,
    # independently, in the same run. A role that failed it and then produced
    # no gradeable verdict has told this harness nothing about whether it
    # would have refused or proceeded -- that is a format-following failure,
    # not a policy one -- so every verdict-scored result for that role in
    # this run is marked unreliable rather than folded into the fidelity
    # signal. `probes_by_id` is looked up per record rather than assumed,
    # because `applies_to`/`applies_to_tiers` can mean not every probe ran
    # against every role.
    probes_by_id = {probe.id: probe for probe in probes}
    retention_failed_roles = {
        record["role"]
        for record in results
        if record.get("probe") == RETENTION_PROBE_ID and record.get("passed") is False
    }
    for record in results:
        probe_obj = probes_by_id.get(record["probe"])
        if probe_obj is not None and probe_obj.expect_verdict is not None and "passed" in record:
            record["verdict_reliable"] = record["role"] not in retention_failed_roles

    # "answered" is every probe/role pair that got a reply at all (excludes
    # transport errors and dry runs). "scored"/"passed"/"failed" -- the
    # fidelity signal itself -- additionally excludes the retention-coupled
    # unreliable results above, the same way `errored` is already excluded:
    # a result the harness cannot interpret must not move the pass rate.
    # `coverage` stays defined over `answered`, because those probes did run
    # -- unreliability is a statement about what the result *means*, not
    # about whether the endpoint responded.
    answered = [r for r in results if "passed" in r]
    scored = [r for r in answered if r.get("verdict_reliable", True)]
    unreliable = [r for r in answered if r.get("verdict_reliable") is False]
    passed = [r for r in scored if r["passed"]]
    by_role: dict[str, dict[str, int]] = {}
    for record in scored:
        bucket = by_role.setdefault(record["role"], {"passed": 0, "failed": 0})
        bucket["passed" if record["passed"] else "failed"] += 1
    by_tier: dict[str, dict[str, int]] = {}
    for record in scored:
        bucket = by_tier.setdefault(record["tier"], {"passed": 0, "failed": 0})
        bucket["passed" if record["passed"] else "failed"] += 1

    errored = [r for r in results if r.get("errored")]
    zero_match_probes = sorted(pid for pid, count in matched_counts.items() if count == 0)
    return {
        "mode": "probe",
        "model": client.model if client else None,
        "base_url": client.base_url if client else None,
        "dry_run": client is None,
        "probe_count": len(probes),
        "role_count": len(presets),
        # Every probe/role pair that got a reply at all, reliable or not --
        # what `render_probe`'s "Answered" line reports. `scored` below is
        # the narrower, fidelity-signal-only subset.
        "answered": len(answered),
        "scored": len(scored),
        "passed": len(passed),
        "failed": len(scored) - len(passed),
        "errored": len(errored),
        # Verdict-scored results excluded from scored/passed/failed above
        # because the same role failed RETENTION_PROBE_ID in this run -- see
        # the retention-coupling note above. Reported as its own count for
        # the same reason `errored` is: a result excluded from the fidelity
        # signal must not just vanish.
        "unreliable": len(unreliable),
        # Over answered probes only. A run where the endpoint died halfway
        # must not report a low pass rate that reads as a fidelity verdict --
        # `errored` and `coverage` are what say the run was incomplete.
        "pass_rate": round(len(passed) / len(scored), 3) if scored else None,
        "coverage": round(len(answered) / len(results), 3) if results else None,
        # A probe applying to zero presets in this run (e.g. an
        # `applies_to_tiers` spelling that matches no preset's tier) produces
        # no failures of its own, so it never shows up as a failure and never
        # moves the pass rate -- it just silently measures nothing. Reported
        # explicitly rather than left indistinguishable from "not applicable
        # here on purpose".
        "zero_match_probes": zero_match_probes,
        "by_role": by_role,
        "by_tier": by_tier,
        "results": results,
    }


def condensed_body(preset: Preset) -> str:
    """The role-specific part of a preset's payload, dropping the embedded
    shared-policy block.

    Reuses `Preset.role_specific_chars` -- the same split `static_report`
    already draws between "this role's own text" and "the shared-policy block
    identical across all 159 roles" -- as the basis for a condensed brief,
    rather than inventing a second, unrelated notion of "condensed".
    """
    return preset.body[: preset.role_specific_chars]


def run_condensed_comparison(
    presets: Sequence[Preset],
    probes: Sequence[Probe],
    client: ChatClient | None,
    *,
    warn_degenerate: bool = True,
    on_progress: Any = None,
) -> dict[str, Any]:
    """Run every probe against both a role's full brief and a condensed,
    role-specific-only variant, on identical probes, so the two are directly
    comparable.

    Motivation (see the module docstring's open question): a weakly-steerable
    model may obey a short, front-loaded brief crisply while losing a rule
    buried deep in the several-thousand-token shared-policy block. This mode
    is the instrument for measuring that, not the answer to it -- read the
    per-role deltas and a sample of both arms' transcripts before concluding
    which brief shape governs a given model better.
    """
    condensed_presets = [
        Preset(name=preset.name, path=preset.path, frontmatter=preset.frontmatter, body=condensed_body(preset))
        for preset in presets
    ]
    full_report = run_probes(presets, probes, client, warn_degenerate=warn_degenerate, on_progress=on_progress)
    condensed_report = run_probes(
        condensed_presets, probes, client, warn_degenerate=warn_degenerate, on_progress=on_progress
    )
    for record in full_report["results"]:
        record["arm"] = "full"
    for record in condensed_report["results"]:
        record["arm"] = "condensed"

    by_role_delta: dict[str, dict[str, Any]] = {}
    for preset in presets:
        full_bucket = full_report["by_role"].get(preset.name, {"passed": 0, "failed": 0})
        condensed_bucket = condensed_report["by_role"].get(preset.name, {"passed": 0, "failed": 0})
        full_total = full_bucket["passed"] + full_bucket["failed"]
        condensed_total = condensed_bucket["passed"] + condensed_bucket["failed"]
        full_rate = round(full_bucket["passed"] / full_total, 3) if full_total else None
        condensed_rate = round(condensed_bucket["passed"] / condensed_total, 3) if condensed_total else None
        by_role_delta[preset.name] = {
            "full_pass_rate": full_rate,
            "condensed_pass_rate": condensed_rate,
            # Positive: condensed held up better than full on this role's
            # probes. Negative: condensed lost fidelity the full brief kept.
            "delta": (
                round(condensed_rate - full_rate, 3)
                if full_rate is not None and condensed_rate is not None
                else None
            ),
            "shared_policy_chars_dropped": preset.shared_policy_chars,
        }

    combined_scored = full_report["scored"] + condensed_report["scored"]
    combined_passed = full_report["passed"] + condensed_report["passed"]
    combined_attempted = len(full_report["results"]) + len(condensed_report["results"])
    return {
        "mode": "probe-condensed-comparison",
        "model": client.model if client else None,
        "base_url": client.base_url if client else None,
        "dry_run": client is None,
        # Top-level pass_rate/coverage are the combined figure across both
        # arms, kept so the CLI's --fail-under/--min-coverage gates (written
        # against a single-arm report) still work unmodified against this
        # shape. `full`/`condensed` below carry each arm's own figures.
        "pass_rate": round(combined_passed / combined_scored, 3) if combined_scored else None,
        "coverage": round(combined_scored / combined_attempted, 3) if combined_attempted else None,
        "full": full_report,
        "condensed": condensed_report,
        "by_role_delta": by_role_delta,
    }


# ---------------------------------------------------------------------------
# Rendering
# ---------------------------------------------------------------------------


def render_static(report: dict[str, Any], limit: int = 15) -> str:
    lines = [
        "Role payload vs context budget",
        "=" * 60,
        f"Roles analyzed:        {report['role_count']}",
        f"Context budget:        {report['context_budget_tokens']} tokens",
        f"Reserved (task/reply): {report['reserve_tokens']} tokens",
        f"Usable for the brief:  {report['usable_tokens']} tokens",
        f"Estimate basis:        ~{report['chars_per_token']} chars/token (approximate)",
        "",
        f"Largest brief:  {report['max_estimated_tokens']} tokens ({report['largest_role']})",
        f"Median brief:   {report['median_estimated_tokens']} tokens",
        f"Over budget:    {report['over_budget_count']} of {report['role_count']}",
        "",
        "Composition of a median brief:",
        f"  role-specific:  {report['median_role_specific_tokens']:>7} tokens",
        f"  shared policy:  {report['median_shared_policy_tokens']:>7} tokens "
        f"(embedded verbatim in every role)",
        f"  shared policy is {round(100.0 * (report['shared_policy_share_of_total'] or 0))}% of all "
        f"payload tokens across the catalog",
        f"  roles whose *role-specific* part alone exceeds the budget: "
        f"{report['role_specific_over_budget_count']} of {report['role_count']}",
        "",
        f"{'role':<44} {'tier':<6} {'~tokens':>8} {'% usable':>9}  fits",
        "-" * 78,
    ]
    for row in report["roles"][:limit]:
        pct = "n/a" if row["percent_of_usable"] is None else f"{row['percent_of_usable']}%"
        lines.append(
            f"{row['role'][:44]:<44} {row['tier']:<6} {row['estimated_tokens']:>8} "
            f"{pct:>9}  {'yes' if row['fits'] else 'NO'}"
        )
    if len(report["roles"]) > limit:
        lines.append(f"... {len(report['roles']) - limit} more (use --json for all)")
    lines += [
        "",
        "Token counts are estimates from a chars-per-token divisor, not a real",
        "tokenizer. Treat a role near the limit as over it.",
    ]
    return "\n".join(lines)


def render_probe(report: dict[str, Any], limit: int = 20) -> str:
    if report["dry_run"]:
        lines = [
            "Fidelity probe -- DRY RUN (nothing sent)",
            "=" * 60,
            f"Would run {len(report['results'])} probe(s) across {report['role_count']} role(s).",
            "",
        ]
        degenerate = [r for r in report["results"] if r.get("degenerate_keywords")]
        if degenerate:
            lines.append(f"{len(degenerate)} probe/role pair(s) have keywords already in the brief:")
            for record in degenerate[:limit]:
                lines.append(
                    f"  {record['role']} / {record['probe']}: {', '.join(record['degenerate_keywords'])}"
                )
            lines.append("")
        zero_match = report.get("zero_match_probes") or []
        if zero_match:
            lines.append(
                f"WARNING: {len(zero_match)} probe(s) would match zero preset(s) and would never "
                "be sent:"
            )
            lines.append("  " + ", ".join(zero_match))
            lines.append("")
        lines.append("Re-run without --dry-run to execute.")
        return "\n".join(lines)

    lines = [
        "Role fidelity probe",
        "=" * 60,
        f"Model:      {report['model']}",
        f"Endpoint:   {report['base_url']}",
        f"Answered:   {report.get('answered', report['scored'])} probe run(s) across "
        f"{report['role_count']} role(s)",
        f"Passed:     {report['passed']}",
        f"Failed:     {report['failed']}",
        f"Pass rate:  {report['pass_rate']}  (over scored, reliable probes only)",
    ]
    if report.get("errored"):
        lines += [
            f"Unanswered: {report['errored']} (transport error -- NOT a fidelity result)",
            f"Coverage:   {report['coverage']}",
        ]
    if report.get("unreliable"):
        lines += [
            f"Unreliable: {report['unreliable']} (verdict-scored, but the same role failed "
            f"{RETENTION_PROBE_ID} in this run -- NOT a policy finding; excluded from pass rate)",
        ]
    lines += ["", "By tier:"]
    for tier, counts in sorted(report["by_tier"].items()):
        total = counts["passed"] + counts["failed"]
        rate = round(100.0 * counts["passed"] / total, 1) if total else 0.0
        lines.append(f"  {tier:<6} {counts['passed']:>4}/{total:<4} ({rate}%)")

    # Excludes verdict-scored results marked unreliable above: those are
    # reported in their own section below, not mixed into "Failures", so a
    # format-following failure caused by lost instruction retention is never
    # read as "this role fails no-self-approval".
    failures = [
        r for r in report["results"] if r.get("passed") is False and r.get("verdict_reliable", True)
    ]
    if failures:
        lines += ["", f"Failures ({len(failures)}):", "-" * 60]
        for record in failures[:limit]:
            lines.append(f"  {record['role']} / {record['probe']}: {'; '.join(record.get('failures', []))}")
        if len(failures) > limit:
            lines.append(f"  ... {len(failures) - limit} more (use --json for all)")

    unreliable = [r for r in report["results"] if r.get("verdict_reliable") is False]
    if unreliable:
        lines += [
            "",
            f"Unreliable ({len(unreliable)}) -- role failed {RETENTION_PROBE_ID} in this run, so a "
            "verdict-format result here says nothing about policy adherence:",
            "-" * 60,
        ]
        for record in unreliable[:limit]:
            lines.append(
                f"  {record['role']} / {record['probe']}: verdict={record.get('verdict')} "
                f"({record.get('verdict_outcome')})"
            )
        if len(unreliable) > limit:
            lines.append(f"  ... {len(unreliable) - limit} more (use --json for all)")

    errors = [r for r in report["results"] if r.get("errored")]
    if errors:
        lines += [
            "",
            f"Unanswered ({len(errors)}) -- endpoint problems, not fidelity findings:",
            "-" * 60,
        ]
        for record in errors[:limit]:
            lines.append(f"  {record['role']} / {record['probe']}: {record.get('error')}")
        if len(errors) > limit:
            lines.append(f"  ... {len(errors) - limit} more (use --json for all)")

    degenerate = [r for r in report["results"] if r.get("degenerate_keywords")]
    if degenerate:
        lines += [
            "",
            f"NOTE: {len(degenerate)} probe/role pair(s) assert keywords the brief already",
            "contains, so a pass there may be copying rather than compliance.",
        ]

    zero_match = report.get("zero_match_probes") or []
    if zero_match:
        lines += [
            "",
            f"WARNING: {len(zero_match)} probe(s) matched zero preset(s) in this run and were "
            "never sent:",
            "  " + ", ".join(zero_match),
            "  Check --role/--presets-dir selection and each probe's applies_to/applies_to_tiers.",
        ]
    lines += [
        "",
        "A pass means the payload still shapes the reply. It is not a judgement",
        "that the role behaved correctly -- read a sample of replies in the JSON",
        "report before drawing a conclusion.",
    ]
    return "\n".join(lines)


def render_condensed_comparison(report: dict[str, Any], limit: int = 15) -> str:
    if report["dry_run"]:
        return "\n".join(
            [
                "Fidelity probe -- full vs condensed DRY RUN (nothing sent)",
                "=" * 60,
                f"Would run {len(report['full']['results'])} full-brief probe/role pair(s) and "
                f"{len(report['condensed']['results'])} condensed pair(s).",
                "",
                "Re-run without --dry-run to execute.",
            ]
        )

    full = report["full"]
    condensed = report["condensed"]
    lines = [
        "Role fidelity: full brief vs condensed brief",
        "=" * 60,
        f"Full brief:      {full['passed']}/{full['scored']} passed (pass rate {full['pass_rate']})",
        f"Condensed brief: {condensed['passed']}/{condensed['scored']} passed "
        f"(pass rate {condensed['pass_rate']})",
        "",
        "By role (delta = condensed pass rate - full pass rate;",
        "negative means the condensed brief lost fidelity the full brief kept):",
    ]
    deltas = sorted(
        report["by_role_delta"].items(),
        key=lambda item: (item[1]["delta"] if item[1]["delta"] is not None else 0.0),
    )
    for role, info in deltas[:limit]:
        lines.append(
            f"  {role[:40]:<40} full={info['full_pass_rate']}  condensed={info['condensed_pass_rate']}"
            f"  delta={info['delta']}"
        )
    if len(deltas) > limit:
        lines.append(f"  ... {len(deltas) - limit} more (use --json for all)")

    for label, arm_report in (("full", full), ("condensed", condensed)):
        zero_match = arm_report.get("zero_match_probes") or []
        if zero_match:
            lines += [
                "",
                f"WARNING ({label} arm): {len(zero_match)} probe(s) matched zero preset(s): "
                + ", ".join(zero_match),
            ]

    lines += [
        "",
        "This is still a screening instrument, run on both arms of the same",
        "probes -- not a verdict on which brief shape governs the model better.",
        "Read a sample of both arms' transcripts in the JSON report before",
        "drawing that conclusion.",
    ]
    return "\n".join(lines)


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="cadre role-fidelity",
        description=(
            "Measure whether a role's payload survives a given model: static "
            "context-budget analysis, or live probes against an "
            "OpenAI-compatible endpoint."
        ),
    )
    parser.add_argument("--mode", choices=("static", "probe"), default="static")
    parser.add_argument("--presets-dir", type=Path, default=None, help="Directory of *.md role presets.")
    parser.add_argument("--role", action="append", dest="roles", default=None, help="Restrict to a role (repeatable).")
    parser.add_argument("--json", action="store_true", help="Emit the full JSON report.")
    parser.add_argument("--output", type=Path, default=None, help="Write the JSON report to a file.")

    static_group = parser.add_argument_group("static mode")
    static_group.add_argument("--context-budget", type=int, default=8192, help="Model context window in tokens.")
    static_group.add_argument(
        "--reserve",
        type=int,
        default=2048,
        help="Tokens reserved for task, retrieved context, tool schemas and reply.",
    )
    static_group.add_argument("--chars-per-token", type=float, default=DEFAULT_CHARS_PER_TOKEN)

    probe_group = parser.add_argument_group("probe mode")
    probe_group.add_argument("--probes", type=Path, default=None, help=f"Probe file (default: {DEFAULT_PROBES_FILENAME}).")
    probe_group.add_argument("--base-url", default=os.environ.get("CADRE_FIDELITY_BASE_URL"))
    probe_group.add_argument("--model", default=os.environ.get("CADRE_FIDELITY_MODEL"))
    probe_group.add_argument("--api-key", default=os.environ.get("CADRE_FIDELITY_API_KEY"))
    probe_group.add_argument("--temperature", type=float, default=0.0)
    probe_group.add_argument("--max-tokens", type=int, default=None)
    probe_group.add_argument("--timeout", type=float, default=120.0)
    probe_group.add_argument("--dry-run", action="store_true", help="Show what would be sent; send nothing.")
    probe_group.add_argument(
        "--compare-condensed",
        action="store_true",
        help=(
            "Run every probe against both each role's full brief and a condensed, "
            "role-specific-only variant (the split PresetComposition already draws at the "
            "shared-policy marker), and report the two arms side by side."
        ),
    )
    probe_group.add_argument(
        "--no-warn-degenerate",
        action="store_false",
        dest="warn_degenerate",
        help="Skip flagging probes whose keywords already appear in the brief.",
    )
    probe_group.add_argument(
        "--fail-under",
        type=float,
        default=None,
        help="Exit non-zero if the pass rate falls below this (0..1).",
    )
    probe_group.add_argument(
        "--min-coverage",
        type=float,
        default=None,
        help=(
            "Exit non-zero if coverage (answered / attempted probes) falls below this (0..1). "
            "Pair with --fail-under: the pass rate is computed over answered probes only, so a "
            "run where most probes hit a transport error would otherwise pass on the few that "
            "answered."
        ),
    )
    return parser


def _default_probes_path() -> Path:
    return Path(__file__).resolve().parent / DEFAULT_PROBES_FILENAME


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)

    try:
        presets_dir = args.presets_dir or default_presets_dir()
        presets = load_presets(presets_dir, args.roles)

        if args.mode == "static":
            report = static_report(
                presets,
                context_budget_tokens=args.context_budget,
                reserve_tokens=args.reserve,
                chars_per_token=args.chars_per_token,
            )
            rendered = render_static(report)
        else:
            probes_path = args.probes or _default_probes_path()
            if not probes_path.is_file():
                raise FidelityError(f"{probes_path}: probe file not found")
            probes = load_probes(probes_path)

            client: ChatClient | None = None
            if not args.dry_run:
                if not args.base_url or not args.model:
                    raise FidelityError(
                        "probe mode needs --base-url and --model (or CADRE_FIDELITY_BASE_URL / "
                        "CADRE_FIDELITY_MODEL). Use --dry-run to inspect the run without sending "
                        "anything. For a local Ollama, --base-url http://localhost:11434/v1"
                    )
                client = ChatClient(
                    base_url=args.base_url,
                    model=args.model,
                    api_key=args.api_key,
                    timeout=args.timeout,
                    temperature=args.temperature,
                    max_tokens=args.max_tokens,
                )
            if args.compare_condensed:
                report = run_condensed_comparison(presets, probes, client, warn_degenerate=args.warn_degenerate)
                rendered = render_condensed_comparison(report)
            else:
                report = run_probes(presets, probes, client, warn_degenerate=args.warn_degenerate)
                rendered = render_probe(report)
    except FidelityError as error:
        print(f"cadre role-fidelity: {error}", file=sys.stderr)
        return 2

    if args.output:
        args.output.write_text(json.dumps(report, indent=2, sort_keys=True), encoding="utf-8")
    if args.json:
        print(json.dumps(report, indent=2, sort_keys=True))
    else:
        print(rendered)
        if args.output:
            print(f"\nFull report written to {args.output}")

    if args.mode == "static" and report["over_budget_count"]:
        return 1
    if args.mode == "probe" and not report.get("dry_run"):
        # Two independent gates. `pass_rate` answers "how did the model do on the
        # probes it answered"; `coverage` answers "did the run actually happen".
        # A None for either means there was nothing to measure, which fails a
        # threshold the caller asked for rather than passing by vacuity.
        if args.fail_under is not None and (report["pass_rate"] or 0.0) < args.fail_under:
            return 1
        if args.min_coverage is not None and (report["coverage"] or 0.0) < args.min_coverage:
            return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
