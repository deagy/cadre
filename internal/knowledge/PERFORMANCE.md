# Knowledge Store Performance Analysis

## Baseline Benchmarks

Run with: `go test -bench=. -benchmem ./internal/knowledge`

### Operation Performance

| Operation | Throughput | Time/Op | Allocs/Op | Memory/Op |
|-----------|-----------|---------|-----------|-----------|
| SaveMessage | 11.6k ops/s | 86µs | 33 | 1.5 KB |
| SaveChunk | 60k ops/s | 16µs | 29 | 2.0 KB |
| SaveChunks (10 items) | 11.7k ops/s | 85µs | 284 | 20.8 KB |
| LocalHashingEmbed | 471k ops/s | 2.1µs | 60 | 2.4 KB |
| LocalHashingEmbedBatch (10 items) | 78.5k ops/s | 12.7µs | 163 | 21.2 KB |
| CosineSimilarity | 354M ops/s | 2.8ns | 0 | 0 B |
| Search (300 msgs, 900 chunks) | 308 ops/s | 3.2ms | 13,082 | 1.3 MB |
| SearchWithSourceFilter | 1,930 ops/s | 518µs | 1,865 | 164 KB |
| SearchByContent | 39.9k ops/s | 25µs | 307 | 8.7 KB |
| DeleteMessage | 14.7k ops/s | 68µs | 8 | 224 B |
| DeleteExpired | 4.8k ops/s | 207µs | 141 | 5.0 KB |
| DeleteByClassification | 4.9k ops/s | 202µs | 97 | 3.9 KB |
| BulkIngest (100 msgs) | 66 ops/s | 15ms | 9,092 | 564 KB |
| Stats | 30k ops/s | 33µs | 140 | 5.1 KB |
| GetMessage | 132.6k ops/s | 7.5µs | 86 | 2.4 KB |
| GetChunks | 97k ops/s | 10.3µs | 121 | 5.5 KB |
| VectorToJSON | 3.0M ops/s | 328ns | 3 | 120 B |
| JSONToVector | 1.1M ops/s | 907ns | 9 | 472 B |

## Performance Hotspots (Optimization Opportunities)

### 1. **Search Operation** - CRITICAL 🔴
- **Current**: 3.2ms per search, 13,082 allocations
- **Bottleneck**: Row scanning and chunk deserialization in result aggregation
- **Optimization**: 
  - Pre-allocate result slice with proper capacity
  - Reuse embedding JSON deserialization (cache or lazy parse)
  - Implement query result pooling
- **Target**: Reduce to 500µs (6.4x improvement)

### 2. **BulkIngest** - HIGH 🟠
- **Current**: 15ms for 100 messages (150µs per message)
- **Bottleneck**: Transaction overhead per message, repeated embeddings
- **Optimization**:
  - Batch embeddings API calls (if using remote)
  - Reduce transaction frequency for large imports
  - Pool message ID hash computations
- **Target**: Reduce to 10ms (1.5x improvement)

### 3. **SaveChunks Batch** - MODERATE 🟡
- **Current**: 85µs for 10 chunks (8.5µs per chunk), but 284 allocations
- **Bottleneck**: JSON embedding serialization allocations
- **Optimization**:
  - Reuse JSON marshaling buffers
  - Implement string interning for provider/model names
- **Target**: Reduce allocations from 284 to 100 (64% reduction)

### 4. **SaveMessage** - MODERATE 🟡
- **Current**: 86µs, 33 allocations
- **Bottleneck**: UPSERT query complexity with many fields
- **Optimization**:
  - Simplify prepared statement to avoid conditional logic
  - Batch multiple messages in single transaction
- **Target**: Reduce to 50µs (1.7x improvement)

### 5. **JSONToVector** - LOW 🟢
- **Current**: 907ns, 9 allocations per embedding
- **Bottleneck**: JSON unmarshaling overhead
- **Optimization**:
  - Implement custom binary encoding for vectors
  - Cache deserialized vectors if used multiple times
- **Target**: Reduce to 200ns (4.5x improvement) - low priority

## Fast Operations (No Action Needed) ✅

- **CosineSimilarity**: 2.8ns, zero allocations - already optimal
- **LocalHashingEmbed**: 2.1µs - very fast for local hashing
- **SearchByContent**: 25µs - text search is efficient
- **GetMessage/GetChunks**: 7.5-10µs - good query performance
- **VectorToJSON**: 328ns - efficient serialization

## Scalability Concerns

### Current Limitations
1. **Memory per search**: 1.3 MB for 300 messages - grows with dataset
2. **Allocation rate**: 13,082 allocations per search - GC pressure
3. **Transaction overhead**: Each operation has fixed overhead (~60µs)

### Scaling Strategy
- Use pagination for large searches (TOP 10 default mitigates)
- Implement result streaming for large datasets
- Consider sharding by classification level

## Recommended Optimization Priority

1. **Phase A** (High Impact, Low Risk)
   - [ ] Pre-allocate result slice in Search (1-2ms savings)
   - [ ] Batch embeddings for local hashing (200µs savings per message)
   - [ ] Implement message ID cache for UPSERT (15µs savings)

2. **Phase B** (Medium Impact)
   - [ ] JSON buffer pooling for chunk serialization (reduce 284→100 allocs)
   - [ ] String interning for provider/model names (reduce allocations)
   - [ ] Lazy JSON deserialization in search results

3. **Phase C** (Lower Priority)
   - [ ] Custom vector encoding instead of JSON
   - [ ] Result streaming for large datasets
   - [ ] Query result caching for common searches

## Memory Profiling

To identify allocation hotspots:
```bash
go test -bench=BenchmarkSearch -benchmem -cpuprofile=cpu.prof -memprofile=mem.prof ./internal/knowledge
go tool pprof mem.prof
```

To analyze CPU profile:
```bash
go tool pprof cpu.prof
```

## Monitoring

Track these metrics in production:
- Search latency (p50, p95, p99)
- Allocation rate per search
- Database query time (SQL-level profiling)
- GC pause time and frequency

## Compilation Flags

Build with optimizations:
```bash
CGO_ENABLED=1 go build -ldflags="-s -w" ./cmd/cadre
```

Run with profiling enabled (development only):
```bash
GODEBUG=gctrace=1 ./cadre knowledge search --classification general "query"
```
