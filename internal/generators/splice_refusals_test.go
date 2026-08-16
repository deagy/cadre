package generators

import (
	"strings"
	"testing"
)

// What the knowledge_focus splice refuses, and the invariant behind it.
//
// SpliceKnowledgeFocus is a targeted text edit into routing.json: it replaces
// the bytes between one `{` and its matching `}` and leaves every other byte
// alone. That is deliberate -- rewriting the file through a JSON encoder would
// reorder keys and reflow formatting that people read and diff -- and it is
// also the riskiest way to change a config file, because nothing about the
// edit is type-checked.
//
// catalog_generation_test.go covers the happy paths: a byte-exact no-op on an
// unchanged role set, appending a new role, and refusing a missing anchor.
// This covers the ways a targeted edit goes wrong.

func routingWithFocus(focusBlock string) string {
	return strings.Join([]string{
		"{",
		`  "version": 1,`,
		`  "routes": [`,
		`    {"id": "keep-me", "keywords": ["alpha"]}`,
		"  ],",
		focusBlock,
		`  "team_recipes": []`,
		"}",
		"",
	}, "\n")
}

const wellFormedFocus = `  "knowledge_focus": {
    "beta": "second role",
    "alpha": "first role"
  },`

func TestASpliceRefusesADocumentWithTwoAnchors(t *testing.T) {
	// Two anchors means the edit has to guess which block is the real one, and
	// a wrong guess writes the roster's focus prose into whatever the other
	// block was. Refusing is the only safe answer: there is no reading of the
	// file that makes one of them obviously right.
	doubled := strings.Join([]string{
		"{",
		wellFormedFocus,
		`  "vendor": {`,
		`    "knowledge_focus": {`,
		`      "unrelated": "a second block that happens to share the key"`,
		"    }",
		"  }",
		"}",
		"",
	}, "\n")

	_, err := SpliceKnowledgeFocus(doubled, []RoleMetadata{
		{ID: "alpha", KnowledgeFocus: "first role"},
	})
	if err == nil {
		t.Fatal("a document with two knowledge_focus anchors was spliced anyway")
	}
	if !strings.Contains(err.Error(), "exactly one") {
		t.Errorf("the refusal does not say what was ambiguous: %v", err)
	}
}

func TestASpliceRefusesABlockThatIsNeverClosed(t *testing.T) {
	// A truncated or hand-mangled routing.json. Without the check, the brace
	// scan runs to the end of the file and the "block" becomes everything
	// after the anchor -- so the splice would replace the rest of the document
	// with a focus map.
	unclosed := strings.Join([]string{
		"{",
		`  "knowledge_focus": {`,
		`    "alpha": "first role"`,
		"",
	}, "\n")

	_, err := SpliceKnowledgeFocus(unclosed, []RoleMetadata{
		{ID: "alpha", KnowledgeFocus: "first role"},
	})
	if err == nil {
		t.Fatal("a knowledge_focus block with no closing brace was spliced anyway")
	}
	if !strings.Contains(err.Error(), "matching closing") {
		t.Errorf("the refusal does not name the problem: %v", err)
	}
}

func TestABraceInsideAStringDoesNotEndTheBlock(t *testing.T) {
	// The scan tracks string state, and this is why. Focus prose is free text
	// written by role authors; one containing a `}` would otherwise close the
	// block early and truncate the splice mid-document.
	//
	// The brace is deliberately *unbalanced*. A balanced `{}` inside a string
	// takes the depth counter 1 -> 2 -> 1 and lands back where it started, so
	// a scan ignoring string state still finds the right closing brace and the
	// test passes while proving nothing -- which is what the first version of
	// this fixture did, and what removing the string-state check showed.
	original := routingWithFocus(`  "knowledge_focus": {
    "alpha": "prior defects, especially a stray } in generated output",
    "beta": "second role"
  },`)

	spliced, err := SpliceKnowledgeFocus(original, []RoleMetadata{
		{ID: "alpha", KnowledgeFocus: "prior defects, especially a stray } in generated output"},
		{ID: "beta", KnowledgeFocus: "second role"},
	})
	if err != nil {
		t.Fatalf("a brace inside focus prose broke the splice: %v", err)
	}
	if spliced != original {
		t.Errorf("an unchanged role set did not reproduce the original bytes.\n"+
			"original:\n%s\nspliced:\n%s", original, spliced)
	}
	// And the document after the block survived, which is what an early close
	// would have destroyed.
	if !strings.Contains(spliced, `"team_recipes": []`) {
		t.Error("content after the knowledge_focus block was lost")
	}
}

func TestASpliceLeavesEveryOtherKeyByteIdentical(t *testing.T) {
	// The property the whole approach exists for. A JSON round trip would
	// reorder keys and reflow the file; this edit must touch nothing outside
	// the block, including formatting nobody would notice changing until a
	// diff was unreadable.
	original := routingWithFocus(wellFormedFocus)
	spliced, err := SpliceKnowledgeFocus(original, []RoleMetadata{
		{ID: "beta", KnowledgeFocus: "second role"},
		{ID: "alpha", KnowledgeFocus: "first role, reworded"},
	})
	if err != nil {
		t.Fatalf("SpliceKnowledgeFocus: %v", err)
	}

	before, _, found := strings.Cut(original, `  "knowledge_focus": {`)
	if !found {
		t.Fatal("the fixture lost its anchor")
	}
	if !strings.HasPrefix(spliced, before) {
		t.Errorf("bytes before the block changed.\nwanted prefix:\n%s\ngot:\n%s",
			before, spliced)
	}
	_, afterOriginal, _ := strings.Cut(original, "  },\n")
	_, afterSpliced, _ := strings.Cut(spliced, "  },\n")
	if afterOriginal != afterSpliced {
		t.Errorf("bytes after the block changed.\nbefore:\n%s\nafter:\n%s",
			afterOriginal, afterSpliced)
	}
	// The one thing that should have changed, did.
	if !strings.Contains(spliced, "first role, reworded") {
		t.Error("the reworded focus was not written")
	}
	// And row order still follows the file, not the roles slice. Searched
	// inside the block: "alpha" also appears in the routes array above it, so
	// indexing the whole document compares the wrong occurrence -- which is
	// what the first version of this assertion did.
	_, block, _ := strings.Cut(spliced, `"knowledge_focus": {`)
	block, _, _ = strings.Cut(block, "\n  },")
	if strings.Index(block, `"beta"`) > strings.Index(block, `"alpha"`) {
		t.Errorf("the splice reordered existing rows to match the roles slice:\n%s", block)
	}
}

func TestTheInvariantCatchesASpliceThatDamagedAnotherKey(t *testing.T) {
	// The check that makes the surgical edit defensible. A brace-matching bug
	// need not produce invalid JSON -- it can produce *valid* JSON with a
	// neighbouring key eaten or altered, which is the failure a byte-level
	// edit is most prone to and the one hardest to see in a large diff.
	//
	// Exercised directly, because the point is that the verification would
	// catch a damaged result rather than that today's splice produces one.
	original := routingWithFocus(wellFormedFocus)
	focus := map[string]string{"alpha": "first role", "beta": "second role"}

	if err := verifySpliceLeftEverythingElseAlone(original, original, focus); err != nil {
		t.Fatalf("an untouched document was reported as damaged: %v", err)
	}

	for _, probe := range []struct{ name, damaged, wants string }{
		{"a neighbouring key altered",
			strings.Replace(original, `"keep-me"`, `"renamed-out-from-under-us"`, 1),
			"altered routing.json key"},
		// Removed in a way that leaves the document valid, so the invariant is
		// reporting a missing key rather than a parse failure. Dropping the
		// last key instead leaves a trailing comma, and the invariant then
		// says "not valid JSON" -- true, and not what this case is about.
		{"a neighbouring key removed",
			strings.Replace(original, "  \"version\": 1,\n", "", 1),
			"altered routing.json key"},
		{"a focus row silently dropped",
			strings.Replace(original, "    \"beta\": \"second role\",\n", "", 1),
			"id-set mismatch"},
	} {
		t.Run(probe.name, func(t *testing.T) {
			err := verifySpliceLeftEverythingElseAlone(original, probe.damaged, focus)
			if err == nil {
				t.Fatalf("damage went unreported:\n%s", probe.damaged)
			}
			if !strings.Contains(err.Error(), probe.wants) {
				t.Errorf("reported for a different reason than this case is about.\n"+
					"wanted something naming %q, got: %v", probe.wants, err)
			}
		})
	}
}

func TestTheInvariantRefusesAResultThatIsNoLongerJSON(t *testing.T) {
	// The coarsest damage, and the one a caller would otherwise discover when
	// something downstream failed to load routing.json.
	original := routingWithFocus(wellFormedFocus)
	focus := map[string]string{"alpha": "first role", "beta": "second role"}

	if err := verifySpliceLeftEverythingElseAlone(original, "{not json", focus); err == nil {
		t.Error("a spliced result that is not JSON was accepted")
	}
	if err := verifySpliceLeftEverythingElseAlone("{not json", original, focus); err == nil {
		t.Error("an original that is not JSON was accepted")
	}
}

func TestASplicedDocumentIsStillValidJSON(t *testing.T) {
	// End of the chain: whatever the edit produces has to load. Asserted after
	// a real change rather than a no-op, since a no-op reproduces bytes that
	// were already valid.
	original := routingWithFocus(wellFormedFocus)
	spliced, err := SpliceKnowledgeFocus(original, []RoleMetadata{
		{ID: "alpha", KnowledgeFocus: "first role"},
		{ID: "beta", KnowledgeFocus: "second role"},
		{ID: "gamma", KnowledgeFocus: `a "quoted" phrase and a \ backslash`},
	})
	if err != nil {
		t.Fatalf("SpliceKnowledgeFocus: %v", err)
	}
	focus := map[string]string{
		"alpha": "first role", "beta": "second role",
		"gamma": `a "quoted" phrase and a \ backslash`,
	}
	if err := verifySpliceLeftEverythingElseAlone(original, spliced, focus); err != nil {
		t.Fatalf("the splice's own verification rejected its output: %v", err)
	}
	if !strings.Contains(spliced, `\"quoted\"`) {
		t.Errorf("a quoted phrase was not escaped into the document:\n%s", spliced)
	}
}
