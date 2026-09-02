package knowledge

// SQLite storage for staged knowledge records, and the structural home of the
// authorship/approval separation invariant.
//
// A Go port of roster/knowledge-store/src/staged_store.py, deleted by
// b418031e together with the rest of the staged-records subsystem. This is a
// storage backend, never a second implementation of the contract: every write
// goes through ValidateStagedRecord, and SerializeStagedRecord emits only
// constructs ParseStagedRecord accepts, so the round trip is closed by
// construction rather than by agreement between two hand-written formats. The
// digest is never computed here -- ComputeStagedDigest is the only
// implementation, for the reason its own doc comment gives.
//
// The separation checks in this file are the point of the whole subsystem.
// AGENTS.md and CLAUDE.md both state that authorship/approval separation is a
// hard invariant enforced structurally; roster/knowledge-store/SECURITY.md
// names the four checks that enforce it for knowledge staging. Two of them
// live here (DispositionStagedRecord, and the shared self-approval predicate
// import uses); the other two live on the propose and import paths in
// internal/cli/knowledge_staged.go and on the ingest path in
// staged_ingest.go. DeleteStagedRecord carries a fifth, closely related check
// that protects evidence of a decision rather than the decision itself.
//
// Honest limit, restated from SECURITY.md so it is not lost in the port: these
// checks compare two caller-asserted strings, authenticated by nobody. An
// actor that stages as one name and decides as another satisfies them. They
// make an honest actor's mistake impossible and a dishonest one's attribution
// self-incriminating; they are not an identity control.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/deagy/cadre/cli/internal/platform"
)

// stagedSchema is additive and idempotent, matching database.go's own
// `CREATE TABLE IF NOT EXISTS` schema, so an existing store picks these tables
// up without a migration step.
const stagedSchema = `
CREATE TABLE IF NOT EXISTS staged_records (
  id TEXT PRIMARY KEY,
  status TEXT NOT NULL,
  frontmatter_json TEXT NOT NULL,
  body TEXT NOT NULL,
  content_digest TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_staged_records_status ON staged_records(status);
CREATE TABLE IF NOT EXISTS staged_record_dispositions (
  record_id TEXT NOT NULL REFERENCES staged_records(id) ON DELETE CASCADE,
  sequence INTEGER NOT NULL,
  action TEXT NOT NULL,
  reason TEXT NOT NULL,
  classification_used TEXT NOT NULL,
  diverged_from_proposal INTEGER NOT NULL,
  decided_by TEXT NOT NULL,
  decided_at TEXT NOT NULL,
  PRIMARY KEY (record_id, sequence)
);
CREATE TABLE IF NOT EXISTS staged_record_imports (
  record_id TEXT NOT NULL,
  content_digest TEXT NOT NULL,
  status_at_import TEXT NOT NULL,
  authorized_by TEXT NOT NULL,
  directory TEXT NOT NULL,
  imported_at TEXT NOT NULL,
  PRIMARY KEY (record_id, imported_at)
);
CREATE TABLE IF NOT EXISTS staged_record_deletions (
  record_id TEXT NOT NULL,
  title TEXT NOT NULL,
  content_digest TEXT NOT NULL,
  status_at_deletion TEXT NOT NULL,
  reason TEXT NOT NULL,
  deleted_by TEXT NOT NULL,
  authorized_by TEXT,
  -- What the process saw, beside what the caller said. deleted_by and
  -- authorized_by are caller-asserted strings; this is not, and no flag
  -- sets it. The two are stored separately so a reader can tell an
  -- assertion from an observation -- see platform.ObservedActor.
  observed_actor TEXT NOT NULL DEFAULT '',
  deleted_at TEXT NOT NULL,
  PRIMARY KEY (record_id, deleted_at)
);

-- What a steward made retrievable, and where.
--
-- Kept here rather than derived from the corpus. The corpus is recall's now,
-- and recall can be asked for a chunk by id but not for what matches a
-- metadata scope -- so "has this record been ingested?" has no answer over
-- there. It is also the better home: the fact that a steward's acceptance was
-- carried out is governance evidence, and it should outlive any particular
-- store being rebuilt.
CREATE TABLE IF NOT EXISTS staged_record_ingestions (
  record_id TEXT PRIMARY KEY,
  document_id TEXT NOT NULL,
  corpus TEXT NOT NULL,
  classification TEXT NOT NULL,
  chunk_count INTEGER NOT NULL,
  ingested_at TEXT NOT NULL
);
`

// StagedRecordError is a staged record refused, with the contract findings
// attached when the refusal came from the validator.
type StagedRecordError struct {
	Message  string
	Findings []string
}

func (e *StagedRecordError) Error() string { return e.Message }

func stagedErrorf(format string, args ...any) error {
	return &StagedRecordError{Message: fmt.Sprintf(format, args...)}
}

// ErrStagedRecordNotFound is wrapped by every lookup that names a record id
// the store does not hold.
var ErrStagedRecordNotFound = errors.New("no staged record with that id in this store")

// StagedSummary is one row of `list-staged`, ordered by id for determinism.
type StagedSummary struct {
	ID                string `json:"id"`
	Status            string `json:"status"`
	Title             string `json:"title"`
	RecommendedAction string `json:"recommended_action"`
	ContentDigest     string `json:"content_digest"`
	CreatedAt         string `json:"created_at"`
	UpdatedAt         string `json:"updated_at"`
}

// DispositionEntry is one decision in a record's append-only history.
type DispositionEntry struct {
	Sequence             int    `json:"sequence"`
	Action               string `json:"action"`
	Reason               string `json:"reason"`
	ClassificationUsed   string `json:"classification_used"`
	DivergedFromProposal bool   `json:"diverged_from_proposal"`
	DecidedBy            string `json:"decided_by"`
	DecidedAt            string `json:"decided_at"`
}

// StagedImportAuthorization records who authorized admitting one
// already-dispositioned record into this store.
type StagedImportAuthorization struct {
	RecordID       string `json:"record_id"`
	ContentDigest  string `json:"content_digest"`
	StatusAtImport string `json:"status_at_import"`
	AuthorizedBy   string `json:"authorized_by"`
	Directory      string `json:"directory"`
	ImportedAt     string `json:"imported_at"`
}

// StagedDeletionEvidence outlives the record it describes.
type StagedDeletionEvidence struct {
	RecordID         string  `json:"record_id"`
	Title            string  `json:"title"`
	ContentDigest    string  `json:"content_digest"`
	StatusAtDeletion string  `json:"status_at_deletion"`
	Reason           string  `json:"reason"`
	DeletedBy        string  `json:"deleted_by"`
	AuthorizedBy     *string `json:"authorized_by"`

	// ObservedActor is what the process saw, as distinct from what the
	// caller said. DeletedBy and AuthorizedBy above are caller-asserted
	// strings; this is not, and no flag sets it. Rendered with its source
	// prefixed ("os:deagy git:a@b.c") so it cannot be read as a name.
	ObservedActor string `json:"observed_actor"`

	DeletedAt string `json:"deleted_at"`
}

func stagedNow() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
}

// InstallStagedSchema creates the staged-record tables if they are absent.
// Idempotent, so callers may run it per command rather than tracking whether
// an existing store has been migrated.
func (s *Store) InstallStagedSchema() error {
	if _, err := s.db.Exec(stagedSchema); err != nil {
		return fmt.Errorf("cannot install staged-record schema: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Self-approval separation
// ---------------------------------------------------------------------------

// StagedString reads one frontmatter value as a string, returning "" when the
// key is absent or is not a string. Used wherever a check needs an identity
// field without caring why a malformed one is missing -- the validator has
// already reported that separately.
func StagedString(fm map[string]any, key string) string {
	value, _ := fm[key].(string)
	return value
}

// StagedDecidedBy returns the record's disposition.decided_by, or "" when the
// record carries no disposition.
func StagedDecidedBy(fm map[string]any) string {
	disposition, ok := fm["disposition"].(map[string]any)
	if !ok {
		return ""
	}
	decidedBy, _ := disposition["decided_by"].(string)
	return decidedBy
}

// StagedRecordIsSelfApproved reports whether a record's own disposition names
// its stager as the decider.
//
// One predicate, used by every path that can admit an already-decided record
// (import) or act on one (ingest), so the four separation checks cannot drift
// into disagreeing about what a self-approval is. An absent or empty
// decided_by is not a self-approval: it is a malformed disposition, which the
// contract validator refuses on its own terms.
func StagedRecordIsSelfApproved(fm map[string]any) bool {
	decidedBy := StagedDecidedBy(fm)
	return decidedBy != "" && decidedBy == StagedString(fm, "staged_by")
}

// ---------------------------------------------------------------------------
// Reads
// ---------------------------------------------------------------------------

// GetStagedRecord loads one record's frontmatter and body. A missing record is
// reported as ErrStagedRecordNotFound rather than as a nil result, so a caller
// cannot mistake "absent" for "empty".
func (s *Store) GetStagedRecord(recordID string) (map[string]any, string, error) {
	var frontmatterJSON, body string
	err := s.db.QueryRow(
		"SELECT frontmatter_json, body FROM staged_records WHERE id = ?", recordID,
	).Scan(&frontmatterJSON, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", fmt.Errorf("%q: %w", recordID, ErrStagedRecordNotFound)
	}
	if err != nil {
		return nil, "", fmt.Errorf("cannot read staged record %q: %w", recordID, err)
	}
	var frontmatter map[string]any
	if err := json.Unmarshal([]byte(frontmatterJSON), &frontmatter); err != nil {
		return nil, "", fmt.Errorf("staged record %q holds unreadable frontmatter: %w", recordID, err)
	}
	return frontmatter, body, nil
}

// GetStagedRecordText loads one record and re-serialises it to record text.
func (s *Store) GetStagedRecordText(recordID string) (string, error) {
	frontmatter, body, err := s.GetStagedRecord(recordID)
	if err != nil {
		return "", err
	}
	return SerializeStagedRecord(frontmatter, body)
}

// ListStagedRecords returns summaries ordered by id. An empty status means
// every record.
func (s *Store) ListStagedRecords(status string) ([]StagedSummary, error) {
	query := "SELECT id, status, content_digest, created_at, updated_at, frontmatter_json FROM staged_records"
	args := []any{}
	if status != "" {
		query += " WHERE status = ?"
		args = append(args, status)
	}
	query += " ORDER BY id"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("cannot list staged records: %w", err)
	}
	defer func() { _ = rows.Close() }()

	summaries := []StagedSummary{}
	for rows.Next() {
		var summary StagedSummary
		var frontmatterJSON string
		if err := rows.Scan(&summary.ID, &summary.Status, &summary.ContentDigest,
			&summary.CreatedAt, &summary.UpdatedAt, &frontmatterJSON); err != nil {
			return nil, fmt.Errorf("cannot read staged record row: %w", err)
		}
		var frontmatter map[string]any
		if err := json.Unmarshal([]byte(frontmatterJSON), &frontmatter); err != nil {
			return nil, fmt.Errorf("staged record %q holds unreadable frontmatter: %w", summary.ID, err)
		}
		summary.Title = StagedString(frontmatter, "title")
		summary.RecommendedAction = StagedString(frontmatter, "recommended_action")
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cannot list staged records: %w", err)
	}
	return summaries, nil
}

// StagedHistory returns every disposition ever recorded for a record, oldest
// first.
//
// Append-only by construction: DispositionStagedRecord only ever inserts, and
// the frontmatter carries the *current* disposition while this carries all of
// them. A record deferred and later accepted retains both, which is exactly
// what a single overwritten field would lose.
func (s *Store) StagedHistory(recordID string) ([]DispositionEntry, error) {
	rows, err := s.db.Query(
		"SELECT sequence, action, reason, classification_used, diverged_from_proposal, decided_by, "+
			"decided_at FROM staged_record_dispositions WHERE record_id = ? ORDER BY sequence", recordID)
	if err != nil {
		return nil, fmt.Errorf("cannot read disposition history for %q: %w", recordID, err)
	}
	defer func() { _ = rows.Close() }()

	history := []DispositionEntry{}
	for rows.Next() {
		var entry DispositionEntry
		var diverged int
		if err := rows.Scan(&entry.Sequence, &entry.Action, &entry.Reason, &entry.ClassificationUsed,
			&diverged, &entry.DecidedBy, &entry.DecidedAt); err != nil {
			return nil, fmt.Errorf("cannot read disposition history row: %w", err)
		}
		entry.DivergedFromProposal = diverged != 0
		history = append(history, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cannot read disposition history for %q: %w", recordID, err)
	}
	return history, nil
}

// StagedImportAuthorizations returns every authorized import of a
// dispositioned record, oldest first. An empty recordID reads every record's.
//
// A read path exists because evidence nobody can read is not evidence.
// `show-staged` surfaces this beside the record's disposition history, which
// is where the question is actually asked: this decision was made elsewhere --
// who let it in here?
func (s *Store) StagedImportAuthorizations(recordID string) ([]StagedImportAuthorization, error) {
	query := "SELECT record_id, content_digest, status_at_import, authorized_by, directory, imported_at " +
		"FROM staged_record_imports"
	args := []any{}
	if recordID != "" {
		query += " WHERE record_id = ?"
		args = append(args, recordID)
	}
	query += " ORDER BY imported_at, record_id"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("cannot read import authorizations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	authorizations := []StagedImportAuthorization{}
	for rows.Next() {
		var row StagedImportAuthorization
		if err := rows.Scan(&row.RecordID, &row.ContentDigest, &row.StatusAtImport,
			&row.AuthorizedBy, &row.Directory, &row.ImportedAt); err != nil {
			return nil, fmt.Errorf("cannot read import authorization row: %w", err)
		}
		authorizations = append(authorizations, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cannot read import authorizations: %w", err)
	}
	return authorizations, nil
}

// StagedDeletionEvidenceRows returns every staged-record deletion ever
// performed, oldest first.
//
// The table deliberately carries no foreign key to staged_records: the whole
// point is that this outlives the row it describes. Evidence that vanished
// with its subject would make a deletion indistinguishable from data loss.
func (s *Store) StagedDeletionEvidenceRows() ([]StagedDeletionEvidence, error) {
	rows, err := s.db.Query(
		"SELECT record_id, title, content_digest, status_at_deletion, reason, deleted_by, " +
			"authorized_by, observed_actor, deleted_at FROM staged_record_deletions " +
			"ORDER BY deleted_at, record_id")
	if err != nil {
		return nil, fmt.Errorf("cannot read staged deletion evidence: %w", err)
	}
	defer func() { _ = rows.Close() }()

	evidence := []StagedDeletionEvidence{}
	for rows.Next() {
		var row StagedDeletionEvidence
		var authorizedBy sql.NullString
		if err := rows.Scan(&row.RecordID, &row.Title, &row.ContentDigest, &row.StatusAtDeletion,
			&row.Reason, &row.DeletedBy, &authorizedBy, &row.ObservedActor,
			&row.DeletedAt); err != nil {
			return nil, fmt.Errorf("cannot read staged deletion evidence row: %w", err)
		}
		if authorizedBy.Valid {
			value := authorizedBy.String
			row.AuthorizedBy = &value
		}
		evidence = append(evidence, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cannot read staged deletion evidence: %w", err)
	}
	return evidence, nil
}

// ---------------------------------------------------------------------------
// Writes
// ---------------------------------------------------------------------------

func validatedStagedRecord(frontmatter map[string]any, body string) error {
	findings := ValidateStagedRecord(frontmatter, body)
	if len(findings) == 0 {
		return nil
	}
	return &StagedRecordError{
		Message:  "staged record does not satisfy the contract: " + strings.Join(findings, "; "),
		Findings: findings,
	}
}

// PutStagedRecord validates and stores a record, returning its id.
//
// Validation happens *before* the write, so a malformed record never exists
// rather than being caught later by a checker. Replacing an existing id is
// deliberate and preserves created_at: a steward amending a disposition is
// updating the same record, not creating a second one.
//
// The write is an upsert, never INSERT OR REPLACE. REPLACE *deletes* the
// existing row before reinserting, and with `PRAGMA foreign_keys = ON` (which
// Open sets) that cascades into staged_record_dispositions and silently erases
// the record's entire disposition history.
func (s *Store) PutStagedRecord(frontmatter map[string]any, body string) (string, error) {
	if err := validatedStagedRecord(frontmatter, body); err != nil {
		return "", err
	}
	recordID := StagedString(frontmatter, "id")
	encoded, err := json.Marshal(frontmatter)
	if err != nil {
		return "", fmt.Errorf("cannot encode staged record %q: %w", recordID, err)
	}

	now := stagedNow()
	createdAt := now
	var existing string
	switch err := s.db.QueryRow("SELECT created_at FROM staged_records WHERE id = ?", recordID).Scan(&existing); {
	case err == nil:
		createdAt = existing
	case errors.Is(err, sql.ErrNoRows):
	default:
		return "", fmt.Errorf("cannot read staged record %q: %w", recordID, err)
	}

	_, err = s.db.Exec(
		"INSERT INTO staged_records (id, status, frontmatter_json, body, content_digest, created_at, updated_at) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?) "+
			"ON CONFLICT(id) DO UPDATE SET status = excluded.status, "+
			"frontmatter_json = excluded.frontmatter_json, body = excluded.body, "+
			"content_digest = excluded.content_digest, updated_at = excluded.updated_at",
		recordID, StagedString(frontmatter, "status"), string(encoded), body,
		StagedString(frontmatter, "content_digest"), createdAt, now)
	if err != nil {
		return "", fmt.Errorf("cannot store staged record %q: %w", recordID, err)
	}
	return recordID, nil
}

// GeneratedStagedResult reports what PutGeneratedStagedRecord did.
type GeneratedStagedResult struct {
	Status        string `json:"status"`
	ID            string `json:"id"`
	RecordStatus  string `json:"record_status"`
	ContentDigest string `json:"content_digest"`
	Note          string `json:"note"`
}

// PutGeneratedStagedRecord stores a record whose id was generated by
// BuildStagedRecordFromFinding, refusing to silently clobber whatever already
// occupies that id.
//
// PutStagedRecord upserts by id unconditionally, which is correct when a human
// or steward is amending a record they know exists -- they typed its id on
// purpose. It is the wrong behaviour for a *generated* id: the caller never
// chose it, so a collision there is either the same finding proposed twice
// (harmless -- the digest is a pure function of the body, so identical content
// means nothing changed) or two different findings that happened to land on
// the same id (never harmless -- the upsert would silently overwrite one,
// including reverting an already-dispositioned record's status back to
// 'proposed').
//
// Same id, same digest -> already staged, nothing written, existing status
// untouched. Same id, different digest -> refused loudly, nothing written.
// There is no third case where this function writes over an existing row.
func (s *Store) PutGeneratedStagedRecord(frontmatter map[string]any, body string) (*GeneratedStagedResult, error) {
	recordID := StagedString(frontmatter, "id")
	digest := StagedString(frontmatter, "content_digest")

	existing, _, err := s.GetStagedRecord(recordID)
	switch {
	case err == nil:
		if StagedString(existing, "content_digest") == digest {
			return &GeneratedStagedResult{
				Status:        "already-staged",
				ID:            recordID,
				RecordStatus:  StagedString(existing, "status"),
				ContentDigest: digest,
				Note: "A record with this generated id and identical content is already staged; nothing " +
					"was written, so its current disposition (if any) was left untouched.",
			}, nil
		}
		return nil, stagedErrorf(
			"generated id %q collides with an existing staged record whose content differs (existing "+
				"content_digest %q != %q). Refused rather than overwritten: reword the finding's title so "+
				"the generated id differs, or use `propose --input` with a hand-assigned id if this is "+
				"deliberately meant to replace that exact record.",
			recordID, StagedString(existing, "content_digest"), digest)
	case errors.Is(err, ErrStagedRecordNotFound):
	default:
		return nil, err
	}

	if _, err := s.PutStagedRecord(frontmatter, body); err != nil {
		return nil, err
	}
	return &GeneratedStagedResult{
		Status:        "staged",
		ID:            recordID,
		RecordStatus:  StagedString(frontmatter, "status"),
		ContentDigest: digest,
		Note: "Staged for knowledge-store-steward disposition. Staging is not ingestion: nothing is " +
			"retrievable until a steward accepts this record and it is ingested.",
	}, nil
}

// DispositionInput is one steward decision.
type DispositionInput struct {
	Action               string
	Reason               string
	ClassificationUsed   string
	DivergedFromProposal bool
	DecidedBy            string
}

// DispositionResult is what DispositionStagedRecord reports back.
type DispositionResult struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Sequence int    `json:"sequence"`
}

// DispositionStagedRecord records a steward decision, appending to history and
// updating the record.
//
// SEPARATION CHECK 2 of 4 (roster/knowledge-store/SECURITY.md): whoever staged
// a record cannot disposition it. That rule already existed in prose -- the
// steward role says an agent may not disposition its own proposal -- and prose
// that nothing checks is the failure mode this contract was written to avoid.
// Refusing here means no history row is written and the record's status is
// left as it was.
//
// The automatic-defer rule and the action/status agreement rule are
// deliberately not re-implemented here: the amended frontmatter goes back
// through PutStagedRecord, so the contract's own validator stays the single
// authority on whether the result is legal.
func (s *Store) DispositionStagedRecord(recordID string, input DispositionInput) (*DispositionResult, error) {
	frontmatter, body, err := s.GetStagedRecord(recordID)
	if err != nil {
		return nil, err
	}
	if input.DecidedBy == StagedString(frontmatter, "staged_by") {
		return nil, stagedErrorf(
			"%q staged this record and cannot also disposition it. Authorship and approval are separate: "+
				"a steward other than the proposer must decide, per roster/shared/agent-autonomy.yaml and "+
				"the knowledge-store steward's role definition.", input.DecidedBy)
	}
	if strings.TrimSpace(input.Reason) == "" {
		return nil, stagedErrorf(
			"a disposition requires a reason: an unexplained decision is not an audit trail")
	}

	amended := make(map[string]any, len(frontmatter)+1)
	for key, value := range frontmatter {
		amended[key] = value
	}
	amended["status"] = input.Action
	amended["disposition"] = map[string]any{
		"action":                 input.Action,
		"reason":                 input.Reason,
		"classification_used":    input.ClassificationUsed,
		"diverged_from_proposal": input.DivergedFromProposal,
		"decided_by":             input.DecidedBy,
	}

	// Validated by PutStagedRecord before anything is written, so an illegal
	// disposition (action disagreeing with status, or accepting a record
	// flagged untrusted_instruction_risk) leaves no history row behind.
	if _, err := s.PutStagedRecord(amended, body); err != nil {
		return nil, err
	}

	history, err := s.StagedHistory(recordID)
	if err != nil {
		return nil, err
	}
	sequence := len(history) + 1
	diverged := 0
	if input.DivergedFromProposal {
		diverged = 1
	}
	_, err = s.db.Exec(
		"INSERT INTO staged_record_dispositions (record_id, sequence, action, reason, classification_used, "+
			"diverged_from_proposal, decided_by, decided_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		recordID, sequence, input.Action, input.Reason, input.ClassificationUsed, diverged,
		input.DecidedBy, stagedNow())
	if err != nil {
		return nil, fmt.Errorf("cannot record disposition for %q: %w", recordID, err)
	}
	return &DispositionResult{ID: recordID, Status: input.Action, Sequence: sequence}, nil
}

// RecordStagedImportAuthorization persists who authorized admitting one
// already-dispositioned record.
//
// `import-staged --authorized-by` is the only route by which a decision this
// store never watched being made can still enter it, and the flag's whole
// justification is that it names the human accountable for admitting that
// decision. Echoing the name into the command's JSON output does not do that:
// the process exits and the accountability goes with it, leaving a record
// whose disposition has an attributable decider and an unattributable
// admission. This is where the name survives.
//
// The table carries no foreign key to staged_records, for the same reason
// staged_record_deletions carries none: this outlives the row it describes, so
// a record deleted afterwards does not take the evidence of its admission with
// it.
func (s *Store) RecordStagedImportAuthorization(recordID, contentDigest, statusAtImport, authorizedBy, directory string) error {
	// INSERT OR REPLACE can only collide on (record_id, imported_at) -- the
	// same record admitted twice within the same millisecond, where the
	// replaced row and its replacement are identical in every column. Two
	// genuinely separate imports get two rows.
	_, err := s.db.Exec(
		"INSERT OR REPLACE INTO staged_record_imports (record_id, content_digest, status_at_import, "+
			"authorized_by, directory, imported_at) VALUES (?, ?, ?, ?, ?, ?)",
		recordID, contentDigest, statusAtImport, authorizedBy, directory, stagedNow())
	if err != nil {
		return fmt.Errorf("cannot record import authorization for %q: %w", recordID, err)
	}
	return nil
}

// DeleteStagedInput names the actor, the reason, and any authorizing human.
type DeleteStagedInput struct {
	Reason       string
	DeletedBy    string
	AuthorizedBy string
}

// DeleteStagedResult is what DeleteStagedRecord reports back.
type DeleteStagedResult struct {
	Status           string `json:"status"`
	ID               string `json:"id"`
	ContentDigest    string `json:"content_digest"`
	StatusAtDeletion string `json:"status_at_deletion"`
	EvidenceRetained bool   `json:"evidence_retained"`

	// ObservedActor is what the process saw when this ran, echoed back so
	// the distinction is visible at the moment of the deletion rather than
	// only to whoever reads the evidence table afterwards. The caller
	// already knows what they passed in --deleted-by; this is the half they
	// did not choose.
	ObservedActor string `json:"observed_actor"`
}

// DeleteStagedRecord deletes a staged record, leaving evidence behind.
//
// **This deletes a staging-table row, not ingested knowledge.** A staged
// record has never been embedded or made retrievable. This function never
// reaches any of the ingested-content tables in database.go's schema --
// deletion of ingested content is a separate capability with its own evidence
// table and its own steward-only authorization requirements. Nothing here
// should be read as that capability.
//
// Two refusals, protecting different things:
//
// SEPARATION CHECK 5 (the additional one, beyond SECURITY.md's four): a
// still-'proposed' record has had no decision made about it yet, so its own
// proposer may delete it -- that is just withdrawing a draft, and nothing
// about the authorship/approval-separation invariant applies to a record
// nobody has approved or rejected. Once a record carries *any* disposition
// (accepted, rejected, or deferred), a decision exists, and the same
// separation DispositionStagedRecord enforces for making that decision is
// enforced here for erasing evidence of it. Without this, an agent whose
// finding was rejected could quietly delete the rejection and retry elsewhere,
// leaving only the retained deletion-evidence row -- accurate, but not
// something anyone would think to check.
//
// The human-authorization gate: an 'accepted' record requires AuthorizedBy.
// Acceptance is a steward's decision that the record is durable knowledge, so
// removing it afterwards is a reversal that needs a human. This is independent
// of, and in addition to, the proposer check above -- an accepted record needs
// both a non-proposer deleter *and* human authorization.
func (s *Store) DeleteStagedRecord(recordID string, input DeleteStagedInput) (*DeleteStagedResult, error) {
	frontmatter, _, err := s.GetStagedRecord(recordID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.Reason) == "" {
		return nil, stagedErrorf(
			"a deletion requires a reason: an unexplained deletion is indistinguishable from data loss")
	}
	status := StagedString(frontmatter, "status")
	if status != "proposed" && input.DeletedBy == StagedString(frontmatter, "staged_by") {
		return nil, stagedErrorf(
			"%q staged this record and it already carries a disposition (%q), so deleting it must not be "+
				"the proposer's own act either -- the same separation DispositionStagedRecord enforces for "+
				"deciding a record's outcome applies to erasing evidence of that decision. A steward other "+
				"than the proposer must delete it. A record that is still 'proposed' has no decision yet to "+
				"protect and may be withdrawn by its own proposer.", input.DeletedBy, status)
	}
	// Whitespace is not a name: a gate that "   " satisfies records a blank as
	// the accountable human.
	authorizer := strings.TrimSpace(input.AuthorizedBy)
	if status == "accepted" && authorizer == "" {
		return nil, stagedErrorf(
			"record %q was accepted, so deleting it reverses a steward's decision and requires an "+
				"authorized human: pass --authorized-by. Acceptance is not a draft state.", recordID)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("cannot begin staged-record deletion: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var authorizedBy any
	if authorizer != "" {
		authorizedBy = authorizer
	}
	// Evidence is written first, so a failure in the delete cannot leave a
	// deletion unrecorded. The record's history goes with the record via
	// ON DELETE CASCADE; this row is what survives.
	// Resolved once: the row written and the row reported must not be able
	// to disagree about what was seen.
	observed := platform.ObserveActor().String()

	if _, err := tx.Exec(
		"INSERT INTO staged_record_deletions (record_id, title, content_digest, status_at_deletion, "+
			"reason, deleted_by, authorized_by, observed_actor, deleted_at) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		recordID, StagedString(frontmatter, "title"), StagedString(frontmatter, "content_digest"),
		status, input.Reason, input.DeletedBy, authorizedBy,
		observed, stagedNow()); err != nil {
		return nil, fmt.Errorf("cannot record staged deletion evidence for %q: %w", recordID, err)
	}
	if _, err := tx.Exec("DELETE FROM staged_records WHERE id = ?", recordID); err != nil {
		return nil, fmt.Errorf("cannot delete staged record %q: %w", recordID, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("cannot commit staged-record deletion for %q: %w", recordID, err)
	}

	return &DeleteStagedResult{
		Status:           "deleted",
		ID:               recordID,
		ContentDigest:    StagedString(frontmatter, "content_digest"),
		StatusAtDeletion: status,
		EvidenceRetained: true,
		ObservedActor:    observed,
	}, nil
}
