package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A workflow job that runs Go must pin the toolchain it runs.
//
// GitHub's runner images ship a Go, so a job that omits setup-go still works
// -- it just silently uses whatever version the image happens to carry rather
// than the one go.mod names. The build that verifies a release is then not the
// build the repository specifies, and nothing says so.
//
// release.yml is where this matters most and is hardest to notice: it only
// executes on a release, so a job that drifted is discovered at the moment
// someone needs a release to work. Three of its jobs gained `go run` calls
// when the inline Python left, and one of them was missed.
//
// The rule is about pinning, not availability. A job that never runs Go needs
// no toolchain and is not required to declare one.

var (
	// Matched on the verb, not the bare word: "go" appears in prose, and a
	// step named "Go to the release page" is not a toolchain user.
	runsGo = regexp.MustCompile(`\bgo\s+(run|build|test|vet|tool|install|generate|mod)\b`)
	// bin/cadre compiles and execs cmd/cadre, so it needs a toolchain too.
	runsCadreShim = regexp.MustCompile(`(^|[^\w./])\.?/?bin/cadre\b`)
)

type workflowFile struct {
	Jobs map[string]struct {
		Steps []struct {
			Name string `yaml:"name"`
			Uses string `yaml:"uses"`
			Run  string `yaml:"run"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

func TestEveryWorkflowJobThatRunsGoPinsItsToolchain(t *testing.T) {
	root := filepath.Dir(filepath.Dir(mustGetwd(t)))
	workflows, err := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatalf("globbing workflows: %v", err)
	}
	if len(workflows) == 0 {
		t.Fatal("no workflows found; this guard checked nothing")
	}

	checked := 0
	var findings []string
	for _, path := range workflows {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("cannot read %s: %v", filepath.Base(path), err)
			continue
		}
		var workflow workflowFile
		if err := yaml.Unmarshal(contents, &workflow); err != nil {
			t.Errorf("%s does not parse: %v", filepath.Base(path), err)
			continue
		}
		names := make([]string, 0, len(workflow.Jobs))
		for name := range workflow.Jobs {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			job := workflow.Jobs[name]
			usesGo, pinned := false, false
			for _, step := range job.Steps {
				if runsGo.MatchString(step.Run) || runsCadreShim.MatchString(step.Run) {
					usesGo = true
				}
				if strings.Contains(step.Uses, "setup-go") {
					pinned = true
				}
			}
			if !usesGo {
				continue
			}
			checked++
			if !pinned {
				findings = append(findings, filepath.Base(path)+": job "+name)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no workflow job was found to run Go; either the scan is broken or " +
			"the last one was removed and this guard should go with it")
	}
	sort.Strings(findings)
	if len(findings) > 0 {
		t.Errorf("%d job(s) run Go without pinning a toolchain:\n  %s\n\n"+
			"The runner image ships a Go, so these work -- with whatever version "+
			"that image carries rather than the one go.mod names. Add "+
			"actions/setup-go with go-version-file: go.mod.",
			len(findings), strings.Join(findings, "\n  "))
	}
	t.Logf("checked %d Go-running job(s) across %d workflows", checked, len(workflows))
}

// The mirror of the rule above: a job that sets up Python must use it.
//
// Every Python interpreter this repository's CI installs is now there for one
// of three reasons -- the two structural scripts' tests, building or
// installing the wheel, or the installers, which shell out to
// bootstrap_sdlc.py. When a job stops needing Python, the setup step is what
// gets left behind, and it is invisible: it costs a runner thirty seconds and
// declares a dependency that no longer exists.
//
// Two were found this way. generated-content set up Python and ran only
// `./bin/cadre generate-plugin --check`, which is Go. roster went further: it
// carried a two-leg Python *matrix*, installed a hash-pinned lockfile, and
// then ran nothing but Go binaries -- so both legs executed the same Go twice
// and the lockfile fed nothing. Its matrix comment explained the floor in
// terms of `pip install cadre` users, on a job that never runs pip.
var (
	runsPython = regexp.MustCompile(`\b(python3?|pip3?|uv)\b`)
	// The installers resolve an interpreter themselves and run
	// bootstrap_sdlc.py, so a job executing one needs Python without ever
	// naming it.
	runsInstaller = regexp.MustCompile(`(^|[^\w./])\.?/?install\.(sh|ps1)\b`)
)

func TestNoWorkflowJobSetsUpAPythonItNeverUses(t *testing.T) {
	root := filepath.Dir(filepath.Dir(mustGetwd(t)))
	workflows, err := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatalf("globbing workflows: %v", err)
	}

	declared := 0
	var findings []string
	for _, path := range workflows {
		contents, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var workflow workflowFile
		if err := yaml.Unmarshal(contents, &workflow); err != nil {
			continue
		}
		names := make([]string, 0, len(workflow.Jobs))
		for name := range workflow.Jobs {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			job := workflow.Jobs[name]
			setsUp, uses := false, false
			for _, step := range job.Steps {
				if strings.Contains(step.Uses, "setup-python") {
					setsUp = true
				}
				if runsPython.MatchString(step.Run) || runsInstaller.MatchString(step.Run) {
					uses = true
				}
			}
			if !setsUp {
				continue
			}
			declared++
			if !uses {
				findings = append(findings, filepath.Base(path)+": job "+name)
			}
		}
	}
	if declared == 0 {
		t.Skip("no job sets up Python any more; this guard can go with the last one")
	}
	sort.Strings(findings)
	if len(findings) > 0 {
		t.Errorf("%d job(s) install Python and never use it:\n  %s\n\n"+
			"A setup step left behind after its last Python went is a dependency "+
			"the repository no longer has, declared where nobody reads it.",
			len(findings), strings.Join(findings, "\n  "))
	}
	t.Logf("checked %d job(s) that set up Python", declared)
}
