package kernel

import (
	"path/filepath"
	"strings"
	"testing"
)

// Validate: what this kernel owes, stated without reference to any other
// implementation.
//
// Split out of validate_differential_test.go, which compares this kernel against the
// Python one and goes when it does. These say what the behaviour *is*, and
// stay. Keeping both in one file meant a whole-file deletion would have taken
// the second half with the first -- measured, that cost twenty points of
// coverage nobody intended to give up.

func TestAnAuthorReviewerOverlapIsAnErrorNotABlocker(t *testing.T) {
	// The distinction that matters most in this report. An overlap is not an
	// undecided question a human still has to answer -- it is a configuration
	// that contradicts the separation the gates exist to enforce, so it makes
	// the project invalid rather than merely not-ready.
	root, manifest := initProject(t)
	mutateJSON(t, filepath.Join(root, Overlay, "routing.json"), func(document map[string]any) {
		routes, _ := document["routes"].([]any)
		if len(routes) == 0 {
			t.Skip("no routes to overlap")
		}
		route := routes[0].(map[string]any)
		route["agents"] = []any{"shared-identity"}
		route["reviewers"] = []any{"shared-identity"}
	})

	golang := goConfiguration(t, root, manifest)
	if golang.Valid {
		t.Error("a route assigning one identity as both author and reviewer was called valid")
	}
	found := false
	for _, message := range golang.Errors {
		if strings.Contains(message, "author and reviewer") {
			found = true
		}
	}
	if !found {
		t.Errorf("the overlap was not reported as an error: %v", golang.Errors)
	}
	for _, message := range golang.Blockers {
		if strings.Contains(message, "author and reviewer") {
			t.Error("the overlap was reported as a blocker, which reads as 'decide this later'")
		}
	}
}

func goConfiguration(t *testing.T, root, manifest string) ValidationReport {
	t.Helper()
	registry := NewRegistry()
	if err := registry.LoadProvider(manifest); err != nil {
		t.Fatalf("LoadProvider: %v", err)
	}
	overlay, err := LoadOverlay(root)
	if err != nil {
		t.Fatalf("LoadOverlay: %v", err)
	}
	return registry.ValidateConfiguration(root, overlay)
}

// pythonValidate runs the Python kernel's validate and parses its report.
