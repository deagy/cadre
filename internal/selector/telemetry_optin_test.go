package selector

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The two telemetry opt-ins, and the escaping that keeps the log parseable.
//
// telemetry_test.go covers the properties that matter most -- off by default,
// no raw content unless asked twice, one line per call, Python's spacing. This
// covers what it leaves: TelemetryIncludeTask was at 0% and controlEscape at
// 0%, with expandUser at 33.3% and writeUnicodeString at 40%.
//
// Both gaps are reachable from ordinary use. `--task` is free text a person
// types or pastes, and the file is JSONL.

func TestTheTwoOptInsAreSeparateSwitches(t *testing.T) {
	// Recording *that* a selection happened and recording *what was asked for*
	// are different decisions, which is why they are two variables. Wiring
	// TelemetryIncludeTask to the enable variable -- the obvious slip, since
	// the names differ by one suffix -- would mean anyone who turned telemetry
	// on started logging raw task text without asking to.
	t.Setenv(telemetryEnableEnv, "1")
	t.Setenv(telemetryIncludeTaskEnv, "")
	if !TelemetryIsEnabled(false) {
		t.Fatal("the enable variable stopped working; the rest proves nothing")
	}
	if TelemetryIncludeTask(false) {
		t.Error("enabling telemetry also switched on raw task capture")
	}

	// And the reverse: asking for task text does not by itself start recording.
	t.Setenv(telemetryEnableEnv, "")
	t.Setenv(telemetryIncludeTaskEnv, "1")
	if TelemetryIsEnabled(false) {
		t.Error("the include-task variable switched telemetry on")
	}
	if !TelemetryIncludeTask(false) {
		t.Error("the include-task variable does not work")
	}
}

func TestTheIncludeTaskOptInReadsTheSameValuesAsTheOther(t *testing.T) {
	// One shared parser, so a value accepted by one switch is accepted by the
	// other. The failure this rules out is a second switch that treats "no"
	// or "0" as truthy because it checks only for a non-empty string.
	t.Setenv(telemetryEnableEnv, "")
	for _, value := range []string{"1", "true", "YES", " on "} {
		t.Setenv(telemetryIncludeTaskEnv, value)
		if !TelemetryIncludeTask(false) {
			t.Errorf("env value %q must enable raw task capture", value)
		}
	}
	for _, value := range []string{"0", "false", "no", "off", "maybe", ""} {
		t.Setenv(telemetryIncludeTaskEnv, value)
		if TelemetryIncludeTask(false) {
			t.Errorf("env value %q must NOT enable raw task capture", value)
		}
	}
	// The flag is an independent path to the same answer.
	t.Setenv(telemetryIncludeTaskEnv, "")
	if !TelemetryIncludeTask(true) {
		t.Error("the flag must enable raw task capture on its own")
	}
}

func TestATaskContainingNewlinesStaysOneRecordPerLine(t *testing.T) {
	// The file is JSONL: one record per line is the format, not a nicety. A
	// pasted multi-line ticket body is the ordinary way a newline gets into
	// --task, and an unescaped one would split a record across two lines --
	// leaving every reader with one truncated record and one fragment that is
	// not JSON at all. Every later line is misaligned too, so a single pasted
	// task corrupts the whole file from that point on.
	newline, tab, control := string(rune(10)), string(rune(9)), string(rune(1))
	task := "first line" + newline + "second" + tab + "tabbed" + control + "control" +
		newline + `and a "quoted" \ backslash`

	root := t.TempDir()
	destination := filepath.Join(root, "telemetry.jsonl")
	plan := map[string]any{
		"task_id": "T-1", "status": "ready",
		"inputs": map[string]any{"task": task, "changed_files": []any{"a.go"}},
	}
	for range 3 {
		if _, err := RecordSelection(plan, root, destination, true); err != nil {
			t.Fatal(err)
		}
	}

	contents, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(contents), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("three calls wrote %d lines; a task's newline escaped into "+
			"the record separator:\n%s", len(lines), contents)
	}
	for index, line := range lines {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("line %d is not a complete record: %v\n%q", index, err, line)
		}
		// And the text survives the round trip unchanged -- escaping that
		// dropped or mangled a character would still produce parseable JSONL.
		if got, _ := record["task"].(string); got != task {
			t.Errorf("line %d: task came back as %q, want %q", index, got, task)
		}
	}
}

func TestControlCharactersAreEscapedAsPythonEscapesThem(t *testing.T) {
	// json.dumps writes the five shorthands and \u00XX for the rest, in
	// lowercase hex. A record written with Go's default encoder would use the
	// same escapes but differ elsewhere; a hand-rolled one is free to emit
	// \u0001 as \x01, which no JSON parser accepts.
	var probe strings.Builder
	for code := rune(0); code < 0x20; code++ {
		probe.WriteRune(code)
	}
	encoded, err := TelemetryJSON(map[string]any{"task": probe.String()})
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}
	// The whole range at once rather than a sample, so a wrong escape for one
	// code point cannot hide between two spot checks. This is what
	// `json.dumps({"task": "".join(chr(c) for c in range(0x20))},
	// sort_keys=True, ensure_ascii=False)` printed, recorded 2026-08-15 while
	// the Python selector was still in the tree.
	const wanted = `{"task": "\u0000\u0001\u0002\u0003\u0004\u0005\u0006\u0007\b\t\n\u000b\f\r\u000e\u000f\u0010\u0011\u0012\u0013\u0014\u0015\u0016\u0017\u0018\u0019\u001a\u001b\u001c\u001d\u001e\u001f"}`
	if string(encoded) != wanted {
		t.Errorf("control characters encode differently from json.dumps.\n"+
			"json.dumps: %s\nencoded:    %s", wanted, encoded)
	}
	// Whatever it wrote has to parse, and parse back to what went in.
	var round map[string]any
	if err := json.Unmarshal(encoded, &round); err != nil {
		t.Fatalf("the encoding does not parse: %v\n%s", err, encoded)
	}
	if got, _ := round["task"].(string); got != probe.String() {
		t.Errorf("the control characters did not survive the round trip")
	}
}

func TestATelemetryPathBeginningWithATildeIsExpanded(t *testing.T) {
	// `~` is shell syntax, not filesystem syntax: nothing below expands it, so
	// an unexpanded path creates a directory literally named "~" under the
	// working directory. The telemetry then works -- it writes, it appends, it
	// reports a path -- while the file is nowhere the operator asked for, and
	// a second run from a different directory starts a second one.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory on this machine: %v", err)
	}
	t.Setenv(telemetryPathEnv, "")

	for _, probe := range []struct{ name, given, want string }{
		{"a bare tilde", "~", home},
		{"a tilde path", "~/logs/telemetry.jsonl",
			filepath.Join(home, "logs", "telemetry.jsonl")},
	} {
		t.Run(probe.name, func(t *testing.T) {
			if got := ResolveTelemetryPath("/repo", probe.given); got != probe.want {
				t.Errorf("path = %q, want %q", got, probe.want)
			}
		})
	}

	// A tilde that is not a home reference is left alone: `~user` is a form
	// this does not implement, and inventing an expansion for it would point
	// at the wrong account's directory rather than at a missing one.
	for _, unchanged := range []string{
		"~user/telemetry.jsonl",
		"/absolute/~/telemetry.jsonl",
		"relative/telemetry.jsonl",
		"~tilde-prefixed-name.jsonl",
	} {
		if got := ResolveTelemetryPath("/repo", unchanged); got != unchanged {
			t.Errorf("path %q was rewritten to %q", unchanged, got)
		}
	}

	// The environment variable goes through the same expansion as the flag --
	// otherwise the two ways of naming a path disagree about what `~` means.
	t.Setenv(telemetryPathEnv, "~/from-env.jsonl")
	if got, want := ResolveTelemetryPath("/repo", ""),
		filepath.Join(home, "from-env.jsonl"); got != want {
		t.Errorf("the environment path was not expanded: %q, want %q", got, want)
	}
}
