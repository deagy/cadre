# Security controls register

This is the repository-wide register of security controls: every control in
this suite's own code that maps to a threat-model expectation or an
`agent-autonomy.yaml` guarantee, with a plain statement of how strongly each
one is actually enforced and against whom.

**Provenance of this file.** It began as `roster/orchestration/mcp/SECURITY-CONTROLS.md`,
covering the MCP dispatch server alone, and was promoted to this path in the
[#234](https://github.com/deagy/cadre/issues/234) follow-up, when its first
non-MCP entry landed (the role-fidelity model-validation notice below). A
register claiming repository-wide scope while living in `mcp/` misrepresents
itself, which is the thing this file exists not to do.
The promotion is a reframing, not a rewrite: every
control entry that existed before the move is preserved below with its
enforcement classification unchanged, now under a heading that scopes it to
the path it actually covers. Nothing was reclassified, weakened, or dropped
by the move. Historical records under `roster/orchestration/runs/` and
already-shipped `CHANGELOG.md` entries still cite the old path; that is
correct for a point-in-time record and must not be edited to match today's
layout.

## Enforcement classifications

Every entry states which of these two it is. The vocabulary is fixed --
do not introduce a third informal tier in prose, and do not describe an
advisory control with enforcement language:

- **Mechanically enforced** -- the code makes the bad outcome structurally
  impossible for a caller to produce through that control's own interface,
  and a specific automated test exercises that claim. For the MCP entries
  that test suite is `internal/orchestration/dispatch_core_phase4_mcp_server_test.go`; each
  entry names the tests it relies on.
- **Advisory** -- the code implements the control and it holds against an
  ordinary, non-adversarial, or single-turn caller, but a sufficiently
  adversarial or fully autonomous caller (in particular: a compromised child
  process with its own code-execution authority, or a host CLI/calling
  session willing to script around a human-confirmation step) could route
  around it. Advisory controls are still worth having -- they raise the bar
  and produce an audit trail -- but must never be described or relied upon as
  hard guarantees.

A control that only *informs* an operator and never blocks anything is
advisory in this vocabulary too. It is not a weaker third category and it
does not get softer language: if an operator who ignores it proceeds
unimpeded, the entry says so in its first line.

**Accuracy discipline.** Each entry is written against the code on disk at
the time it was written, and cites the functions and tests it depends on. If
that code changes, the entry must be re-checked against it, not regenerated
from memory or intent.

## Scope: the dispatch paths, and which controls reach them

Four distinct paths can cause a role in this suite to run. They share role
*content* -- `roster/<phase>/<role>/AGENT.md` and the wrappers generated from
it -- and they share almost no *enforcement*. A control's reach is the path
it was built for, never "this suite" in general, and this register states
that reach per entry rather than leaving it to be inferred.

| Dispatch path | What it is | Where the child's model comes from | Entries in this register that reach it |
|---|---|---|---|
| **MCP servers, CLI runners** (`internal/orchestration/`) | `dispatch_secure_cloud_role` / `dispatch_team` / `dispatch_team_recipe` spawning a `codex exec` or `claude` child, plus the separate create-only GitLab evidence server | the generated wrapper resolved by `ResolveRoleFileCodex()` / `ResolveClaudeCodeRoleFile()` -- never caller-supplied, *unless* the operator configured a local provider (see "Self-hosted model providers") | every MCP entry below |
| **MCP servers, api runner** (`internal/orchestration/api_runner_*.go`) | the same three tools with `runner="api"`, driving an OpenAI-compatible chat endpoint over HTTP with **no child process at all** | operator settings only (`runners.api_base_url` + `runners.local_model_<tier>`). The wrapper's own `model` field is deliberately discarded -- it names a vendor model the endpoint has never heard of -- so the wrapper decides *which role* runs but not *which model* serves it | every MCP entry below, **except** the four named as not reaching it in "API runner" |
| **`cline-agents` plugin** (`cline-plugins/cline-agents/`) | `start_subagent` / `dispatch_selected_roles` starting a subagent inside a Cline session | operator environment: `CLINE_AGENTS_PROVIDER_ID` plus `CLINE_AGENTS_MODEL_<TIER>` / `CLINE_AGENTS_MODEL_DEFAULT`, or a per-call override | the role-fidelity model-validation notice, and nothing else |
| **Claude Code plugin subagents** (`plugin/agents/*.md`) | an operator invoking a packaged subagent directly in a Claude Code session, with no MCP server in the loop | the wrapper's own `model:` frontmatter tier, resolved by Claude Code itself | none |
| **Manual / direct invocation** | a human or agent pasting a role brief into any runner, or invoking `codex`/`claude`/an API by hand | whatever the operator chose | none |

Two disambiguations this table depends on, both of which have caused
confusion already:

- **"Claude Code" names two different things here.** The "Claude Code runner
  (`runner="claude-code"`)" entry below is about the *MCP dispatch server*
  spawning a `claude` child process; it is an MCP-path control and
  every other MCP control applies to it. That is a different thing from the
  third row above, where an operator uses the packaged Claude Code subagents
  directly and no code in this repository is in the execution path at all.
- **"No entries reach it" is a statement of fact, not an assessment that the
  path is safe.** The third and fourth rows are uncovered because nothing in
  this repository sits between the operator and the model on those paths, so
  there is no place a control could be implemented without introducing one.
  Anything relying on a guarantee for those paths must obtain it elsewhere
  (host-runner configuration, operator process, or a control not yet built),
  and must not cite this register as the source.

## Role-fidelity model-validation notice (`cline-agents` dispatch path)

**Advisory, and specifically a notice rather than a gate: dispatch always
proceeds, and an operator who ignores it is not stopped by anything in this
repository.** Classifying it any higher would misstate what it does.

**What it is.** `resolveProviderAndModel()` in
`cline-plugins/cline-agents/index.ts` checks whether a role-fidelity
attestation exists for the exact model string it just resolved, and when none
does, `warnMissingRoleFidelityAttestation()` prints a single stderr notice
naming the model and the command that would measure it. It fires once per
model string per plugin-process session, not once per dispatched role,
because a per-call warning across a wave of ten-plus roles on one model is
noise an operator learns to filter permanently. It never throws and never
alters what `start_subagent` / `dispatch_selected_roles` does.

**Why it exists.** Measurement on this suite's own role briefs
(`roster/orchestration/runs/cadre-cline-local-model-fidelity-2026-08-10/fidelity-baseline.md`)
found a weakly-steered model producing fluent, confident, well-formatted
violations of role-scope discipline -- authorship/approval separation, the
one invariant this suite is least able to tolerate losing -- with nothing
erroring anywhere: a 70B model scored 0/9 on the `stays-in-remit` probe where
a 27B model of a different family scored 9/9 on the same probes. The failure
is silent by construction, which is why the response is a notice at all
rather than nothing.

**Reach -- state this wherever the notice is described, not only here:**

- Covers the `cline-agents` dispatch path only. `start_subagent` and
  `dispatch_selected_roles` (both via `startPresetSubagent()`) and
  `list_agent_presets` all resolve through the same function, so a listing
  shows the notice too rather than disagreeing with what would actually run.
- Does **not** cover the MCP path. Those servers take the child's model from
  the generated Codex/Claude wrapper resolved server-side, not from this
  resolver or from operator environment, so this control is not in that code
  path at all.
- Does **not** cover manual injection of a model string outside this
  plugin's own tools, or the Claude Code plugin-subagent path.

**Suppression is an attestation, not a flag -- and the mechanical part of
that claim is narrower than it sounds.** Mechanically enforced: the notice
cannot be silenced by a truthy scalar.
`CLINE_AGENTS_ROLE_FIDELITY_ATTESTATIONS` must parse as a JSON object
carrying an *own* key (`Object.hasOwn`, so a model literally named
`constructor` does not read as attested) exactly equal to the resolved model
string, with a non-null value; a bare `=1`, an array, or malformed JSON all
fail toward showing the notice rather than toward silence. On the writing
side, `write_attestation()` in `internal/orchestration/role_fidelity.go`
(reached as `cadre role-fidelity --mode probe ... --attest-file <path>`)
refuses to write from a `--dry-run` (nothing was sent), from a
`--compare-condensed` run (no single per-model result exists), or from a run
with zero scored results (nothing was measured), and merges rather than
clobbering so one model's re-run never erases another's record.

**Honest limit, and it is the important one:** the consumer checks only that
*some* record exists for that exact model string. It does not read,
threshold, or provenance-check the record's contents -- deliberately, because
the question the notice asks is "was this model measured", not "did it pass"
(a low score is the operator's call with the transcript in hand), and because
a threshold enforced here would move a human judgement somewhere the record
cannot show it happened. The consequence is that an operator can hand-write
`{"<model>": {}}` and suppress the notice without ever running a probe.
The barrier this raises is that they must name the exact model string and
write a structured record, not that anything verified the record came from a
real measurement. Do not describe attestation as proof that a model was
measured.

**Deferred gate: recorded as deferred, not rejected.** A blocking gate --
refusing dispatch on an unattested model -- was considered and deliberately
not built in this increment.

- **Decision and authority.** Warn now, non-enforcing attestation, gate
  deferred. Decided by the accountable human control owner, recorded
  2026-08-10 in the [#234](https://github.com/deagy/cadre/issues/234)
  follow-up.
  This register's author has no authority to promote it; doing so is a new
  decision by that owner, not a follow-up an agent may take on its own
  judgement.
- **Rationale.** The measured failure is real but its blast radius depends
  entirely on what the dispatched role is allowed to do, and a gate that
  refuses dispatch on a model an operator has legitimate reason to run --
  including the read-only, low-stakes majority of dispatches -- would be
  routed around by disabling it, leaving less signal than a notice nobody
  had reason to suppress.
- **Named trigger to revisit.** Evidence of unvalidated models on
  write-capable dispatch: an unattested model resolving for a dispatch whose
  effective sandbox is not read-only. That is the condition under which the
  notice's advisory classification stops being adequate, and it is the
  trigger to re-open the gate decision with the accountable owner rather
  than to widen this entry's language.
- **Open, and blocking nothing today:** nothing currently detects that
  trigger automatically, though the ingredients exist. `resolveToolPolicyConfig()`
  already computes exactly the write-capability signal the trigger needs -- a
  preset whose `allowedTools` contains none of `WRITE_OR_EXEC_TOOL_NAMES`
  (`run_commands`, `editor`, `apply_patch`) is treated as genuinely read-only
  and additionally gets Cline's `mode: "plan"` guard. What is missing is any
  join between that signal and attestation state: `resolveProviderAndModel()`'s
  declared parameter type omits `allowedTools`, so it cannot read the signal
  without a signature change; nothing correlates the two; and no dispatch
  record is written anywhere that a reviewer could count unattested
  write-capable dispatches from. So the trigger is evaluable in principle and
  unmeasured in practice. Until something measures it, it is an operator and
  reviewer obligation -- do not read its presence here as a control.

## MCP dispatch and evidence servers (`internal/orchestration/`)

Everything in this section enumerates controls in `dispatch_core*.go` and
`dispatch_core_phase4_mcp_server.go` (and, in its own subsection,
`gitlab.go` / `gitlab_mcp.go`). These entries are unchanged in substance from before
this register was promoted to repository-wide scope; they were written to be
accurate as of the code on disk after the H-1 (project-tier git-clean) and
L-1 (`confirmation_required` response shape) fixes. If that code changes,
they must be re-checked against it, not regenerated from memory or intent.

None of these controls reach the `cline-agents`, Claude Code plugin-subagent,
or manual dispatch paths -- see the scope table above.

### Sandbox narrowing for `planning-review-only` mode

**Mechanically enforced.** `compute_effective_sandbox()` has exactly two
inputs: the caller-supplied `mode` (constrained to the two-value `MODES` set)
and the resolved role file's own `sandbox_mode` (never caller-supplied --
the tool schema in
`internal/orchestration/dispatch_core_phase4_mcp_server.go` has no
`developer_instructions` or `sandbox_mode` parameter). When `mode == "planning-review-only"`, the
function unconditionally returns `read-only` regardless of what the file
declares; there is no third parameter through which a caller could request a
wider sandbox. This is asserted directly by
`SandboxNarrowingTests.test_planning_review_only_forces_read_only_regardless_of_file`
and `SandboxNarrowingTests.test_there_is_no_caller_parameter_that_can_widen_sandbox`
(which asserts the function's actual parameter list via `inspect.signature`,
not just its documented behavior), and at the top-level dispatch entry point
by
`TerminalVsFallbackDispatchTests.test_planning_review_only_mode_forces_read_only_even_for_a_write_capable_file`,
which inspects the literal `argv` handed to the child process (not just a
status field) for a role file that declares `sandbox_mode = "danger-full-access"`.

### H-1 remediation: project-tier git-clean check

**Enforced against accidental/single-turn escalation. NOT enforced against a
determined actor.** Before Fix H-1, a caller with ordinary repository write
access could write `.codex/agents/<role_id>.toml` declaring
`sandbox_mode = "danger-full-access"` and arbitrary `developer_instructions`
for an existing catalog `role_id`, then immediately call
`dispatch_secure_cloud_role` with `mode="scoped-repository-edit"` against
it, defeating the "no caller-supplied instructions" and "danger-full-access
never reachable via caller input" guarantees for that one path, all within a
single session turn.

The fix (`_is_project_tier_git_clean()`, called from `resolve_role_file()`
only when `tier == "project"` and `mode == "scoped-repository-edit"`)
requires the project-tier file to be tracked in git with no staged or
unstaged modification relative to `HEAD` (`git status --porcelain -- <path>`
must produce empty output and exit 0) before any of its fields are trusted.
Failure -- dirty, untracked, or git itself unavailable/erroring -- raises
`ProjectTierNotGitCleanError`, a distinct `DispatchDenied` subtype, with a
non-generic message, and the check's boolean outcome is carried through
`ResolvedRole.project_tier_git_clean` into every audit record for that
dispatch (`denied`, `confirmation-required`, and the terminal decision),
so this control's actual behavior is auditable rather than assumed.

Tests: `ProjectTierGitCleanTests` (clean-committed succeeds; dirty rejected;
untracked rejected; planning-review-only is unaffected by this specific
check in both the dirty and untracked case; global-tier and plugin-tier
resolution are unaffected by the project directory's git state; the default
`mode` parameter value preserves pre-H-1 behavior for callers that don't
pass `mode`) and, at the top-level dispatch entry point,
`TerminalVsFallbackDispatchTests.test_untracked_project_tier_file_denies_dispatch_with_a_distinct_reason`,
`test_clean_committed_project_tier_file_dispatches_successfully`,
`test_dirty_project_tier_file_in_planning_review_only_is_not_denied_by_the_git_check`,
`test_audit_record_captures_the_git_clean_check_outcome_on_denial`, and
`test_audit_record_captures_the_git_clean_check_outcome_on_success`.

**Honest limit of this guarantee:** this control prevents a same-session,
single-turn write-then-dispatch escalation by requiring a separate, distinct
git-commit action to happen first -- something outside this tool's own
request/response cycle. It does **not** prevent a determined actor from
locally committing malicious `developer_instructions`/`sandbox_mode` content
without any code review and then dispatching against that commit: a clean
`git status` only proves the file matches some prior commit in the local
repository, not that the commit's content was reviewed, authorized, or safe.
This is risk-reduction against accidental or blindly-automated escalation,
not risk-elimination against an adversary who fully controls the local git
history (e.g. a compromised or malicious session with ordinary `git commit`
access and no external review gate). Global (`~/.codex/agents/`) and plugin
(`provider/codex-agents/`) tiers are intentionally out of
scope for this check: the global tier is only populated through the
provenance-marker-gated `internal/orchestration/sync_codex_agents.go` sync, and the plugin tier
ships with the package -- neither is writable by an ordinary caller through
the same direct "just edit a file in the repo" path the project tier is.

### Dispatch-depth limit (`MAX_DISPATCH_DEPTH = 1`)

**Advisory against an adversarial child.** `current_dispatch_depth()` reads
an integer counter from `SECURE_CLOUD_AGENTS_DISPATCH_DEPTH` in this
process's own environment; `build_child_env()` writes `depth + 1` into the
allowlisted child environment so a child that also happens to run this same
MCP server enforces the cap against itself. This holds for a well-behaved
child that simply inherits and reads the environment variable as intended.
It does **not** hold against a child process with its own code-execution
authority (which is exactly what a write-capable, `workspace-write` or
`danger-full-access` dispatched child has): such a child can unset, forge,
or ignore `SECURE_CLOUD_AGENTS_DISPATCH_DEPTH` entirely, since nothing
outside the child's own process enforces that it actually reads or respects
that variable when it in turn tries to call this same dispatch tool again.
`current_dispatch_depth()`'s fail-closed behavior on an unparseable value
(`DispatchDepthTests.test_unparseable_value_fails_closed_to_the_limit`)
protects against accidental corruption of the counter, not against a
deliberate adversary erasing it. Treat this exactly as the depth-guard
docstring and the prior security review already state: an accident/misuse
guard, not a hard depth bound against a compromised or hostile child.

### Human-confirmation gate for write-capable dispatch (`ConfirmationGate`)

**Advisory against a fully autonomous calling session.** The gate is a
mechanical two-call, single-use, TTL-bound token scheme (see
`ConfirmationGate`'s own docstring, which this section deliberately mirrors
and expands): a first call for a write-capable dispatch never spawns a
child and instead returns `status="confirmation_required"` plus an opaque
token bound to the exact `(role_id, brief, mode, classification,
effective_sandbox)` tuple; only a second call carrying that token, with an
identical parameter tuple, actually spawns the child, and the token is
single-use and expires after `CONFIRMATION_TTL_SECONDS`. This part --
that the first call never spawns a child, that a mismatched or reused token
is rejected -- is mechanically enforced and tested
(`ConfirmationGateTests`, plus
`TerminalVsFallbackDispatchTests.test_write_capable_dispatch_without_confirmation_never_spawns_a_child`
and `test_write_capable_dispatch_requires_confirmation_round_trip`).

What is **not** enforced, and cannot be from inside this tool: whether a
human actually read and approved the intermediate `confirmation_required`
response before the second call happened. A fully autonomous host CLI or
calling session is free to issue both calls back-to-back, itself, with no
human ever seeing the intermediate result -- this tool has no visibility
into, and no control over, the host CLI's own approval-prompt or
user-confirmation behavior around tool invocations. True human-presence
enforcement is entirely a property of the host environment this server runs
inside, not of this module. Treat this gate as a necessary layer that raises
the bar against a single accidental or blindly-scripted write-capable
dispatch, never as a sufficient proof that a human was in the loop.

### Env allowlist for the child process

**Mechanically enforced.** `build_child_env()` only ever copies names
present in the fixed `ENV_ALLOWLIST` tuple out of this server process's own
environment; it never does a blanket `os.environ` inheritance, so an
arbitrary variable in this process's environment (API keys, tokens, other
credentials) cannot reach the dispatched child by default. Tested by
`EnvAllowlistTests.test_only_allowlisted_names_are_copied` and
`test_credential_shaped_variables_never_leak_through`, which poisons the
environment with credential-shaped variable names
(`AWS_SECRET_ACCESS_KEY`, `API_TOKEN`, `GITLAB_TOKEN`, `OPENAI_API_KEY`) and
asserts none of them appear in the resulting child environment.

`CODEX_HOME` was added to `ENV_ALLOWLIST` for the self-hosted-provider work.
It is a directory path, not a credential, and it is required for `codex exec
--profile <name>` to resolve `$CODEX_HOME/<name>.config.toml`. Codex defaults
it to `~/.codex` and `HOME` was already allowlisted, so this matters only to
operators who relocate it.

**`runners.forward_env` -- an opt-in widening, mechanically bounded.** A Codex
`[model_providers.*]` block declaring `env_key = "SOME_VAR"` cannot
authenticate to a self-hosted endpoint unless `SOME_VAR` is present in the
child's environment, so `build_child_env()` also copies the variables named
by `runners.forward_env`. What keeps this from dissolving the control above:

- It is empty by default. An operator who does not opt in gets exactly the
  deny-by-default behavior this entry has always described, and the poisoned-
  environment test above still passes unchanged.
- It accepts **exact names only**. `_validate_env_var_name_list` refuses
  wildcards and prefixes outright rather than ignoring them, so one config
  line cannot widen the environment by an unbounded amount.
- It is `global_only`, so a cloned repository's project-local
  `.agents/cadre.yaml` can never set it.
- It is applied *before* `DEPTH_ENV_VAR`, so forwarding cannot overwrite the
  re-dispatch counter.
- The forwarded **names** are recorded in the audit trail; the values are
  structurally barred from it by `_FORBIDDEN_AUDIT_KEYS`.

Tested by `ForwardEnvTests` (`test_nothing_is_forwarded_by_default`,
`test_an_unnamed_variable_is_not_forwarded_alongside_a_named_one`,
`test_a_wildcard_is_refused_rather_than_matched`,
`test_forwarding_cannot_overwrite_the_dispatch_depth_counter`) and, for the
scope rule, `SelfHostedProviderFieldTests.test_every_new_field_is_global_only`.

Note what this widening honestly is: a deliberate mechanism for handing a
credential to a dispatched child. It is bounded and auditable, not absent.
An operator who forwards a broadly-scoped token has given that token to the
dispatched role, and no control here narrows it afterward.

### Audit-record secret redaction

**Mechanically enforced.** `build_audit_record()` asserts (raises
`AssertionError`, not a silent drop) if any of the fixed `_FORBIDDEN_AUDIT_KEYS`
(`developer_instructions`, `brief`, `prompt`, `output`, `stdout`, `stderr`,
`stdout_text`, `environment`, `env`, `child_env`, `credentials`, `auth`,
`token`, `confirmation_token`) are present in the fields it's asked to
record, so a future code change that accidentally tries to log one of these
fails loudly at record-construction time rather than silently leaking into
the on-disk JSON-lines audit log. Tested directly by
`AuditRecordTests.test_forbidden_keys_raise` (parameterized over every
forbidden key) and, at the top-level dispatch entry point, by
`TerminalVsFallbackDispatchTests.test_audit_records_never_contain_the_brief_or_instructions_or_output`,
which dispatches with a marked secret brief and marked child output and
asserts neither marker appears anywhere in the raw audit file contents.

### Concurrency / timeout / output caps

**Mechanically enforced.**

- Concurrency: `ConcurrencyLimiter` is a bounded semaphore (`try_acquire()`
  returns `False`, never blocks or queues unboundedly, once
  `MAX_CONCURRENT_CHILDREN` children are active); a full limiter causes the
  top-level dispatch to return a structured `denied` backpressure error
  before any child is spawned. Tested by
  `ConcurrencyLimiterTests.test_caps_concurrent_acquisitions` and
  `TerminalVsFallbackDispatchTests.test_concurrency_cap_returns_structured_backpressure_error`.
- Timeout: `spawn_and_wait()` spawns the child in its own process group
  (`start_new_session=True`) specifically so a timeout can group-kill
  (`os.killpg(..., SIGKILL)`) the whole process group, not just the direct
  child, on expiry. Tested by
  `SpawnAndWaitTests.test_group_kill_on_timeout`, which spawns a child that
  sleeps far longer than the configured timeout and asserts it is
  terminated promptly.
- Output cap: the child's stdout is read and capped at `max_output_bytes`
  in a background reader thread, with truncation explicitly recorded
  (`stdout_truncated`) rather than silently dropped, and the audit record
  never contains the captured output itself (see the redaction section
  above). Tested by
  `SpawnAndWaitTests.test_output_is_capped_and_truncation_recorded`.

### Role-file resolution safety (symlink/non-regular refusal, path containment)

**Mechanically enforced.**

- Symlink refusal: `_read_role_file_capped()` opens with `O_NOFOLLOW` and
  then verifies `S_ISREG` on the resulting file descriptor's `fstat`,
  refusing any resolved role-file path that is (or resolves through) a
  symlink at every tier. Tested by
  `SymlinkAndNonRegularRefusalTests.test_project_tier_symlink_refused`,
  `test_global_tier_symlink_refused`, `test_plugin_tier_symlink_refused`,
  and `test_symlink_at_higher_tier_does_not_fall_through_to_lower_valid_tier`
  (a symlinked higher-tier file must be a terminal denial, never a silent
  fallthrough to a valid lower tier).
- Non-regular-file refusal: the same `S_ISREG` check also refuses
  directories or other non-regular file types at the expected path. Tested
  by `test_project_tier_directory_refused`, `test_global_tier_directory_refused`,
  and `test_plugin_tier_directory_refused`.
- Path containment: `_ensure_contained()` verifies the resolved candidate
  path sits under the realpath of its declared tier root, defending against
  the tier root itself being replaced by a symlink pointed elsewhere (the
  `role_id` value itself cannot produce a traversal path, since it is
  already constrained to `^[a-z0-9-]+$` before any path is built from it).
  Exercised indirectly by the symlink-refusal tests above (which construct
  exactly this shape) and by `RoleIdValidationTests.test_rejects_path_traversal_shapes`
  for the `role_id` input side of the same defense-in-depth boundary.

### Team dispatch (`dispatch_team`)

Generalizes the single-role mechanism above to more than one member per call,
waiting for every member to reach a terminal state before returning
(implements `INTENT-CADRE-TEAM-DISPATCH-001`). Every control above still
applies per member exactly as documented; this section covers only the
team-specific additions and how each answers the intent record's OD-5
questions. `dispatch_secure_cloud_role()` itself, `ConfirmationGate`, and
`ConcurrencyLimiter.try_acquire()` are untouched by any of this -- team
support is additive, verified by
`DispatchTeamTests.test_single_role_dispatch_is_unaffected_by_team_support`
and the full pre-existing single-role suite passing unmodified.

**`dispatch_team_recipe` is a convenience wrapper, not a new control
surface.** It expands a `routing.json` `team_recipes[]` entry into concrete
`{role_id, brief}` members (`ExpandRecipeToMembers` in
`internal/orchestration/team_recipe_expand.go`, which delegates the firing
decision to the selector's own `BuildTeams` rather than reimplementing it)
and then calls `dispatch_team()` exactly as a caller who built the members
list by hand would -- every control below applies identically regardless of
which of the two tools produced the `members` list. The one thing this
wrapper adds is a refusal to expand a recipe that would not actually have
fired for the caller-supplied `matched_route_ids`/`selected_agent_ids` (or a
dynamic recipe's `instance_count` outside its declared min/max, or a member
left without a brief) -- `expand_recipe_to_members()` raises `ValueError` in
each case, which the tool surfaces as `status: "denied"` before
`dispatch_team()` is ever called. Tested by
`internal/orchestration/team_recipe_expand_test.go` and
`DispatchServerSchemaTests.test_recipe_tool_denies_a_recipe_that_would_not_fire_without_dispatching_anything`
/ `test_recipe_tool_unknown_recipe_id_is_denied_without_dispatching_anything`
(`internal/orchestration/dispatch_core_phase4_mcp_server_test.go`), both asserting `dispatch_team()` is never invoked
on a refused expansion.

- **Classification/sandbox narrowing: mechanically enforced, per member
  independently.** Each member is resolved and narrowed against the same
  caller-declared `parent_classification` exactly as a single dispatch would
  be -- there is no team-wide ceiling distinct from each member's own check,
  and no member can use another member's classification or sandbox as
  cover. Tested by `DispatchTeamTests.test_missing_parent_classification_is_denied`
  and the shared `validate_classification()`/`compute_effective_sandbox()`
  code path (same functions the single-role tests already cover).
- **Team size cap: mechanically enforced.** `MAX_TEAM_SIZE = 8`; a team
  larger than this is denied entirely before any member is resolved. This is
  a conservative v1 constant, not derived from a load test -- revisit if a
  real team recipe needs more. Tested by
  `DispatchTeamTests.test_team_over_max_size_is_denied`.
- **Dispatch-depth guard: same advisory limit as single-role, checked once
  per team.** `current_dispatch_depth() >= MAX_DISPATCH_DEPTH` denies the
  *entire* team before any member is resolved, exactly like a single
  dispatch; each spawned child still receives `depth + 1` in its own
  environment. This does **not** add a separate total-fan-out cap beyond
  `MAX_TEAM_SIZE` -- a team dispatch at depth 0 can cause up to 8 children to
  run, same honest limitation as the single-role depth guard above (advisory
  against a well-behaved child, not enforceable against one with its own
  code-execution authority).
- **Confirmation gating: mechanically enforced, one team-wide token covering
  every member.** `TeamConfirmationGate` mirrors `ConfirmationGate`'s
  single-use, TTL-bound, exact-match mechanism, but its bound subject is the
  ordered tuple of *every* member's `(role_id, brief_hash, mode,
  classification, effective_sandbox)` -- not only the write-capable ones --
  so altering any member (including a read-only one) after the first call
  invalidates the token. The `confirmation_required` response explicitly
  lists which members are write-capable (`write_capable_members`), so a
  human reviewing it sees exactly what they're approving rather than an
  opaque "this team needs confirmation." Tested by
  `TeamConfirmationGateTests` and
  `DispatchTeamTests.test_write_capable_member_requires_one_team_wide_confirmation`
  / `test_tampering_with_a_member_after_confirmation_request_invalidates_it`.
  Same honest limit as the single-role gate: this proves the two calls
  matched, not that a human actually read the intermediate response.
- **Concurrency: mechanically enforced, shared with single-role dispatch,
  blocking instead of immediate-deny.** Team members acquire the *same*
  `ConcurrencyLimiter` instance/pool single-role dispatch uses -- there is no
  separate team-scoped cap -- but via a new `acquire(timeout=...)` method
  that blocks until a slot frees (or the dispatch timeout elapses), rather
  than `try_acquire()`'s immediate denial. This is deliberate: a team can
  exceed `MAX_CONCURRENT_CHILDREN` by design (`routing.json`'s
  `competing-hypotheses-debugging` recipe allows up to 4 instances against a
  default cap of 3), and immediate denial would make dispatching any such
  team larger than the global cap unusable. `try_acquire()` itself is
  unchanged. Tested by `ConcurrencyLimiterBlockingAcquireTests` (waits for a
  released slot; times out when none frees) and
  `DispatchTeamTests.test_team_larger_than_the_concurrency_cap_still_completes_by_waiting`.

  **Two honest limits, found by independent security review, not fixed in
  this increment:**
  1. *Worst-case latency compounds, it isn't bounded by `timeout_seconds`
     alone.* `_run_member()` calls `limiter.acquire(timeout=timeout_seconds)`
     to wait for a slot, then passes that same `timeout_seconds` to
     `child_runner(...)` for the child's own execution timeout. A member that
     waits nearly the full timeout for a slot, then runs for nearly the full
     timeout again, can take up to ~2x `timeout_seconds` (up to ~20 minutes
     at the `DEFAULT_TIMEOUT_SECONDS = 600s` default) before its result is
     known, and `dispatch_team()` blocks on every member via `thread.join()`
     -- so a single slow member can hold up the whole tool call for that
     long. Not a security bypass, but "blocks until a slot frees, or the
     dispatch timeout elapses" understates real worst-case latency; treat
     `timeout_seconds` as a per-phase budget (queuing, then execution), not a
     total-call bound.
  2. *`try_acquire()` traffic can repeatedly out-race a parked team waiter.*
     `release()` only calls `notify()` once; a woken `acquire()` waiter must
     re-acquire the condition's lock and re-check before it can claim the
     freed slot, and a concurrent `try_acquire()` call (never blocks, checks
     and claims in one step) can win that race first. Under sustained
     ordinary single-role dispatch traffic sharing the same limiter, an
     oversized team's overflow members can be systematically passed over
     until they hit their own `acquire()` timeout, rather than eventually
     getting a slot as "blocks until a slot frees" implies. This fails safe
     (the member is denied via timeout, not left hanging forever) but is a
     real fairness gap, not just a theoretical one. Fixing this would need a
     FIFO-ordered wait queue instead of a bare condition variable; deferred
     as a follow-up, not done here.
- **Audit logging: mechanically enforced, one record per member plus one
  team-summary record, correlated by `team_id`.** Every member's audit
  record (`decision="dispatched"`/`"denied"`/`"unavailable"`) carries
  `team_id`, `team_size`, and `team_member_index` alongside the same fields a
  single dispatch's record would have; one additional record with
  `decision="team-completed"` (or `"team-denied"`/`"team-unavailable"` for a
  whole-team-level failure before any member is resolved) is written once
  every member reaches a terminal state, with a `status_counts` summary.
  **`_run_member()`'s body is wrapped in a catch-all `except Exception`**
  (added after independent security review found the gap): without it, any
  exception other than the anticipated `DispatchUnavailable` from
  `child_runner(...)` -- a bug in a custom `child_runner`, a malformed
  result dict, etc. -- would propagate out of the background thread
  uncaught (`threading.Thread` swallows it, prints to stderr, the thread
  just dies), leaving that member's `results[index]` as `None` and crashing
  `dispatch_team()`'s own aggregation loop, which would lose every sibling
  member's already-completed result and skip the team-completed record
  entirely. The catch-all guarantees a result and an audit record are always
  written for every member, however it fails. Tested by
  `DispatchTeamTests.test_unexpected_exception_in_one_member_never_crashes_the_team_or_drops_siblings`.
  `_FORBIDDEN_AUDIT_KEYS`'s redaction assertion applies identically -- team
  support introduces no new audit fields that could carry secret-shaped
  content. Tested by
  `DispatchTeamTests.test_audit_records_carry_a_shared_team_id_across_members`.
- **A concurrency bug found and fixed while building this feature:**
  `_ensure_audit_log_path()`'s `os.path.lexists()` check followed by an
  `O_CREAT | O_EXCL` open was not itself race-safe -- two threads (team
  members write audit records concurrently) could both observe the file
  absent and both attempt the exclusive create, and the loser raised
  `FileExistsError` uncaught, silently killing that member's thread before
  it recorded a result (surfaced as an intermittent `None` entry in
  `dispatch_team`'s results list during this feature's own test
  development). Fixed by catching `FileExistsError` from the losing thread's
  create attempt and treating it as success (the file exists with the
  correct mode either way); still `O_EXCL`, not `O_CREAT` alone, so a
  pre-placed symlink at this path is refused exactly as before. This bug
  predates team dispatch (the same race was always theoretically reachable
  from concurrent single-role dispatches sharing one audit path) but was
  never exercised by a test until team dispatch's genuine multi-threaded
  writes made it happen routinely; the single-role test suite gained no new
  regression coverage for it specifically because the fix is in the shared
  `_ensure_audit_log_path()` path both call.

### Async dispatch (`wait=False`) audit-write durability

**Best-effort, not mechanically enforced, for the completion/team-summary
records specifically -- this is a deliberate, narrower guarantee than the
"Audit logging: mechanically enforced" claim above, which describes the
default synchronous (`wait=True`) path.** When `dispatch_secure_cloud_role`,
`dispatch_team`, or `dispatch_team_recipe` is called with `wait=False`, the
job/team is tracked by an in-process `DispatchJobStore` /
`TeamDispatchJobStore` and the caller polls for the result via
`poll_dispatch_status()` / `poll_team_status()`. Two classes of audit write
exist on this path:

- **Pre-side-effect writes** (e.g. the initial `"dispatched-async"` record,
  written before the background thread starts) are still a plain
  `write_audit_record()` call inside a `try/except Exception:
  limiter.release(); raise` -- a failure here aborts the dispatch and
  propagates to the caller, exactly as the synchronous path does.
- **Post-side-effect writes** (a background job's or team member's
  completion/failure record, and `dispatch_team`'s own
  `"team-dispatched-async"` record) go through
  `_write_audit_record_best_effort()`, which swallows a write failure rather
  than propagating it. This is intentional: by this point a background
  thread is already running or a child process has already finished, so
  letting the audit write's failure prevent `job_store.complete()` /
  `results[index] = ...` / `team_id` from ever being returned would strand
  the job or team in `"running"` forever with no way for the caller to ever
  observe the real outcome -- worse, for an operationally-critical module,
  than one missing audit line. That job/team-state-survives-a-failed-write
  guarantee is tested by
  `test_audit_write_failure_between_acquire_and_thread_start_releases_slot`,
  `test_audit_write_failure_on_completion_still_reaches_terminal_job_state`,
  `test_member_audit_write_failure_on_completion_still_reaches_terminal_result`,
  and `test_team_dispatched_async_audit_failure_still_returns_pollable_team_id`.
  A failure here is not silent: it is written to stderr (`decision`,
  `job_id`/`team_id`, and the exception) so an operator has a trace to grep
  for even though the primary on-disk audit log is missing that record; that
  stderr fallback specifically is tested by
  `WriteAuditRecordBestEffortStderrFallbackTests` (all three methods --
  failure content, `team_id` fallback when `job_id` is absent, and no
  stderr noise on a successful write). All of the above live in
  `internal/orchestration/dispatch_core_phase4_mcp_server_test.go`.

The synchronous (`wait=True`) path is unaffected: its completion audit write
is still a plain `write_audit_record()` call, so a write failure there
propagates directly to the same caller who would otherwise receive the
result -- there is no background thread to silently swallow it.

### Claude Code runner (`runner="claude-code"`)

Implements OD-4 of `INTENT-CADRE-TEAM-DISPATCH-001`. Both
`dispatch_secure_cloud_role()` and `dispatch_team()` now accept a `runner`
parameter (`"codex"`, the default and the only runner covered by every
control above unchanged, or `"claude-code"`); every existing caller that
never passes `runner` gets exactly the pre-existing Codex behavior, byte for
byte -- confirmed by the full pre-existing single-role and team test suites
passing unmodified.

- **Role-file resolution: two tiers, not three, and the plugin tier's path
  is unverified.** `resolve_claude_role_file()` checks a project-tier
  `.claude/agents/<role_id>.md` override (a real, documented convention --
  `runner-adapters.md`) first, then falls back to an installed plugin's own
  generated `agents/<role_id>.md`. There is no Claude Code equivalent of
  `internal/orchestration/sync_codex_agents.go`'s `~/.codex/agents/` global-sync tier. The plugin
  tier's search path
  (`~/.claude/plugins/cache/<marketplace>/<plugin>/<version>/agents/<role>.md`)
  was *observed*, not documented, in the session that wrote this code --
  `_find_claude_plugin_role_file()` glob-searches every
  marketplace/plugin/version combination under the search root rather than
  assuming a single fixed path, and **mechanically refuses (denies) rather
  than guesses** when more than one installed copy matches, asking for a
  project-tier override to disambiguate instead. This was confirmed to be a
  real, not theoretical, scenario in this session's own environment: eight
  distinct installed marketplace/plugin/version combinations of
  `code-reviewer.md` existed simultaneously in the actual plugin cache used
  to test this. Tested by `ClaudeCodeRunnerTests`.
- **Frontmatter parsing: mechanically enforced, targeted, not a general
  parser.** `_extract_markdown_frontmatter()` only extracts the fixed keys
  this tool needs (`model`, `effort`); any other declared field
  (`name`/`description`/`tools`/`generated`/`canonical_source`) is ignored,
  matching `_extract_toml_fields()`'s discipline for the Codex format.
  Verified directly against a real installed plugin's generated
  `agents/code-reviewer.md` in the same session, not only synthetic
  fixtures. Tested by `MarkdownFrontmatterTests`.
- **Sandbox narrowing: this runner can only ever produce `read-only` in this
  increment -- a scoping fact, not a gap someone forgot to close.** No field
  in the Claude Code wrapper format declares write-capability (unlike
  Codex's `.toml` `sandbox_mode` field); `ResolvedRole.sandbox_mode` is
  therefore always `None` for this runner, and
  `compute_effective_sandbox()`'s existing behavior (already tested for the
  Codex path) narrows a `None` file-declared sandbox to `read-only`
  regardless of `mode`. Extending this to write-capable Claude Code dispatch
  needs a new wrapper-format field and generator support -- tracked as
  follow-up, not done here. Tested by
  `ClaudeCodeRunnerTests.test_effective_sandbox_is_always_read_only_regardless_of_mode`.
- **`--permission-mode` mapping: an unreviewed design choice, not a proven
  equivalent to Codex's `--sandbox`, and this is the single most important
  open risk in this section.** `build_claude_child_argv()` maps
  `read-only`/`workspace-write`/`danger-full-access` to Claude Code's
  `plan`/`acceptEdits`/`bypassPermissions` permission modes (flag and
  choices VERIFIED 2026-08-03 against a real installed `claude --help`).
  But Codex's `--sandbox` is (believed to be) an OS-enforced execution
  boundary, while Claude Code's `--permission-mode` governs whether Claude
  Code's own tool-dispatch layer auto-approves or blocks a tool call --
  a different enforcement mechanism with a different trust boundary (it
  trusts the Claude Code binary's own tool-gating logic to be correct,
  rather than relying on OS-level sandboxing independent of that binary).
  Because sandbox narrowing above means `workspace-write`/
  `danger-full-access` are currently unreachable in practice (this runner
  can only ever resolve to `read-only`), only the `plan` mapping is
  exercised in production today -- but this mapping must be re-examined by
  the accountable Security Lead specifically when write-capability is added
  for this runner, not assumed correct because it "looks" analogous to the
  Codex mapping.
- **Prompt delivery: verified empirically, reuses the existing pipeline
  unchanged.** A live `echo "..." | claude -p --model haiku` call in the
  session that wrote this code confirmed that omitting the positional
  `prompt` argument and piping stdin instead is read as the prompt exactly
  like Codex's trailing `-` convention -- so `compose_prompt()`'s existing
  untrusted-brief-fencing output (already covered by
  `ComposePromptTests`/the audit-redaction tests) is fed to a Claude Code
  child on stdin completely unchanged, via the same `spawn_and_wait()` both
  runners share. No `--system-prompt` flag is used, avoiding a decision
  about whether to duplicate `developer_instructions` between a flag and
  stdin.
- **Everything else (env allowlist, output caps, timeout/group-kill,
  confirmation gating, audit logging, depth guard) is runner-agnostic
  *across the two CLI runners*** -- none of those controls inspect `runner`
  at all, so their existing enforcement/test coverage applies identically to
  a Claude Code dispatch. This sentence used to say "runner-agnostic" without
  qualification, which the `api` runner made false: four of those six live
  inside `spawn_and_wait()`/`build_child_env()` and therefore reach only a
  dispatch that spawns a child process. See "API runner" below for exactly
  which four, and what replaces them there.
- **Not verified: live, authenticated end-to-end execution of a real role
  dispatch.** The one live `claude` invocation in this session was a
  trivial smoke test of stdin-piping behavior with `--model haiku`, not a
  full role dispatch through `dispatch_secure_cloud_role()`/`dispatch_team()`
  against a real installed Claude Code CLI. Do this before relying on the
  Claude Code runner for anything beyond local development.

### Self-hosted model providers (Codex runner)

An operator may point the `codex` runner at an OpenAI-compatible endpoint they
host themselves -- typically `llama-server` -- instead of a vendor API. Three
settings do this, all `global_only`: `runners.codex_profile`,
`runners.local_model_<tier>`, and `runners.forward_env` (covered in the env
allowlist entry above). All three are consumed in exactly one function,
`build_child_argv()`.

- **The endpoint and its credential never enter this repository's
  configuration: mechanically enforced.** Cadre passes `--profile <name>` and
  nothing else about the provider; `base_url`, `wire_api` and `env_key` live
  in the operator's own `$CODEX_HOME/<name>.config.toml`, which Codex owns.
  There is no Cadre setting that can hold a `base_url` for this runner, and
  `internal/config`'s secret-leaf patterns refuse a credential-shaped key in
  any config file it manages. Tested by
  `SelfHostedProviderFieldTests.test_every_new_field_is_global_only` and
  `test_an_api_key_leaf_is_still_refused_in_a_config_file`.
- **Model-tier semantics survive the switch: mechanically enforced.** Each
  catalog tier maps to its own operator-chosen model, so a `haiku`-tier
  triage role does not silently get the model an `opus`-tier architecture
  role was meant to use. `ResolvedRole.model_tier` is derived by inverting
  `runner-capabilities.json`'s `model_tiers` -- the manifest's first
  dispatch-time read, anticipated by
  `runs/cadre-idea-8-capability-manifest-2026-07-29/requirements.md:13`. An
  identifier in neither namespace yields `None`, i.e. "no override applies",
  never a guessed tier. Tested by `ModelTierReverseMapTests` and
  `CodexLocalProviderArgvTests.test_a_different_tiers_setting_does_not_leak_across_tiers`.
- **No configuration means no behavior change: mechanically enforced.** With
  none of the three settings present, `build_child_argv()` produces the argv
  it produced before this feature existed. Tested by
  `CodexLocalProviderArgvTests.test_no_settings_configured_keeps_the_original_argv`.
- **Model identifiers cannot smuggle arguments: mechanically enforced.** A
  model or profile name is validated against an anchored allowlist
  (`_validate_identifier`) that admits letters, digits and `. _ : / + -` and
  refuses whitespace, shell metacharacters, and a leading `-` -- the last
  because a value beginning with `-` is read as another option by the CLI
  receiving it. Tested by
  `SelfHostedProviderFieldTests.test_a_model_identifier_with_shell_metacharacters_is_refused`.
- **What this does *not* control.** The operator's chosen endpoint sees the
  role's full `developer_instructions` and the task brief, exactly as a
  vendor API would. Pointing a role at a self-hosted or third-party endpoint
  is a data-egress decision, and this register does not make it a safe one --
  it only ensures the decision is explicit, `global_only`, and recorded in
  the audit trail as the `runner` and tier that served it. The
  role-fidelity notice earlier in this file applies with full force here:
  a smaller self-hosted model may not honor a role's constraints, and no
  control in this repository detects that.

### API runner (`runner="api"`)

The first dispatch runner that spawns no coding CLI. `api_runner_loop.go` drives
an OpenAI-compatible `/chat/completions` endpoint directly and supplies both
halves a child CLI would otherwise provide: the agent loop, and the sandbox.
That second half is the whole risk of this runner, and this section is
written to be read before enabling it, not after.

- **Which existing MCP controls still reach it.** Role resolution, sandbox
  narrowing, classification checks, the confirmation gate, the concurrency
  limiter, audit logging, and untrusted-brief fencing are all above the
  runner switch and apply unchanged. Brief fencing in particular reuses
  `dispatch_core.fence_untrusted_brief()` -- the same code the CLI runners
  use, not a second copy -- with the role's trusted instructions in the
  system slot and the fenced brief in the user slot. Tested by
  `ApiRunnerLoopTests.test_the_brief_is_fenced_through_dispatch_cores_own_helper`.
- **Which four do not, and why.** The env allowlist, output caps,
  timeout/group-kill, and the depth guard all live in `spawn_and_wait()` /
  `build_child_env()`. On the model-call path there is no child process for
  them to apply to. Their replacements:
  - *env allowlist*: not applicable -- there is no child environment. See the
    new trust boundary below.
  - *output caps*: `MAX_RESPONSE_BYTES` per HTTP response,
    `MAX_TOOL_RESULT_BYTES` per tool result, and `core.MAX_CHILD_OUTPUT_BYTES`
    on the assembled transcript.
  - *timeout*: one wall-clock deadline for the whole loop, plus
    `MAX_TOOL_ITERATIONS`. Tested by
    `ApiRunnerLoopTests.test_the_iteration_cap_terminates_a_looping_model`.
  - *depth guard*: **narrowed, not replaced -- and this entry previously
    overclaimed it.** An earlier revision said recursive dispatch was
    "structurally impossible rather than merely capped." That was wrong, and
    it was the strongest-worded claim in this section. Three mechanisms
    genuinely apply, and none of them is structural:
      1. No tool exposes dispatch, so a role cannot re-dispatch directly.
      2. `_REFUSED_COMMANDS` refuses `cadre`/`codex`/`claude`/`cline`/
         `agentic-sdlc` — but **only as the literal `command` argument**. An
         operator who allowlists any general-purpose interpreter (`python`,
         `bash`, `node`, `make`, `npm`) has given the dispatched agent a way
         to exec one of those as a *grandchild*, which this check never sees:
         it is handed the string `"python"`, never `"codex"`. Tested by
         `ApiRunnerCommandAllowlistTests.test_agent_launching_binaries_are_refused_even_if_allowlisted`,
         which proves exactly the narrow claim and nothing wider.
      3. `_sanitized_child_path()` strips from the child's `PATH` every
         directory containing one of those binaries, so a bare-name `exec`
         inside an allowlisted interpreter fails to resolve. This defeats
         bare-name lookup only; an absolute path is unaffected, and any
         interpreter can construct one.
    So the honest statement is: **the attack surface is narrower than the CLI
    runners', and `MAX_DISPATCH_DEPTH` remains the actual backstop** — a
    guard this register already documents as advisory against an adversarial
    child. If the structural property is wanted, `runners.api_command_allowlist`
    must exclude general-purpose interpreters, and that is an operator
    obligation this code does not enforce.
  On the `run_command` path all four are restored, because that tool routes
  through the real `spawn_and_wait()` and `build_child_env()`. Tested by
  `ApiRunnerCommandAllowlistTests.test_the_command_child_gets_the_deny_by_default_environment`.
- **Path confinement: mechanically enforced, but in-process only.** Every
  file tool resolves its path with `os.path.realpath` and then
  `dispatch_core._ensure_contained` -- the same helper the role-file resolver
  uses -- and refuses `.git`. Traversal, absolute paths outside the root, and
  symlinks pointing out of the tree are all denied, and a denied path is
  reported back to the model as a tool error so it can correct a typo without
  aborting the dispatch; the refused operation never happens either way.

  Both reads *and* writes then open with `O_NOFOLLOW` plus a post-open
  `fstat`/`S_ISREG` check -- reads via `_read_text_capped()`, writes via
  `_write_bytes_nofollow()`. This matters more than it looks: a `realpath`
  check proves containment at check time, and the write happens afterwards.
  An earlier revision of this entry claimed universal `O_NOFOLLOW` coverage
  while `write_file`/`edit_file` in fact used `Path.write_bytes`/`write_text`,
  which follow a symlink at the final component -- leaving a check-then-open
  window that a symlink appearing in between would defeat, redirecting a
  write anywhere this process can reach. `dispatch_team()` runs members
  concurrently against one project root, so that interleaving was reachable,
  not theoretical. The claim is now true as written.

  Tested by `ApiRunnerPathConfinementTests` (21 tests), including
  `test_a_symlink_at_the_write_target_is_refused_by_o_nofollow`, which
  exercises the write syscall's own guard rather than the resolve-time check.

  **Read the limit precisely: this constrains the tool surface, not the
  process.** The CLI runners get kernel-level confinement from Codex's and
  Claude Code's own sandboxes; this runner has none. It holds against a model
  asking for the wrong path. It would not hold against arbitrary code
  execution inside this process.
- **Write gating: mechanically enforced.** A write requires all four of
  `mode="scoped-repository-edit"`, a write-capable `effective_sandbox` from
  `compute_effective_sandbox()`, a consumed `ConfirmationGate` token, and the
  operator's `runners.api_allow_writes` opt-in (default false). The role's
  own capability tier is checked independently: a `read_only` tier is never
  offered write tools no matter what the other three say. Tested by
  `ApiRunnerWriteAuthorizationTests` and `ApiRunnerToolAvailabilityTests`.
- **`run_command`'s allowlist: ADVISORY, not enforced.** This is the single
  most important line in this section. The allowlist bounds which *commands*
  may start -- bare names only, no path separators, resolved through
  `shutil.which`, `shell=False`, `cwd` pinned to the project root, and empty
  by default so the tool is not offered at all. It does **not** bound what
  those commands then do, because the dispatched agent chooses the arguments
  and every command an operator would realistically allowlist (`pytest`,
  `go`, `npm`) executes repository-controlled code by design: a `conftest.py`,
  a `TestMain`, a `package.json` script. Enabling `runners.api_command_allowlist`
  is therefore equivalent to granting the dispatched role the ability to run
  repository code with this server process's own file access. It must never
  be described as equivalent to the CLI runners' OS sandbox, and it is not a
  containment boundary. Tested by `ApiRunnerCommandAllowlistTests`, which
  asserts which commands can start and deliberately asserts nothing about
  what an allowlisted command then does.
- **Network posture: the first outbound-HTTP entry for a dispatch path.**
  `cadre mcp-dispatch-server` is stdio-only and has no networked transport
  at all; this runner nonetheless opens outbound HTTP,
  which is new ground for this register. Bounded by: an operator-configured
  `base_url` only (never caller- or model-supplied, and `global_only` so a
  cloned repository cannot redirect it); redirects refused outright
  (`_RejectRedirects`, following `internal/knowledge/persistence.go`), so an endpoint cannot move
  the request and its `Authorization` header to a host the operator never
  configured; a response size cap; a request timeout; and a URL policy that
  accepts `https://` anywhere but `http://` only toward a loopback,
  link-local or RFC1918/ULA host, so a mistyped public endpoint cannot
  receive an API key in the clear. URL userinfo is refused.

  **Two distinct halves, deliberately cited separately.** An earlier revision
  cited only the configuration-time tests as proof of the whole bullet,
  including redirect refusal -- which those tests do not touch at all. They
  validate URL *strings* in `internal/config`; they never construct a
  `ChatEndpoint` or open a socket. A runtime behavior needs a runtime test:
  - *Configuration-time URL policy* (scheme, host class, userinfo): tested by
    `SelfHostedProviderFieldTests.test_plaintext_http_toward_a_public_host_is_refused`,
    `test_url_userinfo_is_refused`, `test_https_is_accepted_for_any_host`,
    `test_plaintext_http_is_accepted_toward_a_private_host`, and
    `ApiRunnerEndpointConfigTests`.
  - *Runtime HTTP behavior* (redirect refusal, response cap, error handling):
    tested by `ChatEndpointHttpTests`, which drives `ChatEndpoint.complete()`
    over real loopback HTTP.
    `test_a_redirect_is_refused_and_the_credential_never_reaches_the_new_host`
    stands up the redirect *target* as well and asserts it received no
    `Authorization` header. That test was verified mutation-sensitive:
    making `_RejectRedirects` follow redirects makes it fail.

  Host classification unwraps IPv4-mapped IPv6 (`::ffff:<v4>`) and judges the
  mapped address, rather than trusting `ipaddress`'s `is_private` directly.
  CPython classified the whole `::ffff:0:0/96` range as private until
  CVE-2024-4032 (fixed in 3.12.4 / 3.13.0a6, backported); this repository
  supports Python 3.10+ and cannot control an operator's patch release, so
  trusting `is_private` would make a security-relevant decision depend on the
  interpreter build -- on an unpatched one, `http://[::ffff:8.8.8.8]/v1`
  would have been accepted as "private" and shipped a key in cleartext to a
  public host.

  Honest limit, unchanged: the private-host check resolves the name once, at
  configuration time. It is a guard against a typo, not against DNS
  rebinding.
- **New trust boundary: the API key is read from this server process's own
  environment**, via the variable *named* by `runners.api_key_env`. That is
  precisely the class of variable the child env allowlist exists to keep out
  of a dispatched child. There is no contradiction -- this path spawns no
  child, so there is no child environment to leak into, and
  `test_credential_shaped_variables_never_leak_through` still passes
  unchanged -- but it is a genuinely new boundary and is stated here rather
  than left to be inferred.
- **Audit trail: `runner` is now recorded on every record**, along with
  `tool_calls`, `files_written` (paths only) and `commands_run` for this
  runner. Before this, the trail could not distinguish a Codex dispatch from
  a Claude one; with three runners of materially different enforcement
  properties that was untenable. File *contents* remain structurally barred
  by `_FORBIDDEN_AUDIT_KEYS`. The single-role unknown-runner denial is now
  audited too, matching `dispatch_team`, which always did. Tested by
  `DispatchWithApiRunnerTests.test_the_runner_is_recorded_on_every_audit_record`,
  `test_tool_activity_reaches_the_audit_record`, and
  `test_an_unknown_runner_is_denied_and_now_audited`.
- **Not verified: live execution against a real llama.cpp server.** Every
  test above runs against a scripted fake endpoint, and the end-to-end
  exercise during development used a local stub, not `llama-server`. Tool
  calling on llama.cpp additionally requires `--jinja` and often a
  `--chat-template-file` override, and its `tool_calls[].function.arguments`
  is known to deviate from the OpenAI schema (ggml-org/llama.cpp#20198;
  `parse_tool_calls` accepts both shapes). Confirm behavior against the real
  endpoint before relying on this runner for anything beyond local
  development.
- **Deadline integrity.** Each model call is bounded by the *smaller* of the
  endpoint ceiling and the dispatch budget still remaining, so a slow
  endpoint cannot overrun the `timeout_seconds` the rest of the pipeline
  (`spawn_and_wait`'s group-kill, the concurrency limiter, job/team TTLs) is
  built around. An earlier revision checked the deadline only *between* turns
  and passed a flat 120s to each call, letting a final in-flight request
  overrun by up to that much. Tested by
  `ApiRunnerLoopTests.test_each_call_is_bounded_by_the_remaining_dispatch_budget`
  and `test_an_expired_deadline_stops_the_dispatch_before_any_model_call`.
- **Partial-mutation reporting.** Tool side effects are **not transactional
  and are never rolled back.** If the endpoint fails partway through a
  write-capable dispatch, files already written stay written. What is
  guaranteed is that this is *reported*: the result and the audit record
  carry `files_written`/`commands_run`/`tool_calls` even on that failure
  path, and the transcript states explicitly that the effects are not rolled
  back. An earlier revision let the exception escape, producing an
  `unavailable` verdict with no indication the workspace had been mutated --
  the single worst outcome for an auditor. A mid-loop failure with *no*
  mutations still raises `unavailable`, so a genuine outage is not disguised
  as a dispatch that ran. Tested by
  `ApiRunnerLoopTests.test_a_mid_loop_endpoint_failure_still_reports_what_was_written`
  and `DispatchWithApiRunnerTests.test_async_dispatch_surfaces_the_api_runners_activity_fields`
  (the `wait=False` path dropped these fields entirely until that test caught it).
- **Accountable-review note.** Following the precedent set by the Claude Code
  runner's `--permission-mode` mapping, the `run_command` allowlist and the
  in-process path confinement are design choices that have not been reviewed
  by the accountable Security Lead. They must be, specifically before this
  runner's write-capable path is used anywhere but a local working copy.

  Two items belong in that review specifically, both raised by an independent
  security review of the change that introduced this runner and both
  documented above rather than silently fixed:
  1. The recursion claim under "which four do not, and why" was an overclaim
     and is now stated honestly. Whether "narrower surface plus an advisory
     `MAX_DISPATCH_DEPTH`" is *sufficient*, or whether
     `runners.api_command_allowlist` needs a mechanical interpreter refusal,
     is a Security Lead decision, not an author one.
  2. The `run_command` allowlist's advisory status is accurately labelled,
     but the consequence deserves an explicit accepted-risk decision:
     enabling it grants the dispatched role the ability to run
     repository-controlled code with this server process's own file access.

### GitLab evidence MCP server (`gitlab.go` / `gitlab_mcp.go`)

A separate, create-only MCP surface (`create_review_subtask`, `write_wiki_page`,
`write_evidence_comment`) for recording human-reviewable evidence in a single,
pre-configured, docs-only GitLab project. It shares the dispatch core's
`ConfirmationGate`, `wrap_untrusted_output`, and audit-record mechanism
directly (not a reimplementation) but is otherwise an independent module with
its own token, transport, and retry logic. This section uses the same
mechanically-enforced/advisory classification as above.

- **Token handling: mechanically enforced.** `resolve_token()` reads exactly
  one env var (`GITLAB_SVC_TOKEN`; `GL_SVC_TOKEN`/`GITLAB_SERVICE_TOKEN` are
  never honored as aliases), lazily, only from inside a tool function that
  needs it -- never at import or server-startup time -- and fails closed on
  unset/empty/whitespace-only. The token travels only in the `PRIVATE-TOKEN`
  HTTP header, never a query parameter, never folded into an exception
  message, log line, or audit record. Tested by `TokenResolutionTests`,
  `TokenNeverLeaksTests`.
- **TLS / redirect controls: mechanically enforced.** `resolve_config()`
  requires `GITLAB_BASE_URL` to start with `https://`; there is no
  configuration path anywhere in this module that accepts an `http://` base
  URL or that can disable certificate verification (`_build_opener()` only
  ever constructs `ssl.create_default_context()` with no
  `check_hostname=False`/custom-verify-mode escape hatch). `_NoCrossHostRedirectHandler`
  refuses (raises `GitLabPermanentError`, never a silent fallthrough) any
  redirect whose target host differs from the original request's host, *and*
  any redirect whose target scheme is not `https` -- so a same-host
  `https`-to-`http` downgrade can never cause the `PRIVATE-TOKEN` header to be
  replayed in cleartext. `_build_opener()` also passes an explicit
  `urllib.request.ProxyHandler({})` to `build_opener()`, so proxying is
  disabled unconditionally regardless of any ambient `HTTPS_PROXY` /
  `https_proxy` / `ALL_PROXY` (or other `getproxies()`-recognized) environment
  variable in the process this module runs in -- without it,
  `build_opener()` silently installs its own default, environment-driven
  `ProxyHandler()` (it only omits a default handler class when an instance of
  that exact class is already among the handlers passed in, and none of this
  module's other handlers is a `ProxyHandler`), which would route every
  GitLab API call through an attacker- or misconfiguration-controlled proxy
  with no logging and no opt-out anywhere in this module. This is the same
  "no escape hatch anywhere" discipline as the TLS-verification and redirect
  controls above, extended to ambient-environment-driven proxy routing, not
  just GitLab-response-driven redirects. Tested by
  `ConfigResolutionTests.test_requires_https_base_url` and `RedirectAndTlsTests`
  (cross-host redirect, same-host scheme-downgrade redirect, a code-reading
  assertion that no env var or config value can weaken the SSL context's
  `check_hostname`/`verify_mode`, and an assertion that the opener's
  `ProxyHandler` carries an explicit empty proxy map even when
  `HTTPS_PROXY`/`https_proxy`/`ALL_PROXY` are set in the ambient environment).
- **Create-only invariant / no state-transition path: mechanically enforced
  at two distinct layers -- Python call-graph shape, and GitLab body-text
  interpretation. Be precise about what each layer actually covers; neither
  alone is the whole guarantee.**
  - *Source-level structural guarantee.* This module implements no function,
    and calls no function anywhere in its own source or the dispatch core's,
    that closes, reopens, resolves, or relabels-away-from-open-review a
    GitLab issue. `create_review_subtask` only ever performs a `GET`
    (idempotency search) and at most one `POST`. Tested by
    `StructuralNoStateTransitionTests` (name-shape scan of every function in
    the module, an explicit check that the named forbidden functions don't
    exist, a source-level check that GitLab's `state_event` field is never
    used, and a scan of `create_review_subtask`'s own function body for a
    call-shaped use of a forbidden verb). **This layer, by itself, cannot see
    a GitLab-side effect triggered by body text this module sends** -- it
    only scans this module's own Python source and call shapes, and GitLab
    itself interprets a note/issue body server-side as coming from this
    module's own service-account token, entirely independent of which HTTP
    endpoint sent that body. A prior round of this document described the
    create-only invariant as "mechanically enforced" based on this layer
    alone; that was accurate about the Python call graph but incomplete about
    the actual guarantee a caller experiences, which is why the second layer
    below was added.
  - *Body-text quick-action neutralization.* GitLab executes "quick actions"
    (slash-commands such as `/close`, `/unlabel`, `/relabel`,
    `/confidential`, `/lock`, `/reopen`, `/label`) embedded anywhere in an
    issue description or a note body, interpreted server-side as coming from
    the note's author -- this integration's own service-account token --
    regardless of which of this module's own HTTP endpoints sent that body.
    Before this fix, a caller-supplied `description` (`create_review_subtask`)
    or `content` (`write_evidence_comment`) line like `/close` or
    `/unlabel ~"review-subtask"` reached GitLab byte-identical and was
    executed, silently transitioning the evidence issue's state even though
    no Python code path in this module ever called a close/label-removal
    endpoint -- defeating the create-only guarantee at the effect level, not
    the call-graph level, and invisible to `StructuralNoStateTransitionTests`
    since the transition happens via GitLab's own body-text interpretation.
    `_reject_quick_action_syntax()` now rejects (never silently strips or
    truncates) any line whose first non-whitespace characters match
    `^\s*/[a-z][a-z_]*\b`, matched case-insensitively (`re.IGNORECASE`) since
    GitLab's own quick-action matching is case-insensitive server-side
    (verified against `lib/gitlab/quick_actions/extractor.rb`) -- a prior
    version of this filter was case-sensitive and so missed `/Close`,
    `/CLOSE`, `/UNLABEL ~"review-subtask"`, and any other case-varied
    command, all of which GitLab still executes; that gap is now closed. The
    pattern is applied to caller-supplied `description`/`content`, raising
    `GitLabValidationError` before any HTTP call, applied *before*
    `create_review_subtask`'s own trusted `/relate #<iid>` line is appended
    (so the check only ever sees caller-supplied text, and that
    module-authored line -- a deliberate, intentional quick action this
    module relies on for the hierarchy fallback -- is never itself rejected).
    This filter is deliberately broader than GitLab's real, finite quick-action
    command list -- GitLab's own extractor only interprets a line as a quick
    action if it names one of a fixed set of registered command names
    (matched via `Regexp.union(names)`; a line like `/notacommand` is never
    interpreted), but this module matches any line merely *shaped* like a
    quick action, so it never needs to track GitLab's exact, version-specific
    command set to remain safe. The tradeoff is over-rejection of some
    legitimate content shaped like a quick action (e.g. a fenced code block
    containing a shell command starting with `/`, or a path-starting log
    line) that GitLab's real extractor would not have interpreted because it
    excludes inline code, fenced code blocks, and quote blocks from
    quick-action parsing and this module's filter does not; that is a known,
    accepted false-positive/usability gap, not a security gap, and is
    tracked as separate follow-up work rather than fixed here.
    `write_wiki_page`'s `content` is deliberately NOT run through this check:
    GitLab's quick-action interpreter only ever parses issue/note bodies, not
    wiki page content (see `write_wiki_page`'s own docstring for the
    "Quick-action scope note" and the revisit condition if that is ever found
    to be inaccurate for a specific GitLab version/edition). Tested by
    `QuickActionNeutralizationTests`.
- **Idempotency search: mechanically enforced, exact-match, never a substring
  match against untrusted content.** `_find_existing_subtask()` queries with
  `state=opened` (a closed/already-resolved issue is never silently adopted;
  a fresh call after the prior subtask was closed intentionally creates a new
  one) and all three labels -- `review-subtask`, `gate:<gate_id>`, and a
  hash-based `evidence-key:<hash>` label derived from
  `(task_id, gate_id, parent_issue_iid)` -- filtered server-side and
  re-verified locally against each candidate's own `labels`/`state` fields,
  paginated (`per_page=100`, bounded page loop) so a match beyond an
  unpaginated first page is never missed. Folding `parent_issue_iid` into the
  hashed input (not only `task_id`/`gate_id`) is what restores parent binding
  deterministically and structurally: without it, an open issue carrying the
  right three labels would be adopted as a match regardless of which parent
  it actually references, which was a real regression introduced when the
  matching moved off the untrusted free-text `Parent: #<iid>` description
  check. The `Parent: #<iid>` text is still written into the description for
  human readability only and is never read back for matching. The prior
  design's unauthenticated substring match against issue description text is
  gone entirely; a decoy issue would need the exact three-label combination,
  which requires the same permission tier (e.g. GitLab Reporter+ on this
  project) the legitimate create flow already assumes -- not the same
  identity/credential, since any project member at that tier can label an
  issue. `create_review_subtask`'s result surfaces `state` at the top level
  (not only nested inside the wrapped issue payload) for both a reused match
  and a freshly created issue. Tested by `CreateReviewSubtaskTests`.
  **Honest limit, distinct from the stale/poisoning issues already fixed in
  earlier rounds: the search-then-create sequence is not atomic under
  genuine concurrency.** Two independent concurrent calls with the same
  `(task_id, gate_id, parent_issue_iid)` can both observe "no existing
  issue" via their own GET before either has POSTed, and both then POST,
  producing two open issues carrying the identical evidence-key label. There
  is no distributed lock or server-side compare-and-swap primitive closing
  this gap, and none is added in this round -- disclosed explicitly (see
  `create_review_subtask`'s own docstring) rather than implicitly claimed
  away by the word "idempotent".
- **Retry / fail-closed behavior: mechanically enforced.** `request_json()`
  retries 429/5xx/timeout/network errors with bounded jittered exponential
  backoff (`MAX_RETRY_ATTEMPTS`, `MAX_RETRY_ELAPSED_SECONDS`) and raises
  immediately, without retry, on 401/403/404. Every non-2xx/network outcome
  raises a `GitLabError` subclass rather than fabricating a success shape; no
  caller-visible false success is possible. **Honest limit:** retrying a
  non-idempotent write (POST/PUT) on a 5xx/timeout cannot distinguish
  "never processed" from "processed but the response was lost" without a
  server-side idempotency key the targeted GitLab REST API doesn't expose --
  `create_review_subtask`'s search-before-create is this module's mitigation
  for the one operation where a duplicate would be most visible;
  `write_evidence_comment` and `write_wiki_page` have no equivalent
  caller-level dedup and could in principle double-apply on this specific
  failure shape. Tested by `RequestJsonRetryTests`.
- **1 MiB evidence-comment cap: mechanically enforced.** `write_evidence_comment`
  rejects (never truncates) content whose UTF-8 encoding exceeds
  `MAX_EVIDENCE_COMMENT_BYTES` (1 MiB), before any HTTP call.
  `write_wiki_page` has its own, separate 2 MiB cap
  (`MAX_WIKI_PAGE_CONTENT_BYTES`), also reject-not-truncate, before any HTTP
  call. Tested by `WriteEvidenceCommentTests`.
- **Untrusted-output wrapping: mechanically enforced, including the error
  path.** Every piece of GitLab-retrieved content returned in a *successful*
  tool result (`result["issue"]`, `result["page"]`, `result["comment"]`) is
  wrapped with `dispatch_core.wrap_untrusted_output`'s exact marker-token
  scheme via `wrap_untrusted_gitlab_payload()`, so text an attacker
  deliberately wrote into an issue title/description/wiki body/comment can
  never be mistaken by the calling model for a trusted instruction. The
  *error* path gets the same treatment: `_error_result()` wraps
  `GitLabPermanentError`/`GitLabRetryableExhaustedError`'s `str(error)` --
  which can embed a snippet of GitLab's own raw response body -- with the
  identical marker-token scheme before it reaches `result["reason"]`; a
  validation/config error's message is this module's own generated wording
  and is returned unwrapped. Tested by `UntrustedWrappingTests` (success
  path, asserting the marker tokens themselves are present in the payload,
  not merely that some substring of the underlying data appears) and
  `TokenNeverLeaksTests`/the retry test suite (error path).
- **Error-response bodies never reach the audit trail: mechanically
  enforced.** `request_json()` still embeds a snippet of GitLab's raw
  response body in the exception `message` used for the caller-facing
  result above, but every audit-record write uses
  `_audit_safe_reason(error)` instead of `str(error)` -- which returns
  `GitLabPermanentError.audit_reason` (a body-free, this-module-generated
  variant) rather than the raw-body-bearing message -- plus, when available,
  a `response_body_sha256`/`response_body_length` pair (hash/length only,
  never content) via `_audit_error_meta()`. `dispatch_core._FORBIDDEN_AUDIT_KEYS`
  additionally now includes `content`/`body`/`description` as a defense-in-
  depth backstop against any future call site accidentally passing raw
  content under one of those names.
- **Human-confirmation gate for `write_wiki_page`: same mechanism and same
  honest limit as the dispatch core's own `ConfirmationGate` above.** Every
  call, with no exception, must round-trip through a first
  `confirmation_required` response and a second call replaying the token
  bound to the exact `(slug, title, format, content hash)` tuple before any
  GitLab write happens. This is mechanically enforced for the two-call,
  single-use, TTL-bound, tamper-detecting mechanism itself; it is **not** and
  cannot be enforced from inside this tool that a human actually read the
  intermediate response before a fully autonomous caller issued the second
  call. The first call's `confirmation_required` response also discloses
  `will_overwrite_existing: bool`, computed by checking `_get_wiki_page()`
  *before* requesting confirmation (not folded into the tamper-detected
  `brief` itself, so a page's existence changing between the two calls can
  never spuriously invalidate an otherwise-unchanged confirmation) -- so the
  human approving the confirmation sees whether the write creates a new page
  or overwrites an existing one as part of what they're approving, not only
  after the fact. Tested by `WriteWikiPageConfirmationTests`,
  `WriteWikiPageCreateVsUpdateTests`, `GetWikiPageTests`.
  **Honest limit: the disclosed `will_overwrite_existing` hint can go stale
  during the confirmation window.** It is computed once, before requesting
  confirmation, from whichever page state existed at that moment; because
  the confirmation TTL is up to `CONFIRMATION_TTL_SECONDS` (300s), another
  actor could create or delete a page at the same slug before the second
  call. The actual write always re-checks fresh at consume-time (`_get_wiki_page()`
  inside the confirmed branch), so the create-vs-update *behavior* is never
  wrong -- only the informational hint shown to the approving human can lag
  reality. No code fix for this in this round; see `write_wiki_page`'s own
  docstring for the same note.
- **Audit trail: mechanically enforced.** Every call to all three tools
  writes a structured JSON-lines audit record via
  `dispatch_core.build_audit_record`/`write_audit_record` (the same
  forbidden-key redaction as the main dispatch audit log applies here too),
  to its own file (`~/.agents/mcp-gitlab/audit.jsonl`, distinct from
  `dispatch_core`'s `~/.agents/mcp-dispatch/audit.jsonl`) -- covering every
  confirmation-requested / confirmed / denied / unavailable / ok outcome, not
  only final success. Records carry the tool name, task_id (when the tool's
  signature has one), gate_id/parent_issue_iid/issue_iid/slug identifiers, a
  content hash/length in place of raw wiki/comment body content, the returned
  GitLab artifact identifier (issue iid / comment id) on success, and the
  decision -- never the token, never a raw confirmation-token value, never
  wiki/comment/issue body content. Tested by `AuditTrailTests`.
- **Deliberate, stated scope boundary: `classification` is intentionally not
  an in-code parameter on any of this module's three tools.** Unlike
  the dispatch core's `dispatch_secure_cloud_role`/`dispatch_team`, this
  module performs no classification check at all. This is a recorded,
  human-accepted residual-risk decision, not an oversight: containment is
  achieved operationally, by pointing `GITLAB_BASE_URL`/`GITLAB_DOCS_PROJECT_ID`
  at a dedicated, docs-only GitLab project and issuing a least-privilege
  service token scoped to only that project, rather than by an in-code
  classification gate. See `gitlab.go`'s package doc for the same
  statement. If this integration is ever pointed at a project that also holds
  higher-classification content, this boundary must be revisited before that
  happens, not assumed to still hold. Both `GITLAB_BASE_URL` and
  `GITLAB_DOCS_PROJECT_ID` are `global_only` in `internal/config`'s
  field registry (see `roster/RUNBOOK.md`'s config-file section) specifically
  so this operational containment can't be silently weakened by a
  project-local `.agents/cadre.yaml` -- untrusted, clonable repository
  content -- redirecting either value.
- **`agent-autonomy.yaml`'s `gitlab_issue_or_comment_write: on_request` is
  deliberately advisory-only, not mechanically enforced in code.** Unlike
  `gitlab_wiki_write: human_approval` (mechanically enforced above via
  `ConfirmationGate`), `create_review_subtask` and `write_evidence_comment`
  have no in-code confirmation gate of their own -- an agent operating under
  this policy calls them directly, on its own judgment of when a task
  warrants it, without a mandatory round trip. This is consistent with how
  every other `on_request`-ranked entry in `agent-autonomy.yaml` is already
  handled: `repository.commit`, `repository.push`, and
  `create_gitlab_merge_request` are also `on_request` and also have no
  matching in-code gate anywhere in this suite's tooling (they are ordinary
  git/GitLab operations an agent performs directly when a task calls for it,
  not operations wrapped in a confirmation mechanism). Treating
  `gitlab_issue_or_comment_write` the same way follows that existing
  precedent rather than introducing a new, inconsistent enforcement tier for
  one entry. If this is ever revisited to add real gating (mirroring
  `write_wiki_page`'s `ConfirmationGate` reuse), that is a deliberate policy
  change, not a gap being quietly closed.

### Not covered above (MCP servers)

**M-2 (hash-pinning the `mcp` dependency in `requirements-mcp.txt`) is
closed, by removal rather than by pinning.** The Python MCP servers are gone
and `cadre mcp-dispatch-server` / `cadre mcp-gitlab-server` are served by the
Go CLI, so there is no `mcp` Python package to pin -- the dependency whose
integrity the item was about no longer exists in the distribution.

M-3 (verifying the `codex exec` invocation shape against a real `codex`
binary) remains open, and now applies to `spawnCodexChildIn` in
`internal/orchestration/dispatch_core_phase3_spawn.go`. It is tracked via a
dated `TODO` comment at that location in the source. Neither could be meaningfully
resolved in this sandbox (no network/package access to fetch a verified
package hash or invoke a real Codex CLI binary), and both were categorized
by the security reviewer as shippable with a tracked follow-up rather than
must-fix-before-merge.
