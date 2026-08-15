package contextstore

import (
	"reflect"
	"strings"
	"testing"
)

// Promotion takes a context entry and emits a finding for the knowledge store
// to consider. It is the point where working material becomes a candidate for
// something durable and shared, so the questions are who may promote what, and
// what the caller gets to say about it.
//
// Ported from roster/context-store/test/test_promotion.py, which has 22 tests
// against 3 in Go.

func promoteOptions(handle string, caller CallerOptions) PromoteOptions {
	return PromoteOptions{
		CallerOptions:        caller,
		Handle:               handle,
		Artifact:             "docs/report.md",
		Revision:             "abc123",
		SensitivityNotes:     "none",
		ConflictsOrStaleness: "none known",
		RecommendedAction:    "ingest",
	}
}

func TestPromotingAnotherAgentsEntryIsRefused(t *testing.T) {
	// The entry is agent-scoped, so its author is the only one who may offer
	// it for ingestion. Otherwise any agent could push another's working
	// material into the shared store under its own judgement.
	cfg, _ := searchTestStore(t)
	handle := storeEntry(t, cfg, PutOptions{
		Scope: "agent", Agent: "agent-one", TaskID: "T-1",
		Classification: "internal", Source: "s", Label: "theirs",
		Content: "findings from a review",
	})

	db, err := OpenStore(cfg.Database, false)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer func() { _ = db.Close() }()

	_, err = PromoteEntry(db, promoteOptions(handle, CallerOptions{
		Agent: "agent-two", TaskID: "T-1", Classification: "internal", Source: "s",
	}))
	if err == nil {
		t.Fatal("another agent promoted an agent-scoped entry")
	}

	// The author still can.
	if _, err := PromoteEntry(db, promoteOptions(handle, CallerOptions{
		Agent: "agent-one", TaskID: "T-1", Classification: "internal", Source: "s",
	})); err != nil {
		t.Errorf("the entry's own author was refused: %v", err)
	}
}

func TestAnUnknownHandleIsRefusedWithoutRevealingWhy(t *testing.T) {
	// A caller must not be able to tell "no such entry" from "not yours". The
	// difference is an oracle: probe handles until the message changes and you
	// have enumerated what exists in a partition you cannot read.
	cfg, _ := searchTestStore(t)
	hidden := storeEntry(t, cfg, PutOptions{
		Scope: "agent", Agent: "agent-one", TaskID: "T-1",
		Classification: "internal", Source: "s", Label: "hidden",
		Content: "not yours",
	})

	db, err := OpenStore(cfg.Database, false)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer func() { _ = db.Close() }()

	caller := CallerOptions{Agent: "agent-two", TaskID: "T-1", Classification: "internal", Source: "s"}

	_, existsButUnreadable := PromoteEntry(db, promoteOptions(hidden, caller))
	_, doesNotExist := PromoteEntry(db, promoteOptions("ctx_00000000000000000000000000000000", caller))

	if existsButUnreadable == nil || doesNotExist == nil {
		t.Fatal("both cases must be refused")
	}
	// Compared with the handle removed: the messages name the handle they were
	// given, which differs by construction and is not a leak.
	unreadable := strings.ReplaceAll(existsButUnreadable.Error(), hidden, "<handle>")
	absent := strings.ReplaceAll(doesNotExist.Error(),
		"ctx_00000000000000000000000000000000", "<handle>")
	if unreadable != absent {
		t.Errorf("the two refusals differ, which tells a caller the entry exists:\n  unreadable: %s\n  absent:     %s",
			unreadable, absent)
	}
}

func TestPromotionCannotCrossClassification(t *testing.T) {
	// Reading at a lower classification than the entry carries would let a
	// caller launder it into the knowledge store under its own, lower label.
	cfg, _ := searchTestStore(t)
	handle := storeEntry(t, cfg, PutOptions{
		Scope: "agent", Agent: "a", TaskID: "T-1",
		Classification: "confidential", Source: "s", Label: "sensitive",
		Content: "sensitive findings",
	})

	db, err := OpenStore(cfg.Database, false)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := PromoteEntry(db, promoteOptions(handle, CallerOptions{
		Agent: "a", TaskID: "T-1", Classification: "internal", Source: "s",
	})); err == nil {
		t.Error("an entry was promoted under a classification it does not carry")
	}
}

func TestPromotionWritesNothingAndLeavesTheEntryInPlace(t *testing.T) {
	// Promotion emits a finding for a human and the knowledge steward to act
	// on. It is not itself an ingestion: if it wrote to the knowledge store,
	// an agent would be ingesting its own material, which is the separation
	// this whole path exists to preserve.
	cfg, _ := searchTestStore(t)
	handle := storeEntry(t, cfg, PutOptions{
		Scope: "agent", Agent: "a", TaskID: "T-1",
		Classification: "internal", Source: "s", Label: "candidate",
		Content: "a durable-looking conclusion",
	})

	db, err := OpenStore(cfg.Database, false)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer func() { _ = db.Close() }()

	finding, err := PromoteEntry(db, promoteOptions(handle, CallerOptions{
		Agent: "a", TaskID: "T-1", Classification: "internal", Source: "s",
	}))
	if err != nil {
		t.Fatalf("PromoteEntry: %v", err)
	}
	if finding == nil {
		t.Fatal("promotion returned no finding")
	}

	// The entry survives, and is marked with when it was offered -- a
	// timestamp, not a record id, because no record was created anywhere.
	bundle, err := GetEntry(db, GetOptions{
		Handle: handle,
		CallerOptions: CallerOptions{
			Agent: "a", TaskID: "T-1", Classification: "internal", Source: "s",
		},
	})
	if err != nil {
		t.Fatalf("GetEntry after promotion: %v", err)
	}
	if len(bundle.Results) != 1 {
		t.Fatalf("the entry did not survive promotion: %+v", bundle.Results)
	}
	if promoted := bundle.Results[0].PromotedAt; promoted == nil || *promoted == "" {
		t.Error("promotion was not recorded on the entry")
	}
}

func TestTheUntrustedFlagIsNotSomethingACallerAsserts(t *testing.T) {
	// The flag records that material descended from something untrusted. A
	// caller that could set or clear it could launder poisoned content by
	// declaring it clean -- so it is derived on write and carried through
	// promotion, never accepted as an argument.
	//
	// Asserted structurally: neither the promote nor the put option set has a
	// field for it. A behavioural test cannot show the absence of a parameter.
	for _, field := range []string{"UntrustedInputs", "Untrusted", "InjectionRisk"} {
		if promoteOptionsHasField(field) {
			t.Errorf("PromoteOptions has a %q field, so a caller can assert the trust flag", field)
		}
		if putOptionsHasField(field) {
			t.Errorf("PutOptions has a %q field, so a caller can assert the trust flag", field)
		}
	}
}

func TestARecommendedActionMustBeOneOfTheDeclaredOnesAndNeverDeletes(t *testing.T) {
	// The action is what a steward acts on. An invented one would be stored
	// and then mean whatever a later reader guessed -- and "delete" is
	// deliberately absent: promotion proposes, it does not remove.
	for _, action := range RecommendedActions {
		if action == "delete" || action == "remove" || action == "purge" {
			t.Errorf("%q is a destructive recommended action; promotion only ever proposes", action)
		}
	}

	cfg, _ := searchTestStore(t)
	handle := storeEntry(t, cfg, PutOptions{
		Scope: "agent", Agent: "a", TaskID: "T-1",
		Classification: "internal", Source: "s", Label: "e", Content: "content",
	})
	db, err := OpenStore(cfg.Database, false)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer func() { _ = db.Close() }()

	caller := CallerOptions{Agent: "a", TaskID: "T-1", Classification: "internal", Source: "s"}
	for _, action := range []string{"delete", "", "whatever-i-like"} {
		opts := promoteOptions(handle, caller)
		opts.RecommendedAction = action
		if _, err := PromoteEntry(db, opts); err == nil {
			t.Errorf("recommended_action %q was accepted", action)
		}
	}
}

func TestEveryJudgementFieldIsRequiredByName(t *testing.T) {
	// These are the caller's own assessment, and the finding is worth less
	// than nothing without them: a steward reading "" cannot tell "nothing to
	// note" from "nobody looked".
	cfg, _ := searchTestStore(t)
	handle := storeEntry(t, cfg, PutOptions{
		Scope: "agent", Agent: "a", TaskID: "T-1",
		Classification: "internal", Source: "s", Label: "e", Content: "content",
	})
	db, err := OpenStore(cfg.Database, false)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer func() { _ = db.Close() }()

	caller := CallerOptions{Agent: "a", TaskID: "T-1", Classification: "internal", Source: "s"}
	for _, blank := range []func(*PromoteOptions){
		func(o *PromoteOptions) { o.Artifact = "" },
		func(o *PromoteOptions) { o.Revision = "" },
		func(o *PromoteOptions) { o.SensitivityNotes = "" },
		func(o *PromoteOptions) { o.ConflictsOrStaleness = "" },
	} {
		opts := promoteOptions(handle, caller)
		blank(&opts)
		if _, err := PromoteEntry(db, opts); err == nil {
			t.Errorf("a promotion with a blank judgement field was accepted: %+v", opts)
		}
	}
}

// promoteOptionsHasField and putOptionsHasField report whether the option
// struct exposes a named field, using reflection so the check is about the
// type rather than about any particular call.
func promoteOptionsHasField(name string) bool {
	_, present := reflect.TypeOf(PromoteOptions{}).FieldByName(name)
	return present
}

func putOptionsHasField(name string) bool {
	_, present := reflect.TypeOf(PutOptions{}).FieldByName(name)
	return present
}
