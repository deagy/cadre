package orchestration

import (
	"strings"
	"testing"
)

// These pin the gate in front of the context store, not the store itself.
//
// The store is already tested in internal/contextstore. What is ported here
// is what decides *whether a dispatched child may reach it at all*: identity
// resolution, the classification ceiling, and the source partition. Each of
// these refusals is the difference between a child reading its own material
// and a child reading somebody else's.

func TestAmbientRoleIdentityBeatsTheAssertedOne(t *testing.T) {
	// The parameter exists for a caller with no dispatch environment. Where
	// the environment does say who this is, that wins -- a child able to
	// override it could write into another role's scope, which is the whole
	// point of scoping.
	t.Setenv(RoleIDEnvVar, "code-reviewer")

	got, err := resolvedAgent("security-reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if got != "code-reviewer" {
		t.Errorf("agent = %q, want the ambient role to win over the asserted one", got)
	}
}

func TestAssertedRoleIsUsedOnlyWithNoAmbientIdentity(t *testing.T) {
	t.Setenv(RoleIDEnvVar, "")

	got, err := resolvedAgent("security-reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if got != "security-reviewer" {
		t.Errorf("agent = %q, want the asserted role when nothing is ambient", got)
	}

	if _, err := resolvedAgent(""); err == nil {
		t.Error("with neither ambient nor asserted identity the call must be refused")
	}
}

func TestAnUnattributableEntryIsRefused(t *testing.T) {
	t.Setenv(TaskIDEnvVar, "")

	if _, err := resolvedTaskID(""); err == nil {
		t.Fatal("a call with no task id anywhere must be refused, not given one")
	}

	t.Setenv(TaskIDEnvVar, "T-ambient")
	got, err := resolvedTaskID("")
	if err != nil {
		t.Fatal(err)
	}
	if got != "T-ambient" {
		t.Errorf("task id = %q, want the ambient one", got)
	}
}

func TestAClassificationCeilingMustComeFromTheServer(t *testing.T) {
	// With no ceiling set, a caller would be asserting its own -- which is
	// not a ceiling. Refusing is the only safe answer.
	if _, err := checkedClassification("internal", ""); err == nil {
		t.Fatal("with no parent classification the store must be unusable")
	} else if !strings.Contains(err.Error(), parentClassificationEnvVar) {
		t.Errorf("the refusal must name the variable to set: %v", err)
	}
}

func TestClassificationIsNarrowOnly(t *testing.T) {
	// Reuses the dispatch path's own rule rather than a second, weaker check:
	// an entry written above the ceiling is exactly the labelling error
	// dispatch already refuses.
	if _, err := checkedClassification("restricted", "internal"); err == nil {
		t.Error("a classification above the ceiling must be refused")
	}
	got, err := checkedClassification("public", "internal")
	if err != nil {
		t.Fatalf("narrowing must be allowed: %v", err)
	}
	if got != "public" {
		t.Errorf("classification = %q, want the narrowed value", got)
	}
	// An omitted classification defaults rather than inheriting the ceiling
	// silently.
	if got, err := checkedClassification("", "restricted"); err != nil || got != "internal" {
		t.Errorf("default = %q (%v), want internal", got, err)
	}
}

func TestDispatchSourceIsDerivedNeverAsserted(t *testing.T) {
	// A child that could name its own source could read another project's
	// entries, so the partition comes from the project root and nothing else.
	// Same basename, different parents. This is what the digest is for, and
	// it is the case a name-only source gets wrong: comparing
	// project-alpha with project-beta passes even with the digest stubbed to
	// a constant, because the names already differ.
	first := DispatchSource("/tmp/one/project")
	second := DispatchSource("/tmp/two/project")

	if first == second {
		t.Errorf("two roots sharing a basename must not share a source partition: %q", first)
	}
	if first != DispatchSource("/tmp/one/project") {
		t.Error("the same root must produce a stable source")
	}
	if !strings.HasPrefix(first, "dispatch-") {
		t.Errorf("source = %q, want a dispatch- prefix", first)
	}
	// A root whose basename sanitises to nothing still yields a usable name.
	if got := DispatchSource("/"); !strings.HasPrefix(got, "dispatch-") {
		t.Errorf("source = %q, want a usable fallback name", got)
	}
}

func TestRelayRefusesMoreThanItWillHandBack(t *testing.T) {
	// The Python original enforced this on the CLI subprocess's stdout. The
	// subprocess is gone; the ceiling is not.
	big := map[string]any{"blob": strings.Repeat("x", MaxContextRelayBytes+1)}
	if _, err := relay(big); err == nil {
		t.Error("a result larger than the relay cap must be refused")
	}
	if _, err := relay(map[string]any{"ok": true}); err != nil {
		t.Errorf("an ordinary result must relay: %v", err)
	}
}

func TestEveryContextToolIsDefinedAndRoutable(t *testing.T) {
	// A tool defined but unroutable lists in tools/list and fails on call; a
	// tool routable but undefined can never be called. Both are silent.
	defined := map[string]bool{}
	for _, definition := range contextToolDefinitions() {
		defined[definition.Name] = true
		if definition.Description == "" {
			t.Errorf("%s has no description", definition.Name)
		}
		if _, ok := definition.Schema["properties"]; !ok {
			t.Errorf("%s has no schema properties", definition.Name)
		}
	}

	server := &DispatchMCPServer{projectRoot: t.TempDir()}
	for _, name := range []string{"context_put", "context_get", "context_list", "context_search"} {
		if !defined[name] {
			t.Errorf("%s is not defined", name)
		}
		if _, handled := server.dispatchContextToolCall(name, []byte(`{}`)); !handled {
			t.Errorf("%s is defined but not routable", name)
		}
	}
	if _, handled := server.dispatchContextToolCall("not_a_context_tool", []byte(`{}`)); handled {
		t.Error("a non-context tool must fall through to the dispatch tools")
	}
}

func TestGateRefusalsSurfaceAsToolErrorsNotProtocolFaults(t *testing.T) {
	// A refusal is something the model can act on -- it names the variable to
	// set. Returning it as a JSON-RPC error would hide that behind a
	// transport failure.
	t.Setenv(RoleIDEnvVar, "")
	t.Setenv(TaskIDEnvVar, "")
	t.Setenv(parentClassificationEnvVar, "")

	server := &DispatchMCPServer{projectRoot: t.TempDir()}
	response, handled := server.dispatchContextToolCall("context_put", []byte(`{"label":"x","content":"y"}`))
	if !handled {
		t.Fatal("context_put must be routed")
	}
	if !response.IsError {
		t.Fatal("a gate refusal must be reported as a tool error")
	}
	if response.Error == "" {
		t.Error("a refusal must carry its reason")
	}
}
