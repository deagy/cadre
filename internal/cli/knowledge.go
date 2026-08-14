package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

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
  search         Search the knowledge store (stubbed for Phase 4.2)
  context        Retrieve agent context (stubbed for Phase 4.2)

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
	case "search":
		fmt.Fprintf(os.Stderr, "cadre knowledge search: not yet implemented (Phase 4.3+)\n")
		return 1
	case "context":
		fmt.Fprintf(os.Stderr, "cadre knowledge context: not yet implemented (Phase 4.3+)\n")
		return 1
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
