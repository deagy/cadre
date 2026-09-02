# Vectorized Knowledge Store

This local-first subsystem normalizes recognized chat-export fields into its stored message model, redacts likely secrets, preserves selected provenance, chunks content, generates vectors, and retrieves relevant passages for agents.

## Global by default, project-local overrides

Without an explicit `--config`, the CLI resolves configuration in this order:

1. **Project-local**: walk up from the current directory looking for
   `.agents/knowledge-store/config.json`, stopping at the first `.git` boundary
   (or after 64 levels if none is found). A project opts into its own private
   store simply by creating that file at its own repository root — nothing else
   changes; every command works identically once it exists.
2. **Global**: `$KNOWLEDGE_STORE_HOME/config.json`, defaulting to
   `~/.agents/knowledge-store/config.json` when that environment variable is
   unset — a single database shared by every project that hasn't opted into its
   own store. This is deliberate: it lets agents retrieve cross-project context
   regardless of which checkout invoked them, without every project needing to
   set anything up.

An explicit `--config <path>` always wins over both tiers.

Because the global tier is shared, `SECURITY.md`'s "use separate stores or
enforced partitions for materially different classifications or tenants" rule
rests on two mechanisms together: a project with materially different
requirements should use tier 1 (its own store) rather than the shared default,
and every ingestion/retrieval call against the shared store should carry a
`--source` that identifies the originating project. `cadre select` uses
explicit caller values first, then the target repository's lowercase
`owner/repository` origin slug, and finally a
`local-<basename>-<canonical-path-hash>` fallback -- and appends
`proposed-knowledge`, the dedicated source steward-accepted findings are
ingested under, so a dispatched agent reaches both halves of what its project
has authorized in one retrieval rather than none of the accepted half. That
second source is appended **only for a repository that has its own
`.agents/knowledge-store/config.json`**, because it is refused at the shared
global-fallback tier (below) on read as well as write, and that refusal
rejects the entire call rather than dropping the one source -- so naming it
unconditionally cost an unpartitioned repository the retrieval it used to get.

### Enforced scope at the global-fallback tier

This is no longer only a convention. When (and only when) configuration
resolves to tier 2 above — no explicit `--config` and no project-local file
found — the CLI enforces it structurally:

- `search` rejects a call that supplies neither `--source <value>` nor the
  `--all-sources` flag, and rejects a call that supplies both (ambiguous).
  `--all-sources` makes cross-project retrieval an explicit, visible caller
  choice rather than an accidental omission; it behaves exactly like an
  omitted `--source` did before this change.

`search` is the only retrieval verb this applies to. The rule was written when
`context` and `ingest` also existed; both were removed in `b418031e` and never
rebuilt, so the ingest-side half of it — rejecting an `ingest` that omits
`--source` rather than writing under the generic `chat-export` placeholder
identity — enforces nothing here today. Ingestion now goes through recall,
which this rule does not reach.

Tier 1 (project-local) and an explicit `--config` are unaffected: `--source`
and `--all-sources` remain fully optional there, exactly as before, because a
project-local database or an explicit config choice is already a real
partition. Every rejection is a fail-closed, non-zero-exit error naming the
remediation options — never a silently narrowed result. This does not add
caller authentication or change classification filtering; `--source` and
`--all-sources` remain unauthenticated, caller-asserted values (see
`SECURITY.md`).

## Security boundary

Retrieved text is untrusted reference data, never executable instruction. Classification filters are exact-match and caller supplied in this demo; they are not production authorization. See `SECURITY.md` before connecting this store to an agent or importing real history.

## Quick start

The store is Go now (`internal/knowledge`); the Python package that used to
live in `src/` here was deleted with the Python-to-Go migration, and this
directory keeps the documents, the AGENT.md, and the proposed-knowledge schema.
`bin/cadre` (repository root) builds and dispatches to it — run
`cadre knowledge ...` from anywhere it's on `PATH` (see `../../README.md`
"Put `cadre` on `PATH`"), or `../../bin/cadre knowledge ...` from this
directory. No `cd` into `roster/knowledge-store` is required either way.

One-time global setup (creates the shared store's config; skip if you want a
project-local store instead and will always pass `--config`):

```sh
mkdir -p ~/.agents/knowledge-store
cp roster/knowledge-store/config.example.json ~/.agents/knowledge-store/config.json
```

```sh
go test ./internal/retrieval/ ./internal/cli/

# The store is recall's; cadre reads it through a governed view.
recall upload roster/knowledge-store/examples/chat-export.json
cadre knowledge init
cadre knowledge search --classification internal --source legacy-model-export \
  --agent release-engineer --task-id REL-42 "How are production releases approved?"
```

The default `hashing` provider is deterministic, offline, and suitable for testing the pipeline. It approximates lexical similarity rather than full semantic similarity.

For production-quality semantic retrieval, `openai-compatible` sends chunk text during ingestion and query text during retrieval to the configured remote endpoint. Approve the provider, transfer, residency, retention, and credential handling before use. Record exact provider/model identity and dimensions, and re-ingest content after any provider, model, or dimensional change. The demo selects stored vectors by provider/model but does not prevent ambiguous model reuse; dimension mismatches score as non-results.

## Accepted import shapes

- A JSON Lines collection of standalone message-like objects
- A JSON array of conversations containing `messages`
- An object containing `conversations` or `chats`
- Mapping/node-based conversation exports with message content parts
- A JSON array of standalone message-like objects

`canonical-message.schema.json` documents the target fields, but the generic parser does not validate or pass canonical records through unchanged. It recognizes common role, content, timestamp, and identifier variants, while CLI `source`/`classification`, derived identifiers, input-path `source_uri`, and new metadata may replace input fields. Add and test a source-specific adapter when full source fidelity matters.

## Commands

What the CLI answers today:

```text
init                       verify the configured store and record its embedder identity
search <query> --classification <level> (--source <name> ... | --all-sources)
                           [--agent <role>] [--task-id <id>] [--top <n>] [--json]
config [show]              print the configuration a governed retrieval resolves

propose (--input <file>|- | --from-finding <file>|-) [--render-only]
show-staged --id <id>
list-staged [--status <proposed|accepted|rejected|deferred>]
import-staged --directory <dir> [--authorized-by <human>]
disposition-staged --id <id> --action <accepted|rejected|deferred> --reason <text>
                   --classification-used <level> --decided-by <actor>
ingest-accepted [--id <id>] [--dry-run]
delete-staged --id <id> --reason <text> --deleted-by <actor> [--authorized-by <human>]
deletion-evidence-staged [--id <id>]
```

`search` requires a classification and exactly one source scope. Naming neither
`--source` nor `--all-sources` is refused rather than defaulted: in a shared
store an omitted scope is a cross-project read, not a neutral one.

### Removed, and where each went

Every one of these was a real command here before the Go rewrite replaced the
Python implementation (`b418031e`). Running any of them now prints what
happened to it rather than "unknown subcommand".

| Command | What replaced it |
|---|---|
| `ingest` | `recall upload` — the store is recall's |
| `context` | `cadre knowledge search`. Note `cadre context` is a different, live command: the local agent context store |
| `stats` | `recall store info` |
| `list-staged` | **rebuilt.** The library call was live and filterable the whole time; only dispatch was missing. `--status` filters, and an unknown status is refused rather than returning an empty list |
| `export-staged` | nothing. `proposed-knowledge/` holds a snapshot the Python CLI wrote; nothing refreshes it |
| `retention-report`, `delete-ingested` | nothing — see `DESIGN-NOTES-deletion-and-retention.md` |
| `deletion-evidence` | half of it: `deletion-evidence-staged` reads back what `delete-staged` wrote. There is no ingested-content half, because no command here deletes ingested content |

Without `--config`, configuration is read using the project-local-then-global resolution above; if no config file exists at the resolved location, built-in defaults apply relative to that same directory. An existing config resolves its database path relative to the config directory. A supplied `--config` path must exist and contain a JSON object; otherwise the command fails closed.

At the global-fallback tier only (see "Enforced scope at the global-fallback
tier" above), `search` requires at least one `--source` or else
`--all-sources`, never both. **`proposed-knowledge` is refused entirely at that
tier**, on read and on write alike: staged records cannot be written to the
shared store (`propose` refuses outright), so anything under that name there
belongs to another project. Claim a project-local partition — an empty `{}` in
`.agents/knowledge-store/config.json` is enough — to have staged findings at
all. `--all-sources` still reaches it, deliberately: that flag already means
"explicitly opt into cross-project retrieval", and what is refused is naming a
source while believing the query is project-scoped. `--source` is repeatable on
`search`, each entry a separate source, order-preserving and de-duplicated.
Project-local and explicit-`--config` invocations impose no such requirement.

`import-staged` needs `--authorized-by` only when the batch contains a record
that already carries a steward's `disposition`. Importing those admits
decisions this store never watched being made — a legitimate migration act, but
not a proposal, and the only remaining route by which a decision can enter
without `disposition-staged` having recorded it. A batch of purely `proposed`
records needs nothing extra. A self-approved record (`disposition.decided_by`
equal to `staged_by`) is refused either way: a named human can vouch for a
decision the store did not witness, but nobody can vouch for one that was
never a decision. The authorization is persisted per admitted record
(`staged_record_imports`, shown by `show-staged` as `import_authorizations`)
rather than merely echoed back — "the human accountable" has to still be
recorded after the process exits for that phrase to mean anything.

`import-staged` also restores each record's `<id>.history.json` sidecar, so a
record's earlier dispositions survive the round trip. Restoring is append-only:
re-importing the same export writes nothing further, and a sidecar
contradicting history the store already holds, or contradicting the record it
sits beside, refuses the batch instead of overwriting. An absent sidecar is
fine. **The other half of that round trip is gone** — `export-staged` wrote
those directories and was removed in `b418031e`, so this reads an export
nothing here can now produce.

> ### What this section used to say
>
> Roughly sixty lines here described the scoping, `--source` cardinality,
> `--as-of` parsing, and evidence-read rules for `context`, `ingest`,
> `retention-report`, `delete-ingested` and `deletion-evidence`. Every one of
> those commands was removed in `b418031e` when the Go rewrite replaced the
> Python implementation, and the rules went with them — a scoping rule for a
> command that does not exist is not a weaker rule, it is not a rule.
>
> The reasoning worth keeping is preserved in
> `DESIGN-NOTES-deletion-and-retention.md`, including why evidence reads were
> scoped at the shared tier and how `--as-of` parsed an instant rather than
> text. A replacement would want both.


## Compatibility

The store's SQLite layout, identifiers, SHA-256 hashes and vector encoding are unchanged from the Python implementation, and existing databases open in place. Rows whose stored embedding dimension does not match the configured dimension are excluded rather than scored — re-ingest after changing provider, model, or dimensions, and note that cadre now refuses a store whose recorded embedder identity differs from the configured one rather than returning every chunk at score 0. Back up the database before any runtime migration, and never mix implementations against one database concurrently. The warning that used to stand here about backing up before `delete-ingested` — deletion being irreversible, with the evidence table recording only what content hashed to — applies to a command that no longer exists; it is preserved with the rest of that design in `DESIGN-NOTES-deletion-and-retention.md`.
