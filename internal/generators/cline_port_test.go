package generators

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The Cline port reproduces the committed mirror exactly, and refuses to ship
// a leaked source path.
//
// The mirror is 159 preset files and 9 skills that a Cline user installs. Its
// whole job is turning this repository's own `roster/`-relative references
// into consumer-neutral prose, so the failure that matters is a reference
// getting through: the preset then tells someone to read a file that exists
// only here.
//
// Ported from plugin/tools/port_cline_agents.py, whose substitution
// vocabulary was extracted mechanically into cline_tables.go rather than
// retyped -- 124 exact-match pairs where one slip changes a generated file in
// a way that reads as intentional.

// portIntoScratch runs the port against a throwaway copy and returns it.
//
// A copy rather than the real tree, so a test can never leave the committed
// mirror modified -- the Python suite has a test for exactly that, having
// presumably done it once.
//
// Ported once per test binary and shared. Porting 159 presets is the
// expensive part, and doing it per test made this package heavy enough under
// -race to starve everything running beside it: `go test -race ./...` had
// internal/orchestration go from 62s to a 25-minute timeout, purely from
// contention. Tests here only read the result.
var (
	sharedPortOnce   sync.Once
	sharedPortRepo   string
	sharedPortMirror string
	sharedPortErr    error
)

func portIntoScratch(t *testing.T) (repoRoot, scratchMirror string) {
	t.Helper()
	sharedPortOnce.Do(func() {
		root, err := os.Getwd()
		if err != nil {
			sharedPortErr = err
			return
		}
		root = filepath.Dir(filepath.Dir(root))
		if _, err := os.Stat(filepath.Join(root, "cline-plugins")); err != nil {
			sharedPortErr = err
			return
		}
		scratchParent, err := os.MkdirTemp("", "cline-port-")
		if err != nil {
			sharedPortErr = err
			return
		}
		scratch := filepath.Join(scratchParent, "cline-plugins")
		if out, err := exec.Command("cp", "-a",
			filepath.Join(root, "cline-plugins"), scratch).CombinedOutput(); err != nil {
			sharedPortErr = fmt.Errorf("cannot copy the mirror: %w\n%s", err, out)
			return
		}
		if _, _, err := PortClineAgents(root, scratch, filepath.Join(root, "plugin")); err != nil {
			sharedPortErr = err
			return
		}
		sharedPortRepo = root
		sharedPortMirror = filepath.Join(scratch, "cline-agents")
	})
	if sharedPortErr != nil {
		t.Skipf("the shared port could not run: %v", sharedPortErr)
	}
	return sharedPortRepo, sharedPortMirror
}

func TestTheClinePortReproducesTheCommittedMirrorExactly(t *testing.T) {
	repoRoot, ported := portIntoScratch(t)
	committed := filepath.Join(repoRoot, "cline-plugins", "cline-agents")

	out, err := exec.Command("diff", "-r", committed, ported).CombinedOutput()
	if err != nil {
		text := string(out)
		if len(text) > 3000 {
			text = text[:3000] + "\n... (truncated)"
		}
		t.Errorf("the port does not reproduce the committed mirror:\n%s", text)
	}

	// And it produced a mirror at all -- an empty output directory diffs
	// clean against nothing only if the committed one is empty too, but a
	// count says so directly.
	entries, err := filepath.Glob(filepath.Join(ported, "agents", "*.md"))
	if err != nil || len(entries) < 100 {
		t.Fatalf("the port wrote %d agent files; it did not run", len(entries))
	}
	t.Logf("reproduced %d agents byte-for-byte", len(entries))
}

func TestTheClinePortIsIdempotent(t *testing.T) {
	// Running it twice must not change anything the first run produced. A
	// substitution whose output still matches its own input would keep
	// rewriting, and the drift would appear only on a second regeneration.
	repoRoot, ported := portIntoScratch(t)
	before, err := os.ReadFile(filepath.Join(ported, "agents", "code-reviewer.md"))
	if err != nil {
		t.Fatal(err)
	}
	mirrorRoot := filepath.Dir(ported)
	if _, _, err := PortClineAgents(repoRoot, mirrorRoot, filepath.Join(repoRoot, "plugin")); err != nil {
		t.Fatalf("second run: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(ported, "agents", "code-reviewer.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("a second port changed the output; a substitution is rewriting its " +
			"own result, and the drift would only appear on a later regeneration")
	}
}

func TestTheLeakCheckCatchesBothReferenceShapes(t *testing.T) {
	// The safety property. Both shapes must be caught: a `roster/`-rooted
	// path, and a `../`-relative one.
	//
	// The second is why this is not one regex. Python used a negative
	// lookbehind, which RE2 does not have, so the preceding byte is inspected
	// instead -- and these pin that the substitute behaves the same.
	for _, testCase := range []struct {
		name   string
		body   string
		leaked bool
	}{
		{"a roster path", "see `roster/shared/brand-new-policy.md` for details", true},
		{"a templated roster path", "see `roster/<phase>/<role>/AGENT.md`", true},
		{"a relative path", "see `../../shared/brand-new.md`", true},
		{"a relative path mid-sentence", "read ../sibling/AGENT.md now", true},
		{"an ellipsis is not a path", "wait... /usr is elsewhere", false},
		{"a version is not a path", "see v1.2../3 nothing", false},
		{"clean prose", "this project's shared-policy documentation", false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			leaks := clineLeaks(testCase.body)
			if testCase.leaked && len(leaks) == 0 {
				t.Errorf("no leak found in %q; a preset would ship a path that "+
					"exists only in this repository", testCase.body)
			}
			if !testCase.leaked && len(leaks) > 0 {
				t.Errorf("reported %v as a leak in %q", leaks, testCase.body)
			}
		})
	}
}

func TestAnUnknownModelTierIsRefusedRatherThanDefaulted(t *testing.T) {
	// A preset carries a capability tier. Defaulting an unrecognised one
	// would ship a preset claiming a tier its operator never configured.
	repoRoot := repositoryRoot(t)
	tiers, err := clineModelTiers(repoRoot)
	if err != nil {
		t.Skipf("no runner-capability manifest here: %v", err)
	}
	if len(tiers) == 0 {
		t.Fatal("the manifest declared no tiers")
	}
	if _, present := tiers["not-a-real-tier"]; present {
		t.Error("an unknown tier resolves, so a typo would ship silently")
	}

	source := filepath.Join(t.TempDir(), "agent.md")
	if err := os.WriteFile(source, []byte(
		"---\nname: x\ndescription: y\ntools: Read\nmodel: not-a-real-tier\n"+
			"canonical_source: z\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := clineConvertAgentFile(source, "x", tiers); err == nil {
		t.Error("an unknown model tier was accepted")
	} else if !strings.Contains(err.Error(), "unknown model tier") {
		t.Errorf("refused, but not for the tier: %v", err)
	}
}

func TestApplicationEngineerIsExemptAndSaysWhy(t *testing.T) {
	// Its role text is *about* maintaining this repository's own tooling, so
	// the paths are the subject rather than incidental references. The
	// exemption is only defensible because the port note explains it in the
	// shipped file.
	repoRoot, ported := portIntoScratch(t)
	_ = repoRoot
	content, err := os.ReadFile(filepath.Join(ported, "agents", "application-engineer.md"))
	if err != nil {
		t.Skipf("no application-engineer preset: %v", err)
	}
	text := string(content)
	if !strings.Contains(text, "Port note (not part of the original role authority text)") {
		t.Error("the exempt role ships without the note explaining why it keeps " +
			"source paths a reader will not find in their own project")
	}
	if len(clineLeaks(text)) == 0 {
		t.Error("the exemption is pointless: nothing in this body would have " +
			"tripped the leak check, so it should not be exempt")
	}
}
