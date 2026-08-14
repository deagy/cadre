<!-- GENERATED FILE: edit the canonical source and regenerate; do not edit this copy. -->

# Week 1-5 Infrastructure Implementation Guide

Complete infrastructure setup for production deployment.

## Week 1: Critical Path (10-12 days)

### Day 1-2: CI/CD Pipeline Setup

**Status:** ✓ IMPLEMENTED

**Files Created:**
- `.github/workflows/ci-cd.yml` - Full GitHub Actions pipeline

**Pipeline Stages:**
1. **Test Job** (2 min)
   - Run Go tests with race detector
   - Code coverage reporting
   - Linting with golangci-lint
   - Format checking with gofmt

2. **Security Job** (3 min)
   - GoSec scanning
   - Trivy vulnerability scanning
   - SLSA provenance generation

3. **Build Job** (5 min, on release branch)
   - Docker image build
   - Registry push
   - Multi-stage caching

4. **Deploy to Staging** (5 min)
   - Kubernetes deployment update
   - Rollout status verification
   - Health check validation

5. **Integration Tests** (5 min)
   - Full E2E test suite
   - Against staging environment

6. **Load Testing** (10 min)
   - k6 load test execution
   - SLA validation
   - Performance metrics collection

7. **Production Release** (on tag)
   - Automated release notes
   - Changelog generation
   - Production deployment

**Triggers:**
- Every push to main or release/go-cli
- Every pull request
- Manual trigger on tags for production

**Actions to Complete:**
- [ ] Set up GitHub secrets (KUBE_CONFIG, KUBE_CONFIG_PROD)
- [ ] Configure container registry
- [ ] Set up staging Kubernetes access
- [ ] Enable branch protection rules
- [ ] Configure required status checks

### Day 3-4: Database Setup

**Status:** ✓ IMPLEMENTED

**Database Schema Files:**
- `db/schema.sql` - Full database schema

**Tables Created:**

1. **audit_logs** - Immutable audit trail
   - Supports chain integrity verification
   - Indexed by trace_id, actor, event_type
   - JSONB metadata support

2. **agent_executions** - Agent execution tracking
   - Status tracking
   - Quality scoring
   - Error counting

3. **tasks** - Task lifecycle management
   - Status tracking (pending → completed)
   - Workflow correlation
   - Result summary

4. **cache_entries** - Performance optimization
   - TTL-based expiration
   - Hit rate tracking
   - Access patterns

5. **rate_limits** - Rate limit enforcement
   - Per-client tracking
   - Window-based quotas
   - Cleanup queries

6. **performance_metrics** - Performance tracking
   - Latency collection
   - Status codes
   - Percentile calculations

7. **secrets** - Encrypted secret storage
   - Encryption at rest
   - Access tracking
   - Rotation timestamps

8. **roles** (for Phase 13A RBAC) - Authorization
9. **user_roles** (for Phase 13A) - Role assignment
10. **health_checks** - Component health history

**Views:**
- `audit_summary` - Daily audit aggregates
- `performance_summary` - 24h performance stats with P50/P95/P99

**Migration Strategy:**

```bash
# Initialize schema
psql -h localhost -U cadre < db/schema.sql

# Verify tables created
psql -h localhost -U cadre -c "\dt"

# Verify views
psql -h localhost -U cadre -c "\dv"
```

**Actions to Complete:**
- [ ] Set up PostgreSQL database
- [ ] Run schema initialization
- [ ] Configure backups (daily)
- [ ] Set up replication (if multi-region)
- [ ] Configure connection pooling (PgBouncer)
- [ ] Test disaster recovery procedure

### Day 5-6: Load Testing Framework

**Status:** ✓ IMPLEMENTED

**Load Test File:**
- `load-test.js` - k6 performance testing suite

**Test Scenarios:**

1. **Ramp-up Phase** (2 min)
   - 0 → 50 VUs linearly
   - Gradual load introduction
   - Warm-up metrics

2. **Sustained Load** (5 min)
   - 50 VUs constant
   - Normal operation baseline
   - Metric stabilization

3. **Spike Test** (3 min)
   - 50 → 100 VUs sudden
   - 100 VUs sustained 2 min
   - Load spike response

4. **Ramp-down** (2 min)
   - 100 → 0 VUs linearly
   - Graceful shutdown behavior
   - Connection cleanup

**Endpoints Tested:**
- `/health` - Liveness probe (target: <100ms)
- `/ready` - Readiness probe (target: <50ms)
- `/status` - Combined status (target: <100ms)
- `/metrics` - Prometheus export (target: <200ms)
- `404` - Error handling verification

**SLA Thresholds:**
- P95 latency: <500ms ✓
- P99 latency: <1000ms ✓
- Error rate: <0.1% ✓

**Running Load Tests:**

```bash
# Install k6
curl https://dl.k6.io/install/linux.sh | bash

# Run load test against localhost
K6_VUS=50 K6_DURATION=5m k6 run load-test.js

# Run against staging
CADRE_URL=https://cadre-staging.example.com k6 run load-test.js

# Generate HTML report
k6 run --out json=results.json load-test.js
# Use online reporter to view results
```

**Actions to Complete:**
- [ ] Install k6 in CI
- [ ] Configure staging endpoint
- [ ] Set baseline metrics
- [ ] Monitor P95/P99 trends
- [ ] Alert on SLA violations
- [ ] Document performance tuning

## Week 2: Security & Observability (8-10 days)

### Day 1-2: Automated Security Scanning

**Implemented:**
- GoSec integration in CI (gosec scanning)
- Trivy container scanning
- SLSA provenance generation
- golangci-lint with security rules

**Manual Setup Required:**

1. **GitHub Secret Scanning**
   - Enable in repo settings
   - Configure custom patterns

2. **Dependabot Integration**
   - Enable dependabot alerts
   - Configure update schedule
   - Set auto-merge for patches

3. **CodeQL Analysis**
   - Already integrated in CI
   - Configure for Go analysis
   - Set query suite to "extended"

**Security Scanning Commands:**

```bash
# Run GoSec locally
go install github.com/securego/gosec/v2/cmd/gosec@latest
gosec ./...

# Run Trivy on code
go install github.com/aquasecurity/trivy@latest
trivy fs .

# Run Trivy on images
trivy image cadre:latest
```

**Actions to Complete:**
- [ ] Enable Dependabot in GitHub
- [ ] Configure branch protection for security checks
- [ ] Set up security dashboard
- [ ] Create incident response for vulnerabilities
- [ ] Schedule periodic security audits

### Day 3-4: Staging Environment

**Kubernetes Staging Setup:**

```yaml
# namespaces.yaml
apiVersion: v1
kind: Namespace
metadata:
  name: cadre-staging
  labels:
    environment: staging

---
# staging-deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: cadre-staging
  namespace: cadre-staging
spec:
  replicas: 2
  selector:
    matchLabels:
      app: cadre
      env: staging
  template:
    metadata:
      labels:
        app: cadre
        env: staging
    spec:
      containers:
      - name: cadre
        image: cadre:staging
        env:
        - name: ENVIRONMENT
          value: staging
        - name: LOG_LEVEL
          value: debug
        - name: DATABASE_URL
          valueFrom:
            secretKeyRef:
              name: db-credentials
              key: url
        livenessProbe:
          httpGet:
            path: /health
            port: 8080
          initialDelaySeconds: 10
          periodSeconds: 30
        readinessProbe:
          httpGet:
            path: /ready
            port: 8080
          initialDelaySeconds: 5
          periodSeconds: 10

---
# staging-service.yaml
apiVersion: v1
kind: Service
metadata:
  name: cadre-staging
  namespace: cadre-staging
spec:
  type: LoadBalancer
  ports:
  - port: 80
    targetPort: 8080
    name: http
  selector:
    app: cadre
    env: staging
```

**Database Setup for Staging:**

```bash
# Create staging database
createdb -h localhost -U cadre cadre_staging

# Initialize schema
psql -h localhost -U cadre -d cadre_staging < db/schema.sql

# Seed test data
psql -h localhost -U cadre -d cadre_staging < db/seed.sql
```

**Actions to Complete:**
- [ ] Create cadre-staging namespace
- [ ] Deploy staging database
- [ ] Configure staging ingress
- [ ] Set up staging monitoring
- [ ] Create staging secrets
- [ ] Document staging runbook

### Day 5-6: Observability Stack

**Deployment Architecture:**

```
┌─────────────────────────────────────────┐
│  Cadre Service                          │
│  ├── Metrics Export (/metrics)          │
│  ├── Structured Logs (JSON)             │
│  └── Distributed Traces (OpenTelemetry) │
└─────────────────────────────────────────┘
           ↓           ↓           ↓
     ┌──────────┬──────────┬──────────┐
     ↓          ↓          ↓
  Prometheus  Loki      Jaeger
     ↓          ↓          ↓
  Grafana ← ─────────────┘
```

**Components to Deploy:**

1. **Prometheus**
   ```yaml
   helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
   helm install prometheus prometheus-community/prometheus
   
   # Configure scrape job
   - job_name: cadre
     static_configs:
     - targets: ['cadre:8080']
     metrics_path: '/metrics'
   ```

2. **Grafana**
   ```bash
   helm install grafana grafana/grafana
   
   # Import dashboards:
   # - Kubernetes cluster monitoring
   # - HTTP server metrics
   # - Custom Cadre metrics
   ```

3. **Loki** (Log Aggregation)
   ```bash
   helm install loki grafana/loki-stack
   
   # Configure log labels:
   # - environment
   # - component
   # - level
   ```

4. **Jaeger** (Distributed Tracing)
   ```bash
   kubectl create deployment jaeger --image=jaegertracing/all-in-one
   kubectl expose deployment jaeger --port=6831 --target-port=6831 --protocol=UDP
   ```

**Kubernetes ServiceMonitor (for Prometheus Operator):**

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: cadre-monitor
spec:
  selector:
    matchLabels:
      app: cadre
  endpoints:
  - port: http
    interval: 30s
    path: /metrics
```

**Actions to Complete:**
- [ ] Deploy Prometheus
- [ ] Deploy Grafana
- [ ] Deploy Loki
- [ ] Deploy Jaeger
- [ ] Import dashboards
- [ ] Configure alerts
- [ ] Document observability access

## Week 3: Features & Compliance (8-10 days)

### Day 1-2: Feature Flags

**Integration Pattern:**

```go
import "github.com/deagy/cadre/internal/features"

flags := features.NewFlagClient(config)

// Check flag at runtime
if flags.IsEnabled("new-agent-dispatch") {
    // Use new implementation
} else {
    // Use legacy implementation
}

// Track flag usage
flags.RecordFlag("new-agent-dispatch", userID, true)
```

**Implementation Stub:**

```go
// internal/features/flags.go
package features

type FlagClient struct {
    enabled map[string]bool
    mu sync.RWMutex
}

func NewFlagClient(config *Config) *FlagClient {
    return &FlagClient{
        enabled: make(map[string]bool),
    }
}

func (f *FlagClient) IsEnabled(flag string) bool {
    f.mu.RLock()
    defer f.mu.RUnlock()
    return f.enabled[flag]
}
```

**Actions to Complete:**
- [ ] Choose feature flag service (LaunchDarkly, FlagSmith)
- [ ] Integrate with CLI
- [ ] Define feature rollout strategy
- [ ] Set up A/B testing
- [ ] Document feature flag process

### Day 3-4: Compliance Scanning

**CIS Kubernetes Benchmark:**

```bash
# Install kubesec
go install github.com/kubesec/kubesec@latest

# Scan deployments
kubesec scan k8s-deployment.yaml

# Review scores
# Score > 5: Good
# Score 0-5: Warning
# Score < 0: Critical
```

**GDPR Data Retention:**

```sql
-- Archive audit logs older than 90 days
CREATE POLICY archive_old_logs
ON audit_logs
FOR DELETE
USING (created_at < NOW() - INTERVAL '90 days');

-- Delete PII after retention period
DELETE FROM audit_logs 
WHERE metadata->>'user_id' IS NOT NULL
  AND created_at < NOW() - INTERVAL '1 year';
```

**Actions to Complete:**
- [ ] Run kubesec scan
- [ ] Fix critical issues
- [ ] Implement data retention policies
- [ ] Set up compliance reporting
- [ ] Schedule quarterly audits

### Day 5-6: End-to-End Testing

**E2E Test Example:**

```go
// e2e_test.go
func TestE2EDeployment(t *testing.T) {
    // 1. Verify service is healthy
    resp, _ := http.Get(stagingURL + "/health")
    assert.Equal(t, 200, resp.StatusCode)
    
    // 2. Verify readiness
    resp, _ = http.Get(stagingURL + "/ready")
    assert.Equal(t, 200, resp.StatusCode)
    
    // 3. Verify metrics collection
    resp, _ = http.Get(stagingURL + "/metrics")
    assert.Contains(t, resp.Body, "http_requests_total")
    
    // 4. Verify database connectivity
    err := db.Ping()
    assert.NoError(t, err)
    
    // 5. Run load test
    result := runLoadTest(stagingURL)
    assert.Less(t, result.P95, 500*time.Millisecond)
}
```

**Actions to Complete:**
- [ ] Write E2E test suite
- [ ] Run against staging
- [ ] Validate all SLAs
- [ ] Document test procedures
- [ ] Set up continuous E2E monitoring

## Week 4: Deployment (4-6 days)

### Production Deployment Runbook

**Pre-Deployment:**
- [ ] All tests passing
- [ ] Load tests validate SLAs
- [ ] Security scan clean
- [ ] Documentation complete
- [ ] Team trained

**Deployment Steps:**

1. **Canary (10% of traffic):**
   ```bash
   kubectl patch service cadre -p '{"spec":{"selector":{"version":"v1.1.0"}}}'
   # Route 10% traffic to new version
   # Monitor metrics for 15 min
   ```

2. **Progressive Rollout (25% → 50% → 100%):**
   ```bash
   kubectl set image deployment/cadre cadre=cadre:v1.1.0
   kubectl rollout status deployment/cadre
   ```

3. **Verification:**
   ```bash
   # Check health
   curl production-cadre/health
   
   # Check metrics don't spike
   curl production-cadre/metrics | grep error
   
   # Check latency acceptable
   curl production-cadre/metrics | grep duration
   ```

4. **Rollback If Needed:**
   ```bash
   kubectl rollout undo deployment/cadre
   ```

**Actions to Complete:**
- [ ] Run full E2E test suite
- [ ] Execute canary deployment
- [ ] Monitor first hour closely
- [ ] Verify alerts not firing
- [ ] Document deployment

## Week 5: Hardening (3-5 days)

### Phase 13 Security Hardening (Roadmap)

**Phase 13A: RBAC Implementation**
- Define roles and permissions
- Implement authorization middleware
- Integrate with audit logging

**Phase 13B: Vault Integration**
- Set up HashiCorp Vault
- Integrate secret rotation
- Add secret access audit

**Phase 13C: Audit Trail Enforcement**
- Enhance audit logging with verification
- Implement chain integrity checks
- Set up audit log archival

**Phase 13D: TLS/HTTPS**
- Certificate management
- Cipher suite configuration
- HTTPS enforcement

### Phase 14 Reliability Hardening (Roadmap)

**Phase 14A: Subprocess Coordination**
- Python subprocess lifecycle management
- Graceful shutdown coordination
- Connection draining

**Phase 14B: Backup & Recovery**
- Automated backup procedures
- Point-in-time recovery testing
- Disaster recovery drills

## Deployment Verification Checklist

- [ ] All tests passing (37+ tests)
- [ ] CI/CD pipeline green
- [ ] Load tests validate SLAs
- [ ] Security scan clean
- [ ] Staging deployment stable 24h
- [ ] E2E tests pass
- [ ] Backup procedures tested
- [ ] Runbooks documented
- [ ] On-call team trained
- [ ] Monitoring alerts active
- [ ] Production secrets configured
- [ ] Database backups working
- [ ] Disaster recovery plan documented

## Next Steps After Week 5

1. **Weeks 6-8:** Phase 13 Security Hardening
2. **Weeks 9-10:** Phase 14 Reliability Hardening  
3. **Week 11+:** Advanced features (multi-region HA, client SDKs)
