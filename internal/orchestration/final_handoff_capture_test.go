package orchestration

import (
	"encoding/json"
	"strings"
	"testing"
)

// Automatic capture stores something permanently, unattended, from a child's
// output. What it accepts is therefore the whole question: every field it
// takes is a field a hostile child gets to fill.

// envelope builds a minimal valid handoff, then applies the given mutations.
func envelope(mutate func(map[string]any)) map[string]any {
	object := map[string]any{
		"kind":           "cadre-final-handoff",
		"schema_version": float64(1),
		"handoff": map[string]any{
			"summary":     "reviewed the change",
			"disposition": "complete",
		},
		"artifacts":    []any{},
		"derived_from": []any{},
	}
	if mutate != nil {
		mutate(object)
	}
	return object
}

func TestAValidEnvelopeIsAccepted(t *testing.T) {
	captured, err := validateFinalHandoff(envelope(nil))
	if err != nil {
		t.Fatalf("a valid envelope was refused: %v", err)
	}
	if captured["kind"] != "cadre-final-handoff" {
		t.Errorf("captured = %v", captured)
	}
	// The stored form is JSON, so anything that survives validation must
	// serialize.
	if _, err := json.Marshal(captured); err != nil {
		t.Errorf("the validated envelope does not serialize: %v", err)
	}
}

func TestAnEnvelopeMustDeclareItsKindAndVersion(t *testing.T) {
	// Without this an arbitrary JSON object a child wrote for its own
	// purposes would be stored as a handoff.
	for _, mutate := range []func(map[string]any){
		func(o map[string]any) { delete(o, "kind") },
		func(o map[string]any) { o["kind"] = "something-else" },
		func(o map[string]any) { o["schema_version"] = float64(2) },
		func(o map[string]any) { delete(o, "schema_version") },
	} {
		if _, err := validateFinalHandoff(envelope(mutate)); err == nil {
			t.Error("an envelope with a wrong kind or version was accepted")
		}
	}
	if _, err := validateFinalHandoff("not an object"); err == nil {
		t.Error("a non-object was accepted as an envelope")
	}
}

func TestUnknownFieldsAreRefusedRatherThanIgnored(t *testing.T) {
	// Ignoring them would store data nothing validated, under a schema that
	// claims everything present was checked.
	added := envelope(func(o map[string]any) { o["extra"] = "smuggled" })
	if _, err := validateFinalHandoff(added); err == nil {
		t.Error("an unknown top-level field was accepted")
	}

	inner := envelope(func(o map[string]any) {
		o["handoff"] = map[string]any{"summary": "s", "not_a_field": "smuggled"}
	})
	if _, err := validateFinalHandoff(inner); err == nil {
		t.Error("an unknown handoff field was accepted")
	}
}

func TestFreeTextIsBoundedInBytesAndInLines(t *testing.T) {
	// The byte cap alone is not enough: a field within it but made of
	// hundreds of short lines is a transcript wearing a structured field's
	// name, which is exactly what this channel exists to keep out.
	long := envelope(func(o map[string]any) {
		o["handoff"] = map[string]any{"summary": strings.Repeat("x", MaxHandoffTextBytes+1)}
	})
	if _, err := validateFinalHandoff(long); err == nil {
		t.Error("an oversized summary was accepted")
	}

	manyLines := envelope(func(o map[string]any) {
		o["handoff"] = map[string]any{"summary": strings.Repeat("line\n", 100)}
	})
	if _, err := validateFinalHandoff(manyLines); err == nil {
		t.Error("a many-line summary was accepted")
	}

	empty := envelope(func(o map[string]any) {
		o["handoff"] = map[string]any{"summary": "   "}
	})
	if _, err := validateFinalHandoff(empty); err == nil {
		t.Error("a blank summary was accepted")
	}
}

func TestListsAreBounded(t *testing.T) {
	tooMany := make([]any, MaxHandoffListItems+1)
	for index := range tooMany {
		tooMany[index] = "assumption"
	}
	bounded := envelope(func(o map[string]any) {
		o["handoff"] = map[string]any{"summary": "s", "assumptions": tooMany}
	})
	if _, err := validateFinalHandoff(bounded); err == nil {
		t.Error("an unbounded assumptions list was accepted")
	}

	artifacts := make([]any, 65)
	for index := range artifacts {
		artifacts[index] = map[string]any{"id": "artifact"}
	}
	manifest := envelope(func(o map[string]any) { o["artifacts"] = artifacts })
	if _, err := validateFinalHandoff(manifest); err == nil {
		t.Error("an over-long artifact manifest was accepted")
	}
}

func TestAnArtifactIsAnIdentifierNotALocation(t *testing.T) {
	// The manifest records *which* artifacts exist. It is not an instruction
	// to fetch them, and a consumer that later resolved an entry must not be
	// resolving a path the child chose.
	for _, identifier := range []string{
		"/etc/passwd",
		"\\\\server\\share\\secret",
		"C:\\Windows\\System32\\config",
		"file:///etc/shadow",
		"report?token=abc123",
	} {
		object := envelope(func(o map[string]any) {
			o["artifacts"] = []any{map[string]any{"id": identifier}}
		})
		if _, err := validateFinalHandoff(object); err == nil {
			t.Errorf("artifact id %q was accepted", identifier)
		}
	}

	// A repository-relative identifier is fine, and so is an https URI.
	for _, ok := range []map[string]any{
		{"id": "docs/report.md"},
		{"id": "report", "uri": "https://example.invalid/report"},
	} {
		object := envelope(func(o map[string]any) { o["artifacts"] = []any{ok} })
		if _, err := validateFinalHandoff(object); err != nil {
			t.Errorf("a legitimate artifact %v was refused: %v", ok, err)
		}
	}

	// A non-https scheme is refused for a uri field.
	object := envelope(func(o map[string]any) {
		o["artifacts"] = []any{map[string]any{"id": "r", "uri": "http://example.invalid/x"}}
	})
	if _, err := validateFinalHandoff(object); err == nil {
		t.Error("a plaintext http artifact uri was accepted")
	}
}

func TestProvenanceMayOnlyBeSomethingTheStoreIssued(t *testing.T) {
	// derived_from is recorded as the entry's lineage. A free string there
	// would be provenance the store cannot check, which is worse than none:
	// it looks verified.
	bad := envelope(func(o map[string]any) {
		o["derived_from"] = []any{"trust me, I read the docs"}
	})
	if _, err := validateFinalHandoff(bad); err == nil {
		t.Error("an unverifiable provenance reference was accepted")
	}

	good := envelope(func(o map[string]any) {
		o["derived_from"] = []any{
			"ctx_0123456789abcdef0123456789abcdef",
			"ks:untrusted:some-source",
		}
	})
	if _, err := validateFinalHandoff(good); err != nil {
		t.Errorf("legitimate provenance was refused: %v", err)
	}
}

func TestACitedHandleMustAlsoBeDeclaredAsProvenance(t *testing.T) {
	// Otherwise the entry claims a lineage the store never recorded: the
	// handoff says it used a handle, and the provenance the store keeps does
	// not mention it.
	const handle = "ctx_0123456789abcdef0123456789abcdef"
	undeclared := envelope(func(o map[string]any) {
		o["handoff"] = map[string]any{"summary": "s", "context_handles": []any{handle}}
		o["derived_from"] = []any{}
	})
	if _, err := validateFinalHandoff(undeclared); err == nil {
		t.Error("a handle cited but not declared as provenance was accepted")
	}

	declared := envelope(func(o map[string]any) {
		o["handoff"] = map[string]any{"summary": "s", "context_handles": []any{handle}}
		o["derived_from"] = []any{handle}
	})
	if _, err := validateFinalHandoff(declared); err != nil {
		t.Errorf("a properly declared handle was refused: %v", err)
	}
}

func TestADispositionMustBeOneOfTheDeclaredOnes(t *testing.T) {
	// The disposition is what a reader acts on. An invented one would be
	// stored and then mean whatever a later consumer guessed.
	bad := envelope(func(o map[string]any) {
		o["handoff"] = map[string]any{"summary": "s", "disposition": "looks-fine-to-me"}
	})
	if _, err := validateFinalHandoff(bad); err == nil {
		t.Error("an invented disposition was accepted")
	}
}

func TestNothingIsCapturedWithoutAnExplicitEnvelope(t *testing.T) {
	// stdout is never inspected. A child that prints something structured
	// has not made a handoff, and inferring one would make every transcript a
	// candidate for permanent unattended storage.
	capture := AutomaticContextCapture("/project",
		map[string]any{"output": `{"kind":"cadre-final-handoff","schema_version":1}`},
		"code-reviewer", "TASK-1", "SESSION-1", "internal", "internal")
	if capture["status"] != "not_provided" {
		t.Errorf("status = %v, want not_provided for output-only", capture["status"])
	}
}

func TestAChannelErrorIsReportedRatherThanCaptured(t *testing.T) {
	// "the child said nothing" and "the child said something we could not
	// read" are different facts, and only one of them is worth looking into.
	capture := AutomaticContextCapture("/project",
		map[string]any{"final_handoff_capture_error": "result file was replaced"},
		"code-reviewer", "TASK-1", "SESSION-1", "internal", "internal")
	if capture["status"] != "not_captured" {
		t.Errorf("status = %v, want not_captured", capture["status"])
	}
	if !strings.Contains(capture["reason"].(string), "replaced") {
		t.Errorf("reason = %v, want the channel's own explanation", capture["reason"])
	}
}

func TestCaptureRefusesRatherThanInventingIdentity(t *testing.T) {
	// Identity, scope and classification are dispatch-owned. With any of them
	// missing the entry would be unattributable, or readable by peers of no
	// particular session -- so it is refused rather than stored under a
	// guess.
	valid := envelope(nil)

	noTask := AutomaticContextCapture("/project",
		map[string]any{"final_handoff": valid},
		"code-reviewer", "", "SESSION-1", "internal", "internal")
	if noTask["status"] != "not_captured" {
		t.Errorf("a capture with no task id was not refused: %v", noTask)
	}

	noSession := AutomaticContextCapture("/project",
		map[string]any{"final_handoff": valid},
		"code-reviewer", "TASK-1", "", "internal", "internal")
	if noSession["status"] != "not_captured" {
		t.Errorf("a dispatch-scoped capture with no session was not refused: %v", noSession)
	}
}

func TestCaptureCannotExceedTheSessionClassificationCeiling(t *testing.T) {
	// The child does not get to label its own output above what the session
	// is cleared for.
	capture := AutomaticContextCapture("/project",
		map[string]any{"final_handoff": envelope(nil)},
		"code-reviewer", "TASK-1", "SESSION-1", "internal", "restricted")
	if capture["status"] != "not_captured" {
		t.Errorf("a capture above the ceiling was not refused: %v", capture)
	}
}
