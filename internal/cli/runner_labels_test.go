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

// Runner labels are reviewed, not typed from memory.
//
// A `runs-on:` naming a label GitHub no longer serves does not fail. No
// runner is ever assigned, and the job sits in "queued" -- indistinguishable
// from a busy pool -- until it eventually times out. release.yml asked for
// macos-13 long after actions/runner-images retired it, and because that job
// only runs on a release, nothing ever waited on it to find out. Every other
// leg of the matrix finished while that one sat unassigned.
//
// The list below is the review record. Adding a label means editing it, which
// is the moment to check the label still exists; `retiredLabels` makes a
// return to a known-dead one say so outright rather than fail as "unknown".
//
// What this cannot do is notice a label retired upstream *after* it was
// added -- exactly what happened here. Nothing offline can. The mitigation
// for that is timeout-minutes on every job, so a dead label costs minutes
// rather than stalling a release for hours.
var (
	approvedRunnerLabels = map[string]string{
		"ubuntu-latest":  "standard Linux runner",
		"macos-latest":   "current macOS Arm64 (macOS 26 arm64 as of 2026-08)",
		"windows-latest": "standard Windows runner",
		"ubuntu-24.04-arm": "GitHub-hosted arm64 Linux, free for public repositories. " +
			"Pinned to a version rather than `ubuntu-latest-arm` on purpose: this leg " +
			"builds cgo against the runner's glibc, and the wheel it produces claims " +
			"manylinux_2_17 -- a floating label would move that floor without saying so",
	}

	retiredLabels = map[string]string{
		"macos-13":     "retired by actions/runner-images; use macos-15-intel or macos-26-intel for Intel builds",
		"macos-12":     "retired by actions/runner-images",
		"macos-11":     "retired by actions/runner-images",
		"ubuntu-18.04": "retired by actions/runner-images",
		"ubuntu-20.04": "retired by actions/runner-images",
		"windows-2019": "retired by actions/runner-images",
	}
)

func TestEveryRunnerLabelIsReviewed(t *testing.T) {
	root := filepath.Dir(filepath.Dir(mustGetwd(t)))
	workflows, err := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatalf("globbing workflows: %v", err)
	}
	if len(workflows) == 0 {
		t.Fatal("no workflows found; this guard checked nothing")
	}

	var findings []string
	seen := map[string]bool{}

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

			labels := []string{}
			// A literal runs-on. `${{ matrix.os }}` is an expression, not a
			// label; the real values come from the matrix below.
			if v := job.RunsOn.Value; v != "" && !strings.Contains(v, "${{") {
				labels = append(labels, v)
			}
			labels = append(labels, job.Strategy.Matrix.OS...)
			for _, entry := range job.Strategy.Matrix.Include {
				if v, ok := entry["os"]; ok {
					labels = append(labels, v)
				}
			}

			for _, label := range labels {
				key := filepath.Base(path) + " " + name + " " + label
				if seen[key] {
					continue
				}
				seen[key] = true

				if why, dead := retiredLabels[label]; dead {
					findings = append(findings, fmt.Sprintf(
						"%s: job %q runs-on %q -- %s. Jobs asking for it queue forever rather than failing",
						filepath.Base(path), name, label, why))
					continue
				}
				if _, ok := approvedRunnerLabels[label]; !ok {
					findings = append(findings, fmt.Sprintf(
						"%s: job %q runs-on %q, which is not in approvedRunnerLabels. "+
							"Confirm the label is one actions/runner-images currently publishes, then add it there with a note",
						filepath.Base(path), name, label))
				}
			}
		}
	}

	if len(seen) == 0 {
		t.Fatal("no runner labels found; this guard checked nothing")
	}
	if len(findings) > 0 {
		sort.Strings(findings)
		t.Fatalf("runner labels needing review:\n  %s", strings.Join(findings, "\n  "))
	}
	t.Logf("checked %d job/label pairs", len(seen))
}
