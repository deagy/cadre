package platform

// What the process can see about who ran it, without being told.
//
// Every actor field this CLI records -- `--decided-by`, `--deleted-by`,
// `--authorized-by`, a staged record's `staged_by` -- is a string the caller
// supplies. roster/knowledge-store/SECURITY.md says so plainly: they are
// "caller-asserted strings, authenticated by nobody", and two of the four
// separation-of-duties checks compare two of them, so "an actor that stages
// as one name and decides as another satisfies both".
//
// Nothing here fixes that, and nothing here could. On a single-operator
// machine there is no identity provider and nothing unforgeable: a caller who
// owns the machine owns every source of identity on it.
//
// What this does fix is narrower and worth having: **a record that presents
// an assertion as an observation.** An evidence row that names an actor
// nobody verified is a record of a string. The same row, carrying beside it
// what the process could see for itself, is a record of a string *and* an
// observation -- and a reader can tell which half the system stands behind.
//
// The distinction is only as good as its sources, so:
//
//   - the OS user comes from os/user.Current(), a syscall against the process
//     credentials, NOT from $USER or $LOGNAME. Those are environment
//     variables the caller sets, and recording one as "observed" would be the
//     exact dishonesty this type exists to remove.
//   - the git identity comes from `git config user.email` resolved in the
//     working directory. That IS a file the caller owns, and it is recorded
//     as context rather than proof -- it says which configured identity the
//     command ran under, not who was at the keyboard.
//
// Neither is authentication. Both are more than the record had before.

import (
	"context"
	"os/exec"
	"os/user"
	"strings"
	"time"
)

// ObservedActor is what the process saw about its own execution.
//
// No field here can be set by a flag. That is the whole property: a caller
// can assert any name they like in `--deleted-by`, and this sits beside it
// unchanged.
type ObservedActor struct {
	// OSUser is the account the process runs as, from the process
	// credentials rather than the environment. Empty only if the lookup
	// fails, which on a normal system means something is badly wrong.
	OSUser string `json:"os_user"`

	// GitIdentity is `user.email` as git resolves it here, or empty when
	// git is absent or unconfigured. Context, not proof: it is a file the
	// caller can edit.
	GitIdentity string `json:"git_identity,omitempty"`
}

// String renders the observation for a record, or "unobserved" when nothing
// could be seen. It never renders as a bare name, so it cannot be mistaken
// for an asserted actor in a log line.
func (o ObservedActor) String() string {
	switch {
	case o.OSUser == "" && o.GitIdentity == "":
		return "unobserved"
	case o.GitIdentity == "":
		return "os:" + o.OSUser
	case o.OSUser == "":
		return "git:" + o.GitIdentity
	}
	return "os:" + o.OSUser + " git:" + o.GitIdentity
}

// ObserveActor resolves what this process can see about who is running it.
//
// Deliberately tolerant: a missing git identity or an unreadable user
// database yields an empty field rather than an error. Refusing to act
// because identity could not be observed would trade a weak record for no
// record, and the weak record is the thing being improved.
func ObserveActor() ObservedActor {
	var observed ObservedActor

	if current, err := user.Current(); err == nil {
		observed.OSUser = current.Username
	}

	// Bounded: git can block on a slow filesystem or a lock, and observing
	// identity must never be the reason a command hangs.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "git", "config", "--get", "user.email")
	if output, err := command.Output(); err == nil {
		observed.GitIdentity = strings.TrimSpace(string(output))
	}

	return observed
}
