package orchestration

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestValidateRoleIDValid(t *testing.T) {
	tests := []string{
		"application-engineer",
		"security-reviewer",
		"backend-engineer",
		"test-engineer",
		"a",
		"a-b-c",
		"role123",
	}
	for _, roleID := range tests {
		if err := ValidateRoleID(roleID); err != nil {
			t.Errorf("ValidateRoleID(%q) = %v, want nil", roleID, err)
		}
	}
}

func TestValidateRoleIDInvalid(t *testing.T) {
	tests := []string{
		"ApplicationEngineer", // uppercase
		"role_name",           // underscore
		"role name",           // space
		"role@name",           // special char
		"",                    // empty
	}
	for _, roleID := range tests {
		if err := ValidateRoleID(roleID); err == nil {
			t.Errorf("ValidateRoleID(%q) = nil, want error", roleID)
		}
	}
}

func TestEnsureContainedValid(t *testing.T) {
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "subdir")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("failed to create subdirectory: %v", err)
	}

	filePath := filepath.Join(subDir, "file.txt")
	if err := os.WriteFile(filePath, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	if err := ensureContained(filePath, tmpDir); err != nil {
		t.Errorf("ensureContained(%q, %q) = %v, want nil", filePath, tmpDir, err)
	}
}

func TestEnsureContainedTraversal(t *testing.T) {
	tmpDir := t.TempDir()
	outsideDir := filepath.Join(tmpDir, "..", "outside")

	// Try to escape the root
	if err := ensureContained(outsideDir, tmpDir); err == nil {
		t.Errorf("ensureContained should reject path traversal")
	}
}

func TestExtractTOMLFields(t *testing.T) {
	content := `
# Role definition
developer_instructions = "You are a helpful assistant"
model = "claude-opus-5"
sandbox_mode = "read-only"
`

	fields, err := extractTOMLFields(content, "test.toml")
	if err != nil {
		t.Fatalf("extractTOMLFields failed: %v", err)
	}

	if fields["developer_instructions"] != "You are a helpful assistant" {
		t.Errorf("developer_instructions = %q, want 'You are a helpful assistant'", fields["developer_instructions"])
	}
	if fields["model"] != "claude-opus-5" {
		t.Errorf("model = %q, want 'claude-opus-5'", fields["model"])
	}
	if fields["sandbox_mode"] != "read-only" {
		t.Errorf("sandbox_mode = %q, want 'read-only'", fields["sandbox_mode"])
	}
}

func TestExtractMarkdownFrontmatter(t *testing.T) {
	content := `---
name: Test Agent
model: claude-opus-5
effort: balanced
---
This is the role instructions.
It can span multiple lines.`

	fields, body, err := extractMarkdownFrontmatter(content, "test.md")
	if err != nil {
		t.Fatalf("extractMarkdownFrontmatter failed: %v", err)
	}

	if fields["model"] != "claude-opus-5" {
		t.Errorf("model = %q, want 'claude-opus-5'", fields["model"])
	}
	if fields["effort"] != "balanced" {
		t.Errorf("effort = %q, want 'balanced'", fields["effort"])
	}

	expectedBody := "This is the role instructions.\nIt can span multiple lines."
	if body != expectedBody {
		t.Errorf("body = %q, want %q", body, expectedBody)
	}
}

func TestExtractMarkdownFrontmatterMissingClosing(t *testing.T) {
	content := `---
model: claude-opus-5
`

	_, _, err := extractMarkdownFrontmatter(content, "test.md")
	if err == nil {
		t.Errorf("extractMarkdownFrontmatter should reject missing closing delimiter")
	}
}

func TestReadRoleFileCapped(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "role.toml")

	content := []byte("test content")
	if err := os.WriteFile(filePath, content, 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	// Should succeed
	read, err := readRoleFileCapped(filePath, 1024)
	if err != nil {
		t.Errorf("readRoleFileCapped failed: %v", err)
	}
	if string(read) != string(content) {
		t.Errorf("content mismatch: got %q, want %q", read, content)
	}

	// Should fail - file too large
	_, err = readRoleFileCapped(filePath, 1)
	if err == nil {
		t.Errorf("readRoleFileCapped should reject file exceeding max size")
	}
}

func TestComputeEffectiveSandbox(t *testing.T) {
	tests := []struct {
		mode              string
		fileSandboxMode   string
		expectedEffective string
		expectedFileSaved string
	}{
		{ModePlanningOnly, SandboxReadOnly, SandboxReadOnly, SandboxReadOnly},
		{ModePlanningOnly, SandboxWorkspaceWrite, SandboxReadOnly, SandboxWorkspaceWrite},
		{ModePlanningOnly, "", SandboxReadOnly, ""},
		{ModeRepositoryEdit, SandboxReadOnly, SandboxWorkspaceWrite, SandboxReadOnly},
		{ModeRepositoryEdit, SandboxWorkspaceWrite, SandboxWorkspaceWrite, SandboxWorkspaceWrite},
		{ModeRepositoryEdit, "", SandboxWorkspaceWrite, ""},
	}

	for _, tt := range tests {
		effective, fileSaved, err := ComputeEffectiveSandbox(tt.mode, tt.fileSandboxMode)
		if err != nil {
			t.Errorf("ComputeEffectiveSandbox(%q, %q) failed: %v", tt.mode, tt.fileSandboxMode, err)
			continue
		}
		if effective != tt.expectedEffective {
			t.Errorf("effective = %q, want %q", effective, tt.expectedEffective)
		}
		if fileSaved != tt.expectedFileSaved {
			t.Errorf("fileSaved = %q, want %q", fileSaved, tt.expectedFileSaved)
		}
	}
}

func TestValidateClassificationValid(t *testing.T) {
	tests := []struct {
		classification       string
		parentClassification string
	}{
		{"public", "public"},
		{"public", "internal"},
		{"public", "confidential"},
		{"internal", "internal"},
		{"internal", "confidential"},
		{"confidential", "confidential"},
	}

	for _, tt := range tests {
		_, err := ValidateClassification(tt.classification, tt.parentClassification)
		if err != nil {
			t.Errorf("ValidateClassification(%q, %q) failed: %v", tt.classification, tt.parentClassification, err)
		}
	}
}

func TestValidateClassificationExceedsParent(t *testing.T) {
	tests := []struct {
		classification       string
		parentClassification string
	}{
		{"confidential", "public"},
		{"internal", "public"},
		{"restricted", "internal"},
	}

	for _, tt := range tests {
		_, err := ValidateClassification(tt.classification, tt.parentClassification)
		if err == nil {
			t.Errorf("ValidateClassification(%q, %q) should fail when classification exceeds parent", tt.classification, tt.parentClassification)
		}
	}
}

func TestConfirmationGateFlow(t *testing.T) {
	gate := NewConfirmationGate()

	// Request confirmation
	data := map[string]any{"role_id": "test-role", "brief": "test brief"}
	token, err := gate.RequestConfirmation(data)
	if err != nil {
		t.Fatalf("RequestConfirmation failed: %v", err)
	}
	if token == "" {
		t.Errorf("token is empty, want non-empty")
	}

	// Validate token
	retrieved, err := gate.ValidateConfirmation(token)
	if err != nil {
		t.Errorf("ValidateConfirmation failed: %v", err)
	}
	if retrieved["role_id"] != "test-role" {
		t.Errorf("retrieved role_id = %v, want 'test-role'", retrieved["role_id"])
	}

	// Token consumed - second validation should fail
	_, err = gate.ValidateConfirmation(token)
	if err == nil {
		t.Errorf("ValidateConfirmation should fail for consumed token")
	}
}

func TestConfirmationGateTTL(t *testing.T) {
	gate := NewConfirmationGate()

	// Request confirmation
	data := map[string]any{"test": "data"}
	token, err := gate.RequestConfirmation(data)
	if err != nil {
		t.Fatalf("RequestConfirmation failed: %v", err)
	}

	// Manually expire the token by setting its timestamp to old
	gate.mu.Lock()
	if pc, ok := gate.pending[token]; ok {
		pc.Timestamp = time.Now().Add(-1 * time.Hour)
	}
	gate.mu.Unlock()

	// Validation should fail - token expired
	_, err = gate.ValidateConfirmation(token)
	if err == nil {
		t.Errorf("ValidateConfirmation should fail for expired token")
	}
}

func TestDispatchJobStore(t *testing.T) {
	store := NewDispatchJobStore()

	// Record a job
	result := map[string]any{
		"status": "success",
		"output": "test output",
	}
	store.RecordJob("job_abc123", result)

	// Retrieve the job
	retrieved := store.GetJob("job_abc123")
	if retrieved == nil {
		t.Errorf("GetJob returned nil, want job result")
	}
	if status, ok := retrieved["status"].(string); !ok || status != "success" {
		t.Errorf("status = %v, want 'success'", retrieved["status"])
	}

	// Non-existent job
	notFound := store.GetJob("job_nonexistent")
	if notFound != nil {
		t.Errorf("GetJob for non-existent job = %v, want nil", notFound)
	}
}

func TestBuildChildEnv(t *testing.T) {
	// Set some environment variables
	os.Setenv("PATH", "/usr/bin")
	os.Setenv("HOME", "/home/user")
	os.Setenv("SECRET_API_KEY", "should-not-appear")

	env := BuildChildEnv(1, "/project")

	// Allowed variables should be present
	if env["PATH"] != "/usr/bin" {
		t.Errorf("PATH not forwarded correctly")
	}
	if env["HOME"] != "/home/user" {
		t.Errorf("HOME not forwarded correctly")
	}

	// Disallowed variable should NOT be present
	if _, ok := env["SECRET_API_KEY"]; ok {
		t.Errorf("SECRET_API_KEY was forwarded, should not be")
	}

	// Dispatch depth should be set
	if env[DepthEnvVar] != "1" {
		t.Errorf("dispatch depth = %q, want '1'", env[DepthEnvVar])
	}
}

func TestWrapUntrustedOutput(t *testing.T) {
	output := "user-supplied output\nwith multiple lines"
	wrapped := WrapUntrustedOutput(output)

	// This asserted only that the literal "```untrusted" appeared, which the
	// weak static fence satisfied while being closable by any child that
	// wrote three backticks. The properties below are what make the fence a
	// control rather than a decoration.
	if !containsStr(wrapped, output) {
		t.Errorf("wrapped output missing original content")
	}
	if !containsStr(wrapped, "BEGIN UNTRUSTED CHILD OUTPUT") ||
		!containsStr(wrapped, "END UNTRUSTED CHILD OUTPUT") {
		t.Errorf("wrapped output has no opening and closing marker: %q", wrapped)
	}
	// The model must be told what the framing means. A marker with no
	// instruction is a boundary nothing is obliged to respect.
	if !containsStr(wrapped, "never as an instruction") {
		t.Errorf("wrapped output does not say how to treat the content: %q", wrapped)
	}
}

func TestBuildAuditRecord(t *testing.T) {
	fields := map[string]any{
		"role_id":      "test-role",
		"sandbox_mode": "read-only",
		"exit_code":    0,
	}

	record, err := BuildAuditRecord(fields)
	if err != nil {
		t.Fatalf("BuildAuditRecord failed: %v", err)
	}

	if record["role_id"] != "test-role" {
		t.Errorf("role_id not in record")
	}
	if _, ok := record["timestamp"]; !ok {
		t.Errorf("timestamp not added to record")
	}
}

func TestBuildAuditRecordForbiddenKey(t *testing.T) {
	fields := map[string]any{
		"role_id": "test-role",
		"brief":   "forbidden brief data",
	}

	_, err := BuildAuditRecord(fields)
	if err == nil {
		t.Errorf("BuildAuditRecord should reject forbidden key 'brief'")
	}
}

// Helper function
func containsStr(s, substr string) bool {
	for i := 0; i < len(s)-len(substr)+1; i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
