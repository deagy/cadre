package orchestration

import (
	"fmt"
	"log"
	"time"
)

// DeprecationWarning represents a deprecated feature warning.
type DeprecationWarning struct {
	Feature       string
	DeprecatedIn  string
	RemovedIn     string
	Replacement   string
	Message       string
	LastWarningAt time.Time
}

// DeprecationTracker tracks deprecated features and their usage.
type DeprecationTracker struct {
	warnings    map[string]*DeprecationWarning
	warnOnce    bool
	warnCount   int
	maxWarnings int
}

// NewDeprecationTracker creates a new deprecation tracker.
func NewDeprecationTracker(warnOnce bool, maxWarnings int) *DeprecationTracker {
	return &DeprecationTracker{
		warnings:    make(map[string]*DeprecationWarning),
		warnOnce:    warnOnce,
		maxWarnings: maxWarnings,
	}
}

// RegisterDeprecation registers a deprecated feature.
func (dt *DeprecationTracker) RegisterDeprecation(feature, deprecatedIn, removedIn, replacement, message string) {
	dt.warnings[feature] = &DeprecationWarning{
		Feature:      feature,
		DeprecatedIn: deprecatedIn,
		RemovedIn:    removedIn,
		Replacement:  replacement,
		Message:      message,
	}
}

// Warn emits a deprecation warning.
func (dt *DeprecationTracker) Warn(feature string) error {
	warning, exists := dt.warnings[feature]
	if !exists {
		return fmt.Errorf("unknown deprecated feature: %s", feature)
	}

	if dt.warnOnce && !warning.LastWarningAt.IsZero() {
		return nil
	}

	if dt.warnCount >= dt.maxWarnings {
		return fmt.Errorf("warning limit reached")
	}

	msg := fmt.Sprintf("DEPRECATED: %s (since %s, will be removed in %s) - %s. Use: %s",
		feature, warning.DeprecatedIn, warning.RemovedIn, warning.Message, warning.Replacement)
	log.Println(msg)

	warning.LastWarningAt = time.Now()
	dt.warnCount++

	return nil
}

// IsRemoved checks if a feature has been removed.
func (dt *DeprecationTracker) IsRemoved(feature, currentVersion string) bool {
	warning, exists := dt.warnings[feature]
	if !exists {
		return false
	}

	return currentVersion >= warning.RemovedIn
}

// MigrationGuide provides migration instructions for deprecated features.
type MigrationGuide struct {
	features map[string]*MigrationStep
}

// MigrationStep represents a migration step.
type MigrationStep struct {
	OldFeature   string
	NewFeature   string
	Instructions string
	Example      string
	Priority     int
}

// NewMigrationGuide creates a new migration guide.
func NewMigrationGuide() *MigrationGuide {
	return &MigrationGuide{
		features: make(map[string]*MigrationStep),
	}
}

// AddStep adds a migration step.
func (mg *MigrationGuide) AddStep(old, new, instructions, example string, priority int) {
	mg.features[old] = &MigrationStep{
		OldFeature:   old,
		NewFeature:   new,
		Instructions: instructions,
		Example:      example,
		Priority:     priority,
	}
}

// GetStep retrieves a migration step.
func (mg *MigrationGuide) GetStep(feature string) *MigrationStep {
	return mg.features[feature]
}

// ListSteps returns all migration steps sorted by priority.
func (mg *MigrationGuide) ListSteps() []*MigrationStep {
	steps := make([]*MigrationStep, 0, len(mg.features))
	for _, step := range mg.features {
		steps = append(steps, step)
	}

	// Simple priority sort (higher priority first)
	for i := 0; i < len(steps)-1; i++ {
		for j := i + 1; j < len(steps); j++ {
			if steps[j].Priority > steps[i].Priority {
				steps[i], steps[j] = steps[j], steps[i]
			}
		}
	}

	return steps
}

// CompatibilityMode provides backward compatibility layer.
type CompatibilityMode struct {
	enabled           bool
	deprecationTracker *DeprecationTracker
	legacyRouting     bool
	legacyFormat      string
}

// NewCompatibilityMode creates a new compatibility mode.
func NewCompatibilityMode(deprecationTracker *DeprecationTracker) *CompatibilityMode {
	return &CompatibilityMode{
		enabled:            true,
		deprecationTracker: deprecationTracker,
		legacyRouting:      false,
		legacyFormat:       "json",
	}
}

// EnableLegacyRouting enables legacy routing compatibility.
func (cm *CompatibilityMode) EnableLegacyRouting() {
	if cm.deprecationTracker != nil {
		cm.deprecationTracker.Warn("legacy-routing")
	}
	cm.legacyRouting = true
}

// UseLegacyFormat switches to legacy output format.
func (cm *CompatibilityMode) UseLegacyFormat(format string) {
	if cm.deprecationTracker != nil {
		cm.deprecationTracker.Warn("legacy-format")
	}
	cm.legacyFormat = format
}

// Disable disables compatibility mode.
func (cm *CompatibilityMode) Disable() {
	cm.enabled = false
}

// IsEnabled checks if compatibility mode is active.
func (cm *CompatibilityMode) IsEnabled() bool {
	return cm.enabled
}

// VersionMigration provides version-specific migration utilities.
type VersionMigration struct {
	from    string
	to      string
	steps   []*MigrationStep
}

// NewVersionMigration creates a new version migration.
func NewVersionMigration(from, to string) *VersionMigration {
	return &VersionMigration{
		from:  from,
		to:    to,
		steps: make([]*MigrationStep, 0),
	}
}

// AddMigrationStep adds a step to the version migration.
func (vm *VersionMigration) AddMigrationStep(step *MigrationStep) {
	vm.steps = append(vm.steps, step)
}

// GetMigrationPath returns the migration steps.
func (vm *VersionMigration) GetMigrationPath() []*MigrationStep {
	return vm.steps
}

// Validate checks if the migration is valid.
func (vm *VersionMigration) Validate() bool {
	if vm.from == "" || vm.to == "" {
		return false
	}

	return vm.from < vm.to
}
