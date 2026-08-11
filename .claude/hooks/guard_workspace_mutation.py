#!/usr/bin/env python3
"""PreToolUse hook: refuse destructive `git` invocations against Bash calls.

Tracks deagy/cadre#129 (structural enforcement for the "never mutate a
working tree you did not create" policy in
`roster/shared/workspace-isolation.md` and the
`repository.discard_uncommitted_work_or_move_branches: never` rule in
`roster/shared/agent-autonomy.yaml`). Prompt-level policy alone has already
failed twice more in one session (an orchestrator running
`git checkout <branch> -- <files>` against its own dirty tree, and another
switching a different session's worktree off its branch) on top of the
original #128/#129 incident (`git reset --hard main`, discarding an unpushed
commit while truthfully reporting "no edits made" -- the hook never touched
a file, only the branch pointer).

Contract verified against the Claude Code "Hooks reference" doc
(https://code.claude.com/docs/en/hooks, "PreToolUse" and "PreToolUse
decision control" sections) on 2026-08-09:

  * `PreToolUse` receives JSON on stdin with (at least) `hook_event_name`,
    `tool_name`, `tool_input`, `cwd`. For the `Bash` tool, `tool_input` is
    `{"command": "...", ...}`.
  * A `PreToolUse` hook communicates its decision through
    `hookSpecificOutput` on stdout while exiting 0 -- NOT via exit code 2.
    (Exit code 2 is also a valid "deny" signal per the docs, but the
    structured JSON form is documented as the way to attach a
    `permissionDecisionReason` that reaches the model, which is required
    here per this task's "refuse with a reason and an alternative" design
    constraint.) The shape used below:

        {
          "hookSpecificOutput": {
            "hookEventName": "PreToolUse",
            "permissionDecision": "deny",
            "permissionDecisionReason": "<why, and what to do instead>"
          }
        }

  * Precedence when multiple hooks disagree is deny > defer > ask > allow,
    so this hook only ever needs to speak up when it wants to deny; silence
    (exit 0, no stdout) means "no opinion," which lets any other configured
    hook or the normal permission flow decide.

Design stance -- false positives are the real risk here, not false
negatives. This hook is defense-in-depth on top of policy text and
`agent-autonomy.yaml` (`repository.push: on_request`,
`repository.discard_uncommitted_work_or_move_branches: never`), not the
only control, so it fails OPEN (allows) whenever it cannot parse the
command confidently, cannot resolve `git` state (not a repo, `git`
missing, a ref that doesn't resolve), or the command doesn't match a
pattern it recognizes as destructive. A guard that blocks routine work
gets disabled by its own users and then protects nothing; a guard that
occasionally lets one destructive command through when its state-checks
themselves are uncertain still leaves every other structural and
policy-level control in place.

What is treated as destructive (state-dependent, not spelling-dependent
where practical -- see `git status --porcelain`/`git clean -n` use below):

  * `git reset --hard [<ref>]` when the working tree has uncommitted
    changes, OR when `<ref>` resolves to a commit other than current HEAD
    (moving the branch pointer, which is exactly what stranded the unpushed
    commit in the original incident even though no *file* was touched).
    Plain `git reset` / `--soft` / `--mixed` are never blocked: they never
    discard working-tree content, only move HEAD/the index, and the moved
    commit stays reachable for recovery.

  * `git checkout <ref> -- <path>...` / `git checkout <ref> <path>...` and
    `git restore --source=<ref> <path>...` -- pulling content from another
    ref into paths that currently have uncommitted changes. This is the
    exact shape of both today's orchestrator incident and the incident
    that overwrote a teammate's worktree. Blocked only when
    `git status --porcelain -- <path>...` shows those specific paths are
    dirty; a checkout of clean paths from another ref discards nothing.
    The no-ref forms -- `git checkout -- <path>` / `git checkout <path>`
    (when `<path>` isn't itself a local branch name) / `git restore
    <path>` with no `--source` -- are the routine "discard my own edit"
    idiom (restoring from the index/HEAD, not from an unrelated ref) and
    are always allowed, per this task's explicit "git restore of your own
    edits" example.

  * `git checkout <branch>` / `git switch <branch>` (switching branches, no
    branch-creating flag, no `--`, and `<branch>` really is a local branch
    per `git show-ref`) when the tree is dirty. Git itself refuses when the
    switch would conflict with modified files, but a switch that doesn't
    conflict still carries or strands uncommitted edits across branches and
    is exactly the "switched another session's worktree off its branch"
    shape from today. A clean tree switch is always allowed. `switch` is
    covered for the same reason as `checkout` and not as an extension of
    scope: guarding only one spelling of one operation makes the guard
    bypassable by typing the other, and `workspace-isolation.md` lists the
    two side by side.

  * `git checkout -B <branch>` and `git switch -C <branch>` (equivalently
    `--force-create`) when `<branch>` already exists as a local branch AND
    resolves to a commit other than the resolved start point -- the same
    check `check_worktree` applies to `git worktree add -B`, and closed
    here under deagy/cadre#221. `-b`/`-c` (plain create) is always allowed
    and is genuinely safe: git refuses it when the branch already exists.
    `-B`/`-C` is not. `git checkout -B feature` with the IMPLICIT HEAD
    start point is precisely the destructive case when `feature` already
    exists and points elsewhere: it moves the branch off its commits,
    reporting only "Switched to and reset branch 'feature'". An implicit
    start point makes the check easier to perform, not the operation safer
    -- an earlier version of this paragraph argued those mechanics
    backwards, and #215 deferred the fix as a scope decision rather than a
    safety claim.
      All of git's flag-value spellings are covered, each verified against
    git 2.53.0 to really move the branch: `-B existing`, the attached
    `-Bexisting`, and the combined group `-fB existing`. The combined form
    needs git's own parse-options rule, not a substring test:
    `git checkout -Bf existing` creates a branch literally named `f` and
    treats `existing` as the START POINT, because `B` is not the group's
    last character and so takes the rest of the group as its value. See
    `flag_value`.

  * `git clean -f`/`-fd`/`-fdx` (any force-clean), but only when a dry run
    (`git clean -n[dx]`, run automatically before deciding) shows it would
    actually remove something. `git clean` without `-f` already refuses to
    delete anything on its own; an explicit `-n`/`--dry-run` is inherently
    non-destructive and always allowed.

  * `git branch -D <name>` (or `--delete --force` / `-d -f`) -- skips
    git's own unmerged-work safety check. Plain `-d`/`--delete` is left
    alone: git already refuses it when the branch has unmerged commits, so
    the tool's own safety net is intact.

  * `git push --force`/`-f` without `--force-with-lease`, and any remote
    branch deletion (`git push <remote> --delete <branch>` or the
    `<remote> :<branch>` colon-refspec form) -- both discard state that
    isn't local and can't be recovered from this working tree at all.
    `--force-with-lease` is always allowed: it is git's own safer
    alternative, refusing itself when the remote has moved since the last
    fetch.

  * `git worktree remove [--force] <worktree>` and `git worktree move
    <source> <destination>` -- unconditionally, tracks deagy/cadre#215.
    `workspace-isolation.md`'s "Never remove or prune a worktree yourself"
    is absolute: it covers every worktree, including an inspection worktree
    the agent created itself, because a worktree that holds work IS the
    deliverable location until a human decides otherwise, and deregistering
    one is a destructive git-metadata operation
    (`destructive_action: human_approval`). Unlike `reset --hard` there is
    no state in which policy permits either verb and no non-destructive
    variant to steer toward, so no state check is performed: adding one
    would buy no safety and would only add a bypass surface. In
    particular, no attempt is made to match the target against
    `git worktree list` -- verified against git 2.53.0 that
    `git worktree remove` accepts a bare basename (`git worktree remove
    wt2`) as well as a path, so path matching would miss that spelling
    while a flat refusal does not. Blocking a `remove` whose target isn't
    a registered worktree costs nothing either: git itself exits 128
    ("is not a working tree") on that input. `move` is included because it
    silently rewrites another session's registration -- an agent whose cwd
    is the old path loses its tree mid-task with no error at the moment of
    the move.

  * `git worktree prune` -- but only when a dry run (`git worktree prune
    -n -v`, run automatically before deciding, with any caller-supplied
    `--expire` passed through) shows it would actually deregister
    something. This is the same shape as the `git clean -n` check above,
    with one behavioral difference confirmed against git 2.53.0: prune's
    dry-run report goes to STDERR, not stdout, so both streams are
    considered. `prune` is the sharper of the two removal verbs precisely
    because it names no target -- it deregisters whatever git currently
    considers unreachable, which can include a teammate's worktree sitting
    on a momentarily unavailable path. "Is this my worktree?" is therefore
    not answerable from the command line at all.
      The stricter alternative considered and rejected: refuse `prune`
    whenever any worktree this session did not create is registered. That
    over-blocks the case where nothing is prunable (a no-op refusal is
    pure friction, and friction is what gets a guard disabled) while
    gaining nothing -- a prune that would remove nothing removes nothing.
    The dry run covers the dangerous case exactly, including the
    unavailable-path teammate case, which is precisely when prune finds
    something prunable. An explicit `-n`/`--dry-run` from the caller is
    inherently non-destructive and always allowed.
      Known race, accepted: the dry run and the real command are separate
    invocations, so a worktree path that becomes unavailable in between is
    reported as nothing-to-prune and allowed through. Consistent with this
    module's fail-open stance; it is defense-in-depth, not the only
    control.

  * `git worktree add -B <branch> ...` when `<branch>` already exists as a
    local branch AND resolves to a commit other than the resolved start
    point. This is the one guarded spelling of an otherwise explicitly
    allowed verb (`agent-autonomy.yaml`'s
    `repository.create_local_branch_or_worktree: allowed`), and it is
    guarded because `-B` force-resets the branch: verified against git
    2.53.0 that `git worktree add -B existing <path>` moved `existing`
    from 523bf6b to 23a851c, reporting it only as "Preparing worktree
    (resetting branch 'existing'; was at 523bf6b)". That is exactly
    `agent-autonomy.yaml`'s `discard_uncommitted_work_or_move_branches:
    never`, and `workspace-isolation.md` names the flag
    ("Never `-B` (force-create/reset the branch)"). Plain `-b` is always
    allowed -- git itself refuses when the branch already exists -- and so
    is `-B` naming a branch that does not exist yet or that already points
    at the start point, since neither moves anything.
      The asymmetry with `check_checkout` that #215 left in place is gone:
    `checkout -B` and `switch -C` are now checked the same way, under
    deagy/cadre#221 (see their bullet above). Those two share
    `_check_force_created_branch`; this branch keeps its own copy of the
    same logic only so it can give worktree-specific guidance ("use
    `git worktree add -b <new-branch>`") in the refusal, and it reads the
    start point from positional[1] rather than [0] because `add`'s first
    positional is the new worktree's path. It does use the same
    `flag_value`/`positionals` helpers, so every flag spelling -- including
    the combined `-fB` group -- resolves identically in all three.

  * `git gc` when its own worktree pruning would deregister a
    registration -- deagy/cadre#217, scoped to worktree registrations only.
    `gc` reaches the exact state `check_worktree`'s `prune` refusal exists
    to protect, through a subcommand that names no worktree, so it is
    checked the same way: a dry run decides, and a `gc` that would
    deregister nothing is a no-op worth no friction. Which dry run is not
    obvious, and the issue's framing was wrong about it -- see `check_gc`
    for the probe transcript. Briefly: gc's own `--prune=<date>` governs
    loose-OBJECT pruning and does not touch worktree registrations at all
    (`git gc --prune=now` left a just-moved worktree registered), while
    `gc.worktreePruneExpire` (default `3.months.ago`) does, so the probe
    runs at THAT expiry rather than at `worktree prune`'s own immediate
    default.

Deliberately NOT covered, with reasoning:

  * Deleting a worktree's directory directly (`rm -rf .worktrees/foo`)
    rather than through `git worktree remove`. **This rule is prompt-only
    for `rm`, deliberately and permanently as far as this hook is
    concerned.** `workspace-isolation.md` forbids it in the same sentence
    as the git verbs, but this hook only inspects `git` invocations -- an
    `rm` is not a git subcommand and `_HANDLERS` never sees it. Deciding
    whether an arbitrary `rm` target is a registered worktree, for every
    `rm` the model runs, is a much broader policy question than workspace
    isolation, and deagy/cadre#217 re-examined it while closing the sibling
    `git gc` path and concluded the same: a guard that tries and
    half-succeeds is worse than one that declares the boundary. It is also
    the most likely real-world bypass of the never-remove rule, which is
    why it is stated plainly here and in `workspace-isolation.md` rather
    than buried. Pinned by
    `test_rm_of_a_worktree_directory_is_a_documented_gap`.
  * `git worktree unlock` (which makes a locked worktree prunable again)
    and `git worktree repair` (which rewrites registration metadata).
    Neither deregisters or relocates a worktree on its own; each would
    need its own state-check design to distinguish routine repair from
    setup for a later removal. Left as known gaps.

  * `git stash drop`/`git stash clear` -- also destructive to uncommitted
    work, but out of the explicit "dangerous cases" list in the task brief
    and structurally different (stash entries, not the tracked working
    tree/branch state the incidents above involved). Left as a known gap
    rather than silently folded in without its own state-check design.
  * Reflog expiry (`git reflog expire`) and `git gc`'s OBJECT-pruning
    surface (`--prune=now`/`--prune=all`) -- these destroy unreachable
    commits, are not operations any workflow here performs routinely, and
    detecting "would this prune something otherwise recoverable" reliably
    is materially harder than the checks above. `check_gc` is scoped to
    worktree registrations and deliberately says nothing about this;
    verified against git 2.53.0 that the two surfaces really are separate
    (`gc --prune=now` did not deregister a prunable worktree). Left as a
    known gap, pinned by
    `test_gc_destructive_object_surface_is_a_documented_gap`.
  * Anything reached through a file the model writes and then executes
    (e.g. a shell script containing `git reset --hard`) rather than a
    literal `Bash` tool command -- `PreToolUse` only sees the `Bash`
    command string itself, so an indirection through a written script is
    invisible to this hook by construction, not by choice. (`bash -c
    "..."`/`sh -c "..."` inline strings ARE recursed into, see below --
    this gap is specifically about content that only exists as a file on
    disk, not an inline string in the `Bash` command itself.)
  * `--git-dir`/`--work-tree`/`GIT_DIR`/`GIT_WORK_TREE` redirection to a
    different repository than `base_cwd`. `-C <dir>` IS honored, and
    correctly: repeated `-C` accumulates in order exactly as git applies it,
    with an absolute value resetting the accumulation (deagy/cadre#220, see
    `accumulate_dash_c` -- the previous last-wins reading resolved
    `git -C .worktrees -C ../ worktree prune` to the wrong directory and
    failed every state-probing handler open). The `--git-dir`/`--work-tree`
    global flags are parsed and skipped so the guard can still recognize the
    subcommand, but this hook's own state-check subprocess calls
    (`git_status_porcelain`, `is_local_branch`, the `rev-parse` calls in
    `check_reset`) always run against `base_cwd`/the `-C` value resolved by
    `resolve_cwd` -- they do not additionally honor a `--git-dir`/
    `--work-tree` flag pair or `GIT_DIR`/`GIT_WORK_TREE` environment
    variables also present on the command line. A command that sets those
    to point the *actual* git invocation at a different repository than
    the one this hook checks can cause the hook to confidently report the
    wrong tree as clean while the real destructive command runs elsewhere
    against a dirty one. Flagged here rather than fixed: reliably
    resolving the effective repository/worktree for an arbitrary
    combination of `-C`, `--git-dir`, `--work-tree`, and their environment
    variable equivalents (which can also appear as a leading
    `VAR=value ...` assignment on the same command line) is materially
    harder than the checks above, and this hook's own author assessed a
    full fix as out of scope for this task.
  * CONFIG-FILE git alias resolution (a user-configured `git co` alias for
    `checkout`, or a `!shell` alias, defined in `.git/config` or
    `~/.gitconfig`). This hook only recognizes git's own literal subcommand
    names in `_HANDLERS`; an aliased spelling of a destructive subcommand is
    invisible to it, and resolving one would require reading and trusting
    the invoking user's git config. Left as a known gap, pinned by
    `test_config_file_alias_remains_a_documented_gap`.
      That justification never extended to the `-c` spelling, and saying so
    was the gap's own blind spot: `git -c alias.wtr='worktree remove' wtr
    <path>` defines the alias INSIDE the command line this hook has already
    tokenized, so no config file needs to be read or trusted to see it.
    That spelling is now CLOSED (deagy/cadre#218): `parse_git_invocation`
    records `-c <name>=<value>` pairs and `expand_git_alias` resolves a
    matching subcommand, including chained aliases, `-c` interleaved with
    other globals, and the `!shell` form, which is fed through the same
    bounded recursion `bash -c "..."` uses. See `expand_git_alias` for the
    git behaviours that were verified rather than assumed -- in particular
    that an alias can NOT shadow a real subcommand, which is why one
    already in `_HANDLERS` is never expanded.

  * Wrapper commands outside `_WRAPPER_TOKENS`. The set was extended under
    deagy/cadre#219 (`timeout`, `nice`, `ionice`, `stdbuf`, `setsid`,
    `chrt`, `taskset`, `xargs`, each with its own flag arity, plus `find
    -exec`/`-execdir`/`-ok`/`-okdir` handled separately because they take
    the command in ARGUMENT position), but it is still NOT exhaustive and is
    not claimed to be: anything else leading the line leaves `tokens[0]` as
    a non-`git` program, so `parse_git_invocation` returns None and the
    wrapped command is allowed. `firejail`, `runuser`, `doas`, and
    `unbuffer` are examples that remain uncovered; pinned by
    `test_wrapper_set_remains_non_exhaustive`.
      The alternative #219 raised -- inverting the design to scan ALL
    tokens for a `git` invocation instead of stripping known prefixes --
    was considered and rejected. It is more robust against unknown
    wrappers, but it reintroduces exactly the false-positive class the
    heredoc handling above exists to prevent: a literal `git worktree
    remove` appearing as DATA (in a heredoc body, a quoted string, a grep
    pattern, this very docstring) would start matching. Given this module's
    stance that false positives are the real risk, enumerating and being
    honest about the remainder beats matching more and being wrong
    sometimes. `find -exec` is the deliberate exception: its command is in
    a syntactically unambiguous position, delimited by `;` or `+`, so it
    can be extracted without guessing.

  * `git worktree add --force <path>` (and `-f -f`) pointed at a path a
    registered-but-currently-missing worktree occupies. Verified against
    git 2.53.0: plain `add` refuses this outright ("fatal: '<path>' is a
    missing but already registered worktree; use 'add -f' to override, or
    'prune' or 'remove' to clear"), `--force` overrides it and silently
    re-registers the path onto the new branch (the registration flipped
    from `[victim]` to `[intruder2]` in the probe), and `-f -f` does the
    same even when the original worktree is LOCKED ("fatal: ... is a
    missing but locked worktree; use 'add -f -f' to override"). That is
    the same hazard `check_worktree`'s own `prune` refusal describes --
    losing a teammate's registration on a momentarily unavailable path --
    reached through the one verb otherwise treated as safe. It is not
    guarded because a correct check means resolving the target path
    against `git worktree list` and so re-introduces the path-vs-basename
    matching problem that `remove`'s flat refusal deliberately avoids;
    getting that half-right would be worse than recording it. Pinned by
    `test_worktree_add_force_over_a_registered_path_is_a_documented_gap`.

Parity with the Cline guard (`cline-plugins/cline-agents/index.ts`): the
two files implement the same guard for two runners and every behavioural
change must land in both, in the same change. That used to be asserted in a
comment -- prose about intent, not a check -- which is the failure
`roster/shared/operating-principles.md` names directly. Since
deagy/cadre#222 it is checked: `plugin/tools/test_guard_parity.py` parses
both files and compares their handler tables, wrapper sets, global-flag
sets, and recursion bounds, and drives
`plugin/tools/guard_parity_fixture.json` -- a table of (command, repository
state, expected decision) cases -- through BOTH implementations against the
same prepared repositories, asserting they agree with each other and with
the declared expectation. Structure alone would miss a `split("=", 2)` that
truncates in JS where Python's `split("=", 1)` does not, a `??` where the
other file means `or`, or a missing bounds check; behaviour alone would
miss a handler present in both files but never reached. Hence both.

Every empirical claim in this docstring was verified by execution against
**git 2.53.0** on the machine that made the change, not recalled. Where a
verified result contradicted the issue that prompted the work -- notably
`git gc --prune=now`, which does NOT prune worktree registrations -- the
code follows the observation and the contradiction is recorded next to it.

Opt-out (env var, not a command-line flag): if `CADRE_DISABLE_WORKSPACE_
MUTATION_GUARD` is set to `1` or `true` (case-insensitive) in the
environment this hook's Python process itself runs in, `main()` allows the
command immediately, before any parsing or git state checks -- this is
checked first, ahead of even reading stdin. This exists so the default-on
posture (this hook ships enabled by default when packaged into the `cadre`
plugin, see `generate_global_plugin.py`'s `GUARD_HOOK_SOURCE`/
`MAIN_PLUGIN_HOOKS`) has a documented, narrowly-scoped way for someone who
has deliberately decided they don't want it to turn it off, without editing
this file or their `.claude/settings.json` hook wiring. The variable name
and behavior are deliberately not referenced anywhere in the generated
`hooks/hooks.json`/`plugin.json` output -- only in this script -- so that
regenerating the plugin (`cadre generate-plugin`) can never silently
re-enable the guard for someone who set this in their own environment; the
opt-out lives entirely in the one file that is copied byte-for-byte into
the package, not in anything the generator computes or overwrites.
"""

from __future__ import annotations

import json
import os
import re
import shlex
import subprocess
import sys
from typing import Iterable, Optional

# ---------------------------------------------------------------------------
# Shell-line splitting (best-effort, not a full shell grammar)
# ---------------------------------------------------------------------------


# The delimiter of a heredoc redirection, matched at the position just past
# the `<<`: an optional `-` (the tab-stripping form), optional whitespace,
# then a quoted or bare word. Applied only where the scanner has already
# established that the `<<` is a real redirection -- outside quotes, outside
# arithmetic expansion, and not part of a `<<<` here-string. That context is
# NOT re-derivable from segment text after the fact, which is why detection
# happens during the scan rather than by searching finished segments.
_HEREDOC_DELIMITER_RE = re.compile(
    r"(-?)[ \t]*(?:'([^']*)'|\"([^\"]*)\"|([A-Za-z0-9_.\-]+))"
)


def _join_line_continuations(command: str) -> str:
    """Remove backslash-newline line continuations, as the shell does.

    `git push \\<newline> origin main --force` is one command, not two.
    Without this the newline splitting below turns it into `git push \\`
    (no `--force`, allowed) and `origin main --force` (not a git
    invocation at all, ignored), so a force-push sails through a guard
    that catches the single-line spelling. Long commands are written this
    way routinely; this is not a corner case.

    Quote-aware, because the shell is: inside SINGLE quotes a
    backslash-newline is literal text and must be preserved, while
    unquoted and inside double quotes it is a continuation and is removed.
    Handles CRLF. A backslash that is itself escaped (`\\\\<newline>`
    inside double quotes) is a known edge this does not model.
    """
    out: list[str] = []
    quote: Optional[str] = None
    i = 0
    n = len(command)
    while i < n:
        ch = command[i]
        # Single quotes first: inside them a backslash is literal, so the
        # continuation branch below must not see it.
        if quote == "'":
            out.append(ch)
            if ch == "'":
                quote = None
            i += 1
            continue
        if ch == "\\" and command[i + 1 : i + 2] == "\n":
            i += 2
            continue
        if ch == "\\" and command[i + 1 : i + 3] == "\r\n":
            i += 3
            continue
        if quote == '"':
            out.append(ch)
            if ch == '"' and (i == 0 or command[i - 1] != "\\"):
                quote = None
            i += 1
            continue
        if ch in ("'", '"'):
            quote = ch
            out.append(ch)
            i += 1
            continue
        out.append(ch)
        i += 1
    return "".join(out)


def _scan_segments(command: str) -> list[dict]:
    """Split into segments, recording for each one what the shell knew at
    the time and this module used to throw away:

      * `raw` -- the segment text, NOT stripped. Leading/trailing
        whitespace is load-bearing for heredoc terminator matching (a
        terminator line must be exactly the delimiter, unless the `<<-`
        form allows leading tabs), so stripping happens only on the way
        out of `split_top_level`.
      * `newline_before` -- whether this segment began a new LINE, rather
        than following `&&`/`||`/`;`/`|` on the previous one. A heredoc
        body starts on the next line, so a command chained onto the
        opener's own line is a command, not body.
      * `heredocs` -- delimiters opened by this segment, in order, each
        recorded only when the `<<` was seen outside quote state and
        outside arithmetic expansion.

    Discarding those three facts and re-deriving them from segment text is
    what produced findings F7 (`cat > f <<EOF && git ...` swallowed the
    chained command), F8 (`echo "see <<EOF"; git ...` treated a quoted
    mention as a real redirection) and the `$(( x << 2 ))` shift case.
    """
    segments: list[dict] = []
    buf: list[str] = []
    heredocs: list[tuple[str, bool]] = []
    quote: Optional[str] = None
    arithmetic_depth = 0
    newline_before = False
    i = 0
    n = len(command)

    def flush(next_starts_a_line: bool) -> None:
        nonlocal buf, heredocs, newline_before
        segments.append(
            {"raw": "".join(buf), "newline_before": newline_before, "heredocs": heredocs}
        )
        buf = []
        heredocs = []
        newline_before = next_starts_a_line

    while i < n:
        ch = command[i]
        if quote:
            buf.append(ch)
            if ch == quote and (i == 0 or command[i - 1] != "\\"):
                quote = None
            i += 1
            continue
        # An unquoted backslash escapes the next character, so `\;` is a
        # LITERAL semicolon and not a command separator. Without this,
        # `find . -exec git worktree remove {} \;` -- the ordinary spelling
        # of `find -exec`, since the shell would otherwise eat the `;` --
        # split at the `;` into a segment ending in a dangling backslash,
        # and the `git` invocation inside it was never evaluated as one.
        # Backslash-NEWLINE is already gone by this point
        # (`_join_line_continuations`), so the only escapes reaching here
        # are the ordinary in-line ones.
        if ch == "\\" and i + 1 < n:
            buf.append(ch)
            buf.append(command[i + 1])
            i += 2
            continue
        if ch in ("'", '"'):
            quote = ch
            buf.append(ch)
            i += 1
            continue
        # `$(( ... ))`: `<<` in here is a left-shift operator, not a
        # redirection. Other arithmetic contexts (a bare `(( ))` command,
        # `let`) are not modelled -- a known limit, in the fail-open
        # direction only.
        if command[i : i + 3] == "$((":
            arithmetic_depth += 1
            buf.append("$((")
            i += 3
            continue
        if arithmetic_depth and command[i : i + 2] == "))":
            arithmetic_depth -= 1
            buf.append("))")
            i += 2
            continue
        if command[i : i + 2] in ("&&", "||"):
            flush(False)
            i += 2
            continue
        if ch in (";", "|"):
            flush(False)
            i += 1
            continue
        if ch == "\n":
            flush(True)
            i += 1
            continue
        if (
            not arithmetic_depth
            and command[i : i + 2] == "<<"
            and command[i : i + 3] != "<<<"  # here-STRING: no body, no terminator
            and (i == 0 or command[i - 1] != "<")  # not the tail of a `<<<`
        ):
            match = _HEREDOC_DELIMITER_RE.match(command, i + 2)
            delimiter = None
            if match:
                # Explicit first-non-None selection rather than `or`, so
                # this and the TypeScript mirror's `??` mean the same thing
                # for an empty delimiter (`<<''`), which `or` would treat
                # as "no match" and `??` would not.
                for group in (match.group(2), match.group(3), match.group(4)):
                    if group is not None:
                        delimiter = group
                        break
            if delimiter is not None:
                heredocs.append((delimiter, match.group(1) == "-"))
                buf.append(command[i : match.end()])
                i = match.end()
                continue
        buf.append(ch)
        i += 1
    flush(False)
    return segments


def _strip_heredoc_bodies(records: list[dict]) -> list[dict]:
    """Drop heredoc body lines (and their terminator line) from `records`.

    Needed because `split_top_level` splits on newlines: without this, the
    BODY of `cat > note.md <<'EOF' / git reset --hard / EOF` would be
    parsed as if it were a command and blocked, even though it is text
    being written to a file. That would be a false positive of exactly the
    kind this module's design stance treats as the real risk -- writing
    documentation that quotes a destructive command is routine, and in
    this repository especially so.

    Three things are deliberately KEPT, each of which a naive
    consume-forward-to-the-delimiter pass gets wrong (finding F7):

      * the heredoc-opening segment (`cat > note.md`), a real command;
      * every remaining segment on the opener's OWN line -- `cat > f
        <<EOF && git worktree remove <path>` runs that `git` command
        before a single body line is read, so it must still be checked;
      * everything after the terminator line.

    Terminator matching is exact against the unstripped segment text,
    which is what the shell requires: `EOF` terminates, `    EOF` does
    not, and only the `<<-` form accepts leading TABS (not spaces). A
    trailing `\\r` is tolerated so CRLF input behaves the same as LF.

    Best-effort, consistent with the rest of this module: an unterminated
    heredoc swallows the remainder, which is what the shell itself would
    do. A delimiter containing `;`/`|`/`&` (only reachable via a quoted
    spelling like `<<';'`) is not matched, because the terminator line is
    split before this pass sees it -- the heredoc then reads as
    unterminated, i.e. fails open.
    """
    out: list[dict] = []
    i = 0
    while i < len(records):
        record = records[i]
        out.append(record)
        i += 1
        if not record["heredocs"]:
            continue

        # The rest of the opener's own line is commands, not body.
        while i < len(records) and not records[i]["newline_before"]:
            out.append(records[i])
            i += 1

        # Bodies begin on the following line, one per delimiter, in order.
        for delimiter, allows_leading_tabs in record["heredocs"]:
            while i < len(records):
                segment = records[i]
                alone_on_its_line = segment["newline_before"] and (
                    i + 1 >= len(records) or records[i + 1]["newline_before"]
                )
                i += 1
                if not alone_on_its_line:
                    continue
                candidate = segment["raw"].rstrip("\r")
                if allows_leading_tabs:
                    candidate = candidate.lstrip("\t")
                if candidate == delimiter:
                    break
    return out


def split_top_level(command: str) -> list[str]:
    """Split a shell command line into top-level segments on `&&`, `||`,
    `;`, `|`, and NEWLINES, respecting single/double quoting. Not a full
    shell parser -- good enough to find each independent `git ...`
    invocation in a chained command line without being fooled by an
    operator sitting inside a quoted string.

    Newline is a separator for the same reason `;` is: the shell treats
    them identically as command terminators. Omitting it (as this function
    did until deagy/cadre#215) silently defeated EVERY handler, not just
    one -- `shlex.split` treats a newline as ordinary whitespace, so a
    two-line command collapsed into a single token list whose `tokens[0]`
    was the first line's program, `parse_git_invocation` returned None,
    and the destructive second line was never inspected. That needed no
    adversarial intent at all: multi-line `Bash` tool calls are routine,
    and the guard's behavior flipped on a keystroke nobody would think
    about.

    A newline inside quotes is NOT a separator (the scanner's `quote`
    branch consumes it), so a quoted multi-line string stays one segment.
    Backslash-newline continuations are joined first, so a command written
    across several lines -- how long commands are normally written -- is
    still seen as the single invocation it is.
    """
    records = _strip_heredoc_bodies(_scan_segments(_join_line_continuations(command)))
    return [s for s in (record["raw"].strip() for record in records) if s]


_ENV_ASSIGN_RE = re.compile(r"^[A-Za-z_][A-Za-z0-9_]*=")

# Wrapper programs that run another command, mapped to the flags of their
# OWN that consume the following token as a value. Getting an arity wrong
# in the "takes no value" direction makes the loop below stop one token
# early, which fails open (the wrapped `git` is never recognized); the
# opposite error would skip past a real command, which also fails open.
# Both directions therefore only ever lose coverage, never invent a block
# -- consistent with this module's stance.
#
# The set is deliberately NOT exhaustive and cannot be (see the wrapper gap
# in the module docstring): it enumerates the prefix wrappers an agent
# plausibly reaches for. Flag lists were checked against the utilities
# installed on the verification machine (GNU coreutils `timeout`/`nice`/
# `stdbuf`, util-linux `ionice`/`setsid`/`chrt`/`taskset`, findutils
# `xargs`), and every entry was confirmed by execution to actually run a
# following `git` command in the token layout parsed here.
#
# `-i` (xargs) and `-t` (`--track`) style flags whose value is OPTIONAL are
# deliberately absent: an optional-value flag written bare must not eat the
# next token.
_WRAPPER_FLAGS_WITH_VALUE = {
    "sudo": {
        "-u", "--user", "-g", "--group", "-p", "--prompt", "-C", "--close-from",
        "-h", "--host", "-r", "--role", "-t", "--type", "-U", "--other-user",
        "-T", "--command-timeout", "-D", "--chdir", "-R", "--chroot",
    },
    "command": set(),
    "exec": set(),
    "nohup": set(),
    "time": set(),
    # `env -u NAME` (`--unset`), `env -C DIR` (`--chdir`), `env -S STRING`
    # (`--split-string`). Missing one lets the flag's VALUE be mistaken for
    # the start of the real command.
    "env": {"-u", "--unset", "-C", "--chdir", "-S", "--split-string"},
    "timeout": {"-s", "--signal", "-k", "--kill-after"},
    "nice": {"-n", "--adjustment"},
    "ionice": {"-c", "--class", "-n", "--classdata", "-p", "--pid", "-P", "--pgid", "-u", "--uid"},
    "stdbuf": {"-i", "--input", "-o", "--output", "-e", "--error"},
    "setsid": set(),
    "chrt": {"-p", "--pid", "-T", "--sched-runtime", "-P", "--sched-period", "-D", "--sched-deadline"},
    "taskset": {"-c", "--cpu-list", "-p", "--pid"},
    "xargs": {
        "-I", "--replace", "-L", "--max-lines", "-n", "--max-args",
        "-P", "--max-procs", "-s", "--max-chars", "-d", "--delimiter",
        "-E", "--eof", "-a", "--arg-file", "--process-slot-var",
    },
}
_WRAPPER_TOKENS = set(_WRAPPER_FLAGS_WITH_VALUE)

# Wrappers that take a mandatory POSITIONAL of their own before the command:
# `timeout <duration> <cmd>`, `chrt <priority> <cmd>`, `taskset <mask> <cmd>`.
# Skipped lazily -- only while the current token is not `git` -- because
# `taskset -c 0,1 git ...` supplies the same value through a flag and then
# has no positional left, so an unconditional skip would step over `git`
# itself. This is a bounded look at a wrapper's own argument slots, not the
# scan-every-token inversion the module docstring rejects.
_WRAPPER_LEADING_POSITIONALS = {"timeout": 1, "chrt": 1, "taskset": 1}

# Wrappers that accept `VAR=value` pairs before the command they run.
_WRAPPER_TAKES_ENV_ASSIGNMENTS = {"env", "sudo"}

_GIT_GLOBAL_FLAGS_WITH_VALUE = {"-C", "--git-dir", "--work-tree", "--namespace", "-c"}


def strip_leading_wrappers(tokens: list[str]) -> list[str]:
    """Strip leading `VAR=value` environment assignments and wrapper
    commands (`sudo`, `env`, `timeout`, `xargs`, ...) so the real command is
    exposed at `tokens[0]`.

    Every wrapper is skipped along with its own flags, flag values, and
    (for `timeout`/`chrt`/`taskset`) its own leading positional, because a
    bare "skip one token" rule leaves those trailing behind and stops
    `parse_git_invocation` from ever recognizing `git` at `tokens[0]` --
    which is exactly how `timeout 10 git worktree remove <path>` walked
    through this guard before deagy/cadre#219.

    Wrappers nest, so the outer loop re-runs against whatever the previous
    wrapper left behind: `sudo timeout 10 git ...` strips both.
    """
    i = 0
    while i < len(tokens):
        t = tokens[i]
        if _ENV_ASSIGN_RE.match(t):
            i += 1
            continue
        if t not in _WRAPPER_TOKENS:
            break
        flags_with_value = _WRAPPER_FLAGS_WITH_VALUE[t]
        takes_assignments = t in _WRAPPER_TAKES_ENV_ASSIGNMENTS
        positionals_left = _WRAPPER_LEADING_POSITIONALS.get(t, 0)
        i += 1
        while i < len(tokens):
            nt = tokens[i]
            if nt == "--":
                i += 1
                break
            if nt in flags_with_value:
                i += 2
                continue
            if nt.startswith("-") and nt != "-":
                i += 1
                continue
            if takes_assignments and _ENV_ASSIGN_RE.match(nt):
                i += 1
                continue
            if positionals_left and nt != "git":
                positionals_left -= 1
                i += 1
                continue
            break
    return tokens[i:]


# `find`'s command-carrying primaries. Unlike everything in
# `_WRAPPER_TOKENS` these take the command in ARGUMENT position, terminated
# by `;` or `+`, so prefix stripping cannot reach them: the invocation sits
# in the middle of `find`'s own expression. Verified by execution that
# `find . -maxdepth 0 -exec git rev-parse --abbrev-ref HEAD \;` runs git.
_FIND_COMMAND_PRIMARIES = {"-exec", "-execdir", "-ok", "-okdir"}
_FIND_COMMAND_TERMINATORS = {";", "+"}


def find_command_invocations(tokens: list[str]) -> list[list[str]]:
    """Command token lists carried in `find ... -exec <cmd> ... ;` position.

    Returns one list per `-exec`/`-execdir`/`-ok`/`-okdir` primary (a single
    `find` can carry several). Returns `[]` for anything that is not a
    `find` invocation, so callers can concatenate unconditionally.
    """
    if not tokens or tokens[0] != "find":
        return []
    found: list[list[str]] = []
    i = 0
    while i < len(tokens):
        if tokens[i] not in _FIND_COMMAND_PRIMARIES:
            i += 1
            continue
        i += 1
        body: list[str] = []
        while i < len(tokens) and tokens[i] not in _FIND_COMMAND_TERMINATORS:
            body.append(tokens[i])
            i += 1
        if body:
            found.append(body)
    return found


_SHELL_DASH_C_PROGRAMS = {"bash", "sh", "zsh"}


def find_shell_dash_c_script(tokens: list[str]) -> Optional[str]:
    """If `tokens` is a `bash`/`sh`/`zsh` invocation using `-c <script>`
    (optionally preceded/combined with other short flags, e.g. `-lc`,
    `sh -lc "..."`, `bash -eu -c "..."`), return the script string.
    Otherwise return None.

    This is intentionally narrow: it only recognizes `-c` as a bare flag or
    combined into a leading run of short flags (`-lc`), not `--` long-flag
    spellings of `-c`-equivalent behavior, and it gives up (returns None) on
    the first token it doesn't recognize as a flag -- consistent with this
    module's fail-open stance elsewhere.
    """
    if not tokens or tokens[0] not in _SHELL_DASH_C_PROGRAMS:
        return None
    i = 1
    while i < len(tokens):
        t = tokens[i]
        if not t.startswith("-") or t == "-":
            return None
        if t == "--":
            return None
        if t == "-c":
            return tokens[i + 1] if i + 1 < len(tokens) else None
        if t.startswith("--"):
            i += 1
            continue
        if "c" in t[1:]:
            # Combined short flags ending in (or containing) `c`, e.g.
            # `-lc`, `-eu` + separate `-c` handled above. Treat the next
            # token as the script, matching how the shell itself consumes
            # `-c`'s argument regardless of where in a combined flag group
            # it appears.
            return tokens[i + 1] if i + 1 < len(tokens) else None
        i += 1
    return None


def accumulate_dash_c(current: Optional[str], value: str) -> Optional[str]:
    """Fold one `git -C <value>` onto the directory already accumulated.

    Git applies repeated `-C` CUMULATIVELY, each relative to the previous,
    and an absolute value resets the accumulation. Verified against git
    2.53.0 from a repository root: `git -C sub rev-parse --show-prefix`
    prints `sub/`, `git -C sub -C deeper` prints `sub/deeper/`, `git -C sub
    -C ..` prints nothing (back at the root), and `git -C sub -C /tmp`
    fails with "not a git repository" -- i.e. it really did land in `/tmp`.
    An empty value is a no-op in both positions (`-C "" -C sub` and `-C sub
    -C ""` both print `sub/`).

    Keeping only the LAST value (this parser's behaviour until
    deagy/cadre#220) resolved `git -C .worktrees -C ../ worktree prune` to
    `<base>/../` instead of `<base>/.worktrees/..`, so every state-probing
    handler ran its `git` calls in the wrong directory, got a non-zero exit,
    and failed open. The flat-refusal verbs (`worktree remove`/`move`) were
    immune precisely because they never probe state.
    """
    if not value:
        return current
    if os.path.isabs(value):
        return value
    if current is None:
        return value
    return os.path.join(current, value)


def record_git_config(config: dict, pair: str) -> None:
    """Record one `git -c <name>=<value>` pair.

    Config variable names are case-insensitive (verified against git
    2.53.0: `git -c alias.WTR=... wtr` and `git -c alias.wtr=... WTR` both
    resolve), so the key is lowercased. A `-c <name>` with no `=` sets a
    boolean and carries no definition, so it is ignored.

    Only the two-part `<section>.<key>` spelling is normalized correctly --
    the middle component of a three-part `<section>.<subsection>.<key>` name
    is case-SENSITIVE in git. Nothing here reads a three-part key.
    """
    name, sep, value = pair.partition("=")
    if not sep:
        return
    config[name.strip().lower()] = value


def parse_git_invocation(tokens: list[str]):
    """Return (subcommand, sub_args, explicit_cwd, config) for a token list
    that starts with `git`, skipping global flags, or None if this isn't a
    recognizable `git <subcommand> ...` invocation.

    `explicit_cwd` is the accumulated `-C` directory (see
    `accumulate_dash_c`); `config` maps lowercased `-c <name>=<value>`
    variables to their values, which is what makes a command-line-defined
    alias visible to `expand_git_alias` and lets `check_gc` see a
    `gc.worktreePruneExpire` override.
    """
    if not tokens or tokens[0] != "git":
        return None
    i = 1
    explicit_cwd = None
    config: dict = {}
    while i < len(tokens):
        t = tokens[i]
        if t == "-C":
            if i + 1 < len(tokens):
                explicit_cwd = accumulate_dash_c(explicit_cwd, tokens[i + 1])
            i += 2
            continue
        if t == "-c":
            # Only the detached spelling exists: verified against git
            # 2.53.0 that `git -calias.x=...` is rejected with "unknown
            # option", so there is no attached form to parse here.
            if i + 1 < len(tokens):
                record_git_config(config, tokens[i + 1])
            i += 2
            continue
        if t in _GIT_GLOBAL_FLAGS_WITH_VALUE:
            i += 2
            continue
        if any(t.startswith(f"{flag}=") for flag in _GIT_GLOBAL_FLAGS_WITH_VALUE):
            i += 1
            continue
        if t.startswith("-"):
            i += 1
            continue
        break
    if i >= len(tokens):
        return None
    subcommand = tokens[i]
    sub_args = tokens[i + 1 :]
    return subcommand, sub_args, explicit_cwd, config


# ---------------------------------------------------------------------------
# git state helpers -- all fail open (return None / False) on any error, so
# an unresolvable repo state never turns into a false-positive block.
# ---------------------------------------------------------------------------


def run_git(args: list[str], cwd: str, timeout: float = 5.0):
    try:
        proc = subprocess.run(
            ["git", *args],
            cwd=cwd,
            capture_output=True,
            text=True,
            timeout=timeout,
        )
        return proc.returncode, proc.stdout, proc.stderr
    except Exception:
        return None, "", ""


def resolve_cwd(base_cwd: Optional[str], explicit_cwd: Optional[str]) -> str:
    if explicit_cwd:
        if os.path.isabs(explicit_cwd):
            return explicit_cwd
        return os.path.join(base_cwd or os.getcwd(), explicit_cwd)
    return base_cwd or os.getcwd()


def git_status_porcelain(cwd: str, paths: Optional[Iterable[str]] = None) -> Optional[str]:
    """None means "could not determine" (not a repo, git missing, etc.) --
    callers must treat that as fail-open, not as "clean" or "dirty".
    """
    args = ["status", "--porcelain"]
    paths = list(paths) if paths else None
    if paths:
        args += ["--", *paths]
    rc, out, _err = run_git(args, cwd)
    if rc != 0:
        return None
    return out


def is_local_branch(cwd: str, name: str) -> bool:
    rc, _out, _err = run_git(["show-ref", "--verify", "--quiet", f"refs/heads/{name}"], cwd)
    return rc == 0


# ---------------------------------------------------------------------------
# Argument-shape helpers shared by the handlers. These model git's own
# parse-options behaviour for short flags, which several handlers need to
# agree on: `-B name`, `-Bname`, and the combined group forms.
# ---------------------------------------------------------------------------


def _short_flag_group(token: str) -> Optional[str]:
    """The letters of a combined short-flag group (`-fB` -> `"fB"`), or None.

    Only all-alphabetic groups qualify, so `-Bexisting-1` and `-B=x` fall
    through to the attached-value handling instead.
    """
    if len(token) > 1 and token[0] == "-" and token[1] != "-" and token[1:].isalpha():
        return token[1:]
    return None


def flag_value(args: list[str], flag: str) -> Optional[str]:
    """Value of `<flag> <value>`, `--long=<value>`, or -- for a short flag --
    git's attached and combined spellings.

    Git's parse-options consumes a short flag's value from the REST of its
    group when characters follow it, and from the next token when it is the
    group's last character. Verified against git 2.53.0:

      * `git checkout -Bexisting` and `git worktree add -Bexisting <path>`
        both reset `existing` exactly as the detached spelling does, so
        missing the attached form would leave the same destructive
        operation unguarded behind a one-space difference;
      * `git checkout -fB existing` resets `existing` (`B` is last in the
        group, so its value is the next token);
      * `git checkout -Bf existing` creates a branch literally named `f`
        and treats `existing` as the START POINT (`B` is not last, so the
        remainder of the group is its value).

    Note `-B=name` is NOT a git spelling despite reading like one:
    `git checkout -B=weird` creates a branch named `=weird`. The attached
    branch returns `"=weird"` here for exactly that reason, and the
    `<flag>=<value>` branch is reserved for LONG flags, where it is real.
    """
    is_short = len(flag) == 2 and flag.startswith("-") and not flag.startswith("--")
    letter = flag[1] if is_short else None
    for i, a in enumerate(args):
        if a == flag:
            return args[i + 1] if i + 1 < len(args) else None
        if is_short:
            group = _short_flag_group(a)
            if group is not None and letter in group:
                position = group.index(letter)
                if position == len(group) - 1:
                    return args[i + 1] if i + 1 < len(args) else None
                return group[position + 1 :]
            if a.startswith(flag) and len(a) > 2:
                return a[2:]
        elif a.startswith(f"{flag}="):
            return a.split("=", 1)[1]
    return None


def flag_present(args: list[str], flag: str) -> bool:
    """Whether `flag` appears at all, in any of the spellings `flag_value`
    understands. Distinct from `flag_value(...) is not None`, which cannot
    tell "absent" from "present with no value left on the line."
    """
    is_short = len(flag) == 2 and flag.startswith("-") and not flag.startswith("--")
    letter = flag[1] if is_short else None
    for a in args:
        if a == flag:
            return True
        if is_short:
            group = _short_flag_group(a)
            if group is not None and letter in group:
                return True
            if a.startswith(flag) and len(a) > 2:
                return True
        elif a.startswith(f"{flag}="):
            return True
    return False


def _consumes_next_token(token: str, flags_with_value: set) -> bool:
    """Whether `token` takes the FOLLOWING token as its value.

    A combined group only does so when the value-taking flag is its last
    character; otherwise the rest of the group is the value (see
    `flag_value`).
    """
    if token in flags_with_value:
        return True
    group = _short_flag_group(token)
    if group is not None and len(group) > 1:
        for position, letter in enumerate(group):
            if f"-{letter}" in flags_with_value:
                return position == len(group) - 1
    return False


def positionals(args: list[str], flags_with_value: set) -> list[str]:
    """Positional arguments, skipping flags and their values.

    Conservative, not exhaustive -- an unrecognized flag falls through to
    the generic `startswith("-")` skip without consuming a value. The
    failure mode of getting a `flags_with_value` set wrong is a mis-resolved
    start point, which `git rev-parse` then fails to resolve, which fails
    open.
    """
    found: list[str] = []
    i = 0
    while i < len(args):
        a = args[i]
        if a == "--":
            found.extend(args[i + 1 :])
            break
        if _consumes_next_token(a, flags_with_value):
            i += 2
            continue
        if a.startswith("-") and a != "-":
            i += 1
            continue
        found.append(a)
        i += 1
    return found


# ---------------------------------------------------------------------------
# Per-subcommand checks. Each returns a reason dict to deny, or None to
# express no opinion (allow).
# ---------------------------------------------------------------------------


def check_reset(sub_args: list[str], cwd: str, config: Optional[dict] = None):
    if "--hard" not in sub_args:
        return None
    positional = [a for a in sub_args if not a.startswith("-")]
    ref = positional[0] if positional else None

    status = git_status_porcelain(cwd)
    dirty = bool(status)

    moves_branch = False
    if ref:
        rc1, head, _ = run_git(["rev-parse", "--verify", "HEAD"], cwd)
        rc2, target, _ = run_git(["rev-parse", "--verify", ref], cwd)
        if rc1 == 0 and rc2 == 0:
            moves_branch = head.strip() != target.strip()
        # If either rev-parse failed, stay fail-open: don't flag on
        # indeterminate ref resolution, since `git reset --hard` will error
        # out on its own if the ref is bad.

    if not dirty and not moves_branch:
        return None

    reasons = []
    if dirty:
        reasons.append("discard uncommitted changes in the working tree")
    if moves_branch:
        reasons.append(
            "move the current branch to a different commit, which can strand any "
            "unpushed commits currently on it"
        )
    return {
        "reason": (
            "Blocked: `git reset --hard` would " + " and ".join(reasons) + ". "
            "If you want to give up your own uncommitted edits, commit or stash them first "
            "(`git stash push`). If you need the branch to point somewhere else, use a "
            "non---hard reset (`git reset <ref>` keeps the working tree contents) or ask the "
            "operator to confirm a hard reset themselves."
        )
    }


def check_clean(sub_args: list[str], cwd: str, config: Optional[dict] = None):
    short_chars: set[str] = set()
    long_opts: set[str] = set()
    for a in sub_args:
        if a.startswith("--"):
            long_opts.add(a.split("=", 1)[0])
        elif a.startswith("-") and len(a) > 1:
            short_chars.update(a[1:])

    is_force = "f" in short_chars or "--force" in long_opts
    is_dry_run = "n" in short_chars or "--dry-run" in long_opts
    if not is_force or is_dry_run:
        # Without -f, `git clean` refuses to delete anything on its own.
        # An explicit dry run is inherently non-destructive.
        return None

    dry_args = ["clean", "-n"]
    if "d" in short_chars:
        dry_args.append("-d")
    if "x" in short_chars:
        dry_args.append("-x")
    if "X" in short_chars:
        dry_args.append("-X")

    rc, out, _err = run_git(dry_args, cwd)
    if rc != 0:
        # Can't confirm repo state; the real `git clean` will fail on its
        # own in the same situation, so don't block on uncertainty here.
        return None
    if not out.strip():
        return None  # nothing would be removed

    files = [line.strip() for line in out.strip().splitlines()]
    example = files[0] if files else "an untracked path"
    return {
        "reason": (
            f"Blocked: `git clean` would permanently delete {len(files)} untracked path(s) "
            f"(e.g. {example}), which git cannot recover afterward -- there is no commit or "
            "stash to undo it from. Review what would be removed with `git clean -n` (add -d/-x "
            "to match your flags) first, then either re-run once you've confirmed it, or remove "
            "the specific paths you actually intend to delete by name."
        )
    }


def check_branch(sub_args: list[str], cwd: str, config: Optional[dict] = None):
    short_chars: set[str] = set()
    long_opts: set[str] = set()
    positional: list[str] = []
    for a in sub_args:
        if a.startswith("--"):
            long_opts.add(a.split("=", 1)[0])
        elif a.startswith("-") and len(a) > 1:
            short_chars.update(a[1:])
        else:
            positional.append(a)

    force_delete = "D" in short_chars or (
        ("d" in short_chars or "--delete" in long_opts) and ("f" in short_chars or "--force" in long_opts)
    )
    if not force_delete:
        return None

    target = positional[0] if positional else "<branch>"
    return {
        "reason": (
            f"Blocked: `git branch -D`/`--delete --force` on '{target}' bypasses git's own "
            "unmerged-work safety check and can discard commits that no other ref points at. "
            f"Use `git branch -d {target}` instead -- it refuses when the branch has unmerged "
            "work -- or ask the operator to force-delete it themselves if that's really intended."
        )
    }


def check_push(sub_args: list[str], cwd: str, config: Optional[dict] = None):  # noqa: ARG001 - cwd/config unused, kept for handler symmetry
    has_force = any(a in ("-f", "--force") for a in sub_args)
    has_lease = any(a == "--force-with-lease" or a.startswith("--force-with-lease=") for a in sub_args)
    has_delete_flag = any(a in ("--delete", "-d") for a in sub_args)
    has_colon_refspec = any(re.match(r"^:\S+$", a) for a in sub_args)

    if has_delete_flag or has_colon_refspec:
        return {
            "reason": (
                "Blocked: this push deletes a remote branch, which removes it for everyone "
                "using that remote and can't be undone from this working tree. If this is "
                "really intended, ask the operator to delete the remote branch themselves."
            )
        }
    if has_force and not has_lease:
        return {
            "reason": (
                "Blocked: `git push --force` can silently overwrite commits someone else has "
                "already pushed, with no local way to detect it beforehand. Use "
                "`git push --force-with-lease` instead -- it refuses on its own if the remote "
                "has moved since your last fetch."
            )
        }
    return None


def _check_ref_into_paths(cwd: str, ref: Optional[str], paths: list[str], cmd: str):
    """Shared logic for `git checkout <ref> -- <paths>` and
    `git restore --source=<ref> <paths>`: only destructive when a source
    ref is given AND the target paths currently have uncommitted changes.
    """
    if not ref:
        return None  # no ref: routine "discard my own edit" form, always allowed

    status = git_status_porcelain(cwd, paths=paths or None)
    if status is None:
        # Can't determine dirty state (not a repo, git missing, ...); the
        # real command will fail or succeed on its own in that situation.
        return None
    if not status.strip():
        return None  # nothing uncommitted at those paths to lose

    path_desc = ", ".join(paths) if paths else "the given path(s)"
    return {
        "reason": (
            f"Blocked: `git {cmd}` from '{ref}' would overwrite uncommitted changes to "
            f"{path_desc} with that ref's version, destroying the current edits with no way "
            "back. Commit or stash the current changes first (`git stash push -- "
            f"{path_desc}`), or re-run naming only paths that are actually clean."
        )
    }


# `git checkout` flags that consume the following token. `-U`/`--unified`
# and `--conflict`/`--orphan`/`--pathspec-from-file` are here so a start
# point is never confused with one of their values. Read off `git checkout
# -h` on git 2.53.0.
_CHECKOUT_FLAGS_WITH_VALUE = {
    "-b", "-B", "-U", "--unified", "--conflict", "--orphan",
    "--pathspec-from-file", "--inter-hunk-context",
}
# `git switch` equivalents. `-c`/`--create` and `-C`/`--force-create` are
# switch's own spellings of checkout's `-b`/`-B`; note `-C` here is the
# SUBCOMMAND's flag and has nothing to do with git's global `-C <dir>`,
# which `parse_git_invocation` has already consumed before the subcommand
# is read.
_SWITCH_FLAGS_WITH_VALUE = {
    "-c", "--create", "-C", "--force-create", "--conflict", "--orphan",
}


def _check_force_created_branch(
    cwd: str,
    args: list[str],
    forced: str,
    flags_with_value: set,
    spelling: str,
    start_index: int = 0,
):
    """Shared `-B`/`-C` force-create check for `checkout`, `switch`, and
    `worktree add`.

    Refuses only when `forced` already exists as a local branch AND its
    current tip differs from the resolved start point -- i.e. only when the
    command would actually move a branch off its commits. A name that does
    not exist yet behaves exactly like `-b`/`-c`, and a branch already at
    the start point moves nothing; both are allowed.

    `start_index` is where the start point sits among the positionals:
    0 for `checkout -B <branch> [<start>]` and `switch -C <branch>
    [<start>]` (the branch name is the flag's value, not a positional),
    1 for `worktree add -B <branch> <path> [<start>]`.
    """
    if not is_local_branch(cwd, forced):
        return None  # behaves like `-b`/`-c`: nothing to move
    found = positionals(args, flags_with_value)
    start = found[start_index] if len(found) > start_index else "HEAD"
    rc1, current, _ = run_git(["rev-parse", "--verify", forced], cwd)
    rc2, target, _ = run_git(["rev-parse", "--verify", start], cwd)
    if rc1 != 0 or rc2 != 0:
        return None  # indeterminate; git will error on its own
    if current.strip() == target.strip():
        return None  # branch already points there: moves nothing
    return {
        "reason": (
            f"Blocked: `git {spelling} {forced}` force-resets the existing branch "
            f"'{forced}' to '{start}', moving it off the commits it points at now -- git "
            "reports this only as a 'Switched to and reset branch' note, and any commit no "
            "other ref reaches is then recoverable from `git reflog` alone. That is "
            "`agent-autonomy.yaml`'s `discard_uncommitted_work_or_move_branches: never`, and "
            "`workspace-isolation.md` names this flag. Creating a branch is allowed: use the "
            "non-forcing spelling with a name that does not exist yet (git refuses it if the "
            f"name is taken), or check out '{forced}' where it already is."
        )
    }


def check_checkout(sub_args: list[str], cwd: str, config: Optional[dict] = None):
    if not sub_args:
        return None  # bare `git checkout`: lists status, not destructive

    if flag_present(sub_args, "-B"):
        # `-B` force-creates: when the branch exists and points elsewhere,
        # this moves it off its commits with no warning. Guarded since
        # deagy/cadre#221; see the module docstring.
        forced = flag_value(sub_args, "-B")
        if not forced:
            return None  # `-B` with no resolvable name; git errors on its own
        return _check_force_created_branch(
            cwd, sub_args, forced, _CHECKOUT_FLAGS_WITH_VALUE, "checkout -B"
        )

    if flag_present(sub_args, "-b"):
        # Creating a branch pointer. Genuinely safe: git refuses `-b` when
        # the branch already exists.
        return None

    if "--" in sub_args:
        idx = sub_args.index("--")
        pre = [a for a in sub_args[:idx] if not a.startswith("-")]
        paths = sub_args[idx + 1 :]
        ref = pre[0] if pre else None
        return _check_ref_into_paths(cwd, ref, paths, cmd="checkout")

    positional = [a for a in sub_args if not a.startswith("-")]
    if not positional:
        return None

    if len(positional) == 1:
        name = positional[0]
        if is_local_branch(cwd, name):
            return _check_branch_switch(cwd, name)
        # Not a known local branch: treat as a bare pathspec checkout
        # (discard-own-edit form, no explicit source ref) -- always allowed.
        return None

    ref, *paths = positional
    return _check_ref_into_paths(cwd, ref, paths, cmd="checkout")


def _check_branch_switch(cwd: str, branch: str):
    status = git_status_porcelain(cwd)
    if status is None:
        return None  # can't determine; fail open
    if not status.strip():
        return None  # clean tree: nothing to strand or carry across branches

    return {
        "reason": (
            f"Blocked: switching to branch '{branch}' while the working tree has uncommitted "
            "changes risks carrying edits onto a branch they don't belong on, or stranding "
            "another session's expectation of what branch this tree is on. Commit or stash "
            "your changes first (`git stash push`), or confirm with the operator before "
            "switching a tree you didn't create."
        )
    }


def check_restore(sub_args: list[str], cwd: str, config: Optional[dict] = None):
    if not sub_args:
        return None

    source = None
    paths: list[str] = []
    i = 0
    while i < len(sub_args):
        a = sub_args[i]
        if a in ("--source", "-s"):
            if i + 1 < len(sub_args):
                source = sub_args[i + 1]
            i += 2
            continue
        if a.startswith("--source="):
            source = a.split("=", 1)[1]
            i += 1
            continue
        if a == "--":
            paths.extend(sub_args[i + 1 :])
            break
        if a.startswith("-"):
            i += 1
            continue
        paths.append(a)
        i += 1

    return _check_ref_into_paths(cwd, source, paths, cmd="restore")


def check_switch(sub_args: list[str], cwd: str, config: Optional[dict] = None):
    """`git switch` -- the newer spelling of the `checkout` operations this
    module already guards. Tracks deagy/cadre#221.

    `-C`/`--force-create` is `checkout -B` under another name: verified
    against git 2.53.0 that `git switch -C existing`, `git switch
    -Cexisting`, and `git switch -fC existing` all move `existing` off its
    commits, reporting only "Switched to and reset branch 'existing'".

    The plain `git switch <branch>` form gets `checkout`'s dirty-tree check
    for the same reason: leaving it out would make the entire branch-switch
    guard bypassable by choosing the other spelling of the same operation,
    which `workspace-isolation.md` lists side by side ("`git checkout
    <ref>` / `git switch <ref>` that would leave dirty state behind").
    `-c`/`--create` (git refuses it when the branch exists), `--orphan`,
    and `-d`/`--detach` move no existing branch and are always allowed.
    """
    if not sub_args:
        return None  # bare `git switch`: errors, mutates nothing

    if flag_present(sub_args, "-C") or flag_present(sub_args, "--force-create"):
        forced = flag_value(sub_args, "-C") or flag_value(sub_args, "--force-create")
        if not forced:
            return None  # no resolvable name; git errors on its own
        return _check_force_created_branch(
            cwd, sub_args, forced, _SWITCH_FLAGS_WITH_VALUE, "switch -C"
        )

    if (
        flag_present(sub_args, "-c")
        or flag_present(sub_args, "--create")
        or "--orphan" in sub_args
        or flag_present(sub_args, "-d")
        or "--detach" in sub_args
    ):
        return None

    found = positionals(sub_args, _SWITCH_FLAGS_WITH_VALUE)
    if not found:
        return None
    name = found[0]
    if is_local_branch(cwd, name):
        return _check_branch_switch(cwd, name)
    return None


# How `git gc` decides whether to deregister a worktree, and the default it
# uses when nothing overrides it. Verified against git 2.53.0, and the
# result CONTRADICTS the framing in deagy/cadre#217:
#
#   * plain `git gc` did NOT prune a worktree whose directory had just been
#     moved away, and neither did `git gc --prune=now` or `--prune=all` --
#     `gc`'s own `--prune=<date>` governs loose-OBJECT pruning and does not
#     reach worktree registrations at all;
#   * `git -c gc.worktreePruneExpire=now gc` DID deregister it immediately;
#   * once the worktree's administrative files were aged past the default,
#     plain `git gc` deregistered it, and `git worktree prune -n -v --expire
#     3.months.ago` reported exactly the same registration beforehand.
#
# So the state probe below is `worktree prune`'s dry run at gc's effective
# expiry, not at prune's own immediate default -- using the immediate
# default would block routine `git gc` runs that deregister nothing, which
# is the friction this module's design stance treats as the real risk.
_GC_WORKTREE_PRUNE_EXPIRE_DEFAULT = "3.months.ago"
_GC_WORKTREE_PRUNE_EXPIRE_KEY = "gc.worktreepruneexpire"


def check_gc(sub_args: list[str], cwd: str, config: Optional[dict] = None):  # noqa: ARG001 - sub_args unused
    """`git gc` -- scoped to worktree registrations only. Tracks
    deagy/cadre#217.

    `gc` runs worktree pruning as housekeeping, so it reaches the exact
    registration state `check_worktree`'s `prune` refusal exists to protect,
    through a subcommand that names no worktree. The check mirrors that one:
    a dry run decides, and a `gc` that would deregister nothing is a no-op
    worth no friction.
    Deliberately NOT extended to `gc`'s destructive surface generally --
    reflog expiry and `--prune=now` object pruning remain the documented gap
    they were, since detecting "would this prune something otherwise
    recoverable" is materially harder than this check.
    """
    expire = (config or {}).get(_GC_WORKTREE_PRUNE_EXPIRE_KEY)
    if expire is None:
        rc, out, _err = run_git(["config", "--get", "gc.worktreePruneExpire"], cwd)
        expire = out.strip() if rc == 0 and out.strip() else _GC_WORKTREE_PRUNE_EXPIRE_DEFAULT
    if not expire:
        expire = _GC_WORKTREE_PRUNE_EXPIRE_DEFAULT

    rc, out, err = run_git(["worktree", "prune", "-n", "-v", "--expire", expire], cwd)
    if rc != 0:
        return None  # can't confirm state; the real command fails the same way
    # Same stream quirk as `check_worktree`'s prune branch: git 2.53.0
    # reports the dry run on stderr.
    report = "\n".join(part for part in (out.strip(), err.strip()) if part)
    if not report:
        return None  # nothing prunable: gc deregisters nothing

    entries = [line.strip() for line in report.splitlines() if line.strip()]
    example = entries[0] if entries else "a registered worktree"
    return {
        "reason": (
            f"Blocked: `git gc` prunes worktrees as part of its own housekeeping, and here "
            f"that would deregister {len(entries)} worktree(s) (e.g. {example}). Like "
            "`git worktree prune`, gc names no target -- it removes whatever git considers "
            "unreachable, which can include a teammate's worktree on a momentarily "
            "unavailable path. `workspace-isolation.md` says never remove or prune a worktree "
            "yourself. Inspect what would go with `git worktree prune -n -v` (allowed, it "
            "removes nothing) and report it, or ask the operator to run gc themselves."
        )
    }


# `git worktree add` flags that consume the following token as their value,
# so it must not be mistaken for a positional (the new worktree's path, or
# its start point). Conservative, not exhaustive -- an unrecognized flag
# falls through to the generic `startswith("-")` skip below without
# consuming a value. The failure mode of getting this wrong is a
# mis-resolved start point, which `git rev-parse` then fails to resolve,
# which fails open. See check_worktree.
_WORKTREE_ADD_FLAGS_WITH_VALUE = {"-b", "-B", "--reason"}


def check_worktree(sub_args: list[str], cwd: str, config: Optional[dict] = None):
    """`git worktree` -- see the module docstring for what each verb does and
    does not block, and why `prune` is state-checked while `remove`/`move`
    are refused flat. Tracks deagy/cadre#215.
    """
    verb_index = next((i for i, a in enumerate(sub_args) if not a.startswith("-")), None)
    if verb_index is None:
        return None  # bare `git worktree`: prints usage, mutates nothing
    verb = sub_args[verb_index]
    rest = sub_args[verb_index + 1 :]

    if verb == "remove":
        target = next((a for a in rest if not a.startswith("-")), "<worktree>")
        return {
            "reason": (
                f"Blocked: `git worktree remove` on '{target}' deregisters a worktree, which "
                "is a destructive git-metadata operation requiring human approval "
                "(`agent-autonomy.yaml`: destructive_action: human_approval). "
                "`workspace-isolation.md` says never remove or prune a worktree yourself -- "
                "including one you created, and including an inspection worktree you are done "
                "with: the worktree IS the deliverable location until a human or the "
                "dispatching process decides otherwise. Leave it in place and say in your "
                "result that it can be cleaned up, or ask the operator to remove it themselves."
            )
        }

    if verb == "move":
        positional = [a for a in rest if not a.startswith("-")]
        source = positional[0] if positional else "<worktree>"
        return {
            "reason": (
                f"Blocked: `git worktree move` relocates the registered worktree '{source}'. "
                "Any session whose working directory is the old path loses its tree mid-task, "
                "with no error at the moment of the move. Rewriting another session's worktree "
                "registration is a destructive git-metadata operation "
                "(`agent-autonomy.yaml`: destructive_action: human_approval) and "
                "`workspace-isolation.md` reserves worktree cleanup and relocation to the "
                "operator. Create a new worktree at the path you want instead, or ask the "
                "operator to move this one."
            )
        }

    if verb == "prune":
        short_chars: set[str] = set()
        long_opts: set[str] = set()
        for a in rest:
            if a.startswith("--"):
                long_opts.add(a.split("=", 1)[0])
            elif a.startswith("-") and len(a) > 1:
                short_chars.update(a[1:])
        if "n" in short_chars or "--dry-run" in long_opts:
            return None  # caller's own dry run: reports, removes nothing

        dry_args = ["worktree", "prune", "-n", "-v"]
        expire = flag_value(rest, "--expire")
        if expire:
            dry_args += ["--expire", expire]

        rc, out, err = run_git(dry_args, cwd)
        if rc != 0:
            # Can't confirm state (not a repo, git missing, bad --expire);
            # the real command fails the same way. Don't block on that.
            return None
        # git 2.53.0 reports prune's dry run on stderr, not stdout -- both
        # are considered so a future/older git writing to either is caught.
        report = "\n".join(part for part in (out.strip(), err.strip()) if part)
        if not report:
            return None  # nothing prunable: the command would be a no-op

        entries = [line.strip() for line in report.splitlines() if line.strip()]
        example = entries[0] if entries else "a registered worktree"
        return {
            "reason": (
                f"Blocked: `git worktree prune` would deregister {len(entries)} worktree(s) "
                f"(e.g. {example}). Prune names no target -- it removes whatever git currently "
                "considers unreachable, which can include a teammate's worktree sitting on a "
                "momentarily unavailable path, so you cannot tell from this command that only "
                "your own worktrees are affected. `workspace-isolation.md` says never remove or "
                "prune a worktree yourself. Inspect what would go with "
                "`git worktree prune -n -v` (allowed, it removes nothing) and report it, or ask "
                "the operator to prune themselves."
            )
        }

    if verb == "add":
        forced = flag_value(rest, "-B")
        if not forced:
            return None  # plain `add`/`-b`: explicitly allowed, creates only
        if not is_local_branch(cwd, forced):
            return None  # `-B` on a new name behaves like `-b`: nothing to move
        positional = positionals(rest, _WORKTREE_ADD_FLAGS_WITH_VALUE)
        # positional[0] is the new worktree's path; positional[1], if
        # present, is the start point. Default start point is HEAD.
        start = positional[1] if len(positional) > 1 else "HEAD"
        rc1, current, _ = run_git(["rev-parse", "--verify", forced], cwd)
        rc2, target, _ = run_git(["rev-parse", "--verify", start], cwd)
        if rc1 != 0 or rc2 != 0:
            return None  # indeterminate; git will error on its own
        if current.strip() == target.strip():
            return None  # branch already points there: `-B` moves nothing
        return {
            "reason": (
                f"Blocked: `git worktree add -B {forced}` force-resets the existing branch "
                f"'{forced}' to '{start}', moving it off the commits it points at now -- git "
                "reports this only as a 'resetting branch' note, and any commit no other ref "
                "reaches is then recoverable from `git reflog` alone. That is "
                "`agent-autonomy.yaml`'s `discard_uncommitted_work_or_move_branches: never`. "
                f"Creating a worktree is allowed: use `git worktree add -b <new-branch>` with a "
                f"name that doesn't exist yet (git refuses `-b` if it does), or check out "
                f"'{forced}' into the new worktree without -B if you want it where it already is."
            )
        }

    # list / lock / unlock / repair and anything else: no opinion.
    return None


# Keep in lockstep with `GIT_GUARD_HANDLERS` in
# `cline-plugins/cline-agents/index.ts`. That is no longer a claim in a
# comment: `plugin/tools/test_guard_parity.py` parses both files and fails
# when the key sets diverge, and drives a shared behavioural fixture through
# both implementations (deagy/cadre#222).
_HANDLERS = {
    "reset": check_reset,
    "checkout": check_checkout,
    "switch": check_switch,
    "restore": check_restore,
    "clean": check_clean,
    "branch": check_branch,
    "push": check_push,
    "worktree": check_worktree,
    "gc": check_gc,
}


# How many alias definitions to follow before giving up. Git itself detects
# alias loops ("fatal: alias loop detected") and this mirrors that with a
# `seen` set; the numeric bound is a second, cheaper backstop so a
# pathological chain can never turn this hook into a long walk.
_MAX_ALIAS_EXPANSION_DEPTH = 5


def expand_git_alias(subcommand: str, sub_args: list[str], config: dict, explicit_cwd):
    """Resolve a subcommand that names an alias defined by `-c` on the same
    command line. Tracks deagy/cadre#218.

    Returns `(subcommand, sub_args, explicit_cwd, config, shell_script)`. A
    non-empty `shell_script` means the alias was git's `!<shell command>`
    form and the caller should evaluate that string through
    `evaluate_command` instead of dispatching a handler.

    `config` is returned, not just consumed internally, because a definition
    may set config the HANDLER needs rather than only config this function
    needs. An earlier version folded `nested_config` into a local and
    dropped it on return; `check_gc` is the one handler that reads config,
    so `git -c alias.g='-c gc.worktreePruneExpire=now gc' g` was allowed
    while real git pruned. Verified against git 2.53.0: the aliased form
    deregisters a prunable worktree exactly as the direct spelling does, so
    the guard must resolve the same expiry the real invocation will use.

    This closes the COMMAND-LINE alias spelling only. The config-file alias
    gap stays open (see the module docstring): resolving one means reading
    and trusting the invoking user's git config, whereas `-c alias.x=...`
    is already in the tokens this hook was handed.

    Behaviour verified against git 2.53.0:

      * `git -c alias.st='status --porcelain' st` runs the alias, so a
        definition is live in the very invocation that defines it;
      * remaining arguments are appended to the definition, for both the
        plain (`git -c alias.lg='log --oneline -n' lg 1`) and shell
        (`git -c alias.sh='!echo GOT' sh extra1 extra2` printed
        "GOT extra1 extra2") forms;
      * an alias may name another alias, and git detects loops;
      * an alias CANNOT shadow a real subcommand -- `git -c
        alias.status='log --oneline -n 1' status` ran the real `status` --
        which is why a subcommand already in `_HANDLERS` is never expanded;
      * alias names are matched case-insensitively;
      * the attached spelling `git -calias.x=...` does not exist ("unknown
        option"), so only the detached one is recorded.
    """
    seen: set = set()
    for _ in range(_MAX_ALIAS_EXPANSION_DEPTH):
        if subcommand in _HANDLERS:
            break  # a real subcommand: git ignores any alias of that name
        key = f"alias.{subcommand.lower()}"
        if key in seen:
            break  # alias loop, as git itself reports
        definition = config.get(key)
        if definition is None:
            break
        seen.add(key)
        if definition.startswith("!"):
            script = " ".join(
                [definition[1:].strip(), *(shlex.quote(a) for a in sub_args)]
            ).strip()
            return subcommand, sub_args, explicit_cwd, config, script
        try:
            parts = shlex.split(definition, posix=True)
        except ValueError:
            break  # unbalanced quoting in the definition; don't guess
        if not parts:
            break
        reparsed = parse_git_invocation(["git", *parts, *sub_args])
        if reparsed is None:
            break
        subcommand, sub_args, nested_cwd, nested_config = reparsed
        # A definition may carry global flags of its own (`git <definition>
        # <args>` is literally what git runs), so fold them in.
        if nested_cwd:
            explicit_cwd = accumulate_dash_c(explicit_cwd, nested_cwd)
        if nested_config:
            config = {**config, **nested_config}
    return subcommand, sub_args, explicit_cwd, config, None


# How many levels of `bash -c "..."`/`sh -c "..."`/`zsh -c "..."` inline
# indirection to recurse into. 3 covers the realistic "step1 && bash -c
# '... && sh -c \"git ...\"'" nesting depth this hook is meant to catch
# without becoming an unbounded walk: a command nested deeper than this
# bound is a documented, known gap -- NOT silently claimed as covered --
# see ShellDashCRecursionTests in the test module for the covered
# (exactly-at-bound) vs. not-covered (one-level-deeper) cases. Kept small
# and fixed rather than unbounded to avoid pathological/adversarial nesting
# turning this hook into an unbounded recursive parser. Matches the Cline
# guard's `MAX_SHELL_C_RECURSION_DEPTH` in `cline-plugins/cline-agents/
# index.ts` -- kept in sync deliberately, not by coincidence.
_MAX_SHELL_RECURSION_DEPTH = 3


def evaluate_command(command: str, base_cwd: str, _depth: int = 0):
    """Return a `{"reason": "..."}` dict to deny the Bash command, or None
    to express no opinion (allow). Never raises: unparseable segments are
    skipped rather than treated as destructive.

    `_depth` is an internal recursion counter for `bash -c`/`sh -c`/`zsh -c`
    inline-string indirection (see `_MAX_SHELL_RECURSION_DEPTH`); callers
    should not pass it.
    """
    for segment in split_top_level(command):
        try:
            tokens = shlex.split(segment, posix=True)
        except ValueError:
            continue  # unbalanced quoting or similar; skip, don't guess
        tokens = strip_leading_wrappers(tokens)
        if not tokens:
            continue

        script = find_shell_dash_c_script(tokens)
        if script is not None:
            if _depth < _MAX_SHELL_RECURSION_DEPTH:
                nested_decision = evaluate_command(script, base_cwd, _depth + 1)
                if nested_decision:
                    return nested_decision
            # Beyond the recursion bound: deliberately not recursed into
            # further, per _MAX_SHELL_RECURSION_DEPTH above. Fall through
            # to the next top-level segment rather than misreading `bash`/
            # `sh`/`zsh` itself as a git invocation.
            continue

        # The segment itself, plus any command `find` carries in argument
        # position (`-exec git ... \;`), which prefix stripping cannot reach.
        for candidate in [tokens, *(
            strip_leading_wrappers(body) for body in find_command_invocations(tokens)
        )]:
            decision = _evaluate_git_tokens(candidate, base_cwd, _depth)
            if decision:
                return decision
    return None


def _evaluate_git_tokens(tokens: list[str], base_cwd: str, _depth: int):
    """Parse one already-wrapper-stripped token list as a `git` invocation
    and run its handler, expanding a command-line-defined alias first.
    """
    parsed = parse_git_invocation(tokens)
    if not parsed:
        return None
    subcommand, sub_args, explicit_cwd, config = parsed
    subcommand, sub_args, explicit_cwd, config, shell_script = expand_git_alias(
        subcommand, sub_args, config, explicit_cwd
    )
    if shell_script is not None:
        # A `!shell` alias: hand the expansion to the same bounded
        # recursion that `bash -c "..."` uses rather than ignoring it.
        if _depth < _MAX_SHELL_RECURSION_DEPTH:
            return evaluate_command(shell_script, base_cwd, _depth + 1)
        return None
    handler = _HANDLERS.get(subcommand)
    if handler is None:
        return None
    cwd = resolve_cwd(base_cwd, explicit_cwd)
    return handler(sub_args, cwd, config)


_DISABLE_ENV_VAR = "CADRE_DISABLE_WORKSPACE_MUTATION_GUARD"


def main() -> int:
    # Opt-out check, deliberately first: before reading stdin, before any
    # parsing, before any git state check. See the module docstring's
    # "Opt-out" section for why this exists and why it is never referenced
    # from generated plugin output.
    if os.environ.get(_DISABLE_ENV_VAR, "").strip().lower() in ("1", "true"):
        return 0

    try:
        raw = sys.stdin.read()
        data = json.loads(raw)
    except Exception:
        # Malformed input: fail open. A guard that crashes or hangs on bad
        # input is a guard that gets disabled outright.
        return 0

    try:
        if not isinstance(data, dict):
            return 0
        if data.get("hook_event_name") != "PreToolUse":
            return 0
        if data.get("tool_name") != "Bash":
            return 0
        tool_input = data.get("tool_input")
        if not isinstance(tool_input, dict):
            return 0
        command = tool_input.get("command")
        if not isinstance(command, str) or not command.strip():
            return 0
        cwd = data.get("cwd")
        if not isinstance(cwd, str) or not cwd:
            cwd = os.getcwd()

        decision = evaluate_command(command, cwd)
    except Exception as exc:  # noqa: BLE001 - deliberate catch-all, see module docstring
        print(f"guard_workspace_mutation: internal error, allowing: {exc}", file=sys.stderr)
        return 0

    if decision:
        print(
            json.dumps(
                {
                    "hookSpecificOutput": {
                        "hookEventName": "PreToolUse",
                        "permissionDecision": "deny",
                        "permissionDecisionReason": decision["reason"],
                    }
                }
            )
        )
    return 0


if __name__ == "__main__":
    sys.exit(main())
