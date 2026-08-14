package orchestration

import (
	"testing"
	"time"
)

func TestNewAgentPool(t *testing.T) {
	pool := NewAgentPool(10)

	if pool == nil {
		t.Errorf("pool should not be nil")
	}
}

func TestRegisterAgent(t *testing.T) {
	pool := NewAgentPool(10)

	err := pool.RegisterAgent("agent-1")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	agent := pool.GetAgent("agent-1")
	if agent == nil {
		t.Fatalf("agent should exist")
	}

	if !agent.Available {
		t.Errorf("agent should be available")
	}
}

func TestRegisterAgentDuplicate(t *testing.T) {
	pool := NewAgentPool(10)

	pool.RegisterAgent("agent-1")
	err := pool.RegisterAgent("agent-1")

	if err == nil {
		t.Errorf("should error on duplicate registration")
	}
}

func TestRegisterAgentPoolFull(t *testing.T) {
	pool := NewAgentPool(1)

	pool.RegisterAgent("agent-1")
	err := pool.RegisterAgent("agent-2")

	if err == nil {
		t.Errorf("should error when pool is full")
	}
}

func TestAcquireAgent(t *testing.T) {
	pool := NewAgentPool(10)
	pool.RegisterAgent("agent-1")

	agentID, err := pool.AcquireAgent(1 * time.Second)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if agentID != "agent-1" {
		t.Errorf("expected agent-1, got %s", agentID)
	}

	agent := pool.GetAgent(agentID)
	if agent.Available {
		t.Errorf("agent should not be available after acquisition")
	}
}

func TestAcquireAgentTimeout(t *testing.T) {
	pool := NewAgentPool(10)
	pool.RegisterAgent("agent-1")

	pool.AcquireAgent(1 * time.Second)

	// Try to acquire when no agents available
	_, err := pool.AcquireAgent(100 * time.Millisecond)
	if err == nil {
		t.Errorf("should timeout when no agents available")
	}
}

func TestReleaseAgent(t *testing.T) {
	pool := NewAgentPool(10)
	pool.RegisterAgent("agent-1")

	agentID, _ := pool.AcquireAgent(1 * time.Second)

	err := pool.ReleaseAgent(agentID, 100*time.Millisecond, true)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	agent := pool.GetAgent(agentID)
	if !agent.Available {
		t.Errorf("agent should be available after release")
	}

	if agent.ExecutionCount != 1 {
		t.Errorf("expected execution count 1")
	}
}

func TestGetAvailableAgents(t *testing.T) {
	pool := NewAgentPool(10)
	pool.RegisterAgent("agent-1")
	pool.RegisterAgent("agent-2")
	pool.RegisterAgent("agent-3")

	available := pool.GetAvailableAgents()
	if available != 3 {
		t.Errorf("expected 3 available agents, got %d", available)
	}

	pool.AcquireAgent(1 * time.Second)

	available = pool.GetAvailableAgents()
	if available != 2 {
		t.Errorf("expected 2 available agents after acquire")
	}
}

func TestGetPoolStats(t *testing.T) {
	pool := NewAgentPool(10)
	pool.RegisterAgent("agent-1")

	stats := pool.GetPoolStats()

	if stats["total_agents"] != 1 {
		t.Errorf("expected 1 total agent")
	}

	if stats["available_agents"] != 1 {
		t.Errorf("expected 1 available agent")
	}
}

func TestLoadBalancerSelectAgent(t *testing.T) {
	pool := NewAgentPool(10)
	pool.RegisterAgent("agent-1")

	lb := NewLoadBalancer(pool)

	agentID, err := lb.SelectAgent(1 * time.Second)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if agentID != "agent-1" {
		t.Errorf("expected agent-1, got %s", agentID)
	}
}

func TestLoadBalancerSelectLeastLoaded(t *testing.T) {
	pool := NewAgentPool(10)
	pool.RegisterAgent("agent-1")
	pool.RegisterAgent("agent-2")

	// Simulate agent-1 executing more
	a1 := pool.GetAgent("agent-1")
	a1.ExecutionCount = 10

	lb := NewLoadBalancer(pool)
	selected := lb.SelectLeastLoaded()

	if selected != "agent-2" {
		t.Errorf("expected agent-2 (least loaded)")
	}
}

func TestLoadBalancerSelectFastest(t *testing.T) {
	pool := NewAgentPool(10)
	pool.RegisterAgent("agent-1")
	pool.RegisterAgent("agent-2")

	// Simulate agent-1 being slower
	a1 := pool.GetAgent("agent-1")
	a1.AverageLatency = 500 * time.Millisecond

	lb := NewLoadBalancer(pool)
	selected := lb.SelectFastest()

	if selected != "agent-2" {
		t.Errorf("expected agent-2 (fastest)")
	}
}

func TestReserveAgent(t *testing.T) {
	pool := NewAgentPool(10)
	pool.RegisterAgent("agent-1")

	until := time.Now().Add(1 * time.Hour)
	err := pool.ReserveAgent("agent-1", until)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	agent := pool.GetAgent("agent-1")
	if agent.ReservedUntil != until {
		t.Errorf("agent should be reserved")
	}
}

func TestGetHealthScore(t *testing.T) {
	pool := NewAgentPool(10)
	pool.RegisterAgent("agent-1")
	pool.RegisterAgent("agent-2")

	health := pool.GetHealthScore()

	if health < 0 || health > 1 {
		t.Errorf("health score should be between 0 and 1, got %f", health)
	}

	if health <= 0 {
		t.Errorf("new pool should have positive health, got %f", health)
	}
}
