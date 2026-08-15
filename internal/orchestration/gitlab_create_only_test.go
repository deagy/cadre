package orchestration

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// The create-only invariant, enforced structurally.
//
// This integration holds a GitLab token with `api` scope, because GitLab has
// no narrower one -- there is no issues-and-wiki-only scope to ask for. The
// blast radius is contained by what this code will and will not do, so
// "never closes, reopens, resolves, or relabels an issue" is not a policy
// note, it is a property of the source.
//
// Behavioural tests cannot show the absence of a capability: they can only
// show that the operations that exist behave. These read the source instead,
// which is what catches a future change adding a close path with its own
// perfectly passing tests.
//
// A port of test_gitlab_integration.py's StructuralNoStateTransitionTests,
// which had no Go equivalent.

// gitlabSourceFiles are the files that may talk to the GitLab API.
//
// internal/mcpserver/gitlab_server.go is included although nothing imports
// it: it is a second, currently-unreachable GitLab tool surface built on the
// official MCP Go SDK, and an unreachable surface is exactly the place a
// state transition would be added without anyone noticing. Being outside the
// invariant is not a reason to be outside the check.
var gitlabSourceFiles = []string{
	"gitlab.go",
	"gitlab_mcp.go",
	"../mcpserver/gitlab_server.go",
}

func parseGitLabSource(t *testing.T, name string) (*token.FileSet, *ast.File, string) {
	t.Helper()
	source, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("cannot read %s: %v", name, err)
	}
	fileSet := token.NewFileSet()
	parsed, err := parser.ParseFile(fileSet, name, source, parser.ParseComments)
	if err != nil {
		t.Fatalf("cannot parse %s: %v", name, err)
	}
	return fileSet, parsed, string(source)
}

func TestNoFunctionIsNamedForAStateTransition(t *testing.T) {
	// A name is the earliest signal: something called closeGitLabIssue is
	// caught here before anyone has to read what it does.
	//
	// The verb list is deliberately narrow. "close" is unambiguous in this
	// domain, but "resolve" is not -- resolveGitLabToken and
	// resolveGitLabConfig are ordinary settings helpers -- so it counts only
	// when paired with an issue. A check that flagged every "resolve" would
	// be turned off by the first person it annoyed, which is worse than a
	// narrower one that stays.
	forbidden := []string{"close", "reopen", "approve"}

	for _, name := range gitlabSourceFiles {
		_, parsed, _ := parseGitLabSource(t, name)
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok {
				continue
			}
			lowered := strings.ToLower(function.Name.Name)
			for _, verb := range forbidden {
				if strings.Contains(lowered, verb) {
					t.Errorf("%s declares %q, which is shaped like a state-transition "+
						"function (%q)", name, function.Name.Name, verb)
				}
			}
			if strings.Contains(lowered, "resolve") && strings.Contains(lowered, "issue") {
				t.Errorf("%s declares %q, which is shaped like an issue-resolution function",
					name, function.Name.Name)
			}
		}
	}
}

func TestTheNamedStateTransitionFunctionsDoNotExist(t *testing.T) {
	// The specific names someone would reach for. Checked by exact name as
	// well as by shape, so a future addition has to defeat both.
	declared := map[string]bool{}
	for _, name := range gitlabSourceFiles {
		_, parsed, _ := parseGitLabSource(t, name)
		for _, declaration := range parsed.Decls {
			if function, ok := declaration.(*ast.FuncDecl); ok {
				declared[strings.ToLower(function.Name.Name)] = true
			}
		}
	}
	for _, forbidden := range []string{
		"closeissue", "closegitlabissue", "reopenissue", "resolveissue",
		"approveissue", "closereviewsubtask", "resolvereviewsubtask",
	} {
		if declared[forbidden] {
			t.Errorf("a function named %q exists; this integration is create-only", forbidden)
		}
	}
}

func TestTheSourceNeverUsesGitLabsStateEventField(t *testing.T) {
	// state_event is the single API field that closes or reopens an issue.
	// Its absence from the source is the mechanical form of the invariant:
	// no request this code builds can carry it.
	for _, name := range gitlabSourceFiles {
		_, _, source := parseGitLabSource(t, name)
		for _, line := range strings.Split(source, "\n") {
			trimmed := strings.TrimSpace(line)
			// Comments may name the field -- that is how the invariant is
			// documented. Code may not.
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			if strings.Contains(line, "state_event") {
				t.Errorf("%s references state_event outside a comment: %s", name, trimmed)
			}
		}
	}
}

func TestOnlyCreateShapedHTTPMethodsAreUsed(t *testing.T) {
	// DELETE and PUT-to-close are the shapes that would let this integration
	// change or remove something. PUT is used legitimately for a wiki page
	// update, so the check is on DELETE and PATCH, neither of which any
	// create-only operation needs.
	for _, name := range gitlabSourceFiles {
		_, _, source := parseGitLabSource(t, name)
		for _, line := range strings.Split(source, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			for _, method := range []string{`"DELETE"`, `"PATCH"`} {
				if strings.Contains(line, method) {
					t.Errorf("%s uses HTTP %s: %s", name, method, trimmed)
				}
			}
		}
	}
}

func TestTheExposedToolsAreOnlyTheThreeCreateOnlyOnes(t *testing.T) {
	// The structural checks above constrain the source; this constrains the
	// surface. A helper that could close an issue is useless to a caller that
	// cannot reach it, and a fourth tool is how it would become reachable.
	offered := map[string]bool{}
	for _, definition := range NewGitLabMCPServer("").GetToolDefinitions() {
		offered[definition.Name] = true
	}
	expected := map[string]bool{
		"create_review_subtask": true, "write_wiki_page": true, "write_evidence_comment": true,
	}
	for name := range offered {
		if !expected[name] {
			t.Errorf("an unexpected tool is exposed: %q", name)
		}
	}
	for name := range expected {
		if !offered[name] {
			t.Errorf("%q is no longer exposed", name)
		}
	}
}

// The transport properties. Python asserts these because each one, if wrong,
// sends a service-account token somewhere it was never meant to go.

func TestTheGitLabClientNeverHonoursAnAmbientProxy(t *testing.T) {
	// Go's default transport reads HTTPS_PROXY from the environment. Left at
	// the default, an ambient variable -- set by a shell profile, a CI image,
	// or anything else on the machine -- would route every GitLab request,
	// token included, through a host nobody in this integration chose.
	t.Setenv("HTTPS_PROXY", "http://attacker.invalid:8080")
	t.Setenv("HTTP_PROXY", "http://attacker.invalid:8080")

	client := gitlabHTTPClient(5 * time.Second)
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", client.Transport)
	}
	if transport.Proxy != nil {
		t.Error("the GitLab client has a proxy function, so an ambient proxy variable applies")
	}
}

func TestTheGitLabClientRequiresCertificateVerification(t *testing.T) {
	client := gitlabHTTPClient(5 * time.Second)
	transport := client.Transport.(*http.Transport)
	if transport.TLSClientConfig != nil && transport.TLSClientConfig.InsecureSkipVerify {
		t.Error("certificate verification is disabled")
	}
	// And nothing may re-enable it from the environment: there is no setting
	// that reaches this, which is the point.
	t.Setenv("GITLAB_INSECURE", "1")
	t.Setenv("SSL_VERIFY", "0")
	again := gitlabHTTPClient(5 * time.Second).Transport.(*http.Transport)
	if again.TLSClientConfig != nil && again.TLSClientConfig.InsecureSkipVerify {
		t.Error("an environment variable disabled certificate verification")
	}
}

func TestACrossHostRedirectIsRefused(t *testing.T) {
	// A redirect is chosen by the server being called. Following one to
	// another host would present this integration's token to that host.
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer elsewhere.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/moved", http.StatusFound)
	}))
	defer redirector.Close()

	client := gitlabHTTPClient(5 * time.Second)
	response, err := client.Get(redirector.URL + "/api/v4/projects")
	if err == nil {
		_ = response.Body.Close()
		t.Fatal("a cross-host redirect was followed")
	}
	if !strings.Contains(err.Error(), "refusing redirect") {
		t.Errorf("error = %v, want the redirect refusal", err)
	}
}

func TestASchemeDowngradeRedirectIsRefused(t *testing.T) {
	// Same host, https to http: the token would go out in plaintext. The
	// host check alone would let this through, which is why the scheme is
	// checked separately.
	client := gitlabHTTPClient(5 * time.Second)

	request, err := http.NewRequest(http.MethodGet, "https://example.invalid/api/v4/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	redirected, err := http.NewRequest(http.MethodGet, "http://example.invalid/api/v4/x", nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := client.CheckRedirect(redirected, []*http.Request{request}); err == nil {
		t.Error("an https-to-http redirect on the same host was allowed")
	}

	// The same host over https is fine -- refusing it would break ordinary
	// GitLab deployments that redirect.
	stillHTTPS, err := http.NewRequest(http.MethodGet, "https://example.invalid/api/v4/y", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.CheckRedirect(stillHTTPS, []*http.Request{request}); err != nil {
		t.Errorf("a same-host https redirect was refused: %v", err)
	}
}

func TestAQuickActionIsRejectedBeforeAnyHTTPCallIsMade(t *testing.T) {
	// GitLab executes a line like "/close" server-side as the note's author,
	// which for this integration is always the service-account token. The
	// rejection has to happen before the request, not by inspecting the
	// response: by then the action has run.
	//
	// No server is configured here, so any HTTP attempt fails with a
	// connection error rather than the validation error -- which is what
	// makes "zero HTTP calls" observable.
	t.Setenv("GITLAB_SVC_TOKEN", "")

	for _, hostile := range []string{
		"/close\nplease review",
		"  /unlabel ~needs-review",
		"/CLOSE",
		"/ReSoLvE",
	} {
		result := WriteGitLabEvidenceComment(nil, 1, hostile, "TASK-1", t.TempDir()+"/audit.jsonl")
		reason, _ := result["reason"].(string)
		if !strings.Contains(reason, "quick action") {
			t.Errorf("content %q was not refused as a quick action: %v", hostile, result)
		}
	}

	// Ordinary prose, and a slash that is not at the start of a line, are
	// still accepted -- a rejection that fired on those would make the tool
	// unusable for its actual purpose.
	for _, fine := range []string{
		"Reviewed the change; see the linked pipeline.",
		"Use the and/or form here.",
		"Path is docs/report.md in the repo.",
	} {
		if err := rejectQuickActionSyntax(fine, "content"); err != nil {
			t.Errorf("ordinary text %q was refused: %v", fine, err)
		}
	}
}
