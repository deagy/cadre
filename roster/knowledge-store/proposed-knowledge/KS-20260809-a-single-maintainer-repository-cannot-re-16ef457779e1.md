---
id: "KS-20260809-a-single-maintainer-repository-cannot-re-16ef457779e1"
title: "a single-maintainer repository cannot require pull-request approvals"
status: "accepted"
evidence:
  - "repos/deagy/cadre/collaborators -- one login"
  - "repos/deagy/cadre/rulesets/19841068 -- bypass_actors empty"
  - "roster/knowledge-store/proposed-knowledge/ -- 15 required checks after the 2026-08-09 correction"
  - "PR #147 -- release signing gated behind an approved environment"
origin:
  artifact: "deagy/cadre main ruleset"
  revision: "e72b50e"
  task: "branch-protection review, 2026-08-09"
proposed_classification: "internal"
source_scope: "repository governance, release engineering, CI configuration"
sensitivity_notes: "names the repository and its sole collaborator's role, no credentials or local paths"
conflicts_or_staleness: "describes the ruleset as of 2026-08-09; re-verify collaborator count and bypass_actors before relying on it, since adding a maintainer changes the conclusion"
recommended_action: "ingest"
untrusted_instruction_risk: false
staged_by: "orchestrator"
content_digest: "16ef457779e1f285ef28128c5a9e393c103f54cf4484cfb6da98157d8dcf4913"
disposition:
  action: "accepted"
  classification_used: "internal"
  decided_by: "knowledge-store-steward"
  diverged_from_proposal: false
  reason: "States a general, verifiable GitHub mechanic (self-approval is impossible) applied honestly to this repository's actual single-collaborator state, with an explicit re-verification caveat if the collaborator count changes. Sensitivity reviewed: names only the repository and role, no credentials. Valuable for anyone reasoning about this repo's branch-protection posture."
---

## Summary

GitHub does not permit approving your own pull request. In a repository with
one collaborator and no bypass actors, setting `required_approving_review_count`
to 1 therefore leaves **no merge path at all** — not stricter review, just a
locked repository, or a standing habit of disabling the rule to get work
through.

This was checked rather than assumed: `repos/OWNER/REPO/collaborators` returned
a single login, and the ruleset's `bypass_actors` was empty.

## What protection is actually achievable

On `deagy/cadre` as of 2026-08-09, the `main` ruleset enforces:

- deletion blocked
- non-fast-forward (force-push) blocked
- pull request required
- 15 required status checks, matching what CI actually runs

and `required_approving_review_count: 0`, deliberately.

A gap worth knowing separately: the required-checks list had drifted from the
CI matrix. Three install-script legs ran on every pull request but were absent
from the required set, so they could fail without blocking a merge — which
quietly undid the point of adding them. Required checks and the workflow's job
list are two lists that drift apart silently; compare them rather than assuming
they match.

## The claim this constrains

Any statement that a change is gated on human review is false here. `main` is
protected against force-push, deletion, and red CI — not against an unreviewed
change. A release-signing gate, or any control justified by "an approval gate
only means something if the branch itself is protected", inherits that limit and
should say so rather than claiming protection unqualified.

## The options, if a human gate is ever wanted

- **Add a second collaborator.** The only version that delivers real review.
- **Bypass actor plus approvals: 1.** Prompts on others' pull requests while the
  owner retains a merge path — decorative for the owner's own changes.
- **Leave it**, and describe the protection accurately.

## Reusable rule

Before recommending a branch-protection setting, check the collaborator list and
`bypass_actors`. A protection rule that cannot be satisfied is not stricter than
no rule; it is a rule that trains people to bypass it.