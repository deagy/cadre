package knowledge

import (
	"fmt"
	"sort"
	"sync"
)

// FederatedStore wraps a StoreRegistry to provide distributed operations.
type FederatedStore struct {
	registry *StoreRegistry
}

// NewFederatedStore creates a new federated store.
func NewFederatedStore(registry *StoreRegistry) *FederatedStore {
	return &FederatedStore{
		registry: registry,
	}
}

// FederatedSearchOptions extends SearchOptions with federation parameters.
type FederatedSearchOptions struct {
	SearchOptions
	ParallelShards int // Number of parallel shard queries (default: all shards)
	TimeoutMs      int // Timeout per shard query in milliseconds (0 = no timeout)
}

// FederatedSearchResult represents results from multiple shards.
type FederatedSearchResult struct {
	Results       []*SearchResult
	ShardResults  map[string][]*SearchResult // Shard ID → results from that shard
	ShardErrors   map[string]error           // Shard ID → error (if any)
	TotalQueried  int                        // Number of shards queried
	TotalFailed   int                        // Number of shards that failed
	TotalTime     int64                      // Total time in nanoseconds
}

// FederatedSearch performs a search across all shards in parallel.
// Results are aggregated and ranked by similarity.
func (f *FederatedStore) FederatedSearch(opts FederatedSearchOptions) (*FederatedSearchResult, error) {
	stores := f.registry.GetStores()
	if len(stores) == 0 {
		return nil, fmt.Errorf("no stores registered")
	}

	// Prepare shard IDs
	shardIDs := f.registry.GetShardIDs()
	parallelShards := opts.ParallelShards
	if parallelShards <= 0 || parallelShards > len(shardIDs) {
		parallelShards = len(shardIDs)
	}

	result := &FederatedSearchResult{
		ShardResults: make(map[string][]*SearchResult),
		ShardErrors:  make(map[string]error),
		TotalQueried: len(shardIDs),
	}

	// Parallel search across shards with semaphore
	sem := make(chan struct{}, parallelShards)
	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, shardID := range shardIDs {
		wg.Add(1)

		go func(shardID string) {
			defer wg.Done()

			// Acquire semaphore slot
			sem <- struct{}{}
			defer func() { <-sem }()

			store := stores[shardID]
			if store == nil {
				mu.Lock()
				result.ShardErrors[shardID] = fmt.Errorf("store not found for shard %s", shardID)
				result.TotalFailed++
				mu.Unlock()
				return
			}

			// Perform search on this shard
			shardResults, err := store.Search(opts.SearchOptions)
			if err != nil {
				mu.Lock()
				result.ShardErrors[shardID] = err
				result.TotalFailed++
				mu.Unlock()
				return
			}

			// Store results
			mu.Lock()
			result.ShardResults[shardID] = shardResults
			mu.Unlock()
		}(shardID)
	}

	wg.Wait()

	// Aggregate results from all shards
	aggregateSearchResults(result, opts.SearchOptions.Top)

	return result, nil
}

// FederatedDeleteOptions specifies deletion across shards.
type FederatedDeleteOptions struct {
	Mode          string // "expired", "classification", "source", "age"
	Classification *string
	Source        *string
	AgeDays       int
	AuthorizedBy  string
}

// FederatedDeleteResult summarizes deletions across shards.
type FederatedDeleteResult struct {
	TotalDeleted   int64              // Total messages deleted
	ShardDeleted   map[string]int64   // Shard ID → deleted count
	ShardErrors    map[string]error   // Shard ID → error (if any)
	TotalQueried   int                // Number of shards queried
	TotalFailed    int                // Number of shards that failed
}

// FederatedDelete performs a deletion operation across all shards.
func (f *FederatedStore) FederatedDelete(opts FederatedDeleteOptions) (*FederatedDeleteResult, error) {
	stores := f.registry.GetStores()
	if len(stores) == 0 {
		return nil, fmt.Errorf("no stores registered")
	}

	shardIDs := f.registry.GetShardIDs()

	result := &FederatedDeleteResult{
		ShardDeleted: make(map[string]int64),
		ShardErrors:  make(map[string]error),
		TotalQueried: len(shardIDs),
	}

	// Parallel deletion across shards
	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, shardID := range shardIDs {
		wg.Add(1)

		go func(shardID string) {
			defer wg.Done()

			store := stores[shardID]
			if store == nil {
				mu.Lock()
				result.ShardErrors[shardID] = fmt.Errorf("store not found for shard %s", shardID)
				result.TotalFailed++
				mu.Unlock()
				return
			}

			// Perform deletion on this shard based on mode
			var deleted int64
			var err error

			switch opts.Mode {
			case "expired":
				deleted, err = store.DeleteExpired(opts.AuthorizedBy)
			case "classification":
				if opts.Classification == nil {
					err = fmt.Errorf("classification required for classification mode")
					break
				}
				deleted, err = store.DeleteByClassification(*opts.Classification, "Federated delete", opts.AuthorizedBy)
			case "source":
				if opts.Source == nil {
					err = fmt.Errorf("source required for source mode")
					break
				}
				deleted, err = store.DeleteBySource(*opts.Source, "Federated delete", opts.AuthorizedBy)
			case "age":
				if opts.AgeDays <= 0 {
					err = fmt.Errorf("valid age in days required")
					break
				}
				deleted, err = store.DeleteByAge(opts.AgeDays, opts.Classification, "Federated delete", opts.AuthorizedBy)
			default:
				err = fmt.Errorf("unknown deletion mode: %s", opts.Mode)
			}

			mu.Lock()
			if err != nil {
				result.ShardErrors[shardID] = err
				result.TotalFailed++
			} else {
				result.ShardDeleted[shardID] = deleted
				result.TotalDeleted += deleted
			}
			mu.Unlock()
		}(shardID)
	}

	wg.Wait()

	return result, nil
}

// FederatedStats collects statistics from all shards.
type FederatedStats struct {
	TotalMessages       int64
	TotalChunks         int64
	TotalIngestionRuns  int64
	TotalRetrievalRuns  int64
	TotalDatabaseSize   int64
	ShardStats          map[string]*StoreStats
	ShardErrors         map[string]error
}

// FederatedStats collects statistics from all shards.
func (f *FederatedStore) FederatedStats() (*FederatedStats, error) {
	stores := f.registry.GetStores()
	if len(stores) == 0 {
		return nil, fmt.Errorf("no stores registered")
	}

	shardIDs := f.registry.GetShardIDs()

	result := &FederatedStats{
		ShardStats:  make(map[string]*StoreStats),
		ShardErrors: make(map[string]error),
	}

	// Parallel stats collection
	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, shardID := range shardIDs {
		wg.Add(1)

		go func(shardID string) {
			defer wg.Done()

			store := stores[shardID]
			if store == nil {
				mu.Lock()
				result.ShardErrors[shardID] = fmt.Errorf("store not found for shard %s", shardID)
				mu.Unlock()
				return
			}

			stats, err := store.Stats()
			mu.Lock()
			if err != nil {
				result.ShardErrors[shardID] = err
			} else {
				result.ShardStats[shardID] = stats
				result.TotalMessages += stats.TotalMessages
				result.TotalChunks += stats.TotalChunks
				result.TotalIngestionRuns += stats.IngestionRuns
				result.TotalRetrievalRuns += stats.RetrievalRuns
				result.TotalDatabaseSize += stats.DatabaseSize
			}
			mu.Unlock()
		}(shardID)
	}

	wg.Wait()

	return result, nil
}

// FederatedIngest ingests a message, routing it to the appropriate shard.
func (f *FederatedStore) FederatedIngest(source, classification, convID string, message *Message) error {
	if message == nil {
		return fmt.Errorf("message is required")
	}

	// Route message to appropriate shard based on sharding strategy
	store, shardID, err := f.registry.GetStore(source, classification, convID)
	if err != nil {
		return fmt.Errorf("cannot get store for routing: %w", err)
	}

	if store == nil {
		return fmt.Errorf("no store available for shard: %s", shardID)
	}

	// Save message to the shard
	_, err = store.SaveMessage(
		message.Source, message.SourceURI, message.ConversationID, message.ConversationTitle,
		message.SourceMessageID, message.Role, message.Content, message.CreatedAt,
		message.Classification, message.InjectionRisk,
		message.RedactionsJSON, message.MetadataJSON, message.RetentionUntil,
	)

	return err
}

// Helper functions

// aggregateSearchResults merges search results from all shards and ranks them.
func aggregateSearchResults(result *FederatedSearchResult, topK int) {
	if topK <= 0 {
		topK = 10
	}

	// Collect all results
	var allResults []*SearchResult
	for _, shardResults := range result.ShardResults {
		allResults = append(allResults, shardResults...)
	}

	// Sort by similarity (descending)
	sort.Slice(allResults, func(i, j int) bool {
		return allResults[i].CosineSimilarity > allResults[j].CosineSimilarity
	})

	// Limit to top K
	if len(allResults) > topK {
		allResults = allResults[:topK]
	}

	result.Results = allResults
}

// ShardingStats returns information about current sharding configuration.
type ShardingStats struct {
	TotalShards    int
	ActiveShards   int
	ShardStrategy  string
	Distribution   map[string]int64 // Shard ID → message count
}

// ShardingStats returns sharding configuration and distribution.
func (f *FederatedStore) ShardingStats() (*ShardingStats, error) {
	stores := f.registry.GetStores()
	shardIDs := f.registry.GetShardIDs()

	stats := &ShardingStats{
		TotalShards:   len(shardIDs),
		ActiveShards:  len(stores),
		ShardStrategy: f.registry.strategy.Name(),
		Distribution:  make(map[string]int64),
	}

	// Get message count per shard
	for shardID, store := range stores {
		if store == nil {
			continue
		}

		count, err := store.MessageCount()
		if err == nil {
			stats.Distribution[shardID] = count
		}
	}

	return stats, nil
}
