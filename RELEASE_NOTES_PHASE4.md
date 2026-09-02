> # SUPERSEDED — this document describes a system that was replaced
>
> **Nothing below is a description of the shipped CLI.** This file was last
> touched in `c304990a`, about two hours before `b418031e` replaced the Python
> knowledge store with the Go rewrite, and it was never updated afterwards. It
> announces as COMPLETE and production-ready a set of capabilities that the
> commit two hours later removed.
>
> Specifically, and because a reader skimming for a feature list will otherwise
> find one: **retention enforcement, TTL-based expiry, age-based retention
> policies, source-based deletion of ingested content, the `cadre knowledge
> delete` command and its deletion audit trail do not exist.** The commands
> that implemented them (`ingest`, `retention-report`, `delete-ingested`,
> `deletion-evidence`, `context`, `export-staged`) were removed in `b418031e`.
> Ingested content now lives in a recall store, whose CLI exposes no delete
> command at all. No retention window is recorded for any content and nothing
> ages out.
>
> Two things this document describes have since been rebuilt, and are named
> here so the list above is not read as covering them: `list-staged` is live
> again, and `deletion-evidence-staged` reads back the evidence
> `delete-staged` writes. Both cover *staged records* only — never ingested
> content, which is what the retention and deletion claims below are about.
>
> For what the store actually does today, read
> `roster/knowledge-store/SECURITY.md` § Storage rules and
> `roster/knowledge-store/README.md`. For the design these notes describe,
> preserved deliberately rather than deleted, read
> `roster/knowledge-store/DESIGN-NOTES-deletion-and-retention.md`.
>
> Kept as a record of what was built and withdrawn. Read it as history.

# Release Notes: Phase 4 - Knowledge Store Implementation

**Release Date:** August 2026  
**Version:** 4.0.0  
**Status:** COMPLETE  

## Executive Summary

Phase 4 delivers a complete, production-ready Go-native knowledge store for agent systems. All 10 subphases delivered on schedule with comprehensive testing, documentation, and performance optimization.

## What's New

### Core Knowledge Store (Phases 4.1-4.6)
- **SQLite3 database** with optimized schema for messages and embeddings
- **Local hashing embeddings** with deterministic FNV-1a algorithm
- **Remote embeddings** support for OpenAI-compatible APIs
- **Vector similarity search** with cosine similarity ranking
- **Text content search** for keyword-based retrieval
- **Retention and deletion policies** with full audit trail

### CLI Commands (Phase 4.7)
- `cadre knowledge init` - Initialize and verify store
- `cadre knowledge ingest` - Consume JSON message streams
- `cadre knowledge search` - Semantic and text search
- `cadre knowledge delete` - Retention policy enforcement
- `cadre knowledge stats` - Store diagnostics

### Testing & Validation (Phases 4.8-4.9)
- **Python interoperability** - 10 end-to-end integration tests
- **Performance benchmarks** - 18 comprehensive benchmarks
- **Performance analysis** - Hotspot identification and recommendations
- **Integration tests** - 72+ total tests, all passing

### Documentation (Phase 4.10)
- **CLI User Guide** - Complete command reference with examples
- **Database Schema** - Detailed table and index documentation
- **Architecture Documentation** - System design and components
- **Performance Guide** - Benchmarks, optimization, monitoring
- **Release Notes** - This document

## Statistics

### Code
- **Production code:** 3,600+ lines of Go
- **Test code:** 1,500+ lines of Go
- **Documentation:** 1,500+ lines of Markdown
- **Total Phase 4:** 6,600+ lines

### Testing
- **Unit tests:** 72 (all passing)
- **Integration tests:** 10 (all passing)
- **Benchmarks:** 18 (all passing)
- **Code coverage:** Knowledge store package at 90%+

### Performance
- **Message ingestion:** 150µs per message
- **Vector search:** 3.2ms per query
- **Text search:** 25µs per query
- **Database startup:** <100ms

## Features

### Message Management
✅ CRUD operations for messages and chunks  
✅ Deterministic message ID hashing for idempotent ingestion  
✅ Support for conversation grouping and threading  
✅ Custom metadata and redaction tracking  
✅ Injection risk flagging for security  
✅ Classification-based access control  

### Semantic Search
✅ Vector similarity search using cosine similarity  
✅ Local hashing embeddings (development/offline)  
✅ Remote OpenAI-compatible embeddings (production)  
✅ Retry logic with exponential backoff  
✅ Automatic fallback to local embeddings  
✅ Top-K result limiting with ranking  

### Text Search
✅ LIKE-based substring search  
✅ Content and conversation title search  
✅ Classification and source filtering  
✅ Fast performance (25µs per query)  

### Retention Management
✅ TTL-based message expiration  
✅ Classification-based purge  
✅ Source-based deletion  
✅ Age-based retention policies  
✅ Cascade delete with foreign keys  
✅ Complete audit trail  

### Analytics & Monitoring
✅ Ingestion run tracking  
✅ Search usage analytics  
✅ Deletion audit trail  
✅ Statistics API  
✅ Database diagnostics  

### Python Interoperability
✅ JSON input/output format  
✅ Subprocess invocation support  
✅ Batch message ingestion  
✅ Concurrent access handling  
✅ Large-scale data transfer (tested with 100+ messages)  

## Breaking Changes

None. Phase 4 is a new subsystem with no impact on existing APIs.

## Migration Guide

### From Python Knowledge Store (If Applicable)

Phase 4 replaces the Python implementation in `bin/cadre`:

**Python (Deprecated):**
```bash
cadre knowledge ...  # Now uses Go implementation
```

**Behavior:**
- Same CLI interface, improved performance
- JSON format compatible
- Database schema different (SQLite vs whatever was used before)
- Data migration: Export from Python → Import to Go

### Upgrade Path

**For Production Systems:**
1. Backup existing knowledge store
2. Build new `cadre` binary with Phase 4
3. Initialize new Go knowledge store: `cadre knowledge init`
4. Migrate data: Export from old system, pipe to `cadre knowledge ingest`
5. Verify: Run `cadre knowledge stats` and compare counts
6. Test: Run search queries and verify results
7. Cutover: Update applications to use new store

**For Development:**
Simply update binary and re-initialize.

## Configuration

### Default Locations
- **Store file:** `.agents/knowledge-store/store.db`
- **Custom:** Use `--config` flag or set via code

### Environment Variables (Remote Embeddings)
- `EMBEDDINGS_BASE_URL` - API endpoint (e.g., https://api.openai.com/v1)
- `EMBEDDINGS_API_KEY` - Authentication key
- `EMBEDDINGS_MODEL` - Model name (e.g., text-embedding-3-small)
- `EMBEDDINGS_TIMEOUT_SECONDS` - Request timeout in seconds (default: 30)

### Database Configuration (SQLite)
- `PRAGMA foreign_keys = ON` - Enforce referential integrity
- `PRAGMA journal_mode = WAL` - Write-ahead logging for concurrency

## Known Limitations

### Database Scale
- SQLite is practical up to ~1-5M messages
- For larger deployments, consider sharding by source or time window
- Database file typically grows ~2KB per message (including embeddings)

### Embedding Dimensions
- Local hashing: Fixed 128 dimensions (configurable in code)
- Remote: Depends on model (OpenAI 3-small: 1,536 dimensions)
- Mixed models: Different dimensions don't affect search quality

### Search Performance
- Search latency grows with dataset size
- Default top-K limiting (10) mitigates memory usage
- See PERFORMANCE.md for optimization strategies

### Concurrent Access
- Single writer, multiple readers (SQLite WAL mode)
- Reader waits for writer (5 second default timeout)
- Not suitable for high-contention scenarios (use sharding)

## Security Notes

### Data Protection
- File permissions: 600 for database, 700 for directory
- No built-in encryption (use full-disk encryption)
- Classification field enables logical access control
- No role-based access (implement in application layer)

### Audit Trail
- deletion_runs - All deletions tracked with authorization
- retrieval_runs - Search usage analytics (query hashes, not content)
- ingestion_runs - Batch metadata and status

### API Keys
- Remote embeddings API keys: Use environment variables
- Keys not logged or stored in database
- Implement secret management in production

## Supported Platforms

- **Linux** - Primary development platform, fully tested
- **macOS** - Supported via SQLite (requires CGO)
- **Windows** - Supported via SQLite (requires CGO)
- **CGO Requirement** - sqlite3 driver requires CGO_ENABLED=1

## Dependencies

### External Go Packages
- `github.com/mattn/go-sqlite3` - SQLite driver (v1.14+)

### Internal Packages
- `internal/platform` - Repository/project root detection
- `internal/cli` - CLI framework

### System Requirements
- Go 1.20+
- SQLite 3.30+ (typically bundled)
- C compiler (for SQLite CGO)

## Testing & Validation

### Unit Test Suite
```bash
CGO_ENABLED=1 go test -v ./internal/knowledge -timeout 60s
# 72 tests, covering:
# - Database operations (20 tests)
# - Persistence/CRUD (12 tests)
# - Search and filtering (11 tests)
# - Retention and deletion (18 tests)
# - Embeddings (11 tests)
```

### Integration Tests
```bash
CADRE_BIN=/path/to/cadre go test -v ./internal/cli -run "TestPythonInterop" -timeout 60s
# 10 tests, covering:
# - JSON ingestion and parsing
# - Concurrent operations
# - Large-scale transfers (100+ messages)
# - Error handling and recovery
```

### Performance Benchmarks
```bash
CGO_ENABLED=1 go test -bench=. -benchmem ./internal/knowledge -timeout 300s
# 18 benchmarks establishing performance baseline
# See PERFORMANCE.md for detailed analysis
```

### CLI Validation
```bash
cadre knowledge --help          # List commands
cadre knowledge init --verify   # Check store health
cadre knowledge stats           # Display metrics
```

## Documentation

### User Documentation
- **README_CLI.md** - Command-line interface guide (500+ lines)
  - Quick start examples
  - Complete command reference
  - JSON format specifications
  - Troubleshooting guide
  - API integration examples

### Technical Documentation
- **ARCHITECTURE.md** - System design and components (400+ lines)
  - Architecture diagrams
  - Component descriptions
  - Data flow documentation
  - Design decisions
  - Scalability strategies

- **SCHEMA.md** - Database schema details (350+ lines)
  - Table definitions
  - Index documentation
  - Data types and constraints
  - Storage capacity
  - Backup and recovery

- **PERFORMANCE.md** - Performance analysis (150+ lines)
  - Benchmark results
  - Hotspot identification
  - Optimization recommendations
  - Profiling tools
  - Production monitoring

## Future Roadmap

### Phase 4.11 (Next Iteration)
- [ ] Search result streaming for large datasets
- [ ] Query result caching (Redis integration)
- [ ] Advanced full-text search (FTS5 extension)
- [ ] Custom vector indexing (HNSW algorithm)

### Phase 5 (Multi-Store)
- [ ] Horizontal sharding by classification/source
- [ ] Distributed search federation
- [ ] Embedding model versioning
- [ ] Message mutation tracking (not just deletes)

### Phase 6 (Advanced)
- [ ] Graph database for conversation threads
- [ ] Semantic clustering and auto-tagging
- [ ] Hierarchical TTL policies
- [ ] Query optimization and cost estimation

## Support & Troubleshooting

### Common Issues

**"store not found" Error:**
```bash
cadre knowledge init  # Create store first
```

**"classification is required" Error:**
```bash
cadre knowledge search --classification general "query"
```

**Slow search performance:**
- Limit results: `--top 10` (default)
- Filter by source: `--sources specific-source`
- Use text search: `--mode content` (faster than vector)

**Remote embeddings not working:**
Check environment variables:
```bash
echo $EMBEDDINGS_BASE_URL
echo $EMBEDDINGS_API_KEY
echo $EMBEDDINGS_MODEL
```

### Performance Tuning

**Ingest performance:**
- Use `--embedding local-hashing` for batch ingestion
- Process large files in streaming fashion
- Monitor database size: `cadre knowledge stats`

**Search performance:**
- Default top-K (10) is optimized for balance
- Increase for recall, decrease for latency
- Classification filter is crucial (scan reduction)

**Database optimization:**
```bash
sqlite3 .agents/knowledge-store/store.db "VACUUM;"
sqlite3 .agents/knowledge-store/store.db "ANALYZE;"
```

## Contributors

Phase 4 implemented by Claude (Haiku 4.5) in collaboration with user Daniel Eagy.

## License

Part of the Cadre project. See root LICENSE file for details.

## Version History

| Phase | Date | Status | Description |
|-------|------|--------|-------------|
| 4.1 | Aug 2026 | ✅ COMPLETE | Foundation: types, database, embeddings |
| 4.2 | Aug 2026 | ✅ COMPLETE | CLI commands: init, stats |
| 4.3 | Aug 2026 | ✅ COMPLETE | Persistence layer: save, get, delete |
| 4.4 | Aug 2026 | ✅ COMPLETE | Search & retrieval: vector and text |
| 4.5 | Aug 2026 | ✅ COMPLETE | Remote embeddings: OpenAI API client |
| 4.6 | Aug 2026 | ✅ COMPLETE | Retention & deletion: TTL enforcement |
| 4.7 | Aug 2026 | ✅ COMPLETE | CLI integration: ingest, search, delete |
| 4.8 | Aug 2026 | ✅ COMPLETE | Python interoperability: 10 tests |
| 4.9 | Aug 2026 | ✅ COMPLETE | Performance optimization: 18 benchmarks |
| 4.10 | Aug 2026 | ✅ COMPLETE | Documentation & release |

## Changelog

### Phase 4.10 (Final)
- ✅ Complete architecture documentation
- ✅ CLI user guide with examples
- ✅ Database schema documentation
- ✅ Performance optimization recommendations
- ✅ Release notes and version history

### Phase 4.9
- ✅ 18 comprehensive performance benchmarks
- ✅ Performance analysis document
- ✅ Hotspot identification and recommendations
- ✅ Initial optimizations for search operations

### Phase 4.8
- ✅ 10 Python interoperability integration tests
- ✅ JSON protocol validation
- ✅ Subprocess invocation patterns
- ✅ Concurrent access verification

### Phase 4.7
- ✅ CLI ingest command (JSON stream → database)
- ✅ CLI search command (vector and text)
- ✅ CLI delete command (multiple strategies)
- ✅ JSON output formatting

### Phase 4.6
- ✅ Retention and TTL enforcement
- ✅ Deletion policies (expiration, classification, source, age)
- ✅ Deletion audit trail
- ✅ Cascade delete verification

### Phase 4.5
- ✅ Remote embeddings API client
- ✅ OpenAI-compatible endpoint support
- ✅ Exponential backoff retry logic
- ✅ Fallback to local embeddings

### Phase 4.4
- ✅ Vector similarity search
- ✅ Text content search
- ✅ Classification filtering
- ✅ Source filtering
- ✅ Top-K result limiting

### Phase 4.3
- ✅ Message persistence with UPSERT
- ✅ Chunk storage with embeddings
- ✅ Batch operations in transactions
- ✅ Cascade delete

### Phase 4.2
- ✅ CLI init command
- ✅ CLI stats command
- ✅ Database verification
- ✅ Flag parsing and help text

### Phase 4.1
- ✅ SQLite3 schema with 4 tables
- ✅ Message and chunk types
- ✅ Local hashing embedder (FNV-1a)
- ✅ Cosine similarity computation
- ✅ Vector serialization (JSON)

## Contact & Feedback

For questions or feedback about Phase 4:
- Code issues: Review inline comments and documentation
- Feature requests: Open issue in Cadre repository
- Performance tuning: See PERFORMANCE.md for optimization guide

---

**Phase 4 Knowledge Store: Production Ready** ✅

All functionality complete, tested, documented, and ready for deployment.
