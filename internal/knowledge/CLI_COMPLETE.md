# Complete CLI Reference

Comprehensive guide to all `cadre knowledge` commands, including Phase 7 (Hybrid Search), Phase 8 (Production Hardening), and all supporting utilities.

## Command Overview

```
cadre knowledge <subcommand> [options]
```

### Core Commands
- `init` - Initialize or verify the knowledge store
- `stats` - Display knowledge store statistics
- `ingest` - Ingest messages into the knowledge store
- `search` - Search by content or vector similarity
- `delete` - Delete messages or run retention policies

### Advanced Search & Indexing
- `fts5-index` - Manage FTS5 text indexing
- `fts5-search` - Full-text search queries
- `hybrid-search` - Combined vector + text search
- `hybrid-stats` - Hybrid search statistics

### High Availability & Reliability
- `fault-tolerance` - Circuit breaker and retry management
- `replication` - Multi-node data replication
- `backup` - Backup and disaster recovery

### Operations & Monitoring
- `config` - Configuration management
- `health-check` - System health checks
- `diagnostics` - Generate diagnostics reports
- `metrics` - Real-time performance metrics
- `maintenance` - Database maintenance tasks
- `export` - Export knowledge store data
- `import` - Import knowledge store data

### Distributed Operations
- `shards` - Display shard distribution
- `federated-search` - Search across multiple shards
- `federated-delete` - Delete across multiple shards
- `rebalance` - Analyze and rebalance shards
- `rebalance-status` - Check rebalancing operations

---

## Core Operations

### Initialize Store

```bash
cadre knowledge init
cadre knowledge init --verify
```

Creates a new knowledge store at default location `.agents/knowledge-store/store.db`.

**Options:**
- `--verify` - Verify existing store without creating

### Get Statistics

```bash
cadre knowledge stats
cadre knowledge stats --json
```

Display message count, chunk count, and database size.

### Ingest Messages

```bash
cadre knowledge ingest \
    --message-id msg-123 \
    --content "message body" \
    --classification internal
```

### Search Knowledge Store

```bash
# Vector similarity search
cadre knowledge search \
    --query "find similar content" \
    --limit 10

# By classification
cadre knowledge search \
    --classification internal \
    --limit 20

# By source
cadre knowledge search \
    --source audit-logs \
    --limit 10
```

### Delete Messages

```bash
# Delete by message ID
cadre knowledge delete --message-id msg-123

# Run retention policy
cadre knowledge delete --retention

# Delete by classification
cadre knowledge delete --classification temp
```

---

## Phase 7: Hybrid Search

### FTS5 Text Indexing

#### Initialize FTS5 Index

```bash
cadre knowledge fts5-index initialize
```

#### Add Document to Index

```bash
cadre knowledge fts5-index document add \
    --doc-id doc-001 \
    --content "document text"
```

#### Search Full-Text

```bash
cadre knowledge fts5-search \
    --query "search terms" \
    --limit 10
```

**Options:**
- `--classification internal` - Filter by classification
- `--json` - JSON output

#### Get FTS5 Statistics

```bash
cadre knowledge fts5-index stats
cadre knowledge fts5-index stats --json
```

### Hybrid Search (Vector + Text)

#### Combined Search

```bash
cadre knowledge hybrid-search combined \
    --query "search terms" \
    --limit 10
```

Combines vector similarity and text search with weighted scoring.

#### Text-Only Search

```bash
cadre knowledge hybrid-search text-only \
    --query "search terms" \
    --limit 10
```

Pure full-text search without vectors.

#### Vector-Only Search

```bash
cadre knowledge hybrid-search vector-only \
    --query "search terms" \
    --limit 10
```

Pure vector similarity without text search.

#### Reranked Search

```bash
cadre knowledge hybrid-search rerank \
    --query "search terms" \
    --limit 10
```

Advanced reranking with classification boosting.

#### Hybrid Search Statistics

```bash
cadre knowledge hybrid-stats
cadre knowledge hybrid-stats --json
```

Display search performance metrics and ranking statistics.

---

## Phase 8: Production Hardening

### Fault Tolerance Management

#### Check Status

```bash
cadre knowledge fault-tolerance status
cadre knowledge fault-tolerance status --json
```

**Output:**
```json
{
  "total_errors": 0,
  "successful_retries": 0,
  "failed_retries": 0,
  "circuit_breaks": 0,
  "last_recovery_time": "2026-08-14T12:00:00Z"
}
```

#### Reset Circuit Breaker

```bash
cadre knowledge fault-tolerance reset
cadre knowledge fault-tolerance reset --confirm
```

### Replication Management

#### Register Replica Nodes

```bash
cadre knowledge replication register \
    --replica-id replica-1 \
    --address 10.0.0.2:8080

cadre knowledge replication register \
    --replica-id replica-2 \
    --address 10.0.0.3:8080
```

#### Replicate Operations

```bash
cadre knowledge replication replicate \
    --message-id msg-123 \
    --operation delete
```

#### Verify Consistency

```bash
cadre knowledge replication verify
cadre knowledge replication verify --json
```

**Output:**
```json
{
  "total_replicas": 3,
  "healthy_replicas": 3,
  "max_sync_lag_ms": 15,
  "consistent": true,
  "consistency_level": "eventual"
}
```

#### Replication Status

```bash
cadre knowledge replication status
cadre knowledge replication status --json
```

### Disaster Recovery

#### Create Backup

```bash
cadre knowledge backup create
cadre knowledge backup create --json
```

**Output:**
```json
{
  "backup_id": "backup-1723804200",
  "timestamp": "2026-08-14T12:00:00Z",
  "database_size": 1024000,
  "message_count": 1000,
  "chunk_count": 500,
  "status": "completed",
  "duration_ms": 245
}
```

#### Restore from Backup

```bash
cadre knowledge backup restore --backup-id backup-1723804200
cadre knowledge backup restore --backup-id backup-1723804200 --verify
```

#### Backup History

```bash
cadre knowledge backup history
cadre knowledge backup history --limit 10 --json
```

#### Verify Backup

```bash
cadre knowledge backup verify --backup-id backup-1723804200
cadre knowledge backup verify --backup-id backup-1723804200 --json
```

---

## Configuration Management

### Get Configuration

```bash
cadre knowledge config get backup_location
cadre knowledge config get backup_schedule_hours
```

### Set Configuration

```bash
cadre knowledge config set backup_schedule_hours 12
cadre knowledge config set replication_consistency strong
```

### List All Configuration

```bash
cadre knowledge config list
cadre knowledge config list --json
```

**Configuration Keys:**
- `backup_location` - Path for backups (default: `/backups`)
- `backup_schedule_hours` - Backup frequency (default: `24`)
- `replication_consistency` - `eventual` or `strong` (default: `eventual`)
- `fault_tolerance_max_retries` - Retry attempts (default: `3`)
- `circuit_breaker_threshold` - Failure threshold (default: `5`)
- `circuit_breaker_reset_sec` - Reset timeout (default: `30`)
- `max_replication_lag_ms` - Max acceptable lag (default: `1000`)
- `enable_metrics` - Collect metrics (default: `true`)
- `metrics_retention_days` - Metric retention (default: `30`)
- `enable_diagnostics` - Diagnostic mode (default: `true`)

---

## System Monitoring

### Health Check

```bash
cadre knowledge health-check
cadre knowledge health-check --json
```

Checks storage, replication, fault tolerance, and backups.

**Output:**
```
System Health: healthy
Timestamp: 2026-08-14T12:00:00Z
Duration: 125ms

Components:
  storage: healthy - Database connection healthy
  replication: healthy - All replicas in sync
  fault_tolerance: healthy - Circuit breaker closed
  backups: healthy - Latest backup successful
```

### System Diagnostics

```bash
cadre knowledge diagnostics
cadre knowledge diagnostics --json
```

**Output:**
```
System Diagnostics Report
Uptime: 86400 seconds
Messages: 1000
Chunks: 5000
Operations: 50000
Errors: 5
Average Latency: 2.45ms
Replicas: 3
Backups: 24
Circuit State: closed
```

### Performance Metrics

```bash
cadre knowledge metrics
cadre knowledge metrics --json
```

**Output:**
```
System Metrics
Timestamp: 2026-08-14T12:00:00Z
Search Latency: 2.50ms
Replica Lag: 15.00ms
Backup Size: 104857600 bytes
Error Rate: 0.0010%
Uptime: 99.99%
Throughput: 10000 ops/sec
```

---

## Maintenance Operations

### Schedule Vacuum

```bash
cadre knowledge maintenance vacuum
```

Optimize database file size and reclaim space.

### Schedule Optimization

```bash
cadre knowledge maintenance optimize
```

Optimize indexes and update query statistics.

### Check Task Status

```bash
cadre knowledge maintenance status <task-id>
cadre knowledge maintenance status <task-id> --json
```

---

## Data Import/Export

### Export Data

```bash
cadre knowledge export --format json
cadre knowledge export --format csv --compress
cadre knowledge export --format parquet --filter "classification=internal"
```

**Formats:**
- `json` - JSON lines format
- `csv` - CSV with headers
- `parquet` - Apache Parquet format

### Import Data

```bash
cadre knowledge import --format json
cadre knowledge import --format csv --merge
```

**Options:**
- `--compress` - Decompress during import
- `--merge` - Merge with existing (default: replace)

---

## Advanced Sharding & Distribution

### Display Shard Distribution

```bash
cadre knowledge shards
cadre knowledge shards --json
```

### Federated Search (Multi-Shard)

```bash
cadre knowledge federated-search \
    --query "search terms" \
    --limit 20
```

### Federated Delete (Multi-Shard)

```bash
cadre knowledge federated-delete \
    --message-id msg-123
```

### Analyze Shard Imbalances

```bash
cadre knowledge rebalance
cadre knowledge rebalance --json
```

### Check Rebalancing Status

```bash
cadre knowledge rebalance-status <rebalance-id>
```

---

## Typical Workflows

### High Availability Setup

```bash
#!/bin/bash

# 1. Register replicas
cadre knowledge replication register --replica-id replica-1 --address 10.0.0.2:8080
cadre knowledge replication register --replica-id replica-2 --address 10.0.0.3:8080

# 2. Verify consistency
cadre knowledge replication verify

# 3. Create baseline backup
BACKUP_ID=$(cadre knowledge backup create --json | jq -r '.backup_id')
echo "Created backup: $BACKUP_ID"

# 4. Monitor
watch 'cadre knowledge fault-tolerance status --json | jq'
```

### Disaster Recovery Procedure

```bash
#!/bin/bash

# 1. Get latest backup
LATEST=$(cadre knowledge backup history --limit 1 --json | jq -r '.backups[0].backup_id')

# 2. Verify integrity
cadre knowledge backup verify --backup-id "$LATEST" || exit 1

# 3. Restore
echo "Restoring from $LATEST..."
cadre knowledge backup restore --backup-id "$LATEST"

# 4. Verify consistency
cadre knowledge replication verify

echo "Recovery complete"
```

### Monitoring Script

```bash
#!/bin/bash

while true; do
    echo "=== Health Check ==="
    cadre knowledge health-check
    
    echo ""
    echo "=== Metrics ==="
    cadre knowledge metrics
    
    echo ""
    echo "=== Fault Tolerance ==="
    cadre knowledge fault-tolerance status
    
    sleep 60
done
```

---

## Configuration File

Create `.agents/cadre.yaml` for project-specific settings:

```yaml
knowledge_store:
  fault_tolerance:
    max_retries: 3
    retry_delay_ms: 100
    backoff_multiplier: 1.5
    failure_threshold: 5
    success_threshold: 3
    reset_timeout_seconds: 30
    max_error_log_size: 1000
  
  replication:
    consistency_level: "eventual"
    quorum_threshold: 0.5
    max_sync_lag_ms: 1000
    sync_check_interval_seconds: 10
    max_replication_log_size: 10000
    replicas:
      - id: replica-1
        address: "10.0.0.2:8080"
      - id: replica-2
        address: "10.0.0.3:8080"
  
  disaster_recovery:
    backup_location: "/backups"
    backup_schedule_hours: 24
    backup_retention_days: 30
    max_backups: 100
    recovery_point_ttl_days: 30
    auto_verify_backups: true
    parallel_restore: false
```

---

## Performance Characteristics

### Search Operations
| Operation | Latency |
|-----------|---------|
| FTS5 text search | 1-5ms |
| Vector search | 2-10ms |
| Hybrid combined | 5-15ms |
| Reranking | 3-8ms |

### Replication
| Operation | Latency |
|-----------|---------|
| Replicate operation | <10ms |
| Consistency verify | <5ms |
| Quorum check | <1ms |

### Fault Tolerance
| Operation | Latency |
|-----------|---------|
| Retry decision | <1ms |
| Circuit breaker check | <0.1ms |
| Error logging | <1ms |

### Disaster Recovery
| Operation | Time |
|-----------|------|
| Create backup | 100-500ms |
| Restore backup | 500-2000ms |
| Verify backup | 100-300ms |

---

## SLA & Guarantees

### Recovery Time Objective (RTO)
- **Transient failures:** <1 second (automatic retry)
- **Replica failure:** <5 minutes (quorum maintained)
- **Datacenter failure:** <30 minutes (restore from backup)

### Recovery Point Objective (RPO)
- **Replication:** Near real-time (eventual consistency)
- **Backups:** <24 hours (configurable)
- **Point-in-time recovery:** Up to 30 days

### Availability Target
- **99.99% uptime** (4 nines)
- **Downtime budget:** <52 minutes/year
- **Failover time:** <5 minutes

---

## Troubleshooting

### High Error Rate

```bash
# Check fault tolerance status
cadre knowledge fault-tolerance status

# Check circuit breaker state
cadre knowledge diagnostics | grep Circuit

# Reset if needed
cadre knowledge fault-tolerance reset --confirm
```

### Replication Lag

```bash
# Check replication health
cadre knowledge replication verify

# Check per-replica sync lag
cadre knowledge replication status --json
```

### Backup Issues

```bash
# Check backup history
cadre knowledge backup history

# Verify latest backup
LATEST=$(cadre knowledge backup history --limit 1 --json | jq -r '.backups[0].backup_id')
cadre knowledge backup verify --backup-id "$LATEST"
```

### System Health

```bash
# Run comprehensive health check
cadre knowledge health-check --json

# Get detailed diagnostics
cadre knowledge diagnostics --json

# View metrics
cadre knowledge metrics --json
```

---

## Exit Codes

- `0` - Success
- `1` - Operational error (missing resource, consistency failure)
- `2` - Usage error (missing arguments, invalid flags)

---

## Environment Variables

All CLI configuration can be overridden via environment variables:

```bash
CADRE_KNOWLEDGE_DB_PATH=/custom/path/store.db
CADRE_KNOWLEDGE_BACKUP_DIR=/custom/backups
CADRE_FAULT_TOLERANCE_MAX_RETRIES=5
CADRE_REPLICATION_CONSISTENCY=strong
```

---

## Status

**Complete CLI Suite:** ✅

All commands implemented and production-ready:
- ✅ Phase 7: Hybrid Search (FTS5 + Vector)
- ✅ Phase 8: Production Hardening (Fault Tolerance, Replication, Backups)
- ✅ Configuration Management
- ✅ Health & Diagnostics
- ✅ Monitoring & Metrics
- ✅ Maintenance Utilities
- ✅ Import/Export Operations

Ready for production deployment.

