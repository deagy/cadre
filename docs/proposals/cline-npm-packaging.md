# Why the Cline plugins live outside `plugin/`

Status: **SHIPPED and measured.** Merged in
[#121](https://github.com/deagy/cadre/pull/121) (`13deb5e`), released as
`plugin-v0.13.1` via [#122](https://github.com/deagy/cadre/pull/122)
(`ba4cbbd`). A marketplace install went from **277 MB to 11 MB**.
Task ID: `cline-npm-packaging-2026-08-08`
Classification: internal
Author role: cicd-engineer (Cadre suite)

Installing `cadre@cadre-team` wrote **277 MB**, of which **263 MB (95%) was
`node_modules`** — 252 top-level packages a Claude Code user never executes.

## How it happened

The marketplace declares plugin `cadre` with `source: "./plugin"`, and
`plugin/package.json` was an npm **workspace root** for the three Cline plugins.
Something ran `npm install` against that directory at install time.

The evidence that it was install-time rather than cloned:

- `node_modules` is gitignored and **absent from the marketplace's git clone**
  (`~/.claude/plugins/marketplaces/cadre-team/`), yet present in the install
  cache.
- It appeared **13 seconds after** the plugin manifest landed.
- Everything was hoisted to one root, with no nested `node_modules` under the
  three workspaces — npm workspace behaviour, not a copy.

What arrived, none of it reachable from a plugin whose manifest declares only
`"skills": "./skills/"`:

| Package | Size |
| --- | --- |
| `@opentelemetry` | 60 MB |
| `typescript` | 23 MB |
| `@ai-sdk` | 18 MB |
| `@sap-ai-sdk` | 13 MB |
| `@esbuild` | 9.9 MB |

Beyond disk, this ran 252 packages' install lifecycle scripts on every install
of a plugin that never uses them — awkward next to the SBOM, checksum-verified
kernel wheel, and signed tags this repository already ships.

## Two findings that shaped the fix

**`--omit=dev` would not have fixed it.** 311 of 411 lockfile entries are
*production* dependencies of `@cline/sdk`. Dropping dev dependencies leaves
roughly three quarters of the tree.

**The trigger is undocumented.** Claude Code's published docs present npm
install as an opt-in `SessionStart` hook pattern, not automatic behaviour. This
plugin declares no hooks — `/plugin details` reports `Hooks (0)`, and the only
`hooks/` directories belong to the lifecycle sub-plugins, which were not
installed. So something undocumented ran it.

That second finding drove the design more than the first. Any fix that depends
on *how* the trigger works is a fix that can silently stop working. The chosen
one does not: after this change there is no `package.json` anywhere under
`plugin/`, at any depth.

## What was rejected

**Moving only the workspace root to the repository root**, leaving
`plugin/cline*` in place. Far less disruptive — every documented path,
including `cline plugin install ./cadre/plugin/cline`, would have stayed valid.
Rejected because it puts a `package.json` at the root of the tree the
*marketplace clone* checks out, and with an undocumented trigger that risks
reintroducing the same 263 MB one directory over.

**Dropping the workspace root, giving each plugin its own lockfile.** Requires
knowing whether the installer recurses into subdirectories — exactly the
undocumented question. Also costs three lockfiles, three un-hoisted trees, and
an SBOM step that merges three files.

## What moved

```
plugin/package.json       → cline-plugins/package.json
plugin/package-lock.json  → cline-plugins/package-lock.json
plugin/cline/             → cline-plugins/cline/
plugin/cline-agents/      → cline-plugins/cline-agents/
plugin/cline-lifecycle/   → cline-plugins/cline-lifecycle/
```

The workspace root was also renamed from `cadre-lifecycle` (the archived
repository's name) to `cadre-cline-plugins`. The lockfile's two embedded name
fields were edited by hand rather than regenerated, so no dependency resolution
could drift — the SBOM must describe the same tree it did before.

### The one code change

All three plugins resolved the CLI as `resolve(MODULE_DIR, "..", "bin",
"cadre")` — one level up, reaching `plugin/bin/cadre`. From
`cline-plugins/<name>/` that becomes two levels, reaching the repository's own
`bin/cadre`. One added `".."` each.

Their comments claimed these plugins sat "at this repository's root", which was
already wrong before the move; they are now accurate.

### Not the fix, but found while making it

- `cline-agents`' test resolved the knowledge-store CLI relative to its own
  parent. That path now lives in a different top-level directory, so it was
  repointed at the packaged plugin explicitly. Shipped code was unaffected — it
  takes the CLI path from its caller.
- All 8 plugin manifests advertised `author.url`, `homepage`, and `repository`
  as `github.com/deagy/cadre-lifecycle`, which is archived. That is what users
  saw in `/plugin details`. Now `github.com/deagy/cadre`.
- `README.md` claimed the workspace root declared a `cline.plugins` key that
  made git-URL installs resolve without a recursive scan. **No such key exists,
  and none did** — the claim described the archived repository. Corrected to
  point at the local-install form rather than moved to a new path.
- The root `.gitignore` said "This repository has no Node code of its own since
  the plugin split." False since the merge.

## Verification

`plugin/` contains no `package.json` at any depth (`git ls-files
'plugin/**/package.json'` is empty). All three Cline workspaces pass `npm test`
and `npm run typecheck` from the new location. 764 orchestration, 175 shared,
42 knowledge-store, and 104 plugin-tools tests pass. `generate-plugin --check`
and `generate-role-metadata --check` are clean. The lockfile still yields 387
distinct package names with `@cline/sdk`, `@cline/shared`, and `zod` present,
so the release SBOM guard's `>= 200` assertion still holds.

### Measured after release (`plugin-v0.13.1`)

Installed from the marketplace, not from a checkout:

| | Before (0.13.0) | After (0.13.1) |
| --- | --- | --- |
| Install size | 277 MB | **11 MB** |
| `node_modules` in the install | 263 MB, 252 packages | none |
| `package.json` in the install | 1 | none |

74 agents, 8 skills, and the routing acceptance checks (`cicd-engineer`,
`visual-designer`, `ai-engineer`) all still resolve from the installed copy.

## The rejected option would have worked

`~/.claude/plugins/marketplaces/cadre-team/` has **no `node_modules`** — 32 MB,
just the git clone. npm install does *not* run against the marketplace
checkout.

That was the entire basis for rejecting the simpler fix: moving only the
workspace root up to the repository root, which would have kept
`cline plugin install ./cadre/plugin/cline` valid and required no code change
at all. The risk it was rejected for does not exist.

Recorded plainly, because the decision reads as more obviously correct than it
was. The information was not available before the change — the only way to
learn it was to release something — and the failure being insured against was
silently re-shipping 263 MB to every user. But the cost was real: Cline users'
documented install path changed to buy insurance that turned out to be
unnecessary. Anyone revisiting this should weigh that rather than assume the
caution was vindicated.

It does not follow that the layout should be reverted. `cline-plugins/` keeps
npm out of *both* trees rather than relocating it into one of them, and undoing
it would cost Cline users a second path change to reverse a completed one.

## Still open

The Cline plugins stay. README.md's [Cline CLI section](#cline-cli) notes that cline CLI 3.0.46 cannot
invoke *any* locally-installed plugin's tool — affecting Cline's own example
plugin — so this cannot be smoke-tested end to end on Cline today. CI's per-workspace
`npm test` and `typecheck` are the available signal.
