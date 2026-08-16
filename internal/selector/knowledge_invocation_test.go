package selector

import (
	"encoding/json"
	"strings"
	"testing"
)

// The command line the selector hands to retrieval.
//
// `cadre select` never retrieves anything. It emits a plan describing the call
// somebody else will make, and that description is the whole of the interface:
// whatever runs it has only these bytes to go on. So the shape is a contract,
// and the ways it can go wrong are not cosmetic --
//
//   - an interpreter prepended to argv[0] changes what is executed
//   - a --config would point retrieval at a store the project did not choose
//   - a cwd or env field would let a plan move the execution environment
//   - a missing --json changes what the caller has to parse
//
// stage3_test.go covers the ordering rules (query last, one --source each,
// never --all-sources). This covers the shape and the things that must not be
// in it, which nothing asserted.
//
// Ported from test_selector.py's
// test_knowledge_invocation_preserves_argv_and_output_contract. Verified
// against the Python selector's emitted JSON on 2026-08-16 -- same request
// keys, same invocation keys, same launcher, same argv[0] -- while it was
// still in the tree.

func oneRequest(t *testing.T, input KnowledgeInput) KnowledgeRequest {
	t.Helper()
	if input.KnowledgeCLI == "" {
		input.KnowledgeCLI = "/opt/cadre/bin/cadre"
	}
	got, err := BuildKnowledgeContext(
		map[string]any{"frontend-engineer": "frontend implementation patterns"},
		[]string{"frontend-engineer"}, input)
	if err != nil {
		t.Fatalf("BuildKnowledgeContext: %v", err)
	}
	if len(got.Requests) != 1 {
		t.Fatalf("planned %d requests, want exactly one", len(got.Requests))
	}
	return got.Requests[0]
}

func plannedRequest(t *testing.T) KnowledgeRequest {
	t.Helper()
	return oneRequest(t, KnowledgeInput{
		Task: "Update the React navigation", TaskID: "UI-8",
		Classification: "confidential",
		Sources:        []string{"approved-decisions", "proposed-knowledge"},
	})
}

func TestTheInvocationCarriesNothingBeyondALauncherAndAnArgv(t *testing.T) {
	// Asserted on the serialized form, because the serialized form is what a
	// consumer reads. A field added to the struct without being considered
	// here -- a cwd, an env, a shell, a timeout -- shows up as a new key and
	// fails, which is the point: each of those changes where or how the
	// command runs, and none should arrive by accident.
	//
	// The reach is exactly that, and no further: a field tagged omitempty and
	// left empty serializes to nothing and is invisible here, as it is to the
	// recorded-plan goldens. Checked rather than assumed -- adding
	// `Cwd string \`json:"cwd,omitempty"\`` fails neither this nor the
	// goldens until something populates it, at which point both fail. That is
	// the correct boundary (an absent field is not part of the contract) but
	// it is worth stating, because "the invocation carries nothing else"
	// sounds stronger than what is actually enforced.
	encoded, err := json.Marshal(plannedRequest(t).Invocation)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	wanted := map[string]bool{"launcher": true, "args": true}
	for key := range decoded {
		if !wanted[key] {
			t.Errorf("the invocation carries an unexpected %q. If it moves where "+
				"or how retrieval runs, that is a decision to make deliberately:\n%s",
				key, encoded)
		}
	}
	for key := range wanted {
		if _, present := decoded[key]; !present {
			t.Errorf("the invocation is missing %q", key)
		}
	}
}

func TestArgvZeroIsTheCLIItselfWithNoInterpreterPrepended(t *testing.T) {
	// It was a .py path plus a probed interpreter until the knowledge store
	// moved to Go. Prepending anything again -- an interpreter, a shell, a
	// wrapper -- changes what is executed while leaving every other assertion
	// in this file true.
	request := oneRequest(t, KnowledgeInput{
		Task: "t", TaskID: "T", Classification: "internal",
		KnowledgeCLI: "/opt/cadre/bin/cadre",
	})
	args := request.Invocation.Args
	if args[0] != "/opt/cadre/bin/cadre" {
		t.Errorf("argv[0] = %q, want the CLI itself", args[0])
	}
	if args[1] != "knowledge" || args[2] != "search" {
		t.Errorf("argv[1:3] = %v, want the subcommand immediately after the binary", args[1:3])
	}
	for _, interpreter := range []string{"python", "python3", "py", "sh", "bash", "env", "uv"} {
		if strings.HasSuffix(args[0], interpreter) {
			t.Errorf("argv[0] looks like an interpreter, not the CLI: %q", args[0])
		}
	}
	// The launcher says how to find it, so a consumer can refuse an old
	// binary rather than parsing whatever it happens to print.
	launcher := request.Invocation.Launcher
	if launcher.Runtime == "" || launcher.MinimumVersion == "" || launcher.Resolution == "" {
		t.Errorf("the launcher does not fully describe how to run argv[0]: %+v", launcher)
	}
}

func TestTheInvocationNamesNoStoreOfItsOwn(t *testing.T) {
	// Retrieval resolves its store from the project it runs in --
	// .agents/knowledge-store/config.json, falling back to the shared global
	// store. A --config, --store, --db or --home in a *plan* would override
	// that from the outside, which is the isolation boundary the knowledge
	// store's SECURITY.md exists to keep.
	args := plannedRequest(t).Invocation.Args
	joined := strings.Join(args, " ")
	for _, forbidden := range []string{
		"--config", "--store", "--store-path", "--db", "--database",
		"--home", "--root", "--all-sources",
	} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("the planned invocation carries %s:\n%v", forbidden, args)
		}
	}
	// And it does ask for the machine-readable form, which is the half of the
	// output contract a consumer depends on.
	if !strings.Contains(joined, "--json") {
		t.Errorf("--json is absent; the caller would have to parse prose:\n%v", args)
	}
}

func TestCallerSuppliedValuesStayValuesRatherThanBecomingFlags(t *testing.T) {
	// task_id is free text from whoever invoked the selector. args is a list,
	// not a string, so a value that looks like a flag stays in the value
	// position -- but only as long as nothing joins it into a command line or
	// splits it on spaces. This is what would notice either.
	//
	// The value chosen is the one that would do real damage if it were ever
	// re-parsed as a flag: --all-sources on the shared global store reads
	// every other project's corpus.
	hostile := "--all-sources"
	request := oneRequest(t, KnowledgeInput{
		Task: "t", TaskID: hostile, Classification: "internal",
		Sources: []string{"one source with spaces"},
	})
	args := request.Invocation.Args

	index := -1
	for position, argument := range args {
		if argument == "--task-id" {
			index = position
			break
		}
	}
	if index < 0 {
		t.Fatal("--task-id is not in the argv")
	}
	if args[index+1] != hostile {
		t.Errorf("the task id did not land in the value position: %v", args)
	}
	// It appears exactly once, in that position -- not also as a bare flag.
	occurrences := 0
	for _, argument := range args {
		if argument == hostile {
			occurrences++
		}
	}
	if occurrences != 1 {
		t.Errorf("%q appears %d times in the argv: %v", hostile, occurrences, args)
	}
	// A source containing spaces is one argument, not three.
	if !containsExactly(args, "one source with spaces") {
		t.Errorf("a source with spaces was split across arguments: %v", args)
	}
	// And the query is still last, after everything above.
	if args[len(args)-1] != request.Query {
		t.Errorf("the query is no longer the trailing positional: %v", args)
	}
}

func TestARequestIsRefusedRatherThanPlannedWithoutAFocus(t *testing.T) {
	// knowledge_focus is what turns a selected agent into a query. An agent
	// without one would otherwise be planned with an empty retrieval brief --
	// a call that runs, returns whatever the store's default ranking gives,
	// and looks like a successful retrieval.
	_, err := BuildKnowledgeContext(
		map[string]any{"frontend-engineer": "patterns"},
		[]string{"frontend-engineer", "backend-engineer"},
		KnowledgeInput{Task: "t", TaskID: "T", Classification: "internal"})
	if err == nil {
		t.Fatal("an agent with no knowledge focus was planned anyway")
	}
	if !strings.Contains(err.Error(), "backend-engineer") {
		t.Errorf("the refusal does not name the agent that lacks a focus: %v", err)
	}
}

func containsExactly(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
