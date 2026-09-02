# Staged knowledge records — frozen snapshot

**Do not hand-edit the `.md` files here.** They were written by
`cadre knowledge export-staged`. Nothing validates them and nothing refreshes
them, so a hand edit will not be caught — and will not be reflected in the
store either, which is the authoritative copy.

## What this directory is now

The store is the source of truth. Records are staged with
`cadre knowledge propose`, read with `show-staged`, and dispositioned with
`disposition-staged` — all against this project's SQLite partition under
`.agents/knowledge-store/`, which is gitignored and operator-controlled.
and enumerated with `list-staged`, which filters by `--status`.

This directory was the **durability snapshot** of that store: a periodic
committed export, refreshed deliberately rather than per record. It existed
because the store is gitignored, so without it the corpus would have no
backup, no cross-machine copy, and no visibility to anyone else.

## Refreshing it — not possible

`cadre knowledge export-staged` was removed in `b418031e` when the Go rewrite
replaced the Python implementation, and was never rebuilt. **This snapshot is
frozen at whatever it last held**, and it drifts further from the store with
every disposition. `import-staged` still reads a directory in this format, so
the round trip is half present: the snapshot can be restored into a store, but
no longer produced from one.

Files are named `<record-id>.md`. The id is the durable identity, so the
filename follows it rather than the other way round — several of these records
were first written under quite different names.

A record whose disposition changed more than once also has an
`<record-id>.history.json` beside it. The frontmatter carries the *current*
disposition; the earlier ones cannot live there, because the frontmatter
dialect is deliberately one level deep and holds no list of mappings.

## What is checked, and what is not

**Nothing checks these files.** `staged_records.py` validated every file here,
in CI and in a `staged-knowledge-records` pre-commit hook; both went with the
Python implementation. `internal/knowledge/staged_records.go` carries the
equivalent parsing logic for records the CLI itself handles, but no job points
it at this directory. A malformed record here can land.

**Nothing verifies that this snapshot is current** either, and now nothing
could make it current if it noticed. Treat it as a historical export: useful
as a record of dispositions made up to the point it was last written, and not
as a view of the store.
