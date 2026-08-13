# Phase 11: Observability (OpenTelemetry-Compatible Tracing)

Distributed tracing infrastructure with OpenTelemetry-compatible span management and trace context propagation.

## Distributed Tracing

### Creating Traces and Spans

```go
import "github.com/deagy/cadre/cli/internal/observability"

tracer := observability.NewTracer("cadre-cli")

// Start a span
ctx := context.Background()
span, newCtx := tracer.StartSpan(ctx, "agent-dispatch")

// Add attributes to the span
span.AddAttribute("agent_id", "agent-001")
span.AddAttribute("task_type", "review")

// Record events
span.AddEvent("agent-started", map[string]interface{}{
    "worker_id": "worker-1",
})

// Handle errors
if err != nil {
    span.RecordError(err)
}

// End the span
tracer.EndSpan(span)

// Export span data
exported := tracer.ExportSpan(span.SpanID)
```

### Trace Context Propagation

#### Through Go Context

Trace IDs are propagated via Go's `context.Context`:

```go
span, ctx := tracer.StartSpan(context.Background(), "parent-operation")
defer tracer.EndSpan(span)

// Pass context to child operations
childSpan, childCtx := tracer.StartSpan(ctx, "child-operation")
defer tracer.EndSpan(childSpan)
```

#### Through Environment Variables

For subprocess/Python integration:

```go
extractor := observability.NewContextExtractor()
traceID := extractor.ExtractTraceID()

ctx := &observability.TraceContext{
    TraceID: traceID,
    SpanID:  span.SpanID,
    Baggage: map[string]string{
        "task_id": "TASK-001",
    },
}

// Inject into subprocess environment
env := ctx.InjectIntoEnv(os.Environ())
cmd := exec.CommandContext(context.Background(), "python", "subcommand.py")
cmd.Env = env
```

### Span Attributes and Events

```go
// Add structured attributes
span.AddAttribute("user_id", user.ID)
span.AddAttribute("operation", "deploy")
span.AddAttribute("request_size_bytes", 1024)

// Record discrete events
span.AddEvent("validation-started", map[string]interface{}{
    "step": 1,
    "total_steps": 5,
})

span.AddEvent("validation-complete", map[string]interface{}{
    "step": 1,
    "duration_ms": 125,
})

// Mark errors
span.RecordError(fmt.Errorf("validation failed: %s", reason))
span.SetStatus("error")
```

## Integration with HTTP Server

The tracing system integrates with Phase 10's HTTP server:

```go
server := server.NewServer(config, logger)

// Register health check that reports on trace collection
server.RegisterHealthCheck("tracing", func() (string, error) {
    spanCount := tracer.GetSpanCount()
    if spanCount > 0 {
        return fmt.Sprintf("collecting traces (%d spans)", spanCount), nil
    }
    return "idle", nil
})
```

## Span Status

Spans can have statuses:
- **"unset"** - No explicit status set
- **"ok"** - Operation succeeded
- **"error"** - Operation failed

```go
span.SetStatus("ok")
// or
span.RecordError(err) // automatically sets to "error"
```

## Exporting Traces

### Single Span Export

```go
exported := tracer.ExportSpan(span.SpanID)
// Returns map with span details, attributes, events
```

### Batch Export

```go
allSpans := tracer.ExportAllSpans()
// Returns []map[string]interface{} with all spans
```

### Export Format

Spans export as JSON-compatible structures:

```json
{
  "trace_id": "0123456789abcdef",
  "span_id": "fedcba9876543210",
  "parent_span_id": "",
  "name": "agent-dispatch",
  "start_time": "2025-08-13T16:30:00.123Z",
  "end_time": "2025-08-13T16:30:00.250Z",
  "duration_ms": 127,
  "status": "ok",
  "service_name": "cadre-cli",
  "attributes": [
    {"key": "agent_id", "value": "agent-001"},
    {"key": "task_type", "value": "review"}
  ],
  "events": [
    {
      "name": "agent-started",
      "timestamp": "2025-08-13T16:30:00.125Z",
      "attributes": {"worker_id": "worker-1"}
    }
  ]
}
```

## Environment Variables

- `TRACING_ENABLED` - Enable/disable tracing (default: true)
- `TRACE_ID` - Override trace ID (auto-generated if not set)
- `SPAN_ID` - Override span ID (auto-generated if not set)
- `BAGGAGE_*` - Custom baggage items propagated through traces

## Testing

```bash
go test -v ./internal/observability/...
```

Test coverage includes:
- Span creation and lifecycle
- Attribute and event recording
- Error handling and status tracking
- Trace context extraction
- Environment variable injection
- Batch export functionality

## Performance Considerations

- Spans are stored in memory; export periodically in production
- Trace IDs use nanosecond timestamps + PID for uniqueness
- Concurrent access is thread-safe via mutexes
- Zero overhead when tracing is disabled

## Production Deployment

1. Enable distributed tracing:
   ```bash
   export TRACING_ENABLED=true
   ```

2. Set service identifier:
   ```bash
   export SERVICE_NAME=cadre-cli
   ```

3. Export traces periodically (e.g., to logs or collector):
   ```go
   ticker := time.NewTicker(30 * time.Second)
   go func() {
       for range ticker.C {
           spans := tracer.ExportAllSpans()
           logger.Info("trace-export", map[string]interface{}{
               "span_count": len(spans),
               "spans": spans,
           })
       }
   }()
   ```

## See Also

- `server/` - HTTP server with metrics endpoints
- `production/` - Configuration management
- `orchestration/` - Agent orchestration engine
