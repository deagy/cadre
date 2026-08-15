#!/usr/bin/env python3
"""Differential probe for `--format text` and `--explain`.

    python3 roster/orchestration/test/probe_text_parity.py

Three layers, narrowest first, because a disagreement in the first would
produce confusing noise in the third:

1. `textwrap.fill` itself, over a wide input space. Go has no equivalent, so
   this is a reimplementation rather than a translation -- and every line
   break in the text rendering comes from it. An approximation would look
   plausible and differ on every long line.
2. `format_plan_text` over real plans, generated from the differential
   corpus, plus hand-built plans for the shapes the corpus does not reach
   (needs-triage, human gates, teams, non-ASCII).
3. `find_near_misses` + `format_near_misses_text`, against synthetic routes,
   because -- as route_near_miss.py's own docstring notes -- no entry under
   `routes:` in the current routing.json declares `keyword_groups`, so a real
   invocation legitimately reports nothing at all.
"""

from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
import textwrap
from pathlib import Path

REPOSITORY_ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(REPOSITORY_ROOT / "roster" / "orchestration" / "src"))

from plan_text_format import format_plan_text  # noqa: E402
from route_near_miss import find_near_misses, format_near_misses_text  # noqa: E402

CORPUS_PATH = Path(__file__).parent / "select_corpus.json"
CADRE = REPOSITORY_ROOT / "bin" / "cadre"


def run_go_probe(name: str, payload: object, prefix: str, workspace: Path) -> list:
    input_path = workspace / f"{prefix.lower()}-in.json"
    output_path = workspace / f"{prefix.lower()}-out.json"
    input_path.write_text(json.dumps(payload), encoding="utf-8")

    result = subprocess.run(
        ["go", "test", "./internal/selector/", "-run", name, "-count=1", "-v"],
        cwd=REPOSITORY_ROOT, capture_output=True, text=True,
        env={**os.environ, "CGO_ENABLED": "1",
             f"CADRE_{prefix}_PROBE_IN": str(input_path),
             f"CADRE_{prefix}_PROBE_OUT": str(output_path)},
    )
    if result.returncode != 0:
        sys.stderr.write(result.stdout + result.stderr)
        raise SystemExit(f"go probe {name} failed")
    return json.loads(output_path.read_text(encoding="utf-8"))


# --- 1. textwrap ---------------------------------------------------------

WRAP_TEXTS = [
    "",
    "short",
    "a b c",
    "   leading whitespace is dropped on a continuation line",
    "trailing whitespace   ",
    "one  two   three    four",
    # Hyphenated role ids: the whole reason break_on_hyphens is off. A default
    # fill would split these into two non-existent roles.
    "kubernetes-manifest-implementer bare-metal-provisioning-implementer "
    "network-management-automation-implementer quantum-network-integration-implementer",
    # A single token longer than the width, which must overrun rather than be cut.
    "supercalifragilisticexpialidociousandthensomemoreletterstopushitwellpastthelimit",
    "short then " + "supercalifragilisticexpialidociousandthensomemoreletterstopushpastlimit",
    "a " * 60,
    "tabs\tand\tmore\ttabs expand before whitespace is flattened",
    "newlines\nbecome\nspaces",
    "carriage\rreturn and \x0bvertical tab and \x0cform feed",
    # Non-ASCII: Python measures width in characters, Go would measure bytes
    # without an explicit rune count, wrapping early on any accented text.
    "café " * 30,
    "日本語のテキストが折り返される場合の挙動を確認するための長い文字列です " * 3,
    "🚀 " * 40,
    "exactly-at-the-boundary " * 4,
    "The dispatch plan names a primary agent, its reviewers, and any support "
    "roles, together with the workflow the change follows and the gates it "
    "must clear before it can ship to a production environment.",
]

WRAP_CASES = []
for text in WRAP_TEXTS:
    for width, initial, subsequent in [
        (78, "", ""),
        (78, "  ", "  "),
        (78, "", " " * 13),
        (78, "    ", "    "),
        (40, "", ""),
        (20, "", "  "),
        (10, "", ""),
        (1, "", ""),
    ]:
        WRAP_CASES.append({"text": text, "width": width, "initial": initial, "subsequent": subsequent})


def python_wrap_answers() -> list[str]:
    return [
        textwrap.fill(
            case["text"], width=case["width"],
            initial_indent=case["initial"], subsequent_indent=case["subsequent"],
            break_on_hyphens=False, break_long_words=False,
        )
        for case in WRAP_CASES
    ]


# --- 2. format_plan_text -------------------------------------------------

def corpus_plans() -> list[dict]:
    """Real plans, produced by the Python selector for every corpus case."""
    cases = json.loads(CORPUS_PATH.read_text(encoding="utf-8"))["cases"]
    plans = []
    for case in cases:
        completed = subprocess.run(
            [str(CADRE), "select",
             "--task", case["task"], "--files", case["files"],
             "--classification", case["classification"], "--task-id", case["id"].upper(),
             "--source", "deagy/cadre", "--source", "proposed-knowledge"],
            capture_output=True, text=True, timeout=180, cwd=REPOSITORY_ROOT,
            env={k: v for k, v in os.environ.items() if k != "CADRE_SELECT_IMPL"},
        )
        if completed.returncode != 0:
            raise SystemExit(f"corpus case {case['id']} failed:\n{completed.stderr}")
        plans.append(json.loads(completed.stdout))
    return plans


def synthetic_plans() -> list[dict]:
    """Shapes the corpus does not reach, and the ones a formatter gets wrong."""
    return [
        # Empty. Every .get falls back; nothing may raise.
        {},
        # needs-triage: structurally valid, every agent list empty. The case a
        # JSON skim misreads as success.
        {"status": "needs-triage", "task_id": "T-1", "workflow": "unclassified",
         "inputs": {"task": "something unroutable", "changed_files": []},
         "agents": {"primary": [], "reviewers": [], "support": []},
         "dispatch_disposition": {"status": "needs-triage",
                                  "reason": "No route or risk rule matched this task, so no agent was selected."}},
        # needs-triage with no reason at all, exercising the default text.
        {"status": "needs-triage", "agents": {}},
        # A staffed plan with an empty reviewers slot, which must read
        # "(none)" rather than a bare dash.
        {"status": "ok", "task_id": "T-2", "workflow": "new-service",
         "inputs": {"task": "add a handler", "changed_files": ["a.go"]},
         "agents": {"primary": ["backend-engineer"], "reviewers": [], "support": []}},
        # More than five changed files, so the "(+N more)" tail fires.
        {"status": "ok", "task_id": "T-3",
         "inputs": {"task": "wide change", "changed_files": [f"file{n}.go" for n in range(12)]},
         "agents": {"primary": ["backend-engineer"]}},
        # Long hyphenated role lists that must wrap without splitting an id.
        {"status": "ok", "task_id": "T-4",
         "inputs": {"task": "big", "changed_files": []},
         "agents": {"primary": ["kubernetes-manifest-implementer", "network-observability-implementer"],
                    "reviewers": ["supply-chain-security-reviewer", "pipeline-security-reviewer",
                                  "infrastructure-reviewer", "compliance-reviewer"],
                    "support": ["quantum-network-integration-implementer",
                                "bare-metal-provisioning-implementer",
                                "network-management-automation-implementer"]}},
        # Human gates: the one part of a plan that is never advisory.
        {"status": "ok", "task_id": "T-5", "agents": {"primary": ["release-engineer"]},
         "human_gates": [
             {"id": "production-release", "required": True,
              "reason": "A production deployment is a decision no agent may make on its own, "
                        "and it requires an authorized human approver who did not author the change."},
             {"id": "not-required", "required": False, "reason": "should be filtered out"},
             {"id": "no-reason-given"},
         ],
         "required_quality_gates": [{"id": "G5", "required": True}, {"id": "G6", "required": False}]},
        # Teams, matched routes with reasons, context packs, fingerprint.
        {"status": "ok", "task_id": "T-6", "workflow": "infrastructure-change",
         "inputs": {"task": "change the cluster", "changed_files": ["k8s/deploy.yaml"]},
         "agents": {"primary": ["kubernetes-manifest-implementer"], "reviewers": ["infrastructure-reviewer"]},
         "teams": [{"id": "parallel-review", "type": "fixed", "communication_mode": "peer",
                    "members": ["infrastructure-reviewer", "security-reviewer"]},
                   {"id": "no-mode"}],
         "matched_routes": [
             {"id": "infra", "reasons": {"keywords": ["cluster", "deploy", "kubernetes", "manifest", "rollout"],
                                         "paths": [{"pattern": "k8s/**"}, {"pattern": "charts/**"},
                                                   {"pattern": "terraform/**"}]}},
             {"id": "grouped", "reasons": {"keyword_groups": [["deploy"], ["prod"]]}},
             {"id": "no-reasons"},
             {"id": "empty-reasons", "reasons": {}},
         ],
         "matched_risks": [{"id": "production-change"}, {"no-id": True}],
         "context_packs": [{"id": "secure-cloud"}, "bare-string-pack", {"no": "id"}],
         "dispatch_fingerprint": "sha256:" + "0" * 64},
        # Non-ASCII throughout: width is measured in characters, not bytes.
        {"status": "ok", "task_id": "TÂCHE-7",
         "inputs": {"task": "café " * 40, "changed_files": ["café.tsx", "日本語.md"]},
         "agents": {"primary": ["backend-engineer"]},
         "matched_routes": [{"id": "unicode", "reasons": {"keywords": ["café", "日本語", "🚀", "naïve"]}}]},
        # A task that is only whitespace, so the trimmed-empty branch fires.
        {"status": "ok", "task_id": "T-8", "inputs": {"task": "   \n\t  "},
         "agents": {"primary": ["backend-engineer"]}},
    ]


# --- 3. near misses ------------------------------------------------------

NEAR_MISS_CONFIGS = [
    # No routes at all.
    {"why": "no routes", "config": {"routes": []}, "task": "anything", "matched": []},
    # A route with no keyword_groups is never a near miss: plain keywords and
    # paths have no partial-match state.
    {"why": "plain keywords only", "config": {"routes": [{"id": "plain", "keywords": ["deploy"]}]},
     "task": "deploy the thing", "matched": []},
    # Partially satisfied: the one case that is surfaced.
    {"why": "one group partially satisfied",
     "config": {"routes": [{"id": "partial", "keyword_groups": [["deploy", "production"]]}]},
     "task": "deploy the service", "matched": []},
    # Fully satisfied group means the route matched -- contradiction, omitted.
    {"why": "group fully satisfied",
     "config": {"routes": [{"id": "full", "keyword_groups": [["deploy", "service"]]}]},
     "task": "deploy the service", "matched": []},
    # Zero-of-N is noise, omitted.
    {"why": "group at zero",
     "config": {"routes": [{"id": "zero", "keyword_groups": [["nothing", "here"]]}]},
     "task": "deploy the service", "matched": []},
    # Already matched routes are skipped entirely.
    {"why": "route already matched",
     "config": {"routes": [{"id": "partial", "keyword_groups": [["deploy", "production"]]}]},
     "task": "deploy the service", "matched": ["partial"]},
    # Several groups on one route, several routes.
    {"why": "multiple groups and routes",
     "config": {"routes": [
         {"id": "first", "keyword_groups": [["deploy", "production"], ["rollback", "revert"]]},
         {"id": "second", "keyword_groups": [["deploy", "canary"]]},
         {"id": "third", "keywords": ["unrelated"]},
     ]},
     "task": "deploy and rollback the service", "matched": []},
    # Keyword boundaries: hyphen is a word character for keyword matching, so
    # "re-deploy" does not contain the keyword "deploy".
    {"why": "hyphen is a word character in keyword matching",
     "config": {"routes": [{"id": "boundary", "keyword_groups": [["deploy", "production"]]}]},
     "task": "re-deploy to production", "matched": []},
    {"why": "multi-word keyword",
     "config": {"routes": [{"id": "phrase", "keyword_groups": [["blue green deploy", "production"]]}]},
     "task": "a blue green deploy", "matched": []},
    {"why": "case is normalised",
     "config": {"routes": [{"id": "case", "keyword_groups": [["DEPLOY", "PRODUCTION"]]}]},
     "task": "Deploy The Service", "matched": []},
    {"why": "non-ascii keyword",
     "config": {"routes": [{"id": "unicode", "keyword_groups": [["café", "naïve"]]}]},
     "task": "the café is open", "matched": []},
]


def compare(label: str, expected: list, actual: list, whys: list[str]) -> int:
    differences = 0
    for index, (left, right) in enumerate(zip(expected, actual)):
        if left == right:
            continue
        differences += 1
        why = whys[index] if index < len(whys) else ""
        print(f"\n  DIFFERS [{label} #{index}] {why}")
        print(f"    python: {left!r}"[:600])
        print(f"    go:     {right!r}"[:600])
    print(f"  {label}: {len(expected)} cases, {len(expected) - differences} identical, {differences} differing")
    return differences


def main() -> int:
    differences = 0
    with tempfile.TemporaryDirectory() as workspace:
        space = Path(workspace)

        print("1. textwrap.fill")
        expected = python_wrap_answers()
        actual = run_go_probe("TestTextwrapParityProbe", WRAP_CASES, "WRAP", space)
        whys = [f"width={c['width']} initial={c['initial']!r} text={c['text'][:40]!r}" for c in WRAP_CASES]
        differences += compare("textwrap", expected, actual, whys)

        print("\n2. format_plan_text")
        plans = corpus_plans() + synthetic_plans()
        expected = [format_plan_text(plan) for plan in plans]
        actual = run_go_probe("TestPlanTextParityProbe", plans, "PLANTEXT", space)
        whys = [f"plan task_id={p.get('task_id')!r}" for p in plans]
        differences += compare("plan text", expected, actual, whys)

        print("\n3. near misses")
        expected = [
            format_near_misses_text(find_near_misses(case["config"], case["task"], set(case["matched"])))
            for case in NEAR_MISS_CONFIGS
        ]
        payload = [{"task": c["task"], "matched": c["matched"], "config": c["config"]} for c in NEAR_MISS_CONFIGS]
        actual = run_go_probe("TestNearMissParityProbe", payload, "NEARMISS", space)
        differences += compare("near misses", expected, actual, [c["why"] for c in NEAR_MISS_CONFIGS])

    print()
    if differences:
        print(f"FAIL: {differences} differing cases")
        return 1
    print("OK: all identical")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
