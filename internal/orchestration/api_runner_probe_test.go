package orchestration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// Env-gated parity probe for the API runner, driven by
// roster/orchestration/test/probe_api_runner_parity.py.
//
// The runner cannot be compared the way `select` was: it has no byte-exact
// output contract, and its behaviour depends on what a model says. So the
// comparison is over *effects and decisions* against a scripted endpoint --
// which files exist afterwards and with what contents, which commands ran,
// how the dispatch was classified -- and deliberately not over error prose,
// which Go and Python word differently without disagreeing.
//
// Each side stands up its own server from the same canned response list, so
// neither depends on the other's ordering.

type apiProbeScenario struct {
	Name             string            `json:"name"`
	Why              string            `json:"why"`
	Responses        []string          `json:"responses"`
	Files            map[string]string `json:"files"`
	WritesAllowed    bool              `json:"writes_allowed"`
	CommandAllowlist []string          `json:"command_allowlist"`
	CapabilityTools  []string          `json:"capability_tools"`
	TimeoutSeconds   float64           `json:"timeout_seconds"`
}

// apiProbeOutcome is what both implementations are compared on.
type apiProbeOutcome struct {
	Name         string            `json:"name"`
	Unavailable  bool              `json:"unavailable"`
	ExitCode     int               `json:"exit_code"`
	FilesWritten []string          `json:"files_written"`
	CommandsRun  []string          `json:"commands_run"`
	ToolCalls    int               `json:"tool_calls"`
	TreeAfter    map[string]string `json:"tree_after"`
}

func TestAPIRunnerParityProbe(t *testing.T) {
	inputPath := os.Getenv("CADRE_APIRUNNER_PROBE_IN")
	outputPath := os.Getenv("CADRE_APIRUNNER_PROBE_OUT")
	if inputPath == "" || outputPath == "" {
		t.Skip("set CADRE_APIRUNNER_PROBE_IN and CADRE_APIRUNNER_PROBE_OUT to run the parity probe")
	}

	raw, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	var scenarios []apiProbeScenario
	if err := json.Unmarshal(raw, &scenarios); err != nil {
		t.Fatal(err)
	}

	outcomes := make([]apiProbeOutcome, 0, len(scenarios))
	for _, scenario := range scenarios {
		outcomes = append(outcomes, runAPIProbeScenario(t, scenario))
	}

	encoded, err := json.MarshalIndent(outcomes, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outputPath, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d outcomes", len(outcomes))
}

func runAPIProbeScenario(t *testing.T, scenario apiProbeScenario) apiProbeOutcome {
	t.Helper()

	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	for relative, content := range scenario.Files {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	call := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if call >= len(scenario.Responses) {
			// Past the script the endpoint fails, which is how the
			// endpoint-failure scenarios are expressed without a second
			// mechanism.
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"scripted endpoint exhausted"}`))
			return
		}
		_, _ = w.Write([]byte(scenario.Responses[call]))
		call++
	}))
	defer server.Close()

	timeout := time.Duration(scenario.TimeoutSeconds * float64(time.Second))
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	// The available set is computed exactly as SpawnAPIChild computes it, so
	// the probe compares the shipped gating rather than a relaxed version of
	// it. CapabilityTools mirrors what the scenario declares.
	available := AvailableToolNames(scenario.CapabilityTools, scenario.WritesAllowed, scenario.CommandAllowlist)
	box := &Toolbox{
		ProjectRoot:      root,
		CommandAllowlist: scenario.CommandAllowlist,
		AvailableTools:   available,
		WritesAllowed:    containsTool(available, "write_file"),
	}
	endpoint := &ChatEndpoint{BaseURL: server.URL, Model: "probe-model"}

	outcome := apiProbeOutcome{
		Name:         scenario.Name,
		FilesWritten: []string{},
		CommandsRun:  []string{},
	}
	result, err := RunAPIDispatch(context.Background(), endpoint, box,
		[]ChatMessage{
			{Role: "system", Content: "you are a probe"},
			{Role: "user", Content: "do the thing"},
		}, nil, timeout)

	if err != nil {
		outcome.Unavailable = true
	} else {
		outcome.ExitCode = result.ExitCode
		outcome.FilesWritten = normalizeProbeList(result.FilesWritten)
		outcome.CommandsRun = normalizeProbeList(result.CommandsRun)
		outcome.ToolCalls = result.ToolCalls
	}
	outcome.TreeAfter = readProbeTree(t, root)
	return outcome
}

func normalizeProbeList(values []string) []string {
	out := append([]string{}, values...)
	sort.Strings(out)
	if out == nil {
		out = []string{}
	}
	return out
}

// readProbeTree is the observable effect that matters most: what the project
// looks like afterwards. A runner that reported the right accounting while
// writing the wrong bytes would pass every other comparison.
func readProbeTree(t *testing.T, root string) map[string]string {
	t.Helper()
	tree := map[string]string{}
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		tree[filepath.ToSlash(relative)] = string(contents)
		return nil
	})
	return tree
}
