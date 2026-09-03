package knowledge

import (
	"strings"
	"testing"
)

// Separation of duties must rest on who ran the command, not on what they
// typed.
//
// DispositionStagedRecord refuses when --decided-by equals the record's
// staged_by. That compares two strings the caller supplied, and
// roster/knowledge-store/SECURITY.md says so in its own voice: "an actor that
// stages as one name and decides as another satisfies both".
//
// Under the bar the previous goal was written to -- one operator, one machine
// -- that was a considered position rather than an oversight: there is nobody
// to impersonate when there is only one person. With colleagues it stops
// holding. An approval record naming a person nobody verified is a record of
// a string, and the sequence below is the whole of what it takes.
//
// observed_actor is already written beside every asserted name, at staging and
// at disposition, and until now no check consulted it. This is that check.

func TestOneObservedActorCannotStageAndApprove(t *testing.T) {
	store := testStagedStore(t)
	const recordID = "KS-20260902-separation-of-duties"

	// One machine, one process. Whatever names the caller types, the
	// observation is identical on both calls -- and it is the one thing
	// they cannot set.
	const sameSubject = "subject:alice@corp.example"
	store.observeActor = func() string { return sameSubject }

	frontmatter := testStagedFrontmatter(recordID)
	frontmatter["staged_by"] = "alice@example.com"
	if _, err := store.PutStagedRecord(frontmatter, testStagedBody); err != nil {
		t.Fatalf("cannot stage: %v", err)
	}

	_, err := store.DispositionStagedRecord(recordID, DispositionInput{
		Action:             "accepted",
		DecidedBy:          "bob@example.com",
		Reason:             "looks right to me",
		ClassificationUsed: "internal",
	})

	if err == nil {
		t.Fatal("one process staged as alice@example.com and approved as bob@example.com, " +
			"and was allowed to.\nSeparation of duties is comparing the two names, which the " +
			"caller chose, rather than the observed actor, which they did not.")
	}
	if !strings.Contains(err.Error(), "authenticated subject") {
		t.Fatalf("refused, but not for the right reason: %v\n"+
			"The refusal must name the authenticated subject, or it is the old string comparison "+
			"happening to fire on something else.", err)
	}
}

// The check must not refuse two genuinely different people.
//
// Over-refusal is the failure mode of this fix: comparing observations that
// should differ but do not would reject a colleague legitimately approving a
// proposal, and a guard that blocks correct work gets removed rather than
// corrected.
func TestTwoObservedActorsMayStageAndApprove(t *testing.T) {
	store := testStagedStore(t)
	const recordID = "KS-20260902-two-people"

	store.observeActor = func() string { return "subject:alice@corp.example" }
	frontmatter := testStagedFrontmatter(recordID)
	frontmatter["staged_by"] = "alice@example.com"
	if _, err := store.PutStagedRecord(frontmatter, testStagedBody); err != nil {
		t.Fatalf("cannot stage: %v", err)
	}

	store.observeActor = func() string { return "subject:bob@corp.example" }
	if _, err := store.DispositionStagedRecord(recordID, DispositionInput{
		Action:             "accepted",
		DecidedBy:          "bob@example.com",
		Reason:             "reviewed, agrees with the source",
		ClassificationUsed: "internal",
	}); err != nil {
		t.Fatalf("two different observed actors were refused: %v", err)
	}
}

// A local store must keep working, and must not pretend it verified anything.
//
// Without a server there is no authenticated subject: ObserveActor reads the
// OS user and git config, both of which the caller owns. Two identical
// observations there mean "the same machine", not "the same person", and one
// operator staging and approving their own proposal is the normal case rather
// than an attack. Refusing on it would remove a workflow that works today --
// AC-1 of this goal exists to stop exactly that -- so the check does not fire,
// and the CLI says the names are unverified instead.
func TestALocalStoreDoesNotRefuseTheSameMachine(t *testing.T) {
	store := testStagedStore(t)
	const recordID = "KS-20260902-local"

	store.observeActor = func() string { return "os:deagy git:test@example.com" }
	frontmatter := testStagedFrontmatter(recordID)
	frontmatter["staged_by"] = "alice@example.com"
	if _, err := store.PutStagedRecord(frontmatter, testStagedBody); err != nil {
		t.Fatalf("cannot stage: %v", err)
	}

	if _, err := store.DispositionStagedRecord(recordID, DispositionInput{
		Action:             "accepted",
		DecidedBy:          "bob@example.com",
		Reason:             "reviewed",
		ClassificationUsed: "internal",
	}); err != nil {
		t.Fatalf("a local store refused the same machine, removing the single-operator workflow: %v", err)
	}
}
