package selector

import (
	"path/filepath"
	"testing"
)

// Routes that must NOT fire, and the keyword boundary that stops them.
//
// The golden corpus pins what the selector *does* select. Almost all of it is
// positive: 176 fixtures naming the agents each task should reach. A route
// that started matching one phrase too many would leave every one of them
// green, because none asserts an absence.
//
// That is the failure mode this file covers, and it is not hypothetical. Each
// case below is a regression pin for a defect that shipped -- recorded in
// roster/orchestration/test/test_selector.py, whose negatives go with the
// Python selector.
//
// The corpus is not silent on this: it carries twelve negative fixtures, and
// PIPELINE-NEGATIVE-1 pins the same cross-runner task as the first case here.
// What it does not carry is the other six, and what no fixture carries is the
// *reason* -- a corpus entry records the agent list a task resolved to, so a
// widened keyword shows up as an unexplained diff in an expected-agents array
// rather than as "the route that once false-positived is false-positiving
// again". The cross-runner case is kept for that: the assertion is the absence
// and the failure message is the history.
//
// Widening a keyword group is a small, plausible, well-meant edit. Its failure
// is silent: an extra specialist is dispatched, extra gates are demanded, and
// nothing errors.

func TestTheKeywordBoundaryTreatsAHyphenAsPartOfTheWord(t *testing.T) {
	// match_test.go already covers this through KeywordMatches, which is the
	// caller that matters. This adds the character classes directly, because
	// those tests reach the predicate only through whichever characters their
	// example strings happen to contain -- digits and uppercase were never
	// exercised, and the function was at 66.7%.
	//
	// The asymmetry is deliberate and load-bearing in both directions: `-` is
	// a word character, so "runner" does not match inside "cross-runner"; `_`
	// and `.` are not, which is what lets routing.json's `bootstrap_sdlc.py`
	// keyword match embedded in a longer token.
	for _, c := range []byte("abcxyzABCXYZ0189-") {
		if !isKeywordBoundaryChar(c) {
			t.Errorf("%q must count as part of a word", string(c))
		}
	}
	for _, c := range []byte("_. /:,;()[]{}\"'\t\n") {
		if isKeywordBoundaryChar(c) {
			t.Errorf("%q must count as a boundary, not part of a word", string(c))
		}
	}
}

func TestAKeywordDoesNotMatchInsideAHyphenatedCompound(t *testing.T) {
	// At the matcher, independent of routing.json's contents. "runner",
	// "index", "lock", "cd", "alert" and "token" all substring-matched inside
	// a hyphenated compound before the boundary fix.
	rule := map[string]any{"keywords": []any{"runner", "index", "lock"}}

	for _, text := range []string{
		"improve cross-runner UX documentation",
		"schedule a re-index-lock maintenance window",
		"the gitlab-runner-lock file",
	} {
		if match := MatchRule(rule, text, nil); match.Matched {
			t.Errorf("a keyword matched inside a hyphenated compound: %q -> %v",
				text, match.Keywords)
		}
	}
	// And the same words as their own tokens still match, so the boundary is
	// a boundary rather than an off switch.
	for _, text := range []string{
		"the index needs a maintenance lock",
		"restart the runner",
		"lock, index and runner all appear here",
	} {
		if match := MatchRule(rule, text, nil); !match.Matched {
			t.Errorf("a plain keyword stopped matching: %q", text)
		}
	}
	// An underscore or a dot is still a boundary, which is what a keyword
	// carrying a filename depends on.
	filename := map[string]any{"keywords": []any{"bootstrap_sdlc.py"}}
	if !MatchRule(filename, "run scripts/bootstrap_sdlc.py --check", nil).Matched {
		t.Error("a keyword containing _ and . stopped matching inside a path")
	}
}

// falsePositiveCase is one task that must not reach a particular route or
// agent, paired with what it *should* reach so the case cannot pass by the
// selector having stopped matching anything at all.
type falsePositiveCase struct {
	name         string
	task         string
	files        []string
	mustNotRoute []string
	mustNotStaff []string
	mustRoute    []string
	mustStaff    []string
	expectTriage bool
	whyItOnceDid string
}

func TestAdjacentDomainPhrasingDoesNotReachTheWrongSpecialist(t *testing.T) {
	config := loadRoutingConfig(t)
	catalog := loadCatalogIDs(t)
	roster := filepath.Join(selectorRepoRoot(t), "roster")

	for _, probe := range []falsePositiveCase{
		{
			name:         "a hyphenated compound does not reach the pipeline route",
			task:         "improve cross-runner UX documentation",
			mustNotRoute: []string{"pipeline"},
			mustRoute:    []string{"documentation"},
			whyItOnceDid: `pipeline's "runner" keyword substring-matched inside "cross-runner"`,
		},
		{
			name:         "a hyphenated compound does not reach database-reliability",
			task:         "schedule a re-index-lock maintenance window",
			mustNotRoute: []string{"database-reliability"},
			expectTriage: true,
			whyItOnceDid: `"index" and "lock" substring-matched inside "re-index-lock"`,
		},
		{
			name:      "and the same words as plain tokens still do",
			task:      "the index needs a maintenance lock",
			mustRoute: []string{"database-reliability"},
			mustStaff: []string{"database-reliability-engineer"},
		},
		{
			name:         "this repository's own TypeScript is not a browser surface",
			task:         "Rename the Cline model tier vocabulary",
			files:        []string{"cline-plugins/cline-agents/index.ts"},
			mustNotRoute: []string{"frontend"},
			mustNotStaff: []string{"frontend-engineer", "accessibility-reviewer", "interaction-designer"},
			mustRoute:    []string{"node-typescript-execution"},
			whyItOnceDid: "every .ts file in this tree lives under cline-plugins/, so the " +
				"frontend route could only ever fire as a false positive -- staffing three " +
				"browser roles and demanding G3/G5/G6/G7 for a dispatch shim",
		},
		{
			name:         "generic documentation does not reach the ADR writer",
			task:         "Update operator documentation",
			files:        []string{"docs/guides/operations.md"},
			mustNotStaff: []string{"adr-writer"},
			mustStaff:    []string{"technical-writer", "technical-documentation-implementer"},
			whyItOnceDid: "an architecture-decision record is a specific artifact; " +
				"routing every doc edit to its author makes the signal meaningless",
		},
		{
			name:         "adding findings to a report is not knowledge-store work",
			task:         "Add findings from the incident review to the report",
			mustNotRoute: []string{"knowledge-store"},
			mustNotStaff: []string{"knowledge-store-steward"},
			mustRoute:    []string{"incident-response"},
			whyItOnceDid: `PR #164's original keyword_groups paired ["add", ...] with ` +
				`["findings", ...], which matches ordinary incident write-ups`,
		},
		{
			name:         "archiving a learnings folder is not knowledge-store work",
			task:         "Archive the old learnings folder",
			mustNotRoute: []string{"knowledge-store"},
			mustNotStaff: []string{"knowledge-store-steward"},
			expectTriage: true,
			whyItOnceDid: `the same groups paired a generic verb with "learnings"`,
		},
	} {
		t.Run(probe.name, func(t *testing.T) {
			plan, err := BuildDispatchPlan(config, PlanInput{
				Task:              probe.task,
				TaskID:            "FALSE-POSITIVE",
				ChangedFiles:      probe.files,
				Classification:    "internal",
				RepositoryRoot:    "<REPO_ROOT>",
				ChangedFileSource: "explicit",
			}, PlanOptions{Catalog: catalog, RosterRoot: roster})
			if err != nil {
				t.Fatalf("BuildDispatchPlan: %v", err)
			}

			routes := planRouteIDs(plan)
			agents := planAgents(t, plan)
			staffed := append(append(append([]string{},
				agents.Primary...), agents.Reviewers...), agents.Support...)

			for _, unwanted := range probe.mustNotRoute {
				if contains(routes, unwanted) {
					t.Errorf("the %s route matched %q.\nIt once did because: %s\nroutes: %v",
						unwanted, probe.task, probe.whyItOnceDid, routes)
				}
			}
			for _, unwanted := range probe.mustNotStaff {
				if contains(staffed, unwanted) {
					t.Errorf("%s was dispatched for %q.\nIt once was because: %s\nagents: %v",
						unwanted, probe.task, probe.whyItOnceDid, staffed)
				}
			}

			// The other half. Without it a selector that matched nothing at
			// all would satisfy every assertion above.
			for _, wanted := range probe.mustRoute {
				if !contains(routes, wanted) {
					t.Errorf("the %s route did not match, so this case no longer "+
						"distinguishes a false positive from a dead selector.\nroutes: %v",
						wanted, routes)
				}
			}
			for _, wanted := range probe.mustStaff {
				if !contains(staffed, wanted) {
					t.Errorf("%s was not dispatched, so this case proves nothing.\nagents: %v",
						wanted, staffed)
				}
			}
			if probe.expectTriage {
				if len(routes) != 0 {
					t.Errorf("expected no route to match, got %v", routes)
				}
				if plan["status"] != "needs-triage" {
					t.Errorf("status = %v, want needs-triage -- a task matching nothing "+
						"must say so rather than resolving to an empty plan that reads "+
						"as success", plan["status"])
				}
			}
		})
	}
}

func TestTheFalsePositiveCorpusIsNotSilentlyPassing(t *testing.T) {
	// Guards the guard. Every case above asserts an absence, and an absence is
	// satisfied by a selector that stopped working entirely. The positive
	// halves cover that per-case; this covers the file: at least one case must
	// staff somebody, and at least one must reach needs-triage, or the table
	// has drifted into testing only one outcome.
	config := loadRoutingConfig(t)
	catalog := loadCatalogIDs(t)
	roster := filepath.Join(selectorRepoRoot(t), "roster")

	staffedSomething, reachedTriage := false, false
	for _, task := range []string{
		"the index needs a maintenance lock",
		"Archive the old learnings folder",
	} {
		plan, err := BuildDispatchPlan(config, PlanInput{
			Task: task, TaskID: "VACUITY", Classification: "internal",
			RepositoryRoot: "<REPO_ROOT>", ChangedFileSource: "explicit",
		}, PlanOptions{Catalog: catalog, RosterRoot: roster})
		if err != nil {
			t.Fatal(err)
		}
		if len(planAgents(t, plan).Primary) > 0 {
			staffedSomething = true
		}
		if plan["status"] == "needs-triage" {
			reachedTriage = true
		}
	}
	if !staffedSomething {
		t.Error("no case staffs anyone; the absence assertions above prove nothing")
	}
	if !reachedTriage {
		t.Error("no case reaches needs-triage; the table covers only one outcome")
	}
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
