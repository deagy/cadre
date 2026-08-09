---
id: KS-20260104-fixture-awkward
title: "quotes \" colons: backslashes \\ and unicode — éü中"
status: proposed
evidence:
  - "roster/orchestration/src/routing.py:35"
  - "https://example.invalid/a?b=c#d"
origin:
  task: "a task with: a colon"
  artifact: "- looks like a list item"
  revision: "null"
proposed_classification: internal
source_scope: "true"
sensitivity_notes: "  leading and trailing spaces  "
conflicts_or_staleness: "~"
recommended_action: reclassify
untrusted_instruction_risk: unknown
staged_by: fixture-author
content_digest: f3439c5307bdbe80961faad4c22437f35e7ebe3ee5204487d8cbc1a7925019e0
---

## Summary

Every serialisation hazard in one record: quotes, colons, backslashes,
unicode, values that look like YAML keywords, and whitespace that must
survive verbatim.

A trailing code fence, because bodies are Markdown:

```
  indented   text
```
