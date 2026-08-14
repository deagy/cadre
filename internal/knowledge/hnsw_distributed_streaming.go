package knowledge

import (
	"fmt"
	"sync"
	"time"
)

// DistributedStreamingNode represents a node in a distributed streaming network.
type DistributedStreamingNode struct {
	mu                sync.RWMutex
	nodeID            string
	peers             map[string]*RemoteNode
	localWriter       *StreamingBatchWriter
	messageLog        []StreamingMessage
	logMaxSize        int
	replicationFactor int
	lastHeartbeat     map[string]time.Time
	healthStatus      map[string]bool
}

// RemoteNode represents a peer node.
type RemoteNode struct {
	NodeID    string
	Address   string
	LastSeen  time.Time
	Status    string // "healthy", "degraded", "offline"
	Latency   int64  // milliseconds
}

// StreamingMessage is the distributed message format.
type StreamingMessage struct {
	ID            string    // Unique message ID
	NodeID        string    // Originating node
	Operation     StreamingOperation
	Timestamp     time.Time
	ReplicationID int // Which replica
	AckCount      int // Number of acknowledgments
	AckedBy       []string
}

// DistributedStreamingStats provides network-wide statistics.
type DistributedStreamingStats struct {
	NodeID               string
	ActivePeers          int
	TotalMessagesLogged  int64
	MessagesReplicated   int64
	ReplicationLatency   int64 // milliseconds
	NetworkHealthScore   float64 // 0-100
	PartitionDetected    bool
	ReplicationFactor    int
}

// NewDistributedStreamingNode creates a node in the distributed network.
func NewDistributedStreamingNode(nodeID string, idx *HSNWIndex, replicationFactor int) *DistributedStreamingNode {
	return &DistributedStreamingNode{
		nodeID:            nodeID,
		peers:             make(map[string]*RemoteNode),
		localWriter:       NewStreamingBatchWriter(idx, 10000),
		messageLog:        make([]StreamingMessage, 0),
		logMaxSize:        100000,
		replicationFactor: replicationFactor,
		lastHeartbeat:     make(map[string]time.Time),
		healthStatus:      make(map[string]bool),
	}
}

// RegisterPeer adds a peer node to the network.
func (dsn *DistributedStreamingNode) RegisterPeer(peerID, address string) error {
	dsn.mu.Lock()
	defer dsn.mu.Unlock()

	if peerID == dsn.nodeID {
		return fmt.Errorf("cannot register self as peer")
	}

	dsn.peers[peerID] = &RemoteNode{
		NodeID:   peerID,
		Address:  address,
		LastSeen: time.Now(),
		Status:   "healthy",
		Latency:  0,
	}

	dsn.lastHeartbeat[peerID] = time.Now()
	dsn.healthStatus[peerID] = true

	return nil
}

// SendOperation broadcasts operation to replicas.
func (dsn *DistributedStreamingNode) SendOperation(op StreamingOperation, replicaID int) error {
	dsn.mu.Lock()
	defer dsn.mu.Unlock()

	msg := StreamingMessage{
		ID:            fmt.Sprintf("%s-%d-%d", dsn.nodeID, time.Now().UnixNano(), replicaID),
		NodeID:        dsn.nodeID,
		Operation:     op,
		Timestamp:     time.Now(),
		ReplicationID: replicaID,
		AckCount:      0,
		AckedBy:       make([]string, 0),
	}

	// Add to local log
	dsn.messageLog = append(dsn.messageLog, msg)
	if len(dsn.messageLog) > dsn.logMaxSize {
		// Keep only recent messages
		dsn.messageLog = dsn.messageLog[len(dsn.messageLog)-dsn.logMaxSize:]
	}

	// Send to peers (simulated)
	for peerID, peer := range dsn.peers {
		if dsn.healthStatus[peerID] {
			go dsn.replicateMessage(peer, msg)
		}
	}

	return nil
}

// replicateMessage sends a message to a peer.
func (dsn *DistributedStreamingNode) replicateMessage(peer *RemoteNode, msg StreamingMessage) {
	// Simulated replication - in real implementation would use gRPC or similar
	start := time.Now()

	// Simulate network latency
	time.Sleep(1 * time.Millisecond)

	latency := time.Since(start).Milliseconds()

	dsn.mu.Lock()
	dsn.peers[peer.NodeID].Latency = latency
	dsn.lastHeartbeat[peer.NodeID] = time.Now()
	dsn.mu.Unlock()
}

// AcknowledgeMessage records acknowledgment from peer.
func (dsn *DistributedStreamingNode) AcknowledgeMessage(msgID string, peerID string) error {
	dsn.mu.Lock()
	defer dsn.mu.Unlock()

	// Find and update message
	for i, msg := range dsn.messageLog {
		if msg.ID == msgID {
			// Check if peer already acked
			alreadyAcked := false
			for _, ack := range msg.AckedBy {
				if ack == peerID {
					alreadyAcked = true
					break
				}
			}

			if !alreadyAcked {
				msg.AckedBy = append(msg.AckedBy, peerID)
				msg.AckCount++
				dsn.messageLog[i] = msg
			}

			return nil
		}
	}

	return fmt.Errorf("message not found: %s", msgID)
}

// GetReplicationStatus returns replication statistics.
func (dsn *DistributedStreamingNode) GetReplicationStatus() *DistributedStreamingStats {
	dsn.mu.RLock()
	defer dsn.mu.RUnlock()

	stats := &DistributedStreamingStats{
		NodeID:             dsn.nodeID,
		TotalMessagesLogged: int64(len(dsn.messageLog)),
		ReplicationFactor:  dsn.replicationFactor,
	}

	// Count active peers
	activePeers := 0
	var totalLatency int64
	for peerID, peer := range dsn.peers {
		if dsn.healthStatus[peerID] {
			activePeers++
			totalLatency += peer.Latency
		}
	}

	stats.ActivePeers = activePeers
	if activePeers > 0 {
		stats.ReplicationLatency = totalLatency / int64(activePeers)
	}

	// Calculate replicated messages
	replicated := 0
	for _, msg := range dsn.messageLog {
		if msg.AckCount >= dsn.replicationFactor-1 {
			replicated++
		}
	}
	stats.MessagesReplicated = int64(replicated)

	// Health score: 0-100 based on replication factor met
	if len(dsn.messageLog) > 0 {
		stats.NetworkHealthScore = float64(replicated) / float64(len(dsn.messageLog)) * 100.0
	}

	// Partition detection: too many offline peers
	offlinePeers := len(dsn.peers) - activePeers
	if len(dsn.peers) > 0 && float64(offlinePeers)/float64(len(dsn.peers)) > 0.3 {
		stats.PartitionDetected = true
	}

	return stats
}

// CheckPeerHealth monitors peer connectivity.
func (dsn *DistributedStreamingNode) CheckPeerHealth() {
	dsn.mu.Lock()
	defer dsn.mu.Unlock()

	timeout := 30 * time.Second
	now := time.Now()

	for peerID, lastSeen := range dsn.lastHeartbeat {
		if now.Sub(lastSeen) > timeout {
			dsn.healthStatus[peerID] = false
			if peer, ok := dsn.peers[peerID]; ok {
				peer.Status = "offline"
			}
		} else {
			dsn.healthStatus[peerID] = true
			if peer, ok := dsn.peers[peerID]; ok {
				peer.Status = "healthy"
			}
		}
	}
}

// GetMessageLog returns recent messages.
func (dsn *DistributedStreamingNode) GetMessageLog(limit int) []StreamingMessage {
	dsn.mu.RLock()
	defer dsn.mu.RUnlock()

	if limit > len(dsn.messageLog) {
		limit = len(dsn.messageLog)
	}

	messages := make([]StreamingMessage, limit)
	copy(messages, dsn.messageLog[len(dsn.messageLog)-limit:])

	return messages
}

// CoordinatedCompactionState tracks cross-node compaction.
type CoordinatedCompactionState struct {
	ExecutionID      string
	ParticipantNodes []string
	GlobalState      string // "proposed", "preparing", "committed", "aborted"
	Votes            map[string]bool
	VoteDeadline     time.Time
	CommitDeadline   time.Time
}

// CrossDatacenterCoordinator manages multi-datacenter compaction.
type CrossDatacenterCoordinator struct {
	mu              sync.RWMutex
	datacenters     map[string]*DistributedStreamingNode
	compactionState map[string]*CoordinatedCompactionState
	consensus       *ConsensusEngine
}

// NewCrossDatacenterCoordinator creates a multi-datacenter coordinator.
func NewCrossDatacenterCoordinator() *CrossDatacenterCoordinator {
	return &CrossDatacenterCoordinator{
		datacenters:     make(map[string]*DistributedStreamingNode),
		compactionState: make(map[string]*CoordinatedCompactionState),
		consensus:       NewConsensusEngine(),
	}
}

// RegisterDatacenter adds a datacenter node.
func (cdc *CrossDatacenterCoordinator) RegisterDatacenter(dcID string, node *DistributedStreamingNode) error {
	cdc.mu.Lock()
	defer cdc.mu.Unlock()

	if _, exists := cdc.datacenters[dcID]; exists {
		return fmt.Errorf("datacenter already registered: %s", dcID)
	}

	cdc.datacenters[dcID] = node
	cdc.consensus.RegisterVoter(dcID)

	return nil
}

// ProposeCompaction initiates 2-phase commit for compaction.
func (cdc *CrossDatacenterCoordinator) ProposeCompaction(execID string, dcIDs []string) error {
	cdc.mu.Lock()

	state := &CoordinatedCompactionState{
		ExecutionID:      execID,
		ParticipantNodes: dcIDs,
		GlobalState:      "proposed",
		Votes:            make(map[string]bool),
		VoteDeadline:     time.Now().Add(5 * time.Second),
		CommitDeadline:   time.Now().Add(30 * time.Second),
	}

	cdc.compactionState[execID] = state
	cdc.mu.Unlock()

	// Initiate vote phase
	return cdc.consensus.BeginConsensus(execID, dcIDs)
}

// VoteOnCompaction records a vote from a datacenter.
func (cdc *CrossDatacenterCoordinator) VoteOnCompaction(execID string, dcID string, vote bool) error {
	cdc.mu.Lock()
	defer cdc.mu.Unlock()

	state, exists := cdc.compactionState[execID]
	if !exists {
		return fmt.Errorf("compaction not found: %s", execID)
	}

	state.Votes[dcID] = vote

	// Check if all votes collected
	if len(state.Votes) == len(state.ParticipantNodes) {
		// Determine outcome
		yesVotes := 0
		for _, v := range state.Votes {
			if v {
				yesVotes++
			}
		}

		if yesVotes == len(state.ParticipantNodes) {
			state.GlobalState = "preparing"
		} else {
			state.GlobalState = "aborted"
		}
	}

	return nil
}

// CommitCompaction finalizes compaction across datacenters.
func (cdc *CrossDatacenterCoordinator) CommitCompaction(execID string) error {
	cdc.mu.Lock()
	defer cdc.mu.Unlock()

	state, exists := cdc.compactionState[execID]
	if !exists {
		return fmt.Errorf("compaction not found: %s", execID)
	}

	if state.GlobalState != "preparing" {
		return fmt.Errorf("compaction not in preparing state: %s", state.GlobalState)
	}

	state.GlobalState = "committed"

	// Notify all participants
	for _, dcID := range state.ParticipantNodes {
		go cdc.notifyCommit(dcID, execID)
	}

	return nil
}

// notifyCommit sends commit notification to datacenter.
func (cdc *CrossDatacenterCoordinator) notifyCommit(dcID string, execID string) {
	// Simulated notification - in real implementation would use distributed messaging
	time.Sleep(1 * time.Millisecond)
}

// GetCoordinationStatus returns current coordination state.
func (cdc *CrossDatacenterCoordinator) GetCoordinationStatus() map[string]*CoordinatedCompactionState {
	cdc.mu.RLock()
	defer cdc.mu.RUnlock()

	status := make(map[string]*CoordinatedCompactionState)
	for k, v := range cdc.compactionState {
		state := *v
		status[k] = &state
	}

	return status
}

// ConsensusEngine implements Raft-like consensus.
type ConsensusEngine struct {
	mu       sync.RWMutex
	voters   []string
	terms    map[string]int64
	leader   string
}

// NewConsensusEngine creates a consensus engine.
func NewConsensusEngine() *ConsensusEngine {
	return &ConsensusEngine{
		voters: make([]string, 0),
		terms:  make(map[string]int64),
	}
}

// RegisterVoter adds a voter.
func (ce *ConsensusEngine) RegisterVoter(voterID string) {
	ce.mu.Lock()
	defer ce.mu.Unlock()

	ce.voters = append(ce.voters, voterID)
	ce.terms[voterID] = 0
}

// BeginConsensus starts a consensus round.
func (ce *ConsensusEngine) BeginConsensus(execID string, voters []string) error {
	ce.mu.Lock()
	defer ce.mu.Unlock()

	if len(voters) == 0 {
		return fmt.Errorf("no voters provided")
	}

	// Increment term
	for _, voter := range voters {
		ce.terms[voter]++
	}

	return nil
}

// IsQuorumReached checks if quorum is met.
func (ce *ConsensusEngine) IsQuorumReached(votes map[string]bool) bool {
	requiredVotes := (len(votes) / 2) + 1
	yesVotes := 0

	for _, vote := range votes {
		if vote {
			yesVotes++
		}
	}

	return yesVotes >= requiredVotes
}
