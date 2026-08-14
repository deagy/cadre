# Cadre Knowledge Store CLI Guide

The `cadre knowledge` command provides access to the knowledge store for ingesting, searching, and managing messages and embeddings.

## Installation

The knowledge store is built into the `cadre` CLI. No additional installation required.

## Quick Start

### Initialize the Store

```bash
# Create or verify knowledge store at default location (.agents/knowledge-store/store.db)
cadre knowledge init

# Verify existing store
cadre knowledge init --verify
```

### Ingest Messages

```bash
# Pipe JSON messages from stdin
cat messages.jsonl | cadre knowledge ingest --source my-app --classification technical

# Example JSON format:
# {"message_id": "msg-1", "conversation_id": "conv-1", "role": "user", "content": "Hello"}
```

### Search

```bash
# Vector similarity search (semantic)
cadre knowledge search --classification technical "machine learning"

# Text content search
cadre knowledge search --classification technical --mode content "neural"

# Search with JSON output (for scripting)
cadre knowledge search --classification technical --json "query"

# Filter by source
cadre knowledge search --classification technical --sources my-app,other-app "query"

# Limit results
cadre knowledge search --classification technical --top 5 "query"
```

### Delete

```bash
# Delete expired messages (past retention_until date)
cadre knowledge delete --expired

# Delete by classification (security level purge)
cadre knowledge delete --classification secret

# Delete by source (cleanup after testing)
cadre knowledge delete --source test-app

# Delete by age (older than N days)
cadre knowledge delete --age 30
```

### Statistics

```bash
# Display store statistics
cadre knowledge stats

# JSON output
cadre knowledge stats --json
```

## Command Reference

### `cadre knowledge init`

Initialize or verify the knowledge store.

**Options:**
- `--verify` - Only verify existing store, don't create

**Examples:**
```bash
cadre knowledge init                 # Create or open store
cadre knowledge init --verify        # Check existing store is valid
```

### `cadre knowledge stats`

Display statistics about the store.

**Options:**
- `--json` - Output as JSON for parsing

**Examples:**
```bash
cadre knowledge stats                # Human-readable format
cadre knowledge stats --json | jq    # Parse with jq
```

### `cadre knowledge ingest`

Ingest messages from JSON stream into the store.

**Options:**
- `--source <src>` (required) - Source identifier for all messages
- `--source-uri <uri>` - Source URI (optional)
- `--classification <cls>` - Classification level (default: general)
- `--embedding <model>` - Embedding model: `local-hashing` (default) or `openai-compatible`

**Input Format:**
JSON lines, one message per line. Each object should have:
- `message_id` (required) - Unique identifier
- `conversation_id` (required) - Conversation grouping
- `role` (required) - user, assistant, system, etc.
- `content` (required) - Message text
- `conversation_title` (optional) - Conversation name
- Other fields are preserved as-is

**Examples:**
```bash
# Simple ingest
echo '{"message_id":"1","conversation_id":"c1","role":"user","content":"Hello"}' | \
  cadre knowledge ingest --source cli-test

# Bulk ingest from file
cat messages.jsonl | cadre knowledge ingest --source my-app --classification technical

# With remote embeddings (requires environment variables)
EMBEDDINGS_BASE_URL=https://api.openai.com/v1 \
EMBEDDINGS_API_KEY=sk-... \
EMBEDDINGS_MODEL=text-embedding-3-small \
cat messages.jsonl | cadre knowledge ingest --source my-app --embedding openai-compatible
```

**Environment Variables (for remote embeddings):**
- `EMBEDDINGS_BASE_URL` - API endpoint (e.g., https://api.openai.com/v1)
- `EMBEDDINGS_API_KEY` - API authentication key
- `EMBEDDINGS_MODEL` - Model name (e.g., text-embedding-3-small)
- `EMBEDDINGS_TIMEOUT_SECONDS` - Request timeout (default: 30)

### `cadre knowledge search`

Search the knowledge store using vector similarity or text content.

**Options:**
- `--classification <cls>` (required) - Classification to search in
- `--sources <src1,src2,...>` - Filter by source(s) (optional)
- `--mode <mode>` - Search mode: `vector` (default) or `content`
- `--top <n>` - Number of results (default: 10)
- `--json` - Output as JSON
- `--embedding <model>` - Embedding model: `local-hashing` (default) or `openai-compatible`

**Vector Search (Default):**
Uses semantic similarity to find conceptually related messages.

```bash
cadre knowledge search --classification technical "machine learning"
cadre knowledge search --classification general --top 5 "how do I..."
```

**Content Search:**
Uses substring matching to find text within messages.

```bash
cadre knowledge search --classification technical --mode content "TensorFlow"
cadre knowledge search --classification general --mode content "database"
```

**JSON Output:**
```bash
cadre knowledge search --classification technical --json "query" | jq '.results[0].message.content'
```

**Examples:**
```bash
# Basic search
cadre knowledge search --classification technical "neural networks"

# Filter by source
cadre knowledge search --classification technical --sources my-app "machine learning"

# Multiple sources
cadre knowledge search --classification technical --sources app1,app2 "data"

# Custom result limit
cadre knowledge search --classification technical --top 20 "algorithm"

# Text search
cadre knowledge search --classification technical --mode content "Python"

# JSON output for scripting
cadre knowledge search --classification technical --json "query" | jq '.count'
```

### `cadre knowledge delete`

Delete messages from the store using various strategies. Specify exactly one deletion mode.

**Options:**
- `--expired` - Delete messages past retention_until date
- `--classification <cls>` - Delete all messages with classification
- `--source <src>` - Delete all messages from source
- `--age <days>` - Delete messages older than N days
- `--authorized-by <user>` - Authorization user (default: cli-user)
- `--json` - Output deletion summary as JSON

**Examples:**
```bash
# Delete expired data
cadre knowledge delete --expired

# Purge security classification
cadre knowledge delete --classification secret --authorized-by admin

# Remove test data
cadre knowledge delete --source test-app

# Cleanup old data
cadre knowledge delete --age 90 --authorized-by cleanup-script

# JSON output
cadre knowledge delete --source old-source --json
```

## Data Classification Levels

The `--classification` parameter controls access and retention policies:

- `public` - Unrestricted, retained indefinitely
- `general` - Internal use, standard retention
- `technical` - Technical details, restricted distribution
- `confidential` - Sensitive information, limited access
- `secret` - Highly restricted, encrypted storage
- Custom values allowed for organization-specific levels

## Source Organization

The `--source` parameter identifies where messages come from:

- `claude-code` - From Claude Code integration
- `slack-bot` - From Slack integration
- `api` - From API clients
- `import-<date>` - From bulk imports
- Custom identifiers for your applications

## JSON Output Formats

### Search Results

```json
{
  "query": "machine learning",
  "mode": "vector",
  "count": 3,
  "results": [
    {
      "message": {
        "id": "hash...",
        "source": "my-app",
        "conversation_id": "conv-1",
        "role": "user",
        "content": "...",
        "classification": "technical",
        "ingested_at": "2026-08-14T12:00:00.000Z"
      },
      "chunk": {
        "ordinal": 0,
        "content": "..."
      },
      "cosine_similarity": 0.8234
    }
  ]
}
```

### Delete Results

```json
{
  "deleted": 42,
  "authorized_by": "admin"
}
```

### Statistics

```json
{
  "total_messages": 1000,
  "total_chunks": 3000,
  "ingestion_runs": 5,
  "retrieval_runs": 128,
  "database_size_bytes": 2097152,
  "sources": 3,
  "classifications": 4,
  "embedding_models": 1
}
```

## Performance Tips

### Ingestion
- Use `--embedding local-hashing` for bulk ingestion (faster, no API calls)
- Process large files in batches using shell pipelines
- Monitor database size with `cadre knowledge stats`

### Searching
- Always specify `--classification` to reduce search space
- Use `--sources` filter when searching multi-tenant data
- Default `--top 10` provides good balance of speed and coverage

### Deletion
- Use `--expired` for automated retention policy enforcement
- Use `--age` with `--classification` for selective pruning
- Monitor `stats` to verify retention policies

## Troubleshooting

### "store not found" Error
```bash
cadre knowledge init  # Create the store first
```

### "classification is required" Error
```bash
# Always specify --classification for search/delete
cadre knowledge search --classification general "query"
```

### "source is required" Error
```bash
# Always specify --source for ingest
cat messages.jsonl | cadre knowledge ingest --source my-app
```

### Slow Search Performance
- Limit results with `--top` (default is 10)
- Filter by `--sources` to reduce search space
- Use `--mode content` for text search instead of vector search
- Check store size with `stats` - very large stores may need archiving

### Remote Embeddings Not Working
Verify environment variables:
```bash
echo $EMBEDDINGS_BASE_URL      # Should be set
echo $EMBEDDINGS_API_KEY       # Should not be empty
echo $EMBEDDINGS_MODEL         # Should be model name
```

## Production Usage

### Automation
```bash
#!/bin/bash
# Ingest script
cadre knowledge ingest \
  --source "$APP_NAME" \
  --classification "$CLASSIFICATION" \
  < "$MESSAGE_FILE"
```

### Monitoring
```bash
# Check store health
cadre knowledge stats --json | jq '.total_messages'

# Find retention violations
cadre knowledge delete --expired --json | jq '.deleted'
```

### Retention Policy
```bash
# Daily cleanup of old data
cadre knowledge delete --age 365 --authorized-by retention-policy

# Quarterly security purge
cadre knowledge delete --classification confidential --authorized-by security-team
```

## API Integration

For programmatic access, use JSON pipelines:

```bash
# Python example
python3 << 'EOF'
import json
import subprocess

# Generate messages
messages = [
    {"message_id": f"msg-{i}", "conversation_id": "python-conv", 
     "role": "user", "content": f"Message {i}"}
    for i in range(100)
]

# Ingest
proc = subprocess.Popen(
    ["cadre", "knowledge", "ingest", "--source", "python-app"],
    stdin=subprocess.PIPE, text=True
)
for msg in messages:
    proc.stdin.write(json.dumps(msg) + "\n")
proc.stdin.close()
proc.wait()

# Search
result = subprocess.run(
    ["cadre", "knowledge", "search", "--classification", "general", "--json", "query"],
    capture_output=True, text=True
)
data = json.loads(result.stdout)
print(f"Found {data['count']} results")
EOF
```

## Command-Line Help

Get help for any command:

```bash
cadre knowledge help              # All subcommands
cadre knowledge init --help       # Init options
cadre knowledge search --help     # Search options
# etc.
```

## See Also

- `cadre knowledge` - Main command help
- `/home/deagy/sdk/cadre/internal/knowledge/SCHEMA.md` - Database schema documentation
- `/home/deagy/sdk/cadre/internal/knowledge/ARCHITECTURE.md` - Architecture details
