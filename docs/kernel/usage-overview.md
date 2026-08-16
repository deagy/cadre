# How Agentic SDLC is used

## What it is

Agentic SDLC is governed, runner-neutral software-delivery lifecycle tooling
built around ten fixed lifecycle gates — **G1 Intent → G2 Requirements
Baseline → G3 Architecture → G4 Governance/Data → G5 Security/Crypto → G6
Verification → G7 Evidence → G8 Release Readiness → G9 Deployment
Authorization (human-only) → G10 Runtime Conformance**. It doesn't do the
engineering work itself — it enforces *sequencing, evidence, and
human-approval discipline* around work that happens elsewhere (in a target
project, done by humans or dispatched agents).

## Three parts, three jobs

| Part | Role |
|---|---|
| **The kernel** (`kernel/`, pip/pipx-installable as `agentic-sdlc`) | Bootstraps a target project, tracks gate/approval state as JSON on disk, validates it. Deterministic bookkeeping — no orchestration. |
| **The LangGraph engine** (`engine/`, installed as `agentic-sdlc-lg`) | Actually *drives* a task through the gates as a compiled graph: dispatches author/reviewer agents, enforces separation-of-duties, stops at human/mutation-gate interrupts. This is what replaced the earlier prompt-driven "six `SKILL.md` files an LLM had to interpret step by step." |
| **A provider** (e.g. `providers/agentic-sdlc-defaults/`, or an external one like `deagy/agents`) | Supplies the domain-specific pieces the kernel deliberately ships none of: an agent catalog, profiles (routing/gate bindings), optional extensions. The kernel is generic; providers make it concrete for a given team's stack. |

**Key architectural rule**: a consuming project owns its own decisions and
state under `.agentic-sdlc/` (authorities, overlays, run records). Neither
the kernel nor a provider ever becomes authoritative for that project's
approvals — install/upgrade the tooling, and the project's own decisions
stay put.

## The actual usage flow

1. **Install** — build the kernel from a checkout (`./bin/agentic-sdlc`
   builds and execs the Go binary; see the [kernel guide](../../kernel/README.md#install)),
   and separately set up the
   LangGraph engine if you want real orchestration (`uv sync` in
   `engine/`, or install it too).
2. **Initialize a target project**:
   ```sh
   agentic-sdlc init --root /path/to/target [--provider provider.json --profile <id>]
   ```
   Detects candidate stack/commands, writes `.agentic-sdlc/project.json`,
   `authorities.json`, `impact-profile.json`, etc. Deliberately leaves human
   authorities, compliance applicability, and production/persistent
   classification **unresolved** — a human has to fill those in. Without
   `--provider`, it runs in "kernel-only mode" (no agent catalog/profiles).
3. **Assign human authorities and resolve applicability** in the generated
   overlay — a manual, accountable step, not automated.
4. **Drive the actual work**, one of two ways:
   - **Bookkeeping only** (kernel CLI): `plan` (dispatch plan), `status`,
     `decide`/`approve-from-github`/`approve-from-gitlab*` (record a human
     gate decision, or turn a real PR review/MR approval into gate-approval
     evidence), `link-intent-from-gitlab-issue`/`link-requirements-from-gitlab-issue`
     and their GitHub counterparts (attach a GitLab or GitHub issue as the
     recorded *source* of intent/requirements — separate from approval),
     `create-gate-issues`/`create-github-gate-issues` (publish tracking
     issues for a task's gates and approvals on GitLab/GitHub),
     `publish-gate-status` (post a read-only gate-status summary comment on
     a PR/MR), `request-gate-reviewers`/`request-gate-reviewers-gitlab`
     (report, never post, reviewer candidates), `publish-reviewer-nudge`
     (post an advisory, non-notifying reviewer suggestion comment, GitHub
     only), `invalidate`/`reenter` (re-baseline a gate after a material
     change), `validate` (exit `0` ready / `2` blocked / `1` error).
   - **Real orchestration** (`agentic-sdlc-lg`, the LangGraph engine): `plan`
     actually dispatches author/reviewer agents per gate and runs to the
     first `interrupt()`; `resume` feeds in a human decision (or a fetched
     GitHub review) to continue; each gate's `human_approval_{gate}`
     interrupt is a hard stop until an authorized human (or their proxy
     evidence) says so. Also available as a FastAPI service and an A2A
     JSON-RPC surface, so a webhook or another agent can drive it with zero
     chat-CLI involvement.
5. **Validate before any handoff or release** — `validate` (either surface)
   checks structural correctness *and* readiness (unresolved decisions
   block, they don't silently pass).

## How an external provider (e.g. `deagy/agents`) fits in

An external provider is not a fork or dependency of the kernel — it supplies
its own `provider.json` (agent catalog + profile(s)) and, if it wants a
convenience launcher, a thin compatibility command that just calls the real
`agentic-sdlc`/`agentic-sdlc-lg` binaries with that provider manifest
pre-supplied. The kernel and engine have no awareness that any particular
provider exists. This one-way boundary is enforced structurally (both
`validate_repository()` in the kernel and the LangGraph engine's
gate-decision nodes reject configs where the same identity is author and
independent reviewer) and by convention (a provider never becomes
authoritative for a consuming project's gate approvals).
