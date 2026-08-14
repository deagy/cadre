package knowledge

import (
	"fmt"
	"sort"
	"time"
)

// EmbeddingModelVersion tracks a specific version of an embedding model.
type EmbeddingModelVersion struct {
	Name        string
	Version     string
	Dimensions  int
	Provider    string
	CreatedAt   time.Time
	Deprecated  bool
	DeprecatedAt *time.Time
}

// ModelVersionRegistry manages multiple versions of embedding models.
type ModelVersionRegistry struct {
	versions map[string]*EmbeddingModelVersion // name:version → EmbeddingModelVersion
	current  map[string]string                // model name → current version
}

// NewModelVersionRegistry creates a new model version registry.
func NewModelVersionRegistry() *ModelVersionRegistry {
	return &ModelVersionRegistry{
		versions: make(map[string]*EmbeddingModelVersion),
		current:  make(map[string]string),
	}
}

// RegisterVersion registers a new model version.
func (r *ModelVersionRegistry) RegisterVersion(mv *EmbeddingModelVersion) error {
	if mv == nil {
		return fmt.Errorf("model version is required")
	}

	if mv.Name == "" {
		return fmt.Errorf("model name is required")
	}

	if mv.Version == "" {
		return fmt.Errorf("version is required")
	}

	key := fmt.Sprintf("%s:%s", mv.Name, mv.Version)
	r.versions[key] = mv

	// Set as current if this is the first version or already set
	if r.current[mv.Name] == "" {
		r.current[mv.Name] = mv.Version
	}

	return nil
}

// SetCurrentVersion sets the current version for a model.
func (r *ModelVersionRegistry) SetCurrentVersion(modelName, version string) error {
	key := fmt.Sprintf("%s:%s", modelName, version)
	if _, exists := r.versions[key]; !exists {
		return fmt.Errorf("model version not found: %s", key)
	}

	r.current[modelName] = version
	return nil
}

// GetCurrentVersion returns the current version of a model.
func (r *ModelVersionRegistry) GetCurrentVersion(modelName string) (*EmbeddingModelVersion, error) {
	version, exists := r.current[modelName]
	if !exists {
		return nil, fmt.Errorf("model not found: %s", modelName)
	}

	key := fmt.Sprintf("%s:%s", modelName, version)
	mv, exists := r.versions[key]
	if !exists {
		return nil, fmt.Errorf("current version not found: %s", key)
	}

	return mv, nil
}

// GetVersion returns a specific model version.
func (r *ModelVersionRegistry) GetVersion(modelName, version string) (*EmbeddingModelVersion, error) {
	key := fmt.Sprintf("%s:%s", modelName, version)
	mv, exists := r.versions[key]
	if !exists {
		return nil, fmt.Errorf("model version not found: %s", key)
	}

	return mv, nil
}

// GetAllVersions returns all versions of a model.
func (r *ModelVersionRegistry) GetAllVersions(modelName string) ([]*EmbeddingModelVersion, error) {
	var versions []*EmbeddingModelVersion

	for key, mv := range r.versions {
		if fmt.Sprintf("%s:", modelName) == key[:len(modelName)+1] {
			versions = append(versions, mv)
		}
	}

	// Sort by creation time (descending)
	sort.Slice(versions, func(i, j int) bool {
		return versions[i].CreatedAt.After(versions[j].CreatedAt)
	})

	return versions, nil
}

// DeprecateVersion marks a model version as deprecated.
func (r *ModelVersionRegistry) DeprecateVersion(modelName, version string) error {
	key := fmt.Sprintf("%s:%s", modelName, version)
	mv, exists := r.versions[key]
	if !exists {
		return fmt.Errorf("model version not found: %s", key)
	}

	mv.Deprecated = true
	now := time.Now()
	mv.DeprecatedAt = &now

	// If this is current, switch to another version
	if r.current[modelName] == version {
		// Find latest non-deprecated version
		allVersions, _ := r.GetAllVersions(modelName)
		for _, v := range allVersions {
			if !v.Deprecated {
				r.current[modelName] = v.Version
				break
			}
		}
	}

	return nil
}

// MessageMutation tracks an edit or modification to a message.
type MessageMutation struct {
	ID              string
	MessageID       string
	MutationType    string    // "edit", "delete", "restore", "redact"
	FieldChanged    string    // Which field was changed
	OldValue        string    // Previous value
	NewValue        string    // New value
	MutatedBy       string    // User/system that made the change
	MutatedAt       time.Time
	Reason          string    // Why the mutation occurred
}

// MutationLog tracks all mutations for audit and recovery.
type MutationLog struct {
	mutations map[string]*MessageMutation // mutation ID → MessageMutation
	byMessage map[string][]*MessageMutation // message ID → mutations
}

// NewMutationLog creates a new mutation log.
func NewMutationLog() *MutationLog {
	return &MutationLog{
		mutations: make(map[string]*MessageMutation),
		byMessage: make(map[string][]*MessageMutation),
	}
}

// RecordMutation records a message mutation.
func (ml *MutationLog) RecordMutation(mutation *MessageMutation) error {
	if mutation == nil {
		return fmt.Errorf("mutation is required")
	}

	if mutation.MessageID == "" {
		return fmt.Errorf("message ID is required")
	}

	if mutation.MutationType == "" {
		return fmt.Errorf("mutation type is required")
	}

	// Generate mutation ID if not set
	if mutation.ID == "" {
		mutation.ID = fmt.Sprintf("mut-%d", time.Now().UnixNano())
	}

	ml.mutations[mutation.ID] = mutation
	ml.byMessage[mutation.MessageID] = append(ml.byMessage[mutation.MessageID], mutation)

	return nil
}

// GetMessageMutations returns all mutations for a message.
func (ml *MutationLog) GetMessageMutations(messageID string) []*MessageMutation {
	mutations := ml.byMessage[messageID]
	result := make([]*MessageMutation, len(mutations))
	copy(result, mutations)

	// Sort by time (ascending)
	sort.Slice(result, func(i, j int) bool {
		return result[i].MutatedAt.Before(result[j].MutatedAt)
	})

	return result
}

// GetMutation returns a specific mutation.
func (ml *MutationLog) GetMutation(mutationID string) (*MessageMutation, error) {
	mutation, exists := ml.mutations[mutationID]
	if !exists {
		return nil, fmt.Errorf("mutation not found: %s", mutationID)
	}

	return mutation, nil
}

// GetMutationsByType returns all mutations of a specific type.
func (ml *MutationLog) GetMutationsByType(mutationType string) []*MessageMutation {
	var result []*MessageMutation

	for _, mutation := range ml.mutations {
		if mutation.MutationType == mutationType {
			result = append(result, mutation)
		}
	}

	// Sort by time (descending)
	sort.Slice(result, func(i, j int) bool {
		return result[i].MutatedAt.After(result[j].MutatedAt)
	})

	return result
}

// GetMutationHistory returns mutations for a message in chronological order.
func (ml *MutationLog) GetMutationHistory(messageID string) []*MessageMutation {
	return ml.GetMessageMutations(messageID)
}

// CanRecoverMessage checks if a message can be recovered from mutations.
func (ml *MutationLog) CanRecoverMessage(messageID string) bool {
	mutations := ml.GetMessageMutations(messageID)

	// Can recover if there's a restore mutation or if all mutations are reversible
	for _, mutation := range mutations {
		if mutation.MutationType == "restore" {
			return true
		}
	}

	return len(mutations) > 0
}

// RecoverMessage reconstructs a message from mutation history.
func (ml *MutationLog) RecoverMessage(messageID string, targetTime time.Time) (map[string]string, error) {
	mutations := ml.GetMessageMutations(messageID)

	if len(mutations) == 0 {
		return nil, fmt.Errorf("no mutations found for message: %s", messageID)
	}

	// Start with empty message state
	state := make(map[string]string)

	// Apply mutations up to target time
	for _, mutation := range mutations {
		if mutation.MutatedAt.After(targetTime) {
			break // Stop after target time
		}

		switch mutation.MutationType {
		case "edit":
			state[mutation.FieldChanged] = mutation.NewValue
		case "delete":
			delete(state, mutation.FieldChanged)
		case "restore":
			state[mutation.FieldChanged] = mutation.OldValue
		case "redact":
			state[mutation.FieldChanged] = "[REDACTED]"
		}
	}

	return state, nil
}

// MutationStats provides statistics about mutations.
type MutationStats struct {
	TotalMutations   int64
	MutationsByType  map[string]int64
	MutationsByUser  map[string]int64
	AverageMutationAge time.Duration
}

// GetMutationStats returns statistics about all mutations.
func (ml *MutationLog) GetMutationStats() *MutationStats {
	stats := &MutationStats{
		TotalMutations:  int64(len(ml.mutations)),
		MutationsByType: make(map[string]int64),
		MutationsByUser: make(map[string]int64),
	}

	var totalAge time.Duration
	now := time.Now()

	for _, mutation := range ml.mutations {
		stats.MutationsByType[mutation.MutationType]++
		stats.MutationsByUser[mutation.MutatedBy]++
		totalAge += now.Sub(mutation.MutatedAt)
	}

	if stats.TotalMutations > 0 {
		stats.AverageMutationAge = time.Duration(int64(totalAge) / stats.TotalMutations)
	}

	return stats
}
