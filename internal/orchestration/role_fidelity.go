// role_fidelity.go ports roster/orchestration/src/role_fidelity.py: measures
// whether a role's *payload* survives contact with a given model.
//
// Two independent modes:
//
//   - Static (the default): needs no model and no network. Measures each
//     role's payload against a context budget -- a role whose brief does not
//     fit is not a fidelity question, it is arithmetic.
//   - Probe: sends each selected role's real system prompt plus a probe task
//     to an OpenAI-compatible /chat/completions endpoint and scores the
//     reply against deterministic, declarative checks from a probe file
//     (role-fidelity-probes.yaml).
//
// Probe mode is a screening instrument, not a certificate of correctness:
// the checks are keyword/structure assertions over reply text. They catch
// the failure mode that matters at small model sizes (the role's
// constraints stop shaping the output at all) and can be fooled by a reply
// that recites the right words while doing the wrong thing. No result from
// this harness may stand in for a human review, approve a gate, or accept a
// risk.
package orchestration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// DefaultCharsPerToken is a crude chars-per-token divisor, used because a
// real tokenizer would mean a dependency this tooling deliberately does not
// carry. Every decision it feeds is a coarse one; treat reported token
// counts as estimates, not exact.
const DefaultCharsPerToken = 4.0

// DefaultProbesFilename is role-fidelity-probes.yaml's standard name,
// resolved relative to this package's own source directory in the Python
// original; here it is resolved relative to a caller-supplied directory
// instead (see RunFidelityProbes's probesPath parameter).
const DefaultProbesFilename = "role-fidelity-probes.yaml"

// FidelityError is a configuration or input problem the caller can act on.
type FidelityError struct{ msg string }

func (e *FidelityError) Error() string { return e.msg }

func fidelityErrorf(format string, args ...any) error {
	return &FidelityError{msg: fmt.Sprintf(format, args...)}
}

// sharedPolicyMarkerRe marks where a generated brief stops being about the
// role and starts being the shared-policy block embedded verbatim into
// every one of them.
var sharedPolicyMarkerRe = regexp.MustCompile(`(?m)^# Shared policy: `)

// FidelityPreset is one role's shipped payload: what a dispatch actually
// sends.
type FidelityPreset struct {
	Name        string
	Path        string
	Frontmatter map[string]string
	Body        string
	Tier        string // Normalized via TierNormalizationMap at load time.
}

// Chars is the payload's raw character count.
func (p FidelityPreset) Chars() int { return len(p.Body) }

// Tokens estimates the payload's token count via a chars-per-token divisor.
func (p FidelityPreset) Tokens(charsPerToken float64) int {
	return int(roundHalfAwayFromZero(float64(p.Chars()) / charsPerToken))
}

// RoleSpecificChars is the character count before the first embedded
// shared-policy block. Falls back to the whole body when a brief carries no
// marker, so a hand-authored or differently-generated preset is counted as
// entirely role-specific rather than silently as zero.
func (p FidelityPreset) RoleSpecificChars() int {
	loc := sharedPolicyMarkerRe.FindStringIndex(p.Body)
	if loc == nil {
		return len(p.Body)
	}
	return loc[0]
}

// SharedPolicyChars is the character count of the embedded shared-policy
// block.
func (p FidelityPreset) SharedPolicyChars() int {
	return p.Chars() - p.RoleSpecificChars()
}

func roundHalfAwayFromZero(v float64) float64 {
	if v < 0 {
		return -roundHalfAwayFromZero(-v)
	}
	return float64(int64(v + 0.5))
}

func parsePresetFrontmatter(text string) (map[string]string, string) {
	if !strings.HasPrefix(text, "---") {
		return map[string]string{}, text
	}
	lines := strings.Split(text, "\n")
	fields := map[string]string{}
	for index := 1; index < len(lines); index++ {
		line := lines[index]
		if strings.TrimSpace(line) == "---" {
			body := strings.Join(lines[index+1:], "\n")
			body = strings.TrimLeft(body, "\n")
			return fields, body
		}
		key, value, found := strings.Cut(line, ":")
		if found {
			fields[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"`)
		}
	}
	// Unterminated frontmatter: treat the whole file as body rather than
	// silently consuming it as metadata.
	return map[string]string{}, text
}

// DefaultRunnerCapabilitiesPath best-effort locates runner-capabilities.json
// under repoRoot. Returns "" rather than erroring: the tier-vocabulary
// normalization it feeds is a correctness improvement over an unnormalized
// value, not a hard requirement to run the harness at all.
func DefaultRunnerCapabilitiesPath(repoRoot string) string {
	candidate := filepath.Join(repoRoot, "roster", "runner-capabilities.json")
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		return candidate
	}
	return ""
}

// TierNormalizationMap maps every raw tier spelling in use onto one
// canonical name. Source of truth is runner-capabilities.json's
// model_tiers block: its keys (opus/sonnet/haiku) are the canonical names,
// and each entry's cline_tier (high/mid/low) is that same tier's other
// spelling. An unrecognized raw value (including "unset") is not in the map
// and is left unchanged by the caller.
func TierNormalizationMap(runnerCapabilitiesPath string) map[string]string {
	mapping := map[string]string{}
	if runnerCapabilitiesPath == "" {
		return mapping
	}
	data, err := os.ReadFile(runnerCapabilitiesPath)
	if err != nil {
		return mapping
	}
	var parsed struct {
		ModelTiers map[string]struct {
			ClineTier string `json:"cline_tier"`
		} `json:"model_tiers"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return mapping
	}
	for canonical, info := range parsed.ModelTiers {
		canonical = strings.TrimSpace(canonical)
		if canonical == "" {
			continue
		}
		mapping[strings.ToLower(canonical)] = canonical
		if info.ClineTier != "" {
			mapping[strings.ToLower(strings.TrimSpace(info.ClineTier))] = canonical
		}
	}
	return mapping
}

func normalizeTier(frontmatter map[string]string, tierMap map[string]string) string {
	raw := frontmatter["modelTier"]
	if raw == "" {
		raw = frontmatter["model"]
	}
	if raw == "" {
		raw = "unset"
	}
	if canonical, ok := tierMap[strings.ToLower(strings.TrimSpace(raw))]; ok {
		return canonical
	}
	return raw
}

// DefaultFidelityPresetsDirs returns the standard preset directory
// candidates under repoRoot, tried in order.
//
// Unlike the Python original (which also tries locations relative to a
// packaged plugin checkout, since that module resolves paths relative to
// its own __file__), this only tries repoRoot-relative locations: a
// compiled Go binary carries no comparable notion of "the plugin checkout
// this binary was packaged from." Pass an explicit presetsDir to
// LoadFidelityPresets to bypass discovery entirely.
func DefaultFidelityPresetsDirs(repoRoot string) []string {
	return []string{
		filepath.Join(repoRoot, "cline-plugins", "cline-agents", "agents"),
		filepath.Join(repoRoot, "plugin", "agents"),
	}
}

// DefaultFidelityPresetsDir picks the first existing candidate from
// DefaultFidelityPresetsDirs.
func DefaultFidelityPresetsDir(repoRoot string) (string, error) {
	candidates := DefaultFidelityPresetsDirs(repoRoot)
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate, nil
		}
	}
	return "", fidelityErrorf(
		"could not locate a presets directory; pass an explicit one (looked in: %s)",
		strings.Join(candidates, ", "))
}

// LoadFidelityPresets loads every *.md preset in presetsDir, or exactly the
// presets named in roles (matched by filename stem) if roles is non-empty.
func LoadFidelityPresets(presetsDir string, roles []string, tierMap map[string]string) ([]FidelityPreset, error) {
	info, err := os.Stat(presetsDir)
	if err != nil || !info.IsDir() {
		return nil, fidelityErrorf("%s: not a directory", presetsDir)
	}
	wanted := map[string]bool{}
	for _, r := range roles {
		wanted[r] = true
	}

	entries, err := os.ReadDir(presetsDir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	var presets []FidelityPreset
	found := map[string]bool{}
	for _, name := range names {
		stem := strings.TrimSuffix(name, ".md")
		if len(wanted) > 0 && !wanted[stem] {
			continue
		}
		path := filepath.Join(presetsDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		fields, body := parsePresetFrontmatter(string(data))
		presetName := fields["name"]
		if presetName == "" {
			presetName = stem
		}
		presets = append(presets, FidelityPreset{
			Name:        presetName,
			Path:        path,
			Frontmatter: fields,
			Body:        body,
			Tier:        normalizeTier(fields, tierMap),
		})
		found[stem] = true
	}
	if len(wanted) > 0 {
		var missing []string
		for r := range wanted {
			if !found[r] {
				missing = append(missing, r)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			return nil, fidelityErrorf("no preset found for role(s): %s", strings.Join(missing, ", "))
		}
	}
	if len(presets) == 0 {
		return nil, fidelityErrorf("%s: contains no *.md presets", presetsDir)
	}
	return presets, nil
}

// ---------------------------------------------------------------------
// Static analysis -- payload size against a context budget
// ---------------------------------------------------------------------

// StaticFidelityRow is one preset's row in a static report.
type StaticFidelityRow struct {
	Role               string   `json:"role"`
	Tier               string   `json:"tier"`
	Chars              int      `json:"chars"`
	EstimatedTokens    int      `json:"estimated_tokens"`
	RoleSpecificTokens int      `json:"role_specific_tokens"`
	SharedPolicyTokens int      `json:"shared_policy_tokens"`
	Fits               bool     `json:"fits"`
	RoleSpecificFits   bool     `json:"role_specific_fits"`
	PercentOfUsable    *float64 `json:"percent_of_usable"`
}

// StaticFidelityReport is static_report's return shape.
type StaticFidelityReport struct {
	RoleSpecificOverBudgetCount int                 `json:"role_specific_over_budget_count"`
	MedianRoleSpecificTokens    int                 `json:"median_role_specific_tokens"`
	MedianSharedPolicyTokens    int                 `json:"median_shared_policy_tokens"`
	SharedPolicyShareOfTotal    *float64            `json:"shared_policy_share_of_total"`
	Mode                        string              `json:"mode"`
	ContextBudgetTokens         int                 `json:"context_budget_tokens"`
	ReserveTokens               int                 `json:"reserve_tokens"`
	UsableTokens                int                 `json:"usable_tokens"`
	CharsPerToken               float64             `json:"chars_per_token"`
	RoleCount                   int                 `json:"role_count"`
	OverBudgetCount             int                 `json:"over_budget_count"`
	LargestRole                 string              `json:"largest_role,omitempty"`
	MaxEstimatedTokens          int                 `json:"max_estimated_tokens"`
	MedianEstimatedTokens       int                 `json:"median_estimated_tokens"`
	TotalEstimatedTokens        int                 `json:"total_estimated_tokens"`
	Roles                       []StaticFidelityRow `json:"roles"`
}

func medianInt(values []int) int {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int{}, values...)
	sort.Ints(sorted)
	return sorted[len(sorted)/2]
}

// StaticFidelityReportFor measures each payload against the room a model
// actually leaves for it. reserveTokens is the part of the window the role
// brief may *not* use: the task, retrieved context, tool schemas, and the
// reply.
func StaticFidelityReportFor(presets []FidelityPreset, contextBudgetTokens, reserveTokens int, charsPerToken float64) StaticFidelityReport {
	usable := contextBudgetTokens - reserveTokens

	sorted := append([]FidelityPreset{}, presets...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Chars() > sorted[j].Chars() })

	rows := make([]StaticFidelityRow, 0, len(sorted))
	for _, preset := range sorted {
		tokens := preset.Tokens(charsPerToken)
		roleTokens := int(roundHalfAwayFromZero(float64(preset.RoleSpecificChars()) / charsPerToken))
		row := StaticFidelityRow{
			Role:               preset.Name,
			Tier:               preset.Tier,
			Chars:              preset.Chars(),
			EstimatedTokens:    tokens,
			RoleSpecificTokens: roleTokens,
			SharedPolicyTokens: tokens - roleTokens,
			Fits:               tokens <= usable,
			RoleSpecificFits:   roleTokens <= usable,
		}
		if usable > 0 {
			pct := roundTo(100.0*float64(tokens)/float64(usable), 1)
			row.PercentOfUsable = &pct
		}
		rows = append(rows, row)
	}

	overCount := 0
	roleSpecificOverCount := 0
	tokenCounts := make([]int, len(rows))
	roleOnlyCounts := make([]int, len(rows))
	sharedCounts := make([]int, len(rows))
	for i, r := range rows {
		if !r.Fits {
			overCount++
		}
		if !r.RoleSpecificFits {
			roleSpecificOverCount++
		}
		tokenCounts[i] = r.EstimatedTokens
		roleOnlyCounts[i] = r.RoleSpecificTokens
		sharedCounts[i] = r.SharedPolicyTokens
	}

	totalTokens := 0
	for _, t := range tokenCounts {
		totalTokens += t
	}
	totalShared := 0
	for _, s := range sharedCounts {
		totalShared += s
	}

	report := StaticFidelityReport{
		RoleSpecificOverBudgetCount: roleSpecificOverCount,
		MedianRoleSpecificTokens:    medianInt(roleOnlyCounts),
		MedianSharedPolicyTokens:    medianInt(sharedCounts),
		Mode:                        "static",
		ContextBudgetTokens:         contextBudgetTokens,
		ReserveTokens:               reserveTokens,
		UsableTokens:                usable,
		CharsPerToken:               charsPerToken,
		RoleCount:                   len(rows),
		OverBudgetCount:             overCount,
		MaxEstimatedTokens:          maxInt(tokenCounts),
		MedianEstimatedTokens:       medianInt(tokenCounts),
		TotalEstimatedTokens:        totalTokens,
		Roles:                       rows,
	}
	if totalTokens > 0 {
		share := roundTo(float64(totalShared)/float64(totalTokens), 3)
		report.SharedPolicyShareOfTotal = &share
	}
	if len(rows) > 0 {
		report.LargestRole = rows[0].Role
	}
	return report
}

func maxInt(values []int) int {
	m := 0
	for _, v := range values {
		if v > m {
			m = v
		}
	}
	return m
}

func roundTo(v float64, places int) float64 {
	mult := 1.0
	for i := 0; i < places; i++ {
		mult *= 10
	}
	return roundHalfAwayFromZero(v*mult) / mult
}
