<!-- GENERATED FILE: edit the canonical source and regenerate; do not edit this copy. -->

# Getting started with a checkout

This guide covers working on Cadre itself from a repository checkout. If you want to *install* Cadre for use, see the [Installation section in README.md](../README.md#installation) instead. For setting up lifecycle gates in a target project, see [Lifecycle and plugin operations](lifecycle-and-plugin-operations.md).

## Prerequisites

- Python 3.10 or newer
- This repository checked out

The in-tree kernel means `cadre sdlc` works immediately with no separate install.

## Quick start

From the repository root:

```sh
./bin/cadre select \
  --task "Review a React and Go upload feature" \
  --files frontend/src/App.tsx,services/internal/api/api.go \
  --classification internal \
  --task-id EXAMPLE-1
```

On PowerShell, use `.\bin\cadre.ps1`.

The selector produces a reviewable dispatch plan only — no agent execution, knowledge retrieval, gate approval, deployment, or infrastructure mutation.

## Testing

Knowledge store tests:

```sh
python3 -m unittest discover -b -s roster/knowledge-store/test -p "test_*.py"
```

Orchestration tests (standalone mode, no lifecycle kernel on PATH):

```sh
python3 -m unittest discover -b -s roster/orchestration/test -p "test_*.py"
```

Orchestration tests with integrated lifecycle contracts (as CI runs them):

```sh
AGENTIC_SDLC_BIN="$PWD/bin/agentic-sdlc" \
  python3 -m unittest discover -b -s roster/orchestration/test -p "test_*.py"
```

Kernel tests (in-process, no PATH dependency):

```sh
python3 -B -m unittest discover -b -s kernel/test -p "test_*.py"
```

## Next steps

- **Roles for a task?** Read [Orchestration](orchestration.md).
- **Target-project setup?** Read [Lifecycle and plugin operations](lifecycle-and-plugin-operations.md).
- **Role purpose and handoff?** Read the [role index](role-index.md).
- **Worked example?** Read [roster/RUNBOOK.md](../roster/RUNBOOK.md).
