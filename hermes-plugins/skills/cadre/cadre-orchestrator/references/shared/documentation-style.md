# Documentation Style: Concision

Principle-based guidance for report and document brevity. No numeric caps
(no word counts, no line limits) — apply judgment against these principles
instead.

- Lead with the conclusion, decision, or change. Restate context only if the
  reader needs it to act.
- Do not restate inputs, the task, or process narrative the reader already
  has.
- When a required section does not materially apply, omit it or state "not
  applicable" in one line — do not fill it with boilerplate or empty
  scaffolding.
- Scale disclosure detail to the size and risk of the change. A trivial
  change does not owe the same shape as a high-risk one — but every
  materially applicable required field still appears in full.
- Prefer short declarative sentences over clause-stacked sentences that pack
  multiple independent facts together.
- Never cut these regardless of size or risk — compress the prose around
  them, never the fields themselves:
  - Audit-trail fields: actor, inputs, decision, evidence, approvals,
    timestamps, resulting artifact identifiers (`operating-principles.md`).
  - Citation and provenance fields (`knowledge-use-policy.md`).
  - Rejected-alternative detail in decision records.
  - Evidence-integrity fields (`evidence-curator/AGENT.md`).
  - Human-gate and approval-status disclosures.
  - Assumption and unresolved-question labeling.

This file governs presentation and proportionality only. It does not
override any inclusion requirement in `operating-principles.md` or any other
shared policy — where a field is required, it stays required; this file
only controls how much surrounding prose accompanies it.
