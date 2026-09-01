# Agent Context Store

A local-first place for an agent to park working material outside its own
context window and get it back later by handle.

This is **not** the knowledge store, and the difference is the entire point.

| | `roster/knowledge-store/` | `roster/context-store/` (here) |
| --- | --- | --- |
| what it holds | curated, authorized historical context | agent working material |
| who writes | steward, after a disposition | any agent, freely |
| how long | retention-governed, indefinite by default | **always expires**; no indefinite entry |
| how you read | semantic top-k over chunks | by handle, a metadata listing, or semantic search |
| trust label | `untrusted_reference` | `untrusted_working_context` |

Nothing crosses from here into the knowledge store except through
`cadre knowledge propose` and a steward's disposition.
`internal/contextstore/boundary_test.go` enforces that
structurally: the two stores are separate databases, separate configs, and may
not import each other.

## Status

**Phase 2.** Handle-addressed storage plus offline semantic search. Content is
chunked and embedded on `put` with the deterministic `hashing` provider, which
approximates lexical rather than full semantic similarity — good enough to find
the entry you half-remember, and honest about not being an embedding model.

**Remote embeddings are refused, structurally.** The provider that could send
text to a third-party endpoint lives in the knowledge store, which this store
may not import; there is no code path from here to anything that opens a socket
or reads an embedding credential. `internal/contextstore/config.go` rejects a non-`hashing` provider
as well, but the module boundary is the real mechanism — a config check alone
would leave the remote path one edit away. Whether unreviewed agent working
material may be transmitted off the machine is OD-5 in
`roster/orchestration/runs/cadre-feature-agent-context-store-2026-08-11/`, and
it is open.

**Phase 3** adds four MCP tools — `context_put`, `context_get`, `context_list`,
`context_search` — on `internal/cli/mcp_dispatch_server.go`. See
"Two surfaces" below.

**Phase 4** adds `promote`, the `context_handles` handoff field, and
`roster/shared/context-use-policy.md`. See "Promotion" below.

**Phase 5** adds `export`. See "Exporting" below. This completes the planned
build; remote embeddings remain deliberately unscheduled pending OD-5.

### Automatic final-handoff capture

Secure-cloud role dispatches automatically capture a final handoff only when a
runner returns it in its separate `final_handoff` result field as this exact,
versioned envelope:

```json
{
  "kind": "cadre-final-handoff",
  "schema_version": 1,
  "handoff": {
    "summary": "Implemented the bounded change.",
    "disposition": "complete"
  },
  "artifacts": [],
  "derived_from": []
}
```

The envelope is deliberately a small allowlist. `handoff` must contain at
least one field and may contain only
`summary`, `disposition`, `findings`, `assumptions`, `unresolved_questions`,
`next_action`, `context_handles`, and `knowledge_steward_handoffs`.
`artifacts` is an identifier-only manifest (up to 64 entries); each entry may
contain only `id`, `kind`, `revision`, `digest`, and `uri`. Capture never reads
an artifact path or copies an artifact body. `derived_from` may cite only
context handles or `ks:untrusted:<id>` markers, preserving untrusted-input
provenance. The complete envelope has a 64 KiB cap.

The dispatcher does not inspect stdout to infer a handoff. A missing or invalid
envelope therefore stores nothing; capture is best-effort and its outcome is
returned as `context_capture` without changing the dispatch result. Stored
metadata is dispatch-derived: role, task and dispatch identifiers,
classification, and source; captured entries use `dispatch` scope and its
normal TTL. Redaction, audit, expiry, and untrusted-data rules are the same as
for an explicit `put`. `context_capture.source` is returned with the handle and
is required for a later dispatch-scoped read.

CLI child adapters receive a private `SECURE_CLOUD_AGENTS_FINAL_HANDOFF_PATH`
and a completion instruction; writing a valid envelope there activates capture
without using stdout. The API runner does not yet expose this result channel,
so it reports `context_capture: not_provided` rather than treating model output
as a substitute.

Conversation transcripts and raw tool results are excluded: they are neither
accepted envelope fields nor inferred from child output. Whether those
higher-volume, more-sensitive sources have enough retrieval value to justify
separate storage remains a parked investigation, not an authorization to begin
collecting them.

## Two surfaces, one behaviour

The CLI above and the MCP tools are the same code path: the tools shell out to
this CLI and parse its JSON rather than importing the store. So a scope rule
fixed in one is fixed in both, and there is no second implementation to drift.

What the MCP path adds is **ambient identity**. `--task-id` and `--dispatch-id`
come from the dispatch environment instead of tool arguments, and
`--classification` is validated against the session's parent classification, so
a caller cannot label an entry above its own ceiling. Retrieved content is also
fenced as untrusted output with a per-call random token before it reaches the
model.

`agent` is the exception and stays caller-asserted on both surfaces: the
dispatch protocol has no role-id environment variable today. `internal/orchestration/mcp_context_tools.go`
reads `SECURE_CLOUD_AGENTS_ROLE_ID` if something sets it, and ambient wins over
the parameter, but nothing sets it yet — wiring it into `build_child_env()`
would make agent identity genuinely ambient for dispatched children and belongs
in its own review of safety-relevant dispatch code.

`source` is the same exception, for the same reason: there is no ambient
project/repository identity in the dispatch protocol to check it against, so
it is forwarded to the CLI exactly as the caller supplied it. Unlike `agent`
this is not merely undecided — the MCP server's own working directory is
explicitly *not* used as a stand-in, because it is unreliable for this purpose
(`internal/cli/mcp_dispatch_server.go` disables project-tier settings resolution for the same
reason: an MCP server's cwd is wherever the host CLI was launched, not
reliably the target project). See `SECURITY.md`'s "What is not enforced".

**The MCP tools are not behind the dispatch confirmation gate**, unlike a
write-capable role dispatch. That gate exists to make a caller stop and
re-assert before a spawned process mutates a repository or a persistent
environment. A context put writes one row to a local, gitignored,
always-expiring database built to absorb high-churn agent writes. Gating it
would make confirmation routine, and a confirmation that fires constantly is
worth less at the moment it actually matters. Classification narrowing, size
limits, redaction, and audit still apply to every call.

## Quick start

Requires Python 3.10 or newer, standard library only.

```sh
mkdir -p ~/.agents/context-store
cp roster/context-store/config.example.json ~/.agents/context-store/config.json

cadre context init
some-long-command | cadre context put --label "test run output" \
  --agent test-engineer --task-id TASK-42 --classification internal --source my-project
cadre context get --handle ctx_… \
  --agent test-engineer --task-id TASK-42 --classification internal --source my-project
```

Run the tests with:

```sh
go test ./internal/contextstore/
```

## Configuration

Resolved in the same three tiers as the knowledge store, against its own paths:

1. **Project-local**: `.agents/context-store/config.json`, found by walking up
   to the first `.git` boundary.
2. **Global**: `$CONTEXT_STORE_HOME/config.json`, defaulting to
   `~/.agents/context-store/config.json`.

An explicit `--config <path>` beats both and fails closed if the file is absent.

`context_store.home` is **global-only** as a settings key. A project-local
`.agents/cadre.yaml` cannot set it, because that file arrives with `git clone`
and is editable by anyone who can open a pull request — and this value picks
where a database is read and written.

## Commands

```text
init
put    --label <text> [--input <file>|-] --agent <role> --task-id <id> --classification <level>
       [--scope agent|dispatch|project] [--dispatch-id <id>] [--tag <t>]...
       [--derived-from <handle|ks:untrusted:<id>>]... [--ttl-days <n>] [--source <name>]
get    --handle <h> --agent <role> --task-id <id> --classification <level> [--dispatch-id <id>] [--source <name>]
list   [--scope <s>] [--dispatch-id <id>] [--filter-agent <r>] [--filter-task-id <id>]
       [--tag <t>]... [--top <n>] --agent <role> --task-id <id> --classification <level> [--source <name>]
search --query <text> [--scope <s>] [--dispatch-id <id>] [--top <n>]
       --agent <role> --task-id <id> --classification <level> [--source <name>]
promote --handle <h> --artifact <path> --revision <rev> --sensitivity-notes <text>
        --conflicts-or-staleness <text> --recommended-action ingest|update|reclassify|defer
        [--finding-only] --agent <role> --task-id <id> --classification <level> [--source <name>]
export --output <dir> [--handle <h>]... [--scope <s>] [--dispatch-id <id>]
       [--filter-dispatch-id <id>] [--acknowledge-commit] [--include-untrusted]
       --agent <role> --task-id <id> --classification <level> [--source <name>]
prune-audit --older-than-days <n> --acknowledge-loss
drop   --handle <h> --reason <text>
expire [--as-of <iso-8601>] [--dry-run]
reindex [--force]
stats
```

`put` reads stdin unless `--input` names a file.

`--agent`, `--task-id`, and `--classification` are required on every read and
write. They are what makes an access attributable in `access_runs` — an
unattributable read of agent working material is exactly the read nobody can
review afterward.

`list` returns metadata only, never content. That is what `get` is for, and it
keeps a broad listing from becoming a bulk read. `search` does return matched
chunk text — returning content is the point of a search — bounded by `--top`
(1–20, the orchestration policy limit).

Every access filter — classification, source, and scope — is applied before a
single vector is scored, and re-checked on each candidate. Ranking is never
what stands between a caller and an entry they may not read: a high-scoring hit
they were not entitled to is still a disclosure, however relevant it was.

### Reindexing

`reindex` re-chunks and re-embeds entries that have no vectors under the
current settings; `--force` rebuilds every entry, which is what a changed
`chunking` block needs since no provider or dimension check would notice it.

This store can do that because it keeps its own content. The knowledge store
cannot — it tells you to re-ingest from source after a provider, model, or
dimension change. That difference matters here: an agent's working material
usually has no source to re-ingest from.

Reindexing replaces an entry's chunks rather than adding to them, so a rebuild
cannot leave vectors written under two different configurations scored against
each other as if comparable.

### Scope

| `--scope` | readable by |
| --- | --- |
| `agent` (default) | the same agent, on the same task |
| `dispatch` | any agent asserting the same `--dispatch-id` |
| `project` | any caller with the same `--source` |

A handle that does not exist, has expired, or is out of scope all return the
same empty result. Distinguishing them would let a caller probe for entries it
may not read.

**Read `SECURITY.md` before trusting that table.** Scope is caller-asserted and
unauthenticated on this CLI; it reduces blast radius and produces an audit
trail, and it is not access control.

### There is no `--all-sources`

`cadre knowledge` offers `--all-sources` as an explicit opt-in to cross-project
retrieval. This store deliberately does not, and will not. Cross-project
retrieval of curated, steward-dispositioned knowledge has a defensible use
case; cross-project retrieval of another project's unreviewed agent working
notes does not, and it would be the widest laundering channel the design could
offer. Against the shared global store, `--source` is simply required.

### Expiry

Every entry has an expiry. There is no indefinite entry, and `expires_at` is a
`NOT NULL` column so no code path can create one.

This inverts the knowledge store's `null`-means-indefinite default on purpose.
There, indefinite keeps an open Product Owner retention decision *visible* over
content a steward has dispositioned. Here, indefinite would mean durable,
unreviewed, agent-written content accumulating outside the gate — which is
exactly the failure this store's separate existence is meant to prevent. The
TTL is a safety mechanism, not a retention policy, and it does not pre-empt the
retention decision recorded in `roster/shared/team-profile.yaml`.

Defaults: 1 day (`agent`), 7 days (`dispatch`), 30 days (`project`), with a
90-day ceiling on `--ttl-days` so no caller can construct a de facto permanent
entry. Expired entries are swept whenever the store is opened; `expire
--dry-run` reports without destroying.

Sweeping is **not** steward-gated, unlike knowledge-store deletion. That is
deliberate: knowledge-store deletion destroys curated, dispositioned content
and the steward is accountable for it, while context expiry destroys working
scratch whose entire contract is that it expires. Gating it would rebuild the
bottleneck this store exists to remove.

`expiry_evidence` records that an entry existed and what it hashed to — never
its content — so a handle cited in a handoff can still be accounted for after
the content is gone. Expiry is not reversible; there is no backup but yours.

### Audit retention

`access_runs` and `expiry_evidence` keep every row indefinitely — no TTL,
no scheduled deletion. That is a deliberate choice, not an unbounded default
left unspecified: content in `entries` always expires because it is
unreviewed and accumulating it would defeat the whole design, but the *record*
that a read, write, or expiry happened is an audit trail, and deleting it on
the same clock that deletes the content it attests to would erase
accountability rather than reduce risk. Indefinite is the deliberate default
here, in contrast to `entries`, and for the opposite reason.

An operator who has decided a cutoff is appropriate for their own deployment
runs `cadre context prune-audit --older-than-days <n> --acknowledge-loss`.
Both flags are required and neither has a default: the age cannot be chosen by
omission, and the acknowledgement exists because this is the one destructive
command here that is **not** hygiene. Sweeping an expired entry destroys
scratch whose contract was always to expire; pruning audit rows destroys the
record that reads and writes happened at all, and neither table is
recoverable. Nothing calls it on a schedule.

### Promotion

`promote` is the only sanctioned route from here into the curated corpus, and
it deliberately cannot take that route by itself. It **writes nothing** — it
prints a proposal document and an operator pipes it:

```sh
cadre context promote --handle ctx_… --finding-only \
  --artifact roster/RUNBOOK.md --revision 7cd2712c \
  --sensitivity-notes "none; public repository behaviour" \
  --conflicts-or-staleness "none known" --recommended-action ingest \
  --agent code-reviewer --task-id TASK-7 --classification internal --source demo \
  | cadre knowledge propose --from-finding -
```

The coupling is a shell pipe: out of process, one-directional, and visible in a
shell history — not an import a later refactor could quietly turn into a direct
write. No knowledge-store function is called here, and `promoted_at` records
only that a proposal was *emitted*, never what happened to it downstream. It is
deliberately not the staged record's id: deriving that id needs knowledge-store
code this store may not import.

The judgement fields are required rather than defaulted, mirroring
`internal/contextstore/service.go`'s refusal to invent one. Promoting into the corpus is meant
to cost more than stashing.

An entry flagged `untrusted_inputs` is **not refused**. It is emitted with
`untrusted_instruction_risk: true`, and the staged-record contract's existing
automatic-defer rule takes over — a steward cannot then accept it without
correcting the assessment. Reusing that gate beats duplicating it: a second
implementation of the same rule is a second place for it to drift.

### Exporting

Everything here expires, so an entry that matters beyond its TTL has nowhere to
survive. `export` is the rescue hatch: one `<handle>.md` per entry, frontmatter
plus the stored content, validated in shape against
`context-entry.schema.json`. Nothing is ever exported automatically. This is
separate from automatic final-handoff capture: capture keeps its structured
handoff and identifier-only artifact manifest as ephemeral local working
material; export creates a durable repository file.

The destination is normally a git-committed run directory, and that changes the
exposure in two ways the command enforces before writing anything:

- **Classification.** `restricted` is refused outright — no flag permits it.
  `confidential` requires `--acknowledge-commit`. A filter inside a gitignored
  local database and a file in a cloneable repository are not the same thing.
- **Provenance.** An entry flagged `untrusted_inputs` is refused without
  `--include-untrusted`, and carries a loud banner when included. This closes a
  git-shaped version of the laundering path: retrieval through the store fences
  content as untrusted, but a committed Markdown file has no fence, and the next
  agent to read it meets it as ordinary repository content.

Refusals are collected and reported together, before any file is written, so a
batch never leaves a half-exported directory behind. The write itself is
atomic against a filesystem failure too, not only a policy refusal: every
render lands in a private staging directory first, and only moves into
`--output` with an atomic rename once the whole batch has rendered
successfully. A disk-full or permission error on entry N is refused the same
way — `--output` is left exactly as it was found, not holding files `1..N-1`.

**There is no `--check` mode**, deliberately, and this is where the analogy with
`cadre knowledge export-staged` stops. That command had one because its
committed snapshot was meant to *track* the store, so a difference was drift
worth reporting. (`export-staged` itself was removed in `b418031e` and never
rebuilt; the analogy is to what it did, not to anything runnable now.) Nothing of the kind holds here: entries expire by design, so a
comparison would flag ordinary, intended expiry as drift — reporting correct
behaviour as a defect. An export from this store is a point-in-time rescue, not
a mirror, and nothing keeps the two in step afterwards.

### `untrusted_inputs`

Set when the submitted text trips injection detection, when any
`--derived-from` handle already carries it, or when a `--derived-from` value is
an unverifiable or expired handle. **It cannot be cleared by the writing
agent.**

This closes the laundering path: agent A reads a poisoned file, summarizes it
into an entry, and agent B reads the summary back as "our own working notes",
affording it more trust than the original source earned. The summary's own text
is clean, so only propagated provenance can flag it.

`ks:untrusted:<id>` is the marker for a knowledge citation whose retrieval
reported `untrusted_instruction_risk: true`. It is caller-asserted — this store
may not read the knowledge database — but its effect is one-directional, so a
caller can only ever make an entry *more* suspect by supplying it.

## Security boundary

Stored content is untrusted working context. It was written by an agent, has
received no steward disposition, and must never be treated as instruction. Read
`SECURITY.md` before connecting this store to anything.
