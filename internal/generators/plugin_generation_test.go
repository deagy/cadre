package generators

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repositoryRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("cannot resolve working directory: %v", err)
	}
	return filepath.Join(wd, "..", "..")
}

func TestPyJSONString(t *testing.T) {
	// ensure_ascii=True is the whole point: anything outside 0x20..0x7e must
	// come out as a backslash-u escape, with a surrogate pair above the BMP.
	// The role instructions embedded into every Codex .toml wrapper are full of
	// em dashes, so getting this wrong diverges on 159 files at once.
	//
	// Note the deliberate asymmetry below: the input column is an interpreted
	// literal (a real character) while the expectation is a raw literal (the
	// six-character escape text). They read alike on purpose -- producing that
	// text from that character is exactly what is under test.
	cases := []struct{ input, want string }{
		{"plain", `"plain"`},
		{"quote\"and\\slash", `"quote\"and\\slash"`},
		{"line\nbreak\ttab", `"line\nbreak\ttab"`},
		{"em \u2014 dash", `"em \u2014 dash"`},
		{"\u0001", `"\u0001"`},
		{"\u007f", `"\u007f"`},
		{"emoji \U0001f600", `"emoji \ud83d\ude00"`},
		// Go's encoding/json escapes <, > and &; Python does not.
		{"angle <b> & quotes", `"angle <b> & quotes"`},
	}
	for _, testCase := range cases {
		if got := pyJSONString(testCase.input); got != testCase.want {
			t.Errorf("pyJSONString(%q) = %s, want %s", testCase.input, got, testCase.want)
		}
	}
}

func TestPyJSONDumpsMatchesPythonIndentTwo(t *testing.T) {
	payload := newOrderedJSON().
		Set("schema_version", 1).
		Set("agents", newOrderedJSON().
			Set("b-role", newOrderedJSON().
				Set("capabilities", []string{"author", "dispatch"}).
				Set("definition", "roles/build/b-role/AGENT.md")).
			Set("empty", newOrderedJSON()).
			Set("none", []any{}))

	want := strings.Join([]string{
		"{",
		`  "schema_version": 1,`,
		`  "agents": {`,
		`    "b-role": {`,
		`      "capabilities": [`,
		`        "author",`,
		`        "dispatch"`,
		`      ],`,
		`      "definition": "roles/build/b-role/AGENT.md"`,
		"    },",
		`    "empty": {},`,
		`    "none": []`,
		"  }",
		"}",
	}, "\n")

	if got := pyJSONDumps(payload, 0); got != want {
		t.Errorf("pyJSONDumps mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestParseOrderedJSONPreservesKeyOrder(t *testing.T) {
	parsed, err := parseOrderedJSON([]byte(`{"z": 1, "a": {"y": "v", "b": [1, 2]}}`))
	if err != nil {
		t.Fatalf("parseOrderedJSON: %v", err)
	}
	if got := strings.Join(parsed.Keys(), ","); got != "z,a" {
		t.Errorf("top-level key order = %q, want %q", got, "z,a")
	}
	nested, _ := parsed.Get("a")
	if got := strings.Join(nested.(*orderedJSON).Keys(), ","); got != "y,b" {
		t.Errorf("nested key order = %q, want %q", got, "y,b")
	}
	// Numbers must round-trip as their literal token, not as a float.
	if !strings.Contains(pyJSONDumps(parsed, 0), `"z": 1,`) {
		t.Errorf("integer did not round-trip as 1: %s", pyJSONDumps(parsed, 0))
	}
}

func TestParseCatalogEntries(t *testing.T) {
	content := strings.Join([]string{
		"agents:",
		"  alpha-role:",
		"    definition: build/alpha-role/AGENT.md",
		"    phase: build",
		"    capability: code_author",
		"    model: sonnet",
		"    unrelated: ignored",
		"  beta-role:",
		"    definition: review/beta-role/AGENT.md",
		"    capability: read_only",
		"",
	}, "\n")

	entries, err := parseCatalogEntries(content)
	if err != nil {
		t.Fatalf("parseCatalogEntries: %v", err)
	}
	if got := strings.Join(entries.Keys(), ","); got != "alpha-role,beta-role" {
		t.Fatalf("ids = %q", got)
	}
	raw, _ := entries.Get("alpha-role")
	alpha := raw.(map[string]string)
	if alpha["definition"] != "build/alpha-role/AGENT.md" || alpha["model"] != "sonnet" {
		t.Errorf("alpha-role metadata mismatch: %v", alpha)
	}
	if _, present := alpha["unrelated"]; present {
		t.Errorf("unlisted field must be ignored, got %v", alpha)
	}
}

func TestParseCatalogEntriesRejectsDuplicateID(t *testing.T) {
	_, err := parseCatalogEntries("  dup:\n    phase: build\n  dup:\n    phase: review\n")
	if err == nil || !strings.Contains(err.Error(), "duplicate id") {
		t.Fatalf("expected a duplicate-id error, got %v", err)
	}
}

func TestSplitPolicySectionsIgnoresFencedHeadings(t *testing.T) {
	body := strings.Join([]string{
		"Preamble text.",
		"",
		"## Real heading",
		"body",
		"",
		"```sh",
		"## not a heading",
		"```",
		"",
		"## Second heading",
		"more",
	}, "\n")

	preamble, sections, err := splitPolicySections(body)
	if err != nil {
		t.Fatalf("splitPolicySections: %v", err)
	}
	if preamble != "Preamble text." {
		t.Errorf("preamble = %q", preamble)
	}
	if len(sections) != 2 {
		t.Fatalf("expected 2 sections, got %d", len(sections))
	}
	if sections[0].heading != "Real heading" || sections[1].heading != "Second heading" {
		t.Errorf("headings = %q, %q", sections[0].heading, sections[1].heading)
	}
	if !strings.Contains(sections[0].text, "## not a heading") {
		t.Errorf("fenced heading should stay inside its section: %q", sections[0].text)
	}
}

func TestSplitPolicySectionsRejectsUnbalancedFence(t *testing.T) {
	if _, _, err := splitPolicySections("intro\n\n```sh\nnever closed\n"); err == nil {
		t.Fatal("expected an unbalanced-fence error")
	}
}

func TestExcerptUniversalSectionsOnRealPolicy(t *testing.T) {
	relative := "roster/shared/workspace-isolation.md"
	raw, err := os.ReadFile(filepath.Join(repositoryRoot(t), filepath.FromSlash(relative)))
	if err != nil {
		t.Skipf("cannot read %s: %v", relative, err)
	}
	excerpt, err := excerptUniversalSections(relative, strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("excerptUniversalSections: %v", err)
	}
	for _, heading := range universalPolicySections[relative] {
		if !strings.Contains(excerpt, "## "+heading) {
			t.Errorf("excerpt is missing universally binding section %q", heading)
		}
	}
	if len(excerpt) >= len(strings.TrimSpace(string(raw))) {
		t.Errorf("excerpt should be shorter than the full file")
	}
}

func TestDeriveKind(t *testing.T) {
	cases := map[string]string{
		"review/code-reviewer/AGENT.md":           "reviewer",
		"engineering/test-engineer/AGENT.md":      "reviewer",
		"support/escalation-manager/AGENT.md":     "specialist",
		"documentation/evidence-curator/AGENT.md": "specialist",
		"knowledge-store/AGENT.md":                "specialist",
		"engineering/backend-engineer/AGENT.md":   "author",
	}
	for definition, want := range cases {
		if got := deriveKind(definition); got != want {
			t.Errorf("deriveKind(%q) = %q, want %q", definition, got, want)
		}
	}
}

func TestFrontmatterHelpers(t *testing.T) {
	text := "---\nid: sample\ndescription: has --- inside\n---\nbody line\n"
	if !IsMigrated(text) {
		t.Fatal("expected migrated text")
	}
	end, ok, err := FrontmatterClosingDelimiterEnd(text)
	if err != nil || !ok {
		t.Fatalf("FrontmatterClosingDelimiterEnd: ok=%v err=%v", ok, err)
	}
	// Must land after the real closing delimiter line, not the "---" embedded
	// in the description value.
	if text[:end] != "---\nid: sample\ndescription: has --- inside\n---" {
		t.Errorf("closing delimiter offset landed wrong: %q", text[:end])
	}
	body, err := StripFrontmatter(text)
	if err != nil {
		t.Fatalf("StripFrontmatter: %v", err)
	}
	if body != "body line\n" {
		t.Errorf("StripFrontmatter = %q", body)
	}
	if got, _ := StripFrontmatter("no frontmatter\n"); got != "no frontmatter\n" {
		t.Errorf("StripFrontmatter must be a no-op on unmigrated text, got %q", got)
	}
}

func TestRewritePackagedMarkdown(t *testing.T) {
	pluginRoot := "/pkg"
	targetParent := "/pkg/suite/roster"
	source := strings.Join([]string{
		"See ../bin/cadre and [root](../README.md).",
		"Skill: ../.agents/skills/run-agent-orchestration/SKILL.md",
		"Lifecycle skill: ../.agents/skills/lifecycle-review/SKILL.md",
		"Changelog: [notes](../CHANGELOG.md)",
		"",
	}, "\n")

	got, err := rewritePackagedMarkdown(source, "roster/README.md", pluginRoot, targetParent)
	if err != nil {
		t.Fatalf("rewritePackagedMarkdown: %v", err)
	}
	if !strings.HasPrefix(got, generatedMarker+"\n\n") {
		t.Errorf("missing generated marker prefix: %q", got[:60])
	}
	if !strings.Contains(got, "../../bin/cadre") {
		t.Errorf("bin/cadre link not re-depthed: %s", got)
	}
	if !strings.Contains(got, "../../skills/run-agent-orchestration/SKILL.md") {
		t.Errorf("default skill link not rewritten: %s", got)
	}
	if !strings.Contains(got, "../../plugins/lifecycle/skills/lifecycle-review/SKILL.md") {
		t.Errorf("retargeted skill link not rewritten: %s", got)
	}
	if !strings.Contains(got, registerURL+"/blob/main/CHANGELOG.md") {
		t.Errorf("changelog link not pointed at the register: %s", got)
	}
}

func TestPackagedSubcommandsExcludesRegisterOnlyEntries(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	table := strings.Join([]string{
		"select\tSelect agents",
		"generate-plugin\tRegenerate",
		"config\tSettings",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(root, "bin", "subcommands.tsv"), []byte(table), 0o644); err != nil {
		t.Fatalf("write table: %v", err)
	}

	rows, err := packagedSubcommands(root)
	if err != nil {
		t.Fatalf("packagedSubcommands: %v", err)
	}
	if len(rows) != 2 || rows[0].name != "select" || rows[1].name != "config" {
		t.Fatalf("unexpected rows: %+v", rows)
	}
}

// TestGeneratePluginPackage exercises the whole generator against the real
// repository. It is the closest thing this package has to the CI
// `generated-content` job, minus the comparison against committed output.
func TestGeneratePluginPackage(t *testing.T) {
	root := repositoryRoot(t)
	if _, err := os.Stat(filepath.Join(root, "roster", "catalog.yaml")); err != nil {
		t.Skipf("not running inside a source checkout: %v", err)
	}
	output := filepath.Join(t.TempDir(), "cadre")

	result, err := RunGeneratePlugin(root, output, GeneratePluginOptions{})
	if err != nil {
		t.Fatalf("RunGeneratePlugin: %v", err)
	}
	if len(result.Written) < 500 {
		t.Errorf("expected a full package, got %d files", len(result.Written))
	}
	if !result.WroteReadme {
		t.Errorf("a fresh target must receive the register's README.md")
	}

	// Spot-check one file from every generated category the packaged plugin
	// needs to be usable: a missing category ships a plugin that installs but
	// cannot do its job.
	required := []string{
		"README.md",
		"bin/cadre",
		"hooks/hooks.json",
		"hooks/guard",
		"hooks/bin/cadre-guard-linux-amd64",
		"provider.json",
		"agent-catalog.json",
		"agents/backend-engineer.md",
		"codex-agents/agents-backend-engineer.toml",
		"suite/README.md",
		"suite/roster/catalog.yaml",
		"suite/roster/orchestration/routing.json",
		"skills/run-agent-orchestration/SKILL.md",
		"plugins/lifecycle/skills/lifecycle-review/SKILL.md",
		"plugins/lifecycle/tools/kernel-compatibility.json",
		"plugins/lifecycle-github/bin/agentic-sdlc",
		"plugins/lifecycle-gitlab/skills/cadre-install-kernel/SKILL.md",
	}
	for _, relative := range required {
		if _, err := os.Stat(filepath.Join(output, filepath.FromSlash(relative))); err != nil {
			t.Errorf("missing generated file %s: %v", relative, err)
		}
	}

	// agent-catalog.json's definitions must be re-pointed at the package's own
	// suite/roster/ tree; the kernel rejects a definition that escapes the
	// directory holding the catalog.
	catalog, err := os.ReadFile(filepath.Join(output, "agent-catalog.json"))
	if err != nil {
		t.Fatalf("cannot read packaged agent-catalog.json: %v", err)
	}
	if strings.Contains(string(catalog), `"`+providerDefinitionPrefix) {
		t.Errorf("packaged agent-catalog.json still carries register-relative definitions")
	}
	if !strings.Contains(string(catalog), `"`+packageDefinitionPrefix) {
		t.Errorf("packaged agent-catalog.json has no package-relative definitions")
	}

	// The generator must be deterministic: --check against what it just wrote
	// has to pass.
	checked, err := RunGeneratePlugin(root, output, GeneratePluginOptions{Check: true})
	if err != nil {
		t.Fatalf("RunGeneratePlugin --check: %v", err)
	}
	if !checked.Current {
		t.Errorf("generator is not deterministic; differences: %v", checked.Differences)
	}
}

func TestGeneratePluginRejectsNonEmptyForeignOutput(t *testing.T) {
	root := repositoryRoot(t)
	output := t.TempDir()
	if err := os.WriteFile(filepath.Join(output, "stray.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := RunGeneratePlugin(root, output, GeneratePluginOptions{}); err == nil {
		t.Fatal("expected a refusal to overwrite an unrelated non-empty directory")
	}
}
