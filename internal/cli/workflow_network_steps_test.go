package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every apt-get in a release workflow must be bounded.
//
// apt-get has no timeout of its own. When a mirror accepts the connection and
// then stops responding it waits forever, and the only thing that ended it here
// was the 30-minute job timeout -- which cancelled the leg, skipped the publish,
// and failed a release on a stalled mirror with every other platform already
// built. That happened on two consecutive releases, on the same leg.
//
// A job timeout is a backstop against a hang, not a retry policy: it turns a
// stall into a failed release rather than a slow one. So each network install
// is wrapped in `timeout` and retried, and this asserts nobody quietly drops
// that back to a bare `apt-get install`.
func TestReleaseWorkflowBoundsItsNetworkInstalls(t *testing.T) {
	for _, name := range []string{"release.yml", "validate.yml"} {
		path := filepath.Join("..", "..", ".github", "workflows", name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("cannot read %s: %v", name, err)
		}
		workflow := string(data)

		// Every apt-get invocation should be preceded by a `timeout <seconds>`
		// on the same line, whatever the surrounding retry shape.
		unbounded := regexp.MustCompile(`(?m)^\s*(?:&&\s*)?sudo apt-get`)
		for _, line := range strings.Split(workflow, "\n") {
			if !strings.Contains(line, "apt-get") || strings.Contains(line, "#") {
				continue
			}
			if !strings.Contains(line, "timeout ") && unbounded.MatchString(line) {
				t.Errorf("%s: unbounded apt-get -- wrap it in `timeout <seconds>` so a stalled "+
					"mirror fails in minutes instead of consuming the whole job budget:\n    %s",
					name, strings.TrimSpace(line))
			}
		}
	}
}
