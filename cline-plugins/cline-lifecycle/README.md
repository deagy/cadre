# Cline Lifecycle Plugin (Agentic SDLC G1-G10 tools)

A third distinct plugin, alongside [`cline/`](../cline) (role-selection planning)
and [`cline-agents/`](../cline-agents) (role dispatch). This plugin,
`cline-lifecycle`, exposes G1-G10 Agentic SDLC lifecycle governance as 23
deterministic tool calls, wrapping the exact `bin/cadre sdlc <subcommand>`
invocations the `cadre-lifecycle-core`/`-github`/`-gitlab` plugins' skills
already document for Claude Code / Codex
(`plugins/lifecycle/skills/{lifecycle-onboarding,lifecycle-review,brief-pending-gates}/SKILL.md`,
all 8 of `plugins/lifecycle-gitlab/skills/*/SKILL.md`, and all 9
`plugins/lifecycle-github/skills/*/SKILL.md` — GitHub has one more skill than
GitLab, `publish-reviewer-nudge-github`, which has no GitLab equivalent —
except `brief-pending-gates-{gitlab,github}`, see below). `sdlc_plan`
additionally wraps `agentic-sdlc plan`, a forge-agnostic kernel subcommand
referenced only in passing ("... or `cadre sdlc plan` first") across 13
forge-specific `SKILL.md` files rather than given a numbered step of its own
in any one of them — see its row in the table below.

**Parity with the Claude Code / Codex skill surface is the standard here**, and
it is met at two levels: every kernel subcommand those skills drive has a tool,
*and* every flag those skills document is reachable through it. A tool that
wraps the right subcommand but silently drops an opt-in flag is the same gap in
a less visible form — a Cline session cannot fall back to running the CLI by
hand the way a Claude Code / Codex session reading the `SKILL.md` can. The
deliberate exceptions are listed under "Flags intentionally not exposed" below.

Skills are a Claude Code / Codex mechanism with no Cline equivalent (see
[`../skills/run-agent-orchestration/references/runner-adapters.md`](../skills/run-agent-orchestration/references/runner-adapters.md)'s
"## Cline" section), so none of that governance was previously reachable from
Cline at all — this plugin closes that gap the same way `cline/` and
`cline-agents/` already close the equivalent gap for role selection/dispatch.

The 18 forge-specific tools close a further, narrower gap: `cadre-lifecycle-gitlab`
and `cadre-lifecycle-github` bundle 8 and 9 forge-specific skills respectively
for Claude Code / Codex, and every one of them but
`brief-pending-gates-{gitlab,github}` wraps a distinct forge-specific kernel
subcommand — all of those are mirrored here, one tool per subcommand:

- **GitLab (7 tools):** `sdlc_approve_from_gitlab`, `sdlc_approve_from_gitlab_mr`
  (`lifecycle-review-gitlab`); `sdlc_link_intent_from_gitlab_issue`,
  `sdlc_link_requirements_from_gitlab_issue` (`link-source-issue-gitlab`);
  `sdlc_list_gate_issues_gitlab`, `sdlc_create_gate_issues_gitlab`
  (`gitlab-gate-tracking`); `sdlc_request_gate_reviewers_gitlab`
  (`report-gate-reviewers-gitlab`).
- **GitHub (9 tools):** `sdlc_approve_from_github`, `sdlc_approve_from_github_pr`
  (`lifecycle-review-github`); `sdlc_link_intent_from_github_issue`,
  `sdlc_link_requirements_from_github_issue` (`link-source-issue-github`);
  `sdlc_list_github_gate_issues`, `sdlc_create_github_gate_issues`
  (`create-github-gate-issues`); `sdlc_request_gate_reviewers_github`
  (`report-gate-reviewers-github`); `sdlc_list_reviewer_nudge`,
  `sdlc_publish_reviewer_nudge` (`publish-reviewer-nudge-github`, GitHub-only —
  no GitLab equivalent skill exists).
- **Shared across both forges (2 tools):** `sdlc_list_gate_status`,
  `sdlc_publish_gate_status` (`publish-gate-status-gitlab`/`-github` — one
  pair of tools, `forge: "gitlab" | "github"` selects the shape).

`brief-pending-gates-gitlab`/`-github` are the only two forge-specific skills
NOT mirrored: both just wrap `bin/cadre sdlc status`, i.e. exactly what
`sdlc_status` above already calls — there is nothing new to wrap.

`link-source-issue-github` was the one other skill previously unmirrored. Its
two subcommands were, like the 10 noted below, absent from the stale kernel
this plugin was first developed against, so they were left out rather than
shipped unverified; the `kernel_compatibility` floor now sits past that and
both are live-verified, so the GitHub and GitLab link-source pairs are
symmetric. The GitHub pair takes `issueNumber` where the GitLab pair takes
`issueIid` — the forges' own naming, mirrored rather than normalized, so a
flag named in a kernel error message maps back to a tool field.

This repository's [`provider/provider.json`](../../provider/provider.json)
pins the minimum `agentic-sdlc` version, as `kernel_compatibility.minimum` —
read the floor there rather than from this page, which would only go stale.
12 of the 18 GitLab/GitHub tools require it — the 10 noted above plus the two
`sdlc_link_*_from_github_issue` tools (see CHANGELOG.md for why). If a
tool call fails with "invalid choice" on an older pinned kernel,
`agentic-sdlc <subcommand> --help` will tell you what that kernel actually
supports.

## Requires the external `agentic-sdlc` kernel

`bin/cadre sdlc` is a thin pass-through to the separately-installed
`agentic-sdlc` kernel binary — gate state and transitions live entirely in
that kernel, a permanently separate, independently versioned component (its
source is in-tree at `kernel/`, but it is installed and invoked as a
standalone binary; see root `CLAUDE.md`'s kernel ownership boundary). Install
it first via one of the lifecycle plugins' bundled bootstrap scripts, e.g.:

```sh
python3 plugins/lifecycle/tools/bootstrap_sdlc.py --root /path/to/project --profile secure-cloud
```

Every tool below fails with a structured error (not a throw) if the kernel
isn't resolvable — this plugin does not install it.

## Install

```sh
git clone https://github.com/deagy/cadre.git
cline plugin install ./cadre/cline-plugins/cline-lifecycle --force
```

## System prompt

This plugin registers a rule (`api.registerRule`, the "rules" capability
declared in [`index.ts`](index.ts)'s manifest and
[`package.json`](package.json)'s `cline.plugins[0].capabilities`) whose
content is appended to the session's composed system prompt automatically —
no host-application configuration required. See
[`../cline/README.md`](../cline/README.md)'s "System prompt" section for the
`@cline/core`/`@cline/shared` source confirming `registerRule` is a genuine,
plugin-controlled injection point, distinct from a host's own `systemPrompt`
config field. The registered content begins with the exact sentence
`"You are a coding assistant with access to Cadre role subagents."` and adds
a clause naming the `sdlc_*` tool family and the separation-of-duties
invariant the external Agentic SDLC kernel enforces (never approve/decide a
gate this session prepared evidence for itself).

If `cline`, `cline-agents`, and/or `cline-lifecycle` are all installed in
the same session, each plugin registers its rule independently -- a plugin's
`setup(api, ctx)` has no way to detect whether a sibling plugin is also
loaded, so each one includes the full base sentence rather than risk
omitting it when installed alone. A session with more than one of these
plugins installed will therefore see the base sentence once per installed
plugin (mildly redundant -- three short, clearly-scoped paragraphs, not one
combined string) rather than a single deduplicated system prompt. See each
plugin's own README for its exact registered content.

## Tools

| Tool | Wraps | Purpose |
|---|---|---|
| `sdlc_init` | `bin/cadre sdlc init` | Initialize G1-G10 lifecycle tracking for a project. Pass `dryRun: true` first and inspect the result before writing for real. |
| `sdlc_validate` | `bin/cadre sdlc validate` | Validate a project's Agentic SDLC configuration and run-record state. Returns errors/blockers as JSON. |
| `sdlc_plan` | `bin/cadre sdlc plan` | Create (or overwrite) a task's dispatch plan and pending run record. This is a real write, not a dry-run preview -- the kernel's `plan` subcommand has no dry-run mode. Needed before `sdlc_status`/`sdlc_decide` can operate on a brand-new task-id. |
| `sdlc_status` | `bin/cadre sdlc status` | Report a task's pending/decided lifecycle gates. Read-only. |
| `sdlc_decide` | `bin/cadre sdlc decide` | Record a lifecycle gate decision. |
| `sdlc_approve_from_gitlab` | `bin/cadre sdlc approve-from-gitlab` | Record a human gate approval from prepared GitLab MR-approval evidence. |
| `sdlc_approve_from_gitlab_mr` | `bin/cadre sdlc approve-from-gitlab-mr` | Record a human gate approval by fetching and verifying an approved GitLab MR approval live. Fails closed if none is found. |
| `sdlc_link_intent_from_gitlab_issue` | `bin/cadre sdlc link-intent-from-gitlab-issue` | Record a GitLab issue as the recorded source for a task's G1 (Intent) gate. |
| `sdlc_link_requirements_from_gitlab_issue` | `bin/cadre sdlc link-requirements-from-gitlab-issue` | Record a GitLab issue as the recorded source for a task's G2 (Requirements Baseline) gate. |
| `sdlc_approve_from_github` | `bin/cadre sdlc approve-from-github` | Record a human gate approval from prepared GitHub PR-review evidence. |
| `sdlc_approve_from_github_pr` | `bin/cadre sdlc approve-from-github-pr` | Record a human gate approval by fetching and verifying an approved GitHub PR review live. Fails closed if none is found. |
| `sdlc_link_intent_from_github_issue` | `bin/cadre sdlc link-intent-from-github-issue` | Record a GitHub issue as the recorded source for a task's G1 (Intent) gate. |
| `sdlc_link_requirements_from_github_issue` | `bin/cadre sdlc link-requirements-from-github-issue` | Record a GitHub issue as the recorded source for a task's G2 (Requirements Baseline) gate. |
| `sdlc_list_gate_issues_gitlab` | `bin/cadre sdlc list-gate-issues` | List a task's existing GitLab gate-tracking issues and their assigned approval sub-issues. Read-only. |
| `sdlc_create_gate_issues_gitlab` | `bin/cadre sdlc create-gate-issues` | Create/reuse GitLab gate-tracking issues plus assigned approval sub-issues. Defaults to a dry-run preview; `apply: true` requires the `planDigest` the preceding dry-run returned. |
| `sdlc_list_github_gate_issues` | `bin/cadre sdlc list-github-gate-issues` | List a task's existing GitHub gate-tracking issues and their assigned approval sub-issues. Read-only. |
| `sdlc_create_github_gate_issues` | `bin/cadre sdlc create-github-gate-issues` | Create/reuse GitHub gate-tracking issues plus assigned approval sub-issues. Defaults to a dry-run preview; `apply: true` requires the `planDigest` the preceding dry-run returned. |
| `sdlc_list_gate_status` | `bin/cadre sdlc list-gate-status` | Show a task's locally-recorded gate-status publication ledger (both forges). Zero-network, can be stale. Read-only. |
| `sdlc_publish_gate_status` | `bin/cadre sdlc publish-gate-status` | Publish/update a one-way gate-status summary note on a GitLab MR or GitHub PR (`forge` selects the shape). Never read back as an approval. Defaults to a dry-run preview. |
| `sdlc_request_gate_reviewers_gitlab` | `bin/cadre sdlc request-gate-reviewers-gitlab` | Report which GitLab usernames would be set as MR reviewers. Read-only/reporting only. |
| `sdlc_request_gate_reviewers_github` | `bin/cadre sdlc request-gate-reviewers` | Report which GitHub logins would be requested as PR reviewers. Read-only/reporting only. |
| `sdlc_list_reviewer_nudge` | `bin/cadre sdlc list-reviewer-nudge` | Show a task's locally-recorded reviewer-nudge publication ledger. GitHub-only. Read-only. |
| `sdlc_publish_reviewer_nudge` | `bin/cadre sdlc publish-reviewer-nudge` | Publish/update an advisory PR comment naming good reviewer candidates. Never a formal review request. GitHub-only. Defaults to a dry-run preview. |

Every tool accepts an optional `root` (defaults to the host session's
workspace root) and otherwise mirrors the exact flags the corresponding
`SKILL.md` documents for its runner-neutral CLI invocation — see
[`index.ts`](index.ts) for the full schemas.

### `sdlc_decide` and the four `sdlc_approve_from_*` tools add no approval logic of their own

The `agentic-sdlc` kernel itself structurally refuses a decision from the same
identity as the gate's preparer/verifier (this repository's human-approval
invariant — see root `CLAUDE.md`). `sdlc_decide`, `sdlc_approve_from_gitlab`,
`sdlc_approve_from_gitlab_mr`, `sdlc_approve_from_github`, and
`sdlc_approve_from_github_pr` all only relay whatever the kernel decides,
success or refusal, as JSON; none attempts its own separation-of-duties
check, and none must ever be called on behalf of a human who has not
actually made the decision (or, for the forge-specific tools, actually
recorded/authored the GitLab MR approval or GitHub PR review) being
recorded.

### The four `sdlc_link_*_from_*_issue` tools record a source, not an approval

`sdlc_link_intent_from_gitlab_issue` / `sdlc_link_requirements_from_gitlab_issue`
and their GitHub counterparts only attach an issue reference to G1/G2
respectively — they never advance, approve, or invalidate a gate. Use
`sdlc_approve_from_gitlab`/`sdlc_approve_from_gitlab_mr` (or the `_github`
pair) to actually record an approval.

### Flags intentionally not exposed

Every other flag the wrapped subcommands accept is reachable, including the
consequential opt-ins (`reconcileAssignees`, `includeScope`, `linkType`,
`breakLock` — see below). Three are deliberately omitted:

- **`--i-know-this-is-mocked`** (on `create-gate-issues`,
  `create-github-gate-issues`, `publish-gate-status`,
  `publish-reviewer-nudge`): a test-harness interlock, required only when
  `AGENTIC_SDLC_TEST_ISSUE_CREATE_FILE` is set. Exposing it to a model would
  hand it a way to make a mocked run look like a real one.
- **`sdlc init --force`**: the kernel documents it as "reserved for future
  use; in this release init never overwrites existing wrapper or managed
  overlay files, with or without `--force`" — a no-op. (The `--force` the
  onboarding `SKILL.md` files reference belongs to `bin/cadre init`, a
  different command.)
- **`sdlc init --extension`**: no lifecycle `SKILL.md` drives it, so adding it
  would exceed the Claude Code / Codex parity standard rather than reach it.

### `reconcileAssignees`, `includeScope`, `linkType`, and `breakLock` are human-authorized opt-ins

All four default to off, which is also the kernel's default, and all four
change something a human should have agreed to first:

- **`reconcileAssignees`** (both `create_*_gate_issues` tools) — when the
  forge's issue assignee no longer matches `authorities.json`, the kernel
  reports the drift and changes nothing. Passing this overwrites the forge's
  assignee. `gitlab-gate-tracking`'s `SKILL.md` is explicit that this is a
  remediation the human asks for: "only re-run with `--reconcile-assignees` if
  they explicitly say yes; never pass that flag proactively." The same rule
  binds a Cline session — report the drift, then wait.
- **`includeScope`** (both `create_*_gate_issues` tools) — adds a sanitized
  scope line to each gate issue's description. That text lands on a real forge
  issue, so leave it off for any task whose scope should not be externally
  visible.
- **`linkType: "relates_to"`** (`sdlc_create_gate_issues_gitlab` only; the
  kernel's GitHub variant has no equivalent) — also records the
  gate/approval issue relationship through the GitLab Issue Links API, and
  fails closed (exit 2) if that API is unavailable.
- **`breakLock`** (`sdlc_create_gate_issues_gitlab`,
  `sdlc_create_github_gate_issues`, `sdlc_publish_gate_status`,
  `sdlc_publish_reviewer_nudge`) — overrides a held ledger lock. A held lock
  usually means a concurrent run is in flight, so this is an operator
  override for a confirmed-stale lock, never a retry knob.

### `sdlc_create_gate_issues_gitlab` / `sdlc_create_github_gate_issues` require a plan-digest handshake before assigning anyone

Both default to a dry-run preview (omit `apply`). Assigning a real gate
approval sub-issue to a real person is consequential and externally visible,
so the underlying kernel command requires a second call with `apply: true`
and the exact `planDigest` value the dry-run returned — never fabricate or
guess one. If the kernel reports the digest is stale, re-run the dry-run and
get fresh confirmation rather than retrying blindly.

### Four tools treat exit code 2 as a normal report, not a failure

`sdlc_request_gate_reviewers_gitlab`, `sdlc_request_gate_reviewers_github`,
`sdlc_create_gate_issues_gitlab`, and `sdlc_create_github_gate_issues` all
wrap kernel commands where exit 0 means "completed cleanly" and exit 2 means
"completed, but the result contains refusals" (all four) or "...refusals or
assignee drift" (the two create-gate-issues commands) — both are valid,
non-failure reports, not something to retry, and all four tools return that
report's JSON normally either way, including (for the two create-gate-issues
tools) the `plan_digest` a subsequent `apply: true` call needs and, in
`--apply` mode, confirmation of any issue that was already created before the
refusal was hit — except for any `GateIssuesBlocked`/`GateIssuesGithubBlocked`
failure during `--apply` (also exit 2; not limited to a concurrent
plan-digest mismatch — ambiguous label matches, identity mismatches, a held
lock, and several other cases all raise it too), which the kernel reports as
a bare `{"error": "..."}` rather than the full structured result, so those
specific cases surface the same way a structural failure would. Only a
genuine structural failure (exit 1: MR/PR
not found or closed, an identity mismatch, a malformed request) is expected
to surface as any of the four tools' `error` field otherwise.

## Behavioral detail

Every tool wraps a `bin/cadre sdlc` subcommand and adds no approval logic of
its own — separation of duties is enforced entirely by the external kernel.
Each tool throws if no `root` argument is given and no workspace root could
be resolved; otherwise it runs the real subprocess and returns a structured
error (never a thrown exception or a false success) for cases like an
un-onboarded project, a task with no run record, or a nonexistent
task/gate. `sdlc_init` supports a real dry-run preview against a scratch
project. A handful of kernel subcommands are gated behind
`kernel_compatibility` since not every `agentic-sdlc` release in range ships
them yet — see the tests for exactly which.

[`index.test.mts`](index.test.mts) covers all of the above against a real,
unmocked `bin/cadre sdlc` subprocess.
[`index.exitcode.test.mts`](index.exitcode.test.mts) is a separate file
specifically for `runCadreSdlcAllowingReportExitCodes`'s exit-code-branching
behavior — it mocks `node:child_process` at module level (to deterministically
exercise the exit-2-with-JSON-on-stdout path a live kernel call can't
reliably reproduce), so it can't share a file with `index.test.mts`'s
real-subprocess tests.

## Development

```sh
cd cline-lifecycle && npm test        # run tests
cd cline-lifecycle && npm run typecheck  # TypeScript type checking
```
