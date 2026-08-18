package guard

import (
	"fmt"
	"regexp"
	"strings"
)

// Per-subcommand checks. Each returns a refusal, or nil to express no opinion.

type handler func(args []string, cwd string, config map[string]string) *Decision

func splitFlagSets(args []string) (shortChars map[byte]bool, longOpts map[string]bool, positional []string) {
	shortChars, longOpts = map[byte]bool{}, map[string]bool{}
	for _, argument := range args {
		switch {
		case strings.HasPrefix(argument, "--"):
			longOpts[strings.SplitN(argument, "=", 2)[0]] = true
		case strings.HasPrefix(argument, "-") && len(argument) > 1:
			for i := 1; i < len(argument); i++ {
				shortChars[argument[i]] = true
			}
		default:
			positional = append(positional, argument)
		}
	}
	return shortChars, longOpts, positional
}

func checkReset(args []string, cwd string, _ map[string]string) *Decision {
	hard := false
	for _, argument := range args {
		if argument == "--hard" {
			hard = true
		}
	}
	if !hard {
		return nil
	}
	var ref string
	for _, argument := range args {
		if !strings.HasPrefix(argument, "-") {
			ref = argument
			break
		}
	}

	status, ok := gitStatusPorcelain(cwd, nil)
	dirty := ok && status != ""

	movesBranch := false
	if ref != "" {
		code1, head, _, ok1 := runGit([]string{"rev-parse", "--verify", "HEAD"}, cwd)
		code2, target, _, ok2 := runGit([]string{"rev-parse", "--verify", ref}, cwd)
		if ok1 && ok2 && code1 == 0 && code2 == 0 {
			movesBranch = strings.TrimSpace(head) != strings.TrimSpace(target)
		}
		// If either rev-parse failed, stay fail-open: `git reset --hard` will
		// error out on its own if the ref is bad.
	}
	if !dirty && !movesBranch {
		return nil
	}
	var reasons []string
	if dirty {
		reasons = append(reasons, "discard uncommitted changes in the working tree")
	}
	if movesBranch {
		reasons = append(reasons, "move the current branch to a different commit, which can strand any "+
			"unpushed commits currently on it")
	}
	return &Decision{Reason: "Blocked: `git reset --hard` would " + strings.Join(reasons, " and ") + ". " +
		"If you want to give up your own uncommitted edits, commit or stash them first " +
		"(`git stash push`). If you need the branch to point somewhere else, use a " +
		"non---hard reset (`git reset <ref>` keeps the working tree contents) or ask the " +
		"operator to confirm a hard reset themselves."}
}

func checkClean(args []string, cwd string, _ map[string]string) *Decision {
	shortChars, longOpts, _ := splitFlagSets(args)
	isForce := shortChars['f'] || longOpts["--force"]
	isDryRun := shortChars['n'] || longOpts["--dry-run"]
	if !isForce || isDryRun {
		// Without -f, `git clean` refuses to delete anything on its own, and
		// an explicit dry run is inherently non-destructive.
		return nil
	}
	dryArgs := []string{"clean", "-n"}
	for _, letter := range []byte{'d', 'x', 'X'} {
		if shortChars[letter] {
			dryArgs = append(dryArgs, "-"+string(letter))
		}
	}
	code, out, _, ok := runGit(dryArgs, cwd)
	if !ok || code != 0 {
		// Can't confirm repo state; the real `git clean` fails on its own in
		// the same situation, so don't block on uncertainty here.
		return nil
	}
	if strings.TrimSpace(out) == "" {
		return nil
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		files = append(files, strings.TrimSpace(line))
	}
	example := "an untracked path"
	if len(files) > 0 {
		example = files[0]
	}
	return &Decision{Reason: fmt.Sprintf(
		"Blocked: `git clean` would permanently delete %d untracked path(s) "+
			"(e.g. %s), which git cannot recover afterward -- there is no commit or "+
			"stash to undo it from. Review what would be removed with `git clean -n` (add -d/-x "+
			"to match your flags) first, then either re-run once you've confirmed it, or remove "+
			"the specific paths you actually intend to delete by name.", len(files), example)}
}

func checkBranch(args []string, _ string, _ map[string]string) *Decision {
	shortChars, longOpts, positional := splitFlagSets(args)
	forceDelete := shortChars['D'] ||
		((shortChars['d'] || longOpts["--delete"]) && (shortChars['f'] || longOpts["--force"]))
	if !forceDelete {
		return nil
	}
	target := "<branch>"
	if len(positional) > 0 {
		target = positional[0]
	}
	return &Decision{Reason: fmt.Sprintf(
		"Blocked: `git branch -D`/`--delete --force` on '%s' bypasses git's own "+
			"unmerged-work safety check and can discard commits that no other ref points at. "+
			"Use `git branch -d %s` instead -- it refuses when the branch has unmerged "+
			"work -- or ask the operator to force-delete it themselves if that's really intended.",
		target, target)}
}

var colonRefspec = regexp.MustCompile(`^:\S+$`)

func checkPush(args []string, _ string, _ map[string]string) *Decision {
	hasForce, hasLease, hasDeleteFlag, hasColonRefspec := false, false, false, false
	for _, argument := range args {
		if argument == "-f" || argument == "--force" {
			hasForce = true
		}
		if argument == "--force-with-lease" || strings.HasPrefix(argument, "--force-with-lease=") {
			hasLease = true
		}
		if argument == "--delete" || argument == "-d" {
			hasDeleteFlag = true
		}
		if colonRefspec.MatchString(argument) {
			hasColonRefspec = true
		}
	}
	if hasDeleteFlag || hasColonRefspec {
		return &Decision{Reason: "Blocked: this push deletes a remote branch, which removes it for everyone " +
			"using that remote and can't be undone from this working tree. If this is " +
			"really intended, ask the operator to delete the remote branch themselves."}
	}
	if hasForce && !hasLease {
		return &Decision{Reason: "Blocked: `git push --force` can silently overwrite commits someone else has " +
			"already pushed, with no local way to detect it beforehand. Use " +
			"`git push --force-with-lease` instead -- it refuses on its own if the remote " +
			"has moved since your last fetch."}
	}
	return nil
}

// checkRefIntoPaths is shared by `git checkout <ref> -- <paths>` and `git
// restore --source=<ref> <paths>`: destructive only when a source ref is given
// AND the target paths currently have uncommitted changes.
func checkRefIntoPaths(cwd, ref string, paths []string, command string) *Decision {
	if ref == "" {
		return nil // no ref: routine "discard my own edit" form, always allowed
	}
	status, ok := gitStatusPorcelain(cwd, paths)
	if !ok {
		// Can't determine dirty state; the real command will fail or succeed
		// on its own in that situation.
		return nil
	}
	if strings.TrimSpace(status) == "" {
		return nil
	}
	pathDesc := "the given path(s)"
	if len(paths) > 0 {
		pathDesc = strings.Join(paths, ", ")
	}
	return &Decision{Reason: fmt.Sprintf(
		"Blocked: `git %s` from '%s' would overwrite uncommitted changes to "+
			"%s with that ref's version, destroying the current edits with no way "+
			"back. Commit or stash the current changes first (`git stash push -- "+
			"%s`), or re-run naming only paths that are actually clean.",
		command, ref, pathDesc, pathDesc)}
}

// checkoutFlagsWithValue are `git checkout` flags consuming the following
// token, so a start point is never confused with one of their values.
var checkoutFlagsWithValue = setOf("-b", "-B", "-U", "--unified", "--conflict", "--orphan",
	"--pathspec-from-file", "--inter-hunk-context")

// switchFlagsWithValue are the `git switch` equivalents. Note `-C` here is the
// SUBCOMMAND's flag and has nothing to do with git's global `-C <dir>`, which
// the invocation parse has already consumed.
var switchFlagsWithValue = setOf("-c", "--create", "-C", "--force-create", "--conflict", "--orphan")

// checkForceCreatedBranch is the shared -B/-C check for checkout, switch and
// worktree add.
//
// Refuses only when the name already exists as a local branch AND its tip
// differs from the resolved start point -- i.e. only when the command would
// actually move a branch off its commits. A name that does not exist behaves
// exactly like -b/-c, and a branch already at the start point moves nothing.
//
// startIndex is where the start point sits among the positionals: 0 for
// `checkout -B <branch> [<start>]`, 1 for `worktree add -B <branch> <path>
// [<start>]`.
func checkForceCreatedBranch(cwd string, args []string, forced string,
	flagsWithValue map[string]bool, spelling string, startIndex int) *Decision {
	if !isLocalBranch(cwd, forced) {
		return nil // behaves like -b/-c: nothing to move
	}
	found := positionalArgs(args, flagsWithValue)
	start := "HEAD"
	if len(found) > startIndex {
		start = found[startIndex]
	}
	code1, current, _, ok1 := runGit([]string{"rev-parse", "--verify", forced}, cwd)
	code2, target, _, ok2 := runGit([]string{"rev-parse", "--verify", start}, cwd)
	if !ok1 || !ok2 || code1 != 0 || code2 != 0 {
		return nil // indeterminate; git will error on its own
	}
	if strings.TrimSpace(current) == strings.TrimSpace(target) {
		return nil // branch already points there: moves nothing
	}
	return &Decision{Reason: fmt.Sprintf(
		"Blocked: `git %s %s` force-resets the existing branch "+
			"'%s' to '%s', moving it off the commits it points at now -- git "+
			"reports this only as a 'Switched to and reset branch' note, and any commit no "+
			"other ref reaches is then recoverable from `git reflog` alone. That is "+
			"`agent-autonomy.yaml`'s `discard_uncommitted_work_or_move_branches: never`, and "+
			"`workspace-isolation.md` names this flag. Creating a branch is allowed: use the "+
			"non-forcing spelling with a name that does not exist yet (git refuses it if the "+
			"name is taken), or check out '%s' where it already is.",
		spelling, forced, forced, start, forced)}
}

func checkCheckout(args []string, cwd string, _ map[string]string) *Decision {
	if len(args) == 0 {
		return nil // bare `git checkout`: lists status, not destructive
	}
	if flagPresent(args, "-B") {
		forced, ok := flagValue(args, "-B")
		if !ok || forced == "" {
			return nil // `-B` with no resolvable name; git errors on its own
		}
		return checkForceCreatedBranch(cwd, args, forced, checkoutFlagsWithValue, "checkout -B", 0)
	}
	if flagPresent(args, "-b") {
		// Creating a branch pointer. Genuinely safe: git refuses -b when the
		// branch already exists.
		return nil
	}
	for index, argument := range args {
		if argument != "--" {
			continue
		}
		var pre []string
		for _, candidate := range args[:index] {
			if !strings.HasPrefix(candidate, "-") {
				pre = append(pre, candidate)
			}
		}
		paths := args[index+1:]
		ref := ""
		if len(pre) > 0 {
			ref = pre[0]
		}
		return checkRefIntoPaths(cwd, ref, paths, "checkout")
	}
	var positional []string
	for _, argument := range args {
		if !strings.HasPrefix(argument, "-") {
			positional = append(positional, argument)
		}
	}
	if len(positional) == 0 {
		return nil
	}
	if len(positional) == 1 {
		name := positional[0]
		if isLocalBranch(cwd, name) {
			return checkBranchSwitch(cwd, name)
		}
		// Not a known local branch: a bare pathspec checkout, always allowed.
		return nil
	}
	return checkRefIntoPaths(cwd, positional[0], positional[1:], "checkout")
}

func checkBranchSwitch(cwd, branch string) *Decision {
	status, ok := gitStatusPorcelain(cwd, nil)
	if !ok {
		return nil
	}
	if strings.TrimSpace(status) == "" {
		return nil // clean tree: nothing to strand or carry across branches
	}
	return &Decision{Reason: fmt.Sprintf(
		"Blocked: switching to branch '%s' while the working tree has uncommitted "+
			"changes risks carrying edits onto a branch they don't belong on, or stranding "+
			"another session's expectation of what branch this tree is on. Commit or stash "+
			"your changes first (`git stash push`), or confirm with the operator before "+
			"switching a tree you didn't create.", branch)}
}

func checkRestore(args []string, cwd string, _ map[string]string) *Decision {
	if len(args) == 0 {
		return nil
	}
	source := ""
	var paths []string
	for i := 0; i < len(args); {
		argument := args[i]
		if argument == "--source" || argument == "-s" {
			if i+1 < len(args) {
				source = args[i+1]
			}
			i += 2
			continue
		}
		if strings.HasPrefix(argument, "--source=") {
			source = strings.SplitN(argument, "=", 2)[1]
			i++
			continue
		}
		if argument == "--" {
			paths = append(paths, args[i+1:]...)
			break
		}
		if strings.HasPrefix(argument, "-") {
			i++
			continue
		}
		paths = append(paths, argument)
		i++
	}
	return checkRefIntoPaths(cwd, source, paths, "restore")
}

// checkSwitch guards `git switch`, the newer spelling of the checkout
// operations above.
//
// -C/--force-create is `checkout -B` under another name. The plain `git switch
// <branch>` form gets checkout's dirty-tree check for the same reason: leaving
// it out would make the entire branch-switch guard bypassable by choosing the
// other spelling of the same operation. -c/--create (git refuses it when the
// branch exists), --orphan and -d/--detach move no existing branch.
func checkSwitch(args []string, cwd string, _ map[string]string) *Decision {
	if len(args) == 0 {
		return nil // bare `git switch`: errors, mutates nothing
	}
	if flagPresent(args, "-C") || flagPresent(args, "--force-create") {
		forced, ok := flagValue(args, "-C")
		if !ok || forced == "" {
			forced, ok = flagValue(args, "--force-create")
		}
		if !ok || forced == "" {
			return nil // no resolvable name; git errors on its own
		}
		return checkForceCreatedBranch(cwd, args, forced, switchFlagsWithValue, "switch -C", 0)
	}
	hasOrphan, hasDetach := false, false
	for _, argument := range args {
		if argument == "--orphan" {
			hasOrphan = true
		}
		if argument == "--detach" {
			hasDetach = true
		}
	}
	if flagPresent(args, "-c") || flagPresent(args, "--create") || hasOrphan ||
		flagPresent(args, "-d") || hasDetach {
		return nil
	}
	found := positionalArgs(args, switchFlagsWithValue)
	if len(found) == 0 {
		return nil
	}
	if isLocalBranch(cwd, found[0]) {
		return checkBranchSwitch(cwd, found[0])
	}
	return nil
}

// How `git gc` decides whether to deregister a worktree, and the default it
// uses. gc's own --prune=<date> governs loose-OBJECT pruning and does not
// reach worktree registrations at all, so the probe below is `worktree
// prune`'s dry run at gc's effective expiry rather than at prune's immediate
// default -- using the immediate default would block routine gc runs that
// deregister nothing.
const (
	gcWorktreePruneExpireDefault = "3.months.ago"
	gcWorktreePruneExpireKey     = "gc.worktreepruneexpire"
)

// checkGC guards `git gc`, scoped to worktree registrations only.
//
// gc runs worktree pruning as housekeeping, so it reaches the exact
// registration state the worktree prune refusal exists to protect, through a
// subcommand that names no worktree. Deliberately NOT extended to gc's
// destructive surface generally -- reflog expiry and --prune=now object
// pruning remain the documented gap they were.
func checkGC(_ []string, cwd string, config map[string]string) *Decision {
	expire, present := config[gcWorktreePruneExpireKey]
	if !present {
		code, out, _, ok := runGit([]string{"config", "--get", "gc.worktreePruneExpire"}, cwd)
		if ok && code == 0 && strings.TrimSpace(out) != "" {
			expire = strings.TrimSpace(out)
		} else {
			expire = gcWorktreePruneExpireDefault
		}
	}
	if expire == "" {
		expire = gcWorktreePruneExpireDefault
	}
	code, out, errOut, ok := runGit([]string{"worktree", "prune", "-n", "-v", "--expire", expire}, cwd)
	if !ok || code != 0 {
		return nil // can't confirm state; the real command fails the same way
	}
	entries := reportEntries(out, errOut)
	if len(entries) == 0 {
		return nil // nothing prunable: gc deregisters nothing
	}
	return &Decision{Reason: fmt.Sprintf(
		"Blocked: `git gc` prunes worktrees as part of its own housekeeping, and here "+
			"that would deregister %d worktree(s) (e.g. %s). Like "+
			"`git worktree prune`, gc names no target -- it removes whatever git considers "+
			"unreachable, which can include a teammate's worktree on a momentarily "+
			"unavailable path. `workspace-isolation.md` says never remove or prune a worktree "+
			"yourself. Inspect what would go with `git worktree prune -n -v` (allowed, it "+
			"removes nothing) and report it, or ask the operator to run gc themselves.",
		len(entries), entries[0])}
}

// reportEntries collects a prune dry run's lines. git reports it on stderr,
// not stdout, so both are considered -- a future or older git writing to
// either is still caught.
func reportEntries(stdout, stderr string) []string {
	var parts []string
	for _, part := range []string{strings.TrimSpace(stdout), strings.TrimSpace(stderr)} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	report := strings.Join(parts, "\n")
	if report == "" {
		return nil
	}
	var entries []string
	for _, line := range strings.Split(report, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			entries = append(entries, trimmed)
		}
	}
	return entries
}

// worktreeAddFlagsWithValue consume the following token, so it is not mistaken
// for the new worktree's path or its start point.
var worktreeAddFlagsWithValue = setOf("-b", "-B", "--reason")

func checkWorktree(args []string, cwd string, _ map[string]string) *Decision {
	verbIndex := -1
	for i, argument := range args {
		if !strings.HasPrefix(argument, "-") {
			verbIndex = i
			break
		}
	}
	if verbIndex < 0 {
		return nil // bare `git worktree`: prints usage, mutates nothing
	}
	verb := args[verbIndex]
	rest := args[verbIndex+1:]

	firstPositional := func(fallback string) string {
		for _, argument := range rest {
			if !strings.HasPrefix(argument, "-") {
				return argument
			}
		}
		return fallback
	}

	switch verb {
	case "remove":
		return &Decision{Reason: fmt.Sprintf(
			"Blocked: `git worktree remove` on '%s' deregisters a worktree, which "+
				"is a destructive git-metadata operation requiring human approval "+
				"(`agent-autonomy.yaml`: destructive_action: human_approval). "+
				"`workspace-isolation.md` says never remove or prune a worktree yourself -- "+
				"including one you created, and including an inspection worktree you are done "+
				"with: the worktree IS the deliverable location until a human or the "+
				"dispatching process decides otherwise. Leave it in place and say in your "+
				"result that it can be cleaned up, or ask the operator to remove it themselves.",
			firstPositional("<worktree>"))}
	case "move":
		return &Decision{Reason: fmt.Sprintf(
			"Blocked: `git worktree move` relocates the registered worktree '%s'. "+
				"Any session whose working directory is the old path loses its tree mid-task, "+
				"with no error at the moment of the move. Rewriting another session's worktree "+
				"registration is a destructive git-metadata operation "+
				"(`agent-autonomy.yaml`: destructive_action: human_approval) and "+
				"`workspace-isolation.md` reserves worktree cleanup and relocation to the "+
				"operator. Create a new worktree at the path you want instead, or ask the "+
				"operator to move this one.",
			firstPositional("<worktree>"))}
	case "prune":
		shortChars, longOpts, _ := splitFlagSets(rest)
		if shortChars['n'] || longOpts["--dry-run"] {
			return nil // caller's own dry run: reports, removes nothing
		}
		dryArgs := []string{"worktree", "prune", "-n", "-v"}
		if expire, ok := flagValue(rest, "--expire"); ok && expire != "" {
			dryArgs = append(dryArgs, "--expire", expire)
		}
		code, out, errOut, ok := runGit(dryArgs, cwd)
		if !ok || code != 0 {
			return nil // can't confirm state; the real command fails the same way
		}
		entries := reportEntries(out, errOut)
		if len(entries) == 0 {
			return nil // nothing prunable: the command would be a no-op
		}
		return &Decision{Reason: fmt.Sprintf(
			"Blocked: `git worktree prune` would deregister %d worktree(s) "+
				"(e.g. %s). Prune names no target -- it removes whatever git currently "+
				"considers unreachable, which can include a teammate's worktree sitting on a "+
				"momentarily unavailable path, so you cannot tell from this command that only "+
				"your own worktrees are affected. `workspace-isolation.md` says never remove or "+
				"prune a worktree yourself. Inspect what would go with "+
				"`git worktree prune -n -v` (allowed, it removes nothing) and report it, or ask "+
				"the operator to prune themselves.",
			len(entries), entries[0])}
	case "add":
		forced, ok := flagValue(rest, "-B")
		if !ok || forced == "" {
			return nil // plain add/-b: explicitly allowed, creates only
		}
		if !isLocalBranch(cwd, forced) {
			return nil // -B on a new name behaves like -b: nothing to move
		}
		positional := positionalArgs(rest, worktreeAddFlagsWithValue)
		// positional[0] is the new worktree's path; positional[1], if present,
		// is the start point.
		start := "HEAD"
		if len(positional) > 1 {
			start = positional[1]
		}
		code1, current, _, ok1 := runGit([]string{"rev-parse", "--verify", forced}, cwd)
		code2, target, _, ok2 := runGit([]string{"rev-parse", "--verify", start}, cwd)
		if !ok1 || !ok2 || code1 != 0 || code2 != 0 {
			return nil
		}
		if strings.TrimSpace(current) == strings.TrimSpace(target) {
			return nil
		}
		return &Decision{Reason: fmt.Sprintf(
			"Blocked: `git worktree add -B %s` force-resets the existing branch "+
				"'%s' to '%s', moving it off the commits it points at now -- git "+
				"reports this only as a 'resetting branch' note, and any commit no other ref "+
				"reaches is then recoverable from `git reflog` alone. That is "+
				"`agent-autonomy.yaml`'s `discard_uncommitted_work_or_move_branches: never`. "+
				"Creating a worktree is allowed: use `git worktree add -b <new-branch>` with a "+
				"name that doesn't exist yet (git refuses `-b` if it does), or check out "+
				"'%s' into the new worktree without -B if you want it where it already is.",
			forced, forced, start, forced)}
	}
	// list / lock / unlock / repair and anything else: no opinion.
	return nil
}

// handlers is kept in lockstep with GIT_GUARD_HANDLERS in
// cline-plugins/cline-agents/index.ts and with the Python hook. That is not a
// claim in a comment: the shared fixture drives all three.
var handlers = map[string]handler{
	"reset":    checkReset,
	"checkout": checkCheckout,
	"switch":   checkSwitch,
	"restore":  checkRestore,
	"clean":    checkClean,
	"branch":   checkBranch,
	"push":     checkPush,
	"worktree": checkWorktree,
	"gc":       checkGC,
}
