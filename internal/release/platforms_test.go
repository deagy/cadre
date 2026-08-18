package release

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The release-asset naming contract has three sources of truth that must
// agree, none of which imports another:
//
//   - SupportedPlatforms here -- the matrix and the naming helpers.
//   - Makefile's cross-build recipe -- the GOOS/GOARCH legs actually built.
//   - DISTRIBUTION.md -- the artifact list a human or a release author reads.
//
// Nothing but a test keeps them aligned. Ported from
// plugin/tools/test_binary_shim_contract.py.

func repositoryRoot(t *testing.T) string {
	t.Helper()
	working, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Dir(filepath.Dir(working))
}

func readRepoFile(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(repositoryRoot(t), name))
	if err != nil {
		t.Fatalf("cannot read %s: %v", name, err)
	}
	return string(content)
}

// crossBuildRecipe returns the body of the Makefile's cross-build target.
var crossBuildTarget = regexp.MustCompile(`(?m)^cross-build:\n((?:\t.*\n?)+)`)

func crossBuildRecipe(t *testing.T) string {
	t.Helper()
	match := crossBuildTarget.FindStringSubmatch(readRepoFile(t, "Makefile"))
	if match == nil {
		t.Fatal("the Makefile has no cross-build recipe; this guard has nothing to check")
	}
	return match[1]
}

var goosGoarch = regexp.MustCompile(`GOOS=(\S+)\s+GOARCH=(\S+)`)

func TestTheCrossBuildMatrixIsExactlyTheContractedOne(t *testing.T) {
	// Equality in both directions. A missing leg means a platform we promise
	// and never build; an extra leg means one we build and never document --
	// including windows/arm64, whose reappearance is a release-blocking
	// regression rather than a welcome addition, for the reason recorded on
	// SupportedPlatforms.
	built := map[string]bool{}
	for _, match := range goosGoarch.FindAllStringSubmatch(crossBuildRecipe(t), -1) {
		built[match[1]+"/"+match[2]] = true
	}
	contracted := map[string]bool{}
	for _, platform := range SupportedPlatforms {
		contracted[platform.String()] = true
	}
	for platform := range contracted {
		if !built[platform] {
			t.Errorf("cross-build does not build %s, which the contract promises", platform)
		}
	}
	for platform := range built {
		if !contracted[platform] {
			t.Errorf("cross-build builds %s, which is not in SupportedPlatforms. "+
				"Add it there deliberately, or remove the Makefile leg.", platform)
		}
	}
}

func TestEveryCrossBuildLegForcesCgo(t *testing.T) {
	// The knowledge store (github.com/mattn/go-sqlite3) needs cgo. A leg
	// without CGO_ENABLED=1 builds fine and produces a binary whose `cadre
	// knowledge` subcommand is a non-functional stub -- verified against a
	// real build, not inferred.
	for _, line := range strings.Split(crossBuildRecipe(t), "\n") {
		if !strings.Contains(line, "GOOS=") {
			continue
		}
		if !strings.Contains(line, "CGO_ENABLED=1") {
			t.Errorf("cross-build leg does not force cgo, so `cadre knowledge` "+
				"ships as a stub on that platform:\n  %s", strings.TrimSpace(line))
		}
	}
}

func TestEveryPlatformIsDocumentedWithItsOwnExtension(t *testing.T) {
	// Checking that the goos/goarch pair merely appears somewhere would pass
	// a doc that claimed the wrong extension for that platform. So: the right
	// extension must appear near the pair, and the wrong one must not.
	doc := readRepoFile(t, "DISTRIBUTION.md")
	for _, platform := range SupportedPlatforms {
		right := regexp.QuoteMeta(ArchiveExtension(platform.GOOS))
		wrong := regexp.QuoteMeta("tar.gz")
		if ArchiveExtension(platform.GOOS) == "tar.gz" {
			wrong = "zip"
		}
		near := func(extension string, window int) *regexp.Regexp {
			// No (?s): `.` must not cross a newline. Go and Python agree on
			// that by default, and widening it to whole-file made an
			// unrelated .tar.gz two lines below the windows row read as
			// windows being documented as a tarball.
			return regexp.MustCompile(fmt.Sprintf(
				`%s.{0,40}%s.{0,%d}%s|%s.{0,%d}%s.{0,40}%s`,
				platform.GOOS, platform.GOARCH, window, extension,
				extension, window, platform.GOOS, platform.GOARCH))
		}
		if !near(right, 120).MatchString(doc) {
			t.Errorf("DISTRIBUTION.md does not document %s with its extension (%s)",
				platform, ArchiveExtension(platform.GOOS))
		}
		if near(wrong, 40).MatchString(doc) {
			t.Errorf("DISTRIBUTION.md pairs %s with the wrong extension (%s)",
				platform, wrong)
		}
	}
}

func TestTheNamingPatternIsStatedLiterally(t *testing.T) {
	// Pins the exact string, so a doc edit that reflows or paraphrases the
	// contract -- dropping the leading cadre-v, or reordering goos/goarch --
	// fails here instead of drifting away from what the release workflow and
	// the shim actually use.
	const pattern = "cadre-v<version>-<goos>-<goarch>"
	if !strings.Contains(readRepoFile(t, "DISTRIBUTION.md"), pattern) {
		t.Errorf("DISTRIBUTION.md no longer states %q literally", pattern)
	}
}

var documentedArchive = regexp.MustCompile(`cadre-v[\w.\-]+-([a-z]+)-([a-z0-9]+)\.(?:tar\.gz|zip)`)

func TestTheDocClaimsNoUncontractedPlatform(t *testing.T) {
	contracted := map[string]bool{}
	for _, platform := range SupportedPlatforms {
		contracted[platform.String()] = true
	}
	for _, match := range documentedArchive.FindAllStringSubmatch(readRepoFile(t, "DISTRIBUTION.md"), -1) {
		name := match[1] + "/" + match[2]
		if !contracted[name] {
			t.Errorf("DISTRIBUTION.md names %s, which is not in SupportedPlatforms", name)
		}
	}
}

func TestTheNamingHelpersAgreeWithTheMatrix(t *testing.T) {
	// Self-consistency only: this has no teeth against anything outside this
	// package, and only a mistake inside it could fail. Kept because the
	// helpers are what the guards above compare against -- if they disagreed
	// with the matrix, every check above would be measuring the wrong thing.
	for _, platform := range SupportedPlatforms {
		name := ArchiveName(platform.GOOS, platform.GOARCH, "0.0.0")
		if platform.GOOS == "windows" {
			if !strings.HasSuffix(name, ".zip") {
				t.Errorf("%s: archive %q is not a .zip", platform, name)
			}
			if ExecutableName(platform.GOOS) != "cadre.exe" {
				t.Errorf("%s: executable is %q, not cadre.exe", platform, ExecutableName(platform.GOOS))
			}
			continue
		}
		if !strings.HasSuffix(name, ".tar.gz") {
			t.Errorf("%s: archive %q is not a .tar.gz", platform, name)
		}
		if ExecutableName(platform.GOOS) != "cadre" {
			t.Errorf("%s: executable is %q, not cadre", platform, ExecutableName(platform.GOOS))
		}
	}
}

// releaseWorkflow is the fragment of .github/workflows/release.yml this guard
// reads: the cli job's build matrix, which decides what actually gets
// published. Everything else in that file is deliberately not modelled.
type releaseWorkflow struct {
	Jobs struct {
		CLI struct {
			Strategy struct {
				Matrix struct {
					Include []struct {
						GOOS   string `yaml:"goos"`
						GOARCH string `yaml:"goarch"`
					} `yaml:"include"`
				} `yaml:"matrix"`
			} `yaml:"strategy"`
		} `yaml:"cli"`
	} `yaml:"jobs"`
}

func TestTheReleaseMatrixIsExactlyTheContractedOne(t *testing.T) {
	// The fourth source of truth, and the one that decides what users can
	// actually download. It used to be checked by a script inlined in
	// validate.yml, which imported SUPPORTED_PLATFORMS from
	// test_binary_shim_contract.py -- the one thing binary_platforms.py's
	// docstring told CI not to import from, since a test module is not a
	// stable interface for a non-test consumer.
	//
	// Strict in both directions, where the inlined script only warned about
	// an extra platform. An extra leg publishes an asset that DISTRIBUTION.md
	// does not document and the shim will not ask for, which is drift in the
	// same way a missing one is.
	var workflow releaseWorkflow
	if err := yaml.Unmarshal([]byte(readRepoFile(t, ".github/workflows/release.yml")), &workflow); err != nil {
		t.Fatalf("release.yml does not parse: %v", err)
	}
	published := map[string]bool{}
	for _, leg := range workflow.Jobs.CLI.Strategy.Matrix.Include {
		published[leg.GOOS+"/"+leg.GOARCH] = true
	}
	if len(published) == 0 {
		t.Fatal("release.yml's cli job has no build matrix; this guard read nothing")
	}
	contracted := map[string]bool{}
	for _, platform := range SupportedPlatforms {
		contracted[platform.String()] = true
	}
	for platform := range contracted {
		if !published[platform] {
			t.Errorf("release.yml never builds %s, so the contract promises an asset "+
				"no release produces", platform)
		}
	}
	for platform := range published {
		if !contracted[platform] {
			t.Errorf("release.yml publishes %s, which is not in SupportedPlatforms and "+
				"so appears in no documentation the shim or a human reads", platform)
		}
	}
}
