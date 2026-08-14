# dispatch_core Python-to-Go Migration Roadmap

## Overview

This document outlines the complete migration of `roster/orchestration/mcp/dispatch_core.py` (3,107 lines) from Python to Go as part of the CLI Python elimination initiative. The migration is divided into 4 phases, targeting completion within 6-8 weeks.

**Current Status**: Phase 2 Complete (1,637 lines); Phase 3-4 planned

## Phase Summary

| Phase | Component | Lines | Status | Est. Days |
|-------|-----------|-------|--------|-----------|
| 1 | Role resolution (Codex .toml + Claude Code .md), sandbox computation, child spawning foundation | 762 | ✅ Complete | 10-14 |
| 2 | Main dispatch function, async job stores, team coordination | 875 | ✅ Complete | 10-14 |
| 3 | State persistence, real spawning, interactive confirmation, integration tests | 1,200-1,500 | 📋 Planned | 20-28 |
| 4 | MCP server, Python elimination, backward compat, full integration | 1,000-1,200 | 📋 Planned | 14-21 |
| **Total** | **dispatch_core.py replacement** | **4,000-4,500** | **1,637 done** | **54-77** |

---

## Phase 1: Foundation (COMPLETE ✅)

**Goal**: Establish tier-based role resolution, sandbox management, and child process spawning infrastructure.

**Deliverables** (762 lines):
- `dispatch_core.go` (392 lines): Constants, types, validation, confirmation gate, job store, utilities
- `dispatch_core_phase1.go` (379 lines):
  - `LoadKnownRoleIDs()` — catalog parsing (stub, full YAML parsing deferred)
  - `ResolveRoleFileCodex()` — Codex .toml tier search (project → global → plugin)
  - `ResolveClaudeCodeRoleFile()` — Claude Code .md tier search (project → plugin)
  - `ensureContained()` — path containment validation (directory traversal prevention)
  - `isProjectTierGitClean()` — git-clean checks for project tier
  - `extractTOMLFields()` — simple TOML field parsing (developer_instructions, model, sandbox_mode)
  - `extractMarkdownFrontmatter()` — Markdown frontmatter parsing (YAML key:value format)
  - `findClaudePluginRoleFile()` — glob-based plugin discovery with ambiguity detection
  - `SpawnAndWait()` — child process execution stub (echo-only, real spawning in Phase 3)
- `dispatch_core_phase1_test.go` (383 lines): 15+ tests covering role resolution, path security, field parsing, sandbox computation

**Key Decisions**:
- Tier-based resolution order (project > global > plugin) with override semantics
- Deny-by-default environment variable allowlist (12 whitelisted: PATH, HOME, LANG, LC_*, TERM, TMPDIR, TZ, USER, LOGNAME, SHELL, CODEX_HOME)
- Git-clean validation only for project tier + write-capable modes (prevents uncommitted dev instructions from being dispatched)
- Separate TOML and Markdown parsing (Codex vs Claude Code role formats)

**Testing**: All 15+ Phase 1 tests passing; gofmt/vet/golangci-lint clean

---

## Phase 2: Main Dispatch Engine (COMPLETE ✅)

**Goal**: Implement core dispatch functions, async job stores, and team coordination.

**Deliverables** (875 lines):
- `dispatch_core_phase2.go` (480 lines):
  - `DispatchSecureCloudRole()` — main entry point (~120 lines)
    - Input validation (role_id, brief, mode, classification, dispatch depth)
    - Confirmation gating for write-capable modes
    - Sync vs async dispatch routing
  - `dispatchSync()` (~50 lines) — spawn child, block, capture output, log audit
  - `dispatchAsync()` (~35 lines) — spawn background goroutine, return job_id
  - `DispatchTeam()` (~100 lines) — multi-role concurrent dispatch (1-8 members)
  - `dispatchTeamSync()` (~45 lines) — concurrent member execution with result aggregation
  - `ConcurrencyLimiter` (~20 lines) — semaphore pattern for goroutine limiting (max 3)
  - `TeamConfirmationGate` (~30 lines) — team-wide confirmation token management
  - `TeamDispatchJobStore` (~50 lines) — team job lifecycle with TTL expiry
  - Job/team polling functions

- `dispatch_core_phase2_test.go` (395 lines): 14+ tests covering dispatch validation, confirmation flow, async polling, team coordination

**Key Decisions**:
- Confirmation gating only for write-capable sandboxes (workspace-write, danger-full-access)
- Async dispatch returns immediately with job_id; polling required to retrieve results
- Team dispatch respects MaxConcurrentChildren (3) to prevent resource exhaustion
- Audit logging rejects forbidden keys (developer_instructions, brief, prompt, output, stdout, stderr, environment, credentials, auth, token)
- Job TTL is 1800 seconds; confirmation TTL is 300 seconds

**Testing**: All 14+ Phase 2 tests passing; 428+ total orchestration tests passing

---

## Phase 3: State Persistence & Real Spawning (PLANNED 📋)

**Goal**: Add persistent job storage, real subprocess execution, and interactive confirmation prompts.

**Est. Size**: 1,200-1,500 lines production code + tests
**Est. Duration**: 20-28 days

### 3.1: Persistent Job Store (300-350 lines)
Migrate from in-memory DispatchJobStore to SQLite backing via contextstore/database.go pattern.

**Deliverables**:
- Job table schema with TTL-based indexes
- PersistDispatchJob(), RetrieveDispatchJob(), CleanupExpiredJobs()
- RecoverPendingJobs() for crash recovery on startup
- Thread-safe concurrent job access with connection pooling
- ~80-100 line test suite

**Success Criteria**:
- Jobs survive process restart
- Expired jobs cleaned up automatically
- No race conditions on concurrent access
- Recovery re-processes jobs older than DefaultTimeoutSeconds

### 3.2: Real Role Resolution Integration (250-300 lines)
Connect Phase 1 role file resolution to Phase 2 dispatch functions.

**Deliverables**:
- ResolveRoleForDispatch() — unified entry point for role resolution
- ValidateResolvedRole() — model tier (opus/sonnet/haiku), sandbox mode, classification checks
- BuildDispatchPrompt() — combine developer instructions + untrusted brief with size caps
- ~60-80 line test suite

**Success Criteria**:
- Resolves both Codex (.toml) and Claude Code (.md) roles
- Fails cleanly on missing roles or validation errors
- Sandbox mode forced appropriately by dispatch mode
- Prompt assembly respects size limits

### 3.3: Real Child Process Spawning (350-400 lines)
Replace echo stub with actual subprocess execution.

**Deliverables**:
- SpawnClaudeCodeChild() — invoke `claude code --agent <model> --brief <prompt>` subprocess
- SpawnCodexChild() (stub) — invoke Codex API (future, marked unavailable)
- ExecuteDispatchChild() — dispatcher routing based on runner type
- SafelyKillProcess() — SIGTERM → timeout → SIGKILL cleanup
- Output wrapping, timeout handling, error propagation
- ~80-100 line test suite

**Success Criteria**:
- Subprocess exits cleanly on normal completion
- Timeout enforcement prevents hanging processes
- No orphaned child processes
- Output properly wrapped as untrusted data
- Environment isolation enforced (allowlist only)

### 3.4: Interactive Confirmation Flow (200-250 lines)
Add real user prompts for write-mode confirmations.

**Deliverables**:
- PromptForConfirmation() — display details, read y/n from stdin with timeout
- DisplayConfirmationPrompt() — human-readable formatted prompt
- RecordConfirmationDecision() — audit log decision with who/when
- ~40-60 line test suite

**Success Criteria**:
- User sees role_id, brief, mode, classification, sandbox implications
- Timeout fallback for non-interactive runs (CI/automation)
- Decision logged to audit trail
- Clear yes/no prompting (y/yes vs n/no)

### 3.5: Phase 3 Integration Tests (200-250 lines)
End-to-end workflow tests combining all Phase 3 components.

**Deliverables**:
- TestPhase3EndToEndSyncDispatch
- TestPhase3EndToEndAsyncDispatch
- TestPhase3ConfirmationFlow
- TestPhase3JobRecovery (crash → restart → recovery)
- TestPhase3MultiRoleTeamDispatch
- TestPhase3RoleResolutionTierSearch

**Success Criteria**:
- Role resolution → child spawn → output capture → audit log working end-to-end
- Async jobs persist across restart
- Confirmation prompts work in interactive mode
- Team dispatch coordinates concurrent roles correctly
- All 550+ orchestration + phase 3 tests passing

---

## Phase 4: MCP Integration & Python Elimination (PLANNED 📋)

**Goal**: Build Go MCP dispatch server to replace Python dispatch_server.py, eliminate all Python dependencies from dispatch path.

**Est. Size**: 1,000-1,200 lines production code + tests + docs
**Est. Duration**: 14-21 days

### 4.1: MCP Dispatch Server (300-350 lines)
Go implementation of roster/orchestration/mcp/dispatch_server.py using mcpserver pattern.

**Deliverables**:
- NewDispatchServer() — MCP server construction
- RegisterDispatchTools() — tool definitions:
  - dispatch_secure_cloud_role(role_id, brief, mode, classification, confirmation_token, task_id, session_id, parent_classification, runner, wait)
  - dispatch_team(members, mode, classification, confirmation_token, task_id, session_id, parent_classification, runner, wait)
  - poll_dispatch_status(job_id)
  - poll_team_status(team_id)
- MCP protocol message handling (read/write JSON over stdio)
- ~60-80 line test suite

**Success Criteria**:
- All 4 tools registered and callable via MCP
- Delegation to Phase 2/3 dispatch functions working
- Error responses follow MCP error format
- Prompt input validation matches Python server

### 4.2: CLI Integration (150-200 lines)
Replace mcp_dispatch_server.go stub with real Go server.

**Deliverables**:
- Rewrite MCPDispatchServerCmd() to start Go MCP server
- Config validation (project/global/plugin roots, catalog)
- MCP protocol loop on stdio
- ~40-60 line test suite

**Success Criteria**:
- CLI command `cadre mcp-dispatch-server` starts Go server
- Server reads/writes MCP protocol on stdin/stdout
- No dependencies on Python subprocess
- Configuration validation prevents invalid startups

### 4.3: Python Dispatch Elimination (200-250 lines)
Remove Python fallbacks, update routing tables.

**Deliverables**:
- Remove mcp-dispatch-server routing to Python dispatch_server.py
- Update bin/subcommands.tsv (remove Python dispatch entries or mark as legacy)
- Update dispatcher.go (no Python fallback in dispatch path)
- CLI help text updates (mark dispatch as Go-native)
- ~30-50 line test suite

**Success Criteria**:
- No Python subprocess calls in dispatch code path
- All dispatch operations routed to Go implementations
- No Python dependencies for core dispatch

### 4.4: Backward Compatibility & Migration (150-200 lines)
Support old confirmation token format, gradual rollout.

**Deliverables**:
- ParseLegacyConfirmationToken() — convert old Python token format to new
- MigrateJobStore() — import old JSON job cache to SQLite
- DeprecationWarning() — notify users of Python CLI deprecation
- ~40-60 line test suite

**Success Criteria**:
- Old Python-generated tokens accepted and converted
- Old job cache imported successfully
- Users warned about Python CLI deprecation
- Smooth migration path for existing deployments

### 4.5: Integration Tests & Documentation (250-300 lines)
Comprehensive Phase 4 testing and user-facing documentation.

**Deliverables**:
- `dispatch_core_phase4_test.go` (80-100 lines):
  - TestPhase4EndToEndMCPDispatch (CLI → MCP → dispatch → result)
  - TestPhase4TeamDispatchViaMCP
  - TestPhase4ConfirmationViaMCP
  - TestPhase4JobPersistenceViaMCP
  - TestPhase4BackwardCompat
  - TestPhase4PythonElimination (no Python calls in trace)
  
- `docs/DISPATCH_CORE.md` (100+ lines):
  - Architecture overview
  - Phase 1-4 breakdown
  - Role resolution tiers (project/global/plugin)
  - Sandbox modes and security model
  
- `docs/MCP_DISPATCH_PROTOCOL.md` (80+ lines):
  - MCP tool signatures
  - Error codes and handling
  - Confirmation flow
  
- `docs/DISPATCH_MIGRATION.md` (50-70 lines):
  - Upgrading from Python CLI
  - Behavior compatibility matrix
  - Known differences

**Success Criteria**:
- All 600+ orchestration + MCP tests passing
- Full dispatch workflow documented
- Users can migrate from Python CLI with confidence
- No regressions in dispatch behavior vs Python version

---

## Technical Dependencies & Sequencing

```
Phase 1 (complete)
    ↓
Phase 2 (complete)
    ↓
Phase 3.1 (persistence) ← independent; start immediately
Phase 3.2 (resolution) ← depends on Phase 1
Phase 3.3 (spawning) ← depends on 3.2
Phase 3.4 (interactive) ← independent
Phase 3.5 (tests) ← depends on all 3.1-3.4
    ↓
Phase 4.1 (MCP) ← depends on Phase 2+3
Phase 4.2 (CLI) ← depends on 4.1
Phase 4.3 (elimination) ← depends on 4.2
Phase 4.4 (compat) ← depends on 4.1+4.2
Phase 4.5 (tests + docs) ← depends on 4.1-4.4
```

### Recommended Work Order
1. **Week 1-2**: Phase 3.1 (persistence), Phase 3.4 (interactive) in parallel
2. **Week 2-3**: Phase 3.2 (resolution)
3. **Week 3-4**: Phase 3.3 (spawning)
4. **Week 4**: Phase 3.5 (integration tests)
5. **Week 5**: Phase 4.1 (MCP server)
6. **Week 5-6**: Phase 4.2-4.4 (CLI integration, elimination, compat)
7. **Week 6-7**: Phase 4.5 (tests + docs)

**Total**: 6-7 weeks, ~34-49 person-days

---

## Success Criteria

### By End of Phase 2 (Current)
- ✅ 1,637 lines of Go implementation + tests
- ✅ Core dispatch infrastructure (validation, confirmation gating, async job stores, team coordination)
- ✅ All 428+ orchestration tests passing
- ✅ Foundation for real subprocess execution and persistence

### By End of Phase 3
- ✅ Persistent job store (SQLite)
- ✅ Real child process spawning with role resolution
- ✅ Interactive confirmation prompts for write modes
- ✅ End-to-end dispatch workflow tests (14+ test cases)
- ✅ 550+ total orchestration tests passing
- ✅ No Python subprocess calls in dispatch code path

### By End of Phase 4
- ✅ Go MCP dispatch server (replaces Python dispatch_server.py)
- ✅ Full Python elimination from dispatch operations
- ✅ Backward compatibility with old token format
- ✅ Complete dispatch_core.py port (3,107 lines → 4,000-4,500 lines Go)
- ✅ 600+ total orchestration + MCP tests passing
- ✅ Production-ready Go CLI with no Python dependencies for dispatch

---

## Risk Mitigation

**Risk**: Python dispatch_server.py has complex state management not captured in tests
- **Mitigation**: Run both Go + Python in parallel during Phase 4; comprehensive backward compat tests

**Risk**: Role resolution tiers may have undocumented precedence rules
- **Mitigation**: Phase 1 implemented tests for all tier combinations; will expand in Phase 3

**Risk**: Child process spawning edge cases (timeout, signal handling, orphaned processes)
- **Mitigation**: Comprehensive test coverage in Phase 3.3; SafelyKillProcess with timeout fallback

**Risk**: MCP protocol implementation mismatches vs Python
- **Mitigation**: Protocol-level tests comparing Go vs Python outputs

**Risk**: Long Phase 4 duration delays Python elimination
- **Mitigation**: Staged rollout: internal → beta features → full; keep Python as fallback until Phase 4 complete

---

## Metrics & Monitoring

**Code Quality**:
- Maintain >90% test coverage for dispatch_core
- Zero golangci-lint warnings
- gofmt compliance
- Zero security/linting issues in audit log handling

**Performance**:
- Dispatch latency: Phase 3 spawn < 100ms (measured)
- Job store query: <5ms for typical operations
- Team dispatch with 3 members: <500ms parallel execution
- vs Python: expect 2-3x speedup (Go subprocess vs Python subprocess)

**Reliability**:
- Job recovery on crash: 100% of pending jobs recovered
- Confirmation prompt timeout: works reliably in automation (CI/CD)
- Team dispatch: handles failures in individual members gracefully
- Audit logging: no forbidden keys leaked, <1% audit log errors

---

## Reference: Architecture Comparison

### dispatch_core.py (Python, 3,107 lines)
```
bin/cadre (Python)
  → cadre_cli/sdlc.py (gate orchestration)
    → cadre_cli/dispatch_core.py (main dispatch)
      → confirm_gate() (manual confirmation prompt)
      → subprocess.run() (child spawning, Python)
      → asyncio.gather() (team coordination, async)
      → json file storage (job persistence)
```

### dispatch_core.go (Go, ~4,000-4,500 lines target)
```
bin/cadre (Go)
  → cmd/cadre/main.go (CLI dispatcher)
    → internal/cli/dispatcher.go (routing)
      → internal/cli/mcp_dispatch_server.go (Phase 4)
        → internal/mcpserver/dispatch_server.go (Phase 4)
          → internal/orchestration/dispatch_core_phase2.go (dispatch)
            → internal/orchestration/dispatch_core_phase3_spawn.go (Phase 3.3)
            → internal/orchestration/dispatch_core_phase3_persistence.go (Phase 3.1)
            → internal/orchestration/dispatch_core_phase3_interactive.go (Phase 3.4)
            → internal/orchestration/dispatch_core_phase1.go (role resolution)
          → internal/contextstore/database.go (Phase 3.1, job storage)
```

**Key Improvements**:
- Native subprocess execution (no Python interpreter overhead)
- SQLite job persistence (vs in-memory JSON files)
- Typed role resolution (vs string munging in Python)
- Concurrency primitives (goroutines + channels vs asyncio)
- Security hardening (git-clean checks, path containment, env allowlisting)

---

## Questions & Feedback

For questions about this roadmap, see: `roster/orchestration/RUNBOOK.md` (Phase 1-2 reference) and `CLAUDE.md` (dispatch_core architecture notes)

Last Updated: 2026-08-14
Status: Phase 2 Complete; Phase 3-4 Ready for Implementation
