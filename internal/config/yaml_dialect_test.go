package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Which YAML dialect a config file is read under.
//
// settings.py used PyYAML, which implements YAML 1.1: `yes`, `no`, `on` and
// `off` are booleans there. This package uses gopkg.in/yaml.v3, which
// implements 1.2, where all four are ordinary strings. The two agree only on
// `true`/`false`, `null`/`~`, and bare numbers.
//
// That is not a detail of the port -- it changes what an existing config file
// means. A file written against the Python CLI and read by this one is parsed
// under different rules, and the difference is silent for exactly the values
// someone is most likely to have typed by hand.
//
// Pinned rather than left to be discovered. Each case below states what the
// two dialects do and what this package therefore does about it.

func writeGlobalConfig(t *testing.T, body string) string {
	t.Helper()
	isolateConfigEnv(t)
	dir := makeGitCheckout(t)
	home := os.Getenv("XDG_CONFIG_HOME")
	if err := os.MkdirAll(filepath.Join(home, "cadre"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeYAML(t, filepath.Join(home, "cadre", "config.yaml"), body)
	ResetCache()
	return dir
}

func TestATristateFlagRefusesTheYAML11BooleanSpellings(t *testing.T) {
	// The case where the dialects change *meaning* rather than error text.
	// PyYAML read `yes` as true and settings.py accepted it. Here it arrives
	// as the string "yes", and rather than guessing, the field refuses it and
	// says what to write instead.
	//
	// Fail-closed is the right answer: silently reading `yes` as false would
	// flip a capability flag, and silently reading it as true would mean this
	// package inventing a coercion its own parser does not perform.
	for _, spelling := range []string{"yes", "no", "on", "off"} {
		dir := writeGlobalConfig(t,
			"gitlab:\n  supports_work_item_hierarchy: "+spelling+"\n")
		_, err := ResolveSetting("gitlab.supports_work_item_hierarchy", dir)
		if err == nil {
			t.Errorf("%q was accepted; under YAML 1.2 it is the string %q, and "+
				"guessing which boolean it meant is how a capability flag flips "+
				"without anyone editing it", spelling, spelling)
			continue
		}
		if !strings.Contains(err.Error(), "'true' or 'false'") {
			t.Errorf("%q was refused without saying what to write instead: %v",
				spelling, err)
		}
	}
}

func TestTheDialectsStillAgreeOnTrueFalseAndNull(t *testing.T) {
	// The subset both dialects read identically, which is what makes the
	// refusal above a narrow one rather than a break: a file that spells its
	// booleans `true`/`false` means the same thing under either reader.
	for _, testCase := range []struct {
		spelling string
		want     *bool
	}{
		{"true", boolPointer(true)},
		{"false", boolPointer(false)},
		{"null", nil},
		{"~", nil},
	} {
		dir := writeGlobalConfig(t,
			"gitlab:\n  supports_work_item_hierarchy: "+testCase.spelling+"\n")
		resolved, err := ResolveSetting("gitlab.supports_work_item_hierarchy", dir)
		if err != nil {
			t.Errorf("%q was refused: %v", testCase.spelling, err)
			continue
		}
		value, _ := resolved.(*bool)
		switch {
		case testCase.want == nil && value != nil:
			t.Errorf("%q resolved to %v, want unset", testCase.spelling, *value)
		case testCase.want != nil && (value == nil || *value != *testCase.want):
			t.Errorf("%q resolved to %v, want %v", testCase.spelling, value, *testCase.want)
		}
	}
}

func TestAStringFieldTakesTheYAML11BooleanSpellingsLiterally(t *testing.T) {
	// The divergence in the other direction, and the reason it is acceptable.
	//
	// PyYAML turned `project_id: yes` into the bool True, which settings.py
	// then refused for being the wrong type. That refusal was an accident of
	// the dialect, not a judgement about the value: a GitLab project id may be
	// a path like `group/project`, so "yes" is not refusable on shape. This
	// package reads it as the string it is written as.
	dir := writeGlobalConfig(t, "gitlab:\n  project_id: yes\n")
	resolved, err := ResolveSetting("gitlab.project_id", dir)
	if err != nil {
		t.Fatalf("a string field refused a bare `yes`: %v", err)
	}
	if resolved != "yes" {
		t.Errorf("project_id resolved to %#v, want the literal string \"yes\"", resolved)
	}
}

func TestANumericLookingStringFieldStillRefusesAnUnquotedValue(t *testing.T) {
	// Where both dialects agree, and the message has to earn its keep: `007`
	// is an integer under 1.1 and 1.2 alike, so a project id typed without
	// quotes silently loses its leading zeros in either. The refusal names the
	// fix.
	dir := writeGlobalConfig(t, "gitlab:\n  project_id: 007\n")
	_, err := ResolveSetting("gitlab.project_id", dir)
	if err == nil {
		t.Fatal("an unquoted numeric project id was accepted")
	}
	if !strings.Contains(err.Error(), "quote") {
		t.Errorf("the refusal does not tell the reader to quote it: %v", err)
	}
}

func boolPointer(b bool) *bool { return &b }
