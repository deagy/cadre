// Package guard decides whether a Bash command would destroy work the agent
// did not create.
//
// It backs a Claude Code PreToolUse hook. The policy it enforces is
// roster/shared/workspace-isolation.md's "never mutate a working tree you did
// not create" and agent-autonomy.yaml's
// repository.discard_uncommitted_work_or_move_branches: never. Prompt-level
// policy alone failed three times before this existed, including a `git reset
// --hard main` that discarded an unpushed commit while truthfully reporting
// "no edits made" -- the hook never touched a file, only the branch pointer.
//
// False positives are the real risk, not false negatives. This is
// defence-in-depth on top of policy text, not the only control, so it FAILS
// OPEN: anything it cannot parse confidently, any git state it cannot resolve,
// and any command it does not recognise as destructive is allowed. A guard
// that blocks routine work gets disabled by its users and then protects
// nothing.
//
// Ported from .claude/hooks/guard_workspace_mutation.py. A third
// implementation of the same rules lives in cline-plugins/cline-agents/
// index.ts; all three are pinned to the same outcomes by the shared fixture in
// plugin/tools/guard_parity_fixture.json.
package guard

import "strings"

// Decision is a refusal. A nil *Decision means no opinion, which is how this
// package says "allow" -- silence is what lets any other configured hook or
// the normal permission flow decide.
type Decision struct {
	// Reason reaches the model, so it says what was refused and what to do
	// instead rather than only that something was refused.
	Reason string
}

// maxShellRecursionDepth bounds `bash -c "..."` indirection. Beyond it a
// nested script is left alone rather than being misread as a git invocation.
const maxShellRecursionDepth = 3

// EvaluateCommand returns a refusal, or nil to express no opinion.
//
// Never panics by contract: an unparseable segment is skipped rather than
// treated as destructive.
func EvaluateCommand(command, baseCwd string) *Decision {
	return evaluateCommand(command, baseCwd, 0)
}

func evaluateCommand(command, baseCwd string, depth int) *Decision {
	for _, segmentText := range splitTopLevel(command) {
		tokens, err := splitWords(segmentText)
		if err != nil {
			continue // unbalanced quoting or similar; skip, don't guess
		}
		tokens = stripLeadingWrappers(tokens)
		if len(tokens) == 0 {
			continue
		}

		if script, isShell := findShellDashCScript(tokens); isShell {
			if depth < maxShellRecursionDepth {
				if nested := evaluateCommand(script, baseCwd, depth+1); nested != nil {
					return nested
				}
			}
			// Beyond the bound, deliberately not recursed further. Fall
			// through to the next segment rather than misreading the shell
			// itself as a git invocation.
			continue
		}

		// The segment itself, plus any command `find` carries in argument
		// position (`-exec git ... \;`), which prefix stripping cannot reach.
		candidates := [][]string{tokens}
		for _, body := range findCommandInvocations(tokens) {
			candidates = append(candidates, stripLeadingWrappers(body))
		}
		for _, candidate := range candidates {
			if decision := evaluateGitTokens(candidate, baseCwd, depth); decision != nil {
				return decision
			}
		}
	}
	return nil
}

// evaluateGitTokens parses one already-wrapper-stripped token list as a git
// invocation and runs its handler, expanding a command-line-defined alias
// first.
func evaluateGitTokens(tokens []string, baseCwd string, depth int) *Decision {
	parsed, ok := parseGitInvocation(tokens)
	if !ok {
		return nil
	}
	expanded, script, isShellAlias := expandGitAlias(parsed)
	if isShellAlias {
		// A `!shell` alias: hand the expansion to the same bounded recursion
		// that `bash -c "..."` uses rather than ignoring it.
		if depth < maxShellRecursionDepth {
			return evaluateCommand(script, baseCwd, depth+1)
		}
		return nil
	}
	run, known := handlers[expanded.subcommand]
	if !known {
		return nil
	}
	return run(expanded.args, resolveCwd(baseCwd, expanded.explicitCwd), expanded.config)
}

// maxAliasExpansionDepth bounds how many alias definitions to follow. Git
// itself detects alias loops, which the seen set below mirrors; the numeric
// bound is a second, cheaper backstop so a pathological chain can never turn
// this hook into a long walk.
const maxAliasExpansionDepth = 5

// expandGitAlias resolves a subcommand naming an alias defined by `-c` on the
// same command line.
//
// The config is carried forward, not just consumed, because a definition may
// set config the HANDLER needs rather than only config this function needs.
// Dropping it meant `git -c alias.g='-c gc.worktreePruneExpire=now gc' g` was
// allowed while real git pruned -- checkGC is the one handler that reads
// config, so the guard has to resolve the same expiry the real invocation
// will use.
//
// This closes the COMMAND-LINE alias spelling only. The config-file alias gap
// stays open: resolving one means reading and trusting the invoking user's git
// config, whereas `-c alias.x=...` is already in the tokens handed to the hook.
//
// An alias cannot shadow a real subcommand -- git runs the real one -- which
// is why a subcommand already in handlers is never expanded. Alias names match
// case-insensitively.
func expandGitAlias(parsed gitInvocation) (gitInvocation, string, bool) {
	seen := map[string]bool{}
	for i := 0; i < maxAliasExpansionDepth; i++ {
		if _, real := handlers[parsed.subcommand]; real {
			break // a real subcommand: git ignores any alias of that name
		}
		key := "alias." + strings.ToLower(parsed.subcommand)
		if seen[key] {
			break // alias loop, as git itself reports
		}
		definition, defined := parsed.config[key]
		if !defined {
			break
		}
		seen[key] = true
		if strings.HasPrefix(definition, "!") {
			words := []string{strings.TrimSpace(definition[1:])}
			for _, argument := range parsed.args {
				words = append(words, shellQuoteWord(argument))
			}
			return parsed, strings.TrimSpace(strings.Join(words, " ")), true
		}
		parts, err := splitWords(definition)
		if err != nil {
			break // unbalanced quoting in the definition; don't guess
		}
		if len(parts) == 0 {
			break
		}
		rebuilt := append([]string{"git"}, parts...)
		rebuilt = append(rebuilt, parsed.args...)
		reparsed, ok := parseGitInvocation(rebuilt)
		if !ok {
			break
		}
		// A definition may carry global flags of its own -- `git <definition>
		// <args>` is literally what git runs -- so fold them in.
		merged := parsed.config
		if len(reparsed.config) > 0 {
			merged = make(map[string]string, len(parsed.config)+len(reparsed.config))
			for name, value := range parsed.config {
				merged[name] = value
			}
			for name, value := range reparsed.config {
				merged[name] = value
			}
		}
		explicitCwd := parsed.explicitCwd
		if reparsed.explicitCwd != "" {
			explicitCwd = accumulateDashC(explicitCwd, reparsed.explicitCwd)
		}
		parsed = gitInvocation{
			subcommand:  reparsed.subcommand,
			args:        reparsed.args,
			explicitCwd: explicitCwd,
			config:      merged,
		}
	}
	return parsed, "", false
}
