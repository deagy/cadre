package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The knowledge CLI stays anchored on the installation, not on the roster.
//
// `cadre select` emits an argv whose first element is the binary that will
// perform retrieval. Two roots are in play when it resolves that path: the
// *installation* this CLI belongs to, and the *roster* the caller pointed at
// with --roster. They are the same directory in an ordinary checkout, which is
// what makes conflating them easy and the consequence invisible in testing.
//
// The failure is specific. Anchor the knowledge CLI on the roster root and a
// caller passing --roster at a directory without a bin/cadre gets a plan whose
// argv[0] does not exist. Nothing in the selection fails -- the plan is
// well-formed, the fingerprint is valid, retrieval is "planned" -- and the
// error surfaces wherever that argv is eventually executed, which for this
// repository's own consumers is a TypeScript file in another package.
//
// Ported from roster/orchestration/test/test_knowledge_store_anchor.py, whose
// docstring records why it was written: two adjacent constants derived from
// the same path walk, one of which was about to become resolver-driven, where
// taking the other along would have looked like tidying.

// relocatedRoster builds a minimal roster elsewhere on disk: enough for a
// selection to succeed, with its own bin/cadre -- see below for why that is
// load-bearing.
func relocatedRoster(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "roster")
	for _, directory := range []string{"orchestration", "shared"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A roster package declares its own layout, and the selector refuses a
	// directory that does not. Writing the manifest is not incidental setup:
	// it is what makes this fixture a roster package rather than a directory
	// that happens to contain two files.
	manifest := `{"schema_version": 1, "id": "relocated", "version": "0.0.1",` +
		` "catalog": "catalog.yaml", "routing": "orchestration/routing.json",` +
		` "role_root": ".", "shared_policy_root": "shared"}`
	if err := os.WriteFile(filepath.Join(root, "roster.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	here, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repository := filepath.Dir(filepath.Dir(here))
	for _, file := range []struct{ from, to string }{
		{filepath.Join(repository, "roster", "catalog.yaml"), filepath.Join(root, "catalog.yaml")},
		{filepath.Join(repository, "roster", "orchestration", "routing.json"),
			filepath.Join(root, "orchestration", "routing.json")},
	} {
		content, err := os.ReadFile(file.from)
		if err != nil {
			t.Fatalf("reading %s: %v", file.from, err)
		}
		if err := os.WriteFile(file.to, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The relocated roster gets its own bin/cadre, and that is load-bearing.
	//
	// The first version of this fixture deliberately omitted it, reasoning that
	// a knowledge CLI resolved through this root would then name a path that
	// does not exist. That was backwards. knowledgeCLIPath falls back to
	// os.Executable() when the shim is absent, and under `go test` that is the
	// same test binary for every run -- so a resolution anchored on the wrong
	// root produced the same answer as a correct one, and the comparison below
	// passed while measuring the fallback rather than the anchoring.
	//
	// With a shim present here, anchoring on the roster returns *this* path and
	// anchoring on the installation returns the real one. The fallback never
	// fires, and the difference is visible.
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(root, "bin", "cadre")
	if err := os.WriteFile(shim, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

// emittedKnowledgeCLI runs a selection and returns the argv[0] of its first
// planned retrieval.
func emittedKnowledgeCLI(t *testing.T, extra ...string) string {
	t.Helper()
	target := filepath.Join(t.TempDir(), "plan.json")
	args := append([]string{
		"--task", "Update the React navigation for keyboard accessibility",
		"--files", "frontend/src/Nav.tsx",
		"--task-id", "ANCHOR-1",
		"--classification", "internal",
		"--source", "deagy/cadre",
		"--output", target,
	}, extra...)
	if code := runSelectGo(args); code != 0 {
		t.Fatalf("cadre select %v exited %d", extra, code)
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	var plan struct {
		KnowledgeContext struct {
			Requests []struct {
				Invocation struct {
					Args []string `json:"args"`
				} `json:"invocation"`
			} `json:"requests"`
		} `json:"knowledge_context"`
	}
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatalf("parsing the plan: %v", err)
	}
	if len(plan.KnowledgeContext.Requests) == 0 {
		t.Fatal("no retrieval was planned, so there is no argv to check")
	}
	args0 := plan.KnowledgeContext.Requests[0].Invocation.Args
	if len(args0) == 0 {
		t.Fatal("the planned invocation has an empty argv")
	}
	return args0[0]
}

func TestPointingTheRosterElsewhereDoesNotMoveTheKnowledgeCLI(t *testing.T) {
	// The whole guard, in one comparison. --roster selects which routing rules
	// and which catalog a selection uses; it says nothing about where the
	// binary that performs retrieval lives.
	anchored := emittedKnowledgeCLI(t)
	relocated := emittedKnowledgeCLI(t, "--roster", relocatedRoster(t))

	if relocated != anchored {
		t.Errorf("--roster moved the knowledge CLI path.\nwithout: %s\nwith:    %s\n"+
			"The emitted argv[0] is the binary a consumer will execute; resolving "+
			"it through the roster means a caller who points --roster at a "+
			"directory with no bin/cadre gets a well-formed plan naming a path "+
			"that does not exist.", anchored, relocated)
	}
}

func TestTheEmittedKnowledgeCLIExistsAndIsExecutable(t *testing.T) {
	// A path that is merely a string is not a plan a consumer can act on. This
	// is what would notice the resolution falling back to a shim that was
	// never built, or to a directory.
	// The absoluteness check below is weakly exercised, and saying so is more
	// use than implying otherwise: making the shim path relative does not fail
	// it, because os.Stat then misses and the os.Executable() fallback returns
	// an absolute path anyway. The fallback is what guarantees absoluteness
	// today, not the shim branch.
	path := emittedKnowledgeCLI(t)
	if !filepath.IsAbs(path) {
		t.Errorf("argv[0] is not absolute: %q. A consumer executes it from its "+
			"own working directory, not this one.", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the emitted knowledge CLI does not exist: %v", err)
	}
	if info.IsDir() {
		t.Fatalf("the emitted knowledge CLI is a directory: %s", path)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("the emitted knowledge CLI is not executable (mode %v): %s",
			info.Mode().Perm(), path)
	}
}

func TestTheEmittedKnowledgeCLIIsNotInsideTheRelocatedRoster(t *testing.T) {
	// Stated structurally as well as by equality, because the comparison above
	// would also pass if *both* answers were wrong in the same way -- if the
	// resolution ignored --roster but still anchored somewhere inside whichever
	// roster it did use.
	roster := relocatedRoster(t)
	path := emittedKnowledgeCLI(t, "--roster", roster)

	resolvedRoster, err := filepath.EvalSymlinks(roster)
	if err != nil {
		resolvedRoster = roster
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		resolvedPath = path
	}
	relative, err := filepath.Rel(resolvedRoster, resolvedPath)
	if err == nil && !strings.HasPrefix(relative, "..") && relative != "." {
		t.Errorf("the knowledge CLI resolved inside the roster the caller "+
			"pointed at: %s is %s of %s", path, relative, roster)
	}
}
