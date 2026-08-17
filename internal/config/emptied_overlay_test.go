package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An emptied overlay is a no-op, not a failure.
//
// A project clears a shared-policy override by emptying the file rather than
// deleting it -- often by commenting the contents out, which keeps a record of
// what used to be there. That has to behave like no overlay at all.
//
// loadStructured checked for emptiness textually, with TrimSpace. A file
// containing only comments is not textually blank, so it reached the parser,
// parsed to nothing, and failed the mapping assertion with "root must be a
// mapping" -- pointing at a file that reads as perfectly fine.
//
// Failing closed is not the safe default here. These overlays are
// narrowing-only: an empty one can only mean "no narrowing", so refusing it
// withholds nothing and breaks the project. resolve.py returned {} for all of
// these, because yaml.safe_load returns None and it mapped None to {}.

func TestADocumentThatParsesToNothingIsAnEmptyMapping(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		content string
	}{
		{"zero bytes", ""},
		{"a single newline", "\n"},
		{"whitespace only", "   \n\t\n"},
		{"only a comment", "# cleared 2026-08-17, see ADR-004\n"},
		{"several comments", "# was:\n#   require_pinned: true\n# cleared\n"},
		{"a comment after a document marker", "---\n# nothing here\n"},
		{"a bare document marker", "---\n"},
		{"an explicit empty mapping", "{}\n"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "overlay.yaml")
			if err := os.WriteFile(path, []byte(testCase.content), 0o644); err != nil {
				t.Fatal(err)
			}
			loaded, err := loadStructured(path)
			if err != nil {
				t.Fatalf("refused an emptied overlay: %v", err)
			}
			if loaded == nil {
				t.Fatal("returned a nil map; a caller ranging over it would see no " +
					"keys either way, but a caller writing to it would panic")
			}
			if len(loaded) != 0 {
				t.Errorf("loaded %d key(s) from an empty document: %v", len(loaded), loaded)
			}
		})
	}
}

func TestAJSONDocumentThatParsesToNullIsAlsoEmpty(t *testing.T) {
	// The same property through the other decoder. `null` is the JSON spelling
	// of an emptied file, and it reaches the same nil.
	path := filepath.Join(t.TempDir(), "overlay.json")
	if err := os.WriteFile(path, []byte("null\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadStructured(path)
	if err != nil {
		t.Fatalf("refused a null JSON overlay: %v", err)
	}
	if len(loaded) != 0 {
		t.Errorf("loaded %v", loaded)
	}
}

func TestANonMappingDocumentStillFailsClosed(t *testing.T) {
	// The other half, and the reason the fix is narrow. "Parses to nothing" is
	// not "parses to something that is not a mapping": a list or a bare scalar
	// at the root is a real mistake, and reading it as an empty overlay would
	// silently discard whatever the author meant.
	for _, testCase := range []struct {
		name    string
		content string
	}{
		{"a top-level list", "- a\n- b\n"},
		{"a bare scalar", "just-a-string\n"},
		{"a number", "42\n"},
		{"a boolean", "true\n"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "overlay.yaml")
			if err := os.WriteFile(path, []byte(testCase.content), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := loadStructured(path)
			if err == nil {
				t.Fatal("accepted a non-mapping root as an empty overlay")
			}
			if !strings.Contains(err.Error(), "must be a mapping") {
				t.Errorf("refused, but not as a shape error: %v", err)
			}
		})
	}
}

func TestMalformedContentStillFailsClosed(t *testing.T) {
	// And a document that does not parse at all is still an error, so the fix
	// did not turn every parse failure into a silent empty overlay.
	for _, testCase := range []struct {
		name    string
		file    string
		content string
	}{
		{"unterminated YAML flow", "overlay.yaml", "a: [\n"},
		{"malformed JSON", "overlay.json", `{"a": `},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), testCase.file)
			if err := os.WriteFile(path, []byte(testCase.content), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := loadStructured(path); err == nil {
				t.Fatal("accepted malformed content")
			}
		})
	}
}

func TestAnOverlayCommentedOutResolvesToTheShippedDefault(t *testing.T) {
	// End to end, which is where the failure actually reached a person: the
	// project has an overlay file, its contents are commented out, and
	// resolution must produce the shipped default rather than an error.
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The shipped default this overlay is clearing.
	defaults := filepath.Join(t.TempDir(), "shared")
	if err := os.MkdirAll(defaults, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(defaults, "library-standards.yaml"),
		[]byte("selection_rules:\n  require_pinned: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	overlayDir := filepath.Join(root, ".agents", "shared")
	if err := os.MkdirAll(overlayDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(overlayDir, "library-standards.yaml"),
		[]byte("# cleared; see ADR-004\n# selection_rules:\n#   require_pinned: false\n"),
		0o644); err != nil {
		t.Fatal(err)
	}

	resolved, err := ResolveSharedConfig(defaults, "library-standards.yaml", root)
	if err != nil {
		t.Fatalf("a commented-out overlay failed resolution: %v", err)
	}
	rules, _ := resolved.Structured["selection_rules"].(map[string]any)
	if rules == nil || rules["require_pinned"] != true {
		t.Errorf("the shipped default did not come through unchanged: %#v",
			resolved.Structured)
	}
}
