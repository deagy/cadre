<!-- GENERATED FILE: edit the canonical source and regenerate; do not edit this copy. -->

# Troubleshooting Guide

Common issues and solutions for production Cadre CLI deployments.

## Health & Readiness Issues

### Problem: `/health` returns 503

**Symptoms:** 
- Kubernetes liveness probe failing
- Service marked as unhealthy

**Diagnosis:**
```bash
curl -v http://localhost:8080/health
# Response shows unhealthy components
```

**Solutions:**
1. Check component status:
   ```bash
   kubectl logs deployment/cadre | grep "unhealthy"
   ```

2. Verify database connectivity:
   ```bash
   ./bin/cadre health check --component database
   ```

3. Check cache health:
   ```bash
   ./bin/cadre health check --component cache
   ```

4. Restart deployment:
   ```bash
   kubectl rollout restart deployment/cadre
   ```

### Problem: `/ready` returns 503

**Symptoms:**
- Traffic not being routed to pod
- Deployment hangs during rolling update

**Solutions:**
1. Check readiness conditions:
   ```bash
   kubectl get pod <pod-name> -o jsonpath='{.status.conditions}'
   ```

2. Wait for initialization:
   - Readiness checks run after liveness passes
   - Allow 30 seconds for initialization

3. Verify migrations completed:
   ```bash
   kubectl logs deployment/cadre | grep "migration"
   ```

4. Increase initial delay if needed:
   ```yaml
   readinessProbe:
     initialDelaySeconds: 30  # Increase from 5s
   ```

## Performance Issues

### Problem: High Latency (P95 > 1s)

**Diagnosis:**
```bash
curl http://localhost:8080/metrics | grep http_request_duration
# Check histogram buckets
```

**Solutions:**
1. **Agent pool saturation:**
   ```bash
   # Increase agent capacity
   export MAX_AGENTS=20
   kubectl set env deployment/cadre MAX_AGENTS=20
   ```

2. **Cache misses:**
   ```bash
   # Monitor cache hit rate
   curl http://localhost:8080/metrics | grep cache_hits
   
   # Increase cache size if hit rate < 70%
   export CACHE_SIZE=5000
   ```

3. **Rate limiting:**
   ```bash
   # Check if requests are being throttled
   curl http://localhost:8080/metrics | grep rate_limit
   
   # Increase if needed
   export RATE_LIMIT_RPS=200
   ```

4. **Database performance:**
   ```bash
   kubectl exec -it <pod> -- psql -c "EXPLAIN ANALYZE SELECT ..."
   ```

### Problem: Memory Leak

**Diagnosis:**
```bash
# Monitor memory usage
kubectl top pod cadre-xxxxx

# Check heap profile
curl http://localhost:8080/debug/pprof/heap > heap.prof
go tool pprof heap.prof
```

**Solutions:**
1. Look for goroutine leaks:
   ```bash
   curl http://localhost:8080/debug/pprof/goroutine > goroutine.prof
   go tool pprof goroutine.prof
   ```

2. Restart pod if memory exceeds limit:
   ```bash
   kubectl set resources deployment/cadre --limits=memory=512Mi
   ```

3. Check for unclosed connections:
   - Review connection draining in shutdown handler
   - Verify all database connections closed

## Error Issues

### Problem: High Error Rate

**Diagnosis:**
```bash
curl http://localhost:8080/metrics | grep errors_total
kubectl logs deployment/cadre | grep ERROR
```

**Solutions:**
1. **Check logs for error pattern:**
   ```bash
   kubectl logs deployment/cadre -f | grep -i error
   ```

2. **Common errors:**
   - `connection refused` → Check database/cache connectivity
   - `timeout` → Increase AGENT_TIMEOUT or database timeouts
   - `permission denied` → Check API keys and credentials

3. **Enable debug logging:**
   ```bash
   export LOG_LEVEL=debug
   kubectl set env deployment/cadre LOG_LEVEL=debug
   ```

### Problem: Python Subprocess Failures

**Symptoms:**
- `agent-dispatch` spans fail
- Errors referencing Python stack traces

**Solutions:**
1. Check Python environment:
   ```bash
   docker exec <container> python3 --version
   ```

2. Verify Python script paths:
   ```bash
   docker exec <container> ls -la /app/bin/cadre.py
   ```

3. Check subprocess logs:
   ```bash
   kubectl logs deployment/cadre | grep "subprocess\|python"
   ```

4. Increase subprocess timeout:
   ```bash
   export AGENT_TIMEOUT=10m
   ```

## Deployment Issues

### Problem: CrashLoopBackOff

**Diagnosis:**
```bash
kubectl describe pod <pod-name>
kubectl logs <pod-name>
```

**Solutions:**
1. **Check configuration:**
   ```bash
   kubectl get configmap cadre-config -o yaml
   ```

2. **Verify image exists:**
   ```bash
   docker pull registry.example.com/cadre:latest
   ```

3. **Check resource limits:**
   ```bash
   kubectl top node
   kubectl describe node <node-name>
   ```

4. **Check security context:**
   ```bash
   kubectl get pod <pod-name> -o jsonpath='{.spec.securityContext}'
   ```

### Problem: Slow Rolling Update

**Symptoms:**
- Rolling update hangs
- Old pods not terminating

**Solutions:**
1. Check termination grace period:
   ```yaml
   terminationGracePeriodSeconds: 60
   ```

2. Monitor pod drain:
   ```bash
   kubectl get pods -w
   ```

3. Force pod eviction:
   ```bash
   kubectl delete pod <pod-name> --grace-period=10
   ```

### Problem: ImagePullBackOff

**Diagnosis:**
```bash
kubectl describe pod <pod-name>
```

**Solutions:**
1. Verify registry credentials:
   ```bash
   kubectl get secrets
   kubectl create secret docker-registry regcred \
     --docker-server=registry.example.com \
     --docker-username=<user> \
     --docker-password=<password>
   ```

2. Check image availability:
   ```bash
   docker pull registry.example.com/cadre:latest
   ```

3. Update image pull policy:
   ```yaml
   imagePullPolicy: IfNotPresent
   ```

## Networking Issues

### Problem: Cannot Connect to Service

**Diagnosis:**
```bash
kubectl exec -it <pod> -- curl http://localhost:8080/health
kubectl exec -it <pod> -- nc -zv cadre 8080
```

**Solutions:**
1. Check service endpoints:
   ```bash
   kubectl get endpoints cadre
   ```

2. Check network policies:
   ```bash
   kubectl get networkpolicies
   kubectl describe networkpolicy cadre-network-policy
   ```

3. Verify DNS:
   ```bash
   kubectl exec -it <pod> -- nslookup cadre.default.svc.cluster.local
   ```

4. Check firewall rules:
   ```bash
   gcloud compute firewall-rules list
   ```

## Database Issues

### Problem: Database Connection Timeout

**Symptoms:**
- Database health check failing
- Long request latencies

**Solutions:**
1. Verify database is accessible:
   ```bash
   kubectl exec -it <pod> -- psql -h db.example.com -U cadre -c "SELECT 1"
   ```

2. Check connection pool:
   ```bash
   export MAX_AGENTS=20  # Reduce concurrent connections
   ```

3. Increase timeout:
   ```bash
   export AGENT_TIMEOUT=10m
   ```

## Security Issues

### Problem: Unauthorized API Access

**Diagnosis:**
```bash
curl -H "Authorization: Bearer <invalid-key>" http://localhost:8080/health
# Returns 401
```

**Solutions:**
1. Verify API key:
   ```bash
   echo $API_KEY
   ```

2. Rotate API key:
   ```bash
   ./bin/cadre config rotate-api-key
   ```

3. Check RBAC permissions:
   ```bash
   kubectl auth can-i get pods --as=system:serviceaccount:cadre:cadre
   ```

### Problem: Certificate Errors

**Symptoms:**
- TLS handshake failures
- x509 certificate errors

**Solutions:**
1. Check certificate validity:
   ```bash
   openssl x509 -in /etc/cadre/certs/tls.crt -noout -text
   ```

2. Verify key/cert match:
   ```bash
   openssl x509 -in tls.crt -noout -modulus | openssl md5
   openssl rsa -in tls.key -noout -modulus | openssl md5
   # Should match
   ```

3. Check certificate expiry:
   ```bash
   kubectl get secret cadre-tls -o jsonpath='{.data.tls\.crt}' | base64 -d | openssl x509 -noout -enddate
   ```

## Escalation

If issues persist:

1. **Gather diagnostics:**
   ```bash
   kubectl logs deployment/cadre --all-containers=true > logs.txt
   kubectl describe deployment cadre > deployment.txt
   kubectl get events -n cadre > events.txt
   ```

2. **Contact on-call:**
   - Include above diagnostics
   - Provide time when issue started
   - Include any recent deployments/changes

3. **Rollback if necessary:**
   ```bash
   kubectl rollout undo deployment/cadre
   ```
