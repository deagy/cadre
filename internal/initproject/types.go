// Package initproject ports roster/shared/src/init_project.py: guides a
// project through generating .agents/shared/<filename> overlays.
//
// cadre init walks a project through RG-A (stack/tooling opinions), RG-B
// (governance/autonomy narrowing), and RG-C (platform impact-profile
// guided fill-in) and writes the resulting overlays through a single,
// containment-checked write chokepoint (WriteOverlay in containment.go).
// It never invents a new config format: every file it writes is exactly
// the same .agents/shared/<filename> overlay internal/config's
// ResolveSharedConfig already knows how to resolve, and every generated
// overlay is validated by calling that same resolver against it,
// in-process, before success is reported.
//
// Defaults come first: a run with no answer source at all is legal and
// keeps every shipped default, planning no writes -- overlays are sparse,
// so "keep the default" means "write no overlay for that field," and a
// project with no overlay resolves to exactly the shipped values.
//
// Scope note (deliberate, not an oversight): this package ports
// init_project.py's full non-interactive surface (--answers, --set,
// --stack presets, defaults-mode, --dry-run/--force, --repair,
// --print-answers) faithfully, including every security property named
// below. It does NOT port init_project_interactive.py (445 lines, a
// separate Python module init_project.py itself only imports when
// --interactive is passed) -- that is the interactive questionnaire UI,
// scoped out per REMAINING_PYTHON_SCOPE.md's original Tier 0.4 note ("needs
// a Go terminal-prompt strategy decision... this repo has no existing
// interactive-CLI precedent to follow"). cadre init --interactive fails
// closed with a message pointing at --answers/--set instead of silently
// doing something different from the Python original.
//
// Security properties ported, named as in the Python original so a future
// reader can cross-reference this package against it:
//
//   - A-001: every generated overlay is validated by round-tripping it
//     through config.ResolveSharedConfig before success is reported.
//   - A-002: refuses to write into this suite's own checkout (or an
//     unrelated clone of the same suite), by filesystem identity, not path
//     string.
//   - A-004: idempotent re-runs merge with (never silently replace) an
//     existing overlay's content -- a manually edited field the current
//     run doesn't touch survives; a managed-block re-run merges with, not
//     replaces, prior addendum entries.
//   - A-005: fail-closed -- if planning ANY write finds a validation error,
//     NOTHING is written, for any file.
//   - A-006 rev 2: every field an answer set touches must carry a recorded
//     kept/overridden/deferred decision, or the run fails closed.
//   - B-002/B-003: agent-autonomy.yaml overlays reuse config's own
//     narrowing/allowlist check directly; this package never re-implements
//     or re-ranks autonomy values itself, and the fixed policy_version/
//     default_rule keys can never be set through cadre init.
//   - B-004: guardrail addenda are scanned against a denylist of
//     override/negation phrasing (a heuristic safety net, not a complete
//     solution) -- purely additive guardrails only.
//   - B-005/B-006: a field_decisions entry's declared category is never
//     trusted as the sole signal for whether a value must be redacted from
//     --print-answers output; ground truth computed directly from the real
//     fragment structure is unioned in (fail-safe OR).
//   - C-002: platform-impact-profile.yaml entries marked
//     applicability=applicable require a definition_reference and an
//     owner.
//   - C-004: the platform impact-category/BOM template list itself is
//     immutable; only per-key overrides on existing entries are accepted.
//   - THREAT-MODEL-HARDENING-4: every actual write is re-read through
//     ResolveSharedConfig and byte-compared against the intended content
//     before being reported as successful.
//   - Rejected/redacted values reach the audit log only as a sha256 hash,
//     never in cleartext -- see AuditEntry's Value/HashOnly handling.
package initproject

import (
	"fmt"

	"github.com/deagy/cadre/cli/internal/config"
)

// AutonomyFilename re-exports config.AutonomyFilename for local brevity.
const AutonomyFilename = config.AutonomyFilename

// AllSections are init_project.py's three question-group section ids.
var AllSections = []string{"rg-a-stack", "rg-b-governance", "rg-c-platform"}

const (
	TeamProfileFilename         = "team-profile.yaml"
	TechnologyStandardsFilename = "technology-standards.md"
	LibraryStandardsFilename    = "library-standards.yaml"
	GuardrailsFilename          = "cloud-guardrails.md"
	PlatformFilename            = "platform-impact-profile.yaml"
)

// PlatformApplicabilityValues are platform-impact-profile.yaml's only three
// recognized applicability values (C-002/finding-3).
var PlatformApplicabilityValues = []string{"applicable", "not-applicable", "unknown"}

// InitOverlayFilenames are every overlay filename cadre init can own.
var InitOverlayFilenames = []string{
	TeamProfileFilename, LibraryStandardsFilename, TechnologyStandardsFilename,
	AutonomyFilename, GuardrailsFilename, PlatformFilename,
}

// GuardrailsDenylist (B-004/THREAT-MODEL-HARDENING-2): a heuristic safety
// net, not a complete solution. Case-insensitive substring match against
// phrasing that would read as an override/negation of the global guardrail
// baseline rather than a genuinely additive project-specific guardrail.
var GuardrailsDenylist = []string{
	"does not apply", "is exempt", "overrides the above", "instead of",
	"replaces", "supersedes", "no longer applies",
}

const (
	ManagedStart = "<!-- agents-init:managed:start -->"
	ManagedEnd   = "<!-- agents-init:managed:end -->"
)

// ProseAddendumEntryMarker separates prior-run addendum entries inside
// technology-standards.md's managed block (finding 1): each cadre init run
// that supplies a new addendum appends it as its own entry rather than
// replacing whatever a prior run already wrote there.
const ProseAddendumEntryMarker = "<!-- agents-init:addendum-entry -->"

var FieldDecisionStatuses = []string{"kept", "overridden", "deferred"}
var FieldDecisionCategories = []string{"stack", "governance"}

// InitError reports a generated overlay, answer set, or write request that
// is invalid. Mirrors init_project.py's InitError.
type InitError struct{ msg string }

func (e *InitError) Error() string { return e.msg }

func initErrorf(format string, args ...any) error {
	return &InitError{msg: fmt.Sprintf(format, args...)}
}

// FieldDecision is one field_decisions[<path>] entry from an answer set.
type FieldDecision struct {
	Path        string
	Status      string
	Category    string
	SourceValue any
	NewValue    any
}

// PlannedWrite is one overlay this run intends to write, pending A-005
// fail-closed validation across every planned write.
type PlannedWrite struct {
	Filename string
	Section  string
	Category string
	Content  string
}

// InitResult accumulates plan_writes's output. See init_project.py's
// InitResult docstring (carried here) for why GovernanceTouchedPaths
// exists as an independent ground-truth signal for redaction.
type InitResult struct {
	Planned            []PlannedWrite
	RejectedGuardrails []RejectedGuardrail
	// (field_path, rejected_value) pairs (finding 2): Value is carried here
	// ONLY so the caller can log it as a hash via AuditEntry; it must never
	// be placed into an errors list or printed anywhere.
	RejectedAutonomy []RejectedAutonomy
	Written          []string
	AuditLogPath     string
	// System-derived ground truth for which dotted paths are actually
	// agent-autonomy.yaml leaves this run touched, computed in PlanWrites
	// directly from the real fragment structure -- independent of anything
	// the answer file merely claims about itself (e.g. a
	// field_decisions[<path>].Category label, which an answer-file author
	// fully controls and can mislabel). RedactAnswersForEcho unions this
	// set with the declared category (fail-safe OR) when deciding whether a
	// field_decisions entry's value must be redacted, so a mislabeled
	// category can never let a real governance leaf's raw value reach
	// --print-answers output.
	GovernanceTouchedPaths map[string]bool
}

// RejectedGuardrail is one guardrail bullet build_guardrails_overlay
// rejected, with the reason.
type RejectedGuardrail struct {
	Bullet string
	Reason string
}

// RejectedAutonomy is one agent-autonomy.yaml field this run rejected. See
// InitResult.RejectedAutonomy's docstring on why Value must never be
// printed raw.
type RejectedAutonomy struct {
	FieldPath string
	Value     any
}

// AutonomyOverlayRejected reports that an agent-autonomy.yaml overlay was
// rejected by config's narrowing/allowlist check (B-002). Deliberately does
// NOT interpolate the rejected value into its message: only the field path
// (never secret) and a hash of the value are ever shown.
type AutonomyOverlayRejected struct {
	FieldPath   string
	Value       any
	ValueSHA256 string
}

func (e *AutonomyOverlayRejected) Error() string {
	return fmt.Sprintf("autonomy overlay rejected for %s (see audit log for hash %s)", e.FieldPath, e.ValueSHA256)
}
