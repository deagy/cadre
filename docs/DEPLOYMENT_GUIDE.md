# Deployment Guide

Complete step-by-step guide for deploying Cadre CLI to production.

## Pre-Deployment Checklist

- [ ] All tests passing (`go test ./...`)
- [ ] Code reviewed and approved
- [ ] Version number updated in config
- [ ] Release notes prepared
- [ ] Backup of current version taken
- [ ] Deployment plan reviewed with team
- [ ] Rollback procedure documented
- [ ] Monitoring alerts configured

## Docker Deployment

### Building the Docker Image

```bash
# Build the CLI
go build -o bin/cadre ./cmd/cadre

# Build Docker image
docker build -t cadre:1.0.0 .
docker tag cadre:1.0.0 cadre:latest

# Push to registry
docker push registry.example.com/cadre:1.0.0
```

### Running in Docker

```bash
docker run -d \
  --name cadre \
  -e ENVIRONMENT=production \
  -e LOG_FORMAT=json \
  -e LOG_LEVEL=info \
  -e PORT=8080 \
  -p 8080:8080 \
  -v /var/log/cadre:/var/log/cadre \
  registry.example.com/cadre:1.0.0
```

### Health Checks

```bash
# Check liveness
curl http://localhost:8080/health

# Check readiness
curl http://localhost:8080/ready

# Check metrics
curl http://localhost:8080/metrics
```

## Kubernetes Deployment

### Prerequisites

- Kubernetes cluster (1.19+)
- kubectl configured
- Container registry access
- Persistent volume (if needed)

### Deploy to Kubernetes

```bash
# Create namespace
kubectl create namespace cadre

# Apply deployment manifest
kubectl apply -f k8s-deployment.yaml -n cadre

# Verify deployment
kubectl get deployments -n cadre
kubectl get pods -n cadre

# Check logs
kubectl logs -f deployment/cadre -n cadre

# Check health
kubectl port-forward svc/cadre 8080:80 -n cadre
curl http://localhost:8080/health
```

### Scaling

```bash
# Scale to 5 replicas
kubectl scale deployment cadre --replicas 5 -n cadre

# Check pod distribution
kubectl get pods -n cadre -o wide
```

### Rolling Update

```bash
# Update image
kubectl set image deployment/cadre cadre=registry.example.com/cadre:1.1.0 -n cadre

# Monitor rollout
kubectl rollout status deployment/cadre -n cadre

# Rollback if needed
kubectl rollout undo deployment/cadre -n cadre
```

## Configuration Management

### Environment Variables

All configuration via environment variables (see `internal/production/config.go`):

```bash
export PORT=8080
export HOST=0.0.0.0
export ENVIRONMENT=production
export LOG_LEVEL=info
export LOG_FORMAT=json
export LOG_OUTPUT=/var/log/cadre/cadre.log
export CACHE_ENABLED=true
export CACHE_SIZE=1000
export RATE_LIMIT_ENABLED=true
export RATE_LIMIT_RPS=100
export MAX_AGENTS=10
export AGENT_TIMEOUT=5m
export VERSION=1.0.0
export TRACING_ENABLED=true
```

### Configuration File

Generate configuration file:

```bash
./bin/cadre config generate --output /etc/cadre/config.toml
```

### Hot Configuration Reload

Not currently supported - configuration is loaded at startup. Restart required for config changes:

```bash
# For rolling deployment
kubectl rollout restart deployment/cadre -n cadre

# For Docker
docker restart cadre
```

## Monitoring Setup

### Prometheus Scrape Config

```yaml
global:
  scrape_interval: 15s

scrape_configs:
  - job_name: 'cadre'
    static_configs:
      - targets: ['localhost:8080']
    metrics_path: '/metrics'
```

### Kubernetes Service Monitor

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
```

### Alerting Rules

```yaml
groups:
- name: cadre
  interval: 30s
  rules:
  - alert: CadreUnhealthy
    expr: cadre_health_status == 0
    for: 2m
    annotations:
      summary: "Cadre service is unhealthy"

  - alert: CadreHighErrorRate
    expr: rate(errors_total[5m]) > 0.05
    annotations:
      summary: "High error rate detected"

  - alert: CadreHighLatency
    expr: histogram_quantile(0.95, rate(http_request_duration_ms_bucket[5m])) > 1000
    annotations:
      summary: "P95 latency exceeding 1 second"
```

## Logging Setup

### Log Rotation

Logs are automatically rotated when they exceed 100MB:

```bash
ls -lh /var/log/cadre/
# -rw------- cadre cadre 100M cadre.log
# -rw------- cadre cadre  50M cadre.log.1
# -rw------- cadre cadre  30M cadre.log.2
```

### Log Aggregation (ELK Stack)

```yaml
# filebeat.yml
filebeat.inputs:
- type: log
  enabled: true
  paths:
    - /var/log/cadre/*.log
  
output.elasticsearch:
  hosts: ["elasticsearch:9200"]

processors:
  - add_docker_metadata: ~
  - add_kubernetes_metadata: ~
```

## Backup & Recovery

### Backup Audit Logs

```bash
# Daily backup
0 2 * * * tar -czf /backup/cadre-audit-$(date +%Y%m%d).tar.gz /var/log/cadre/audit.log
```

### Disaster Recovery

```bash
# Restore from backup
tar -xzf /backup/cadre-audit-20250813.tar.gz -C /

# Verify integrity
./bin/cadre audit verify --logfile /var/log/cadre/audit.log
```

## Security Hardening

### SSL/TLS Configuration

```bash
export HTTPS_ENABLED=true
export TLS_CERT_FILE=/etc/cadre/certs/tls.crt
export TLS_KEY_FILE=/etc/cadre/certs/tls.key
```

### API Key Rotation

```bash
# Generate new API key
./bin/cadre config rotate-api-key

# Old key continues working for 24 hours (grace period)
# Update clients before grace period expires
```

### Network Policies (Kubernetes)

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: cadre-network-policy
spec:
  podSelector:
    matchLabels:
      app: cadre
  policyTypes:
  - Ingress
  - Egress
  ingress:
  - from:
    - namespaceSelector:
        matchLabels:
          name: ingress
    ports:
    - protocol: TCP
      port: 8080
  egress:
  - to:
    - namespaceSelector:
        matchLabels:
          name: default
    ports:
    - protocol: TCP
      port: 443
```

## Troubleshooting Deployment

See [TROUBLESHOOTING_GUIDE.md](./TROUBLESHOOTING_GUIDE.md) for common issues and solutions.

## Performance Tuning

### Agent Pool Tuning

```bash
export MAX_AGENTS=20          # Increase for CPU-bound workloads
export AGENT_TIMEOUT=10m      # Increase for long operations
```

### Cache Tuning

```bash
export CACHE_SIZE=5000        # Increase for higher hit rates
export CACHE_TTL=30m          # Adjust based on data freshness needs
```

### Rate Limiting

```bash
export RATE_LIMIT_RPS=500     # Increase for higher throughput
export QUOTA_WINDOW=5m        # Adjust quota window
```

## Maintenance Windows

### Planned Maintenance

```bash
# Drain connections and shutdown gracefully
kubectl drain node-1 --ignore-daemonsets
kubectl wait --for=condition=Ready pod/cadre-new
kubectl uncordon node-1
```

### Emergency Shutdown

```bash
# Force kill if graceful shutdown hangs
docker kill -s KILL cadre

# or in Kubernetes
kubectl delete pod cadre-xxxxx --grace-period=0 --force
```

## Support & Escalation

For issues during deployment:

1. Check logs: `kubectl logs deployment/cadre`
2. Review troubleshooting guide: [TROUBLESHOOTING_GUIDE.md](./TROUBLESHOOTING_GUIDE.md)
3. Check system status: `curl http://localhost:8080/status`
4. Contact on-call engineer with logs attached
