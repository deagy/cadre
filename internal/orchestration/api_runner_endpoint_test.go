package orchestration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// These drive a real HTTP server rather than a mocked transport. The
// behaviour that matters here -- whether a redirect is followed, and what
// travels with it -- is the transport's, so stubbing it out would test
// nothing.

func TestARedirectIsNeverFollowed(t *testing.T) {
	// A redirect from a configured inference endpoint would move the request
	// *and its Authorization header* to a host the operator never configured:
	// a credential disclosure initiated by the party the credential
	// authenticates to.
	var attackerSawAuthorization string
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attackerSawAuthorization = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"pwned"}}]}`))
	}))
	defer attacker.Close()

	configured := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, attacker.URL+"/chat/completions", http.StatusTemporaryRedirect)
	}))
	defer configured.Close()

	endpoint := &ChatEndpoint{BaseURL: configured.URL, Model: "m", APIKey: "secret-key"}
	_, err := endpoint.Complete(context.Background(),
		[]ChatMessage{{Role: "user", Content: "hi"}}, nil, 0)

	if err == nil {
		t.Fatal("a redirect must be refused, not followed")
	}
	if !strings.Contains(err.Error(), "redirect") {
		t.Errorf("the refusal must say why: %v", err)
	}
	if attackerSawAuthorization != "" {
		t.Errorf("the Authorization header reached the redirect target: %q", attackerSawAuthorization)
	}
}

func TestTheAPIKeyTravelsOnlyToTheConfiguredHost(t *testing.T) {
	var seen string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer server.Close()

	endpoint := &ChatEndpoint{BaseURL: server.URL, Model: "m", APIKey: "k"}
	if _, err := endpoint.Complete(context.Background(),
		[]ChatMessage{{Role: "user", Content: "hi"}}, nil, 0); err != nil {
		t.Fatal(err)
	}
	if seen != "Bearer k" {
		t.Errorf("Authorization = %q, want the bearer token", seen)
	}

	// With no key configured, no header is sent at all -- an empty bearer
	// token is a credential the endpoint might log as one.
	endpoint.APIKey = ""
	seen = "unset"
	if _, err := endpoint.Complete(context.Background(),
		[]ChatMessage{{Role: "user", Content: "hi"}}, nil, 0); err != nil {
		t.Fatal(err)
	}
	if seen != "" {
		t.Errorf("Authorization = %q, want no header when no key is configured", seen)
	}
}

func TestAnOversizedResponseIsRefusedNotTruncated(t *testing.T) {
	// Truncating would hand back invalid JSON and fail at the decode, which
	// reads as a malformed endpoint rather than an oversized one.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"padding":"` + strings.Repeat("x", MaxAPIResponseBytes) + `"}`))
	}))
	defer server.Close()

	endpoint := &ChatEndpoint{BaseURL: server.URL, Model: "m"}
	_, err := endpoint.Complete(context.Background(),
		[]ChatMessage{{Role: "user", Content: "hi"}}, nil, 0)
	if err == nil {
		t.Fatal("an oversized response must be refused")
	}
	if !strings.Contains(err.Error(), "cap") {
		t.Errorf("the refusal must name the cap: %v", err)
	}
}

func TestAnErrorStatusCarriesTheEndpointsOwnDetail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"unknown model 'm'"}`))
	}))
	defer server.Close()

	endpoint := &ChatEndpoint{BaseURL: server.URL, Model: "m"}
	_, err := endpoint.Complete(context.Background(),
		[]ChatMessage{{Role: "user", Content: "hi"}}, nil, 0)
	if err == nil {
		t.Fatal("an error status must fail the call")
	}
	// The endpoint's own message names the remediation; inventing a second
	// vocabulary for the same failure helps nobody.
	if !strings.Contains(err.Error(), "unknown model") {
		t.Errorf("the endpoint's detail must be relayed: %v", err)
	}
}

func TestAPerCallTimeoutMayTightenButNotLoosen(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer server.Close()

	endpoint := &ChatEndpoint{BaseURL: server.URL, Model: "m", Timeout: 5 * time.Second}

	// A tighter per-call bound applies.
	if _, err := endpoint.Complete(context.Background(),
		[]ChatMessage{{Role: "user", Content: "hi"}}, nil, 50*time.Millisecond); err == nil {
		t.Error("a tighter per-call timeout must apply")
	}

	// A looser one does not raise the operator's ceiling.
	endpoint.Timeout = 50 * time.Millisecond
	if _, err := endpoint.Complete(context.Background(),
		[]ChatMessage{{Role: "user", Content: "hi"}}, nil, 10*time.Second); err == nil {
		t.Error("a per-call timeout must not loosen the configured ceiling")
	}
}

func TestToolsAreSentOnlyWhenThereAreSome(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&payload)
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer server.Close()

	endpoint := &ChatEndpoint{BaseURL: server.URL, Model: "m"}
	if _, err := endpoint.Complete(context.Background(),
		[]ChatMessage{{Role: "user", Content: "hi"}}, nil, 0); err != nil {
		t.Fatal(err)
	}
	if _, present := payload["tools"]; present {
		// An empty tools array is not the same as no tools: some endpoints
		// reject it, and a read-only role legitimately has none to offer.
		t.Error("no tools key must be sent when the role has none")
	}

	tools := []map[string]any{{"type": "function", "function": map[string]any{"name": "read_file"}}}
	if _, err := endpoint.Complete(context.Background(),
		[]ChatMessage{{Role: "user", Content: "hi"}}, tools, 0); err != nil {
		t.Fatal(err)
	}
	if _, present := payload["tools"]; !present {
		t.Error("tools must be sent when the role has some")
	}
}

func TestResolveEndpointRequiresABaseURL(t *testing.T) {
	if _, err := ResolveEndpoint("", "", "m"); err == nil {
		t.Error("an unconfigured base URL must be refused")
	} else if !strings.Contains(err.Error(), "runners.api_base_url") {
		t.Errorf("the refusal must name the setting: %v", err)
	}
}

func TestTheAPIKeyComesFromTheEnvironmentNotASettingsFile(t *testing.T) {
	// A project-local settings file is untrusted, clonable content. A
	// credential read from one is a credential any clone could substitute, so
	// the setting names a *variable* and the value comes from the process
	// environment.
	t.Setenv("CADRE_TEST_API_KEY", "from-environment")

	endpoint, err := ResolveEndpoint("http://localhost:1", "CADRE_TEST_API_KEY", "m")
	if err != nil {
		t.Fatal(err)
	}
	if endpoint.APIKey != "from-environment" {
		t.Errorf("APIKey = %q, want the environment's value", endpoint.APIKey)
	}

	// Naming a variable that is not set is a configuration error, not a
	// silent fallback to an unauthenticated request.
	t.Setenv("CADRE_TEST_API_KEY", "")
	if _, err := ResolveEndpoint("http://localhost:1", "CADRE_TEST_API_KEY", "m"); err == nil {
		t.Error("a named-but-unset key variable must be refused")
	}
}

func TestAnUnconfiguredTierIsAConfigurationError(t *testing.T) {
	// Never the wrapper's own model field: that holds a vendor identifier a
	// self-hosted endpoint has never heard of, and sending it would 404
	// against exactly the deployments this runner exists to serve.
	if _, err := ResolveModel("opus", "", "cloud-architect"); err == nil {
		t.Fatal("a tier with no configured model must be a configuration error")
	} else {
		for _, want := range []string{"opus", "cloud-architect", "runners.local_model_opus"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the error must mention %q: %v", want, err)
			}
		}
	}

	got, err := ResolveModel("opus", "qwen2.5-coder", "cloud-architect")
	if err != nil || got != "qwen2.5-coder" {
		t.Errorf("model = %q (%v), want the configured one", got, err)
	}
}

func TestCompleteReachesTheChatCompletionsPath(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer server.Close()

	// A trailing slash on the configured base URL must not produce a double
	// slash in the path -- some gateways route that to a 404.
	for _, base := range []string{server.URL, server.URL + "/"} {
		endpoint := &ChatEndpoint{BaseURL: base, Model: "m"}
		if _, err := endpoint.Complete(context.Background(),
			[]ChatMessage{{Role: "user", Content: "hi"}}, nil, 0); err != nil {
			t.Fatal(err)
		}
		if path != "/chat/completions" {
			t.Errorf("base %q produced path %q", base, path)
		}
	}
}
