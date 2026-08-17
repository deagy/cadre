package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `cadre bootstrap-codex` writes into the user's home directory.
//
// SyncWrappers -- the library underneath -- is well covered: it refuses an
// unowned file at a destination, refuses a symlinked source or destination,
// and writes the role index only after every wrapper has succeeded. Eleven
// tests hold that.
//
// None of them run the command. A refusal the library raises and the command
// swallows is a refusal that does not exist, and this command's default target
// is ~/.codex/agents -- so the difference between "refused" and "reported as
// refused" is the difference between a guard and a comment.
//
// test_repository_health.py drove `cadre bootstrap-codex` for exactly this
// reason; its own comment says the tests were retargeted at the command rather
// than dropped with the module they used to exercise.

// codexSource writes a source tree of namespaced wrappers.
func codexSource(t *testing.T, roles ...string) string {
	t.Helper()
	source := t.TempDir()
	for _, role := range roles {
		body := "# GENERATED FILE: canonical source is roster/review/" + role + "/AGENT.md\n" +
			"name = \"agents-" + role + "\"\n" +
			"model = \"sonnet\"\n"
		if err := os.WriteFile(filepath.Join(source, "agents-"+role+".toml"),
			[]byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return source
}

// runBootstrap invokes the command and captures both streams, because the exit
// code alone cannot distinguish "did the work" from "declined and said why".
func runBootstrap(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	realOut, realErr := os.Stdout, os.Stderr
	outRead, outWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errRead, errWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = outWrite, errWrite

	code = BootstrapCodexCmd(args)

	outWrite.Close()
	errWrite.Close()
	os.Stdout, os.Stderr = realOut, realErr

	return readAll(t, outRead), readAll(t, errRead), code
}

func readAll(t *testing.T, file *os.File) string {
	t.Helper()
	var builder strings.Builder
	buffer := make([]byte, 4096)
	for {
		n, err := file.Read(buffer)
		builder.Write(buffer[:n])
		if err != nil {
			break
		}
	}
	return builder.String()
}

func TestBootstrapCodexInstallsIntoAnExplicitTarget(t *testing.T) {
	// The baseline. Without it, a command that failed on everything would
	// satisfy every refusal below.
	source := codexSource(t, "code-reviewer", "test-engineer")
	target := filepath.Join(t.TempDir(), "agents")

	stdout, stderr, code := runBootstrap(t, "--source", source, "--target", target)
	if code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	for _, name := range []string{"agents-code-reviewer.toml", "agents-test-engineer.toml",
		"agents-index.json"} {
		if _, err := os.Stat(filepath.Join(target, name)); err != nil {
			t.Errorf("%s was not written: %v", name, err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(target, "agents-index.json"))
	if err != nil {
		t.Fatal(err)
	}
	var index struct {
		Roles map[string]struct {
			Path  string  `json:"path"`
			Model *string `json:"model"`
		} `json:"roles"`
	}
	if err := json.Unmarshal(raw, &index); err != nil {
		t.Fatalf("the index does not parse: %v", err)
	}
	entry, present := index.Roles["code-reviewer"]
	if !present {
		t.Fatalf("code-reviewer is not in the index: %v", index.Roles)
	}
	// The path is resolved, not the relative name the wrapper was read under:
	// Codex reads this index from wherever it runs.
	if !filepath.IsAbs(entry.Path) {
		t.Errorf("index path %q is not absolute", entry.Path)
	}
	if entry.Model == nil || *entry.Model != "sonnet" {
		t.Errorf("index model = %v, want sonnet", entry.Model)
	}
}

func TestBootstrapCodexReportsARefusalRatherThanSucceedingQuietly(t *testing.T) {
	// The property the library cannot hold on its own. Each of these is a
	// refusal SyncWrappers raises; what is asserted here is that the command
	// exits non-zero and names the path, rather than printing its cheerful
	// "Installed 0; unchanged 0" line.
	for _, testCase := range []struct {
		name  string
		setup func(t *testing.T, source, target string)
	}{
		{"an unowned file at a wrapper's destination", func(t *testing.T, source, target string) {
			if err := os.MkdirAll(target, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(target, "agents-code-reviewer.toml"),
				[]byte("# mine, not yours\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"an unowned agents-index.json", func(t *testing.T, source, target string) {
			if err := os.MkdirAll(target, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(target, "agents-index.json"),
				[]byte(`{"mine": true}`), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"a symlinked destination", func(t *testing.T, source, target string) {
			if err := os.MkdirAll(target, 0o755); err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(t.TempDir(), "elsewhere.toml")
			if err := os.WriteFile(outside, []byte("x\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside,
				filepath.Join(target, "agents-code-reviewer.toml")); err != nil {
				t.Skipf("symlinks unavailable here: %v", err)
			}
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			source := codexSource(t, "code-reviewer")
			target := filepath.Join(t.TempDir(), "agents")
			testCase.setup(t, source, target)

			stdout, stderr, code := runBootstrap(t, "--source", source, "--target", target)
			if code == 0 {
				t.Fatalf("the command succeeded on a refusal\nstdout: %s", stdout)
			}
			if !strings.Contains(stderr, "refusing") {
				t.Errorf("stderr does not explain the refusal: %q", stderr)
			}
			if strings.Contains(stdout, "Installed") {
				t.Errorf("the success line was printed alongside a refusal: %q", stdout)
			}
		})
	}
}

func TestBootstrapCodexLeavesAnUnownedFileExactlyAsItFoundIt(t *testing.T) {
	// Refusing is only half of it. A refusal that had already truncated the
	// file, or written some of the wrappers first, is a partial install with
	// an error message.
	source := codexSource(t, "code-reviewer", "test-engineer")
	target := filepath.Join(t.TempDir(), "agents")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	original := "# hand-written, keep\nname = \"something-else\"\n"
	unowned := filepath.Join(target, "agents-code-reviewer.toml")
	if err := os.WriteFile(unowned, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, code := runBootstrap(t, "--source", source, "--target", target); code == 0 {
		t.Fatal("expected a refusal")
	}
	after, err := os.ReadFile(unowned)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != original {
		t.Errorf("the unowned file was modified:\n  before: %q\n  after:  %q",
			original, string(after))
	}
	// And no index was left behind claiming an install happened.
	if _, err := os.Stat(filepath.Join(target, "agents-index.json")); err == nil {
		t.Error("an index was written despite the refusal")
	}
}

func TestBootstrapCodexDoesNotTouchTheDefaultTargetWhenGivenOne(t *testing.T) {
	// The default is ~/.codex/agents. A flag that was parsed but not used
	// would write into the operator's real home from a test, from CI, and from
	// any invocation that meant to point somewhere else.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows

	source := codexSource(t, "code-reviewer")
	target := filepath.Join(t.TempDir(), "elsewhere")

	if _, stderr, code := runBootstrap(t, "--source", source, "--target", target); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(target, "agents-code-reviewer.toml")); err != nil {
		t.Errorf("nothing was written to the explicit target: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex")); err == nil {
		t.Error("the default target under $HOME was written to even though " +
			"--target named somewhere else")
	}
}

func TestBootstrapCodexRejectsAPositionalArgument(t *testing.T) {
	// `cadre bootstrap-codex ~/.codex/agents` is the natural typo for someone
	// who has not read --target. Silently ignoring it would install into the
	// default while the operator believes they chose a path.
	source := codexSource(t, "code-reviewer")
	target := filepath.Join(t.TempDir(), "agents")

	_, stderr, code := runBootstrap(t, "--source", source, "--target", target, "extra")
	if code == 0 {
		t.Fatal("a positional argument was accepted")
	}
	if !strings.Contains(stderr, "usage") {
		t.Errorf("the refusal does not show usage: %q", stderr)
	}
	if _, err := os.Stat(target); err == nil {
		t.Error("the target was created despite the usage error")
	}
}
