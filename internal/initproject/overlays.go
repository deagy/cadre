// overlays.go ports init_project.py's per-file overlay content builders:
// structured (YAML) deep-merge, Markdown managed-block addenda, the
// guardrails denylist scan, the agent-autonomy.yaml narrowing check, and
// platform-impact-profile.yaml's per-entry override logic.
package initproject

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/deagy/cadre/cli/internal/config"
	"gopkg.in/yaml.v3"
)

func dumpYAML(data map[string]any) (string, error) {
	out, err := yaml.Marshal(data)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// BuildStructuredOverlay deep-merges fragment over the EXISTING overlay
// file (not the global default), so a manually-edited field the current
// run's fragment doesn't touch survives untouched (A-004/C-006
// idempotency). Returns ("", nil, false) if there is nothing to write (no
// fragment and no existing file).
func BuildStructuredOverlay(targetRoot, filename string, fragment map[string]any) (content string, merged map[string]any, ok bool, err error) {
	existingText, hasExisting := readExistingOverlayText(targetRoot, filename)
	existing := map[string]any{}
	if hasExisting {
		if err := yaml.Unmarshal([]byte(existingText), &existing); err != nil {
			return "", nil, false, err
		}
		if existing == nil {
			existing = map[string]any{}
		}
	}
	if len(fragment) == 0 && !hasExisting {
		return "", nil, false, nil
	}
	merged = existing
	if len(fragment) > 0 {
		merged = config.DeepMergeJSON(existing, fragment)
	}
	content, err = dumpYAML(merged)
	if err != nil {
		return "", nil, false, err
	}
	return content, merged, true, nil
}

// ---------------------------------------------------------------------
// Markdown addenda.
// ---------------------------------------------------------------------

func replaceManagedBlock(existingText string, hasExisting bool, managedBody string) string {
	block := ManagedStart + "\n" + strings.TrimRight(managedBody, "\n \t") + "\n" + ManagedEnd
	if !hasExisting {
		return block + "\n"
	}
	if before, rest, found := strings.Cut(existingText, ManagedStart); found {
		if _, after, closed := strings.Cut(rest, ManagedEnd); closed {
			return before + block + after
		}
	}
	separator := ""
	if !strings.HasSuffix(existingText, "\n") {
		separator = "\n"
	}
	return existingText + separator + block + "\n"
}

// extractManagedBlockBody returns the raw text currently inside the
// managed block, or ("", false) if there is no existing text or no managed
// block yet. Used so a rebuild of the managed block can be merged with
// (rather than replace) whatever a prior run already wrote there (A-004).
func extractManagedBlockBody(existingText string, hasExisting bool) (string, bool) {
	if !hasExisting || existingText == "" {
		return "", false
	}
	if _, rest, found := strings.Cut(existingText, ManagedStart); found {
		if body, _, closed := strings.Cut(rest, ManagedEnd); closed {
			return strings.Trim(body, "\n"), true
		}
	}
	return "", false
}

func extractProseAddendumEntries(existingText string, hasExisting bool) []string {
	body, ok := extractManagedBlockBody(existingText, hasExisting)
	if !ok || body == "" {
		return nil
	}
	var entries []string
	for _, entry := range strings.Split(body, ProseAddendumEntryMarker) {
		entry = strings.TrimSpace(entry)
		if entry != "" {
			entries = append(entries, entry)
		}
	}
	return entries
}

// BuildProseAddendumOverlay appends addendumText as its own dated/labeled
// entry inside the managed block, merged with (never replacing) whatever
// addendum entries a prior run already wrote there (A-004/finding-1).
// Returns ("", false) if there is nothing to write.
func BuildProseAddendumOverlay(targetRoot, filename, addendumText string) (string, bool) {
	existingText, hasExisting := readExistingOverlayText(targetRoot, filename)
	if addendumText == "" && !hasExisting {
		return "", false
	}
	if addendumText == "" {
		return existingText, true
	}
	entries := extractProseAddendumEntries(existingText, hasExisting)
	found := false
	for _, e := range entries {
		if e == addendumText {
			found = true
			break
		}
	}
	if !found {
		entries = append(entries, addendumText)
	}
	body := strings.Join(entries, "\n\n"+ProseAddendumEntryMarker+"\n\n")
	return replaceManagedBlock(existingText, hasExisting, body), true
}

// ScanGuardrailBullet returns a rejection reason, or "" if bullet is
// acceptable.
func ScanGuardrailBullet(bullet string) string {
	lowered := strings.ToLower(bullet)
	for _, phrase := range GuardrailsDenylist {
		if strings.Contains(lowered, phrase) {
			return "contains override/negation phrasing (\"" + phrase + "\"); guardrail addenda must be " +
				"purely additive, not a relaxation of the global baseline"
		}
	}
	return ""
}

func extractGuardrailBullets(existingText string, hasExisting bool) []string {
	body, ok := extractManagedBlockBody(existingText, hasExisting)
	if !ok {
		return nil
	}
	var bullets []string
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- ") {
			bullets = append(bullets, trimmed[2:])
		}
	}
	return bullets
}

// BuildGuardrailsOverlay returns (content, ok, rejected) where rejected is
// every (bullet, reason) pair that failed the denylist scan. content/ok is
// ("", false) if there is nothing to write and no rejections either need
// surfacing.
//
// Bullets already present in the existing managed block are read back out
// and unioned with this run's newly accepted bullets (order-preserving,
// exact-duplicate deduped) rather than discarded, so a prior run's accepted
// bullet always survives a later run that doesn't re-supply it
// (A-004/finding-1).
func BuildGuardrailsOverlay(targetRoot string, bullets []string) (content string, ok bool, rejected []RejectedGuardrail) {
	var accepted []string
	for _, bullet := range bullets {
		if reason := ScanGuardrailBullet(bullet); reason != "" {
			rejected = append(rejected, RejectedGuardrail{Bullet: bullet, Reason: reason})
		} else {
			accepted = append(accepted, bullet)
		}
	}
	existingText, hasExisting := readExistingOverlayText(targetRoot, GuardrailsFilename)
	if len(accepted) == 0 && !hasExisting {
		return "", false, rejected
	}
	if len(accepted) == 0 {
		return existingText, true, rejected
	}
	merged := extractGuardrailBullets(existingText, hasExisting)
	for _, bullet := range accepted {
		found := false
		for _, m := range merged {
			if m == bullet {
				found = true
				break
			}
		}
		if !found {
			merged = append(merged, bullet)
		}
	}
	var body strings.Builder
	body.WriteString("## Project-specific additional guardrails\n\n")
	for i, b := range merged {
		if i > 0 {
			body.WriteString("\n")
		}
		body.WriteString("- " + b)
	}
	return replaceManagedBlock(existingText, hasExisting, body.String()), true, rejected
}

// ---------------------------------------------------------------------
// RG-B autonomy overlay -- B-002/B-003: reuses config's ranking/validation
// directly; this package never re-implements or re-ranks autonomy values
// itself.
// ---------------------------------------------------------------------

// AutonomyAllowedChoices returns every ranked value at or above (more
// restrictive than) defaultValue's rank -- the exhaustive, closed set
// cadre init may ever offer or accept for an agent-autonomy.yaml field. No
// free text is ever accepted.
func AutonomyAllowedChoices(defaultValue string) ([]string, error) {
	defaultRank, err := config.AutonomyRank("<candidate>", defaultValue)
	if err != nil {
		return nil, err
	}
	var all []rankedValue
	for value, rank := range config.AutonomyRestrictivenessRank {
		all = append(all, rankedValue{value, rank})
	}
	sortByRank(all)
	var out []string
	for _, e := range all {
		if e.rank >= defaultRank {
			out = append(out, e.value)
		}
	}
	return out, nil
}

type rankedValue struct {
	value string
	rank  int
}

func sortByRank(items []rankedValue) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j-1].rank > items[j].rank; j-- {
			items[j-1], items[j] = items[j], items[j-1]
		}
	}
}

func sha256Hex(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func identifyOffendingAutonomyField(base, fragment map[string]any) (string, any) {
	baseValues := map[string]any{}
	for _, leaf := range config.AutonomyLeafPaths(base) {
		baseValues[leaf.Path] = leaf.Value
	}
	fragmentLeaves := config.AutonomyLeafPaths(fragment)
	for _, leaf := range fragmentLeaves {
		defaultValue, ok := baseValues[leaf.Path]
		if !ok {
			return leaf.Path, leaf.Value
		}
		defaultRank, err1 := config.AutonomyRank(leaf.Path, defaultValue)
		overlayRank, err2 := config.AutonomyRank(leaf.Path, leaf.Value)
		if err1 != nil || err2 != nil {
			return leaf.Path, leaf.Value
		}
		if overlayRank < defaultRank {
			return leaf.Path, leaf.Value
		}
	}
	if len(fragmentLeaves) > 0 {
		return fragmentLeaves[0].Path, fragmentLeaves[0].Value
	}
	return "<agent-autonomy.yaml>", nil
}

// redactAutonomyOverlayError is the ONE place that converts a config
// overlay-narrowing error into a redacted *AutonomyOverlayRejected. There
// are two independent call sites that can each raise this error for the
// same autonomy overlay content (BuildAutonomyOverlay's own check, and the
// second, separate round-trip ValidateAutonomyOverlayContent performs);
// both route through this one function so neither can leak a raw value.
func redactAutonomyOverlayError(base, fragment map[string]any) *AutonomyOverlayRejected {
	fieldPath, offendingValue := identifyOffendingAutonomyField(base, fragment)
	return &AutonomyOverlayRejected{
		FieldPath:   fieldPath,
		Value:       offendingValue,
		ValueSHA256: sha256Hex(anyToString(offendingValue)),
	}
}

func anyToString(v any) string {
	if v == nil {
		return "None"
	}
	if s, ok := v.(string); ok {
		return s
	}
	if b, ok := v.(bool); ok {
		if b {
			return "True"
		}
		return "False"
	}
	return fmt.Sprintf("%v", v)
}

// BuildAutonomyOverlay merges fragment over the existing agent-autonomy.yaml
// overlay and validates the result narrows (never loosens) the shipped
// default, reusing config's own narrowing/allowlist check directly (B-002).
// Returns ("", nil, false, nil) if there is nothing to write.
func BuildAutonomyOverlay(targetRoot, sharedDefaultsDir string, fragment map[string]any) (content string, merged map[string]any, ok bool, err error) {
	for fixedKey := range config.AutonomyFixedKeys {
		if _, present := fragment[fixedKey]; present {
			return "", nil, false, initErrorf(
				"agent-autonomy.yaml field %q is fixed policy contract and may never be set through "+
					"`cadre init` (B-003)", fixedKey)
		}
	}
	existingText, hasExisting := readExistingOverlayText(targetRoot, AutonomyFilename)
	existing := map[string]any{}
	if hasExisting {
		_ = yaml.Unmarshal([]byte(existingText), &existing)
		if existing == nil {
			existing = map[string]any{}
		}
	}
	if len(fragment) == 0 && !hasExisting {
		return "", nil, false, nil
	}
	merged = existing
	if len(fragment) > 0 {
		merged = config.DeepMergeJSON(existing, fragment)
	}
	base, err := config.LoadStructured(sharedDefaultsPath(sharedDefaultsDir, AutonomyFilename))
	if err != nil {
		return "", nil, false, err
	}
	if err := config.CheckAutonomyOverlay(base, merged); err != nil {
		return "", nil, false, redactAutonomyOverlayError(base, fragment)
	}
	content, err = dumpYAML(merged)
	if err != nil {
		return "", nil, false, err
	}
	return content, merged, true, nil
}

// ValidateAutonomyOverlayContent applies the SAME redaction discipline as
// BuildAutonomyOverlay's own check to the second, independent validation
// PlanWrites also performs for the autonomy file.
func ValidateAutonomyOverlayContent(sharedDefaultsDir, content string, fragment map[string]any) error {
	err := ValidateOverlayContent(sharedDefaultsDir, AutonomyFilename, content)
	if err == nil {
		return nil
	}
	var overlayErr *config.OverlayError
	if errors.As(err, &overlayErr) {
		base, loadErr := config.LoadStructured(sharedDefaultsPath(sharedDefaultsDir, AutonomyFilename))
		if loadErr != nil {
			return loadErr
		}
		return redactAutonomyOverlayError(base, fragment)
	}
	return err
}

func sharedDefaultsPath(sharedDefaultsDir, filename string) string {
	return filepath.Join(sharedDefaultsDir, filename)
}
