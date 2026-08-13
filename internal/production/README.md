# Phase 9: Production Readiness

This package provides comprehensive production-readiness utilities for the Go CLI, including configuration management, graceful shutdown, health checking, security hardening, and deployment support.

## Components

### Configuration Management (`config.go`)

Environment-based configuration loading with sensible defaults.

```go
config := production.NewConfigFromEnv()
if err := config.Validate(); err != nil {
    log.Fatal(err)
}
```

**Supported environment variables:**
- `PORT` (default: 8080)
- `HOST` (default: 0.0.0.0)
- `READ_TIMEOUT` (default: 30s)
- `WRITE_TIMEOUT` (default: 30s)
- `SHUTDOWN_TIMEOUT` (default: 30s)
- `LOG_LEVEL` (default: info)
- `LOG_FORMAT` (default: json)
- `LOG_OUTPUT` (default: stdout)
- `CACHE_ENABLED` (default: true)
- `CACHE_SIZE` (default: 1000)
- `CACHE_TTL` (default: 1h)
- `RATE_LIMIT_ENABLED` (default: true)
- `RATE_LIMIT_RPS` (default: 100.0)
- `QUOTA_WINDOW` (default: 1m)
- `MAX_AGENTS` (default: 10)
- `AGENT_TIMEOUT` (default: 5m)
- `REQUIRE_AUTH` (default: false)
- `API_KEY_ENV` (default: API_KEY)
- `HEALTH_CHECK_PATH` (default: /health)
- `READY_CHECK_PATH` (default: /ready)
- `ENVIRONMENT` (default: development)
- `VERSION` (default: unknown)

### Graceful Shutdown (`shutdown.go`)

Handles clean shutdown with signal handling and connection draining.

```go
shutdown := production.NewShutdownManager(30 * time.Second)
shutdown.Start()

go func() {
    <-shutdown.Wait()
    // Cleanup logic
}()
```

**ConnectionDrainer** tracks active connections and waits for them to complete:

```go
drainer := production.NewConnectionDrainer(30 * time.Second)

// On request start
drainer.Acquire()

// On request end
defer drainer.Release()

// On shutdown
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
drainer.Wait(ctx)
```

### Health Checks (`health.go`)

Provides liveness and readiness probes for container orchestration.

```go
healthChecker := production.NewHealthChecker("1.0.0")

// Register component checks
healthChecker.RegisterCheck("database", func() (string, error) {
    // Check database connectivity
    return "connected", nil
})

// Check all components
status := healthChecker.CheckAll()
fmt.Printf("%+v\n", status)

// Readiness checking
readiness := production.NewReadinessChecker()
readiness.RegisterCheck("cache", func() bool {
    return cacheIsReady
})

if readiness.IsReady() {
    // Accept traffic
}
```

### Production Logging (`logging.go`)

Structured JSON logging with rotation support.

```go
logger, err := production.NewProductionLogger(config, "orchestration")
if err != nil {
    log.Fatal(err)
}

logger.Info("Request received", map[string]interface{}{
    "method": "POST",
    "path": "/api/tasks",
})

logger.Error("Processing failed", err, map[string]interface{}{
    "task_id": "TASK-001",
})

defer logger.Close()
```

**Structured logging with context:**

```go
structured := production.NewStructuredLogger(logger)
structured.WithContext(&production.LogContextKey{
    TraceID:   "trace-123",
    RequestID: "req-456",
})

structured.LogRequest("POST", "/api/tasks", nil)
```

### Security Hardening (`security.go`)

Input validation, API key management, and permission control.

```go
// Authentication
authValidator := production.NewAuthValidator()
authValidator.AddAPIKey("secret-key-123")
if authValidator.ValidateAPIKey(providedKey, expectedKey) {
    // Authenticated
}

// Input validation
validator := production.NewInputValidator(5000) // 5KB limit
if err := validator.ValidateTaskID("TASK-001"); err != nil {
    // Handle validation error
}

if err := validator.ValidateFilePath("src/main.go"); err != nil {
    // Handle path error
}

sanitized := validator.SanitizeInput(userInput)

// Secrets management
secrets := production.NewSecretsManager()
secrets.SetSecret("db-password", os.Getenv("DB_PASSWORD"))
password, _ := secrets.GetSecret("db-password")

// Permission control
perms := production.NewPermissionValidator()
perms.GrantPermission("user1", "read")
perms.GrantPermission("user1", "write")
if perms.HasPermission("user1", "read") {
    // Allowed
}

// Rate limiting per client
rateLimiter := production.NewRateLimitValidator()
if rateLimiter.CheckRateLimit(clientID, 100) {
    // Process request
} else {
    // Reject: rate limit exceeded
}

// Security headers
headers := production.NewSecurityHeaders()
// Use headers in HTTP responses
```

### Deployment Support (`deployment.go`)

Configuration and manifest generation for Docker and Kubernetes.

```go
// Generate Docker configuration
info := production.NewDeploymentInfo()
if info.IsProduction() {
    // Production-specific setup
}

// Generate Dockerfile
production.GenerateDockerfile(config, "./Dockerfile")

// Generate Kubernetes manifest
production.GenerateKubernetesDeployment(config, info, "./k8s-deployment.yaml")

// Generate docker-compose
production.GenerateDockerCompose(config, "./docker-compose.yml")

// Generate configuration file
production.GenerateConfigurationFile(config, "/etc/cadre/config.toml")

// Pre-flight validation
validator := production.NewDeploymentValidator()
if err := validator.PreFlightCheck(); err != nil {
    log.Fatal(err)
}

// Load balancing
loadBalancer := production.NewServiceLoadBalancer()
loadBalancer.AddInstance(production.ServiceInstance{
    ID:       "instance-1",
    Host:     "localhost",
    Port:     8080,
    Healthy:  true,
    Weight:   1,
})

instance, _ := loadBalancer.GetNextInstance()
```

## Production Checklist

- [ ] Configuration management via environment variables
- [ ] Graceful shutdown with signal handling
- [ ] Connection draining on shutdown
- [ ] Health checks (liveness and readiness probes)
- [ ] Structured JSON logging
- [ ] Log rotation support
- [ ] API authentication and validation
- [ ] Input validation and sanitization
- [ ] Secrets management
- [ ] Permission/authorization control
- [ ] Rate limiting per client
- [ ] Security headers (CSP, HSTS, X-Frame-Options, etc.)
- [ ] Deployment manifests (Docker, Kubernetes)
- [ ] Pre-flight deployment validation
- [ ] Service load balancing
- [ ] Database health checks
- [ ] Cache health checks
- [ ] Observability/monitoring integration
- [ ] Error handling and recovery
- [ ] Resource limits configuration

## Environment Configuration for Production

```bash
# Server
export PORT=8080
export HOST=0.0.0.0
export READ_TIMEOUT=30s
export WRITE_TIMEOUT=30s
export SHUTDOWN_TIMEOUT=30s

# Logging
export LOG_LEVEL=info
export LOG_FORMAT=json
export LOG_OUTPUT=/var/log/cadre/cadre.log

# Cache
export CACHE_ENABLED=true
export CACHE_SIZE=1000
export CACHE_TTL=1h

# Rate Limiting
export RATE_LIMIT_ENABLED=true
export RATE_LIMIT_RPS=100.0
export QUOTA_WINDOW=1m

# Agents
export MAX_AGENTS=10
export AGENT_TIMEOUT=5m

# Security
export REQUIRE_AUTH=true
export API_KEY_ENV=CADRE_API_KEY

# Environment
export ENVIRONMENT=production
export VERSION=1.0.0
export REVISION=$(git rev-parse --short HEAD)
export DEPLOYED_BY=cicd-pipeline
export BUILD_NUMBER=$CI_BUILD_NUMBER
export DOCKER_IMAGE=registry.example.com/cadre:1.0.0
```

## Docker Deployment

```bash
docker build -t cadre:latest .
docker run \
  -e ENVIRONMENT=production \
  -e LOG_FORMAT=json \
  -e MAX_AGENTS=10 \
  -p 8080:8080 \
  cadre:latest
```

## Kubernetes Deployment

```bash
kubectl apply -f k8s-deployment.yaml

# Check health
kubectl port-forward svc/cadre 8080:80
curl http://localhost:8080/health
curl http://localhost:8080/ready

# Logs
kubectl logs -f deployment/cadre

# Scale
kubectl scale deployment cadre --replicas 5
```

## Health Check Endpoints

- **Liveness Probe** (`/health`): Indicates if the process is running
- **Readiness Probe** (`/ready`): Indicates if the service is ready to accept traffic

Response format:

```json
{
  "status": "healthy",
  "timestamp": "2025-08-13T10:30:00Z",
  "version": "1.0.0",
  "components": {
    "database": {
      "status": "healthy",
      "message": "connected",
      "duration_ms": 5,
      "timestamp": "2025-08-13T10:30:00Z"
    }
  }
}
```

## Testing

```bash
go test -v ./internal/production/...
```

## See Also

- `router/orchestration/` - Orchestration engine
- `router/performance_monitoring.go` - Performance metrics
- `router/rate_limiting.go` - Rate limiting implementation
- `router/audit_logging.go` - Audit logging
