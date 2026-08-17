package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deagy/cadre/cli/internal/knowledge"
)

// The stewardship wave's rules, where they live and whether they agree.
//
// A knowledge-store-steward reviews records that other roles staged. Two of
// the rules that make that safe are enforced in different places, and only one
// of them is code:
//
//   - A steward may not disposition its own proposal. That is
//     internal/knowledge/staged_store.go, which refuses decided_by ==
//     staged_by, and staged_separation_test.go covers it thoroughly.
//   - Only records newly staged by this run are eligible, excluding any the
//     steward staged itself. That has no code counterpart at all. It is an
//     instruction in .agents/skills/run-agent-orchestration/SKILL.md, and the
//     skill is the implementation surface: an agent reads it and acts.
//
// So the skill is tested as a contract, and -- more usefully -- the two are
// tested against each other. A skill promising the CLI will refuse something
// it accepts is the worse failure: the wave proceeds on an assumption nothing
// holds.
//
// In internal/cli rather than internal/orchestration because the verb check
// needs IsKnowledgeStagedSubcommand, and orchestration cannot import cli
// without a cycle.
//
// Ported from roster/orchestration/test/test_runtime_stewardship_wave.py.

func orchestrationSkill(t *testing.T) string {
	t.Helper()
	path := filepath.Join(filepath.Dir(filepath.Dir(mustGetwd(t))),
		".agents", "skills", "run-agent-orchestration", "SKILL.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("not running inside a source checkout: %v", err)
	}
	if len(raw) < 2000 {
		t.Fatalf("the skill is %d bytes; that is not the document these rules "+
			"live in", len(raw))
	}
	return string(raw)
}

func TestTheSkillStillCarriesEachStewardshipRule(t *testing.T) {
	// Matched on distinctive phrases rather than whole paragraphs or single
	// words. A single word passes a document that mentions the vocabulary
	// while saying something different; a whole paragraph fails on an
	// unrelated edit two sentences away. The phrase is the rule.
	skill := orchestrationSkill(t)
	for _, rule := range []struct {
		what   string
		phrase string
	}{
		{"the steward is independent of what it reviews",
			"a steward cannot disposition its own proposals"},
		{"eligibility excludes the steward's own staging",
			"Filter out from this wave any ID whose `staged_by` value is `knowledge-store-steward`"},
		{"eligibility is bounded to this run",
			"excluding any staged by"},
		{"untrusted provenance defers rather than accepts",
			"requires `deferred`"},
		{"ingestion names its record explicitly",
			"Never omit `--id`"},
		{"provenance comes from the handoff, not the runner",
			"Set `staged_by` to the handoff item's `source_role`"},
		{"an all-steward wave says so rather than running empty",
			"no stewardship wave ran because all staged proposals were from the steward"},
	} {
		if !strings.Contains(skill, rule.phrase) {
			t.Errorf("the skill no longer states that %s.\n  expected phrase: %q\n\n"+
				"An agent reads this document and acts on it. A rule dropped here "+
				"is a rule that stops being followed, whether or not any code "+
				"still enforces it.", rule.what, rule.phrase)
		}
	}
}

func TestTheSkillsSelfDispositionClaimMatchesWhatTheStoreDoes(t *testing.T) {
	// The pairing, and the reason this file is worth more than a string match.
	//
	// The skill tells an agent a steward cannot disposition its own proposal.
	// If that were only prose, the wave would rely on the agent's compliance.
	// It is not: the store refuses it. Asserting both means a change that
	// relaxes the store fails here even though the skill still reads
	// correctly -- the direction that would otherwise go unnoticed, since
	// nobody re-reads a skill while editing a store.
	skill := orchestrationSkill(t)
	if !strings.Contains(skill, "a steward cannot disposition its own proposals") {
		t.Fatal("the skill no longer makes the claim this test pairs with")
	}

	// decided_by lives under `disposition`, not at the top level -- the first
	// version of this fixture put it flat and the predicate correctly saw no
	// decider at all, which reads as "the store does not enforce this" when it
	// means "the fixture is not a dispositioned record".
	record := func(stagedBy, decidedBy string) map[string]any {
		return map[string]any{
			"id":        "KS-20260817-example",
			"status":    "accepted",
			"staged_by": stagedBy,
			"summary":   "an example",
			"disposition": map[string]any{
				"decided_by": decidedBy,
				"action":     "accepted",
				"reason":     "an example",
			},
		}
	}
	if !knowledge.StagedRecordIsSelfApproved(
		record("knowledge-store-steward", "knowledge-store-steward")) {
		t.Error("the store does not recognise a steward dispositioning its own " +
			"proposal as self-approval, but the skill promises it will")
	}
	if knowledge.StagedRecordIsSelfApproved(
		record("security-reviewer", "knowledge-store-steward")) {
		t.Error("the store treats an independent decider as self-approval, which " +
			"would make the wave refuse every legitimate disposition")
	}
	// An absent decider is malformed input, not a self-approval -- reporting
	// it as one would send the reader looking for a separation problem that is
	// not there.
	if knowledge.StagedRecordIsSelfApproved(record("knowledge-store-steward", "")) {
		t.Error("a blank decided_by reads as self-approval")
	}
}

func TestTheSkillNamesCommandsTheCLIActuallyOffers(t *testing.T) {
	// A skill naming a command the CLI does not have would strand the wave at
	// that step, and the failure would read as an agent mistake rather than a
	// stale document.
	skill := orchestrationSkill(t)
	checked := 0
	for _, command := range []string{
		"cadre knowledge disposition-staged --id <id>",
		"cadre knowledge ingest-accepted --id <id>",
	} {
		if !strings.Contains(skill, command) {
			t.Errorf("the skill no longer names %q", command)
			continue
		}
		checked++
		verb := strings.Fields(strings.TrimPrefix(command, "cadre knowledge "))[0]
		if !IsKnowledgeStagedSubcommand(verb) {
			t.Errorf("the skill tells the agent to run %q, but %q is not a "+
				"staged-record verb the CLI offers", command, verb)
		}
	}
	if checked == 0 {
		t.Fatal("no command was checked; this test would prove nothing")
	}
}
