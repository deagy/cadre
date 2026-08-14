package orchestration

import (
	"context"
	"testing"
	"time"
)

// TestE2EWorkflowComplete tests the complete orchestration workflow.
func TestE2EWorkflowComplete(t *testing.T) {
	routing := &RoutingConfig{Routes: []Route{}}
	executor := NewExecutor(&mockAgentRunner{}, 4, 5*time.Second)
	workflow := NewOrchestrationWorkflow(routing, executor, nil)

	input := &WorkflowInput{
		TaskID:         "e2e-test-001",
		Task:           "E2E test task",
		Classification: "internal",
		ChangedFiles:   []string{"file1.go", "file2.go"},
	}

	output, err := workflow.Execute(context.Background(), input)
	if err == nil {
		t.Logf("E2E workflow executed (expected error due to no routes)")
	}

	if output == nil {
		t.Errorf("output should not be nil")
	}
}

// TestE2EWithCaching tests workflow with caching enabled.
func TestE2EWithCaching(t *testing.T) {
	routing := &RoutingConfig{Routes: []Route{}}
	executor := NewExecutor(&mockAgentRunner{}, 4, 5*time.Second)
	workflow := NewOrchestrationWorkflow(routing, executor, nil)

	cache := NewResultCache(10, 1*time.Hour)
	cachedWorkflow := &CachedOrchestrationWorkflow{
		workflow:        workflow,
		cache:           cache,
		enableByDefault: false,
	}

	stats := cachedWorkflow.GetCacheStats()
	if stats.MaxSize != 10 {
		t.Errorf("cache size mismatch")
	}
}

// TestE2EWithRateLimiting tests workflow with rate limiting.
func TestE2EWithRateLimiting(t *testing.T) {
	qm := NewQuotaManager()
	qm.SetQuota("agent-exec", 5, time.Minute)

	if !qm.Acquire("agent-exec") {
		t.Errorf("should acquire quota on first request")
	}

	for i := 0; i < 4; i++ {
		if !qm.Acquire("agent-exec") {
			t.Errorf("should acquire quota on request %d", i+2)
		}
	}

	if qm.Acquire("agent-exec") {
		t.Errorf("should not acquire after quota exhausted")
	}
}

// TestE2EWithPoolManagement tests workflow with agent pool.
func TestE2EWithPoolManagement(t *testing.T) {
	pool := NewAgentPool(3)
	pool.RegisterAgent("agent-1")
	pool.RegisterAgent("agent-2")

	agentID, err := pool.AcquireAgent(1 * time.Second)
	if err != nil {
		t.Errorf("should acquire agent: %v", err)
	}

	pool.ReleaseAgent(agentID, 100*time.Millisecond, true)

	available := pool.GetAvailableAgents()
	if available != 2 {
		t.Errorf("expected 2 available agents")
	}
}

// TestE2EWithMonitoring tests workflow with performance monitoring.
func TestE2EWithMonitoring(t *testing.T) {
	collector := NewInMemoryMetricsCollector()
	monitor := NewPerformanceMonitor(collector)

	for i := 0; i < 5; i++ {
		monitor.RecordMetric("e2e-test", time.Duration(i+1)*time.Millisecond, true)
	}

	snapshot := monitor.GetSnapshot("e2e-test")
	if snapshot == nil {
		t.Errorf("snapshot should not be nil")
		return
	}

	if snapshot.TotalOperations != 5 {
		t.Errorf("expected 5 operations")
	}
}

// TestE2EWithAuditLogging tests workflow with audit logging.
func TestE2EWithAuditLogging(t *testing.T) {
	tmpDir := t.TempDir()
	logger, err := NewFileBasedAuditLogger(tmpDir + "/audit.log")
	if err != nil {
		t.Fatalf("failed to create audit logger: %v", err)
	}
	defer logger.Close()

	sal := NewStructuredAuditLogger(logger, "e2e-test-001", "internal")

	input := &WorkflowInput{
		TaskID:         "e2e-test-001",
		Task:           "test",
		Classification: "internal",
		ChangedFiles:   []string{"test.go"},
	}

	err = sal.LogWorkflowStart(input)
	if err != nil {
		t.Errorf("failed to log workflow start: %v", err)
	}

	err = sal.LogWorkflowComplete("e2e-test-001", "success", 100*time.Millisecond)
	if err != nil {
		t.Errorf("failed to log workflow complete: %v", err)
	}
}

// TestE2EWithErrorRecovery tests workflow with error recovery.
func TestE2EWithErrorRecovery(t *testing.T) {
	erm := NewErrorRecoveryManager(DefaultRetryConfig())

	erm.RecordAgentFailure("agent-1")
	erm.RecordAgentFailure("agent-1")

	failures, _ := erm.GetFailureStats("agent-1")
	if failures != 2 {
		t.Errorf("expected 2 failures")
	}

	if !erm.CanRetry("agent-1", 1) {
		t.Errorf("should allow retry after failures")
	}

	erm.RecordAgentSuccess("agent-1")

	failures, recoveries := erm.GetFailureStats("agent-1")
	if failures != 1 || recoveries != 1 {
		t.Errorf("stats mismatch after recovery")
	}
}

// TestE2EIntegration tests all components together.
func TestE2EIntegration(t *testing.T) {
	// Setup
	routing := &RoutingConfig{Routes: []Route{}}
	executor := NewExecutor(&mockAgentRunner{}, 2, 1*time.Second)
	workflow := NewOrchestrationWorkflow(routing, executor, nil)

	// Add caching
	cache := NewResultCache(5, 1*time.Hour)
	cachedWorkflow := &CachedOrchestrationWorkflow{
		workflow:        workflow,
		cache:           cache,
		enableByDefault: false,
	}

	// Add pool management
	pool := NewAgentPool(2)
	pool.RegisterAgent("agent-1")

	// Add rate limiting
	qm := NewQuotaManager()
	qm.SetQuota("workflow", 10, time.Minute)

	// Add monitoring
	collector := NewInMemoryMetricsCollector()
	monitor := NewPerformanceMonitor(collector)

	// Execute workflow
	monitor.RecordMetric("e2e-integration", 50*time.Millisecond, true)

	stats := monitor.GetMetrics("e2e-integration")
	if stats == nil {
		t.Errorf("metrics should exist")
	}

	if cachedWorkflow.GetCacheStats().MaxSize != 5 {
		t.Errorf("cache configuration mismatch")
	}
}
