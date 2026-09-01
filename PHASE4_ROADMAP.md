> # SUPERSEDED — this roadmap was not followed to completion
>
> This document plans work on the Python-era knowledge store and predates
> `b418031e`, the commit that replaced it with the Go rewrite. It was never
> updated afterwards, and the ✅ COMPLETE marks below describe that earlier
> implementation, not the shipped one.
>
> **Phase 4.6 was never built into the current CLI.** `Store.DeleteExpired`,
> TTL enforcement, cascade delete and the deletion audit log described there
> have no counterpart today: retention windows are not recorded, nothing ages
> out, and there is no command that deletes ingested content. Whether that is
> rebuilt or declared permanently out of scope is an open decision, recorded in
> `roster/knowledge-store/SECURITY.md` § Storage rules.
>
> Kept as a record of what was planned. Read it as history.

# Phase 4: Go CLI Refactoring — Completion Roadmap

## Executive Summary

Phase 4 of the Go CLI refactoring focuses on three core workstreams:
1. **CLI Scope Cleanup** — Remove non-essential infrastructure ✅ COMPLETE
2. **Knowledge Store Foundation** — Establish Go database layer ✅ COMPLETE  
3. **Distribution Strategy** — Define packaging and release process ✅ COMPLETE
4. **Completion Roadmap** — Plan next phases (this document)

**Current Status**: Foundation complete. Ready for incremental feature implementation.

**Total Effort**: 9-10 hours foundation + 30-35 hours for full knowledge store port (Phases 4.2-4.6)

---

## 1. Foundation Complete (Committed)

### Track 1: CLI Scope Cleanup ✅
**Commit**: `1b6f31a7`

Removed out-of-scope infrastructure packages:
- `internal/server/` (14 files, 2.8KB) — HTTP endpoints, not needed for CLI
- `internal/production/` (7 files, 1.1KB) — deployment config, not needed
- `internal/observability/` (3 files, 0.2KB) — distributed tracing, not needed

Result: **4 KB reduction**, cleaner codebase focused on CLI functionality

**What Remains**:
- ✅ `internal/cli/` — dispatcher, routing, SDLC delegation
- ✅ `internal/config/` — settings resolver, precedence chain
- ✅ `internal/generators/` — role metadata, plugin, authority aides
- ✅ `internal/orchestration/` — select, execute commands
- ✅ `internal/interop/` — Python subprocess bridge
- ✅ `internal/platform/` — cross-platform paths
- ✅ `cmd/cadre/` — entry point

### Track 2: Knowledge Store Foundation ✅
**Commit**: `9c1fd77b`

Established Go foundation for vectorized knowledge store:

**Packages Created**:
- `internal/knowledge/types.go` — 100+ lines of core types (Message, Chunk, IngestionRun, etc.)
- `internal/knowledge/database.go` — 400+ lines of SQLite layer with schema matching Python exactly
- `internal/knowledge/embeddings.go` — 300+ lines of vector operations (hashing, normalization, search)
- `internal/knowledge/README.md` — Architecture doc + 6-phase completion roadmap
- `*_test.go` files — Unit tests for embeddings (database tests require CGO)

**Components Ready**:
- ✅ Database schema (4 tables, 5 indexes)
- ✅ Schema migrations infrastructure
- ✅ Ingestion run lifecycle (begin/complete/fail)
- ✅ Deterministic local hashing embeddings (128-dim, FNV-1a)
- ✅ Vector search index with cosine similarity
- ✅ Store statistics collection

**Not Yet Implemented**:
- ⏳ Message/chunk persistence (Phase 4.3)
- ⏳ Full-text search (Phase 4.4)
- ⏳ Remote embeddings API (Phase 4.5)
- ⏳ Retention/deletion (Phase 4.6)
- ⏳ CLI command wrappers (Phase 4.2)

**Dependencies Added**:
- `github.com/mattn/go-sqlite3 v1.14.49` — SQLite driver (requires CGO)

### Track 3: Distribution Strategy ✅
**File**: `DISTRIBUTION.md`

Comprehensive distribution plan covering:
- **Direct Downloads**: GitHub Releases with pre-built binaries
- **Package Managers**: Homebrew, apt, dnf, pacman
- **PyPI**: Backward-compatible wrapper (ship Go binary in Python package)
- **Container**: Docker images on ghcr.io
- **Release Process**: Pre/during/post-release steps
- **Build Configuration**: Makefile targets, CGO requirements
- **Platform Notes**: Linux, macOS, Windows specifics
- **Roadmap**: Timeline from v0.24.0 through v1.0.0

---

## 2. Next Phase: 4.2 CLI Commands

### Scope
Port simple CLI commands that don't require full ingestion pipeline:
- `cadre knowledge init` — initialize or verify store
- `cadre knowledge stats` — display store statistics  
- Stub `search` and `context` (placeholder messages indicating work in progress)

### Deliverables
- `internal/cli/knowledge.go` — CLI command dispatcher
- `internal/cli/knowledge_test.go` — unit tests
- Integration with main dispatcher (route `knowledge` to Go)

### Effort: 4-5 hours
- 2 hours: CLI command scaffolding + flag parsing
- 1 hour: Integrate with dispatcher
- 1 hour: Unit tests
- 30 min: Documentation updates

### Success Criteria
- `cadre knowledge init` creates database with proper schema
- `cadre knowledge stats` displays accurate store metrics
- All tests pass
- CLI help text includes knowledge commands

---

## 3. Phase 4.3: Message/Chunk Persistence

### Scope
Implement persistence layer for ingesting and storing messages:
- `Store.SaveMessage()` — persist message record
- `Store.SaveChunk()` — persist chunked content with embeddings
- Handle duplicate detection (UPSERT)
- Foreign key constraints and cascading deletes

### Deliverables
- `internal/knowledge/persistence.go` (300+ lines)
- `*_test.go` with fixtures for various message types
- Integration tests with real database

### Effort: 6-8 hours
- 3 hours: SaveMessage() implementation
- 2 hours: SaveChunk() and embedding integration
- 1.5 hours: UPSERT logic and duplicate handling
- 1.5 hours: Tests and edge case validation

### Success Criteria
- Messages round-trip correctly through database
- Embeddings stored and retrieved accurately
- Duplicates handled per Python spec
- Cascade delete works correctly

---

## 4. Phase 4.4: Search/Retrieval

### Scope
Full-text and vector search with filtering:
- `Store.Search()` — query messages with filters
- Classification filtering
- Source filtering (optional all-sources mode)
- Vector similarity ranking

### Deliverables
- `internal/knowledge/search.go` (250+ lines)
- SearchOptions and SearchResult types
- Retrieval analytics tracking

### Effort: 5-6 hours
- 2 hours: SQL query construction with filters
- 2 hours: Vector similarity ranking
- 1 hour: Retrieval analytics logging
- 1 hour: Tests with various filter combinations

### Success Criteria
- Queries return ranked results by similarity
- Filters work independently and combined
- Classification + source filtering validated
- Retrieval runs tracked for analytics

---

## 5. Phase 4.5: Remote Embeddings

### Scope
OpenAI-compatible embeddings API client:
- Configuration loading (base_url, api_key, model, timeout)
- HTTP client with retries
- Rate limiting
- Fallback to local hashing on failure

### Deliverables
- `internal/knowledge/remote_embeddings.go` (250+ lines)
- RemoteEmbedder implementing EmbeddingProvider interface
- Configuration validation

### Effort: 4-5 hours
- 1.5 hours: HTTP client implementation
- 1 hour: Configuration validation
- 1 hour: Error handling + fallback
- 1 hour: Tests with mock server

### Success Criteria
- Can embed texts via OpenAI-compatible API
- Falls back gracefully on network errors
- Rate limiting prevents hammering server
- Config validation catches misconfiguration early

---

## 6. Phase 4.6: Retention & Deletion

### Scope
Retention policy enforcement:
- Delete messages past retention window
- Cascade delete chunks
- Evidence tracking for deletions
- Export/import authorizations

### Deliverables
- `internal/knowledge/retention.go` (200+ lines)
- `Store.DeleteExpired()` and `Store.DeleteByID()`
- Deletion evidence audit trail

### Effort: 3-4 hours
- 1.5 hours: Deletion logic
- 1 hour: Cascade delete verification
- 1 hour: Evidence tracking + tests

### Success Criteria
- Expired messages deleted cleanly
- Associated chunks cascade-deleted
- Deletion tracked in audit log
- No orphaned chunks remain

---

## 7. Post-Foundation: Integration & Transition

### Phase 4.7: CLI Integration (2 hours)
- Port all knowledge store subcommands to Go dispatcher
- Update CLI help to mark commands as Go-native
- Remove Python fallback for knowledge commands

### Phase 4.8: Python Interop Tests (3 hours)
- End-to-end tests: Python ingests → Go retrieves
- End-to-end tests: Go ingests → Python retrieves
- Verify database compatibility both directions

### Phase 4.9: Performance Optimization (4-5 hours)
- Benchmark vs Python implementation
- Profile for bottlenecks
- Index optimization for large datasets
- Connection pooling if needed

### Phase 4.10: Documentation & Release (3-4 hours)
- User guide for knowledge store
- API reference
- Migration guide for Python users
- Release notes and announcements

---

## Critical Path

```
Foundation (✅ Done)
    ├─ Track 1: CLI Cleanup (✅ committed)
    ├─ Track 2: DB Foundation (✅ committed)
    └─ Track 3: Distribution (✅ documented)
         ↓
    Phase 4.2: CLI Commands (4-5 hours)
         ↓
    Phase 4.3: Persistence (6-8 hours)
         ↓
    Phase 4.4: Search (5-6 hours)
         ↓
    Phase 4.5: Remote Embeddings (4-5 hours)
         ↓
    Phase 4.6: Retention (3-4 hours)
         ↓
    Phase 4.7-4.10: Integration & Release (12-15 hours)
```

**Total Remaining**: ~39-47 hours (approximately 5 weeks at 8 hours/week)

---

## Resource Requirements

### Skills Needed
- Go: SQL database operations, HTTP client, testing
- Database: SQLite, query optimization, transaction handling
- Vector math: Cosine similarity, normalization (foundation done)
- Testing: Unit tests, integration tests, mocks

### Infrastructure
- SQLite3 development headers (for CGO)
- OpenAI-compatible embedding endpoint (optional for testing)
- Linux/macOS/Windows for cross-platform testing

### Deployment
- CI/CD: Build tests on multiple platforms
- Release automation: Makefile + GitHub Actions
- Distribution: PyPI, Homebrew tap, GitHub Releases

---

## Success Criteria

### Phase 4 Complete Means:
- [ ] All 6 foundation components implemented and tested
- [ ] CLI commands fully working (no Python fallback)
- [ ] Search/retrieval with all filters working
- [ ] Remote embeddings optional but functional
- [ ] Retention/deletion policies enforced
- [ ] Python ↔ Go interop verified
- [ ] Performance parity with Python
- [ ] Distribution plan executable
- [ ] All tests passing (including integration)
- [ ] Documentation complete

### Release Criteria (v0.24.0+):
- [ ] All success criteria met
- [ ] Security audit passed
- [ ] Dependency audit passed
- [ ] Cross-platform builds verified
- [ ] Release notes prepared
- [ ] PyPI package buildable
- [ ] Docker image buildable

---

## Schedule Estimate

**Assuming 1 engineer, 8 hours/week**:
- Weeks 1-2: Phase 4.2 (CLI commands)
- Weeks 2-3: Phase 4.3 (persistence)
- Weeks 4-5: Phase 4.4 (search/retrieval)
- Weeks 5-6: Phase 4.5 (remote embeddings)
- Weeks 6-7: Phase 4.6 (retention)
- Weeks 7-8: Integration, testing, release prep

**Timeline**: ~8 weeks to full Phase 4 completion

---

## Risks & Mitigations

| Risk | Mitigation |
|------|-----------|
| CGO complexity in cross-compilation | Consider pure-Go SQLite (`modernc.org/sqlite`) if issues arise |
| Vector search performance at scale | Implement proper indexes; benchmark with large datasets early |
| API compatibility with Python | End-to-end interop tests; Python → Go → Python round-trips |
| Retention policy edge cases | Comprehensive test matrix (TTL, cascade, evidence) |
| Dependency issues | Regular `go mod tidy` + `govulncheck` |

---

## References

- **Foundation Code**: `internal/knowledge/`
- **Distribution**: `DISTRIBUTION.md` (this repo)
- **Python Reference**: `roster/knowledge-store/src/` (source of truth)
- **Architecture**: `internal/knowledge/README.md`
- **Build**: `Makefile` (cross-platform targets)

---

## Next Action

Once Phase 4 foundation is merged:
1. Create feature branch for Phase 4.2: `feature/ks-cli-commands`
2. Start with `internal/cli/knowledge.go` stub
3. Implement `init` and `stats` commands
4. Target: 1-week iteration for Phase 4.2

**Estimated Completion**: End of Phase 4.2 by end of this week (sprint-based estimate depends on availability).
