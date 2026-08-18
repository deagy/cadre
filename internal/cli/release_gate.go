package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/deagy/cadre/cli/internal/version"
)

// Decide which releasable components had their version bumped in this push.
//
// release.yml gates each publish job on this, so a component reported
// unchanged is simply not released -- silently, with a log line saying so.
// That failure mode has already happened: the check read the CLI version from
// cadre_cli/_version.py, the marker moved to VERSION when the last Python left
// the distribution, and nothing tied the two together. The file was absent at
// both ends of every comparison, both sides read as nil, nil equals nil, and
// the CLI stopped being releasable at all.
//
// So the paths are not written down here. The plugin manifests come from
// pluginManifests, the same list `cadre plugin-version` maintains, and the CLI
// markers come from version.VersionMarkerNames, the same list `cadre
// --version` reads. A marker cannot move without moving this with it, and
// TestEveryWatchedReleasePathExists fails if one goes missing anyway.
//
// Ported from the 85-line Python heredoc in release.yml's `changed` job.

// unreadableMarker is what a present-but-unparseable marker reads as.
//
// Deliberately not an error and deliberately not nil: it has to compare
// unequal to a real version so the release proceeds and the dedicated check
// reports the problem properly, and unequal to *absent* so a file appearing or
// disappearing counts as a change.
const unreadableMarker = "<unreadable>"

// releaseComponent is one independently-versioned, independently-published
// thing, and the paths whose version decides whether it ships.
type releaseComponent struct {
	name string
	// paths are repo-relative. Every one is compared, not just the first: if a
	// bump lands in only some of the plugin manifests, this has to report
	// changed so `cadre plugin-version --check` can fail on the disagreement,
	// rather than the release being skipped and nobody being told.
	paths []string
	// anyOf is true when the component's version lives in whichever of its
	// paths exists, rather than in all of them. The CLI marker is like this:
	// VERSION today, cadre_cli/_version.py in an older tree.
	anyOf bool
}

func releaseComponents(repoRoot string) []releaseComponent {
	var manifests []string
	for _, absolute := range pluginManifests(filepath.Join(repoRoot, "plugin")) {
		relative, err := filepath.Rel(repoRoot, absolute)
		if err != nil {
			continue
		}
		manifests = append(manifests, filepath.ToSlash(relative))
	}
	sort.Strings(manifests)
	return []releaseComponent{
		{name: "plugin", paths: manifests},
		{name: "cli", paths: version.VersionMarkerNames, anyOf: true},
	}
}

// markerVersionAt reads a path's version at a git ref.
//
// Three outcomes, all distinct: absent (nil), present but unparseable, and a
// version. Collapsing the first two is what let a moved marker read as "did
// not change".
func markerVersionAt(repoRoot, ref, path string) *string {
	command := exec.Command("git", "show", ref+":"+path)
	command.Dir = repoRoot
	output, err := command.Output()
	if err != nil {
		return nil // not present at that ref
	}
	if strings.HasSuffix(path, ".json") {
		var manifest struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal(output, &manifest); err != nil {
			// Unparseable is not "unchanged": let the job run and let
			// `cadre plugin-version --check` report it properly.
			unreadable := unreadableMarker
			return &unreadable
		}
		return &manifest.Version
	}
	if parsed, ok := version.ParseMarker(output); ok {
		return &parsed
	}
	unreadable := unreadableMarker
	return &unreadable
}

func describeMarker(value *string) string {
	if value == nil {
		return "absent"
	}
	return *value
}

func commitExists(repoRoot, ref string) bool {
	command := exec.Command("git", "cat-file", "-e", ref+"^{commit}")
	command.Dir = repoRoot
	return command.Run() == nil
}

// comparableBefore reports whether there is a previous commit worth diffing.
//
// workflow_dispatch carries no before-sha, and a force-push or a branch's
// first push reports all zeros. Those fail open -- every component counts as
// changed -- and the already-tagged check decides from there. Failing closed
// would mean a manual release run never publishes anything.
func comparableBefore(repoRoot, event, before string) bool {
	if event != "push" || before == "" {
		return false
	}
	if strings.Trim(before, "0") == "" {
		return false
	}
	return commitExists(repoRoot, before)
}

// changedComponents returns each component's name mapped to whether it moved,
// writing its reasoning to diagnostics.
func changedComponents(repoRoot, event, before string, diagnostics *strings.Builder) map[string]bool {
	changed := map[string]bool{}
	comparable := comparableBefore(repoRoot, event, before)
	if !comparable {
		fmt.Fprintf(diagnostics, "no comparable previous commit (event=%q, before=%q); "+
			"treating every component as changed\n", event, before)
	}
	for _, component := range releaseComponents(repoRoot) {
		if !comparable {
			changed[component.name] = true
			continue
		}
		var moved []string
		for _, path := range component.paths {
			was := markerVersionAt(repoRoot, before, path)
			now := markerVersionAt(repoRoot, "HEAD", path)
			if component.anyOf && was == nil && now == nil {
				continue // this marker is simply not the one in use
			}
			if describeMarker(was) == describeMarker(now) {
				continue
			}
			moved = append(moved, fmt.Sprintf("%s %s -> %s", path,
				describeMarker(was), describeMarker(now)))
		}
		for _, entry := range moved {
			fmt.Fprintf(diagnostics, "%s: %s\n", component.name, entry)
		}
		if len(moved) == 0 {
			fmt.Fprintf(diagnostics, "%s: version unchanged in this push; not releasing\n",
				component.name)
		}
		changed[component.name] = len(moved) > 0
	}
	return changed
}

const usageChangedComponents = `Report which releasable components had their version bumped.

  cadre changed-components --event push --before <sha>

Writes "<component>=true|false" lines on stdout, for a workflow to read, and
its reasoning on stderr. With no comparable previous commit it fails open and
reports every component as changed.`

// ChangedComponentsCmd implements `cadre changed-components`.
func ChangedComponentsCmd(args []string) int {
	fs := flag.NewFlagSet("cadre changed-components", flag.ContinueOnError)
	setUsage(fs, "changed-components", usageChangedComponents)
	event := fs.String("event", "push", "the event that triggered this run")
	before := fs.String("before", "", "the sha this push is measured against")
	root := fs.String("repo-root", ".", "the checkout to inspect")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	var diagnostics strings.Builder
	changed := changedComponents(*root, *event, *before, &diagnostics)
	fmt.Fprint(os.Stderr, diagnostics.String())
	names := make([]string, 0, len(changed))
	for name := range changed {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Printf("%s=%t\n", name, changed[name])
	}
	return 0
}
