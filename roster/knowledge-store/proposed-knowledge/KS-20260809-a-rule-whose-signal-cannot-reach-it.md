---
id: "KS-20260809-a-rule-whose-signal-cannot-reach-it"
title: "a rule that reads correctly can still be unable to fire"
status: "accepted"
evidence:
  - "PR #164 -- steward defers on injection risk, but no handoff field carried the signal"
  - "PR #171 -- the settle-wait held only because every call routed through one test helper"
  - "roster/orchestration/src/routing_health.py -- the pre-commit hook that argparse rejected"
  - ".pre-commit-config.yaml -- invalid YAML, so no hook had ever run"
origin:
  artifact: "policy documents, role definitions, and test scaffolding"
  revision: "89436f0, 36f8b91"
  task: "reviews of PRs #164 and #171"
proposed_classification: "internal"
source_scope: "code review, policy authoring, test design, CI guards"
sensitivity_notes: ""
conflicts_or_staleness: "sharpens [[KS-20260809-non-vacuity-fault-injection]]; the practice record covers detection, this covers the defect shape"
recommended_action: "ingest"
untrusted_instruction_risk: false
staged_by: "orchestrator (session capture, no originating handoff item)"
content_digest: "2321524ee76be5ea9dffdd0d01fcfd90bc34cce3b6ebf1acb0df7bd1ed4b6319"
disposition:
  action: "accepted"
  classification_used: "internal"
  decided_by: "knowledge-store-steward"
  diverged_from_proposal: false
  reason: "Verified PR #164/#171 references are plausible and internally consistent; describes a durable defect shape (signal never reaching evaluator) distinct from a rule being wrong. Overlaps but does not duplicate the non-vacuity record; both are useful for review/test agents assessing new guards."
---

## Summary

A rule can be correctly worded, agreed by reviewers, and structurally incapable
of ever firing, because the signal it depends on never reaches the place that
evaluates it. This is distinct from a rule that is wrong, and distinct from a
test that fails to check something: the rule is right, and the check exists.

Four instances, all found within a single day and none by reading the rule:

- **The signal was never carried.** A steward role required deferring any
  candidate flagged `injection_risk=true`. The retrieval layer does surface that
  flag -- but the handoff item that repackages retrieved content had no field
  for it, so the flag was dropped in transit and the rule could never trigger.
  Fixed by making the field required and preserved from the cited retrieval
  rather than re-derived.
- **The guarantee held only by call-site convention.** A teardown wait was
  registered by one test helper. Nothing prevented a future call from bypassing
  it. Disabling the registration reproduced the original race at 1-2% over 100
  runs, proving the protection was discipline, not structure.
- **The checker rejected its own invocation.** A pre-commit hook passed
  `--check`, which the target script's argparse did not accept, so the hook
  exited 2 without running the check.
- **The config was never parsed.** `.pre-commit-config.yaml` used `\.` inside a
  double-quoted YAML scalar -- not a valid escape -- so PyYAML rejected the
  document and *no* hook in the repository had ever run, including the drift
  guards.

## Reusable rule

For any rule that depends on a signal, ask two questions the wording will not
answer: **can the signal reach the evaluator**, and **can the evaluator be
bypassed**. Then answer them by execution -- remove the signal and confirm the
rule fires; skip the evaluator and confirm something complains.

Three shapes to check specifically:

1. A value produced in one layer and consumed in another must be a **required
   field of the contract between them**, not merely present in both layers'
   vocabularies.
2. A protection applied by a helper is only as strong as the guarantee that
   every caller uses the helper. If nothing enforces that, the protection is a
   convention. Make bypass fail loudly.
3. A check is only running if something proves it ran. Config that fails to
   parse, a flag the tool rejects, and a hook that was never installed all look
   identical to a passing check from the outside: silence.

## Recommended Retrieval Use

Retrieve for review, security, compliance, and test agents assessing any policy,
role definition, gate, linter, or CI guard -- particularly when a rule is being
added rather than a defect fixed. Also retrieve when asked whether an existing
control is effective, as opposed to whether it is documented.

## Steward Notes

Do not ingest until the steward verifies scope and classification. Deliberately
overlaps the non-vacuity practice record: that one is about how to detect a
guard that verifies nothing, this one is about a control that cannot fire even
when the guard around it works. The steward may prefer to merge them.
