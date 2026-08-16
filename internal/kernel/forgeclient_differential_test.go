package kernel

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/deagy/cadre/cli/internal/canonicaljson"
)

// The mock conventions, compared with the Python clients on the same files.
//
// This is the comparison that matters most in these two modules. The network
// path cannot be compared without a network, but the *mock* path is what every
// layer above these clients is tested through -- so if the conventions differ,
// every fixture written for the Python modules stops working against the Go
// ones, silently, in whichever direction a given test happens to read.
//
// Each case below writes one mock file, calls the same function on both sides,
// and compares the result or the refusal.

func TestTheMockConventionsMatchThePythonClients(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is unavailable, so there is nothing to compare against")
	}
	for _, probe := range mockCases {
		t.Run(probe.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mock.json")
			encoded, err := json.MarshalIndent(probe.mock, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, encoded, 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv(probe.variable, path)

			value, goErr := probe.call(t)
			pythonOK, pythonValue, pythonReason := pythonForgeCall(t,
				probe.variable, path, probe.pythonCall)

			if pythonOK != (goErr == nil) {
				t.Fatalf("python ok=%v (%s), go ok=%v (%v)",
					pythonOK, pythonReason, goErr == nil, goErr)
			}
			if !pythonOK {
				// The refusal text is this kernel's own, so it is compared in
				// full -- these messages are what an operator reads to find
				// out which fixture or which credential is wrong.
				if goErr.Error() != pythonReason {
					t.Errorf("python refused with %q, go refused with %q",
						pythonReason, goErr.Error())
				}
				return
			}
			if got := normaliseForgeValue(t, value); got != pythonValue {
				t.Errorf("python returned %s, go returned %s", pythonValue, got)
			}
		})
	}
}

// normaliseForgeValue renders a Go result as canonical JSON, so the comparison
// is about the value rather than about how two languages print one.
func normaliseForgeValue(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicaljson.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	return string(canonical)
}

// pythonForgeCall evaluates one expression against the same mock file.
func pythonForgeCall(t *testing.T, variable, path, expression string) (bool, string, string) {
	t.Helper()
	script := `
import json, os, sys
from agentic_sdlc import github_write, gitlab_write

os.environ[sys.argv[1]] = sys.argv[2]
try:
    value = eval(sys.argv[3])
except Exception as error:
    print(json.dumps({"ok": False, "value": "", "reason": str(error)}))
else:
    print(json.dumps({
        "ok": True,
        "value": json.dumps(value, sort_keys=True, separators=(",", ":")),
        "reason": "",
    }))
`
	command := exec.Command("python3", "-c", script, variable, path, expression)
	command.Dir = filepath.Join(repositoryRoot(t), "kernel")
	output, err := command.Output()
	if err != nil {
		t.Skipf("the Python client could not be run: %v", err)
	}
	var result struct {
		OK     bool   `json:"ok"`
		Value  string `json:"value"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("the Python side did not return JSON: %s", output)
	}
	return result.OK, result.Value, result.Reason
}

func TestTheMockVariableNamesAreThePythonKernels(t *testing.T) {
	// Pinned against the Python constants rather than chosen. A tidier name
	// here would silently stop every fixture written against that kernel being
	// read -- and an absent mock does not fail, it falls through to the
	// network. This test exists because that happened: the status client was
	// briefly given a name nothing else used.
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is unavailable, so there is nothing to compare against")
	}
	script := `
import json
from agentic_sdlc import github_write, gitlab_write, github_status_write, github_issue_write
print(json.dumps({
    "read": github_write.GITHUB_READ_MOCK_ENV_VAR,
    "gitlab": gitlab_write.ISSUE_CREATE_MOCK_ENV_VAR,
    "status": github_status_write.GITHUB_WRITE_MOCK_ENV_VAR,
    "issue": github_issue_write.GITHUB_ISSUE_MOCK_ENV_VAR,
}))
`
	command := exec.Command("python3", "-c", script)
	command.Dir = filepath.Join(repositoryRoot(t), "kernel")
	output, err := command.Output()
	if err != nil {
		t.Skipf("the Python constants could not be read: %v", err)
	}
	var names map[string]string
	if err := json.Unmarshal(output, &names); err != nil {
		t.Fatalf("the Python side did not return JSON: %s", output)
	}
	for _, probe := range []struct {
		key, actual string
	}{
		{"read", GitHubReadMockEnv},
		{"gitlab", GitLabIssueMockEnv},
		{"status", GitHubStatusMockEnv},
		{"issue", GitHubIssueMockEnv},
	} {
		if names[probe.key] != probe.actual {
			t.Errorf("%s: python names %q, go names %q", probe.key, names[probe.key], probe.actual)
		}
	}
}
