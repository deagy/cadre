package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/deagy/cadre/cli/internal/config"
)

// sdlcDescription mirrors bin/cadre.py's SDLC_DESCRIPTION, used by usage
// text.
const sdlcDescription = "Delegated Agentic SDLC CLI"

// SDLCDeps collects the SDLC delegation dependencies that differ between
// production use and tests: where to read/write standard streams, and how
// to resolve the provider-manifest error message. Kept as a struct (rather
// than package-level os.Stdin/os.Stdout/os.Stderr and a hardcoded
// installation-message string) so tests can substitute all of them without
// mutating global process state.
type SDLCDeps struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	// InstallMessage is called only on the failure path (no sdlc binary
	// resolved), lazily, so a successful invocation never reads
	// provider.json. Mirrors bin/cadre.py's sdlc_install_message().
	InstallMessage func() string
}

// DefaultSDLCDeps returns the production dependency set: the process's own
// stdio, and an install-message function that reads provider.json under
// repoRoot the way bin/cadre.py's sdlc_install_message() does.
func DefaultSDLCDeps(repoRoot string) SDLCDeps {
	return SDLCDeps{
		Stdin:          os.Stdin,
		Stdout:         os.Stdout,
		Stderr:         os.Stderr,
		InstallMessage: func() string { return SDLCInstallMessage(repoRoot) },
	}
}

// DispatchSDLC is an exact behavioral replica of bin/cadre.py's
// dispatch_sdlc(): it resolves the Agentic SDLC kernel binary, falls back
// to the in-tree kernel shipped in this repository, injects Cadre's own
// provider bundle unless the caller opted out (see
// ResolveProviderInjection), and shells out to the resolved binary,
// returning its exit code.
//
// repoRoot must already be resolved by the caller (platform.RepoRoot() in
// production). interactive, when true, causes CADRE_INTERACTIVE=1 to be set
// in the child's environment, mirroring bin/cadre.py's _child_env().
func DispatchSDLC(ctx context.Context, repoRoot string, rest []string, interactive bool, deps SDLCDeps) int {
	sdlcBin, err := config.ResolveString(ctx, "agentic_sdlc.bin_path")
	if err != nil && !errors.Is(err, config.ErrSettingNotFound) {
		// resolve_optional() only ever raises for a global_only scope
		// violation (an untrusted project-local file setting
		// agentic_sdlc.bin_path) -- that's a security event this
		// dispatcher must surface, not a bare error swallowed silently.
		_, _ = fmt.Fprintf(deps.Stderr, "cadre: %s\n", err)
		return 1
	}
	if errors.Is(err, config.ErrSettingNotFound) {
		sdlcBin = ""
	}

	if sdlcBin == "" {
		// The kernel ships in this repository since the monorepo merge, so
		// a checkout needs no install and no AGENTIC_SDLC_BIN. This is the
		// last resort deliberately: an explicit env var or a configured
		// agentic_sdlc.bin_path still wins, and so does an `agentic-sdlc`
		// the operator installed themselves, because either is a choice
		// the human made about which kernel to run.
		inTree := filepath.Join(repoRoot, "bin", "agentic-sdlc")
		if info, statErr := os.Stat(inTree); statErr == nil && !info.IsDir() {
			sdlcBin = inTree
		}
	}

	if sdlcBin == "" {
		message := "a compatible version of Agentic SDLC is required; install it from https://github.com/deagy/cadre-kernel"
		if deps.InstallMessage != nil {
			message = deps.InstallMessage()
		}
		_, _ = fmt.Fprintln(deps.Stderr, message)
		return 1
	}

	forwarded, suppressDefault := ResolveProviderInjection(rest)
	var providerArgs []string
	if !suppressDefault {
		providerArgs = []string{"--provider", filepath.Join(repoRoot, "provider", "provider.json")}
	}

	args := make([]string, 0, len(providerArgs)+len(forwarded))
	args = append(args, providerArgs...)
	args = append(args, forwarded...)

	cmd := exec.CommandContext(ctx, sdlcBin, args...)
	cmd.Env = childEnv(interactive)
	cmd.Stdin = deps.Stdin
	cmd.Stdout = deps.Stdout
	cmd.Stderr = deps.Stderr

	runErr := cmd.Run()
	if exitErr, ok := asExitError(runErr); ok {
		return exitErr
	}
	if runErr != nil {
		// The process could not even be started (binary missing/not
		// executable, permission error, etc.) -- report and return the
		// same generic-error exit code bin/cadre.py's subprocess.run would
		// surface via a raised OSError propagating out of main().
		_, _ = fmt.Fprintf(deps.Stderr, "cadre: %s\n", runErr)
		return 1
	}
	return 0
}

// asExitError extracts a subprocess's exit code from the error exec.Cmd.Run
// returns, mirroring Python's subprocess.run(...).returncode. Returns
// ok=false when err is nil (exit code 0) or is not an *exec.ExitError (the
// process never ran at all).
func asExitError(err error) (code int, ok bool) {
	if err == nil {
		return 0, false
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), true
	}
	return 0, false
}

// SDLCInstallMessage reads provider/provider.json under repoRoot to report
// the kernel version range this Cadre checkout requires, exactly mirroring
// bin/cadre.py's sdlc_install_message(). Read lazily and only on the
// failure path so DispatchSDLC stays cheap on every successful invocation.
// Do not hardcode a version range here: provider.json's own `version` and
// its `kernel_compatibility` are different version lines, and quoting the
// wrong one sends operators to a kernel ten minor versions too old.
func SDLCInstallMessage(repoRoot string) string {
	requirement := "a compatible version"
	if compat, err := readKernelCompatibility(repoRoot); err == nil {
		requirement = fmt.Sprintf("v%s or newer (below v%s)", compat.Minimum, compat.MaximumExclusive)
	}
	return fmt.Sprintf(
		"cadre: Agentic SDLC %s is required; install it from https://github.com/deagy/cadre-kernel",
		requirement,
	)
}

// kernelCompatibility mirrors provider/provider.json's kernel_compatibility
// object.
type kernelCompatibility struct {
	Minimum          string `json:"minimum"`
	MaximumExclusive string `json:"maximum_exclusive"`
}

func readKernelCompatibility(repoRoot string) (kernelCompatibility, error) {
	manifestPath := filepath.Join(repoRoot, "provider", "provider.json")
	contents, err := os.ReadFile(manifestPath)
	if err != nil {
		return kernelCompatibility{}, err
	}

	var manifest struct {
		KernelCompatibility kernelCompatibility `json:"kernel_compatibility"`
	}
	if err := json.Unmarshal(contents, &manifest); err != nil {
		return kernelCompatibility{}, err
	}
	if manifest.KernelCompatibility.Minimum == "" || manifest.KernelCompatibility.MaximumExclusive == "" {
		return kernelCompatibility{}, fmt.Errorf("provider.json: missing kernel_compatibility fields")
	}
	return manifest.KernelCompatibility, nil
}

// childEnv mirrors bin/cadre.py's _child_env(): when interactive is false,
// the child inherits the process's own environment unmodified (nil tells
// exec.Cmd to do exactly that). When true, CADRE_INTERACTIVE=1 is added to
// a copy of the current environment -- this process's own environment is
// never mutated.
func childEnv(interactive bool) []string {
	if !interactive {
		return nil
	}
	return append(os.Environ(), "CADRE_INTERACTIVE=1")
}
