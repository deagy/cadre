# Enterprise deployment

Rolling Cadre out to a fleet, with no per-user install steps.

Everything here is Claude Code specific. For Codex or Cline, put
[`install.sh`](https://github.com/deagy/cadre/blob/main/install.sh) in your provisioning script — it is
non-interactive, idempotent, and takes `--runner=`.

## The short version

Deploy one managed-settings file. Users get the marketplace and the plugin
without running anything.

```json
{
  "extraKnownMarketplaces": {
    "cadre-team": {
      "source": { "source": "github", "repo": "deagy/cadre" },
      "autoUpdate": true
    }
  },
  "enabledPlugins": {
    "cadre@cadre-team": true
  }
}
```

| Platform | Path |
| --- | --- |
| macOS | `/Library/Application Support/ClaudeCode/managed-settings.json` |
| Linux / WSL | `/etc/claude-code/managed-settings.json` |
| Windows | `C:\Program Files\ClaudeCode\managed-settings.json` |

Each platform also supports a `managed-settings.d/` drop-in directory next to
that file, which is usually easier to manage from configuration management
than editing one shared JSON document.

> The Windows path changed. `C:\ProgramData\ClaudeCode\managed-settings.json`
> is **no longer supported** as of Claude Code v2.1.75.

Managed settings sit at the top of the precedence order and cannot be
overridden by a user's own settings.

## Read this before you assume it is zero-touch

**`enabledPlugins` does not install anything.** It declares that a plugin
should be *on*; the plugin still has to be fetched. When a user trusts the
folder, Claude Code prompts them to install the declared marketplaces and
plugins. Until they accept, Claude Code reports the plugin as not installed
and prints the `claude plugin install` command.

If you want genuinely zero-touch, pair the managed settings with one line in
your existing provisioning script:

```sh
claude plugin install cadre@cadre-team --scope user
```

That is exactly what `install.sh --runner=claude` does, so either works.

**`autoUpdate` is a managed-settings feature.** Third-party marketplaces have
auto-update *off* by default, and each user would otherwise have to toggle it
themselves. Setting it here turns it on for the fleet.

**Removing a marketplace uninstalls its plugins.** Do not drop the
`extraKnownMarketplaces` entry as a way of pausing a rollout.

## Configuring the lifecycle plugins

Lifecycle governance (G1–G10 gates) is optional and most projects do not need
it. If you are deploying it, its `userConfig` options can be pre-set:

```json
{
  "enabledPlugins": {
    "cadre@cadre-team": true,
    "cadre-lifecycle-github@cadre-team": true
  },
  "pluginConfigs": {
    "cadre-lifecycle-github@cadre-team": {
      "options": {
        "kernelInstall": "auto",
        "profile": "secure-cloud"
      }
    }
  }
}
```

`cadre-lifecycle-github` declares a dependency on `cadre`, so installing it
pulls `cadre` in automatically — you do not have to list both, though listing
both is harmless and clearer.

### `kernelInstall`

The lifecycle plugins need the Agentic SDLC kernel. This option decides how
they are allowed to get it:

| Value | Behaviour |
| --- | --- |
| `auto` (default) | The plugin installs and manages its own copy under its private data directory. Nothing else on the machine is touched. |
| `system` | Use only a kernel you installed. The plugin never installs anything; it reports if none is present. |
| `off` | No kernel checking at all. Silent. |

**This changed.** The plugin used to install nothing on its own initiative:
its `SessionStart` hook detected and reported, and a human ran
`/cadre-install-kernel` to install the kernel. That installer created a venv
and pip-installed a Python kernel this repository has since deleted, so it has
been removed.

The lifecycle `bin/agentic-sdlc` shim now fetches the kernel itself, on first
use, from the checksum-verified `kernel-v` release it was generated against --
the same thing `bin/cadre` already does for the CLI binary. Nothing is fetched
if a kernel already resolves.

`AGENTIC_SDLC_BIN` is unchanged and is still checked **first**, before any
cache or download, so a policy that forbids a plugin fetching code from the
network is still satisfied by setting it:

```json
{
  "env": { "AGENTIC_SDLC_BIN": "/opt/agentic-sdlc/bin/agentic-sdlc" },
  "pluginConfigs": {
    "cadre-lifecycle-github@cadre-team": { "options": { "kernelInstall": "off" } }
  }
}
```

### Air-gapped fleets

Pre-seed the managed kernel directory from an internal mirror and the plugin
will find it and never reach the network:

```
~/.claude/plugins/data/<plugin-id>/kernel/
```

Or install the kernel wherever you like and point `AGENTIC_SDLC_BIN` at it
with `kernelInstall: "system"`. Release artifacts (wheel, sdist, and
`SHA256SUMS`) are attached to each `kernel-v*` release for mirroring.

## Restricting what users can add

`strictKnownMarketplaces` (managed settings only) blocks users from adding
marketplaces you have not listed:

```json
{
  "strictKnownMarketplaces": true,
  "extraKnownMarketplaces": {
    "cadre-team": { "source": { "source": "github", "repo": "deagy/cadre" } }
  }
}
```

## Two constraints worth knowing up front

**`pluginConfigs` is ignored in project settings.** Claude Code reads it only
from user settings, `--settings`, and managed settings — deliberately, since
a cloned repository could otherwise supply values that flow into hook
commands and MCP server configs. Precedence is managed > `--settings` > user.
So a project cannot configure these options for its contributors; only the
user or you can. (`enabledPlugins` is different — it *does* honour project
and local settings.)

**Organization sync supports only some source types.** If you distribute
settings through organization sync rather than a file on disk, `github`,
`url`, and `git-subdir` sources work; `npm` and `archive` do not. The recipe
above uses `github`.

## Verifying a rollout

```sh
claude plugin list                     # cadre@cadre-team present and enabled
claude plugin details cadre@cadre-team # component inventory and context cost
cadre select --task "smoke" --files README.md --task-id ROLLOUT-1
```

A plugin installed by managed settings shows **managed** scope and cannot be
modified by the user.
