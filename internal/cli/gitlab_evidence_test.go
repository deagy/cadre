package cli

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deagy/cadre/cli/internal/orchestration"
)

// TestPrintWikiPageCLIProcessLifetimeNoteIfNeeded verifies the actual
// production function gitlabWriteWikiPageCmd calls (not a copy of its logic)
// to decide whether to print the CLI's process-lifetime limitation note.
//
// The confirmation_required result used here is obtained through a real
// call to orchestration.WriteGitLabWikiPage against a local httptest server
// via server.Client() (which trusts the test server's certificate), so the
// input is a genuine response shape, not hand-built.
func TestPrintWikiPageCLIProcessLifetimeNoteIfNeeded(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound) // no existing page
	}))
	defer server.Close()

	t.Setenv("GITLAB_SVC_TOKEN", "test-token")
	t.Setenv("GITLAB_BASE_URL", server.URL)
	t.Setenv("GITLAB_DOCS_PROJECT_ID", "1")

	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")

	confirmationRequired := orchestration.WriteGitLabWikiPage(
		server.Client(), "test/slug", "Test Title", "test content", "markdown", "", auditPath)
	if status, _ := confirmationRequired["status"].(string); status != "confirmation_required" {
		t.Fatalf("test setup: expected WriteGitLabWikiPage to return confirmation_required with no token, got %v", confirmationRequired)
	}

	deniedResult := map[string]any{"status": "denied", "reason": "confirmation token invalid or expired"}
	okResult := map[string]any{"status": "ok"}

	t.Run("no token and confirmation_required prints the note", func(t *testing.T) {
		out := captureStderr(t, func() {
			printWikiPageCLIProcessLifetimeNoteIfNeeded(confirmationRequired, "")
		})
		if !strings.Contains(out, "confirmation flow requires the same process") {
			t.Errorf("expected process-lifetime note in stderr, got:\n%s", out)
		}
		if !strings.Contains(out, "cadre mcp-gitlab-server") {
			t.Errorf("expected reference to cadre mcp-gitlab-server in stderr, got:\n%s", out)
		}
	})

	t.Run("a token was supplied: note must not print, even for confirmation_required", func(t *testing.T) {
		out := captureStderr(t, func() {
			printWikiPageCLIProcessLifetimeNoteIfNeeded(confirmationRequired, "some-token")
		})
		if out != "" {
			t.Errorf("expected no output when a token was supplied, got:\n%s", out)
		}
	})

	t.Run("denied result: note must not print", func(t *testing.T) {
		out := captureStderr(t, func() {
			printWikiPageCLIProcessLifetimeNoteIfNeeded(deniedResult, "")
		})
		if out != "" {
			t.Errorf("expected no output for a denied result, got:\n%s", out)
		}
	})

	t.Run("ok result: note must not print", func(t *testing.T) {
		out := captureStderr(t, func() {
			printWikiPageCLIProcessLifetimeNoteIfNeeded(okResult, "")
		})
		if out != "" {
			t.Errorf("expected no output for an ok result, got:\n%s", out)
		}
	})
}
