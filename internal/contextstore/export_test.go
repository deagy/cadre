package contextstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func samplePresentedEntry(overrides func(*PresentedEntry)) *PresentedEntry {
	e := &PresentedEntry{
		Handle: "ctx_0123456789abcdef0123456789abcdef", Label: "test", Scope: "agent", Source: "demo",
		Agent: "test-engineer", TaskID: "TASK-1", Classification: "internal", ContentHash: "deadbeef",
		ByteLength: 10, CreatedAt: "2026-01-01T00:00:00.000Z", ExpiresAt: "2026-01-02T00:00:00.000Z",
		Tags: []string{}, DerivedFrom: []string{}, Redactions: []string{}, Content: "hello world",
	}
	if overrides != nil {
		overrides(e)
	}
	return e
}

func TestRenderEntryIncludesFrontmatterAndContent(t *testing.T) {
	rendered := RenderEntry(samplePresentedEntry(nil))
	if !strings.Contains(rendered, "handle: \"ctx_0123456789abcdef0123456789abcdef\"") {
		t.Errorf("expected handle in frontmatter, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "hello world") {
		t.Error("expected content in body")
	}
	if !strings.HasPrefix(rendered, "---\n") {
		t.Error("expected frontmatter delimiter at start")
	}
}

func TestRenderEntryAddsUntrustedBannerWhenFlagged(t *testing.T) {
	rendered := RenderEntry(samplePresentedEntry(func(e *PresentedEntry) { e.UntrustedInputs = true }))
	if !strings.Contains(rendered, "UNTRUSTED PROVENANCE") {
		t.Error("expected the untrusted banner when untrusted_inputs is true")
	}
}

func TestRenderEntryOmitsBannerWhenNotFlagged(t *testing.T) {
	rendered := RenderEntry(samplePresentedEntry(nil))
	if strings.Contains(rendered, "UNTRUSTED PROVENANCE") {
		t.Error("expected no banner for a clean entry")
	}
}

func TestCheckExportableRefusesRestrictedOutright(t *testing.T) {
	entries := []*PresentedEntry{samplePresentedEntry(func(e *PresentedEntry) { e.Classification = "restricted" })}
	err := CheckExportable(entries, true, true)
	if err == nil {
		t.Fatal("expected restricted classification to be refused with no flag able to permit it")
	}
}

func TestCheckExportableRequiresAcknowledgmentForConfidential(t *testing.T) {
	entries := []*PresentedEntry{samplePresentedEntry(func(e *PresentedEntry) { e.Classification = "confidential" })}
	if err := CheckExportable(entries, false, false); err == nil {
		t.Fatal("expected confidential to require --acknowledge-commit")
	}
	if err := CheckExportable(entries, true, false); err != nil {
		t.Fatalf("expected confidential with acknowledgment to pass: %v", err)
	}
}

func TestCheckExportableRequiresIncludeUntrustedFlag(t *testing.T) {
	entries := []*PresentedEntry{samplePresentedEntry(func(e *PresentedEntry) { e.UntrustedInputs = true })}
	if err := CheckExportable(entries, false, false); err == nil {
		t.Fatal("expected untrusted_inputs to require --include-untrusted")
	}
	if err := CheckExportable(entries, false, true); err != nil {
		t.Fatalf("expected untrusted with --include-untrusted to pass: %v", err)
	}
}

func TestCheckExportablePublicAndInternalNeverRefused(t *testing.T) {
	for _, c := range []string{"public", "internal"} {
		entries := []*PresentedEntry{samplePresentedEntry(func(e *PresentedEntry) { e.Classification = c })}
		if err := CheckExportable(entries, false, false); err != nil {
			t.Errorf("classification %q should never be refused: %v", c, err)
		}
	}
}

func TestWriteEntriesWritesOneFilePerEntry(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "out")
	entries := []*PresentedEntry{
		samplePresentedEntry(nil),
		samplePresentedEntry(func(e *PresentedEntry) { e.Handle = "ctx_fedcba9876543210fedcba9876543210" }),
	}
	result, err := WriteEntries(entries, output)
	if err != nil {
		t.Fatalf("WriteEntries: %v", err)
	}
	if result.Count != 2 {
		t.Errorf("count = %d, want 2", result.Count)
	}
	for _, e := range entries {
		if _, err := os.Stat(filepath.Join(output, e.Handle+".md")); err != nil {
			t.Errorf("expected %s.md to exist: %v", e.Handle, err)
		}
	}
	// No leftover staging directory.
	entries2, err := os.ReadDir(output)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries2 {
		if strings.HasPrefix(entry.Name(), ".export-") {
			t.Errorf("expected staging directory to be cleaned up, found %s", entry.Name())
		}
	}
}

func TestWriteEntriesFailsOnDuplicateHandleInBatch(t *testing.T) {
	// Two entries sharing a handle stage to the same filename; the second
	// rename-into-place then finds nothing left at the staged path. This
	// mirrors the Python original's write_entries exactly (os.replace
	// raises the same way on the second occurrence) -- not a Go-specific
	// regression to paper over.
	dir := t.TempDir()
	output := filepath.Join(dir, "out")
	entries := []*PresentedEntry{samplePresentedEntry(nil), samplePresentedEntry(nil)}
	if _, err := WriteEntries(entries, output); err == nil {
		t.Fatal("expected an error for a batch with a duplicate handle")
	}
}
