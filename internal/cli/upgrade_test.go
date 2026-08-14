package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/deagy/cadre/cli/internal/orchestration"
)

// TestUpgradeRoutesOnInstallKind is the point of this command's rework.
//
// It previously probed for a pyproject.toml, then shelled out to `pipx list`,
// then defaulted to "pip" -- so a checkout whose probe missed was told to run
// `pip install --upgrade cadre`, installing a wheel over the CLI it was
// already running from a git tree. Each install kind now gets the one
// instruction that is correct for it.
func TestUpgradeRoutesOnInstallKind(t *testing.T) {
	cases := []struct {
		kind       string
		root       string
		wantAll    []string
		wantAbsent []string
	}{
		{
			kind:       orchestration.InstallKindCheckout,
			root:       "/src/cadre",
			wantAll:    []string{"git -C /src/cadre pull --ff-only", "RUNBOOK.md section 17"},
			wantAbsent: []string{"pip install", "pipx", "go install"},
		},
		{
			kind:       orchestration.InstallKindGoInstall,
			root:       "/home/u/go/bin",
			wantAll:    []string{"go install github.com/deagy/cadre/cmd/cadre@cli-v9.9.9"},
			wantAbsent: []string{"pip install", "pipx", "git pull"},
		},
		{
			kind:       orchestration.InstallKindPluginCache,
			root:       "/home/u/.claude/plugins/cache/deagy/cadre-lifecycle/v1",
			wantAll:    []string{"/plugin marketplace update cadre-team"},
			wantAbsent: []string{"pip install", "pipx", "git pull", "go install"},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.kind, func(t *testing.T) {
			var code int
			output := captureStdout(t, func() {
				code = updateCadre(testCase.kind, testCase.root, "cli-v9.9.9")
			})
			if code != 0 {
				t.Errorf("exit code = %d, want 0", code)
			}
			for _, want := range testCase.wantAll {
				if !strings.Contains(output, want) {
					t.Errorf("output missing %q:\n%s", want, output)
				}
			}
			for _, absent := range testCase.wantAbsent {
				if strings.Contains(output, absent) {
					t.Errorf("output should not mention %q for a %s install:\n%s",
						absent, testCase.kind, output)
				}
			}
		})
	}
}

// TestUpgradeNeverRunsAPackageManagerForANonWheelInstall is the property that
// matters most, stated separately from the message assertions above: for a
// checkout, a go-install binary or a plugin cache, this command must print
// and change nothing. `pip install --upgrade cadre` against a checkout was
// the concrete defect.
func TestUpgradeNeverRunsAPackageManagerForANonWheelInstall(t *testing.T) {
	// A PATH with nothing on it: if updateCadre tried to exec pip or pipx for
	// any of these kinds, the attempt would fail and surface as a non-zero
	// exit rather than a clean instruction.
	t.Setenv("PATH", t.TempDir())

	for _, kind := range []string{
		orchestration.InstallKindCheckout,
		orchestration.InstallKindGoInstall,
		orchestration.InstallKindPluginCache,
	} {
		t.Run(kind, func(t *testing.T) {
			var code int
			_ = captureStdout(t, func() {
				code = updateCadre(kind, "/somewhere", "cli-v9.9.9")
			})
			if code != 0 {
				t.Errorf("%s install returned %d; it must print an instruction and "+
					"succeed without invoking any package manager", kind, code)
			}
		})
	}
}

// TestCLIReleaseTagPrefixMatchesTheReleaseWorkflow guards the one string that
// ties this command to what actually gets published. release.yml's
// cli-publish job tags `cli-v<version>`; if that namespace ever moves, the
// upgrade check silently finds no release rather than failing loudly.
func TestCLIReleaseTagPrefixMatchesTheReleaseWorkflow(t *testing.T) {
	workflow, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Skipf("release workflow not readable: %v", err)
	}
	if !strings.Contains(string(workflow), "refs/tags/"+cliReleaseTagPrefix) {
		t.Errorf("release.yml does not tag %s*; upgrade would find no release",
			cliReleaseTagPrefix)
	}
}
