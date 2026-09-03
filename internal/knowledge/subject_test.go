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

// A server that names the credential as the subject must not have it recorded.
//
// recall's API-key authenticators return the presented key as the subject.
// Persisting that would write a live credential into a staged-record file, in
// a repository whose config layer hard-errors on secret-shaped keys precisely
// to keep credentials off disk. Raised by CP-3v as an aside; it is the more
// serious of the two findings, because the other made a feature unreachable
// and this one would have leaked.
func TestACredentialIsNeverRecordedAsTheSubject(t *testing.T) {
	const secret = "sk-live-do-not-persist-me"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Exactly what APIKeyAuth and ScopedAPIKeyAuth do: the subject is
		// the key that was presented.
		presented := r.Header.Get("X-API-Key")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"authenticated": presented != "",
			"subject":       presented,
		})
	}))
	defer server.Close()

	t.Setenv("TEST_RECALL_KEY", secret)
	got := ResolveActorObserver(&Config{
		Server: ServerConfig{URL: server.URL, APIKeyEnv: "TEST_RECALL_KEY"},
	})()

	if strings.Contains(got, secret) {
		t.Fatalf("the recorded actor contains the credential: %q", got)
	}
	if IsAuthenticatedSubject(got) {
		t.Fatalf("a credential-as-subject was recorded as verified: %q", got)
	}
}

// A subject that is not the credential must be recorded.
//
// The two tests above cover a server echoing the key back (refused) and a
// server vouching for nobody (not a subject). Neither covers the case the
// whole feature exists for: an authenticator whose subject names a person.
//
// That gap was not academic. cadre sent only X-API-Key; recall's JWT
// authenticator reads only Authorization: Bearer, so the one path whose
// subject is a person never authenticated, while the API-key path returned
// the credential and was refused. No configuration produced a usable subject,
// and the mock in the test above hid it by reading whichever header cadre
// happened to send.
//
// This mock reads Bearer specifically, and returns a subject that is not the
// token -- which is what a JWT authenticator does.
func TestABearerSubjectThatIsNotTheCredentialIsRecorded(t *testing.T) {
	const token = "header.payload.signature"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization := r.Header.Get("Authorization")
		if !strings.HasPrefix(authorization, "Bearer ") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if strings.TrimPrefix(authorization, "Bearer ") != token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// A person, not the token -- the distinction the feature turns on.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"authenticated": true,
			"subject":       "carol@corp.example",
		})
	}))
	defer server.Close()

	t.Setenv("TEST_RECALL_KEY", token)
	got := ResolveActorObserver(&Config{
		Server: ServerConfig{URL: server.URL, APIKeyEnv: "TEST_RECALL_KEY"},
	})()

	if !IsAuthenticatedSubject(got) {
		t.Fatalf("a bearer-authenticated subject was not recorded as verified: %q.\n"+
			"If this fails with the local observation, the credential is not reaching the "+
			"server on the header its authenticator reads.", got)
	}
	if got != authenticatedSubjectPrefix+"carol@corp.example" {
		t.Fatalf("recorded %q, want the subject the server named", got)
	}
}
