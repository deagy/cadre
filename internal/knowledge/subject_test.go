package knowledge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// End to end: a configured server decides what this process records.
//
// The pieces exist separately -- recall-server authenticates and answers
// /whoami, and this package refuses when the same authenticated subject
// stages and dispositions. This is the wire between them, and without it the
// subject branch is unreachable and the check can never fire.
func TestAConfiguredServerDecidesTheRecordedActor(t *testing.T) {
	keys := map[string]string{"alice-key": "alice@corp.example", "bob-key": "bob@corp.example"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/whoami" {
			http.NotFound(w, r)
			return
		}
		subject, ok := keys[r.Header.Get("X-API-Key")]
		w.Header().Set("Content-Type", "application/json")
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "unauthorized"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"authenticated": true, "subject": subject})
	}))
	defer server.Close()

	cfg := &Config{Server: ServerConfig{URL: server.URL, APIKeyEnv: "TEST_RECALL_KEY"}}

	t.Setenv("TEST_RECALL_KEY", "alice-key")
	got := ResolveActorObserver(cfg)()
	if got != "subject:alice@corp.example" {
		t.Fatalf("recorded actor was %q, want the subject the server chose", got)
	}
	if !IsAuthenticatedSubject(got) {
		t.Fatalf("%q is not recognised as authenticated, so the check will never fire on it", got)
	}

	t.Setenv("TEST_RECALL_KEY", "bob-key")
	if other := ResolveActorObserver(cfg)(); other == got {
		t.Fatalf("two credentials produced the same actor %q; the server's answer is being ignored", other)
	}
}

// An unreachable server must not silently pass off a local observation as
// verified.
func TestAnUnreachableServerFallsBackToAnUnverifiedObservation(t *testing.T) {
	cfg := &Config{Server: ServerConfig{URL: "http://127.0.0.1:1", APIKeyEnv: "TEST_RECALL_KEY"}}
	t.Setenv("TEST_RECALL_KEY", "anything")

	got := ResolveActorObserver(cfg)()
	if IsAuthenticatedSubject(got) {
		t.Fatalf("an unreachable server produced %q, which reads as verified", got)
	}
	if strings.TrimSpace(got) == "" {
		t.Fatal("an unreachable server produced no observation at all")
	}
}

// A server that authenticates nobody is not an identity.
func TestAServerThatVouchesForNobodyIsNotASubject(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"authenticated": false})
	}))
	defer server.Close()

	got := ResolveActorObserver(&Config{Server: ServerConfig{URL: server.URL}})()
	if IsAuthenticatedSubject(got) {
		t.Fatalf("a server vouching for nobody produced %q, which reads as verified", got)
	}
}

// With no server configured, nothing changes.
func TestNoServerKeepsTheLocalObservation(t *testing.T) {
	got := ResolveActorObserver(&Config{})()
	if IsAuthenticatedSubject(got) {
		t.Fatalf("no server configured produced %q, which reads as verified", got)
	}
}
