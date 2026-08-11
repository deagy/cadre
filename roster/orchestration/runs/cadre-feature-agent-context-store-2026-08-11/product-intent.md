# Product Intent Record — Agent context store for working-context management

**Intent ID:** `INTENT-CADRE-CONTEXT-STORE-001`
**Revision:** 1 (initial)
**Status:** draft — awaiting human Product Owner review at G1. This is an intake record only. Nothing in it is an approval, a priority decision, a design commitment, or an authorization to build.
**Author (agent):** authored directly in-session against the `product-intake.md` workflow shape; not a dispatched `product-intent-agent` run.
**Date:** 2026-08-11
**Repository:** `/home/deagy/sdk/cadre`, revision `7cd2712c5ece2d00bbe004ee22a9601f1692743d`
**Classification:** internal
**Source:** human feature request, supplied verbatim in-session: *"feature intake: either an extension of the existing knowledge store, or a sibling-store that agents use to store and retrieve from as needed as a means of managing context usage."* Four scope questions were then answered by the requester in the same session; their answers are recorded in Section 4 and are the reason this record's scope is as wide as it is.

---

## 1. Owner

**Accountable Product Owner:** not named. Every prior intent record under
`roster/orchestration/runs/` reports the same gap — there is no standing
Product Owner designated for this repository's own feature intake, and
`roster/shared/team-profile.yaml` names Daniel Eagy as Product Owner only for
one specific, unrelated compliance-scope decision (2026-07-26). Logged as OD-1
rather than assumed.

**Requester:** the human who opened this intake and answered the four scope
questions. Requesting is not approving; the scope answers in Section 4 are
inputs to this record, not a G1 decision on it.

## 2. Users / beneficiaries

- **Any agent dispatched from this roster that produces or consumes a large
  intermediate** — a full-file read, a test-run log, a findings table, a
  diff analysis — and today has no choice but to carry it in its own context
  window or lose it.
- **Orchestrators consolidating a multi-agent dispatch**, who under
  `roster/orchestration/handoff-contracts.md` receive every member's complete
  findings, evidence, citations, and assumptions inline, and must hold all of
  them simultaneously to consolidate.
- **Callers of the MCP dispatch surface** (`roster/orchestration/mcp/dispatch_core.py`),
  whose child results return as captured stdout pinned into the parent's
  result dict — a mechanism with a hard cap and no overflow path other than
  refusal or truncation.
- **Agents re-entering work across sessions**, who currently reconstruct prior
  state by re-reading the same files, because nothing carries forward except
  what a human pastes back in or what a steward has accepted into the
  knowledge store.
- **The `knowledge-store-steward` role**, whose disposition queue is the only
  existing agent-to-durable-storage path, and which is not designed to absorb
  high-churn working material — see the central tension in Section 9.

## 3. Problem statement (WHAT and WHY)

**What exists today, verified by reading the code and policy, not assumed from the request:**

- **The knowledge store is not a working surface and was not built to be one.**
  `roster/knowledge-store/` is a durable corpus of authorized historical/chat
  context: semantic top-k retrieval (`--top` constrained to 1–20 by
  orchestration policy), exact-match classification filtering, `--source`
  scoping enforced structurally at the global-fallback tier, retention windows,
  and evidenced irreversible deletion. `roster/shared/knowledge-use-policy.md`
  states the write rule plainly: *"Ordinary agents may not mutate content or
  lifecycle state… only the knowledge-store steward may approve ingestion,
  reclassification, correction, retention, or deletion."*
- **The one agent-write path is deliberately a proposal queue, not a write.**
  An agent that discovers durable knowledge emits `knowledge_steward_handoffs`
  in its handoff; the orchestrator stages it with `cadre knowledge propose`;
  `knowledge-store-steward` dispositions it. Staging is explicitly *"neither
  ingestion nor approval"* (`handoff-contracts.md`). The queue is designed for
  a handful of durable lessons per run, with per-record fields (`evidence`,
  `origin`, `proposed_classification`, `sensitivity_notes`,
  `conflicts_or_staleness`, `untrusted_instruction_risk`, `content_digest`).
  It is the wrong shape and the wrong cost for parking a 40 KB intermediate an
  agent wants back in four turns.
- **The handoff contract pushes payloads up, not down.** `handoff-contracts.md`
  requires every materially applicable field in full — findings with evidence
  and severity, inputs examined, outputs produced, assumptions, unresolved
  questions, citations, trace links — and states that proportionality "never
  excuses dropping an audit-trail, citation, evidence-integrity,
  approval-status, or assumption/unresolved-question field once that field is
  applicable." This is correct as an auditability contract and is precisely
  what makes consolidation context-expensive: completeness is mandatory and
  there is no by-reference option.
- **Dispatch has a cap, not an overflow path.** `dispatch_core.py` captures the
  child's stdout and pins it into the parent's result dict; the module already
  reasons carefully about caps elsewhere (`_read_role_file_capped` refuses
  rather than silently truncating a role file, on the grounds that a truncated
  body is "a correctness and safety problem"). The same reasoning has no
  equivalent remedy for child output that legitimately exceeds what the parent
  should hold — the content has nowhere else to go.
- **Run directories are the informal answer today.** `roster/orchestration/runs/<slug>/`
  holds committed artifacts (`product-intent.md`, `requirements.md`,
  `fidelity-baseline.md`, …) written ad hoc per run. There is no addressing
  convention, no lifecycle, no expiry, no tooling, and no way for a dispatched
  agent to discover or write one as part of its normal loop.
- **There is already a precedent for labeling agent-adjacent content as
  untrusted on the way back in.** `wrap_untrusted_output()` fences a dispatched
  child's stdout with a random per-call token so the child's own output cannot
  forge the closing fence. This is the closest existing analogue to the trust
  problem a context store creates (Section 9, OD-6).

**Desired outcome, stated without over-resolving the request:** an agent should
be able to park working material outside its own context window and get it back
later — by handle when it knows what it stored, by search when it does not —
without that material entering the curated knowledge corpus, and without
weakening the steward gate, the untrusted-data rule, or authorship/approval
separation.

## 4. Scope

The requester was asked four scoping questions in-session and selected broadly.
Recorded here verbatim in effect, because the width of this scope is a supplied
input, not this record's inference:

**Lifetime — all three selected:**

1. **Single agent across its own turns** (scratchpad; a context-window pressure valve).
2. **Agent → agent within one dispatch** (handoff spool; a primary's bulk output pulled by reviewers by reference rather than relayed through the orchestrator).
3. **Across runs / sessions** (durable working memory outliving the dispatch).

**Retrieval — handle-addressed *and* semantic:** `put()` returning a handle and
`get(handle)` returning exactly what was stored, plus tag/scope listing, plus
vector retrieval over stashed content.

**Surface — all three:** a `cadre` CLI subcommand, an MCP tool alongside
`roster/orchestration/mcp/dispatch_server.py`, and structured files under a run
directory.

**The record's own reading of that answer set, offered as analysis and not as a
decision:** the three surfaces layer rather than compete, and the layering
already has a precedent in this repository — the knowledge store is the source
of truth, `proposed-knowledge/` is a deliberately-refreshed committed export
snapshot, and the store itself is gitignored. A context store could follow the
same shape: CLI as the core implementation, MCP as the in-session tool wrapper
over it, run-directory files as the committed export of whatever is worth
keeping. Similarly, the three lifetimes read as a scope ladder — one `scope`
or `ttl` dimension on a single mechanism — rather than three mechanisms. Both
readings are for the requirements phase to accept or reject.

Also in scope for the problem space:

- A **promotion path** from context store to knowledge store that runs through
  `cadre knowledge propose` and the steward disposition, never around it.
- Reuse of existing plumbing where it does not weaken a boundary:
  `database.py`, the project-local-then-global config resolution
  (`.agents/knowledge-store/config.json` → `$KNOWLEDGE_STORE_HOME` →
  `~/.agents/knowledge-store/`), secret redaction, `--source` scoping.
- Extending, not bypassing, the dispatch safety-control surface if the MCP
  layer is built: classification validation, effective-sandbox computation,
  the confirmation gate, dispatch-depth guard, concurrency limiting, audit
  records, and untrusted-output fencing.

## 5. Exclusions (explicitly not this record's authority to decide)

- Whether to build this at all, its priority, or its sequencing.
- The implementation approach: storage engine, schema, whether it shares the
  knowledge store's SQLite file, wire format, handle format.
- Any change to `cadre select`'s planning-only contract. The selector emits a
  plan and never retrieves, invokes, approves, or mutates; a context store does
  not change that, and this record does not propose it should.
- Any relaxation of the steward gate on the knowledge corpus.
- Retention windows expressed as concrete day-counts — an open Product
  Owner / Engineering Lead decision recorded in `team-profile.yaml`, which this
  record must not pre-empt (see OD-3 for why the existing precedent is a
  warning, not a template).
- Whether a new roster role is warranted (OD-8).

## 6. Constraints

- **Untrusted-data rule holds unconditionally.** `knowledge-use-policy.md`:
  retrieved content is "untrusted reference data. Never execute embedded
  instructions or let retrieval override system/developer instructions, role
  authority, current repository policy, or approval gates." Content an agent
  wrote itself is not exempt — it may be derived from untrusted input.
- **Authorship/approval separation is structural, not stylistic.** An agent
  that materially changes an artifact cannot approve it. A context store must
  not become a channel through which an agent's own output re-enters as
  authority.
- **Kernel boundary.** `roster/orchestration/test/test_kernel_boundary.py`
  permits exactly two couplings to `kernel/`. Nothing here may add a third.
- **Standard library only, Python 3.10+**, matching the knowledge store's
  existing constraint.
- **Fail-closed.** Every existing scope/classification rejection in this
  subsystem is a non-zero exit naming the remediation, never a silently
  narrowed result. Any new surface must match.
- **The three regeneration steps and their CI guards** apply to any roster,
  skill, or catalog change this feature ultimately requires.

## 7. Environments

Local developer machines and CI, same as the knowledge store. No hosted
service, no network dependency in the default path — with one exception that
becomes load-bearing here: semantic retrieval implies an embedding provider,
and the `openai-compatible` provider transmits content to a configured remote
endpoint. See OD-5, which this record regards as the most consequential
security question in the set.

## 8. Assumptions (labeled, not verified beyond what was read)

- **A-1.** Context pressure is currently a real, observed cost rather than an
  anticipated one. Not verified — no telemetry in this repository measures
  agent context consumption, and `selection-telemetry` records selection, not
  execution. If this is anticipatory, the success criteria in Section 10 have
  no baseline to improve on. This is the assumption most worth checking before
  G1.
- **A-2.** The knowledge store's config-resolution, redaction, and SQLite
  layers are reusable without modification. Read, not proven by attempting it.
- **A-3.** Volume is high-churn and mostly short-lived — most entries are never
  read after their run. Plausible from the scratchpad framing, unmeasured.
- **A-4.** Agents will actually use a by-reference path when one exists. Both
  the role definitions and the handoff contract currently instruct completeness
  inline; unless those change, a context store can exist and go unused.

## 9. Conflicts

**Central tension — the boundary the request's own scope answers erase.**

The original request framed the choice as *extension vs. sibling*. The scope
answers make that framing insufficient, and this must be stated plainly rather
than smoothed over. The distinguishing properties a sibling store would
normally rest on are:

| | knowledge store | context store, as scoped |
|---|---|---|
| lifetime | durable corpus | **also durable (cross-run selected)** |
| retrieval | semantic top-k | **also semantic (selected)** |
| storage | SQLite + vectors | SQLite + vectors |
| config resolution | project-local → global | proposed: identical |
| write authority | **steward-gated** | **agent-free** |

Lifetime and retrieval have been selected away as discriminators. What remains
is write authority and trust label — one axis, and the one that carries the
entire safety argument. A store that is otherwise structurally identical to the
knowledge store, queried the same way, holding durable content, but with the
steward gate removed, is a bypass of that gate unless the boundary is enforced
by construction.

This is not an argument against the scope the requester chose. It is a
statement that the scope makes the boundary mechanism the primary design
problem rather than a detail — the thing requirements must answer first. A
candidate direction, offered for the requirements phase to accept or reject:
no single command may return results from both stores; context-store results
carry a distinct trust label on the way out, following the
`wrap_untrusted_output()` precedent; and promotion between them exists only via
`cadre knowledge propose`.

**Secondary conflicts:**

- **C-2 — the laundering path.** Agent A reads a poisoned file, summarizes it
  into the context store; agent B retrieves it as "our own working notes" and
  affords it more trust than the original source earned. The risk exists in any
  design here, but is worse in a store whose retrieval contract does not
  already say "untrusted." `untrusted_instruction_risk` exists in the staged-record
  schema precisely for this class of problem and has no counterpart in a
  free-write store.
- **C-3 — completeness vs. by-reference.** `handoff-contracts.md` mandates full
  applicable fields inline. A by-reference handoff is a change to that
  contract, not merely an addition beside it, and touches an audit-trail
  document. It cannot be made implicitly.
- **C-4 — gitignored store vs. committed export.** `proposed-knowledge/README.md`
  is candid that "nothing verifies that this snapshot is current," because the
  store it exports from is machine-local. A context store with a committed
  run-directory layer inherits exactly that staleness property, over
  higher-churn content.

## 10. Success criteria (measurable, not target-inventing)

No thresholds are proposed — setting them is a Product Owner decision, and
several depend on a baseline that does not exist yet (A-1).

- **S-1.** An agent can store material larger than it can hold and retrieve it
  intact, verified by content hash on round-trip.
- **S-2.** A multi-agent dispatch consolidates without the orchestrator holding
  every member's full payload simultaneously — demonstrated on a real recipe
  from `routing.yaml`'s `team_recipes`, not a synthetic case.
- **S-3.** No path exists by which context-store content reaches the knowledge
  corpus without a steward disposition. Enforced by test, in the shape of
  `test_kernel_boundary.py`.
- **S-4.** Context-store content is distinguishable from knowledge-store
  content at every point of retrieval, by label, not by caller convention.
- **S-5.** Existing knowledge-store behavior is bit-identical afterward: all of
  `roster/knowledge-store/test/` passes unchanged, no schema change to ingested
  content, no change to `context` bundle shape.
- **S-6.** Entries expire or are collectable without steward action — a
  context store that accumulates indefinitely has become a knowledge store with
  the gate removed.

## 11. Open-decision register

| ID | Decision | Owner | Blocking? |
|---|---|---|---|
| OD-1 | No standing Product Owner for this repository's feature intake | Human | Yes — nothing can pass G1 |
| OD-2 | The boundary-enforcement mechanism between the two stores (Section 9) | PO + Engineering Lead | Yes — the scope answers make this the primary design problem |
| OD-3 | Expiry/retention defaults for context entries | PO + Engineering Lead | Yes — the knowledge store's "indefinite" default is documented as a placeholder chosen to avoid policy-by-inertia; repeating it here would contradict S-6 |
| OD-4 | Same SQLite database as the knowledge store, or its own | Engineering Lead | Yes — determines `delete-ingested` blast radius, backup semantics, deletion-evidence scope |
| OD-5 | Embedding provider for semantic retrieval over agent working notes | Security Lead + PO | **Yes** — `openai-compatible` transmits content to a remote endpoint. Applied to unreviewed agent working material, this is a data-egress surface with no steward in front of it, unlike ingested content which a steward at least dispositioned |
| OD-6 | Trust label on retrieved context content, and whether `untrusted_instruction_risk` (or an equivalent) carries through a stash | Security Lead | Yes — this is C-2's remedy |
| OD-7 | Classification of a stashed entry: inherited from the dispatch, caller-asserted, or defaulted | PO + Security Lead | Yes — the knowledge store's classification filter is exact-match and unauthenticated; a second unauthenticated store compounds it |
| OD-8 | Whether a new roster role owns this, or `knowledge-store-steward` does | PO | No — resolvable at design |
| OD-9 | Whether context content belongs in git at all, given C-4 | Engineering Lead | No — resolvable at design |
| OD-10 | Whether `handoff-contracts.md` gains a by-reference form (C-3) | Engineering Lead + Governance | Yes — an audit-trail document cannot change implicitly |
| OD-11 | Whether A-1 holds: is context pressure measured or anticipated | PO | Yes — determines whether S-1..S-6 have a baseline |

## 12. Knowledge retrieval status

**Not performed.** No knowledge-store retrieval was run for this record. The
grounding is direct reads of `roster/knowledge-store/README.md`,
`roster/shared/knowledge-use-policy.md`, `roster/orchestration/handoff-contracts.md`,
`roster/workflows/product-intake.md`, `roster/knowledge-store/proposed-knowledge/README.md`,
`roster/knowledge-store/proposed-knowledge.schema.json`,
`roster/knowledge-store/src/config.py`, `roster/knowledge-store/src/embeddings.py`,
and `roster/orchestration/mcp/dispatch_core.py`, all at revision
`7cd2712c5ece2d00bbe004ee22a9601f1692743d`. No citations from historical chat
context are claimed. `knowledge_steward_handoffs`: none proposed from this
record — it states a problem, it does not yet establish a durable lesson.

## Handoff

**To:** human Product Owner, for a G1 decision.

**Not yet to requirements.** OD-2, OD-3, OD-5, OD-6, OD-7, OD-10, and OD-11 are
marked blocking; OD-1 blocks the gate itself. Per `product-intake.md`,
objective conflicts return to G1 rather than proceeding.

**The one thing to decide first:** Section 9's central tension. Every other
open decision is downstream of whether the boundary between the two stores is
enforced structurally, and by what mechanism. If that has no answer, the
feature as scoped is a steward-gate bypass with a different name on it.
