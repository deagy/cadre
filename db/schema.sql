-- Cadre CLI Database Schema

-- Enable extensions
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- Audit logs table
CREATE TABLE IF NOT EXISTS audit_logs (
  id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  trace_id VARCHAR(32) NOT NULL,
  span_id VARCHAR(16) NOT NULL,
  event_type VARCHAR(100) NOT NULL,
  actor VARCHAR(255) NOT NULL,
  resource VARCHAR(255),
  action VARCHAR(100) NOT NULL,
  result VARCHAR(50),
  status_code INTEGER,
  duration_ms INTEGER,
  error_message TEXT,
  metadata JSONB DEFAULT '{}',
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  chain_hash VARCHAR(64),
  previous_hash VARCHAR(64)
);

CREATE INDEX audit_logs_trace_id ON audit_logs(trace_id);
CREATE INDEX audit_logs_created_at ON audit_logs(created_at DESC);
CREATE INDEX audit_logs_actor ON audit_logs(actor);
CREATE INDEX audit_logs_event_type ON audit_logs(event_type);

-- Agent execution log
CREATE TABLE IF NOT EXISTS agent_executions (
  id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  task_id VARCHAR(100) NOT NULL,
  agent_id VARCHAR(100) NOT NULL,
  agent_role VARCHAR(100) NOT NULL,
  status VARCHAR(50) NOT NULL,
  start_time TIMESTAMP NOT NULL,
  end_time TIMESTAMP,
  duration_ms INTEGER,
  findings_count INTEGER DEFAULT 0,
  errors_count INTEGER DEFAULT 0,
  output_summary TEXT,
  quality_score DECIMAL(3, 2),
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  CONSTRAINT fk_task FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE
);

CREATE INDEX agent_executions_task_id ON agent_executions(task_id);
CREATE INDEX agent_executions_status ON agent_executions(status);
CREATE INDEX agent_executions_created_at ON agent_executions(created_at DESC);

-- Tasks table
CREATE TABLE IF NOT EXISTS tasks (
  id VARCHAR(100) PRIMARY KEY,
  description TEXT,
  classification VARCHAR(50),
  status VARCHAR(50) NOT NULL DEFAULT 'pending',
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  completed_at TIMESTAMP,
  workflow_id VARCHAR(100),
  result_summary TEXT,
  result_quality_score DECIMAL(3, 2)
);

CREATE INDEX tasks_status ON tasks(status);
CREATE INDEX tasks_classification ON tasks(classification);
CREATE INDEX tasks_created_at ON tasks(created_at DESC);

-- Cache table
CREATE TABLE IF NOT EXISTS cache_entries (
  key VARCHAR(255) PRIMARY KEY,
  value BYTEA NOT NULL,
  ttl_seconds INTEGER,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at TIMESTAMP NOT NULL,
  hit_count INTEGER DEFAULT 0,
  last_accessed TIMESTAMP
);

CREATE INDEX cache_expires_at ON cache_entries(expires_at);
CREATE INDEX cache_hit_count ON cache_entries(hit_count DESC);

-- Rate limit tracking
CREATE TABLE IF NOT EXISTS rate_limits (
  id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  client_id VARCHAR(100) NOT NULL,
  operation VARCHAR(100) NOT NULL,
  request_count INTEGER NOT NULL DEFAULT 0,
  window_start TIMESTAMP NOT NULL,
  window_end TIMESTAMP NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX rate_limits_client_id ON rate_limits(client_id);
CREATE INDEX rate_limits_window ON rate_limits(window_start, window_end);

-- Performance metrics
CREATE TABLE IF NOT EXISTS performance_metrics (
  id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  operation VARCHAR(100) NOT NULL,
  latency_ms INTEGER NOT NULL,
  status VARCHAR(50),
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX perf_metrics_operation ON performance_metrics(operation);
CREATE INDEX perf_metrics_created_at ON performance_metrics(created_at DESC);

-- Secrets store (encrypted)
CREATE TABLE IF NOT EXISTS secrets (
  id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  name VARCHAR(255) NOT NULL UNIQUE,
  encrypted_value BYTEA NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  rotated_at TIMESTAMP,
  accessed_at TIMESTAMP
);

CREATE INDEX secrets_name ON secrets(name);

-- Authorization/RBAC (for future Phase 13A)
CREATE TABLE IF NOT EXISTS roles (
  id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  name VARCHAR(100) NOT NULL UNIQUE,
  description TEXT,
  permissions TEXT[] DEFAULT ARRAY[]::TEXT[],
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS user_roles (
  id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  user_id VARCHAR(100) NOT NULL,
  role_id UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX user_roles_user_id ON user_roles(user_id);

-- Health check history
CREATE TABLE IF NOT EXISTS health_checks (
  id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  component VARCHAR(100) NOT NULL,
  status VARCHAR(50) NOT NULL,
  message TEXT,
  duration_ms INTEGER,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX health_checks_component ON health_checks(component);
CREATE INDEX health_checks_created_at ON health_checks(created_at DESC);

-- Maintenance views
CREATE OR REPLACE VIEW audit_summary AS
SELECT
  DATE(created_at) as date,
  event_type,
  COUNT(*) as count,
  COUNT(CASE WHEN error_message IS NOT NULL THEN 1 END) as error_count
FROM audit_logs
GROUP BY DATE(created_at), event_type
ORDER BY date DESC, count DESC;

CREATE OR REPLACE VIEW performance_summary AS
SELECT
  operation,
  COUNT(*) as request_count,
  AVG(latency_ms) as avg_latency,
  MIN(latency_ms) as min_latency,
  MAX(latency_ms) as max_latency,
  PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY latency_ms) as p50,
  PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY latency_ms) as p95,
  PERCENTILE_CONT(0.99) WITHIN GROUP (ORDER BY latency_ms) as p99
FROM performance_metrics
WHERE created_at > NOW() - INTERVAL '24 hours'
GROUP BY operation;
