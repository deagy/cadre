# Remaining Python → Go Scope

**Status:** 📋 Scoping only — nothing in this document is implemented
**Date:** August 14, 2026
**Covers:** The 8 `bin/subcommands.tsv` rows still dispatching to Python, plus
the 2 MCP protocol servers, after `ORCHESTRATION_REFACTOR_PLAN.md`'s 8
subsystems shipped.

This is a different, wider scope than the orchestration refactor: three of
these four tiers live outside `roster/orchestration/` entirely
(`roster/shared/`, `roster/context-store/`), and are depended on *by*
orchestration code already ported (see Tier 0's leverage note).

---

## Inventory

| File | Lines | Tier | Subcommand(s) |
|---|---:|---|---|
| `roster/shared/src/settings.py` | 1,612 | 0 | `config` |
| `roster/shared/src/resolve.py` | 370 | 0 | `resolve-shared` |
| `roster/shared/src/init_project.py` | 1,728 | 0 | `init` |
| `roster/shared/src/init_project_interactive.py` | 445 | 0 | `init` (interactive flow) |
| `roster/shared/src/content_protection.py` | 60 | 0 | — (library, imported by others) |
| `roster/shared/src/text_chunking.py` | 44 | 0 | — (library) |
| `roster/shared/src/text_embedding.py` | 88 | 0 | — (library) |
| `roster/context-store/src/cli.py` | 392 | 1 | `context` |
| `roster/context-store/src/config.py` | 234 | 1 | — (library) |
| `roster/context-store/src/database.py` | 457 | 1 | — (library) |
| `roster/context-store/src/service.py` | 723 | 1 | — (library) |
| `roster/context-store/src/export.py` | 200 | 1 | — (library) |
| `roster/context-store/src/handles.py` | 52 | 1 | — (library) |
| `roster/orchestration/src/sync_codex_agents.py` | 183 | 2 | `bootstrap-codex` |
| `roster/orchestration/src/profile_diff.py` | 525 | 2 | `profile` |
| `roster/orchestration/src/upgrade.py` | 287 | 2 | `upgrade` |
| `roster/orchestration/mcp/dispatch_server.py` | 551 | 3 | `mcp-dispatch-server` |
| `roster/orchestration/mcp/gitlab_server.py` | 157 | 3 | (no CLI row; used by `dispatch_server.py`'s process) |

**Total: 7,798 lines.**

---

## Tier 0: `roster/shared/` — foundational, do first

**Why first:** `settings.py` and `resolve.py` are imported by 12 other
Python modules across `context-store`, `orchestration/mcp`, and
`orchestration/src` — including two files already ported this session.
Concretely:

- `internal/config/resolver.go` is **already an explicit, documented
  placeholder** for `settings.py`: its own header says "only the
  environment-variable step... This is a placeholder to keep Phase 1
  compiling." It never reads a project-local `.agents/cadre.yaml` or
  user-global `~/.config/cadre/config.yaml` file at all.
- `internal/orchestration/gitlab.go` (shipped this session) documents its
  own env-var-only config resolution as a **deliberate deviation** from
  `settings.py`'s full precedence chain, specifically because that
  chain doesn't exist in Go yet.
- `internal/orchestration/routing_overlay.go` (shipped this session)
  already reimplements two of `resolve.py`'s primitives locally
  (`deep_merge` → `deepMergeJSON`, `find_file_at_project_root` →
  `findFileAtProjectRoot`) because there was no shared Go version to call
  instead — porting `resolve.go` properly would let both be deleted in
  favor of one canonical implementation.

Porting Tier 0 first doesn't just add the `config`/`resolve-shared`
commands — it removes two standing "this is a simplification, see X"
notes from code that already shipped, and gives Tier 1/2 a real
foundation to build on instead of more one-off env-var reads.

### 0.1 `settings.py` → `internal/config/resolver.go` (extend, don't replace)
**Scope:** ~700-900 lines Go, ~40 tests. **Complexity:** Medium-high.

- 18 `FieldSpec` entries in `FIELDS` (`gitlab.base_url`,
  `gitlab.project_id`, `gitlab.supports_work_item_hierarchy`,
  `agentic_sdlc.bin_path`, `knowledge_store.root`, `context_store.root`,
  and others) — each with a `kind` validator (`gitlab_base_url`,
  tri-state bool, plain string, path, etc.) and a `global_only` vs.
  project-overridable scope flag.
- Precedence chain: env var → project-local `.agents/cadre.yaml` →
  user-global `~/.config/cadre/config.yaml` → default → interactive
  prompt (TTY-gated) → not-found.
- `resolve_many()` batch resolution (used by `gitlab_core.py`'s
  `resolve_config()` — the exact call site `gitlab.go`'s deviation note
  points at).
- Needs a YAML config-file reader (`gopkg.in/yaml.v3`, already a
  dependency from `schema_validate.go`/`role_fidelity.go`).
- **Risk:** `global_only` scope enforcement is a security control (it's
  why `gitlab.base_url`/`gitlab.project_id` can't be set from a
  project-local file — see `gitlab_core.py`'s docstring on why). Must
  port the enforcement, not just the happy path.

### 0.2 `resolve.py` → `internal/config/resolve.go` (new file)
**Scope:** ~200-250 lines Go, ~15 tests. **Complexity:** Low-medium.

- `deep_merge` — **already exists** as `deepMergeJSON` in
  `routing_overlay.go`; extract to `internal/config`, have
  `routing_overlay.go` call the shared version, delete the duplicate.
- `find_file_at_project_root` — **already exists** as
  `findFileAtProjectRoot` in `routing_overlay.go`; same consolidation.
- `find_project_overlay` (the `.agents/shared/<filename>` overlay,
  YAML-or-JSON) — new; distinct from `routing_overlay.go`'s JSON-only
  overlay (different file family, different merge semantics per
  `roster/shared/README.md`'s "three things under `.agents/`" doc).

### 0.3 `content_protection.py` / `text_chunking.py` / `text_embedding.py`
**Scope:** ~120-150 lines Go combined, ~10 tests. **Complexity:** Low.

Small library modules imported by `context-store/service.py` and
`select_agents.py`. Port alongside whichever of Tier 0/1 first needs them
rather than as a standalone step.

### 0.4 `init_project.py` + `init_project_interactive.py`
**Scope:** ~900-1,100 lines Go, ~25 tests. **Complexity:** High —
largest single file in this whole remaining set (1,728 lines) and the
one most likely to need a genuine design decision, not a line-by-line
port: it's an *interactive questionnaire* (`cadre init --interactive`)
that writes `.agents/shared/` overlay files. Needs:
- A terminal-prompt strategy in Go (survey library vs. hand-rolled
  `bufio.Scanner` prompts — this repo has no existing interactive-CLI
  precedent to follow, unlike everything ported so far).
- Careful review of what's genuinely interactive-only vs. what has a
  non-interactive flag equivalent already.

**Recommendation:** scope 0.4 separately, after 0.1-0.3 land and prove
out the YAML-config patterns 0.4 will also need.

**Tier 0 total: ~1,900-2,250 lines Go, ~90 tests, roughly 60-80 hours.**

---

## Tier 1: `roster/context-store/` — a self-contained service

**Depends on:** Tier 0.1 (config resolution) and 0.3 (text chunking/embedding).

Not part of orchestration at all — a separate SQLite-backed service for
"park working material outside an agent's context window, get it back by
handle," with TTL-based expiry (`sweep_expired`) and its own audit/promote/
export workflow. Structurally similar in shape to the knowledge-store CLI
already fully ported to Go (`internal/knowledge/`), which is the closest
precedent to follow for patterns (SQLite persistence, CLI command
structure) — but it is a **different service with no shared code today**,
not an extension of it.

**Scope:** ~1,600-1,900 lines Go, ~60 tests. **Complexity:** Medium — mostly
mechanical CRUD + SQLite, no unusual algorithms, but five source files'
worth of surface area (`cli.py` 392 + `config.py` 234 + `database.py` 457 +
`service.py` 723 + `export.py` 200 + `handles.py` 52).

**Recommendation:** Port as one self-contained unit after Tier 0, mirroring
`internal/knowledge/`'s package structure. Roughly 45-55 hours.

---

## Tier 2: Small orchestration-adjacent utilities

Independent of each other and of Tiers 0/1 except where noted — can be
done in any order, in parallel.

### 2.1 `sync_codex_agents.py` → `bootstrap-codex`
**Scope:** ~150-200 lines Go, ~10 tests. **Complexity:** Low. Installs
namespaced Codex role wrappers — file-copy/templating logic. Straightforward
port once briefly read in full.

### 2.2 `profile_diff.py` → `profile`
**Scope:** ~350-450 lines Go, ~15 tests. **Complexity:** Medium.
Read-only provider/profile drift report against a consuming project's
copy — diffing logic over JSON, no external I/O beyond file reads.

### 2.3 `upgrade.py` → `upgrade`
**Scope:** Needs a product decision before scoping the port, not just a
line-by-line translation. **Complexity:** Unclear until decided.

The Python version's `--check`/`--force` semantics assume a **pip/pipx
package** (`pip install --upgrade cadre`). That distribution model no
longer matches reality: the CLI is now a self-building Go binary via
`bin/cadre`'s build cache, with no `pip`/`pipx` involved for a checkout
user. Before this can be scoped as a port, someone needs to decide what
`cadre upgrade` *means* now:
- Check GitHub releases and print instructions?
- `go install` the latest tag automatically?
- Detect install-kind (doctor.go's `checkout`/`go-install`/`plugin-cache`
  classification already exists and is directly reusable here) and behave
  differently per kind?
- Retire the command entirely for checkout/go-install users, keep it only
  meaningful for the plugin-cache distribution?

**Recommendation:** raise this as a product question before scoping
further; don't treat it as a mechanical port.

**Tier 2 total (2.1+2.2 only): ~500-650 lines Go, ~25 tests, roughly 25-35 hours.**

---

## Tier 3: MCP protocol servers

**Depends on:** nothing new from Tiers 0-2 — the *business logic* both of
these expose is already in Go (`dispatch_core.py`'s equivalent shipped in
earlier orchestration-refactor phases; `gitlab_core.py`'s shipped this
session as `internal/orchestration/gitlab.go`). This tier is purely the
**MCP stdio protocol adapter** layer — registering tools, marshaling
requests/responses over the protocol — not decision logic.

**Feasibility check performed:** `github.com/modelcontextprotocol/go-sdk`
is a real, versioned module on the Go module proxy (latest `v1.6.1`,
confirmed reachable), making this tier implementable without hand-rolling
MCP framing.

### 3.1 `dispatch_server.py` → `mcp-dispatch-server`
**Scope:** ~300-400 lines Go, ~15 tests (mocked MCP transport).
**Complexity:** Medium — mostly protocol wiring around already-ported logic.

### 3.2 `gitlab_server.py`
**Scope:** ~120-180 lines Go, ~8 tests. **Complexity:** Low — thin wrapper
registering three tools that call directly into `internal/orchestration/
gitlab.go`'s already-shipped `CreateGitLabReviewSubtask`/
`WriteGitLabWikiPage`/`WriteGitLabEvidenceComment`.

**Tier 3 total: ~420-580 lines Go, ~23 tests, roughly 30-40 hours** (higher
per-line cost than other tiers — protocol-compliance work, not business
logic, needs its own test discipline against the SDK rather than pure
unit tests).

**Recommendation:** lowest priority of the four tiers. Nothing in
`cadre select`/`execute`/`doctor`/etc. depends on this — it only matters
for external tools (e.g. Codex) that talk to Cadre *over MCP* specifically,
which is a narrower audience than the CLI itself.

---

## Suggested order and rough total

| Order | Tier | Hours (rough) | Rationale |
|---|---|---:|---|
| 1 | 0.1 + 0.2 (settings + resolve, skip init_project) | 35-45 | Unblocks everything else; removes 2 standing deviation notes |
| 2 | 2.1 + 2.2 (sync_codex_agents, profile_diff) | 25-35 | Small, independent, parallelizable with step 1 |
| 3 | 1 (context-store) | 45-55 | Depends on step 1's config resolution |
| 4 | 0.3 + 0.4 (text libs, init_project) | 60-80 | Largest single item; benefits from patterns proven in 1-3 |
| 5 | 3 (MCP servers) | 30-40 | Lowest leverage; narrowest audience |
| — | 2.3 (upgrade) | — | Blocked on a product decision, not effort |

**Total, excluding `upgrade` (blocked): ~195-255 hours.**

All five ordered items are independent enough to reorder or parallelize
across engineers except where an explicit "Depends on" is noted above
(Tier 1 needs 0.1; Tier 0.4 benefits from, but doesn't strictly require,
0.1-0.3 landing first).

---

**Document Version:** 1.0
**Last Updated:** August 14, 2026
**Status:** Scoping only — no implementation started
