<!-- GENERATED FILE: edit the canonical source and regenerate; do not edit this copy. -->

# Phases 13-14: Security Hardening & Reliability (Roadmap)

> **Historical record, not a description of the shipped system.** Written
> 2026-08-13 for a direction cadre did not take: a long-running Cadre service
> deployed as a container, with liveness and readiness probes, a Prometheus
> `/metrics` endpoint and a Kubernetes rollout. None of that exists. This
> repository has no `Dockerfile`, no `docker-compose.yml`, no `k8s/` or
> `charts/` directory; nothing in any `.go` file serves `/health`, `/ready` or
> `/metrics`, and there is no Prometheus dependency. `cadre` is a CLI and a
> plugin suite, and its only servers are the two stdio MCP servers listed in
> `cadre --help`.
>
> The commands below therefore cannot be run — `docker build .` has no
> Dockerfile to read and `kubectl apply -f k8s-deployment.yaml` names a file
> that is not in the repository. Kept because the intent is a legible record of
> what was planned; read it as of its date, not as instructions.

## Phase 13: Security Hardening

### Implemented in Phase 9+

**In `internal/production/security.go`:**
- ✓ Input validation and sanitization
- ✓ API key management with constant-time comparison
- ✓ Permission-based authorization (PermissionValidator)
- ✓ Rate limiting per client
- ✓ Secrets management (SecretsManager)
- ✓ Security headers (CSP, HSTS, X-Frame-Options)

**In Deployment Guides:**
- ✓ TLS/HTTPS configuration examples
- ✓ RBAC policy examples (Kubernetes)
- ✓ Network policy templates
- ✓ Secret rotation procedures
- ✓ API key rotation procedures
- ✓ Security scanning in CI/CD

### Phase 13A: Authorization & RBAC (Future Implementation)

**Scope:** Role-based access control enforcement

```go
// Example future implementation
rbac := security.NewRBACEngine(config)

// Define roles
rbac.DefineRole("admin", []string{"*"})
rbac.DefineRole("reviewer", []string{"review:*"})
rbac.DefineRole("viewer", []string{"read:*"})

// Check authorization
if !rbac.Authorize(userID, "review:agent-dispatch") {
    return fmt.Errorf("unauthorized")
}
```

**Files needed:**
- `internal/security/rbac.go` - Role definitions and policy engine
- `internal/security/rbac_test.go` - RBAC tests
- `internal/production/authz_middleware.go` - HTTP middleware enforcement

**Estimated effort:** 400-500 lines

### Phase 13B: Secret Rotation & Vault Integration (Future)

**Scope:** Automated secret lifecycle management

```go
// Example future implementation
vault := security.NewVaultClient(config)

// Retrieve secret
dbPassword, err := vault.GetSecret("database/password")

// Rotate on schedule
vault.RotateSecret("database/password", 90*24*time.Hour)

// Audit trail
vault.LogAccess("database/password", userID)
```

**Files needed:**
- `internal/security/vault_client.go` - Vault integration
- `internal/security/secret_rotation.go` - Rotation scheduler
- Vault deployment manifests (Kubernetes)

**Estimated effort:** 300-400 lines

### Phase 13C: Audit Trail Enforcement (Future)

**Scope:** Enforce immutable audit logging

Currently implemented in `internal/orchestration/audit_logging.go`, but needs enforcement:

```go
// Example future implementation
auditLogger := audit.NewEnforcedAuditLogger(config)

// All mutations logged
auditLogger.Log(&AuditEntry{
    EventType: "command-executed",
    Actor: userID,
    Action: "agent-dispatch",
    Result: "success",
})

// Verify chain integrity
if !auditLogger.VerifyChain() {
    return fmt.Errorf("audit log tampered")
}
```

**Files needed:**
- Enhanced `internal/orchestration/audit_logging.go`
- `internal/security/audit_enforcement.go`
- Audit log backup and archive procedures

**Estimated effort:** 200-300 lines

### Phase 13D: Secrets at Rest & TLS (Future)

**Scope:** Encryption and HTTPS support

```bash
# Environment-based configuration
export HTTPS_ENABLED=true
export TLS_CERT_FILE=/etc/cadre/certs/tls.crt
export TLS_KEY_FILE=/etc/cadre/certs/tls.key
export CIPHER_SUITES=TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256
```

**Files needed:**
- `internal/server/tls.go` - TLS configuration
- `internal/security/cipher_policy.go` - Cipher suite enforcement
- Certificate generation and rotation tooling

**Estimated effort:** 250-300 lines

## Phase 14: Reliability & Resilience (Roadmap)

### Implemented in Phase 4-5

**In `internal/orchestration/`:**
- ✓ Retry logic with exponential backoff
- ✓ Circuit breaker pattern
- ✓ Error recovery strategies
- ✓ Result consolidation with quality scoring

**In `internal/production/`:**
- ✓ Connection draining on shutdown
- ✓ Graceful shutdown with signal handling
- ✓ Health checks for liveness/readiness

### Phase 14A: Subprocess Coordination (Future)

**Scope:** Graceful Python subprocess lifecycle

```go
// Example future implementation
subprocess := reliability.NewManagedSubprocess(cmd)

// Register cleanup handlers
subprocess.OnShutdown(func() error {
    return subprocess.WaitForCompletion()
})

// Start with timeout
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
defer cancel()
result, err := subprocess.Run(ctx)
```

**Files needed:**
- `internal/reliability/subprocess.go` - Process management
- `internal/reliability/subprocess_test.go` - Process tests

**Estimated effort:** 300-400 lines

### Phase 14B: Backup & Point-in-Time Recovery (Future)

**Scope:** Audit log backup and recovery

```bash
# Automated daily backups
0 2 * * * tar -czf /backup/cadre-audit-$(date +\%Y\%m\%d).tar.gz \
           /var/log/cadre/audit.log

# Point-in-time recovery
./bin/cadre recover --to "2025-08-13T15:30:00Z" --output recovered.log
```

**Files needed:**
- `internal/backup/backup_manager.go` - Backup scheduling
- `internal/recovery/recovery_engine.go` - Recovery procedures
- Backup storage and retention policies

**Estimated effort:** 300-400 lines

### Phase 14C: Capacity Planning & Load Forecasting (Future)

**Scope:** Predictive scaling and resource planning

```go
// Example future implementation
planner := reliability.NewCapacityPlanner(metrics)

// Forecast resource needs
forecast := planner.Forecast(7*24*time.Hour)
fmt.Printf("Predicted CPU: %d%%\n", forecast.CPUPercent)
fmt.Printf("Predicted Memory: %dMB\n", forecast.MemoryMB)

// Recommend scaling
if forecast.CPUPercent > 80 {
    fmt.Println("Recommend scaling to 10 replicas")
}
```

**Files needed:**
- `internal/reliability/capacity_planner.go` - Forecasting logic
- `internal/reliability/metrics_predictor.go` - ML-based predictions

**Estimated effort:** 400-500 lines

### Phase 14D: Chaos Engineering & Resilience Testing (Future)

**Scope:** Intentional failure injection for testing

```go
// Example future implementation
chaos := reliability.NewChaosEngine()

// Inject failures
chaos.InjectError("database", 0.05)  // 5% database errors
chaos.InjectLatency("cache", 500*time.Millisecond)  // 500ms delay
chaos.KillRandomAgent(0.01)  // Kill 1% of agents

// Verify system recovers
results := chaos.RunExperiment(context.Background())
```

**Files needed:**
- `internal/chaos/chaos_engine.go` - Failure injection
- `internal/chaos/experiments.go` - Test scenarios
- Chaos test harness and metrics collection

**Estimated effort:** 500-600 lines

## Implementation Priority

### High Priority (For Production)
1. **Phase 13A: RBAC** - Authorization is critical for multi-tenant deployments
2. **Phase 14A: Subprocess Coordination** - Proper Python subprocess lifecycle management
3. **Phase 13C: Audit Trail Enforcement** - Compliance and forensics

### Medium Priority (Recommended)
4. **Phase 13B: Vault Integration** - Secrets at scale
5. **Phase 14B: Backup & Recovery** - Data durability
6. **Phase 13D: TLS** - Secure communication

### Lower Priority (Nice-to-Have)
7. **Phase 14C: Capacity Planning** - Operational efficiency
8. **Phase 14D: Chaos Engineering** - Advanced resilience testing

## Estimated Total Effort

| Phase | Files | Lines | Tests | Effort |
|-------|-------|-------|-------|--------|
| 13A | 3 | 450 | 15 | 4-5 days |
| 13B | 2 | 350 | 10 | 3-4 days |
| 13C | 2 | 250 | 8 | 2-3 days |
| 13D | 3 | 300 | 10 | 3-4 days |
| 14A | 2 | 350 | 12 | 4-5 days |
| 14B | 2 | 300 | 10 | 3-4 days |
| 14C | 2 | 450 | 12 | 4-5 days |
| 14D | 3 | 550 | 15 | 5-6 days |
| **Total** | **19** | **3,000** | **92** | **28-36 days** |

## Next Steps

1. **Immediate (Week 1):**
   - Implement Phase 13A (RBAC)
   - Implement Phase 14A (Subprocess Coordination)
   - Complete Phase 13C (Audit Enforcement)

2. **Short-term (Weeks 2-3):**
   - Implement Phase 13B (Vault Integration)
   - Implement Phase 14B (Backup & Recovery)

3. **Medium-term (Weeks 4-5):**
   - Implement Phase 13D (TLS)
   - Implement Phase 14C (Capacity Planning)

4. **Long-term (Weeks 6+):**
   - Implement Phase 14D (Chaos Engineering)
   - Production compliance audits

## References

- [Security Hardening Guide](./DEPLOYMENT_GUIDE.md#security-hardening)
- [Operations Manual](./OPERATIONS_MANUAL.md)
- [Production Readiness Status](./PRODUCTION_READINESS_STATUS.md)
- Implementation: `internal/production/`, `internal/security/`, `internal/reliability/`
