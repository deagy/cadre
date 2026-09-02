package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	"github.com/deagy/cadre/cli/internal/engine/provider"
	"github.com/deagy/cadre/cli/internal/knowledge"
	"github.com/deagy/cadre/cli/internal/orchestration"
	"github.com/deagy/cadre/cli/internal/platform"
)

// DoctorCmd is the `cadre doctor` command. Reports which cadre binary is
// running, what kind of install it is, and warns on a cwd/checkout
// mismatch. Exit code: 0 when the picture is internally consistent (or
// nothing could be determined either way); 1 when cwd sits inside a Cadre
// checkout but the binary that actually ran is demonstrably a different
// location than that checkout's own build.
func DoctorCmd(args []string) int {
	asJSON := false
	var unexpected []string
	help := false
	for _, arg := range args {
		switch arg {
		case "--json":
			asJSON = true
		case "-h", "--help":
			help = true
		default:
			unexpected = append(unexpected, arg)
		}
	}

	if help {
		fmt.Fprintln(os.Stderr, "usage: cadre doctor [--json]")
		return 0
	}
	if len(unexpected) > 0 {
		fmt.Fprintf(os.Stderr, "cadre doctor: unknown argument(s): %s\n", joinArgs(unexpected))
		fmt.Fprintln(os.Stderr, "usage: cadre doctor [--json]")
		return 2
	}

	report := orchestration.GatherDoctorReport("", "")

	// Probe the sqlite driver here rather than inside GatherDoctorReport:
	// this package already imports both, so orchestration keeps its current
	// dependency graph. See DoctorReport's field comment.
	//
	// It used to report whether the cgo sqlite3 driver was linked, because a
	// CGO_ENABLED=0 build of cadre compiled fine and then failed at the first
	// knowledge query with "go-sqlite3 requires cgo to work. This is a stub" --
	// a degraded binary an operator had to be able to find out they had. Both
	// halves of the knowledge path are pure Go now: staged records use
	// modernc.org/sqlite and retrieval is recall's, which uses the same. The
	// probe stays because a driver that fails to open is still worth catching
	// before an operator hits it mid-command.
	if err := knowledge.StagedDriverAvailable(); err != nil {
		report.KnowledgeStoreOK = false
		report.KnowledgeStoreDetail = "unavailable -- " + err.Error()
	} else {
		report.KnowledgeStoreOK = true
		report.KnowledgeStoreDetail = "available (pure-Go sqlite driver, no cgo required)"
	}

	// Which kernel this machine would actually run. Probed here for the same
	// reason the store driver is: internal/cli already imports both packages.
	// FindInstallationRoot, not RepoRoot: this must report the same kernel
	// `cadre sdlc` would run, and RepoRoot walks up from the working directory
	// looking for a `.git`. A packaged install has none and is usually invoked
	// from somewhere else entirely, so doctor reported "no packaged lifecycle
	// shim was found" on a machine where `cadre sdlc --version` answered 0.14.4
	// from that very shim. A doctor that disagrees with the command it is
	// diagnosing is worse than one that says nothing.
	kernelRoot, rootErr := platform.FindInstallationRoot()
	if rootErr != nil {
		kernelRoot = ""
	}
	report.Kernel = orchestration.ResolveKernel(
		os.Getenv("AGENTIC_SDLC_BIN"), provider.KernelVersion, exec.LookPath,
		orchestration.LookAllOnPath, orchestration.KernelVersionOf, kernelRoot)

	if asJSON {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "cadre doctor: %v\n", err)
			return 1
		}
		fmt.Println(string(data))
	} else {
		fmt.Print(orchestration.RenderDoctorReport(report))
	}

	if report.Mismatch {
		return 1
	}
	return 0
}

func joinArgs(args []string) string {
	out := ""
	for i, a := range args {
		if i > 0 {
			out += " "
		}
		out += a
	}
	return out
}
