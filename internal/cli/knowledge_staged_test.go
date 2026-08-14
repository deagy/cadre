package cli

// CLI-level tests for the staged-records subsystem, with the negative
// separation cases as the point.
//
// Ported from roster/knowledge-store/test/test_staged_cli.py on main. Every
// refusal test also asserts that nothing was written -- a guard that refuses
// loudly and stages anyway is not a guard.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deagy/cadre/cli/internal/knowledge"
)

const (
	testProposer   = "proposing-agent"
	testSteward    = "knowledge-store-steward"
	testAuthorizer = "an authorized human"
	testBody       = "The staged-records subsystem enforces authorship/approval separation.\n"
)

func testFrontmatter(recordID string) map[string]any {
	return map[string]any{
		"id":                         recordID,
		"title":                      "An example finding",
		"status":                     "proposed",
		"evidence":                   []any{"internal/cli/knowledge_staged.go:1"},
		"origin":                     map[string]any{"task": "TASK-1", "artifact": "internal/cli/knowledge_staged.go", "revision": "abc1234"},
		"proposed_classification":    "internal",
		"source_scope":               "cadre",
		"sensitivity_notes":          "",
		"conflicts_or_staleness":     "",
		"recommended_action":         "ingest",
		"untrusted_instruction_risk": false,
		"staged_by":                  testProposer,
		"content_digest":             knowledge.ComputeStagedDigest(testBody),
	}
}

func testDisposition(decidedBy string) map[string]any {
	return map[string]any{
		"action":                 "accepted",
		"reason":                 "reviewed and reproducible",
		"classification_used":    "internal",
		"diverged_from_proposal": false,
		"decided_by":             decidedBy,
	}
}

func testStagedDB(t *testing.T) string {
	t.Helper()
	requireSQLite(t)
	return filepath.Join(t.TempDir(), "knowledge.db")
}

func writeRecordFile(t *testing.T, directory string, frontmatter map[string]any) string {
	t.Helper()
	text, err := knowledge.SerializeStagedRecord(frontmatter, testBody)
	if err != nil {
		t.Fatalf("cannot serialise record: %v", err)
	}
	path := filepath.Join(directory, knowledge.StagedString(frontmatter, "id")+".md")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatalf("cannot write record file: %v", err)
	}
	return path
}

// listStaged reads the staging table directly rather than through a CLI verb,
// so "nothing was written" is asserted against the store itself.
func listStaged(t *testing.T, dbPath string) []knowledge.StagedSummary {
	t.Helper()
	store, err := knowledge.Open(dbPath)
	if err != nil {
		t.Fatalf("cannot open store: %v", err)
	}
	defer func() { _ = store.Close() }()
	if err := store.InstallStagedSchema(); err != nil {
		t.Fatalf("cannot install staged schema: %v", err)
	}
	records, err := store.ListStagedRecords("")
	if err != nil {
		t.Fatalf("cannot list staged records: %v", err)
	}
	return records
}

// ---------------------------------------------------------------------------
// Routing
// ---------------------------------------------------------------------------

func TestKnowledgeStagedRouteRecognisesTheStagedVerbs(t *testing.T) {
	routed := map[string][]string{
		"bare verb":            {"propose", "--input", "x.md"},
		"after -config":        {"-config", "/tmp/store.db", "show-staged", "--id", "KS-1"},
		"after --config=":      {"--config=/tmp/store.db", "delete-staged"},
		"after a boolean flag": {"--verbose", "ingest-accepted"},
	}
	for name, args := range routed {
		if !KnowledgeStagedRoute(args) {
			t.Errorf("%s: expected %v to route to the staged handler", name, args)
		}
	}
	notRouted := map[string][]string{
		"other subcommand":  {"search", "--query", "propose"},
		"config value only": {"-config", "propose"},
		"no subcommand":     {"-config", "/tmp/store.db"},
		"empty":             {},
	}
	for name, args := range notRouted {
		if KnowledgeStagedRoute(args) {
			t.Errorf("%s: expected %v NOT to route to the staged handler", name, args)
		}
	}
}

// ---------------------------------------------------------------------------
// propose -- SEPARATION CHECK 1
// ---------------------------------------------------------------------------

func TestProposeStagesAWellFormedRecord(t *testing.T) {
	dbPath := testStagedDB(t)
	directory := t.TempDir()
	path := writeRecordFile(t, directory, testFrontmatter("KS-20260101-ok"))

	result, err := runKnowledgeStaged(dbPath, "propose", []string{"--input", path})
	if err != nil {
		t.Fatalf("a well-formed proposal was refused: %v", err)
	}
	encoded, _ := json.Marshal(result)
	if !strings.Contains(string(encoded), `"status":"staged"`) {
		t.Fatalf("unexpected propose result: %s", encoded)
	}
	if records := listStaged(t, dbPath); len(records) != 1 {
		t.Fatalf("expected one staged record, got %d", len(records))
	}
}

func TestProposeRefusesARecordThatArrivesAlreadyAccepted(t *testing.T) {
	dbPath := testStagedDB(t)
	directory := t.TempDir()
	frontmatter := testFrontmatter("KS-20260101-pre-accepted")
	frontmatter["status"] = "accepted"
	frontmatter["disposition"] = testDisposition(testSteward)
	path := writeRecordFile(t, directory, frontmatter)

	_, err := runKnowledgeStaged(dbPath, "propose", []string{"--input", path})
	if err == nil {
		t.Fatal("expected a pre-dispositioned proposal to be refused")
	}
	if !strings.Contains(err.Error(), "propose refuses a record whose status is") {
		t.Fatalf("refusal does not name the rule: %v", err)
	}
	if records := listStaged(t, dbPath); len(records) != 0 {
		t.Fatalf("a refused proposal was staged anyway: %+v", records)
	}
}

// The disposition block is refused even when status is still 'proposed' --
// the check rejects the shape of a decided record, not just an inconsistent
// status. This is the name-independent half of the guard.
func TestProposeRefusesADispositionBlockEvenOnAProposedRecord(t *testing.T) {
	dbPath := testStagedDB(t)
	directory := t.TempDir()
	frontmatter := testFrontmatter("KS-20260101-sneaky")
	frontmatter["disposition"] = testDisposition(testSteward)
	path := writeRecordFile(t, directory, frontmatter)

	_, err := runKnowledgeStaged(dbPath, "propose", []string{"--input", path})
	if err == nil {
		t.Fatal("expected a disposition block on a proposal to be refused")
	}
	if !strings.Contains(err.Error(), "propose refuses a record carrying a `disposition` block") {
		t.Fatalf("refusal does not name the rule: %v", err)
	}
	if records := listStaged(t, dbPath); len(records) != 0 {
		t.Fatalf("a refused proposal was staged anyway: %+v", records)
	}
}

func TestRenderOnlyRefusesAPreDispositionedRecordToo(t *testing.T) {
	dbPath := testStagedDB(t)
	directory := t.TempDir()
	frontmatter := testFrontmatter("KS-20260101-render-refused")
	frontmatter["status"] = "accepted"
	frontmatter["disposition"] = testDisposition(testSteward)
	path := writeRecordFile(t, directory, frontmatter)

	_, err := runKnowledgeStaged(dbPath, "propose", []string{"--input", path, "--render-only"})
	if err == nil {
		t.Fatal("expected --render-only to apply the same refusal")
	}
	if records := listStaged(t, dbPath); len(records) != 0 {
		t.Fatalf("--render-only staged a record: %+v", records)
	}
}

func TestRenderOnlyDoesNotStage(t *testing.T) {
	dbPath := testStagedDB(t)
	directory := t.TempDir()
	path := writeRecordFile(t, directory, testFrontmatter("KS-20260101-render"))

	result, err := runKnowledgeStaged(dbPath, "propose", []string{"--input", path, "--render-only"})
	if err != nil {
		t.Fatalf("--render-only refused a valid record: %v", err)
	}
	rendered, ok := result.(map[string]any)
	if !ok || rendered["status"] != "rendered" {
		t.Fatalf("unexpected render result: %#v", result)
	}
	if records := listStaged(t, dbPath); len(records) != 0 {
		t.Fatalf("--render-only staged a record: %+v", records)
	}
}

func TestProposeFromFindingGeneratesTheCeremonyFields(t *testing.T) {
	dbPath := testStagedDB(t)
	directory := t.TempDir()
	finding := map[string]any{
		"summary":                    testBody,
		"title":                      "A generated finding",
		"evidence":                   []any{"internal/cli/knowledge_staged.go:1"},
		"origin":                     map[string]any{"task": "TASK-2", "artifact": "internal/cli", "revision": "def5678"},
		"proposed_classification":    "internal",
		"source_scope":               "cadre",
		"sensitivity_notes":          "",
		"conflicts_or_staleness":     "",
		"recommended_action":         "ingest",
		"untrusted_instruction_risk": false,
		"staged_by":                  testProposer,
	}
	encoded, err := json.Marshal(finding)
	if err != nil {
		t.Fatalf("cannot encode finding: %v", err)
	}
	path := filepath.Join(directory, "finding.json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatalf("cannot write finding: %v", err)
	}

	result, err := runKnowledgeStaged(dbPath, "propose", []string{"--from-finding", path})
	if err != nil {
		t.Fatalf("--from-finding was refused: %v", err)
	}
	generated, ok := result.(*knowledge.GeneratedStagedResult)
	if !ok || generated.Status != "staged" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if !strings.HasPrefix(generated.ID, "KS-") {
		t.Fatalf("generated id does not match the contract: %q", generated.ID)
	}
	if generated.RecordStatus != "proposed" {
		t.Fatalf("--from-finding produced status %q, want proposed", generated.RecordStatus)
	}
}

func TestProposeRefusesBothOrNeitherInput(t *testing.T) {
	dbPath := testStagedDB(t)
	for name, args := range map[string][]string{
		"neither": {},
		"both":    {"--input", "a.md", "--from-finding", "b.json"},
	} {
		if _, err := runKnowledgeStaged(dbPath, "propose", args); err == nil {
			t.Errorf("%s: expected propose to require exactly one input", name)
		}
	}
}

// ---------------------------------------------------------------------------
// import-staged -- SEPARATION CHECK 3
// ---------------------------------------------------------------------------

func TestADispositionedBatchIsRefusedWithoutAuthorization(t *testing.T) {
	dbPath := testStagedDB(t)
	directory := t.TempDir()
	frontmatter := testFrontmatter("KS-20260101-decided")
	frontmatter["status"] = "accepted"
	frontmatter["disposition"] = testDisposition(testSteward)
	writeRecordFile(t, directory, frontmatter)

	_, err := runKnowledgeStaged(dbPath, "import-staged", []string{"--directory", directory})
	if err == nil {
		t.Fatal("expected a dispositioned batch to require --authorized-by")
	}
	if !strings.Contains(err.Error(), "requires --authorized-by") {
		t.Fatalf("refusal does not name the gate: %v", err)
	}
	if records := listStaged(t, dbPath); len(records) != 0 {
		t.Fatalf("a refused batch was imported anyway: %+v", records)
	}
}

func TestAProposedOnlyBatchNeedsNoAuthorization(t *testing.T) {
	dbPath := testStagedDB(t)
	directory := t.TempDir()
	writeRecordFile(t, directory, testFrontmatter("KS-20260101-plain-a"))
	writeRecordFile(t, directory, testFrontmatter("KS-20260101-plain-b"))

	result, err := runKnowledgeStaged(dbPath, "import-staged", []string{"--directory", directory})
	if err != nil {
		t.Fatalf("a proposed-only batch was refused: %v", err)
	}
	imported, ok := result.(map[string]any)
	if !ok || imported["count"] != 2 {
		t.Fatalf("unexpected import result: %#v", result)
	}
	if _, recorded := imported["authorization_recorded"]; recorded {
		t.Fatal("a proposed-only batch recorded an authorization it never needed")
	}
}

// One dispositioned record gates the whole batch: a partial import is the
// wrong outcome for a migration.
func TestOneDispositionedRecordGatesTheWholeBatch(t *testing.T) {
	dbPath := testStagedDB(t)
	directory := t.TempDir()
	writeRecordFile(t, directory, testFrontmatter("KS-20260101-plain"))
	decided := testFrontmatter("KS-20260101-mixed-decided")
	decided["status"] = "accepted"
	decided["disposition"] = testDisposition(testSteward)
	writeRecordFile(t, directory, decided)

	if _, err := runKnowledgeStaged(dbPath, "import-staged", []string{"--directory", directory}); err == nil {
		t.Fatal("expected the whole batch to be gated")
	}
	if records := listStaged(t, dbPath); len(records) != 0 {
		t.Fatalf("a gated batch imported %d record(s)", len(records))
	}
}

func TestWhitespaceIsNotAnAuthorization(t *testing.T) {
	dbPath := testStagedDB(t)
	directory := t.TempDir()
	frontmatter := testFrontmatter("KS-20260101-blank-authorizer")
	frontmatter["status"] = "accepted"
	frontmatter["disposition"] = testDisposition(testSteward)
	writeRecordFile(t, directory, frontmatter)

	_, err := runKnowledgeStaged(dbPath, "import-staged",
		[]string{"--directory", directory, "--authorized-by", "   "})
	if err == nil {
		t.Fatal("expected whitespace to fail the authorization gate")
	}
	if records := listStaged(t, dbPath); len(records) != 0 {
		t.Fatalf("a refused batch was imported anyway: %+v", records)
	}
}

func TestTheAuthorizationIsPersistedNotOnlyEchoed(t *testing.T) {
	dbPath := testStagedDB(t)
	directory := t.TempDir()
	recordID := "KS-20260101-admitted"
	frontmatter := testFrontmatter(recordID)
	frontmatter["status"] = "accepted"
	frontmatter["disposition"] = testDisposition(testSteward)
	writeRecordFile(t, directory, frontmatter)

	if _, err := runKnowledgeStaged(dbPath, "import-staged",
		[]string{"--directory", directory, "--authorized-by", "  " + testAuthorizer + "  "}); err != nil {
		t.Fatalf("an authorized import was refused: %v", err)
	}

	store, err := knowledge.Open(dbPath)
	if err != nil {
		t.Fatalf("cannot open store: %v", err)
	}
	defer func() { _ = store.Close() }()
	authorizations, err := store.StagedImportAuthorizations(recordID)
	if err != nil {
		t.Fatalf("cannot read import authorizations: %v", err)
	}
	if len(authorizations) != 1 {
		t.Fatalf("expected exactly one persisted authorization, got %d", len(authorizations))
	}
	if authorizations[0].AuthorizedBy != testAuthorizer {
		t.Fatalf("authorizer stored as %q, want %q (untrimmed?)", authorizations[0].AuthorizedBy, testAuthorizer)
	}
	if authorizations[0].StatusAtImport != "accepted" {
		t.Fatalf("unexpected status_at_import: %q", authorizations[0].StatusAtImport)
	}
}

// The one thing no named human can vouch for: a record whose stager and
// decider are the same actor is not a decision, so there is nothing to
// authorize. Refused with --authorized-by present and correct.
func TestAuthorizationCannotLaunderASelfApproval(t *testing.T) {
	dbPath := testStagedDB(t)
	directory := t.TempDir()
	frontmatter := testFrontmatter("KS-20260101-laundered")
	frontmatter["status"] = "accepted"
	frontmatter["disposition"] = testDisposition(testProposer)
	writeRecordFile(t, directory, frontmatter)

	_, err := runKnowledgeStaged(dbPath, "import-staged",
		[]string{"--directory", directory, "--authorized-by", testAuthorizer})
	if err == nil {
		t.Fatal("expected a self-approved record to be refused regardless of authorization")
	}
	if !strings.Contains(err.Error(), testProposer) {
		t.Fatalf("refusal does not name the actor: %v", err)
	}
	if !strings.Contains(err.Error(), "self-approval") {
		t.Fatalf("refusal does not name the rule: %v", err)
	}
	if records := listStaged(t, dbPath); len(records) != 0 {
		t.Fatalf("a self-approved record was imported: %+v", records)
	}
}

// An earlier self-decision hidden behind a legitimate latest one is the same
// laundering with an extra step.
func TestASidecarCannotLaunderASelfApproval(t *testing.T) {
	dbPath := testStagedDB(t)
	directory := t.TempDir()
	recordID := "KS-20260101-sidecar-laundered"
	frontmatter := testFrontmatter(recordID)
	frontmatter["status"] = "accepted"
	frontmatter["disposition"] = testDisposition(testSteward)
	writeRecordFile(t, directory, frontmatter)

	history := []map[string]any{
		{
			"sequence": 1, "action": "deferred", "reason": "self deferred first",
			"classification_used": "internal", "diverged_from_proposal": false,
			"decided_by": testProposer, "decided_at": "2026-01-01T00:00:00.000Z",
		},
		{
			"sequence": 2, "action": "accepted", "reason": "reviewed and reproducible",
			"classification_used": "internal", "diverged_from_proposal": false,
			"decided_by": testSteward, "decided_at": "2026-01-02T00:00:00.000Z",
		},
	}
	encoded, err := json.Marshal(history)
	if err != nil {
		t.Fatalf("cannot encode history: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, recordID+".history.json"), encoded, 0o600); err != nil {
		t.Fatalf("cannot write sidecar: %v", err)
	}

	_, err = runKnowledgeStaged(dbPath, "import-staged",
		[]string{"--directory", directory, "--authorized-by", testAuthorizer})
	if err == nil {
		t.Fatal("expected a self-approval in the history sidecar to be refused")
	}
	if !strings.Contains(err.Error(), "launder a self-approval through a history sidecar") {
		t.Fatalf("refusal does not name the rule: %v", err)
	}
	if records := listStaged(t, dbPath); len(records) != 0 {
		t.Fatalf("a laundered batch was imported: %+v", records)
	}
}

func TestAValidSidecarRestoresTheDispositionHistory(t *testing.T) {
	dbPath := testStagedDB(t)
	directory := t.TempDir()
	recordID := "KS-20260101-with-history"
	frontmatter := testFrontmatter(recordID)
	frontmatter["status"] = "accepted"
	frontmatter["disposition"] = testDisposition(testSteward)
	writeRecordFile(t, directory, frontmatter)

	history := []map[string]any{{
		"sequence": 1, "action": "accepted", "reason": "reviewed and reproducible",
		"classification_used": "internal", "diverged_from_proposal": false,
		"decided_by": testSteward, "decided_at": "2026-01-02T00:00:00.000Z",
	}}
	encoded, err := json.Marshal(history)
	if err != nil {
		t.Fatalf("cannot encode history: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, recordID+".history.json"), encoded, 0o600); err != nil {
		t.Fatalf("cannot write sidecar: %v", err)
	}

	result, err := runKnowledgeStaged(dbPath, "import-staged",
		[]string{"--directory", directory, "--authorized-by", testAuthorizer})
	if err != nil {
		t.Fatalf("a valid sidecar import was refused: %v", err)
	}
	imported, ok := result.(map[string]any)
	if !ok || imported["disposition_history_rows_restored"] != 1 {
		t.Fatalf("history was not restored: %#v", result)
	}
}

func TestImportRejectsADirectoryWithNoRecords(t *testing.T) {
	dbPath := testStagedDB(t)
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "README.md"), []byte("# not a record\n"), 0o600); err != nil {
		t.Fatalf("cannot write README: %v", err)
	}
	// README.md is skipped by name, so a directory holding only one is empty
	// as far as the importer is concerned -- and says so rather than failing
	// on the README's missing frontmatter.
	_, err := runKnowledgeStaged(dbPath, "import-staged", []string{"--directory", directory})
	if err == nil || !strings.Contains(err.Error(), "no .md staged-record files found") {
		t.Fatalf("expected an empty-directory refusal, got %v", err)
	}
}

func TestAnUnparseableFileOtherThanReadmeFailsLoudly(t *testing.T) {
	dbPath := testStagedDB(t)
	directory := t.TempDir()
	writeRecordFile(t, directory, testFrontmatter("KS-20260101-good"))
	if err := os.WriteFile(filepath.Join(directory, "notes.md"), []byte("no frontmatter here\n"), 0o600); err != nil {
		t.Fatalf("cannot write stray file: %v", err)
	}

	if _, err := runKnowledgeStaged(dbPath, "import-staged", []string{"--directory", directory}); err == nil {
		t.Fatal("expected an unparseable file to fail the batch")
	}
	if records := listStaged(t, dbPath); len(records) != 0 {
		t.Fatalf("a failed batch imported %d record(s)", len(records))
	}
}

// ---------------------------------------------------------------------------
// disposition-staged, delete-staged, show-staged, ingest-accepted
// ---------------------------------------------------------------------------

func stageThroughCLI(t *testing.T, dbPath, recordID string) {
	t.Helper()
	directory := t.TempDir()
	path := writeRecordFile(t, directory, testFrontmatter(recordID))
	if _, err := runKnowledgeStaged(dbPath, "propose", []string{"--input", path}); err != nil {
		t.Fatalf("cannot stage %s: %v", recordID, err)
	}
}

func TestDispositionStagedRefusesTheProposerThroughTheCLI(t *testing.T) {
	dbPath := testStagedDB(t)
	recordID := "KS-20260101-cli-self"
	stageThroughCLI(t, dbPath, recordID)

	_, err := runKnowledgeStaged(dbPath, "disposition-staged", []string{
		"--id", recordID, "--action", "accepted", "--reason", "self approval",
		"--classification-used", "internal", "--decided-by", testProposer,
	})
	if err == nil || !strings.Contains(err.Error(), "cannot also disposition it") {
		t.Fatalf("expected the CLI to refuse a self-disposition, got %v", err)
	}
}

func TestDispositionStagedRequiresEveryIdentityField(t *testing.T) {
	dbPath := testStagedDB(t)
	recordID := "KS-20260101-cli-missing"
	stageThroughCLI(t, dbPath, recordID)

	// Missing --decided-by is the case that matters: without it there is no
	// identity to compare against staged_by, and the separation check would
	// have nothing to refuse on.
	_, err := runKnowledgeStaged(dbPath, "disposition-staged", []string{
		"--id", recordID, "--action", "accepted", "--reason", "r", "--classification-used", "internal",
	})
	if err == nil || !strings.Contains(err.Error(), "--decided-by") {
		t.Fatalf("expected a missing --decided-by to be refused by name, got %v", err)
	}
}

func TestShowStagedReturnsTheFullRecordAndItsHistory(t *testing.T) {
	dbPath := testStagedDB(t)
	recordID := "KS-20260101-cli-show"
	stageThroughCLI(t, dbPath, recordID)
	if _, err := runKnowledgeStaged(dbPath, "disposition-staged", []string{
		"--id", recordID, "--action", "accepted", "--reason", "reviewed",
		"--classification-used", "internal", "--decided-by", testSteward,
	}); err != nil {
		t.Fatalf("cannot disposition: %v", err)
	}

	result, err := runKnowledgeStaged(dbPath, "show-staged", []string{"--id", recordID})
	if err != nil {
		t.Fatalf("show-staged failed: %v", err)
	}
	shown, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected show result: %#v", result)
	}
	history, ok := shown["disposition_history"].([]knowledge.DispositionEntry)
	if !ok || len(history) != 1 || history[0].DecidedBy != testSteward {
		t.Fatalf("disposition history missing or wrong: %#v", shown["disposition_history"])
	}
	if text, ok := shown["text"].(string); !ok || !strings.Contains(text, recordID) {
		t.Fatal("show-staged did not return the full record text")
	}
}

func TestShowStagedNamesAnUnknownIDRatherThanReturningEmpty(t *testing.T) {
	dbPath := testStagedDB(t)
	_, err := runKnowledgeStaged(dbPath, "show-staged", []string{"--id", "KS-20260101-nope"})
	if err == nil || !strings.Contains(err.Error(), "KS-20260101-nope") {
		t.Fatalf("expected an unknown id to be named, got %v", err)
	}
}

func TestDeleteStagedRefusesTheProposerOnADecidedRecordThroughTheCLI(t *testing.T) {
	dbPath := testStagedDB(t)
	recordID := "KS-20260101-cli-delete"
	stageThroughCLI(t, dbPath, recordID)
	if _, err := runKnowledgeStaged(dbPath, "disposition-staged", []string{
		"--id", recordID, "--action", "rejected", "--reason", "not reproducible",
		"--classification-used", "internal", "--decided-by", testSteward,
	}); err != nil {
		t.Fatalf("cannot disposition: %v", err)
	}

	_, err := runKnowledgeStaged(dbPath, "delete-staged", []string{
		"--id", recordID, "--reason", "trying to erase the outcome",
		"--deleted-by", testProposer, "--authorized-by", testAuthorizer,
	})
	if err == nil || !strings.Contains(err.Error(), "already carries a disposition") {
		t.Fatalf("expected the CLI to refuse the proposer's deletion, got %v", err)
	}
	if records := listStaged(t, dbPath); len(records) != 1 {
		t.Fatalf("a refused deletion removed the record: %+v", records)
	}
}

// ingest-accepted takes no --authorized-by and no --decided-by: it executes a
// steward decision rather than taking one. This test pins that flag surface so
// a human-approval gate cannot be reintroduced here without failing a test
// that says why it was removed.
func TestIngestAcceptedTakesNoApprovalFlags(t *testing.T) {
	dbPath := testStagedDB(t)
	recordID := "KS-20260101-cli-ingest"
	stageThroughCLI(t, dbPath, recordID)
	if _, err := runKnowledgeStaged(dbPath, "disposition-staged", []string{
		"--id", recordID, "--action", "accepted", "--reason", "reviewed",
		"--classification-used", "internal", "--decided-by", testSteward,
	}); err != nil {
		t.Fatalf("cannot disposition: %v", err)
	}

	result, err := runKnowledgeStaged(dbPath, "ingest-accepted", nil)
	if err != nil {
		t.Fatalf("ingest-accepted failed: %v", err)
	}
	report, ok := result.(*knowledge.StagedIngestReport)
	if !ok || len(report.Ingested) != 1 {
		t.Fatalf("unexpected ingest report: %#v", result)
	}

	for _, flag := range []string{"--authorized-by", "--decided-by"} {
		if _, err := runKnowledgeStaged(dbPath, "ingest-accepted", []string{flag, "someone"}); err == nil {
			t.Fatalf("ingest-accepted accepted %s: it takes no decision and must take no approval flag", flag)
		}
	}
}

func TestIngestAcceptedDryRunWritesNothing(t *testing.T) {
	dbPath := testStagedDB(t)
	recordID := "KS-20260101-cli-dry"
	stageThroughCLI(t, dbPath, recordID)
	if _, err := runKnowledgeStaged(dbPath, "disposition-staged", []string{
		"--id", recordID, "--action", "accepted", "--reason", "reviewed",
		"--classification-used", "internal", "--decided-by", testSteward,
	}); err != nil {
		t.Fatalf("cannot disposition: %v", err)
	}

	result, err := runKnowledgeStaged(dbPath, "ingest-accepted", []string{"--dry-run"})
	if err != nil {
		t.Fatalf("dry-run failed: %v", err)
	}
	report, ok := result.(*knowledge.StagedIngestReport)
	if !ok || !report.DryRun || len(report.Ingested) != 1 || !report.Ingested[0].DryRun {
		t.Fatalf("unexpected dry-run report: %#v", result)
	}

	store, err := knowledge.Open(dbPath)
	if err != nil {
		t.Fatalf("cannot open store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ingested, err := store.StagedRecordAlreadyIngested(recordID)
	if err != nil {
		t.Fatalf("cannot check corpus: %v", err)
	}
	if ingested {
		t.Fatal("a dry run wrote to the corpus")
	}
}

func TestUnknownStagedSubcommandIsRefused(t *testing.T) {
	dbPath := testStagedDB(t)
	if _, err := runKnowledgeStaged(dbPath, "not-a-verb", nil); err == nil {
		t.Fatal("expected an unknown staged subcommand to be refused")
	}
}

// The staging-scope gate: staged records are per project and must never land
// in the shared global-fallback store. This is the Go port of cli.py's
// _enforce_staging_scope, and it is the one piece of the staged-record
// contract that survived neither half of the Go migration on its own -- the
// staged verbs landed without a config-tier concept, and the config tiers
// landed without knowing about staging.
//
// Both directions are asserted. A gate that refuses everything is not a gate,
// so the project-local case has to keep working.

func TestStagingIsRefusedAgainstTheSharedGlobalStore(t *testing.T) {
	requireSQLite(t)
	// A directory with no .agents/knowledge-store/config.json and no .git
	// boundary above it resolves to the global fallback tier.
	dir := t.TempDir()
	t.Setenv("KNOWLEDGE_STORE_HOME", filepath.Join(dir, "global"))
	t.Chdir(dir)

	_, err := knowledgeStagedDatabasePath("")
	if err == nil {
		t.Fatal("expected staging into the shared global store to be refused")
	}
	// The message has to name the remedy, not just the refusal: a caller who
	// hits this needs to know an empty {} claims a partition.
	for _, want := range []string{"per project", "config.json"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal should mention %q, got: %v", want, err)
		}
	}
}

func TestStagingIsAllowedAgainstAProjectLocalStore(t *testing.T) {
	requireSQLite(t)
	dir := t.TempDir()
	storeDir := filepath.Join(dir, ".agents", "knowledge-store")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// An empty object is the documented minimum for claiming a partition.
	if err := os.WriteFile(filepath.Join(storeDir, "config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KNOWLEDGE_STORE_HOME", filepath.Join(dir, "global"))
	t.Chdir(dir)

	dbPath, err := knowledgeStagedDatabasePath("")
	if err != nil {
		t.Fatalf("project-local staging should be allowed: %v", err)
	}
	if !strings.HasPrefix(dbPath, dir) {
		t.Errorf("resolved store %q should live under the project at %q", dbPath, dir)
	}
}
