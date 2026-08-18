package gitlabissue

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// percentEncode must match Python's quote(value, safe="") exactly.
//
// The pairs below were produced by the Python being replaced. The one that
// matters most is "group/project": a GitLab project path is a path, and the
// API needs it as a single encoded segment. Go's url.PathEscape leaves the
// slash alone and would address a different endpoint; url.QueryEscape renders
// a space as "+" rather than %20.
func TestPercentEncodeMatchesPythonQuote(t *testing.T) {
	pinned := map[string]string{
		"group/project":  "group%2Fproject",
		"group/sub/proj": "group%2Fsub%2Fproj",
		"a b":            "a%20b",
		"a+b":            "a%2Bb",
		"ünicode":        "%C3%BCnicode",
		"a~b":            "a~b",
		"a.b_c-d":        "a.b_c-d",
	}
	for input, want := range pinned {
		if got := percentEncode(input); got != want {
			t.Errorf("percentEncode(%q) = %q, python produced %q", input, got, want)
		}
	}
}

func TestParseIssueURI(t *testing.T) {
	reference := ParseIssueURI("gitlab-issue:group/project:issues/42")
	if reference == nil {
		t.Fatal("a well-formed URI did not parse")
	}
	if reference.ProjectPath != "group/project" || reference.IID != "42" {
		t.Errorf("parsed %+v, want group/project and 42", reference)
	}

	for _, malformed := range []string{
		"gitlab-issue:group/project:issues/",
		"gitlab-issue:group/project:issues/abc",
		"gitlab-issue::issues/1",
		"github-issue:group/project:issues/1",
		"gitlab-issue:group/project:merge_requests/1",
		"",
		// A trailing newline must not be accepted: Go's regexp `$` matches
		// before a final newline, so the anchors have to be checked.
		"gitlab-issue:group/project:issues/42\n",
	} {
		if reference := ParseIssueURI(malformed); reference != nil {
			t.Errorf("ParseIssueURI(%q) parsed to %+v, want no match", malformed, reference)
		}
	}
}

func TestIssueURIParsesItsOwnOutput(t *testing.T) {
	uri, err := IssueURI("group/project", 42)
	if err != nil {
		t.Fatalf("IssueURI: %v", err)
	}
	if uri != "gitlab-issue:group/project:issues/42" {
		t.Errorf("built %q", uri)
	}
	// A project path the URI grammar cannot express must be refused rather
	// than returned as a URI nothing can parse back.
	if _, err := IssueURI("group project", 42); err == nil {
		t.Error("a project path with a space produced a URI")
	}
}

func writeMock(t *testing.T, envVar, contents string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mock.json")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("writing the mock: %v", err)
	}
	t.Setenv(envVar, path)
}

func TestFetchIssueUsesTheMockAndValidatesIt(t *testing.T) {
	writeMock(t, IssueMockEnvVar, `{"title": "Add a thing", "state": "opened", "web_url": "https://x/1"}`)
	issue, err := FetchIssue("group/project", 7)
	if err != nil {
		t.Fatalf("FetchIssue: %v", err)
	}
	if issue.IID != 7 || issue.Title != "Add a thing" || issue.State != "opened" {
		t.Errorf("fetched %+v", issue)
	}

	writeMock(t, IssueMockEnvVar, `{"title": "", "state": "opened"}`)
	if _, err := FetchIssue("group/project", 7); err == nil || !strings.Contains(err.Error(), "missing a title") {
		t.Errorf("an untitled issue produced %v", err)
	}

	writeMock(t, IssueMockEnvVar, `{"title": "t", "state": "merged"}`)
	if _, err := FetchIssue("group/project", 7); err == nil || !strings.Contains(err.Error(), "unrecognized state") {
		t.Errorf("an unknown state produced %v", err)
	}
}

func TestResolveIssueReference(t *testing.T) {
	writeMock(t, IssueMockEnvVar, `{"title": "t", "state": "opened"}`)

	uri, err := ResolveIssueReference("group/project#12")
	if err != nil {
		t.Fatalf("ResolveIssueReference: %v", err)
	}
	if uri != "gitlab-issue:group/project:issues/12" {
		t.Errorf("resolved to %q", uri)
	}

	if uri, err := ResolveIssueReference(""); err != nil || uri != "" {
		t.Errorf("an empty reference produced (%q, %v), want empty and no error", uri, err)
	}

	for _, malformed := range []string{"group/project", "#12", "group/project#", "group/project#abc"} {
		if _, err := ResolveIssueReference(malformed); err == nil {
			t.Errorf("ResolveIssueReference(%q) was accepted", malformed)
		}
	}
}

// Identity is matched case-insensitively, and the confirmed name is returned
// rather than the requested one.
func TestVerifyIdentity(t *testing.T) {
	writeMock(t, CreateMockEnvVar, `{"identity": {"username": "Release-Bot"}}`)

	confirmed, err := VerifyIdentity("release-bot")
	if err != nil {
		t.Fatalf("VerifyIdentity: %v", err)
	}
	if confirmed != "Release-Bot" {
		t.Errorf("returned %q, want the name the credential actually confirmed", confirmed)
	}

	if _, err := VerifyIdentity("someone-else"); err == nil {
		t.Error("a mismatched identity was accepted")
	} else if !strings.Contains(err.Error(), "does not match required bot identity") {
		t.Errorf("error was %q", err)
	}

	writeMock(t, CreateMockEnvVar, `{"identity": {}}`)
	if _, err := VerifyIdentity("release-bot"); err == nil || !strings.Contains(err.Error(), "missing a username") {
		t.Errorf("an identity with no username produced %v", err)
	}
}

func TestSearchIssuesByLabels(t *testing.T) {
	writeMock(t, CreateMockEnvVar, `{"search": {"a,b": [{"iid": 1}]}}`)

	results, err := SearchIssuesByLabels("group/project", []string{"a", "b"})
	if err != nil {
		t.Fatalf("SearchIssuesByLabels: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("got %d results, want 1", len(results))
	}

	// An unmatched label set is empty, not an error.
	empty, err := SearchIssuesByLabels("group/project", []string{"z"})
	if err != nil || len(empty) != 0 {
		t.Errorf("an unmatched label set produced (%v, %v)", empty, err)
	}
}
