package orchestration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// catalog.yaml and routing.json must agree, in both directions.
//
// A catalog role no routing rule ever names cannot be selected. It is not an
// error anywhere -- the role file exists, the catalog entry is valid, the
// packaged plugin ships a wrapper for it -- and somebody maintains a
// specialist that can never be dispatched.
//
// A routing rule naming a role the catalog does not declare is the mirror
// image: selection reaches for an agent that is not there.
//
// And a rule whose exclude_paths fully shadow one of its own path globs keeps
// its reviewers and its human_gate while silently losing path coverage, so it
// fires on keywords alone.
//
// Ported from roster/orchestration/src/routing_health.py, which linted this
// and had no Go equivalent.

// checkoutRoot locates this repository from the package directory, the same
// two-levels-up walk the other tests here use.
func checkoutRoot(t *testing.T) string {
	t.Helper()
	working, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(filepath.Dir(working))
}

func routingDocument(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(checkoutRoot(t), "roster", "orchestration", "routing.json"))
	if err != nil {
		t.Skipf("not running inside a source checkout: %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("routing.json does not parse: %v", err)
	}
	return document
}

func catalogRoleIDSet(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(checkoutRoot(t), "roster", "catalog.yaml"))
	if err != nil {
		t.Skipf("not running inside a source checkout: %v", err)
	}
	ids := map[string]bool{}
	line := regexp.MustCompile(`^  ([a-z][a-z0-9-]*):\s*$`)
	for _, text := range strings.Split(string(raw), "\n") {
		if match := line.FindStringSubmatch(text); match != nil {
			ids[match[1]] = true
		}
	}
	if len(ids) < 100 {
		t.Fatalf("read %d catalog roles; the parse is broken, not the catalog", len(ids))
	}
	return ids
}

func stringsAt(value any) []string {
	list, ok := value.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, item := range list {
		if text, ok := item.(string); ok && text != "" {
			out = append(out, text)
		}
	}
	return out
}

// agentsNamedByRouting collects every role id routing.json refers to, from
// every structure that can name one.
//
// Walked generically rather than through the typed loader: several of these
// sections are `interface{}` there, and a linter that only looked at the
// fields somebody remembered to type is how an orphan survives.
func agentsNamedByRouting(t *testing.T, document map[string]any) map[string][]string {
	t.Helper()
	named := map[string][]string{}
	add := func(source string, ids []string) {
		for _, id := range ids {
			named[id] = append(named[id], source)
		}
	}
	for _, section := range []string{"routes", "risk_rules"} {
		for _, entry := range objectListOf(document[section]) {
			id, _ := entry["id"].(string)
			for _, field := range []string{"primary", "reviewers", "support"} {
				add(section+"/"+id+"."+field, stringsAt(entry[field]))
			}
		}
	}
	for _, recipe := range objectListOf(document["team_recipes"]) {
		id, _ := recipe["id"].(string)
		add("team_recipes/"+id+".members", stringsAt(recipe["members"]))
		if role, ok := recipe["role"].(string); ok && role != "" {
			add("team_recipes/"+id+".role", []string{role})
		}
	}
	if intake, ok := document["change_intake"].(map[string]any); ok {
		add("change_intake.agents", stringsAt(intake["agents"]))
	}
	if cross, ok := document["cross_stack"].(map[string]any); ok {
		add("cross_stack.support", stringsAt(cross["support"]))
	}
	if len(named) == 0 {
		t.Fatal("routing.json names no agents at all; the walk is broken")
	}
	return named
}

func objectListOf(value any) []map[string]any {
	list, ok := value.([]any)
	if !ok {
		return nil
	}
	var out []map[string]any
	for _, item := range list {
		if object, ok := item.(map[string]any); ok {
			out = append(out, object)
		}
	}
	return out
}

func TestEveryCatalogRoleIsReachableFromRouting(t *testing.T) {
	// An orphan is invisible. Nothing errors: the role file is there, the
	// catalog entry is valid, the packaged plugin ships a wrapper. It simply
	// can never be selected, and whoever maintains it has no way to find out.
	catalog := catalogRoleIDSet(t)
	named := agentsNamedByRouting(t, routingDocument(t))

	var orphans []string
	for id := range catalog {
		if len(named[id]) == 0 {
			orphans = append(orphans, id)
		}
	}
	sort.Strings(orphans)
	if len(orphans) > 0 {
		t.Errorf("%d catalog role(s) no routing rule can select: %v", len(orphans), orphans)
	}
}

func TestEveryRoleRoutingNamesExistsInTheCatalog(t *testing.T) {
	// The mirror image, and the one with a runtime consequence: selection
	// reaches for an agent that is not there. Each dangling reference is
	// reported with where it was found, because "code-reviewr" in one of a
	// hundred rules is otherwise a search.
	catalog := catalogRoleIDSet(t)
	named := agentsNamedByRouting(t, routingDocument(t))

	var dangling []string
	for id, sources := range named {
		if !catalog[id] {
			dangling = append(dangling, id+" (from "+strings.Join(sources, ", ")+")")
		}
	}
	sort.Strings(dangling)
	if len(dangling) > 0 {
		t.Errorf("%d routing reference(s) name a role the catalog does not declare: %v",
			len(dangling), dangling)
	}
}

func TestNoRuleExcludesAwayItsOwnPathCoverage(t *testing.T) {
	// A rule whose exclude_paths fully shadow one of its own path globs keeps
	// its reviewers and its human_gate, and matches on keywords alone. Nothing
	// about it looks wrong: the glob is still listed, and it never fires.
	document := routingDocument(t)
	checked, undecided := 0, 0
	var shadowed []string

	for _, section := range []string{"routes", "risk_rules"} {
		for _, rule := range objectListOf(document[section]) {
			id, _ := rule["id"].(string)
			excludes := stringsAt(rule["exclude_paths"])
			if len(excludes) == 0 {
				continue
			}
			for _, include := range stringsAt(rule["paths"]) {
				checked++
				switch GlobContains(include, excludes) {
				case Contained:
					shadowed = append(shadowed, section+"/"+id+": "+include+
						" is fully covered by "+strings.Join(excludes, ", "))
				case Undetermined:
					undecided++
				}
			}
		}
	}
	if checked == 0 {
		t.Skip("no rule declares both paths and exclude_paths")
	}
	// Counting what was looked at is not the same as counting what was
	// decided. This reports only on Contained, so 46 Undetermined verdicts and
	// 46 clean ones produce identical output -- the reassuring "checked 46"
	// below included. TestEveryRealRoutingPatternIsActuallyDecided holds the
	// strict version; this is the floor.
	if undecided == checked {
		t.Fatalf("all %d include globs came back Undetermined; this check is "+
			"passing without deciding anything", checked)
	}
	sort.Strings(shadowed)
	if len(shadowed) > 0 {
		t.Errorf("%d path glob(s) are excluded away entirely: %v", len(shadowed), shadowed)
	}
	t.Logf("checked %d include globs against their rule's exclusions", checked)
}

func TestTheReachabilityWalkSeesEverySectionThatCanNameARole(t *testing.T) {
	// Guards the guard. The orphan check passes if the walk finds a role
	// anywhere, so a walk that quietly stopped reading team_recipes would
	// still pass -- until a role reachable only through a recipe was reported
	// as an orphan, or worse, was not.
	//
	// Asserts the walk actually draws from each section the shipped file uses,
	// rather than that it contains the code to.
	document := routingDocument(t)
	named := agentsNamedByRouting(t, document)

	sources := map[string]bool{}
	for _, found := range named {
		for _, source := range found {
			switch {
			case strings.HasPrefix(source, "routes/"):
				sources["routes"] = true
			case strings.HasPrefix(source, "risk_rules/"):
				sources["risk_rules"] = true
			case strings.HasPrefix(source, "team_recipes/"):
				sources["team_recipes"] = true
			case strings.HasPrefix(source, "change_intake"):
				sources["change_intake"] = true
			case strings.HasPrefix(source, "cross_stack"):
				sources["cross_stack"] = true
			}
		}
	}
	for _, section := range []string{"routes", "risk_rules", "team_recipes",
		"change_intake", "cross_stack"} {
		// Only require what the shipped document actually populates.
		populated := document[section] != nil
		if populated && !sources[section] {
			t.Errorf("routing.json declares %s but the reachability walk drew no "+
				"role from it; an orphan reachable only through that section would "+
				"be misreported", section)
		}
	}
}
