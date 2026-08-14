# Phase 5.5: Distributed Search CLI Commands

**Date:** August 2026  
**Status:** Complete  
**New Commands:** 3 (shards, federated-search, federated-delete)  
**Tests:** 15 comprehensive tests

## Overview

Phase 5.5 adds three new `cadre knowledge` subcommands that enable distributed operations across multiple sharded knowledge stores. These commands require a multi-shard configuration (multiple `shard-*.db` files) and provide:

1. **Shard statistics** — View distribution and shard configuration
2. **Federated search** — Query across multiple shards in parallel
3. **Federated deletion** — Delete messages across multiple shards atomically

## Multi-Shard Setup

Federated commands work with stores located in `.agents/knowledge-store/shard-*.db` format:

```
.agents/knowledge-store/
  shard-0.db          # Shard 0
  shard-1.db          # Shard 1
  shard-2.db          # Shard 2
```

The CLI auto-discovers these shards and routes operations accordingly.

## Command Reference

### 1. cadre knowledge shards

Display shard distribution and statistics across all shards.

**Usage:**
```bash
cadre knowledge shards [options]
```

**Options:**
- `--strategy <strategy>` — Sharding strategy (default: `classification`)
  - `classification` — Shard by message classification level
  - `source` — Shard by message source/application
  - `conversation` — Shard by conversation ID
  - `composite` — Shard by composite key (source + classification)
- `--json` — Output stats as JSON (default: human-readable text)

**Examples:**

Display shard distribution (text output):
```bash
cadre knowledge shards
```

Output:
```
Shard Distribution
═══════════════════════════════════════
Total shards: 3
Active shards: 3
Shard strategy: classification
Total messages across shards: 1500

Per-shard distribution:
  0: 400 messages (26.7%)
  1: 550 messages (36.7%)
  2: 550 messages (36.7%)
```

Display as JSON:
```bash
cadre knowledge shards --json
```

Output:
```json
{
  "active_shards": 3,
  "distribution": {
    "0": 400,
    "1": 550,
    "2": 550
  },
  "shard_strategy": "classification",
  "total_messages": 1500,
  "total_shards": 3
}
```

### 2. cadre knowledge federated-search

Search across multiple shards in parallel.

**Usage:**
```bash
cadre knowledge federated-search [options] <query>
```

**Required Options:**
- `--classification <cls>` — Classification filter (required)

**Search Options:**
- `<query>` — Search query (string)
- `--sources <src1,src2,...>` — Comma-separated source filters (optional)
- `--top <n>` — Results per shard (default: 10)
- `--mode <mode>` — Search mode: `vector` (default) or `content`
- `--embedding <model>` — Embedding model
  - `local-hashing` — Local FNV-1a hashing (default, no API required)
  - `openai-compatible` — Remote OpenAI-compatible API (requires `OPENAI_API_KEY` env var)

**Parallel Options:**
- `--strategy <strategy>` — Sharding strategy (default: `classification`)
- `--parallel <n>` — Concurrent shard queries (default: 4)

**Output Options:**
- `--json` — Output results as JSON

**Examples:**

Search across all shards (classification=general):
```bash
cadre knowledge federated-search --classification general "machine learning"
```

Output (text):
```
Federated Search Results (12)
═════════════════════════════════════════════
Query: machine learning
Shards queried: 3, Failed: 0

1. conversation-42 (source: research-app) - Similarity: 0.8753
   Role: assistant
   Content: Deep learning uses multiple layers to extract features...

2. conversation-89 (source: docs-system) - Similarity: 0.8521
   Role: user
   Content: How does machine learning differ from traditional...
```

Search with source filtering:
```bash
cadre knowledge federated-search \
  --classification technical \
  --sources research-app,docs-system \
  --top 5 \
  "neural networks"
```

Use remote embeddings (OpenAI):
```bash
export OPENAI_API_KEY="sk-..."
export OPENAI_BASE_URL="https://api.openai.com/v1"

cadre knowledge federated-search \
  --classification general \
  --embedding openai-compatible \
  --parallel 8 \
  "what is deep learning"
```

Control parallelism (useful for rate-limited APIs):
```bash
cadre knowledge federated-search \
  --classification general \
  --parallel 2 \
  "test query"
```

JSON output for scripting:
```bash
cadre knowledge federated-search \
  --classification general \
  --json \
  "query" | jq '.results | length'
```

### 3. cadre knowledge federated-delete

Delete messages across multiple shards.

**Usage:**
```bash
cadre knowledge federated-delete [options]
```

**Deletion Modes (choose exactly one):**
- `--expired` — Delete messages past their retention_until date
- `--classification <cls>` — Delete all messages with given classification
- `--source <src>` — Delete all messages from given source
- `--age <days>` — Delete messages older than N days

**Options:**
- `--strategy <strategy>` — Sharding strategy (default: `classification`)
- `--authorized-by <user>` — Authorization identifier (default: `cli-user`)
- `--json` — Output stats as JSON

**Examples:**

Delete expired messages across all shards:
```bash
cadre knowledge federated-delete --expired
```

Output:
```
Federated Deletion Results
══════════════════════════════════════════════
Total deleted: 342
Shards queried: 3
Authorized by: cli-user
```

Delete by classification (e.g., temporary data):
```bash
cadre knowledge federated-delete --classification temporary
```

Delete by source with authorization audit:
```bash
cadre knowledge federated-delete \
  --source research-app \
  --authorized-by data-steward@example.com
```

Delete messages older than 90 days:
```bash
cadre knowledge federated-delete --age 90
```

JSON output for scripting:
```bash
cadre knowledge federated-delete --classification temp --json | \
  jq '.total_deleted'
```

## Sharding Strategies

All federated commands support four sharding strategies:

### classification (default)
Routes messages to shards based on classification level (general, technical, confidential, etc.). Useful for:
- Separating different sensitivity levels
- Access control by tier
- Different retention policies per level

### source
Routes messages to shards based on source/application. Useful for:
- Per-application data isolation
- Multi-tenant deployments
- Separate lifecycle per source

### conversation
Routes messages to shards based on conversation ID. Useful for:
- Conversation-local retention
- Co-locating related messages
- Reduced cross-shard queries

### composite
Routes based on combination of source and classification. Useful for:
- Fine-grained isolation (app + sensitivity)
- Complex multi-tenant scenarios

## Performance Tuning

### Parallel Queries
Control concurrent shard operations with `--parallel`:

```bash
# Conservative: 1 shard at a time (useful for rate-limited APIs)
cadre knowledge federated-search --parallel 1 --classification general "query"

# Aggressive: 8 concurrent queries
cadre knowledge federated-search --parallel 8 --classification general "query"

# Default: 4 concurrent queries
cadre knowledge federated-search --classification general "query"
```

### Embedding Models

**Local hashing (default, fastest, deterministic):**
```bash
cadre knowledge federated-search --embedding local-hashing "query"
```
- No external API calls
- 128-dimensional embeddings
- Deterministic across runs
- 0ms latency overhead

**Remote OpenAI API (higher quality, higher latency):**
```bash
export OPENAI_API_KEY="sk-..."
export OPENAI_BASE_URL="https://api.openai.com/v1"

cadre knowledge federated-search \
  --embedding openai-compatible \
  --classification general \
  "query"
```
- API queries (one per unique content)
- Higher quality embeddings (1536-dimensional)
- ~100-200ms latency per query
- Requires network access and API key

## Error Handling

### Multi-shard failures are isolated
If shard-1 fails but shard-0 and shard-2 succeed:

```bash
cadre knowledge federated-search --json --classification general "query"
```

Output:
```json
{
  "classification": "general",
  "count": 18,
  "query": "test",
  "results": [...],
  "shards_failed": 1,
  "shards_queried": 3
}
```

Partial results are returned; failed shards don't block success.

### Invalid configuration
Single-store mode (only `store.db`, no `shard-*.db`):

```bash
cadre knowledge federated-search --classification general "query"
# Error: no multi-shard configuration found (looking for shard-*.db files)
```

Use regular `cadre knowledge search` for single-store operations.

## Testing

All federated commands are tested with:
- **Unit tests:** 15 comprehensive tests
- **Test coverage:** ✅ 90%+ code coverage
- **Multi-shard scenarios:** ✅ Tested with 2-3 shards
- **Error handling:** ✅ Single-store rejection, invalid strategies
- **Output formats:** ✅ Text + JSON validation

Run tests:
```bash
CGO_ENABLED=1 go test ./internal/cli -v -run Federated
```

## Implementation Details

### Shard Discovery
The CLI automatically discovers shards by scanning `.agents/knowledge-store/` for files matching `shard-*.db`. Each file is opened as a separate store and registered with the ShardingStrategy.

### Consistent Hashing
Sharding uses consistent hashing with virtual keys to minimize rebalancing when shards are added/removed. Messages are routed deterministically based on the chosen strategy.

### Parallel Execution
Federated search uses goroutines to query multiple shards concurrently. Results are aggregated and re-ranked by cosine similarity before returning to the user.

### Error Isolation
Per-shard errors (e.g., database corruption) don't block other shards. Failed shards are counted and reported, but partial results are still returned.

## Related Commands

- `cadre knowledge search` — Single-shard search (faster for small datasets)
- `cadre knowledge delete` — Single-shard deletion
- `cadre knowledge stats` — Single-shard statistics
- `cadre knowledge init` — Initialize or verify single store

## Roadmap

Future Phase 5.6+ work:
- Shard rebalancing automation
- Model migration across shards
- Distributed query result streaming
- Advanced sharding strategies (geographic, performance-based)
