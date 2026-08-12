# Changelog

This changelog tracks **consumer-visible** changes to the packaged plugins:
what installing `cadre@cadre-lifecycle-team` (and its optional companions)
gives you, and how this repository is built and released. Changes to the
roles, skills, routing, and CLI behaviour
*inside* the package are recorded in the register repository's own changelog
([`deagy/cadre`](https://github.com/deagy/cadre/blob/main/CHANGELOG.md)) —
this file does not restate them.

Format loosely follows [Keep a Changelog](https://keepachangelog.com/). The
release convention (see `README.md`'s "Releasing" section) ties git tags
(`vMAJOR.MINOR.PATCH`) to a deliberate, reviewed version bump of
`.claude-plugin/plugin.json` / `.codex-plugin/plugin.json`, checked with
`python3 tools/plugin_version.py --check`/`--set`. Each version heading
below links to its [GitHub Release](https://github.com/deagy/cadre/releases).

## [0.22.0](https://github.com/deagy/cadre/releases/tag/plugin-v0.22.0) - 2026-08-12

### Added

- **The `cline-lifecycle` plugin now reaches full parity with the Claude Code / Codex lifecycle skill surface, at the flag level and not just the subcommand level.** Two new tools, `sdlc_link_intent_from_github_issue` and `sdlc_link_requirements_from_github_issue`, mirror `link-source-issue-github` — the last unmirrored forge-specific skill, now reachable because `provider.json`'s `kernel_compatibility.minimum` (0.13.2) sits past the kernel release that introduced the two underlying subcommands. Four previously unreachable flags are also exposed across the four apply-gate tools, all defaulting to off as the kernel does: `reconcileAssignees` (`sdlc_create_gate_issues_gitlab`, `sdlc_create_github_gate_issues` — the human-authorized remediation for forge assignee drift; a Cline session could previously see the drift reported but never correct it), `includeScope` (same two tools), `linkType: "relates_to"` (`sdlc_create_gate_issues_gitlab` only), and `breakLock` (all four apply-gate tools). A tool's `.strict()` input schema makes an undocumented flag not just unreachable in practice but rejected outright, so this closes a real capability gap, not just a documentation one.

### Changed

- **Dispatched retrieval now scopes to both knowledge sources.** `cadre knowledge ingest-accepted` writes steward-accepted findings to a dedicated `proposed-knowledge` source, but a generated dispatch plan previously scoped retrieval to the repository's own source alone, so no dispatched agent ever named the other one. `--source` is now repeatable on `cadre knowledge search`/`context`, and an emitted plan carries one `--source` entry per source rather than falling back to `--all-sources`.

- **Cline agent dispatch (`cline-agents`) now falls back to the parent session's own active model** when neither a per-call override, an operator environment variable, nor a preset pin resolves `providerId`/`modelId`. This is not a shipped default choosing a vendor on the operator's behalf — it inherits whatever model the operator's already-running Cline session is actively using, read from the most recent assistant message carrying `modelInfo`. Applied only when neither field resolved through a more specific signal, preserving the existing atomic-pair behavior: a half-configured operator environment still fails closed.

- **A knowledge proposal can no longer approve itself.** `cadre knowledge propose --from-finding -` is now usable by any dispatched agent directly, not only by orchestration consolidation — but a caller-asserted `status: accepted` alongside a hand-written `disposition` previously let a record staged by an ordinary role become retrievable without a steward ever touching it. Staging now enforces `decided_by != staged_by`, and `cadre knowledge ingest-accepted` is the only path that makes an accepted finding retrievable.

## [0.21.0](https://github.com/deagy/cadre/releases/tag/plugin-v0.21.0) - 2026-08-12

### Added

- **`cadre select --roster <path>`, and the `roster.root` setting behind it (`CADRE_ROSTER_ROOT`, or `roster.root` in user-global config).** Runs selection against a roster package the plugin did not ship with — its own catalog, routing rules, and role definitions — declared by a `roster.json` manifest (`schema_version`, `id`, `version`, `catalog`, `routing`, `role_root`, `shared_policy_root`), with every declared path resolved relative to the manifest and rejected if it escapes. With neither the flag nor the setting present, selection is unchanged and default plans are byte-identical. **Worth knowing:** `roster.root` is global-scope-only, like `agentic_sdlc.bin_path` and `knowledge_store.home` — a project-local `.agents/cadre.yaml` cannot redirect it, because that file arrives with `git clone` and this setting chooses the role prose an agent is handed as its operating instructions. Per-invocation redirection is the explicit `--roster` flag, visible in shell history and CI logs.

- **`cadre select --format text`.** A rendering that leads with the decision — who works on this, what the workflow is, which gates need a human — instead of the ~260-line plan. The JSON plan is the contract every downstream tool reads and stays the default, byte-for-byte unchanged; the default deliberately does *not* switch on whether stdout is a terminal, so a plan's shape never depends on invocation context. The text form is a pure function of the plan dict and never re-runs selection, so it cannot disagree with the JSON for the same invocation. It also gives `needs-triage` words, which in JSON reads as a successful plan with empty agent lists.

- **`cadre knowledge ingest-accepted`**, the step that makes a steward-accepted finding retrievable. Accepting a proposal previously recorded the disposition without putting the content anywhere retrieval could reach it.

- **`cadre sdlc --no-default-provider`**, to run the kernel with no provider bundle injected, and **`default_gate_review_agents` in `routing.json`** — reviewer ids injected into a plan's `support` for each configured lifecycle gate that declares no `review_agents` of its own. Cadre declares `["code-reviewer"]`, so its own plans are unchanged; a roster omitting the key injects nothing.

### Changed

- **Eight subcommands identified themselves by their implementation filename in usage and error messages; they now use their public name.** `cadre select --task ...` answered a usage error as `select_agents.py`, and `knowledge` and `context` both answered as `cli.py` — indistinguishable from each other in the one message meant to tell you what you had just mistyped.

- **`cadre generate-authority-aides` now rejects an unknown flag instead of treating it as "no flags given".** That fallback was the *write* path: `--help` regenerated the eight aide files, and so would a typo. **Required action:** if you run this in CI, a misspelled `--check` previously rewrote the tree and exited 0 — reporting success while masking the drift the check exists to catch. It now fails.

- **`cadre mcp-dispatch-server --help` prints help.** It parsed no argv at all, so `--help` started the stdio server and sat reading stdin, which reads as a hang.

- **Selection is faster, with identical output.** Route and risk-rule matchers are now memoized and keyword matchers are skipped when a cheap necessary-condition test proves they cannot match: a one-shot `cadre select` goes 231 ms → 130 ms, and repeated selection in a long-lived process (`cadre mcp-dispatch-server`) goes 188 ms → 2 ms warm. The 175-case golden corpus passes unchanged and plans are byte-identical.

- **`roster/orchestration/routing.yaml` is renamed to `routing.json`**, matching what the file has always contained. **Required action:** if you reference that path — an overlay, a tool, a doc of your own — update it.

- **The `agents_select` MCP tool and the Cline runner notes now describe Cline's real hook surface.** Beside `setup(api, ctx)` there is a config-file subprocess hook system with `UserPromptSubmit` and `PreToolUse` events, and the two are not equivalent: a `PreToolUse` hook's stdout is parsed, a `UserPromptSubmit` hook's is discarded. So a mutation gate is real on Cline; per-prompt context injection via a hook file is not, and it fails silently in both directions — the hook runs, exits 0, and nothing reports that its output went nowhere. `runner-capabilities.json` records this per runner as `prompt_hook_support`/`tool_gate_support`, as build-time descriptive data with a test forbidding any dispatch-time consumer.

## [0.20.0](https://github.com/deagy/cadre/releases/tag/plugin-v0.20.0) - 2026-08-11

### Added

- **`cadre context` — a place for an agent to park working material outside its own context window.** `init`, `put`, `get`, `list`, `search`, `reindex`, `export`, and `promote`, backed by a store that is separate from the knowledge store in database file, config, resolution root, and module tree. Entries are addressed by handle, so a handoff between roles can carry a reference instead of bulk content. **Worth knowing before you use it:** this store is explicitly *not* a second knowledge store. Entries are agent-written and unreviewed, every entry has a mandatory expiry (there is no indefinite entry — the TTL is a safety mechanism, not a retention policy), and nothing crosses into the curated corpus without a steward: `promote` writes nothing, it emits a document you pipe into `cadre knowledge propose --from-finding -`. Content retrieved from it carries a distinct `untrusted_working_context` trust label and stays flagged through summarization, so a clean-looking summary of poisoned material cannot launder its way into the corpus. Embeddings are offline-only by construction — the store has no import path to anything that opens a socket. See `roster/context-store/README.md` and `SECURITY.md` in the packaged suite.

- **Dispatch can now drive roles against your own OpenAI-compatible endpoint, with or without a coding CLI installed.** Two paths, both inert until configured — with none of the new settings present, every existing dispatch produces byte-identical behaviour. `runners.codex_profile` plus per-tier `runners.local_model_<tier>` points the existing `codex` runner at a local provider (typically `llama-server`), keeping one model per catalog tier rather than collapsing every role onto one. The new `runner="api"` needs no coding CLI at all: it drives `/chat/completions` directly with its own bounded agent loop and file/search/edit tools, reusing the same role wrappers. **Read the security posture before enabling the `api` runner:** it spawns no child process, so it has no OS-level sandbox — file access is confined in-process, which is strictly weaker than what Codex and Claude Code enforce. Writes are off by default (`runners.api_allow_writes`), command execution is absent unless you supply an allowlist and is advisory rather than a containment boundary, and the whole write path is documented as still needing accountable Security Lead review before use outside a local working copy. Your inference endpoint and credentials are never stored here — the `codex` path keeps them in `$CODEX_HOME`, and the `api` path reads a variable *name* from `runners.api_key_env`.

### Changed

- **Every dispatch audit record now carries a `runner` field**, so the trail can distinguish a Codex dispatch from a Claude Code one — it previously could not. A single-role dispatch to an unknown runner is now audited when denied, matching what team dispatch always did. `api`-runner records additionally carry `tool_calls`, `files_written` (paths only), and `commands_run`. **Required action:** if you parse audit records against a fixed set of keys, expect the new fields.

## [0.19.0](https://github.com/deagy/cadre/releases/tag/plugin-v0.19.0) - 2026-08-11

### Fixed

- **A `platform-impact-profile.yaml` overlay written by `cadre init` dropped every impact category and BOM the run did not explicitly answer.** Shared-config resolution replaces lists wholesale rather than merging them by id, but the overlay only listed the entries a run touched — so answering one category left the resolved profile containing that entry alone. The dropped entries were the ones still marked `applicability: unknown`, which is the state that blocks the governance, security, and evidence gates, so resolving a single category silently cleared the blocking state on all the others. **Required action:** if you ran `cadre init` against this file on an earlier version, open your project's `.agents/shared/platform-impact-profile.yaml` and check it still lists every entry the shipped template does; re-running `cadre init` now writes the complete list.

### Added

- **`cadre init` keeps every shipped default unless told otherwise, and no longer requires an answer source.** `--answers <file>` or `--interactive` used to be mandatory; running with neither now keeps the shipped defaults and writes nothing. That is a complete run, not a skipped one — overlays are sparse, so "keep the default" means "write no overlay for that field", and a project with no overlay resolves to exactly the shipped values.

- **`cadre init --set [REGION:]PATH=VALUE`** overrides a single field without authoring an answer file, and is repeatable. The region (`stack`, `libraries`, `autonomy`, `platform`) is derived by looking the path up in the shipped defaults rather than being supplied by the caller, which is what keeps each recorded decision's `stack`/`governance` category honest. A path no shipped default defines, or one that is ambiguous across regions, fails closed. A `--set` on an `agent-autonomy.yaml` field goes through the same allowlist and narrowing check as every other path, so it can only ever narrow the shipped policy.

### Changed

- **`cadre init --interactive` asks about a group of fields at a time rather than every field individually**, taking the floor from roughly 160 questions to roughly 30. Declining a group keeps its shipped defaults. This is safe for the autonomy section specifically because that section's overlay check only ever permits narrowing, so a field nobody reviewed keeps the most restrictive value it shipped with.

- **The `lifecycle-onboarding` skill no longer interviews for all 13 authority roles during onboarding.** An authority only gates the G-gate(s) that name it, so the skill now asks for the product owner and engineering lead — enough to clear G1 and G2 — and resolves the rest when a task first reaches the gate that needs them. Unresolved roles leave a project `valid` but not `ready`, tasks can still be planned, and gates whose authorities are resolved can still be approved; only the gate belonging to an unresolved role is held. The skill also now prefers `--set` over hand-authoring an answer file. **Worth knowing:** a run record captures which authorities were assigned when the record was created, so assign a role before planning the task that will need its gate — a role assigned afterwards does not unblock a task that was already planned.

## [0.18.0](https://github.com/deagy/cadre/releases/tag/plugin-v0.18.0) - 2026-08-11

### Changed

- **The bundled workspace-mutation hook now blocks destructive `git` commands it previously allowed.** A newline separating two commands defeated the guard entirely: it split on `&&`, `||`, `;`, and `|` but not on newlines, so everything after the first line went uninspected. That affected every check the hook performs — `reset`, `checkout`, `restore`, `clean`, `branch`, `push` — and it needed no adversarial intent, since multi-line `Bash` calls are routine. **Required action if you rely on the hook:** multi-line commands that happened to pass because of this bug will now be refused. The same fix landed in the Cline guard. See the [register changelog](https://github.com/deagy/cadre/blob/main/CHANGELOG.md) for the shell-context work that made it safe.

- **A dispatch plan's `schema_version` goes 5 → 6, and the plan may carry a new `undeclared_workflow_shape_routes` property.** **Required action:** if you validate `cadre select` output against a pinned copy of `selection.schema.json`, update that copy when you upgrade. The schema is closed, so a v5 copy rejects a plan carrying the new property. Plans already archived as v5 stay valid v5 documents.

### Added

- **The hook now covers `git worktree`.** `remove` and `move` are refused outright; `prune` is refused only when its own dry run shows a registration would actually be removed. `worktree add` stays allowed — it is the isolation step the policy asks for — except `-B` on an existing branch pointing elsewhere, which force-resets it. Six spellings remain deliberately uncovered and are each pinned by a test asserting they are not blocked.

- **Read-only role wrappers now state that this enforcement exists**, and that it is partial: the policy prose lists what the hook cannot see and names the environment variable that disables it, so no role treats the guard as a boundary to lean on.

## [0.17.0](https://github.com/deagy/cadre/releases/tag/plugin-v0.17.0) - 2026-08-10

### Changed

- **The 28 read-only role wrappers are about 13% smaller.** Reviewers and authorities (`code-reviewer`, `security-reviewer`, the `*-aide` authority roles, and the rest) previously carried `roster/shared/workspace-isolation.md` in full — 331 lines of a roughly 1020-line wrapper — even though the file's own applicability header scopes most of itself to write-capable tiers. Their wrappers now embed the applicability header plus the four sections that bind every tier, and drop from 1020 to 883 lines on both runners. The 131 write-capable wrappers are unaffected by the mechanism. No rule that binds a read-only role was removed, and `cadre resolve-shared workspace-isolation.md` still returns the whole file to any caller at any tier.

- **Every wrapper's embedded copy of `workspace-isolation.md` differs textually**, because the file's prose was reordered so that both the full and excerpted renderings read correctly standalone. No rule's meaning changed.

- **A dispatch plan's `workflow` field takes a different value for many tasks.** See the [register changelog](https://github.com/deagy/cadre/blob/main/CHANGELOG.md) for the mechanism and the affected combinations. It selects which workflow document a plan points at and gates nothing.

## [0.16.0](https://github.com/deagy/cadre/releases/tag/plugin-v0.16.0) - 2026-08-10

### Added

- **The package now ships 159 role wrappers, up from 86.** 85 new bounded execution specialists — one per narrow technology surface — alongside the existing accountable roles, which are unchanged. Twenty non-authoring vendor and platform context packs ship with them. What each role is and how selection reaches it is in the [register changelog](https://github.com/deagy/cadre/blob/main/CHANGELOG.md).

### Changed

- **The dispatch plan's `schema_version` goes 4 → 5, and `context_packs` is now required and always emitted.** **Required action:** if you validate `cadre select` output against a pinned copy of `selection.schema.json`, update that copy when you upgrade. The schema is closed (`additionalProperties: false`), so a v4 copy rejects every plan this version produces. Plans you have already archived as v4 stay valid v4 documents.

- **A project-local routing overlay now takes effect.** `.agents/orchestration/routing-overlay.json` was documented as the way to customize routing without forking `routing.yaml`, but nothing in the selection path read it. It is now applied on every run. **Required action if you already have an overlay file:** it starts governing your dispatch on upgrade. The merge is widen-only — an existing file can add matching conditions but cannot remove a route, drop a reviewer, or narrow a gated risk rule — and an overlay that would narrow now fails the run rather than being ignored.

- **`roster/context-packs/` is included in the pip/pipx distribution.** Without it, a task mentioning any pack keyword aborted `cadre select` in a pip install.

## [0.15.0](https://github.com/deagy/cadre/releases/tag/plugin-v0.15.0) - 2026-08-10

### Changed

- **The Cline agent-dispatch plugin no longer ships a default model provider, and no longer requires `ANTHROPIC_API_KEY`.** Every bundled preset carried `providerId: anthropic` and a vendor-qualified `modelId`, so automatic dispatch selected Anthropic regardless of how Cline itself was configured — prompting for that credential, or silently using it where it happened to exist. Presets now carry only the capability tier (`opus`/`sonnet`/`haiku`); the provider and the concrete model serving a tier are operator configuration resolved at dispatch time via `CLINE_AGENTS_PROVIDER_ID` and `CLINE_AGENTS_MODEL_OPUS`/`_SONNET`/`_HAIKU` (or a single `CLINE_AGENTS_MODEL_DEFAULT`). ([#142](https://github.com/deagy/cadre/issues/142))

  **This is a required action for existing Cline installs.** A dispatch with no provider configured no longer silently falls back to Anthropic; set the environment variables above.

- **Installing the plugin now enables destructive-git protection, on by default.** The guard previously lived only in this repository's own `.claude/settings.json`, so a consuming project that installed the plugin got no equivalent. It is now wired into the main plugin's `hooks/hooks.json` (Claude Code) and the Cline plugin's `startPresetSubagent` dispatch path as a `beforeTool` hook. Destructive git commands — `git reset --hard`, `git clean -f`, `git branch -D`, `git push --force`, or a checkout that would discard uncommitted or unpushed work — are refused when the working tree actually has something to lose. Fail-open on parse ambiguity rather than a blanket blocklist. Opt out with `CADRE_DISABLE_WORKSPACE_MUTATION_GUARD=1`, kept outside generated configuration so regeneration cannot silently re-enable it. ([#129](https://github.com/deagy/cadre/issues/129))

### Fixed

- **The Cline Git-source install failed to install its dependencies.** Fixed in the install path, with `docs/INSTALL.md`'s Cline Git-source section regenerated into `plugin/suite/` so the packaged copy matches. Local Codex CLI install state (`.codex-marketplace-install.json`) that had been swept into the tree — pinning whichever revision was on the author's disk — is no longer committed. ([#127](https://github.com/deagy/cadre/pull/127))

- **The cross-lockfile version guard could fail on a correctly-pinned tree.** `test_both_lockfiles_resolve_the_same_runtime_versions` compared only the top-level `node_modules/<dep>` key between `package-lock.json` and `cline-plugins/package-lock.json`, assuming a nested entry always belongs to an unrelated transitive dependant. npm hoisting can instead push a `cline-plugins` workspace's own correctly-pinned dependency into a workspace-scoped key. Adds a workspace-scoped fallback lookup — never a name-only glob, so an unrelated nested pin cannot satisfy it — that fails loudly on disagreeing nested candidates rather than picking one silently, and raises explicit `AssertionError`s so the fail-loud guarantees survive `python -O`. ([#182](https://github.com/deagy/cadre/pull/182))

### Security

- **Releases now pause for an explicit human approval before signing and publishing.** `main` is protected against deletion and force-push and requires a pull request with passing checks, but "landed on `main`" still went straight to a signed, published release: both release jobs read `TAG_SIGNING_KEY` with no `environment:` declared. Both now declare `environment: release`, which requires approval before the job starts and limits the allowed refs to `main` and the `plugin-v*`/`kernel-v*` tags. Scoping the secret to that environment is what makes the gate protect the key rather than merely gate the job. The `changed` detection job is deliberately not gated — it decides whether a release is needed and touches no secret. ([#147](https://github.com/deagy/cadre/pull/147))

### Dependencies

- `@cline/shared` 0.0.65 → 0.0.71, `vitest` 3.2.7 → 4.1.10, `actions/setup-node` 6.0.0 → 7.0.0.

## [0.14.0](https://github.com/deagy/cadre/releases/tag/plugin-v0.14.0) - 2026-08-08

### Added

- **Three new roles, taking the catalog from 71 to 74.** Each fills a gap an
  existing role explicitly disclaimed, rather than widening one.
- **A `version-control-workflow` skill** (12 skills, up from 11) — branching,
  merge vs rebase, history repair, conflict resolution, and PR/MR hygiene
  across GitHub and GitLab, preferring the `gh`/`glab` CLIs. Deliberately a
  skill and not a role: procedural know-how is not an accountability
  boundary. Note `agent-version-control` is a false friend — it tracks
  role-definition provenance, not git.
- **An AI/ML product-features section in `technology-standards.md`.** Model
  provider, eval framework, and vector store are recorded
  `not_yet_selected`, so a role must present alternatives rather than pick
  one. It also records the load-bearing constraints: model output is
  untrusted data, an eval baseline precedes a prompt or model change, and
  model output never authorizes a privileged action on its own.

### Changed

- **BREAKING (behavioral): write-capable roles now default to working in a
  dedicated `git worktree` rather than the caller's main working tree.**
  **Consumer impact:** a project that dispatches these roles and then
  inspects its main working tree for changes will see none — the changes land
  in `.worktrees/<task-id>/<role-id>/` on a new branch, and must be reviewed
  and merged from there. This is prompt policy and a dispatch-contract
  expectation, not a mechanically enforced gate; no autonomy value moved, so
  it loosens no permission and only changes where allowed edits land by
  default. **Opt out** by narrowing `repository.create_local_branch_or_worktree`
  in a project's own `.agents/shared/agent-autonomy.yaml` overlay, which is
  legitimate under the narrowing-only merge rule and makes every write-capable
  role degrade explicitly to in-place edits.
- **Every role is now told never to mutate a working tree it did not
  create** — no `git reset --hard`, `checkout`/`switch` leaving dirty state,
  `restore`, `stash`, `clean -f`, `branch -f/-D`, `rebase`, or force-push in
  a tree it was dispatched into, with read-only alternatives given for
  inspecting a revision. `agent-autonomy.yaml` gains the matching
  `repository.discard_uncommitted_work_or_move_branches: never`. Prompted by
  a dispatched role that ran `git reset --hard main` to read a branch's diff,
  moved the branch off an unpushed commit, and truthfully reported it had
  made no edits — it never touched a file. Binds every capability tier: the
  destructive commands need no file-write tool and produce no edit, so
  gating the rule behind write capability was gating it on the wrong thing.
- **The `pipeline` route's bare `pipeline` keyword is narrowed to compounds**
  (`ci pipeline`, `build pipeline`, `delivery pipeline`, `deployment
  pipeline`, `release pipeline`). It previously matched *any* pipeline —
  data, ETL, or RAG. **Consumer impact:** this is the one change here that
  can *remove* an agent from an existing task's plan. A task whose text says
  only "pipeline", with no CI-shaped file in scope, no longer selects
  `cicd-engineer`; say "ci pipeline" or change a pipeline file.
- **Agent-generated documentation follows an explicit concision policy**, so
  role output stays proportionate to what the task actually changed.

### Fixed

- **A task changing a GitHub Actions workflow selected no primary agent at
  all.** The `pipeline` route carried only GitLab paths, so
  `.github/workflows/**` matched nothing build-shaped while the identical
  task on `.gitlab-ci.yml` staffed correctly. Both forges now staff
  identically for the same task, pinned by a test. `cicd-engineer` and
  `pipeline-security-reviewer` no longer hardcode GitLab either — both must
  establish which forge applies and review against that forge's own
  controls, with an explicit warning not to carry a control's name across:
  job permission scoping, environment approval, and workload identity
  federation differ materially. Neither role's authority widened.
- **Python work matched no route.** The `backend` route now carries a
  `python` keyword — deliberately not a `**/*.py` path glob, unlike its
  `**/*.go` counterpart, because that cross-matched this repository's own
  orchestration source and added a spurious second primary.
- **Documentation no longer points at repositories that were archived by the
  monorepo merge.** Install and regeneration instructions across the bundled
  suite named `deagy/cadre-lifecycle` and `deagy/agentic-sdlc`; both are
  archived, and the plugin, kernel, and engine now live in `deagy/cadre`.
  Also fixes a stale kernel `Repository` field that made the documented
  `pip show` provenance check fail against the real package.

## [0.13.1](https://github.com/deagy/cadre/releases/tag/plugin-v0.13.1) - 2026-08-08

### Fixed

- **Installing this plugin no longer downloads 263 MB of npm dependencies it
  never uses.** A Claude Code install was writing **277 MB**, of which 263 MB
  was `node_modules`: 252 packages including OpenTelemetry (60 MB), TypeScript
  (23 MB), the AWS SDK, and the SAP AI SDK. None of it is reachable from this
  plugin, whose content is Markdown, generated role wrappers, and stdlib
  Python.

  The cause was packaging, not dependencies. The three **Cline** plugins are
  a real npm workspace, and their workspace root sat at `plugin/package.json`
  — the same directory the marketplace installs this plugin from. Something
  ran `npm install` against it at install time. The Cline workspaces now live
  in a sibling `cline-plugins/` directory, so there is no `package.json`
  anywhere under `plugin/` at any depth.

  **Expect roughly 14 MB instead of 277 MB.** Nothing about this plugin's
  contents, agents, skills, or behaviour changes.

### Changed

- **Cline users: the install path moved.** `cline plugin install
  ./cadre/plugin/cline` is now `cline plugin install
  ./cadre/cline-plugins/cline`, and likewise for `cline-agents` and
  `cline-lifecycle`. `install.sh` and `install.ps1` handle this for you; only
  a hand-written install command needs updating. The plugins themselves are
  unchanged.

- **All 8 plugin manifests now advertise the correct repository.**
  `homepage`, `repository`, and `author.url` pointed at
  `github.com/deagy/cadre-lifecycle`, which was archived at the monorepo
  merge — that is the URL users saw in `/plugin details`. They now point at
  `github.com/deagy/cadre`.

## [0.13.0](https://github.com/deagy/cadre/releases/tag/plugin-v0.13.0) - 2026-08-08

### Added

- **Three new roles, taking the packaged catalog from 71 to 74.** Each fills a
  gap an existing role explicitly disclaimed:

  - **`ai-engineer`** — a project's model-facing layer: model and provider
    selection, prompt and agent design, retrieval, eval harnesses, and
    inference cost/latency. Nothing covered this. The two roles that look
    adjacent are both scoped to the suite's *own* machinery —
    `knowledge-store-steward` operates the agent knowledge store, and
    `agent-performance-evaluator` assesses the catalog's own roles — and
    `ai-engineer`'s authority section names both, so the boundary travels with
    the role rather than living only in release notes.
  - **`visual-designer`** — design tokens, component specifications, and usage
    rules. `interaction-designer` ends its own scope statement with "not the
    visual system," and `frontend-engineer` may not select a component library
    or styling system while that decision is unresolved. The visual system sat
    in the gap both of them name.
  - **`delivery-sequencer`** — dependency map, critical path, and sequencing.
    `premortem` already listed a dependency map among its inputs and nothing
    produced one. Order and prerequisites only; it may not set priority,
    dates, scope, or risk tolerance.

- **A `version-control-workflow` skill** (12 skills, up from 11): branching,
  merge vs rebase, history repair, conflict resolution, and PR/MR hygiene
  across both forges. Deliberately a skill rather than a role — procedural
  know-how is not an accountability boundary.

### Fixed

- **A task changing a GitHub Actions workflow selected no primary agent.** The
  routing's `pipeline` route carried only GitLab paths, so `.github/workflows/**`
  matched nothing build-shaped, while the identical task on `.gitlab-ci.yml`
  selected `cicd-engineer` and `pipeline-security-reviewer`. Both forges now
  staff identically for the same task, asserted by a test so it cannot quietly
  regress to one-forge coverage. `cicd-engineer` and `pipeline-security-reviewer`
  no longer assume GitLab either: both now require establishing which forge
  applies first, since job permission scoping, environment approval, and
  workload identity federation differ materially between them. Neither role's
  authority widened.

- **Python service work matched no route at all.** The `backend` route now
  carries a `python` keyword.

### Changed

- **The `pipeline` route's bare `pipeline` keyword is narrowed to compounds**
  (`ci pipeline`, `build pipeline`, `delivery pipeline`, …). It previously
  matched *any* pipeline — data, ETL, or RAG — which is why an AI feature task
  was dispatched to the CI/CD engineer.

  **Upgrade impact:** this is the one change here that can *remove* an agent
  from a plan you get today. A task whose text says only "pipeline", with no
  CI-shaped file in its changed set, no longer selects `cicd-engineer`. Say
  "ci pipeline", or include the pipeline file.

- **Every packaged role wrapper changes**, including the 71 whose own
  definitions did not. Shared policy is embedded verbatim into each wrapper,
  and `technology-standards.md` gained an AI/ML section: model output is
  untrusted data, an eval baseline precedes a prompt or model change, and
  model output never authorizes a privileged action on its own. Model
  provider, eval framework, and vector store are recorded as unresolved, so a
  role must present alternatives rather than choose one.

## [0.12.5](https://github.com/deagy/cadre/releases/tag/plugin-v0.12.5) - 2026-08-08

### Fixed

- **Signed tags now show as Verified on GitHub.** 0.12.4's tag was correctly
  signed and verified locally, but GitHub reported `unknown_key`: it matches
  a signing key to the signer's account by email, and the tagger was
  `github-actions[bot]@users.noreply.github.com`, which is not an address on
  the account holding the key.

  Release tags are now tagged as `deagy
  <48447733+deagy@users.noreply.github.com>` — the noreply form, always
  associated with the account and exposing no personal address. The tag is
  still created by the release workflow; the identity reflects whose key
  signs it.

  `plugin-v0.12.4` remains signed and locally verifiable, but GitHub will
  always show it Unverified.

## [0.12.4](https://github.com/deagy/cadre/releases/tag/plugin-v0.12.4) - 2026-08-08

### Added

- **Release tags are signed**, with an SSH key. GitHub shows them as
  **Verified** — no tooling needed to check the common case.

  To verify locally, build an `allowed_signers` entry from the public key
  committed at [`.github/tag-signing-key.pub`](https://github.com/deagy/cadre/blob/main/.github/tag-signing-key.pub):

  ```sh
  echo "releases@cadre $(curl -sL https://raw.githubusercontent.com/deagy/cadre/main/.github/tag-signing-key.pub)" \
    > /tmp/cadre-allowed-signers
  git -c gpg.ssh.allowedSignersFile=/tmp/cadre-allowed-signers verify-tag plugin-v0.12.4
  ```

  Artifacts remain keyless (Sigstore/Rekor); tags use a stored key. That
  inconsistency is deliberate — keyless tag signing was tried in 0.12.2 and
  produced signatures with no Rekor entry, so nothing could verify them. See
  [SECURITY.md](https://github.com/deagy/cadre/blob/main/SECURITY.md).

  The release workflow verifies its own signature before pushing the tag and
  fails if it does not hold.

## [0.12.3](https://github.com/deagy/cadre/releases/tag/plugin-v0.12.3) - 2026-08-08

### Fixed

- **Reverted the keyless tag signing added in 0.12.2, and the documentation
  claiming it was verifiable.** It was not.

  gitsign produced a real signature on the tag object but created no Rekor
  entry. A keyless certificate is ephemeral, so with nothing in the
  transparency log there is nothing to verify the signature against.
  Verification failed immediately at signing time, and still failed hours
  later using the same gitsign version that produced it. The contrast in the
  same workflow run was the giveaway: the SBOM attestation logged
  "uploaded to Rekor transparency log" with a log index; the tag signing
  logged no upload at all.

  `plugin-v0.12.2` therefore carries a signature nobody can verify. That is
  worse than an unsigned tag, because it implies an assurance that does not
  exist. Tags are unsigned annotated tags again, and SECURITY.md now says so
  and explains why.

  **Artifact attestations are unaffected** — those reach Rekor and verify
  normally, which is precisely what made the tag failure visible.

## [0.12.2](https://github.com/deagy/cadre/releases/tag/plugin-v0.12.2) - 2026-08-08

### Added

- **Release tags are now signed**, keylessly, via
  [gitsign](https://github.com/sigstore/gitsign) — an ephemeral certificate
  from the release workflow's OIDC identity, recorded in Rekor. No private
  key exists in this project to be stolen or rotated, matching the
  `cosign keyless (Sigstore OIDC)` standard the suite already declares.

  ```sh
  gitsign verify plugin-v0.12.2 \
    --certificate-identity-regexp='https://github.com/deagy/cadre/' \
    --certificate-oidc-issuer=https://token.actions.githubusercontent.com
  ```

  GitHub's UI will show these tags as "Unverified": its badge recognises
  only GPG and SSH keys registered to an account, and there is deliberately
  no such key here. That is a display limitation, not a failed signature.
  See [SECURITY.md](https://github.com/deagy/cadre/blob/main/SECURITY.md).

## [0.12.1](https://github.com/deagy/cadre/releases/tag/plugin-v0.12.1) - 2026-08-08

### Added

- **An SBOM of the Cline plugins' dependencies**, attached to the release as
  `cadre-plugin-cline-sbom.spdx.json` and carrying a SLSA provenance
  attestation (`gh attestation verify <file> --repo deagy/cadre`).

  This plugin's own content is Markdown and stdlib Python, with no install
  step — its entire third-party surface is the three Cline workspaces' npm
  trees, 287 packages beneath `@cline/sdk`, `@cline/shared`, `zod`, and
  `yaml`. That is what the SBOM inventories.

  It is generated from the committed `package-lock.json`, which is already
  the resolved tree, so producing it needs no `npm ci` and a release cannot
  fail on a registry outage.

The plugin itself deliberately carries no provenance attestation. A
marketplace installs it by cloning a git commit, so there is no downloaded
file for anyone to verify; integrity comes from git's content addressing.
Building a tarball purely to have something to sign would create an artifact
nobody installs from, free to drift from what the marketplace actually
serves.

## [0.12.0](https://github.com/deagy/cadre/releases/tag/plugin-v0.12.0) - 2026-08-08

### Changed

- **The lifecycle plugins now install Agentic SDLC kernel v0.13.2**, up from
  v0.13.0. `provider.json`'s `kernel_compatibility` window moved to
  `[0.13.2, 1.0.0)`, and each lifecycle plugin's `kernel-compatibility.json`
  follows it.

  0.13.2 is the first kernel release whose SBOM records the resolved
  dependency tree (19 packages) rather than the single declared one, so this
  is the first version where the published inventory describes what actually
  installs. Both it and the wheel carry a SLSA provenance attestation;
  `bootstrap_sdlc.py` verifies the wheel's checksum before installing.

  **If you already have a kernel**, nothing breaks. A 0.13.0 on your `PATH`
  is now outside the window, and the plugin leaves it strictly alone and
  installs its own copy alongside. If you pinned `AGENTIC_SDLC_BIN` at an
  out-of-window kernel, the bootstrap fails closed and says so rather than
  silently substituting a different binary — point it at 0.13.2+, or unset
  it to let the plugin manage its own.

- Two READMEs cited kernel v0.13.0 with links to the archived
  `deagy/agentic-sdlc` repository's retired tag scheme. Both now point at
  `deagy/cadre`'s `kernel-v*` releases.

## [0.11.1](https://github.com/deagy/cadre/releases/tag/plugin-v0.11.1) - 2026-08-07

### Fixed

- **`suite/README.md` told you to install from an archived marketplace.**
  It shipped in 0.11.0 still describing "one of three" repositories and
  instructing `/plugin marketplace add deagy/cadre-lifecycle` +
  `/plugin install cadre@cadre-lifecycle-team` — a marketplace archived
  before that release was cut. Rewritten around what the packaged suite
  actually is, with install steps pointing at the canonical guide rather
  than being repeated a third time.

  The file is generated from `packaging/plugin-README.md`. The generator
  skips the package-root copy for an already-initialized package, which is
  why the hand-authored `plugin/README.md` was correct while this one was
  not — it is always written.

- **The role wrappers understated what releases now carry.** Shared policy
  embedded into all 71 wrappers claimed the release workflow "does not
  currently attach a release tarball, SBOM, or attestation". Kernel releases
  have attached a wheel, an sdist, and `SHA256SUMS` since 0.11.0, and the
  bootstrap verifies the checksum before installing. Corrected, and it still
  records what is genuinely missing (no SBOM, no SLSA provenance).

- Assorted documentation that described the archived `deagy/cadre-lifecycle`
  as the live home of this plugin.

No behaviour change. If 0.11.0 works for you, this is prose only.

## [0.11.0](https://github.com/deagy/cadre/releases/tag/plugin-v0.11.0) - 2026-08-07

First release from the `deagy/cadre` monorepo. Everything below has been on
`main` since 0.10.1 but unreleased, so no existing install has it yet.

Note the tag scheme: releases are now `plugin-v<version>`, not `v<version>`.
The monorepo inherited 25 bare `v*` tags from before the merge, and an
unprefixed tag would collide with them — silently, since the release
workflow's already-tagged check would match one and report "nothing to do".
The kernel releases separately on `kernel-v*`.

### Added

- **Each lifecycle plugin declares `dependencies` on `cadre`.** Every
  lifecycle skill shells out to `bin/cadre sdlc`, which exists only in the
  `cadre` plugin; that requirement was previously prose in a `description`
  field and enforced by nothing. Installing a lifecycle plugin now pulls
  `cadre` in automatically.
- **`userConfig` options `kernelInstall` and `profile`.** `kernelInstall`
  chooses how the plugin may obtain the Agentic SDLC kernel: `auto`
  (default, manages its own copy), `system` (never installs anything), or
  `off` (no checking). Set them at install time with
  `--config kernelInstall=system`, or fleet-wide via managed settings.
- **`/cadre-install-kernel`**, and a `SessionStart` hook that reports when
  the kernel is missing or out of range. The hook only detects and reports —
  it never installs. Installation is always an explicit human action.
- **A `bin/agentic-sdlc` shim in each lifecycle plugin**, so the kernel is
  reachable without touching your shell profile.

### Changed

- **The kernel installs from a checksum-verified release wheel** rather than
  a git ref. A tag can be moved; a published artifact plus its `SHA256SUMS`
  can be verified. If no wheel is available the install falls back to the git
  ref with a warning — but a *checksum mismatch* aborts rather than falling
  back, since that is a tampering signal, not an unavailable route.
- **No `pipx` prerequisite.** The kernel installs into a virtualenv the
  plugin owns, under its own data directory. This also removes the old "run
  `pipx ensurepath`, start a new shell, re-run" dead end.
- **An incompatible `agentic-sdlc` on `PATH` is left alone**, and the
  plugin's own copy is used instead. It used to be a hard stop that left you
  with a broken plugin and no way forward except uninstalling your own tool.
  A kernel you named via `AGENTIC_SDLC_BIN` is still never substituted.

### Fixed

- **Four skills had unparseable YAML frontmatter** — a `description`
  containing `": "` ends a plain scalar — so they loaded with no name and no
  description, effectively undiscoverable:
  `create-github-gate-issues`, `publish-gate-status-github`,
  `gitlab-gate-tracking`, `publish-gate-status-gitlab`.
- **`cadre-install-kernel` was stranded in `cline-agents`**, which had kept
  advertising a skill nothing generated. The porting step now prunes.

### Install

```text
/plugin marketplace add deagy/cadre
/plugin install cadre@cadre-team
```

See [docs/INSTALL.md](https://github.com/deagy/cadre/blob/main/docs/INSTALL.md),
or [docs/enterprise.md](https://github.com/deagy/cadre/blob/main/docs/enterprise.md)
for a fleet.

## [0.10.1](https://github.com/deagy/cadre-lifecycle/releases/tag/v0.10.1) - 2026-08-07

### Fixed

- **Bumped `cadre-ref.txt` to [`deagy/cadre@v0.16.0`](https://github.com/deagy/cadre/blob/main/CHANGELOG.md) and reapplied regeneration.** Corrections to how the packaged CLI resolves operator settings: the project tier is now anchored to the project being acted on rather than the process's working directory (so a dispatched role's runner binary resolves against the project being dispatched, and the bundled MCP servers no longer read an unrelated checkout's `.agents/cadre.yaml`), executable-valued settings reject a leading `-` and embedded control characters, and secret-shaped-key rejection now walks sequences as well as mappings. No new or changed CLI surface, so nothing here changes how the plugins are invoked. See cadre's own 0.16.0 entry for the detail — this changelog does not restate register-side changes.

  Also ships `suite/docs/examples/role-selection-workflow.md`, a new end-to-end walkthrough from task to dispatched agents, and `suite/roster/shared/README.md`'s reconciliation of the three differently-trusted project-local mechanisms that share `.agents/`.

  This is the first regeneration to land after the `regenerate.yml` patch-truncation fix in 0.10.0, and it exercised the fixed path: the new example doc is a *newly added* file, exactly the class of content the previous `git diff`-based patch silently dropped.

## [0.10.0](https://github.com/deagy/cadre-lifecycle/releases/tag/v0.10.0) - 2026-08-07

### Added

- **Bumped `cadre-ref.txt` to [`deagy/cadre@v0.15.0`](https://github.com/deagy/cadre/releases/tag/v0.15.0) and reapplied regeneration**, which adds new packaged CLI surface: `cadre config` (`show`/`path`/`resolve`) for inspecting where each operator setting resolved from, a leading `cadre --interactive <subcommand>` flag, config-file support for settings that were previously environment-variable-only (`.agents/cadre.yaml` project-local and `~/.config/cadre/config.yaml` user-global, with environment variables still winning), `cadre gitlab-evidence` as a non-MCP CLI over the GitLab evidence tools, and opt-in asynchronous MCP dispatch (`wait=False`) with new `poll_dispatch_status`/`poll_team_status` tools. Existing environment-variable-only setups are unaffected. See [cadre's own 0.15.0 entry](https://github.com/deagy/cadre/blob/main/CHANGELOG.md) for the full detail, including why secrets are never read from a config file and why a project-local config file is treated as untrusted content — this changelog does not restate register-side changes.

  The packaged `bin/cadre` wrapper gained the corresponding `--interactive` handling and now resolves `agentic_sdlc.bin_path` through the same precedence chain as the register's own dispatcher, rather than an environment-variable-and-`PATH`-only lookup that ignored a configured value. It still requires no Python interpreter for `cadre sdlc` when the binary is already locatable via `AGENTIC_SDLC_BIN` or `PATH`.

### Fixed

- **`regenerate.yml` silently dropped every file a cadre release newly added, shipping a broken package.** The workflow built its patch artifact with a plain `git diff --binary`, which reports only *tracked* files — so a regeneration that both modified existing files and added new ones produced a patch containing only the modifications. Nothing failed: the `changed` check one step earlier uses `git status --porcelain`, which *does* see untracked files, so the workflow correctly decided there was something to ship and then shipped a truncated patch; and `validate.yml` does not run automatically on a PR opened with the default `GITHUB_TOKEN`, so CI was silent too. Found in the cadre v0.15.0 regeneration (PR #45), which updated six suite modules and `bin/cadre` to import `roster/shared/src/settings.py` while omitting that module entirely — `cadre select` and `cadre config` in that package died with `ModuleNotFoundError`. The patch is now taken from the index (`git add -A` then `git diff --cached --binary`), which also propagates deletions of generated files. New `tools/test_regenerate_workflow.py` pins both the workflow text and the underlying git behavior that makes staging necessary.

## [0.9.8](https://github.com/deagy/cadre-lifecycle/releases/tag/v0.9.8) - 2026-08-06

### Added

- **New `.github/workflows/drift-check.yml`, run weekly and on demand.** Regenerates from the revision `cadre-ref.txt` already pins (not cadre's latest) and diffs against this checkout, opening/updating a tracking issue if anything beyond the documented hand-authored exceptions differs. `regenerate.yml` only re-verifies content against a *new* cadre release; it never revisits an already-applied revision, so a generated file hand-edited directly here (bypassing "edit the canonical source in deagy/cadre and regenerate") previously produced no CI signal at all and would only surface the next time someone happened to run the manual regeneration procedure. Built after finding exactly that: `suite/AGENTS.md`, `suite/CONTRIBUTING.md`, `provider.json`'s `kernel_compatibility`, `skills/run-agent-orchestration/references/runner-adapters.md`, and `suite/README.md`'s regeneration-safety text had all drifted this way — see the `cadre-ref.txt` bump below, which reconciles all of it (ported the corresponding fixes to `deagy/cadre`: #105, #106, #107).

- **`cline-lifecycle` gains a 21st tool, `sdlc_plan`**, wrapping `bin/cadre sdlc plan` (`agentic-sdlc plan`). Found during a routine Cline feature-parity scan: 13 forge-specific `SKILL.md` files reference `cadre sdlc plan` in passing ("... or `cadre sdlc plan` first") as the way to create a task's dispatch plan/run record before `sdlc_status`/`sdlc_decide` can operate on a brand-new task-id, but no skill gives it a numbered step of its own and no `cline-lifecycle` tool wrapped it — the only forge-agnostic `agentic-sdlc` subcommand referenced by the skills that had no Cline tool call. `sdlc_plan` takes `taskId`/`task` (both required) plus the usual optional `root`; like `sdlc_decide`, it is a real write with no dry-run mode (the kernel's `plan` subcommand has none).
- **`tools/test_readme_identity.py` guards against an unsafe `cadre generate-plugin --output` run clobbering `README.md`.** The register's own safety guard against overwriting a non-empty `--output` directory passes trivially against this repository (it only checks for a `.codex-plugin/plugin.json`, which this repo has since it's itself a packaged plugin) — see [deagy/cadre-lifecycle#3](https://github.com/deagy/cadre-lifecycle/issues/3) and the upstream fix it tracks, [deagy/cadre#97](https://github.com/deagy/cadre/issues/97). Since that guard's real fix lives outside this repository, this test is a local backstop: it asserts `README.md` still carries this repository's own identity (the 4-plugin split, the per-runner Installing sub-headings) and fails the `python-tools` CI job if a clobber ever replaces it with the register's generic single-plugin template.
- **`cline`'s `agents_select` now accepts cancellation via `context.signal`** (`AgentToolContext`, part of the `@cline/sdk` `AgentTool` contract). `execute()` previously omitted the `context` parameter entirely — permitted by TypeScript's parameter bivariance, so it compiled cleanly but left no way to interrupt a hung `cadre select` child process. `context.signal` is now threaded into the underlying `execFile` call; aborting it cancels the process instead of blocking the session indefinitely (deagy/cadre#64).

### Fixed

- **Bumped `cadre-ref.txt` to `deagy/cadre@15d8e76` and reapplied regeneration**, reconciling exactly the kind of drift `drift-check.yml` (above) now exists to catch: `suite/AGENTS.md`/`suite/CONTRIBUTING.md`'s prohibited-content cross-reference, `provider.json`'s `kernel_compatibility` (was `[0.3.0, 0.4.0)`, now `[0.13.0, 1.0.0)`, matching the fix already live here since the 0.9.8 kernel-version-history entry above), `skills/run-agent-orchestration/references/runner-adapters.md`'s Cline MCP re-verification finding, and `suite/README.md`'s regeneration-safety text had all previously been fixed in this repository directly instead of at their canonical source, so every prior regeneration silently reverted them. Also fixed `tools/test_plugin_duplication_health.py`: the three bundled lifecycle skills carry a "Duplication note" callout that can only ever live in the forge-specific copies (it references this repository's own `AGENTS.md`, a concept the register has no notion of), which a previous hand-edit had leaked into the register-generated core copy too; this regeneration correctly stripped it back out, and the test now asserts that placement explicitly instead of just diffing bodies.
- **`cline`'s `agents_select` workspace-root-unresolved error omitted the `stderr` field** the CLI-failure catch path always includes, making the two error shapes structurally inconsistent for a caller that iterates over error response fields. Added `stderr: ""` — not `undefined`, since `sanitizeToolResult`'s JSON round-trip silently drops undefined-valued keys, so only a real (if empty) string actually reaches the caller (deagy/cadre#65).
- **`cline`'s `agents_select` catch path could crash instead of returning a structured error, and passed unbounded content through unfiltered.** `caught as {message?, stderr?}` is a compile-time assertion only; nothing guarantees a thrown value's `.stderr`/`.message` are actually strings at runtime, and the code called `.trim()` directly on `err.stderr` without checking. A malformed error shape (e.g. a non-string, circular `.stderr`) threw an uncaught `TypeError`, defeating `agents_select`'s "never throw" guarantee regardless of `sanitizeToolResult`'s own shape-safety. Both fields are now normalized through a real `typeof` check before use, always producing a real (possibly empty) string. Separately, `sanitizeToolResult` only ever guaranteed JSON-serialization safety, never content bounding — a future change to the spawned binary's error output (e.g. an uncaught Python traceback) could pass through arbitrarily large or path-laden text verbatim. Both `stderr` and the error message are now capped at 2000 characters via `@cline/shared`'s `truncateStr`. Fixes deagy/cadre#72 (test coverage: sanitization is now exercised via a mocked circular-reference/oversized-stderr catch path, not just regex-matched CLI text) and deagy/cadre#73 (the unbounded-passthrough finding).
- **`cline-agents/skills/run-agent-orchestration.md`'s "## Cline" section had drifted from its canonical source**, `skills/run-agent-orchestration/references/runner-adapters.md`. A 2026-08-06 live-verification correction (MCP registration now supports a real end-to-end dispatch, not just discovery — superseding an earlier 2026-08-05 finding) had been hand-added directly to the generated `cline-agents/` copy and never ported back to the canonical source, so the next `tools/port_cline_agents.py` regeneration (run automatically by `regenerate.yml`) would have silently regressed it back to stale text. Ported the correction into the canonical source and regenerated; this also fixed a leaked `suite/roster/orchestration/mcp/...` path in the previously hand-edited copy, now correctly abstracted by the generator's path-substitution table.
- **`cline-lifecycle/README.md` miscounted its own forge-specific skills and tools.** Its intro claimed "all 8" GitHub-side skills (actually 9 — `publish-reviewer-nudge-github` has no GitLab equivalent, as the tool table already said) and its "GitLab (8 tools)"/"GitHub (8 tools)" bullet headers each actually list 7 tools (the other 2 are the forge-shared `sdlc_list_gate_status`/`sdlc_publish_gate_status`, already counted separately). Both counts corrected; 7 + 7 + 2 = 16 forge-specific tools, matching the stated total.
- **`cline-lifecycle/README.md`'s kernel-version-history paragraph moved here, trimmed to a one-line pointer in the README.** `create-gate-issues`, `list-gate-issues`, `create-github-gate-issues`, `list-github-gate-issues`, `publish-gate-status`, `list-gate-status`, `request-gate-reviewers-gitlab`, `request-gate-reviewers`, `publish-reviewer-nudge`, and `list-reviewer-nudge` (10 of the 16 — every GitLab/GitHub tool except the 6 approve/link ones) were documented by the packaged skills but **missing** ("invalid choice") from the `agentic-sdlc` version installed when these 10 tools were first added here, despite being within this repository's then-declared `kernel_compatibility` range. Traced upstream: `agentic-sdlc`'s own `VERSION` constant hadn't been bumped across 9 tagged releases that actually shipped these subcommands — fixed in `deagy/agentic-sdlc` v0.13.0 (see that repo's `agentic_sdlc/__init__.py`). This repository's `provider.json` now pins `kernel_compatibility.minimum` to that fixed release (`[0.13.0, 1.0.0)`), and all 10 tools have been live-verified against it. This was never Cline-specific — Claude Code and Codex hit the identical error running the same commands their own skills document, against the same stale kernel.

## [0.9.7](https://github.com/deagy/cadre-lifecycle/releases/tag/v0.9.7) - 2026-08-06

### Fixed

- **`list_agent_presets` and `list_skills` (`cline-agents`) threw `Error: JSON.stringify cannot serialize cyclic structures`** — the same error class 0.9.6 fixed for `dispatch_selected_roles`, which only wrapped that one tool rather than every tool whose return value flows through the same Cline SDK serialization path.
  - Audited every `execute()` in `cline-agents/index.ts` and `cline/index.ts`.
  - Applied the existing `sanitizeToolResult()` helper to every tool return path that lacked it: `start_subagent`, `list_agent_presets`, `message_subagent`, `get_subagent`, `save_handoff`, `read_handoff`, `list_skills`, `get_skill`, `create_review_subtask`, `write_wiki_page`, `write_evidence_comment` in `cline-agents`, plus one previously-unwrapped early-return path in `cline`'s `agents_select`.
  - No tool's behavior, error semantics, or return shape changed beyond sanitization.
  - Added regression tests for `list_agent_presets`/`list_skills`, following the same genuine-self-referential-object-plus-control-assertion pattern as 0.9.6's `dispatch_selected_roles` test.

## [0.9.6](https://github.com/deagy/cadre-lifecycle/releases/tag/v0.9.6) - 2026-08-06

### Fixed

- **`dispatch_selected_roles` (`cline-agents`) now sanitizes its tool result against non-JSON-serializable values** before returning it, via a new `sanitizeToolResult()` helper (mirroring the existing pattern in `cline/index.ts`) built on `@cline/shared`'s `safeJsonStringify` (#31).
  - Independent review found two problems with the original PR, both corrected before merge: the regression test didn't actually reproduce a cyclic-reference failure, and an unrelated `package-lock.json` version-pin drift on `typescript` had crept in.
  - The lockfile now pins `typescript` exactly.
  - The test suite includes a direct unit test against a genuinely self-referential object, with a control assertion proving it would have failed pre-fix.

## [0.9.5](https://github.com/deagy/cadre-lifecycle/releases/tag/v0.9.5) - 2026-08-06

### Added

- **`cline-agents` gains three GitLab evidence tools: `create_review_subtask`, `write_wiki_page`, `write_evidence_comment`** (#29). `cline-agents` has no MCP client and could not attach `suite/roster/orchestration/mcp/gitlab_server.py` (a stdio MCP server) directly, so these were previously unreachable from a Cline session. Rather than reimplementing GitLab HTTP/validation/audit logic in TypeScript, all three tools shell out to `cadre gitlab-evidence <op>` ([deagy/cadre#103](https://github.com/deagy/cadre/pull/103)'s new non-MCP CLI adapter), reaching the exact same safety-audited `gitlab_core.py` core Claude Code/Codex use via MCP. Every tool requires `GITLAB_SVC_TOKEN`/`GITLAB_BASE_URL`/`GITLAB_DOCS_PROJECT_ID` and returns `status="unavailable"` if unset. `write_wiki_page` is the `human_approval`-tier tool: its first call never writes, only returns a confirmation token a human must approve before a second, identical call actually writes.

### Fixed

- **`suite/roster/orchestration/mcp/` was missing `gitlab_core.py`/`gitlab_server.py`/`GITLAB-EVIDENCE.md` entirely**, despite `cadre-ref.txt` claiming sync with a `deagy/cadre` revision that already had them — a pre-existing regeneration-drift gap, found and fixed while adding the tools above. Regenerated `suite/` from `deagy/cadre@589e7d8`.

## [0.9.4](https://github.com/deagy/cadre-lifecycle/releases/tag/v0.9.4) - 2026-08-06

### Fixed

- **`cline plugin install https://github.com/deagy/cadre-lifecycle --force` still failed with `Cannot find module 'vitest'` after v0.9.3** (#27). v0.9.3's fix was based on the wrong theory (that Cline reads a package's `tsconfig.json` `include` set to decide which files to load). Verified empirically instead, using fast local-path installs: Cline's installer actually recursively `require()`s every `.ts` file anywhere under each workspace directory, ignoring `tsconfig.json`, `cline.plugins[].paths`, and node_modules entirely — but it only matches `.ts`, not `.mts`. Renamed `cline-agents/test/presets.test.ts` to `presets.test.mts`, matching the convention `cline/` and `cline-lifecycle/`'s test files already use (which is why they were never affected). A real `cline plugin install --force` against the fix now completes with no sync warnings at all.

## [0.9.3](https://github.com/deagy/cadre-lifecycle/releases/tag/v0.9.3) - 2026-08-06

### Fixed

- **`cline plugin install https://github.com/deagy/cadre-lifecycle --force` no longer fails with `Cannot find module 'vitest'`** (#25). Cline's plugin installer runs `npm install` without `devDependencies`, then syncs MCP servers using each sub-package's `tsconfig.json` `include` set. `cline-agents/tsconfig.json` was the only one of the three plugin packages whose `include` also pulled in `test/**`, so Cline's sync step tried to load `test/presets.test.ts`, which imports the never-installed `vitest`. Narrowed `cline-agents/tsconfig.json` back to `["*.ts"]`, matching `cline/` and `cline-lifecycle/`, and moved the test-inclusive program into a new `tsconfig.test.json` used only by the `typecheck` script, so CI still typechecks the test suite.

## [0.9.2](https://github.com/deagy/cadre-lifecycle/releases/tag/v0.9.2) - 2026-08-06

### Fixed

- **`cline plugin install https://github.com/deagy/cadre-lifecycle --force` no longer fails with `Cannot find module 'zod'`/`'vitest'`** (#23). Cline's plugin installer only runs `npm install` at the repository root; without an npm `workspaces` declaration, the root `package.json` had zero dependencies, so npm never visited the `cline`/`cline-agents`/`cline-lifecycle` subdirectories where the actual runtime deps live and no `node_modules/` was ever created. Added `workspaces` to root `package.json` and a `cline.plugins[].paths` block to `cline/package.json` (previously missing, making the `agents_select` plugin invisible to Cline's installer).
- **Restored `npm ci` in this repository's own CI** (`.github/workflows/validate.yml`), broken by the same change: moving to npm workspaces requires a single root `package-lock.json` in place of the three per-plugin lockfiles it replaces, and CI still needed to install from that root lockfile instead of the deleted per-directory ones.

## [0.9.1](https://github.com/deagy/cadre-lifecycle/releases/tag/v0.9.1) - 2026-08-06

### Fixed

- **Synced register content that had been sitting unapplied since `deagy/cadre` v0.13.0/v0.14.0**, via this repository's new release-triggered regeneration bot (see 0.9.0's entry) — the first real run of that automation. Consumer-visible content that reaches installed plugins with this release:
  - New `gitlab_issue_or_comment_write`/`gitlab_wiki_write`/`gitlab_approval_issue_state_change` autonomy policy entries added to all 71 role wrappers (from `cadre`'s GitLab evidence MCP server, which existing installs previously had no autonomy policy for at all).
  - A new `gitlab-evidence` route in the packaged routing table, and a new `suite/roster/orchestration/mcp/SECURITY-CONTROLS.md` plus a defense-in-depth audit-key backstop in `dispatch_core.py` (`content`/`body`/`description` now always redacted from audit records, not just documented as forbidden).
  - The `roster/README.md`/`RUNBOOK.md`/`shared/README.md`/`workflows/*.md` documentation restructure (tables, deduplication, verified Mermaid diagrams) from `cadre` v0.14.0.
- **Regeneration also surfaced a real gap in this repository's own automation**: `regenerate.yml`'s first live run correctly generated, diffed, and pushed the regeneration branch, but failed to open the PR — this repository's "Allow GitHub Actions to create and approve pull requests" setting was off, so the default `GITHUB_TOKEN` `peter-evans/create-pull-request` uses was rejected by the GitHub API. Fixed by enabling that repository setting (affects every workflow's default token here, not only `regenerate.yml`).

## [0.9.0](https://github.com/deagy/cadre-lifecycle/releases/tag/v0.9.0) - 2026-08-06

### Added

- **A new plugin, `cline-lifecycle`, exposes G1–G10 Agentic SDLC lifecycle
  governance on Cline as 4 deterministic tool calls** (`sdlc_init`,
  `sdlc_validate`, `sdlc_status`, `sdlc_decide`), each a thin wrapper around
  the exact `bin/cadre sdlc <subcommand>` invocation the
  `cadre-lifecycle-core`/`-github`/`-gitlab` plugins' skills already
  document for Claude Code/Codex. G1–G10 governance was previously
  unreachable from Cline at all, since skills are a Claude Code/Codex
  mechanism with no Cline equivalent. `sdlc_decide` adds no approval logic
  of its own — the `agentic-sdlc` kernel already structurally refuses a
  decision from the same identity as a gate's preparer/verifier; this tool
  only relays that outcome, success or refusal.
- **`cline-agents` gained `dispatch_selected_roles`**, closing the
  plan-to-dispatch gap `agents_select`'s own tool description pointed at:
  it calls `bin/cadre select` (the same authoritative selector) and, if the
  plan is staffed, immediately dispatches every selected primary/reviewer
  role in one call, instead of requiring a human/model to read the JSON
  plan and match role IDs to `start_subagent` calls by hand. Support roles
  stay advisory and are never auto-dispatched.
- **`cline-agents` now bundles this repository's own skills**
  (`list_skills`/`get_skill`), a static port of `skills/*/SKILL.md` with
  any `references/*.md` content inlined. Previously these tools only read
  global/project tiers and returned "none" for every skill in this
  repository, including `run-agent-orchestration` itself.
- **`dispatch_selected_roles` can retrieve knowledge-store context before
  dispatch** (`retrieveKnowledge: true`, opt-in — `classification` is
  caller-asserted, not authenticated, so this does not default on),
  injecting it into each role's instructions as fenced, labeled untrusted
  reference material with a trailing authority re-assertion, plus an
  explicit count of any passage the store's own ingestion-time heuristics
  flagged as containing instruction-like text. A retrieval failure or
  timeout for one role never blocks dispatch or broadens access for any
  role.
- **`team-recipes.md` documents a Cline-specific approximation for all 3
  named team recipes**, using `dispatch_selected_roles`/`start_subagent`
  (persona-addressable, unlike Cline's native `/team`) plus
  `save_handoff`/`read_handoff` for cross-teammate visibility — explicitly
  described as orchestrator-relayed in substance, not a claim that
  `communication_mode: "peer"` runs unmodified on Cline.
- **Register regeneration is now release-triggered, not purely manual.**
  When `deagy/cadre` cuts a new tag, it notifies this repository via
  `repository_dispatch`, which regenerates the packaged plugin content and
  opens a PR for review — the existing manual procedure in README.md's
  "Regenerating Assets" remains available as a local-preview/fallback path.
  The regeneration workflow itself is split into an unprivileged job (which
  executes `cadre`'s own generator code, read-only token) and a privileged
  job (which only applies the resulting diff and opens the PR, never
  executing `cadre`'s code) so a compromised `cadre` revision can't use this
  automation to push or open anything here on its own.

All of the above were investigated first (see the "Cline feature parity"
gap analysis referenced in this repository's own history) and then
implemented across 5 independently reviewed changes; the knowledge-store
retrieval work in particular went through two review rounds after the first
found two High-severity findings (untrusted content injected into a
subagent's system prompt with only a label as control, and retrieval
defaulting on for a caller-asserted classification) — both fixed and
re-verified, including by mutation-testing the fix itself.

## [0.8.0](https://github.com/deagy/cadre-lifecycle/releases/tag/v0.8.0) - 2026-08-05

### Added

- **A new plugin, `cline-agents`, ports all 71 Cadre catalog roles into
  dispatchable Cline SDK subagent presets.** Distinct from the existing
  `cline` plugin (which only exposes `agents_select`, a routing-plan tool
  with no ability to spawn agents itself) — `cline-agents` lets a host Cline
  session actually dispatch a role as a subagent that does real work, built
  on the `agents-squad` reference pattern (`start_subagent`/
  `message_subagent`/`get_subagent`/`list_agent_presets`/`list_skills`/
  `get_skill`/`save_handoff`/`read_handoff`). Adds three hardenings beyond
  the reference example: real per-tool enforcement via `toolPolicies`
  (deny-by-default) plus `mode:"plan"` for the 28 read-only roles, so a
  ported `security-reviewer` (`Read`/`Grep`/`Glob` only in the source
  catalog) can't silently gain `Bash`/`Edit`/`Write`; reserved bundled role
  names, so a project- or global-tier preset can't impersonate a bundled
  role by frontmatter name and manufacture false approval/review authority;
  and preset-only dispatch (no free-form-instructions fallback) with
  workspace-containment-checked `cwd`. Reviewed independently by
  security-reviewer, test-engineer, code-reviewer,
  supply-chain-security-reviewer, and technical-writer — no High/Critical
  findings. A static, one-time hand-authored port of this repository's own
  `agents/*.md`, not wired to auto-regenerate from the Cadre register or
  from `agents/*.md`; drift risk is named in its own README.

### Fixed

- **`cline-agents/` was undiscoverable from this repository's own top-level docs.** It ships a full Cline plugin (own `package.json`, `npm test`/`npm run typecheck`, 71 dispatchable Cadre role presets) but was mentioned nowhere in README.md's Repository Layout tree or "Running Tests" list, nor in AGENTS.md's component description or command list. Added a `cline-agents/` row to README's layout tree and test commands, and an equivalent component description and test commands to AGENTS.md (the file CLAUDE.md points to as authoritative for commands, so not duplicated there).
- **`agents_select` could accept or reject the same out-of-taxonomy
  `classification` value depending on which internal path served the
  request.** The (now-removed, see below) native LangGraph bridge's
  `DispatchRequest.validate()` rejected an out-of-taxonomy `classification`
  unconditionally at parse time, before routing happened — while
  `build_dispatch_plan.py`, the CLI path's and the actual dispatch source of
  truth, only rejects it once a task has actually routed to an agent,
  short-circuiting to "not-applicable" on a needs-triage result without
  validating classification at all. Same input, opposite outcome depending
  on which path ran it. Fixed by removing the bridge's own check and
  deferring entirely to `build_dispatch_plan.py` as the single source of
  truth. Moot for any consumer as of this release: the entry below removes
  the native path this divergence lived in, so it can no longer recur by
  construction rather than merely by fix.
- **Install instructions embedded in this repository's own shipped
  `suite/roster/RUNBOOK.md`, and reference URLs across every generated
  `agents/*.md`/`codex-agents/*.toml` role file, still pointed at the
  archived `deagy/cadre-plugin` repository instead of this one.** Corrected
  the `cline plugin install`, `/plugin marketplace add`, and `codex plugin
  marketplace add` examples and version pins in `RUNBOOK.md` (they were
  literal copy-paste install commands, not just prose), plus repo-name
  references throughout the generated `agents/*.md`/`codex-agents/*.toml`
  files and `suite/`. In the same pass, the corrected text also disclosed a
  real gap: this repository's `release.yml` does not currently attach a
  release tarball, SBOM, or attestation the way the archived
  `deagy/cadre-plugin` repository's release process did — a known
  regression, not yet closed.

### Removed

- **`agents_select`'s native LangGraph bridge execution path, and the vendored
  `agentic_sdlc_langgraph/` engine it depended on, have been removed.**
  `cline/index.ts` now has a single execution path: it always shells out to
  this repository's own `bin/cadre select` CLI, the same code path every
  other consumer (Codex, `cline-agents/`, `bin/cadre` itself) already uses.
  This drops `cline/index.ts` from 554 to 179 lines — the file-path
  resolution, `child.stdin` handling, response-envelope translation,
  validation-message reconciliation, and `SIGKILL` timer-escalation logic
  that the native path required (see the [0.1.1] entry below for the bug
  chain that path accumulated) are gone along with it, not carried forward.
  `agentic_sdlc_langgraph/` (`bridge.py`, `runtime.py`, and their tests) is
  deleted outright; the Agentic SDLC kernel remains available exclusively as
  the external, separately-installed `agentic-sdlc` CLI dependency it always
  was for every other consumer. `cline/index.test.mts` was rewritten to
  match (10 tests, replacing the prior native-path-aware suite of 15), then
  4 more were added on review to close coverage gaps: `requireSdlc`
  forwarding to `--require-sdlc` (asserting the actual
  hard-failure-vs-standalone-degrade behavioral difference, not just flag
  presence, across two tests), `base` used alone without `files` (the
  `<base>...HEAD` git-diff discovery path), and a routed task rejecting an
  out-of-taxonomy `classification` value — 14 tests in total.
- Corrected two remaining "merged Cadre + Agentic SDLC + Cline + LangGraph"
  repository-identity references (`README.md`'s and `cadre-ref.txt`'s
  "Regenerating Assets" prose) left stale by the above — this repository's
  own composition is now Cadre + Agentic SDLC + Cline identity; LangGraph is
  only ever present as the external Agentic SDLC kernel's own internal
  engine, correctly described elsewhere (README.md's architecture section,
  `suite/roster/RUNBOOK.md`, `skills/run-agent-orchestration/SKILL.md`) and
  left untouched.

## [0.7.0](https://github.com/deagy/cadre-lifecycle/releases/tag/v0.7.0) - 2026-08-04

### Changed

- **`cadre-lifecycle-github` and `cadre-lifecycle-gitlab` no longer require a separate `cadre-lifecycle-core` install.** Each now bundles its own renamed copies of `lifecycle-onboarding`, the generic `lifecycle-review`, and `brief-pending-gates` (`-github`/`-gitlab` suffixed, to avoid a skill-name collision if installed alongside `cadre-lifecycle-core` anyway), plus its own copy of the kernel bootstrap script. `cadre-lifecycle-core` is unchanged and remains available standalone. `provider.json` itself is not duplicated — all three bootstrap-script copies still read the single shared repository-root file.
- Added `tools/test_plugin_duplication_health.py`, run alongside the existing `tools/` suite, to fail loudly if the duplicated skills or bootstrap script ever drift out of sync across the three plugins — this is the mechanical safeguard for a duplication approach that has no build-time dependency resolution to fall back on (Claude Code/Codex plugin manifests carry no dependency-declaration field).
- README's "Regenerating Assets" section now documents the extra manual re-sync step this duplication requires after every register regen of `cadre-lifecycle-core`'s skills.

### Fixed

- The initial `cadre-lifecycle-github` duplication left GitLab-only terminology untranslated in three skills — `lifecycle-onboarding-github`, `link-source-issue-github`, and `publish-gate-status-github` referenced a `gitlab-gate-tracking` skill and `gitlab-user-unresolved`/`gitlab-user-ambiguous` reason codes that don't exist in that plugin, instead of their real GitHub equivalents (`create-github-gate-issues`, `github-user-unresolved`). Corrected, along with a `github-user-ambiguous` reason code that was fabricated while fixing the above — GitHub's login lookup is exact-match, unlike GitLab's, so there is no ambiguous-match case to translate. `tools/test_plugin_duplication_health.py` now asserts these GitLab-only tokens can never appear untranslated in a github copy again.

## [0.6.0](https://github.com/deagy/cadre-lifecycle/releases/tag/v0.6.0) - 2026-08-04

### Added

- **`brief-pending-gates`** (`cadre-lifecycle-core`) — a local-only, forge-agnostic briefing of a task's pending lifecycle gates: which gate(s) are still awaiting a decision and which authority role/person is required for each. Composes existing `agentic-sdlc status` output with direct `run-record.json`/`authorities.json` reads, without modifying `gate_status_projection()` or adding kernel code. Aimed at teams recording approvals via plain `agentic-sdlc decide` rather than a GitHub/GitLab review flow, who otherwise have no equivalent to `report-gate-reviewers-*`/`publish-gate-status-*`'s pending-reviewer visibility. No new kernel version required.

### Changed

Tightened the "Before you start" wording across all 11 existing forge skills (`cadre-lifecycle-github`, `cadre-lifecycle-gitlab`) to explicitly permit reusing root/task-id/project-path context already established earlier in the conversation instead of re-asking every time, while preserving each skill's "never fabricate" invariant. Came out of the same follow-up review round as `brief-pending-gates`; both were found to need no new kernel code.

## [0.5.0](https://github.com/deagy/cadre-lifecycle/releases/tag/v0.5.0) - 2026-08-04

### Added

Three more skills, from a second recommendation round that reviewed what shipped in `v0.4.0`. Requires `agentic-sdlc` [v0.12.0](https://github.com/deagy/agentic-sdlc/releases/tag/v0.12.0) or later — same caveat pattern as prior releases: `provider.json`'s `kernel_compatibility` range does not by itself guarantee an installed kernel has these commands.

- **`create-github-gate-issues`** (`cadre-lifecycle-github`) — GitHub mirror of `gitlab-gate-tracking`: publishes a tracking issue per gate plus assigned approval-subtask issues per authority. Deliberately scoped narrower than the GitLab original — no issue-linking enhancement (GitHub has no clean equivalent to GitLab's Issue Links API; only the description cross-reference floor exists), and a new repository-visibility pre-flight (`--allow-public-repo` required for public repos) since GitHub issues have no per-issue confidentiality flag.
- **`publish-reviewer-nudge-github`** (`cadre-lifecycle-github`) — posts an advisory PR comment listing who should be asked to review, sourced from `report-gate-reviewers-github`'s existing candidate report. Explicitly not a review request: logins are rendered as `` `code spans` ``, never `@`-mentions, so posting the comment cannot itself notify anyone. This sidesteps the still-unbuilt write-capable reviewer-request path (`Pull requests: write` has no narrower scope and still needs an explicit human decision that was never made) by reusing `publish-gate-status-github`'s already-approved comment-write capability instead.
- **`report-gate-reviewers-gitlab`** (`cadre-lifecycle-gitlab`) — GitLab reviewer-candidate reporting, read-only, targeting the MR `reviewer_ids` field rather than GitLab's heavier, quorum-based approval-rules model. GitLab's approval API exposes no per-approver commit SHA, so there is no equivalent of GitHub's `review-stale` classification — a documented, permanent gap, not a placeholder; the report surfaces the MR's head SHA for manual cross-checking instead.

All three independently security- and code-reviewed (no critical/high findings) before landing.

## [0.4.0](https://github.com/deagy/cadre-lifecycle/releases/tag/v0.4.0) - 2026-08-04

### Added

Six new skills across the two forge plugins, following a recommendation review of what `cadre-lifecycle-github`/`cadre-lifecycle-gitlab` were missing relative to each other and to the underlying kernel's actual command surface. All drive new kernel commands in [`agentic-sdlc` v0.11.0](https://github.com/deagy/agentic-sdlc/releases/tag/v0.11.0) — `cadre-lifecycle-core` requires that version or later for these to work; `provider.json`'s `kernel_compatibility` range does not by itself guarantee it (same caveat pattern as `v0.3.1`/`v0.3.2`).

- **`link-source-issue-github` / `link-source-issue-gitlab`** (`cadre-lifecycle-github`/`cadre-lifecycle-gitlab`) — record a GitHub or GitLab issue as the source for a G1 (Intent) or G2 (Requirements Baseline) gate, via the kernel's `link-intent-from-<forge>-issue`/`link-requirements-from-<forge>-issue`. GitLab already had this kernel-side; GitHub didn't. Neither forge had a conversational skill wrapper for it before now. Deliberately not approval evidence — never touches `human_approvals`/`gate.status`.
- **`publish-gate-status-github` / `publish-gate-status-gitlab`** (`cadre-lifecycle-github`/`cadre-lifecycle-gitlab`) — publish a one-way, read-only gate-status summary comment on a task's PR/MR, updated in place on re-run, via the kernel's `publish-gate-status`. Carries a mandatory non-approval advisory; never derived from anything the kernel itself doesn't already track, and the underlying command was independently security- and code-reviewed before this skill was built on top of it.
- **`report-gate-reviewers-github`** (`cadre-lifecycle-github`) — reports which GitHub logins would be requested as PR reviewers for a task's gates, and their current status, via the kernel's `request-gate-reviewers`. **Read-only in this version** — it never actually requests a review. GitHub has no token scope narrower than `Pull requests: write` for that (which also permits editing/closing PRs and changing labels), and shipping the write-capable version needs an explicit decision on that permission escalation, not an inferred one. This skill is explicit about that limitation rather than implying more capability than exists.

### Changed

- `cadre-lifecycle-core`'s `lifecycle-onboarding` skill now preflight-checks a GitHub/GitLab identity's *shape* (explicit field, or well-formed `gitlab.com/`/`github.com/` URI) as soon as a human provides it for an authority role, instead of only surfacing a binding problem later, the first time a forge-write skill actually runs. Explicitly documented as a shape check only, not live account-existence verification — no kernel command exposes that today, and the skill says so rather than overclaiming.

## [0.3.2](https://github.com/deagy/cadre-lifecycle/releases/tag/v0.3.2) - 2026-08-04

### Fixed

- **`gitlab-gate-tracking`'s kernel-availability caveat updated: `agentic-sdlc create-gate-issues` is now released.**
  `v0.3.1`'s CHANGELOG entry and the Architecture table noted that the
  kernel commit this skill depends on hadn't been cut into a tagged
  `agentic-sdlc` release yet. It has, as
  [v0.10.0](https://github.com/deagy/agentic-sdlc/releases/tag/v0.10.0) —
  both references updated to point at that release instead of the raw
  commit. Also fixed a broken link: the `v0.3.1` entry linked to
  `deagy/agentic-sdlc`'s `CHANGELOG.md`, which doesn't exist in that
  repository (it uses GitHub Releases only, no changelog file) — now
  points at the actual release notes.

## [0.3.1](https://github.com/deagy/cadre-lifecycle/releases/tag/v0.3.1) - 2026-08-04

### Added

- **`cadre-lifecycle-gitlab` gains a second skill, `gitlab-gate-tracking`**,
  the opposite direction from `lifecycle-review-gitlab`: instead of reading
  an existing GitLab MR approval back into the kernel, it publishes a GitLab
  tracking issue per applicable lifecycle gate, plus a linked "approval"
  issue per gate per required authority role, assigned to that authority's
  real GitLab account (resolved via the kernel's existing
  `authority_gitlab_username()`). Drives the new kernel commands
  `agentic-sdlc create-gate-issues`/`list-gate-issues` — see
  [`deagy/agentic-sdlc` v0.10.0's release notes](https://github.com/deagy/agentic-sdlc/releases/tag/v0.10.0)
  for the kernel-side implementation, which was independently
  security-reviewed and code-reviewed before landing. The skill is
  dry-run-first by design and refuses to `--apply` without the human
  explicitly confirming the shown assignments — creating an issue and
  assigning a real person is treated as a mutation requiring human
  confirmation, the same as any other consequential action in this suite.

  **Requires**: an `agentic-sdlc` kernel at
  [v0.10.0](https://github.com/deagy/agentic-sdlc/releases/tag/v0.10.0) or
  later. `provider.json`'s `kernel_compatibility` range (`>=0.3.0,<0.4.0`)
  does not by itself guarantee an installed kernel has this command (the
  kernel's own `VERSION` constant deliberately stayed at `0.3.0` for this
  release, since it's additive with no G1-G10 contract/schema change); the
  skill's own "Before you start" step checks for the command and tells the
  human plainly if it's missing, rather than assuming.

## [0.3.0](https://github.com/deagy/cadre-lifecycle/releases/tag/v0.3.0) - 2026-08-04

### Changed — breaking

- **Lifecycle governance is no longer bundled into the core plugin — it's
  now 3 separate, optional plugins, and the core plugin is renamed.** This
  repository previously shipped one plugin, `cadre-lifecycle`, combining
  Cadre role selection (71 specialist roles, `agents_select`) with Agentic
  SDLC lifecycle governance (G1–G10 gates). It now ships 4 independently
  installable plugins from the same repository:
  - **`cadre`** (renamed from `cadre-lifecycle`) — role selection only:
    the catalog, routing, `agents_select` Cline tool, and orchestration
    skills. No lifecycle governance.
  - **`cadre-lifecycle-core`** (`plugins/lifecycle/`) — forge-agnostic
    lifecycle governance: the `lifecycle-onboarding` and `lifecycle-review`
    skills, plus `plugins/lifecycle/tools/bootstrap_sdlc.py` (moved from
    `tools/bootstrap_sdlc.py`).
  - **`cadre-lifecycle-github`** (`plugins/lifecycle-github/`) — a
    `lifecycle-review-github` skill that records gate decisions from a real
    GitHub PR review (`approve-from-github`/`approve-from-github-pr`).
  - **`cadre-lifecycle-gitlab`** (`plugins/lifecycle-gitlab/`) — the GitLab
    equivalent (`approve-from-gitlab`/`approve-from-gitlab-mr`).

  The marketplace itself is unchanged (`cadre-lifecycle-team`, still at
  this repository) — only the plugin entries within it changed. All 4
  plugins share one version number and release together.

  **Migration**: existing `cadre-lifecycle@cadre-lifecycle-team` installs
  do not automatically become `cadre@cadre-lifecycle-team` — the rename is
  a new install key. Uninstall the old plugin and run:
  ```text
  /plugin install cadre@cadre-lifecycle-team
  ```
  and, only if you use lifecycle governance, also install
  `cadre-lifecycle-core@cadre-lifecycle-team` (plus `-github`/`-gitlab` as
  needed). See README.md's "Installing" section for the full per-plugin
  instructions.

### Fixed

- `lifecycle-review`'s GitHub/GitLab `approve-from-*` preference logic
  moved out to the new forge-specific plugins — the forge-agnostic skill
  now only ever calls `decide`, and points at the matching
  `cadre-lifecycle-github`/`cadre-lifecycle-gitlab` plugin instead of
  trying to cover every forge itself.

## [0.2.5](https://github.com/deagy/cadre-lifecycle/releases/tag/v0.2.5) - 2026-08-04

### Added

- **`.github/workflows/release.yml`.** `tools/plugin_version.py`'s docstring
  and README's "Releasing" section already described a version bump landing
  on `main` as automatically tagging and publishing a GitHub Release — that
  workflow didn't actually exist until now. It verifies both plugin
  manifests agree on a valid semver, skips (idempotently) if `v<version>` is
  already tagged, then tags the commit and creates a GitHub Release titled
  `v<version>` with that version's `CHANGELOG.md` entry as its notes,
  extracted by the new `tools/changelog_entry.py` (and its test suite,
  `tools/test_changelog_entry.py`). Manual `git tag`/`gh release create` is
  no longer the release step for a version bump reaching `main` — this
  workflow's own release (`v0.2.5`) is the first to be cut by it rather than
  by hand, proving it end-to-end.

### Changed

- README's Claude Code and Codex CLI install pins re-pointed from `v0.2.4`
  to `v0.2.5`, this release itself (same reasoning as `v0.2.4`'s own fix:
  pin to the version this change ships as, not the previous latest).

## [0.2.4](https://github.com/deagy/cadre-lifecycle/releases/tag/v0.2.4) - 2026-08-04

### Fixed

- **README's Claude Code and Codex CLI install instructions were pinned to
  `v0.1.0`**, the repository's very first tag, left over from before the
  release-tagging convention existed. Both `/plugin marketplace add
  deagy/cadre-lifecycle@v0.1.0` and `git clone --branch v0.1.0 ...` now
  pin to `v0.2.4`, this release itself.

## [0.2.3](https://github.com/deagy/cadre-lifecycle/releases/tag/v0.2.3) - 2026-08-04

### Added

- **GitHub Release links in README.md and CHANGELOG.md.** `v0.1.0` through
  `v0.2.2` had git tags with no corresponding GitHub Release pages, so the
  release history wasn't browsable from GitHub's UI. Backfilled a Release
  for each existing tag (title, notes from this file's matching entry), and
  linked them: README's "Releasing" section now links the full Releases
  page plus each version, and each version heading in this file links to
  its own Release.

## [0.2.2](https://github.com/deagy/cadre-lifecycle/releases/tag/v0.2.2) - 2026-08-04

### Fixed

- **`agents_select` failed on every call inside a real Cline host** (not
  reproducible in this plugin's own tests). `createTool()` only converts a
  Zod `inputSchema` to JSON Schema via `schema instanceof ZodType`, checked
  against the *host's* bundled `zod`, not the plugin's. A Cline plugin loads
  from a separate installation than its host, so even a version-matching
  `zod` is a different module instance there — the check silently failed,
  conversion was skipped, and the raw `ZodObject` (which carries circular
  internal references) was registered as the tool's declared schema,
  breaking its serialization. `cline/index.ts` now converts the schema to
  JSON Schema with the plugin's own `zod` before registering it, removing
  the dependency on that cross-realm `instanceof` check.

## [0.2.1](https://github.com/deagy/cadre-lifecycle/releases/tag/v0.2.1) - 2026-08-04

### Fixed

- **`cadre generate-plugin --output` was unsafe to run directly against this
  repository.** `deagy/cadre` split its downstream plugin distribution into
  a separate `deagy/cadre-plugin` repository before this repository's
  previously-pinned `cadre-ref.txt` revision, and the register's `README.md`
  template describes that repository — a different three-way
  `cadre`/`cadre-plugin`/`agentic-sdlc` split with its own versioning — not
  this repository's merged Cadre + Agentic SDLC + Cline + LangGraph
  identity. `CLAUDE.md`, `AGENTS.md`, and `README.md` previously instructed
  running that command directly, which would have silently overwritten
  `README.md` with the wrong content. Corrected all three to document the
  actual safe procedure (regenerate into a scratch directory, diff, apply
  everything except `README.md`, which is now explicitly hand-authored
  here) and fixed `cadre-ref.txt`'s claim of CI enforcement that doesn't
  exist in this repository.

### Changed

- **`cadre-ref.txt` bumped to `8511c75`** to pick up
  `run-agent-orchestration`'s broadened proactive trigger (see
  [`deagy/cadre`'s changelog](https://github.com/deagy/cadre/blob/main/CHANGELOG.md)
  for the trigger-description change itself). Applied by hand to
  `skills/run-agent-orchestration/SKILL.md` rather than through the (at the
  time still unsafe) regeneration command, then verified byte-for-byte
  against this revision's actual generated output before the ref bump.

## [0.2.0](https://github.com/deagy/cadre-lifecycle/releases/tag/v0.2.0) - 2026-08-03

### Added

- **`tools/bootstrap_sdlc.py`** — an opt-in, one-command way to install and
  configure the external Agentic SDLC kernel, instead of requiring a
  separate manual `pipx install` plus `agentic-sdlc init` invocation. It
  `pipx install`s the exact version `provider.json`'s
  `kernel_compatibility.minimum` currently declares (never a floating
  "latest", to avoid reintroducing kernel/provider version drift), refuses
  to touch an existing `agentic-sdlc` install that falls outside that
  range rather than silently replacing it, and then runs `agentic-sdlc
  init --provider provider.json` against the target project. Deliberately
  a standalone script under `tools/`, not a `bin/cadre` subcommand:
  `bin/cadre` and everything under `suite/` is fully regenerated by
  `deagy/cadre`'s `cadre generate-plugin` on every sync, so a hand-added
  case there would be silently lost on the next regeneration.

## [0.1.2](https://github.com/deagy/cadre-lifecycle/releases/tag/v0.1.2) - 2026-08-03

### Fixed

- **`cline plugin install` warned `Cannot find module 'vitest'` on every
  install.** Cline's plugin installer scans every non-hidden `.js`/`.ts`
  file under the installed package as a candidate plugin module,
  including `cline/index.test.ts`. That file imports `vitest`, a
  `devDependency` the installer's production-only `npm install` never
  provisions. Renamed to `cline/index.test.mts` — outside the
  installer's `.js`/`.ts` scan, still discovered and run by `vitest`'s
  default include glob.
- **`codex plugin marketplace add` failed with `marketplace 'cadre-team'
  is already added from a different source`.** `.agents/plugins/
  marketplace.json` still declared the pre-merge marketplace/plugin
  names (`cadre-team`/`cadre`) left over from before this repository
  combined the standalone `cadre` and `agentic-sdlc` repos, colliding
  with an older, separately-installed `cadre-team` marketplace pointing
  at the original pre-merge `cadre` repo. Renamed to
  `cadre-lifecycle-team`/`cadre-lifecycle` to match
  `.claude-plugin/marketplace.json`, which already used the correct
  post-merge names.

## [0.1.1](https://github.com/deagy/cadre-lifecycle/releases/tag/v0.1.1) - 2026-08-03

### Fixed

- **The `agents_select` tool's native LangGraph bridge path was silently
  unreachable.** `cline/index.ts` resolved the bridge's file path two
  directories too high (a leftover from before this repository's rename/
  merge), so every call silently fell back to the slower CLI path.
  Fixing that path exposed and required fixing a chain of further,
  previously-dormant bugs before the native path actually worked: Node's
  async `execFile` silently ignoring its `input` option, a
  response-envelope shape mismatch, a validation-message wording
  mismatch, the native adapter hardcoding this repository's own root
  instead of accepting an arbitrary target workspace, and the native
  adapter's changed-file discovery not mirroring the CLI's git-status
  fallback for the default no-args invocation shape. Also fixed: a
  missing `child.stdin` error handler that could crash the host process
  on a broken pipe, and a timer-escalation bug that could orphan a
  scheduled `SIGKILL`.
- **Documentation described a vendored `agentic_sdlc/` kernel and a
  fabricated release history that don't match this checkout.**
  CLAUDE.md/README.md/AGENTS.md corrected to describe the Agentic SDLC
  kernel as an external, separately-installed CLI dependency, not
  vendored; this file's own `[0.1.0]` entry (below) replaced a copied-
  from-a-different-repository entry describing a split/SBOM/provenance
  story that never happened here; `PHASE2_COMPLETION_SUMMARY.md`'s
  status corrected to match its own reported test state.

### Changed

- `cline/package.json`'s version realigned to `0.1.0` to match every
  other manifest in this repository (it had drifted to `0.1.2`
  independently, with no corresponding release).
- Removed root `package.json`'s npm workspace declaration
  (`workspaces: ["cline"]` and its two forwarding scripts). It backed no
  documented or actually-used workflow (every install/test instruction
  in this repository runs `npm ...` from inside `cline/` directly, never
  `npm ... --workspaces` from the root) and was an active footgun: npm
  invoked inside `cline/` auto-detected the ancestor workspace root and
  silently reached for a lockfile/`node_modules` there instead of
  `cline/`'s own, on both `npm install` and `npm ci`.

## [0.1.0](https://github.com/deagy/cadre-lifecycle/releases/tag/v0.1.0) - 2026-08-03

First release from this repository.

### Changed

- **Renamed and merged from `cadre-agentic-sdlc` into `cadre-lifecycle`**,
  combining the Cadre register's generated role-selection assets
  (catalog, routing, orchestration runtime under `suite/roster/`) with the
  Cline plugin (`cline/`), the vendored LangGraph orchestration engine
  (`agentic_sdlc_langgraph/`), and an external dependency on the
  `agentic-sdlc` CLI ([`deagy/agentic-sdlc`](https://github.com/deagy/agentic-sdlc))
  for G1–G10 lifecycle gate execution. See `RENAME_SUMMARY.md` for the file
  and package-name inventory of the rename.
