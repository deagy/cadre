# Getting started

This guide is for someone using the suite from a checkout of this repository.
To *install* it instead, see [Installing Cadre](INSTALL.md) — that is the
canonical install guide for every runner.
For a target project's lifecycle setup, use the [lifecycle and plugin
operations guide](lifecycle-and-plugin-operations.md).

## Prerequisites

- Python 3.10 or newer.
- A checkout of this repository.

The lifecycle kernel is in-tree, under
[`kernel/`](https://github.com/deagy/cadre/tree/main/kernel) — `cadre sdlc`
and the lifecycle-contract tests need no separate install and no
`AGENTIC_SDLC_BIN`. Set that env var only to point at a *different* kernel
deliberately; see the [lifecycle guide](lifecycle-and-plugin-operations.md)
for that case.

The `bin/cadre` launcher probes for `python3` or `python`; PowerShell users
can use `bin/cadre.ps1`.

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
python3 -m unittest discover -b -s roster/knowledge-store/test -p "test_*.py"
```

The orchestration tests need no install and run as-is:

```sh
python3 -m unittest discover -b -s roster/orchestration/test -p "test_*.py"
```

Run that way they exercise the selector in **standalone** mode, because the
lifecycle contract is resolved by looking for an `agentic-sdlc` executable on
`PATH` — being in-tree is not enough on its own. To exercise the
lifecycle-*integrated* assertions as CI does, point `AGENTIC_SDLC_BIN` at the
launcher this repository already ships; there is still nothing to install:

```sh
AGENTIC_SDLC_BIN="$PWD/bin/agentic-sdlc" \
  python3 -m unittest discover -b -s roster/orchestration/test -p "test_*.py"
```

The kernel's own tests (`kernel/test`) are different again: they import the
package in-process and need neither the variable nor anything on `PATH`. See
the [lifecycle guide](lifecycle-and-plugin-operations.md) to point at a
separately installed kernel instead.

## Choosing the next guide

- Need roles for a task? Read [Orchestration](orchestration.md).
- Need a target-project overlay or gate record? Read [Lifecycle and plugin operations](lifecycle-and-plugin-operations.md).
- Need a role's purpose and handoff? Read the [role index](role-index.md).
- Need a complete worked example? Read the [runbook](../roster/RUNBOOK.md),
  starting with its section index.
