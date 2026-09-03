package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Operational documentation may not point at a path that does not exist.
//
// The sibling guard in dead_python_references_test.go catches one shape of
// this -- a named .py module that was deleted. This is the general case, and
// it exists because the narrow one could not see the defect that actually
// shipped: nine live documents told a reader the lifecycle kernel lived in
// this repository under `kernel/`, a directory deleted at 11eefd47. None of
// them named a .py file, so nothing caught them.
//
// Four passes were needed to clear that, and each was incomplete for its own
// reason:
//
//   - the first searched for phrasings, and prose has more phrasings than
//     anyone enumerates;
//   - the second widened the pattern, and missed four more;
//   - the third resolved paths against the tree -- the right idea -- but
//     reported findings per file, so a file holding one corrected instance
//     and one uncorrected read as clean;
//   - the fourth found a single line carrying two separate assertions.
//
// So this reports file:line, and it derives the question from the filesystem
// rather than from a guess about wording: a document claiming something lives
// here necessarily names a path, and the path either resolves or it does not.
//
// Scope is every tracked document except generated output and records, and
// that default is inverted from the sibling guard's on purpose.
//
// That guard lists its documents by hand, arguing that whether a file claims
// something about the present is a judgement per file rather than a path
// pattern. The argument is sound and the list is still a list: it named 23 of
// this repository's 291 live documents, and the whole of docs/kernel/ was
// outside it, so three documents describing how to use the kernel went four
// verification rounds without being read. A curated set cannot fail on a
// document nobody thought to curate.
//
// So this one starts from everything and subtracts, and the subtraction is a
// rule rather than a roster. A record of what was true when written -- a
// changelog, an ADR, a migration write-up, a superseded plan -- names dead
// paths as its subject matter, and rewriting one to satisfy a test falsifies
// the record.

// referencedPath matches a markdown link target and a backticked path.
//
// Both forms, because the same claim is written both ways: "[`kernel/`](kernel/)"
// and "under `kernel/`" say the same thing and were both wrong.
var (
	markdownLinkTarget = regexp.MustCompile(`\[[^\]]*\]\(([^)\s#]+)\)`)
	backtickedPath     = regexp.MustCompile("`([A-Za-z0-9_.-]+/[A-Za-z0-9_./-]*)`")
	selfRepoTreeURL    = regexp.MustCompile(`https://github\.com/deagy/cadre/(?:tree|blob)/[^/]+/([^)\s#]+)`)

	// A bare directory token, unbackticked, inside prose or a diagram label.
	//
	// This exists because the guard could not see the thing it was written to
	// catch. docs/terminology.md said the kernel lives "within one repository"
	// and drew a mermaid node labelled `kernel/` -- no backticks, no markdown
	// link -- so it survived four rounds of a defect class this guard is the
	// instrument for, while another document claimed the class was closed and
	// guarded.
	//
	// Deliberately narrow: only the top-level directories this repository
	// either has or is known to have lost, and only where the token ends in a
	// slash. Matching every `word/` in prose would report a URL fragment, a
	// GitLab group, and every path in every other project.
	//
	// Anchored so a path *ending* in these names does not match: `internal/kernel/`
	// is this repository's own concern and `~/.claude/plugins/data/<id>/kernel/`
	// is a directory in the reader's environment. Only a bare token, at a word
	// boundary with no path separator before it, is a claim about the top level
	// here.
	bareDirectoryToken = regexp.MustCompile(`(?:^|[\s"'(\[])(kernel|engine)/(?:[\s"',.)\]]|$)`)
)

// pathRootsThisRepoOwns are the first segments a reference must start with to
// be a claim about this repository.
//
// Without this the guard reports every `foo/bar` in prose -- a URL fragment, a
// GitLab group, another project's layout. The list is the top level of this
// repository, so a reference starting with one of these is asserting something
// about a path here.
var pathRootsThisRepoOwns = map[string]bool{
	"bin": true, "cline-plugins": true, "cmd": true, "docs": true,
	"engine": true, "internal": true, "kernel": true, "packaging": true,
	"plugin": true, "provider": true, "roster": true, ".github": true,
}

// pathsThatNameSomewhereElse are references that look like this repository's
// layout and are not.
//
// Two kinds, and both are legitimate prose. A document explaining where a
// *target project* keeps its configuration names `.agents/...` paths that
// exist in the reader's repository, never in this one. A document explaining
// what the kernel owns names the kernel repository's own layout.
//
// A map with a reason each, so widening it is a sentence somebody has to
// write rather than a silent loosening -- the same discipline the sibling
// guard applies to module names.
var pathsThatNameSomewhereElse = map[string]string{
	".agents/knowledge-store/config.json":   "a store config in the reader's own project, created by them; never present here",
	".agents/cadre.yaml":                    "project-local operator settings in the reader's repository",
	".claude/agents":                        "a runtime override directory in the reader's environment",
	".codex/agents":                         "a runtime override directory in the reader's environment",
	"kernel/contracts":                      "the kernel repository's own layout, at deagy/cadre-kernel",
	"bin/agentic-sdlc":                      "the build-and-exec wrapper in deagy/cadre-kernel's checkout, named here when explaining how that repository works",
	"kernel/contracts/lifecycle-gates.json": "the kernel repository's own file, at deagy/cadre-kernel",
}

func trackedPaths(t *testing.T, root string) map[string]bool {
	t.Helper()
	command := exec.Command("git", "ls-files")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		t.Skipf("cannot list tracked files (not a git checkout?): %v", err)
	}
	// Every tracked file, and every directory prefix of one. Asked of git for
	// the reason the sibling guard documents: a filesystem walk finds stale
	// worktrees under .worktrees/ holding full checkouts of older commits,
	// where the deleted directories are all still present and every dead
	// reference looks alive.
	paths := map[string]bool{}
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		paths[line] = true
		for directory := filepath.Dir(line); directory != "." && directory != "/"; directory = filepath.Dir(directory) {
			paths[filepath.ToSlash(directory)] = true
		}
	}
	return paths
}

// generatedTrees are produced by generate-role-metadata, generate-plugin and
// port-cline-agents. A dead reference in one of them comes from a source that
// is also scanned, so fixing the source fixes every copy; reporting all four
// would turn one defect into hundreds of findings.
var generatedTrees = []string{"plugin/", "cline-plugins/", "provider/"}

// recordPathMarkers identify documents that describe a past state by their
// nature rather than by a banner.
var recordPathMarkers = []string{
	"CHANGELOG", "_PLAN.md", "_SCOPE.md", "ROADMAP", "ADR-",
	"/proposals/", "/migration/", "/investigations/", "proposed-knowledge/",
	"docs/archive/", "roster/orchestration/runs/", "ARCHITECTURE.md",
}

// recordBanner is the self-declaration a document carries when it is kept as
// history. Checked against the opening of the file, where such a banner
// belongs -- a reader who has to scroll to find it has already been misled.
var recordBanner = regexp.MustCompile(`(?i)historical record|not a description of the shipped|superseded|record of what was`)

func liveDocuments(t *testing.T, root string) []string {
	t.Helper()
	command := exec.Command("git", "ls-files", "*.md")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		t.Skipf("cannot list tracked files (not a git checkout?): %v", err)
	}
	var live []string
	for _, relative := range strings.Split(string(output), "\n") {
		relative = strings.TrimSpace(relative)
		if relative == "" {
			continue
		}
		generated := false
		for _, tree := range generatedTrees {
			if strings.HasPrefix(relative, tree) {
				generated = true
			}
		}
		if generated || strings.Contains(relative, "/agents/") {
			continue
		}
		record := false
		for _, marker := range recordPathMarkers {
			if strings.Contains(relative, marker) {
				record = true
			}
		}
		if record {
			continue
		}
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			continue
		}
		opening := content
		if len(opening) > 1400 {
			opening = opening[:1400]
		}
		if recordBanner.Match(opening) {
			continue
		}
		live = append(live, relative)
	}
	return live
}

func TestNoOperationalDocPointsAtAPathThatIsGone(t *testing.T) {
	root := filepath.Dir(filepath.Dir(mustGetwd(t)))
	if _, err := os.Stat(filepath.Join(root, "roster")); err != nil {
		t.Skipf("not running inside a source checkout: %v", err)
	}
	tracked := trackedPaths(t, root)

	paths := liveDocuments(t, root)

	check := func(doc string, number int, line, reference string, findings *[]string) {
		reference = strings.TrimSuffix(reference, "/")
		if reference == "" {
			return
		}
		root := strings.SplitN(reference, "/", 2)[0]
		if !pathRootsThisRepoOwns[root] {
			return
		}
		if tracked[reference] {
			return
		}
		if _, elsewhere := pathsThatNameSomewhereElse[reference]; elsewhere {
			return
		}
		*findings = append(*findings, doc+":"+itoa(number)+"  points at "+reference+", which this repository does not have\n      "+strings.TrimSpace(line))
	}

	read := 0
	var findings []string
	for _, relative := range paths {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			findings = append(findings, relative+" is listed as operational but does not exist")
			continue
		}
		read++
		for index, line := range strings.Split(string(content), "\n") {
			number := index + 1
			// The same escape the sibling guard uses: a line may name a
			// removed path deliberately, and saying so is explicit and
			// greppable rather than inferred from phrasing.
			// A line may name a removed path deliberately. The escape is
			// explicit rather than inferred, but it accepts the natural
			// phrasing as well as the marker, because "the `kernel/`
			// directory was deleted at 11eefd47" is exactly the sentence
			// this guard wants documents to contain.
			if strings.Contains(line, "(deleted)") || strings.Contains(line, "(removed)") ||
				strings.Contains(line, "deleted at") || strings.Contains(line, "deleted from this repository") ||
				strings.Contains(line, "It began as") || strings.Contains(line, "until the port finished") {
				continue
			}
			for _, match := range markdownLinkTarget.FindAllStringSubmatch(line, -1) {
				target := match[1]
				if strings.HasPrefix(target, "http") || strings.HasPrefix(target, "mailto:") {
					continue
				}
				check(relative, number, line, target, &findings)
			}
			for _, match := range selfRepoTreeURL.FindAllStringSubmatch(line, -1) {
				check(relative, number, line, match[1], &findings)
			}
			for _, match := range backtickedPath.FindAllStringSubmatch(line, -1) {
				check(relative, number, line, match[1], &findings)
			}
			for _, match := range bareDirectoryToken.FindAllStringSubmatch(line, -1) {
				check(relative, number, line, match[1], &findings)
			}
		}
	}

	// A guard that scans nothing passes. The operational set is listed by
	// hand, so an editing accident that empties it would look like success.
	// A guard that scans nothing passes. The set is discovered rather than
	// listed now, so the way it empties is a bad exclusion rule rather than an
	// editing accident -- which is easier to introduce and just as silent.
	if read < 100 {
		t.Fatalf("read only %d live documents of an expected ~290; the exclusion rules are broken, not the docs", read)
	}
	if len(findings) > 0 {
		sort.Strings(findings)
		t.Fatalf("operational documentation points at %d path(s) this repository does not have:\n  %s",
			len(findings), strings.Join(findings, "\n  "))
	}
}
