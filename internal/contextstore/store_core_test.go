package contextstore

import (
	"path/filepath"
	"strings"
	"testing"
)

// The last of the context-store comparison: the core invariants from
// roster/context-store/test/test_context_store.py and test_expiry.py that had
// no Go counterpart.

func TestTheContentHashCoversTheStoredContentNotTheOriginal(t *testing.T) {
	// If the hash were taken before redaction it would be a fingerprint of the
	// secret. Anyone holding a candidate value could hash it and compare --
	// so a redacted entry would still confirm what was redacted, to anyone who
	// could guess it.
	//
	// The hash also has to describe what is actually stored, or it stops being
	// a check on the stored bytes.
	cfg, _ := searchTestStore(t)
	const secret = "ghp_0123456789abcdefghijklmnopqrstuvwxyzAB" //nolint:gosec // fake token shaped to trip the redactor
	const content = "deployment notes token " + secret + " end"

	handle := storeEntry(t, cfg, PutOptions{
		Scope: "agent", Agent: "a", TaskID: "T-1", Classification: "internal",
		Source: "s", Label: "with-secret", Content: content,
	})

	db, err := OpenStore(cfg.Database, false)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer func() { _ = db.Close() }()

	var stored, storedHash string
	if err := db.QueryRow(
		"SELECT content, content_hash FROM entries WHERE handle = ?", handle,
	).Scan(&stored, &storedHash); err != nil {
		t.Fatalf("reading the entry: %v", err)
	}
	if strings.Contains(stored, secret) {
		t.Skip("the redactor did not treat this value as a secret; the check below would be vacuous")
	}

	if storedHash == ContentHash(content) {
		t.Error("content_hash is the hash of the original content, so it fingerprints the redacted secret")
	}
	if storedHash != ContentHash(stored) {
		t.Error("content_hash does not describe the stored content")
	}
}

func TestIdenticalContentGetsDistinctHandles(t *testing.T) {
	// A handle names an entry, not a piece of text. Deriving it from content
	// would make two agents storing the same thing collide -- and would let
	// anyone holding the content compute the handle and try to read it.
	cfg, _ := searchTestStore(t)
	base := PutOptions{
		Scope: "agent", Agent: "a", TaskID: "T-1", Classification: "internal",
		Source: "s", Label: "same", Content: "exactly the same content",
	}
	first := storeEntry(t, cfg, base)
	second := storeEntry(t, cfg, base)

	if first == second {
		t.Error("identical content produced the same handle")
	}
	for _, handle := range []string{first, second} {
		if _, err := ValidateHandle(handle); err != nil {
			t.Errorf("handle %q is not well formed: %v", handle, err)
		}
	}
}

func TestEveryCallerFieldIsRequiredOnEveryPath(t *testing.T) {
	// Agent, task and source are what attribution and partitioning are built
	// from. A path that let any of them default would write an entry nobody
	// can be held to, or read across a partition boundary by omission.
	cfg, _ := searchTestStore(t)
	db, err := OpenStore(cfg.Database, false)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer func() { _ = db.Close() }()

	complete := PutOptions{
		Scope: "agent", Agent: "a", TaskID: "T-1", Classification: "internal",
		Source: "s", Label: "e", Content: "content",
	}
	stored, err := PutEntry(db, cfg, complete)
	if err != nil {
		t.Fatalf("a complete put was refused: %v", err)
	}

	for name, blank := range map[string]func(*PutOptions){
		"agent":   func(o *PutOptions) { o.Agent = "" },
		"task_id": func(o *PutOptions) { o.TaskID = "" },
		"source":  func(o *PutOptions) { o.Source = "" },
	} {
		opts := complete
		blank(&opts)
		if _, err := PutEntry(db, cfg, opts); err == nil {
			t.Errorf("put accepted a blank %s", name)
		}
	}

	// And the same on the read paths, so a caller cannot omit its way past a
	// partition.
	for name, blank := range map[string]func(*CallerOptions){
		"agent":   func(c *CallerOptions) { c.Agent = "" },
		"task_id": func(c *CallerOptions) { c.TaskID = "" },
		"source":  func(c *CallerOptions) { c.Source = "" },
	} {
		caller := CallerOptions{Agent: "a", TaskID: "T-1", Classification: "internal", Source: "s"}
		blank(&caller)
		if _, err := GetEntry(db, GetOptions{Handle: stored.Handle, CallerOptions: caller}); err == nil {
			t.Errorf("get accepted a blank %s", name)
		}
		if _, err := SearchEntries(db, cfg, "anything", SearchOptions{CallerOptions: caller}); err == nil {
			t.Errorf("search accepted a blank %s", name)
		}
	}
}

func TestExpiryEvidenceRecordsThatSomethingLeftButNotWhatItSaid(t *testing.T) {
	// Evidence outlives the entry. It exists so an auditor can see that
	// material was released and when -- which means it must not itself become
	// a copy of the content that was supposed to be gone.
	cfg, _ := searchTestStore(t)
	const distinctive = "DISTINCTIVE-BODY-TEXT-0f3a9c"
	handle := storeEntry(t, cfg, PutOptions{
		Scope: "agent", Agent: "a", TaskID: "T-1", Classification: "internal",
		Source: "s", Label: "doomed", Content: distinctive,
	})

	db, err := OpenStore(cfg.Database, false)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := DropEntry(db, DropOptions{
		CallerOptions: CallerOptions{
			Agent: "a", TaskID: "T-1", Classification: "internal", Source: "s",
		},
		Handle: handle, Reason: "no longer needed",
	}); err != nil {
		t.Fatalf("DropEntry: %v", err)
	}

	rows, err := db.Query(
		"SELECT handle, content_hash, byte_length, reason FROM expiry_evidence WHERE handle = ?", handle)
	if err != nil {
		t.Fatalf("reading evidence: %v", err)
	}
	defer func() { _ = rows.Close() }()

	found := false
	for rows.Next() {
		var evidenceHandle, hash, reason string
		var byteLength int
		if err := rows.Scan(&evidenceHandle, &hash, &byteLength, &reason); err != nil {
			t.Fatal(err)
		}
		found = true
		if strings.Contains(hash, distinctive) || strings.Contains(reason, distinctive) {
			t.Error("expiry evidence retained the content it recorded the removal of")
		}
		if byteLength != len(distinctive) {
			t.Errorf("byte_length = %d, want the length of what was removed (%d)",
				byteLength, len(distinctive))
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("dropping an entry recorded no evidence")
	}

	// And the content itself is gone.
	var remaining int
	if err := db.QueryRow("SELECT COUNT(*) FROM entries WHERE handle = ?", handle).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Error("the entry survived the drop")
	}
}

func TestNoEntryCanBeStoredWithoutAnExpiry(t *testing.T) {
	// The store is working material with a window, not an archive. An entry
	// with no expiry would sit there indefinitely, which is the one outcome
	// the whole expiry mechanism exists to prevent -- so the column is NOT
	// NULL and the database refuses it directly, whatever the code above does.
	path := filepath.Join(t.TempDir(), "store.db")
	db, err := OpenStore(path, false)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer func() { _ = db.Close() }()

	_, err = db.Exec(
		`INSERT INTO entries (handle, scope, source, task_id, agent, label, tags_json, content,
		   content_hash, byte_length, classification, derived_from_json, redactions_json,
		   created_at, expires_at)
		 VALUES ('ctx_ffffffffffffffffffffffffffffffff','agent','s','t','a','no-expiry','[]','x','h',1,
		   'internal','[]','[]','2026-01-01T00:00:00Z', NULL)`)
	if err == nil {
		t.Error("the database accepted an entry with no expiry")
	}
}
