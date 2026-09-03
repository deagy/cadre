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
// Scope is the same operationalDocs set the Python guard uses, for the same
// reason. A record of what was true when written -- CHANGELOG.md, a migration
// write-up, an ADR -- names dead paths as its subject matter, and rewriting
// one to satisfy a test falsifies the record.

// referencedPath matches a markdown link target and a backticked path.
//
// Both forms, because the same claim is written both ways: "[`kernel/`](kernel/)"
// and "under `kernel/`" say the same thing and were both wrong.
var (
	markdownLinkTarget = regexp.MustCompile(`\[[^\]]*\]\(([^)\s#]+)\)`)
	backtickedPath     = regexp.MustCompile("`([A-Za-z0-9_.-]+/[A-Za-z0-9_./-]*)`")
	selfRepoTreeURL    = regexp.MustCompile(`https://github\.com/deagy/cadre/(?:tree|blob)/[^/]+/([^)\s#]+)`)
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
	".agents/knowledge-store/config.json": "a store config in the reader's own project, created by them; never present here",
	".agents/cadre.yaml":                  "project-local operator settings in the reader's repository",
	".claude/agents":                      "a runtime override directory in the reader's environment",
	".codex/agents":                       "a runtime override directory in the reader's environment",
	"kernel/contracts":                    "the kernel repository's own layout, at deagy/cadre-kernel",
	"bin/agentic-sdlc":                    "the build-and-exec wrapper in deagy/cadre-kernel's checkout, named here when explaining how that repository works",
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

func TestNoOperationalDocPointsAtAPathThatIsGone(t *testing.T) {
	root := filepath.Dir(filepath.Dir(mustGetwd(t)))
	if _, err := os.Stat(filepath.Join(root, "roster")); err != nil {
		t.Skipf("not running inside a source checkout: %v", err)
	}
	tracked := trackedPaths(t, root)

	paths := append([]string{}, operationalDocs...)
	for _, glob := range operationalDocGlobs {
		matches, err := filepath.Glob(filepath.Join(root, glob))
		if err != nil {
			t.Fatalf("bad glob %q: %v", glob, err)
		}
		for _, match := range matches {
			relative, _ := filepath.Rel(root, match)
			paths = append(paths, filepath.ToSlash(relative))
		}
	}

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
		}
	}

	// A guard that scans nothing passes. The operational set is listed by
	// hand, so an editing accident that empties it would look like success.
	if read < 10 {
		t.Fatalf("read only %d operational documents; the list is broken, not the docs", read)
	}
	if len(findings) > 0 {
		sort.Strings(findings)
		t.Fatalf("operational documentation points at %d path(s) this repository does not have:\n  %s",
			len(findings), strings.Join(findings, "\n  "))
	}
}
