package release

import (
	"fmt"
	"os"
	"os/exec"
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

func TestEveryCliCrossBuildLegForcesCgo(t *testing.T) {
	// The knowledge store (github.com/mattn/go-sqlite3) needs cgo. A leg
	// without CGO_ENABLED=1 builds fine and produces a binary whose `cadre
	// knowledge` subcommand is a non-functional stub -- verified against a
	// real build, not inferred.
	//
	// Scoped to the CLI rather than every leg. That reason is about the
	// knowledge store, which only the CLI links; requiring cgo on the kernel's
	// legs too would make it unbuildable without a cross toolchain for a
	// dependency it does not have. TestTheKernelBuildsWithoutCgo holds the
	// other half, so neither program can drift into the wrong setting.
	for _, line := range strings.Split(crossBuildRecipe(t), "\n") {
		if !strings.Contains(line, "./cmd/"+ProgramCLI) {
			continue
		}
		if !strings.Contains(line, "CGO_ENABLED=1") {
			t.Errorf("a CLI cross-build leg does not force cgo, so `cadre knowledge` "+
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
		name := ArchiveName(ProgramCLI, platform.GOOS, platform.GOARCH, "0.0.0")
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

// crossBuildLeg matches one build line: the program it builds and for what.
var crossBuildLeg = regexp.MustCompile(
	`GOOS=(\S+)\s+GOARCH=(\S+)\s+go build[^\n]*\./cmd/(\S+)`)

func TestCrossBuildCoversEveryPublishedProgramOnEveryPlatform(t *testing.T) {
	// Programs names what a release publishes; cross-build is what produces
	// it. A program in one and not the other is an asset the release promises
	// and never builds, or builds and never promises.
	//
	// The kernel was absent here for a long time on the reasoning that it
	// "implements one subcommand so far". That comment named its own expiry
	// and outlived it, and the consequence was not cosmetic: with no published
	// Go kernel, an installed lifecycle plugin falls through to bootstrapping
	// the Python kernel this repository deleted.
	built := map[string]bool{}
	for _, match := range crossBuildLeg.FindAllStringSubmatch(crossBuildRecipe(t), -1) {
		built[match[3]+" "+match[1]+"/"+match[2]] = true
	}
	if len(built) == 0 {
		t.Fatal("no build legs parsed out of cross-build; this guard checked nothing")
	}
	for _, program := range Programs {
		for _, platform := range SupportedPlatforms {
			key := program + " " + platform.String()
			if !built[key] {
				t.Errorf("cross-build never builds %s for %s, so a release cannot "+
					"publish it there", program, platform)
			}
		}
	}
	for key := range built {
		program := strings.Fields(key)[0]
		known := false
		for _, candidate := range Programs {
			if candidate == program {
				known = true
			}
		}
		if !known {
			t.Errorf("cross-build builds ./cmd/%s, which is not in Programs -- so it "+
				"is built and never published, or published under a name nothing "+
				"records", program)
		}
	}
}

func TestTheKernelBuildsWithoutCgo(t *testing.T) {
	// The CLI legs force CGO_ENABLED=1 because the knowledge store needs it.
	// The kernel does not, and forcing it there would make the kernel
	// unbuildable on any machine without a cross toolchain -- including the
	// one that would otherwise be able to produce every kernel asset locally.
	for _, line := range strings.Split(crossBuildRecipe(t), "\n") {
		if !strings.Contains(line, "./cmd/"+ProgramKernel) {
			continue
		}
		if !strings.Contains(line, "CGO_ENABLED=0") {
			t.Errorf("a kernel cross-build leg does not set CGO_ENABLED=0:\n  %s",
				strings.TrimSpace(line))
		}
	}
}

// The release workflow has to publish what the contract says it publishes,
// and its wiring has to resolve.
//
// Both halves below were written after finding the bugs by hand. The CLI's
// publish job read its version from cadre_cli/_version.py, a marker deleted
// when the last Python left the distribution -- under `set -eu` that aborts,
// so every CLI publish failed at that step. And adding a job gated on
// `needs.changed.outputs.kernel` while the `changed` job declared only
// `plugin` and `cli` produced a job that silently never runs.
//
// Neither is visible outside a release, which is the worst place to find out.

type releaseWorkflowShape struct {
	Jobs map[string]struct {
		If      string `yaml:"if"`
		Outputs map[string]string
		Steps   []struct {
			Name string `yaml:"name"`
			Run  string `yaml:"run"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

func releaseWorkflowText(t *testing.T) (string, releaseWorkflowShape) {
	t.Helper()
	text := readRepoFile(t, ".github/workflows/release.yml")
	var shape releaseWorkflowShape
	if err := yaml.Unmarshal([]byte(text), &shape); err != nil {
		t.Fatalf("release.yml does not parse: %v", err)
	}
	if len(shape.Jobs) == 0 {
		t.Fatal("release.yml declares no jobs; this guard read nothing")
	}
	return text, shape
}

func TestEveryPublishedProgramHasAReleaseJobThatTagsIt(t *testing.T) {
	text, _ := releaseWorkflowText(t)
	for _, program := range Programs {
		prefix, recorded := TagPrefix[program]
		if !recorded {
			t.Errorf("%s is published but has no tag prefix recorded", program)
			continue
		}
		// The tag is what makes a release findable at all; an asset published
		// under no tag is one nobody can ask for by version.
		if !strings.Contains(text, `git tag -s "`+prefix) && !strings.Contains(text, `git tag -a "`+prefix) {
			t.Errorf("release.yml never tags %s under %s, so %s is built and never "+
				"published under a version anyone can name", program, prefix, program)
		}
		if !strings.Contains(text, "./cmd/"+program) {
			t.Errorf("release.yml never builds ./cmd/%s", program)
		}
	}
}

var changedOutputReference = regexp.MustCompile(`needs\.changed\.outputs\.(\w+)`)

func TestEveryChangedOutputAJobGatesOnIsDeclared(t *testing.T) {
	text, shape := releaseWorkflowText(t)
	declared := shape.Jobs["changed"].Outputs
	if len(declared) == 0 {
		t.Fatal("the changed job declares no outputs; every gated job would be dead")
	}
	seen := 0
	for _, match := range changedOutputReference.FindAllStringSubmatch(text, -1) {
		name := match[1]
		if _, ok := declared[name]; ok {
			seen++
			continue
		}
		// A gate on an undeclared output evaluates empty, so the job never
		// runs and nothing says so.
		t.Errorf("a job gates on needs.changed.outputs.%s, which the changed job "+
			"does not declare -- that job silently never runs", name)
	}
	if seen == 0 {
		t.Fatal("no job gates on a changed output; this guard checked nothing")
	}
}

var versionReadPath = regexp.MustCompile(`(?m)value=\$\((?:tr[^<]*<\s*|grep\s+\S+\s+)([^\s|)]+)`)

func TestEveryReleaseVersionReadNamesAPathThatExists(t *testing.T) {
	// The specific failure: a publish job reading its version out of a file
	// that had been deleted. `set -eu` turns that into a failed release rather
	// than a wrong tag, but either way it is found at release time.
	text, _ := releaseWorkflowText(t)
	root := repositoryRoot(t)
	checked := 0
	for _, match := range versionReadPath.FindAllStringSubmatch(text, -1) {
		candidate := strings.Trim(match[1], `"'`)
		if strings.HasPrefix(candidate, "$") || strings.Contains(candidate, "{{") {
			continue // resolved at run time, not a path this can check
		}
		checked++
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(candidate))); err != nil {
			t.Errorf("a release step reads a version from %s, which does not exist: %v",
				candidate, err)
		}
	}
	if checked == 0 {
		t.Fatal("no version read was found in release.yml; this guard checked nothing")
	}
	t.Logf("checked %d version-read path(s)", checked)
}

// A program's current version must not be claimed by a tag that predates the
// program.
//
// The publish job skips when its tag exists. That is correct and is what stops
// a re-run re-releasing -- after a real release the current version IS tagged,
// which is why this cannot simply forbid an existing tag. An earlier version
// of this guard did, and started failing the moment the kernel's first real
// release was published.
//
// The failure it exists for is narrower and was live: every kernel tag in the
// 0.13 range was cut from the Python kernel that #317 deleted, so the Go kernel
// declaring 0.13.2 meant its publish job would skip forever, against a tag
// naming a different implementation's bits. Tag existence cannot tell those
// apart; the tag's own tree can. A tag cut before the program existed does not
// contain the program.
func TestNoPublishedProgramsVersionIsClaimedByATagThatPredatesIt(t *testing.T) {
	root := repositoryRoot(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	checked := 0
	for _, program := range Programs {
		version, ok := currentVersionOf(t, root, program)
		if !ok {
			continue
		}
		checked++
		tag := TagPrefix[program] + version
		exists := exec.Command("git", "rev-parse", "--verify", "--quiet", "refs/tags/"+tag)
		exists.Dir = root
		if err := exists.Run(); err != nil {
			continue // not released yet, which is the ordinary pre-release state
		}
		// The tag exists. It is only a problem if it was cut from a tree that
		// did not contain this program -- then it can never be re-cut, and it
		// describes something else.
		contains := exec.Command("git", "cat-file", "-e", tag+":cmd/"+program+"/main.go")
		contains.Dir = root
		if err := contains.Run(); err != nil {
			t.Errorf("%s declares version %s, and %s already exists as a tag cut from a "+
				"tree with no cmd/%s in it -- a different implementation's release. "+
				"The publish job skips an existing tag, so this version can never be "+
				"published. Bump past the occupied range.",
				program, version, tag, program)
		}
	}
	if checked == 0 {
		t.Fatal("no program's version could be read; this guard checked nothing")
	}
	t.Logf("checked %d program version(s) against existing tags", checked)
}

// currentVersionOf reads a program's declared version the same way the release
// workflow does, so this guard and the release agree on what would be published.
func currentVersionOf(t *testing.T, root, program string) (string, bool) {
	t.Helper()
	switch program {
	case ProgramCLI:
		contents, err := os.ReadFile(filepath.Join(root, "VERSION"))
		if err != nil {
			return "", false
		}
		return strings.TrimSpace(string(contents)), true
	case ProgramKernel:
		// Asked of the binary, matching the workflow. Building it is slower
		// than parsing the constant and is the point: a guard that read the
		// source could pass while the binary reported something else.
		command := exec.Command("go", "run", "./cmd/agentic-sdlc", "--version")
		command.Dir = root
		out, err := command.Output()
		if err != nil {
			t.Logf("cannot ask the kernel its version: %v", err)
			return "", false
		}
		return strings.TrimSpace(string(out)), true
	}
	t.Errorf("no way to read %s's version; add one when adding a program", program)
	return "", false
}
