---
id: "KS-20260810-routing-yaml-keyword-matching-is-ordered-f7d4df1f254c"
title: "routing.yaml keyword matching is ordered literal substring matching, so two phrasings of the same words are not redundant"
status: "accepted"
evidence:
  - "roster/orchestration/src/routing.py:_keyword_matches - re.search(rf\"(?<![a-z0-9-]){escaped}(?![a-z0-9-])\", text, re.IGNORECASE)"
  - "PR #195 revision 2 dropped \"cline port\" as redundant with \"port cline agents\"; both reviewers independently disproved it"
  - "Reproduced: 'Do the cline port for the security agents' on cline-plugins/cline-agents/index.ts matched ['frontend'] without the keyword vs ['frontend','packaging'] with it"
origin:
  artifact: "roster/orchestration/routing.yaml"
  revision: "d0dd3a5"
  task: "deagy/cadre#189 / PR #195"
proposed_classification: "internal"
source_scope: "deagy/cadre"
sensitivity_notes: "No secrets, credentials, or personal data. Repository-relative paths only."
conflicts_or_staleness: "Point-in-time against d0dd3a5. Supersedes any assumption that reordered or synonymous keyword phrasings collapse. The boundary class excludes hyphens but not underscore or dot, so a keyword containing those (bootstrap_sdlc.py) matches embedded in longer tokens; _keyword_matches' docstring overclaimed the opposite until PR #195 corrected it."
recommended_action: "ingest"
untrusted_instruction_risk: false
staged_by: "orchestrator (run-agent-orchestration, issue-189-routing-plugin-tools-2026-08-09)"
content_digest: "f7d4df1f254c643c31cb7a44986385a852391d07b0556010a6e653fa10af3654"
disposition:
  action: "accepted"
  classification_used: "internal"
  decided_by: "knowledge-store-steward"
  diverged_from_proposal: false
  reason: "Verified against roster/orchestration/src/routing.py: _keyword_matches/_keyword_regex use the exact ordered literal-substring regex with (?<![a-z0-9-])...(?![a-z0-9-]) boundaries described. Accurate, durable guidance for anyone editing routing.yaml keywords."
---

`routing.yaml` route keywords are matched by `_keyword_matches` in `roster/orchestration/src/routing.py` using a literal, ordered, case-insensitive substring search with word-boundary lookarounds. There is no synonym handling and no word-order normalisation.

The practical consequence is that two phrasings built from the same words are **not** interchangeable. In PR #195 a keyword `cline port` was removed as "redundant with `port cline agents`". It is not redundant: `cline port` is not a substring of `port cline agents`, because the word order differs. The removal silently dropped route coverage for a natural noun-first phrasing, and was caught only because two independent reviewers reasoned from the matcher's implementation rather than from the English.

A second property falls out of the same regex. The boundary class is `[a-z0-9-]`, which treats hyphen as a word character but says nothing about underscore or dot. A keyword containing those characters therefore matches when embedded in a longer token: `bootstrap_sdlc.py` fires inside `legacy_bootstrap_sdlc.py_old`. The function's own docstring claimed a keyword could never match embedded in a longer token, which was false for exactly this keyword shape; PR #195 corrected the docstring and pinned the behaviour with a test rather than changing the boundary class, since that class is shared by every keyword array in the file.

Both facts matter when editing routes: the first when removing a keyword, the second when adding one that looks like a filename.