package executor

import (
	"encoding/json"
	"sort"
	"sync"

	"github.com/deagy/cadre/cli/internal/engine/state"
)

// Checkpoint is a run's persisted position.
//
// Pending is what makes resumption possible: the state alone cannot say
// whether a run stopped for a mutation-gate authorisation or for a particular
// gate's approval, and applying a decision to the wrong one would record a
// human's answer against a question they were not asked.
type Checkpoint struct {
	State   state.SDLCState `json:"state"`
	Pending *Suspension     `json:"pending"`
}

// Checkpointer persists runs between advances.
type Checkpointer interface {
	Load(taskID string) (Checkpoint, bool, error)
	Save(taskID string, checkpoint Checkpoint) error
	// Rewind drops a run's position so it can be re-run from a gate.
	Rewind(taskID string) error
}

// MemoryCheckpointer keeps runs in memory. Suitable for tests and one-shot
// CLI use; a service wants the persistent one.
type MemoryCheckpointer struct {
	mutex sync.Mutex
	runs  map[string][]byte
}

// NewMemoryCheckpointer builds an empty in-memory store.
func NewMemoryCheckpointer() *MemoryCheckpointer {
	return &MemoryCheckpointer{runs: map[string][]byte{}}
}

// Load returns a run's checkpoint.
func (m *MemoryCheckpointer) Load(taskID string) (Checkpoint, bool, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	encoded, found := m.runs[taskID]
	if !found {
		return Checkpoint{}, false, nil
	}
	// Round-tripped through JSON rather than shared by reference: a caller
	// mutating what it loaded must not alter the stored run, which is the one
	// behaviour a persistent store would not have.
	var checkpoint Checkpoint
	if err := json.Unmarshal(encoded, &checkpoint); err != nil {
		return Checkpoint{}, false, err
	}
	return checkpoint, true, nil
}

// Save writes a run's checkpoint.
func (m *MemoryCheckpointer) Save(taskID string, checkpoint Checkpoint) error {
	encoded, err := json.Marshal(checkpoint)
	if err != nil {
		return err
	}
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if m.runs == nil {
		m.runs = map[string][]byte{}
	}
	m.runs[taskID] = encoded
	return nil
}

// Rewind forgets a run.
func (m *MemoryCheckpointer) Rewind(taskID string) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	delete(m.runs, taskID)
	return nil
}

// --- small decoding helpers shared by the decision logic ------------------

func sortedSlots(outputs map[string]map[string]any) []string {
	slots := make([]string, 0, len(outputs))
	for slot := range outputs {
		slots = append(slots, slot)
	}
	// Map order is undefined, and preparer order reaches the run record, so
	// it is sorted rather than left to chance.
	sort.Strings(slots)
	return slots
}

func asString(value any) string {
	text, _ := value.(string)
	return text
}

func remarshal(value any, into any) bool {
	encoded, err := json.Marshal(value)
	if err != nil {
		return false
	}
	return json.Unmarshal(encoded, into) == nil
}

func decodeIdentity(value any) state.Identity {
	var identity state.Identity
	remarshal(value, &identity)
	return identity
}

func decodeIdentityPointer(value any) *state.Identity {
	if value == nil {
		return nil
	}
	var identity state.Identity
	if !remarshal(value, &identity) || identity.ID == "" {
		return nil
	}
	return &identity
}

func decodeArtifactBinding(value any) (state.ArtifactBinding, bool) {
	var binding state.ArtifactBinding
	if !remarshal(value, &binding) || binding.ArtifactID == "" {
		return binding, false
	}
	return binding, true
}

func decodeEvidence(value any) (state.Evidence, bool) {
	var evidence state.Evidence
	if !remarshal(value, &evidence) || evidence.EvidenceID == "" {
		return evidence, false
	}
	return evidence, true
}

func decodeEvidenceList(value any) []state.Evidence {
	var refs []state.Evidence
	remarshal(value, &refs)
	return refs
}

func orEmptyIdentities(values []state.Identity) []state.Identity {
	if values == nil {
		return []state.Identity{}
	}
	return values
}

func orEmptyBindings(values []state.ArtifactBinding) []state.ArtifactBinding {
	if values == nil {
		return []state.ArtifactBinding{}
	}
	return values
}

func orEmptyEvidence(values []state.Evidence) []state.Evidence {
	if values == nil {
		return []state.Evidence{}
	}
	return values
}
