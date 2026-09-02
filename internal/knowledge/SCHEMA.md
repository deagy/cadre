# Staged-record schema

`staged-records.db`, beside whatever database the knowledge config names.

Its own file rather than the configured one. That path names a **recall**
store now, and cadre's governance tables have no business inside a database
recall's own backup, restore and migration tooling operates on without knowing
they are there.

The corpus schema this file used to describe — `messages`, `chunks`,
`ingestion_runs`, `retrieval_runs`, `deletion_runs` — is gone with the
retrieval engine. Chunks and their embeddings are recall's; the audit of what
was retrieved is an append-only `retrievals.jsonl` beside the store, written
by `internal/retrieval`.

## Tables

```sql
CREATE TABLE IF NOT EXISTS staged_records (
  id TEXT PRIMARY KEY,
  status TEXT NOT NULL,
  frontmatter_json TEXT NOT NULL,
  body TEXT NOT NULL,
  content_digest TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_staged_records_status ON staged_records(status);
CREATE TABLE IF NOT EXISTS staged_record_dispositions (
  record_id TEXT NOT NULL REFERENCES staged_records(id) ON DELETE CASCADE,
  sequence INTEGER NOT NULL,
  action TEXT NOT NULL,
  reason TEXT NOT NULL,
  classification_used TEXT NOT NULL,
  diverged_from_proposal INTEGER NOT NULL,
  decided_by TEXT NOT NULL,
  decided_at TEXT NOT NULL,
  PRIMARY KEY (record_id, sequence)
);
CREATE TABLE IF NOT EXISTS staged_record_imports (
  record_id TEXT NOT NULL,
  content_digest TEXT NOT NULL,
  status_at_import TEXT NOT NULL,
  authorized_by TEXT NOT NULL,
  directory TEXT NOT NULL,
  imported_at TEXT NOT NULL,
  PRIMARY KEY (record_id, imported_at)
);
CREATE TABLE IF NOT EXISTS staged_record_deletions (
  record_id TEXT NOT NULL,
  title TEXT NOT NULL,
  content_digest TEXT NOT NULL,
  status_at_deletion TEXT NOT NULL,
  reason TEXT NOT NULL,
  deleted_by TEXT NOT NULL,
  authorized_by TEXT,
  deleted_at TEXT NOT NULL,
  PRIMARY KEY (record_id, deleted_at)
);

-- What a steward made retrievable, and where.
--
-- Kept here rather than derived from the corpus. The corpus is recall's now,
-- and recall can be asked for a chunk by id but not for what matches a
-- metadata scope -- so "has this record been ingested?" has no answer over
-- there. It is also the better home: the fact that a steward's acceptance was
-- carried out is governance evidence, and it should outlive any particular
-- store being rebuilt.
CREATE TABLE IF NOT EXISTS staged_record_ingestions (
  record_id TEXT PRIMARY KEY,
  document_id TEXT NOT NULL,
  corpus TEXT NOT NULL,
  classification TEXT NOT NULL,
  chunk_count INTEGER NOT NULL,
  ingested_at TEXT NOT NULL
);```

## What each one is for

**`staged_records`** — the proposal itself: frontmatter, body, and the digest
that with the id forms its durable identity. `status` moves proposed →
accepted/rejected/deferred and nothing else writes it.

**`staged_record_dispositions`** — append-only decision history. A record
dispositioned before this table existed carries a disposition with no history;
that is a readable state, not a repair job.

**`staged_record_imports`** — who authorized admitting a batch of
already-decided records. Importing a decided corpus admits decisions this
store never saw made, which is why it names an accountable human.

**`staged_record_deletions`** — deletion evidence that outlives the record it
describes. No foreign key, on purpose: evidence with a cascade is evidence
that disappears with its subject. Read back with `cadre knowledge
deletion-evidence-staged`; `show-staged` cannot, because it resolves a record
by id and the record is what the deletion removed.

**`staged_record_ingestions`** — what a steward made retrievable, and where.
Read rather than derived: the corpus is recall's, and recall can be asked for
a chunk by id but not for what matches a metadata scope. It is also the better
home — a steward's acceptance having been carried out should outlive any
particular store being rebuilt.

## Driver

`modernc.org/sqlite`, pure Go, the same driver recall uses. Pragmas travel in
the DSN rather than in `Exec` calls: `database/sql` hands out pooled
connections, so a `PRAGMA` run after opening applies to whichever connection
served it and to no other. That is not theoretical — a `busy_timeout` set that
way was absent on the next connection, and two concurrent writers failed with
"database is locked".

## Backup

Copy the file. `staged-records.db` is small and cadre ships no backup verb:
one that copied nothing while reporting success is a defect this repository
has already had once.
