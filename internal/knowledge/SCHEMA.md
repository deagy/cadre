# Knowledge Store Database Schema

The knowledge store uses SQLite3 with the following schema for persistent storage of messages, embeddings, and metadata.

## Database File Location

- Default: `.agents/knowledge-store/store.db`
- Custom: Via `--config` flag to any `cadre knowledge` command
- Environment variable: `KNOWLEDGE_STORE_PATH` (if implemented)

## Schema Overview

The database consists of four main tables:

1. **ingestion_runs** - Tracks message ingestion operations
2. **messages** - Core message records
3. **chunks** - Message chunks with embeddings
4. **retrieval_runs** - Search and analytics tracking
5. **deletion_runs** - Deletion audit trail

## Table Definitions

### `ingestion_runs`

Tracks metadata about message ingestion batches.

```sql
CREATE TABLE ingestion_runs (
  id TEXT PRIMARY KEY,
  source TEXT NOT NULL,
  source_uri TEXT,
  started_at TEXT NOT NULL,
  completed_at TEXT,
  status TEXT NOT NULL,        -- 'running', 'complete', 'failed'
  message_count INTEGER NOT NULL DEFAULT 0,
  chunk_count INTEGER NOT NULL DEFAULT 0,
  error TEXT
);
```

**Fields:**
- `id` - UUID identifying this ingestion run
- `source` - Source identifier (e.g., "my-app", "claude-code")
- `source_uri` - Optional URI for the source
- `started_at` - ISO 8601 timestamp when ingestion began
- `completed_at` - ISO 8601 timestamp when ingestion finished
- `status` - Current status: 'running', 'complete', or 'failed'
- `message_count` - Number of messages ingested
- `chunk_count` - Total chunks across all messages
- `error` - Error message if status is 'failed'

**Indexes:**
- PRIMARY KEY on `id`

**Usage:**
Track which batches of messages were ingested, when, and whether they succeeded.

---

### `messages`

Core message records storing conversation data.

```sql
CREATE TABLE messages (
  id TEXT PRIMARY KEY,
  source TEXT NOT NULL,
  source_uri TEXT,
  conversation_id TEXT NOT NULL,
  conversation_title TEXT,
  source_message_id TEXT NOT NULL,
  role TEXT NOT NULL,
  content TEXT NOT NULL,
  content_hash TEXT NOT NULL,
  created_at TEXT,
  classification TEXT NOT NULL,
  injection_risk INTEGER NOT NULL DEFAULT 0,
  redactions_json TEXT NOT NULL,
  metadata_json TEXT NOT NULL,
  ingested_at TEXT NOT NULL,
  retention_until TEXT,
  UNIQUE(source, conversation_id, source_message_id)
);
```

**Fields:**
- `id` - SHA256 hash of (source, conversation_id, source_message_id) - deterministic
- `source` - Source identifier (required)
- `source_uri` - Optional URI for the source
- `conversation_id` - Groups related messages
- `conversation_title` - Optional conversation name
- `source_message_id` - Original ID from source system
- `role` - Message role (user, assistant, system, etc.)
- `content` - Full message text
- `content_hash` - SHA256 hash of content
- `created_at` - Original message timestamp
- `classification` - Security/access level (public, general, technical, confidential, secret, etc.)
- `injection_risk` - 0/1 boolean flag for prompt injection risk
- `redactions_json` - Array of redacted fields/ranges
- `metadata_json` - Custom JSON metadata
- `ingested_at` - When message was stored (ISO 8601)
- `retention_until` - Optional expiration date (ISO 8601)

**Constraints:**
- PRIMARY KEY on `id` ensures uniqueness
- UNIQUE constraint on (source, conversation_id, source_message_id) enables UPSERT
- Foreign key references from chunks

**Indexes:**
- PRIMARY KEY on `id`
- INDEX on `source` - for source-based queries
- INDEX on `conversation_id` - for conversation grouping
- INDEX on `classification` - for access control filtering
- INDEX on `retention_until` - for expiration queries

**Usage:**
Store individual messages with full metadata, enabling search, filtering, and retention management.

---

### `chunks`

Message chunks with computed embeddings for vector similarity search.

```sql
CREATE TABLE chunks (
  id TEXT PRIMARY KEY,
  message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
  ordinal INTEGER NOT NULL,
  content TEXT NOT NULL,
  content_hash TEXT NOT NULL,
  embedding_provider TEXT NOT NULL,
  embedding_model TEXT NOT NULL,
  embedding_dimensions INTEGER NOT NULL,
  embedding_json TEXT NOT NULL,
  UNIQUE(message_id, ordinal, embedding_provider, embedding_model)
);
```

**Fields:**
- `id` - SHA256 hash of (message_id, ordinal, provider, model)
- `message_id` - Foreign key to parent message
- `ordinal` - Chunk sequence within message (0-based)
- `content` - Chunk text
- `content_hash` - SHA256 hash of chunk content
- `embedding_provider` - Embedder name (local-hashing, openai-compatible)
- `embedding_model` - Model variant (fnv1a-d128, text-embedding-3-small)
- `embedding_dimensions` - Vector dimensionality
- `embedding_json` - JSON array of floats

**Constraints:**
- PRIMARY KEY on `id`
- FOREIGN KEY to messages with ON DELETE CASCADE
- UNIQUE constraint enables UPSERT per provider/model
- Multiple chunks per message allowed (for long messages)
- Multiple embeddings per chunk allowed (for different models)

**Indexes:**
- PRIMARY KEY on `id`
- INDEX on (embedding_provider, embedding_model) - for provider filtering

**Storage Details:**
- Embedding vectors stored as JSON arrays: `[0.1, 0.2, 0.3, ...]`
- Serialized size ~500 bytes per 128-dim embedding
- Content limited to SQLite TEXT field (~1GB practical limit per chunk)

**Usage:**
Store vector embeddings and chunk content for semantic search via cosine similarity.

---

### `retrieval_runs`

Analytics tracking for search operations.

```sql
CREATE TABLE retrieval_runs (
  id TEXT PRIMARY KEY,
  query_hash TEXT NOT NULL,
  task_id TEXT NOT NULL,
  agent TEXT NOT NULL,
  classification TEXT NOT NULL,
  source_filter TEXT,
  embedding_provider TEXT NOT NULL,
  embedding_model TEXT NOT NULL,
  requested_top INTEGER NOT NULL,
  result_count INTEGER NOT NULL,
  created_at TEXT NOT NULL
);
```

**Fields:**
- `id` - UUID identifying this search
- `query_hash` - SHA256 hash of query text (not the query itself, for privacy)
- `task_id` - Associated task identifier
- `agent` - Agent/user performing search
- `classification` - Classification level searched
- `source_filter` - Comma-separated sources (if filtered)
- `embedding_provider` - Provider used for search
- `embedding_model` - Model used
- `requested_top` - Top-k requested
- `result_count` - Actual results returned
- `created_at` - Search timestamp

**Indexes:**
- PRIMARY KEY on `id`
- INDEX on (task_id, agent) - for agent-centric queries

**Usage:**
Track search patterns and performance for analytics and optimization.

---

### `deletion_runs`

Audit trail for all deletion operations.

```sql
CREATE TABLE deletion_runs (
  id TEXT PRIMARY KEY,
  reason TEXT NOT NULL,
  policy_type TEXT NOT NULL,
  target_count INTEGER NOT NULL,
  deleted_count INTEGER NOT NULL,
  status TEXT NOT NULL,
  authorized_by TEXT,
  classification TEXT,
  source TEXT,
  min_age_days INTEGER,
  started_at TEXT NOT NULL,
  completed_at TEXT,
  error TEXT
);
```

**Fields:**
- `id` - UUID identifying this deletion
- `reason` - Why deletion was performed
- `policy_type` - Type: 'expiration', 'classification', 'source', 'age'
- `target_count` - Messages targeted for deletion
- `deleted_count` - Actually deleted count
- `status` - 'running', 'complete', 'failed'
- `authorized_by` - User/process authorizing deletion
- `classification` - Classification filter (if applicable)
- `source` - Source filter (if applicable)
- `min_age_days` - Age filter (if applicable)
- `started_at` - Deletion start timestamp
- `completed_at` - Deletion completion timestamp
- `error` - Error message if status is 'failed'

**Indexes:**
- PRIMARY KEY on `id`
- INDEX on `status` - for finding in-progress deletions

**Usage:**
Complete audit trail for compliance, recovery, and deletion verification.

---

## Indexes

The schema includes strategic indexes for common queries:

```sql
CREATE INDEX idx_messages_source ON messages(source);
CREATE INDEX idx_messages_conversation ON messages(conversation_id);
CREATE INDEX idx_messages_classification ON messages(classification);
CREATE INDEX idx_messages_retention ON messages(retention_until);
CREATE INDEX idx_chunks_model ON chunks(embedding_provider, embedding_model);
CREATE INDEX idx_retrieval_runs_task ON retrieval_runs(task_id, agent);
CREATE INDEX idx_deletion_runs_status ON deletion_runs(status);
```

**Index Usage:**
- `idx_messages_source` - Source-based search and cleanup
- `idx_messages_conversation` - Conversation grouping queries
- `idx_messages_classification` - Access control filtering
- `idx_messages_retention` - Expiration policy enforcement
- `idx_chunks_model` - Embedding provider filtering
- `idx_retrieval_runs_task` - Analytics by task/agent
- `idx_deletion_runs_status` - Audit trail queries

## Pragmas

SQLite pragmas for consistency and performance:

```sql
PRAGMA foreign_keys = ON;      -- Enforce foreign key constraints
PRAGMA journal_mode = WAL;     -- Write-ahead logging for concurrency
```

**Benefits:**
- Foreign key enforcement prevents orphaned chunks
- WAL mode allows concurrent readers while one writer
- Better performance for multi-threaded access

## Data Types

SQLite type mapping:

| SQL Type | Storage | Usage |
|----------|---------|-------|
| TEXT | Variable length | All text fields, JSON arrays |
| INTEGER | 1-8 bytes | Counts, ordinals, flags |
| PRIMARY KEY | 8 bytes (TEXT as stored) | Identifiers |

## Storage Capacity

**Practical Limits:**
- Max database file: 2TB (SQLite default)
- Max table: 2TB
- Max row: Limited by available memory (typical <100MB)
- Max field: 1GB (TEXT field)

**Typical Sizing:**
- Empty database: ~80 KB
- 1000 messages, 3000 chunks: ~2 MB
- 100,000 messages, 300,000 chunks: ~200 MB
- 1,000,000 messages, 3,000,000 chunks: ~2 GB

## Data Retention

**Message Retention:**
- `retention_until` field controls automatic expiration
- NULL = retain indefinitely
- ISO 8601 timestamp = delete after this date

**Deletion Cascade:**
- Deleting a message automatically deletes all associated chunks
- Foreign key constraint with ON DELETE CASCADE

## Backup & Recovery

**Backup Strategy:**
```bash
# Copy database file
cp .agents/knowledge-store/store.db backup-$(date +%Y%m%d).db

# Or use SQLite backup
sqlite3 store.db ".backup backup.db"
```

**Recovery:**
```bash
# Restore from backup
cp backup.db .agents/knowledge-store/store.db

# Or use SQLite restore
sqlite3 backup.db ".restore store.db"
```

## Optimization

**Vacuum:**
Reclaim disk space after deletions:
```bash
sqlite3 .agents/knowledge-store/store.db "VACUUM;"
```

**Analyze:**
Update statistics for query planner:
```bash
sqlite3 .agents/knowledge-store/store.db "ANALYZE;"
```

**Defragment:**
Periodic defragmentation:
```bash
sqlite3 store.db ".dump" | sqlite3 store-new.db
mv store-new.db store.db
```

## Access Control

**File Permissions:**
```bash
# Restrict access to knowledge store database
chmod 600 .agents/knowledge-store/store.db
chmod 700 .agents/knowledge-store/
```

**SQLite Encryption:**
Not supported in standard SQLite. Use:
- SELinux for system-level encryption
- Full-disk encryption (recommended)
- Application-level encryption (future enhancement)

## Concurrency

**Concurrent Access:**
- Multiple readers supported (WAL mode)
- Single writer at a time
- Locks released after 5 seconds (default timeout)
- Reader waits for writer (fair scheduling)

**Transactions:**
- Atomic operations guaranteed via ACID
- Savepoints supported for nested transactions
- Rollback on error

## Migration

**Schema Upgrades:**
The schema is versioned and managed via `migrateAdditiveColumns()`:
- Only additive migrations supported (no column drops)
- New columns added with ALTER TABLE
- Default values provided for existing rows

**Current Schema Version:** 1.0

## Troubleshooting

**"database is locked" Error:**
- Another process holding exclusive lock
- Solution: Check running processes, close editor/viewer
- WAL mode helps reduce lock contention

**"database disk image is malformed" Error:**
- Database corruption (rare)
- Solution: Restore from backup, run `PRAGMA integrity_check`

**Slow Queries:**
- Check indexes with `EXPLAIN QUERY PLAN`
- Run `ANALYZE` to update statistics
- Consider adding indexes for common filters

**Large Database:**
- Archive old messages to separate file
- Use `VACUUM` to reclaim space
- Consider sharding by classification or time

## SQL Examples

```sql
-- Count messages by source
SELECT source, COUNT(*) FROM messages GROUP BY source;

-- Find messages by age
SELECT * FROM messages WHERE ingested_at < datetime('now', '-90 days');

-- Average chunks per message
SELECT COUNT(*) / (SELECT COUNT(*) FROM messages) FROM chunks;

-- Search quality metrics
SELECT 
  embedding_model, 
  COUNT(*) as searches,
  AVG(result_count) as avg_results
FROM retrieval_runs GROUP BY embedding_model;

-- Deletion audit
SELECT * FROM deletion_runs WHERE status = 'complete' ORDER BY completed_at DESC;

-- Retention violations
SELECT * FROM messages WHERE retention_until IS NOT NULL AND retention_until < datetime('now');
```

## See Also

- `README_CLI.md` - Command-line interface documentation
- `ARCHITECTURE.md` - System architecture and design
- `PERFORMANCE.md` - Performance benchmarks and optimization
