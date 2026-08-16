package selector

import (
	"path/filepath"
	"strings"
	"testing"
)

// dispatch_disposition: whether anybody in this plan can actually be sent.
//
// A plan can name agents and still have nobody accountable. `support` roles
// advise -- they do not author a change or review one -- so a selection that
// filled only that group is a plan an orchestrator must not act on alone.
//
// The field exists because that case was once indistinguishable from a fully
// staffed one. Exporting a backlog and then deleting the source issues matched
// only a generic "delete" keyword, landing three advisers in `support` with no
// primary and no reviewer; nothing in the plan said so, and an orchestrator
// could perform the destructive step itself with no structured reason
// surfaced.
//
// selection_test.go covers the four branches as a unit. These cover the
// consequence: what the disposition says about a plan built from the shipped
// routing, including when lifecycle gates are in play.
//
// Ported from test_selector.py's DispatchDispositionTests.

func planForDisposition(t *testing.T, task string, gates []LifecycleGate) map[string]any {
	t.Helper()
	options := PlanOptions{
		Catalog:    loadCatalogIDs(t),
		RosterRoot: filepath.Join(selectorRepoRoot(t), "roster"),
	}
	if gates != nil {
		options.Gates = gates
	}
	plan, err := BuildDispatchPlan(loadRoutingConfig(t), PlanInput{
		Task: task, TaskID: "DISPOSITION", Classification: "internal",
		RepositoryRoot: "<REPO_ROOT>", ChangedFileSource: "explicit",
	}, options)
	if err != nil {
		t.Fatalf("BuildDispatchPlan: %v", err)
	}
	return plan
}

func dispositionOf(t *testing.T, plan map[string]any) (status, reason string) {
	t.Helper()
	disposition, ok := plan["dispatch_disposition"].(Disposition)
	if !ok {
		object := objectOf(plan["dispatch_disposition"])
		status, _ = object["status"].(string)
		reason, _ = object["reason"].(string)
		return status, reason
	}
	return disposition.Status, disposition.Reason
}

func TestASupportOnlySelectionIsAdvisoryRatherThanStaffed(t *testing.T) {
	// The regression this field was added for. The task pairs an ordinary
	// export with a destructive step, and matches only a generic keyword --
	// so three advisers land in support and nobody is accountable.
	plan := planForDisposition(t,
		"Export the GitLab issues to a local backlog artifact, then delete the GitLab issues",
		nil)

	agents := planAgents(t, plan)
	if len(agents.Primary) != 0 || len(agents.Reviewers) != 0 {
		t.Fatalf("the task now staffs somebody (primary=%v reviewers=%v), so this "+
			"case no longer exercises the support-only path",
			agents.Primary, agents.Reviewers)
	}
	if len(agents.Support) == 0 {
		t.Fatal("no support role was selected either; the case matches nothing now")
	}

	status, reason := dispositionOf(t, plan)
	if status != "advisory-only" {
		t.Errorf("disposition = %q, want advisory-only. A support-only selection "+
			"that reads as staffed lets an orchestrator perform the destructive "+
			"step itself.", status)
	}
	// The reason names the roles, because an orchestrator surfaces this text
	// to a human before deciding whether to proceed without a dispatch.
	for _, role := range agents.Support {
		if !strings.Contains(reason, role) {
			t.Errorf("the reason does not name the support role %q: %s", role, reason)
		}
	}
	// And the plan's own status still reads ready -- agents were selected. The
	// disposition is the field that distinguishes them, which is exactly why
	// it exists.
	if plan["status"] != "ready" {
		t.Errorf("status = %v; the distinction being tested is between status "+
			"and disposition, so this case has moved", plan["status"])
	}
}

func TestGateAgentsInSupportDoNotMakeAPlanLookStaffed(t *testing.T) {
	// The same risk by a second path. With lifecycle gates supplied, the gate
	// machinery can add a default review agent to `support` for a route that
	// has none of its own. Landing in support is correct -- it advises -- but
	// conflating it with a real reviewers-group role would flip the
	// disposition to staffed and lose the warning.
	contract := loadLifecycleContract(t)
	task := "Discuss the new component's platform topology"

	standalone := planForDisposition(t, task, nil)
	integrated := planForDisposition(t, task, contract.Gates)

	for name, plan := range map[string]map[string]any{
		"standalone": standalone, "gate-integrated": integrated,
	} {
		agents := planAgents(t, plan)
		if len(agents.Primary) != 0 || len(agents.Reviewers) != 0 {
			t.Fatalf("%s: the task staffs somebody (primary=%v reviewers=%v); this "+
				"case has moved", name, agents.Primary, agents.Reviewers)
		}
		if status, _ := dispositionOf(t, plan); status != "advisory-only" {
			t.Errorf("%s: disposition = %q, want advisory-only", name, status)
		}
	}

	// The reviewer-in-support only appears on the gate-integrated path, and
	// that asymmetry is the case rather than an inconvenience: the standalone
	// plan has no gate machinery to add one. Asserting it of both would have
	// been wrong, and asserting it of neither would leave the conflation this
	// guards against unreachable.
	standaloneSupport := planAgents(t, standalone).Support
	integratedSupport := planAgents(t, integrated).Support
	if contains(standaloneSupport, "code-reviewer") {
		t.Errorf("code-reviewer is in support without gates (%v); the gate path is "+
			"no longer what introduces it, so this case has moved", standaloneSupport)
	}
	if !contains(integratedSupport, "code-reviewer") {
		t.Errorf("supplying gates did not add a review agent to support (%v), so "+
			"the conflation this guards against cannot occur here", integratedSupport)
	}

	// Supplying gates changes the lifecycle status but not the verdict on who
	// can be dispatched.
	if objectOf(integrated["lifecycle_tracking"])["status"] != "integrated" {
		t.Errorf("gates were supplied but lifecycle_tracking = %v",
			integrated["lifecycle_tracking"])
	}
}

func TestASelectionWithARealReviewerIsStaffed(t *testing.T) {
	// The contrast, without which every case above is satisfied by a
	// disposition hardcoded to advisory-only. A reviewer in the reviewers
	// group is an accountable independent reviewer, and the plan says so.
	plan := planForDisposition(t,
		"Update the React navigation for keyboard accessibility", nil)
	agents := planAgents(t, plan)
	if len(agents.Primary) == 0 && len(agents.Reviewers) == 0 {
		t.Fatal("the task staffs nobody, so this contrast proves nothing")
	}
	status, reason := dispositionOf(t, plan)
	if status != "staffed" {
		t.Errorf("disposition = %q, want staffed", status)
	}
	if !strings.Contains(reason, "primary") && !strings.Contains(reason, "reviewer") {
		t.Errorf("the staffed reason explains nothing: %s", reason)
	}
}

func TestAPlanThatMatchedNothingSaysNoAgentsWereSelected(t *testing.T) {
	// Distinct from advisory-only: there is nobody to advise either. Reporting
	// the two the same way would send a reader looking for support roles that
	// do not exist.
	plan := planForDisposition(t, "Recalibrate the quantum tachyon manifold", nil)
	agents := planAgents(t, plan)
	if len(agents.Primary)+len(agents.Reviewers)+len(agents.Support) != 0 {
		t.Fatalf("the task selected agents (%+v); it no longer matches nothing", agents)
	}
	status, reason := dispositionOf(t, plan)
	if status != "no-agents-selected" {
		t.Errorf("disposition = %q, want no-agents-selected", status)
	}
	if strings.TrimSpace(reason) == "" {
		t.Error("no reason was given for an unstaffed plan")
	}
	if plan["status"] != "needs-triage" {
		t.Errorf("status = %v, want needs-triage", plan["status"])
	}
}
