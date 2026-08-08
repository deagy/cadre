<!-- GENERATED FILE: edit the canonical source and regenerate; do not edit this copy. -->

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
python3 -m unittest discover -s roster/knowledge-store/test -p "test_*.py"
```

The orchestration tests exercise the lifecycle contract against the in-tree
kernel, so no install or env var is needed:

```sh
python3 -m unittest discover -s roster/orchestration/test -p "test_*.py"
```

See the [lifecycle guide](lifecycle-and-plugin-operations.md) if you need to
point the tests at a separately installed kernel instead.

## Choosing the next guide

- Need roles for a task? Read [Orchestration](orchestration.md).
- Need a target-project overlay or gate record? Read [Lifecycle and plugin operations](lifecycle-and-plugin-operations.md).
- Need a role's purpose and handoff? Read the [role index](role-index.md).
- Need a complete worked example? Read the [runbook](../roster/RUNBOOK.md),
  starting with its section index.
