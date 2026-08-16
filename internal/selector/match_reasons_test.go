package selector

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// Why each route and risk matched: the plan's own audit trail.
//
// A plan that names agents without saying why leaves a reader to reconstruct
// the decision from routing.json. matched_routes and matched_risks each carry
// a `reasons` block naming the keywords, keyword groups and path patterns that
// fired -- and they carry the *same* block, so one reader and one consumer
// handle both.
//
// Nothing tested the builder. The shape is load-bearing for consumers: the
// plugin wrapper, the Cline plugins and the text renderer all walk these
// lists, and a null where an empty list belongs is a crash in whichever of
// them iterates without checking.

func reasonedMatch(id string, keywords []string, groups [][]string, paths []PathMatch) Match {
	return Match{ID: id, Reasons: RuleMatch{
		Matched: true, Keywords: keywords, KeywordGroups: groups, Paths: paths,
	}}
}

// reasonBlocks renders entries the way a consumer receives them: through JSON,
// because that is the only form anything outside this process ever sees.
func reasonBlocks(t *testing.T, entries []map[string]any) []map[string]any {
	t.Helper()
	encoded, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

func TestEveryMatchEntryCarriesAnIDAndTheSameThreeReasonKeys(t *testing.T) {
	// One shape for routes and risks. A consumer written against one and
	// handed the other is the ordinary case -- the plan puts them side by side
	// -- so a key present in only one is a bug that surfaces in somebody
	// else's code.
	entries := reasonBlocks(t, reasonEntries([]Match{
		reasonedMatch("backend", []string{"api"}, nil,
			[]PathMatch{{Pattern: "src/**", File: "src/a.go"}}),
		reasonedMatch("production", nil, [][]string{{"production"}, {"deploy"}}, nil),
	}))
	if len(entries) != 2 {
		t.Fatalf("built %d entries", len(entries))
	}
	for _, entry := range entries {
		if _, ok := entry["id"].(string); !ok {
			t.Errorf("entry has no id: %v", entry)
		}
		reasons, ok := entry["reasons"].(map[string]any)
		if !ok {
			t.Fatalf("entry has no reasons block: %v", entry)
		}
		for _, key := range []string{"keywords", "keyword_groups", "paths"} {
			if _, present := reasons[key]; !present {
				t.Errorf("%v is missing reasons.%s", entry["id"], key)
			}
		}
		if len(reasons) != 3 {
			t.Errorf("%v carries %d reason keys, want exactly the three: %v",
				entry["id"], len(reasons), reasons)
		}
	}
}

func TestAnEmptyReasonIsAnEmptyListRatherThanNull(t *testing.T) {
	// The property the orEmpty helpers exist for, and the one a Go author
	// loses without noticing: a nil slice marshals to null, and a consumer
	// iterating the list crashes rather than seeing nothing.
	//
	// Every reason can legitimately be empty. A route matched purely on a path
	// has no keywords; a risk matched purely on keywords has no paths.
	entries := reasonBlocks(t, reasonEntries([]Match{
		reasonedMatch("path-only", nil, nil, []PathMatch{{Pattern: "db/**", File: "db/a.sql"}}),
		reasonedMatch("keyword-only", []string{"deploy"}, nil, nil),
		reasonedMatch("nothing-at-all", nil, nil, nil),
	}))
	for _, entry := range entries {
		reasons := entry["reasons"].(map[string]any)
		for _, key := range []string{"keywords", "keyword_groups", "paths"} {
			value := reasons[key]
			if value == nil {
				t.Errorf("%v reasons.%s is null; a consumer iterating it breaks",
					entry["id"], key)
				continue
			}
			if _, ok := value.([]any); !ok {
				t.Errorf("%v reasons.%s is %T, want a list", entry["id"], key, value)
			}
		}
	}
}

func TestAReasonBlockReportsWhatActuallyMatched(t *testing.T) {
	// The other half. Emitting the right shape with nothing in it would
	// satisfy every structural check above while telling a reader nothing --
	// and "no reasons" is indistinguishable from "matched for reasons we did
	// not record".
	entries := reasonBlocks(t, reasonEntries([]Match{
		reasonedMatch("infrastructure",
			[]string{"terraform", "rotate"},
			[][]string{{"production", "deploy"}},
			[]PathMatch{{Pattern: "terraform/**", File: "terraform/main.tf"}}),
	}))
	reasons := entries[0]["reasons"].(map[string]any)

	keywords := reasons["keywords"].([]any)
	if len(keywords) != 2 || keywords[0] != "terraform" {
		t.Errorf("keywords = %v, want both in order", keywords)
	}
	groups := reasons["keyword_groups"].([]any)
	if len(groups) != 1 {
		t.Fatalf("keyword_groups = %v, want one group", groups)
	}
	inner, ok := groups[0].([]any)
	if !ok || len(inner) != 2 {
		t.Errorf("a keyword group is not a list of its keywords: %v", groups[0])
	}
	paths := reasons["paths"].([]any)
	if len(paths) != 1 {
		t.Fatalf("paths = %v, want one", paths)
	}
	path := paths[0].(map[string]any)
	if path["pattern"] != "terraform/**" || path["file"] != "terraform/main.tf" {
		t.Errorf("a path entry does not name both the pattern and the file: %v", path)
	}
}

func TestARealPlanCarriesReasonsForEveryMatch(t *testing.T) {
	// Against the shipped routing rather than a fixture, because the shape
	// above is only worth having if the real thing fills it in. A task chosen
	// to trip both a route and a risk, so both lists are non-empty and the
	// "same shape" claim is exercised on real data.
	plan, err := BuildDispatchPlan(loadRoutingConfig(t), PlanInput{
		Task:   "Run a production database migration that alters the users table",
		TaskID: "REASONS-1", ChangedFiles: []string{"db/migrate.sql"},
		Classification: "internal", RepositoryRoot: "<REPO_ROOT>",
		ChangedFileSource: "explicit",
	}, PlanOptions{
		Catalog:    loadCatalogIDs(t),
		RosterRoot: filepath.Join(selectorRepoRoot(t), "roster"),
	})
	if err != nil {
		t.Fatalf("BuildDispatchPlan: %v", err)
	}

	for _, key := range []string{"matched_routes", "matched_risks"} {
		entries, ok := plan[key].([]map[string]any)
		if !ok {
			t.Fatalf("%s is %T, not []map[string]any", key, plan[key])
		}
		if len(entries) == 0 {
			t.Fatalf("%s is empty, so this case checks nothing", key)
		}
		populated := 0
		for _, entry := range reasonBlocks(t, entries) {
			reasons, ok := entry["reasons"].(map[string]any)
			if !ok {
				t.Errorf("%s entry %v carries no reasons", key, entry["id"])
				continue
			}
			for _, name := range []string{"keywords", "keyword_groups", "paths"} {
				if reasons[name] == nil {
					t.Errorf("%s entry %v has a null %s", key, entry["id"], name)
				}
			}
			for _, name := range []string{"keywords", "keyword_groups", "paths"} {
				if list, ok := reasons[name].([]any); ok && len(list) > 0 {
					populated++
					break
				}
			}
		}
		if populated == 0 {
			t.Errorf("every %s entry has empty reasons; the plan names matches it "+
				"cannot explain", key)
		}
	}
}
