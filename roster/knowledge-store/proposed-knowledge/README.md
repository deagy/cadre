# Staged knowledge records — generated snapshot

**Do not hand-edit the `.md` files here.** They are written by
`cadre knowledge export-staged`, and anything you type into them is lost the
next time the snapshot is refreshed.

## What this directory is now

The store is the source of truth. Records are staged with
`cadre knowledge propose`, listed with `list-staged`, read with `show-staged`,
and dispositioned with `disposition-staged` — all against this project's
SQLite partition under `.agents/knowledge-store/`, which is gitignored and
operator-controlled.

This directory is the **durability snapshot** of that store: a periodic
committed export, refreshed deliberately rather than per record. It exists
because the store is gitignored, so without it the corpus would have no
backup, no cross-machine copy, and no visibility to anyone else.

That split is the whole point of the change. Capturing a finding used to cost
a pull request and a full CI matrix; now capture is a local command and the
committed copy is a batch you refresh when it is worth refreshing.

## Refreshing it

```sh
cadre knowledge export-staged --output roster/knowledge-store/proposed-knowledge
```

Files are named `<record-id>.md`. The id is the durable identity, so the
filename follows it rather than the other way round — several of these records
were first written under quite different names.

A record whose disposition has changed more than once also gets an
`<record-id>.history.json` beside it. The frontmatter carries the *current*
disposition; the earlier ones cannot live there, because the frontmatter
dialect is deliberately one level deep and holds no list of mappings.

## What is checked, and what is not

`staged_records.py` validates every file here — in CI, and in the
`staged-knowledge-records` pre-commit hook. A malformed record cannot land.

**Nothing verifies that this snapshot is current.** The store it is exported
from is gitignored and machine-local, so no CI job can compare them. A stale
snapshot is therefore possible and will look perfectly valid. Refresh it when
you have staged or dispositioned anything you would mind losing.
