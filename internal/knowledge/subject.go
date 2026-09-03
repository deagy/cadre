package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/deagy/cadre/cli/internal/platform"
)

// ResolveActorObserver decides what this process records as having acted.
//
// Two answers, and the difference between them is the whole of AC-6.
//
// With no server configured, the answer is what the process can see about
// itself: the OS user and git identity. Worth recording — it says which
// machine acted — and worth nothing as proof, because a caller who owns the
// machine owns both. Nothing here pretends otherwise, and `show-staged`
// reports the distinction in the record.
//
// With a server configured, the answer is the subject that server
// authenticated the credential as. The caller chose which credential to
// present; it did not choose what the server decided that credential names,
// and that is the one property an actor field needs to be evidence rather
// than a string.
//
// A configured server that cannot be reached, or that vouches for nobody,
// falls back to the local observation rather than failing the command. The
// alternative — refusing to stage a record because a server is down — trades
// a real capability for an attribution guarantee the caller did not ask for
// at that moment. What it must never do is silently record the local
// observation as though a server had confirmed it, which is why the two are
// distinguishable by prefix rather than by hope.
func ResolveActorObserver(cfg *Config) func() string {
	local := func() string { return platform.ObserveActor().String() }
	if cfg == nil || strings.TrimSpace(cfg.Server.URL) == "" {
		return local
	}

	subject, credential, err := authenticatedSubject(cfg.Server)

	// A subject that is the credential is not an identity worth writing down.
	//
	// recall's API-key authenticators return the presented key as the subject
	// -- both APIKeyAuth and ScopedAPIKeyAuth do, and for their own purposes
	// that is reasonable, because the key is what they know. It is not
	// reasonable to persist: a staged record carries observed_actor into a
	// file, and this repository's config layer hard-errors on secret-shaped
	// keys specifically so that credentials do not reach disk. Recording one
	// as the actor would route a secret to the same place through a door
	// nobody was watching.
	//
	// So this refuses the value rather than storing or hashing it. A digest
	// would still be derived from a live credential, and would still be a
	// secret-shaped string in a record a person reads. Configure a JWT
	// authenticator, whose subject is a claim about a person rather than the
	// token itself, and this records it.
	if subject != "" && credential != "" && subject == credential {
		fmt.Fprintf(os.Stderr,
			"cadre knowledge: %s authenticated this credential and named it as the subject, "+
				"which means the subject is the credential itself. Refusing to write that into "+
				"a record; use an authenticator whose subject names a person, such as JWT. "+
				"Recording the local observation instead, which names this machine.\n",
			cfg.Server.URL)
		return local
	}

	if err != nil || subject == "" {
		if err != nil {
			fmt.Fprintf(os.Stderr,
				"cadre knowledge: could not ask %s who this credential authenticates as (%v). "+
					"Recording the local observation instead, which names this machine "+
					"rather than a person.\n", cfg.Server.URL, err)
		}
		return local
	}
	vouched := authenticatedSubjectPrefix + subject
	return func() string { return vouched }
}

// authenticatedSubject asks a recall-server who the configured credential
// authenticates as.
//
// Deliberately its own small request rather than a field on some other
// response: the question "who does this server say I am" has one answer and
// wanting it should not require uploading or searching anything.
func authenticatedSubject(server ServerConfig) (subject string, credential string, err error) {
	key := ""
	if name := strings.TrimSpace(server.APIKeyEnv); name != "" {
		key = os.Getenv(name)
		if key == "" {
			return "", "", fmt.Errorf("%s names the credential and is unset or empty", name)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	endpoint := strings.TrimRight(server.URL, "/") + "/whoami"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", key, err
	}
	if key != "" {
		// Both headers, and the reason is that neither alone reaches every
		// authenticator recall ships. Its API-key authenticators read
		// X-API-Key first and fall back to Bearer; its JWT authenticator
		// reads only Bearer.
		//
		// Sending only X-API-Key -- which this did -- left the JWT path
		// unauthenticated, and the API-key path returns the credential as
		// the subject, which the caller above refuses to record. Between
		// them there was no configuration that produced a usable subject at
		// all: the mechanism was present in the code and dead in practice,
		// and the refusal message directed operators at JWT, the one path
		// that could not work.
		request.Header.Set("X-API-Key", key)
		request.Header.Set("Authorization", "Bearer "+key)
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", key, err
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return "", key, fmt.Errorf("whoami returned %s", response.Status)
	}

	var body struct {
		Authenticated bool   `json:"authenticated"`
		Subject       string `json:"subject"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		return "", key, err
	}
	// A server that vouches for nobody is not an error and is not a subject.
	// Recording its silence as an identity is the defect this exists to
	// avoid, one layer further out.
	if !body.Authenticated {
		return "", key, nil
	}
	return body.Subject, key, nil
}
