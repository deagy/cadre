# Retention and deletion of ingested content — design notes

**Nothing described here is currently implemented.** This is preserved design
intent, not a description of the shipped system.

`cadre knowledge retention-report`, `delete-ingested` and `deletion-evidence`
were real, tested commands in `roster/knowledge-store/src/cli.py`, removed
wholesale in `b418031e` when the Go replacement landed. They were never
rebuilt. Ingested content now lives in a recall store, whose Go API deletes by
document or chunk id and whose CLI exposes no delete command at all — so there
is no steward-facing way to remove ingested content today, and nothing reports
what has expired.

This note exists because the reasoning is worth more than the code was. It
records why retention defaults were indefinite rather than guessed, why
deletion evidence commits in two phases, and which guarantees the Python
implementation deliberately did **not** make. Whoever decides whether to
rebuild this needs all three, and none of it is recoverable from a deleted
file.

Kept verbatim from `SECURITY.md`, where it sat as present-tense description of
a shipped capability. The words are unchanged; only the claim they make about
the world has been corrected.

## Why this is worth rebuilding carefully, or declining deliberately

Read the limits section below before treating a rebuild as mechanical. Three
of the honest limits it records — that a deletion cannot redact what a past
retrieval already returned, that exported bundles are outside its reach, and
that residue reclaim covers only the live database file — are properties of
the *problem*, not of the Python implementation. Any replacement inherits
them, and a replacement that quietly does not say so would be a weaker
artifact than the one it replaced.

The same applies to the caller-asserted identity trade: `--deleted-by` and
`--authorized-by` were authenticated by nobody, *including* the case of one
actor asserting both roles, which was accepted and recorded as written. That
was stated plainly rather than hidden, and it is the standard a rebuild should
meet.

---

## Preserved text

The demo implements retention and deletion of **ingested content** -- messages, chunks, and their embeddings -- as of issue #184. `cadre knowledge ingest` records a per-message retention window (`--retention-days`, or the classification default from config's `retention.default_days_by_classification`). **Every shipped default is indefinite/`null` -- no window is recorded for `internal`, `confidential`, or `public` unless a caller or a project's config supplies one.** That is a deliberate placeholder, not a judgement that content should be kept forever: concrete retention windows are an open Product Owner / Engineering Lead decision recorded in `roster/shared/team-profile.yaml`, and shipping working day-counts ahead of that decision would let them become policy by default inertia. Indefinite records "no window has been decided" rather than asserting one nobody chose. The practical consequence, stated plainly: until windows are configured, `retention-report` has nothing to report and nothing ages out on its own -- deletion is entirely steward-initiated. `restricted` is excluded from that placeholder and is still refused unless `--retention-days` is passed explicitly, so the one tier where "kept forever because nobody decided" is least acceptable cannot reach that state by omission. `cadre knowledge retention-report` lists what has expired -- id, source, classification, `retention_until`, and counts, never bodies -- and never deletes anything itself; no sweep or apply command exists, and nothing reachable from `ingest`, `search`, `context`, or `stats` deletes anything either. `cadre knowledge delete-ingested` is the one capability that does, and only it: steward-only, requiring `--scope {source|conversation|message}`, `--id`, `--reason`, `--deleted-by`, `--authorized-by`, and `--trigger` for every scope and every classification (there is no proposer-may-withdraw-their-own-draft exception the way `delete-staged` has for a still-proposed record -- ingested content has already been made retrievable to other agents). `--dry-run` reports what would happen without writing anything.

It writes evidence to `ingested_content_deletions` -- a distinct table with no foreign key to anything it describes, digests and counts only, never content -- in two steps, not one shared transaction: an evidence row is inserted with `delete_status='attempted'` and committed first, so a refusal writes nothing and every attempt is durably on the record even if the process dies before the actual removal runs; the removal itself (`DELETE FROM messages`, cascading chunks, plus `ingestion_runs` redaction for source scope) and the evidence row's flip to `delete_status='completed'` then happen together in one atomic follow-up transaction, so `'completed'` can never be true unless the content is actually gone. **Only `delete_status='completed'` means the content was removed** -- `'attempted'` or `'failed'` both mean it may still be present and must be treated identically as "not confirmed removed"; a steward reading `deletion-evidence` should check this field before treating a row as proof of removal, not just its presence. `reclaim_status` is unrelated to this and reports only whether post-delete disk-residue cleanup ran -- it says nothing about whether content was removed.

Reading that evidence back is scoped, not open: at the global-fallback tier `deletion-evidence` requires `--source` or an explicit `--all-sources`, like `search`/`context`. Digests are not content, but a row still names the deleting project, its steward's free-text reason, and asserted `deleted_by`/`authorized_by` identities, so an unscoped read of a shared store discloses other projects' deletion metadata.

Honest limits on `delete-ingested`, stated plainly rather than left implicit: (i) `retrieval_runs` cannot be linked to deleted content -- no linkage from a retrieval run to the messages it once returned ever existed, so a deletion cannot retroactively redact what a past retrieval already returned to an agent; (ii) preserved retrieval bundles an agent or reviewer kept as evidence (see "Retrieval rules" above) are outside deletion's reach entirely -- deleting the store's copy does not, and cannot, reach a copy already exported elsewhere; (iii) the residue-reclaim step (`PRAGMA secure_delete=ON`, `wal_checkpoint(TRUNCATE)`, `VACUUM`, recorded as `reclaim_status`) covers only the live database file -- never backups, snapshots, or copies, which must be handled separately by whatever retention/deletion process governs them. The evidence table itself is an ordinary SQLite table with no tamper-resistance, the same as `staged_record_deletions` -- anyone with write access to the database file can alter or delete evidence rows directly. `--deleted-by` and `--authorized-by` are caller-asserted values, authenticated by nobody, the same as `--source`/`--all-sources` above: production integration must still authenticate callers and derive these from authorized claims, not trust the flag -- including the case of one actor asserting both roles, which nothing here prevents: `--deleted-by X --authorized-by X` is accepted and recorded as written. Nor are the digests themselves secret-bearing-proof: `content_digest` and the per-message `content_hash` values are unsalted SHA-256 over content that was often short, so evidence retained after a deletion can still confirm that a *guessed* string was once present. That is a deliberate trade (evidence must outlive content) but not a null one.
