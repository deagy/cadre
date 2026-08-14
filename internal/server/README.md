# Phase 10: HTTP Server & Endpoints

This package provides HTTP server for observability, health checks, and metrics export.

## Endpoints

### Health Check (`/health`)
Liveness probe for Kubernetes and container orchestration.

```bash
curl http://localhost:8080/health
```

Response:
```json
{"status":"healthy","timestamp":"2025-08-13T16:30:00Z","version":"1.0.0","components":3}
```

Returns:
- **200 OK** if all components are healthy
- **503 Service Unavailable** if any component is unhealthy

### Readiness Check (`/ready`)
Readiness probe for traffic acceptance.

```bash
curl http://localhost:8080/ready
```

Response:
```json
{"status":"ready","timestamp":"2025-08-13T16:30:00Z"}
```

Returns:
- **200 OK** if service is ready
- **503 Service Unavailable** if service is not ready

### Status (`/status`)
Combined health and readiness status.

```bash
curl http://localhost:8080/status
```

Response:
```json
{"health":"healthy","ready":true,"version":"1.0.0"}
```

### Metrics (`/metrics`)
Prometheus-compatible metrics export.

```bash
curl http://localhost:8080/metrics
```

Exports:
- Request counters (by method and path)
- Request latencies (histogram with percentiles)
- Error counters (by error type)
- Process uptime
- Custom business metrics

## Usage

```go
import "github.com/deagy/cadre/cli/internal/server"

// Create server
config := production.NewConfigFromEnv()
logger, _ := production.NewProductionLogger(config, "cli")

server := server.NewServer(config, logger)

// Register health checks
server.RegisterHealthCheck("database", func() (string, error) {
    // Check database connectivity
    return "connected", nil
})

server.RegisterHealthCheck("cache", func() (string, error) {
    // Check cache health
    return "ready", nil
})

// Register readiness checks
server.RegisterReadinessCheck("migrations", func() bool {
    // Check if migrations are complete
    return true
})

// Start server
if err := server.Start(); err != nil {
    log.Fatal(err)
}

// Graceful shutdown
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
server.Shutdown(ctx)
```

## Kubernetes Integration

### Liveness Probe

```yaml
livenessProbe:
  httpGet:
    path: /health
    port: 8080
  initialDelaySeconds: 10
  periodSeconds: 30
  timeoutSeconds: 5
  failureThreshold: 3
```

### Readiness Probe

```yaml
readinessProbe:
  httpGet:
    path: /ready
    port: 8080
  initialDelaySeconds: 5
  periodSeconds: 10
  timeoutSeconds: 3
  failureThreshold: 2
```

## Metrics Collection

The server automatically collects:

- **Request metrics**: `http_requests_total`, `http_request_duration_ms`
- **Error metrics**: `errors_total` by error type
- **Process metrics**: `process_uptime_seconds`

Custom metrics can be recorded:

```go
server.recordMetric("custom_counter", 1, map[string]string{
    "label": "value",
})

server.recordMetric("custom_latency_ms", 50, map[string]string{
    "operation": "fetch_data",
})
```

## Configuration

All settings via environment variables (see `production/config.go`):

```bash
export PORT=8080
export HOST=0.0.0.0
export HEALTH_CHECK_PATH=/health
export READY_CHECK_PATH=/ready
export ENVIRONMENT=production
```

## Testing

```bash
go test -v ./internal/server/...
```

## See Also

- `production/` - Configuration management and security
- `orchestration/` - Agent orchestration engine
