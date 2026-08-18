package cli

import (
	"flag"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func newOrderingFlagSet() *flag.FlagSet {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	// io.Discard, not os.NewFile(0, os.DevNull): that call does not open
	// /dev/null, it wraps file descriptor 0 and gives it that name. When the
	// returned *os.File is garbage collected its finaliser closes fd 0, and a
	// later os.ReadFile anywhere in the package fails with "bad file
	// descriptor". It made this package fail roughly one run in three.
	fs.SetOutput(io.Discard)
	fs.Int("min", 1, "a value flag")
	fs.String("require", "", "another value flag")
	fs.Bool("force", false, "a boolean flag")
	return fs
}

func TestFlagsFirstReordersWithoutLosingValues(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"already flags first", []string{"--min", "5", "file"}, []string{"--min", "5", "file"}},
		{"positional first", []string{"file", "--min", "5"}, []string{"--min", "5", "file"}},
		{"positional between flags", []string{"--min", "5", "file", "--require", "a"},
			[]string{"--min", "5", "--require", "a", "file"}},
		{"equals form keeps its value", []string{"file", "--min=5"}, []string{"--min=5", "file"}},
		{"boolean consumes nothing", []string{"file", "--force"}, []string{"--force", "file"}},
		// The failure this must not introduce: a boolean must not swallow the
		// positional that happens to follow it.
		{"boolean before positional", []string{"--force", "file"}, []string{"--force", "file"}},
		{"single dash spelling", []string{"file", "-min", "5"}, []string{"-min", "5", "file"}},
		{"double dash ends flags", []string{"--force", "--", "--not-a-flag"},
			[]string{"--force", "--not-a-flag"}},
		{"two positionals keep order", []string{"a", "--force", "b"}, []string{"--force", "a", "b"}},
		{"nothing", nil, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := flagsFirst(newOrderingFlagSet(), tc.in)
			if strings.Join(got, " ") != strings.Join(tc.want, " ") {
				t.Errorf("flagsFirst(%q)\n  got  %q\n  want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestFlagsFirstParsesBothOrdersIdentically(t *testing.T) {
	// The property that matters: whatever order a caller types, the FlagSet
	// ends up with the same values and the same positionals.
	for _, args := range [][]string{
		{"report.json", "--min", "5", "--require", "pkg"},
		{"--min", "5", "--require", "pkg", "report.json"},
		{"--min", "5", "report.json", "--require", "pkg"},
	} {
		fs := newOrderingFlagSet()
		if err := fs.Parse(flagsFirst(fs, args)); err != nil {
			t.Fatalf("parsing %q: %v", args, err)
		}
		if got := fs.Lookup("min").Value.String(); got != "5" {
			t.Errorf("%q: --min = %s, want 5", args, got)
		}
		if got := fs.Lookup("require").Value.String(); got != "pkg" {
			t.Errorf("%q: --require = %s, want pkg", args, got)
		}
		if fs.NArg() != 1 || fs.Arg(0) != "report.json" {
			t.Errorf("%q: positionals = %q, want [report.json]", args, fs.Args())
		}
	}
}

// A usage string that documents a positional before a flag must mean it.
//
// `usage: cadre sbom-check <sbom.spdx.json> [--min N]` describes an order
// Go's flag package cannot parse: Parse stops at the first non-flag argument,
// so the flags become positionals and the command rejects the invocation it
// just printed. Both affected commands were ported from argparse, which
// accepts either order, and their usage strings came across unchanged.
//
// The only caller using the documented order was release.yml, which had never
// run, so the release failed on `cadre sbom-check <file> --min 5 --require
// ...` after every build leg had passed.

func TestCommandsDocumentingPositionalBeforeFlagsAcceptThatOrder(t *testing.T) {
	root := mustGetwd(t)
	sources, err := filepath.Glob(filepath.Join(root, "*.go"))
	if err != nil {
		t.Fatalf("globbing sources: %v", err)
	}

	// usage.go holds the strings; the implementation lives elsewhere, so a
	// match there is attributed to the command file of the same name.
	usageOwner := map[string]string{
		"usageResolveShared": "resolve_shared.go",
		"usageSBOMCheck":     "sbom_check.go",
	}

	var findings []string
	checked := 0

	for _, path := range sources {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		text := string(contents)

		for _, line := range strings.Split(text, "\n") {
			if !documentsPositionalBeforeFlag(line) {
				continue
			}
			if !strings.Contains(line, "usage") && !strings.Contains(line, "Usage") {
				continue
			}
			checked++

			owner := filepath.Base(path)
			for constant, file := range usageOwner {
				if strings.Contains(line, constant) {
					owner = file
				}
			}
			ownerSource, err := os.ReadFile(filepath.Join(root, owner))
			if err != nil {
				t.Fatalf("reading %s: %v", owner, err)
			}
			if !strings.Contains(string(ownerSource), "flagsFirst(") {
				findings = append(findings, owner+": documents a positional before its flags ("+
					strings.TrimSpace(line)+") but does not call flagsFirst, so that order fails to parse")
			}
		}
	}

	if checked == 0 {
		t.Skip("no usage string documents a positional before a flag")
	}
	if len(findings) > 0 {
		sort.Strings(findings)
		t.Fatalf("usage strings promising an order the command rejects:\n  %s", strings.Join(findings, "\n  "))
	}
}

// documentsPositionalBeforeFlag reports whether a usage string puts a required
// positional ahead of an optional flag.
//
// Bracket-aware on purpose. A regex for "<...> then [-" also matches
// `[--root <dir>] [--source <dir>]`, where the angle brackets are a flag's
// value rather than a positional and the order is fine. Only a `<...>` at
// bracket depth zero is a positional.
func documentsPositionalBeforeFlag(usage string) bool {
	depth := 0
	positionalAt := -1

	for i := 0; i < len(usage); i++ {
		switch usage[i] {
		case '[':
			depth++
		case ']':
			if depth > 0 {
				depth--
			}
		case '<':
			if depth == 0 && positionalAt < 0 {
				positionalAt = i
			}
		}
	}
	if positionalAt < 0 {
		return false
	}
	return strings.Contains(usage[positionalAt:], "[-")
}
