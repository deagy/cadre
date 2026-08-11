# Implementation Plan — Agent context store (sibling)

**Traces to:** `INTENT-CADRE-CONTEXT-STORE-001` (`product-intent.md`, this directory)
**Revision:** 1 (initial)
**Status:** draft. The requester selected the sibling direction, which resolves the extension-vs-sibling question. This plan **proposes** answers to several open decisions the intent record marked blocking; a proposal in this document is not a decision, and OD-1 and OD-11 remain open and unresolvable here.
**Date:** 2026-08-11
**Repository revision this plan was written against:** `7cd2712c5ece2d00bbe004ee22a9601f1692743d`

---

## 1. What this plans, and what it does not

Plans: the module layout, schema, handle format, scope and trust model, expiry
semantics, CLI/MCP/export surfaces, the promotion path, every change required to
existing files, the test set, and a build order.

Does not plan: whether to build it, its priority, remote-embedding
authorization (OD-5 is narrowed here, not closed), or concrete retention
day-counts as *policy*. Section 8 draws a distinction between a TTL that exists
as a safety mechanism and retention windows that exist as policy; the first is
proposed here, the second remains the Product Owner decision recorded in
`team-profile.yaml`.

## 2. The decision, and the four separations that make it real

Sibling, not extension. Section 9 of the intent record established that with
cross-run durability and semantic retrieval both in scope, write authority is
the only discriminator left — so the boundary has to be structural rather than
conventional. Four separations carry it:

1. **Separate database file.** Not a second set of tables in `knowledge.db`.
2. **Separate config file and resolution root.** `.agents/context-store/config.json` → `$CONTEXT_STORE_HOME` → `~/.agents/context-store/`.
3. **Separate module tree with an enforced import boundary.** `roster/context-store/src/` may not import knowledge-store modules, and vice versa, asserted by an AST test modeled on `test_kernel_boundary.py`.
4. **No shared query path.** No command, tool, or function returns results from both stores.

Separation 1 is doing more work than it appears to. S-3 asks for proof that no
path exists from context content into the knowledge corpus without a steward
disposition. With two physically distinct SQLite files, a cross-store `JOIN` is
not merely disallowed — it cannot be written. That converts S-3 from a policy
claim into a property of the deployment, which is the same move
`test_kernel_boundary.py` makes for the kernel.

Separation 1 also settles OD-4 on independent grounds: `delete_ingested()`
scopes deletions by source/conversation/message across the `messages` table,
and a shared file would put high-churn context rows inside that blast radius.
The two stores also want opposite durability postures — the knowledge store
documents that deletion is irreversible and a backup is the only recovery,
while the context store is designed to lose things on a timer.

## 3. Layout

```
roster/context-store/
  README.md                       # operator-facing, mirrors knowledge-store/README.md
  SECURITY.md                     # honest-limits document; see §6 on what scope is not
  config.example.json
  context-entry.schema.json       # the entry record, for the export layer
  .gitignore                      # data/*.db*, config.json — same shape as knowledge-store
  data/.gitkeep
  src/
    config.py                     # tiered resolution; own defaults
    database.py                   # schema, open_store, additive migration, sweep
    handles.py                    # handle mint/parse/validate
    service.py                    # put/get/list/search/promote-record construction
    cli.py                        # argparse surface
  test/
    test_context_store.py
    test_scope_enforcement.py
    test_expiry.py
    test_trust_propagation.py
    test_promotion.py
    test_cli.py
```

Flat module imports with no `__init__.py`, matching the knowledge store — its
`config.py` appends `roster/shared/src` to `sys.path` for `settings`, and the
same pattern applies here.

## 4. Data model

```sql
CREATE TABLE IF NOT EXISTS entries (
  handle           TEXT PRIMARY KEY,
  scope            TEXT NOT NULL,          -- 'agent' | 'dispatch' | 'project'
  source           TEXT NOT NULL,          -- project identity, same semantics as knowledge store
  task_id          TEXT NOT NULL,
  agent            TEXT NOT NULL,          -- writing role id
  dispatch_id      TEXT,                   -- required when scope='dispatch'
  label            TEXT NOT NULL,
  tags_json        TEXT NOT NULL,
  content          TEXT NOT NULL,          -- post-redaction
  content_hash     TEXT NOT NULL,          -- sha256 of stored content
  byte_length      INTEGER NOT NULL,
  classification   TEXT NOT NULL,
  injection_risk   INTEGER NOT NULL DEFAULT 0,
  untrusted_inputs INTEGER NOT NULL DEFAULT 0,
  derived_from_json TEXT NOT NULL,         -- handles and/or knowledge citations
  redactions_json  TEXT NOT NULL,
  created_at       TEXT NOT NULL,
  expires_at       TEXT NOT NULL,          -- NOT NULL, deliberately; see §8
  promoted_record_id TEXT                  -- KS-record id if a proposal was emitted; provenance only
);

CREATE TABLE IF NOT EXISTS entry_chunks (
  id TEXT PRIMARY KEY,
  handle TEXT NOT NULL REFERENCES entries(handle) ON DELETE CASCADE,
  ordinal INTEGER NOT NULL, content TEXT NOT NULL, content_hash TEXT NOT NULL,
  embedding_provider TEXT NOT NULL, embedding_model TEXT NOT NULL,
  embedding_dimensions INTEGER NOT NULL, embedding_json TEXT NOT NULL,
  UNIQUE(handle, ordinal, embedding_provider, embedding_model)
);

CREATE TABLE IF NOT EXISTS access_runs (
  id TEXT PRIMARY KEY, operation TEXT NOT NULL, handle TEXT, query_hash TEXT,
  task_id TEXT NOT NULL, agent TEXT NOT NULL, classification TEXT NOT NULL,
  scope_filter TEXT, source TEXT NOT NULL, result_count INTEGER NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS expiry_evidence (
  id TEXT PRIMARY KEY, handle TEXT NOT NULL, content_hash TEXT NOT NULL,
  byte_length INTEGER NOT NULL, classification TEXT NOT NULL, source TEXT NOT NULL,
  created_at TEXT NOT NULL, expired_at TEXT NOT NULL, swept_at TEXT NOT NULL
);
```

`entry_chunks` mirrors the knowledge store's `chunks` deliberately, including
the provider/model/dimensions tuple in the uniqueness constraint and the
dimension-mismatch exclusion rule — the same "re-ingest after changing
provider" caveat applies, and there is no reason to invent a second answer to a
problem already solved next door.

`expiry_evidence` records that an entry expired and what it hashed to, never
its content — the same shape as the knowledge store's ingested-deletion
evidence. It exists so that a promoted-then-expired entry's provenance chain
does not dead-end, not to make expiry reversible.

## 5. Handles

`ctx_` + 32 lowercase hex characters from `secrets.token_hex(16)`.

Random, **not** content-derived. Content addressing would give free dedup, and
that is not worth what it costs: identical content stored by two agents in two
scopes would collide onto one handle, turning the store into an equality oracle
across a boundary the scope model is supposed to hold. A handle is also never a
path, satisfying the same no-local-path rule that governs citation `source_uri`
and staged-record `evidence`.

Handles are opaque to agents. Nothing about scope, agent, or task is
recoverable from one, which keeps a leaked handle from being a map.

## 6. Scope and classification

**Read rules**, evaluated before ranking, in this order:

| `scope` | readable by |
|---|---|
| `agent` | same `agent` **and** same `task_id` |
| `dispatch` | any agent with the same `dispatch_id` |
| `project` | any caller with the same `source` |

Classification is filtered exact-match, as the knowledge store does.

At the global-fallback config tier, `--source` is **required** — and unlike the
knowledge store, there is **no `--all-sources` escape hatch**. Cross-project
retrieval of curated knowledge has a defensible use case; cross-project
retrieval of another project's unreviewed agent working notes does not, and it
would be the widest laundering channel in the design. This is a deliberate
divergence from the sibling subsystem and `SECURITY.md` must say so, since a
reader who knows the knowledge store will expect the flag to exist.

**What this is not.** Scope is caller-asserted and unauthenticated on the CLI
path, exactly as classification is in the knowledge store, whose `SECURITY.md`
already states it "is not production authorization." Scope is a blast-radius
reducer and an audit signal. The `SECURITY.md` for this subsystem must state
that in the same register rather than letting a table of read rules imply
enforcement it does not have.

**The MCP path is genuinely stronger, and the docs should say which is which.**
`dispatch_server.py` already derives `_task_id()`, `_session_id()`, and
`_parent_classification()` from the dispatch environment rather than from tool
arguments, and `validate_classification(classification, parent_classification)`
already refuses to widen a child's classification beyond its parent's. Routed
through those, `task_id`, `dispatch_id`, and the classification ceiling are
ambient facts rather than caller claims. The two surfaces are not equivalent
and the plan does not pretend they are.

## 7. Trust, and the anti-laundering rule (C-2 / OD-6)

Three mechanisms, none of them novel — each is an existing repository
mechanism applied to the new surface.

1. **A distinct trust label.** Retrieval bundles carry
   `"trust": "untrusted_working_context"`, where the knowledge store's
   `build_agent_context()` emits `"trust": "untrusted_reference"`. Different
   value, same field position. This satisfies S-4 by label rather than by
   caller convention.

2. **Fenced output.** Content returned through the MCP tools is wrapped by
   `wrap_untrusted_output()` — the existing random-per-call-token fence, whose
   docstring already reasons about a child forging a closing fence to claim
   trusted instructions resume. Content an agent wrote is exactly as capable of
   that as a dispatched child's stdout.

3. **Monotonic `untrusted_inputs`.** On `put`, the flag is set when any of:
   `protect_content()` reports `injection_risk` on the submitted text; any
   `--derived-from` handle has `untrusted_inputs = 1`; or any `--derived-from`
   knowledge citation carried `untrusted_instruction_risk: true`. It can never
   be cleared by the writing agent. This is the direct analogue of the rule
   `knowledge-use-policy.md` already states — *"an agent cannot clear it"* —
   extended across a `put`/`get` cycle so that a summarization step does not
   launder the signal away. It is the specific remedy for C-2.

`protect_content()` runs on every `put`, so secret redaction and
injection-pattern detection apply to stored content identically to ingestion.

## 8. Expiry (OD-3), and why it inverts the knowledge store's default

`expires_at` is `NOT NULL`. There is no indefinite entry.

This looks like it contradicts the knowledge store, whose retention defaults
are all `null` on purpose, with a config comment explaining that shipping
working day-counts ahead of the Product Owner decision "would let them become
policy by default inertia." The reasoning does not transfer, and the difference
matters enough to state plainly:

- In the knowledge store, indefinite **keeps an open decision visible**. Content is steward-dispositioned and curated; the harm of holding it is a policy question nobody has answered yet, so recording "no window decided" is the honest state.
- In the context store, indefinite **would silently defeat the design**. Entries are agent-written with no steward in front of them. An entry that never expires is durable, unreviewed, semantically searchable content — which is precisely the "knowledge store with the gate removed" failure mode Section 9 of the intent record identified. S-6 says so directly.

So the TTL here is a safety mechanism, not a retention policy, and shipping a
concrete default does not pre-empt the `team-profile.yaml` decision — that
decision governs how long *curated* content is kept, a different question.

Proposed defaults, configurable, with the `NOT NULL` constraint not
configurable:

| scope | default TTL |
|---|---|
| `agent` | 24 hours |
| `dispatch` | 7 days |
| `project` | 30 days |

`--ttl-days` overrides per entry, bounded by a configurable maximum (proposed:
90 days) so that no caller can construct a de facto permanent entry.

**Sweeping.** A lazy sweep runs on every store open — cheap, idempotent, in the
manner of `_migrate_additive_columns` — deleting expired rows and writing
`expiry_evidence`. `cadre context expire --dry-run` exposes the same sweep for
inspection.

Expiry is deliberately **not** steward-gated, which is the sharpest operational
asymmetry with the knowledge store and needs its justification in the docs:
knowledge-store deletion destroys curated, dispositioned content and therefore
demands a reason, an authorized human, and retained evidence. Context expiry
destroys working scratch whose entire contract is that it expires. Routing it
through a steward would rebuild the bottleneck this feature exists to remove,
and would make the steward accountable for content they never dispositioned.

## 9. Embeddings (OD-5, narrowed but not closed)

Default provider `hashing` — offline, deterministic, no egress. Semantic search
ships on it.

`openai-compatible` is **refused entirely in phase 2** and gated behind an
explicit OD-5 resolution thereafter. The precedent is already in the tree:
`config.py` refuses remote embeddings when configuration resolved
project-locally, on the grounds that a project-local file is untrusted,
clonable content. Context entries are a strictly weaker-provenance input than a
project-local config file — they are unreviewed agent output — so the same
reasoning argues for at least as strict a posture.

If OD-5 later authorizes remote embeddings, the proposed conditions are:
global-tier config only, an explicit `remote_embeddings_acknowledged: true`
field rather than mere `base_url` presence, and an unconditional refusal for
`restricted` entries. Recorded as a proposal; the Security Lead owns it.

## 10. CLI surface

One new `bin/subcommands.tsv` row → `cadre context`.

```text
init
put    --label <text> [--input <file>|-] --agent <role> --task-id <id> --classification <level>
       [--scope agent|dispatch|project] [--dispatch-id <id>] [--tag <t>]...
       [--derived-from <handle|citation>]... [--ttl-days <n>] [--source <name>]
get    --handle <h> --agent <role> --task-id <id> --classification <level> [--source <name>]
list   [--agent <r>] [--task-id <id>] [--dispatch-id <id>] [--tag <t>] [--scope <s>]
       --classification <level> [--source <name>]
search --query <text> --agent <role> --task-id <id> --classification <level>
       [--top <n>] [--scope <s>] [--source <name>]
drop   --handle <h> --reason <text>
expire [--as-of <iso-8601>] [--dry-run]
stats
promote --handle <h> [--recommended-action ingest|update|reclassify|defer]
export --output <dir> [--dispatch-id <id>] [--handle <h>]... [--acknowledge-commit]
```

`get` requires `--agent`, `--task-id`, and `--classification` for the same
reason `cadre knowledge context` does: every read is attributable in
`access_runs`. `--top` reuses the 1–20 orchestration limit. Every rejection is a
non-zero exit naming the remediation, never a narrowed result.

## 11. MCP surface

Add `context_put`, `context_get`, `context_list`, `context_search` to the
existing `roster/orchestration/mcp/dispatch_server.py` rather than standing up a
second server. The reason is §6's: that module already resolves the ambient
dispatch identity and already owns the classification-narrowing rule, and a
separate server would have to either re-derive both or accept them as caller
claims — reintroducing exactly the weakness the MCP path is supposed to remove.

These tools inherit the module's existing control surface: classification
validation, effective-sandbox computation, concurrency limiting, audit records,
and untrusted-output fencing.

**Revision 2 (2026-08-11), recorded after implementation.** Revision 1 of this
section required that `context_put` "sit behind the same `ConfirmationGate`
treatment as other write-capable operations rather than being treated as
read-shaped because it returns only a handle." **What shipped does not do
that**, and this record is amended rather than left contradicting the code — a
plan a gate reviews must not describe something other than what was built.

The reasoning for the departure: the confirmation gate exists to make a caller
stop and re-assert before a *spawned process* mutates a repository or a
persistent environment. A context put writes one row to a local, gitignored,
always-expiring database whose entire purpose is absorbing high-churn agent
writes. Gating it would make confirmation routine, and a confirmation that
fires constantly is worth less at the moment it actually matters. The controls
that do apply — classification narrowing against the session ceiling, entry
size limits, secret redaction, TTL, and audit — are enforced on every call.

This is a judgement call made by the implementing author, not a reviewed
decision, and it is recorded here so that a reviewer meets it in the plan
rather than only in a code comment. Its residual cost is stated in
`roster/context-store/SECURITY.md`: a compromised or confused agent can fill
the store without a second prompt, bounded by size limit, TTL, and ceiling
rather than by a human. A reviewer who disagrees should treat this paragraph,
not the code comment, as the thing to overturn.

## 12. Run-directory export

`cadre context export --output roster/orchestration/runs/<slug>/context/`
writes one Markdown file per entry, frontmatter validated against
`context-entry.schema.json`, mirroring `cadre knowledge export-staged`.

Inherits C-4 exactly: the store is gitignored and machine-local, so no CI job
can verify the snapshot is current. The README must carry the same candid
warning `proposed-knowledge/README.md` carries, over higher-churn content.

Because the destination is committed to git, export refuses `restricted`
entries outright and requires `--acknowledge-commit` for `confidential`.
Nothing is exported automatically.

## 13. Promotion (S-3)

`cadre context promote --handle <h>` **writes nothing to the knowledge store.**
It emits, on stdout, a finding record in exactly the shape
`cadre knowledge propose --from-finding -` already accepts. The operator or
orchestrator pipes it:

```sh
cadre context promote --handle ctx_… | cadre knowledge propose --from-finding -
```

Two consequences worth being explicit about:

- The context store never imports a knowledge-store write function. The pipe is the entire coupling, and it is one-directional, out of process, and visible in a shell history.
- When the entry has `untrusted_inputs = 1`, the emitted record sets `untrusted_instruction_risk: true`. Promotion does **not** refuse. It hands the flag to the existing gate, whose schema already forces automatic deferral on that value. Reusing that rule is better than duplicating it — a second implementation of the same decision is a second place for it to drift.

`promoted_record_id` is recorded on the entry as provenance only and confers
nothing.

## 14. Changes to existing files

| File | Change | Note |
|---|---|---|
| `bin/subcommands.tsv` | one row: `context` | dispatch is table-driven |
| `roster/orchestration/mcp/dispatch_server.py` | four tools | §11 |
| `roster/orchestration/src/generate_global_plugin.py` | bundle `roster/context-store/src/` into `suite/` | **easy to miss.** `plugin/suite/` bundles a copy of knowledge-store `src/`; omitting the parallel entry ships a plugin whose CLI cannot import its own code — the exact failure `CLAUDE.md` warns about |
| `roster/knowledge-store/src/service.py:195` | amend one bundle requirement string | §14.1 |
| `roster/orchestration/handoff-contracts.md` | add `context_handles` | §14.2, resolves C-3/OD-10 |
| `roster/shared/knowledge-use-policy.md` | boundary paragraph pointing at the new policy | |
| `roster/shared/context-use-policy.md` | **new**, parallel to the knowledge policy | |
| `.agents/skills/run-agent-orchestration/` | consolidation step references handles | |
| `roster/context-store/` | the subsystem | §3 |
| `roster/orchestration/test/test_context_boundary.py` | **new**, AST import guard | §15 |

Touching `roster/shared/` and `.agents/skills/` triggers all three regeneration
steps and both guards, per `CLAUDE.md`. `git add` new files before
regenerating.

No role `AGENT.md` files change. Agent-facing behavior arrives through
`roster/shared/` policy, which roles already inherit, and through the
orchestration skill — editing 159 role files to teach one capability would be
the wrong shape and an enormous review surface.

### 14.1 The one-line change with real weight

`build_agent_context()` currently emits this requirement in every knowledge
bundle:

> "Do not write retrieved or generated content back to the store; propose it to the knowledge-store steward."

Read literally today it means "never write generated content anywhere," and
after this feature that reading is wrong in a way that matters. It must be made
precise — the prohibition on writing to the *knowledge* store stays absolute,
while working material gains a named legitimate destination.

This changes a bundle declared `schema_version: 1`. Whether an amended
requirement string constitutes a schema change is a real question, not a
formality: consumers may match on these strings. Flagged for the requirements
phase; the conservative answer is to bump.

### 14.2 `context_handles` in the handoff contract

Add a `context_handles: []` list beside the existing `findings: []`,
`human_gates: []`, and `knowledge_steward_handoffs: []` siblings.

The rule that resolves C-3: **a handle may replace bulk content, never a
required audit field.** Findings with evidence and severity, assumptions,
unresolved questions, citations, approval status, and trace links stay inline
and complete, exactly as `handoff-contracts.md` requires today. What moves
behind a handle is volume — the full log, the complete diff analysis, the raw
tables. The proportionality principle already in that document permits this;
its explicit carve-out ("never excuses dropping an audit-trail, citation,
evidence-integrity, approval-status, or assumption/unresolved-question field")
is what the rule above preserves.

This is an audit-trail document, so the change needs its own review and cannot
land implicitly as part of a code PR.

## 15. Tests

**Boundary (S-3), `roster/orchestration/test/test_context_boundary.py`** —
modeled directly on `test_kernel_boundary.py`, which already walks the AST for
prohibited imports:

- No module under `roster/context-store/src/` imports `staged_store`, `ingested_deletion`, `service`, `database`, `normalize`, or `finding_record` from the knowledge store.
- No knowledge-store module imports a context-store module.
- Neither store's config resolution can produce the other's database path.
- The only textual coupling permitted is `promote`'s output *shape*.

**Subsystem, `roster/context-store/test/`:**

- Round-trip: `put` then `get` returns byte-identical content, verified by hash (S-1).
- `expires_at` is never `NULL` on any write path; a sweep removes expired entries and writes evidence; `--ttl-days` cannot exceed the configured maximum.
- `untrusted_inputs` propagates through `derived_from` and cannot be cleared; a `put` derived from a flagged entry is itself flagged.
- Scope enforcement: cross-agent read of `scope=agent` is refused; cross-dispatch read of `scope=dispatch` is refused; classification mismatch returns nothing.
- Global tier requires `--source`; `--all-sources` is not a recognized flag.
- `export` refuses `restricted`, and refuses `confidential` without `--acknowledge-commit`.
- `promote` writes nothing to any database and emits a record `propose --from-finding` accepts; a flagged entry produces `untrusted_instruction_risk: true`.
- Dimension-mismatched vectors are excluded rather than scored (parity with the knowledge store).

**Regression (S-5):** `roster/knowledge-store/test/` passes unchanged, no
ingested-content schema change, `context` bundle shape unchanged apart from
§14.1.

**Guards:** `test_repository_health.py` and `plugin/tools` after regeneration.

## 16. Build order

| Phase | Contents | Delivers |
|---|---|---|
| 1 | `roster/context-store/` + config/schema/handles/`put`/`get`/`list`/`drop`/`expire`/`stats`; boundary test | S-1, S-3, S-6 — usable without any semantic machinery |
| 2 | chunking + `hashing` embeddings + `search` | the retrieval half |
| 3 | four MCP tools on `dispatch_server.py` | the surface with real ambient identity (§6) |
| 4 | `promote`, `context_handles`, `context-use-policy.md`, skill updates, §14.1 | S-2, S-4; the audit-document changes reviewed together |
| 5 | `export` + run-directory layer | the committed snapshot |
| — | remote embeddings | **not scheduled**; blocked on OD-5 |

Phase 1 is deliberately shippable alone. If A-1 turns out not to hold — if
context pressure is anticipated rather than measured — phase 1 is a small,
self-contained subsystem to have built, and phases 2–5 are the ones that would
have been wasted.

## 17. Decision status

**Proposed here, for the requirements phase or the named owner to accept or reject:**

| ID | Proposal | §  |
|---|---|---|
| OD-2 | Four structural separations + distinct trust label + pipe-only promotion | 2, 7, 13 |
| OD-3 | `expires_at NOT NULL`; per-scope defaults; TTL as safety mechanism, not retention policy | 8 |
| OD-4 | Separate database file | 2 |
| OD-5 | `hashing` only; remote refused in phase 2, conditions proposed for later | 9 |
| OD-6 | Distinct trust label + `wrap_untrusted_output()` + monotonic `untrusted_inputs` | 7 |
| OD-7 | Caller-asserted on CLI, ambient and ceiling-enforced on MCP; both documented as such | 6 |
| OD-8 | No new role; `knowledge-store-steward` unchanged, since nothing here is steward-gated | 8, 13 |
| OD-9 | Export is opt-in, refuses `restricted`, gated for `confidential` | 12 |
| OD-10 | `context_handles` may replace bulk content, never a required audit field | 14.2 |

**Still open, and not closable by this plan:**

- **OD-1** — no standing Product Owner for this repository's intake. Blocks G1 itself.
- **OD-11** — whether context pressure is measured or anticipated (A-1). Nothing in this repository measures agent context consumption; `selection-telemetry` records selection, not execution. Phase 1's scoping is the hedge against this being wrong, not an answer to it.
