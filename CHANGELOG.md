# Changelog

This changelog tracks **consumer-visible** changes to what this suite ships:
new or changed `cadre` CLI subcommands and flags, new/changed provider and
profile artifact fields, and new backlog features landing. It does not
restate this repository's own internal commit history — see `git log` for
that. New adopters should start with the
[adopt-cadre quickstart](docs/adopt-cadre-quickstart.md) instead of this file.

Format loosely follows [Keep a Changelog](https://keepachangelog.com/).

**Note on the entries below the monorepo merge.** They describe an arrangement
that no longer exists: the packaged plugin used to live in its own repository
(`deagy/cadre-plugin`, later `deagy/cadre-lifecycle`), this repository's tags
tracked register-side changes only, and `release.yml` was not here — so tags
below that point were cut by hand. All four upstreams are now merged and
archived. The plugin is built in-tree, `.github/workflows/release.yml` cuts
both component lines, and tags are component-prefixed (`plugin-v*`,
`kernel-v*`) because the merge inherited 25 bare `v*` tags that an unprefixed
scheme would collide with — silently, matching the workflow's already-tagged
check and reporting "nothing to do". See
[`docs/migration/monorepo-migration.md`](docs/migration/monorepo-migration.md).

## [Unreleased]

### Added

- **Routing rules can now exclude paths, not just include them.** `routes[]` and `risk_rules[]` in `roster/orchestration/routing.yaml` accept an `exclude_paths` array alongside `paths`. It subtracts at the *file* level: a broad include glob can carve out the paths it was never meant to reach, while still matching on any other changed file in the same change set. ([#156](https://github.com/deagy/cadre/issues/156))

  Until now the only fix for a broad glob's false positive was to narrow the glob, which traded it for false negatives. `**/architecture/**` matched this repository's own `roster/architecture/` role definitions, so a prose tweak to one role summoned `cloud-architect` and `threat-modeler`; narrowing it to `architecture/**` + `docs/architecture/**` fixed that and silently stopped matching nested consuming-project paths like `services/payments/architecture/`. The broad glob is restored with `exclude_paths: ["roster/**"]`, which fixes the false positive *and* recovers the nested paths.

  `routing_health.py` now also fails when a rule's `exclude_paths` fully shadows one of its own `paths` globs. Such a glob is dead — the rule keeps its entry, its `reviewers` and any `human_gate`, but contributes nothing on paths and matches on keywords alone, previously with no signal from any check. The verdict is exact rather than sampled: `roster/orchestration/src/glob_containment.py` decides `L(paths[i]) ⊆ ⋃L(exclude_paths)` as a regular-language containment question, which this glob dialect makes decidable. A finding therefore means every path the glob could ever match is excluded — including shadowing by the *union* of several exclusions that no individual one achieves. A pattern the decision procedure cannot settle within its state budget is skipped rather than reported, so its only imprecision is a missed finding. No rule in this repository is currently shadowed. ([#162](https://github.com/deagy/cadre/issues/162))

  **The `backend` route's `**/*.py` asymmetry is deliberately not restored.** It could be, mechanically — but only by excluding `roster/**`, `plugin/**`, `engine/**`, `kernel/**`, `cadre_cli/**` and `bin/**`, and `engine/`, `plugin/` and `bin/` are perfectly ordinary directory names in a consuming project, whose Python would then drop out of routing with no signal. That trades a documented asymmetry for a hidden one. `roster/**` is a single, distinctly-Cadre name, which is why the architecture case is safe and this one is not.


### Changed

- **The Cline agent-dispatch plugin no longer ships a default model provider, and no longer requires `ANTHROPIC_API_KEY`.** Every bundled preset carried `providerId: anthropic` and a vendor-qualified `modelId`, and the runtime fell back to the literal `"anthropic"` in two more places, so automatic dispatch selected Anthropic regardless of how Cline itself was configured — prompting for its credential, or, where that credential happened to exist, silently using it. Presets now carry only the capability tier (`opus`/`sonnet`/`haiku`), which is this suite's own domain knowledge; the provider and the concrete model serving a tier are operator configuration resolved at dispatch time, via `CLINE_AGENTS_PROVIDER_ID` and `CLINE_AGENTS_MODEL_OPUS`/`_SONNET`/`_HAIKU` (or a single `CLINE_AGENTS_MODEL_DEFAULT`). `dispatch_selected_roles` gains matching per-call `providerId`/`modelId` overrides, which `start_subagent` already had. ([#142](https://github.com/deagy/cadre/issues/142))

  **Action required if you use `cline-agents` dispatch.** With nothing configured, dispatch now fails before any session starts, naming the missing variable, rather than falling back to a vendor. `cline-plugins/cline-agents/README.md` carries a copy-pasteable block reproducing the previous behaviour exactly. There is deliberately no transition period during which the old default still applies — that would reinstate the surprise being removed.

  This also matters beyond model choice: `dispatch_selected_roles` retrieves knowledge-store content into a role's instructions, which becomes part of the outbound prompt. An implicitly selected provider meant the one subsystem here with explicit classification and tenancy rules could emit content to a provider the operator never chose.

- **`cadre select` plans now say *why* each route matched.** `matched_routes` changes from an array of route-id strings to an array of `{id, reasons}` objects — the same shape `matched_risks` already used — where `reasons` names the literal `keywords`, conjunctive `keyword_groups`, and `paths` (`pattern`/`file` pairs) that fired. The selector already computed this for every matched route and discarded it one line after computing it, so `matched_risks` could explain itself and `matched_routes` could not: answering "why did this route fire?" meant reading `routing.yaml` and `routing.py` rather than the plan. Both fields now resolve to one `$defs/idWithReasonsArray` in `selection.schema.json`, so the two shapes cannot drift apart.

  **`schema_version` goes 3 → 4. This is a breaking change to `matched_routes`.** Code that treated it as a list of strings must now read `route["id"]` — for example `set(plan["matched_routes"])` becomes `{r["id"] for r in plan["matched_routes"]}`. Beyond the retype, `selection.schema.json` is closed (`additionalProperties: false`) and vendored into both the pip wheel and the plugin distribution, so a consumer validating against the copy they installed must update it alongside the CLI. Plans archived under `schema_version: 3` stay readable as v3 documents and should be validated against a v3 schema.

  This also corrects the reasoning used for `dispatch_disposition` below, which was added to `required` without a version bump and described as "additive and non-breaking." That held for readers of the producer's output but not for a pinned schema copy — on a closed, separately-shipped schema there is no purely additive field. `RUNBOOK.md` now records when `schema_version` increments so this does not recur.

  **One further consequence:** the reasons ride inside the fingerprinted payload, so every plan's `dispatch_fingerprint` changes. That is what a change to the emitted field set is supposed to do — fingerprints are comparable only between plans from the same producer version — and it is not a determinism regression.

  Telemetry records are unaffected in shape: they continue to store bare route/risk ids. `reasons.paths[].file` entries are changed-file paths, and records are limited by design to structural facts about the outcome so raw file paths stay out of a plaintext local log (`RUNBOOK.md`).

## [0.17.0] - 2026-08-08

Shipped to users as plugin [v0.14.0](https://github.com/deagy/cadre/releases/tag/plugin-v0.14.0) — the packaged plugin is how register-side changes reach an installed runner.

### Added

- **Three new roles, taking the catalog from 71 to 74.** Each fills a gap an existing role explicitly disclaimed, rather than widening one:

  - **`ai-engineer`** (`build` / `code_author`) — a consuming project's model-facing layer: model and provider selection, prompt and agent design, retrieval, eval harnesses, and inference cost/latency. Nothing covered this: `knowledge-store-steward` operates *this suite's own* vectorized store and `agent-performance-evaluator` assesses *this catalog's own* roles, and `ai-engineer`'s `## Authority` names both so the boundary is in the role, not just in the changelog.
  - **`visual-designer`** (`design` / `document_author`) — design tokens, component specifications, and usage rules. `interaction-designer` ends its own scope statement with "not the visual system," and `frontend-engineer` is forbidden to select a component library or styling system while those remain unresolved; the visual system sat in the gap both name.
  - **`delivery-sequencer`** (`planning` / `document_author`) — dependency map, critical path, and sequencing. `premortem` already listed "the assumption register, capacity model, and dependency map" as inputs and nothing produced the third. It owns order and prerequisites only and may not set priority, dates, scope, or risk tolerance.

- **`roster/shared/technology-standards.md` gains an AI/ML product-features section, and `team-profile.yaml` an `ai` block.** Model provider, eval framework, and vector store are recorded `not_yet_selected`, so a role must present alternatives rather than choose. The section also records the load-bearing constraints: model output is untrusted data, an eval baseline precedes a prompt or model change, and model output never authorizes a privileged action on its own. **Consumer impact:** shared policy is embedded verbatim into every generated wrapper, so all 74 wrappers change even though 73 roles' own definitions did not.

- **A `version-control-workflow` skill** (12 skills, up from 11) — branching, merge vs rebase, history repair, conflict resolution, and PR/MR hygiene across both forges. Deliberately a skill and not a role: procedural know-how is not an accountability boundary. Note `agent-version-control` is a false friend — it tracks role-definition provenance, not git.

### Fixed

- **A task changing a GitHub Actions workflow selected no primary agent at all.** `routing.yaml`'s `pipeline` route carried only GitLab paths, so `.github/workflows/**` matched nothing build-shaped, while the identical task on `.gitlab-ci.yml` selected `cicd-engineer` + `pipeline-security-reviewer`. The route now covers `.github/workflows/**` and `.github/actions/**` with matching keywords, and a selector test asserts the two forges staff *identically* for the same task, so this cannot regress to one-forge coverage silently. `cicd-engineer` and `pipeline-security-reviewer` no longer hardcode GitLab either: both now require establishing which forge applies and reviewing against that forge's own controls, with an explicit warning not to carry a control's name across — job permission scoping, environment approval, and workload identity federation differ materially. Neither role's authority widened.

- **Python work matched no route.** The `backend` route now carries a `python` keyword. **Deliberately not a `**/*.py` path glob**, unlike its `**/*.go` counterpart: this repository is itself Python, and the glob cross-matched its own orchestration source — already correctly routed to `application-engineer` — adding `backend-engineer` as a spurious second primary. The routing schema has no exclusion mechanism, so the asymmetry is the honest fix and is pinned by a test asserting both halves.

### Changed

- **The `pipeline` route's bare `pipeline` keyword is narrowed to compounds** (`ci pipeline`, `build pipeline`, `delivery pipeline`, `deployment pipeline`, `release pipeline`). It previously matched *any* pipeline — data, ETL, or RAG — which is why "build a RAG pipeline with an LLM provider" returned a confident, fully-staffed plan naming `cicd-engineer`. **Consumer impact:** a task whose text says only "pipeline" with no CI-shaped file no longer selects `cicd-engineer`; say "ci pipeline" or change a pipeline file. This is the one change here that can *remove* an agent from an existing task's plan.

- **BREAKING (behavioral): write-capable Cadre roles now default to working in a dedicated `git worktree` instead of the caller's main working tree.** A new shared policy, `roster/shared/workspace-isolation.md`, is embedded into every role's generated wrapper instructions via `SHARED_POLICIES`; its worktree-isolation steps apply to the four capability tiers with `sandbox_mode != "read-only"` (`document_author`, `code_author`, `test_author`, `environment_operator`, derived from `roster/runner-capabilities.json`, not hardcoded), and the file's applicability header says so. It instructs an agent to check whether it is already inside a linked worktree, and if not and conditions allow (`agent-autonomy.yaml`'s `repository.create_local_branch_or_worktree: allowed`, no dirty paths intersecting the task's scope), create one in-root at `<repository_root>/.worktrees/<task-id>/<role-id>/` and work there, reporting the path/branch in its result. This is **prompt policy plus an orchestrator dispatch-contract expectation, not a mechanically enforced gate** — `roster/orchestration/mcp/dispatch_core.py` is unchanged, and no `agent-autonomy.yaml` value moved (`commit: on_request`, `push: on_request`, `merge: never`, and `create_local_branch_or_worktree: allowed` are all exactly as they were; this loosens no permission, it only changes where allowed edits land by default).

  **Consumer impact:** a project that dispatches these roles and then inspects its main working tree for changes will see none — the changes land in `.worktrees/<task-id>/<role-id>/` on a new branch instead, and must be discovered, reviewed, and merged from there. Projects with existing tooling that assumes edits appear directly in the dispatching working tree should account for this before adopting the updated role wrappers.

  **Opt-out:** a project that wants the previous in-place-only behavior can narrow `repository.create_local_branch_or_worktree` in its own `.agents/shared/agent-autonomy.yaml` overlay from `allowed` to `never`, `human_approval`, or `on_request` — legitimate under `agent-autonomy.yaml`'s narrowing-only merge rule (moving from rank 0 to rank 3/8/10 is a tightening, not a loosening). With the value narrowed, `workspace-isolation.md`'s Step 1 condition fails and every write-capable role degrades explicitly to in-place edits instead. `cadre init`'s RG-B flow already surfaces this key — it walks every leaf of `agent-autonomy.yaml` dynamically rather than using a fixed allowlist — so it is discoverable without hand-authoring the overlay. No code change was needed for that; it is called out here only because this change makes the key newly relevant.

- **`.gitignore` now ignores `/.worktrees/` (this repository's own in-root worktree convention) and `/.claude/worktrees/`** (Claude Code's own worktree-isolation feature, which writes directly into the project tree and was not ignored before — a pre-existing latent defect: `cadre select`'s git-status mode folds untracked paths into `inputs.changed_files`, so a populated, untracked, in-repo worktree could get swept into selection input and re-route dispatch around its own scratch content).

- **`roster/shared/workspace-isolation.md` gains a "Never mutate a working tree you did not create" section, binding every capability tier**, and the file moves to `SHARED_POLICIES` so it reaches all 74 roles rather than write-capable tiers only. It forbids `git` operations that discard uncommitted work or move a branch off commits it already had (`reset --hard`, `checkout`/`switch` leaving dirty state, `restore`, `stash`, `clean -f`, `branch -f/-D`, `rebase`, force-push) in a tree the agent did not create, and gives the read-only alternatives that need no mutation (`git diff main...HEAD`, `git show <ref>:<path>`, `gh pr diff`, or a `--detach` inspection worktree). `roster/shared/agent-autonomy.yaml` gains the matching `repository.discard_uncommitted_work_or_move_branches: never`. Prompted by a dispatched write-capable role that ran `git reset --hard main` to read a branch's diff, moved the branch off an unpushed commit, and truthfully reported that it had made no edits — it never touched a file. `generate_global_plugin.py`'s `TIER_SCOPED_POLICIES` mechanism remains but is now empty.

- **`roster/shared/README.md`** documents the tier-scoped shared-policy mechanism and its asymmetry: `TIER_SCOPED_POLICIES` gates what the *generated wrapper* embeds per capability tier, but `cadre resolve-shared <filename>` remains filename-only and returns a file's full text regardless of the caller's tier — a tier-scoped file must state its own applicability in its own text for that reason.

- **`.agents/skills/run-agent-orchestration/`**: `references/dispatch-contract.md` now requires the same end-of-task workspace-isolation result block `workspace-isolation.md` defines, and `SKILL.md`'s "Consolidate Results" step relays it; `references/runner-adapters.md` gains per-runner isolation notes (Claude Code's worktree-isolation feature and Codex's `--cd`/`--sandbox workspace-write` path scoping), flagged where the exact behavior is not independently verified against runner documentation.

- **`roster/runner-capabilities.json`** gains a `native_workspace_isolation` field per runner (`"worktree"` for `claude-code`, `null` for `codex` and `cline`), documenting which runners natively support workspace isolation as a launch parameter. Build-time only — no runtime code currently reads it.

- **`roster/RUNBOOK.md`** gains an operator section on discovering, inspecting, and removing agent-created worktrees (`git worktree list`, `git worktree remove`, `git worktree prune` — operator-run only, never self-run by an agent per `workspace-isolation.md`).

## [0.16.0] - 2026-08-07

### Added

- **`settings.disable_project_tier_cwd_fallback()`**, for processes whose working directory is not a project anchor. Without an explicit `start=`, the project tier is discovered by walking up from `Path.cwd()` — right for a CLI a human ran inside a project, wrong for a long-lived, project-agnostic one. Both stdio MCP servers now call it at import (alongside the existing `disable_interactive()`), so an unrelated checkout's `.agents/cadre.yaml` can no longer steer a tool call it has nothing to do with. It suppresses only the *implicit* anchor; an explicit `start=` is always honored, and scope violations still raise through it.

- **`docs/examples/role-selection-workflow.md`**, an end-to-end walkthrough from task to dispatched agents, linked from a new README "Examples" section. Also adds `bin/README.md` documenting the CLI entry points, a Dependabot configuration, and a pre-commit configuration.

- The pip/pipx distribution channel's own version (`cadre_cli/_version.py`) moves to `0.3.0`. This is a separate version line from this repository's tags and from the packaged plugin's version, as that file's docstring records.

### Fixed

- **Operator settings resolved the project tier from the process's working directory instead of the project being acted on.** No call site passed `start=`, so every project-tier lookup guessed from cwd — including `dispatch_core`'s runner-binary resolution, which already receives a validated `project_root` for the dispatch and ignored it. Both `build_claude_child_argv` and `build_child_argv` now anchor to that `project_root`. Impact was bounded (every runner/sdlc/knowledge-store field is `global_only`, and `gitlab.supports_work_item_hierarchy` is currently the only `project_or_global` field), but the pattern was established repo-wide and would have become a live bug the moment a project-scoped field mattered to dispatch.

- **Executable-valued settings accepted a leading `-`, which the program that runs them parses as an option rather than a command.** Verified under bash: `bin="-a"; exec "$bin" --provider p.json --version` makes `exec` consume `--provider` as `-a`'s argv[0] argument and then attempt to execute `p.json`, so an inert-looking value silently reinterprets the rest of the command line. dash's `exec` has no `-a` and merely fails, but the packaged `bin/cadre` wrapper is `#!/bin/sh`, which is bash on some systems. Executable and path fields also now reject embedded control characters — a newline in particular breaks `cadre config resolve`'s contract of printing one value on stdout, which that wrapper captures with `$(...)`. Internal spaces remain legal by design (`/opt/My Tools/bin/x` is a real path, and every consumer quotes the value).

- **Secret-shaped-key rejection walked mappings only, not sequences.** A key like `gitlab.extra[0].token` was never scanned. No registered field is list-shaped, so such a key could not be resolved — but `write_setting`'s "preserve unknown keys" merge would have round-tripped it into every later rewrite of the file, silently persisting a pasted credential that `settings.py` promises is never stored.

### Changed

- **`roster/shared/README.md` now reconciles the three project-local mechanisms under `.agents/`**, which look interchangeable and are not: `.agents/shared/<filename>` policy overlays (trusted, deep-merged, narrowing-only for autonomy), `.agents/knowledge-store/config.json` (its *presence* selects a security-relevant tier), and `.agents/cadre.yaml` (untrusted — it arrives with `git clone` — first-wins, with `global_only` fields rejected outright). Records why the asymmetry is deliberate and why the knowledge-store config is not folded into the new one.

## [0.15.0] - 2026-08-07

### Added

- **Cadre is now configurable from a YAML (or JSON) config file, not environment variables alone.** A new resolver, `roster/shared/src/settings.py`, replaces the scattered `os.environ.get(...)` calls that previously read operator settings, with one precedence chain applied independently per field: environment variable > project-local `.agents/cadre.yaml` > user-global `${XDG_CONFIG_HOME:-~/.config}/cadre/config.yaml` > built-in default > interactive prompt (which offers to write the file) > fail-closed error naming every source it checked. Existing environment-variable-only setups keep working unchanged — the file is a lower-precedence addition, never a replacement. Covers `GITLAB_BASE_URL`, `GITLAB_DOCS_PROJECT_ID`, `GITLAB_SUPPORTS_WORK_ITEM_HIERARCHY`, `AGENTIC_SDLC_BIN`, `SECURE_CLOUD_AGENTS_CLAUDE_BIN`, `SECURE_CLOUD_AGENTS_CODEX_BIN`, and `KNOWLEDGE_STORE_HOME`. PyYAML stays an optional dependency: it is imported lazily, only when a `.yaml` file actually exists, and a `.json` sibling is accepted at either location (PRs #108, #109).

  Two properties are security-critical rather than conveniences. **Secrets are never read from or written to any config file** — `GITLAB_SVC_TOKEN` and the knowledge-store embedding API key remain environment-only, the interactive prompt refuses to collect them, and a secret-shaped key (`*_token`, `*.api_key`, `*.password`, …) appearing in a config file is rejected at load without echoing its value. And **a project-local `.agents/cadre.yaml` is treated as untrusted content**, because it arrives with `git clone` and is editable by anyone who can open a pull request: fields that select an executable to spawn (`runners.claude_bin`, `runners.codex_bin`, `agentic_sdlc.bin_path`), a data-store location (`knowledge_store.home`), or a token-receiving destination (`gitlab.base_url`, `gitlab.project_id`) are `global_only` and may come only from an environment variable or the user-global file. A project-local file setting one raises a hard error rather than being silently ignored — this is what prevents cloning a repository from redirecting an agent's runner binary, its knowledge database, or its GitLab service token. The project-local read path also refuses a symlink resolving outside the project, matching the write path's existing containment check.

- **New `cadre config` subcommand** for diagnosing where a setting's value came from: `cadre config show` lists every known setting with its resolved value, origin tier, and source file (declared secrets print as `env-only: NAME (set|not set)`, never a value); `cadre config path` prints both candidate config file locations; `cadre config resolve <key>` prints a single non-secret setting's value for shell consumption. A new leading `cadre --interactive <subcommand>` flag opts the dispatched subcommand into prompting for a missing setting; without it (or without a real terminal) a missing required setting fails closed with an actionable message instead of blocking on input, so CI never hangs (PRs #108, #109).

- **Opt-in asynchronous dispatch (`wait=False`) for the MCP dispatch tools**, with new `poll_dispatch_status`/`poll_team_status` tools to retrieve the result. MCP clients with a short, non-configurable client-side `tools/call` timeout (confirmed: Cline's `@cline/core`, hardcoded 5000 ms) previously could never receive a `dispatch_secure_cloud_role`/`dispatch_team` result even when the dispatch itself succeeded, because the child process can legitimately run for minutes. The default remains `wait=True`, so Codex CLI callers see no behavior change. When `wait=False`, the confirmation gate and concurrency limiter still run synchronously and return immediately; only the child process moves to a background thread, tracked in a TTL-purged job store, and the eventual result is returned in exactly the shape the synchronous path already produces (PRs #101, #102).

- **New `cadre gitlab-evidence` subcommand**, a thin argv-in/JSON-out CLI over the same safety-audited `gitlab_core.py` functions the GitLab evidence MCP server exposes (`create-review-subtask`, `write-wiki-page`, `write-evidence-comment`). This lets a subprocess-capable caller without an MCP client — such as the `cline-agents` plugin in `deagy/cadre-lifecycle` — reach the identical validation, quick-action neutralization, wiki-write confirmation gate, and audit logging, rather than that logic being reimplemented in a second language (PR #103).

- **`run-agent-orchestration`'s "Dispatch in Waves" section now warns against scoping a role to an entire large codebase.** Investigating #68 (two agents, `security-reviewer` and `supply-chain-security-reviewer`, timed out during a full-repository codebase review) found no config-level cause — every dispatched role that day shared the identical `model`/`reasoning_effort`/`capability` tier, and this repository exposes no timeout knob for that dispatch path at all (the one configurable timeout, `dispatch_core.py`'s `spawn_and_wait()`, is for a separate external MCP dispatch path with a different job-ID scheme, not what produced this incident's `run_00003`/`run_00006` identifiers). The skill now recommends splitting a whole-repository review into narrower per-subsystem or per-directory waves instead.

### Fixed

- **Audit-log write failures on the async dispatch path are no longer completely silent.** `_write_audit_record_best_effort()` swallowed them outright, contradicting `SECURITY-CONTROLS.md`'s "mechanically enforced" audit-logging claim for this module. It now falls back to a stderr trace (decision, job/team id, exception) so an operator has something to grep for when the primary write fails, and the async path's best-effort contract is documented explicitly as weaker than the synchronous path's guarantee. The stderr write is itself wrapped, so a broken or closed stderr can never turn a logging failure into a dispatch failure (PR #102).

- **`cadre generate-plugin --output` could silently clobber a downstream package's hand-authored README.md.** The existing guard against overwriting a non-empty `--output` directory only checked for the *presence* of a `.codex-plugin/plugin.json` marker, not whether the target's declared identity actually matched what this generator would produce — so it passed trivially for a repository that is both a legitimate regeneration target and its own initialized downstream package (e.g. `deagy/cadre-lifecycle`, which merges Cadre with Agentic SDLC/Cline/LangGraph and hand-authors its own README describing that identity), and generation proceeded straight to an unconditional README.md write. `generate-plugin --output` now leaves README.md untouched whenever the target already has a `.codex-plugin/plugin.json`; `--check` correspondingly excludes README.md from its drift comparison in that case. Pass the new `--force-readme` flag to write this generator's own README.md over an existing marker anyway (fixes #97).

## [0.14.0] - 2026-08-06

### Added

- **This repository now notifies `deagy/cadre-lifecycle` on every release
  tag** (`.github/workflows/notify-lifecycle.yml`, triggered on `push:
  tags: v*`), via a `repository_dispatch` call authenticated with a
  cross-repo PAT secret. That repository's own `regenerate.yml` workflow
  responds by regenerating its packaged plugin content and opening a PR for
  review — automating the "Regenerating Assets" procedure this repository's
  own "Releasing" section already documented as a manual step. Soft-skips
  (warns, doesn't fail this repository's own release) if the secret isn't
  provisioned. See `deagy/cadre-lifecycle`'s own CHANGELOG (0.9.0) for the
  receiving half.

### Changed

- **Documentation restructured across the README and `roster/` docs**:
  deduplicated repeated explanations, converted prose into tables where a
  table reads faster, and added Mermaid diagrams verified against the
  actual `routing.yaml`/`aides.yaml` content they describe (rather than
  hand-drawn approximations). No behavioral change — this suite's actual
  routing, selection, and dispatch logic is unaffected.

## [0.13.0] - 2026-08-05

### Added

- **GitLab evidence MCP server** (`roster/orchestration/mcp/gitlab_core.py`
  + `gitlab_server.py`), mirroring the existing dispatch MCP server's
  architecture. Exposes three create-only tools —
  `create_review_subtask`, `write_wiki_page`, `write_evidence_comment` — so
  any agent (in this repo or a consuming project) can create GitLab
  review/approval issues as subtasks of a parent issue, write durable
  documentation to a project wiki, and attach size-capped per-task evidence
  comments. Recognizes exactly one service-account token env var
  (`GITLAB_SVC_TOKEN`, no aliases). GitLab issues/wiki pages are evidence
  pointers only, never gate authority — no code path can close, approve, or
  transition issue state, and caller-supplied text is neutralized against
  GitLab quick-action injection before it ever reaches a request body.
  New `agent-autonomy.yaml` mutation entries
  (`gitlab_issue_or_comment_write`, `gitlab_wiki_write`,
  `gitlab_approval_issue_state_change`), a `routing.yaml` route, a redacted
  audit trail, and operator/security documentation
  (`roster/orchestration/mcp/GITLAB-EVIDENCE.md`,
  `SECURITY-CONTROLS.md`). See PR #98.

### Changed

- **`run-agent-orchestration`'s proactive trigger broadened.** Its skill
  description previously only matched requests explicitly phrased as
  orchestration/dispatch/review, so a runner without an explicit
  `/run-agent-orchestration` invocation would only pick it up when a user
  happened to use that vocabulary. Broadened to cover any non-trivial
  engineering task — implementation, bug fixes, reviews, planning, design,
  testing, security, compliance, CI/CD, infrastructure, release, or
  knowledge-store work — while keeping an explicit floor so genuinely
  trivial changes (a typo, a single config value, a version bump) and pure
  read-only lookups still get handled directly instead of triggering full
  agent dispatch. Both canonical copies (`.agents/skills/` and the
  `.claude/skills/` pointer's own frontmatter) were updated together.

- **The packaged plugin distributions moved to their own repository,**
  [`deagy/cadre-plugin`](https://github.com/deagy/cadre-plugin). This repository is now purely the agent *register*: role
  definitions, the catalog, routing, orchestration tooling, the knowledge
  store, and the `provider/` bundle. The generated Claude Code / Codex
  package (formerly `plugins/cadre/`) and the hand-authored Cline CLI plugin
  (formerly `plugins/cline/`) are maintained and released there, with full
  file history preserved through the move.

  What this changes for consumers:

  - **Installing the plugin** now points at a `deagy/cadre-plugin` checkout
    rather than this one: `codex plugin marketplace add /path/to/cadre-plugin`
    (likewise `/plugin marketplace add`). The plugin's own version continues
    from `0.12.1` rather than resetting, so existing installs read it as a
    continuation.
  - **`cadre generate-plugin` now requires `--output`**, naming a
    `deagy/cadre-plugin` checkout to regenerate. Invoked without it, it exits
    with an explicit error rather than creating a stray `plugins/` directory.
    Drift between the two repositories is guarded by the plugin repository's
    CI against the register revision pinned in its `cadre-ref.txt`.
  - **`cadre version` was removed** from this repository's CLI. It read the
    plugin manifests, which now live in the plugin repository; the equivalent
    is `python3 tools/plugin_version.py` there.
  - **`cadre sdlc init --profile secure-cloud` keeps producing full role
    content.** `agent-catalog.json`'s `definition` values are resolved by the
    kernel relative to whichever copy of that file it reads, and it rejects
    paths escaping that directory — so the register and the package need
    different spellings. `provider/roles/` holds register-side copies of every
    role's `AGENT.md`, and the package's catalog is rewritten to point at its
    own `suite/roster/`. Without this the kernel falls back silently to
    one-line generic role instructions. This also fixes the pip/pipx
    distribution, which never shipped resolvable definitions at all.

  - **New `provider/` directory**: `provider.json`, `profiles/`,
    `extensions/`, `agent-catalog.json`, and `codex-agents/` moved here from
    `plugins/cadre/`, and are register-owned. `cadre sdlc` and `cadre
    bootstrap-codex` read them from this location, and the pip/pipx
    distribution vendors them directly, so both keep working from an install
    with no plugin checkout. The last two are generated: `cadre
    generate-role-metadata` now writes and drift-checks them alongside
    `roster/catalog.yaml`.

### Added

- **`cadre profile diff`** (idea #4): a new, read-only subcommand that
  compares a consuming project's copy of `provider.json` /
  `profiles/<id>/profile.json` against this checkout's current canonical
  versions and classifies each artifact as `current`, `stale-unmodified`,
  `diverged`, `copy-invalid`, or `provenance-undetermined`, naming every
  differing field in one pass. Matters to consumers because it turns "did our
  copy drift from upstream?" from a manual diff into a scriptable check —
  without ever re-syncing your project or reading/interpreting your
  project's `.agentic-sdlc/` gate-approval state. See
  [roster/RUNBOOK.md](roster/RUNBOOK.md) §16.1 and the
  [quickstart](docs/adopt-cadre-quickstart.md#6-check-for-drift-against-this-suites-canonical-profile-later).

- **Project-local routing overlay** (idea #6): a consuming project can now
  add `.agents/orchestration/routing-overlay.json` to add routes, risk
  rules, and team recipes, or widen an existing rule's matching keywords,
  without forking `orchestration/routing.yaml`. Every safety-relevant field
  on an existing base entry (`human_gate`, `reviewers`, `quality_gates`,
  `primary`, `support`) stays immutable; new entries are additive-only. Run
  `python3 roster/orchestration/src/routing_overlay.py --check` to validate,
  or `--out <path>` to materialize the effective merged configuration. See
  the [quickstart](docs/adopt-cadre-quickstart.md#5-add-a-project-local-routing-overlay-optional).

- **Declarative runner capability manifest** (idea #8): `roster/runner-capabilities.json`
  (validated by `roster/runner-capabilities.schema.json`) is now the single
  source of truth for per-runner (Claude Code, Codex, Cline) capability
  tiers, model-tier mappings, and structural divergence facts, generated
  into the packaged plugin instead of hand-duplicated across three
  generator files. Consumers reading `plugins/cadre/` output get the same
  data, now guaranteed consistent by construction. Validate with
  `python3 roster/orchestration/src/validate_runner_capabilities.py`.

- **`provenance` field on dispatch plans** (idea #7): `cadre select`'s
  emitted plan now optionally carries a `provenance` object — `sha256`
  content hashes of the exact `catalog.yaml`/`routing.yaml` bytes used, and
  best-effort `git_commit_sha`/`git_dirty_paths` for those two files — so a
  reviewer with independent repository access can verify exactly which
  suite-input content produced a given plan. **Additive and non-breaking**:
  `provenance` is optional in `selection.schema.json` (not in the schema's
  `required` array), excluded from the existing `dispatch_fingerprint`
  computation, and absent entirely on any code path that doesn't supply
  `catalog_path`/`routing_path` — plans generated before this field existed,
  and any caller not touching that path, keep validating unchanged. Recording
  provenance is never itself an approval.

- **`cadre profile diff` and the routing overlay build on two already-shipped
  features they depend on**: strict JSON Schema validation of
  `catalog.yaml`/`routing.yaml` (idea #10, `roster/catalog.schema.json`,
  `roster/orchestration/routing.schema.json`, run via
  `python3 roster/orchestration/src/schema_validate.py`) and the routing
  coverage/orphan linter, selection golden-corpus regression harness, and
  full migration of role metadata to `AGENT.md` YAML frontmatter (ideas
  #1-#3). The frontmatter migration (idea #3) is the more consequential
  change for consumers who parse role metadata directly: `roster/catalog.yaml`
  and `orchestration/routing.yaml`'s `knowledge_focus` block are now fully
  *generated* output (`cadre generate-role-metadata`, `--check` for drift
  detection) derived from each role's `AGENT.md` frontmatter — their
  on-disk shape and field values are unchanged (verified as a zero-drift
  migration across all 47 roles), so no consumer-facing action is required,
  but hand-editing either file directly no longer has any effect once it's
  regenerated.

- **pip/pipx-installable `cadre` distribution**: `pyproject.toml` now
  packages the CLI so `pipx install dist/cadre-*.whl` (built locally; not
  yet published to PyPI) puts a `cadre` console script directly on `PATH`
  with no repository checkout required at runtime, as an additional channel
  alongside the existing `./bin/cadre` checkout path (which keeps working
  identically). `cadre generate-plugin` and `cadre generate-authority-aides`
  remain checkout-only, since they read and write generated artifacts;
  `cadre generate-role-metadata --check` works from an install but its write
  mode is checkout-only for the same reason. Every other subcommand, including
  `cadre select` and `cadre sdlc`, works fully from the installed
  distribution. Optional `[yaml]`/`[mcp]` extras keep a bare `pip install
  cadre` dependency-light.

- **`dispatch_disposition` field on dispatch plans** (fixes #45): every
  `cadre select` plan now carries `dispatch_disposition: {status, reason}`,
  where `status` is `staffed` (a primary and/or reviewer role was selected),
  `advisory-only` (only `agents.support` was populated — e.g. via
  `routing.yaml`'s generic `change_intake` keywords or a default gate review
  agent — with no primary or reviewer matched), or `no-agents-selected`
  (nothing matched; `needs-triage`). Matters to consumers because a
  support-only selection used to be indistinguishable in the plan from a
  fully-staffed one, so an orchestrator had no structured signal before
  silently performing a destructive or persistent-environment action itself
  instead of dispatching or reporting why nothing was dispatched. The
  `run-agent-orchestration` skill now requires checking this field before
  dispatch and reporting its status in every final summary. **Additive and
  non-breaking**: a new required field in `selection.schema.json`, but every
  plan already always populated `agents.primary`/`reviewers`/`support`, so
  the field is deterministically derivable and always present.

### Fixed

- **pip wheel was missing `roster/runner-capabilities.json`**: `cadre
  generate-role-metadata --check` (an installed-must-work subcommand)
  crashed with a raw traceback from a pip/pipx install, because
  `pyproject.toml`'s wheel `force-include` list never vendored this
  manifest (only the sdist's `roster/**` wildcard covered it). Every other
  pip-installed subcommand was unaffected.

### Changed

- **Knowledge-store scope is now enforced, not just conventional, at the
  global-fallback config tier** (idea #9): when a `cadre knowledge` command
  resolves to the shared, cross-project store (no explicit `--config`, no
  project-local `.agents/knowledge-store/config.json` found), `search` and
  `context` now require exactly one of `--source <value>` or the new
  `--all-sources` opt-in flag, and `ingest` now requires an explicit
  `--source` instead of silently defaulting to the generic `"chat-export"`
  identity. **This is a breaking change only for scripts that invoke
  `cadre knowledge search`/`context`/`ingest` against the shared global
  store without `--source`** — they will now fail closed with a clear error
  instead of silently retrieving/ingesting across every project on the
  machine. Project-local stores and an explicit `--config` are unaffected;
  `--source` remains fully optional there. `cadre select`'s own
  knowledge-retrieval path already always supplied an explicit `--source`
  and needed no changes.

## Earlier history

Releases before this changelog existed are not individually itemized here.
See `git log` and each tagged `vMAJOR.MINOR.PATCH` release's GitHub Release
notes (published automatically by `.github/workflows/release.yml` once a
version-bump PR merges) for that history, starting from v0.3.0 (the first tag
following this repository's current versioning scheme).
