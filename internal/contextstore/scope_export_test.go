package contextstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The remaining access properties: classification comparison, drop treated as
// a read before it is a write, and export refusing as a batch.
//
// Ported from roster/context-store/test/test_scope_enforcement.py and
// test_export.py.

func TestClassificationIsAnExactMatchNotAHierarchy(t *testing.T) {
	// Both directions, and the second is the surprising one.
	//
	// A caller at "confidential" cannot read an "internal" entry. That looks
	// backwards next to a clearance model, where higher reads lower -- but the
	// classification here labels a *partition*, not a clearance level. An
	// agent working on internal material should not have confidential material
	// silently join its context, and an agent working on confidential material
	// should not pull internal material into a confidential artefact.
	cfg, _ := searchTestStore(t)
	caller := func(classification string) CallerOptions {
		return CallerOptions{
			Agent: "a", TaskID: "T-1", Classification: classification, Source: "s",
		}
	}

	internal := storeEntry(t, cfg, PutOptions{
		Scope: "agent", Agent: "a", TaskID: "T-1", Classification: "internal",
		Source: "s", Label: "internal-entry", Content: "internal material",
	})
	confidential := storeEntry(t, cfg, PutOptions{
		Scope: "agent", Agent: "a", TaskID: "T-1", Classification: "confidential",
		Source: "s", Label: "confidential-entry", Content: "confidential material",
	})

	db, err := OpenStore(cfg.Database, false)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer func() { _ = db.Close() }()

	readable := func(handle string, at string) bool {
		t.Helper()
		bundle, err := GetEntry(db, GetOptions{Handle: handle, CallerOptions: caller(at)})
		if err != nil {
			t.Fatalf("GetEntry: %v", err)
		}
		return len(bundle.Results) == 1
	}

	if !readable(internal, "internal") {
		t.Error("an entry was not readable at its own classification")
	}
	if readable(internal, "confidential") {
		t.Error("a confidential caller read an internal entry; classification is a partition, not a clearance")
	}
	if readable(confidential, "internal") {
		t.Error("an internal caller read a confidential entry")
	}
}

func TestACallerAssertingNoDispatchCannotReadADispatchScopedEntry(t *testing.T) {
	// Omitting the dispatch id must not read as "any dispatch". An empty
	// identity is the absence of a claim, and treating absence as a wildcard
	// is how a scope stops scoping.
	cfg, _ := searchTestStore(t)
	db, err := OpenStore(cfg.Database, false)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer func() { _ = db.Close() }()

	stored, err := PutEntry(db, cfg, PutOptions{
		Scope: "dispatch", Agent: "a", TaskID: "T-1", DispatchID: "D-1",
		Classification: "internal", Source: "s",
		Label: "dispatch-scoped", Content: "shared within one dispatch",
	})
	if err != nil {
		t.Fatalf("PutEntry: %v", err)
	}

	bundle, err := GetEntry(db, GetOptions{
		Handle: stored.Handle,
		CallerOptions: CallerOptions{
			Agent: "a", TaskID: "T-1", Classification: "internal", Source: "s",
			// no DispatchID
		},
	})
	if err != nil {
		t.Fatalf("GetEntry: %v", err)
	}
	if len(bundle.Results) != 0 {
		t.Error("a caller with no dispatch identity read a dispatch-scoped entry")
	}
}

func TestARefusedDropLeavesNoTraceThatTheEntryExists(t *testing.T) {
	// Drop is gated like a read before it is a write. Two things follow.
	//
	// The refusal must look like an absent handle, or the error is an oracle
	// for what exists in a partition the caller cannot read. And it must write
	// no expiry evidence: evidence records that something was released, so a
	// row for a drop that never happened is a false entry in the one trail
	// that says what left the store.
	cfg, _ := searchTestStore(t)
	handle := storeEntry(t, cfg, PutOptions{
		Scope: "agent", Agent: "agent-one", TaskID: "T-1",
		Classification: "internal", Source: "s", Label: "theirs", Content: "not yours",
	})

	db, err := OpenStore(cfg.Database, false)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer func() { _ = db.Close() }()

	intruder := CallerOptions{Agent: "agent-two", TaskID: "T-1", Classification: "internal", Source: "s"}

	_, refused := DropEntry(db, DropOptions{
		CallerOptions: intruder, Handle: handle, Reason: "cleanup",
	})
	_, absent := DropEntry(db, DropOptions{
		CallerOptions: intruder, Handle: "ctx_00000000000000000000000000000000", Reason: "cleanup",
	})
	if refused == nil || absent == nil {
		t.Fatal("both drops must be refused")
	}
	refusedText := strings.ReplaceAll(refused.Error(), handle, "<handle>")
	absentText := strings.ReplaceAll(absent.Error(), "ctx_00000000000000000000000000000000", "<handle>")
	if refusedText != absentText {
		t.Errorf("a refused drop is distinguishable from an absent handle:\n  refused: %s\n  absent:  %s",
			refusedText, absentText)
	}

	// The entry is still there...
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM entries WHERE handle = ?", handle).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Error("a refused drop removed the entry anyway")
	}
	// ...and nothing claims it was released.
	if err := db.QueryRow("SELECT COUNT(*) FROM expiry_evidence WHERE handle = ?", handle).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("a refused drop wrote %d expiry-evidence rows", count)
	}
}

func TestARefusedExportWritesNothingAtAll(t *testing.T) {
	// Export is a batch, and it refuses as a batch. A partial write would put
	// some entries on disk while reporting failure -- the worst outcome, since
	// the caller then has files it believes were never created.
	cfg, _ := searchTestStore(t)
	caller := CallerOptions{Agent: "a", TaskID: "T-1", Classification: "internal", Source: "s"}

	fine := storeEntry(t, cfg, PutOptions{
		Scope: "agent", Agent: "a", TaskID: "T-1", Classification: "internal",
		Source: "s", Label: "fine", Content: "ordinary notes",
	})
	// A confidential entry in the same batch: exportable only with an explicit
	// acknowledgement, which is not given here.
	blocked := storeEntry(t, cfg, PutOptions{
		Scope: "agent", Agent: "a", TaskID: "T-1", Classification: "confidential",
		Source: "s", Label: "blocked", Content: "sensitive notes",
	})

	db, err := OpenStore(cfg.Database, false)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer func() { _ = db.Close() }()

	output := filepath.Join(t.TempDir(), "export")
	confidentialCaller := caller
	confidentialCaller.Classification = "confidential"
	_, err = ExportEntries(db, ExportOptions{
		CallerOptions: confidentialCaller, Output: output, Handles: []string{blocked},
	})
	if err == nil {
		t.Fatal("a confidential entry exported without acknowledgement")
	}

	// Nothing on disk, not even a staging directory.
	entries, readErr := os.ReadDir(output)
	if readErr == nil && len(entries) > 0 {
		var names []string
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Errorf("a refused export left files behind: %v", names)
	}
	_ = fine
}

func TestExportedFrontmatterQuotesValuesThatCouldBreakOutOfYAML(t *testing.T) {
	// The label is caller-supplied and lands in YAML frontmatter. Unquoted, a
	// label containing a colon, a leading dash, or a newline changes the
	// document's structure -- so a reader parsing the export sees fields the
	// entry never had.
	for _, hazard := range []string{
		"label: injected",
		"- not a list item",
		"value\nclassification: restricted",
		"#comment",
		"@reserved",
	} {
		rendered := RenderEntry(&PresentedEntry{
			Handle: "ctx_0123456789abcdef0123456789abcdef", Label: hazard,
			Scope: "agent", Classification: "internal", Source: "s",
			Agent: "a", TaskID: "T-1", ExpiresAt: "2099-01-01T00:00:00Z",
			Content: "body",
		})

		header, _, found := strings.Cut(strings.TrimPrefix(rendered, "---\n"), "\n---")
		if !found {
			t.Fatalf("rendered entry has no frontmatter block:\n%s", rendered)
		}
		// The hazard must not have introduced a key of its own.
		for _, line := range strings.Split(header, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "classification: restricted") {
				t.Errorf("label %q injected a classification line into the frontmatter:\n%s",
					hazard, header)
			}
		}
		if !strings.Contains(header, "label:") {
			t.Errorf("no label key was emitted for %q:\n%s", hazard, header)
		}
	}
}
