# `internal/knowledge`

What is left of cadre's knowledge store after the retrieval engine moved to
[recall](https://github.com/deagy/recall): **the governance record over
proposed knowledge**, plus the configuration and embedding providers the
governed retrieval path needs.

There is no corpus here. There is no search, no index, no chunk table, no
retention sweep. Retrieval is `internal/retrieval`, over a recall store,
behind `recall/govern`.

## What this package owns

**Staged records** (`staged_*.go`) — the proposal workflow, and the reason
this package still exists. An agent proposes a durable finding; a steward
dispositions it; an accepted record is made retrievable. Separation of duties
is enforced on four verbs rather than assumed on one: `propose` refuses a
record arriving already dispositioned, `disposition-staged` refuses a decider
equal to the stager, `import-staged` requires a named authorizing human for
any batch carrying a disposition, and `ingest-accepted` checks the
stager/decider match again as the last point a self-approved record can still
be stopped.

Its store is `staged-records.db`, beside whatever database the knowledge
config names — **its own file**. That config now names a recall store, and
cadre's governance tables have no business inside a database recall's own
backup, restore and migration tooling operates on without knowing they are
there. Records staged before that separation are copied across once, on first
open, leaving the originals in place.

**Configuration** (`config.go`) — the three-tier resolution behind `--config`,
the four classifications, and the validation that refuses a label no policy
describes.

**Embedding providers** (`embeddings.go`, `remote_embeddings.go`) — local
hashing and OpenAI-compatible. A provider is not a store: `internal/retrieval`
takes any `EmbeddingProvider` and records its name and model on every
retrieval, because a retrieval is only reproducible against the model that
produced the vectors it searched.

## Where an accepted record goes

`ingest-accepted` hands the record to a `Corpus` — an interface, because the
destination is not cadre's any more. `internal/retrieval` implements it over
the same governed view the read path uses, so a record cannot be written with
vectors the store's other content will never be comparable against.

Screening still happens here: secret redaction and injection detection run
before anything crosses that boundary, and a record flagged
`untrusted_instruction_risk` is refused rather than handed over. "An agent
wrote it" is not provenance.

Whether a record has been made retrievable is read from
`staged_record_ingestions`, cadre's own evidence, rather than from the corpus.
recall can be asked for a chunk by id but not for what matches a metadata
scope — and a steward's acceptance having been carried out is governance
evidence that should outlive any particular store being rebuilt.

## No cgo

Both halves of the knowledge path are pure Go: the staged store uses
`modernc.org/sqlite` and retrieval is recall's, which uses the same. A
`CGO_ENABLED=0` build of cadre used to link cleanly and then fail at the first
knowledge query with `go-sqlite3 requires cgo to work. This is a stub`. That
is gone. `internal/contextstore` and `internal/engine/executor` still need
cgo, so cadre as a whole is not cgo-free — the knowledge store is.

## See also

- `README_CLI.md` — command-line usage
- `SCHEMA.md` — the staged-record tables
- `../retrieval/` — the governed retrieval path
