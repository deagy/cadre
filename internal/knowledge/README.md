# Knowledge Store Go Foundation

## Overview

This package (`internal/knowledge`) is a partial Go implementation of the vectorized knowledge store, providing a foundation for gradual migration from Python. It is **not** a complete knowledge store replacement — it is designed to be completed incrementally across multiple phases.

**Current Status: Phase 4.1 Foundation**
- ✅ Database schema and SQLite persistence
- ✅ Core data types (Message, Chunk, IngestionRun, etc.)
- ✅ Local hashing embeddings (deterministic, offline)
- ✅ Vector search infrastructure
- 🚧 CLI commands (stubbed, planned for 4.2)
- 🚧 Ingestion workflow (planned for 4.3)
- 🚧 Full text search (planned for 4.4)
- 🚧 Remote embeddings API client (planned for 4.5)
- 🚧 Retention/deletion policies (planned for 4.6)

## Packages

### types.go
Defines core types matching the Python schema:
- `Message` — a persisted message with embeddings
- `Chunk` — a chunked piece of message content
- `IngestionRun` — metadata for an ingestion operation
- `RetrievalRun` — analytics for a search operation
- `StoreStats` — summary statistics
- `EmbeddingProvider` — interface for embedding implementations
- `SearchOptions` & `SearchResult` — retrieval contracts

### database.go
SQLite persistence layer:
- `Store` — main database connection and operations
- Schema creation with full `CREATE TABLE` and indexes (matching Python)
- Migration infrastructure for schema evolution
- Ingestion run tracking (begin/complete/fail)
- Methods: `Open()`, `Close()`, `Stats()`
- Planned: `SaveMessage()`, `SaveChunk()`, `Search()`, etc.

**Build Requirements**: SQLite requires CGO to be enabled:
```bash
CGO_ENABLED=1 go build ./internal/knowledge/...
```

### embeddings.go
Vector embedding providers:
- `LocalHashingEmbedder` — deterministic offline embeddings (128-dim default)
  - Feature hashing with FNV-1a
  - L2 normalization
  - Compatible with Python's `text_embedding.hashing_embedding()`
- `SearchIndex` — in-memory cosine similarity search
- Utilities: `CosineSimilarity()`, `VectorToJSON()`, `JSONToVector()`

Tests include deterministic embedding validation and vector serialization.

## Migration Roadmap

### Phase 4.2: CLI Commands (~4-5 hours)
Port simpler CLI commands that don't require full ingestion:
- `cadre knowledge init` — create/verify store
- `cadre knowledge stats` — display store statistics
- Stub out `search`, `context` (require full ingestion)

### Phase 4.3: Ingestion Workflow (~6-8 hours)
Implement message + chunk persistence:
- `SaveMessage()` — persist message record with protection/redactions
- `SaveChunk()` — persist chunked content with embeddings
- Duplicate handling (UPSERT logic matching Python)
- Ingestion run lifecycle

### Phase 4.4: Search/Retrieval (~5-6 hours)
Full text search and vector retrieval:
- `Search()` — query messages by classification, source, retention
- Similarity search using local or remote embeddings
- Classification + source filtering
- Retrieval analytics tracking

### Phase 4.5: Remote Embeddings (~4-5 hours)
OpenAI-compatible embeddings API client:
- Configuration (base_url, api_key, model, timeout)
- HTTP client with proper error handling
- Rate limiting and request batching
- Fallback to local hashing on failure

### Phase 4.6: Retention & Deletion (~3-4 hours)
Retention policy enforcement:
- Deletion by retention window
- Cascade delete (messages → chunks)
- Retention evidence tracking
- Export/import authorizations

**Total Foundation + All Phases: ~30-35 hours**

## Architecture Notes

### Schema Compatibility
The SQLite schema exactly matches `internal/contextstore/database.go`:
- Same table names, columns, types, constraints
- Same indexes for performance
- Additive migrations for future schema evolution
- Foreign key constraints with cascade delete

### Embedding Interface
`EmbeddingProvider` allows swapping implementations:
- `LocalHashingEmbedder` — for development/testing (current)
- Planned: `RemoteEmbedder` — OpenAI-compatible API
- Extensible for future providers (e.g., local Ollama)

### Testing
- Embeddings tests: ✅ pass (no CGO required)
- Database tests: require `CGO_ENABLED=1`
- Build with `go build ./internal/knowledge/...` or `CGO_ENABLED=1 go build ./...`

## Python Interoperability

During transition, Python and Go implementations will coexist:
- Python CLI routes through Go dispatcher (existing)
- Knowledge store CLI commands remain Python until ported
- Database is shared (same SQLite file)
- Embedding providers are compatible (same format/dimensions)

## Next Steps

1. **Verify foundation**: `CGO_ENABLED=1 go test ./internal/knowledge/...`
2. **Phase 4.2**: Implement CLI commands stub
3. **Phase 4.3+**: Implement persistence layer in parallel with CLI
4. **Testing**: End-to-end tests with Python → Go interop
5. **Migration**: Gradually move commands from Python to Go

## See Also

- `internal/knowledge/database.go` — SQLite implementation
- `internal/knowledge/embeddings.go` — Vector operations
- `roster/knowledge-store/src/` — Python reference implementation

## The phase reports are gone

`CLI_PHASE5.5` through `CLI_PHASE8`, `CLI_COMPLETE`, `CLI_FULLY_FUNCTIONAL`,
`CLI_STATUS`, `CLI_ROADMAP` and `MIGRATION_PYTHON_TO_GO` were deleted in
2026-08. They were 7,400 lines of build-out reports that referenced only each
other, and they had stopped being true.

They described an HNSW vector index, hybrid search, distributed streaming,
fault tolerance and replication as **Complete ✅** -- subsystems that were
unreachable from every binary and have since been removed. `CLI_PHASE8`
advertised "sub-millisecond fault detection" and "<100ms recovery overhead",
figures nothing ever measured, from the same family as the placeholder metrics
the CLI used to print. `CLI_FULLY_FUNCTIONAL` opened with "All 20+ commands are
now fully functional"; eleven of them were reporting work they had not done,
and nine now refuse.

Documentation that asserts a capability the code does not have is the same
defect as a command that prints a result it did not compute, and it is harder
to catch because nothing runs it. The history is in git if the reasoning behind
any of that work is ever wanted.

What remains here is the reference material: `ARCHITECTURE.md`, `SCHEMA.md`,
`PERFORMANCE.md` and `README_CLI.md`, each of which describes what the store
actually does.

