package generators

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// `roster/runner-capabilities.json` as the single source of truth.
//
// Four things have to agree about what a capability tier is, what models exist
// and which reasoning efforts either runner accepts: this manifest, the plugin
// generator's capability profiles, the role-metadata generator's tier map, and
// `roster/catalog.schema.json`'s enums. Three of the four are derived from the
// first at build time; the fourth is a schema file a human edits.
//
// The failure mode is a fifth hand-copied list. Add a model tier to the
// manifest and forget the schema, and every role using it fails validation
// with a message about an enum rather than about the tier. Add one to the
// schema and forget the manifest, and the generator writes a catalog the
// schema accepts and the generator itself cannot tier.
//
// Ported from roster/orchestration/test/test_runner_capabilities.py, which
// reached into the *Python* generators for their constants -- the last of the
// four guards holding those generators alive after the Go CLI replaced them.
// The Python version compared the constants; this compares what they are
// derived from, which is the same guarantee one layer closer to the file.

func rosterRootFor(t *testing.T) string {
	t.Helper()
	root := filepath.Join(repositoryRoot(t), "roster")
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("not running inside a source checkout: %v", err)
	}
	return root
}

func loadManifest(t *testing.T) *runnerCapabilities {
	t.Helper()
	manifest, err := loadRunnerCapabilities(filepath.Join(rosterRootFor(t), "runner-capabilities.json"))
	if err != nil {
		t.Fatalf("loading the capability manifest: %v", err)
	}
	return manifest
}

// catalogSchemaEnum reads one enum out of roster/catalog.schema.json.
func catalogSchemaEnum(t *testing.T, field string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(rosterRootFor(t), "catalog.schema.json"))
	if err != nil {
		t.Fatalf("reading the catalog schema: %v", err)
	}
	var schema struct {
		Defs struct {
			Role struct {
				Properties map[string]struct {
					Enum []string `json:"enum"`
				} `json:"properties"`
			} `json:"role"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("parsing the catalog schema: %v", err)
	}
	property, present := schema.Defs.Role.Properties[field]
	if !present {
		t.Fatalf("the catalog schema declares no %s property", field)
	}
	if len(property.Enum) == 0 {
		t.Fatalf("the catalog schema's %s enum is empty", field)
	}
	return property.Enum
}

func sortedSet(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func sameSet(a, b []string) bool {
	left, right := sortedSet(a), sortedSet(b)
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func TestTheCatalogSchemaEnumsComeFromTheCapabilityManifest(t *testing.T) {
	// The schema is the one of the four a human edits by hand, so it is the
	// one that drifts. Each mismatch below has a distinct failure: a tier the
	// schema rejects makes every role using it invalid; a tier the manifest
	// lacks makes the generator unable to tier a role the schema accepted.
	manifest := loadManifest(t)

	tiers := make([]string, 0, len(manifest.CapabilityTiers))
	for tier := range manifest.CapabilityTiers {
		tiers = append(tiers, tier)
	}
	if got := catalogSchemaEnum(t, "capability"); !sameSet(got, tiers) {
		t.Errorf("capability enum %v, manifest tiers %v", sortedSet(got), sortedSet(tiers))
	}

	models := make([]string, 0, len(manifest.ModelTiers))
	codexModels := make([]string, 0, len(manifest.ModelTiers))
	for model, info := range manifest.ModelTiers {
		models = append(models, model)
		codexModels = append(codexModels, info.CodexModel)
	}
	if got := catalogSchemaEnum(t, "model"); !sameSet(got, models) {
		t.Errorf("model enum %v, manifest tiers %v", sortedSet(got), sortedSet(models))
	}
	if got := catalogSchemaEnum(t, "codex_model"); !sameSet(got, codexModels) {
		t.Errorf("codex_model enum %v, manifest codex models %v",
			sortedSet(got), sortedSet(codexModels))
	}
	if got := catalogSchemaEnum(t, "reasoning_effort"); !sameSet(got, manifest.AllowedReasoningEfforts) {
		t.Errorf("reasoning_effort enum %v, manifest efforts %v",
			sortedSet(got), sortedSet(manifest.AllowedReasoningEfforts))
	}
}

func TestEveryRoleInTheCatalogUsesATierTheManifestDefines(t *testing.T) {
	// The schema agreeing with the manifest is necessary and not sufficient:
	// the committed catalog is what the generator actually reads, and a role
	// carrying a tier neither knows about is one nothing can render.
	manifest := loadManifest(t)
	data, err := os.ReadFile(filepath.Join(rosterRootFor(t), "catalog.yaml"))
	if err != nil {
		t.Fatalf("reading the catalog: %v", err)
	}

	efforts := map[string]bool{}
	for _, effort := range manifest.AllowedReasoningEfforts {
		efforts[effort] = true
	}
	codexModels := map[string]bool{}
	for _, info := range manifest.ModelTiers {
		codexModels[info.CodexModel] = true
	}

	role := ""
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "    ") &&
			strings.HasSuffix(strings.TrimRight(line, " \t"), ":") {
			role = strings.TrimSuffix(trimmed, ":")
			continue
		}
		key, value, found := strings.Cut(trimmed, ":")
		if !found || role == "" {
			continue
		}
		value = strings.TrimSpace(value)
		switch key {
		case "capability":
			if _, known := manifest.CapabilityTiers[value]; !known {
				t.Errorf("%s declares capability %q, which the manifest does not define", role, value)
			}
		case "model":
			if _, known := manifest.ModelTiers[value]; !known {
				t.Errorf("%s declares model %q, which the manifest does not define", role, value)
			}
		case "codex_model":
			if !codexModels[value] {
				t.Errorf("%s declares codex_model %q, which no manifest tier maps to", role, value)
			}
		case "reasoning_effort":
			if !efforts[value] {
				t.Errorf("%s declares reasoning_effort %q, which the manifest does not allow",
					role, value)
			}
		}
	}
	if role == "" {
		t.Fatal("no roles were read from the catalog; this test would prove nothing")
	}
}

func TestACapabilityManifestThatCannotBeTrustedFailsClosed(t *testing.T) {
	// Every one of these would otherwise let the generator fall back to
	// whatever it last had -- which is how a tier silently keeps its old tools
	// or sandbox mode after somebody removed it.
	for _, probe := range []struct {
		name    string
		content string
		wants   string
	}{
		{"no capability tiers", `{"model_tiers":{},"allowed_reasoning_efforts":[]}`,
			"capability_tiers"},
		{"no model tiers", `{"capability_tiers":{},"allowed_reasoning_efforts":[]}`,
			"model_tiers"},
		{"no reasoning efforts", `{"capability_tiers":{},"model_tiers":{}}`,
			"allowed_reasoning_efforts"},
		{"not JSON at all", `{`, "invalid JSON"},
	} {
		t.Run(probe.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "runner-capabilities.json")
			if err := os.WriteFile(path, []byte(probe.content), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := loadRunnerCapabilities(path)
			if err == nil {
				t.Fatalf("accepted a manifest it cannot use: %s", probe.content)
			}
			if !strings.Contains(err.Error(), probe.wants) {
				t.Errorf("refused for a different reason than this case is about.\n"+
					"wanted something naming %q, got: %v", probe.wants, err)
			}
		})
	}

	t.Run("a manifest that is not there at all", func(t *testing.T) {
		_, err := loadRunnerCapabilities(filepath.Join(t.TempDir(), "absent.json"))
		if err == nil {
			t.Error("a missing manifest was accepted")
		}
	})
}

func TestWriteCapableTiersAreDerivedFromSandboxModeRatherThanNamed(t *testing.T) {
	// Which tiers may edit a repository decides which wrappers carry the
	// worktree-isolation steps. Deriving it from each tier's sandbox_mode
	// means adding a tier to the manifest is enough; a hardcoded list of tier
	// names would silently treat a new tier as read-only.
	manifest := loadManifest(t)
	writeCapable := manifest.writeCapableTiers()
	if len(writeCapable) == 0 {
		t.Fatal("no tier is write-capable; this test would prove nothing")
	}
	for tier, profile := range manifest.CapabilityTiers {
		readOnly := profile.SandboxMode == "read-only"
		if writeCapable[tier] == readOnly {
			t.Errorf("%s has sandbox_mode %q and is classified write-capable=%v",
				tier, profile.SandboxMode, writeCapable[tier])
		}
	}

	// And a tier nobody has seen before, with a writing sandbox mode, is
	// write-capable without anything being added to a list.
	invented := &runnerCapabilities{CapabilityTiers: map[string]capabilityProfile{
		"read_only":      {SandboxMode: "read-only"},
		"newly_invented": {SandboxMode: "workspace-write"},
	}}
	derived := invented.writeCapableTiers()
	if !derived["newly_invented"] {
		t.Error("a new writing tier was not recognised as write-capable")
	}
	if derived["read_only"] {
		t.Error("a read-only tier was classified as write-capable")
	}
}

func TestThePackagedPluginCarriesTheCapabilityManifest(t *testing.T) {
	// The plugin's own tooling reads this manifest at runtime, from inside the
	// installed package. A distribution that shipped without it, or with a
	// stale copy, gives an installed plugin a different idea of what a
	// capability tier is than the repository it was generated from.
	//
	// Checked against the committed distribution rather than by driving the
	// generator into a fixture repository, which is what the Python guard did:
	// the committed copy is what people install, and `cadre generate-plugin
	// --check` already fails if it drifts from what the generator would emit.
	root := repositoryRoot(t)
	for _, name := range []string{
		"runner-capabilities.json",
		"runner-capabilities.schema.json",
	} {
		t.Run(name, func(t *testing.T) {
			source, err := os.ReadFile(filepath.Join(root, "roster", name))
			if err != nil {
				t.Fatalf("reading the source: %v", err)
			}
			packaged, err := os.ReadFile(filepath.Join(root, "plugin", "suite", "roster", name))
			if err != nil {
				t.Fatalf("the packaged distribution does not carry it: %v", err)
			}
			if string(packaged) != string(source) {
				t.Errorf("the packaged copy differs from roster/%s -- an installed "+
					"plugin would disagree with this repository about capability tiers", name)
			}
		})
	}
}
