// role_fidelity_client.go: the OpenAI-compatible chat client, probe-run
// orchestration, condensed-brief comparison, and attestation writing for
// role_fidelity.go's probe mode.
package orchestration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// FidelityChatClient is a minimal /chat/completions client. Speaks the
// OpenAI-compatible dialect Ollama, LM Studio, vLLM, llama.cpp's server and
// most hosted providers all speak.
type FidelityChatClient struct {
	BaseURL      string
	Model        string
	APIKey       string
	Timeout      time.Duration
	Temperature  float64
	MaxTokens    *int
	ExtraHeaders map[string]string
	HTTPClient   *http.Client // Overridable for tests; defaults to a per-call client with Timeout.
}

type fidelityChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type fidelityChatRequest struct {
	Model       string                `json:"model"`
	Temperature float64               `json:"temperature"`
	Messages    []fidelityChatMessage `json:"messages"`
	MaxTokens   *int                  `json:"max_tokens,omitempty"`
}

type fidelityChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// Complete sends systemPrompt/userPrompt and returns the reply content.
func (c *FidelityChatClient) Complete(systemPrompt, userPrompt string) (string, error) {
	url := trimRightSlash(c.BaseURL) + "/chat/completions"
	payload := fidelityChatRequest{
		Model:       c.Model,
		Temperature: c.Temperature,
		Messages: []fidelityChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		MaxTokens: c.MaxTokens,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fidelityErrorf("%s: cannot encode request: %v", url, err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fidelityErrorf("%s: cannot build request: %v", url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range c.ExtraHeaders {
		req.Header.Set(k, v)
	}
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	client := c.HTTPClient
	if client == nil {
		timeout := c.Timeout
		if timeout <= 0 {
			timeout = 120 * time.Second
		}
		client = &http.Client{Timeout: timeout}
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fidelityErrorf("%s: cannot reach endpoint: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fidelityErrorf("%s: unreadable response: %v", url, err)
	}
	if resp.StatusCode >= 300 {
		detail := string(respBody)
		if len(detail) > 500 {
			detail = detail[:500]
		}
		return "", fidelityErrorf("%s: HTTP %d: %s", url, resp.StatusCode, detail)
	}

	var parsed fidelityChatResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		preview := string(respBody)
		if len(preview) > 500 {
			preview = preview[:500]
		}
		return "", fidelityErrorf("%s: unexpected response shape: %s", url, preview)
	}
	if len(parsed.Choices) == 0 {
		preview := string(respBody)
		if len(preview) > 500 {
			preview = preview[:500]
		}
		return "", fidelityErrorf("%s: unexpected response shape: %s", url, preview)
	}
	return parsed.Choices[0].Message.Content, nil
}

func trimRightSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

// RetentionProbeID is the one probe id RunFidelityProbes treats specially:
// the harness's own reliability check for "can this model follow a format
// instruction at all." A verdict-scored probe's result is only informative
// about *policy* when the same role passed this one in the same run.
const RetentionProbeID = "instruction-retention-under-load"

// FidelityProbeRecord is one probe/role result within a FidelityProbeReport.
type FidelityProbeRecord struct {
	Role               string   `json:"role"`
	Tier               string   `json:"tier"`
	Probe              string   `json:"probe"`
	SystemPromptChars  int      `json:"system_prompt_chars"`
	DegenerateKeywords []string `json:"degenerate_keywords,omitempty"`
	DryRun             bool     `json:"dry_run,omitempty"`
	Errored            bool     `json:"errored,omitempty"`
	Error              string   `json:"error,omitempty"`
	Reply              *string  `json:"reply,omitempty"`
	Passed             *bool    `json:"passed,omitempty"`
	Failures           []string `json:"failures,omitempty"`
	WordCount          *int     `json:"word_count,omitempty"`
	Verdict            *string  `json:"verdict,omitempty"`
	VerdictOutcome     string   `json:"verdict_outcome,omitempty"`
	VerdictReliable    *bool    `json:"verdict_reliable,omitempty"`
	Arm                string   `json:"arm,omitempty"`
}

// FidelityProbeReport is RunFidelityProbes's return shape.
type FidelityProbeReport struct {
	Mode            string                    `json:"mode"`
	Model           string                    `json:"model,omitempty"`
	BaseURL         string                    `json:"base_url,omitempty"`
	DryRun          bool                      `json:"dry_run"`
	ProbeCount      int                       `json:"probe_count"`
	RoleCount       int                       `json:"role_count"`
	Answered        int                       `json:"answered"`
	Scored          int                       `json:"scored"`
	Passed          int                       `json:"passed"`
	Failed          int                       `json:"failed"`
	Errored         int                       `json:"errored"`
	Unreliable      int                       `json:"unreliable"`
	PassRate        *float64                  `json:"pass_rate"`
	Coverage        *float64                  `json:"coverage"`
	ZeroMatchProbes []string                  `json:"zero_match_probes"`
	ByRole          map[string]map[string]int `json:"by_role"`
	ByTier          map[string]map[string]int `json:"by_tier"`
	Results         []FidelityProbeRecord     `json:"results"`
}

// RunFidelityProbes runs every applicable probe against every preset.
// client == nil is a dry run: it reports exactly what would be sent, with
// no network call.
func RunFidelityProbes(presets []FidelityPreset, probes []FidelityProbe, client *FidelityChatClient, warnDegenerate bool) FidelityProbeReport {
	var results []FidelityProbeRecord
	matchedCounts := map[string]int{}
	for _, p := range probes {
		matchedCounts[p.ID] = 0
	}

	for _, preset := range presets {
		for _, probe := range probes {
			if !probe.Applies(preset) {
				continue
			}
			matchedCounts[probe.ID]++
			record := FidelityProbeRecord{
				Role:              preset.Name,
				Tier:              preset.Tier,
				Probe:             probe.ID,
				SystemPromptChars: preset.Chars(),
			}
			if warnDegenerate {
				if overlap := DegenerateFidelityKeywords(probe, preset); len(overlap) > 0 {
					record.DegenerateKeywords = overlap
				}
			}
			if client == nil {
				record.DryRun = true
				results = append(results, record)
				continue
			}
			reply, err := client.Complete(preset.Body, probe.Prompt)
			if err != nil {
				record.Errored = true
				record.Error = err.Error()
				results = append(results, record)
				continue
			}
			score := ScoreFidelityReply(probe, reply)
			passed := score.Passed
			wc := score.WordCount
			record.Passed = &passed
			record.Failures = score.Failures
			record.WordCount = &wc
			record.Verdict = score.Verdict
			record.VerdictOutcome = score.VerdictOutcome
			record.Reply = &reply
			results = append(results, record)
		}
	}

	// Retention-coupling: verdict scoring only measures policy-following
	// when the model can follow a reply-format instruction at all, and
	// RetentionProbeID is exactly the probe that measures that,
	// independently, in the same run. A role that failed it and then
	// produced no gradeable verdict has told this harness nothing about
	// whether it would have refused or proceeded.
	probesByID := map[string]FidelityProbe{}
	for _, p := range probes {
		probesByID[p.ID] = p
	}
	retentionFailedRoles := map[string]bool{}
	for _, r := range results {
		if r.Probe == RetentionProbeID && r.Passed != nil && !*r.Passed {
			retentionFailedRoles[r.Role] = true
		}
	}
	for i := range results {
		probeObj, ok := probesByID[results[i].Probe]
		if ok && probeObj.ExpectVerdict != "" && results[i].Passed != nil {
			reliable := !retentionFailedRoles[results[i].Role]
			results[i].VerdictReliable = &reliable
		}
	}

	var answered, scored, unreliable, passed []FidelityProbeRecord
	for _, r := range results {
		if r.Passed != nil {
			answered = append(answered, r)
			if r.VerdictReliable == nil || *r.VerdictReliable {
				scored = append(scored, r)
				if *r.Passed {
					passed = append(passed, r)
				}
			} else {
				unreliable = append(unreliable, r)
			}
		}
	}

	byRole := map[string]map[string]int{}
	byTier := map[string]map[string]int{}
	for _, r := range scored {
		roleBucket, ok := byRole[r.Role]
		if !ok {
			roleBucket = map[string]int{"passed": 0, "failed": 0}
			byRole[r.Role] = roleBucket
		}
		tierBucket, ok := byTier[r.Tier]
		if !ok {
			tierBucket = map[string]int{"passed": 0, "failed": 0}
			byTier[r.Tier] = tierBucket
		}
		key := "failed"
		if *r.Passed {
			key = "passed"
		}
		roleBucket[key]++
		tierBucket[key]++
	}

	var errored []FidelityProbeRecord
	for _, r := range results {
		if r.Errored {
			errored = append(errored, r)
		}
	}
	var zeroMatch []string
	for id, count := range matchedCounts {
		if count == 0 {
			zeroMatch = append(zeroMatch, id)
		}
	}
	sort.Strings(zeroMatch)

	report := FidelityProbeReport{
		Mode:            "probe",
		DryRun:          client == nil,
		ProbeCount:      len(probes),
		RoleCount:       len(presets),
		Answered:        len(answered),
		Scored:          len(scored),
		Passed:          len(passed),
		Failed:          len(scored) - len(passed),
		Errored:         len(errored),
		Unreliable:      len(unreliable),
		ZeroMatchProbes: zeroMatch,
		ByRole:          byRole,
		ByTier:          byTier,
		Results:         results,
	}
	if client != nil {
		report.Model = client.Model
		report.BaseURL = client.BaseURL
	}
	if len(scored) > 0 {
		rate := roundTo(float64(len(passed))/float64(len(scored)), 3)
		report.PassRate = &rate
	}
	if len(results) > 0 {
		cov := roundTo(float64(len(answered))/float64(len(results)), 3)
		report.Coverage = &cov
	}
	return report
}

// CondensedFidelityBody is the role-specific part of a preset's payload,
// dropping the embedded shared-policy block.
func CondensedFidelityBody(preset FidelityPreset) string {
	return preset.Body[:preset.RoleSpecificChars()]
}

// FidelityRoleDelta is one role's full-vs-condensed comparison.
type FidelityRoleDelta struct {
	FullPassRate             *float64 `json:"full_pass_rate"`
	CondensedPassRate        *float64 `json:"condensed_pass_rate"`
	Delta                    *float64 `json:"delta"`
	SharedPolicyCharsDropped int      `json:"shared_policy_chars_dropped"`
}

// FidelityCondensedComparisonReport is RunFidelityCondensedComparison's
// return shape.
type FidelityCondensedComparisonReport struct {
	Mode        string                       `json:"mode"`
	Model       string                       `json:"model,omitempty"`
	BaseURL     string                       `json:"base_url,omitempty"`
	DryRun      bool                         `json:"dry_run"`
	PassRate    *float64                     `json:"pass_rate"`
	Coverage    *float64                     `json:"coverage"`
	Full        FidelityProbeReport          `json:"full"`
	Condensed   FidelityProbeReport          `json:"condensed"`
	ByRoleDelta map[string]FidelityRoleDelta `json:"by_role_delta"`
}

// RunFidelityCondensedComparison runs every probe against both a role's
// full brief and a condensed, role-specific-only variant, on identical
// probes, so the two are directly comparable.
func RunFidelityCondensedComparison(presets []FidelityPreset, probes []FidelityProbe, client *FidelityChatClient, warnDegenerate bool) FidelityCondensedComparisonReport {
	condensedPresets := make([]FidelityPreset, len(presets))
	for i, p := range presets {
		condensedPresets[i] = FidelityPreset{Name: p.Name, Path: p.Path, Frontmatter: p.Frontmatter, Body: CondensedFidelityBody(p), Tier: p.Tier}
	}

	fullReport := RunFidelityProbes(presets, probes, client, warnDegenerate)
	condensedReport := RunFidelityProbes(condensedPresets, probes, client, warnDegenerate)
	for i := range fullReport.Results {
		fullReport.Results[i].Arm = "full"
	}
	for i := range condensedReport.Results {
		condensedReport.Results[i].Arm = "condensed"
	}

	byRoleDelta := map[string]FidelityRoleDelta{}
	for _, preset := range presets {
		fullBucket := fullReport.ByRole[preset.Name]
		condensedBucket := condensedReport.ByRole[preset.Name]
		fullTotal := fullBucket["passed"] + fullBucket["failed"]
		condensedTotal := condensedBucket["passed"] + condensedBucket["failed"]

		delta := FidelityRoleDelta{SharedPolicyCharsDropped: preset.SharedPolicyChars()}
		if fullTotal > 0 {
			r := roundTo(float64(fullBucket["passed"])/float64(fullTotal), 3)
			delta.FullPassRate = &r
		}
		if condensedTotal > 0 {
			r := roundTo(float64(condensedBucket["passed"])/float64(condensedTotal), 3)
			delta.CondensedPassRate = &r
		}
		if delta.FullPassRate != nil && delta.CondensedPassRate != nil {
			d := roundTo(*delta.CondensedPassRate-*delta.FullPassRate, 3)
			delta.Delta = &d
		}
		byRoleDelta[preset.Name] = delta
	}

	combinedScored := fullReport.Scored + condensedReport.Scored
	combinedPassed := fullReport.Passed + condensedReport.Passed
	combinedAttempted := len(fullReport.Results) + len(condensedReport.Results)

	report := FidelityCondensedComparisonReport{
		Mode:        "probe-condensed-comparison",
		DryRun:      client == nil,
		Full:        fullReport,
		Condensed:   condensedReport,
		ByRoleDelta: byRoleDelta,
	}
	if client != nil {
		report.Model = client.Model
		report.BaseURL = client.BaseURL
	}
	if combinedScored > 0 {
		r := roundTo(float64(combinedPassed)/float64(combinedScored), 3)
		report.PassRate = &r
	}
	if combinedAttempted > 0 {
		c := roundTo(float64(combinedScored)/float64(combinedAttempted), 3)
		report.Coverage = &c
	}
	return report
}

// ---------------------------------------------------------------------
// Attestation writing
// ---------------------------------------------------------------------

// CLINEAttestationEnv is the env var cline-plugins/cline-agents/index.ts
// reads: a JSON object mapping an exact model string to a record.
const CLINEAttestationEnv = "CLINE_AGENTS_ROLE_FIDELITY_ATTESTATIONS"

// WriteFidelityAttestation writes, merging rather than clobbering, a
// role-fidelity attestation record for this run's model into path -- a JSON
// object keyed by the exact model string. Refuses on a dry run, a
// non-"probe" mode report, or a run with zero scored results.
func WriteFidelityAttestation(report FidelityProbeReport, path string) (map[string]any, error) {
	if report.DryRun {
		return nil, fidelityErrorf("cannot attest a dry run: no probe was sent, so there is nothing to attest")
	}
	if report.Mode != "probe" {
		return nil, fidelityErrorf("cannot attest a %q report: attestation is defined for a single probe-mode run", report.Mode)
	}
	if report.Model == "" {
		return nil, fidelityErrorf("cannot attest: report carries no model")
	}
	if report.Scored <= 0 {
		return nil, fidelityErrorf("cannot attest a run with zero scored results -- nothing was measured for this model")
	}

	var caveats []string
	if report.Coverage != nil && *report.Coverage < 1.0 {
		caveats = append(caveats, fmt.Sprintf(
			"coverage %v (< 1.0): %d probe/role pair(s) did not answer (transport error) and are excluded from this measurement",
			*report.Coverage, report.Errored))
	}
	if report.Unreliable > 0 {
		caveats = append(caveats, fmt.Sprintf(
			"%d verdict-scored result(s) excluded as unreliable: the same role failed %s in this run, "+
				"so a format-following failure means the policy verdict they would have produced is unknown, not passing or failing",
			report.Unreliable, RetentionProbeID))
	}
	if caveats == nil {
		caveats = []string{}
	}

	record := map[string]any{
		"model":        report.Model,
		"generated_at": time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		"base_url":     report.BaseURL,
		"pass_rate":    report.PassRate,
		"coverage":     report.Coverage,
		"scored":       report.Scored,
		"passed":       report.Passed,
		"failed":       report.Failed,
		"answered":     report.Answered,
		"errored":      report.Errored,
		"unreliable":   report.Unreliable,
		"probe_count":  report.ProbeCount,
		"role_count":   report.RoleCount,
		"caveats":      caveats,
	}

	existing := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &existing); err != nil {
			return nil, fidelityErrorf("%s: existing attestation file is not valid JSON: %v", path, err)
		}
	}

	existing[report.Model] = record
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return nil, err
	}
	return existing, nil
}
