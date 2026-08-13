package orchestration

import (
	"fmt"
	"sync"
	"time"
)

// PooledAgent represents an agent in the pool with its state.
type PooledAgent struct {
	ID             string
	Available      bool
	LastUsedAt     time.Time
	ExecutionCount int64
	FailureCount   int64
	AverageLatency time.Duration
	ReservedUntil  time.Time
}

// AgentPool manages a pool of available agents.
type AgentPool struct {
	mu          sync.RWMutex
	agents      map[string]*PooledAgent
	available   chan string
	maxPoolSize int
}

// NewAgentPool creates a new agent pool.
func NewAgentPool(maxSize int) *AgentPool {
	return &AgentPool{
		agents:      make(map[string]*PooledAgent),
		available:   make(chan string, maxSize),
		maxPoolSize: maxSize,
	}
}

// RegisterAgent registers an agent in the pool.
func (ap *AgentPool) RegisterAgent(agentID string) error {
	ap.mu.Lock()
	defer ap.mu.Unlock()

	if len(ap.agents) >= ap.maxPoolSize {
		return fmt.Errorf("pool at capacity: %d/%d", len(ap.agents), ap.maxPoolSize)
	}

	if _, exists := ap.agents[agentID]; exists {
		return fmt.Errorf("agent already registered: %s", agentID)
	}

	ap.agents[agentID] = &PooledAgent{
		ID:        agentID,
		Available: true,
	}

	ap.available <- agentID

	return nil
}

// AcquireAgent acquires an available agent from the pool.
func (ap *AgentPool) AcquireAgent(timeout time.Duration) (string, error) {
	select {
	case agentID := <-ap.available:
		ap.mu.Lock()
		defer ap.mu.Unlock()

		agent, exists := ap.agents[agentID]
		if !exists {
			return "", fmt.Errorf("agent not found: %s", agentID)
		}

		agent.Available = false
		return agentID, nil

	case <-time.After(timeout):
		return "", fmt.Errorf("timeout waiting for available agent")
	}
}

// ReleaseAgent releases an agent back to the pool.
func (ap *AgentPool) ReleaseAgent(agentID string, duration time.Duration, success bool) error {
	ap.mu.Lock()
	defer ap.mu.Unlock()

	agent, exists := ap.agents[agentID]
	if !exists {
		return fmt.Errorf("agent not found: %s", agentID)
	}

	agent.Available = true
	agent.LastUsedAt = time.Now()
	agent.ExecutionCount++

	if !success {
		agent.FailureCount++
	}

	// Update average latency
	if agent.AverageLatency == 0 {
		agent.AverageLatency = duration
	} else {
		agent.AverageLatency = (agent.AverageLatency + duration) / 2
	}

	ap.available <- agentID

	return nil
}

// ReserveAgent reserves an agent until a specific time.
func (ap *AgentPool) ReserveAgent(agentID string, until time.Time) error {
	ap.mu.Lock()
	defer ap.mu.Unlock()

	agent, exists := ap.agents[agentID]
	if !exists {
		return fmt.Errorf("agent not found: %s", agentID)
	}

	agent.ReservedUntil = until
	agent.Available = false

	return nil
}

// GetAgent returns agent information.
func (ap *AgentPool) GetAgent(agentID string) *PooledAgent {
	ap.mu.RLock()
	defer ap.mu.RUnlock()

	return ap.agents[agentID]
}

// GetAvailableAgents returns the count of available agents.
func (ap *AgentPool) GetAvailableAgents() int {
	ap.mu.RLock()
	defer ap.mu.RUnlock()

	count := 0
	for _, agent := range ap.agents {
		if agent.Available && (agent.ReservedUntil.IsZero() || time.Now().Before(agent.ReservedUntil)) {
			count++
		}
	}

	return count
}

// GetPoolStats returns statistics about the pool.
func (ap *AgentPool) GetPoolStats() map[string]interface{} {
	ap.mu.RLock()
	defer ap.mu.RUnlock()

	totalAgents := len(ap.agents)
	availableAgents := 0
	totalExecutions := int64(0)
	totalFailures := int64(0)

	for _, agent := range ap.agents {
		if agent.Available {
			availableAgents++
		}
		totalExecutions += agent.ExecutionCount
		totalFailures += agent.FailureCount
	}

	successRate := 0.0
	if totalExecutions > 0 {
		successRate = float64(totalExecutions-totalFailures) / float64(totalExecutions)
	}

	return map[string]interface{}{
		"total_agents":     totalAgents,
		"available_agents": availableAgents,
		"busy_agents":      totalAgents - availableAgents,
		"total_executions": totalExecutions,
		"total_failures":   totalFailures,
		"success_rate":     successRate,
	}
}

// LoadBalancer distributes agents based on their load.
type LoadBalancer struct {
	pool *AgentPool
}

// NewLoadBalancer creates a new load balancer.
func NewLoadBalancer(pool *AgentPool) *LoadBalancer {
	return &LoadBalancer{
		pool: pool,
	}
}

// SelectAgent selects the least-loaded available agent.
func (lb *LoadBalancer) SelectAgent(timeout time.Duration) (string, error) {
	agentID, err := lb.pool.AcquireAgent(timeout)
	if err != nil {
		return "", err
	}

	return agentID, nil
}

// SelectLeastLoaded selects the agent with fewest executions.
func (lb *LoadBalancer) SelectLeastLoaded() string {
	lb.pool.mu.RLock()
	defer lb.pool.mu.RUnlock()

	var selected *PooledAgent
	minExecutions := int64(-1)

	for _, agent := range lb.pool.agents {
		if agent.Available && minExecutions == -1 || agent.ExecutionCount < minExecutions {
			selected = agent
			minExecutions = agent.ExecutionCount
		}
	}

	if selected == nil {
		return ""
	}

	return selected.ID
}

// SelectFastest selects the agent with lowest average latency.
func (lb *LoadBalancer) SelectFastest() string {
	lb.pool.mu.RLock()
	defer lb.pool.mu.RUnlock()

	var selected *PooledAgent

	for _, agent := range lb.pool.agents {
		if agent.Available && (selected == nil || agent.AverageLatency < selected.AverageLatency) {
			selected = agent
		}
	}

	if selected == nil {
		return ""
	}

	return selected.ID
}

// PoolStats provides statistics about pool health.
type PoolStats struct {
	TotalAgents     int
	AvailableAgents int
	BusyAgents      int
	Utilization     float64
	HealthScore     float64
}

// GetHealthScore returns pool health (0-1).
func (ap *AgentPool) GetHealthScore() float64 {
	stats := ap.GetPoolStats()

	available := stats["available_agents"].(int)
	total := stats["total_agents"].(int)
	successRate := stats["success_rate"].(float64)

	if total == 0 {
		return 1.0
	}

	utilization := float64(total-available) / float64(total)
	health := (successRate * 0.7) + ((1.0 - utilization) * 0.3)

	if health < 0 {
		health = 0
	}
	if health > 1 {
		health = 1
	}

	return health
}
