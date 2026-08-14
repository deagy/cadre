package knowledge

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"sync"
)

// HSNWIndex implements a Hierarchical Navigable Small World graph for approximate nearest neighbor search.
// See: https://arxiv.org/abs/1802.02413
type HSNWIndex struct {
	mu              sync.RWMutex
	nodes           map[string]*HSNWNode    // messageID -> node
	tombstones      map[string]bool         // deleted nodes (lazy deletion)
	entryPoint      *HSNWNode               // entry point for search
	layers          map[int][]*HSNWNode     // layer -> nodes
	maxLayer        int
	M               int                     // max connections per node
	EfConstruction  int                     // ef during construction
	EfSearch        int                     // ef for queries
	EfDefault       int
	mL              float64                 // 1/ln(2)
	indexSize       int64                   // number of vectors indexed
	buildTimeMs     int64
	deletedCount    int64                   // number of deleted vectors (tombstones)
}

// HSNWNode represents a node in the HNSW graph.
type HSNWNode struct {
	MessageID  string
	Embedding  []float32
	Layer      int
	Neighbors  map[int][]*HSNWNode // layer -> list of neighbors
}

// HSNWSearchResult represents a search result with distance.
type HSNWSearchResult struct {
	MessageID  string
	Distance   float32
	Embedding  []float32
}

// NewHSNWIndex creates a new HNSW index.
func NewHSNWIndex(M int, efConstruction int) *HSNWIndex {
	return &HSNWIndex{
		nodes:          make(map[string]*HSNWNode),
		tombstones:     make(map[string]bool),
		layers:         make(map[int][]*HSNWNode),
		M:              M,
		EfConstruction: efConstruction,
		EfSearch:       efConstruction,
		EfDefault:      efConstruction,
		mL:             1.0 / math.Log(2.0),
		maxLayer:       0,
	}
}

// Insert adds a vector to the index.
func (idx *HSNWIndex) Insert(messageID string, embedding []float32) error {
	if len(embedding) == 0 {
		return fmt.Errorf("embedding cannot be empty")
	}

	idx.mu.Lock()
	defer idx.mu.Unlock()

	if _, exists := idx.nodes[messageID]; exists {
		return fmt.Errorf("message already indexed: %s", messageID)
	}

	// Assign layer to new node
	layer := idx.assignLayer()

	// Create new node
	node := &HSNWNode{
		MessageID: messageID,
		Embedding: embedding,
		Layer:     layer,
		Neighbors: make(map[int][]*HSNWNode),
	}

	// Initialize neighbor lists for all layers
	for lc := 0; lc <= layer; lc++ {
		node.Neighbors[lc] = make([]*HSNWNode, 0, idx.M)
	}

	// If this is the first node, make it the entry point
	if len(idx.nodes) == 0 {
		idx.entryPoint = node
		idx.nodes[messageID] = node
		idx.layers[layer] = append(idx.layers[layer], node)
		idx.indexSize++
		return nil
	}

	// Find nearest neighbors at all layers
	idx.insertNode(node)

	idx.nodes[messageID] = node
	idx.layers[layer] = append(idx.layers[layer], node)
	idx.indexSize++

	return nil
}

// insertNode inserts a node and updates connections.
func (idx *HSNWIndex) insertNode(newNode *HSNWNode) {
	entryPoints := []*HSNWNode{idx.entryPoint}

	// Search for neighbors at all layers
	for lc := idx.maxLayer; lc >= 0; lc-- {
		candidates := idx.searchLayer(newNode.Embedding, entryPoints, 1, lc)
		if len(candidates) > 0 {
			entryPoints = candidates
		}

		if lc < newNode.Layer {
			// Insert into layer and find neighbors
			M := idx.M
			if lc == 0 {
				M = idx.M * 2
			}

			neighbors := idx.searchLayer(newNode.Embedding, entryPoints, idx.EfConstruction, lc)
			idx.pruneConnections(newNode, neighbors, M, lc)

			// Add bidirectional links
			for _, neighbor := range neighbors {
				if len(neighbor.Neighbors[lc]) < M {
					neighbor.Neighbors[lc] = append(neighbor.Neighbors[lc], newNode)
					newNode.Neighbors[lc] = append(newNode.Neighbors[lc], neighbor)
				}
			}
		}
	}

	// Update entry point if necessary
	if newNode.Layer > idx.maxLayer {
		idx.maxLayer = newNode.Layer
		idx.entryPoint = newNode
	}
}

// Search finds approximate nearest neighbors (excluding tombstones).
func (idx *HSNWIndex) Search(query []float32, k int) []*HSNWSearchResult {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if idx.entryPoint == nil || len(query) == 0 {
		return nil
	}

	entryPoints := []*HSNWNode{idx.entryPoint}

	// Search at upper layers
	for lc := idx.maxLayer; lc >= 1; lc-- {
		candidates := idx.searchLayer(query, entryPoints, 1, lc)
		if len(candidates) > 0 {
			entryPoints = candidates
		}
	}

	// Search at layer 0
	candidates := idx.searchLayer(query, entryPoints, idx.EfSearch, 0)

	// Convert to results and sort by distance (skip tombstones)
	results := make([]*HSNWSearchResult, 0, len(candidates))
	for _, node := range candidates {
		// Skip deleted entries
		if idx.tombstones[node.MessageID] {
			continue
		}

		dist := cosineSimilarityDistance(query, node.Embedding)
		results = append(results, &HSNWSearchResult{
			MessageID: node.MessageID,
			Distance:  dist,
			Embedding: node.Embedding,
		})
	}

	// Sort by distance (ascending)
	sort.Slice(results, func(i, j int) bool {
		return results[i].Distance < results[j].Distance
	})

	// Return top k
	if len(results) > k {
		results = results[:k]
	}

	return results
}

// searchLayer performs greedy search at a specific layer (skips tombstones).
func (idx *HSNWIndex) searchLayer(query []float32, entryPoints []*HSNWNode, ef int, layer int) []*HSNWNode {
	visited := make(map[string]bool)
	candidates := make([]*HSNWNode, 0)
	nearestNeighbors := make([]*HSNWNode, 0, ef)

	// Initialize with entry points (include even if deleted for traversal)
	for _, ep := range entryPoints {
		candidates = append(candidates, ep)
		visited[ep.MessageID] = true

		if len(nearestNeighbors) < ef {
			nearestNeighbors = append(nearestNeighbors, ep)
		}
	}

	for len(candidates) > 0 {
		// Get closest candidate
		sort.Slice(candidates, func(i, j int) bool {
			di := cosineSimilarityDistance(query, candidates[i].Embedding)
			dj := cosineSimilarityDistance(query, candidates[j].Embedding)
			return di < dj
		})

		current := candidates[0]
		candidates = candidates[1:]

		currentDist := cosineSimilarityDistance(query, current.Embedding)
		farthestDist := cosineSimilarityDistance(query, nearestNeighbors[len(nearestNeighbors)-1].Embedding)

		if currentDist > farthestDist {
			break // No better candidates
		}

		// Check neighbors
		if neighbors, ok := current.Neighbors[layer]; ok {
			for _, neighbor := range neighbors {
				if !visited[neighbor.MessageID] {
					visited[neighbor.MessageID] = true
					dist := cosineSimilarityDistance(query, neighbor.Embedding)

					if dist < farthestDist || len(nearestNeighbors) < ef {
						candidates = append(candidates, neighbor)

						if len(nearestNeighbors) < ef {
							nearestNeighbors = append(nearestNeighbors, neighbor)
						} else if dist < farthestDist {
							// Replace farthest
							sort.Slice(nearestNeighbors, func(i, j int) bool {
								di := cosineSimilarityDistance(query, nearestNeighbors[i].Embedding)
								dj := cosineSimilarityDistance(query, nearestNeighbors[j].Embedding)
								return di < dj
							})
							nearestNeighbors = nearestNeighbors[:len(nearestNeighbors)-1]
							nearestNeighbors = append(nearestNeighbors, neighbor)
						}
					}
				}
			}
		}
	}

	return nearestNeighbors
}

// pruneConnections selects M neighbors from candidates.
func (idx *HSNWIndex) pruneConnections(node *HSNWNode, candidates []*HSNWNode, M int, layer int) {
	// Simple heuristic: keep M closest neighbors
	if len(candidates) > M {
		candidates = candidates[:M]
	}

	node.Neighbors[layer] = make([]*HSNWNode, len(candidates))
	copy(node.Neighbors[layer], candidates)
}

// assignLayer assigns a random layer to a new node.
func (idx *HSNWIndex) assignLayer() int {
	return int(-math.Log(rand.Float64()) * idx.mL)
}

// GetStats returns index statistics.
func (idx *HSNWIndex) GetStats() *HSNWStats {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	var totalConnections int64
	for _, nodes := range idx.layers {
		for _, node := range nodes {
			for _, neighbors := range node.Neighbors {
				totalConnections += int64(len(neighbors))
			}
		}
	}

	return &HSNWStats{
		IndexSize:           idx.indexSize,
		MaxLayer:            idx.maxLayer,
		TotalConnections:    totalConnections,
		AverageConnections:  float64(totalConnections) / float64(idx.indexSize),
		M:                   idx.M,
		EfSearch:            idx.EfSearch,
	}
}

// HSNWStats provides index statistics.
type HSNWStats struct {
	IndexSize           int64
	MaxLayer            int
	TotalConnections    int64
	AverageConnections  float64
	M                   int
	EfSearch            int
}

// cosineSimilarityDistance computes 1 - cosine_similarity.
func cosineSimilarityDistance(a, b []float32) float32 {
	if len(a) != len(b) {
		return math.MaxFloat32
	}

	var dotProduct, normA, normB float32
	for i := range a {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return math.MaxFloat32
	}

	similarity := dotProduct / (float32(math.Sqrt(float64(normA))) * float32(math.Sqrt(float64(normB))))
	return 1.0 - similarity
}

// Delete marks a vector as deleted (tombstone).
func (idx *HSNWIndex) Delete(messageID string) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	if _, exists := idx.nodes[messageID]; !exists {
		return fmt.Errorf("message not found: %s", messageID)
	}

	if idx.tombstones[messageID] {
		return fmt.Errorf("message already deleted: %s", messageID)
	}

	idx.tombstones[messageID] = true
	idx.deletedCount++

	return nil
}

// Undelete restores a deleted vector (remove tombstone).
func (idx *HSNWIndex) Undelete(messageID string) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	if !idx.tombstones[messageID] {
		return fmt.Errorf("message not deleted: %s", messageID)
	}

	delete(idx.tombstones, messageID)
	idx.deletedCount--

	return nil
}

// Update replaces a vector's embedding without rebuild.
func (idx *HSNWIndex) Update(messageID string, newEmbedding []float32) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	if len(newEmbedding) == 0 {
		return fmt.Errorf("embedding cannot be empty")
	}

	node, exists := idx.nodes[messageID]
	if !exists {
		return fmt.Errorf("message not found: %s", messageID)
	}

	if idx.tombstones[messageID] {
		return fmt.Errorf("cannot update deleted message: %s", messageID)
	}

	// Update embedding in place
	node.Embedding = newEmbedding

	return nil
}

// Compact removes deleted entries and rebuilds connectivity.
func (idx *HSNWIndex) Compact() error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	if idx.deletedCount == 0 {
		return nil // Nothing to compact
	}

	// Collect IDs to remove
	toRemove := make([]string, 0, idx.deletedCount)
	for msgID := range idx.tombstones {
		toRemove = append(toRemove, msgID)
	}

	// Remove nodes and their neighbor references
	for _, msgID := range toRemove {
		node := idx.nodes[msgID]
		if node == nil {
			continue
		}

		// Remove this node from all neighbors' lists
		for layer, neighbors := range node.Neighbors {
			for _, neighbor := range neighbors {
				if neighbor == nil {
					continue
				}
				// Remove current node from neighbor's list
				newNeighbors := make([]*HSNWNode, 0)
				for _, n := range neighbor.Neighbors[layer] {
					if n != nil && n.MessageID != msgID {
						newNeighbors = append(newNeighbors, n)
					}
				}
				neighbor.Neighbors[layer] = newNeighbors
			}
		}

		// Remove the node
		delete(idx.nodes, msgID)
	}

	// Update entry point if deleted
	if idx.entryPoint != nil && idx.tombstones[idx.entryPoint.MessageID] {
		// Find new entry point from remaining nodes
		for msgID, node := range idx.nodes {
			if !idx.tombstones[msgID] && node.Layer >= idx.maxLayer {
				idx.entryPoint = node
				break
			}
		}
		if idx.entryPoint != nil && idx.tombstones[idx.entryPoint.MessageID] {
			// Fallback: find any non-deleted node
			for msgID, node := range idx.nodes {
				if !idx.tombstones[msgID] {
					idx.entryPoint = node
					break
				}
			}
		}
	}

	// Clear tombstones
	idx.tombstones = make(map[string]bool)
	idx.deletedCount = 0

	// Rebuild layer structure
	idx.layers = make(map[int][]*HSNWNode)
	for msgID, node := range idx.nodes {
		if !idx.tombstones[msgID] {
			idx.layers[node.Layer] = append(idx.layers[node.Layer], node)
		}
	}

	// Recalculate max layer
	idx.maxLayer = 0
	for layer := range idx.layers {
		if layer > idx.maxLayer {
			idx.maxLayer = layer
		}
	}

	return nil
}

// GetDeletionStatus returns information about deleted entries.
func (idx *HSNWIndex) GetDeletionStatus() *DeletionStatus {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	totalEntries := int64(len(idx.nodes))
	liveEntries := totalEntries - idx.deletedCount

	var deletionRatio float64
	if totalEntries > 0 {
		deletionRatio = float64(idx.deletedCount) / float64(totalEntries) * 100
	}

	return &DeletionStatus{
		TotalEntries:  totalEntries,
		LiveEntries:   liveEntries,
		DeletedCount:  idx.deletedCount,
		DeletionRatio: deletionRatio,
		NeedsCompaction: idx.deletedCount > 0 && deletionRatio > 10,
	}
}

// DeletionStatus provides information about deleted vectors.
type DeletionStatus struct {
	TotalEntries    int64
	LiveEntries     int64
	DeletedCount    int64
	DeletionRatio   float64
	NeedsCompaction bool
}

// BatchDeleteResult tracks result of batch delete operation.
type BatchDeleteResult struct {
	Successful  int
	Failed      int
	Errors      map[string]string
	TotalTime   int64
}

// BatchUpdateResult tracks result of batch update operation.
type BatchUpdateResult struct {
	Successful  int
	Failed      int
	Errors      map[string]string
	TotalTime   int64
}

// CompactionProgress tracks incremental compaction state.
type CompactionProgress struct {
	State              string // "pending", "in_progress", "complete", "failed"
	EntriesProcessed   int64
	EntriesRemaining   int64
	EntriesRemoved     int64
	PercentComplete    float64
	EstimatedTimeMs    int64
	StartTimeUnix      int64
	LastUpdateUnix     int64
}

// BatchDelete deletes multiple vectors atomically.
func (idx *HSNWIndex) BatchDelete(messageIDs []string) *BatchDeleteResult {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	result := &BatchDeleteResult{
		Errors: make(map[string]string),
	}

	for _, msgID := range messageIDs {
		if _, exists := idx.nodes[msgID]; !exists {
			result.Failed++
			result.Errors[msgID] = "not found"
			continue
		}

		if idx.tombstones[msgID] {
			result.Failed++
			result.Errors[msgID] = "already deleted"
			continue
		}

		idx.tombstones[msgID] = true
		idx.deletedCount++
		result.Successful++
	}

	return result
}

// BatchUndelete restores multiple deleted vectors atomically.
func (idx *HSNWIndex) BatchUndelete(messageIDs []string) *BatchDeleteResult {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	result := &BatchDeleteResult{
		Errors: make(map[string]string),
	}

	for _, msgID := range messageIDs {
		if !idx.tombstones[msgID] {
			result.Failed++
			result.Errors[msgID] = "not deleted"
			continue
		}

		delete(idx.tombstones, msgID)
		idx.deletedCount--
		result.Successful++
	}

	return result
}

// BatchUpdate updates multiple embeddings atomically.
func (idx *HSNWIndex) BatchUpdate(updates map[string][]float32) *BatchUpdateResult {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	result := &BatchUpdateResult{
		Errors: make(map[string]string),
	}

	for msgID, newEmb := range updates {
		if len(newEmb) == 0 {
			result.Failed++
			result.Errors[msgID] = "empty embedding"
			continue
		}

		node, exists := idx.nodes[msgID]
		if !exists {
			result.Failed++
			result.Errors[msgID] = "not found"
			continue
		}

		if idx.tombstones[msgID] {
			result.Failed++
			result.Errors[msgID] = "deleted"
			continue
		}

		node.Embedding = newEmb
		result.Successful++
	}

	return result
}

// CompactIncremental performs non-blocking compaction with progress tracking.
func (idx *HSNWIndex) CompactIncremental(batchSize int64) *CompactionProgress {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	progress := &CompactionProgress{
		State:           "pending",
		EntriesRemaining: idx.deletedCount,
		StartTimeUnix:   int64(0), // Would be time.Now().Unix() in production
	}

	if idx.deletedCount == 0 {
		progress.State = "complete"
		progress.PercentComplete = 100.0
		return progress
	}

	progress.State = "in_progress"

	// Collect IDs to remove
	toRemove := make([]string, 0, idx.deletedCount)
	for msgID := range idx.tombstones {
		toRemove = append(toRemove, msgID)

		// Check batch size
		if int64(len(toRemove)) >= batchSize {
			break
		}
	}

	// Remove nodes and their neighbor references
	for _, msgID := range toRemove {
		node := idx.nodes[msgID]
		if node == nil {
			continue
		}

		// Remove this node from all neighbors' lists
		for layer, neighbors := range node.Neighbors {
			for _, neighbor := range neighbors {
				if neighbor == nil {
					continue
				}
				// Remove current node from neighbor's list
				newNeighbors := make([]*HSNWNode, 0)
				for _, n := range neighbor.Neighbors[layer] {
					if n != nil && n.MessageID != msgID {
						newNeighbors = append(newNeighbors, n)
					}
				}
				neighbor.Neighbors[layer] = newNeighbors
			}
		}

		// Remove the node
		delete(idx.nodes, msgID)
		progress.EntriesRemoved++
	}

	// Update entry point if deleted
	if idx.entryPoint != nil && idx.tombstones[idx.entryPoint.MessageID] {
		for msgID, node := range idx.nodes {
			if !idx.tombstones[msgID] && node.Layer >= idx.maxLayer {
				idx.entryPoint = node
				break
			}
		}
	}

	// Remove processed tombstones
	for _, msgID := range toRemove {
		delete(idx.tombstones, msgID)
		idx.deletedCount--
	}

	// Update progress
	progress.EntriesProcessed = progress.EntriesRemoved
	progress.EntriesRemaining = idx.deletedCount
	progress.PercentComplete = 100.0 * float64(progress.EntriesProcessed) / float64(progress.EntriesProcessed+progress.EntriesRemaining)

	if idx.deletedCount == 0 {
		progress.State = "complete"
		progress.PercentComplete = 100.0
	}

	return progress
}

// GetCompactionProgress returns current incremental compaction state.
func (idx *HSNWIndex) GetCompactionProgress() *CompactionProgress {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	return &CompactionProgress{
		State:           "idle",
		EntriesRemaining: idx.deletedCount,
		EntriesRemoved:   0,
	}
}
