package generators

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Whether the thing we ship works on someone else's machine.
//
// `generate-plugin --check` proves the committed distribution is what the
// generator produces here. It says nothing about whether that distribution is
// usable anywhere else -- an absolute path from this checkout, or a relative
// link rewritten to the wrong depth, reproduces identically every time and is
// still broken for every installer.
//
// Ported from test_repository_health.py, whose link test records the failure
// that produced it: "22 links pointing at unpackaged files, plus one silently
// retargeted to suite/roster/README.md instead of suite/README.md, went
// unnoticed exactly this way." A rewrite that lands on the wrong depth
// produces a link that still resolves *in the source tree*, so inspection does
// not catch it.

// freshPackage generates a distribution into a temporary directory.
//
// Generated rather than read from plugin/, because the property is about paths
// belonging to *this* checkout leaking into the output, and the committed copy
// was produced on whichever machine last regenerated it.
func freshPackage(t *testing.T) (packageRoot, repoRoot string) {
	t.Helper()
	repoRoot = repositoryRoot(t)
	if _, err := os.Stat(filepath.Join(repoRoot, "roster", "catalog.yaml")); err != nil {
		t.Skipf("not running inside a source checkout: %v", err)
	}
	packageRoot = filepath.Join(t.TempDir(), "plugin")
	if _, err := RunGeneratePlugin(repoRoot, packageRoot, GeneratePluginOptions{}); err != nil {
		t.Fatalf("generating a distribution: %v", err)
	}
	return packageRoot, repoRoot
}

// packagedFiles walks the distribution, skipping compiled Python leftovers,
// which are bytes rather than text and carry no links.
func packagedFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == "__pycache__" {
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(path) {
		case ".pyc", ".pyo":
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walking the distribution: %v", err)
	}
	if len(files) < 100 {
		t.Fatalf("walked %d files; the distribution did not generate", len(files))
	}
	return files
}

func TestNoFileInTheDistributionNamesThisCheckout(t *testing.T) {
	// The generator runs inside a source checkout. Any output that embeds its
	// path ships a reference to a directory that exists on exactly one
	// machine, and the failure surfaces at the installer as a missing file
	// rather than as anything pointing back here.
	packageRoot, repoRoot := freshPackage(t)

	var offenders []string
	for _, path := range packagedFiles(t, packageRoot) {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		if strings.Contains(string(content), repoRoot) {
			relative, _ := filepath.Rel(packageRoot, path)
			offenders = append(offenders, relative)
		}
	}
	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Errorf("%d packaged file(s) contain this checkout's absolute path (%s):\n  %s",
			len(offenders), repoRoot, strings.Join(offenders, "\n  "))
	}
}

// markdownLink matches any inline link target. Go's regexp is RE2, which has
// no negative lookahead, so "not a URL, not an anchor, not a mail address" is
// a filter in relativeLinkTargets rather than a pattern -- which is the better
// place for it anyway, since it can then be tested directly.
var markdownLink = regexp.MustCompile(`\]\(<?([^)#>]+)`)

// relativeLinkTargets returns the link targets in text that point at a file
// alongside it, dropping absolute URLs, in-page anchors and mail addresses.
func relativeLinkTargets(text string) []string {
	var targets []string
	for _, match := range markdownLink.FindAllStringSubmatch(text, -1) {
		target := strings.TrimSpace(match[1])
		switch {
		case target == "",
			strings.HasPrefix(target, "#"),
			strings.HasPrefix(target, "http://"),
			strings.HasPrefix(target, "https://"),
			strings.HasPrefix(target, "mailto:"),
			// A bare scheme-relative or protocol-qualified target is not ours
			// to resolve either.
			strings.Contains(target, "://"),
			strings.HasPrefix(target, "//"):
			continue
		}
		targets = append(targets, target)
	}
	return targets
}

func TestEveryRelativeLinkInTheDistributionResolves(t *testing.T) {
	packageRoot, _ := freshPackage(t)

	checked := 0
	var broken []string
	for _, path := range packagedFiles(t, packageRoot) {
		if !strings.HasSuffix(path, ".md") {
			continue
		}
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		relativeDocument, _ := filepath.Rel(packageRoot, path)
		for _, target := range relativeLinkTargets(string(content)) {
			checked++
			resolved := filepath.Join(filepath.Dir(path), filepath.FromSlash(target))
			if _, err := os.Stat(resolved); err != nil {
				broken = append(broken, relativeDocument+" -> "+target)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no relative links were found; this test would prove nothing")
	}
	sort.Strings(broken)
	if len(broken) > 0 {
		t.Errorf("%d of %d relative link(s) do not resolve inside the "+
			"distribution:\n  %s\n\n"+
			"A link rewritten to the wrong depth still resolves in the source "+
			"tree, so this is not visible by reading the diff.",
			len(broken), checked, strings.Join(broken, "\n  "))
	}
	t.Logf("checked %d relative links across the distribution", checked)
}

func TestNoPackagedDocumentEscapesTheDistributionWithDotDot(t *testing.T) {
	// A `../` that resolves outside the package root reaches for the source
	// tree it was generated from. It may even resolve here, which is the
	// problem: it cannot resolve anywhere else.
	packageRoot, _ := freshPackage(t)
	dotDot := regexp.MustCompile(`(?:^|[\s(\[])(\.\./[^\s` + "`" + `)'"\]]+)`)

	checked := 0
	var escaping []string
	for _, path := range packagedFiles(t, packageRoot) {
		if !strings.HasSuffix(path, ".md") {
			continue
		}
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		relativeDocument, _ := filepath.Rel(packageRoot, path)
		for _, match := range dotDot.FindAllStringSubmatch(string(content), -1) {
			target := strings.TrimRight(match[1], ".,;:")
			checked++
			resolved, err := filepath.Abs(filepath.Join(filepath.Dir(path),
				filepath.FromSlash(target)))
			if err != nil {
				continue
			}
			inside, err := filepath.Rel(packageRoot, resolved)
			if err != nil || inside == ".." ||
				strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
				escaping = append(escaping, relativeDocument+" -> "+target)
			}
		}
	}
	sort.Strings(escaping)
	if len(escaping) > 0 {
		t.Errorf("%d of %d `../` path(s) point outside the distribution:\n  %s\n\n"+
			"These resolve in the source checkout and nowhere else.",
			len(escaping), checked, strings.Join(escaping, "\n  "))
	}
	t.Logf("checked %d `../` paths", checked)
}

func TestTheDistributionCarriesItsOwnProviderAndCatalog(t *testing.T) {
	// The two files that make it a package rather than a directory of
	// markdown. Without the catalog nothing can be selected; without
	// provider.json nothing identifies what was installed.
	packageRoot, _ := freshPackage(t)

	raw, err := os.ReadFile(filepath.Join(packageRoot, "provider.json"))
	if err != nil {
		t.Fatalf("the distribution has no provider.json: %v", err)
	}
	var provider struct {
		ID      string `json:"id"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &provider); err != nil {
		t.Fatalf("provider.json does not parse: %v", err)
	}
	if provider.ID == "" {
		t.Error("provider.json declares no id")
	}
	if provider.Version == "" {
		t.Error("provider.json declares no version")
	}
	// The version is deliberately not pinned to a literal here. The Python
	// asserted "0.3.0", which turns every release into a test edit and says
	// nothing about the package being usable.
	if _, err := os.Stat(filepath.Join(packageRoot, "suite", "roster", "catalog.yaml")); err != nil {
		t.Errorf("the distribution ships no catalog: %v", err)
	}
}

func TestTheLinkScanWouldNoticeABrokenLink(t *testing.T) {
	// Guards the guard. Both scans above pass over a clean distribution, which
	// is also what they do if their patterns match nothing.
	for _, testCase := range []struct {
		text string
		want string
	}{
		{"see [the runbook](roster/RUNBOOK.md) for details", "roster/RUNBOOK.md"},
		{"see [it](<spaced name.md>)", "spaced name.md"},
		{"nested [link](../suite/roster/README.md) here", "../suite/roster/README.md"},
	} {
		got := relativeLinkTargets(testCase.text)
		if len(got) != 1 || got[0] != testCase.want {
			t.Errorf("relativeLinkTargets(%q) = %v, want [%q]",
				testCase.text, got, testCase.want)
		}
	}
	for _, ignored := range []string{
		"see [the site](https://example.com/page)",
		"jump to [the section](#heading)",
		"mail [us](mailto:someone@example.com)",
		"a [protocol](ftp://host/file) link",
	} {
		if got := relativeLinkTargets(ignored); len(got) != 0 {
			t.Errorf("relativeLinkTargets(%q) = %v, want none", ignored, got)
		}
	}
}
