<!-- GENERATED FILE: edit the canonical source and regenerate; do not edit this copy. -->

# Monorepo migration and install-UX rework

A record of why this repository absorbed three others, what was decided along
the way, and what is still open. Written after the fact; the phases it
describes are complete.

## Why

Installing this system meant touching three to four GitHub repositories with
no installer and nothing published anywhere — roughly 18–20 discrete manual
actions. The measured cause was duplication, not distribution.

Of `deagy/cadre-lifecycle`'s 500 tracked files, **~340 were generated copies
of `deagy/cadre` content**: `suite/` (175), `agents/` (71), `codex-agents/`
(71), `skills/` (17), plus `agent-catalog.json`, `bin/cadre`, `provider.json`,
`profiles/`, and `extensions/`. `deagy/cadre-profile-secure-cloud` was a
two-file repository whose `profile.json` was **byte-identical** to the copy
already in `cadre`.

An entire coordination layer existed only to keep those copies honest, and
all of it was deletable:

| Machinery | Purpose |
| --- | --- |
| `cadre-ref.txt` | pinned which `cadre` revision the copies came from |
| `drift-check.yml` | weekly: had someone hand-edited a generated file? |
| `regenerate.yml` | on `cadre` tag: regenerate and open a PR |
| `notify-lifecycle.yml` | `repository_dispatch` to trigger the above |
| `apply_regeneration.py` | applied the regenerated diff selectively |
| "regenerate into a scratch dir, `diff -rq`, apply all but README" | the manual procedure |
| three byte-identical `bootstrap_sdlc.py` copies | one per lifecycle plugin |
| `cross-repo-integration` CI job | checked out two repos, `sed`ed hardcoded `/home/deagy/sdk/*` paths, and ran a **committed 0-byte file** — green while testing nothing |

## What changed

| Was | Is |
| --- | --- |
| `deagy/cadre` | `roster/`, `bin/`, `provider/`, `docs/`, `packaging/`, `cadre_cli/` |
| `deagy/agentic-sdlc` | `kernel/`, `engine/`, `docs/kernel/`, `providers/` |
| `deagy/cadre-lifecycle` | `plugin/` |
| `deagy/cadre-profile-secure-cloud` | nothing — already present, byte-identical |

The three upstreams are archived. `deagy/cadre`'s pre-merge history is
preserved on the `archive/pre-monorepo` branch.

## Decisions, including the ones reversed

**Keep the kernel boundary, enforce it differently.** `kernel/` owns G1–G10
gate schemas, run-record validation, and gate-authority semantics; `roster/`
owns roles, routing, and policy. Two repositories cannot import each other's
internals — one tree can, and nothing would have stopped `roster/` from doing
`from agentic_sdlc import validate_repository` and quietly taking over gate
evaluation. `roster/orchestration/test/test_kernel_boundary.py` is the
replacement: it permits exactly two couplings, shelling out to the kernel CLI
and reading `kernel/contracts/*.json` as data. Verified against a planted
violation.

**Generated content is committed — a reversal.** The plan said it would not
be. A GitHub-sourced marketplace serves the repository tree, so an
uncommitted distribution installs a plugin with no roles in it. This is not
the old arrangement returning: source and output now live in one commit and
the `generated-content` CI job regenerates and diffs in the same run, so
drift cannot outlive a pull request.

**No meta-plugin.** Lifecycle governance stays opt-in;
`/plugin install cadre@cadre-team` remains the one command.

**Nothing published to PyPI.** Both names are squatted —
`pip install agentic-sdlc` installs an unrelated third-party project that
looks plausible. Renaming was considered and rejected as a fourth version
line with no distinct audience. Release artifacts with checksums solve the
actual problem; `SECURITY.md` documents the collision.

**Component-prefixed tags.** The monorepo inherited 25 bare `v*` tags
(`v0.1.1`–`v0.16.0`, `v1`–`v7`). An unprefixed `v<version>` scheme collides
from `v0.11.0` on — and would have failed *silently*, matching the release
workflow's already-tagged check and reporting "nothing to do". Tags are now
`plugin-v*` and `kernel-v*`.

## What the merge exposed

Bugs that had shipped, found only because merging forced everything to be
rebuilt and re-run:

- **`bootstrap_sdlc.py` could never have worked from an installed plugin.**
  It resolved `provider.json` via `parents[3]`, but the lifecycle plugins are
  packaged from subdirectories that never contained that file. Every plugin
  user hit "missing provider manifest", at any path.
- **43 of 71 committed `cline-agents` files** were ported from an older
  revision, and five shared-policy paths had no substitution rule — including
  `workspace-isolation.md`, embedded verbatim into every wrapper. Nothing
  re-ported them because the distribution lived in another repository.
- **Declaring `"hooks": "./hooks/hooks.json"` prevents the hook loading**
  (`Duplicate hooks file detected`); the standard path loads automatically.
  This silently disabled the `cadre-lifecycle` v0.11.0 migration notice — the
  entire point of that release — and its own test asserted the condition
  backwards.
- **Four skills had unparseable YAML frontmatter** (a `description`
  containing `": "` ends a plain scalar), so they loaded with no name and no
  description, effectively undiscoverable.
- **`uv sync --locked` was already failing** in `agentic-sdlc` before the
  migration, so its CI had been red on lockfile drift independently.

## Install UX, before and after

| Audience | Was | Is |
| --- | --- | --- |
| Claude Code user | add marketplace at a tag stale in 3 documents, then install | two slash commands, no tag |
| Any runner | 18–20 actions across 3–4 repos | one `curl … \| sh` |
| Enterprise | undocumented | one managed-settings file |
| Repo adopter | clone, symlink, build a wheel, clone the kernel | `cadre` plus one guided command |

The kernel install specifically: from *"clone a repo, have pipx, run a
relative-path script that cannot work from your install location, maybe
restart your shell, re-run"* to one consented `/cadre-install-kernel`.

**Consent was preserved deliberately.** The `SessionStart` hook only detects
and reports; it never installs. A plugin fetching and executing code from the
network before the human has asked for anything is a supply-chain objection,
not a convenience.

## Releasing, and what a release now carries

Three version lines, deliberately independent — `provider.json`'s
`kernel_compatibility` window is only meaningful if the kernel can move
separately from the role catalog:

| Component | Version source | Tag |
| --- | --- | --- |
| Plugin distribution | `plugin/**/plugin.json` (8 manifests) | `plugin-v*` |
| Lifecycle kernel | `kernel/agentic_sdlc/__init__.py` | `kernel-v*` |
| LangGraph engine | `engine/pyproject.toml` — pinned `0.0.0` | never released; checkout-only |

`release.yml` did not work at all after the merge — it watched pre-merge
paths and called scripts at their old locations, so the repository could not
cut a release. Fixing that surfaced the tag-namespace collision recorded
above.

| Release | Carries |
| --- | --- |
| `kernel-v*` | wheel, sdist, `SHA256SUMS`, SPDX SBOM, SLSA provenance |
| `plugin-v*` | SPDX SBOM of the Cline npm trees, SLSA provenance over it |

`bootstrap_sdlc.py` installs the kernel from the published wheel and
verifies its checksum before installing, falling back to a git ref only when
a release has no assets. A checksum *mismatch* aborts rather than falling
back — "this route is unavailable" and "what was served is not what was
published" are different failures.

### An SBOM has to describe the resolved tree

The first kernel SBOM listed **2 packages**. Scanning a source tree reads
`pyproject.toml`, so it found the one declared dependency and stopped. A
real install pulls **19**. The SBOM is now taken from an environment where
the wheel is actually installed — create a venv, install, scan
site-packages — which is what `bootstrap_sdlc.py` does on a user's machine,
so it describes the environment a consumer really gets.

The plugin's own content is Markdown and stdlib Python with no install step;
its entire third-party surface is the three Cline workspaces' npm trees, 287
packages. That SBOM is generated from the committed `package-lock.json`,
which is already the resolved tree — so it needs no `npm ci` and a release
cannot fail on a registry outage. Scanning `node_modules` directly returns
4 packages; the javascript cataloger wants the lockfile.

Both are guarded: the steps assert a minimum package count and the presence
of known transitives, so a silent revert to declaration-scanning fails the
release instead of publishing a misleading inventory.

### Tag signing took three attempts, and the failures are the useful part

**Keyless (gitsign) does not work here.** It produced a valid-looking
signature on the tag object but created no Rekor entry, and a keyless
certificate is ephemeral — with nothing in the transparency log there is
nothing to verify against. It failed at signing time and still failed hours
later with the same gitsign version. In the same workflow run the artifact
attestation logged its Rekor upload normally; the tag signing logged none.
`plugin-v0.12.2` still carries that unverifiable signature.

That shipped because the in-workflow verification was non-fatal and its
output went unread. A signature nobody can verify is worse than none — it
asserts an assurance that does not exist. The check is now fatal.

**GitHub matches a signing key to the signer's account by email.**
`plugin-v0.12.4` was signed with a correctly registered SSH key and verified
locally with "Good git signature", yet GitHub reported `unknown_key`,
because the tagger was `github-actions[bot]@users.noreply.github.com` — not
an address on the account holding the key. Tags are now made as
`deagy <48447733+deagy@users.noreply.github.com>`.

Note the in-workflow check *could not* have caught that second one: it
verifies the signature cryptographically, which passed. Account association
is a separate, server-side check that only exists after the push. Verifying
through the API afterwards is what found it.

So artifacts are signed keylessly and tags with a stored key. That
inconsistency is deliberate, and `SECURITY.md` explains it rather than
hiding it. Setup for the key is in `.github/TAG-SIGNING-SETUP.md`.

## Still open

- `install.ps1` is **untested** — no PowerShell was available. Treat the
  first real Windows run as the test.
- The LangGraph engine is checkout-only **by construction**, not by
  omission: `runtime.py` reads the kernel's contracts at a path relative to
  the checkout, so an installed copy resolves to a directory that does not
  exist and fails at graph-build time. `release.yml` covering only the
  plugin and the kernel is therefore correct.

  Its version is pinned at `0.0.0` with a `Private :: Do Not Upload`
  classifier so the file stops implying a release line — it read `0.3.0`
  while the lockfile said `0.1.0`, since nothing consumed either number.
  Making it installable is a real change (bundle the contracts as package
  data, as `kernel/pyproject.toml` already does via `force-include`), not a
  version bump.
- The plugin *distribution itself* still carries no provenance
  attestation, deliberately. A marketplace installs it by cloning a git
  commit, so there is no downloaded file to verify and integrity comes from
  git's content addressing; signing a tarball nobody installs from would
  prove something about a file no user touches. Its SBOM is attested,
  because that one is genuinely downloadable.
Deliberately not closed:

- **`required_approving_review_count` stays `0`.** This repository's roles
  enforce that no agent approves its own work, and that stands. But the rule
  exists to keep a second *person* in the loop, and there is only one
  maintainer here — a required-review setting with nobody to satisfy it
  blocks releases without adding a reviewer. Revisit if a second maintainer
  joins. The branch ruleset still requires a pull request, so changes remain
  reviewable after the fact rather than pushed straight to `main`.

## Related

- [Installing Cadre](../../README.md#installation)
- [Enterprise deployment](../../README.md#enterprise-deployment)
- [`plugin/README.md`](https://github.com/deagy/cadre/blob/main/plugin/README.md)
  for what is generated versus hand-authored
