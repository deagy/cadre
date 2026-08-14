# Cadre CLI Distribution Strategy

## Current status (as of 2026-08-14)

**Implementation wired and active.** The packaged plugin distribution
(`plugin/bin/cadre`, installed via the `cadre-team` marketplace) now
download-on-first-use a platform-matched compiled Go binary from GitHub
Releases, verify it, cache it locally, and execute it for all subcommands
except `select` (which remains Python). The Python suite under
`plugin/suite/roster/` is unchanged and remains the mandatory fallback for
any failure (no network, download failure, checksum mismatch, unsupported
platform, or offline use).

**Path resolution blocker was NOT solved.** The compiled `cmd/cadre` binary's
path resolution still cannot work from inside a packaged plugin environment
that contains no `.git` boundary at the repository root. This remains true,
but is irrelevant: the Go binary is used only for fast-path implementations
of leaf commands and does not need to locate `roster/shared/` or other
repository content. Subcommands requiring path resolution (e.g. `select` via
its delegation to Python `select_agents.py`, or `knowledge`, or `config`)
that might in a future checkpoint migrate to Go are still blocked and would
require either a plugin-specific path-resolution mode or a structural
rearrangement of the packaged plugin tree before they could migrate. But
that future work is orthogonal to what ships now.

## Chosen binary-delivery mechanism: download-on-first-use

**Decision: `plugin/bin/cadre` downloads the platform-matched release
archive from GitHub Releases on first use of a subcommand the compiled
binary can serve, verifies it before executing, caches it, and falls back to
today's Python path unconditionally on any failure (no network, download
failure, checksum mismatch, or unsupported platform).** The Python suite
under `plugin/suite/roster/` remains vendored and is not removed by this
change -- it is both the required fallback and, per the blocker above and
the byte-exact-plan requirement, the permanent implementation for `select`.

### Rejected alternative: vendor per-platform binaries in the plugin package

The current `cmd/cadre` binary is ~9.5 MB. Five supported platforms
(`linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`,
`windows/amd64` -- see "Platform support" below for why the matrix is five,
not six) is ~48 MB committed into a repository a marketplace install `git
clone`s or pulls in full, on every release, forever -- `plugin/`'s generated
content is already committed by design (see `plugin/CLAUDE.md`) precisely
because the marketplace serves the repository tree rather than a downloaded
artifact; vendoring binaries multiplies that cost by five platforms a given
install will never use four of. Rejected.

### Rejected alternative: platform-specific plugin package per release

Splitting `cadre` into per-platform marketplace entries avoids the git-size
problem but has no matching install-time mechanism: Claude Code's and
Codex's marketplace manifests (`.claude-plugin/marketplace.json`,
`.agents/plugins/marketplace.json`) have no platform-selection concept, so
this would require either six separately-named plugins (a user manually
picks the right one, and picks wrong on every OS upgrade or new machine) or
external tooling to rewrite the manifest per install target. It also
sextuples every other piece of plugin logic (hooks, skills, manifests) for a
difference that is purely which binary a shared shim resolves. Rejected.

### Chosen: download-on-first-use, verified

**Trade-offs accepted:**
- Introduces a network dependency on first invocation of an affected
  subcommand. Mitigated by the mandatory Python fallback: a fully offline or
  air-gapped install is never broken by this, only slower (Python startup
  instead of a cached Go binary) until/unless it can reach the release host.
- Introduces a local cache location and its own staleness/eviction
  questions (out of scope for this document to resolve; would need an
  explicit TTL or version-pin policy before implementation).
- Requires verification before executing anything downloaded, not
  optionally. Downloads are verified against the release's `SHA256SUMS`
  file (fail-closed: refuse and fall back on mismatch). Cached binaries are
  re-verified before execution using a sidecar hash file (computed from the
  extracted binary at cache time, checked locally without network on every
  warm-path invocation). This detects corruption, partial writes, and
  unprivileged tampering. Opportunistic `gh attestation verify` against the
  SLSA build-provenance attestation (per `team-profile.yaml`'s
  `cicd.artifact_signing`) is attempted if the `gh` CLI is present, but
  checksum verification remains mandatory and non-optional even when `gh`
  is unavailable.
  
  **Honest scope of cache-local controls:** A process with the invoking
  user's UID can rewrite anything in the cache, including a sidecar hash
  file. Cache verification detects corruption (partial writes, bit flips,
  interrupted extraction) and tampering by unprivileged processes
  (different UID, or with permission escalation via group/world-writable
  binaries). Same-UID attackers are a privilege-escalation boundary that
  operating-system access controls enforce, not something a cache-local
  control can defend against. The cache directory is created with restrictive
  permissions (mode 700, owner-only access) and cached binaries are checked
  for owner/group/world-writable bits before execution; both are applied
  nonetheless as defense-in-depth even where they cannot be complete.

### Release asset naming contract

Agreed with the release-workflow agent working the same task in parallel:

```
cadre-v<version>-<goos>-<goarch>.tar.gz   (all platforms except Windows)
cadre-v<version>-<goos>-<goarch>.zip      (Windows)
```

Rendered against the five supported platforms (placeholder version
`<version>`):

- `cadre-v<version>-linux-amd64.tar.gz`
- `cadre-v<version>-linux-arm64.tar.gz`
- `cadre-v<version>-darwin-amd64.tar.gz`
- `cadre-v<version>-darwin-arm64.tar.gz`
- `cadre-v<version>-windows-amd64.zip`

Each archive contains exactly one executable, named `cadre` (`cadre.exe` on
Windows). `plugin/tools/binary_platforms.py` is the single source of truth
for the platform matrix and naming helpers; `plugin/tools/test_binary_shim_contract.py`
pins that matrix against `Makefile`'s `cross-build` recipe and this document,
so the three cannot silently drift apart. It does not yet assert anything
about `plugin/bin/cadre` itself, since that activation is the part still
blocked (see above).

### Platform support: five platforms, not six -- `windows/arm64` deliberately excluded

The original design in this document (and the first cut of `Makefile`'s
`cross-build`) included `windows/arm64`, matching `cmd/cadre`'s general
GOOS/GOARCH support. **This was corrected after pipeline-security review
found it release-blocking, not merely untested:** GitHub's hosted
`windows-latest` runner is x64; its `gcc` is x86_64 MinGW and cannot emit
ARM64 Windows objects. With `CGO_ENABLED=1` forced (required by the
knowledge store -- see below), a `windows/arm64` cross-build leg fails to
build outright on that runner. Because the release workflow's publish job
depends on every build leg succeeding, an attempted `windows/arm64` leg
would fail *every future CLI release*, publishing nothing at all -- not a
missing platform, a broken pipeline. This exact failure class was
reproduced locally in this task (`cc: error: unrecognized command-line
option '-m64'`) before the review caught the same root cause on the actual
target runner.

**The human decision, made:** drop `windows/arm64` from the contract
entirely rather than provision a dedicated ARM64 Windows runner. Five
platforms -- `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`,
`windows/amd64` -- is the contract now, recorded in
`plugin/tools/binary_platforms.py`. This is a decided exclusion, not a gap:
re-adding `windows/arm64` requires provisioning a real ARM64 Windows runner
(or an equivalent cross toolchain capable of emitting ARM64 Windows PE
objects with cgo) first, and that provisioning has not happened.

## CGO requirement -- verified, not theoretical

The knowledge store depends on `github.com/mattn/go-sqlite3`
(`roster/shared/library-standards.yaml`), which requires `CGO_ENABLED=1`.
Verified directly against this checkout: a `CGO_ENABLED=0` build of
`cmd/cadre` compiles and links without error (`go-sqlite3` ships a cgo-less
stub for exactly this case) but fails at runtime on any `cadre knowledge`
invocation with `Binary was compiled with 'CGO_ENABLED=0', go-sqlite3
requires cgo to work. This is a stub`. `make cross-build` previously set no
`CGO_ENABLED` at all (defaulting to whatever the host's Go toolchain
resolves -- `0` on this machine), so every binary it produced shipped a dead
knowledge store with no build-time signal. Fixed in this task: every
`cross-build` line now sets `CGO_ENABLED=1` explicitly, and
`plugin/tools/test_binary_shim_contract.py` pins that every platform line
does so.

**Consequence for cross-compilation:** `CGO_ENABLED=1` requires a matching C
cross-compiler per target `GOOS`/`GOARCH` (verified: this environment's
native `gcc` fails immediately on the first non-native `GOOS=linux
GOARCH=amd64` -- actually native here -- cross target with `cc: error:
unrecognized command-line option '-m64'`, i.e. even same-OS
cross-architecture needs a dedicated toolchain, not just the host compiler).
A native, same-platform build (`CGO_ENABLED=1 go build ./cmd/cadre` with no
`GOOS`/`GOARCH` override) was verified to build and run `cadre knowledge
--help` correctly in this checkout. Provisioning the per-platform C
toolchains needed for `make cross-build`'s five remaining legs to succeed
everywhere is release infrastructure, out of this document's scope, and
worth flagging to whoever owns `.github/workflows/release.yml` -- and is the
same class of gap that ruled out a sixth (`windows/arm64`) leg entirely
rather than merely leaving it unverified (see "Platform support" above).

**Also worth flagging, out of this task's file ownership:** the repository
root's own `bin/cadre` (which the top-level `CLAUDE.md` describes as
building and exec'ing `cmd/cadre` directly, not through a release archive)
inherits this same default. A contributor whose machine's Go toolchain
resolves `CGO_ENABLED=0` by default -- confirmed to be this environment's
default -- gets a knowledge-broken `bin/cadre` from an ordinary build with
no error at build time. This document does not fix that, since `bin/cadre`
and its build path are outside this task's ownership; recorded here as a
cross-cutting finding for whoever does own it.

## See also

- `Makefile` -- `cross-build` target (fixed platform matrix + `CGO_ENABLED=1`
  in this task).
- `plugin/bin/cadre` -- the packaged plugin's dispatcher (generated by
  `roster/orchestration/src/generate_global_plugin.py`; unchanged by this
  task pending the path-resolution blocker above).
- `plugin/tools/binary_platforms.py` -- the platform matrix and naming
  helpers (single source of truth, importable by CI without depending on a
  `test_*.py` module).
- `plugin/tools/test_binary_shim_contract.py` -- the asset-naming/platform-
  matrix guard added in this task, checked against `binary_platforms.py`.
- `ADR-001-CLI-GO-REFACTOR.md`, `CADRE_CLI_GO_ARCHITECTURE.md` -- the
  checkout-CLI migration this document's plugin-distribution work follows.
- `.github/workflows/release.yml` -- publishes the kernel wheel/sdist today;
  does not yet publish `cmd/cadre` release archives (owned by a parallel
  workstream on this same task).
