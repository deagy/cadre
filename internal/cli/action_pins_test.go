package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// A SHA-pinned action must name a commit that actually exists.
//
// Pinning by SHA is the supply-chain-correct thing to do, and it moves the
// failure mode somewhere nothing was looking: a fabricated pin is still forty
// hexadecimal characters. It parses, it reviews clean, and YAML linters have
// no opinion on it. The workflow fails at "Set up job" -- before any step of
// ours runs -- with "unable to resolve action".
//
// release.yml's `cli` matrix carried
//
//	actions/upload-artifact@6f51ac03b9356f520e9adb1b1b122d8733d54848 # v4.5.0
//
// against a real v4.5.0 of ...b1b7802705f340c2b. The first twenty-six
// characters are correct and the rest is invented, which is what a truncated
// SHA completed from memory looks like. It sat there through every pull
// request, because the job it belongs to only runs on a release -- and the
// release gate was independently broken, so it had never once executed. Two
// dormant faults hid each other until the first real CLI release.
//
// The offline half of this guard catches the cheap cases and runs always. The
// half that would have caught *this* case has to ask GitHub, so it is opt-in:
// see TestEveryPinnedActionShaResolvesUpstream.

// Trailing-comment form is the convention throughout .github/workflows: the
// SHA is what runs, the comment is what a human reads.
var shaPinnedUses = regexp.MustCompile(`uses:\s*(\S+?)@([0-9a-fA-F]{40})\s*(?:#\s*(\S+))?`)

type actionPin struct {
	action  string // e.g. actions/upload-artifact, or anchore/sbom-action/download-syft
	sha     string
	version string // the trailing comment, e.g. v4.5.0
	source  string // "release.yml:403"
}

// repo returns the owner/name to query. An action may live in a subdirectory
// (anchore/sbom-action/download-syft) or be a reusable workflow
// (slsa-framework/slsa-github-generator/.github/workflows/x.yml); in both
// cases the commit belongs to the first two path segments.
func (p actionPin) repo() string {
	parts := strings.Split(p.action, "/")
	if len(parts) < 2 {
		return p.action
	}
	return parts[0] + "/" + parts[1]
}

func collectActionPins(t *testing.T) []actionPin {
	t.Helper()
	root := filepath.Dir(filepath.Dir(mustGetwd(t)))
	workflows, err := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatalf("globbing workflows: %v", err)
	}
	if len(workflows) == 0 {
		t.Fatal("no workflows found; this guard checked nothing")
	}

	var pins []actionPin
	for _, path := range workflows {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		for i, line := range strings.Split(string(contents), "\n") {
			m := shaPinnedUses.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			pins = append(pins, actionPin{
				action:  m[1],
				sha:     m[2],
				version: m[3],
				source:  fmt.Sprintf("%s:%d", filepath.Base(path), i+1),
			})
		}
	}
	if len(pins) == 0 {
		t.Fatal("no SHA-pinned actions found; this guard checked nothing")
	}
	return pins
}

// The offline half: form, and agreement between pins of the same action.
//
// Two pins of one action that claim the same version must name the same
// commit, and one commit must not be labelled two different versions. Neither
// catches a lone fabricated pin -- there is nothing to disagree with -- but
// both catch the copy-paste that produces a second one.
func TestShaPinnedActionsAreWellFormedAndAgree(t *testing.T) {
	pins := collectActionPins(t)

	byVersion := map[string]actionPin{} // action@version -> first pin seen
	bySHA := map[string]actionPin{}     // action@sha     -> first pin seen
	var findings []string

	for _, p := range pins {
		if p.sha != strings.ToLower(p.sha) {
			findings = append(findings, fmt.Sprintf(
				"%s: %s is pinned with an uppercase SHA; GitHub matches these case-sensitively",
				p.source, p.action))
		}
		if p.version == "" {
			findings = append(findings, fmt.Sprintf(
				"%s: %s@%s… has no trailing version comment, so nobody can tell what it is meant to be",
				p.source, p.action, p.sha[:12]))
			continue
		}

		key := p.action + "@" + p.version
		if first, ok := byVersion[key]; ok && first.sha != p.sha {
			findings = append(findings, fmt.Sprintf(
				"%s and %s both claim %s but name different commits (%s… vs %s…); at most one can be right",
				first.source, p.source, key, first.sha[:12], p.sha[:12]))
		} else if !ok {
			byVersion[key] = p
		}

		shaKey := p.action + "@" + p.sha
		if first, ok := bySHA[shaKey]; ok && first.version != p.version {
			findings = append(findings, fmt.Sprintf(
				"%s and %s pin %s to the same commit but label it %s and %s",
				first.source, p.source, p.action, first.version, p.version))
		} else if !ok {
			bySHA[shaKey] = p
		}
	}

	if len(findings) > 0 {
		sort.Strings(findings)
		t.Fatalf("SHA pins disagree:\n  %s", strings.Join(findings, "\n  "))
	}
	t.Logf("checked %d SHA-pinned action references", len(pins))
}

// The half that catches a fabricated pin, which requires asking GitHub.
//
// Opt-in rather than default: a test that reaches the network fails offline
// for reasons that have nothing to do with the repository, and a guard that
// cries wolf on a plane gets deleted. CI sets CADRE_VERIFY_ACTION_PINS=1,
// where the network is a given and the answer is worth having on every pull
// request rather than at the next release.
func TestEveryPinnedActionShaResolvesUpstream(t *testing.T) {
	if os.Getenv("CADRE_VERIFY_ACTION_PINS") == "" {
		t.Skip("set CADRE_VERIFY_ACTION_PINS=1 to resolve action pins against GitHub")
	}

	pins := collectActionPins(t)
	client := &http.Client{Timeout: 20 * time.Second}

	// One request per distinct repo@sha: actions/checkout alone appears
	// fifteen times, and the rate limit is not generous unauthenticated.
	seen := map[string]bool{}
	var findings []string
	checked := 0

	for _, p := range pins {
		key := p.repo() + "@" + p.sha
		if seen[key] {
			continue
		}
		seen[key] = true

		url := fmt.Sprintf("https://api.github.com/repos/%s/commits/%s", p.repo(), p.sha)
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			t.Fatalf("building request for %s: %v", key, err)
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		if token := os.Getenv("GITHUB_TOKEN"); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("resolving %s: %v (network required for this guard)", key, err)
		}
		body := struct {
			SHA string `json:"sha"`
		}{}
		decodeErr := json.NewDecoder(resp.Body).Decode(&body)
		status := resp.StatusCode
		resp.Body.Close()

		switch {
		case status == http.StatusOK && decodeErr == nil && strings.EqualFold(body.SHA, p.sha):
			checked++
		case status == http.StatusNotFound || status == http.StatusUnprocessableEntity:
			findings = append(findings, fmt.Sprintf(
				"%s: %s@%s (%s) does not exist upstream -- the workflow will fail at 'Set up job'",
				p.source, p.action, p.sha, p.version))
		case status == http.StatusForbidden || status == http.StatusTooManyRequests:
			t.Skipf("GitHub rate-limited this run (%d) after %d pins; not a repository fault", status, checked)
		default:
			t.Fatalf("resolving %s: unexpected status %d", key, status)
		}
	}

	if len(findings) > 0 {
		sort.Strings(findings)
		t.Fatalf("pinned actions that do not resolve:\n  %s", strings.Join(findings, "\n  "))
	}
	t.Logf("resolved %d distinct pinned commits upstream", checked)
}
