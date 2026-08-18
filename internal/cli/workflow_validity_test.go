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

// A reusable workflow cannot be a step.
//
// `uses: owner/repo/.github/workflows/x.yml@ref` is valid only at job level,
// as `jobs.<id>.uses`. Written inside `steps:` GitHub rejects the *whole file*
// -- not the step, not the job -- so no job in it ever starts.
//
// The failure is quiet in the worst way. The run appears in the Actions list
// with a red X and the note "This run likely failed because of a workflow file
// issue", it has no jobs to open, and it looks exactly like an ordinary
// failing build. ci-cd.yml carried this from the day it was added and failed
// 100 runs out of 100 -- every push to main, including six release pushes in
// one day -- without once executing a step. A permanently red workflow also
// costs more than itself: it trains everyone to ignore a red X, so the next
// real failure is invisible too.
//
// The YAML parses fine, so the other guards in this package -- which
// yaml.Unmarshal every workflow -- were happy with it.
func TestNoWorkflowUsesAReusableWorkflowAsAStep(t *testing.T) {
	root := filepath.Dir(filepath.Dir(mustGetwd(t)))
	workflows, err := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatalf("globbing workflows: %v", err)
	}
	if len(workflows) == 0 {
		t.Fatal("no workflows found; this guard checked nothing")
	}

	var findings []string
	steps := 0

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
			for _, step := range wf.Jobs[name].Steps {
				steps++
				if step.Uses == "" {
					continue
				}
				// A reusable workflow reference names a file under
				// .github/workflows; an action reference never does.
				reference := step.Uses
				if at := strings.LastIndex(reference, "@"); at >= 0 {
					reference = reference[:at]
				}
				if !strings.Contains(reference, "/.github/workflows/") {
					continue
				}
				label := step.Name
				if label == "" {
					label = step.Uses
				}
				findings = append(findings, fmt.Sprintf(
					"%s: job %q step %q uses a reusable workflow (%s). That is only valid as "+
						"`jobs.%s.uses`; inside `steps:` GitHub rejects the whole file and no job in it runs",
					filepath.Base(path), name, label, step.Uses, name))
			}
		}
	}

	if steps == 0 {
		t.Fatal("no steps parsed out of any workflow; this guard checked nothing")
	}
	if len(findings) > 0 {
		sort.Strings(findings)
		t.Fatalf("workflow files GitHub will reject outright:\n  %s", strings.Join(findings, "\n  "))
	}
	t.Logf("checked %d steps across %d workflows", steps, len(workflows))
}
