package requirementissues

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deagy/cadre/cli/internal/engine/gitlabissue"
)

func itemsDocument(t *testing.T, keys ...string) []byte {
	t.Helper()
	items := make([]any, 0, len(keys))
	for _, key := range keys {
		items = append(items, map[string]any{
			"key": key, "title": "Requirement " + key, "description": "Do the thing for " + key,
		})
	}
	encoded, err := json.Marshal(map[string]any{
		"schema_version": ItemsSchemaVersion, "gate_id": "G2", "items": items,
	})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

// A mock covering identity, the label search, and creation.
func writeCreateMock(t *testing.T, labelsToIID map[string][]any, creates map[string]any) {
	t.Helper()
	mock := map[string]any{
		"identity": map[string]any{"username": "release-bot"},
		"search":   map[string]any{},
		"create":   map[string]any{},
	}
	for labels, results := range labelsToIID {
		mock["search"].(map[string]any)[labels] = results
	}
	for labels, created := range creates {
		mock["create"].(map[string]any)[labels] = created
	}
	encoded, _ := json.MarshalIndent(mock, "", "  ")
	path := filepath.Join(t.TempDir(), "mock.json")
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(gitlabissue.CreateMockEnvVar, path)
}

func TestParseItemsFileRefusesRatherThanTruncates(t *testing.T) {
	document := itemsDocument(t, "req-1", "req-2", "req-3")

	parsed, err := ParseItemsFile(document, 10)
	if err != nil {
		t.Fatalf("ParseItemsFile: %v", err)
	}
	if len(parsed.Items) != 3 {
		t.Errorf("parsed %d items, want 3", len(parsed.Items))
	}

	// Over the cap the whole file is refused. Publishing some requirements and
	// silently dropping others leaves the operator unable to tell which.
	if _, err := ParseItemsFile(document, 2); err == nil {
		t.Error("an over-long items file was accepted")
	} else if !strings.Contains(err.Error(), "refusing rather than truncating") {
		t.Errorf("error was %q", err)
	}
}

func TestParseItemsFileValidation(t *testing.T) {
	refused := map[string]string{
		"not json":       `{`,
		"wrong schema":   `{"schema_version": 2, "gate_id": "G2", "items": [{"key":"a","title":"t","description":"d"}]}`,
		"wrong gate":     `{"schema_version": 1, "gate_id": "G3", "items": [{"key":"a","title":"t","description":"d"}]}`,
		"empty items":    `{"schema_version": 1, "gate_id": "G2", "items": []}`,
		"bad key":        `{"schema_version": 1, "gate_id": "G2", "items": [{"key":"-bad","title":"t","description":"d"}]}`,
		"duplicate keys": `{"schema_version": 1, "gate_id": "G2", "items": [{"key":"a","title":"t","description":"d"},{"key":"a","title":"t2","description":"d2"}]}`,
	}
	for name, document := range refused {
		if _, err := ParseItemsFile([]byte(document), 50); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// A content hash covers the title and the description together.
func TestContentHashCoversBothFields(t *testing.T) {
	base, _ := ParseItemsFile([]byte(`{"schema_version":1,"gate_id":"G2","items":[{"key":"a","title":"t","description":"d"}]}`), 50)
	titleChanged, _ := ParseItemsFile([]byte(`{"schema_version":1,"gate_id":"G2","items":[{"key":"a","title":"T","description":"d"}]}`), 50)
	descChanged, _ := ParseItemsFile([]byte(`{"schema_version":1,"gate_id":"G2","items":[{"key":"a","title":"t","description":"D"}]}`), 50)

	if base.Items[0].ContentHash == titleChanged.Items[0].ContentHash {
		t.Error("a changed title did not change the content hash")
	}
	if base.Items[0].ContentHash == descChanged.Items[0].ContentHash {
		t.Error("a changed description did not change the content hash")
	}
}

// A dry run produces the plan and its digest, and touches nothing.
func TestDryRunPublishesNothing(t *testing.T) {
	root := t.TempDir()
	result, err := Publish(PublishRequest{
		Root: root, TaskID: "task-1", Project: "group/project",
		ItemsRaw: itemsDocument(t, "req-1"), Eligibility: Eligibility{GateStatus: "ready"},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if result["mode"] != "dry-run" || result["plan_digest"] == "" {
		t.Errorf("result = %v", result)
	}
	if _, err := os.Stat(ledgerPath(root, "task-1")); err == nil {
		t.Error("a dry run wrote a ledger")
	}
}

// An apply requires the digest from the dry run that produced it.
func TestApplyRequiresAMatchingPlanDigest(t *testing.T) {
	root := t.TempDir()
	items := itemsDocument(t, "req-1")
	eligibility := Eligibility{GateStatus: "ready"}

	// The message matters, not just the refusal: without the explicit check
	// the mismatch test below catches it anyway, but reports "recomputed X !=
	// supplied ''", which tells an operator to compare a digest they never
	// had rather than to run a dry run.
	_, err := Publish(PublishRequest{
		Root: root, TaskID: "task-1", Project: "group/project", ItemsRaw: items,
		Apply: true, AsBot: "release-bot", Eligibility: eligibility,
	})
	if err == nil {
		t.Error("an apply with no plan digest was accepted")
	} else if !strings.Contains(err.Error(), "requires a plan digest") {
		t.Errorf("error was %q, want it to say a dry run is needed", err)
	}

	if _, err := Publish(PublishRequest{
		Root: root, TaskID: "task-1", Project: "group/project", ItemsRaw: items,
		Apply: true, PlanDigest: "sha256:wrong", AsBot: "release-bot", Eligibility: eligibility,
	}); err == nil {
		t.Error("an apply with a mismatched digest was accepted")
	} else if _, blocked := err.(Blocked); !blocked {
		t.Errorf("a digest mismatch is %T, want Blocked so a caller can tell it from a defect", err)
	}
}

// A re-entry between the dry run and the apply invalidates the digest.
//
// This is what makes the digest a safety mechanism rather than a checksum: the
// requirements were agreed against a run state that no longer holds.
func TestAReentryBetweenPlanAndApplyInvalidatesTheDigest(t *testing.T) {
	root := t.TempDir()
	items := itemsDocument(t, "req-1")

	planned, err := Publish(PublishRequest{
		Root: root, TaskID: "task-1", Project: "group/project", ItemsRaw: items,
		Eligibility: Eligibility{GateStatus: "ready", ReEntryCount: 0},
	})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	digest, _ := planned["plan_digest"].(string)

	_, err = Publish(PublishRequest{
		Root: root, TaskID: "task-1", Project: "group/project", ItemsRaw: items,
		Apply: true, PlanDigest: digest, AsBot: "release-bot",
		Eligibility: Eligibility{GateStatus: "ready", ReEntryCount: 1},
	})
	if err == nil {
		t.Fatal("an apply proceeded after a re-entry changed the run state")
	}
	if _, blocked := err.(Blocked); !blocked {
		t.Errorf("error is %T, want Blocked", err)
	}
}

// Applying against a mocked backend must be acknowledged.
//
// A mocked apply writes a ledger and reports created issues exactly as a real
// one does. Without this, a mocked run is indistinguishable from a published
// one until somebody opens the project.
func TestApplyingAgainstAMockMustBeAcknowledged(t *testing.T) {
	root := t.TempDir()
	items := itemsDocument(t, "req-1")
	eligibility := Eligibility{GateStatus: "ready"}

	planned, err := Publish(PublishRequest{
		Root: root, TaskID: "task-1", Project: "group/project", ItemsRaw: items, Eligibility: eligibility,
	})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	digest, _ := planned["plan_digest"].(string)

	// A *complete* mock, so the apply would otherwise succeed. With an
	// incomplete one the apply fails at creation regardless, and the test
	// passes whether or not the acknowledgement is enforced.
	marker := ComputeMarker("task-1", "G2", "req-1")
	label, _ := ItemLabel(marker)
	writeCreateMock(t,
		map[string][]any{FixedLabel + "," + label: {}},
		map[string]any{FixedLabel + "," + label: map[string]any{"iid": float64(101)}},
	)

	_, err = Publish(PublishRequest{
		Root: root, TaskID: "task-1", Project: "group/project", ItemsRaw: items,
		Apply: true, PlanDigest: digest, AsBot: "release-bot", Eligibility: eligibility,
	})
	if err == nil {
		t.Fatal("an apply against a mocked backend proceeded unacknowledged")
	}
	if !strings.Contains(err.Error(), "mocked") {
		t.Errorf("error was %q, want it to name the mock", err)
	}
}

// A full apply, acknowledged, records what it published and is idempotent.
func TestApplyRecordsAndIsIdempotent(t *testing.T) {
	root := t.TempDir()
	items := itemsDocument(t, "req-1")
	eligibility := Eligibility{GateStatus: "ready"}

	planned, err := Publish(PublishRequest{
		Root: root, TaskID: "task-1", Project: "group/project", ItemsRaw: items, Eligibility: eligibility,
	})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	digest, _ := planned["plan_digest"].(string)

	marker := ComputeMarker("task-1", "G2", "req-1")
	label, _ := ItemLabel(marker)
	writeCreateMock(t,
		map[string][]any{FixedLabel + "," + label: {}},
		map[string]any{FixedLabel + "," + label: map[string]any{"iid": float64(101)}},
	)

	apply := PublishRequest{
		Root: root, TaskID: "task-1", Project: "group/project", ItemsRaw: items,
		Apply: true, PlanDigest: digest, AsBot: "release-bot",
		AcknowledgeMock: true, Eligibility: eligibility,
	}
	result, err := Publish(apply)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	// The confirmed bot name is recorded, not the requested one.
	if result["bot_username"] != "release-bot" {
		t.Errorf("bot_username = %v", result["bot_username"])
	}
	if result["mocked"] != true {
		t.Error("a mocked apply did not record that it was mocked")
	}

	ledger, err := ReadLedger(root, "task-1")
	if err != nil {
		t.Fatalf("ReadLedger: %v", err)
	}
	entry, published := ledger.Entries["req-1"]
	if !published || entry.IID != 101 {
		t.Fatalf("ledger entry = %+v", entry)
	}

	// Publishing again must not create a second issue.
	second, err := Publish(apply)
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	results, _ := second["results"].([]any)
	first, _ := results[0].(map[string]any)
	if first["status"] != "already-published" {
		t.Errorf("second apply reported %v, want already-published", first["status"])
	}
}

// The lock is never broken implicitly.
func TestTheLockIsHeldUntilExplicitlyBroken(t *testing.T) {
	root := t.TempDir()

	held, err := AcquireLock(root, "task-1", false)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}

	if _, err := AcquireLock(root, "task-1", false); err == nil {
		t.Fatal("a second publisher acquired a held lock")
	} else if _, blocked := err.(Blocked); !blocked {
		t.Errorf("a held lock reports %T, want Blocked", err)
	}

	// Breaking it is explicit, never a timeout: a lock that looks stale may
	// belong to a run mid-publish, and breaking it automatically is how the
	// same requirements get filed twice.
	if _, err := AcquireLock(root, "task-1", true); err != nil {
		t.Errorf("an explicit break failed: %v", err)
	}

	ReleaseLock(held)
	if _, err := AcquireLock(root, "task-1", false); err != nil {
		t.Errorf("a released lock could not be retaken: %v", err)
	}
}

// Publishing is refused outright from an ineligible run, before parsing.
func TestAnIneligibleRunPublishesNothing(t *testing.T) {
	_, err := Publish(PublishRequest{
		Root: t.TempDir(), TaskID: "task-1", Project: "group/project",
		ItemsRaw:    itemsDocument(t, "req-1"),
		Eligibility: Eligibility{RunHalted: true, GateStatus: "ready"},
	})
	if err == nil {
		t.Fatal("a halted run published")
	}
	if _, blocked := err.(Blocked); !blocked {
		t.Errorf("error is %T, want Blocked", err)
	}
}
