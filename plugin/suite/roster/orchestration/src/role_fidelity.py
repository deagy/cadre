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
import json
import os
import re
import sys
import urllib.error
import urllib.request
from dataclasses import dataclass, field
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
        return self.frontmatter.get("modelTier") or self.frontmatter.get("model") or "unset"

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
        missing = sorted(wanted - {p.name for p in presets})
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
    max_words: int | None = None
    min_words: int | None = None

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
        probe = Probe(
            id=probe_id,
            prompt=prompt,
            description=str(entry.get("description") or ""),
            applies_to=_as_tuple(entry.get("applies_to")),
            applies_to_tiers=_as_tuple(entry.get("applies_to_tiers")),
            must_mention_any=_as_tuple(entry.get("must_mention_any")),
            must_mention_all=_as_tuple(entry.get("must_mention_all")),
            must_not_mention_any=_as_tuple(entry.get("must_not_mention_any")),
            max_words=int(max_words) if max_words is not None else None,
            min_words=int(min_words) if min_words is not None else None,
        )
        if not any(
            (
                probe.must_mention_any,
                probe.must_mention_all,
                probe.must_not_mention_any,
                probe.max_words is not None,
                probe.min_words is not None,
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
    failures: list[str] = []
    words = len(reply.split())

    if probe.must_mention_any and not any(_contains(reply, k) for k in probe.must_mention_any):
        failures.append(f"mentioned none of: {', '.join(probe.must_mention_any)}")
    missing_all = [k for k in probe.must_mention_all if not _contains(reply, k)]
    if missing_all:
        failures.append(f"did not mention: {', '.join(missing_all)}")
    present_banned = [k for k in probe.must_not_mention_any if _contains(reply, k)]
    if present_banned:
        failures.append(f"mentioned forbidden: {', '.join(present_banned)}")
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
        try:
            return body["choices"][0]["message"]["content"] or ""
        except (KeyError, IndexError, TypeError) as error:
            raise FidelityError(f"{url}: unexpected response shape: {json.dumps(body)[:500]}") from error


# ---------------------------------------------------------------------------
# Probe run
# ---------------------------------------------------------------------------


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
    for preset in presets:
        for probe in probes:
            if not probe.applies(preset):
                continue
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
                record.update({"passed": False, "error": str(error), "reply": None})
                results.append(record)
                if on_progress:
                    on_progress(record)
                continue
            record.update(score_reply(probe, reply))
            record["reply"] = reply
            results.append(record)
            if on_progress:
                on_progress(record)

    scored = [r for r in results if "passed" in r]
    passed = [r for r in scored if r["passed"]]
    by_role: dict[str, dict[str, int]] = {}
    for record in scored:
        bucket = by_role.setdefault(record["role"], {"passed": 0, "failed": 0})
        bucket["passed" if record["passed"] else "failed"] += 1
    by_tier: dict[str, dict[str, int]] = {}
    for record in scored:
        bucket = by_tier.setdefault(record["tier"], {"passed": 0, "failed": 0})
        bucket["passed" if record["passed"] else "failed"] += 1

    return {
        "mode": "probe",
        "model": client.model if client else None,
        "base_url": client.base_url if client else None,
        "dry_run": client is None,
        "probe_count": len(probes),
        "role_count": len(presets),
        "scored": len(scored),
        "passed": len(passed),
        "failed": len(scored) - len(passed),
        "pass_rate": round(len(passed) / len(scored), 3) if scored else None,
        "by_role": by_role,
        "by_tier": by_tier,
        "results": results,
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
        lines.append("Re-run without --dry-run to execute.")
        return "\n".join(lines)

    lines = [
        "Role fidelity probe",
        "=" * 60,
        f"Model:      {report['model']}",
        f"Endpoint:   {report['base_url']}",
        f"Scored:     {report['scored']} probe run(s) across {report['role_count']} role(s)",
        f"Passed:     {report['passed']}",
        f"Failed:     {report['failed']}",
        f"Pass rate:  {report['pass_rate']}",
        "",
        "By tier:",
    ]
    for tier, counts in sorted(report["by_tier"].items()):
        total = counts["passed"] + counts["failed"]
        rate = round(100.0 * counts["passed"] / total, 1) if total else 0.0
        lines.append(f"  {tier:<6} {counts['passed']:>4}/{total:<4} ({rate}%)")

    failures = [r for r in report["results"] if r.get("passed") is False]
    if failures:
        lines += ["", f"Failures ({len(failures)}):", "-" * 60]
        for record in failures[:limit]:
            detail = record.get("error") or "; ".join(record.get("failures", []))
            lines.append(f"  {record['role']} / {record['probe']}: {detail}")
        if len(failures) > limit:
            lines.append(f"  ... {len(failures) - limit} more (use --json for all)")

    degenerate = [r for r in report["results"] if r.get("degenerate_keywords")]
    if degenerate:
        lines += [
            "",
            f"NOTE: {len(degenerate)} probe/role pair(s) assert keywords the brief already",
            "contains, so a pass there may be copying rather than compliance.",
        ]
    lines += [
        "",
        "A pass means the payload still shapes the reply. It is not a judgement",
        "that the role behaved correctly -- read a sample of replies in the JSON",
        "report before drawing a conclusion.",
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
        if args.fail_under is not None and (report["pass_rate"] or 0.0) < args.fail_under:
            return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
