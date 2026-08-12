# Agentic SDLC — LangGraph engine

Drives a task through the repository's G1-G10 lifecycle (see
[`../kernel/contracts/lifecycle-gates.json`](../kernel/contracts/lifecycle-gates.json))
as a compiled [LangGraph](https://github.com/langchain-ai/langgraph)
`StateGraph`, built declaratively from that contract plus a provider's
profile/agent-catalog — not from prose an LLM host has to interpret. Gate
sequencing, author/reviewer dispatch, separation-of-duties enforcement, and
human/mutation-gate stops are graph control flow, checked in code and
covered by tests.

This replaces the plugin's earlier skill-based orchestration (six
`SKILL.md` files a Claude Code/Codex CLI host read and followed step by
step). That layer has been retired; this package is the only way to
actually drive a task through the lifecycle now. The deterministic kernel
CLI in [`../kernel/`](../kernel) is unaffected —
it still owns the contracts/schemas this engine is built from, plus
project-overlay bootstrapping (`init`/`detect`).

## Setup

```sh
cd engine
uv sync
uv run python -m pytest
```

`python -m pytest` rather than `uv run pytest`: the latter needs pytest's
console-entry-point script present in `.venv/bin`, and a venv can end up with
the package importable but that script missing — `uv sync` reports success and
`uv run pytest` then fails with `Failed to spawn: pytest`, which reads as a
missing dependency rather than a damaged venv. The module form runs from the
import alone. (If you hit that state, `uv sync --reinstall` restores the
script.)

No `ANTHROPIC_API_KEY`/`OPENAI_API_KEY` is required to run the tests — they
use a deterministic `FakeModelClient`. Set
`AGENTIC_SDLC_LANGGRAPH_FAKE_MODEL=1` to make the CLI/service use it too
(no network calls), instead of a real model-backed client. Otherwise,
`AGENTIC_SDLC_LANGGRAPH_MODEL_PROVIDER` selects which real client:
`anthropic` uses `AnthropicModelClient`; `openai` uses
`OpenAICompatibleModelClient` against any OpenAI-compatible
chat-completions server (OpenAI itself — including Codex's own backend —
or a self-hosted/third-party server mirroring its API shape: vLLM,
Ollama, Azure OpenAI, LiteLLM, etc, via `OPENAI_BASE_URL`), and requires
`AGENTIC_SDLC_LANGGRAPH_OPENAI_MODEL` to name the model to call.
Anthropic is one option, not a requirement: if
`AGENTIC_SDLC_LANGGRAPH_MODEL_PROVIDER` is left unset, the provider is
auto-detected from whichever credential is actually present
(`ANTHROPIC_API_KEY` vs. `OPENAI_API_KEY`/`OPENAI_BASE_URL`/
`AGENTIC_SDLC_LANGGRAPH_OPENAI_MODEL`); with neither (or both) present,
dispatch fails fast at graph-build time with an actionable error instead
of only failing once a gate actually dispatches and hits a missing key
deep in the SDK. An external CLI agent (Codex CLI or anything else) can
also be wired in per-agent via the agent catalog's `transport: "a2a"` +
`endpoint`, independent of this provider selection — see
`agents.A2AModelClient`/`DispatchingModelClient`.

## CLI

Installed as a console script, `agentic-sdlc-lg`:

```sh
export AGENTIC_SDLC_LANGGRAPH_FAKE_MODEL=1   # or a real ANTHROPIC_API_KEY
ROOT=/path/to/project

uv run agentic-sdlc-lg plan --root "$ROOT" --task-id demo-1 \
  --task "Define and review a small internal order-processing API architecture and service"

# Optionally link a GitLab issue as G1 Intent's / G2 Requirements Baseline's
# recorded source (fetched and validated via `glab api`, not just a
# free-text label) -- <project-path>#<iid> form. Never approval evidence;
# gate approval is unaffected either way. See gitlab_issue.py.
uv run agentic-sdlc-lg plan --root "$ROOT" --task-id demo-2 \
  --task "..." --intent-gitlab-issue group/project#42 --requirements-gitlab-issue group/project#43

echo '{"status":"approved","approver":{"id":"product_owner","role":"Product Owner","kind":"human"},"evidence_refs":[]}' \
  > /tmp/decision.json
uv run agentic-sdlc-lg resume --root "$ROOT" --task-id demo-1 --decision /tmp/decision.json

uv run agentic-sdlc-lg status   --root "$ROOT" --task-id demo-1
uv run agentic-sdlc-lg export   --root "$ROOT" --task-id demo-1 --output /tmp/run-record.json
uv run agentic-sdlc-lg validate --root "$ROOT" --task-id demo-1   # exits 0/1/2, see below
uv run agentic-sdlc-lg invalidate --root "$ROOT" --task-id demo-1 --earliest-gate G2 --reason "..." --actor "..."
uv run agentic-sdlc-lg reenter    --root "$ROOT" --task-id demo-1 --earliest-gate G2 --reason "..." --actor "..."

# Stage A, GitLab-only: back a G2 Requirements Baseline item list with real
# GitLab issues, idempotently (reused by label across re-entries, never
# duplicated). See requirement_issues.py.
uv run agentic-sdlc-lg create-requirement-issues --root "$ROOT" --task-id demo-1 \
  --project group/project --items /path/to/items.json --as-bot svc-agentic-sdlc \
  --allow-classification internal   # --dry-run (default): prints a plan digest only

uv run agentic-sdlc-lg create-requirement-issues --root "$ROOT" --task-id demo-1 \
  --project group/project --items /path/to/items.json --as-bot svc-agentic-sdlc \
  --allow-classification internal --apply --plan-digest <digest from --dry-run>

uv run agentic-sdlc-lg list-requirement-issues --root "$ROOT" --task-id demo-1
```

`create-requirement-issues` requires a dedicated bot/machine GitLab
identity (`--as-bot`): the command calls `glab api user` and refuses to
proceed unless the *authenticated* identity matches `--as-bot` exactly
(case-insensitively). The tool only verifies this -- it does not choose or
switch credentials for you. Point your `glab` credential configuration
(e.g. `GLAB_CONFIG_DIR`, or whatever your `glab` install uses to select a
host/token) at the bot's credentials *before* running this command,
especially before `--apply`.

`--apply` holds a whole-run lock (`<root>/.agentic-sdlc/runs/<task_id>/
requirement-issues.lock`, `O_CREAT|O_EXCL`) for the duration of the run.
This lock is **local-filesystem-scoped only** -- it prevents two
concurrent `--apply` runs on the *same host* sharing the same `root`, but
does **not** prevent two different hosts/runners from concurrently
applying against the same task against two independent local filesystems.
Only one host/runner should be running `--apply` for a given task at a
time; this is an operational convention this tool cannot enforce across
hosts. If that convention is violated and a genuine race occurs, the
result is *detected*, not *prevented*: the per-item label search (the
actual idempotency mechanism -- see `requirement_issues.py`) will find
`n > 1` matching issues on a later run and abort with the ambiguous-match
error, requiring human resolution, rather than silently duplicating or
corrupting anything.

**Accepted residual risk (v1, human-input-only):** post-creation
verification can only catch quick-action injection that corrupts a field
it actually re-checks (labels, assignees, confidential, state, title,
project, author). GitLab quick actions with no corresponding checkable
field (`/relate`, `/spend`, `/subscribe`, `/due`, `/weight`, `/milestone`,
`/epic`, ...) are not independently detectable by that verification step;
the actual primary control is the description-line quick-action rejection
in `sanitize_description`. This is accepted for v1 because Stage A only
ever accepts human-supplied item content (no agent/LLM content path exists
yet) -- see `requirement_issues.py`'s module docstring. This acceptance
must be revisited before any future stage that lets agent-authored content
reach `create_gitlab_issue` is authorized.

Each command is a separate process — state persists across them in
`<root>/.agentic-sdlc/state.db` (a LangGraph `SqliteSaver`) and
`<root>/.agentic-sdlc/runs/<task_id>/graph-config.json` (records what
`plan` resolved, so later commands can rebuild an identical graph).

`validate` follows the kernel CLI's convention: exit `0` (valid and ready),
`2` (structurally valid but blocked — e.g. an authority is unassigned), or
`1` (a real error).

## Service

A minimal FastAPI service exposes the same lifecycle over HTTP —
`POST /tasks`, `POST /tasks/{task_id}/resume`, `GET /tasks/{task_id}` — see
`service.py`. This is what makes the engine runnable with no chat CLI (or
any interactive terminal) in the loop at all: a webhook, cron, or any other
caller can drive a task end to end.

## Modules

| Module | Purpose |
|---|---|
| `state.py` | Graph state schema (`SDLCState`/`GateState`), mirroring `run-record.schema.json` |
| `contracts.py`, `planning.py` | Contract loaders and build-time gate-sequence derivation |
| `provider.py` | Pure-function provider/profile loading (semver, path confinement, separation-of-duties at load time) |
| `agents.py` | `ModelClient` protocol, `FakeModelClient`/`AnthropicModelClient`/`OpenAICompatibleModelClient`, role-prompt resolution |
| `graph.py` | Declarative graph builder: dispatch, gate decisions, human/mutation-gate interrupts |
| `reentry.py` | Invalidate/reenter as `graph.update_state(...)` operations, with real re-execution on reenter |
| `export.py`, `validate.py` | Schema-shaped run-record export and the residual (0/1/2) validator |
| `github_approval.py` | GitHub PR review → `Command(resume=...)` approval adapter |
| `gitlab_issue.py` | GitLab issue linkage (G1/G2 source) plus the four GitLab calls `requirement_issues.py` uses |
| `requirement_issues.py` | Stage A `create-requirement-issues`/`list-requirement-issues`: item validation, sanitization, plan-digest, sidecar ledger, orchestration -- backend-neutral, never imported by `graph.py` |
| `runtime.py`, `cli.py`, `service.py` | Cross-process graph rebuild, CLI, and HTTP service |

Every module's docstring documents which legacy `agentic_sdlc.py` function
(if any) it ports, and calls out deliberate deviations explicitly — most
are either a fix for a legacy bug (e.g. dead/broken authority-role checks
in `validate_repository` weren't ported) or a required architectural
change (e.g. no module-level global state, since this engine needs to be
reentrant across separate processes in a way the original one-shot CLI
never had to be).
