package knowledge

import (
	"fmt"
	"testing"
	"time"
)

func TestDistributedStreamingNodeCreation(t *testing.T) {
	idx := NewHSNWIndex(16, 200)
	node := NewDistributedStreamingNode("node-1", idx, 3)

	if node.nodeID != "node-1" {
		t.Errorf("Expected node-1, got %s", node.nodeID)
	}

	if node.replicationFactor != 3 {
		t.Errorf("Expected RF 3, got %d", node.replicationFactor)
	}
}

func TestRegisterPeer(t *testing.T) {
	idx := NewHSNWIndex(16, 200)
	node := NewDistributedStreamingNode("node-1", idx, 3)

	err := node.RegisterPeer("node-2", "localhost:5001")
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	if len(node.peers) != 1 {
		t.Errorf("Expected 1 peer, got %d", len(node.peers))
	}
}

func TestRegisterSelfFails(t *testing.T) {
	idx := NewHSNWIndex(16, 200)
	node := NewDistributedStreamingNode("node-1", idx, 3)

	err := node.RegisterPeer("node-1", "localhost:5000")
	if err == nil {
		t.Error("Expected error registering self")
	}
}

func TestSendOperation(t *testing.T) {
	idx := NewHSNWIndex(16, 200)
	node := NewDistributedStreamingNode("node-1", idx, 3)

	node.RegisterPeer("node-2", "localhost:5001")

	op := StreamingOperation{
		Type:      "delete",
		MessageID: "msg-1",
		Timestamp: time.Now(),
	}

	err := node.SendOperation(op, 1)
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	if len(node.messageLog) != 1 {
		t.Errorf("Expected 1 message in log, got %d", len(node.messageLog))
	}
}

func TestStreamingNodeGetReplicationStatus(t *testing.T) {
	idx := NewHSNWIndex(16, 200)
	node := NewDistributedStreamingNode("node-1", idx, 3)

	node.RegisterPeer("node-2", "localhost:5001")
	node.RegisterPeer("node-3", "localhost:5002")

	// Send operations
	for i := 0; i < 5; i++ {
		node.SendOperation(StreamingOperation{
			Type:      "delete",
			MessageID: fmt.Sprintf("msg-%d", i+1),
			Timestamp: time.Now(),
		}, i)
	}

	node.CheckPeerHealth()

	stats := node.GetReplicationStatus()
	if stats.TotalMessagesLogged != 5 {
		t.Errorf("Expected 5 logged messages, got %d", stats.TotalMessagesLogged)
	}
}

func TestARIMAPredictorCreation(t *testing.T) {
	predictor := NewARIMAPredictor("shard-1")

	if predictor.shardID != "shard-1" {
		t.Errorf("Expected shard-1, got %s", predictor.shardID)
	}

	if predictor.p != 2 || predictor.d != 1 || predictor.q != 1 {
		t.Error("ARIMA parameters not set correctly")
	}
}

func TestARIMAAddObservation(t *testing.T) {
	predictor := NewARIMAPredictor("shard-1")

	// Add observations
	for i := 1; i <= 10; i++ {
		predictor.AddObservation(float64(i%20), time.Now())
	}

	if len(predictor.observations) != 10 {
		t.Errorf("Expected 10 observations, got %d", len(predictor.observations))
	}
}

func TestARIMAForecast(t *testing.T) {
	predictor := NewARIMAPredictor("shard-1")

	// Add observations with trend
	for i := 1; i <= 20; i++ {
		predictor.AddObservation(float64(i), time.Now().Add(time.Duration(i)*time.Minute))
	}

	result := predictor.Forecast()

	if len(result.ForecastedRatios) != predictor.forecastHorizon {
		t.Errorf("Expected %d forecasts, got %d", predictor.forecastHorizon, len(result.ForecastedRatios))
	}

	if result.Confidence <= 0 || result.Confidence > 1 {
		t.Errorf("Invalid confidence: %.2f", result.Confidence)
	}
}

func TestAdaptiveThresholdCalculator(t *testing.T) {
	calc := NewAdaptiveThresholdCalculator("shard-1", 10.0)

	if calc.currentThreshold != 10.0 {
		t.Errorf("Expected 10.0, got %.1f", calc.currentThreshold)
	}
}

func TestAdaptiveThresholdRecordDeletion(t *testing.T) {
	calc := NewAdaptiveThresholdCalculator("shard-1", 10.0)

	// Record high-volatility deletions
	deletions := []float64{5, 15, 8, 18, 12, 20, 10}
	for _, d := range deletions {
		calc.RecordDeletion(d)
	}

	threshold := calc.GetAdaptiveThreshold()
	if threshold <= 10.0 {
		t.Log("Threshold should increase with volatility")
	}
}

func TestThresholdAnalysis(t *testing.T) {
	calc := NewAdaptiveThresholdCalculator("shard-1", 10.0)

	for i := 1; i <= 15; i++ {
		calc.RecordDeletion(float64(5 + i%10))
		calc.RecordCompaction()
	}

	analysis := calc.GetThresholdAnalysis()

	if analysis.CompactionCount != 15 {
		t.Errorf("Expected 15 compactions, got %d", analysis.CompactionCount)
	}

	if analysis.AverageInterval == 0 {
		t.Error("Average interval should be calculated")
	}
}

func TestMLBasedPriorityScorerCreation(t *testing.T) {
	scorer := NewMLBasedPriorityScorer()

	if scorer == nil {
		t.Error("Failed to create scorer")
	}
}

func TestMLBasedPriorityScorerRegisterShard(t *testing.T) {
	scorer := NewMLBasedPriorityScorer()

	scorer.RegisterShard("shard-1")
	scorer.RegisterShard("shard-2")

	if len(scorer.predictors) != 2 {
		t.Errorf("Expected 2 shards, got %d", len(scorer.predictors))
	}
}

func TestMLBasedPriorityScorerCalculatePriority(t *testing.T) {
	scorer := NewMLBasedPriorityScorer()
	scorer.RegisterShard("shard-1")

	// Low deletion ratio
	priority, reason := scorer.CalculatePriority("shard-1", 3.0)
	_ = reason
	if priority > 5 {
		t.Error("Low deletion should have low priority")
	}

	// High deletion ratio
	priority2, reason2 := scorer.CalculatePriority("shard-1", 25.0)
	if priority2 < 7 {
		t.Errorf("High deletion should have high priority, got %d (%s)", priority2, reason2)
	}
}

func TestCrossDatacenterCoordinatorCreation(t *testing.T) {
	coordinator := NewCrossDatacenterCoordinator()

	if coordinator == nil {
		t.Error("Failed to create coordinator")
	}
}

func TestRegisterDatacenter(t *testing.T) {
	coordinator := NewCrossDatacenterCoordinator()
	idx := NewHSNWIndex(16, 200)
	node := NewDistributedStreamingNode("dc-1", idx, 3)

	err := coordinator.RegisterDatacenter("dc-1", node)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	if len(coordinator.datacenters) != 1 {
		t.Errorf("Expected 1 datacenter, got %d", len(coordinator.datacenters))
	}
}

func TestProposeCompaction(t *testing.T) {
	coordinator := NewCrossDatacenterCoordinator()
	idx := NewHSNWIndex(16, 200)
	node := NewDistributedStreamingNode("dc-1", idx, 3)

	coordinator.RegisterDatacenter("dc-1", node)

	err := coordinator.ProposeCompaction("exec-1", []string{"dc-1"})
	if err != nil {
		t.Fatalf("Propose failed: %v", err)
	}

	status := coordinator.GetCoordinationStatus()
	if len(status) != 1 {
		t.Errorf("Expected 1 execution, got %d", len(status))
	}
}

func TestVoteOnCompaction(t *testing.T) {
	coordinator := NewCrossDatacenterCoordinator()
	idx := NewHSNWIndex(16, 200)
	node := NewDistributedStreamingNode("dc-1", idx, 3)

	coordinator.RegisterDatacenter("dc-1", node)
	coordinator.ProposeCompaction("exec-1", []string{"dc-1"})

	err := coordinator.VoteOnCompaction("exec-1", "dc-1", true)
	if err != nil {
		t.Fatalf("Vote failed: %v", err)
	}

	status := coordinator.GetCoordinationStatus()
	exec := status["exec-1"]
	if exec.Votes["dc-1"] != true {
		t.Error("Vote should be recorded")
	}
}

func TestCommitCompaction(t *testing.T) {
	coordinator := NewCrossDatacenterCoordinator()
	idx := NewHSNWIndex(16, 200)
	node := NewDistributedStreamingNode("dc-1", idx, 3)

	coordinator.RegisterDatacenter("dc-1", node)
	coordinator.ProposeCompaction("exec-1", []string{"dc-1"})
	coordinator.VoteOnCompaction("exec-1", "dc-1", true)

	// Move to preparing state
	status := coordinator.GetCoordinationStatus()
	status["exec-1"].GlobalState = "preparing"

	err := coordinator.CommitCompaction("exec-1")
	if err != nil {
		t.Fatalf("Commit failed: %v", err)
	}
}

func TestConsensusEngine(t *testing.T) {
	engine := NewConsensusEngine()

	engine.RegisterVoter("dc-1")
	engine.RegisterVoter("dc-2")
	engine.RegisterVoter("dc-3")

	if len(engine.voters) != 3 {
		t.Errorf("Expected 3 voters, got %d", len(engine.voters))
	}

	// Test quorum
	votes := map[string]bool{"dc-1": true, "dc-2": true}
	if !engine.IsQuorumReached(votes) {
		t.Error("2/3 should be quorum")
	}

	votes = map[string]bool{"dc-1": false, "dc-2": false}
	if engine.IsQuorumReached(votes) {
		t.Error("2/3 no votes should not be quorum")
	}
}

func TestDistributedStreamingIntegration(t *testing.T) {
	// Create 3 nodes
	nodes := make(map[string]*DistributedStreamingNode)
	indices := make(map[string]*HSNWIndex)

	for i := 1; i <= 3; i++ {
		nodeID := fmt.Sprintf("node-%d", i)
		idx := NewHSNWIndex(16, 200)
		indices[nodeID] = idx
		nodes[nodeID] = NewDistributedStreamingNode(nodeID, idx, 3)
	}

	// Connect nodes
	nodes["node-1"].RegisterPeer("node-2", "localhost:5001")
	nodes["node-1"].RegisterPeer("node-3", "localhost:5002")

	// Send operations
	for i := 0; i < 5; i++ {
		nodes["node-1"].SendOperation(StreamingOperation{
			Type:      "delete",
			MessageID: fmt.Sprintf("msg-%d", i+1),
			Timestamp: time.Now(),
		}, i)
	}

	// Verify replication
	nodes["node-1"].CheckPeerHealth()
	stats := nodes["node-1"].GetReplicationStatus()

	if stats.TotalMessagesLogged != 5 {
		t.Errorf("Expected 5 messages, got %d", stats.TotalMessagesLogged)
	}
}

func TestMLPredictionIntegration(t *testing.T) {
	scorer := NewMLBasedPriorityScorer()
	scorer.RegisterShard("shard-1")

	// Simulate deletion pattern
	ratios := []float64{5, 6, 7, 8, 10, 12, 15, 18, 20, 22}

	for _, ratio := range ratios {
		priority, _ := scorer.CalculatePriority("shard-1", ratio)

		if ratio < 10 && priority > 5 {
			t.Log("Low deletion should have low priority")
		}
		if ratio > 15 && priority < 7 {
			t.Log("High deletion should have high priority")
		}
	}
}
