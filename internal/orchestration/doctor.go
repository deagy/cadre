// doctor.go ports roster/orchestration/src/doctor.py: reports which `cadre`
// binary is actually executing, to catch one specific DX trap. A bare
// `cadre` on PATH can resolve to (a) this checkout's own self-built binary
// (via bin/cadre's build cache), (b) a `go install`-placed copy under
// $GOBIN/$GOPATH/bin, or (c) a stale Claude Code plugin-cache copy
// (~/.claude/plugins/cache/<marketplace>/<plugin>/<version>/bin/cadre).
// These are separately maintained, potentially differently-versioned builds
// of the same CLI. Doctor makes a mismatch between "the checkout your cwd is
// in" and "the binary that actually ran" diagnosable in one command instead
// of a "why isn't my change showing up" debugging session.
package orchestration

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Install-kind classifications, mirroring doctor.py's return values.
const (
	InstallKindCheckout    = "checkout"
	InstallKindGoInstall   = "go-install"
	InstallKindPluginCache = "plugin-cache"
	InstallKindUnknown     = "unknown"
)

// MinGoVersion tracks go.mod's `go` directive. Kept as a plain constant
// (like doctor.py's MIN_PYTHON) rather than parsed from go.mod at runtime:
// a built binary does not carry go.mod with it, so a hardcoded floor is the
// only value doctor can report regardless of install kind. Update this
// alongside go.mod's `go` line if that floor ever moves.
const MinGoVersion = "1.25"

// pluginCacheMarker is the one Claude Code plugin-cache path shape this
// repository documents elsewhere. There is no known API or env var that
// reports "this process is running from a plugin cache" more reliably than
// this path shape, so this check is a heuristic and says so in its output.
var pluginCacheMarker = []string{"plugins", "cache"}

// DoctorReport is the complete `cadre doctor` result, serialized directly
// for --json output.
type DoctorReport struct {
	RunningBinary   string `json:"running_binary"`
	GoVersion       string `json:"go_version"`
	GoVersionOK     bool   `json:"go_version_ok"`
	GoMinVersion    string `json:"go_min_version"`
	InstallKind     string `json:"install_kind"`
	InstallRoot     string `json:"install_root"`
	InstallDetail   string `json:"install_detail"`
	CWD             string `json:"cwd"`
	CWDCheckoutRoot string `json:"cwd_checkout_root,omitempty"`
	Mismatch        bool   `json:"mismatch"`
	MismatchDetail  string `json:"mismatch_detail,omitempty"`

	// KnowledgeStoreOK reports whether the running binary can actually reach
	// its sqlite-backed knowledge store, and KnowledgeStoreDetail explains
	// the verdict either way.
	//
	// GatherDoctorReport does not fill these in: probing the driver means
	// importing it, and this package has no sqlite dependency today -- adding
	// one for a diagnostic line would pull cgo into every consumer of
	// orchestration. internal/cli owns the wiring instead (it already imports
	// both packages) and calls knowledge.DriverAvailable.
	//
	// An empty KnowledgeStoreDetail therefore means "not probed", not
	// "unavailable", and the renderer stays silent rather than reporting a
	// failure nobody checked for.
	KnowledgeStoreOK     bool   `json:"knowledge_store_ok"`
	KnowledgeStoreDetail string `json:"knowledge_store_detail,omitempty"`
}

// repoMarkersPresent is true when root looks like a Cadre checkout root by
// real filesystem signals, not by name: a .git entry (directory for a
// normal checkout, file for a worktree), plus roster/catalog.yaml and
// bin/cadre, the two paths every checkout has and no packaged/vendored copy
// renames.
func repoMarkersPresent(root string) bool {
	if _, err := os.Lstat(filepath.Join(root, ".git")); err != nil {
		return false
	}
	if info, err := os.Stat(filepath.Join(root, "roster", "catalog.yaml")); err != nil || info.IsDir() {
		return false
	}
	if info, err := os.Stat(filepath.Join(root, "bin", "cadre")); err != nil || info.IsDir() {
		return false
	}
	return true
}

// FindCheckoutRoot walks upward from start looking for Cadre checkout
// markers, the same "walk to the nearest boundary" shape
// platform.FindProjectRoot uses for the plain .git walk. Returns "" when no
// boundary is found before the filesystem root.
func FindCheckoutRoot(start string) string {
	current, err := filepath.Abs(start)
	if err != nil {
		return ""
	}
	for {
		if repoMarkersPresent(current) {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}

// goInstallRoot returns the $GOBIN/$GOPATH/bin-shaped directory containing
// path, if any -- the Go analogue of a Python pip/pipx site-packages
// install: a copy placed by `go install` rather than built in-checkout.
func goInstallRoot(path string) string {
	candidates := []string{}
	if gobin := os.Getenv("GOBIN"); gobin != "" {
		candidates = append(candidates, gobin)
	}
	if gopath := os.Getenv("GOPATH"); gopath != "" {
		candidates = append(candidates, filepath.Join(gopath, "bin"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, "go", "bin"))
	}
	dir := filepath.Dir(path)
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		absCandidate, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if dir == absCandidate {
			return absCandidate
		}
	}
	return ""
}

// pluginCacheRoot returns the <version> directory of a
// .../plugins/cache/<marketplace>/<plugin>/<version>/... shaped path, or ""
// if path does not sit under one.
func pluginCacheRoot(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for i := 0; i+len(pluginCacheMarker) <= len(parts); i++ {
		match := true
		for j, marker := range pluginCacheMarker {
			if parts[i+j] != marker {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		// parts[i] == "plugins", [i+1] == "cache", [i+2] == marketplace,
		// [i+3] == plugin, [i+4] == version.
		versionIndex := i + len(pluginCacheMarker) + 2
		if versionIndex < len(parts) {
			return strings.Join(parts[:versionIndex+1], "/")
		}
		return ""
	}
	return ""
}

// ClassifyRunningBinary classifies the install location that runningFile
// (the resolved path of the currently executing binary) actually ran from.
// Returns (kind, root, detail).
func ClassifyRunningBinary(runningFile string) (string, string, string) {
	if root := goInstallRoot(runningFile); root != "" {
		return InstallKindGoInstall, root, "running from a go-install location under " + root
	}

	if root := pluginCacheRoot(runningFile); root != "" {
		return InstallKindPluginCache, root,
			"running from a Claude Code plugin cache copy under " + root +
				" (path-shape heuristic: ~/.claude/plugins/cache/<marketplace>/<plugin>/<version>/...; " +
				"not a documented, guaranteed-stable Claude Code contract)"
	}

	if root := FindCheckoutRoot(filepath.Dir(runningFile)); root != "" {
		return InstallKindCheckout, root, "running from a Cadre git checkout at " + root
	}

	return InstallKindUnknown, filepath.Dir(runningFile),
		"could not classify the install kind for " + runningFile + " as a checkout, a go-install " +
			"location, or a Claude Code plugin cache -- reporting the raw resolved path only rather " +
			"than guessing"
}

// goVersionOK reports whether the running Go toolchain's runtime version
// meets MinGoVersion. runtime.Version() is the toolchain that *built* the
// binary (e.g. "go1.25.1"), not necessarily what's on PATH now -- which is
// exactly the property doctor wants: what actually produced this binary.
func goVersionOK(version string) bool {
	if strings.HasPrefix(version, "devel") {
		return true // A development toolchain build; assume newer than any release floor.
	}
	v := strings.TrimPrefix(version, "go")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return true // Can't parse -- don't fail loudly on a heuristic.
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	minParts := strings.SplitN(MinGoVersion, ".", 2)
	minMajor, err3 := strconv.Atoi(minParts[0])
	minMinor, err4 := strconv.Atoi(minParts[1])
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
		return true // Unparseable -- don't fail loudly on a heuristic.
	}
	if major != minMajor {
		return major > minMajor
	}
	return minor >= minMinor
}

// GatherDoctorReport builds the complete report. cwd and runningFile are
// injectable so tests can deterministically simulate "this process is
// actually running from a different install location" without spawning a
// real second copy of this repo; production callers pass "" for both to get
// the real cwd and os.Executable() location.
func GatherDoctorReport(cwd, runningFile string) DoctorReport {
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}
	if runningFile == "" {
		if exe, err := os.Executable(); err == nil {
			if resolved, err := filepath.EvalSymlinks(exe); err == nil {
				runningFile = resolved
			} else {
				runningFile = exe
			}
		}
	}

	kind, installRoot, detail := ClassifyRunningBinary(runningFile)
	goVersion := runtime.Version()

	report := DoctorReport{
		RunningBinary: runningFile,
		GoVersion:     goVersion,
		GoVersionOK:   goVersionOK(goVersion),
		GoMinVersion:  MinGoVersion,
		InstallKind:   kind,
		InstallRoot:   installRoot,
		InstallDetail: detail,
		CWD:           cwd,
	}

	cwdCheckoutRoot := FindCheckoutRoot(cwd)
	report.CWDCheckoutRoot = cwdCheckoutRoot

	if cwdCheckoutRoot != "" {
		// The DX trap this command exists to catch: cwd is inside a real
		// Cadre checkout, but the binary that actually ran is not that
		// checkout's own build -- either a different install kind entirely,
		// or (kind == checkout but) a different checkout root, e.g. two
		// clones on disk and PATH picked the wrong one.
		if kind != InstallKindCheckout || installRoot != cwdCheckoutRoot {
			report.Mismatch = true
			report.MismatchDetail = "cwd " + cwd + " is inside a Cadre checkout rooted at " + cwdCheckoutRoot +
				", but the binary that actually ran is " + detail + ". Run " +
				filepath.Join(cwdCheckoutRoot, "bin", "cadre") +
				" explicitly (or put it first on PATH) instead of a bare `cadre` to exercise this checkout's own code."
		}
	}

	return report
}

// RenderDoctorReport formats a report as human-readable text, mirroring
// doctor.py's _render_human.
func RenderDoctorReport(report DoctorReport) string {
	var b strings.Builder
	b.WriteString("cadre doctor\n\n")
	b.WriteString("  running binary:     " + report.RunningBinary + "\n")
	goLine := "  go version:         " + report.GoVersion
	if !report.GoVersionOK {
		goLine += " (below the required go" + report.GoMinVersion + "+)"
	}
	b.WriteString(goLine + "\n")
	// Empty detail means the caller did not probe -- say nothing rather than
	// report a failure nobody checked for. See DoctorReport's field comment.
	if report.KnowledgeStoreDetail != "" {
		b.WriteString("  knowledge store:    " + report.KnowledgeStoreDetail + "\n")
	}
	b.WriteString("  install kind:       " + report.InstallKind + "\n")
	b.WriteString("  detail:             " + report.InstallDetail + "\n")
	b.WriteString("  cwd:                " + report.CWD + "\n")
	cwdRoot := report.CWDCheckoutRoot
	if cwdRoot == "" {
		cwdRoot = "not inside a Cadre checkout"
	}
	b.WriteString("  cwd checkout root:  " + cwdRoot + "\n\n")

	if !report.GoVersionOK {
		b.WriteString("WARNING: this toolchain is below the declared floor (go.mod requires go " +
			report.GoMinVersion + "+); some subcommands may behave in ways unrelated to your change.\n\n")
	}
	if report.KnowledgeStoreDetail != "" && !report.KnowledgeStoreOK {
		b.WriteString("WARNING: this binary was built without cgo, so every `cadre knowledge` " +
			"command will fail at runtime.\n" +
			"         bin/cadre prefers a cgo build and falls back to a cgo-less one when no C\n" +
			"         compiler is on PATH. Install a C toolchain (e.g. build-essential, Xcode\n" +
			"         command line tools, or mingw-w64) and delete .cadre-build-cache/ to rebuild.\n\n")
	}
	if report.Mismatch {
		b.WriteString("WARNING: " + report.MismatchDetail + "\n")
	} else {
		b.WriteString("OK: the binary that ran matches the checkout your cwd is in (or your cwd isn't in one).\n")
	}
	return b.String()
}
