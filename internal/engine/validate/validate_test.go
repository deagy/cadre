package validate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"

	"github.com/deagy/cadre/cli/internal/engine/contracts"
)

// The Python's verdicts, pinned.
//
// testdata/python_verdicts.json holds nine run records built by the Python's
// own export pipeline, together with the exit code and messages
// validate_run_record produced for each. It is committed because this is
// safety-critical logic -- approver independence, verifier separation, the
// invalidation cascade -- and once the Python is deleted there is nothing left
// to re-derive its answers from.
//
// The scenarios cover all three exit codes, including the one that is easiest
// to get wrong: an unresolved authority applicability is a *blocker* (2), not
// an error (1), because it is a decision nobody has made rather than something
// that is wrong.
type pinnedCase struct {
	Record    map[string]any            `json:"record"`
	Contracts map[string]contracts.Gate `json:"contracts"`
	Code      int                       `json:"code"`
	Messages  []string                  `json:"messages"`
}

func loadPinnedCases(t *testing.T) map[string]pinnedCase {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join("testdata", "python_verdicts.json"))
	if err != nil {
		t.Fatalf("reading the pinned verdicts: %v", err)
	}
	var cases map[string]pinnedCase
	if err := json.Unmarshal(contents, &cases); err != nil {
		t.Fatalf("parsing the pinned verdicts: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("no pinned cases; this guard checked nothing")
	}
	return cases
}

func runRecordSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	working, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(working)))
	schema, err := jsonschema.Compile(filepath.Join(root, "kernel-contracts", "run-record.schema.json"))
	if err != nil {
		t.Fatalf("compiling the run-record schema: %v", err)
	}
	return schema
}

func TestVerdictsMatchThePython(t *testing.T) {
	schema := runRecordSchema(t)

	for name, pinned := range loadPinnedCases(t) {
		t.Run(name, func(t *testing.T) {
			// A nil contracts map is meaningfully different from an empty one:
			// it skips the missing-authority check entirely.
			var gateContracts map[string]contracts.Gate
			if pinned.Contracts != nil {
				gateContracts = pinned.Contracts
			}

			code, messages := RunRecord(pinned.Record, schema, gateContracts)

			if code != pinned.Code {
				t.Errorf("exit code = %d, python said %d\n  go said: %v\n  python said: %v",
					code, pinned.Code, messages, pinned.Messages)
			}
			if len(messages) != len(pinned.Messages) {
				t.Errorf("produced %d messages, python produced %d\n  go: %v\n  python: %v",
					len(messages), len(pinned.Messages), messages, pinned.Messages)
			}
		})
	}
}

// The messages must still name the same thing, even where wording differs.
func TestMessagesNameTheSameFault(t *testing.T) {
	schema := runRecordSchema(t)
	expectSubstring := map[string]string{
		"approved_missing_evidence":     "evidence_refs",
		"unresolved_authority":          "unresolved authority applicability",
		"verifier_is_preparer":          "also a preparer",
		"approver_is_preparer":          "not independent",
		"missing_authority_requirement": "missing authority requirements",
		"bad_timestamp":                 "decided_at",
	}

	cases := loadPinnedCases(t)
	for name, want := range expectSubstring {
		pinned, present := cases[name]
		if !present {
			t.Fatalf("the pinned verdicts have no case %q", name)
		}
		_, messages := RunRecord(pinned.Record, schema, pinned.Contracts)
		joined := strings.Join(messages, "\n")
		if !strings.Contains(joined, want) {
			t.Errorf("%s: no message mentions %q\n  got: %v", name, want, messages)
		}
	}
}

// A blocker must never be reported while a hard error is present: code 2 says
// "structurally valid, undecided", which is a different instruction to a human
// than "this is wrong".
func TestABlockerNeverMasksAnError(t *testing.T) {
	schema := runRecordSchema(t)
	cases := loadPinnedCases(t)

	blocked := cases["unresolved_authority"]
	// Introduce a hard error alongside the unresolved authority.
	record := blocked.Record
	gates, _ := record["lifecycle_gates"].([]any)
	if len(gates) == 0 {
		t.Fatal("the pinned record has no gates")
	}
	first, _ := gates[0].(map[string]any)
	first["evidence_refs"] = []any{}

	code, messages := RunRecord(record, schema, blocked.Contracts)
	if code != 1 {
		t.Errorf("code = %d with both an error and a blocker present, want 1", code)
	}
	for _, message := range messages {
		if strings.Contains(message, "unresolved authority applicability") {
			t.Error("a blocker was reported alongside hard errors; code 1 must carry errors only")
		}
	}
}

func TestIsValidDateTime(t *testing.T) {
	valid := []string{
		"2026-08-18T12:00:00Z",
		"2026-08-18T12:00:00+00:00",
		"2026-08-18T12:00:00.123456+02:00",
		"2026-08-18 12:00:00+00:00", // fromisoformat accepts a space; so does this
	}
	for _, value := range valid {
		if !isValidDateTime(value) {
			t.Errorf("isValidDateTime(%q) = false, want true", value)
		}
	}

	invalid := []any{
		"not-a-real-timestamp",
		"2026-08-18T12:00:00", // naive: no offset, which the check requires
		"2026-13-01T00:00:00Z",
		"",
		nil,
		12345,
	}
	for _, value := range invalid {
		if isValidDateTime(value) {
			t.Errorf("isValidDateTime(%#v) = true, want false", value)
		}
	}
}
