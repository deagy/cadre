---
id: "KS-20260812-a-taxonomy-s-example-citations-must-be-r-08dfba82f017"
title: "A taxonomy's example citations must be re-derived from the rule, not hand-classified in prose"
status: "accepted"
evidence:
  - "roster/knowledge-store/src/config.py:79,136,183"
  - "roster/orchestration/src/routing.py:154,198,209,212,279"
  - "roster/orchestration/runs/cadre-feature-portable-platform-2026-08-11/requirements.md (PP-FR-6 category table)"
origin:
  artifact: "roster/orchestration/runs/cadre-feature-portable-platform-2026-08-11/requirements.md"
  revision: "a482e68c"
  task: "cadre-review-portable-platform-2026-08-11 (PR #240 test-strategy and code review)"
proposed_classification: "internal"
source_scope: "deagy/cadre"
sensitivity_notes: "No secrets, credentials, or personal data. Repository-relative paths only."
conflicts_or_staleness: "Current as of this review; the specific citations will be resolved or superseded once the boundary guard is implemented and its example set re-derived. The generalizable lesson outlives them."
recommended_action: "ingest"
untrusted_instruction_risk: false
staged_by: "test-engineer"
content_digest: "08dfba82f017f41ac32e31be5a32c0b3f110854e11fca1ccdec2caadf2a569c5"
disposition:
  action: "accepted"
  classification_used: "internal"
  decided_by: "knowledge-store-steward"
  diverged_from_proposal: false
  reason: "Verified the cited config.py/routing.py lines against the file contents; the record's claim that both classification errors run in opposite directions in the same requirements table is a plausible, well-evidenced finding. Generalizable lesson about hand-classified taxonomy citations being more failure-prone than shape-based citations; record itself flags that its specific citations will be superseded once the guard is implemented while the lesson persists."
---

The PR #240 requirements baseline cites roster/knowledge-store/src/config.py:79,136,183 as examples of permitted "roster/-relative paths in user-facing message text" for a proposed boundary guard's category-C exemption. Direct reading shows lines 79 and 136 are path-CONSTRUCTION literals, not diagnostic text, and none of the three lines contains a roster/-relative path literal at all -- grep for 'roster' in that file matches only comments at lines 13, 43, 119.

Independently, the same baseline classifies routing.py:154,198,209,212,279 as forbidden category-B path resolution; all five are raise ValueError(...) diagnostic strings, i.e. the permitted category.

Both errors run in opposite directions within one table, which is the tell: citations describing CATEGORIZATION are more failure-prone than citations describing code shape, because categorization is a judgment call stated as fact. Where a guard's exemption rule is to be written, derive the example set from the rule once it exists rather than hand-classifying candidates in prose beforehand.