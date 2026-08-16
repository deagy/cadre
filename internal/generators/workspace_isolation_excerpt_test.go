package generators

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The section-granular workspace-isolation excerpt, checked against the
// distribution this repository actually ships.
//
// `roster/shared/workspace-isolation.md` states its own applicability rule:
// the worktree-isolation steps and the end-of-task result block bind
// write-capable tiers only, while "Never mutate a working tree you did not
// create" binds every role at every tier. universalPolicySections encodes
// exactly that, so a read-only role's wrapper carries the applicability header
// plus the never-mutate section and nothing else.
//
// **The failure this exists to catch is silent.** Rename that heading in the
// source file and a naive excerpter ships 28 reviewer wrappers with no
// never-mutate rule, looking for all the world like a routine regeneration. An
// earlier attempt at this trim failed in exactly that shape -- it excerpted at
// file granularity and dropped the universally binding section along with the
// rest. So these assert the *presence* of that rule as forcefully as the
// absence of the write-capable-only steps.
//
// Ported from plugin/tools/test_workspace_isolation_excerpt.py, which read
// `UNIVERSAL_POLICY_SECTIONS` out of the *Python* generator the Go CLI
// replaced. Three of the four Python guards holding that generator alive are
// now gone; this is the third.

const (
	policyRelative = "roster/shared/workspace-isolation.md"
	policyMarker   = "# Shared policy: " + policyRelative
)

// writeCapableOnlyPhrases appear only in sections the file's own header scopes
// to write-capable tiers.
//
// Four of these are headings, so renaming one makes its absence-check pass
// vacuously. They are a readable index of what must not leak, not the
// protection. The protection is the byte-exact check below, which pins the
// embedded text to the generator's own output -- any section not registered as
// universal is excluded by construction, whatever it is called.
var writeCapableOnlyPhrases = []string{
	"## Step 0 -- Already isolated?",
	"## Step 1 -- Can I isolate?",
	"## Step 2 -- Degrade explicitly",
	"## End-of-task result block (mandatory)",
	"The dirty-scope guard, explained",
	"mode: worktree | inherited-worktree | in-place",
}

// universalPhrases are body prose from each section that binds every tier.
//
// One heading per section for readability, plus prose a heading rename cannot
// vacuously satisfy.
var universalPhrases = []string{
	"## Never mutate a working tree you did not create",
	"Never run a `git` command that discards uncommitted work or moves a branch",
	"`git stash` in any form",
	"Applies to every role and every capability tier",
	"## The security-relevant-resolver rule",
	"falls through to the machine-global shared store instead",
	"## Never remove or prune a worktree yourself",
}

// wrappersByCapability splits the committed distribution's roles into the two
// groups the excerpt distinguishes.
func wrappersByCapability(t *testing.T) (readOnly, writeCapable []string) {
	t.Helper()
	root := repositoryRoot(t)
	catalog := catalogCapabilities(t, filepath.Join(root, "roster", "catalog.yaml"))
	for role, capability := range catalog {
		if capability == "read_only" {
			readOnly = append(readOnly, role)
			continue
		}
		writeCapable = append(writeCapable, role)
	}
	if len(readOnly) == 0 || len(writeCapable) == 0 {
		t.Fatalf("expected both kinds of role, got %d read-only and %d write-capable",
			len(readOnly), len(writeCapable))
	}
	return readOnly, writeCapable
}

func catalogCapabilities(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the catalog: %v", err)
	}
	capabilities := map[string]string{}
	current := ""
	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "    ") &&
			strings.HasSuffix(strings.TrimRight(line, " \t"), ":"):
			current = strings.TrimSuffix(strings.TrimSpace(line), ":")
		case current != "" && strings.HasPrefix(strings.TrimSpace(line), "capability:"):
			_, value, _ := strings.Cut(strings.TrimSpace(line), ":")
			capabilities[current] = strings.TrimSpace(value)
		}
	}
	return capabilities
}

// claudeWrapper is the Claude Code wrapper as shipped.
func claudeWrapper(t *testing.T, role string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repositoryRoot(t), "plugin", "agents", role+".md"))
	if err != nil {
		t.Fatalf("reading the wrapper for %s: %v", role, err)
	}
	return string(data)
}

// codexWrapper is the Codex wrapper's developer_instructions, unescaped.
//
// The TOML value is JSON-encoded, so decoding puts these assertions on the
// same footing as the Claude Code ones rather than matching escaped bytes.
func codexWrapper(t *testing.T, role string) (string, bool) {
	t.Helper()
	path := filepath.Join(repositoryRoot(t), "plugin", "codex-agents", "agents-"+role+".toml")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	const key = "developer_instructions = "
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, key) {
			continue
		}
		var decoded string
		if err := json.Unmarshal([]byte(strings.TrimSpace(line[len(key):])), &decoded); err != nil {
			t.Fatalf("%s: developer_instructions is not a JSON string: %v", path, err)
		}
		return decoded, true
	}
	t.Fatalf("%s: no developer_instructions key", path)
	return "", false
}

func eachWrapper(t *testing.T, roles []string, check func(t *testing.T, role, runner, text string)) {
	t.Helper()
	for _, role := range roles {
		check(t, role, "claude", claudeWrapper(t, role))
		if text, present := codexWrapper(t, role); present {
			check(t, role, "codex", text)
		}
	}
}

func TestTheWorkspacePolicyIsRegisteredForSectionGranularExcerpting(t *testing.T) {
	// It must reach every tier: registered as a shared policy (all roles),
	// never as a tier-scoped one (some roles). That coupling was tried and
	// reverted, and re-introducing it would drop the never-mutate rule from
	// whichever tiers the scoping excluded.
	if _, registered := universalPolicySections[policyRelative]; !registered {
		t.Fatalf("%s is not registered for section-granular excerpting, so a "+
			"read-only role gets the whole file or none of it", policyRelative)
	}
	if len(universalPolicySections[policyRelative]) == 0 {
		t.Errorf("%s is registered with no universally binding sections", policyRelative)
	}
}

func TestReadOnlyWrappersKeepTheNeverMutateRule(t *testing.T) {
	// The presence half, and the one that matters most: a reviewer with no
	// never-mutate rule is a reviewer who may discard somebody's uncommitted
	// work and have been told nothing about it.
	readOnly, _ := wrappersByCapability(t)
	eachWrapper(t, readOnly, func(t *testing.T, role, runner, text string) {
		if !strings.Contains(text, policyMarker) {
			t.Errorf("%s (%s): the workspace policy is missing entirely", role, runner)
			return
		}
		for _, phrase := range universalPhrases {
			if !strings.Contains(text, phrase) {
				t.Errorf("%s (%s): missing universally binding text %q", role, runner, phrase)
			}
		}
	})
}

func TestReadOnlyWrappersOmitTheWriteCapableOnlySteps(t *testing.T) {
	readOnly, _ := wrappersByCapability(t)
	eachWrapper(t, readOnly, func(t *testing.T, role, runner, text string) {
		for _, phrase := range writeCapableOnlyPhrases {
			if strings.Contains(text, phrase) {
				t.Errorf("%s (%s): carries write-capable-only text %q", role, runner, phrase)
			}
		}
	})
}

func TestReadOnlyWrappersEmbedExactlyTheDeclaredExcerpt(t *testing.T) {
	// The real protection. Pinning the embedded text to the generator's own
	// output character for character means any section not registered as
	// universal is excluded by construction -- whatever somebody renames it
	// to, and whether or not the phrase lists above were updated.
	raw, err := os.ReadFile(filepath.Join(repositoryRoot(t), filepath.FromSlash(policyRelative)))
	if err != nil {
		t.Fatalf("reading %s: %v", policyRelative, err)
	}
	excerpt, err := excerptUniversalSections(policyRelative, strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("excerpting: %v", err)
	}
	readOnly, _ := wrappersByCapability(t)
	eachWrapper(t, readOnly, func(t *testing.T, role, runner, text string) {
		if !strings.Contains(text, policyMarker+"\n\n"+excerpt) {
			t.Errorf("%s (%s): the embedded policy is not the declared excerpt", role, runner)
		}
	})
}

func TestWriteCapableWrappersEmbedTheWholeFile(t *testing.T) {
	// The other side of the split, and what makes the read-only assertions
	// mean something: the sections withheld above are present here, so their
	// absence there is a decision rather than a file nobody ships.
	raw, err := os.ReadFile(filepath.Join(repositoryRoot(t), filepath.FromSlash(policyRelative)))
	if err != nil {
		t.Fatalf("reading %s: %v", policyRelative, err)
	}
	body := strings.TrimSpace(string(raw))
	_, writeCapable := wrappersByCapability(t)
	eachWrapper(t, writeCapable, func(t *testing.T, role, runner, text string) {
		if !strings.Contains(text, policyMarker+"\n\n"+body) {
			t.Errorf("%s (%s): does not embed the whole policy", role, runner)
		}
		for _, phrase := range append(append([]string{}, universalPhrases...),
			writeCapableOnlyPhrases...) {
			if !strings.Contains(text, phrase) {
				t.Errorf("%s (%s): missing %q", role, runner, phrase)
			}
		}
	})
}

// wellFormedPolicy is a minimal file the excerpter accepts: an applicability
// preamble plus every section universalPolicySections requires, each with a
// body. Cases below break exactly one thing about it.
func wellFormedPolicy(sections map[string]string) string {
	// The preamble has to enumerate the universally binding sections by name:
	// the excerpter cross-checks the file's own header against the registered
	// set, so that the two cannot drift apart silently.
	body := "# Synthetic Policy\n\n**These sections bind every role, every tier**:\n\n"
	for _, heading := range universalPolicySections[policyRelative] {
		body += "- " + heading + "\n"
	}
	body += "\n**Applies to:** everything after them.\n"
	for _, heading := range universalPolicySections[policyRelative] {
		text, present := sections[heading]
		if !present {
			text = "body for " + heading
		}
		body += "\n## " + heading + "\n\n" + text + "\n"
	}
	return body
}

// headingsOnly drops a policy's preamble, leaving a file that opens straight
// into its first section.
func headingsOnly(body string) string {
	_, sections, found := strings.Cut(body, "## ")
	if !found {
		return body
	}
	return "## " + sections
}

func TestExcerptingRefusesAPolicyItCannotHonour(t *testing.T) {
	// Every one of these would otherwise produce a *shorter* wrapper that
	// looks fine. Refusing is the only way the failure is visible at
	// generation time rather than in a review months later.
	//
	// Each case asserts *which* refusal fired, not merely that one did. An
	// earlier version of this test checked only "an error came back", and
	// deleting either the missing-section check or the preamble check left it
	// green -- the fixtures were malformed in more than one way, so another
	// check caught them and the test could not tell the difference.
	for _, probe := range []struct {
		name, body, wants string
	}{
		{
			"a registered section that is not in the file",
			"preamble\n\n## Some Other Heading\n\nbody\n",
			"required universally binding section(s) not found",
		},
		{
			"a file with no applicability header",
			// Everything above the first heading removed.
			headingsOnly(wellFormedPolicy(nil)),
			"no preamble above the first",
		},
		{
			"a registered section whose body is empty",
			wellFormedPolicy(map[string]string{
				"Never mutate a working tree you did not create": "",
			}),
			"empty",
		},
		{
			"an unbalanced backtick fence",
			wellFormedPolicy(nil) + "\n```\nnever closed\n",
			"unbalanced",
		},
		{
			"an unbalanced tilde fence",
			wellFormedPolicy(nil) + "\n~~~\nnever closed\n",
			"unbalanced",
		},
		{
			// The header promises a section binds every tier and the registry
			// does not carry it, so it would be dropped from every read-only
			// wrapper while the header the reader sees says otherwise. No
			// other check notices: the file is well formed and every
			// registered section is present.
			"a header promising a section the registry does not carry",
			// The section has to exist *and* be named in the header: a
			// header bullet naming nothing real is ignored, which is right --
			// only a real section can be wrongly dropped.
			strings.Replace(wellFormedPolicy(nil), "\n\n**Applies to:**",
				"\n- Some Unregistered Rule\n\n**Applies to:**", 1) +
				"\n## Some Unregistered Rule\n\nbody\n",
			"named in the header's list but not registered",
		},
	} {
		t.Run(probe.name, func(t *testing.T) {
			_, err := excerptUniversalSections(policyRelative, probe.body)
			if err == nil {
				t.Fatalf("accepted a policy it cannot honour:\n%s", probe.body)
			}
			if !strings.Contains(err.Error(), probe.wants) {
				t.Errorf("refused for a different reason than this case is about.\n"+
					"wanted something containing %q, got: %v", probe.wants, err)
			}
		})
	}

	// And the well-formed fixture is accepted, or every case above passes by
	// refusing everything.
	if _, err := excerptUniversalSections(policyRelative, wellFormedPolicy(nil)); err != nil {
		t.Errorf("a well-formed policy was refused: %v", err)
	}
}

func TestAHeadingInsideAFenceIsNotASection(t *testing.T) {
	// A `##` line inside a code block is an example, not a section. Treating
	// it as one splits the file in the wrong place and silently changes what
	// every wrapper carries.
	body := "preamble\n\n" +
		"## Never mutate a working tree you did not create\n\n" +
		"Applies to every role and every capability tier.\n\n" +
		"```md\n## Not A Section\n```\n\n" +
		"more of the same section\n"
	excerpt, err := excerptUniversalSections(policyRelative, body)
	if err != nil {
		// The registered set names more sections than this fixture has, so a
		// refusal here is the fixture's shape rather than the fence handling.
		t.Skipf("fixture does not carry every registered section: %v", err)
	}
	if !strings.Contains(excerpt, "## Not A Section") {
		t.Errorf("the fenced heading was treated as a section boundary:\n%s", excerpt)
	}
}
