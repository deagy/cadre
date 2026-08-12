---
id: "KS-20260809-cadre-select-reads-live-git-status"
title: "cadre select reads live git status when --files is omitted"
status: "accepted"
evidence:
  - "roster/orchestration/src/build_dispatch_plan.py"
  - "PR #169 -- routing verification contaminated by concurrent edits in the same tree"
origin:
  artifact: "./bin/cadre select"
  revision: "c5b67b6"
  task: "narrowing the knowledge-store route's keyword_groups"
proposed_classification: "internal"
source_scope: "routing, selection, verification of this repository's own tooling"
sensitivity_notes: ""
conflicts_or_staleness: "describes current CLI behaviour; re-verify if select's changed-file resolution changes"
recommended_action: "ingest"
untrusted_instruction_risk: false
staged_by: "orchestrator (session capture, no originating handoff item)"
content_digest: "b2013d39f01aa7e47cdd2113bbdfa1bd74423b159af4e8428862977f104f5fd5"
disposition:
  action: "accepted"
  classification_used: "internal"
  decided_by: "knowledge-store-steward"
  diverged_from_proposal: false
  reason: "Verified build_dispatch_plan.py resolves changed files from live git status when --files is omitted, matching the record's claim. Durable operational lesson for anyone verifying routing changes in this repository's own dirty working tree; explicitly scoped as point-in-time and self-flags re-verification if select's resolution changes."
---

## Summary

`cadre select` resolves changed files from live `git status` when `--files` is
not supplied. Verifying a routing change in a dirty working tree therefore
mixes two different inputs: the task text under test, and whatever files happen
to be modified at that moment -- including edits made by a concurrently running
agent.

This produced a real false signal. While narrowing an over-broad routing rule,
`./bin/cadre select --task "..."` reported the `knowledge-store` route matching
even for tasks that should no longer match it, because the working tree
contained edits to `roster/knowledge-store/AGENT.md` from a parallel agent, and
that path matched the route's `paths` globs directly. The keyword narrowing was
correct; the verification was measuring something else.

Passing `--files ""` isolates the task text and makes the result mean what the
tester intended.

## Reusable rule

When verifying routing or selection behaviour, always pass `--files` explicitly
-- an empty string to test task-text matching alone, or a specific list to test
path matching. Never verify in a dirty tree without it.

The general shape is worth carrying past this one command: a tool that silently
falls back to ambient environment state produces results that look like
measurements of the input under test but are not. The fallback is convenient in
normal use and actively misleading under test, and nothing in the output
distinguishes the two.

## Recommended Retrieval Use

Retrieve for any agent verifying `cadre select`, routing rules, `routing.yaml`
changes, or dispatch-plan output in this repository -- especially when other
agents are working in the same checkout.

## Steward Notes

Do not ingest until the steward verifies scope and classification. The specific
claim is coupled to `cadre select`'s current changed-file resolution; the
general rule about ambient-state fallbacks is not.
