package cli

// AC-5 is falsified by running the command and reading what it says.
//
// Not by grepping the source for a message: that proves the message exists,
// not that anyone reaching for retention ever reaches it. Every case here
// invokes the real dispatch and reads what a steward would see on stderr.

import (
	"os"
	"strings"
	"testing"
)

// runKnowledgeCapturingStderr invokes the dispatch and returns what a user
// would see, plus the exit code.
func runKnowledgeCapturingStderr(t *testing.T, staged bool, args ...string) (string, int) {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stderr
	os.Stderr = write

	done := make(chan string, 1)
	go func() {
		buffer := make([]byte, 8192)
		n, _ := read.Read(buffer)
		done <- string(buffer[:n])
	}()

	var code int
	if staged {
		code = KnowledgeStagedCmd(args)
	} else {
		code = KnowledgeCmd(args)
	}

	_ = write.Close()
	os.Stderr = original
	output := <-done
	_ = read.Close()
	return output, code
}

// A retention or erasure flag on a live command is refused by name.
func TestARetentionRequestIsRefusedWhereItIsReachedFor(t *testing.T) {
	cases := []struct {
		name   string
		staged bool
		args   []string
		says   string
	}{
		{"retention on search", false,
			[]string{"search", "--retention-days", "30"}, "retention-days"},
		{"retention with equals", false,
			[]string{"search", "--retention-days=30"}, "retention-days"},
		{"single dash", false,
			[]string{"search", "-retention-days", "30"}, "retention-days"},
		{"erasure trigger", false,
			[]string{"search", "--trigger", "gdpr-request"}, "trigger"},
		{"evidence as-of", false,
			[]string{"search", "--as-of", "2026-01-01"}, "as-of"},
		{"on the staged route", true,
			[]string{"delete-staged", "--retention-days", "30"}, "retention-days"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			output, code := runKnowledgeCapturingStderr(t, tc.staged, tc.args...)
			if code != 2 {
				t.Fatalf("exit %d, want 2 (a usage error, distinguishable from a run that failed)", code)
			}
			if !strings.Contains(output, "--"+tc.says) {
				t.Fatalf("the refusal does not name the flag reached for.\ngot: %s", output)
			}
			// The message has to carry what SECURITY.md carries, at the
			// moment of use -- what was removed, that nothing rebuilt it,
			// and that the decision is open. A polite "not supported" is a
			// parser error with better manners.
			for _, required := range []string{"b418031e", "recall", "open decision"} {
				if !strings.Contains(output, required) {
					t.Fatalf("the refusal omits %q, so it names the gap without explaining it.\ngot: %s",
						required, output)
				}
			}
			if strings.Contains(output, "flag provided but not defined") {
				t.Fatalf("the parser answered before the refusal did.\ngot: %s", output)
			}
		})
	}
}

// The refusal must not eat flags that mean something elsewhere.
func TestTheRefusalDoesNotReachLiveFlags(t *testing.T) {
	for _, args := range [][]string{
		{"search", "--classification", "internal", "--all-sources", "q"},
		{"config"},
		{"--config", "/nonexistent/config.json", "config"},
	} {
		output, _ := runKnowledgeCapturingStderr(t, false, args...)
		if strings.Contains(output, "belonged to a capability this binary does not have") {
			t.Fatalf("a live invocation was refused as an absent capability: %v\ngot: %s",
				args, output)
		}
	}
}
