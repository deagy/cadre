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
`local-<basename>-<canonical-path-hash>` fallback -- and always appends
`proposed-knowledge`, the dedicated source steward-accepted findings are
ingested under, so a dispatched agent reaches both halves of what its project
has authorized in one retrieval rather than none of the accepted half.

### Enforced scope at the global-fallback tier

This is no longer only a convention. When (and only when) configuration
resolves to tier 2 above — no explicit `--config` and no project-local file
found — the CLI enforces it structurally:

- `search`/`context` reject a call that supplies neither `--source <value>`
  nor the new `--all-sources` flag, and reject a call that supplies both
  (ambiguous). `--all-sources` makes today's cross-project retrieval an
  explicit, visible caller choice rather than an accidental omission; it
  behaves exactly like an omitted `--source` did before this change.
- `ingest` rejects a call that omits `--source` instead of silently writing
  under the generic `chat-export` placeholder identity.

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

Requires Python 3.10 or newer and uses only the standard library. `bin/cadre`
(repository root) resolves an interpreter for you and dispatches to this
package's `src/cli.py` — run `cadre knowledge ...` from anywhere it's on
`PATH` (see `../../README.md` "Put `cadre` on `PATH`"), or
`../../bin/cadre knowledge ...` from this directory. No `cd` into
`roster/knowledge-store` is required either way.

One-time global setup (creates the shared store's config; skip if you want a
project-local store instead and will always pass `--config`):

```sh
mkdir -p ~/.agents/knowledge-store
cp roster/knowledge-store/config.example.json ~/.agents/knowledge-store/config.json
```

```sh
python3 -m unittest discover -s roster/knowledge-store/test -p "test_*.py"
cadre knowledge init
cadre knowledge ingest --input roster/knowledge-store/examples/chat-export.json --source legacy-model-export
cadre knowledge context --agent release-engineer --task-id REL-42 --query "How are production releases approved?" --classification internal --top 5
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

```text
init
ingest --input <file> [--source <name>] [--classification <level>] [--retention-days <n>]
search --query <text> --classification <level> [--top <n>] [--source <name> ... | --all-sources]
context --agent <role> --task-id <id> --query <text> --classification <level> [--top <n>] [--source <name> ... | --all-sources]
stats
retention-report [--as-of <iso-8601 date or timestamp>]
delete-ingested --scope {source|conversation|message} --id <id> --reason <text> --deleted-by <actor> --authorized-by <human> --trigger <trigger> [--source <name>] [--dry-run]

propose (--input <file>|- | --from-finding <file>|-) [--render-only]
list-staged [--status <status>]
show-staged --id <id>
import-staged --directory <dir> [--authorized-by <human>]
export-staged --output <dir> [--status <status>] [--check]
disposition-staged --id <id> --action <accepted|rejected|deferred> --reason <text> --classification-used <level> --decided-by <actor> [--diverged-from-proposal]
delete-staged --id <id> --reason <text> --deleted-by <actor> [--authorized-by <human>]
deletion-evidence [--source <name> | --all-sources]
```

`import-staged` needs `--authorized-by` only when the batch contains a record that already carries a steward's `disposition`. Importing those admits decisions this store never watched being made — a legitimate migration act, but not a proposal, and the only remaining route by which a decision can enter without `disposition-staged` having recorded it. A batch of purely `proposed` records needs nothing extra. A self-approved record (`disposition.decided_by` equal to `staged_by`) is refused either way: a named human can vouch for a decision the store did not witness, but nobody can vouch for one that was never a decision. A `README.md` in the directory is skipped, matching `export-staged --check`; any other unparseable file fails the whole batch.

Without `--config`, configuration is read using the project-local-then-global resolution above; if no config file exists at the resolved location, built-in defaults apply relative to that same directory. An existing config resolves its database path relative to the config directory. A supplied `--config` path must exist and contain a JSON object; otherwise the command fails closed.

At the global-fallback tier only (see "Enforced scope at the global-fallback
tier" above), `search`/`context`/`deletion-evidence` require at least one
`--source` or else `--all-sources`, never both. **`proposed-knowledge` is
refused entirely at that tier**, on read and on write alike: staged records
cannot be written to the shared store (`propose` refuses outright), so
anything under that name there belongs to another project, and a dispatch plan
names the source in every retrieval. Claim a project-local partition -- an
empty `{}` in `.agents/knowledge-store/config.json` is enough -- to have
staged findings at all. `--all-sources` still reaches it, deliberately: that
flag already means "explicitly opt into cross-project retrieval", and what is
refused is naming the source while believing the query is project-scoped. `--source` is repeatable on
`search`/`context` (each entry is a separate source to search, order-preserving
and de-duplicated); it stays single-valued on `ingest`, `delete-ingested`, and
`deletion-evidence`, which each act on exactly one source. `ingest`/`delete-ingested`
require an explicit `--source`. Project-local and explicit-`--config` invocations impose no such
requirement. `deletion-evidence` is scoped for the same reason retrieval is:
an evidence row is not content, but it carries the deleting project's
identifier, its steward's free-text reason, and asserted actor identities, so
reading every project's rows out of a shared store is a cross-project read and
has to say so. A source-scoped read returns ingested-content evidence only --
staged-record deletions carry no source to filter by and cannot exist in the
shared store at all.

`retention-report --as-of` takes an ISO-8601 date or timestamp and is compared
as an instant, not as text: a date alone means midnight UTC starting that day
(pass a full timestamp to include that day's expiries), a value without an
offset is read as UTC, and anything unparseable is refused rather than
silently sorted against stored values.

`context` is the agent-facing command. It returns a schema-versioned bundle containing trust requirements, citations, and retrieved passages. `search` is a lower-level diagnostic command. Both require an explicit classification and apply exact-match classification and optional source filtering before ranking. `--top` must be an integer from 1 through 20, enforcing the orchestration policy limit.

`context` records `query_hash`, `task_id`, `agent`, `classification`, `source_filter`, embedding provider/model, requested top, result count, and creation time. `source_filter` holds a JSON array of the sources the call named (NULL for `--all-sources`); **rows written before `--source` became repeatable hold a bare source string instead**, and this append-only log is never rewritten, so a reader must accept both encodings. It does not record an authenticated subject, tenant/project/environment scope, authorization decision or policy version, nor returned chunk/citation identifiers. Production auditing must add those fields and derive access from authenticated claims.

Read-only retrieval means agents cannot mutate stored content or lifecycle state. Opening any command, including `context`, can nevertheless create the database directory, SQLite file, schema, and WAL files; `context` also writes retrieval metadata. Grant that operational write access separately from content-steward authority. Citations use `source`, `conversation_id`, `message_id`, `chunk_id`, `content_hash`, `created_at`, and `classification`. The database retains `source_uri` for steward provenance, but retrieval output omits it by default because it may expose a local path. `content_hash` covers stored redacted content, not the original source.

Citations are point-in-time references, not immutable or permanently stable identifiers: re-ingestion can update content under the same source/conversation/message identity. Preserve each retrieved bundle plus its integrity hash for review/compliance evidence until the store provides versioned or append-only content and audits returned result snapshots.

The demo now has retention and deletion commands, added by issue #184. This corrects an
earlier statement here that was already false once #181 shipped `delete-staged`, `deletion-evidence`,
and the rest of the staged-record lifecycle commands listed above -- and it is now false in a second,
larger way: `ingest` records a per-message retention window (`retention_until`, config's `retention`
block). Every shipped default is indefinite -- no window is recorded for `internal`,
`confidential`, or `public` unless a caller passes `--retention-days` or a project configures
one -- and `restricted` is refused outright unless `--retention-days` is passed explicitly.
Indefinite is a deliberate placeholder, not a judgement that content should be kept forever:
concrete windows are an open Product Owner / Engineering Lead decision recorded in
`roster/shared/team-profile.yaml`, and shipping working day-counts ahead of it would let them
become policy by default inertia. Until windows are configured, nothing ages out on its own and
`retention-report` has nothing to report; deletion is entirely steward-initiated. `retention-report`
lists expired content read-only, without deleting anything; and `delete-ingested` is the
steward-only, evidenced capability that actually removes ingested messages and their chunks by
`--scope {source|conversation|message}`, always requiring `--reason`, `--deleted-by`,
`--authorized-by`, and `--trigger`. See `SECURITY.md` for the full contract (including the
`delete_status` field that distinguishes a confirmed-removed deletion from a merely-attempted or
failed one), honest limits, and the distinction
from `delete-staged` (which never touches ingested content at all). Its ingestion response
additionally reports the resolved `retention_until`; redaction and embedding summaries still
require supplemental steward records until implemented.

## Compatibility

The Python implementation retains the existing SQLite tables, indexes, identifiers, SHA-256 hashes, JSON vector encoding, hashing-vector algorithm, and provider/model selection. Existing databases are opened in place: a store created before `retention_until` existed gains that nullable column additively on next open (`database._migrate_additive_columns`), with pre-existing rows reading back as "no window recorded" rather than failing to open. Rows whose stored embedding dimension does not match the configured dimension are excluded rather than scored; re-ingest after changing provider, model, or dimensions. Back up the database before any runtime migration, before running `delete-ingested` (deletion is intentionally irreversible -- the evidence table records that a deletion happened and what its content hashed to, not the content itself, so a backup is the only way to recover the actual data), and never mix implementations against one database concurrently.
