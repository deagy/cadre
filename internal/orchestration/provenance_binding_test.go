package orchestration

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// Provenance: what it binds a plan to, and what it must never invent.
//
// provenance_test.go covers the shape -- fields set, fields left alone, a
// missing catalog failing hard. This covers what that leaves, which is mostly
// the difference between a field being *present* and being *right*:
//
//   - TestHashFile asserts the hash begins with "sha256:". That is a label
//     check. A function hashing the file's *path*, or computing SHA-1 and
//     labelling it sha256, passes it.
//   - GitIdentity's contract distinguishes an empty dirty list from a nil one
//     -- "clean tree" versus "git could not tell us" -- and nothing exercised
//     either branch against a real repository.
//
// Ported from roster/orchestration/test/test_provenance.py, which tests the
// Python selector's provenance module and goes with it.

// independentSHA256 computes the hash a second way, without going through any
// code under test here.
func independentSHA256(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func TestTheContentHashIsTheSHA256OfTheFileContents(t *testing.T) {
	// Not "starts with sha256:". The hash is what binds a plan to the exact
	// catalog and routing rules that produced it, so a plan claiming a hash
	// nobody can reproduce is worse than a plan claiming none: it reads as
	// evidence while being unverifiable.
	directory := t.TempDir()
	path := writeTestFile(t, directory, "catalog.yaml", "version: 1\nroles:\n  a: {}\n")

	got, err := HashFile(path)
	if err != nil {
		t.Fatalf("HashFile: %v", err)
	}
	if want := independentSHA256(t, path); got != want {
		t.Errorf("hash = %s\nwant  %s (sha256 of the file's bytes)", got, want)
	}

	// Two files with identical contents under different names hash the same,
	// which is what rules out the path leaking into the digest.
	other := writeTestFile(t, directory, "differently-named.yaml", "version: 1\nroles:\n  a: {}\n")
	otherHash, err := HashFile(other)
	if err != nil {
		t.Fatal(err)
	}
	if otherHash != got {
		t.Errorf("identical contents under a different name hashed differently:\n%s\n%s",
			got, otherHash)
	}
}

func TestEditingOneInputMovesOnlyItsOwnHash(t *testing.T) {
	// The two hashes answer separate questions -- "which roles existed" and
	// "which rules routed to them". A shared or swapped digest still changes
	// when anything changes, so the plan still looks bound; it just stops
	// saying *which* input moved, which is the only reason to record two.
	directory := t.TempDir()
	catalogPath := writeTestFile(t, directory, "catalog.yaml", "version: 1\n")
	routingPath := writeTestFile(t, directory, "routing.json", `{"version":1,"routes":[]}`)

	before, err := BuildProvenance(catalogPath, routingPath, BuildProvenanceOptions{})
	if err != nil {
		t.Fatalf("BuildProvenance: %v", err)
	}

	writeTestFile(t, directory, "catalog.yaml", "version: 2\n")
	after, err := BuildProvenance(catalogPath, routingPath, BuildProvenanceOptions{})
	if err != nil {
		t.Fatalf("BuildProvenance: %v", err)
	}

	if after.CatalogContentHash == before.CatalogContentHash {
		t.Error("the catalog changed and its hash did not")
	}
	if after.RoutingContentHash != before.RoutingContentHash {
		t.Errorf("editing the catalog moved the routing hash:\n%s\n%s",
			before.RoutingContentHash, after.RoutingContentHash)
	}

	// And the same in the other direction, so this cannot pass by the two
	// hashes being derived from the catalog alone.
	writeTestFile(t, directory, "routing.json", `{"version":1,"routes":[{"id":"x"}]}`)
	third, err := BuildProvenance(catalogPath, routingPath, BuildProvenanceOptions{})
	if err != nil {
		t.Fatalf("BuildProvenance: %v", err)
	}
	if third.RoutingContentHash == after.RoutingContentHash {
		t.Error("routing changed and its hash did not")
	}
	if third.CatalogContentHash != after.CatalogContentHash {
		t.Error("editing routing moved the catalog hash")
	}
}

func TestProvenanceIsTheSameForTwoRunsOfTheSameInputs(t *testing.T) {
	// A field that varies between two runs of an unchanged checkout is not
	// provenance -- it is noise that makes every plan look different from
	// every other. This is also why provenance is excluded from the
	// fingerprint; see the selector-side test.
	directory := t.TempDir()
	catalogPath := writeTestFile(t, directory, "catalog.yaml", "version: 1\n")
	routingPath := writeTestFile(t, directory, "routing.json", `{"version":1}`)

	first, err := BuildProvenance(catalogPath, routingPath, BuildProvenanceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildProvenance(catalogPath, routingPath, BuildProvenanceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if *first != *second {
		t.Errorf("two runs over unchanged inputs differ:\n%+v\n%+v", *first, *second)
	}
}

// initGitRepo makes a real repository with one commit, so the git-identity
// cases run against git rather than against a mock of it.
func initGitRepo(t *testing.T) (directory, catalogPath, routingPath string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	directory = t.TempDir()
	// The inputs live in a subdirectory, as they do in a real checkout
	// (roster/catalog.yaml). This is load-bearing for the dirty-path case:
	// GitIdentity runs git from the catalog's own directory, so with the files
	// at the repository root a path relative to the root and a path relative
	// to that directory are the same string, and the test cannot tell a
	// correct implementation from one asking git for root-relative paths.
	roster := filepath.Join(directory, "roster")
	if err := os.MkdirAll(roster, 0o755); err != nil {
		t.Fatal(err)
	}
	catalogPath = writeTestFile(t, roster, "catalog.yaml", "version: 1\n")
	routingPath = writeTestFile(t, roster, "routing.json", `{"version":1}`)

	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "test@example.invalid"},
		{"config", "user.name", "Test"},
		{"config", "commit.gpgsign", "false"},
		{"add", "roster/catalog.yaml", "roster/routing.json"},
		{"commit", "-q", "-m", "initial"},
	} {
		command := exec.Command("git", args...)
		command.Dir = directory
		// A developer's own git config must not decide whether this passes.
		command.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if output, err := command.CombinedOutput(); err != nil {
			t.Skipf("git %v failed, so this environment cannot run the case: %v\n%s",
				args, err, output)
		}
	}
	return directory, catalogPath, routingPath
}

func TestGitIdentityRecordsTheHeadCommitAndACleanTree(t *testing.T) {
	directory, catalogPath, routingPath := initGitRepo(t)

	sha, dirty, ok := GitIdentity(catalogPath, routingPath)
	if !ok {
		t.Fatal("a real repository was not recognised as one")
	}

	// Compared against git's own answer, not against a shape.
	command := exec.Command("git", "rev-parse", "HEAD")
	command.Dir = directory
	expected, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	if want := string(expected[:len(expected)-1]); sha != want {
		t.Errorf("commit sha = %q, want git rev-parse HEAD = %q", sha, want)
	}

	// The distinction the implementation's comment calls out: a clean tree
	// reports an empty list, and nil is reserved for "git status could not
	// run". Collapsing the two makes an unreadable checkout indistinguishable
	// from a pristine one -- in the direction that looks reassuring.
	if dirty == nil {
		t.Error("a clean tree reported nil, which means git could not tell us")
	}
	if len(dirty) != 0 {
		t.Errorf("a freshly committed tree reported dirty paths: %v", dirty)
	}
}

func TestGitIdentityNamesTheUncommittedInputs(t *testing.T) {
	// The point of recording dirty paths at all: a plan generated against an
	// edited working tree is not reproducible from its commit sha alone, and
	// the plan should say so rather than cite a commit that never contained
	// the rules it used.
	directory, catalogPath, routingPath := initGitRepo(t)
	writeTestFile(t, filepath.Join(directory, "roster"), "catalog.yaml", "version: 2\n")

	_, dirty, ok := GitIdentity(catalogPath, routingPath)
	if !ok {
		t.Fatal("a real repository was not recognised as one")
	}
	if !slices.Contains(dirty, "catalog.yaml") {
		t.Errorf("the edited input is not listed: %v", dirty)
	}
	if slices.Contains(dirty, "routing.json") {
		t.Errorf("an unedited input was listed as dirty: %v", dirty)
	}
	// Relative, not absolute: an absolute path records the generating
	// machine's directory layout in something meant to be comparable across
	// checkouts.
	for _, path := range dirty {
		if filepath.IsAbs(path) {
			t.Errorf("dirty path %q is absolute", path)
		}
	}
}

func TestOutsideAGitTreeTheContentHashesStillStand(t *testing.T) {
	// A consuming project may vendor the roster without a repository, or run
	// from an unpacked archive. Content hashes are computable there and the
	// git fields are not, so the plan should carry what it can rather than
	// failing or -- worse -- reporting a commit it did not read.
	directory := t.TempDir()
	catalogPath := writeTestFile(t, directory, "catalog.yaml", "version: 1\n")
	routingPath := writeTestFile(t, directory, "routing.json", `{"version":1}`)

	provenance, err := BuildProvenance(catalogPath, routingPath, BuildProvenanceOptions{})
	if err != nil {
		t.Fatalf("BuildProvenance outside a git tree: %v", err)
	}
	if provenance.CatalogContentHash != independentSHA256(t, catalogPath) {
		t.Error("the catalog hash is missing or wrong outside a git tree")
	}
	if provenance.RoutingContentHash != independentSHA256(t, routingPath) {
		t.Error("the routing hash is missing or wrong outside a git tree")
	}
	if provenance.GitCommitSHA != "" {
		t.Errorf("a commit sha was reported outside any repository: %q",
			provenance.GitCommitSHA)
	}
}

func TestWithNoGitBinaryTheRestOfProvenanceSurvives(t *testing.T) {
	// Distinct from "not a repository": here the checkout may well be one and
	// the tool to ask is absent -- a slim container, a locked-down runner. The
	// failure to avoid is a selection run that dies because a *provenance*
	// field could not be filled in.
	directory, catalogPath, routingPath := initGitRepo(t)
	t.Setenv("PATH", "")

	if _, _, ok := GitIdentity(catalogPath, routingPath); ok {
		t.Error("git identity was reported with no git on PATH")
	}
	provenance, err := BuildProvenance(catalogPath, routingPath, BuildProvenanceOptions{})
	if err != nil {
		t.Fatalf("provenance failed rather than degrading: %v", err)
	}
	if provenance.CatalogContentHash != independentSHA256(t, filepath.Join(directory, "roster", "catalog.yaml")) {
		t.Error("the content hashes did not survive git being unavailable")
	}
	if provenance.GitCommitSHA != "" {
		t.Errorf("a commit sha appeared with no git to produce it: %q",
			provenance.GitCommitSHA)
	}
}
