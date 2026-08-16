package selector

import (
	"strconv"
	"testing"
)

// `--top 0`: the divergence this file was written for.
//
// Orchestration policy bounds retrieval breadth at 1..20, and the refusal says
// so in as many words. The Go selector accepted 0 anyway and quietly retrieved
// five results, because KnowledgeInput.Top was a plain int and the builder
// read 0 as "the caller expressed no preference".
//
// The Python selector refused it. The differential suite compares both
// implementations byte-for-byte and passed regardless, because
// select_corpus.json never passes --top 0 -- a reminder that a corpus gates
// the inputs it contains and nothing else.
//
// Found by auditing test_selector.py's
// test_rejects_knowledge_top_outside_orchestration_policy against this
// package, which is the last moment the reference implementation exists to be
// compared against.

func TestAnExplicitTopIsDistinguishableFromNoPreference(t *testing.T) {
	// The distinction the pointer exists to carry. Both arms must hold: a nil
	// Top defaulting is what every programmatic caller relies on, and an
	// explicit 0 being refused is the policy bound.
	focus := map[string]any{"agent-a": "prior defects"}
	agents := []string{"agent-a"}

	unset, err := BuildKnowledgeContext(focus, agents, KnowledgeInput{
		Task: "t", TaskID: "T", Classification: "internal",
	})
	if err != nil {
		t.Fatalf("a caller expressing no preference was refused: %v", err)
	}
	if len(unset.Requests) == 0 {
		t.Fatal("no request was planned; the rest of this proves nothing")
	}
	if got := argumentAfter(unset.Requests[0].Invocation.Args, "--top"); got != "5" {
		t.Errorf("--top = %q with no preference expressed, want the default of 5", got)
	}

	if _, err := BuildKnowledgeContext(focus, agents, KnowledgeInput{
		Task: "t", TaskID: "T", Classification: "internal", Top: knowledgeTop(0),
	}); err == nil {
		t.Error("an explicit --top 0 was accepted; policy bounds retrieval at 1 " +
			"through 20, and silently substituting 5 gives a caller five results " +
			"they did not ask for")
	}
}

func TestTheRetrievalBoundIsRefusedAtBothEnds(t *testing.T) {
	// One case per boundary, on both sides, so an off-by-one in either
	// direction shows up as a named case rather than as a shifted range that
	// still refuses *something*.
	focus := map[string]any{"agent-a": "prior defects"}
	agents := []string{"agent-a"}

	for _, probe := range []struct {
		top        int
		acceptable bool
	}{
		{-1, false}, {0, false}, {1, true}, {2, true},
		{MaximumKnowledgeTop - 1, true}, {MaximumKnowledgeTop, true},
		{MaximumKnowledgeTop + 1, false}, {1000, false},
	} {
		got, err := BuildKnowledgeContext(focus, agents, KnowledgeInput{
			Task: "t", TaskID: "T", Classification: "internal",
			Top: knowledgeTop(probe.top),
		})
		switch {
		case probe.acceptable && err != nil:
			t.Errorf("top=%d is inside the policy range and was refused: %v", probe.top, err)
		case !probe.acceptable && err == nil:
			t.Errorf("top=%d is outside 1..%d and was accepted", probe.top, MaximumKnowledgeTop)
		case probe.acceptable:
			if want := strconv.Itoa(probe.top); argumentAfter(got.Requests[0].Invocation.Args, "--top") != want {
				t.Errorf("top=%d did not reach the invocation as %q", probe.top, want)
			}
		}
	}
}

func TestNoRetrievalMeansNoBoundToCheck(t *testing.T) {
	// The scoping that decides where this validation belongs. A task matching
	// nothing plans no retrieval, so there is no breadth to bound and an
	// out-of-range --top is simply unused -- which is what the Python selector
	// does, because its check sits inside the knowledge builder after the
	// early returns rather than at the flag.
	//
	// Moving the check to argument parsing would look tidier and would refuse
	// a value that was never going to be used, changing the exit code of a run
	// that has nothing to do with retrieval.
	for _, top := range []int{0, MaximumKnowledgeTop + 1} {
		got, err := BuildKnowledgeContext(map[string]any{"a": "f"}, nil, KnowledgeInput{
			Task: "t", TaskID: "T", Classification: "internal", Top: knowledgeTop(top),
		})
		if err != nil {
			t.Errorf("top=%d was refused for a plan that retrieves nothing: %v", top, err)
		}
		if got.Status != "not-applicable" {
			t.Errorf("status = %q, want not-applicable", got.Status)
		}
	}
}

// argumentAfter returns the value following flag in an argv, or "".
func argumentAfter(args []string, flag string) string {
	for index, argument := range args {
		if argument == flag && index+1 < len(args) {
			return args[index+1]
		}
	}
	return ""
}
