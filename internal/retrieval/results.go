package retrieval

import (
	"strconv"
	"strings"

	"github.com/deagy/recall/core"
	"github.com/deagy/recall/index"
)

// Metadata keys the bundle reads off a chunk. They are named here rather
// than inline because they are a contract with whatever wrote the chunk: a
// store populated by `recall upload` carries whatever keys that ingest set,
// and a citation field with no key behind it is reported as empty rather
// than invented.
const (
	MetaSource         = "source"
	MetaClassification = "classification"
	MetaConversationID = "conversation_id"
	MetaConversation   = "conversation_title"
	MetaMessageID      = "message_id"
	MetaContentHash    = "content_hash"
	MetaCreatedAt      = "created_at"
	MetaRole           = "role"
	MetaInjectionRisk  = "untrusted_instruction_risk"
)

// ResultsFrom converts recall search results into bundle results.
//
// UntrustedInstructionRisk is read from chunk metadata and is false when no
// key is present. That is not a claim the passage is clean: the flag was
// produced by cadre's own ingest, which is retired, and `recall upload` does
// not compute one. Absence means unknown, which is why the bundle's
// requirements tell a reader to treat every result as untrusted regardless of
// the flag -- the flag can only ever add suspicion, never remove it.
func ResultsFrom(results []index.SearchResult) []Result {
	out := make([]Result, 0, len(results))
	for _, result := range results {
		if result.Chunk == nil {
			continue
		}
		meta := result.Chunk.Metadata
		citation := Citation{
			Source:         metaString(meta, MetaSource),
			ConversationID: metaString(meta, MetaConversationID),
			MessageID:      metaString(meta, MetaMessageID),
			ContentHash:    metaString(meta, MetaContentHash),
			Classification: metaString(meta, MetaClassification),
			ChunkID:        result.Chunk.ID,
		}
		if title := metaString(meta, MetaConversation); title != "" {
			citation.ConversationTitle = &title
		}
		if created := metaString(meta, MetaCreatedAt); created != "" {
			citation.CreatedAt = &created
		} else if !result.Chunk.CreatedAt.IsZero() {
			created := result.Chunk.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z")
			citation.CreatedAt = &created
		}
		ordinal := result.Chunk.ChunkIndex
		citation.ChunkOrdinal = &ordinal

		out = append(out, Result{
			Score:                    result.Score,
			Citation:                 citation,
			Role:                     metaString(meta, MetaRole),
			Content:                  result.Chunk.Content,
			UntrustedInstructionRisk: metaBool(meta, MetaInjectionRisk),
		})
	}
	return out
}

func metaString(meta map[string]core.Value, key string) string {
	value, ok := meta[key]
	if !ok || value == nil {
		return ""
	}
	return value.String()
}

func metaBool(meta map[string]core.Value, key string) bool {
	raw := metaString(meta, key)
	if raw == "" {
		return false
	}
	parsed, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	return parsed
}
