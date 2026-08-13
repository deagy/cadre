# ADR-001: Refactor Cadre CLI from Python to Go

**Status:** ACCEPTED  
**Date:** 2026-08-13  
**Author:** Daniel Eagy  
**Deciders:** Engineering team  

---

## 1. Context

The Cadre CLI currently consists of:
- `bin/cadre` and `bin/cadre.ps1` — thin shell/PowerShell wrappers that locate Python 3.10+
- `bin/cadre.py` — main dispatcher (~280 lines) implementing subcommand routing and SDLC delegation
- 17 subcommands implemented across `roster/orchestration/src/`, `roster/knowledge-store/src/`, `roster/shared/src/`, etc.

### Current State

**Strengths:**
- Feature-complete CLI with proven stability
- Settings resolution (`roster/shared/src/settings.py`) handles complex precedence (env → project → global → default)
- SDLC delegation works correctly with provider injection logic
- Well-tested through plugin/Codex distribution

**Pain Points:**
1. **Python runtime dependency:** Every Cadre user must have Python 3.10+ installed
   - Complicates pip/pipx installation (requires Python + setuptools)
   - Adds version detection complexity in shell wrappers
   - Increases cold-start overhead (Python interpreter startup)

2. **Cross-platform complexity:** Separate shell (`bin/cadre`) and PowerShell (`bin/cadre.ps1`) wrappers
   - ~40 lines of sh/ps1 logic just to locate Python
   - Different error messages on different platforms
   - Harder to test (requires shell environment setup)

3. **Distribution friction:** Binary distribution (Go) vs interpreted distribution (Python)
   - Pip/pipx users must have Python; non-Python users have no path
   - Static binary much simpler for air-gapped/constrained environments
   - Smaller distribution size (single binary vs Python + site-packages)

4. **Startup performance:** Python interpreter startup (~100-200ms) adds latency to every CLI invocation
   - Users running many cadre commands (e.g., in CI/CD loops) accumulate overhead
   - Go binary startup is near-instant (<1ms)

### Why Now?

- Cadre is sufficiently stable (v0.23.2+) that a refactoring won't destabilize core use cases
- The router-neutral design goal (`AGENTS.md`: "Runner-neutral Cadre") is already met by Go (adds to Claude Code, Codex, Cline without favoring one runner)
- Go expertise exists in the team (see `pkg/` monorepo in sibling workspace)
- No major features are planned for the CLI surface (orchestration is feature-complete)

---

## 2. Decision

**Refactor the Cadre CLI from Python to Go.**

### Scope

**Will be ported to Go:**
1. `bin/cadre.py` dispatcher and argument routing
2. Version detection (from Python version marker)
3. SDLC delegation (exact replica of provider injection logic)
4. Settings/config resolution (exact replica of `roster/shared/src/settings.py` precedence chain)
5. 17 subcommands:
   - Phase 1 (core): `config`, `doctor`, `upgrade`, SDLC delegation
   - Phase 2 (orchestration): `select`, `selection-telemetry`, `profile`
   - Phase 3 (generators): `generate-plugin`, `generate-role-metadata`, `generate-authority-aides`
6. Interop layer for fallback to Python subcommands (during transition)

**Will remain Python:**
- Agentic SDLC kernel and LangGraph orchestration engine (in `kernel/`, `engine/` directories)
- Knowledge store (`cadre knowledge`) and context store (`cadre context`)
- Complex interaction subcommands: `init`, `bootstrap-codex`, `mcp-dispatch-server`, `gitlab-evidence`, `role-fidelity`

### Implementation Strategy

**Single monolithic Go binary** (not multiple binaries):
- Directory structure: `cmd/cadre/main.go`, `internal/cli/`, `internal/config/`, `internal/platform/`, `internal/subcommands/`
- Minimal dependencies: `viper` only for YAML/JSON config parsing (already preferred in `library-standards.yaml`)
- Standard library flag parsing (no cobra, urfave/cli — unnecessary overhead for 18 static subcommands)

**Incremental 3-phase migration** (not big-bang):
- **Phase 1 (~2 weeks):** Core infrastructure and lightweight commands (dispatcher, version, config, doctor, upgrade, SDLC)
- **Phase 2 (~2 weeks):** High-value orchestration commands (select, selection-telemetry, profile)
- **Phase 3 (~2 weeks):** Complex generators (generate-plugin, generate-role-metadata, generate-aides)
- **Compatibility layer:** During transition, Python fallback for any unported subcommands
- **Switchover (~1 week):** Final cutover to Go-only CLI

**Total effort:** ~7 weeks, ~80 hours (one engineer full-time on CLI refactoring)

### Backward Compatibility Guarantees

**Invariants preserved:**
1. CLI interface: `cadre [--interactive] <subcommand> [args...]` unchanged
2. Exit codes: 0 (success), 1 (error), 2 (argument parse error)
3. Output formats: JSON, YAML, text bit-for-bit identical
4. Error messages: Identical to Python versions (scripts depend on them)
5. Environment variables: All CADRE_* and subprocess env vars respected
6. Config file precedence: env > project-local > global > default > interactive > error
7. Config scope model: global_only fields cannot be set in project-local config
8. SDLC delegation: Provider injection logic identical to Python
9. Performance: CLI startup faster, memory footprint lower

### Testing Strategy

**Three-tier approach:**
1. **Unit tests:** Dispatcher routing, version detection, config resolution, scope violations, provider injection (5 test cases)
2. **Integration tests:** End-to-end CLI dispatch, config resolution across tiers, SDLC delegation
3. **Compatibility tests:** Golden files comparing Go output to Python reference (JSON/YAML structure comparison, not byte-for-byte)

**Coverage targets:** >80% for new Go packages; all edge cases covered (multi-word args, circular symlinks, permission errors, SDLC not found, etc.)

---

## 3. Rationale

### Why Go?

| Criterion | Python | Go | Winner |
|-----------|--------|----|----|
| Runtime dependency | Yes (3.10+) | No (static binary) | Go |
| Distribution complexity | Wheel + wrapper scripts | Single binary | Go |
| Cross-platform support | Different wrappers (.sh, .ps1) | Single binary (conditional code) | Go |
| Startup latency | ~100-200ms | <1ms | Go |
| CLI framework maturity | argparse (stdlib) | flag (stdlib) | Tie |
| Dependencies | PyYAML, requests, others | viper only | Go |
| Type safety | Dynamic | Compile-time | Go |
| Maintainability (for this team) | Good | Good (existing Go expertise) | Tie |

**Go is the clear winner for deployment simplicity, startup performance, and distribution friction.**

### Why Not Alternatives?

**Keep Python as-is:**
- ✗ Does not solve runtime dependency or cold-start latency issues
- ✗ Distribution remains complex (Python + pip/pipx required)

**Use Rust instead of Go:**
- ✗ Larger learning curve
- ✗ Slower compilation
- ✗ No existing project expertise in workspace

**Multi-language hybrid (Go dispatcher + Python subcommands via subprocess):**
- ✗ More complex than full port (subprocess handoff overhead)
- ✗ Still requires Python 3.10+ for any Python subcommand
- ✗ No net benefit over phased migration to Go

**Big-bang rewrite (all subcommands at once):**
- ✗ Cannot test against Python reference during development
- ✗ Harder to review (10k+ lines in one PR)
- ✗ Higher rollback cost if issues discovered late
- ✓ Incremental phasing chosen instead

---

## 4. Consequences

### Positive

1. **Distribution simplified:** Single static Go binary, no Python runtime dependency
   - Easier for air-gapped environments, container images, CI/CD systems
   - `pip install cadre` (or equivalent) installs just a binary, no interpreted code
   - Long-term: pure Go distribution (Homebrew, apt, etc.) becomes viable

2. **Startup performance improved:** ~100-200ms Python overhead eliminated
   - CLI responsiveness improves for users running many cadre commands
   - CI/CD pipelines using cadre see faster feedback loops

3. **Cross-platform parity:** Single binary (with conditional OS-specific code) replaces shell/PowerShell wrappers
   - Identical behavior on Windows, macOS, Linux
   - Fewer environment-specific bugs
   - Easier to maintain and test

4. **Type safety:** Compile-time error detection for config, routing, dispatch logic
   - Fewer runtime surprises than Python
   - Refactoring confidence (Go compiler catches breaking changes)

5. **No new runtime dependencies:** Minimal deps (viper only) keeps binary small and fast
   - Aligns with library-standards.yaml preferences

### Negative / Trade-offs

1. **Effort cost:** ~80 hours to implement and test all phases
   - Blocks engineer for 7 weeks
   - Must maintain two implementations during transition (Python fallback)

2. **Knowledge store stays Python (for now):**
   - Subcommands like `cadre knowledge` still require Python 3.10+
   - Partial benefit: CLI dispatcher is faster, but knowledge store is slower
   - Future decision: reimplement knowledge store in Go (deferred to Phase 4)

3. **New language in repository:**
   - Adds Go linting/testing/build infrastructure
   - Requires Go expertise (mitigated: expertise exists in workspace)
   - CI/CD must build multiple platforms (handled by Makefile + cross-compilation)

4. **Testing complexity during transition:**
   - Compatibility tests must compare Go output to Python reference
   - Golden file maintenance (Python reference tests + Go equivalents)

### Risk Mitigation

1. **Incremental phasing:** Can stop at any phase if issues emerge
2. **Compatibility layer:** Python fallback during transition means no hard cutoff
3. **Comprehensive testing:** Unit + integration + compatibility tests catch regressions
4. **Code review:** All Go code reviewed by backend-engineer + code-reviewer
5. **Rollback path:** Python CLI remains in tree as fallback during phases 1-3

---

## 5. Implementation Plan

### Phase 1: Core Infrastructure (Weeks 1-2, ~30 hours)
- Go module setup (`go.mod`, `cmd/cadre/main.go`, `Makefile`)
- Dispatcher and version detection
- Config resolver (exact replica of settings.py precedence chain)
- SDLC delegation with provider injection (exact replica)
- Interop layer for Python fallback
- Platform-specific paths (POSIX/Windows)
- Unit tests for dispatcher, config, SDLC, paths
- Cross-platform build (linux, darwin, windows)

### Phase 2: High-Value Orchestration (Weeks 3-4, ~20 hours)
- Port `cadre select` (deterministic agent selection)
- Port `cadre selection-telemetry` (telemetry summarization)
- Port `cadre profile` (provider/profile drift)
- Compatibility tests (Go output vs Python reference)
- Integration tests (end-to-end CLI usage)

### Phase 3: Generators (Weeks 5-6, ~25 hours)
- Port `cadre generate-role-metadata` (catalog + routing)
- Port `cadre generate-plugin` (most complex; package generation)
- Port `cadre generate-authority-aides` (authority role generation)
- Compatibility tests for all generators
- Stress testing (large catalogs, complex routing)

### Switchover (Week 7, ~5 hours)
- `bin/cadre` modified to invoke Go binary (or embedded in wheel)
- CI updated to build and test Go binary
- Python fallback layer tested and validated
- Documentation updated (README, CLAUDE.md, AGENTS.md)
- Gradual rollout to users (dogfood internally, then release)

### Post-Refactor (Phase 4+, deferred)
1. Reimplement knowledge store in Go (adds db/x dependencies, larger scope)
2. Decide on long-term distribution model (pure Go vs PyPI-embedded)
3. Add observability/telemetry to Go CLI (optional, off by default)

---

## 6. Validation Checklist

**Before implementation starts:**
- [x] Architecture document approved (CADRE_CLI_GO_ARCHITECTURE.md)
- [x] ADR recorded and accepted (this document)
- [x] Scope validated against CLAUDE.md and AGENTS.md
- [x] Dependencies approved (viper only; library-standards.yaml compliance checked)
- [x] Team has Go expertise
- [x] No conflicting priorities

**During implementation (per phase):**
- [ ] Unit tests pass (>80% coverage)
- [ ] Linting passes (golangci-lint, gofmt, goimports)
- [ ] Cross-platform builds succeed (linux, darwin, windows)
- [ ] Compatibility tests pass (Go matches Python reference)
- [ ] Code review completed (code-reviewer, security-reviewer)
- [ ] Documentation updated

**Before shipping:**
- [ ] All 18 subcommands routable (via Go or Python fallback)
- [ ] Backward compatibility verified (same CLI interface, same output, same errors)
- [ ] Performance benchmarks show improvement
- [ ] Security audit passed (no secrets in config, scope validation strict)
- [ ] User documentation updated
- [ ] Release notes prepared

---

## 7. References

- **Architecture document:** `CADRE_CLI_GO_ARCHITECTURE.md` (comprehensive 1100+ line design spec)
- **Current CLI implementation:** `bin/cadre`, `bin/cadre.py`, `bin/cadre.ps1`, `bin/subcommands.tsv`
- **Config resolver (Python reference):** `roster/shared/src/settings.py`
- **Library standards:** `roster/shared/library-standards.yaml` (Go tool preferences)
- **Team profile:** `roster/shared/team-profile.yaml` (primary_language: golang)
- **Related workspace:** `/home/deagy/sdk/pkg/` (Go monorepo with proven expertise)

---

## 8. Decision Log

**Decided by:** Daniel Eagy (Human Project Lead)  
**Date:** 2026-08-13  
**Approval:** ACCEPTED (human authorization explicit, ready for implementation)  
**Human Approval:** User explicitly authorized proceeding with refactoring despite agent escalations. Architecture document committed to repository. Full authorization chain established.

---

**This ADR formalizes the decision to refactor Cadre CLI from Python to Go, authorizing the implementation work to proceed in three phases with formal architecture and design documents.**
