package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A settings error names the file, never its contents.
//
// Every one of these errors is printed by a CLI, which means it reaches
// terminal scrollback, CI logs, and whatever captured the run. A message that
// quotes the offending line to be helpful publishes it -- and the two documents
// this package reads are the two most likely places for someone to have put
// something they should not have.
//
// The behaviour is already right. Nothing asserted it, so the next person to
// improve an error message by including the value would find nothing in their
// way.

const canaryValue = "CANARY-a7f3e1-DO-NOT-ECHO"

// assertNoEcho fails if the error text carries the canary, and also if there is
// no error at all -- a check for "the message does not contain X" is satisfied
// by every message that was never produced.
func assertNoEcho(t *testing.T, err error, what string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s produced no error; this test would prove nothing", what)
	}
	if strings.Contains(err.Error(), canaryValue) {
		t.Errorf("%s echoes the file's contents into the error:\n  %v", what, err)
	}
	// And it does name the file, because an error that hides both is unusable.
	if !strings.Contains(err.Error(), "cadre") {
		t.Errorf("%s does not name the file it is about:\n  %v", what, err)
	}
}

func TestASecretShapedKeyIsRefusedWithoutEchoingItsValue(t *testing.T) {
	// The one that matters most: the value under a key called `token` is a
	// token. Refusing it and then printing it is worse than not checking.
	for _, testCase := range []struct {
		name     string
		document map[string]any
	}{
		{"at the top level", map[string]any{"token": canaryValue}},
		{"nested in a map", map[string]any{"gitlab": map[string]any{"api_key": canaryValue}}},
		{"inside a list", map[string]any{"runners": []any{
			map[string]any{"password": canaryValue}}}},
		{"inside a list of lists", map[string]any{"a": []any{[]any{
			map[string]any{"secret": canaryValue}}}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := rejectSecretShapedKeys(testCase.document, "", "cadre.yaml")
			assertNoEcho(t, err, "secret-shaped key rejection")
			// The path to the key is what makes the message actionable, and it
			// is not itself sensitive.
			if !strings.Contains(err.Error(), "key ") {
				t.Errorf("the refusal does not say which key:\n  %v", err)
			}
		})
	}
}

func TestMalformedConfigContentIsNotEchoedBack(t *testing.T) {
	// A parse failure is where echoing is most tempting -- the parser already
	// has the offending text in hand. Both file shapes are checked because
	// they go through different decoders.
	for _, testCase := range []struct {
		name     string
		filename string
		content  string
	}{
		{"malformed YAML", "cadre.yaml",
			"gitlab:\n  base_url: \"" + canaryValue + "\n  unclosed: [\n"},
		{"malformed JSON", "cadre.json",
			`{"gitlab": {"base_url": "` + canaryValue + `"` + "\n"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			isolateConfigEnv(t)
			dir := makeGitCheckout(t)
			agents := filepath.Join(dir, ".agents")
			if err := os.MkdirAll(agents, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(agents, testCase.filename),
				[]byte(testCase.content), 0o644); err != nil {
				t.Fatal(err)
			}
			ResetCache()

			_, err := ResolveSetting("gitlab.supports_work_item_hierarchy", dir)
			assertNoEcho(t, err, testCase.name)
		})
	}
}

func TestTheCanaryWouldBeNoticedIfItWereEchoed(t *testing.T) {
	// Guards the guard. Every check above passes when the canary is absent,
	// which is also what happens if the canary never reached the file, or if
	// assertNoEcho's containment test were wrong.
	//
	// So: an error that does carry it must fail the same assertion.
	fake := settingsErrorf("cadre.yaml: bad value %s", canaryValue)
	if !strings.Contains(fake.Error(), canaryValue) {
		t.Fatal("the canary does not survive into an error message, so the checks " +
			"above are asserting something they could never observe")
	}

	// And confirm the file really did contain the canary in the malformed
	// cases -- otherwise those tests assert the absence of something that was
	// never present.
	isolateConfigEnv(t)
	dir := makeGitCheckout(t)
	agents := filepath.Join(dir, ".agents")
	if err := os.MkdirAll(agents, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "gitlab:\n  base_url: \"" + canaryValue + "\n  unclosed: [\n"
	path := filepath.Join(agents, "cadre.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), canaryValue) {
		t.Error("the fixture does not contain the canary")
	}
}
