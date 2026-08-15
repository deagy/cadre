package selector

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These pin what probe_discover_parity.py measured against Python across 47
// cases. The probe is the evidence; these are what CI can afford to run.

func TestExplicitFilesDeduplicatesInFirstSeenOrder(t *testing.T) {
	// changed_files reaches the hashed payload, so naming one path twice
	// would otherwise fingerprint differently from naming it once -- the same
	// set of changes producing two different plans.
	got := ExplicitFiles([]string{"a.go,b.go", "a.go", " c.go ", "b.go"})
	if strings.Join(got, ",") != "a.go,b.go,c.go" {
		t.Errorf("ExplicitFiles = %v, want first-seen order with duplicates dropped", got)
	}
	if got := ExplicitFiles([]string{",", "  ", ""}); len(got) != 0 {
		t.Errorf("ExplicitFiles = %v, want nothing from empty segments", got)
	}
}

func TestNormalizeExplicitSourcesRefusesAnEmptyValue(t *testing.T) {
	// An empty source would produce source_filter: [""], a plan that violates
	// its own schema and whose argv the store rejects only at execution --
	// after the invalid plan has been emitted and possibly consumed.
	if _, err := NormalizeExplicitSources([]string{"one", "  "}); err == nil {
		t.Fatal("a blank --source must be refused")
	} else if !strings.Contains(err.Error(), "argument 2 was empty") {
		t.Errorf("error = %q, want it to name which argument was empty", err)
	}

	got, err := NormalizeExplicitSources([]string{"a", "b", "a"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "a,b" {
		t.Errorf("sources = %v, want order-preserving de-duplication", got)
	}
}

func TestOriginSlugAcceptsTheThreeRemoteForms(t *testing.T) {
	for _, testCase := range []struct {
		origin string
		want   string
	}{
		{"https://github.com/deagy/cadre.git", "deagy/cadre"},
		{"https://github.com/deagy/cadre", "deagy/cadre"},
		{"ssh://git@github.com/deagy/cadre.git", "deagy/cadre"},
		{"git@github.com:deagy/cadre.git", "deagy/cadre"},
		// Case is normalised, so two clones of one repository that happen to
		// disagree on capitalisation still scope retrieval to one source.
		{"https://GitHub.com/Deagy/Cadre.git", "deagy/cadre"},
		// Deepest two path segments win, so a nested GitLab group resolves to
		// its subgroup and project rather than to the top-level group.
		{"git@gitlab.example.com:group/subgroup/project.git", "subgroup/project"},
		{"https://user:token@host/owner/repo.git", "owner/repo"},
		// Only the final .git is a suffix to strip.
		{"https://host/owner/repo.git.git", "owner/repo.git"},
	} {
		root := newProbeCheckout(t, testCase.origin)
		got, ok := OriginSlug(root)
		if !ok || got != testCase.want {
			t.Errorf("OriginSlug(%q) = %q/%v, want %q", testCase.origin, got, ok, testCase.want)
		}
	}
}

func TestOriginSlugRefusesRatherThanGuessing(t *testing.T) {
	// This string scopes knowledge retrieval. A wrong slug reads another
	// project's corpus, so anything that does not reduce cleanly to
	// owner/repository must yield no slug at all.
	for _, origin := range []string{
		"https://host/only-one-part.git",
		"https://host/",
		"git@host:owner/repo with spaces.git",
		"https://host/owner/repo!bang.git",
	} {
		root := newProbeCheckout(t, origin)
		if got, ok := OriginSlug(root); ok {
			t.Errorf("OriginSlug(%q) = %q, want no slug rather than a guess", origin, got)
		}
	}
}

func TestProjectKnowledgeSourceFallsBackToAPathDigest(t *testing.T) {
	// With no usable origin the source must still be stable and unique to
	// this checkout, so two same-named directories do not share a corpus.
	first := newProbeCheckout(t, "")
	second := newProbeCheckout(t, "")

	firstSource := ResolveProjectKnowledgeSource(first)
	if !strings.HasPrefix(firstSource, "local-") {
		t.Errorf("source = %q, want a local- fallback", firstSource)
	}
	if firstSource == ResolveProjectKnowledgeSource(second) {
		t.Error("two distinct checkouts must not resolve to one source")
	}
	if firstSource != ResolveProjectKnowledgeSource(first) {
		t.Error("the fallback source must be stable across calls")
	}
}

func TestStagedSourceOnlyWhenAProjectLocalStoreExists(t *testing.T) {
	// The store refuses to read proposed-knowledge from the shared
	// global-fallback store, and refusal is per call, not per source. A plan
	// that named it unconditionally would return the agent nothing at all --
	// including the project's own corpus it would otherwise have retrieved.
	root := newProbeCheckout(t, "")
	if got := ResolveKnowledgeSources(root); len(got) != 1 {
		t.Errorf("sources = %v, want only the project's own corpus", got)
	}

	configPath := filepath.Join(root, ".agents", "knowledge-store", "config.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := ResolveKnowledgeSources(root)
	if len(got) != 2 || got[1] != StagedKnowledgeSource {
		t.Errorf("sources = %v, want the staged source appended", got)
	}
}

func TestStagedSourceIgnoresADirectoryNamedConfigJSON(t *testing.T) {
	// Existence is not the question; being a file is.
	root := newProbeCheckout(t, "")
	if err := os.MkdirAll(filepath.Join(root, ".agents", "knowledge-store", "config.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := ResolveKnowledgeSources(root); len(got) != 1 {
		t.Errorf("sources = %v, want a directory not to enable the staged source", got)
	}
}

func TestDiscoverChangedFilesSkipsARenamesOriginalPath(t *testing.T) {
	// A rename entry appends the original path as one extra NUL-separated
	// field. Failing to step over it puts a path that does not exist into the
	// plan -- and because the status prefix is three bytes wide, the phantom
	// arrives with its first three characters missing.
	root := newProbeCheckout(t, "")
	writeAndCommit(t, root, "original.txt", "body\n")
	runProbeGit(t, root, "mv", "original.txt", "moved.txt")
	runProbeGit(t, root, "add", "-A")

	got, err := DiscoverChangedFiles("", root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.Files, ",") != "moved.txt" {
		t.Errorf("files = %v, want only the destination path", got.Files)
	}
	if got.Source != "git-status" {
		t.Errorf("source = %q, want git-status", got.Source)
	}
}

func TestDiscoverChangedFilesKeepsQuotablePathsIntact(t *testing.T) {
	// -z is why these survive: git's default --short would apply
	// core.quotePath and wrap them in C-style escapes, which line-wise
	// parsing would carry into the plan as a mangled path rather than
	// failing.
	root := newProbeCheckout(t, "")
	for _, name := range []string{"café.txt", "日本語.md", "with space.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := DiscoverChangedFiles("", root)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(got.Files, ",")
	for _, name := range []string{"café.txt", "日本語.md", "with space.txt"} {
		if !strings.Contains(joined, name) {
			t.Errorf("files = %v, want %q intact", got.Files, name)
		}
	}
}

func TestDiscoverChangedFilesWithBaseAnswersADifferentQuestion(t *testing.T) {
	// A base-ref diff reports what the branch changes; status reports what is
	// uncommitted. An uncommitted file belongs to the second answer only.
	root := newProbeCheckout(t, "")
	writeAndCommit(t, root, "seed.txt", "seed\n")
	runProbeGit(t, root, "checkout", "-q", "-b", "feature")
	writeAndCommit(t, root, "added.go", "package main\n")
	if err := os.WriteFile(filepath.Join(root, "uncommitted.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := DiscoverChangedFiles("main", root)
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != "git-diff:main...HEAD" {
		t.Errorf("source = %q, want the diff spec recorded", got.Source)
	}
	if strings.Join(got.Files, ",") != "added.go" {
		t.Errorf("files = %v, want only what the branch committed", got.Files)
	}

	// An unknown base must fail rather than report an empty change set --
	// "nothing changed" and "I could not tell" route very differently.
	if _, err := DiscoverChangedFiles("no-such-ref", root); err == nil {
		t.Error("an unresolvable base ref must be an error, not an empty diff")
	}
}

func runProbeGit(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = root
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func newProbeCheckout(t *testing.T, origin string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	root := t.TempDir()
	runProbeGit(t, root, "init", "-q", "-b", "main")
	runProbeGit(t, root, "config", "user.email", "test@example.invalid")
	runProbeGit(t, root, "config", "user.name", "Test")
	if origin != "" {
		runProbeGit(t, root, "remote", "add", "origin", origin)
	}
	return root
}

func writeAndCommit(t *testing.T, root, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	runProbeGit(t, root, "add", "-A")
	runProbeGit(t, root, "commit", "-q", "-m", "commit "+name, "--no-gpg-sign")
}
