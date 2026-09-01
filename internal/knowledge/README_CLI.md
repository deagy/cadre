# Cadre Knowledge Store CLI Guide

`cadre knowledge` is a **governed retrieval interface**, not a store. The store
is [recall](https://github.com/deagy/recall); cadre reads it through
`recall/govern`, which refuses a request that has not decided its scope, has
not declared a classification, or cannot be recorded — and refuses before the
store is opened, because an interface that only refuses after connecting has
already revealed that the caller asked.

Storage-side work — creating a store, uploading content, backups, restores,
keyword search — is done with the `recall` CLI directly.

## Installation

```sh
go build -o bin/cadre ./cmd/cadre
```

Every command below is pure Go: retrieval is recall's (`modernc.org/sqlite`)
and the staged store uses the same driver. A `CGO_ENABLED=0` build used to
link cleanly and then fail at the first knowledge query with `go-sqlite3
requires cgo to work. This is a stub`; that is gone.

## Quick start

```sh
mkdir -p ~/.agents/knowledge-store
cp roster/knowledge-store/config.example.json ~/.agents/knowledge-store/config.json

# recall owns the store: create and populate it. Its embedder must be the one
# cadre's config names -- see "Embedders must match" below.
recall upload /path/to/authorized-export.json

# cadre verifies the store and records which embedder produced its vectors.
cadre knowledge init

# Retrieval states its classification and its source scope, or is refused.
cadre knowledge search \
  --classification internal \
  --source legacy-model-export \
  --agent release-engineer \
  --task-id REL-42 \
  "How are production releases approved?"
```

## Command reference

### `cadre knowledge init`

Verifies that the configured store exists and that a governed view can be
constructed over it, then **records which embedder produced the store's
vectors** in `embedder-identity.json` beside the store.

**Creates no store.** A missing store is an error naming `recall upload`,
because a command that quietly created an empty database when a path was wrong
is how an operator ends up searching a store nobody ingested into.

```
--reclaim   Replace a previously recorded identity with this config's
--json      Machine-readable output
```

The identity matters because **recall's schema records nothing about what
embedded a store**. Vectors from a different provider, model or width do not
compare, and the search does not fail. Measured: recall's cosine similarity
returns 0 for vectors of different widths, so the query comes back with every
chunk in scope at score 0, in index order — a full, ordinary-looking result
set with no relevance in it, and an audit row naming an embedder that did not
produce those vectors. `init` makes the operator state the fact, and every
search checks it, so a mismatch is a refusal instead of that.

Like classification and source scope, the recorded identity is asserted and
authenticated by nobody. What it buys is that it cannot be skipped by
omission.

### `cadre knowledge search <query>`

Governed retrieval. Exactly one source scope is required: `--source`
(repeatable, or comma-separated) or `--all-sources`.

```
--classification <cls>   Required. Recorded on the retrieval.
--source <src>           Source scope; repeatable
--all-sources            Deliberately span every source in the store
--agent <name>           Retrieving agent, recorded in the audit row
--task-id <id>           Task this retrieval is for, recorded in the audit row
--top <n>                Results to return (default 10)
--mode vector            The only mode cadre serves
--json                   Emit the retrieval bundle as JSON
```

Results come back inside the untrusted-data envelope: a trust label, the
handling requirements, and a per-result citation. The bundle never carries
`source_uri`, which a store may hold but which can expose a local filesystem
path from the machine that performed the ingestion.

**Every completed retrieval is recorded** to `retrievals.jsonl` beside the
store — query id (not the query text), classification, scope, agent, task,
result count, and the embedder and model that produced the vectors searched. A
retrieval that cannot be recorded is refused rather than served unrecorded.

### `cadre knowledge config [show]`

Prints the configuration a governed retrieval resolves: config tier, store
path, audit log path, and the embedder identity recorded on every retrieval.
Edit the config file itself to change it.

`config get`, `config set` and `config list` retired with the engine. Their
keys configured the SQLite engine cadre no longer owns, and `set` wrote to a
map that was discarded at exit.

## Configuration

```json
{
  "database": "~/.agents/knowledge-store/store.db",
  "embedding": {
    "provider": "local-hashing",
    "model": "",
    "dimensions": 128
  }
}
```

`--config <path>` names a config **file**, not a database. A named-but-missing
config is an error.

The embedder identity is required, not defaulted: the provider and model are
written into every audit row, so a silent default would make retrievals
unattributable. `local-hashing` has no model name of its own, so its width is
its identity and is recorded as `hashing-<n>d`. Every other provider must name
its model.

## Embedders must match

A query is only comparable to the vectors it searches when both came from the
same provider, model and width. cadre records that identity on every retrieval
and checks it against the store's before searching, but **it cannot verify
it** — recall stores chunks and embeddings and nothing about what produced
them, so the check is on a fact the operator states, not one anybody proved.

The practical consequence: `recall upload` embeds with **recall's** configured
embedder (`mock`, `openai`, `cohere`, `ollama`, `onnx`), and cadre's
`local-hashing` is not among them. A store uploaded by recall under cadre's
default config is a store cadre cannot usefully search. Either configure both
sides to the same real embedder, or seed the store with cadre's own provider.

This is a gap in the migration rather than a property of it, and it is
recorded as one.

## Retired commands

The retrieval engine moved to recall. Running any of these names its
replacement and exits 2.

| Retired | Instead |
|---|---|
| `stats`, `health-check`, `diagnostics`, `metrics` | `recall store info` |
| `ingest`, `batch-import` | `recall upload <path>...` |
| `hybrid-search` | `recall hybrid-search <query>` |
| `backup` | `recall store backup <destination>` — cadre's copied nothing and said so; recall's is real |
| `export` | `recall store backup <destination>` |
| `import` | `recall store restore <backup>` |
| `fts5-index`, `fts5-search`, `hybrid-stats` | retired with the engine; `recall hybrid-search` is the keyword-weighted path |
| `fault-tolerance`, `replication`, `maintenance` | retired with the engine |
| `check-integrity`, `repair`, `rebuild-indexes`, `defragment` | retired with the engine; recall's index has its own lifecycle |
| `batch-delete`, `batch-update` | retired with the engine |
| `delete` | `recall` deletes by document or chunk id — see "Deletion" below |

## Deletion

`cadre knowledge delete` is retired. It removed messages from cadre's own
engine, and against a recall store it could not even open the database — it
failed with `no such column: embedding_provider`, because it inherited the
same configured store path the governed verbs use.

Removing content is `recall`'s, by document or chunk id.

**Deletion by retention window, classification, source or age has no
equivalent.** recall deletes by id and cannot enumerate what matches a
metadata scope, so the four modes cadre offered cannot be rebuilt over it
without deleting whatever a capped query happened to return. This is a
capability gap in the migration, recorded as one. Note that
`roster/knowledge-store/SECURITY.md` describes a `delete-ingested` verb with
deletion evidence that the Go CLI never shipped — the policy was already ahead
of the implementation, and this widens that distance rather than creating it.

## Proposal workflow

`propose`, `show-staged`, `import-staged`, `disposition-staged`,
`ingest-accepted` and `delete-staged` are a separate concern from retrieval:
separation of duties over proposed knowledge.

Their store is **`staged-records.db`**, beside the database the config names —
its own file, since that path is a recall store now. Records staged before the
separation are copied across once, on first open, and the originals are left
in place.

`ingest-accepted` is the step that makes a steward-accepted record
retrievable. It uploads to the same recall store `search` reads, through the
same governed view — so a record cannot be written with vectors the store's
other content will never be comparable against. Screening runs first: secret
redaction and injection detection, with a record flagged
`untrusted_instruction_risk` refused rather than handed over.

If the store does not exist yet, `ingest-accepted` creates it and records the
embedder identity, because the vectors it is about to write are its own. A
store that already holds content with no recorded identity is refused —
claiming it would assert exactly the thing nobody can check.

**Ingested records land under one fixed source: `proposed-knowledge`.** Not
the `source_scope` declared on the record — that travels as metadata. So the
scope to search them with is:

```sh
cadre knowledge search --classification internal --source proposed-knowledge "<query>"
```

Searching the `source_scope` you declared returns nothing, with no error,
because it is not a source anything was written under. Worth knowing before
you go looking for a finding you know you ingested.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | Runtime error (store unreachable, retrieval failed) |
| 2 | Usage error, including every governance refusal and every retired verb |
