// profile_diff.go ports roster/orchestration/src/profile_diff.py: a
// read-only, information-only drift report between a consuming project's
// copy of this suite's provider profile (provider.json /
// profiles/<id>/profile.json) and this suite's own current release of
// those same two artifacts.
//
// Never writes to, re-syncs, or remediates anything belonging to a
// consuming project, and never reads or interprets that project's
// .agentic-sdlc/ gate-approval, human-authority, or risk-acceptance state
// -- only the project-supplied copy itself and (optionally) a
// caller-supplied ORIGINAL snapshot.
//
// Deviation from the Python original, deliberate and documented per this
// port's established convention: find_default_current_paths walks up from
// Python's own __file__ location, which a compiled Go binary has no
// equivalent of. This port walks up from os.Executable() (resolved through
// symlinks) instead, matching internal/orchestration/doctor.go's own
// existing "this binary's own location" convention -- the assumption in
// both cases is that a self-built binary runs from inside the checkout or
// package it was built from.
package orchestration

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
)

// Disclaimer is printed with every report, text and JSON alike.
const Disclaimer = "This report is drift information only. It is not an approval, gate-pass, " +
	"or compliance signal, and it does not read or reflect this project's " +
	"lifecycle/gate-approval state; see the consuming project's own " +
	".agentic-sdlc/ records and deagy/agentic-sdlc tooling for that."

// ProviderRequiredFields/ProfileRequiredFields are the minimum structural
// validity check a COPY must pass before any field comparison is
// attempted (PD-FR-4).
var (
	ProviderRequiredFields = []string{"id", "version"}
	ProfileRequiredFields  = []string{"id", "version", "agents"}
)

// Finding is one differing leaf/list-membership/dict-key, named by an
// approximate JSON-path-like field path (e.g.
// `kernel_compatibility.maximum_exclusive`, `agents[]`,
// `routing[].id="frontend".reviewers[]`).
type ProfileFinding struct {
	Path string
	Kind string // "changed" | "added" | "removed"
	Old  any
	New  any
}

// Render renders one finding line, matching Python's Finding.render().
func (f ProfileFinding) Render() string {
	switch f.Kind {
	case "added":
		return fmt.Sprintf("  - %s : added %s", f.Path, pyRepr(f.New))
	case "removed":
		return fmt.Sprintf("  - %s : removed %s", f.Path, pyRepr(f.Old))
	default:
		return fmt.Sprintf("  - %s : %s -> %s", f.Path, pyRepr(f.Old), pyRepr(f.New))
	}
}

// ArtifactResult is one artifact's (provider or profile) classification.
type ArtifactResult struct {
	Artifact                      string // "provider" | "profile"
	State                         string
	Findings                      []ProfileFinding
	Reason                        string
	HasReason                     bool
	OriginalDiffersFromCurrent    bool
	HasOriginalDiffersFromCurrent bool
	ComparedAs                    string // "current-vs-original" | "original-vs-copy" | ""
}

// ---------------------------------------------------------------------
// Field-level diffing (PD-FR-10, PD-FR-12)
// ---------------------------------------------------------------------

func allDictsWithID(items []any) bool {
	if len(items) == 0 {
		return false
	}
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			return false
		}
		if _, ok := m["id"].(string); !ok {
			return false
		}
	}
	return true
}

func allHashable(items []any) bool {
	for _, item := range items {
		switch item.(type) {
		case map[string]any, []any:
			return false
		}
	}
	return true
}

// DiffValues is a recursive, exhaustive (not first-match) structural diff
// over parsed-JSON values (map[string]any, []any, string, float64, bool,
// nil).
func DiffValues(old, new any, path string) []ProfileFinding {
	oldMap, oldIsMap := old.(map[string]any)
	newMap, newIsMap := new.(map[string]any)
	if oldIsMap && newIsMap {
		var findings []ProfileFinding
		keys := unionKeys(oldMap, newMap)
		for _, key := range keys {
			subPath := key
			if path != "" {
				subPath = path + "." + key
			}
			oldVal, oldHas := oldMap[key]
			newVal, newHas := newMap[key]
			switch {
			case !oldHas:
				findings = append(findings, ProfileFinding{subPath, "added", nil, newVal})
			case !newHas:
				findings = append(findings, ProfileFinding{subPath, "removed", oldVal, nil})
			default:
				findings = append(findings, DiffValues(oldVal, newVal, subPath)...)
			}
		}
		return findings
	}

	oldList, oldIsList := old.([]any)
	newList, newIsList := new.([]any)
	if oldIsList && newIsList {
		return diffLists(oldList, newList, path)
	}

	if !reflect.DeepEqual(old, new) {
		return []ProfileFinding{{path, "changed", old, new}}
	}
	return nil
}

func unionKeys(a, b map[string]any) []string {
	set := map[string]bool{}
	for k := range a {
		set[k] = true
	}
	for k := range b {
		set[k] = true
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func diffLists(old, new []any, path string) []ProfileFinding {
	if (allDictsWithID(old) || len(old) == 0) && (allDictsWithID(new) || len(new) == 0) {
		oldByID := map[string]any{}
		for _, item := range old {
			m := item.(map[string]any)
			oldByID[m["id"].(string)] = item
		}
		newByID := map[string]any{}
		for _, item := range new {
			m := item.(map[string]any)
			newByID[m["id"].(string)] = item
		}
		var ids []string
		seen := map[string]bool{}
		for id := range oldByID {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		for id := range newByID {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)

		var findings []ProfileFinding
		for _, id := range ids {
			itemPath := fmt.Sprintf("%s[].id=%q", path, id)
			oldVal, oldHas := oldByID[id]
			newVal, newHas := newByID[id]
			switch {
			case !oldHas:
				findings = append(findings, ProfileFinding{itemPath, "added", nil, newVal})
			case !newHas:
				findings = append(findings, ProfileFinding{itemPath, "removed", oldVal, nil})
			default:
				findings = append(findings, DiffValues(oldVal, newVal, itemPath)...)
			}
		}
		return findings
	}

	if allHashable(old) && allHashable(new) {
		return diffHashableLists(old, new, path)
	}

	// Fallback for lists this tool can't key or hash cleanly (e.g. lists
	// containing further nested lists) -- positional comparison.
	var findings []ProfileFinding
	max := len(old)
	if len(new) > max {
		max = len(new)
	}
	for i := 0; i < max; i++ {
		itemPath := fmt.Sprintf("%s[%d]", path, i)
		switch {
		case i >= len(old):
			findings = append(findings, ProfileFinding{itemPath, "added", nil, new[i]})
		case i >= len(new):
			findings = append(findings, ProfileFinding{itemPath, "removed", old[i], nil})
		default:
			findings = append(findings, DiffValues(old[i], new[i], itemPath)...)
		}
	}
	return findings
}

func diffHashableLists(old, new []any, path string) []ProfileFinding {
	oldKeys := map[string]any{}
	for _, v := range old {
		oldKeys[canonicalKey(v)] = v
	}
	newKeys := map[string]any{}
	for _, v := range new {
		newKeys[canonicalKey(v)] = v
	}
	listPath := path + "[]"

	var added, removed []string
	for k := range newKeys {
		if _, ok := oldKeys[k]; !ok {
			added = append(added, k)
		}
	}
	for k := range oldKeys {
		if _, ok := newKeys[k]; !ok {
			removed = append(removed, k)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)

	var findings []ProfileFinding
	for _, k := range added {
		findings = append(findings, ProfileFinding{listPath, "added", nil, newKeys[k]})
	}
	for _, k := range removed {
		findings = append(findings, ProfileFinding{listPath, "removed", oldKeys[k], nil})
	}
	return findings
}

func canonicalKey(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(data)
}

// pyRepr renders a value approximately the way Python's repr() would for
// the JSON scalar/collection types this module ever diffs -- close enough
// for a human-readable report line; not a byte-exact match to CPython's
// repr, the same documented simplification this port applies elsewhere to
// non-machine-parsed preview/report text.
func pyRepr(v any) string {
	switch val := v.(type) {
	case nil:
		return "None"
	case bool:
		if val {
			return "True"
		}
		return "False"
	case string:
		data, _ := json.Marshal(val)
		return string(data)
	case float64:
		if val == float64(int64(val)) {
			return fmt.Sprintf("%d", int64(val))
		}
		return fmt.Sprintf("%v", val)
	default:
		data, err := json.Marshal(val)
		if err != nil {
			return fmt.Sprintf("%v", val)
		}
		return string(data)
	}
}

// ---------------------------------------------------------------------
// Loading and structural validation (PD-FR-4)
// ---------------------------------------------------------------------

// LoadRequiredArtifact loads an artifact this tool cannot proceed without
// (CURRENT). Missing/unreadable/invalid content here is a CLI usage error,
// not a classification state.
func LoadRequiredArtifact(path, label string) (map[string]any, error) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return nil, fmt.Errorf("%s not found: %s", label, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s not found: %s", label, path)
	}
	var parsed any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON (%s): %w", label, path, err)
	}
	m, ok := parsed.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a JSON object (%s)", label, path)
	}
	return m, nil
}

// LoadOptionalArtifact loads ORIGINAL. Any failure is reported back as an
// unresolved reason string rather than an error -- an unresolved ORIGINAL
// is an expected, reportable condition (PD-FR-3), not a hard CLI error.
func LoadOptionalArtifact(path string) (map[string]any, string) {
	if path == "" {
		return nil, "no version-lock/original-snapshot reference was supplied"
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return nil, fmt.Sprintf("the referenced original snapshot could not be located: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Sprintf("the referenced original snapshot could not be located: %s", path)
	}
	var parsed any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Sprintf("the referenced original snapshot is not valid JSON (%s): %v", path, err)
	}
	m, ok := parsed.(map[string]any)
	if !ok {
		return nil, fmt.Sprintf("the referenced original snapshot is not a JSON object (%s)", path)
	}
	return m, ""
}

// ValidateCopy is PD-FR-4: COPY validity is checked independently of
// ORIGINAL/CURRENT resolution, and reported as copy-invalid rather than
// diverged when it fails.
func ValidateCopy(rawText string, requiredFields []string) (map[string]any, string) {
	var parsed any
	if err := json.Unmarshal([]byte(rawText), &parsed); err != nil {
		return nil, fmt.Sprintf("malformed JSON: %v", err)
	}
	m, ok := parsed.(map[string]any)
	if !ok {
		return nil, "copy is not a JSON object"
	}
	var missing []string
	for _, name := range requiredFields {
		if _, ok := m[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return nil, "missing required field(s): " + joinComma(missing)
	}
	return m, ""
}

func joinComma(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

// ---------------------------------------------------------------------
// Classification (PD-FR-5..PD-FR-9, PD-FR-11)
// ---------------------------------------------------------------------

// ClassifyArtifact resolves one artifact's drift state. Classification is
// first-match-wins: copy-invalid, provenance-undetermined, current,
// stale-unmodified, diverged.
func ClassifyArtifact(
	artifact, copyText string, current, original map[string]any, originalUnresolvedReason string,
	requiredFields []string,
) ArtifactResult {
	copy, invalidReason := ValidateCopy(copyText, requiredFields)
	if copy == nil {
		return ArtifactResult{Artifact: artifact, State: "copy-invalid", Reason: invalidReason, HasReason: true}
	}

	if original == nil {
		return ArtifactResult{
			Artifact: artifact, State: "provenance-undetermined",
			Reason: originalUnresolvedReason, HasReason: originalUnresolvedReason != "",
		}
	}

	// Equality for classification is deliberately derived from DiffValues
	// itself (empty findings == equal), not a blanket reflect.DeepEqual --
	// DiffValues/diffLists treat certain list fields as order-insensitive
	// membership/keyed-by-id comparisons. Deriving equality from the same
	// function that produces the findings makes a reordered-but-equivalent
	// list misclassifying as diverged structurally impossible.
	currentVsCopy := DiffValues(mapToAny(current), mapToAny(copy), "")
	if len(currentVsCopy) == 0 {
		return ArtifactResult{Artifact: artifact, State: "current"}
	}

	originalVsCopy := DiffValues(mapToAny(original), mapToAny(copy), "")
	if len(originalVsCopy) == 0 {
		findings := DiffValues(mapToAny(original), mapToAny(current), "")
		return ArtifactResult{
			Artifact: artifact, State: "stale-unmodified", Findings: findings, ComparedAs: "current-vs-original",
		}
	}

	originalVsCurrent := DiffValues(mapToAny(original), mapToAny(current), "")
	return ArtifactResult{
		Artifact: artifact, State: "diverged", Findings: originalVsCopy,
		OriginalDiffersFromCurrent: len(originalVsCurrent) > 0, HasOriginalDiffersFromCurrent: true,
		ComparedAs: "original-vs-copy",
	}
}

func mapToAny(m map[string]any) any {
	if m == nil {
		return nil
	}
	return m
}

// ---------------------------------------------------------------------
// Default CURRENT resolution
// ---------------------------------------------------------------------

// isCadreProvider guards against find_default_current_paths' walk-up
// landing on a coincidentally-named provider.json belonging to something
// other than this suite.
func isCadreProvider(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return false
	}
	id, _ := parsed["id"].(string)
	return id == "cadre"
}

// FindDefaultCurrentPaths locates this suite's own current
// provider.json/profile.json by walking up from this binary's own
// resolved location, so the same default works whether this binary is
// running from a source checkout (<repo>/provider/provider.json) or from
// inside an already-packaged plugin install (<plugin_root>/provider.json,
// <plugin_root>/profiles/<id>/profile.json). Returns ok=false if neither
// shape is found.
func FindDefaultCurrentPaths(profileID string) (providerPath, profilePath string, ok bool) {
	exe, err := os.Executable()
	if err != nil {
		return "", "", false
	}
	start := exe
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		start = resolved
	}
	current := filepath.Dir(start)
	for {
		packagedProvider := filepath.Join(current, "provider.json")
		if isCadreProvider(packagedProvider) {
			return packagedProvider, filepath.Join(current, "profiles", profileID, "profile.json"), true
		}
		checkoutProvider := filepath.Join(current, "provider", "provider.json")
		if isCadreProvider(checkoutProvider) {
			return checkoutProvider, filepath.Join(current, "provider", "profiles", profileID, "profile.json"), true
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", "", false
		}
		current = parent
	}
}

// ---------------------------------------------------------------------
// Run
// ---------------------------------------------------------------------

// ProfileDiffRequest bundles RunProfileDiff's inputs.
type ProfileDiffRequest struct {
	CopyProviderPath     string
	CopyProfilePath      string
	CurrentProviderPath  string
	CurrentProfilePath   string
	OriginalProviderPath string
	OriginalProfilePath  string
}

// RunProfileDiff computes both artifacts' results. Read-only: every path
// here is only ever opened for reading -- never written, deleted, or
// modified.
func RunProfileDiff(req ProfileDiffRequest) (map[string]ArtifactResult, error) {
	currentProvider, err := LoadRequiredArtifact(req.CurrentProviderPath, "current provider.json")
	if err != nil {
		return nil, err
	}
	currentProfile, err := LoadRequiredArtifact(req.CurrentProfilePath, "current profile.json")
	if err != nil {
		return nil, err
	}

	copyProviderText, err := readRequiredText(req.CopyProviderPath, "--copy-provider")
	if err != nil {
		return nil, err
	}
	copyProfileText, err := readRequiredText(req.CopyProfilePath, "--copy-profile")
	if err != nil {
		return nil, err
	}

	originalProvider, originalProviderReason := LoadOptionalArtifact(req.OriginalProviderPath)
	originalProfile, originalProfileReason := LoadOptionalArtifact(req.OriginalProfilePath)

	return map[string]ArtifactResult{
		"provider": ClassifyArtifact("provider", copyProviderText, currentProvider, originalProvider,
			originalProviderReason, ProviderRequiredFields),
		"profile": ClassifyArtifact("profile", copyProfileText, currentProfile, originalProfile,
			originalProfileReason, ProfileRequiredFields),
	}, nil
}

func readRequiredText(path, flagName string) (string, error) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", fmt.Errorf("%s not found: %s", flagName, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%s not found: %s", flagName, path)
		}
		return "", err
	}
	return string(data), nil
}

// ---------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------

// RenderArtifact renders one artifact's report lines, matching
// _render_artifact.
func RenderArtifact(name string, result ArtifactResult) []string {
	filename := "profile.json"
	if name == "provider" {
		filename = "provider.json"
	}
	lines := []string{fmt.Sprintf("%s: %s", filename, result.State)}
	if result.HasReason && result.Reason != "" {
		lines = append(lines, "  reason: "+result.Reason)
	}
	switch result.ComparedAs {
	case "current-vs-original":
		lines = append(lines, "  compared as: CURRENT vs ORIGINAL (what re-syncing this copy would change)")
	case "original-vs-copy":
		note := "  compared as: ORIGINAL vs COPY (what was locally changed since capture)"
		if result.HasOriginalDiffersFromCurrent && result.OriginalDiffersFromCurrent {
			note += "; this suite's CURRENT release has also changed since ORIGINAL was captured"
		} else {
			note += "; ORIGINAL still matches this suite's CURRENT release"
		}
		lines = append(lines, note)
	}
	if result.State == "current" {
		lines = append(lines, "  (copy matches this suite's current release; no findings)")
	}
	for _, finding := range result.Findings {
		lines = append(lines, finding.Render())
	}
	return lines
}

// ProfileDiffJSON is the --json output shape, matching _to_jsonable.
type ProfileDiffJSON struct {
	Disclaimer string                  `json:"disclaimer"`
	Artifacts  map[string]ArtifactJSON `json:"artifacts"`
	Overall    string                  `json:"overall"`
}

// ArtifactJSON is one artifact's JSON-rendered result.
type ArtifactJSON struct {
	State                      string        `json:"state"`
	Reason                     any           `json:"reason"`
	ComparedAs                 any           `json:"compared_as"`
	OriginalDiffersFromCurrent any           `json:"original_differs_from_current"`
	Findings                   []FindingJSON `json:"findings"`
}

// FindingJSON is one finding's JSON-rendered shape.
type FindingJSON struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
	Old  any    `json:"old"`
	New  any    `json:"new"`
}

// ToJSONable renders results into the same shape as _to_jsonable.
func ToJSONable(results map[string]ArtifactResult) ProfileDiffJSON {
	payload := ProfileDiffJSON{Disclaimer: Disclaimer, Artifacts: map[string]ArtifactJSON{}}
	allCurrent := true
	for name, result := range results {
		if result.State != "current" {
			allCurrent = false
		}
		findings := make([]FindingJSON, len(result.Findings))
		for i, f := range result.Findings {
			findings[i] = FindingJSON(f)
		}
		artifact := ArtifactJSON{State: result.State, Findings: findings}
		if result.HasReason {
			artifact.Reason = result.Reason
		}
		if result.ComparedAs != "" {
			artifact.ComparedAs = result.ComparedAs
		}
		if result.HasOriginalDiffersFromCurrent {
			artifact.OriginalDiffersFromCurrent = result.OriginalDiffersFromCurrent
		}
		payload.Artifacts[name] = artifact
	}
	payload.Overall = "drift"
	if allCurrent {
		payload.Overall = "current"
	}
	return payload
}

// AllCurrent reports whether every artifact resolved to "current" -- the
// process exit-code contract (0 means every compared artifact is current).
func AllCurrent(results map[string]ArtifactResult) bool {
	for _, r := range results {
		if r.State != "current" {
			return false
		}
	}
	return true
}
