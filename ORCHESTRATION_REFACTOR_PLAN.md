# Cadre Orchestration System: Python → Go Refactor Plan

**Status:** ✅ Complete — all 8 gap-closure subsystems implemented, tested, and wired into the live CLI
**Date:** August 14, 2026 (final revision)

---

## Revision history

- **v1.0**: From-scratch, 5-phase, 480-hour plan. Written without checking
  repository state — wrong, since most of the engine already existed.
- **v2.0**: Corrected after auditing `git log -- internal/orchestration/`
  (20 prior commits, ~10,890 lines already shipped). Re-scoped to the 8
  subsystems confirmed missing by grep, ~150 hours.
- **v3.0 (this revision)**: All 8 subsystems from v2.0 are now implemented,
  tested, and live in the CLI. This document is retained as the historical
  record of what was ported and how; see Part 3 for what actually shipped.

---

## Part 1: What Was Already Done (as of v2.0)

10,890 lines of Go, 20 commits, covering routing, dispatch execution,
subprocess/API agent runners, result caching/consolidation, audit logging,
retry logic, rate limiting, agent pooling, and a Python interop bridge. See
git log for the full phase history (Phase 2, 4a–4m, 5a–5c, 6, 7–8).

## Part 2: The Bug Found Alongside the Gap Audit

While verifying the v2.0 gap list against the live repository, `bin/cadre`
and `bin/cadre.ps1` were found to be **completely broken**: an earlier
Python-removal commit (`b418031e`) deleted `bin/cadre.py`, but the shell/
PowerShell entry points still shelled out to it. `./bin/cadre help` failed
with `No such file or directory` on this branch — meaning CI's
`validate.yml` smoke tests (`./bin/cadre help`, `./bin/cadre select ...`)
would have failed too. Fixed first, before any of the 8 subsystems, since
it blocked using the real CLI to verify anything else:

- `bin/cadre` and `bin/cadre.ps1` rewritten to self-build the Go binary
  into a gitignored `.cadre-build-cache/` on first run (rebuilding only
  when `cmd/`/`internal/` sources are newer than the cached binary), then
  exec it — no separate `make build` step required.
- `.gitignore` updated (stale "bin/ is the Python dispatcher" comment
  corrected; build-cache path added).
- Two pre-existing, unrelated duplicate-test-name build breaks in
  `internal/knowledge/*_test.go` fixed (`go vet ./...` was failing at the
  module level because of them) — collateral, not orchestration, but
  blocking verification of everything else.

## Part 3: The 8 Subsystems — All Shipped

| # | Subsystem | Go source | Tests | Status |
|---|-----------|-----------|------:|--------|
| 1 | Doctor (install-kind / cwd-checkout mismatch) | `internal/orchestration/doctor.go` | 13 | ✅ Wired to `cadre doctor` |
| 2 | Provenance binding (sha256 + git commit) | `internal/orchestration/provenance.go` | 9 | ✅ Wired into `cadre select`'s plan output |
| 3 | Selection telemetry (opt-in JSONL) | `internal/orchestration/telemetry.go` | 12 | ✅ Wired into `cadre select` + `cadre selection-telemetry` |
| 4 | Routing overlay (project-local merge) | `internal/orchestration/routing_overlay.go` | 22 | ✅ Wired into `cadre select`'s routing load |
| 5 | Glob containment (NFA exclude-shadow detection) | `internal/orchestration/glob_containment.go` | 13 | ✅ Library function (`CheckRouteExcludeShadowing`) |
| 6 | Schema validation (Draft 2020-12 + supplementary checks) | `internal/orchestration/schema_validate.go` | 13 | ✅ Wired to `cadre schema-validate` |
| 7 | Role fidelity (static + probe modes) | `internal/orchestration/role_fidelity*.go` (3 files) | 33 | ✅ Wired to `cadre role-fidelity` |
| 8 | GitLab evidence integration | `internal/orchestration/gitlab.go` | 27 | ✅ Wired to `cadre gitlab-evidence` |

**Totals:** ~7,900 lines of Go (implementation + tests), 142 new tests, all
passing. `bin/subcommands.tsv`'s Python-backed rows for all 8 are removed;
each command now dispatches directly into `internal/orchestration` from
`internal/cli/dispatcher.go`, the same pattern the pre-existing `select`/
`execute`/`knowledge` commands already used.

### Verification performed (not just unit tests passing)

Every subsystem was checked against this repository's own real, committed
data — not only synthetic fixtures:

- **Doctor**: live-run via the real `bin/cadre`, confirmed it correctly
  reports `checkout` install-kind with no mismatch, and correctly *detects*
  a mismatch when run from a binary built outside the checkout.
- **Provenance**: live `cadre select` output inspected — real sha256
  hashes of the actual `roster/catalog.yaml`/`routing.json`, real git
  commit SHA, correctly present.
- **Selection telemetry**: live-recorded a real selection, confirmed the
  JSONL record excludes raw task text by default and includes it only with
  `--record-telemetry-include-task`; summarized via
  `cadre selection-telemetry --summarize`.
- **Routing overlay**: live-tested against the real `routing.json` — an
  overlay attempting to narrow the real `debugging` route's keywords was
  correctly *rejected*; a new additive route was correctly merged and
  matched.
- **Glob containment**: run against every real `exclude_paths` entry in
  the actual 8,500-line `routing.json` (8 routes) — all correctly resolve
  `not-contained` (no coverage bugs), and NotContained witnesses are
  cross-checked against the real `globToRegex` compiler in the same test
  run.
- **Schema validation**: run against the real, committed
  `roster/catalog.yaml`, `roster/orchestration/routing.json`, and
  `roster/roster.json` — zero findings, confirming both the schemas and
  the validator agree with production data.
- **Role fidelity**: static mode run against all 159 real role presets
  (`cline-plugins/cline-agents/agents/*.md`); probe mode dry-run against
  the real 5-probe `role-fidelity-probes.yaml`, matching all 795 probe/role
  pairs with zero mismatches, and correctly flagging a real degenerate
  keyword (`untrusted-input-not-obeyed` on `backend-engineer`).
- **GitLab evidence**: HTTPS enforcement, URL-userinfo rejection,
  quick-action-syntax rejection, the wiki-write confirmation gate
  (issue → tamper detection → single-use consumption), idempotent
  review-subtask creation, and bounded-retry-with-backoff on 5xx/429 all
  verified against an `httptest.NewTLSServer` mock GitLab.

### Documented deviations from the Python originals

- **Provenance/GitLab config**: environment-variable resolution only, not
  the full `roster/shared/src/settings.py` project-local/user-global
  precedence chain. Both fields this affects (`gitlab.base_url`,
  `gitlab.project_id`) are documented in the Python original as
  `global_only` (never project-local), so the env-var tier alone already
  covers the security-relevant case.
- **Glob containment**: models `route_matching.go`'s actual `globToRegex`
  semantics (case-sensitive, single `**` construct), not Python's
  `re`-backed, case-insensitive dialect with two distinct doublestar forms
  — this is the *correct* choice, since the point is to answer "does the
  Go matcher's exclude shadow its include," not to reproduce Python
  behavior that no longer executes anywhere in this codebase.
- **Role fidelity preset discovery**: tries `repoRoot`-relative preset
  directories only, not the Python original's additional packaged-plugin-
  relative candidates (`__file__`-relative resolution doesn't have a Go
  binary equivalent). `--presets-dir` bypasses discovery entirely.
- **Selection telemetry**: omits `matched_risks`, `source_filter`, and
  `lifecycle_tracking_status` fields — the Go `DispatchPlan` doesn't track
  these concepts distinctly (yet); every other field is a direct port.

### Known pre-existing, unrelated gaps (not fixed, out of scope)

- `internal/knowledge/database_repair_test.go` has an unused `database/sql`
  import breaking `go vet ./...` at the module level. Predates this work.
- ~34 `internal/cli` tests (`TestKnowledge*Shard*`, `TestKnowledgeFederated*`,
  `TestKnowledgeRebalance*`) fail identically with or without this work's
  changes (confirmed via `git stash`) — pre-existing knowledge-CLI test
  bugs, unrelated to orchestration.
- `mcp-dispatch-server` / GitLab's MCP protocol server remain Python — see
  the original v2.0 plan's §3.9: porting the actual MCP stdio protocol
  layer (not the business logic, which is what shipped here) is separate,
  optional scope that doesn't block `cadre select`/`execute`/`doctor`/etc.
  CLI parity.

---

## Summary

The orchestration refactor — core engine (v2.0's "75-80% done") plus all 8
gap-closure subsystems from the v2.0 plan — is complete. `bin/cadre` (the
actual entry point, not a manually-built binary) runs entirely on Go for
every command except the small residual list in `bin/subcommands.tsv`
(`context`, `bootstrap-codex`, `resolve-shared`, `mcp-dispatch-server`,
`init`, `profile`, `config`, `upgrade`) — none of which were in scope for
this orchestration-focused effort.

---

**Document Version:** 3.0 (final)
**Last Updated:** August 14, 2026
