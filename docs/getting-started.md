# Getting started

This guide is for someone using the suite from a checkout of this repository.
To *install* it instead, see [Installing Cadre](INSTALL.md) — that is the
canonical install guide for every runner.
For a target project's lifecycle setup, use the [lifecycle and plugin
operations guide](lifecycle-and-plugin-operations.md).

## Prerequisites

- Go (`go.mod` pins `go 1.26.5`).
- A checkout of this repository.

The lifecycle kernel is **not in this repository** — it lives at
[deagy/cadre-kernel](https://github.com/deagy/cadre-kernel) and installs
separately. `cadre sdlc` finds it on `PATH`, through `AGENTIC_SDLC_BIN`, or
from the shim the lifecycle plugin packages; `./install.sh --with-lifecycle`
sets this up. Set `AGENTIC_SDLC_BIN` to pin a *particular* kernel
deliberately; see the [lifecycle guide](lifecycle-and-plugin-operations.md)
for that case.

The `bin/cadre` launcher builds and execs the Go CLI under `cmd/cadre`;
PowerShell users can use `bin/cadre.ps1`.

## Five-minute path

From the repository root:

```sh
./bin/cadre select \
  --task "Review a React and Go upload feature" \
  --files frontend/src/App.tsx,services/internal/api/api.go \
  --classification internal \
  --task-id EXAMPLE-1
```

The selector produces a reviewable dispatch plan. It does not execute agents,
retrieve knowledge, approve gates, deploy, mutate infrastructure, merge, or
push changes.

Run the suite-only check with:

```sh
make test
```

which runs `go test -tags sqlite_fts5 ./...` — the CLI, kernel-facing stores,
and generators, needing no external services. Validate the role catalog and
generated output directly with:

```sh
./bin/cadre schema-validate
./bin/cadre generate-role-metadata --check
```

If you're also touching the Cline plugins, each has its own Vitest suite:

```sh
cd cline-plugins/cline-agents && npm test
```

(and likewise for `cline-plugins/cline` and `cline-plugins/cline-lifecycle`).

The kernel's own tests live in its own repository now (deleted here): they import the
package in-process and need neither `AGENTIC_SDLC_BIN` nor anything on `PATH`. See
the [lifecycle guide](lifecycle-and-plugin-operations.md) to point at a
separately installed kernel instead.

## Choosing the next guide

- Need roles for a task? Read [Orchestration](orchestration.md).
- Need a target-project overlay or gate record? Read [Lifecycle and plugin operations](lifecycle-and-plugin-operations.md).
- Need a role's purpose and handoff? Read the [role index](role-index.md).
- Need a complete worked example? Read the [runbook](../roster/RUNBOOK.md),
  starting with its section index.
