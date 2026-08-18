# Python -> Go: complete

**Status: there is no Python left in this repository.** `git ls-files '*.py'`
returns nothing, no workflow runs an inline Python block, and nothing in the
build, the tests, the hooks or the installers needs an interpreter.

**Last verified:** 2026-08-18, by measuring the tree after deleting `engine/`.
Re-measure rather than trusting this line; it is a claim about a moment.

```sh
git ls-files '*.py' | wc -l                              # expect 0
grep -rn "python3 <<\|python3 -c" .github/workflows/     # expect nothing
```

Two uses of a Python *interpreter* survive deliberately and are not Python
source: `.github/workflows/` builds and inspects the pip wheel with
`python -m venv` and `python -m zipfile`. That is Python tooling operating on
a Python distribution artifact, which is the one place it is the right tool.

---

## What the last stage was

`engine/` — the LangGraph orchestration runtime, 39 files and ~13,700 lines
including tests — was the final module. Unlike everything before it, this was
not a translation: LangGraph has no Go equivalent, so the compiled `StateGraph`
was replaced by a derived executor rather than reimplemented.

The reasoning, kept here because the shape of the replacement is not obvious
from the result: the topology `graph.py` compiled was derived entirely from the
gate sequence — per gate, authors fan out, then reviewers, then a decision,
then an optional human stop, with inter-gate edges from the contract's
prerequisites — and it had no cycles, because re-entry rewinds a checkpoint
rather than following an edge back. So the gate list *is* the shape, and a
driver over it is the whole engine. `internal/engine/executor` is that driver.

Suspension became a return value rather than a coroutine. LangGraph's
`interrupt()` unwinds the stack and lets a checkpointer persist what was
reached; the Go executor returns `Suspended` with what is being waited on, and
`Resume` continues from the checkpoint. Nothing blocks a goroutine while a
human thinks, which is what makes it usable from a service that may not be
running when the answer arrives.

## Where it went

| Python | Go |
| --- | --- |
| `contracts.py` | `internal/engine/contracts` |
| `state.py` | `internal/engine/state` |
| `validate.py` | `internal/engine/validate` |
| `export.py` | `internal/engine/export` |
| `provider.py` | `internal/engine/provider` |
| `planning.py` | `internal/engine/planning` |
| `gitlab_issue.py` | `internal/engine/gitlabissue` |
| `github_approval.py` | `internal/engine/githubapproval` |
| `a2a/*.py` | `internal/engine/a2a` |
| `agents.py` | `internal/engine/agents` |
| `graph.py`, `reentry.py` | `internal/engine/executor` |
| `runtime.py` | `internal/engine/runtime` |
| `service.py` | `internal/engine/service` |
| `cli.py` | `internal/engine/enginecli`, `cmd/agentic-sdlc-engine` |
| `requirement_issues.py` | `internal/engine/requirementissues` |

## What was found on the way

Porting is where a codebase gets read properly, and reading it found defects
the Python had been carrying. These are recorded because each one is a class,
not an incident:

- **A human-only mutation gate that could never fire.** `mutation_gate_guard`
  lowercased the task text but compared each phrase verbatim, so any phrase
  with a capital letter would never match. Latent only because every shipped
  phrase is lowercase.
- **The engine could not load this repository's own provider.** `KERNEL_VERSION`
  was a hand-kept mirror that had drifted two releases behind, below the
  `secure-cloud` provider's declared minimum — and the error blamed the
  provider. Live, not latent.
- **`human_only` was silently dropped** from the gate contract, which would
  have turned a legitimate G9 approval into a hard validation error.
- **"Latest review wins" ordered timestamps as strings**, so with mixed UTC
  offsets a stale `APPROVED` could beat the `CHANGES_REQUESTED` that superseded
  it — in the code that decides whether an approval is independent.

Each of those was found by checking against real shipped data or the real
previous implementation. None was found by reading code.

## What the tests are anchored to

The Python is gone, so "what did it do here" is only answerable through what
was pinned before it went:

- `internal/engine/a2a/testdata/pydantic_wire.json` — the exact bytes pydantic
  produced at each call site, which differ per site.
- `internal/engine/validate/testdata/python_verdicts.json` — nine run records
  with the exit code and messages `validate_run_record` produced.
- `internal/engine/export/testdata/python_records.json` — seven exported records.
- `internal/engine/planning/testdata/python_sequences.json` — six derived
  sequences and one error message.

Beyond those, behaviour is asserted against the shipped contracts themselves
(`kernel/contracts/`, `providers/agentic-sdlc-defaults/`) rather than against
fixtures, so a contract change fails a test rather than drifting past one.

## Where the running instructions live

This file is a record, not a manual. `README.md` documents how to run the
engine; `PYTHON_ELIMINATION_PLAN.md` holds the phase-by-phase history of how
the migration ran.
