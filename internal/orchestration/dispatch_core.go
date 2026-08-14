// Package orchestration ports roster/orchestration/mcp/dispatch_core.py: the
// pure Go core logic for agents MCP dispatch, replacing Python subprocess-based
// dispatch management with native Go child process control, sandbox narrowing,
// confirmation gating, and audit logging. Deliberate zero dependency on MCP
// package so tests pass without it installed.
package orchestration

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

// Constants matching Python dispatch_core.py exactly
const (
	// Role/mode validation
	RoleIDPattern      = `^[a-z0-9-]+$`
	ModePlanningOnly   = "planning-review-only"
	ModeRepositoryEdit = "scoped-repository-edit"

	// Sandbox modes
	SandboxReadOnly            = "read-only"
	SandboxWorkspaceWrite      = "workspace-write"
	SandboxDangerFullAccess    = "danger-full-access"
	DefaultTimeoutSeconds      = 600.0
	MaxConcurrentChildren      = 3
	ConfirmationTTLSeconds     = 300.0
	MaxDispatchDepth           = 1
	DispatchJobTTLSeconds      = 1800.0
	MaxRoleFileBytes           = 256 * 1024
	MaxBriefBytes              = 32 * 1024
	MaxChildOutputBytes        = 1 * 1024 * 1024
	MaxFinalHandoffResultBytes = 64 * 1024

	// Runners
	RunnerCodex      = "codex"
	RunnerClaudeCode = "claude-code"
	RunnerAPI        = "api"

	// Environment
	DepthEnvVar              = "SECURE_CLOUD_AGENTS_DISPATCH_DEPTH"
	ParentClassificationVar  = "SECURE_CLOUD_AGENTS_PARENT_CLASSIFICATION"
	FinalHandoffResultEnvVar = "SECURE_CLOUD_AGENTS_FINAL_HANDOFF_PATH"
)

// Classifications with ordering
var (
	Classifications        = map[string]bool{"public": true, "internal": true, "confidential": true, "restricted": true}
	ClassificationOrder    = []string{"public", "internal", "confidential", "restricted"}
	ClassificationRank     = map[string]int{"public": 0, "internal": 1, "confidential": 2, "restricted": 3}
	KnownSandboxModes      = map[string]bool{"read-only": true, "workspace-write": true, "danger-full-access": true}
	WriteCarpableSandboxes = map[string]bool{"workspace-write": true, "danger-full-access": true}
	Runners                = map[string]bool{"codex": true, "claude-code": true, "api": true}
	DefaultRunner          = RunnerCodex
	AuditLogDir            = filepath.Join(os.Getenv("HOME"), ".agents", "mcp-dispatch")
	AuditLogPath           = filepath.Join(AuditLogDir, "audit.jsonl")

	// Environment variables to forward to child processes (deny-by-default)
	EnvAllowlist = []string{
		"PATH", "HOME", "LANG", "LC_ALL", "LC_CTYPE", "TERM",
		"TMPDIR", "TZ", "USER", "LOGNAME", "SHELL", "CODEX_HOME",
	}
)

// DispatchError is the base error type for dispatch operations
type DispatchError struct {
	Message string
}

func (e DispatchError) Error() string {
	return e.Message
}

// DispatchDenied indicates the dispatch was explicitly denied
type DispatchDenied struct {
	Reason string
}

func (e DispatchDenied) Error() string {
	return fmt.Sprintf("denied: %s", e.Reason)
}

// DispatchUnavailable indicates the dispatch could not proceed
type DispatchUnavailable struct {
	Reason string
}

func (e DispatchUnavailable) Error() string {
	return fmt.Sprintf("unavailable: %s", e.Reason)
}

// ResolvedRole represents a role after file resolution and validation
type ResolvedRole struct {
	ID                  string
	FilePath            string
	DeveloperInstructs  string
	Model               string
	SandboxMode         string
	CoexecProfile       string
	CodrayTemplate      string
	ReadonlyWhenStarved string
}

// ValidateRoleID checks that a role_id matches the required pattern
func ValidateRoleID(roleID string) error {
	rx := regexp.MustCompile(RoleIDPattern)
	if !rx.MatchString(roleID) {
		return fmt.Errorf("invalid role_id: %q (must match %s)", roleID, RoleIDPattern)
	}
	return nil
}

// ValidateClassification checks classification precedence
func ValidateClassification(classification, parentClassification string) (string, error) {
	if !Classifications[classification] {
		return "", fmt.Errorf("invalid classification: %q", classification)
	}
	if !Classifications[parentClassification] {
		return "", fmt.Errorf("invalid parent classification: %q", parentClassification)
	}

	// classification may not exceed parent
	if ClassificationRank[classification] > ClassificationRank[parentClassification] {
		return "", fmt.Errorf(
			"classification %q exceeds parent ceiling %q",
			classification, parentClassification,
		)
	}

	return classification, nil
}

// ComputeEffectiveSandbox determines the effective sandbox mode
func ComputeEffectiveSandbox(mode, fileSandboxMode string) (string, string, error) {
	if !Runners[mode] && mode != ModePlanningOnly && mode != ModeRepositoryEdit {
		// mode is actually a runner, not a dispatch mode
		return "", "", fmt.Errorf("mode must be 'planning-review-only' or 'scoped-repository-edit', got %q", mode)
	}

	effective := fileSandboxMode
	if effective == "" {
		effective = SandboxReadOnly
	}

	// planning-review-only forces read-only
	if mode == ModePlanningOnly {
		effective = SandboxReadOnly
	}

	// scoped-repository-edit permits write modes
	if mode == ModeRepositoryEdit {
		if !WriteCarpableSandboxes[effective] {
			effective = SandboxWorkspaceWrite
		}
	}

	return effective, fileSandboxMode, nil
}

// ConfirmationGate manages confirmation tokens for write-capable operations
type ConfirmationGate struct {
	mu             sync.Mutex
	pending        map[string]*PendingConfirmation // token -> confirmation
	confirmationID int64
}

type PendingConfirmation struct {
	ID        int64
	Token     string
	Timestamp time.Time
	Data      map[string]any
}

// NewConfirmationGate creates a new confirmation gate
func NewConfirmationGate() *ConfirmationGate {
	return &ConfirmationGate{
		pending: make(map[string]*PendingConfirmation),
	}
}

// RequestConfirmation creates a pending confirmation that requires a token replay
func (cg *ConfirmationGate) RequestConfirmation(data map[string]any) (string, error) {
	cg.mu.Lock()
	defer cg.mu.Unlock()

	token, err := generateConfirmationToken()
	if err != nil {
		return "", err
	}

	cg.confirmationID++
	pc := &PendingConfirmation{
		ID:        cg.confirmationID,
		Token:     token,
		Timestamp: time.Now(),
		Data:      data,
	}
	cg.pending[token] = pc
	return token, nil
}

// ValidateConfirmation checks that the token is valid and current
func (cg *ConfirmationGate) ValidateConfirmation(token string) (map[string]any, error) {
	cg.mu.Lock()
	defer cg.mu.Unlock()

	pc, ok := cg.pending[token]
	if !ok {
		return nil, fmt.Errorf("invalid or expired confirmation token")
	}

	// Check TTL
	if time.Since(pc.Timestamp) > ConfirmationTTLSeconds*time.Second {
		delete(cg.pending, token)
		return nil, fmt.Errorf("confirmation token expired")
	}

	// Consume the token
	delete(cg.pending, token)
	return pc.Data, nil
}

// DispatchJobStore manages async dispatch job lifecycle
type DispatchJobStore struct {
	mu   sync.Mutex
	jobs map[string]*DispatchJobRecord
}

type DispatchJobRecord struct {
	JobID     string
	Started   time.Time
	Completed time.Time
	Result    map[string]any
}

// NewDispatchJobStore creates a new job store
func NewDispatchJobStore() *DispatchJobStore {
	return &DispatchJobStore{
		jobs: make(map[string]*DispatchJobRecord),
	}
}

// RecordJob stores a dispatch result
func (ds *DispatchJobStore) RecordJob(jobID string, result map[string]any) {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	ds.jobs[jobID] = &DispatchJobRecord{
		JobID:     jobID,
		Started:   time.Now().Add(-1 * time.Minute), // approximate
		Completed: time.Now(),
		Result:    result,
	}
}

// GetJob retrieves a job result, returning nil if not found or expired
func (ds *DispatchJobStore) GetJob(jobID string) map[string]any {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	job, ok := ds.jobs[jobID]
	if !ok {
		return nil
	}

	// Check TTL
	if time.Since(job.Completed) > DispatchJobTTLSeconds*time.Second {
		delete(ds.jobs, jobID)
		return nil
	}

	return job.Result
}

// generateConfirmationToken creates a random confirmation token
func generateConfirmationToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "confirm_" + hex.EncodeToString(b), nil
}

// generateJobID creates a unique job ID
func generateJobID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "job_" + hex.EncodeToString(b), nil
}

// UTCNowISO returns current time in ISO 8601 format
func UTCNowISO() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// BuildChildEnv constructs the environment for a dispatched child process
func BuildChildEnv(dispatchDepth int, projectRoot string) map[string]string {
	env := make(map[string]string)

	// Copy allowed environment variables
	for _, key := range EnvAllowlist {
		if val := os.Getenv(key); val != "" {
			env[key] = val
		}
	}

	// Set dispatch depth
	env[DepthEnvVar] = fmt.Sprintf("%d", dispatchDepth)

	// Set parent classification if needed
	if parentClass := os.Getenv(ParentClassificationVar); parentClass != "" {
		env[ParentClassificationVar] = parentClass
	}

	return env
}

// WrapUntrustedOutput wraps untrusted output in fences
func WrapUntrustedOutput(text string) string {
	const fence = "```untrusted"
	return fmt.Sprintf("%s\n%s\n```", fence, text)
}

// FenceUntrustedBrief wraps brief in danger fence
func FenceUntrustedBrief(brief string) string {
	return fmt.Sprintf("<!-- untrusted caller-supplied brief -->\n%s", brief)
}

// ComposePrompt combines developer instructions with untrusted brief
func ComposePrompt(developerInstructions, brief string) string {
	return developerInstructions + "\n\n" + FenceUntrustedBrief(brief)
}

// BuildAuditRecord creates an audit log entry
func BuildAuditRecord(fields map[string]any) (map[string]any, error) {
	// Forbidden keys that contain sensitive data
	forbidden := map[string]bool{
		"developer_instructions": true, "brief": true, "prompt": true,
		"output": true, "stdout": true, "stderr": true, "stdout_text": true,
		"environment": true, "env": true, "child_env": true,
		"credentials": true, "auth": true, "token": true,
	}

	record := make(map[string]any)
	record["timestamp"] = UTCNowISO()

	for key, val := range fields {
		if forbidden[key] {
			return nil, fmt.Errorf("forbidden audit key: %s", key)
		}
		record[key] = val
	}

	return record, nil
}

// WriteAuditLog appends a record to the audit log
func WriteAuditLog(record map[string]any) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}

	// Ensure audit dir exists
	if err := os.MkdirAll(AuditLogDir, 0700); err != nil {
		return err
	}

	// Append to audit log
	f, err := os.OpenFile(AuditLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	if _, err := f.WriteString(string(data) + "\n"); err != nil {
		return err
	}

	return nil
}

// DispatchSecureCloudRole, PollDispatchStatus, DispatchTeam, PollTeamStatus
// are implemented in dispatch_core_phase2.go
