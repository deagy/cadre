# Installing Cadre

Pick the row that describes you. Everything else on this page is detail.

| You are | Do this |
| --- | --- |
| A Claude Code user | `/plugin marketplace add deagy/cadre` then `/plugin install cadre@cadre-team` |
| On any runner, or several | `curl -fsSL https://raw.githubusercontent.com/deagy/cadre/main/install.sh \| sh` |
| Rolling out to a fleet | [Enterprise deployment](enterprise.md) — one managed-settings file |
| Working on Cadre itself | `git clone https://github.com/deagy/cadre.git` and run `bin/cadre` from the checkout |

Lifecycle governance (G1–G10 gates) is **optional** and most projects do not
need it. Nothing above installs it. See [Adding lifecycle
governance](#adding-lifecycle-governance) if you want it.

---

## Claude Code

```text
/plugin marketplace add deagy/cadre
/plugin install cadre@cadre-team
```

Deliberately unpinned. The version you get comes from the plugin's own
manifest, and `release.yml` only tags `main` from a state where every plugin
manifest agrees — so the marketplace ref does not need a tag, and writing one
down only guarantees a stale document. Use `/plugin update` to move forward.

If your policy requires a pinned source, append `@<tag>` from
[the releases](https://github.com/deagy/cadre/releases) and own keeping it
current. Note that a *marketplace* source accepts a branch or tag but not a
commit SHA, so the pin is only as immutable as the tag.

`owner/repo` shorthand clones over SSH by default; set
`CLAUDE_CODE_PLUGIN_PREFER_HTTPS=1` for HTTPS.

## Any runner (install script)

```sh
curl -fsSL https://raw.githubusercontent.com/deagy/cadre/main/install.sh | sh
```

Installs for whichever of `claude`, `codex`, and `cline` it finds on `PATH`.
On Windows use [`install.ps1`](https://github.com/deagy/cadre/blob/main/install.ps1):

```powershell
irm https://raw.githubusercontent.com/deagy/cadre/main/install.ps1 | iex
```

If you save it and run the file instead, PowerShell blocks unsigned scripts
by default — use `pwsh -ExecutionPolicy Bypass -File install.ps1`. The
piped form above is unaffected.

```sh
./install.sh --dry-run           # print every action, change nothing
./install.sh --runner=codex      # just one runner
./install.sh --with-lifecycle    # include G1-G10 governance and its kernel
./install.sh --uninstall         # reverse everything (honours --runner)
```

It needs `git` and Python 3.10+, checked up front rather than halfway
through. What it touches, and nothing else:

| Path | Why |
| --- | --- |
| `~/.cadre/dist` | a shallow checkout, used by Cline and for the `cadre` CLI |
| `~/.local/bin/cadre` | symlink to that checkout's launcher |
| `~/.codex/config.toml` | an MCP entry, in a fenced block, backed up first |
| each runner's plugin store | via that runner's own CLI |

Re-running is safe: it updates in place rather than duplicating.

## Codex CLI

The install script covers this, or do it directly:

```sh
codex plugin marketplace add deagy/cadre
codex plugin marketplace upgrade cadre-team   # `add` alone does not refresh an existing one
codex plugin add cadre@cadre-team
cadre bootstrap-codex                          # Codex finds agents only under ~/.codex/agents/
```

For mid-session dispatch, add the MCP server to `~/.codex/config.toml`:

```toml
[mcp_servers.cadre-dispatch]
command = "cadre"
args = ["mcp-dispatch-server"]
```

This needs `cadre` on your shell `PATH` (the install script's symlink) and
the MCP extra: `pip install -r roster/orchestration/mcp/requirements-mcp.txt`.

## Cline

```sh
cline plugin install https://github.com/deagy/cadre --force
```

This installs the `cline` role-selection tool, `cline-agents` subagent
presets, and `cline-lifecycle` governance tools together. The Git-source
manifest pins the plugin-owned runtime dependencies required while Cline
syncs all three entrypoints. For local development, the equivalent command is
`cline plugin install ./cadre --force` from a checkout; run `npm ci` in
`cline-plugins/` before running that workspace's tests or typechecks.

**Known upstream defect:** as of cline CLI 3.0.46, invoking *any*
locally-installed plugin's tool fails with `JSON.stringify cannot serialize
cyclic structures`. This affects Cline's own example plugin too. Install and
uninstall work; tool invocation is expected to start working when Cline ships
a fix.

## From a checkout

For working on Cadre itself, or running it without installing anything:

```sh
git clone https://github.com/deagy/cadre.git && cd cadre
./bin/cadre select --task "..." --files a.go --task-id T-1
```

`bin/cadre` finds a Python 3.10+ interpreter itself (`bin/cadre.ps1` on
PowerShell). To get it on your shell `PATH`, symlink it — do not copy it, as
it resolves its own location:

```sh
ln -s "$PWD/bin/cadre" ~/.local/bin/cadre
```

The lifecycle kernel is in this repository, so `cadre sdlc` works from a
checkout with no install and no `AGENTIC_SDLC_BIN`.

---

## Adding lifecycle governance

Three optional plugins, each self-sufficient — install whichever matches how
your team records approvals. Each declares a dependency on `cadre`, so it
comes along automatically.

```text
/plugin install cadre-lifecycle-core@cadre-team      # forge-agnostic
/plugin install cadre-lifecycle-github@cadre-team    # PR-review-sourced decisions
/plugin install cadre-lifecycle-gitlab@cadre-team    # MR-approval-sourced decisions
```

They need the Agentic SDLC kernel. The plugin will tell you once, at session
start, and installing it is one explicit step:

```text
/cadre-install-kernel
```

That installs a copy under the plugin's own data directory. It never
modifies, replaces, or uninstalls a kernel you installed yourself — if
`AGENTIC_SDLC_BIN` is set, that binary is used as-is, and an incompatible one
on `PATH` is left alone rather than touched. Deleting the plugin's data
directory undoes the install completely.

The `kernelInstall` option controls this: `auto` (default) manages its own
copy, `system` never installs anything, `off` disables the check. See
[Enterprise deployment](enterprise.md#kernelinstall).

Then set up a project:

```text
/lifecycle-onboarding
```

Use the skill rather than running `agentic-sdlc init` directly — assigning
the eight human authorities is a decision a person has to make, and the skill
walks through it.

## A warning about PyPI

**Neither package is on PyPI, and both names belong to other people.**

`pip install agentic-sdlc` installs an unrelated third-party project. It will
install cleanly and look plausible. The name `cadre` on PyPI is likewise a
placeholder uploaded by someone else in 2022.

Install only from this repository — a checkout, the marketplace, the install
script, or a wheel attached to a `kernel-v*` release (verify it against the
release's `SHA256SUMS`). See [SECURITY.md](https://github.com/deagy/cadre/blob/main/SECURITY.md).

## Verifying

```sh
cadre select --task "smoke test" --files README.md --task-id SMOKE-1
cadre sdlc --version        # only if you installed lifecycle governance
```

In Claude Code, `/plugin details cadre@cadre-team` shows exactly what the
plugin contributes and what it costs in context.
