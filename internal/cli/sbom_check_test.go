package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// Ported from the two `python3 -c` SBOM assertions in release.yml.

// spdxWith builds an SPDX document naming the given packages.
func spdxWith(t *testing.T, names ...string) []byte {
	t.Helper()
	type pkg struct {
		Name string `json:"name"`
	}
	document := struct {
		Packages []pkg `json:"packages"`
	}{}
	for _, name := range names {
		document.Packages = append(document.Packages, pkg{Name: name})
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("building the fixture: %v", err)
	}
	return encoded
}

func TestAnSBOMFromTheWrongPathIsRefused(t *testing.T) {
	// syft reports success on a path it found nothing useful in, so this is
	// what a misaimed scan actually looks like: valid, signable, and empty.
	problems, counted, err := checkSBOM(spdxWith(t, "one", "two"), 200, nil)
	if err != nil {
		t.Fatalf("checkSBOM: %v", err)
	}
	if counted != 2 {
		t.Errorf("counted %d packages, expected 2", counted)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "fewer than the 200") {
		t.Errorf("a near-empty inventory was not refused: %v", problems)
	}
}

func TestAnSBOMMissingARequiredPackageIsRefused(t *testing.T) {
	// The count alone passes here. This is the other failure: the scan ran and
	// found plenty, but not the dependency set it was aimed at -- the wrong
	// cataloger, or a lockfile that resolved to nothing.
	many := make([]string, 0, 300)
	for i := 0; i < 300; i++ {
		many = append(many, "filler-"+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	problems, _, err := checkSBOM(spdxWith(t, many...), 200,
		[]string{"@cline/sdk", "zod"})
	if err != nil {
		t.Fatalf("checkSBOM: %v", err)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "@cline/sdk") {
		t.Errorf("a large SBOM missing its required packages was not refused: %v", problems)
	}
	if !strings.Contains(problems[0], "zod") {
		t.Errorf("the refusal names only some of the missing packages: %v", problems)
	}
}

func TestAGoodSBOMPasses(t *testing.T) {
	names := []string{"@cline/sdk", "@cline/shared", "zod"}
	for i := 0; i < 250; i++ {
		names = append(names, "pkg-"+string(rune('a'+i%26))+string(rune('0'+i/26)))
	}
	problems, counted, err := checkSBOM(spdxWith(t, names...), 200,
		[]string{"@cline/sdk", "@cline/shared", "zod"})
	if err != nil {
		t.Fatalf("checkSBOM: %v", err)
	}
	if len(problems) > 0 {
		t.Errorf("a complete inventory was refused: %v", problems)
	}
	if counted < 200 {
		t.Errorf("counted %d packages, expected at least 200", counted)
	}
}

func TestUnnamedPackagesAreNotCounted(t *testing.T) {
	// An SPDX document can carry entries with no name. Counting them would
	// inflate the total past the floor that exists to catch an empty scan.
	problems, counted, err := checkSBOM(spdxWith(t, "real", "", "", ""), 3, nil)
	if err != nil {
		t.Fatalf("checkSBOM: %v", err)
	}
	if counted != 1 {
		t.Errorf("counted %d packages; only one has a name", counted)
	}
	if len(problems) == 0 {
		t.Error("three unnamed entries were counted towards the floor")
	}
}

func TestDuplicatePackagesCountOnce(t *testing.T) {
	// The floor is on *distinct* names, matching the Python's set(). A scan
	// that reported the same package repeatedly would otherwise clear it.
	_, counted, err := checkSBOM(spdxWith(t, "same", "same", "same"), 1, nil)
	if err != nil {
		t.Fatalf("checkSBOM: %v", err)
	}
	if counted != 1 {
		t.Errorf("counted %d, expected duplicates to collapse to 1", counted)
	}
}

func TestAnUnparseableSBOMIsAnErrorNotAnEmptyInventory(t *testing.T) {
	// Reading it as zero packages would also refuse the release, but for a
	// reason that sends the reader looking at syft's output rather than at the
	// file being unreadable.
	if _, _, err := checkSBOM([]byte("{ not json"), 1, nil); err == nil {
		t.Error("an unparseable SBOM was accepted as a document with no packages")
	}
}
