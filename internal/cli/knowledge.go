package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/deagy/cadre/cli/internal/knowledge"
	"github.com/deagy/cadre/cli/internal/platform"
)

// KnowledgeCmd is the `cadre knowledge` subcommand.
func KnowledgeCmd(args []string) int {
	fs := flag.NewFlagSet("cadre knowledge", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: cadre knowledge <subcommand> [options]

Subcommands:
  init           Initialize or verify the knowledge store
  stats          Display knowledge store statistics
  ingest         Ingest messages into the knowledge store
  search         Search the knowledge store by content or vector similarity
  delete         Delete messages or run retention policies

Options:
`)
		fs.PrintDefaults()
	}

	configFlag := fs.String("config", "", "Path to knowledge store config (optional)")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	if fs.NArg() < 1 {
		fs.Usage()
		return 2
	}

	subcommand := fs.Arg(0)
	subArgs := fs.Args()[1:]

	// Find repository root for database path
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre: cannot get working directory: %v\n", err)
		return 1
	}

	repoRoot, err := platform.FindProjectRoot(wd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre: cannot find repository root: %v\n", err)
		return 1
	}

	// Determine database path
	dbPath := *configFlag
	if dbPath == "" {
		// Use default project-local store
		agentsDir := filepath.Join(repoRoot, ".agents", "knowledge-store")
		dbPath = filepath.Join(agentsDir, "store.db")
	}

	switch subcommand {
	case "init":
		return knowledgeInit(dbPath, subArgs)
	case "stats":
		return knowledgeStats(dbPath, subArgs)
	case "ingest":
		return knowledgeIngest(dbPath, subArgs)
	case "search":
		return knowledgeSearch(dbPath, subArgs)
	case "delete":
		return knowledgeDelete(dbPath, subArgs)
	case "help", "-h", "--help":
		fs.Usage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "cadre knowledge: unknown subcommand '%s'\n", subcommand)
		fs.Usage()
		return 1
	}
}

// knowledgeInit initializes or verifies the knowledge store.
func knowledgeInit(dbPath string, args []string) int {
	fs := flag.NewFlagSet("cadre knowledge init", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: cadre knowledge init [options]\n\nOptions:\n")
		fs.PrintDefaults()
	}

	verifyOnly := fs.Bool("verify", false, "Verify existing store without creating")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "cadre knowledge init: unexpected argument '%s'\n", fs.Arg(0))
		return 2
	}

	// Check if store already exists
	_, err := os.Stat(dbPath)
	storeExists := err == nil

	if *verifyOnly {
		if !storeExists {
			fmt.Fprintf(os.Stderr, "cadre knowledge init: store does not exist at %s\n", dbPath)
			return 1
		}

		// Try to open and verify
		store, err := knowledge.Open(dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cadre knowledge init: cannot open store: %v\n", err)
			return 1
		}
		defer store.Close()

		stats, err := store.Stats()
		if err != nil {
			fmt.Fprintf(os.Stderr, "cadre knowledge init: cannot get stats: %v\n", err)
			return 1
		}

		fmt.Printf("Knowledge store verified: %s\n", dbPath)
		fmt.Printf("  Messages: %d\n", stats.TotalMessages)
		fmt.Printf("  Chunks: %d\n", stats.TotalChunks)
		fmt.Printf("  Database size: %d bytes\n", stats.DatabaseSize)
		return 0
	}

	// Create or open store
	store, err := knowledge.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre knowledge init: cannot open store: %v\n", err)
		return 1
	}
	defer store.Close()

	if storeExists {
		fmt.Printf("Knowledge store already exists: %s\n", dbPath)
	} else {
		fmt.Printf("Knowledge store initialized: %s\n", dbPath)
	}

	// Show initial stats
	stats, err := store.Stats()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre knowledge init: cannot get stats: %v\n", err)
		return 1
	}

	fmt.Printf("  Schema version: 1.0\n")
	fmt.Printf("  Messages: %d\n", stats.TotalMessages)
	fmt.Printf("  Chunks: %d\n", stats.TotalChunks)

	return 0
}

// knowledgeStats displays statistics about the knowledge store.
func knowledgeStats(dbPath string, args []string) int {
	fs := flag.NewFlagSet("cadre knowledge stats", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: cadre knowledge stats [options]\n\nOptions:\n")
		fs.PrintDefaults()
	}

	jsonOutput := fs.Bool("json", false, "Output stats as JSON")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "cadre knowledge stats: unexpected argument '%s'\n", fs.Arg(0))
		return 2
	}

	// Check if store exists
	_, err := os.Stat(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre knowledge stats: store not found at %s\n", dbPath)
		return 1
	}

	// Open store
	store, err := knowledge.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre knowledge stats: cannot open store: %v\n", err)
		return 1
	}
	defer store.Close()

	// Get stats
	stats, err := store.Stats()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre knowledge stats: cannot get stats: %v\n", err)
		return 1
	}

	if *jsonOutput {
		// JSON output
		fmt.Printf(`{
  "total_messages": %d,
  "total_chunks": %d,
  "ingestion_runs": %d,
  "retrieval_runs": %d,
  "database_size_bytes": %d,
  "sources": %d,
  "classifications": %d,
  "embedding_models": %d
}
`, stats.TotalMessages, stats.TotalChunks, stats.IngestionRuns,
			stats.RetrievalRuns, stats.DatabaseSize,
			len(stats.Sources), len(stats.Classifications), len(stats.EmbeddingModels))
	} else {
		// Text output
		fmt.Printf("Knowledge Store Statistics\n")
		fmt.Printf("═══════════════════════════\n")
		fmt.Printf("\nStorage:\n")
		fmt.Printf("  Database location: %s\n", dbPath)
		fmt.Printf("  Database size: %d bytes\n", stats.DatabaseSize)

		fmt.Printf("\nMessages & Chunks:\n")
		fmt.Printf("  Total messages: %d\n", stats.TotalMessages)
		fmt.Printf("  Total chunks: %d\n", stats.TotalChunks)
		if stats.TotalMessages > 0 {
			avgChunks := float64(stats.TotalChunks) / float64(stats.TotalMessages)
			fmt.Printf("  Average chunks per message: %.1f\n", avgChunks)
		}

		fmt.Printf("\nOperations:\n")
		fmt.Printf("  Ingestion runs: %d\n", stats.IngestionRuns)
		fmt.Printf("  Retrieval runs: %d\n", stats.RetrievalRuns)

		if len(stats.Sources) > 0 {
			fmt.Printf("\nSources:\n")
			for source, count := range stats.Sources {
				fmt.Printf("  %s: %d messages\n", source, count)
			}
		}

		if len(stats.Classifications) > 0 {
			fmt.Printf("\nClassifications:\n")
			for classification, count := range stats.Classifications {
				fmt.Printf("  %s: %d messages\n", classification, count)
			}
		}

		if len(stats.EmbeddingModels) > 0 {
			fmt.Printf("\nEmbedding Models:\n")
			for model, count := range stats.EmbeddingModels {
				fmt.Printf("  %s: %d chunks\n", model, count)
			}
		}
	}

	return 0
}

// knowledgeIngest ingests messages into the knowledge store.
func knowledgeIngest(dbPath string, args []string) int {
	fs := flag.NewFlagSet("cadre knowledge ingest", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: cadre knowledge ingest [options]

Ingests messages from stdin (JSON) into the knowledge store.
Each line should be a JSON object with message fields.

Options:
`)
		fs.PrintDefaults()
	}

	source := fs.String("source", "", "Source identifier (required)")
	sourceURI := fs.String("source-uri", "", "Source URI (optional)")
	classification := fs.String("classification", "general", "Classification level")
	embeddingModel := fs.String("embedding", "local-hashing", "Embedding model (local-hashing or openai-compatible)")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *source == "" {
		fmt.Fprintf(os.Stderr, "cadre knowledge ingest: --source is required\n")
		return 2
	}

	// Open store
	store, err := knowledge.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre knowledge ingest: cannot open store: %v\n", err)
		return 1
	}
	defer store.Close()

	// Create embedder
	var embedder knowledge.EmbeddingProvider
	if *embeddingModel == "openai-compatible" {
		remoteEmbedder, err := knowledge.NewRemoteEmbedderFromEnv()
		if err != nil {
			fmt.Fprintf(os.Stderr, "cadre knowledge ingest: cannot create remote embedder: %v\n", err)
			return 1
		}
		embedder = remoteEmbedder
	} else {
		embedder = knowledge.NewLocalHashingEmbedder(128)
	}

	// Start ingestion run
	runID, err := store.BeginRun(*source, *sourceURI)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre knowledge ingest: cannot begin ingestion run: %v\n", err)
		return 1
	}

	// Read messages from stdin (JSON lines)
	var messageCount, chunkCount int
	decoder := json.NewDecoder(os.Stdin)

	for decoder.More() {
		var msgData map[string]interface{}
		if err := decoder.Decode(&msgData); err != nil {
			store.FailRun(runID, err)
			fmt.Fprintf(os.Stderr, "cadre knowledge ingest: cannot decode message: %v\n", err)
			return 1
		}

		// Extract message fields
		convID := getString(msgData, "conversation_id", "default-conversation")
		convTitle := getStringPtr(msgData, "conversation_title")
		srcMsgID := getString(msgData, "message_id", "")
		role := getString(msgData, "role", "user")
		content := getString(msgData, "content", "")

		if srcMsgID == "" || content == "" {
			continue // Skip incomplete messages
		}

		// Save message
		msgID, err := store.SaveMessage(
			*source, sourceURI, convID, convTitle, srcMsgID,
			role, content, nil, *classification, false,
			`[]`, `{}`, nil,
		)
		if err != nil {
			store.FailRun(runID, err)
			fmt.Fprintf(os.Stderr, "cadre knowledge ingest: cannot save message: %v\n", err)
			return 1
		}
		messageCount++

		// Embed and save chunk
		embeddings, err := embedder.Embed([]string{content})
		if err != nil {
			store.FailRun(runID, err)
			fmt.Fprintf(os.Stderr, "cadre knowledge ingest: cannot embed message: %v\n", err)
			return 1
		}

		if len(embeddings) > 0 {
			err = store.SaveChunk(msgID, 0, content, embedder.Name(), embedder.Model(), embeddings[0])
			if err != nil {
				store.FailRun(runID, err)
				fmt.Fprintf(os.Stderr, "cadre knowledge ingest: cannot save chunk: %v\n", err)
				return 1
			}
			chunkCount++
		}
	}

	// Complete ingestion run
	if err := store.CompleteRun(runID, messageCount, chunkCount); err != nil {
		fmt.Fprintf(os.Stderr, "cadre knowledge ingest: cannot complete ingestion run: %v\n", err)
		return 1
	}

	fmt.Printf("Ingested %d messages (%d chunks) from source '%s'\n", messageCount, chunkCount, *source)
	return 0
}

// knowledgeSearch searches the knowledge store.
func knowledgeSearch(dbPath string, args []string) int {
	fs := flag.NewFlagSet("cadre knowledge search", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: cadre knowledge search [options] <query>

Searches the knowledge store by vector similarity or text content.

Options:
`)
		fs.PrintDefaults()
	}

	classification := fs.String("classification", "", "Classification filter (required)")
	sources := fs.String("sources", "", "Comma-separated source filters (optional)")
	topK := fs.Int("top", 10, "Number of results to return")
	searchMode := fs.String("mode", "vector", "Search mode: vector or content")
	embeddingModel := fs.String("embedding", "local-hashing", "Embedding model for vector search")
	jsonOutput := fs.Bool("json", false, "Output results as JSON")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	if fs.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "cadre knowledge search: query is required\n")
		return 2
	}

	if *classification == "" {
		fmt.Fprintf(os.Stderr, "cadre knowledge search: --classification is required\n")
		return 2
	}

	query := fs.Arg(0)

	// Open store
	store, err := knowledge.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre knowledge search: cannot open store: %v\n", err)
		return 1
	}
	defer store.Close()

	if *searchMode == "content" {
		// Text search
		results, err := store.SearchByContent(query, *classification, *topK)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cadre knowledge search: cannot search by content: %v\n", err)
			return 1
		}

		if *jsonOutput {
			data := map[string]interface{}{
				"query":      query,
				"mode":       "content",
				"count":      len(results),
				"results":    results,
			}
			encoder := json.NewEncoder(os.Stdout)
			encoder.SetIndent("", "  ")
			encoder.Encode(data)
		} else {
			fmt.Printf("Content Search Results (%d)\n", len(results))
			fmt.Printf("═══════════════════════════════\n")
			for i, msg := range results {
				fmt.Printf("\n%d. %s (source: %s)\n", i+1, msg.ConversationID, msg.Source)
				fmt.Printf("   Role: %s\n", msg.Role)
				fmt.Printf("   Content: %s...\n", truncate(msg.Content, 100))
			}
		}
		return 0
	}

	// Vector search (default)
	var embedder knowledge.EmbeddingProvider
	if *embeddingModel == "openai-compatible" {
		remoteEmbedder, err := knowledge.NewRemoteEmbedderFromEnv()
		if err != nil {
			fmt.Fprintf(os.Stderr, "cadre knowledge search: cannot create remote embedder: %v\n", err)
			return 1
		}
		embedder = remoteEmbedder
	} else {
		embedder = knowledge.NewLocalHashingEmbedder(128)
	}

	sourceFilters := []string{}
	if *sources != "" {
		sourceFilters = strings.Split(*sources, ",")
		for i := range sourceFilters {
			sourceFilters[i] = strings.TrimSpace(sourceFilters[i])
		}
	}

	results, err := store.Search(knowledge.SearchOptions{
		Query:             query,
		Classification:    *classification,
		SourceFilters:     sourceFilters,
		AllSources:        *sources == "",
		EmbeddingProvider: embedder,
		Top:               *topK,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre knowledge search: cannot search: %v\n", err)
		return 1
	}

	if *jsonOutput {
		data := map[string]interface{}{
			"query":      query,
			"mode":       "vector",
			"count":      len(results),
			"results":    results,
		}
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		encoder.Encode(data)
	} else {
		fmt.Printf("Vector Search Results (%d)\n", len(results))
		fmt.Printf("═════════════════════════════\n")
		for i, result := range results {
			fmt.Printf("\n%d. %s (source: %s) - Similarity: %.4f\n",
				i+1, result.Message.ConversationID, result.Message.Source,
				result.CosineSimilarity)
			fmt.Printf("   Role: %s\n", result.Message.Role)
			fmt.Printf("   Content: %s...\n", truncate(result.Message.Content, 100))
			fmt.Printf("   Chunk ordinal: %d\n", result.Chunk.Ordinal)
		}
	}

	return 0
}

// knowledgeDelete deletes messages or runs retention policies.
func knowledgeDelete(dbPath string, args []string) int {
	fs := flag.NewFlagSet("cadre knowledge delete", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: cadre knowledge delete [options]

Deletes messages or runs retention policies.

Deletion modes:
  --expired               Delete messages past their retention_until date
  --classification <cls>  Delete all messages with given classification
  --source <src>          Delete all messages from given source
  --age <days>            Delete messages older than N days

Options:
`)
		fs.PrintDefaults()
	}

	deleteExpired := fs.Bool("expired", false, "Delete expired messages")
	classification := fs.String("classification", "", "Delete by classification")
	source := fs.String("source", "", "Delete by source")
	ageDays := fs.Int("age", 0, "Delete by age (days)")
	authorizedBy := fs.String("authorized-by", "cli-user", "Authorization user")
	jsonOutput := fs.Bool("json", false, "Output stats as JSON")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	// Validate that exactly one deletion mode is specified
	modeCount := 0
	if *deleteExpired {
		modeCount++
	}
	if *classification != "" {
		modeCount++
	}
	if *source != "" {
		modeCount++
	}
	if *ageDays > 0 {
		modeCount++
	}

	if modeCount != 1 {
		fmt.Fprintf(os.Stderr, "cadre knowledge delete: must specify exactly one deletion mode\n")
		return 2
	}

	// Open store
	store, err := knowledge.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre knowledge delete: cannot open store: %v\n", err)
		return 1
	}
	defer store.Close()

	var deleted int64

	if *deleteExpired {
		deleted, err = store.DeleteExpired(*authorizedBy)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cadre knowledge delete: cannot delete expired: %v\n", err)
			return 1
		}
	} else if *classification != "" {
		deleted, err = store.DeleteByClassification(*classification, "CLI deletion", *authorizedBy)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cadre knowledge delete: cannot delete by classification: %v\n", err)
			return 1
		}
	} else if *source != "" {
		deleted, err = store.DeleteBySource(*source, "CLI deletion", *authorizedBy)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cadre knowledge delete: cannot delete by source: %v\n", err)
			return 1
		}
	} else if *ageDays > 0 {
		deleted, err = store.DeleteByAge(*ageDays, nil, "CLI deletion", *authorizedBy)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cadre knowledge delete: cannot delete by age: %v\n", err)
			return 1
		}
	}

	if *jsonOutput {
		data := map[string]interface{}{
			"deleted": deleted,
			"authorized_by": *authorizedBy,
		}
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		encoder.Encode(data)
	} else {
		fmt.Printf("Deleted %d messages\n", deleted)
	}

	return 0
}

// Helper functions

func getString(data map[string]interface{}, key, defaultVal string) string {
	if val, ok := data[key]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return defaultVal
}

func getStringPtr(data map[string]interface{}, key string) *string {
	if val, ok := data[key]; ok {
		if str, ok := val.(string); ok {
			return &str
		}
	}
	return nil
}

func truncate(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen]
	}
	return s
}
