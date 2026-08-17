package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The pip/pipx wheel ships no Python.
//
// pyproject.toml records two failures that already happened, and guards both
// with comments:
//
//   - "No [project.scripts]. ... a console-script entry here would generate a
//     Python launcher into <prefix>/bin/cadre and silently overwrite it,
//     which is exactly what happened the first time this was built."
//   - "hatchling's shared-data maps *files only*, and a directory source
//     produces no files and **no error** -- a wheel that builds successfully
//     and is missing all 159 role definitions."
//
// A comment cannot fail. Both regressions are a one-line edit to a config
// file, which is the kind of change that reads as obviously correct.
//
// The division of labour: CI's `cadre pip/pipx distribution` job builds a real
// wheel and installs it, which is the only way to know what actually ends up
// inside. This covers the declarations that decide what goes in -- cheap,
// no toolchain, and it fails at the edit rather than at the release.
//
// Ported from test_cli_surface.py's WheelShipsNoPythonTest.

// tomlSection returns the lines belonging to [name], excluding the header.
//
// A section-aware text scan rather than a parse: the only TOML libraries here
// are indirect dependencies, and promoting one to build a test is a bigger
// change than these four assertions justify. The helper is small enough to
// test directly, which the last case below does.
func tomlSection(document, name string) (lines []string, present bool) {
	header := "[" + name + "]"
	inSection := false
	for _, line := range strings.Split(document, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if trimmed == header {
				inSection, present = true, true
				continue
			}
			inSection = false
			continue
		}
		if inSection {
			lines = append(lines, trimmed)
		}
	}
	return lines, present
}

func pyprojectDocument(t *testing.T) string {
	t.Helper()
	root := filepath.Dir(filepath.Dir(mustGetwd(t)))
	raw, err := os.ReadFile(filepath.Join(root, "pyproject.toml"))
	if err != nil {
		t.Skipf("not running inside a source checkout: %v", err)
	}
	return string(raw)
}

func TestTheWheelDeclaresNoConsoleScript(t *testing.T) {
	// [project.scripts] generates a Python launcher at <prefix>/bin/<name>.
	// The wheel installs the Go binary to exactly that path, so a console
	// script does not sit alongside it -- it replaces it, and the replacement
	// is a Python stub that imports a package this wheel does not ship.
	document := pyprojectDocument(t)
	if lines, present := tomlSection(document, "project.scripts"); present {
		t.Errorf("pyproject.toml declares [project.scripts]: %v\n"+
			"That generates a Python launcher into <prefix>/bin/cadre and "+
			"overwrites the Go binary the wheel installs there.", lines)
	}
}

func TestTheWheelDeclaresNoPythonPackages(t *testing.T) {
	// `packages` is how hatchling is told which Python to include. Its absence
	// is the whole point, and `bypass-selection` is what tells hatchling that
	// absence is deliberate rather than a missing configuration -- without it
	// the build fails rather than silently shipping nothing, which is the
	// right failure but not the one we want to discover at release time.
	document := pyprojectDocument(t)
	lines, present := tomlSection(document, "tool.hatch.build.targets.wheel")
	if !present {
		t.Fatal("pyproject.toml declares no wheel target")
	}
	sawBypass := false
	for _, line := range lines {
		if strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "packages") {
			t.Errorf("the wheel target declares Python packages: %s", line)
		}
		if strings.HasPrefix(line, "bypass-selection") && strings.Contains(line, "true") {
			sawBypass = true
		}
	}
	if !sawBypass {
		t.Error("the wheel target does not set bypass-selection = true; without it " +
			"hatchling treats a package-less wheel as a misconfiguration")
	}
}

func TestTheWheelVersionComesFromAPlainFile(t *testing.T) {
	// cadre_cli/_version.py was the last Python in this distribution. The Go
	// CLI already parsed it as text without executing it, so the .py extension
	// bought nothing and cost one file against the elimination plan.
	document := pyprojectDocument(t)
	lines, present := tomlSection(document, "tool.hatch.version")
	if !present {
		t.Fatal("pyproject.toml declares no version source")
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "path") {
			if strings.Contains(line, ".py") {
				t.Errorf("the version is read from a Python module: %s", line)
			}
			return
		}
	}
	t.Error("the version source declares no path")
}

func TestNoWheelTargetSectionMentionsAPythonFile(t *testing.T) {
	// The general form. Any `.py` reaching a wheel target -- through
	// force-include, shared-data, artifacts, whatever the next mechanism is --
	// puts Python back into a distribution whose entire claim is that it has
	// none.
	document := pyprojectDocument(t)
	checked := 0
	for _, section := range []string{
		"tool.hatch.build.targets.wheel",
		"tool.hatch.build.targets.wheel.shared-scripts",
		"tool.hatch.build.targets.wheel.shared-data",
		"tool.hatch.build.targets.wheel.force-include",
	} {
		lines, present := tomlSection(document, section)
		if !present {
			continue
		}
		checked++
		for _, line := range lines {
			if strings.HasPrefix(line, "#") || line == "" {
				continue
			}
			if strings.Contains(line, ".py") {
				t.Errorf("[%s] names a Python file: %s", section, line)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no wheel target sections were found; this test would prove nothing")
	}
}

func TestTheSectionScannerReadsWhatItClaimsTo(t *testing.T) {
	// Guards the guard. Every check above passes when a section is absent,
	// which is also what happens if the scanner never finds one -- so the
	// absence checks and the presence checks share a failure mode.
	document := strings.Join([]string{
		"[project]",
		`name = "cadre"`,
		"",
		"[project.scripts]",
		`cadre = "cadre_cli:main"`,
		"",
		"[tool.hatch.build.targets.wheel]",
		"# a comment",
		"bypass-selection = true",
		"", // a trailing blank line inside the last section
	}, "\n")

	if _, present := tomlSection(document, "project.scripts"); !present {
		t.Error("a declared section was reported absent")
	}
	if _, present := tomlSection(document, "project.entry-points"); present {
		t.Error("an undeclared section was reported present")
	}
	wheel, present := tomlSection(document, "tool.hatch.build.targets.wheel")
	if !present {
		t.Fatal("the wheel section was not found")
	}
	// Keys from a *later* section must not leak into an earlier one, which is
	// how a scanner that never resets would report the whole file as one
	// section and make every absence check vacuous.
	if _, present := tomlSection(document, "project"); !present {
		t.Error("the first section was not found")
	}
	projectLines, _ := tomlSection(document, "project")
	for _, line := range projectLines {
		if strings.Contains(line, "bypass-selection") {
			t.Error("a later section's keys leaked into [project]")
		}
	}
	sawBypass := false
	for _, line := range wheel {
		if strings.HasPrefix(line, "bypass-selection") {
			sawBypass = true
		}
	}
	if !sawBypass {
		t.Error("the wheel section did not carry its own key")
	}
}
