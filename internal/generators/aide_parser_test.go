package generators

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Reading aides.yaml: what a hand-edited data table may not say.
//
// The file declares eight authority aides, each with a title, gate numbers and
// knowledge_focus prose. Every value reaches a generated AGENT.md that a human
// reads to know which lifecycle gates they prepare decision packages for, so a
// malformed entry is not caught by anything downstream -- it renders.

func writeAides(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "aides.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const oneGoodAide = `aides:
  product-owner-aide:
    title: Product Owner
    gates: [1, 2, 6]
    knowledge_focus: prior decisions
`

func TestAWellFormedAideTableLoads(t *testing.T) {
	// The baseline every refusal below is measured against. Without it, a
	// loader that rejected everything would satisfy them all.
	aides, err := LoadAides(writeAides(t, oneGoodAide))
	if err != nil {
		t.Fatalf("a well-formed table was refused: %v", err)
	}
	if len(aides) != 1 {
		t.Fatalf("loaded %d aides", len(aides))
	}
	aide := aides[0]
	if aide.ID != "product-owner-aide" || aide.Title != "Product Owner" {
		t.Errorf("identity came out as %q / %q", aide.ID, aide.Title)
	}
	if len(aide.Gates) != 3 || aide.Gates[0] != 1 || aide.Gates[1] != 2 || aide.Gates[2] != 6 {
		t.Errorf("gates came out as %v, want [1 2 6]", aide.Gates)
	}
	if aide.KnowledgeFocus != "prior decisions" {
		t.Errorf("knowledge_focus came out as %q", aide.KnowledgeFocus)
	}
}

func TestAGateListedTwiceIsRefused(t *testing.T) {
	// It renders. gatePhrase turns [1, 1, 2] into "gates G1, G1, and G2",
	// which ships in the aide's own brief.
	//
	// Refused rather than de-duplicated: silently collapsing it would hide a
	// typo in a hand-edited file whose entire purpose is to say which gates an
	// authority prepares for, and the person who wrote `[1, 1, 2]` probably
	// meant a gate they have now lost.
	_, err := LoadAides(writeAides(t, `aides:
  product-owner-aide:
    title: Product Owner
    gates: [1, 1, 2]
    knowledge_focus: prior decisions
`))
	if err == nil {
		t.Fatal("an aide listing a gate twice was accepted")
	}
	for _, want := range []string{"product-owner-aide", "duplicate", "1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
}

func TestEveryDuplicatedGateIsNamedOnce(t *testing.T) {
	// Several duplicates report together rather than one edit-and-retry round
	// each, and a gate repeated three times is still named once.
	_, err := LoadAides(writeAides(t, `aides:
  a:
    title: T
    gates: [1, 1, 1, 3, 3, 5]
    knowledge_focus: f
`))
	if err == nil {
		t.Fatal("duplicates were accepted")
	}
	message := err.Error()
	if !strings.Contains(message, "1") || !strings.Contains(message, "3") {
		t.Errorf("not every duplicated gate is named: %v", err)
	}
	if strings.Count(message, "1,") > 1 {
		t.Errorf("a gate duplicated three times was reported more than once: %v", err)
	}
	if strings.Contains(strings.ReplaceAll(message, "duplicate", ""), "5") {
		t.Errorf("a gate that appears once was reported as duplicated: %v", err)
	}
}

func TestAnAideMissingARequiredFieldIsRefusedByName(t *testing.T) {
	// One case per field, because each is separately required and each ends up
	// in the rendered brief. A missing one would otherwise render as an empty
	// heading or an aide with no gates at all.
	for _, probe := range []struct{ name, body, wants string }{
		{"no title", `aides:
  a:
    gates: [1]
    knowledge_focus: f
`, "title"},
		{"no gates", `aides:
  a:
    title: T
    knowledge_focus: f
`, "gates"},
		{"an empty gates list", `aides:
  a:
    title: T
    gates: []
    knowledge_focus: f
`, "gates"},
		{"no knowledge_focus", `aides:
  a:
    title: T
    gates: [1]
`, "knowledge_focus"},
	} {
		t.Run(probe.name, func(t *testing.T) {
			_, err := LoadAides(writeAides(t, probe.body))
			if err == nil {
				t.Fatal("an incomplete aide was accepted")
			}
			if !strings.Contains(err.Error(), probe.wants) {
				t.Errorf("the refusal does not name the missing field %q: %v",
					probe.wants, err)
			}
			if !strings.Contains(err.Error(), `"a"`) {
				t.Errorf("the refusal does not name the aide: %v", err)
			}
		})
	}
}

func TestAnUnreadableAideTableIsRefused(t *testing.T) {
	// Distinct failures, so a corrupted file is not mistaken for an empty one.
	if _, err := LoadAides(writeAides(t, "aides:\n  a:\n   bad indent\n")); err == nil {
		t.Error("malformed YAML was accepted")
	}
	if _, err := LoadAides(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Error("a missing aides.yaml was accepted")
	}
	// A non-integer gate is the parser's job, not the contract check's: a
	// contract cross-check never sees a value that failed to parse.
	if _, err := LoadAides(writeAides(t, `aides:
  a:
    title: T
    gates: [one]
    knowledge_focus: f
`)); err == nil {
		t.Error("a non-integer gate was accepted")
	}
}

func TestBlockStyleGatesAreAcceptedWhereThePythonParserRefusedThem(t *testing.T) {
	// A deliberate difference, recorded rather than left as a surprise.
	//
	// The Python generator hand-parsed the line and could only read the inline
	// `[1, 2]` form, so it raised a clear error on block style. This uses a
	// real YAML parser, for which both spellings are the same document -- so
	// it accepts more than its predecessor did, in the direction of reading
	// valid YAML correctly.
	aides, err := LoadAides(writeAides(t, `aides:
  a:
    title: T
    gates:
      - 1
      - 2
    knowledge_focus: f
`))
	if err != nil {
		t.Fatalf("block-style gates were refused: %v", err)
	}
	if len(aides) != 1 || len(aides[0].Gates) != 2 {
		t.Errorf("block-style gates did not parse as two gates: %+v", aides)
	}
}
