package cli

import (
	"fmt"
	"os"

	"github.com/deagy/cadre/cli/internal/orchestration"
)

// requireCheckout refuses a regeneration command when the tree it would write
// into is not a git checkout.
//
// These commands rewrite roster/ and provider/ in place. In a checkout that is
// the point; from an installed distribution it silently edits the install --
// `cadre generate-authority-aides` from a pip/pipx install rewrote eight
// AGENT.md files under <prefix>/share/cadre/roster/authority, reported
// success, and left the installation differing from the release it claims to
// be. Nothing downstream would notice: the files are still valid, just no
// longer the ones that shipped.
//
// The check is deliberately "is the write target a checkout" rather than "what
// kind of install is this". Those differ: a binary can be classified as a
// go-install while CADRE_REPO_ROOT points at a real checkout it may legitimately
// regenerate. What matters is where the bytes land.
//
// The pure-Python wheel used to enforce this in cadre_cli's
// _CHECKOUT_ONLY_SUBCOMMANDS. That module is gone with Phase 2 of
// PYTHON_ELIMINATION_PLAN.md, so the guarantee moves here -- and now covers the
// packaged-plugin channel too, which never had it.
func requireCheckout(command, installationRoot string) bool {
	if orchestration.FindCheckoutRoot(installationRoot) != "" {
		return true
	}
	fmt.Fprintf(os.Stderr,
		"cadre %s: refusing to write into %s, which is not a Cadre git checkout.\n"+
			"This command regenerates files in place and is a maintainer operation. Run it "+
			"from a checkout (./bin/cadre %s), or set CADRE_REPO_ROOT to one.\n",
		command, installationRoot, command)
	return false
}
