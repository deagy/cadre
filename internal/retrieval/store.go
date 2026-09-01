package retrieval

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/deagy/recall/embedder"
	"github.com/deagy/recall/govern"
	"github.com/deagy/recall/store"
)

// Provider is the embedding provider cadre resolves from its config.
//
// Declared here rather than imported so this package does not depend on the
// engine being retired. Anything that turns text into vectors satisfies it.
type Provider interface {
	Embed(texts []string) ([][]float64, error)
}

// Request is a governed retrieval. Aliased rather than redeclared: a second
// struct with the same fields is the two-authorities-for-one-shape defect
// this consolidation keeps finding, and an alias cannot drift from govern's.
type Request = govern.Request

// Options describe which store a governed retrieval talks to and what
// produced the vectors in it.
type Options struct {
	// Database is the recall store file.
	Database string

	// Namespace is the recall namespace to read. Empty means "default".
	Namespace string

	// EmbedderName and Model identify what produced the vectors being
	// searched. Both reach every audit row.
	EmbedderName string
	Model        string

	// Dimensions is the embedding width the provider emits.
	Dimensions int

	// AuditPath is where retrieval rows are appended. Empty derives it from
	// the database path.
	AuditPath string

	// SkipIdentityCheck opens without requiring the store's recorded embedder
	// identity. Set only by `cadre knowledge init`, which is the command that
	// records it.
	SkipIdentityCheck bool
}

// Governed is a fail-closed retrieval view plus the resources behind it.
type Governed struct {
	*govern.Store

	Audit    *AuditLog
	Identity StoreIdentity
	store    *store.SQLiteStore
}

// Close releases the underlying store.
func (g *Governed) Close() error {
	if g == nil || g.store == nil {
		return nil
	}
	return g.store.Close()
}

// Open wires a recall store behind recall/govern.
//
// The refusals live in govern, not here. What this function owns is the part
// govern requires and cannot supply for itself: a recorder that actually
// persists, and an embedder identity that makes a recorded retrieval
// reproducible.
func Open(opts Options, provider Provider) (*Governed, error) {
	if strings.TrimSpace(opts.Database) == "" {
		return nil, fmt.Errorf("retrieval: no store configured; set \"database\" in the knowledge config")
	}
	if provider == nil {
		return nil, fmt.Errorf("retrieval: an embedding provider is required")
	}

	name, model, err := EmbedderIdentity(opts.EmbedderName, opts.Model, opts.Dimensions)
	if err != nil {
		return nil, err
	}

	// Checked before the store is opened, for the same reason the refusals
	// are: a mismatch discovered after connecting has already searched.
	if !opts.SkipIdentityCheck {
		if err := CheckIdentity(opts.Database, identityFor(name, model, opts.Dimensions)); err != nil {
			return nil, err
		}
	}

	auditPath := opts.AuditPath
	if auditPath == "" {
		auditPath = DefaultAuditPath(opts.Database)
	}
	audit, err := NewAuditLog(auditPath)
	if err != nil {
		return nil, err
	}

	namespace := opts.Namespace
	if namespace == "" {
		namespace = "default"
	}

	recallStore, err := store.NewSQLiteStore(store.Config{
		Namespace: namespace,
		Embedder:  &ProviderAdapter{provider: provider, dimensions: opts.Dimensions},
	}, opts.Database)
	if err != nil {
		return nil, fmt.Errorf("retrieval: cannot open store: %w", err)
	}

	governed, err := govern.New(recallStore, audit, name, model)
	if err != nil {
		_ = recallStore.Close()
		return nil, fmt.Errorf("retrieval: %w", err)
	}

	return &Governed{
		Store:    governed,
		Audit:    audit,
		Identity: identityFor(name, model, opts.Dimensions),
		store:    recallStore,
	}, nil
}

// DefaultAuditPath puts the retrieval log beside the store it describes.
func DefaultAuditPath(database string) string {
	return filepath.Join(filepath.Dir(database), "retrievals.jsonl")
}

// EmbedderIdentity resolves the pair recorded on every retrieval.
//
// A model is derived only for the local hashing embedder, which has no model
// name of its own but whose output width changes its vectors -- so the width
// is the identity. Every other provider must name its model in config: a
// retrieval recorded against "openai-compatible" with no model cannot be
// reproduced, which is the same hazard the engine refused a missing embedding
// provider for.
func EmbedderIdentity(name, model string, dimensions int) (string, string, error) {
	name = strings.TrimSpace(name)
	model = strings.TrimSpace(model)
	if name == "" {
		return "", "", fmt.Errorf(
			"embedding provider is required: set \"embedding.provider\" in the knowledge config")
	}
	if model == "" {
		if name != "local-hashing" {
			return "", "", fmt.Errorf(
				"embedding model is required: provider %q names no model; set \"embedding.model\" so a "+
					"recorded retrieval can be reproduced against the model that produced its vectors", name)
		}
		if dimensions <= 0 {
			return "", "", fmt.Errorf(
				"retrieval: local-hashing needs a positive \"embedding.dimensions\"; its width is its identity")
		}
		model = fmt.Sprintf("hashing-%dd", dimensions)
	}
	return name, model, nil
}

// ProviderAdapter presents a cadre embedding provider as recall's.
type ProviderAdapter struct {
	provider   Provider
	dimensions int
}

func (a *ProviderAdapter) Embed(ctx context.Context, text string) ([]float32, error) {
	vectors, err := a.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vectors) != 1 {
		return nil, fmt.Errorf("retrieval: embedder returned %d vectors for one text", len(vectors))
	}
	return vectors[0], nil
}

func (a *ProviderAdapter) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	vectors, err := a.provider.Embed(texts)
	if err != nil {
		return nil, err
	}
	if len(vectors) != len(texts) {
		return nil, fmt.Errorf("retrieval: embedder returned %d vectors for %d texts", len(vectors), len(texts))
	}
	out := make([][]float32, len(vectors))
	for i, vector := range vectors {
		converted := make([]float32, len(vector))
		for j, value := range vector {
			converted[j] = float32(value)
		}
		out[i] = converted
	}
	return out, nil
}

func (a *ProviderAdapter) Dimension() int { return a.dimensions }

var _ embedder.Embedder = (*ProviderAdapter)(nil)

// NewProviderAdapter presents a cadre embedding provider as recall's
// embedder. Exported for callers that need to seed or populate the same
// store cadre reads, so they embed with the identity cadre records.
func NewProviderAdapter(provider Provider, dimensions int) *ProviderAdapter {
	return &ProviderAdapter{provider: provider, dimensions: dimensions}
}
