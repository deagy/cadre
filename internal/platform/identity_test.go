package platform

import (
	"os"
	"strings"
	"testing"
)

// The observation must not come from the environment.
//
// This is the property the whole type exists for. `$USER` is a variable the
// caller sets; recording one as "observed" would present an assertion as an
// observation, which is exactly the defect ObservedActor was written to
// remove. os/user.Current() reads the process credentials instead.
func TestTheObservedUserIgnoresTheEnvironment(t *testing.T) {
	t.Setenv("USER", "not-the-real-user")
	t.Setenv("LOGNAME", "not-the-real-user")

	observed := ObserveActor()
	if observed.OSUser == "not-the-real-user" {
		t.Fatal("the observed OS user came from $USER.\n" +
			"  An environment variable is chosen by the caller, so recording it as an\n" +
			"  observation makes the record look verified while being exactly as\n" +
			"  asserted as the flag it sits beside.")
	}
	if observed.OSUser == "" {
		t.Fatal("no OS user observed at all; user.Current() should succeed here")
	}
}

// An observation renders so it cannot be read as a name.
func TestTheObservationNeverRendersAsABareName(t *testing.T) {
	cases := []struct {
		name     string
		observed ObservedActor
		want     string
	}{
		{"nothing seen", ObservedActor{}, "unobserved"},
		{"os only", ObservedActor{OSUser: "deagy"}, "os:deagy"},
		{"git only", ObservedActor{GitIdentity: "a@b.c"}, "git:a@b.c"},
		{"both", ObservedActor{OSUser: "deagy", GitIdentity: "a@b.c"}, "os:deagy git:a@b.c"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.observed.String()
			if got != tc.want {
				t.Fatalf("String() = %q, want %q", got, tc.want)
			}
			// A bare name would be indistinguishable from --deleted-by in a
			// log line or an evidence row. Every rendering carries a prefix
			// or says plainly that nothing was seen.
			if got != "unobserved" && !strings.Contains(got, ":") {
				t.Fatalf("%q reads as a bare name; an observation must be labelled", got)
			}
		})
	}
}

// Observing identity must never be why a command hangs or fails.
func TestObservingIsTolerantOfAMissingGit(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no git on PATH
	observed := ObserveActor()
	if observed.GitIdentity != "" {
		t.Fatalf("git identity %q resolved with no git on PATH", observed.GitIdentity)
	}
	if observed.OSUser == "" {
		t.Fatal("the OS user should still be observed when git is absent")
	}
	if _, err := os.Stat("/"); err != nil {
		t.Fatal(err)
	}
}
