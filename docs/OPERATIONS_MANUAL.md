# Operations Manual

Day-to-day operational procedures for Cadre CLI production deployments.

## Daily Operations

### Morning Check (09:00)

```bash
#!/bin/bash
# health-check.sh

echo "=== Checking Cadre Health ==="

# Service health
HEALTH=$(curl -s http://cadre:8080/health)
echo "Health: $HEALTH"

# Resource usage
echo "=== Resource Usage ==="
kubectl top pods -n cadre

# Recent errors
echo "=== Recent Errors (last 1 hour) ==="
kubectl logs deployment/cadre --since=1h | grep ERROR | tail -20

# Pending issues
echo "=== Pending Issues ==="
kubectl get events -n cadre --sort-by='.lastTimestamp' | tail -10
```

Run daily check:
```bash
chmod +x health-check.sh
./health-check.sh
```

### Weekly Maintenance (Monday 02:00 UTC)

```bash
# 1. Backup audit logs
tar -czf /backup/cadre-audit-$(date +%Y%m%d).tar.gz /var/log/cadre/audit.log

# 2. Rotate logs
kubectl exec deployment/cadre -- logrotate /etc/logrotate.d/cadre

# 3. Verify database integrity
kubectl exec deployment/cadre -- ./bin/cadre audit verify

# 4. Update vulnerability signatures
kubectl set env deployment/cadre SCAN_VERSION=$(curl -s https://api.example.com/latest-scan)

# 5. Generate health report
kubectl logs deployment/cadre --since=7d | grep "metric:" > /reports/weekly-metrics.txt
```

### Monthly Tasks (First Monday)

- [ ] Review and rotate API keys
- [ ] Audit access logs
- [ ] Update documentation
- [ ] Performance tuning review
- [ ] Capacity planning
- [ ] Security audit

## On-Call Procedures

### Incident Response (P1 - Critical)

**Definition:** Service unavailable, data loss risk, or security breach

1. **Immediate Actions (0-5 min):**
   ```bash
   # 1. Declare incident
   ./bin/cadre incident declare --severity P1 --description "..."
   
   # 2. Enable debug logging
   kubectl set env deployment/cadre LOG_LEVEL=debug
   
   # 3. Gather diagnostics
   kubectl logs deployment/cadre --tail=1000 > incident.log
   
   # 4. Page on-call team
   ./bin/cadre alert page --team oncall
   ```

2. **Investigation (5-30 min):**
   - Review logs and metrics
   - Identify root cause
   - Assess impact scope
   - Decide: fix or rollback

3. **Resolution (30+ min):**
   - Implement fix or rollback
   - Verify service recovery
   - Test failover
   - Document postmortem

### Incident Response (P2 - Urgent)

**Definition:** Degraded performance, increased errors, or partial outages

1. **Actions (0-15 min):**
   - Acknowledge incident
   - Gather metrics and logs
   - Contact on-call engineer
   - Begin investigation

2. **Resolution Timeline:**
   - Goal: Resolved within 1 hour
   - Update status every 15 minutes
   - Escalate to manager if longer

### Incident Response (P3 - Normal)

**Definition:** Minor issues with workaround available

- Address during business hours
- No escalation needed
- Document for future prevention

## Common Operational Tasks

### Rolling Update

```bash
# 1. Pre-update checks
kubectl get pods -n cadre -o wide
kubectl top pods -n cadre

# 2. Begin rolling update
kubectl set image deployment/cadre \
  cadre=registry.example.com/cadre:1.1.0

# 3. Monitor rollout
kubectl rollout status deployment/cadre

# 4. Verify new version
kubectl logs deployment/cadre | grep VERSION

# 5. Rollback if needed
kubectl rollout undo deployment/cadre
```

### Scaling

```bash
# Scale to 5 replicas
kubectl scale deployment cadre --replicas 5

# Autoscaling setup
kubectl autoscale deployment cadre --min=2 --max=10 \
  --cpu-percent=70

# Monitor scaling
kubectl get hpa -n cadre
kubectl describe hpa cadre
```

### Configuration Changes

```bash
# 1. Update configmap
kubectl edit configmap cadre-config

# 2. Restart deployment to apply
kubectl rollout restart deployment/cadre

# 3. Verify new config
kubectl exec deployment/cadre -- env | grep CONFIG
```

### Database Maintenance

```bash
# Connect to database
kubectl exec -it deployment/cadre -- psql -h db -U cadre

# Analyze query performance
EXPLAIN ANALYZE SELECT ...;

# Vacuum and analyze
VACUUM ANALYZE;

# Backup database
pg_dump -h db -U cadre > cadre-backup.sql
```

## Monitoring & Alerting

### Key Metrics to Monitor

1. **Availability:**
   - Health endpoint response (target: 100% OK)
   - Request success rate (target: > 99.9%)

2. **Performance:**
   - P50 latency (target: < 100ms)
   - P95 latency (target: < 500ms)
   - P99 latency (target: < 1000ms)

3. **Resource Usage:**
   - CPU utilization (target: < 70%)
   - Memory usage (target: < 80%)
   - Disk usage (target: < 80%)

4. **Errors:**
   - Error rate (target: < 0.1%)
   - Timeout errors (target: < 0.01%)

### Alert Rules

```yaml
# Critical Alerts
- cadre_unavailable: health endpoint returns 503
- error_rate_high: > 1% error rate
- latency_high_p99: P99 > 2000ms
- database_down: cannot connect
- disk_full: > 95% used

# Warning Alerts  
- cpu_high: > 80%
- memory_high: > 85%
- cache_hit_rate_low: < 70%
- connection_pool_exhausted: > 90% used
```

## Runbooks

### Runbook: Restart Failed Service

```bash
# 1. Assess current state
kubectl get pod <pod-name>
kubectl describe pod <pod-name>

# 2. Delete failed pod (causes restart)
kubectl delete pod <pod-name>

# 3. Monitor new pod
kubectl get pod <pod-name> -w

# 4. Verify health
kubectl exec <pod-name> -- curl localhost:8080/health

# 5. If still failing, escalate
```

### Runbook: Memory Leak

```bash
# 1. Detect memory leak
kubectl top pods -n cadre
# Memory usage continuously increasing

# 2. Gather heap profile
kubectl exec <pod> -- curl localhost:8080/debug/pprof/heap > heap.prof

# 3. Analyze with pprof
go tool pprof heap.prof
(pprof) top10
(pprof) list <function>

# 4. Restart pod
kubectl delete pod <pod-name>

# 5. File issue with findings
```

### Runbook: High Latency

```bash
# 1. Check metrics
curl http://cadre:8080/metrics | grep latency

# 2. Identify bottleneck
# - Agent pool? Check MAX_AGENTS
# - Cache? Check hit rate
# - Database? Run EXPLAIN ANALYZE

# 3. Scale if needed
kubectl scale deployment cadre --replicas 10

# 4. Monitor improvement
kubectl top pods -n cadre
curl http://cadre:8080/metrics

# 5. If not resolved, check database
```

## Escalation Procedures

### Level 1: Service Degradation
- On-call engineer addresses
- Target: 30-minute resolution
- Update status every 15 minutes

### Level 2: Major Outage
- On-call + Team Lead engaged
- Target: 1-hour resolution
- Page management if longer

### Level 3: Critical/Security
- On-call + Team Lead + Manager
- Incident commander assigned
- All hands standby mode

## Change Management

### Deployment Checklist

Before each deployment:

- [ ] Code reviewed and approved
- [ ] All tests passing
- [ ] Version number updated
- [ ] Release notes written
- [ ] Backup of current version
- [ ] Rollback plan documented
- [ ] Team notified of window
- [ ] Monitoring alerts active
- [ ] On-call engineer available

### Post-Deployment Checklist

After each deployment:

- [ ] All pods running
- [ ] Health checks passing
- [ ] Metrics look normal
- [ ] No error spike
- [ ] Team notified completion
- [ ] Deployment logged
- [ ] Metrics baseline updated

## Contact & Escalation

**On-Call Rotation:** See `/etc/cadre/oncall-schedule.txt`

**Escalation Path:**
1. On-call engineer → pagerduty
2. Team lead → Slack #cadre-incidents
3. Manager → Email management
4. VP/Director → Phone call

**Status Page:** https://status.example.com/cadre

## References

- [Deployment Guide](./DEPLOYMENT_GUIDE.md)
- [Troubleshooting Guide](./TROUBLESHOOTING_GUIDE.md)
- Configuration: `internal/production/config.go`
- Health checks: `internal/server/server.go`
