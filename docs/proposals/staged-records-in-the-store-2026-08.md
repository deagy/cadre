# Proposal: stage knowledge records in the store, not in git

Status: **PROPOSED — decisions taken, not yet scheduled.** The four design
decisions below were settled by the Product Owner on 2026-08-09; the work
itself is not approved by this document.
Task ID: `staged-records-in-the-store-2026-08-09`
Classification: internal
Author role: governance-planner (Cadre suite)
Requested by: repository owner / declared Product Owner
(`roster/shared/team-profile.yaml`)
Required approver: Product Owner.

Supersedes the storage half of
[`durable-knowledge-capture-2026-08.md`](durable-knowledge-capture-2026-08.md)
(issue [#165](https://github.com/deagy/cadre/issues/165)). The contract,
schema, validator, and disposition model that proposal produced all stand —
only the substrate changes.

## The problem, measured

Capturing a finding currently costs a pull request. On 2026-08-09 three
records-only pull requests (#172, #173, #174) each ran the full 15-job
`validate.yml` matrix — the G1–G10 kernel suite, the LangGraph engine, three
npm workspaces, both install-script legs, the pip distribution — for markdown
files in a staging directory that nothing consumes yet. Then each ran again on
`main`, where two of the three post-merge runs were cancelled by the following
merge (`concurrency.cancel-in-progress` keyed on `github.ref`).

That is the wrong cost curve for the one activity the capture mechanism exists
to make routine. A tax paid per finding is a tax on noticing things.

Two cheaper options were considered and rejected as insufficient: batching
records into fewer pull requests (helps, but keeps git as the substrate), and
path-filtered CI (helps, but the 12 *required* status checks would never report
and the pull request would block rather than merge quickly — the correct shape
there is an early-exit step inside each job, not a workflow-level filter).

## The design

**Move the instances into the store. Keep the contract in git.**

### 1. A `staged_records` table

`roster/knowledge-store/src/database.py` already creates its four tables with
`CREATE TABLE IF NOT EXISTS`, so a fifth is additive and existing stores pick it
up on next open. Columns mirror the frontmatter contract one-for-one — `id`
(primary key), `title`, `status`, `evidence`, `origin_task`, `origin_artifact`,
`origin_revision`, `proposed_classification`, `source_scope`,
`sensitivity_notes`, `conflicts_or_staleness`, `recommended_action`,
`untrusted_instruction_risk`, `staged_by`, `content_digest` — plus `body`,
`created_at`, and the disposition fields (`action`, `reason`,
`classification_used`, `diverged_from_proposal`, `decided_by`, `decided_at`).

Disposition history is **append-only** rather than an overwrite: a record that
is deferred and later accepted must retain both, or the audit trail is worse
than the git one it replaces.

### 2. One validator, two substrates

`roster/knowledge-store/src/staged_records.py` keeps `compute_digest`,
`parse_record`, and every rule. The database becomes a second *storage backend
behind the same validation*, never a second implementation of it.

This is the part to guard hardest. A second copy of the digest normalisation or
the disposition-linkage rules is exactly the defect class this repository keeps
rediscovering, and the module's own error message already says so: *"Never
compute it by hand: a second implementation of the normalisation is how a digest
silently stops meaning anything."*

The round trip is the invariant worth testing directly:
`parse(export(record)) == record`, digest included.

### 3. CLI verbs on `cadre knowledge`

- `propose --input <file|->` — validate against the schema, then insert at
  `status: proposed`. **Validation moves from merge time to write time**, which
  is strictly better: a malformed record never exists rather than being caught
  later.
- `list --status proposed` — what awaits the steward.
- `show <id>` — a database row cannot be read in a diff, so discoverability has
  to be built rather than assumed. This is not optional polish; without it the
  corpus is invisible.
- `disposition <id> --action … --reason … --decided-by …` — enforces the
  action/status agreement rule and the `untrusted_instruction_risk` automatic
  defer.
- `export [--status …] --output <dir>` — writes records back out as
  markdown+frontmatter, byte-identical to what `propose` would accept.
- `delete <id>` — see decision 3.

### 4. Remove the guards that would become vacuous

Once `proposed-knowledge/` empties, the `staged-knowledge-records` pre-commit
hook and the directory validation inside the knowledge-store test suite pass
while checking nothing. They must be removed in the same change. Leaving them is
the precise failure this repository has spent a week finding instances of.

## Decisions taken (Product Owner, 2026-08-09)

### Durability — periodic committed export

The store is gitignored (`.gitignore:45-46`, "operator-controlled"), so records
in it have no backup, no cross-machine sync, and no collaborator visibility.
`cadre knowledge export` writes them back to a committed directory on demand —
before a release, or on a cadence — so capture stays cheap while durability
becomes a deliberate batched act rather than a per-record tax.

**Consequence to design for:** the export must round-trip losslessly, including
`content_digest` and full disposition history. An export that quietly drops a
field turns the backup into a corruption vector, and it would be discovered only
when the store was lost — the worst possible moment.

### The ten existing records — migrate, then delete from git

All ten are imported via `propose`, preserving ids, digests, and the two
dispositions recorded in #173. `roster/knowledge-store/proposed-knowledge/` is
then removed. Single source of truth; no drift between two homes.

The migration is the first real test of the import path, and should be run as
one: import, `export` to a temporary directory, and diff against the original
files. Any difference is a defect in the round trip, found before the originals
are deleted rather than after.

### Deletion — implement it in this work

`SECURITY.md:36` names authorized lifecycle operations and evidence as a
prerequisite for production use, and the absence of deletion is the stated
reason nothing has been ingested. In a table, deletion is a row delete, so C
makes it cheap almost incidentally.

Requirements, not merely a `DELETE`:

- Steward-only, consistent with
  `roster/shared/agent-autonomy.yaml`'s
  `ingest_update_reclassify_or_delete: knowledge_store_steward_only`.
- Deletion evidence recorded — who, when, why, and the record's `id` and
  `content_digest` — retained after the record itself is gone. A deletion that
  leaves no trace is indistinguishable from data loss.
- Deleting an *accepted* record still escalates to an authorized human;
  implementing the capability does not lower the authority required to use it.
- `roster/workflows/knowledge-ingestion.md:22` and `SECURITY.md:36` both state
  the capability does not exist. Both must be updated, or the documents become
  the stale guidance the knowledge store itself warns about.

**The consequence most likely to be missed:** `recommended_action` deliberately
has no `delete` value, and `knowledge-use-policy.md` explains why — no
capability supported it. Once deletion exists, that omission must be
*re-decided*, not silently reversed. The conservative answer is that
`recommended_action` still excludes `delete`, because proposing deletion and
being authorized to perform it are different acts; that should be stated rather
than assumed either way.

### Scope — project store, per repository

Records about Cadre live in Cadre's store. This repository already has its own
partition (`.agents/knowledge-store/config.json` plus
`data/knowledge.db`), which `SECURITY.md` treats as a real partition rather than
`--source` filtering over a shared store.

Cross-project promotion is deliberately **not** designed here. Some records
generalize (`squash-merge discards merge ancestry`) and some do not
(`glob_to_regex` asymmetries), but a promotion path is a second mechanism and
belongs in its own proposal.

## Sequencing

1. `staged_records` table plus the storage backend behind the existing
   validator; round-trip test first.
2. `propose`, `list`, `show`.
3. Migrate the ten records; verify by export-and-diff **before** deleting
   anything.
4. `disposition`, preserving append-only history.
5. `export`.
6. Turn the git directory into a generated export snapshot (**revised** --
   see below).
7. `delete`, with evidence, and the documentation corrections it forces.

Steps 1–3 are the load-bearing ones: if the round trip is not lossless, nothing
after it is safe.

## Step 6, revised during implementation

The original step 6 said "delete the git directory and the two guards that
would become vacuous". Implementing it surfaced a contradiction with decision 1:
deleting the directory outright leaves the corpus in a gitignored, machine-local
SQLite with no backup, no cross-machine copy, and no visibility to anyone else --
exactly the risk the periodic-export decision exists to close, and there would
be a window with no committed copy at all.

**Revised, and agreed with the Product Owner on 2026-08-09:**
`roster/knowledge-store/proposed-knowledge/` is *kept* and becomes the
generated export snapshot. It is no longer hand-authored, and no longer costs a
pull request per record -- which was the entire point of moving to the store --
but it remains the committed durability copy, refreshed deliberately by
`cadre knowledge export-staged`.

Two consequences follow, and both are improvements on the original plan:

- **The guards are not vacuous and are kept.** `staged_records.py` and the
  `staged-knowledge-records` pre-commit hook now validate the snapshot. A
  malformed record still cannot land.
- **Nothing verifies the snapshot is current.** The store it is exported from
  is gitignored and machine-local, so no CI job can compare them. A stale
  snapshot is possible and will validate perfectly. This is stated in the
  directory's README rather than left for someone to discover, and it is the
  honest cost of the arrangement.

The tests moved off the corpus in the same change. They had been using the ten
committed records as fixtures, which made them hostage to whatever the corpus
happened to contain -- one assertion had already over-fitted to "the corpus
holds only proposed and accepted records", which was true of the data and not of
the contract. `roster/knowledge-store/test/fixtures/` now holds purpose-built
records covering the dispositioned, untrusted-flagged, and serialisation-hazard
cases deliberately.

## Non-goals

- **No change to who may do what.** Ordinary agents propose; only the steward
  dispositions, ingests, or deletes. Changing the substrate must not change the
  authority model.
- **No automatic ingestion.** `accepted` remains a disposition on a staged
  record, not an instruction to embed it.
- **No cross-project promotion**, as above.
- **No change to the retrieval path.** Records are not searchable by
  `cadre knowledge search` until they are actually ingested, and the second
  ingest shape that requires (recorded in the capture proposal) is still open.

## Verification

The non-vacuity bar this repository has settled on applies to every guard added
here, and is not optional given the history:

- Round trip: export every record, re-import into an empty store, and assert
  equality including digests and disposition history. Then corrupt one field in
  the exported form and confirm the re-import **fails** naming it.
- Disposition rules: re-run the two injections that already bind in the file
  implementation — `disposition.action` disagreeing with `status`, and an
  `accepted` status with no disposition — against the database backend, and
  confirm they still fail.
- Deletion evidence: delete a record, confirm the evidence row survives, and
  confirm a non-steward path cannot reach the command.
- Removal of the vacuous guards: confirm by injection that nothing else was
  silently relying on them.
