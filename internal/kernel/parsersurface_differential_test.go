package kernel

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The whole argument surface, derived from the Python parser rather than
// listed by hand.
//
// Every other differential in this package is a set of cases somebody thought
// of. This one asks argparse what it accepts -- 32 subcommands, 175 flags, 21
// choice sets, 6 mutually exclusive groups -- and probes each one against both
// kernels. It exists because the hand-written cases missed a class of gap
// entirely: five flags the Go CLI rejected outright, one choice set it never
// validated, and every exclusive group unimplemented. None of those is exotic;
// they were simply flags nobody had written a case for.
//
// The probes are deliberately narrow, because a generated test that tries to
// exercise behaviour would mostly be testing its own fixture generation:
//
//   - **Acceptance.** Every declared flag, passed alongside valid values for
//     everything required, must not come back as an unknown argument.
//   - **Choice sets.** A value outside the set must be a usage error in both,
//     with the same message.
//   - **Exclusive groups.** Two flags from one group must be a usage error in
//     both, with the same message.
//
// What it does not check is what the flag then does -- that is what the
// hand-written differentials are for, and they can reach cases this cannot
// construct.

// parserSurface is what argparse says each subcommand accepts.
type parserSurface map[string]subcommandSurface

type subcommandSurface struct {
	Required  [][]string          `json:"required"`
	Flags     map[string]flagSpec `json:"flags"`
	Exclusive [][]string          `json:"exclusive"`
}

type flagSpec struct {
	TakesValue bool     `json:"takes_value"`
	Integer    bool     `json:"integer"`
	Choices    []string `json:"choices"`
}

// readParserSurface introspects the Python parser.
//
// argparse's own model rather than a regex over the source: a parser built
// across four hundred lines of `add_argument` calls, some of them in loops and
// groups, is not something to re-derive by pattern matching.
func readParserSurface(t *testing.T) parserSurface {
	t.Helper()
	script := `
import json, sys
from agentic_sdlc import build_parser

surface = {}
for action in build_parser()._actions:
    choices = getattr(action, "choices", None)
    if not choices or not hasattr(choices, "items"):
        continue
    for name, sub in choices.items():
        flags, required = {}, []
        for a in sub._actions:
            if not a.option_strings:
                continue
            if a.required:
                pair = [a.option_strings[0]]
                if a.nargs != 0:
                    if a.choices:
                        pair.append(list(a.choices)[0])
                    elif a.type is int:
                        pair.append("1")
                    else:
                        pair.append("x")
                required.append(pair)
            for opt in a.option_strings:
                if opt in ("-h", "--help"):
                    continue
                flags[opt] = {"takes_value": a.nargs != 0,
                              "integer": a.type is int,
                              "choices": list(a.choices) if a.choices else None}
        surface[name] = {
            "required": required,
            "flags": flags,
            "exclusive": [[x.option_strings[0] for x in g._group_actions]
                          for g in sub._mutually_exclusive_groups],
        }
print(json.dumps(surface))
`
	command := exec.Command("python3", "-c", script)
	command.Dir = filepath.Join(repositoryRoot(t), "kernel")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("introspecting the Python parser: %v", err)
	}
	var surface parserSurface
	if err := json.Unmarshal(output, &surface); err != nil {
		t.Fatalf("decoding the parser surface: %v", err)
	}
	if len(surface) == 0 {
		t.Fatal("the parser reported no subcommands; this test would check nothing")
	}
	return surface
}

// The global flags, which the per-subcommand sweep below cannot reach: they
// belong to the top-level parser, not to any subparser.
func TestTheGlobalFlagsMatchThePythonKernel(t *testing.T) {
	for _, probe := range []struct {
		name string
		args []string
	}{
		{"the version", []string{"--version"}},
		{
			// argparse answers --version during parsing, so a manifest that
			// could not load never gets read. A Go kernel that loaded
			// providers first would exit 1 here.
			"the version, behind a provider that cannot load",
			[]string{"--provider", "/nonexistent-provider.json", "--version"},
		},
		{
			// Not a version request: the subparser has no such flag, and
			// answering it would make `detect --version` mean two things.
			"a version flag after the subcommand",
			[]string{"detect", "--version"},
		},
		{"no arguments at all", []string{}},
		{"a subcommand nobody declared", []string{"not-a-subcommand"}},
	} {
		t.Run(probe.name, func(t *testing.T) {
			pythonCode, pythonOutput := runPythonGateStatus(t, probe.args)
			var stdout, stderr bytes.Buffer
			goCode := Run(probe.args, &stdout, &stderr)
			if pythonCode != goCode {
				t.Errorf("python exited %d, go exited %d\npython: %s\ngo: %s",
					pythonCode, goCode, pythonOutput, stdout.String()+stderr.String())
			}
			// Only the successful ones are compared on output: a usage error
			// prints argparse's own block, exempted the same way everywhere
			// else in this package.
			if goCode == 0 && pythonOutput != stdout.String()+stderr.String() {
				t.Errorf("output differs.\npython: %q\ngo: %q",
					pythonOutput, stdout.String()+stderr.String())
			}
		})
	}
}

func TestEveryDeclaredFlagIsAccepted(t *testing.T) {
	surface := readParserSurface(t)
	checked, expected := 0, 0
	for _, spec := range surface {
		for flag := range spec.Flags {
			if !isRequiredOrRoot(flag, spec.Required) {
				expected++
			}
		}
	}
	for _, command := range sortedMapKeys(surface) {
		spec := surface[command]
		t.Run(command, func(t *testing.T) {
			base := probeBase(t, command, spec.Required)
			for _, flag := range sortedMapKeys(spec.Flags) {
				if isRequiredOrRoot(flag, spec.Required) {
					continue
				}
				argv := append(append([]string{}, base...), flag)
				if spec.Flags[flag].TakesValue {
					argv = append(argv, valueFor(spec.Flags[flag]))
				}
				var stdout, stderr bytes.Buffer
				Run(argv, &stdout, &stderr)
				// Matched on the two halves rather than the whole sentence,
				// so the check does not depend on how the CLI quotes the
				// flag -- which is exactly the kind of detail that changed
				// under it once already.
				output := stdout.String() + stderr.String()
				if strings.Contains(output, "unknown argument") && strings.Contains(output, flag) {
					t.Errorf("%s %s: the Python parser accepts this flag and the Go CLI does not",
						command, flag)
				}
				checked++
			}
		})
	}
	// A sweep that swept nothing, or swept less than the parser declares,
	// would otherwise pass silently.
	if checked != expected || expected == 0 {
		t.Errorf("probed %d flags, expected %d", checked, expected)
	}
}

func TestEveryChoiceSetIsEnforcedTheSameWay(t *testing.T) {
	surface := readParserSurface(t)
	for _, command := range sortedMapKeys(surface) {
		spec := surface[command]
		t.Run(command, func(t *testing.T) {
			base := probeBase(t, command, spec.Required)
			for _, flag := range sortedMapKeys(spec.Flags) {
				if len(spec.Flags[flag].Choices) == 0 {
					continue
				}
				argv := append(append([]string{}, base...), flag, "__not_a_choice__")
				comparePythonAndGo(t, command+" "+flag, argv)
			}
		})
	}
}

func TestEveryExclusiveGroupIsEnforcedTheSameWay(t *testing.T) {
	surface := readParserSurface(t)
	groups := 0
	for _, command := range sortedMapKeys(surface) {
		spec := surface[command]
		if len(spec.Exclusive) == 0 {
			continue
		}
		t.Run(command, func(t *testing.T) {
			base := probeBase(t, command, spec.Required)
			for _, group := range spec.Exclusive {
				if len(group) < 2 {
					continue
				}
				// Both orders, because argparse names the flag seen *second*
				// as the offender: a guard on only one of the two flags
				// catches one order and not the other, and one probe would
				// not tell them apart.
				for _, pair := range [][]string{
					{group[0], group[1]}, {group[1], group[0]},
				} {
					argv := append(append([]string{}, base...), pair...)
					comparePythonAndGo(t, command+" "+strings.Join(pair, " "), argv)
					groups++
				}
				// And a repeat of one flag, which argparse allows: a tracker
				// that refused it would break `--apply --apply`, which
				// scripts assembling arguments do produce.
				for _, flag := range group {
					argv := append(append([]string{}, base...), flag, flag)
					comparePythonAndGo(t, command+" "+flag+" (repeated)", argv)
				}
			}
		})
	}
	if groups == 0 {
		t.Error("no exclusive groups were probed; the parser declares six")
	}
}

// comparePythonAndGo runs one usage probe through both kernels.
//
// The exit code and the final line are compared. Not the whole stream:
// argparse prints a wrapped usage block that the Go CLI does not, which is the
// same exemption the approval-adapter differential documents.
func comparePythonAndGo(t *testing.T, label string, argv []string) {
	t.Helper()
	pythonCode, pythonOutput := runPythonGateStatus(t, argv)
	var stdout, stderr bytes.Buffer
	goCode := Run(argv, &stdout, &stderr)
	goOutput := stdout.String() + stderr.String()

	if pythonCode != goCode {
		t.Errorf("%s: python exited %d, go exited %d\npython: %s\ngo: %s",
			label, pythonCode, goCode, pythonOutput, goOutput)
		return
	}
	python := normalizeChoiceList(lastLine(pythonOutput))
	golang := normalizeChoiceList(lastLine(goOutput))
	if python != golang {
		t.Errorf("%s differs.\npython: %s\ngo:     %s", label, python, golang)
	}
}

// probeBase is the subcommand with every required flag filled in, so the only
// thing wrong in a probe is the thing being probed.
func probeBase(t *testing.T, command string, required [][]string) []string {
	t.Helper()
	base := []string{command, "--root", t.TempDir()}
	for _, pair := range required {
		base = append(base, pair...)
	}
	return base
}

func isRequiredOrRoot(flag string, required [][]string) bool {
	if flag == "--root" {
		return true
	}
	for _, pair := range required {
		if pair[0] == flag {
			return true
		}
	}
	return false
}

// valueFor is a value the flag will accept, so acceptance is what is measured
// rather than validation.
func valueFor(spec flagSpec) string {
	switch {
	case len(spec.Choices) > 0:
		return spec.Choices[0]
	case spec.Integer:
		return "1"
	}
	return "x"
}

func sortedMapKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
