package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fail-closed retrieval contract, driven through the command line.
//
// internal/knowledge/testdata/fail-closed-contract.json is the authority for
// what a governed retrieval refuses. It was captured from the engine while
// the engine still existed, and recall/govern is held to the same file on its
// side. This file holds the third party to that agreement: the CLI, which is
// where every real caller is.
//
// It is not enough that govern refuses these. cadre's own history is the
// argument: the library refused an unscoped read, and the CLI translated an
// omitted --source into an explicit "span every source" on the way past, so
// the library's gate was unreachable from the command line. A contract proven
// only one layer below the caller is a contract with a hole in it.
const cliContractPath = "../knowledge/testdata/fail-closed-contract.json"

type cliContractCase struct {
	Name           string   `json:"name"`
	Why            string   `json:"why"`
	Query          string   `json:"query"`
	Classification string   `json:"classification"`
	Provider       bool     `json:"provider"`
	AllSources     bool     `json:"all_sources"`
	SourceFilters  []string `json:"source_filters"`
	ExpectRefusal  string   `json:"expect_refusal"`
}

func loadCLIContract(t *testing.T) []cliContractCase {
	t.Helper()
	raw, err := os.ReadFile(cliContractPath)
	if err != nil {
		t.Fatalf("read %s: %v", cliContractPath, err)
	}
	var file struct {
		Cases []cliContractCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("parse %s: %v", cliContractPath, err)
	}
	if len(file.Cases) == 0 {
		t.Fatalf("%s holds no cases; this guard would assert nothing", cliContractPath)
	}
	return file.Cases
}

// argsFor turns a contract case into the command line that expresses it.
//
// Cases run through KnowledgeCmd rather than the handler, because the
// dispatcher resolves configuration and one of the refusals now lives there:
// the engine took an embedding provider per search, recall takes its embedder
// at construction, and a config naming no provider is how that case is
// expressed at the command line.
func argsFor(cfgPath string, c cliContractCase) []string {
	args := []string{"--config", cfgPath, "search"}
	if c.Classification != "" {
		args = append(args, "--classification", c.Classification)
	}
	if c.AllSources {
		args = append(args, "--all-sources")
	}
	for _, source := range c.SourceFilters {
		args = append(args, "--source", source)
	}
	if c.Query != "" {
		args = append(args, c.Query)
	}
	return args
}

// captureStderr runs fn with os.Stderr redirected and returns what it wrote.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	previous := os.Stderr
	os.Stderr = file
	defer func() {
		os.Stderr = previous
		_ = file.Close()
	}()
	fn()
	if err := file.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	data, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	return string(data)
}

// TestTheCLIRefusesEveryContractCase is the acceptance evidence for the
// migration: the six refusals the engine enforced are still enforced by the
// path that replaced it.
func TestTheCLIRefusesEveryContractCase(t *testing.T) {
	for _, c := range loadCLIContract(t) {
		t.Run(c.Name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "store.db")
			cfgPath := writeStoreConfig(t, dbPath, contractConfig(c))

			var code int
			stderr := captureStderr(t, func() { code = KnowledgeCmd(argsFor(cfgPath, c)) })

			if code == 0 {
				t.Fatalf("%s was served, not refused.\nwhy this matters: %s", c.Name, c.Why)
			}
			if !strings.Contains(stderr, c.ExpectRefusal) {
				t.Errorf("refusal does not name the reason.\nwant substring: %q\ngot: %s",
					c.ExpectRefusal, stderr)
			}
		})
	}
}

// TestEveryContractCaseIsRefusedBeforeTheStoreIsOpened.
//
// The refusals must happen before anything is touched: an interface that only
// refuses after opening a connection has already revealed that the caller
// asked. The store path in each case does not exist, and recall's SQLite
// store creates its file on open -- so the file appearing is proof the
// refusal came too late.
func TestEveryContractCaseIsRefusedBeforeTheStoreIsOpened(t *testing.T) {
	for _, c := range loadCLIContract(t) {
		t.Run(c.Name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "store.db")
			cfgPath := writeStoreConfig(t, dbPath, contractConfig(c))

			captureStderr(t, func() { _ = KnowledgeCmd(argsFor(cfgPath, c)) })

			if _, err := os.Stat(dbPath); err == nil {
				t.Fatalf("%s opened the store before refusing: %s exists", c.Name, dbPath)
			}
		})
	}
}

// TestNoContractCaseIsAudited: a refused request is not a retrieval, so it
// leaves no audit row. A refusal that recorded one would make the log a
// record of questions asked rather than of content served.
func TestNoContractCaseIsAudited(t *testing.T) {
	for _, c := range loadCLIContract(t) {
		t.Run(c.Name, func(t *testing.T) {
			dir := t.TempDir()
			dbPath := filepath.Join(dir, "store.db")
			cfgPath := writeStoreConfig(t, dbPath, contractConfig(c))

			captureStderr(t, func() { _ = KnowledgeCmd(argsFor(cfgPath, c)) })

			if _, err := os.Stat(filepath.Join(dir, "retrievals.jsonl")); err == nil {
				t.Fatalf("%s wrote an audit row for a refused request", c.Name)
			}
		})
	}
}

// contractConfig expresses the case's provider requirement as configuration.
func contractConfig(c cliContractCase) map[string]any {
	if c.Provider {
		return nil
	}
	return map[string]any{
		"embedding": map[string]any{"provider": "", "model": "", "dimensions": 128.0},
	}
}

// TestTheContractIsCoveredCaseForCase guards the fixture itself. A refusal
// quietly dropped from the contract would otherwise make this file weaker
// without failing anything.
func TestTheContractIsCoveredCaseForCase(t *testing.T) {
	expected := map[string]bool{
		"query is required":              false,
		"classification is required":     false,
		"embedding provider is required": false,
		"source scope is required":       false,
		"source scope is ambiguous":      false,
		"must be non-empty":              false,
	}
	for _, c := range loadCLIContract(t) {
		if _, known := expected[c.ExpectRefusal]; !known {
			t.Errorf("%s expects an unknown refusal %q; add it here deliberately",
				c.Name, c.ExpectRefusal)
			continue
		}
		expected[c.ExpectRefusal] = true
	}
	for refusal, covered := range expected {
		if !covered {
			t.Errorf("the contract no longer carries a case for %q", refusal)
		}
	}
}
