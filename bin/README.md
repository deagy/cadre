
# bin/ — Cadre CLI Dispatch

This directory contains the executable dispatch entry point for the Cadre agent suite.

## Entry points

| File | Purpose |
|------|---------|
| `cadre` | POSIX shell shim — the primary CLI dispatch mechanism |
| `cadre.ps1` | PowerShell shim — Windows equivalent |
| `cadre.py` | Python dispatch table — routes to subcommand handlers |
| `subcommands.tsv` | Tab-separated subcommand registry — defines available commands |

## Dispatch mechanism

`cadre` resolves the Python interpreter and routes every subcommand through `bin/cadre.py`. The dispatch table in `subcommands.tsv` maps subcommand names to their handler modules.

### Running subcommands

```sh
./bin/cadre help                      # List all available subcommands
./bin/cadre select --task "..."       # Deterministic role selection (read-only)
./bin/cadre knowledge --source ...    # Knowledge store ingestion/retrieval
./bin/cadre sdlc <subcommand>         # Agentic SDLC lifecycle governance
./bin/cadre generate-role-metadata    # Regenerate derived role metadata
./bin/cadre init <project-root>       # From this checkout, initialize another project's shared-policy overlays
```

### Subcommand categories

- **Selection**: `select`, `selection-telemetry` — route tasks to specialist roles
- **Knowledge**: `knowledge` — ingest and retrieve from the knowledge store
- **Lifecycle**: `sdlc` — G1-G10 gate management, plan, validate, approve
- **Generation**: `generate-plugin`, `generate-role-metadata`, `generate-authority-aides` — build derived artifacts
- **Bootstrap**: `bootstrap-codex`, `init`, `resolve-shared` — project setup
- **Server**: `mcp-dispatch-server` — MCP protocol dispatch server (runners: `codex`, `claude-code`, `api`)

### Constraints

- Requires Python 3.10+ (resolved via `python3`/`python`/`py -3`)
- `generate-plugin` and `generate-authority-aides` only run from a checkout directory, never from an installed distribution
- All subcommands that accept `--task` are read-only for `select`

## See also

- [README.md](../README.md) — full repository documentation
- [docs/](../docs/) — adoption guides, capability index, and getting started
- [roster/](../roster/) — role definitions and orchestration source
