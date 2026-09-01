package retrieval

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrNoRecordedIdentity: the store carries no record of what embedded it.
var ErrNoRecordedIdentity = errors.New("retrieval: the store's embedder identity has not been recorded")

// ErrIdentityMismatch: the configured embedder is not the one that produced
// the store's vectors.
var ErrIdentityMismatch = errors.New("retrieval: configured embedder does not match the store")

// StoreIdentity is what produced a store's vectors.
//
// recall's schema records chunks and their embeddings and nothing about what
// embedded them, so a store cannot say. Nothing about a query embedded by a
// different provider, model or width fails, and -- measured, not assumed --
// it does not come back empty either: recall's cosine similarity returns 0
// for vectors of different widths, so every chunk in scope is returned, all
// at score 0, in whatever order the index held them.
//
// That is worse than emptiness. The caller gets a full, plausible-looking
// result set with no relevance in it at all, and the audit row attributes it
// to an embedder that did not produce the vectors searched.
//
// So cadre records it beside the store, as a fact an operator states rather
// than one anybody verified. That is the same standing as classification and
// source scope: caller-asserted, authenticated by nobody, and required to be
// stated rather than assumed. What it buys is that a mismatch becomes a
// refusal instead of an empty result set.
type StoreIdentity struct {
	Embedder   string `json:"embedder"`
	Model      string `json:"model"`
	Dimensions int    `json:"dimensions"`
}

// IdentityPath is where a store's recorded identity lives.
func IdentityPath(database string) string {
	return filepath.Join(filepath.Dir(database), "embedder-identity.json")
}

// ReadIdentity returns the identity recorded for a store.
func ReadIdentity(database string) (StoreIdentity, error) {
	raw, err := os.ReadFile(IdentityPath(database))
	if os.IsNotExist(err) {
		return StoreIdentity{}, ErrNoRecordedIdentity
	}
	if err != nil {
		return StoreIdentity{}, fmt.Errorf("retrieval: cannot read the store's embedder identity: %w", err)
	}
	var identity StoreIdentity
	if err := json.Unmarshal(raw, &identity); err != nil {
		return StoreIdentity{}, fmt.Errorf(
			"retrieval: %s is not readable as an embedder identity: %w", IdentityPath(database), err)
	}
	return identity, nil
}

// WriteIdentity records what produced a store's vectors.
//
// Only `cadre knowledge init` calls this. A search that recorded the identity
// on first use would be asserting, on the operator's behalf, a fact it has no
// way to check -- and would do it silently, at exactly the moment the
// operator is least likely to notice being wrong.
func WriteIdentity(database string, identity StoreIdentity) error {
	path := IdentityPath(database)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("retrieval: cannot create the store directory: %w", err)
	}
	encoded, err := json.MarshalIndent(identity, "", "  ")
	if err != nil {
		return fmt.Errorf("retrieval: cannot encode the embedder identity: %w", err)
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
		return fmt.Errorf("retrieval: cannot record the embedder identity: %w", err)
	}
	return nil
}

// CheckIdentity refuses a configured embedder that is not the recorded one.
func CheckIdentity(database string, configured StoreIdentity) error {
	recorded, err := ReadIdentity(database)
	if err != nil {
		if errors.Is(err, ErrNoRecordedIdentity) {
			return fmt.Errorf(
				"%w: run `cadre knowledge init` to record that %s was embedded with %s / %s at %d "+
					"dimensions. Queried by any other embedder this store returns every chunk in "+
					"scope at score 0 -- a full result set with no relevance in it, and an audit "+
					"row naming the wrong embedder",
				ErrNoRecordedIdentity, database, configured.Embedder, configured.Model, configured.Dimensions)
		}
		return err
	}
	if recorded != configured {
		return fmt.Errorf(
			"%w: %s was embedded with %s / %s at %d dimensions, and this configuration would "+
				"query it with %s / %s at %d. Vectors from different embedders do not compare: "+
				"the search would return every chunk in scope at score 0 rather than fail",
			ErrIdentityMismatch,
			database, recorded.Embedder, recorded.Model, recorded.Dimensions,
			configured.Embedder, configured.Model, configured.Dimensions)
	}
	return nil
}

// identityFor builds the configured identity from resolved options.
func identityFor(name, model string, dimensions int) StoreIdentity {
	return StoreIdentity{
		Embedder:   strings.TrimSpace(name),
		Model:      strings.TrimSpace(model),
		Dimensions: dimensions,
	}
}
