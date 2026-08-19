package knowledge

import (
	"crypto/md5"
	"fmt"
	"sort"
)

// ShardingStrategy defines how messages are distributed across stores.
type ShardingStrategy interface {
	// GetShardKey computes the shard key for a message.
	GetShardKey(source, classification string, convID string) string

	// GetShardID returns the shard ID (0-based) for a shard key.
	GetShardID(shardKey string, numShards int) int

	// Name returns the strategy identifier.
	Name() string
}

// ClassificationShardingStrategy shards by classification level.
// Example: "public" → shard 0, "secret" → shard 2, etc.
type ClassificationShardingStrategy struct {
	classifications []string // Ordered list of classifications
}

// NewClassificationShardingStrategy creates a new classification-based sharding strategy.
func NewClassificationShardingStrategy(classifications ...string) *ClassificationShardingStrategy {
	// Sort for consistency
	sorted := make([]string, len(classifications))
	copy(sorted, classifications)
	sort.Strings(sorted)

	return &ClassificationShardingStrategy{
		classifications: sorted,
	}
}

func (c *ClassificationShardingStrategy) GetShardKey(source, classification string, convID string) string {
	return classification
}

func (c *ClassificationShardingStrategy) GetShardID(shardKey string, numShards int) int {
	// Find index of classification in sorted list
	for i, cls := range c.classifications {
		if cls == shardKey {
			return i % numShards
		}
	}
	// Unknown classification → hash it
	return hashShardKey(shardKey) % numShards
}

func (c *ClassificationShardingStrategy) Name() string {
	return "classification"
}

// SourceShardingStrategy shards by source identifier.
// Example: "app-a" → shard 0, "app-b" → shard 1, etc.
type SourceShardingStrategy struct{}

func (s *SourceShardingStrategy) GetShardKey(source, classification string, convID string) string {
	return source
}

func (s *SourceShardingStrategy) GetShardID(shardKey string, numShards int) int {
	return hashShardKey(shardKey) % numShards
}

func (s *SourceShardingStrategy) Name() string {
	return "source"
}

// ConversationShardingStrategy shards by conversation ID.
// Keeps all messages in a conversation on the same shard.
type ConversationShardingStrategy struct{}

func (c *ConversationShardingStrategy) GetShardKey(source, classification string, convID string) string {
	return convID
}

func (c *ConversationShardingStrategy) GetShardID(shardKey string, numShards int) int {
	return hashShardKey(shardKey) % numShards
}

func (c *ConversationShardingStrategy) Name() string {
	return "conversation"
}

// CompositeShardingStrategy shards using multiple keys in sequence.
// Example: (classification, source) → hash both together
type CompositeShardingStrategy struct {
	strategies []ShardingStrategy
	weights    []int // Relative weight of each strategy
}

// NewCompositeShardingStrategy creates a composite strategy.
func NewCompositeShardingStrategy(strategies ...ShardingStrategy) *CompositeShardingStrategy {
	weights := make([]int, len(strategies))
	// Equal weights by default
	for i := range weights {
		weights[i] = 1
	}

	return &CompositeShardingStrategy{
		strategies: strategies,
		weights:    weights,
	}
}

func (c *CompositeShardingStrategy) GetShardKey(source, classification string, convID string) string {
	// Combine all strategy keys
	var result string
	for _, strategy := range c.strategies {
		result += "|" + strategy.GetShardKey(source, classification, convID)
	}
	return result
}

func (c *CompositeShardingStrategy) GetShardID(shardKey string, numShards int) int {
	return hashShardKey(shardKey) % numShards
}

func (c *CompositeShardingStrategy) Name() string {
	return "composite"
}

// ConsistentHashRing implements consistent hashing for shard assignment.
// Supports adding/removing shards with minimal key redistribution.
type ConsistentHashRing struct {
	shards      []string          // Ordered shard IDs
	hashRing    map[uint32]string // Hash → shard ID mapping
	virtualKeys int               // Virtual keys per shard for distribution
}

// NewConsistentHashRing creates a new consistent hash ring.
func NewConsistentHashRing(shards []string, virtualKeys int) *ConsistentHashRing {
	if virtualKeys <= 0 {
		virtualKeys = 150 // Default: 150 virtual keys per shard
	}

	ring := &ConsistentHashRing{
		shards:      make([]string, len(shards)),
		hashRing:    make(map[uint32]string),
		virtualKeys: virtualKeys,
	}

	copy(ring.shards, shards)
	sort.Strings(ring.shards)
	ring.buildRing()

	return ring
}

// buildRing constructs the hash ring.
func (c *ConsistentHashRing) buildRing() {
	c.hashRing = make(map[uint32]string)

	for _, shard := range c.shards {
		for i := 0; i < c.virtualKeys; i++ {
			// Create virtual key: "shard-id:virtual-key-index"
			virtualKey := fmt.Sprintf("%s:%d", shard, i)
			hash := hashConsistent(virtualKey)
			c.hashRing[hash] = shard
		}
	}
}

// GetShard returns the shard for a given key.
func (c *ConsistentHashRing) GetShard(key string) string {
	if len(c.hashRing) == 0 {
		return ""
	}

	hash := hashConsistent(key)

	// Find the first shard with hash >= key's hash
	for _, shard := range c.shards {
		for i := 0; i < c.virtualKeys; i++ {
			virtualKey := fmt.Sprintf("%s:%d", shard, i)
			vHash := hashConsistent(virtualKey)
			if vHash >= hash {
				return shard
			}
		}
	}

	// Wrap around to first shard
	return c.shards[0]
}

// AddShard adds a new shard to the ring.
func (c *ConsistentHashRing) AddShard(shard string) {
	// Check if already exists
	for _, s := range c.shards {
		if s == shard {
			return // Already in ring
		}
	}

	c.shards = append(c.shards, shard)
	sort.Strings(c.shards)
	c.buildRing()
}

// RemoveShard removes a shard from the ring.
func (c *ConsistentHashRing) RemoveShard(shard string) {
	newShards := make([]string, 0, len(c.shards)-1)
	for _, s := range c.shards {
		if s != shard {
			newShards = append(newShards, s)
		}
	}

	c.shards = newShards
	c.buildRing()
}

// GetAllShards returns all shard IDs in the ring.
func (c *ConsistentHashRing) GetAllShards() []string {
	result := make([]string, len(c.shards))
	copy(result, c.shards)
	return result
}

// StoreRegistry manages multiple knowledge stores.
type StoreRegistry struct {
	stores   map[string]*Store // shard ID → Store
	strategy ShardingStrategy
	ring     *ConsistentHashRing
}

// NewStoreRegistry creates a new store registry.
func NewStoreRegistry(strategy ShardingStrategy) *StoreRegistry {
	return &StoreRegistry{
		stores:   make(map[string]*Store),
		strategy: strategy,
	}
}

// AddStore registers a store for a shard.
func (r *StoreRegistry) AddStore(shardID string, store *Store) error {
	if store == nil {
		return fmt.Errorf("store is required")
	}

	r.stores[shardID] = store

	// Update consistent hash ring if needed
	if r.ring != nil {
		r.ring.AddShard(shardID)
	}

	return nil
}

// RemoveStore unregisters a store.
func (r *StoreRegistry) RemoveStore(shardID string) error {
	if _, exists := r.stores[shardID]; !exists {
		return fmt.Errorf("store not found: %s", shardID)
	}

	delete(r.stores, shardID)

	if r.ring != nil {
		r.ring.RemoveShard(shardID)
	}

	return nil
}

// GetStore returns the store for a message based on sharding strategy.
func (r *StoreRegistry) GetStore(source, classification, convID string) (*Store, string, error) {
	if len(r.stores) == 0 {
		return nil, "", fmt.Errorf("no stores registered")
	}

	shardKey := r.strategy.GetShardKey(source, classification, convID)

	// Simple modulo hashing if no consistent hash ring
	if r.ring == nil {
		shardID := r.strategy.GetShardID(shardKey, len(r.stores))
		shardIDs := r.GetShardIDs()
		if shardID < 0 || shardID >= len(shardIDs) {
			shardID %= len(shardIDs)
		}
		shard := shardIDs[shardID]
		store := r.stores[shard]
		return store, shard, nil
	}

	// Use consistent hashing
	shardID := r.ring.GetShard(shardKey)
	store := r.stores[shardID]
	if store == nil {
		return nil, "", fmt.Errorf("store not found for shard: %s", shardID)
	}

	return store, shardID, nil
}

// GetStores returns all registered stores.
func (r *StoreRegistry) GetStores() map[string]*Store {
	result := make(map[string]*Store)
	for k, v := range r.stores {
		result[k] = v
	}
	return result
}

// GetShardIDs returns all shard IDs in order.
func (r *StoreRegistry) GetShardIDs() []string {
	ids := make([]string, 0, len(r.stores))
	for id := range r.stores {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Close closes all stores in the registry.
func (r *StoreRegistry) Close() error {
	var lastErr error
	for _, store := range r.stores {
		if err := store.Close(); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// Helper functions

// hashShardKey computes a hash for shard key assignment.
//
// MD5 is deliberate and not a security decision: this picks a bucket, and
// nothing downstream treats the digest as a commitment or an identity. It is
// left as MD5 rather than modernised because the function *is* the placement --
// changing it re-hashes every key onto a different shard, which is a data
// migration, not a cleanup. gosec's G401/G501 flag the import, not a finding.
func hashShardKey(key string) int {
	h := md5.Sum([]byte(key))
	// Convert first 4 bytes to int
	val := int(h[0])<<24 | int(h[1])<<16 | int(h[2])<<8 | int(h[3])
	if val < 0 {
		val = -val
	}
	return val
}

// hashConsistent computes a hash for consistent hashing.
func hashConsistent(key string) uint32 {
	h := md5.Sum([]byte(key))
	// Convert first 4 bytes to uint32
	return uint32(h[0])<<24 | uint32(h[1])<<16 | uint32(h[2])<<8 | uint32(h[3])
}
