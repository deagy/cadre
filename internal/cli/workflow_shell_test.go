package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A job that can land on a Windows runner must say which shell its steps are.
//
// GitHub's default shell is platform-dependent: bash on Linux and macOS,
// PowerShell on Windows. A matrix job written in sh therefore works on four
// legs out of five and fails on the fifth, with an error that names the shell
// rather than the mistake -- release.yml's `Read CLI version` died on
//
//	[ -n "$value" ] || { echo "..." >&2; exit 1; }
//	  ~  Missing type name after '['.
//
// which reads as a YAML or quoting problem and is neither.
//
// This is a slow fault to find by running it, because the legs fail one at a
// time: fix the version step and the next release fails on the tag check,
// fix that and it fails on the build. release.yml's `cli` job had three such
// steps and had never executed on Windows at all.
//
// The rule is deliberately structural rather than a scan for shell syntax:
// declare the shell, at the job or the step, and the ambiguity is gone. A
// step may still override -- the Windows-only archive step sets `shell: pwsh`
// on purpose.

type shellWorkflow struct {
	Jobs map[string]struct {
		RunsOn   yaml.Node `yaml:"runs-on"`
		Defaults struct {
			Run struct {
				Shell string `yaml:"shell"`
			} `yaml:"run"`
		} `yaml:"defaults"`
		Strategy struct {
			Matrix struct {
				OS      []string            `yaml:"os"`
				Include []map[string]string `yaml:"include"`
			} `yaml:"matrix"`
		} `yaml:"strategy"`
		Steps []struct {
			Name  string `yaml:"name"`
			Uses  string `yaml:"uses"`
			Run   string `yaml:"run"`
			Shell string `yaml:"shell"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

func TestEveryWindowsCapableJobDeclaresItsShell(t *testing.T) {
	root := filepath.Dir(filepath.Dir(mustGetwd(t)))
	workflows, err := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatalf("globbing workflows: %v", err)
	}
	if len(workflows) == 0 {
		t.Fatal("no workflows found; this guard checked nothing")
	}

	var findings []string
	windowsJobs := 0

	for _, path := range workflows {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		var wf shellWorkflow
		if err := yaml.Unmarshal(contents, &wf); err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}

		names := make([]string, 0, len(wf.Jobs))
		for name := range wf.Jobs {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			job := wf.Jobs[name]

			// Where a Windows runner can come from: a literal runs-on, a
			// matrix `os` list, or a matrix `include` entry naming one.
			candidates := []string{job.RunsOn.Value}
			candidates = append(candidates, job.Strategy.Matrix.OS...)
			for _, entry := range job.Strategy.Matrix.Include {
				for _, v := range entry {
					candidates = append(candidates, v)
				}
			}
			onWindows := false
			for _, c := range candidates {
				if strings.Contains(strings.ToLower(c), "windows") {
					onWindows = true
					break
				}
			}
			if !onWindows {
				continue
			}
			windowsJobs++
			if job.Defaults.Run.Shell != "" {
				continue
			}
			for _, step := range job.Steps {
				if strings.TrimSpace(step.Run) == "" || step.Shell != "" {
					continue
				}
				label := step.Name
				if label == "" {
					label = "(unnamed step)"
				}
				findings = append(findings, fmt.Sprintf(
					"%s: job %q can run on Windows and step %q has a `run:` with no `shell:`; "+
						"it will execute under PowerShell there",
					filepath.Base(path), name, label))
			}
		}
	}

	if windowsJobs == 0 {
		t.Fatal("no Windows-capable job found; this guard checked nothing")
	}
	if len(findings) > 0 {
		sort.Strings(findings)
		t.Fatalf("undeclared shell on Windows-capable jobs:\n  %s", strings.Join(findings, "\n  "))
	}
	t.Logf("checked %d Windows-capable job(s)", windowsJobs)
}
