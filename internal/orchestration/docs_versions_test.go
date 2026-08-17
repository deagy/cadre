package orchestration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Hand-maintained version coordinates rot.
//
// The install instructions used to pin a marketplace ref to a tag and clone at
// one. Nothing checked them, so they drifted apart: this repository's README
// and RUNBOOK quoted one version, the packaging template quoted another, and
// the actual release was a third. A user who copied a stale tag got a plugin
// whose provider.json declared a kernel-compatibility window ten minor
// versions behind the kernel they had.
//
// The fix was to stop writing the coordinate down -- `/plugin install`
// resolves the version from the plugin's own manifest, so the marketplace ref
// needs no tag at all. This keeps it that way.
//
// The packaging template matters most: the generator renders it into the
// downstream distribution's README, so a stale tag written there propagates on
// every regeneration.
//
// Ported from roster/orchestration/test/test_docs_versions.py.

var (
	pinnedMarketplaceRef = regexp.MustCompile(`marketplace add\s+\S*cadre-lifecycle@v[\d.]+`)
	pinnedCloneRef       = regexp.MustCompile(`clone\s+--branch\s+v[\d.]+`)
	// Both spellings seen in practice: plain prose, and a backticked package
	// reference followed by a release link.
	kernelVersionProse = regexp.MustCompile(`(?:Agentic SDLC\s+v|` + "`agentic-sdlc`" + `\s*\[v)(\d+\.\d+)`)
)

const (
	versionHistoryOpen  = "<!-- version-history -->"
	versionHistoryClose = "<!-- /version-history -->"
)

// stripVersionHistory blanks out opted-out regions while preserving line
// numbering, so a finding still points at the right line.
//
// CHANGELOG-style files legitimately record the tags that shipped; the marker
// is how a document says which of its lines are history rather than
// instruction.
func stripVersionHistory(text string) string {
	lines := strings.Split(text, "\n")
	keeping := true
	for index, line := range lines {
		if strings.Contains(line, versionHistoryOpen) {
			keeping = false
		}
		if !keeping {
			lines[index] = ""
		}
		if strings.Contains(line, versionHistoryClose) {
			keeping = true
		}
	}
	return strings.Join(lines, "\n")
}

// trackedMarkdown lists the documentation this repository hand-authors.
//
// Asked of git rather than walked: a filesystem walk finds every stale
// worktree under .worktrees/ and .claude/worktrees/, each a full checkout of
// an older commit whose docs legitimately quote the tags of their day.
func trackedMarkdown(t *testing.T) (root string, paths []string) {
	t.Helper()
	root = checkoutRoot(t)
	command := exec.Command("git", "ls-files", "*.md")
	command.Dir = root
	out, err := command.Output()
	if err != nil {
		t.Skipf("cannot list tracked files: %v", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		relative := strings.TrimSpace(line)
		if relative == "" || filepath.Base(relative) == "CHANGELOG.md" {
			continue
		}
		paths = append(paths, relative)
	}
	if len(paths) < 50 {
		t.Fatalf("listed %d markdown files; the listing is broken, not the tree",
			len(paths))
	}
	return root, paths
}

func scanMarkdown(t *testing.T, pattern *regexp.Regexp) []string {
	t.Helper()
	root, paths := trackedMarkdown(t)
	var findings []string
	for _, relative := range paths {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			continue
		}
		for number, line := range strings.Split(stripVersionHistory(string(raw)), "\n") {
			if pattern.MatchString(line) {
				findings = append(findings,
					relative+":"+itoaVersion(number+1)+": "+strings.TrimSpace(line))
			}
		}
	}
	sort.Strings(findings)
	return findings
}

func itoaVersion(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func TestNoDocumentPinsTheMarketplaceRefToATag(t *testing.T) {
	if findings := scanMarkdown(t, pinnedMarketplaceRef); len(findings) > 0 {
		t.Errorf("%d document(s) pin the marketplace ref to a tag:\n  %s\n\n"+
			"The installed version comes from the plugin's own manifest, so a "+
			"written-down tag only goes stale. Drop the @vX.Y.Z.",
			len(findings), strings.Join(findings, "\n  "))
	}
}

func TestNoDocumentClonesAtAHardcodedTag(t *testing.T) {
	if findings := scanMarkdown(t, pinnedCloneRef); len(findings) > 0 {
		t.Errorf("%d document(s) clone at a hardcoded tag:\n  %s\n\n"+
			"Use a plain `git clone`.", len(findings), strings.Join(findings, "\n  "))
	}
}

func TestQuotedKernelVersionsAgreeWithTheProviderManifest(t *testing.T) {
	// provider.json carries two unrelated version lines: its own `version`
	// (the provider-manifest version) and `kernel_compatibility` (the kernel
	// range). Every install message here used to quote the former while
	// meaning the latter, which is the exact bug this guards.
	root, paths := trackedMarkdown(t)
	raw, err := os.ReadFile(filepath.Join(root, "provider", "provider.json"))
	if err != nil {
		t.Skipf("no provider manifest here: %v", err)
	}
	var manifest struct {
		KernelCompatibility struct {
			Minimum string `json:"minimum"`
		} `json:"kernel_compatibility"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("provider.json does not parse: %v", err)
	}
	minimum := manifest.KernelCompatibility.Minimum
	if minimum == "" {
		t.Fatal("provider.json declares no kernel_compatibility.minimum")
	}
	parts := strings.Split(minimum, ".")
	if len(parts) < 2 {
		t.Fatalf("kernel_compatibility.minimum %q is not a version", minimum)
	}
	// The series, not the patch: prose reasonably says "v0.13" or "v0.13.3"
	// for a 0.13.2 minimum, and pinning the patch would make every kernel
	// patch release a documentation edit.
	series := parts[0] + "." + parts[1]

	checked := 0
	var mismatches []string
	for _, relative := range paths {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			continue
		}
		for number, line := range strings.Split(stripVersionHistory(string(content)), "\n") {
			for _, match := range kernelVersionProse.FindAllStringSubmatch(line, -1) {
				checked++
				if match[1] != series {
					mismatches = append(mismatches,
						relative+":"+itoaVersion(number+1)+": quotes v"+match[1]+
							", expected v"+series)
				}
			}
		}
	}
	if checked == 0 {
		t.Skip("no document quotes a kernel version")
	}
	sort.Strings(mismatches)
	if len(mismatches) > 0 {
		t.Errorf("%d of %d quoted kernel version(s) disagree with "+
			"provider.json's kernel_compatibility.minimum (%s):\n  %s\n\n"+
			"That manifest's own `version` field is a different version line; "+
			"quoting it here is the bug this guards.",
			len(mismatches), checked, minimum, strings.Join(mismatches, "\n  "))
	}
	t.Logf("checked %d quoted kernel version(s) against v%s", checked, series)
}

func TestTheVersionHistoryOptOutBlanksOnlyItsOwnBlock(t *testing.T) {
	// Guards the guard. Every scan above passes when a pattern matches
	// nothing, which is also what happens if the opt-out swallowed the whole
	// document -- and one file does use the marker, so this is not
	// hypothetical.
	document := strings.Join([]string{
		"before the block",
		versionHistoryOpen,
		"inside, ignored",
		versionHistoryClose,
		"after the block",
	}, "\n")
	stripped := strings.Split(stripVersionHistory(document), "\n")

	if len(stripped) != 5 {
		t.Fatalf("stripping changed the line count: %d, want 5 -- a finding's "+
			"line number would point at the wrong line", len(stripped))
	}
	if stripped[0] != "before the block" {
		t.Errorf("content before the block was blanked: %q", stripped[0])
	}
	if stripped[2] != "" {
		t.Errorf("content inside the block survived: %q", stripped[2])
	}
	if stripped[4] != "after the block" {
		t.Errorf("content after the block was blanked: %q", stripped[4])
	}
}

func TestTheScansWouldNoticeAPinnedCoordinate(t *testing.T) {
	// The patterns, over lines that must match and lines that must not. A
	// pattern that stopped matching would leave three tests passing over a
	// repository full of stale tags.
	for _, testCase := range []struct {
		pattern *regexp.Regexp
		matches []string
		ignores []string
	}{
		{pinnedMarketplaceRef,
			[]string{
				"/plugin marketplace add deagy/cadre-lifecycle@v0.7.0",
				"run `/plugin marketplace add cadre-lifecycle@v1.2.3` first",
			},
			[]string{
				"/plugin marketplace add deagy/cadre",
				"marketplace add deagy/cadre-lifecycle",
			}},
		{pinnedCloneRef,
			[]string{"git clone --branch v0.7.0 https://example.com/x.git"},
			[]string{"git clone https://example.com/x.git", "git clone --depth 1 x"}},
		{kernelVersionProse,
			[]string{
				"requires Agentic SDLC v0.13 or newer",
				"see `agentic-sdlc` [v0.13.2](https://example.com)",
			},
			[]string{"requires the Agentic SDLC kernel", "version 0.13 of something else"}},
	} {
		for _, line := range testCase.matches {
			if !testCase.pattern.MatchString(line) {
				t.Errorf("%v did not match: %s", testCase.pattern, line)
			}
		}
		for _, line := range testCase.ignores {
			if testCase.pattern.MatchString(line) {
				t.Errorf("%v wrongly matched: %s", testCase.pattern, line)
			}
		}
	}
}
