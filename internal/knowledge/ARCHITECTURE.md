# Knowledge Store Architecture

## Overview

The knowledge store is a Go-native semantic search and persistence layer for managing messages, embeddings, and metadata. It enables agent systems to ingest conversation history, search by semantic similarity, and enforce data retention policies.

## Architecture Diagram

```
┌─────────────────────────────────────────────────────────────────┐
│                        CLI Interface                             │
│  (cadre knowledge init|ingest|search|delete|stats)              │
└────────────────────────┬────────────────────────────────────────┘
                         │
┌────────────────────────┴────────────────────────────────────────┐
│                   Go CLI Commands                                │
│  (internal/cli/knowledge.go)                                    │
│  - knowledgeInit()                                              │
│  - knowledgeIngest()                                            │
│  - knowledgeSearch()                                            │
│  - knowledgeDelete()                                            │
│  - knowledgeStats()                                             │
└────────────────────────┬────────────────────────────────────────┘
                         │
┌────────────────────────┴────────────────────────────────────────┐
│                 Knowledge Package API                            │
│  (internal/knowledge/*.go)                                      │
├──────────────────────────────────────────────────────────────────┤
│ Core Types:                                                      │
│  - Store - Database connection and operations                   │
│  - Message - Conversation message with metadata                 │
│  - Chunk - Message segment with embedding                       │
│  - SearchOptions - Query parameters                             │
│  - EmbeddingProvider - Interface for embeddings                 │
│                                                                  │
│ Subsystems:                                                      │
│  ├─ Database (database.go) - SQLite schema & connection         │
│  ├─ Persistence (persistence.go) - CRUD operations             │
│  ├─ Search (search.go) - Vector similarity queries              │
│  ├─ Retention (retention.go) - TTL & deletion policies          │
│  ├─ Embeddings (embeddings.go) - Local hashing embedder        │
│  └─ Remote Embeddings (remote_embeddings.go) - API client       │
└────────────────────────┬────────────────────────────────────────┘
                         │
┌────────────────────────┴────────────────────────────────────────┐
│                  SQLite Database                                 │
│  (.agents/knowledge-store/store.db)                             │
│                                                                  │
│  Tables:                                                         │
│  ├─ ingestion_runs - Batch metadata                             │
│  ├─ messages - Core message records                             │
│  ├─ chunks - Message segments + embeddings                      │
│  ├─ retrieval_runs - Search analytics                           │
│  └─ deletion_runs - Audit trail                                 │
│                                                                  │
│  Indexes:                                                        │
│  ├─ idx_messages_source - Source filtering                      │
│  ├─ idx_messages_classification - Access control                │
│  ├─ idx_messages_retention - Expiration queries                 │
│  ├─ idx_chunks_model - Embedding provider lookup                │
│  └─ idx_deletion_runs_status - Audit queries                    │
└──────────────────────────────────────────────────────────────────┘
```

## Component Description

### 1. CLI Layer (`internal/cli/knowledge.go`)

Entry point for all user interactions. Provides five main commands:

**knowledgeInit()** - Database initialization and verification
- Creates database file at specified path
- Initializes schema (idempotent)
- Displays initial statistics

**knowledgeIngest()** - JSON stream consumption
- Reads JSON lines from stdin
- Validates message fields
- Tracks ingestion run metadata
- Computes embeddings for each message
- Supports both local and remote embeddings

**knowledgeSearch()** - Query interface
- Accepts semantic or text search queries
- Filters by classification (required)
- Optionally filters by source(s)
- Returns results ranked by similarity
- JSON and text output formats

**knowledgeDelete()** - Deletion policies
- Supports four deletion modes: expired, by classification, by source, by age
- Tracks all deletions with authorization metadata
- Cascade deletes associated chunks
- Provides deletion audit trail

**knowledgeStats()** - Store diagnostics
- Reports message/chunk counts
- Breaks down by source and classification
- Shows database file size
- Displays embedding models in use

### 2. Database Layer (`internal/knowledge/database.go`)

SQLite3 connection and schema management.

**Store Type:**
```go
type Store struct {
    db   *sql.DB
    path string
}
```

**Key Functions:**
- `Open(path)` - Create/open database, initialize schema
- `Stats()` - Aggregate statistics
- `BeginRun()` / `CompleteRun()` / `FailRun()` - Ingestion tracking
- `Close()` - Safe shutdown

**Schema Design:**
- Five tables (messages, chunks, ingestion_runs, retrieval_runs, deletion_runs)
- Foreign key constraints with cascade delete
- Strategic indexes for common queries
- PRAGMA settings for consistency and performance

### 3. Persistence Layer (`internal/knowledge/persistence.go`)

CRUD operations for messages and chunks.

**Message Operations:**
- `SaveMessage()` - UPSERT with deterministic ID hashing
- `GetMessage()` - Retrieve by ID
- `DeleteMessage()` - With cascade delete
- `MessageCount()` - Statistics

**Chunk Operations:**
- `SaveChunk()` - Single chunk persistence
- `SaveChunks()` - Batch operation in transaction
- `GetChunks()` - Retrieve by message ID
- `ChunkCount()` - Statistics

**UPSERT Strategy:**
- Message ID computed as SHA256(source|conversation_id|source_message_id)
- Deterministic across multiple ingestion runs
- Enables idempotent message updates
- Prevents duplicates via UNIQUE constraint

### 4. Search Layer (`internal/knowledge/search.go`)

Vector similarity and text search.

**Vector Search:**
1. Query embedding generated
2. SQL query joins messages and chunks
3. Embeddings deserialized from JSON
4. Cosine similarity computed for each chunk
5. Results sorted by similarity (descending)
6. Top-K limited and returned
7. Search metadata recorded

**Text Search:**
- LIKE-based substring matching
- Searches message content and conversation title
- No embedding required
- Fast for simple keyword searches

**SearchOptions Struct:**
```go
type SearchOptions struct {
    Query             string
    Classification    string
    SourceFilters     []string
    AllSources        bool
    EmbeddingProvider EmbeddingProvider
    Top               int
}
```

**Analytics Tracking:**
- Every search recorded in retrieval_runs
- Query hash (not content) stored for privacy
- Result count and metadata tracked
- Enables usage analytics and optimization

### 5. Retention Layer (`internal/knowledge/retention.go`)

TTL enforcement and deletion policies.

**Deletion Strategies:**
- **Expiration**: Delete messages past retention_until date
- **Classification**: Purge all messages at security level
- **Source**: Remove all messages from specific source
- **Age**: Delete messages older than N days

**Audit Trail:**
- Each deletion creates deletion_run record
- Tracks reason, authorization, counts
- Supports compliance and recovery

**Cascade Delete:**
- Foreign key with ON DELETE CASCADE
- Deleting message automatically removes chunks
- Ensures data consistency

### 6. Embedding Layer (`internal/knowledge/embeddings.go`)

Vector representation of message content.

**EmbeddingProvider Interface:**
```go
type EmbeddingProvider interface {
    Name() string
    Model() string
    Dimensions() int
    Embed(texts []string) ([][]float64, error)
}
```

**LocalHashingEmbedder:**
- Deterministic feature hashing (FNV-1a)
- No external API calls required
- Fast (2.1µs per text)
- Suitable for development and offline use
- 128-dimensional output (configurable)

**Vector Operations:**
- `CosineSimilarity()` - Compute similarity (zero allocations)
- `VectorToJSON()` - Serialize to JSON array
- `JSONToVector()` - Deserialize from JSON

### 7. Remote Embeddings Layer (`internal/knowledge/remote_embeddings.go`)

OpenAI-compatible embeddings API client.

**RemoteEmbedder:**
- Calls external API for embeddings
- Exponential backoff retry logic (1s, 2s, 4s, ...)
- Automatic fallback to local hashing on failure
- Configurable timeout and retry behavior

**Configuration:**
- Code-level: RemoteEmbedderConfig struct
- Environment variables: EMBEDDINGS_* prefix
- Validation: Required fields checked on construction

**Error Handling:**
- Network errors trigger retries
- Validation errors fail immediately (no retry)
- Fallback provides graceful degradation

## Data Flow

### Ingestion Flow

```
User JSON Message
    ↓
knowledgeIngest() - Validates JSON fields
    ↓
SaveMessage() - UPSERT with deterministic ID
    ↓
Embed(content) - Get vector representation
    ↓
SaveChunk() - Store with embedding
    ↓
BeginRun/CompleteRun - Record metadata
    ↓
SQLite Database (persistent storage)
```

### Search Flow

```
User Query
    ↓
knowledgeSearch() - Parse options
    ↓
Embed(query) - Get query vector
    ↓
Search() in Store
    ├─ SQL query with filters (classification, sources)
    ├─ Deserialize chunk embeddings
    ├─ Compute cosine similarity for each
    └─ Sort by similarity (descending)
    ↓
recordRetrievalRun() - Track search
    ↓
Format and return results
```

### Deletion Flow

```
User Request (expired/classification/source/age)
    ↓
knowledgeDelete() - Parse options
    ↓
Get target messages - Count and identify
    ↓
Transaction
    ├─ Delete from messages table
    └─ (Cascade deletes chunks via FK constraint)
    ↓
recordDeletionRun() - Audit trail
    ↓
Return deletion count
```

## Key Design Decisions

### 1. Deterministic Message IDs
**Why:** Enable idempotent ingestion from multiple sources
- ID = SHA256(source|conversation_id|source_message_id)
- Same message re-ingested doesn't create duplicate
- Simplifies distributed ingestion

### 2. SQLite Over Other Databases
**Why:** Simplicity and portability
- No server setup required
- Single file per store
- Built-in ACID transactions
- Sufficient for typical use cases (<1M messages)

### 3. Separate Chunks Table
**Why:** Support semantic search at subsentence level
- Messages can be chunked for better search granularity
- Multiple embeddings per message (different models)
- Reduces memory usage for large messages

### 4. Local Hashing Embeddings
**Why:** Development and offline support
- No external dependencies
- Deterministic and reproducible
- Fast enough for testing
- Allows graceful fallback from remote embeddings

### 5. JSON Embedding Serialization
**Why:** SQLite compatibility and portability
- Human-readable in queries
- Easy to extract for analysis
- No specialized binary format needed
- Trade-off: Slightly larger storage (~500B per embedding)

### 6. Deletion Audit Trail
**Why:** Compliance and recovery
- Every deletion recorded
- Authorization tracking
- Enables compliance verification
- Supports data recovery if needed

## Scalability Considerations

### Current Limitations
1. **Single SQLite file** - Not horizontally scalable
2. **In-memory sorting** - Large result sets consume RAM
3. **No query optimization** - Same SQL for all queries
4. **Embedding generation** - Bottleneck for bulk operations

### Scaling Strategies

**Vertical (Single Machine):**
- Archive old data to separate files
- Use indexes for hot queries
- Tune SQLite pragmas (journal_mode, cache_size)
- Consider read replicas with WAL mode

**Horizontal (Multiple Stores):**
- Shard by classification level
- Shard by source or time window
- Use consistent hashing for message distribution
- Implement distributed search across stores

**Optimization:**
- Add query result caching (Redis or memcached)
- Batch embedding operations
- Pre-compute common search results
- Implement approximate nearest neighbor search (HNSW)

## Performance Characteristics

**Ingestion:**
- ~150µs per message (including embedding)
- ~15ms for 100 messages (with transactions)
- Bottleneck: Embedding computation

**Search:**
- ~3.2ms for 300 messages, 10 results
- Query: ~100µs, deserialization: ~500µs, sorting: ~2.6ms
- Bottleneck: Result aggregation and sorting

**Deletion:**
- ~68µs per message (delete operation)
- ~200µs per policy evaluation (expired/classification/source/age)
- Bottleneck: Transaction commit

See `PERFORMANCE.md` for detailed benchmarks and optimization strategies.

## Security Considerations

### Data Protection
- File permissions: 600 for database, 700 for directory
- SQLite supports encryption via SEE (commercial)
- Recommended: Full-disk encryption
- No built-in encryption (add via application layer if needed)

### Access Control
- Classification field enables logical access control
- Physical access control via file permissions
- No role-based access within database
- Implement in application layer

### Audit Trail
- deletion_runs table provides deletion audit
- retrieval_runs provides search audit
- ingestion_runs provides ingestion audit
- No mutation tracking (only delete tracking)

## Future Enhancements

### Short Term (Phases 4.11-4.12)
- [ ] Search result streaming for large datasets
- [ ] Query result caching layer
- [ ] Advanced full-text search (FTS5)
- [ ] Custom vector indexing (HNSW)

### Medium Term (Phase 5)
- [ ] Multi-store sharding
- [ ] Distributed search federation
- [ ] Embedding model versioning
- [ ] Message mutation tracking

### Long Term (Phase 6+)
- [ ] Graph database integration for conversation threads
- [ ] Semantic clustering and auto-tagging
- [ ] Advanced retention policies (hierarchical TTL)
- [ ] Query optimization and statistics

## Testing

**Unit Tests:** `*_test.go` files in internal/knowledge
- Database operations (50+ tests)
- Search and filtering (10+ tests)
- Deletion policies (18+ tests)
- Embeddings (15+ tests)
- Python interoperability (10+ tests)

**Benchmarks:** `benchmarks_test.go`
- 18 comprehensive performance benchmarks
- Baseline metrics established
- Hotspots identified

**Integration:** `knowledge_integration_test.go`
- Python interoperability verification
- End-to-end workflows
- Concurrent access patterns

## Deployment

**Installation:**
```bash
# Binary includes knowledge store
go build ./cmd/cadre

# Initialize on first use
./cadre knowledge init
```

**Configuration:**
- Default database: `.agents/knowledge-store/store.db`
- Custom location: `--config` flag
- Embedding API: Environment variables

**Monitoring:**
```bash
# Check store health
cadre knowledge stats

# Verify retention policies
cadre knowledge delete --expired --json
```

## See Also

- `README_CLI.md` - Command-line usage guide
- `SCHEMA.md` - Database schema details
- `PERFORMANCE.md` - Benchmarks and optimization
- `tests/integration/` - Integration tests
