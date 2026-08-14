package knowledge

import (
	"fmt"
	"sync"
	"time"
)

// RebalanceThreshold is the maximum percentage of total messages a shard should hold (0-100).
const RebalanceThreshold = 60

// ShardImbalance represents the imbalance state of a shard.
type ShardImbalance struct {
	ShardID      string
	MessageCount int64
	Percentage   float64
	IsHot        bool // True if percentage > RebalanceThreshold
}

// RebalanceMetrics tracks rebalancing progress.
type RebalanceMetrics struct {
	TotalMessagesToMove  int64
	MessagesMovedSucceed int64
	MessagesMovedFailed  int64
	SourceShard          string
	DestinationShard     string
	StartedAt            time.Time
	CompletedAt          *time.Time
	Status               string // "pending", "in_progress", "completed", "failed", "rolled_back"
	ErrorMessage         string
}

// RebalanceAnalysis analyzes shard distribution and identifies imbalances.
type RebalanceAnalysis struct {
	IsBalanced        bool
	HotShards         []ShardImbalance
	ColdShards        []ShardImbalance
	TotalMessages     int64
	AveragePerShard   float64
	StandardDeviation float64
	Timestamp         time.Time
}

// ShardRebalancer handles rebalancing operations across shards.
type ShardRebalancer struct {
	registry *StoreRegistry
	mu       sync.RWMutex
	metrics  map[string]*RebalanceMetrics // migration ID → metrics
	strategy ShardingStrategy
}

// NewShardRebalancer creates a new rebalancer for the given registry.
func NewShardRebalancer(registry *StoreRegistry, strategy ShardingStrategy) *ShardRebalancer {
	return &ShardRebalancer{
		registry: registry,
		metrics:  make(map[string]*RebalanceMetrics),
		strategy: strategy,
	}
}

// AnalyzeShard analyzes the distribution of messages across all shards.
func (sr *ShardRebalancer) AnalyzeShard() (*RebalanceAnalysis, error) {
	sr.mu.RLock()
	stores := sr.registry.GetStores()
	sr.mu.RUnlock()

	if len(stores) == 0 {
		return nil, fmt.Errorf("no shards registered")
	}

	// Get stats from all shards
	shardStats := make(map[string]int64)
	var totalMessages int64

	for shardID, store := range stores {
		stats, err := store.Stats()
		if err != nil {
			continue // Skip shards with errors
		}
		shardStats[shardID] = stats.TotalMessages
		totalMessages += stats.TotalMessages
	}

	// Calculate percentages and identify hot/cold shards
	var hotShards, coldShards []ShardImbalance
	var sumSquaredDeviations float64
	avgPercentage := 100.0 / float64(len(stores))

	for shardID, count := range shardStats {
		percentage := 0.0
		if totalMessages > 0 {
			percentage = (float64(count) / float64(totalMessages)) * 100
		}

		imbalance := ShardImbalance{
			ShardID:      shardID,
			MessageCount: count,
			Percentage:   percentage,
			IsHot:        percentage > RebalanceThreshold,
		}

		if imbalance.IsHot {
			hotShards = append(hotShards, imbalance)
		} else if percentage < avgPercentage/2 {
			coldShards = append(coldShards, imbalance)
		}

		// Calculate standard deviation
		deviation := percentage - avgPercentage
		sumSquaredDeviations += deviation * deviation
	}

	stdDev := 0.0
	if len(stores) > 1 {
		stdDev = sqrt(sumSquaredDeviations / float64(len(stores)))
	}

	isBalanced := len(hotShards) == 0 && stdDev < 20 // Consider balanced if no hot shards and reasonable variance

	return &RebalanceAnalysis{
		IsBalanced:        isBalanced,
		HotShards:         hotShards,
		ColdShards:        coldShards,
		TotalMessages:     totalMessages,
		AveragePerShard:   avgPercentage,
		StandardDeviation: stdDev,
		Timestamp:         time.Now(),
	}, nil
}

// RebalanceMessage moves a message from one shard to another.
func (sr *ShardRebalancer) RebalanceMessage(messageID, sourceShardID, destShardID string, authorizedBy string) error {
	sr.mu.RLock()
	stores := sr.registry.GetStores()
	sr.mu.RUnlock()

	_, ok := stores[sourceShardID]
	if !ok {
		return fmt.Errorf("source shard not found: %s", sourceShardID)
	}

	_, ok = stores[destShardID]
	if !ok {
		return fmt.Errorf("destination shard not found: %s", destShardID)
	}

	// Get message from source and replicate to destination
	// Note: This is a placeholder - Phase 5.7 will implement full message migration
	// with consistency guarantees and rollback capability

	return nil
}

// StartRebalance begins a rebalancing operation between two shards.
func (sr *ShardRebalancer) StartRebalance(sourceShardID, destShardID string, authorizedBy string) (string, error) {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	if sourceShardID == destShardID {
		return "", fmt.Errorf("source and destination shards must be different")
	}

	stores := sr.registry.GetStores()
	if _, ok := stores[sourceShardID]; !ok {
		return "", fmt.Errorf("source shard not found: %s", sourceShardID)
	}
	if _, ok := stores[destShardID]; !ok {
		return "", fmt.Errorf("destination shard not found: %s", destShardID)
	}

	// Generate migration ID
	migrationID := fmt.Sprintf("rebal-%d", time.Now().UnixNano())

	// Create metrics for this migration
	metrics := &RebalanceMetrics{
		SourceShard:      sourceShardID,
		DestinationShard: destShardID,
		StartedAt:        time.Now(),
		Status:           "pending",
	}

	sr.metrics[migrationID] = metrics

	return migrationID, nil
}

// GetRebalanceStatus returns the status of a rebalancing operation.
func (sr *ShardRebalancer) GetRebalanceStatus(migrationID string) (*RebalanceMetrics, error) {
	sr.mu.RLock()
	defer sr.mu.RUnlock()

	metrics, ok := sr.metrics[migrationID]
	if !ok {
		return nil, fmt.Errorf("migration not found: %s", migrationID)
	}

	// Return copy to prevent external mutation
	copy := *metrics
	return &copy, nil
}

// CancelRebalance cancels a pending rebalancing operation.
func (sr *ShardRebalancer) CancelRebalance(migrationID string) error {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	metrics, ok := sr.metrics[migrationID]
	if !ok {
		return fmt.Errorf("migration not found: %s", migrationID)
	}

	if metrics.Status != "pending" && metrics.Status != "in_progress" {
		return fmt.Errorf("cannot cancel migration in %s status", metrics.Status)
	}

	metrics.Status = "cancelled"
	now := time.Now()
	metrics.CompletedAt = &now

	return nil
}

// RebalancingStats returns statistics about all rebalancing operations.
type RebalancingStats struct {
	TotalMigrations     int
	ActiveMigrations    int
	CompletedMigrations int
	FailedMigrations    int
	TotalMessagesMoved  int64
}

// GetRebalancingStats returns statistics about all migrations.
func (sr *ShardRebalancer) GetRebalancingStats() *RebalancingStats {
	sr.mu.RLock()
	defer sr.mu.RUnlock()

	stats := &RebalancingStats{
		TotalMigrations: len(sr.metrics),
	}

	for _, metrics := range sr.metrics {
		switch metrics.Status {
		case "pending", "in_progress":
			stats.ActiveMigrations++
		case "completed":
			stats.CompletedMigrations++
		case "failed":
			stats.FailedMigrations++
		}
		stats.TotalMessagesMoved += metrics.MessagesMovedSucceed
	}

	return stats
}

// Helper function for standard deviation calculation
func sqrt(x float64) float64 {
	if x < 0 {
		return 0
	}
	z := x
	for i := 0; i < 10; i++ {
		z = (z + x/z) / 2
	}
	return z
}
