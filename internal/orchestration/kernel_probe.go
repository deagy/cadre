package orchestration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// KernelResolution is which `agentic-sdlc` this machine would actually run,
// and how it was found.
//
// Reported by `cadre doctor` because the kernel is the most consequential
// thing cadre resolves and it reported nothing about it. A stale binary
// answering the name is not a hypothetical: a pipx-installed Python
// `agentic-sdlc 0.13.2`, a build from before the Go port, was found holding
// the name on a developer machine while the released kernel was not installed
// at all -- and because 0.13.2 sat exactly on the provider bundle's inclusive
// floor, `cadre select --require-sdlc` accepted it. The mechanism whose job is
// to refuse an unsuitable kernel passed it through, by one patch version.
type KernelResolution struct {
	// Path is the binary that answers, or empty when none does.
	Path string `json:"path,omitempty"`
	// Source is "AGENTIC_SDLC_BIN", "PATH" or "packaged plugin".
	Source string `json:"source,omitempty"`
	// Version is what it reports, trimmed.
	Version string `json:"version,omitempty"`
	// Shadowed lists every other `agentic-sdlc` on PATH behind the first.
	// More than one is worth saying out loud: which answers depends on
	// ordering, and ordering is not something anyone reads.
	Shadowed []string `json:"shadowed,omitempty"`
	// Detail explains the verdict either way.
	Detail string `json:"detail,omitempty"`
	// OK is false when nothing is reachable, or when what is reachable
	// cannot be asked its version.
	OK bool `json:"ok"`

	// Expected is the kernel version this repository pins, and TooOld says
	// the resolved one is below it. Reporting the version without judging it
	// is what the compatibility window already did: 0.13.2 was displayed,
	// accepted, and wrong.
	Expected string `json:"expected,omitempty"`
	TooOld   bool   `json:"too_old,omitempty"`
}

// olderThan compares two dotted versions, ignoring anything after a dash.
// Unparseable input is not "older" -- an unreadable version is a different
// complaint from a stale one, and guessing would turn it into a false alarm.
func olderThan(version, floor string) bool {
	split := func(text string) []int {
		parts := strings.SplitN(strings.TrimSpace(text), ".", 3)
		out := make([]int, 0, 3)
		for _, part := range parts {
			value, err := strconv.Atoi(strings.SplitN(part, "-", 2)[0])
			if err != nil {
				return nil
			}
			out = append(out, value)
		}
		return out
	}
	left, right := split(version), split(floor)
	if len(left) != 3 || len(right) != 3 {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return left[i] < right[i]
		}
	}
	return false
}

// PackagedKernelShim reports the lifecycle plugin's own `agentic-sdlc`, which
// downloads and verifies a released kernel on first use.
//
// It is the kernel entry point an `install.sh` install actually produces:
// nothing is put on PATH and no `AGENTIC_SDLC_BIN` is set, so a machine that
// followed the documented instructions had the kernel cached and every
// `cadre sdlc` still answering "install Agentic SDLC". The stale answer here
// was `<repoRoot>/bin/agentic-sdlc` -- true while the kernel shipped in this
// repository, and a path that has not existed since it was extracted.
//
// Last resort by design: an explicit AGENTIC_SDLC_BIN, a configured
// agentic_sdlc.bin_path, or an `agentic-sdlc` the operator installed on PATH
// all still win, because each is a choice a human made about which kernel runs.
func PackagedKernelShim(repoRoot string) (string, bool) {
	if repoRoot == "" {
		return "", false
	}
	// Two layouts, because CADRE_REPO_ROOT means different directories
	// depending on which launcher started the process: the checkout root from
	// bin/cadre, and <package>/suite from the packaged plugin's own launcher.
	// Checking only the first found nothing in exactly the install this
	// fallback exists for.
	for _, candidate := range []string{
		filepath.Join(repoRoot, "plugin", "plugins", "lifecycle", "bin", "agentic-sdlc"),
		filepath.Join(filepath.Dir(repoRoot), "plugins", "lifecycle", "bin", "agentic-sdlc"),
	} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, true
		}
	}
	return "", false
}

// ResolveKernel answers the question `cadre sdlc` and `cadre select` answer
// silently, in the same order they do: the explicit override first, then PATH,
// then the packaged shim inside repoRoot. Pass an empty repoRoot to skip the
// last one.
func ResolveKernel(explicitBin string, expected string, lookPath func(string) (string, error),
	allOnPath func(string) []string, run func(string) (string, error), repoRoot string) KernelResolution {

	resolution := KernelResolution{Expected: expected}

	switch {
	case strings.TrimSpace(explicitBin) != "":
		resolution.Path, resolution.Source = explicitBin, "AGENTIC_SDLC_BIN"
	default:
		found, err := lookPath("agentic-sdlc")
		if err != nil {
			if shim, ok := PackagedKernelShim(repoRoot); ok {
				resolution.Path, resolution.Source = shim, "packaged plugin"
				break
			}
			resolution.Detail = "no agentic-sdlc on PATH, AGENTIC_SDLC_BIN is unset, and " +
				"no packaged lifecycle shim was found; `cadre select` runs standalone " +
				"and says so in the plan"
			return resolution
		}
		resolution.Path, resolution.Source = found, "PATH"
	}

	// Every match, not just the winner. `command -v` stops at the first.
	if all := allOnPath("agentic-sdlc"); len(all) > 1 {
		for _, candidate := range all {
			if candidate != resolution.Path {
				resolution.Shadowed = append(resolution.Shadowed, candidate)
			}
		}
	}

	version, err := run(resolution.Path)
	if err != nil {
		resolution.Detail = fmt.Sprintf("%s did not answer --version: %v", resolution.Path, err)
		return resolution
	}

	resolution.Version = strings.TrimSpace(version)
	resolution.OK = true
	resolution.TooOld = expected != "" && olderThan(resolution.Version, expected)
	resolution.Detail = fmt.Sprintf("%s via %s", resolution.Version, resolution.Source)
	if resolution.TooOld {
		resolution.Detail += fmt.Sprintf(" (this checkout pins %s)", expected)
	}
	return resolution
}

// LookAllOnPath returns every match for name on PATH, in order.
func LookAllOnPath(name string) []string {
	var found []string
	seen := map[string]bool{}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() &&
			info.Mode().Perm()&0o111 != 0 && !seen[candidate] {
			seen[candidate] = true
			found = append(found, candidate)
		}
	}
	return found
}

// KernelVersionOf asks a binary for its version.
func KernelVersionOf(binary string) (string, error) {
	output, err := exec.Command(binary, "--version").Output()
	if err != nil {
		return "", err
	}
	return string(output), nil
}
