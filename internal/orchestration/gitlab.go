// gitlab.go ports roster/orchestration/mcp/gitlab_core.py: a small,
// deliberately create-only set of GitLab operations (a review-subtask
// issue, a wiki page, an evidence comment) so any agent can record
// human-reviewable evidence against a single, pre-configured, docs-only
// GitLab project without ever being able to transition an issue's state
// (close/reopen/resolve/relabel-away-from open-review).
//
// STATE TRANSITION: this file never implements any function that closes,
// reopens, resolves, or relabels-away-from-open-review an issue, and never
// calls such a function on any caller's behalf. There is no such function
// to find beyond this comment.
//
// Deliberate scope boundary carried over from the Python original: none of
// the three operations here accepts or enforces a classification parameter
// on the GitLab write it performs. The accepted residual-risk decision for
// this integration is that scope containment is achieved operationally (a
// dedicated, docs-only GitLab project and a least-privilege service token
// scoped to only that project), not by an in-code classification check.
//
// gitlab.base_url/gitlab.project_id/gitlab.supports_work_item_hierarchy
// resolve through internal/config's full settings.py-mirroring precedence
// chain (env var > project-local .agents/cadre.yaml, only for fields not
// global_only > user-global config file > default): see
// resolveGitLabConfig below. GITLAB_SVC_TOKEN stays a direct env-var read
// (resolveGitLabToken), never routed through internal/config -- secrets
// are always environment-variable-only, matching settings.py's own
// resolve_token()-style functions, which it documents as deliberately
// staying outside its resolver rather than being read from or written to
// a config file it manages.
package orchestration

import (
	cryptorand "crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	mathrand "math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	cadreconfig "github.com/deagy/cadre/cli/internal/config"
)

// ---------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------

const (
	gitlabTokenEnvVar     = "GITLAB_SVC_TOKEN"
	gitlabBaseURLEnvVar   = "GITLAB_BASE_URL"
	gitlabProjectIDEnvVar = "GITLAB_DOCS_PROJECT_ID"
	gitlabHierarchyEnvVar = "GITLAB_SUPPORTS_WORK_ITEM_HIERARCHY"
)

// MaxEvidenceCommentBytes is the hard, reject-not-truncate UTF-8 byte cap
// for write_evidence_comment's content.
const MaxEvidenceCommentBytes = 1 * 1024 * 1024

// MaxWikiPageContentBytes is the hard, reject-not-truncate UTF-8 byte cap
// for write_wiki_page's content.
const MaxWikiPageContentBytes = 2 * 1024 * 1024

// MaxGitLabResponseBytes is a defensive cap on any single API response body.
const MaxGitLabResponseBytes = 4 * 1024 * 1024

const (
	gitlabDefaultTimeout   = 15 * time.Second
	gitlabMaxRetryAttempts = 5
	gitlabMaxRetryElapsed  = 30 * time.Second
	gitlabBaseBackoff      = 500 * time.Millisecond
	gitlabMaxBackoff       = 8 * time.Second
)

const reviewSubtaskLabel = "review-subtask"
const evidenceKeyLabelPrefix = "evidence-key:"

const issueSearchPageSize = 100
const issueSearchMaxPages = 20

var gateIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]+$`)
var taskIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]+$`)

// quickActionLinePattern: GitLab interprets any line whose first
// non-whitespace characters match this shape as a "quick action" attempt
// (e.g. /close, /unlabel, /relabel), executed server-side as coming from
// the note/issue author -- which, for this integration, is always this
// module's own service-account token, never the human the content actually
// came from. Matching is case-insensitive (GitLab's own quick-action
// extractor is), and deliberately broader than GitLab's real, finite
// command list, so this module never needs to track GitLab's exact command
// set to stay safe.
var quickActionLinePattern = regexp.MustCompile(`(?im)^\s*/[a-z][a-z_]*\b`)

// GitLabConfig is the resolved target-project configuration.
type GitLabConfig struct {
	BaseURL                   string // always https://..., no trailing slash
	ProjectID                 string // numeric id or "namespace/project" path
	SupportsWorkItemHierarchy *bool  // nil = unset/undetectable -> fallback
}

// GitLabErrorKind mirrors dispatch_core's status vocabulary.
type GitLabErrorKind string

const (
	GitLabKindUnavailable GitLabErrorKind = "unavailable"
	GitLabKindDenied      GitLabErrorKind = "denied"
)

// GitLabError is the base type for structured GitLab tool failures.
type GitLabError struct {
	Kind               GitLabErrorKind
	Message            string
	StatusCode         *int
	AuditReason        string // "" means use Message
	ResponseBodySHA256 string
	ResponseBodyLength *int
}

func (e *GitLabError) Error() string { return e.Message }

func (e *GitLabError) auditReason() string {
	if e.AuditReason != "" {
		return e.AuditReason
	}
	return e.Message
}

func gitlabConfigError(format string, args ...any) *GitLabError {
	return &GitLabError{Kind: GitLabKindUnavailable, Message: fmt.Sprintf(format, args...)}
}

func gitlabValidationError(format string, args ...any) *GitLabError {
	return &GitLabError{Kind: GitLabKindDenied, Message: fmt.Sprintf(format, args...)}
}

// resolveGitLabToken resolves GITLAB_SVC_TOKEN lazily. Fails closed on
// unset/empty/whitespace-only. Callers must never log the returned token.
func resolveGitLabToken() (string, error) {
	raw, ok := os.LookupEnv(gitlabTokenEnvVar)
	if !ok || strings.TrimSpace(raw) == "" {
		return "", gitlabConfigError("%s is not set or is empty/whitespace-only", gitlabTokenEnvVar)
	}
	return raw, nil
}

// resolveGitLabConfig resolves the target-project settings via
// internal/config's full precedence chain (env var > project-local
// .agents/cadre.yaml, only for fields not global_only > user-global
// ~/.config/cadre/config.yaml > default): the deviation this file's
// package doc used to document (env-var-only resolution) is closed now
// that internal/config exists. gitlab.base_url and gitlab.project_id are
// registered there as global_only, so a project-local file attempting to
// set either still fails closed with the same *config.SettingsScopeError
// discussed in this file's package doc -- that security property is
// enforced by internal/config now, not reimplemented here.
func resolveGitLabConfig() (GitLabConfig, error) {
	values, err := cadreconfig.ResolveMany(
		[]string{"gitlab.base_url", "gitlab.project_id", "gitlab.supports_work_item_hierarchy"}, "")
	if err != nil {
		if scopeErr, ok := err.(*cadreconfig.SettingsScopeError); ok {
			return GitLabConfig{}, gitlabConfigError("%s", scopeErr.Error())
		}
		return GitLabConfig{}, gitlabConfigError("%s", err.Error())
	}

	baseURL, _ := values["gitlab.base_url"].(string)
	projectID, _ := values["gitlab.project_id"].(string)
	hierarchy, _ := values["gitlab.supports_work_item_hierarchy"].(*bool)

	return GitLabConfig{BaseURL: baseURL, ProjectID: projectID, SupportsWorkItemHierarchy: hierarchy}, nil
}

func rejectQuickActionSyntax(value, fieldName string) error {
	if quickActionLinePattern.MatchString(value) {
		return gitlabValidationError(
			"%s contains a line shaped like a GitLab quick action (a line starting with '/' followed "+
				"by a letter and then letters/underscores, matched case-insensitively since GitLab "+
				"itself matches quick actions case-insensitively), which GitLab would interpret and "+
				"execute server-side as this integration's own service-account token -- rejected rather "+
				"than sent to GitLab; remove or reword the offending line", fieldName)
	}
	return nil
}

// ---------------------------------------------------------------------
// Audit trail
// ---------------------------------------------------------------------

// GitLabAuditLogPath is the default GitLab-audit JSONL file location,
// separate from any other subsystem's audit trail.
func GitLabAuditLogPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".agents", "mcp-gitlab", "audit.jsonl")
}

// forbiddenAuditKeys are never permitted in a GitLab audit record's extra
// fields -- token, confirmation-token value, or raw body content must never
// reach the audit trail.
var forbiddenAuditKeys = map[string]bool{"token": true, "confirmation_token": true, "content": true}

func writeGitLabAuditRecord(path, tool, taskID, decision string, extra map[string]any) error {
	for k := range extra {
		if forbiddenAuditKeys[k] {
			return fmt.Errorf("gitlab audit: forbidden key %q in extra fields", k)
		}
	}
	if path == "" {
		path = GitLabAuditLogPath()
	}
	record := map[string]any{
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"tool":      tool,
		"decision":  decision,
	}
	if taskID != "" {
		record["task_id"] = taskID
	}
	for k, v := range extra {
		record[k] = v
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	return err
}

// ---------------------------------------------------------------------
// HTTP transport
// ---------------------------------------------------------------------

// gitlabHTTPClient builds an http.Client that refuses cross-host redirects
// and any redirect to a non-https scheme, and disables proxying
// unconditionally (regardless of ambient HTTPS_PROXY/https_proxy/ALL_PROXY
// env vars) -- consistent with the Python original's "no escape hatch
// anywhere" hardening.
func gitlabHTTPClient(timeout time.Duration) *http.Client {
	transport := &http.Transport{
		Proxy:           nil,           // No proxying, regardless of ambient env vars.
		TLSClientConfig: &tls.Config{}, // Default verification; never InsecureSkipVerify.
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) == 0 {
				return nil
			}
			originalHost := via[0].URL.Hostname()
			if req.URL.Hostname() != originalHost || req.URL.Scheme != "https" {
				return fmt.Errorf(
					"refusing redirect during GitLab API call (from host %q to %q scheme %q): "+
						"cross-host redirection and same-host https-to-http scheme downgrade are both refused",
					originalHost, req.URL.Hostname(), req.URL.Scheme)
			}
			return nil
		},
	}
}

func gitlabAPIURL(config GitLabConfig, path string) string {
	return config.BaseURL + "/api/v4" + path
}

func quoteGitLabProjectID(config GitLabConfig) string {
	return url.PathEscape(config.ProjectID)
}

func gitlabPermanentStatus(status int) bool {
	return status == 401 || status == 403 || status == 404
}

func gitlabShouldRetry(attempt int, started time.Time) bool {
	if attempt >= gitlabMaxRetryAttempts {
		return false
	}
	return time.Since(started) < gitlabMaxRetryElapsed
}

func gitlabSleepBackoff(attempt int, sleep func(time.Duration)) {
	base := gitlabBaseBackoff * time.Duration(1<<uint(attempt-1))
	if base > gitlabMaxBackoff {
		base = gitlabMaxBackoff
	}
	jitter := time.Duration(mathrand.Float64() * float64(base) * 0.25)
	sleep(base + jitter)
}

func errorBodyMeta(bodySnippet string) (string, *int) {
	if bodySnippet == "" {
		return "", nil
	}
	sum := sha256.Sum256([]byte(bodySnippet))
	n := len(bodySnippet)
	return hex.EncodeToString(sum[:]), &n
}

// gitlabRequestJSON performs one logical GitLab API call, retrying
// 429/5xx/timeout/network errors with bounded jittered exponential backoff
// and raising immediately (no retry) on 401/403/404. Never returns a result
// unless the call actually succeeded.
func gitlabRequestJSON(client *http.Client, method, path string, config GitLabConfig, token string, query url.Values, jsonBody any, sleep func(time.Duration)) (any, error) {
	fullURL := gitlabAPIURL(config, path)
	if len(query) > 0 {
		fullURL += "?" + query.Encode()
	}

	var bodyBytes []byte
	if jsonBody != nil {
		var err error
		bodyBytes, err = json.Marshal(jsonBody)
		if err != nil {
			return nil, gitlabValidationError("cannot encode request body: %v", err)
		}
	}

	attempt := 0
	started := time.Now()
	for {
		attempt++
		req, err := http.NewRequest(method, fullURL, bytesReader(bodyBytes))
		if err != nil {
			return nil, gitlabValidationError("cannot build request: %v", err)
		}
		req.Header.Set("PRIVATE-TOKEN", token)
		req.Header.Set("Accept", "application/json")
		if bodyBytes != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := client.Do(req)
		if err != nil {
			if !gitlabShouldRetry(attempt, started) {
				return nil, &GitLabError{
					Kind: GitLabKindUnavailable,
					Message: fmt.Sprintf("GitLab API call %s %s did not succeed after %d attempt(s) over %.1fs "+
						"(network/timeout error); giving up", method, path, attempt, time.Since(started).Seconds()),
				}
			}
			gitlabSleepBackoff(attempt, sleep)
			continue
		}

		result, retryable, apiErr := readGitLabResponse(resp, method, path)
		if apiErr == nil {
			return result, nil
		}
		if !retryable {
			return nil, apiErr
		}
		if !gitlabShouldRetry(attempt, started) {
			return nil, &GitLabError{
				Kind: GitLabKindUnavailable,
				Message: fmt.Sprintf("GitLab API call %s %s did not succeed after %d attempt(s) over %.1fs "+
					"(last status from a retryable error); giving up", method, path, attempt, time.Since(started).Seconds()),
			}
		}
		gitlabSleepBackoff(attempt, sleep)
	}
}

func bytesReader(b []byte) *strings.Reader {
	if b == nil {
		return strings.NewReader("")
	}
	return strings.NewReader(string(b))
}

// readGitLabResponse reads and classifies one HTTP response. retryable is
// true for 429/5xx; err is nil only on 2xx success.
func readGitLabResponse(resp *http.Response, method, path string) (result any, retryable bool, err *GitLabError) {
	defer resp.Body.Close()
	raw := make([]byte, 0, 4096)
	buf := make([]byte, 4096)
	total := 0
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			total += n
			if total <= MaxGitLabResponseBytes {
				raw = append(raw, buf[:n]...)
			}
		}
		if readErr != nil {
			break
		}
	}
	if total > MaxGitLabResponseBytes {
		return nil, false, &GitLabError{
			Kind:        GitLabKindDenied,
			Message:     fmt.Sprintf("GitLab API response for %s %s exceeded %d-byte cap", method, path, MaxGitLabResponseBytes),
			AuditReason: fmt.Sprintf("GitLab API response exceeded the %d-byte cap", MaxGitLabResponseBytes),
		}
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if len(raw) == 0 {
			return nil, false, nil
		}
		var parsed any
		if jsonErr := json.Unmarshal(raw, &parsed); jsonErr != nil {
			return nil, false, &GitLabError{Kind: GitLabKindDenied, Message: fmt.Sprintf("GitLab API returned non-JSON body for %s %s", method, path)}
		}
		return parsed, false, nil
	}

	bodySnippet := string(raw)
	if len(bodySnippet) > 4096 {
		bodySnippet = bodySnippet[:4096]
	}
	sha, length := errorBodyMeta(bodySnippet)
	status := resp.StatusCode

	if gitlabPermanentStatus(status) {
		return nil, false, &GitLabError{
			Kind:       GitLabKindDenied,
			Message:    fmt.Sprintf("GitLab API returned %d for %s %s: %s", status, method, path, bodySnippet),
			StatusCode: &status,
			AuditReason: fmt.Sprintf("GitLab API returned %d for %s %s (response body redacted from the "+
				"audit trail; see response_body_sha256/response_body_length)", status, method, path),
			ResponseBodySHA256: sha,
			ResponseBodyLength: length,
		}
	}
	if status == 429 || (status >= 500 && status < 600) {
		return nil, true, &GitLabError{Kind: GitLabKindUnavailable, Message: fmt.Sprintf("GitLab API returned %d for %s %s", status, method, path), StatusCode: &status}
	}
	return nil, false, &GitLabError{
		Kind:       GitLabKindDenied,
		Message:    fmt.Sprintf("GitLab API returned unexpected status %d for %s %s: %s", status, method, path, bodySnippet),
		StatusCode: &status,
		AuditReason: fmt.Sprintf("GitLab API returned unexpected status %d for %s %s (response body redacted "+
			"from the audit trail; see response_body_sha256/response_body_length)", status, method, path),
		ResponseBodySHA256: sha,
		ResponseBodyLength: length,
	}
}

// ---------------------------------------------------------------------
// Untrusted-output wrapping
// ---------------------------------------------------------------------

const untrustedOutputMarkerBegin = "<<<UNTRUSTED_GITLAB_CONTENT_BEGIN>>>"
const untrustedOutputMarkerEnd = "<<<UNTRUSTED_GITLAB_CONTENT_END>>>"

// wrapUntrustedGitLabPayload serializes payload and wraps it with an
// explicit marker-token pair, so GitLab-retrieved text can never be
// mistaken by the calling model for an instruction, including text an
// attacker deliberately wrote into an issue title/description/wiki body to
// try to forge a fake trusted-instruction boundary.
func wrapUntrustedGitLabPayload(payload any) string {
	data, err := json.Marshal(payload)
	if err != nil {
		data = []byte(`{"error":"could not serialize payload"}`)
	}
	return untrustedOutputMarkerBegin + string(data) + untrustedOutputMarkerEnd
}

func gitlabErrorResult(err *GitLabError) map[string]any {
	reasonText := err.Message
	if err.Kind == GitLabKindUnavailable || (err.StatusCode != nil) {
		// Errors carrying a raw GitLab response snippet (permanent HTTP
		// errors or retry-exhausted errors) get their caller-facing message
		// wrapped; pure validation/config errors (this module's own
		// generated wording) are returned unwrapped.
		if err.StatusCode != nil || err.ResponseBodySHA256 != "" {
			reasonText = wrapUntrustedGitLabPayload(err.Message)
		}
	}
	result := map[string]any{"status": string(err.Kind), "reason": reasonText}
	if err.StatusCode != nil {
		result["status_code"] = *err.StatusCode
	}
	return result
}

func resolveGitLabTokenAndConfig() (string, GitLabConfig, map[string]any) {
	token, err := resolveGitLabToken()
	if err != nil {
		return "", GitLabConfig{}, gitlabErrorResult(err.(*GitLabError))
	}
	config, err := resolveGitLabConfig()
	if err != nil {
		return "", GitLabConfig{}, gitlabErrorResult(err.(*GitLabError))
	}
	return token, config, nil
}

// ---------------------------------------------------------------------
// create_review_subtask
// ---------------------------------------------------------------------

func validateLabelComponent(value, fieldName string, pattern *regexp.Regexp) error {
	if value == "" || !pattern.MatchString(value) {
		return gitlabValidationError("%s must be a non-empty string matching %s: %q", fieldName, pattern.String(), value)
	}
	return nil
}

func idempotencyKey(taskID, gateID string) string {
	return fmt.Sprintf("task_id=%s gate_id=%s", taskID, gateID)
}

func evidenceKeyLabel(taskID, gateID string, parentIssueIID int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("task_id=%s gate_id=%s parent=%d", taskID, gateID, parentIssueIID)))
	return evidenceKeyLabelPrefix + hex.EncodeToString(sum[:])
}

func findExistingGitLabSubtask(client *http.Client, config GitLabConfig, token string, parentIssueIID int, gateID, taskID string) (map[string]any, error) {
	expectedLabels := map[string]bool{
		reviewSubtaskLabel: true,
		"gate:" + gateID:   true,
		evidenceKeyLabel(taskID, gateID, parentIssueIID): true,
	}
	labelList := make([]string, 0, len(expectedLabels))
	for l := range expectedLabels {
		labelList = append(labelList, l)
	}
	sort.Strings(labelList)
	labelsFilter := strings.Join(labelList, ",")

	for page := 1; page <= issueSearchMaxPages; page++ {
		query := url.Values{
			"labels":   {labelsFilter},
			"state":    {"opened"},
			"per_page": {strconv.Itoa(issueSearchPageSize)},
			"page":     {strconv.Itoa(page)},
		}
		result, err := gitlabRequestJSON(client, http.MethodGet,
			fmt.Sprintf("/projects/%s/issues", quoteGitLabProjectID(config)), config, token, query, nil, time.Sleep)
		if err != nil {
			return nil, err
		}
		candidates, ok := result.([]any)
		if !ok {
			return nil, nil
		}
		for _, raw := range candidates {
			issue, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if issue["state"] != "opened" {
				continue
			}
			labelsRaw, _ := issue["labels"].([]any)
			issueLabels := map[string]bool{}
			for _, l := range labelsRaw {
				if s, ok := l.(string); ok {
					issueLabels[s] = true
				}
			}
			allPresent := true
			for l := range expectedLabels {
				if !issueLabels[l] {
					allPresent = false
					break
				}
			}
			if allPresent {
				return issue, nil
			}
		}
		if len(candidates) < issueSearchPageSize {
			return nil, nil
		}
	}
	return nil, nil
}

// CreateGitLabReviewSubtask creates (or, if one already exists, returns) a
// GitLab issue linked to parentIssueIID as a review subtask. Idempotent via
// a search-before-create step keyed on three exact labels; see this
// function's Python original for a full discussion of the idempotency
// search's race window (not atomic; a best-effort dedup, not a hard
// uniqueness guarantee).
func CreateGitLabReviewSubtask(client *http.Client, parentIssueIID int, title, description, gateID, taskID, auditPath string) map[string]any {
	if client == nil {
		client = gitlabHTTPClient(gitlabDefaultTimeout)
	}
	auditFields := map[string]any{
		"tool": "create_review_subtask", "gate_id": gateID, "parent_issue_iid": parentIssueIID,
	}

	if parentIssueIID <= 0 {
		err := gitlabValidationError("parent_issue_iid must be a positive integer: %d", parentIssueIID)
		writeGitLabAuditRecord(auditPath, "create_review_subtask", taskID, "denied", withReason(auditFields, err))
		return gitlabErrorResult(err)
	}
	if strings.TrimSpace(title) == "" {
		err := gitlabValidationError("title must be a non-empty string")
		writeGitLabAuditRecord(auditPath, "create_review_subtask", taskID, "denied", withReason(auditFields, err))
		return gitlabErrorResult(err)
	}
	if err := rejectQuickActionSyntax(description, "description"); err != nil {
		gErr := err.(*GitLabError)
		writeGitLabAuditRecord(auditPath, "create_review_subtask", taskID, "denied", withReason(auditFields, gErr))
		return gitlabErrorResult(gErr)
	}
	if err := validateLabelComponent(gateID, "gate_id", gateIDPattern); err != nil {
		gErr := err.(*GitLabError)
		writeGitLabAuditRecord(auditPath, "create_review_subtask", taskID, "denied", withReason(auditFields, gErr))
		return gitlabErrorResult(gErr)
	}
	if err := validateLabelComponent(taskID, "task_id", taskIDPattern); err != nil {
		gErr := err.(*GitLabError)
		writeGitLabAuditRecord(auditPath, "create_review_subtask", taskID, "denied", withReason(auditFields, gErr))
		return gitlabErrorResult(gErr)
	}

	token, config, errResult := resolveGitLabTokenAndConfig()
	if errResult != nil {
		writeGitLabAuditRecord(auditPath, "create_review_subtask", taskID, fmt.Sprintf("%v", errResult["status"]), auditFields)
		return errResult
	}

	existing, err := findExistingGitLabSubtask(client, config, token, parentIssueIID, gateID, taskID)
	if err != nil {
		gErr := err.(*GitLabError)
		writeGitLabAuditRecord(auditPath, "create_review_subtask", taskID, string(gErr.Kind), withReason(auditFields, gErr))
		return gitlabErrorResult(gErr)
	}
	if existing != nil {
		writeGitLabAuditRecord(auditPath, "create_review_subtask", taskID, "ok", mergeMap(auditFields, map[string]any{"created": false, "issue_iid": existing["iid"]}))
		return map[string]any{
			"status": "ok", "created": false,
			"hierarchy_supported": config.SupportsWorkItemHierarchy,
			"state":               existing["state"],
			"issue":               wrapUntrustedGitLabPayload(existing),
		}
	}

	key := idempotencyKey(taskID, gateID)
	fullDescription := fmt.Sprintf("Parent: #%d\n\n%s\n\n<!-- %s -->\n\n/relate #%d\n", parentIssueIID, description, key, parentIssueIID)
	payload := map[string]any{
		"title":       title,
		"description": fullDescription,
		"labels":      []string{reviewSubtaskLabel, "gate:" + gateID, evidenceKeyLabel(taskID, gateID, parentIssueIID)},
	}
	created, err := gitlabRequestJSON(client, http.MethodPost, fmt.Sprintf("/projects/%s/issues", quoteGitLabProjectID(config)), config, token, nil, payload, time.Sleep)
	if err != nil {
		gErr := err.(*GitLabError)
		writeGitLabAuditRecord(auditPath, "create_review_subtask", taskID, string(gErr.Kind), withReason(auditFields, gErr))
		return gitlabErrorResult(gErr)
	}
	createdMap, _ := created.(map[string]any)
	writeGitLabAuditRecord(auditPath, "create_review_subtask", taskID, "ok", mergeMap(auditFields, map[string]any{"created": true, "issue_iid": createdMap["iid"]}))
	return map[string]any{
		"status": "ok", "created": true,
		"hierarchy_supported": config.SupportsWorkItemHierarchy,
		"state":               createdMap["state"],
		"issue":               wrapUntrustedGitLabPayload(createdMap),
	}
}

func withReason(fields map[string]any, err *GitLabError) map[string]any {
	out := mergeMap(fields, map[string]any{"reason": err.auditReason()})
	if err.StatusCode != nil {
		out["status_code"] = *err.StatusCode
	}
	return out
}

func mergeMap(a, b map[string]any) map[string]any {
	out := make(map[string]any, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

// ---------------------------------------------------------------------
// write_evidence_comment
// ---------------------------------------------------------------------

// WriteGitLabEvidenceComment adds a comment ("note") to an existing issue
// for small, structured per-task evidence. Rejects (does not truncate)
// content whose UTF-8 encoding exceeds MaxEvidenceCommentBytes.
func WriteGitLabEvidenceComment(client *http.Client, issueIID int, content, taskID, auditPath string) map[string]any {
	if client == nil {
		client = gitlabHTTPClient(gitlabDefaultTimeout)
	}
	auditFields := map[string]any{
		"tool": "write_evidence_comment", "issue_iid": issueIID, "content_length_bytes": len(content),
	}

	if issueIID <= 0 {
		err := gitlabValidationError("issue_iid must be a positive integer: %d", issueIID)
		writeGitLabAuditRecord(auditPath, "write_evidence_comment", taskID, "denied", withReason(auditFields, err))
		return gitlabErrorResult(err)
	}
	if strings.TrimSpace(taskID) == "" {
		err := gitlabValidationError("task_id must be a non-empty string")
		writeGitLabAuditRecord(auditPath, "write_evidence_comment", taskID, "denied", withReason(auditFields, err))
		return gitlabErrorResult(err)
	}
	if err := rejectQuickActionSyntax(content, "content"); err != nil {
		gErr := err.(*GitLabError)
		writeGitLabAuditRecord(auditPath, "write_evidence_comment", taskID, "denied", withReason(auditFields, gErr))
		return gitlabErrorResult(gErr)
	}
	if len(content) > MaxEvidenceCommentBytes {
		err := gitlabValidationError(
			"content exceeds the %d-byte UTF-8-encoded cap for write_evidence_comment (%d bytes); "+
				"shorten the content, do not truncate it here", MaxEvidenceCommentBytes, len(content))
		writeGitLabAuditRecord(auditPath, "write_evidence_comment", taskID, "denied", withReason(auditFields, err))
		return gitlabErrorResult(err)
	}

	token, config, errResult := resolveGitLabTokenAndConfig()
	if errResult != nil {
		writeGitLabAuditRecord(auditPath, "write_evidence_comment", taskID, fmt.Sprintf("%v", errResult["status"]), auditFields)
		return errResult
	}

	created, err := gitlabRequestJSON(client, http.MethodPost,
		fmt.Sprintf("/projects/%s/issues/%d/notes", quoteGitLabProjectID(config), issueIID), config, token, nil,
		map[string]any{"body": content}, time.Sleep)
	if err != nil {
		gErr := err.(*GitLabError)
		writeGitLabAuditRecord(auditPath, "write_evidence_comment", taskID, string(gErr.Kind), withReason(auditFields, gErr))
		return gitlabErrorResult(gErr)
	}
	createdMap, _ := created.(map[string]any)
	writeGitLabAuditRecord(auditPath, "write_evidence_comment", taskID, "ok", mergeMap(auditFields, map[string]any{"comment_id": createdMap["id"]}))
	return map[string]any{"status": "ok", "comment": wrapUntrustedGitLabPayload(createdMap)}
}

// ---------------------------------------------------------------------
// write_wiki_page: mandatory confirmation gate
// ---------------------------------------------------------------------

// GitLabConfirmationTTL bounds how long an issued confirmation token stays
// valid.
const GitLabConfirmationTTL = 300 * time.Second

type pendingGitLabConfirmation struct {
	brief     string
	expiresAt time.Time
}

var (
	gitlabConfirmationMu      sync.Mutex
	gitlabConfirmationPending = map[string]pendingGitLabConfirmation{}
)

func issueGitLabConfirmationToken(brief string) string {
	raw := make([]byte, 24)
	_, _ = cryptorand.Read(raw)
	token := hex.EncodeToString(raw)
	gitlabConfirmationMu.Lock()
	gitlabConfirmationPending[token] = pendingGitLabConfirmation{brief: brief, expiresAt: time.Now().Add(GitLabConfirmationTTL)}
	gitlabConfirmationMu.Unlock()
	return token
}

// consumeGitLabConfirmationToken validates and consumes a confirmation
// token issued by issueGitLabConfirmationToken. Fails closed on an unknown,
// expired, or tampered (brief mismatch) token; a token is single-use even
// on a failed match.
func consumeGitLabConfirmationToken(token, expectedBrief string) error {
	gitlabConfirmationMu.Lock()
	pending, ok := gitlabConfirmationPending[token]
	delete(gitlabConfirmationPending, token)
	gitlabConfirmationMu.Unlock()

	if !ok {
		return gitlabValidationError("confirmation_token is unknown or already used")
	}
	if time.Now().After(pending.expiresAt) {
		return gitlabValidationError("confirmation_token has expired")
	}
	if pending.brief != expectedBrief {
		return gitlabValidationError("confirmation_token does not match the original request (tampered or stale replay)")
	}
	return nil
}

func wikiWriteBrief(slug, title, content, format string) string {
	sum := sha256.Sum256([]byte(content))
	data, _ := json.Marshal(map[string]any{
		"slug": slug, "title": title, "format": format,
		"content_sha256": hex.EncodeToString(sum[:]), "content_length_bytes": len(content),
	})
	return string(data)
}

func getGitLabWikiPage(client *http.Client, config GitLabConfig, token, slug string) (map[string]any, error) {
	result, err := gitlabRequestJSON(client, http.MethodGet,
		fmt.Sprintf("/projects/%s/wikis/%s", quoteGitLabProjectID(config), url.PathEscape(slug)), config, token, nil, nil, time.Sleep)
	if err != nil {
		var gErr *GitLabError
		if errors.As(err, &gErr) && gErr.StatusCode != nil && *gErr.StatusCode == 404 {
			return nil, nil
		}
		return nil, err
	}
	m, _ := result.(map[string]any)
	return m, nil
}

var validWikiFormats = map[string]bool{"markdown": true, "rdoc": true, "asciidoc": true, "org": true}

// WriteGitLabWikiPage creates or updates (versioned, by GitLab's own wiki
// history) a wiki page in the configured project. Every call must
// round-trip through a mandatory human-confirmation gate: a first call
// with confirmationToken == "" never writes anything; it returns
// status="confirmation_required" plus a token bound to the exact (slug,
// title, format, content hash) tuple, and a second call replaying that
// token is required before any GitLab write happens.
//
// Quick-action scope note: unlike CreateGitLabReviewSubtask's description
// and WriteGitLabEvidenceComment's content, content here is NOT run
// through rejectQuickActionSyntax -- GitLab's quick-action interpreter
// only ever parses issue descriptions and issue/MR/commit/epic notes,
// never wiki page content.
func WriteGitLabWikiPage(client *http.Client, slug, title, content, format, confirmationToken, auditPath string) map[string]any {
	if client == nil {
		client = gitlabHTTPClient(gitlabDefaultTimeout)
	}
	if format == "" {
		format = "markdown"
	}
	auditFields := map[string]any{"tool": "write_wiki_page", "slug": slug, "format": format}

	if strings.TrimSpace(slug) == "" {
		err := gitlabValidationError("slug must be a non-empty string")
		writeGitLabAuditRecord(auditPath, "write_wiki_page", "", "denied", withReason(auditFields, err))
		return gitlabErrorResult(err)
	}
	if strings.TrimSpace(title) == "" {
		err := gitlabValidationError("title must be a non-empty string")
		writeGitLabAuditRecord(auditPath, "write_wiki_page", "", "denied", withReason(auditFields, err))
		return gitlabErrorResult(err)
	}
	if !validWikiFormats[format] {
		err := gitlabValidationError("format must be one of markdown/rdoc/asciidoc/org: %q", format)
		writeGitLabAuditRecord(auditPath, "write_wiki_page", "", "denied", withReason(auditFields, err))
		return gitlabErrorResult(err)
	}
	if len(content) > MaxWikiPageContentBytes {
		err := gitlabValidationError(
			"content exceeds the %d-byte UTF-8-encoded cap for write_wiki_page (%d bytes); shorten the "+
				"content, do not truncate it here", MaxWikiPageContentBytes, len(content))
		writeGitLabAuditRecord(auditPath, "write_wiki_page", "", "denied", withReason(auditFields, err))
		return gitlabErrorResult(err)
	}

	token, config, errResult := resolveGitLabTokenAndConfig()
	if errResult != nil {
		writeGitLabAuditRecord(auditPath, "write_wiki_page", "", fmt.Sprintf("%v", errResult["status"]), auditFields)
		return errResult
	}

	sum := sha256.Sum256([]byte(content))
	auditFields["content_sha256"] = hex.EncodeToString(sum[:])
	auditFields["content_length_bytes"] = len(content)
	brief := wikiWriteBrief(slug, title, content, format)

	if confirmationToken == "" {
		existingBefore, err := getGitLabWikiPage(client, config, token, slug)
		if err != nil {
			gErr := err.(*GitLabError)
			writeGitLabAuditRecord(auditPath, "write_wiki_page", "", string(gErr.Kind), withReason(auditFields, gErr))
			return gitlabErrorResult(gErr)
		}
		willOverwrite := existingBefore != nil
		issued := issueGitLabConfirmationToken(brief)
		writeGitLabAuditRecord(auditPath, "write_wiki_page", "", "confirmation-required", mergeMap(auditFields, map[string]any{"will_overwrite_existing": willOverwrite}))
		return map[string]any{
			"status":                  "confirmation_required",
			"confirmation_token":      issued,
			"expires_in_seconds":      int(GitLabConfirmationTTL.Seconds()),
			"will_overwrite_existing": willOverwrite,
			"message": "write_wiki_page requires human confirmation. Replay this call unchanged, " +
				"adding confirmation_token, to actually write the wiki page.",
		}
	}

	if err := consumeGitLabConfirmationToken(confirmationToken, brief); err != nil {
		gErr := err.(*GitLabError)
		writeGitLabAuditRecord(auditPath, "write_wiki_page", "", "denied", withReason(auditFields, gErr))
		return gitlabErrorResult(gErr)
	}

	existing, err := getGitLabWikiPage(client, config, token, slug)
	if err != nil {
		gErr := err.(*GitLabError)
		writeGitLabAuditRecord(auditPath, "write_wiki_page", "", string(gErr.Kind), withReason(auditFields, gErr))
		return gitlabErrorResult(gErr)
	}
	payload := map[string]any{"title": title, "content": content, "format": format}
	var result any
	if existing == nil {
		result, err = gitlabRequestJSON(client, http.MethodPost, fmt.Sprintf("/projects/%s/wikis", quoteGitLabProjectID(config)), config, token, nil, payload, time.Sleep)
	} else {
		result, err = gitlabRequestJSON(client, http.MethodPut,
			fmt.Sprintf("/projects/%s/wikis/%s", quoteGitLabProjectID(config), url.PathEscape(slug)), config, token, nil, payload, time.Sleep)
	}
	if err != nil {
		gErr := err.(*GitLabError)
		writeGitLabAuditRecord(auditPath, "write_wiki_page", "", string(gErr.Kind), withReason(auditFields, gErr))
		return gitlabErrorResult(gErr)
	}
	writeGitLabAuditRecord(auditPath, "write_wiki_page", "", "ok", mergeMap(auditFields, map[string]any{"created": existing == nil}))
	return map[string]any{"status": "ok", "created": existing == nil, "page": wrapUntrustedGitLabPayload(result)}
}
