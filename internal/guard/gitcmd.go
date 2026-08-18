package guard

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var envAssignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// wrapperFlagsWithValue maps a wrapper program to the flags of its OWN that
// consume the following token as a value.
//
// Getting an arity wrong in the "takes no value" direction makes the scan stop
// one token early, so the wrapped git is never recognised; the opposite error
// steps past a real command. Both directions only ever lose coverage, never
// invent a block, which is the stance this package takes everywhere.
//
// Deliberately not exhaustive and cannot be: it enumerates the prefix wrappers
// an agent plausibly reaches for. Flags whose value is OPTIONAL are absent on
// purpose -- written bare, such a flag must not eat the next token.
var wrapperFlagsWithValue = map[string]map[string]bool{
	"sudo": setOf("-u", "--user", "-g", "--group", "-p", "--prompt", "-C", "--close-from",
		"-h", "--host", "-r", "--role", "-t", "--type", "-U", "--other-user",
		"-T", "--command-timeout", "-D", "--chdir", "-R", "--chroot"),
	"command": setOf(),
	"exec":    setOf(),
	"nohup":   setOf(),
	"time":    setOf(),
	// `env -u NAME`, `env -C DIR`, `env -S STRING`. Missing one lets the
	// flag's VALUE be mistaken for the start of the real command.
	"env":     setOf("-u", "--unset", "-C", "--chdir", "-S", "--split-string"),
	"timeout": setOf("-s", "--signal", "-k", "--kill-after"),
	"nice":    setOf("-n", "--adjustment"),
	"ionice":  setOf("-c", "--class", "-n", "--classdata", "-p", "--pid", "-P", "--pgid", "-u", "--uid"),
	"stdbuf":  setOf("-i", "--input", "-o", "--output", "-e", "--error"),
	"setsid":  setOf(),
	"chrt":    setOf("-p", "--pid", "-T", "--sched-runtime", "-P", "--sched-period", "-D", "--sched-deadline"),
	"taskset": setOf("-c", "--cpu-list", "-p", "--pid"),
	"xargs": setOf("-I", "--replace", "-L", "--max-lines", "-n", "--max-args",
		"-P", "--max-procs", "-s", "--max-chars", "-d", "--delimiter",
		"-E", "--eof", "-a", "--arg-file", "--process-slot-var"),
}

func setOf(values ...string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}

// wrapperLeadingPositionals are wrappers taking a mandatory positional of
// their own before the command: `timeout <duration> <cmd>`.
//
// Skipped lazily -- only while the current token is not `git` -- because
// `taskset -c 0,1 git ...` supplies the same value through a flag and then has
// no positional left, so an unconditional skip would step over git itself.
var wrapperLeadingPositionals = map[string]int{"timeout": 1, "chrt": 1, "taskset": 1}

// wrapperTakesEnvAssignments are wrappers accepting VAR=value before the
// command they run.
var wrapperTakesEnvAssignments = map[string]bool{"env": true, "sudo": true}

var gitGlobalFlagsWithValue = setOf("-C", "--git-dir", "--work-tree", "--namespace", "-c")

// stripLeadingWrappers exposes the real command at tokens[0].
//
// Every wrapper is skipped along with its own flags, flag values and leading
// positional, because a bare "skip one token" rule leaves those behind and
// stops the git parse from ever recognising git at tokens[0] -- which is
// exactly how `timeout 10 git worktree remove <path>` walked through this
// guard once. Wrappers nest, so the outer loop re-runs against what the
// previous one left.
func stripLeadingWrappers(tokens []string) []string {
	i := 0
	for i < len(tokens) {
		token := tokens[i]
		if envAssignment.MatchString(token) {
			i++
			continue
		}
		flagsWithValue, isWrapper := wrapperFlagsWithValue[token]
		if !isWrapper {
			break
		}
		takesAssignments := wrapperTakesEnvAssignments[token]
		positionalsLeft := wrapperLeadingPositionals[token]
		i++
		for i < len(tokens) {
			next := tokens[i]
			if next == "--" {
				i++
				break
			}
			if flagsWithValue[next] {
				i += 2
				continue
			}
			if strings.HasPrefix(next, "-") && next != "-" {
				i++
				continue
			}
			if takesAssignments && envAssignment.MatchString(next) {
				i++
				continue
			}
			if positionalsLeft > 0 && next != "git" {
				positionalsLeft--
				i++
				continue
			}
			break
		}
	}
	return tokens[i:]
}

var (
	findCommandPrimaries   = setOf("-exec", "-execdir", "-ok", "-okdir")
	findCommandTerminators = setOf(";", "+")
)

// findCommandInvocations returns the command token lists carried in `find ...
// -exec <cmd> ... ;` position.
//
// Unlike every prefix wrapper these take the command in ARGUMENT position, so
// prefix stripping cannot reach them: the invocation sits in the middle of
// find's own expression. Returns nil for anything that is not a find
// invocation, so callers can concatenate unconditionally.
func findCommandInvocations(tokens []string) [][]string {
	if len(tokens) == 0 || tokens[0] != "find" {
		return nil
	}
	var found [][]string
	for i := 0; i < len(tokens); {
		if !findCommandPrimaries[tokens[i]] {
			i++
			continue
		}
		i++
		var body []string
		for i < len(tokens) && !findCommandTerminators[tokens[i]] {
			body = append(body, tokens[i])
			i++
		}
		if len(body) > 0 {
			found = append(found, body)
		}
	}
	return found
}

var shellDashCPrograms = setOf("bash", "sh", "zsh")

// findShellDashCScript returns the script of a `bash`/`sh`/`zsh -c <script>`
// invocation, or "" and false otherwise.
//
// Intentionally narrow: it recognises `-c` bare or combined into a leading run
// of short flags (`-lc`), not long-flag spellings, and gives up on the first
// token it does not recognise as a flag.
func findShellDashCScript(tokens []string) (string, bool) {
	if len(tokens) == 0 || !shellDashCPrograms[tokens[0]] {
		return "", false
	}
	for i := 1; i < len(tokens); {
		token := tokens[i]
		if !strings.HasPrefix(token, "-") || token == "-" {
			return "", false
		}
		if token == "--" {
			return "", false
		}
		if token == "-c" {
			if i+1 < len(tokens) {
				return tokens[i+1], true
			}
			return "", false
		}
		if strings.HasPrefix(token, "--") {
			i++
			continue
		}
		if strings.Contains(token[1:], "c") {
			// Combined short flags containing `c`, e.g. `-lc`. The shell
			// consumes `-c`'s argument regardless of where in the group it is.
			if i+1 < len(tokens) {
				return tokens[i+1], true
			}
			return "", false
		}
		i++
	}
	return "", false
}

// pathJoin joins as Python's os.path.join does, deliberately WITHOUT cleaning.
//
// filepath.Join would collapse `sub/..` to `.`. Those name the same directory
// only when `sub` exists: when it does not, the uncleaned form fails to resolve
// and the guard falls open, while the cleaned form would silently succeed
// against the parent. Keeping the literal keeps the two implementations
// agreeing on which directory git is actually run in.
func pathJoin(base, element string) string {
	if element == "" {
		return base
	}
	if filepath.IsAbs(element) {
		return element
	}
	if base == "" {
		return element
	}
	if strings.HasSuffix(base, string(os.PathSeparator)) || strings.HasSuffix(base, "/") {
		return base + element
	}
	return base + string(os.PathSeparator) + element
}

// accumulateDashC folds one `git -C <value>` onto what is already accumulated.
//
// Git applies repeated -C CUMULATIVELY, each relative to the previous, and an
// absolute value resets it. Keeping only the LAST value resolved `git -C
// .worktrees -C ../ worktree prune` to `<base>/../` instead of
// `<base>/.worktrees/..`, so every state-probing handler ran in the wrong
// directory, got a non-zero exit and failed open. The flat-refusal verbs were
// immune precisely because they never probe state.
func accumulateDashC(current, value string) string {
	if value == "" {
		return current
	}
	if filepath.IsAbs(value) {
		return value
	}
	if current == "" {
		return value
	}
	return pathJoin(current, value)
}

// recordGitConfig records one `git -c <name>=<value>` pair.
//
// Config names are case-insensitive, so the key is lowercased. A `-c <name>`
// with no `=` sets a boolean and carries no definition, so it is ignored.
// Only the two-part `<section>.<key>` spelling normalises correctly -- the
// middle component of a three-part name is case-sensitive in git, and nothing
// here reads a three-part key.
func recordGitConfig(config map[string]string, pair string) {
	name, value, found := strings.Cut(pair, "=")
	if !found {
		return
	}
	config[strings.ToLower(strings.TrimSpace(name))] = value
}

// gitInvocation is a parsed `git <subcommand> ...`.
type gitInvocation struct {
	subcommand  string
	args        []string
	explicitCwd string // the accumulated -C directory
	config      map[string]string
}

// parseGitInvocation skips global flags to find the subcommand, or reports
// false when this is not a recognisable git invocation.
func parseGitInvocation(tokens []string) (gitInvocation, bool) {
	if len(tokens) == 0 || tokens[0] != "git" {
		return gitInvocation{}, false
	}
	parsed := gitInvocation{config: map[string]string{}}
	i := 1
	for i < len(tokens) {
		token := tokens[i]
		if token == "-C" {
			if i+1 < len(tokens) {
				parsed.explicitCwd = accumulateDashC(parsed.explicitCwd, tokens[i+1])
			}
			i += 2
			continue
		}
		if token == "-c" {
			// Only the detached spelling exists: `git -calias.x=...` is
			// rejected by git itself, so there is no attached form to parse.
			if i+1 < len(tokens) {
				recordGitConfig(parsed.config, tokens[i+1])
			}
			i += 2
			continue
		}
		if gitGlobalFlagsWithValue[token] {
			i += 2
			continue
		}
		attached := false
		for flag := range gitGlobalFlagsWithValue {
			if strings.HasPrefix(token, flag+"=") {
				attached = true
				break
			}
		}
		if attached {
			i++
			continue
		}
		if strings.HasPrefix(token, "-") {
			i++
			continue
		}
		break
	}
	if i >= len(tokens) {
		return gitInvocation{}, false
	}
	parsed.subcommand = tokens[i]
	parsed.args = tokens[i+1:]
	return parsed, true
}

// git state helpers. All fail open -- an unresolvable repository state never
// becomes a false-positive block.

// runGit returns the exit code, stdout and stderr, or ok=false when the
// command could not be run at all.
func runGit(args []string, cwd string) (code int, stdout, stderr string, ok bool) {
	command := exec.Command("git", args...)
	command.Dir = cwd
	var out, errOut strings.Builder
	command.Stdout = &out
	command.Stderr = &errOut
	timer := time.AfterFunc(5*time.Second, func() {
		if command.Process != nil {
			_ = command.Process.Kill()
		}
	})
	err := command.Run()
	stopped := timer.Stop()
	if !stopped {
		return 0, "", "", false // timed out
	}
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return 0, "", "", false
		}
		return exitErr.ExitCode(), out.String(), errOut.String(), true
	}
	return 0, out.String(), errOut.String(), true
}

func resolveCwd(baseCwd, explicitCwd string) string {
	if explicitCwd != "" {
		if filepath.IsAbs(explicitCwd) {
			return explicitCwd
		}
		base := baseCwd
		if base == "" {
			base, _ = os.Getwd()
		}
		return pathJoin(base, explicitCwd)
	}
	if baseCwd != "" {
		return baseCwd
	}
	working, _ := os.Getwd()
	return working
}

// gitStatusPorcelain reports the porcelain status, or ok=false for "could not
// determine" -- which callers must treat as fail-open, not as clean or dirty.
func gitStatusPorcelain(cwd string, paths []string) (string, bool) {
	args := []string{"status", "--porcelain"}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	code, stdout, _, ok := runGit(args, cwd)
	if !ok || code != 0 {
		return "", false
	}
	return stdout, true
}

func isLocalBranch(cwd, name string) bool {
	if name == "" {
		return false
	}
	code, _, _, ok := runGit([]string{"show-ref", "--verify", "--quiet",
		"refs/heads/" + name}, cwd)
	return ok && code == 0
}
