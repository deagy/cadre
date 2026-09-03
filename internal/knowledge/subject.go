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

	subject, err := authenticatedSubject(cfg.Server)
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
func authenticatedSubject(server ServerConfig) (string, error) {
	key := ""
	if name := strings.TrimSpace(server.APIKeyEnv); name != "" {
		key = os.Getenv(name)
		if key == "" {
			return "", fmt.Errorf("%s names the credential and is unset or empty", name)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	endpoint := strings.TrimRight(server.URL, "/") + "/whoami"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	if key != "" {
		request.Header.Set("X-API-Key", key)
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", err
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("whoami returned %s", response.Status)
	}

	var body struct {
		Authenticated bool   `json:"authenticated"`
		Subject       string `json:"subject"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		return "", err
	}
	// A server that vouches for nobody is not an error and is not a subject.
	// Recording its silence as an identity is the defect this exists to
	// avoid, one layer further out.
	if !body.Authenticated {
		return "", nil
	}
	return body.Subject, nil
}
