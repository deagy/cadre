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

// StoreRegistry manages multiple knowledge stores.
type StoreRegistry struct {
	stores   map[string]*Store // shard ID → Store
	strategy ShardingStrategy
}

// There is no consistent-hash ring here, deliberately.
//
// StoreRegistry used to carry `ring *ConsistentHashRing`, and NewStoreRegistry
// never set it -- so every ring branch was guarded by a field that was nil in
// every execution, and shard routing always took the modulo path below. The
// ring type itself was reachable from nothing at all. Both are gone; what
// remains is what ran.

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

	return nil
}

// RemoveStore unregisters a store.
func (r *StoreRegistry) RemoveStore(shardID string) error {
	if _, exists := r.stores[shardID]; !exists {
		return fmt.Errorf("store not found: %s", shardID)
	}

	delete(r.stores, shardID)

	return nil
}

// GetStore returns the store for a message based on sharding strategy.
func (r *StoreRegistry) GetStore(source, classification, convID string) (*Store, string, error) {
	if len(r.stores) == 0 {
		return nil, "", fmt.Errorf("no stores registered")
	}

	shardKey := r.strategy.GetShardKey(source, classification, convID)

	// Modulo hashing over the registered shards. This was the `r.ring == nil`
	// branch, and nothing ever set a ring, so it is simply what happens.
	shardID := r.strategy.GetShardID(shardKey, len(r.stores))
	shardIDs := r.GetShardIDs()
	if shardID < 0 || shardID >= len(shardIDs) {
		shardID %= len(shardIDs)
	}
	shard := shardIDs[shardID]
	return r.stores[shard], shard, nil
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
