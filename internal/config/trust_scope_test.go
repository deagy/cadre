package config

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Which settings a project-local file may set.
//
// `.agents/cadre.yaml` arrives with `git clone`. A field marked
// ScopeGlobalOnly may never be read from there, and the enforcement is well
// covered already -- three tests in settings_test.go drive it.
//
// All three use gitlab.base_url. That tests the mechanism, not the
// classification: move roster.root to project-settable and every one of them
// still passes, while a cloned repository gains the ability to redirect which
// role prose its own reviewers are handed as operating instructions.
//
// So this pins the classification itself. Any field crossing between tiers,
// in either direction, fails here and has to be argued for.

// globalOnlyFields is the full set, written out rather than derived, because
// deriving it from FIELDS would make this test agree with whatever FIELDS
// happens to say.
//
// The common thread: every one of these redirects where cadre reads code,
// prose, or credentials from, or what it is allowed to execute. The single
// project-settable field describes a fact about the GitLab instance instead --
// it changes what cadre believes, not what it runs or reads.
var globalOnlyFields = []string{
	"agentic_sdlc.bin_path",
	"context_store.home",
	"gitlab.base_url",
	"gitlab.project_id",
	"knowledge_store.home",
	"roster.root",
	"runners.api_allow_writes",
	"runners.api_base_url",
	"runners.api_command_allowlist",
	"runners.api_key_env",
	"runners.claude_bin",
	"runners.codex_bin",
	"runners.codex_profile",
	"runners.forward_env",
	"runners.local_model_haiku",
	"runners.local_model_opus",
	"runners.local_model_sonnet",
}

var projectSettableFields = []string{
	"gitlab.supports_work_item_hierarchy",
}

func TestTheTrustScopeOfEverySettingIsWhatWeSaidItIs(t *testing.T) {
	expected := map[string]bool{}
	for _, key := range globalOnlyFields {
		expected[key] = true
	}
	for _, key := range projectSettableFields {
		expected[key] = false
	}

	var wrongTier, unregistered, missing []string
	for key, spec := range FIELDS {
		wantGlobal, known := expected[key]
		if !known {
			unregistered = append(unregistered,
				key+" (currently "+scopeName(spec.Scope)+")")
			continue
		}
		if isGlobalOnly := spec.Scope == ScopeGlobalOnly; isGlobalOnly != wantGlobal {
			wrongTier = append(wrongTier, key+" is "+scopeName(spec.Scope)+
				", expected "+map[bool]string{true: "global-only", false: "project-settable"}[wantGlobal])
		}
	}
	for key := range expected {
		if _, present := FIELDS[key]; !present {
			missing = append(missing, key)
		}
	}
	sort.Strings(wrongTier)
	sort.Strings(unregistered)
	sort.Strings(missing)

	if len(wrongTier) > 0 {
		t.Errorf("%d setting(s) changed trust tier:\n  %s\n\n"+
			"A project-local file arrives with `git clone`. Moving a field into it "+
			"is a decision about what a cloned repository may redirect.",
			len(wrongTier), strings.Join(wrongTier, "\n  "))
	}
	if len(unregistered) > 0 {
		t.Errorf("%d new setting(s) are not classified here:\n  %s\n\n"+
			"Add it to globalOnlyFields or projectSettableFields. The default "+
			"should be global-only unless the field describes a fact rather than "+
			"redirecting what cadre reads or runs.",
			len(unregistered), strings.Join(unregistered, "\n  "))
	}
	if len(missing) > 0 {
		t.Errorf("%d classified setting(s) no longer exist: %v",
			len(missing), missing)
	}
}

func scopeName(scope Scope) string {
	if scope == ScopeGlobalOnly {
		return "global-only"
	}
	return "project-settable"
}

func TestAProjectFileCannotRedirectTheRoster(t *testing.T) {
	// The concrete case, asserted on roster.root itself rather than through
	// gitlab.base_url standing in for it.
	//
	// roster.root selects the role *prose* an agent is handed as its operating
	// instructions. Of everything in the registry this is the most powerful
	// redirect: a cloned repository that could set it would be choosing what
	// its own reviewers are told to do.
	isolateConfigEnv(t)
	dir := makeGitCheckout(t)
	elsewhere := t.TempDir()
	writeYAML(t, filepath.Join(dir, ".agents", "cadre.yaml"),
		"roster:\n  root: \""+elsewhere+"\"\n")

	resolved, err := ResolveSetting("roster.root", dir)
	var scopeErr *SettingsScopeError
	if !errors.As(err, &scopeErr) {
		t.Fatalf("a project-local roster.root was not refused as a scope violation; "+
			"got value=%v err=%v", resolved, err)
	}
	// Refused loudly, naming both halves. Silently ignoring it would also be
	// safe, and would leave someone staring at a config file that appears to
	// do nothing.
	message := scopeErr.Error()
	for _, want := range []string{"roster.root", "cadre.yaml"} {
		if !strings.Contains(message, want) {
			t.Errorf("the refusal does not name %q: %s", want, message)
		}
	}
}

func TestEveryGlobalOnlyFieldIsRefusedFromAProjectFile(t *testing.T) {
	// The classification above is structural -- it reads Scope off the spec.
	// This is the behavioural half: for each field, a project file that sets
	// it is actually refused. A spec marked global-only that the resolver did
	// not consult would pass the first test and fail here.
	checked := 0
	for _, key := range globalOnlyFields {
		t.Run(key, func(t *testing.T) {
			isolateConfigEnv(t)
			dir := makeGitCheckout(t)

			// Nest the key the way a YAML file would spell it.
			parts := strings.SplitN(key, ".", 2)
			if len(parts) != 2 {
				t.Skipf("%s is not a two-part key", key)
			}
			writeYAML(t, filepath.Join(dir, ".agents", "cadre.yaml"),
				parts[0]+":\n  "+parts[1]+": \"anything\"\n")

			_, err := ResolveSetting(key, dir)
			var scopeErr *SettingsScopeError
			if !errors.As(err, &scopeErr) {
				t.Errorf("a project-local %s was not refused; got %T: %v", key, err, err)
			}
		})
		checked++
	}
	if checked != len(globalOnlyFields) {
		t.Fatalf("checked %d of %d fields", checked, len(globalOnlyFields))
	}
}

func TestTheRosterRootDefaultIsARealDirectory(t *testing.T) {
	// A computed default, not a null one. If it resolved to nothing, the
	// selector would fall back to whatever a caller passed -- and the point of
	// the field is that the roster location is not a caller's choice.
	isolateConfigEnv(t)
	fieldSpec, err := spec("roster.root")
	if err != nil {
		t.Fatal(err)
	}
	if fieldSpec.ComputedDefault == nil {
		t.Fatal("roster.root has no computed default")
	}
	value, ok := fieldSpec.ComputedDefault()
	if !ok {
		t.Skip("no roster directory discoverable from here")
	}
	path, isString := value.(string)
	if !isString || path == "" {
		t.Fatalf("the computed default is %#v, not a path", value)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the computed default %q does not exist: %v", path, err)
	}
	if !info.IsDir() {
		t.Errorf("the computed default %q is not a directory", path)
	}
}
