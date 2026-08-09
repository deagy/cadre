<!-- GENERATED FILE: edit the canonical source and regenerate; do not edit this copy. -->

# Proposal: make durable-knowledge capture a checked artifact, not a prose obligation

Status: **PROPOSED — not approved, not scheduled.**
Task ID: `durable-knowledge-capture-2026-08-09`
Classification: internal
Author role: governance-planner (Cadre suite)
Requested by: repository owner / declared Product Owner
(`roster/shared/team-profile.yaml`)
Required approver: Product Owner. Nothing here is approved by its presence in
this file.

Related: PR [#164](https://github.com/deagy/cadre/pull/164) ("Add knowledge
steward handoffs"), which introduces the governance *shape* this proposal
depends on and should land — with corrections — before any of this.

## The problem, stated as an observation rather than a theory

Over recent sessions this repository has produced a steady stream of findings
that are durable in exactly the sense `roster/shared/knowledge-use-policy.md`
means: they generalize past the change that surfaced them, and a future agent
working in the same area would be materially better off knowing them. A
representative sample, all from review work on PRs #161 and #163:

- A soundness argument ran backwards. A routing check reported a glob as fully
  shadowed when *sampled* probe paths were all excluded — but the finding is a
  universal claim, so an incomplete sample makes it strictly *easier* to
  satisfy. Incomplete sampling produced false accusations, not missed ones.
- A differential test passed with its subject entirely disabled.
- A case-folding regression was invisible to all fifty tests covering the
  module it lived in.
- `glob_to_regex` compiles `**` to `.` (which excludes `\n`) but `*` to `[^/]`
  (which includes it), and `$` matches before a single trailing newline — three
  matcher asymmetries that any future work on the glob dialect must model.

None of these are recorded anywhere an agent can retrieve them. Each survived
only because it was written into a pull-request description by whoever happened
to be orchestrating at the time. The knowledge store — the mechanism built for
precisely this — has never received one.

The dominant failure mode this catalogue keeps rediscovering is *a guard that
passes while verifying nothing*, found roughly a dozen times in a single
session. That is the single most valuable thing this repository knows about
itself, and it is currently transmitted by narrative.

## Why PR #164 alone does not close it

#164 adds the right governance shape: agents **propose** durable candidates in
a `knowledge_steward_handoff`, and `knowledge-store-steward` accepts, rejects,
or defers each one. Proposal is separated from approval, consistent with the
authorship/approval invariant that holds everywhere else in this workspace.
That separation is correct and this proposal assumes it.

What #164 does not do is make the obligation checkable. As merged-candidate
content stands (verified by review on branch `codex/review-cline-shortcomings`):

| Gap | Evidence |
| --- | --- |
| Named two ways, two shapes | `knowledge_steward_handoff` (singular, `none` when empty) in four documents; `knowledge_steward_handoffs: []` (plural list) in `dispatch-contract.md:81`, 35 lines from the singular use at `:46` |
| Specified four ways | Full 8-field list in `knowledge-use-policy.md`, `task-brief-template.md`, `knowledge-store/AGENT.md`; `handoff-contracts.md` drops title and summary; `dispatch-contract.md` — the document that builds the actual dispatch prompt — compresses it to "evidence and classification/scope metadata", dropping five fields |
| Enforced nowhere | Zero matches for `knowledge_steward_handoff` in any `.py` file; nothing in `selection.schema.json` |
| No destination | The steward must record accept/reject/defer with reasons; no location, schema, or `evidence-curator` integration is defined |
| No exit | `delete when supported` names a capability that does not exist (`cli.py` exposes `init/ingest/search/context/stats`; `SECURITY.md:36` says so plainly) |
| Not dischargeable | Every agent must propose a classification; `roster/engineering/frontend-engineer/AGENT.md` does not reference `knowledge-use-policy.md` at all, and no file under `roster/shared/` enumerates classification levels |

An obligation stated four different ways, under two different names, that
nothing parses, will be satisfied nominally. This repository's own recurring
finding predicts the outcome.

## The asset that already exists and is wired to nothing

The single sample record committed under
`roster/knowledge-store/proposed-knowledge/` (a compose-runtime-lessons note;
not named here because `test_sample_references_are_limited_to_allowed_archives`
forbids sample identifiers outside allowlisted archive paths) is a complete,
well-formed proposed-knowledge record. It carries `Status:`,
`Classification:`, `Source task:`, a summary, a "Recommended Retrieval Use"
section naming which agents should retrieve it, and explicit `Steward Notes`
withholding ingestion pending review. It is exactly the artifact #164 describes
in prose.

It is referenced from one place in the entire repository:
`pyproject.toml:185`, which *excludes* the directory from the wheel, commenting
that it is a "dev-only/generated-at-runtime" path. The packaging already treats
`proposed-knowledge/` as a runtime landing zone. Nothing generates into it,
nothing validates it, and no role's `AGENT.md` mentions it.

The convention exists. It is unwired, unschematized, and undocumented.

## Proposal

Four changes, in dependency order. Each is independently shippable and each
leaves the repository better than it found it, so the sequence can stop at any
point without stranding work.

### 1. A typed record, validated in CI

Give `roster/knowledge-store/proposed-knowledge/` a schema —
`proposed-knowledge.schema.json`, alongside the existing
`roster/orchestration/routing.schema.json` and `selection.schema.json` — and a
test that validates every file in the directory against it.

Fields, taken from the union of #164's four divergent statements and reconciled
to one shape (recommend the plural list `knowledge_steward_handoffs`, empty
list meaning none, matching the sibling keys `findings: []`, `human_gates: []`
in the same result block):

`title`, `summary`, `evidence` (citations or file:line references),
`origin` (task ID, artifact, revision), `proposed_classification`,
`source_scope`, `sensitivity_notes`, `conflicts_or_staleness`,
`recommended_action` (one of `ingest`/`update`/`reclassify`/`defer` — see §4 on
`delete`), and `status` (`proposed`/`accepted`/`rejected`/`deferred`).

This converts "agents should mention durable findings" into "a file either
conforms or fails the build", which is the difference between a documented
guarantee and a checked one. The precedent is established: `routing.schema.json`
is enforced by `schema_validate.py` and a pre-commit hook today.

**Non-goal:** validating that an agent *emitted* a handoff. A handoff is
free-form agent output, not a generated artifact, and no CI job can check for
the absence of something an agent chose not to say. What CI *can* check is that
every record which does exist is well-formed. Claiming more than that would
reproduce the exact failure mode this proposal exists to address.

### 2. Emit at review time, not only at handoff time

Every finding in the opening list arrived as a review agent's report. That is
the natural capture point: reviews already produce structured, evidenced,
severity-ranked findings, and the orchestrator already reads them.

Add to the review workflow a step that writes qualifying findings into
`proposed-knowledge/` as schema-conforming records — one file per finding,
named `<TASK-ID>-<slug>.md`. "Qualifying" means the finding generalizes beyond
the change under review; a defect in one function does not qualify, but "a
differential test can pass with its subject disabled, so validate the witness"
does.

This is the highest-leverage step and the one most likely to be skipped,
because it is the only one that changes what agents *do* rather than what
documents *say*.

### 3. A steward disposition that is itself a file

`roster/knowledge-store/AGENT.md` (as amended by #164) requires the steward to
record accept/reject/defer with reasons. Give that record a home: the same file,
amended in place — `status` transitions from `proposed`, and a `Steward Notes`
section carries the reason and any missing evidence or authorization. Cross-
reference it from `roster/documentation/evidence-curator/AGENT.md` so the
audit obligation has an indexable artifact, matching how
`roster/workflows/knowledge-ingestion.md` step 7 already wires the bulk path.

Two corrections belong here, both from the #164 review:

- **Redaction.** `evidence` and `origin` are the same leak class as
  `source_uri`, which the repo already redacts by default because it can expose
  local paths (`knowledge-use-policy.md:12`, `task-brief-template.md:50`).
  Extend that rule verbatim to the new fields.
- **Classification divergence must be visible.** The steward's disposition
  should state the classification actually used and whether it diverged from
  the agent's proposal, so rubber-stamping is distinguishable from judgment.
  `SECURITY.md:42` already establishes that caller flags are not authentication;
  that caveat now applies one hop earlier.

### 4. Deletion before more inflow

Do not scale ingestion into a store with no working exit. Either implement
authorized lifecycle operations (`SECURITY.md:36` names this as a prerequisite
for production use), or remove `delete when supported` from the recommended
actions until it exists and add an explicit escalation path for "an accepted
record later requires deletion and none exists."

Recording a promise against a capability that does not exist is worse than
recording nothing: it creates a retention obligation with no discharge.

## What this proposal deliberately does not do

- **No automatic ingestion.** Nothing here lets an agent write to the knowledge
  store. `proposed-knowledge/` is a staging directory; only the steward
  ingests. `roster/shared/agent-autonomy.yaml`'s
  `ingest_update_reclassify_or_delete: knowledge_store_steward_only` is
  unchanged and this proposal depends on it.
- **No claim that the injection loop is closed.** Untrusted content an agent
  read can still reach a proposed record and, if the steward accepts it, later
  reach another agent's prompt. `protect_content()` flags `injection_risk` but
  does not block ingestion, and this proposal does not change that. The mitigation
  remains the steward's judgment; the honest framing is that this proposal makes
  that judgment *auditable*, not automatic. Recommend the steward treat
  `injection_risk=true` on a handoff-originated candidate as an automatic
  `defer`.
- **No scoring, ranking, or confidence on a record.** Consistent with the
  standing rejection of numeric confidence in routing (`CLAUDE.md`: selection is
  deterministic, not agent judgment), a record is durable or it is not; the
  steward decides.

## Decisions taken

Both were open questions in the first draft of this proposal and were settled by
the Product Owner on 2026-08-09. They are recorded here so a later reader does
not reopen them as oversights.

### The store grows a second ingest shape

`normalize_file` (`normalize.py:125`) expects chat-export conversation shapes: it
derives canonical messages via `_messages_of`/`_canonical_message`, keyed on
`role`, `content`, and per-message identifiers. A knowledge record is not a
conversation and forcing one into that mould would mean inventing a synthetic
`conversation_id` and a single pseudo-message, which corrupts the citation
semantics that `knowledge-use-policy.md` depends on — `message_id` and
`chunk_id` would point at a fiction.

**Decision:** the store gains a second, first-class input shape for structured
knowledge records, rather than the steward converting records into chat-export
form at ingestion time. Conversion-at-ingestion was the alternative considered
and rejected: it would place a lossy, hand-performed transformation between the
validated record and what is actually stored, so the artifact CI checks (§1)
would not be the artifact retrieved.

Consequences to work through when §1 is scoped:

- `normalize_file` needs a shape discriminator, and the record shape needs its
  own canonicalization producing stable `content_hash` values.
- `--source` semantics: records originate in this repository's own review work,
  not an external export. `_enforce_ingest_scope` (KS-FR-10..12) requires an
  explicit `--source` at the shared global-fallback tier; records need a
  documented source identifier rather than inheriting the `chat-export` default.
- Retrieval must keep records and chat passages distinguishable, since the
  untrusted-content rules differ in emphasis: a curated record has passed steward
  review, a chat passage has not. Neither becomes trusted instruction.

This is the largest piece of work in the proposal and the step most likely to
need its own design pass.

### Motivating findings are written up retroactively

**Decision:** the findings in the opening section are captured as records under
`roster/knowledge-store/proposed-knowledge/`, at `Status: proposed`, awaiting
steward disposition like any other candidate. They are not ingested by this
work and no approval is implied by their existence.

Writing them first is deliberate sequencing rather than a shortcut: they are the
only real corpus available, so the schema in §1 should be derived from records
that already exist rather than designed against hypothetical ones. If the schema
cannot express these findings cleanly, that is a defect in the schema, and it is
cheaper to discover before the schema is enforced in CI than after.

## Verification

Once §1 lands:

```sh
python3 -m unittest discover -s roster/knowledge-store/test -p "test_*.py"
python3 roster/orchestration/src/schema_validate.py
```

The non-vacuity bar this repository has settled on applies to the new
validator itself, and is not optional given the history above: corrupt a
committed record (drop a required field, set an invalid `recommended_action`),
confirm the check **fails** with a message naming the real problem, revert, and
confirm the tree is clean. A schema guard that passes against a malformed
record is another entry in the list this proposal opens with.
