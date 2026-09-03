package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// go.mod must not name a path on somebody's machine.
//
// A `replace` directive pointing at a local checkout is the ordinary way to
// develop two repositories together, and it is a build that works on exactly
// one computer. Committed, it makes the module unbuildable for everyone else;
// released, it makes the artifact unbuildable for everyone including its
// author on a different machine.
//
// This exists because it happened. Developing `DocumentChunkCount` in recall
// against this repository needed
//
//	replace github.com/deagy/recall => /home/deagy/sdk/recall
//
// and `git add -A` swept it into a commit. Nothing published it, and the thing
// that caught it was a person looking rather than a check -- which is the
// distinction this whole goal has been about. A test costs less than the next
// person's afternoon.
//
// Module-path replacements are allowed: `replace foo => bar v1.2.3` swaps one
// published module for another and builds anywhere. Only filesystem paths are
// refused.

// localPathReplace matches a replace whose right-hand side is a path rather
// than a module. Absolute, home-relative, and ./ or ../ forms all appear.
var localPathReplace = regexp.MustCompile(`(?m)^\s*(?:replace\s+)?\S+\s+=>\s+(\.{1,2}/|/|~)`)

func TestGoModNamesNoLocalPath(t *testing.T) {
	root := filepath.Dir(filepath.Dir(mustGetwd(t)))
	path := filepath.Join(root, "go.mod")

	content, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("not running inside a source checkout: %v", err)
	}

	// A guard that reads nothing passes. go.mod always has a module line.
	if !strings.Contains(string(content), "module ") {
		t.Fatalf("%s has no module line; this test is reading the wrong file", path)
	}

	var offending []string
	for number, line := range strings.Split(string(content), "\n") {
		if localPathReplace.MatchString(line) {
			offending = append(offending, "go.mod:"+itoa(number+1)+"  "+strings.TrimSpace(line))
		}
	}
	if len(offending) > 0 {
		t.Fatalf("go.mod replaces a module with a path on this machine:\n  %s\n\n"+
			"Fine while developing two repositories together, and unbuildable for anyone "+
			"else once committed. Release the dependency and name its version instead.",
			strings.Join(offending, "\n  "))
	}
}
