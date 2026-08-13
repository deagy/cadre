# Agentic SDLC plugin

This plugin makes the repository's G1–G10 Agentic SDLC portable. It supplies a versioned lifecycle kernel — the G1–G10 gate contracts, mutation-gate definitions, run-record/agent-catalog/profile/provider JSON Schemas — plus a deterministic CLI (`agentic_sdlc/`, the pip/pipx-installable `agentic-sdlc` distribution) for bootstrapping a target project's overlay, planning a task's dispatch, and validating lifecycle state, while leaving project-specific authority and lifecycle state in the target repository.

## Install

> [!WARNING]
> **Not on PyPI.** The PyPI name `agentic-sdlc` belongs to an unrelated
> third-party project — `pip install agentic-sdlc` installs *different
> software*, not this kernel. Use only the checkout or git-URL forms below, or
> a wheel from this repository's
> [Releases](https://github.com/deagy/cadre/releases) (filter for `kernel-v*`
> tags). See [SECURITY.md](../SECURITY.md).

```sh
pipx install ./kernel        # from a checkout
pipx install "git+https://github.com/deagy/cadre.git@kernel-v<version>#subdirectory=kernel"
```

Either form puts a real `agentic-sdlc` executable on `PATH`, isolated in its own environment, with no repository checkout required at runtime — `contracts/` is bundled into the installed distribution at build time (see `pyproject.toml`). `pip install` works the same way if you'd rather manage the environment yourself; use `pip install -e ./kernel` for an editable install while developing the kernel itself. Requires Python 3.10+.

Orchestrating actual work against this kernel — dispatching author/reviewer roles, stopping at human/mutation gates, tracking gate state across a task's lifetime — is done by the LangGraph engine in [`../engine/agentic_sdlc_langgraph/`](../engine/agentic_sdlc_langgraph), not by this plugin. See that package's README for the `agentic-sdlc-lg` CLI and the standalone service. (An earlier version of this plugin shipped that orchestration as six Claude Code/Codex CLI skills an LLM host had to interpret step by step; those were retired once the LangGraph engine replaced them with real, testable control flow.)

The intended adoption path is:

```text
Initialize target repository -> review detected overlay -> assign human
authorities and resolve applicability -> plan or orchestrate work via
agentic-sdlc-lg
```

Initialization makes a project immediately usable for planning, artifact preparation, independent review, and validation. It does not make unresolved organizational decisions merely to produce a green result.

## Initialize a project

The canonical command is the installed `agentic-sdlc` executable (see
"Install" above), or `../bin/agentic-sdlc` (from the repository root:
`./bin/agentic-sdlc`) / `python3 -m agentic_sdlc` from a checkout during
development, without installing anything:

```sh
agentic-sdlc init --root /path/to/target
```

The CLI `init` command detects candidate stack and command information, writes the project overlay, preserves unknowns and unassigned authorities, and reports blockers. Run `validate` separately afterward. Review generated files before using them as policy.

Use `--help` for the exact options supported by the installed plugin version:

```sh
agentic-sdlc init --help
```

## Portable architecture

The distribution has three deliberately separate layers:

| Layer | Owner | Contents |
|---|---|---|
| Portable kernel | Plugin maintainer | G1–G10 definitions, mutation-gate separation, schemas, lifecycle state, and validation. |
| Project overlay | Target project | Technology and command detection, routing/profile choices, authority assignments, environment declarations, applicability decisions, and kernel version lock. |
| Project state | Target project | Dispatch plans, run records, findings, exceptions, invalidations, evidence references, and human approval references. |

The plugin may be upgraded independently. It never becomes the authoritative location for a project's decisions or evidence.

Initialization creates or manages this target-repository structure:

```text
.agentic-sdlc/
├── project.json
├── authorities.json
├── impact-profile.json
├── routing.json
├── commands.json
├── version.lock
└── runs/<task-id>/
    ├── dispatch-plan.json
    └── run-record.json
.codex/agents/                 # Profile-selected project agent wrappers (Codex CLI)
.claude/agents/                # Profile-selected project agent wrappers (Claude Code)
AGENTS.md                      # Small managed Agentic SDLC instruction block
```

`init --runner {codex,claude,both}` (default `both`) controls which wrapper set is generated; both are safe to keep even if only one runner is in active use. Existing custom agent wrapper files are never overwritten, and existing managed overlay files (`.agentic-sdlc/project.json`, `authorities.json`, `impact-profile.json`, `routing.json`, `commands.json`) are never overwritten either. `init`, with or without `--force`, remains non-destructive and idempotent with respect to existing wrapper and overlay files.

For an interrupted or stale initialization, inspect the explicit repair plan before changing anything:

```sh
agentic-sdlc repair --root /path/to/target
agentic-sdlc repair --root /path/to/target --apply
# Equivalent compatibility spelling: agentic-sdlc init --repair [--apply]
```

Repair is read-only by default. It recreates only missing baseline files and profile-generated wrappers, refreshes only the uniquely delimited Agentic SDLC block in `AGENTS.md`, and updates stale kernel/contract metadata in `version.lock`. It preserves existing project JSON, custom wrappers, run records, approvals, evidence, authority assignments, applicability decisions, and content outside that `AGENTS.md` block. Malformed managed JSON, an incomplete/ambiguous managed block, or provider-profile drift fails closed without a repair write; resolve or migrate that state explicitly instead.

## Safe defaults

Initialization and orchestration fail closed where a correct decision cannot be derived from repository content:

- Human decision authorities start unassigned unless explicitly configured.
- Conditional authorities for data/control ownership, key ownership, UAT, and
  runtime-implicated Security or Governance Leads start with `unknown`
  applicability. Marking one `not-applicable` requires an accountable rationale.
- Compliance, jurisdiction, specialized BOM, and extension applicability remain `unknown` until an accountable owner decides them.
- Environments are not assumed disposable, persistent, or production from a name alone.
- No gate is approved by initialization, detection, planning, or validation.
- Quality-gate readiness never substitutes for production, destructive, persistent-migration, privileged-identity, exception, or risk-acceptance authorization.
- Unknown applicable requirements block the affected gate instead of being treated as not applicable.

These defaults allow work products to be prepared immediately while preventing an incomplete bootstrap from silently granting authority.

## Profiles and extensions

A profile supplies provider-owned routing and contribution bindings. The kernel ships no profiles or agent catalog. Use kernel-only mode without `--profile`, or load an external provider such as `agentic-sdlc-defaults`.

Mutation gates are evaluated independently of providers, so production, destructive, persistent-migration, privileged-identity, and risk-acceptance requests still stop for human approval.

The kernel ships no domain extensions. A provider contributes profiles, an
agent catalog, and optional extensions through a versioned manifest:

```json
{
  "schema_version": 1,
  "id": "agentic-sdlc-defaults",
  "version": "0.3.0",
  "kernel_compatibility": {
    "minimum": "0.3.0",
    "maximum_exclusive": "1.0.0"
  },
  "agent_catalog": "agent-catalog.json",
  "profile_roots": ["profiles"],
  "extension_roots": ["extensions"]
}
```

Keep `kernel_compatibility` a deliberately wide, honest range rather than
pinning tightly to whatever the kernel's `VERSION` happens to read today.
`VERSION` (`agentic_sdlc/__init__.py`) is bumped by hand on every tagged
release with nothing enforcing it stays in sync with the actual tag — it
drifted for 9 releases (v0.4.0 through v0.12.0) before being caught, which
silently made every provider pinning a narrow range (like this manifest's
own former `[0.3.0, 0.4.0)`) reject perfectly compatible newer kernel
releases. A pre-1.0 provider that only relies on backward-compatible
additions should express that as `[<oldest tested>, 1.0.0)`, not
`[<oldest tested>, <oldest tested's next minor>)`.
```

(Illustrative shape, based on `providers/agentic-sdlc-defaults/provider.json`, the reference provider this repository ships — that file's own `extension_roots`/`dependencies` differ slightly since it declares no extensions. Any external provider, such as the one `deagy/agents` supplies, follows the same manifest shape with its own `id`.)

Load providers explicitly before the subcommand:

```sh
agentic-sdlc --provider /path/to/provider.json \
  init --root /path/to/project --profile secure-cloud --extension sqs-platform
```

Provider paths resolve relative to the manifest and must remain inside its
directory. Duplicate profile or extension IDs, incompatible versions, missing
resources, and path escapes fail closed. The selected provider identity,
version, and manifest digest are recorded in the project version lock.

## Commands

The bundled command entry point is `kernel/agentic_sdlc/` (the `agentic-sdlc` distribution's `[project.scripts]` entry point; see "Install" above):

```text
init        Create or update a project overlay using safe defaults.
detect      Inspect a repository and report candidate project characteristics.
plan        Produce a reviewable dispatch plan for a task.
validate    Validate the overlay and lifecycle records.
status      Report lifecycle and gate state for a task.
approve-from-github  Record a human lifecycle approval from a GitHub PR review.
approve-from-github-pr  Fetch an approved GitHub PR review and record it as lifecycle approval evidence.
approve-from-gitlab  Record a human lifecycle approval from a GitLab MR approval. Speculative: not the approval source this kernel's own default provider uses (see "Current limitations").
approve-from-gitlab-mr  Fetch an approved GitLab MR approval and record it as lifecycle approval evidence. Speculative: not the approval source this kernel's own default provider uses (see "Current limitations").
link-intent-from-gitlab-issue  Link a GitLab issue as the recorded source for G1 Intent.
link-requirements-from-gitlab-issue  Link a GitLab issue as the recorded source for G2 Requirements Baseline.
link-intent-from-github-issue  Link a GitHub issue as the recorded source for G1 Intent.
link-requirements-from-github-issue  Link a GitHub issue as the recorded source for G2 Requirements Baseline.
create-gate-issues  Publish GitLab gate/approval tracking issues for a task's lifecycle gates.
list-gate-issues   Print the gate-issues sidecar ledger for a task.
create-github-gate-issues  Publish GitHub gate/approval tracking issues for a task's lifecycle gates.
list-github-gate-issues   Print the GitHub gate-issues sidecar ledger for a task.
publish-gate-status  Post or update a one-way, read-only gate-status summary comment on a task's GitHub PR or GitLab MR.
list-gate-status   Print the gate-status sidecar ledger(s) for a task (both forges, zero network).
request-gate-reviewers  Report GitHub PR reviewer candidates for a task's lifecycle gates. Read-only / reporting only in this version -- see "Reporting GitHub PR reviewer candidates" below.
request-gate-reviewers-gitlab  Report GitLab MR reviewer candidates for a task's lifecycle gates. Read-only / reporting only -- see "Reporting GitLab MR reviewer candidates" below.
publish-reviewer-nudge  Post or update an advisory GitHub PR comment suggesting reviewers, based on request-gate-reviewers's classification. Never a review request, never notifies anyone -- see "Publishing an advisory reviewer nudge" below.
list-reviewer-nudge  Print the reviewer-nudge sidecar ledger for a task (GitHub only, zero network).
decide      Record a human decision (approved/rejected/request-changes) for a lifecycle gate, evidenced by an external URI -- the platform-agnostic counterpart to approve-from-github*/approve-from-gitlab*.
invalidate  Record a material change and invalidate the earliest affected gate and its dependents.
reenter     Prepare an invalidated run for explicit re-entry at a gate.
upgrade     Check (--check) or apply (--apply) a non-destructive kernel lock upgrade.
repair      Inspect (default) or apply a safe repair for an incomplete or stale initialization.
provider / profile / extension  Inspect loaded provider/profile/extension resources (list, or inspect for a given provider id).
show-contract  Print a bundled lifecycle contract or schema (lifecycle-gates, mutation-gates, run-record.schema, etc.) as JSON.
```

Always inspect command-specific help before scripting an interface:

```sh
agentic-sdlc --help
agentic-sdlc plan --help
```

Task IDs are preserved exactly and must already use only letters, numbers, dots, underscores, and hyphens. The CLI rejects lossy normalization so distinct external IDs cannot share lifecycle state.

Representative invocations are:

```sh
agentic-sdlc detect --root /path/to/target
agentic-sdlc init --root /path/to/target --classification internal
agentic-sdlc plan --root /path/to/target --task-id TEAM-DEMO-001 --task "Define requirements traceability for the order API"
agentic-sdlc validate --root /path/to/target
agentic-sdlc status --root /path/to/target --task-id TEAM-DEMO-001
agentic-sdlc approve-from-github --root /path/to/target --task-id TEAM-DEMO-001 --gate G2 --role product_owner --repo example/service --pr 42 --review-id 314159 --reviewer-login octocat --commit-sha 0123abcd
agentic-sdlc approve-from-github-pr --root /path/to/target --task-id TEAM-DEMO-001 --gate G2 --role product_owner --repo example/service --pr 42 --commit-sha 0123abcd
agentic-sdlc invalidate --root /path/to/target --task-id TEAM-DEMO-001 --earliest-gate G2 --reason "Approved intent changed" --actor "product-owner"
```

Projects that want GitHub PR reviews to be the authoritative human-approval source can opt in through `.agentic-sdlc/project.json`:

```json
"approval_sources": {
  "human_gate_default": "github-review",
  "allow_manual_fallback": false
}
```

When that mode is enabled, approved human gates must carry `github-review:` evidence in the form:

```text
github-review:<owner>/<repo>:pull/<pr>:review/<review-id>:reviewer/<login>
```

Assigned human authorities should also include a GitHub identity binding, either through `github_login` or an assignee in `github.com/<login>` form, so validation can confirm the review author matches the assigned approver.

`approve-from-github-pr` uses the GitHub CLI (`gh api repos/<owner>/<repo>/pulls/<pr>/reviews`) to fetch reviews, select the latest matching `APPROVED` review for the authority login, and record it through the same run-record approval path. Supply `--commit-sha` when you need the review tied to an exact reviewed revision; otherwise the command picks the latest approved review for the matching login. It fails closed if `gh` cannot reach GitHub or if no matching approved review exists.

An analogous GitLab MR approval-evidence adapter is available (`approve-from-gitlab` / `approve-from-gitlab-mr`, opt in via `human_gate_default: "gitlab-mr"`), for projects whose authoritative human-approval source is a GitLab merge request rather than a GitHub PR review. It has the same trust level as the GitHub adapter above — a trusted API attestation read from GitLab's own approval state, not independent non-repudiation or signing — and persists only the approver's pseudonymous GitLab username, never their name, email, or avatar. Only `gitlab.com/<username>` identities are recognized by convention; a self-hosted GitLab instance requires an explicit `gitlab_username` authority field. Because GitLab's approvals API exposes MR-level rather than per-approver timestamps and reviewed-commit values, `decided_at` and `commit_sha` in the resulting evidence are MR-level approximations, not exact per-approver facts, and `--commit-sha` filtering correctness depends on the GitLab project having "reset approvals on push" enabled.

### Linking a GitLab issue as an intent/requirements source

`link-intent-from-gitlab-issue` and `link-requirements-from-gitlab-issue` record where a task's G1 Intent or G2 Requirements Baseline actually came from, by fetching and validating a real GitLab issue rather than accepting a free-text label. This is a deliberately new capability, not a "speculative" one the way the GitLab MR approval adapter above is — it fills a gap that existed for every task until now: `intent_record_id`/`requirements_baseline_id` are run-record fields that have always existed in the schema but, before this, nothing ever set them.

```sh
agentic-sdlc link-intent-from-gitlab-issue --root /path/to/target --task-id TEAM-DEMO-001 --role product_owner --project-path group/project --issue-iid 42
agentic-sdlc link-requirements-from-gitlab-issue --root /path/to/target --task-id TEAM-DEMO-001 --role engineering_lead --project-path group/project --issue-iid 42
```

Each command fetches the issue via `glab api projects/<project>/issues/<iid>`, records it as gate-level evidence in the form `gitlab-issue:<project-path>:issues/<iid>`, and sets the corresponding run-record field to that URI. Re-linking replaces the gate's prior source-link evidence rather than accumulating it — including when the new link points at a different issue than the one previously linked, so the gate always carries at most one source-link entry, matching `intent_record_id`/`requirements_baseline_id`'s single-URI semantics. `invalidate`/`reenter` on G1/G2 clear the linked source (both the gate evidence and the run-record field) along with the rest of the gate's contribution, since a re-baselined gate no longer has a settled source.

**This is deliberately not approval evidence.** Linking a GitLab issue never marks G1/G2 approved, and gate approval (`approve-from-github`/`approve-from-gitlab*` above, or the LangGraph engine's `human_approval_{gate}` interrupt) is completely unaffected by whether a source is linked — the two are orthogonal by design. Authorization still requires the caller's `--role` to be an assigned, applicable authority for the target gate (the same discipline the approval adapters use), so only accountable humans can attach a source, but attaching one is not itself a sign-off.

Unlike the approval adapters, no per-person identity is ever fetched or persisted here — an issue link has no "approver" concept, so there is nothing to data-minimize away. Only the issue's `iid`, `title`, `state`, and `web_url` are used.

### Linking a GitHub issue as an intent/requirements source

`link-intent-from-github-issue` and `link-requirements-from-github-issue` are the GitHub counterpart to the GitLab issue-linking commands above, with identical behavior and the same `intent_record_id`/`requirements_baseline_id` run-record fields — only the forge, URI scheme, and flags differ.

```sh
agentic-sdlc link-intent-from-github-issue --root /path/to/target --task-id TEAM-DEMO-001 --role product_owner --repo owner/project --issue-number 42
agentic-sdlc link-requirements-from-github-issue --root /path/to/target --task-id TEAM-DEMO-001 --role engineering_lead --repo owner/project --issue-number 42
```

Each command fetches the issue via `gh api repos/<owner>/<repo>/issues/<issue-number>`, records it as gate-level evidence in the form `github-issue:<owner>/<repo>:issues/<issue-number>`, and sets the corresponding run-record field to that URI. The same replace-on-relink, `invalidate`/`reenter` clearing, and "deliberately not approval evidence" properties described above apply unchanged — linking a GitHub issue never marks G1/G2 approved, and gate approval is unaffected by whether a source is linked. As with the GitLab adapter, no per-person identity is ever fetched or persisted; only the issue's number, `title`, `state`, and `html_url` (as `web_url`) are used.

### Publishing GitLab gate/approval tracking issues

`create-gate-issues` backs a task's lifecycle gates with real GitLab issues, idempotently: one **gate tracking issue** per applicable, in-sequence lifecycle gate, plus one **approval issue** per applicable `authority_requirements[]` entry, assigned to the resolved GitLab username (`authority_gitlab_username()` — same identity binding `approve-from-gitlab-mr` uses). GitLab itself (queried by a deterministic, non-sensitive label pair) is the source of truth for "does this issue already exist"; a local sidecar ledger (`.agentic-sdlc/runs/<task-id>/gate-issues-gitlab.json`) is diagnostics only and is never trusted over a fresh label search. This is strictly outbound-only and orthogonal to the approval adapters above: it never writes `human_approvals`, `gate.status`, `evidence_refs`, or `disposition` — closing a tracking issue on GitLab is never approval evidence.

Each approval issue's description carries a module-emitted `> parent <group/project>#<gate_iid>` cross-reference line, which GitLab auto-renders as a working bidirectional link with no API-tier requirement. `--link-type relates_to` additionally calls the GitLab Issue Links API as an opt-in enhancement; if that API is unavailable on the target instance, the whole run aborts (never silently falls back to the description-only link).

The command is two-phase, mirroring the plan-digest pattern used elsewhere in this kernel: a `--dry-run` (the default) computes and prints a `plan_digest` with no GitLab calls; `--apply` requires that exact `--plan-digest` (recomputed and re-checked before every issue, so a concurrent `authorities.json`/run-record change aborts cleanly rather than silently changing behavior mid-run). An authority requirement that cannot be resolved to a real, non-self-approving GitLab assignee (missing authority, unassigned, no GitLab username binding, username resolves to zero or multiple active GitLab users, or the resolved assignee is a preparer/independent verifier of that gate) is refused individually — the run continues creating everything else it can, but exits 2 and reports every refusal by reason code. GitLab assignee drift on a reused approval issue is reported (exit 2), never silently overwritten, unless `--reconcile-assignees` is passed.

```sh
agentic-sdlc create-gate-issues --root /path/to/target --task-id TEAM-DEMO-001 --project-path group/project --as-bot svc-agentic-sdlc --allow-classification internal --dry-run
agentic-sdlc create-gate-issues --root /path/to/target --task-id TEAM-DEMO-001 --project-path group/project --as-bot svc-agentic-sdlc --allow-classification internal --apply --plan-digest sha256:...
agentic-sdlc list-gate-issues --root /path/to/target --task-id TEAM-DEMO-001
```

### Publishing GitHub gate/approval tracking issues

`create-github-gate-issues` / `list-github-gate-issues` are the GitHub mirror of `create-gate-issues` / `list-gate-issues` above — a **separate command pair**, not a `--forge github` flag on the GitLab command, since the two forges' idempotency mechanism, verification fields, and refusal-code sets differ enough that sharing one CLI surface would blur those differences rather than make them explicit. Same two-level granularity (one gate tracking issue per applicable lifecycle gate, one approval issue per applicable `authority_requirements[]` entry, assigned to the resolved GitHub login via `authority_github_login()` — the same identity binding `approve-from-github-pr` uses), same forge-is-the-source-of-truth idempotency philosophy, same two-phase `--dry-run`/`--apply`/`--plan-digest` handshake, and the same orthogonality guarantee (never writes `human_approvals`, `gate.status`, `evidence_refs`, or `disposition`).

**No live GitHub API verification session was available while building this feature** (no scratch-repo credentials). The exact existence-query shape (`state=all` accepted together with `labels=`), whether GitHub auto-creates a label on issue-create, and the secondary-rate-limit stderr signature are implemented as documented, fail-closed assumptions, not independently verified facts — see `agentic_sdlc/gate_issues_github.py` and `agentic_sdlc/github_issue_write.py`'s module docstrings for the full writeup before relying on this command against a real GitHub repository.

Existence is determined by querying GitHub's issue-list endpoint for the marker label **alone** (`GET /repos/<owner>/<repo>/issues?labels=<marker>&state=all&per_page=20`) — never GitHub's full-text issue-search endpoint, and never a two-label pair the way the GitLab command queries. A returned entry carrying a `pull_request` key blocks the run (GitHub's issue-list endpoint returns pull requests too); label comparison is case-insensitive throughout (GitHub label names are unique case-insensitively, unlike GitLab's exact-match comparison); and a full page of exactly 20 matches blocks rather than paginating.

This command adds a repository pre-flight beyond GitLab parity: it fetches `GET /repos/<owner>/<repo>` before any write and refuses to proceed if `has_issues` is false, or if the repository is public and `--allow-public-repo` was not passed — GitHub issues have no per-issue "confidential" flag the way GitLab does, so this repository-level check is this command's data-minimization substitute. There is no `--link-type` flag at all (GitHub has no separate Issue Links API); every approval issue's description carries a `> parent <owner>/<repo>#<gate_issue_number>` cross-reference line, which GitHub auto-renders as a live link, and that is the only linkage in v1.

Because GitHub can silently drop an assignment to a non-collaborator rather than reject it, authority resolution is two-layered: a pre-check (`github-user-unresolved` / `not-a-collaborator` refusal codes) before creating an approval issue, plus a post-create/post-`PATCH` re-fetch-and-verify step that blocks (never just reports) if the assignment did not actually take. Assignee drift on a reused approval issue is reported (exit 2), never silently overwritten, unless `--reconcile-assignees` is passed — and a `--reconcile-assignees` `PATCH` that itself silently drops the assignee still blocks rather than reporting success.

```sh
agentic-sdlc create-github-gate-issues --root /path/to/target --task-id TEAM-DEMO-001 --repo owner/project --as-bot svc-agentic-sdlc --allow-classification internal --dry-run
agentic-sdlc create-github-gate-issues --root /path/to/target --task-id TEAM-DEMO-001 --repo owner/project --as-bot svc-agentic-sdlc --allow-classification internal --allow-public-repo --apply --plan-digest sha256:...
agentic-sdlc list-github-gate-issues --root /path/to/target --task-id TEAM-DEMO-001
```

The local sidecar ledger (`.agentic-sdlc/runs/<task-id>/gate-issues-github.json`) is diagnostics only, exactly like the GitLab command's ledger — a fresh label search is always the source of truth for "does this issue already exist," never the ledger.

### Publishing a one-way gate-status summary comment

`publish-gate-status` posts (and idempotently updates in place on re-run) a single **read-only, diagnostics-only** comment summarizing all ten lifecycle gates' status on a task's GitHub PR or GitLab MR, so reviewers can see gate state without leaving the PR/MR. **This is never approval evidence and is never read back**: the rendered comment says so explicitly, `agentic-sdlc` never reads the comment, its reactions, or its replies into gate state, and there is no adapter that could turn a reaction or reply into an approval. Gate approval remains exclusively `agentic-sdlc decide` / `approve-from-gitlab-mr` / `approve-from-github-pr`, against an external approval record.

Content comes from exactly two sources: a pure, read-only projection of the run record (`gate_status_projection()` — the same function `status` uses for its own printed summary, refactored out specifically so this command can share it without ever writing `run-record.json`) and the bundled lifecycle-gate contract (gate names, `human_only`). `authorities.json` is never opened, `evidence_refs`/`applicability_rationale`/`scope`/`findings`/`specialist_attestations`/`disposition` and any human identity field are never read, and `re_entry_history` is reduced to a count plus the earliest re-entered gate id only — no free-text or identity field from the run record can ever reach the rendered comment.

`--forge {github,gitlab}` is required and never inferred; supply `--repo`/`--pr` for GitHub or `--project-path`/`--mr-iid` for GitLab (the wrong pair for the selected forge fails immediately). Matching an existing comment is by a domain-separated marker embedded in an HTML comment (`<!-- agentic-sdlc:gate-status:v1:<marker> -->`, matched on the marker only, never the version segment, so a future template version still finds and updates today's comment instead of duplicating it) — up to 1,000 comments/notes are scanned; exceeding that cap fails closed (exit 2) rather than risk missing a match on a later page. Zero matches creates a new comment; one match authored by the verified `--as-bot` identity updates it in place (or reports `unchanged` with no write at all if the freshly rendered body matches the existing comment once the live `rendered {timestamp}` token is excluded from the comparison — that token changes on every invocation by design and must never by itself force an `update`); more than one match, or a match authored by anyone else, is refused (exit 2) rather than silently reused or overwritten. Every create/update is re-fetched and verified (id, author, byte-identical body — this post-write check is an exact comparison, unlike the unchanged/update classification above) before being trusted; a mismatch is recorded as `suspect` in the local sidecar ledger and blocks (exit 2).

`--dry-run` (the default) always lists existing comments and verifies `--as-bot` identity — both read-only forge calls needed to report an accurate `create`/`update`/`unchanged`/`blocked` action — but never writes; only `--apply` can actually create or update the comment. Unlike `create-gate-issues`, there is no `--plan-digest` two-phase handshake: re-running to keep the comment current is the intended workflow, and there is no identity/assignment confirmation that would need binding across dry-run and apply.

```sh
agentic-sdlc publish-gate-status --root /path/to/target --task-id TEAM-DEMO-001 --forge github --repo owner/project --pr 42 --as-bot svc-agentic-sdlc --allow-classification internal --dry-run
agentic-sdlc publish-gate-status --root /path/to/target --task-id TEAM-DEMO-001 --forge github --repo owner/project --pr 42 --as-bot svc-agentic-sdlc --allow-classification internal --apply
agentic-sdlc publish-gate-status --root /path/to/target --task-id TEAM-DEMO-001 --forge gitlab --project-path group/project --mr-iid 7 --as-bot svc-agentic-sdlc --allow-classification internal --apply
agentic-sdlc list-gate-status --root /path/to/target --task-id TEAM-DEMO-001
```

The local sidecar ledger (`.agentic-sdlc/runs/<task-id>/gate-status-<forge>.json`) is diagnostics only, exactly like `create-gate-issues`'s ledger — a fresh scan of the PR/MR's live comments for the marker is always the source of truth for "does this comment already exist," never the ledger. `list-gate-status` takes no `--forge` flag and reports both forges' ledgers (empty for a forge never published to), since a task's lifecycle could in principle be tracked on either or both over its life.

### Reporting GitHub PR reviewer candidates (read-only / reporting only in this version)

`request-gate-reviewers` reports which GitHub logins *would* be requested as PR reviewers for a task's lifecycle gates, derived from each eligible gate's `authority_requirements[]` and `authorities.json`, and classifies each candidate against the PR's live state: already requested, already reviewed (at the current head commit), review is stale (approved an older commit), or still to-request. **This version never posts a review request. There is no `--apply` flag and no write call anywhere in this code path.** Requesting PR reviewers requires a GitHub token with `Pull requests: write` scope, which has no narrower equivalent and also permits editing/closing PRs and changing labels — introducing that write capability is a real permission-escalation decision that needs explicit human sign-off before it is built, not something inferred from this feature's name. This read-only version needs no GitHub permissions beyond what the kernel already used for `approve-from-github-pr` (read-only PR/reviews access).

Multiple `(gate, authority)` pairs that resolve to the same GitHub login collapse into one reviewer entry carrying every motivating gate/authority/role. Because a GitHub review request is PR-wide (not gate-scoped), if *any* in-scope pair refuses a login for an independence reason (`self-approval`, `pr-author-conflict` — the login is the PR's author, or `actor-is-reviewer` — the login is the verified `--as-bot` identity), that login is withheld from every one of its motivations, not just the conflicting one, and reported as `withheld-conflict` with a pointer to the gate that caused it. Resolution failures (no GitHub login bound, the login doesn't resolve to a real GitHub user, or the login isn't a repository collaborator) do not have this PR-wide poisoning effect.

```sh
agentic-sdlc request-gate-reviewers --root /path/to/target --task-id TEAM-DEMO-001 --repo owner/project --pr 42 --as-bot svc-agentic-sdlc --allow-classification internal
```

There is no `list-gate-reviewer-requests` companion command and no local ledger: unlike `create-gate-issues`, this command performs no action, so nothing needs to be remembered between runs — a fresh invocation is always at least as accurate as any cached report would be, since GitHub's review/requested-reviewer state can change between runs.

### Reporting GitLab MR reviewer candidates (read-only / reporting only)

`request-gate-reviewers-gitlab` is the GitLab counterpart of `request-gate-reviewers` above, reporting which GitLab usernames *would* be set as MR reviewers (GitLab's lightweight, per-MR `reviewers`/`reviewer_ids` field — the direct structural analog of GitHub's requested-reviewers, not GitLab's separate quorum-based approval-rules mechanism) for a task's lifecycle gates. It is a **separate command, not a `--forge` flag on `request-gate-reviewers`**, because the two forges' classification vocabularies are structurally different, not just relabeled: GitLab adds a resolution-ambiguity case GitHub cannot produce (`gitlab-user-ambiguous` — GitLab's `GET /users?username=` is search-based, unlike GitHub's exact-match `GET /users/{login}`), has no repository-collaborator-equivalent check (no `not-a-collaborator`), and — see below — has no `review-stale` equivalent at all. Eligibility, self-approval/independence, and the PR-wide/MR-wide poisoning rule are exactly the same policy as the GitHub version and are shared code, not a reimplementation.

**This version never sets `reviewer_ids`. There is no `--apply` flag and no write call anywhere in this code path.** Setting MR reviewers requires a GitLab token with API write scope — the same permission-escalation reasoning as the GitHub version applies. This read-only version needs no GitLab permissions beyond what the kernel already used for `approve-from-gitlab-mr` (read-only MR/approvals access) plus the existing `resolve_gitlab_user_id` username lookup `create-gate-issues` already uses.

```sh
agentic-sdlc request-gate-reviewers-gitlab --root /path/to/target --task-id TEAM-DEMO-001 --project-path group/project --mr-iid 7 --as-bot svc-agentic-sdlc --allow-classification internal
```

### Publishing an advisory reviewer nudge (GitHub only)

`publish-reviewer-nudge` posts (and idempotently updates in place on re-run) a comment on a task's GitHub PR *suggesting* who a human might ask to review it, reusing `request-gate-reviewers`'s classification directly (`gate_reviewers.run()`, which itself calls `gate_reviewers.build_plan()` — this command never reimplements eligibility, self-approval/independence, or motivation aggregation). It exists specifically because actually requesting PR reviewers needs a GitHub token with `Pull requests: write` scope, which has no narrower equivalent and also permits editing/closing PRs and changing labels — a real permission escalation that has not been signed off. Instead, this command reuses `publish-gate-status`'s already-approved, already-in-use comment-write capability (`Issues: write` scope) to post a suggestion.

**This is not a review request and nobody is notified.** The rendered comment states this explicitly and unambiguously, and the mechanics back the claim up: suggested logins are written as plain code spans (`` `login` ``), never as GitHub `@`-mentions, specifically so that posting or updating the comment never itself triggers a GitHub notification to anyone. `agentic-sdlc` never reads this comment, its reactions, or its replies back into gate state, exactly like `publish-gate-status`'s comment.

Only logins classified `to-request` or `review-stale` are named, each with the gate(s)/authority type that motivate suggesting them. `already-requested` and `already-reviewed` logins are omitted entirely (nothing to nudge about), as are unresolved logins (`github-user-unresolved`, `not-a-collaborator`). Logins classified `withheld-conflict` (self-approval, PR-author conflict, or the acting bot itself) are **never named in the posted comment** — only a count ("N additional reviewer(s) not shown due to a gate-independence conflict — see the full report locally"), since naming a specific person as conflicted in a public PR comment would be a data-exposure regression from how `request-gate-reviewers`'s own report already keeps that reasoning local-only.

Comment matching, create/update-in-place, the page cap, and the create/update/unchanged/blocked classification all reuse `publish-gate-status`'s machinery directly (`gate_status.GithubForgeAdapter`, `gate_status.classify()`) under this command's own domain-separated marker (`<!-- agentic-sdlc:reviewer-nudge:v1:<marker> -->`) — a distinct comment from any `publish-gate-status` comment on the same PR. `--dry-run` (the default) never writes; only `--apply` can create or update the comment, and every create/update is re-fetched and verified before being trusted, exactly like `publish-gate-status`.

```sh
agentic-sdlc publish-reviewer-nudge --root /path/to/target --task-id TEAM-DEMO-001 --repo owner/project --pr 42 --as-bot svc-agentic-sdlc --allow-classification internal --dry-run
agentic-sdlc publish-reviewer-nudge --root /path/to/target --task-id TEAM-DEMO-001 --repo owner/project --pr 42 --as-bot svc-agentic-sdlc --allow-classification internal --apply
agentic-sdlc list-reviewer-nudge --root /path/to/target --task-id TEAM-DEMO-001
```

The local sidecar ledger (`.agentic-sdlc/runs/<task-id>/reviewer-nudge-github.json`) is diagnostics only, exactly like `publish-gate-status`'s — a fresh scan of the PR's live comments for the marker is always the source of truth for "does this comment already exist," never the ledger. There is no GitLab equivalent of this command yet.

**Known, permanent gap: no `review-stale` classification.** GitHub's per-review `commit_id` lets a review be matched against the PR's exact head SHA, distinguishing a review at HEAD from one against an older commit. GitLab's MR-approvals endpoint (`GET .../merge_requests/:iid/approvals`) has no per-approver commit field at all — its one `sha` field is the *MR's* current diff-head SHA, applied uniformly to every entry in `approved_by`. Faking staleness by comparing that MR-level `sha` to the MR's head would misrepresent every approver identically rather than per-approver, which is worse than no classification at all because it would look precise without being so. This command therefore has exactly one "has approved" classification, `already-approved` (with no staleness qualifier), never `review-stale`; whether a given approval genuinely reflects the current head depends on the GitLab project having "reset approvals on push" enabled, which this command cannot observe from the API response. `mr_head_sha` is still included in the report purely so a human reader can manually cross-check currency.

There is no `list-gate-reviewer-requests-gitlab` companion command and no local ledger, for the same reason as the GitHub version: this command performs no action, so a fresh invocation is always at least as accurate as any cached report.

`validate` exits with `0` when valid and ready, `2` when structurally valid but blocked by unresolved decisions, and `1` for errors. Treat both `1` and `2` as non-ready in CI.

Initialization, detection, planning, status, and invalidation work with Python 3.10+ and the standard library. Install the pinned validation dependencies before using `validate`; validation fails closed when they are absent. Enable complete Draft
2020-12 structural validation in CI or assurance environments with:

```sh
python3 -m pip install -r kernel/requirements-validation.txt
```

This kernel CLI covers bootstrapping and bookkeeping (`init`/`detect`/`validate`/`status`/`invalidate`/`approve-from-github*`). For actually dispatching and driving a task through the G1–G10 lifecycle — author/reviewer dispatch, human/mutation-gate interrupts, invalidation with real re-execution — use the LangGraph engine's `agentic-sdlc-lg` CLI or service in [`../engine/agentic_sdlc_langgraph/`](../engine/agentic_sdlc_langgraph).

## Team demonstration

Use a synthetic or non-production repository for the first demonstration:

1. Initialize the repository (`agentic-sdlc init`).
2. Show the generated unknown/unassigned values and explain why they fail closed.
3. Use `detect` to review observable stack and command candidates.
4. Use `plan` for an intent-and-requirements task and inspect the selected workflow, agents, `required_quality_gates`, and separate `human_gates`.
5. From `../engine/agentic_sdlc_langgraph/`, run `agentic-sdlc-lg plan`/`resume` against the same task and show it suspending at each gate's human-approval interrupt and at any matched mutation-gate phrase, with author/reviewer separation enforced structurally rather than by convention.
6. Validate and display the exported run record (`agentic-sdlc-lg export` / `validate`).
7. Change a material upstream assumption and demonstrate downstream invalidation (`agentic-sdlc-lg invalidate` then `reenter`) without granting a new approval.

## Upgrades and version lock

The generated overlay records both the kernel and plugin versions it was created against. Treat that lock as a compatibility declaration, not as proof that the project has adopted a newer lifecycle.

For an upgrade:

1. Update the plugin package to the new kernel version.
2. Review release and schema changes before changing the project lock.
3. Run `detect` and `validate` against the existing overlay and records.
4. Review any generated overlay differences; do not overwrite local authority or applicability decisions without an accountable owner.
5. Migrate incompatible records explicitly, rerun validation, and commit the lock change with the reviewed overlay changes.

Use `repair` only for missing baseline artifacts and stale lock metadata. It is not a schema migration tool and never rewrites an existing project decision or lifecycle record.

Keep lifecycle state in version control according to the project's evidence-classification and retention rules. Do not commit secrets or raw approval credentials.

## Current limitations

- The development CLI requires Python 3.10 or newer; standalone executables are not bundled.
- Detection is advisory and inspects repository-root signatures rather than deeply evaluating every component. Candidate commands are not automatically trusted or executed.
- It cannot identify human authorities, legal obligations, risk acceptance, evidence-retention policy, or production authorization.
- The portable validator fails closed unless `requirements-validation.txt` is installed. With it, validation enforces lifecycle safety semantics and exhaustive Draft 2020-12 structural and format validation against the bundled schemas; CI enables this mode.
- The plugin prepares and validates decision records but does not authenticate an approver's real-world identity; projects must reference evidence from their authoritative approval system.
- The GitLab approval-evidence adapter is speculative: this kernel's own default provider profile uses GitHub PR reviews for approvals (GitLab is only its CI/CD platform). The GitLab adapter is fully wired and callable, but is not currently the approval source any bundled provider or profile actually selects.
- It does not deploy, apply infrastructure, run persistent migrations, accept risk, merge, or approve gates.
- Project-specific agent wrappers, knowledge-store integrations, CI wiring, and organization-specific impact extensions may require an overlay customization.
- Specialized SQS/BOM semantics remain unavailable until an authorized owner supplies definitions and applicability.

Use `show-contract lifecycle-gates` for the normative lifecycle contract. Provider-specific operating guidance belongs to the provider package.
