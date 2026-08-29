package release

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The release workflow must archive the executable name this package promises.
//
// ExecutableNameFor says an archive contains "cadre" (or "cadre.exe"), and
// DISTRIBUTION.md tells people to extract and run it. The workflow packed the
// *build* output instead -- "cadre-linux-arm64", carrying the platform suffix
// that lets four matrix legs coexist -- so `tar xzf` produced a binary that
// name could not find. It shipped that way through cli-v0.6.3.
//
// Nothing caught it because nothing compared the two. The contract lived in Go,
// the packing lived in YAML, and the only check on either was that the archive
// *file* was named correctly, which it always was. The kernel job never had the
// bug: it builds straight to the contract name, so being right about one
// program said nothing about the two beside it.
//
// This reads the workflow as text for the same reason platforms_test.go reads
// the Makefile: a comment cannot fail, and neither can a contract nobody
// checks against the thing it describes.
func TestReleaseWorkflowArchivesTheContractExecutableName(t *testing.T) {
	workflow := readWorkflow(t)

	for _, program := range []string{ProgramCLI, ProgramEngine} {
		unix := ExecutableNameFor(program, "linux")
		windows := ExecutableNameFor(program, "windows")

		// tar.gz leg: the archived path must be the bare contract name.
		tarPattern := regexp.MustCompile(`tar czf "` + regexp.QuoteMeta(program) +
			`-v\$\{\{ steps\.version\.outputs\.value \}\}-\$\{\{ matrix\.goos \}\}-\$\{\{ matrix\.goarch \}\}\.tar\.gz" (\S+)`)
		match := tarPattern.FindStringSubmatch(workflow)
		if match == nil {
			t.Errorf("%s: no tar czf line found in the release workflow", program)
			continue
		}
		archived := strings.Trim(match[1], `"`)
		if archived != unix {
			t.Errorf("%s: workflow archives %q, but ExecutableNameFor says the archive contains %q -- "+
				"extracting it gives a binary the documented name cannot find", program, archived, unix)
		}

		// zip leg: the compressed path must end in the contract name.
		if !strings.Contains(workflow, `Compress-Archive -Path "dist\`+windows+`"`) {
			t.Errorf("%s: the Windows leg does not compress dist\\%s; "+
				"ExecutableNameFor says the archive contains %s", program, windows, windows)
		}
	}
}

func readWorkflow(t *testing.T) string {
	t.Helper()
	// internal/release -> repo root
	path := filepath.Join("..", "..", ".github", "workflows", "release.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read the release workflow: %v", err)
	}
	return string(data)
}

// Whatever unpacks an archive must expect the name that was packed into it.
//
// The archive step and the wheel builder live in the same file and disagreed:
// the archives were fixed to contain `cadre`, and the wheel step went on
// extracting them and looking for `cadre-linux-amd64`, so the release failed at
// "no binary extracted for linux/amd64" -- after every build leg had passed.
//
// Guarding the producer was not enough. A contract needs both ends checked, or
// fixing one end simply moves the breakage downstream.
func TestWheelBuilderExpectsTheContractExecutableName(t *testing.T) {
	workflow := readWorkflow(t)

	// The wheel step composes the path with a $suffix variable holding ".exe"
	// on Windows, so the contract name appears without an extension here.
	want := `binary="wheel-binaries/` + ProgramCLI + `$suffix"`
	if !strings.Contains(workflow, want) {
		t.Errorf("the wheel builder does not look for %s; it must expect the name the archive step packs "+
			"(ExecutableNameFor: %q), or every release fails after the binaries are already built",
			want, ExecutableNameFor(ProgramCLI, "linux"))
	}
}
