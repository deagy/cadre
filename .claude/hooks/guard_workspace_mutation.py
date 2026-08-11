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

  * `git checkout <branch>` (switching branches, no `-b`/`-B`, no `--`,
    and `<branch>` really is a local branch per `git show-ref`) when the
    tree is dirty. Git itself refuses when the switch would conflict with
    modified files, but a switch that doesn't conflict still carries or
    strands uncommitted edits across branches and is exactly the "switched
    another session's worktree off its branch" shape from today. A clean
    tree switch is always allowed. `git checkout -b`/`-B <new>` (creating,
    or forcing, a branch pointer) is always allowed: it does not overwrite
    working-tree content when the (implicit or explicit) start point is
    the current HEAD, which is overwhelmingly the common case, and
    tightening this further would mean parsing/resolving arbitrary start
    points for a workflow explicitly called out as should-stay-allowed.

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
      Note the deliberate asymmetry with `check_checkout`, which allows
    `git checkout -B` unconditionally (see its bullet above): there the
    start point is overwhelmingly the implicit current HEAD and resolving
    arbitrary start points was judged out of scope. Here the start point
    is a plain positional argument that `git rev-parse` resolves cheaply,
    so the check is taken. Whether `check_checkout` should adopt the same
    check is a separate question, deliberately not changed under #215.

Deliberately NOT covered, with reasoning:

  * Deleting a worktree's directory directly (`rm -rf .worktrees/foo`)
    rather than through `git worktree remove`.
    `workspace-isolation.md` forbids this in the same sentence as the git
    verbs, but this hook only inspects `git` invocations -- an `rm` is not
    a git subcommand and `_HANDLERS` never sees it. Deciding whether an
    arbitrary `rm` target is a registered worktree (and doing it for every
    `rm` the model runs) is a materially different and much broader
    check than anything else here. Left as a known gap, pinned by
    `test_rm_of_a_worktree_directory_is_a_documented_gap`.
  * `git worktree prune` reached indirectly through `git gc`, which runs
    worktree pruning as part of its own housekeeping. `gc` has no handler
    (see the reflog/`gc --prune=now` gap below), so this spelling is not
    intercepted. Pinned by `test_git_gc_is_a_documented_gap`.
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
  * Reflog expiry / `git gc --prune=now` -- destroys unreachable commits,
    but is not an operation any workflow here performs routinely, and
    detecting "would this prune something otherwise recoverable" reliably
    is materially harder than the checks above. Left as a known gap.
  * Anything reached through a file the model writes and then executes
    (e.g. a shell script containing `git reset --hard`) rather than a
    literal `Bash` tool command -- `PreToolUse` only sees the `Bash`
    command string itself, so an indirection through a written script is
    invisible to this hook by construction, not by choice. (`bash -c
    "..."`/`sh -c "..."` inline strings ARE recursed into, see below --
    this gap is specifically about content that only exists as a file on
    disk, not an inline string in the `Bash` command itself.)
  * `--git-dir`/`--work-tree`/`GIT_DIR`/`GIT_WORK_TREE` redirection to a
    different repository than `base_cwd`. `-C <dir>` (and the equivalent
    `--git-dir`/`--work-tree` global flags) are parsed and skipped by
    `parse_git_invocation` so the guard can still recognize the
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
  * git alias resolution (e.g. a user-configured `git co` alias for
    `checkout`, or a `!shell` alias). This hook only recognizes git's own
    literal subcommand names in `_HANDLERS`; an aliased spelling of a
    destructive subcommand is invisible to it, and resolving aliases would
    require reading and trusting the invoking user's git config. Left as a
    known gap.

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


def split_top_level(command: str) -> list[str]:
    """Split a shell command line into top-level segments on `&&`, `||`,
    `;`, and `|`, respecting single/double quoting. Not a full shell
    parser -- good enough to find each independent `git ...` invocation in
    a chained command line without being fooled by an operator sitting
    inside a quoted string.
    """
    segments: list[str] = []
    buf: list[str] = []
    quote: Optional[str] = None
    i = 0
    n = len(command)
    while i < n:
        ch = command[i]
        if quote:
            buf.append(ch)
            if ch == quote and (i == 0 or command[i - 1] != "\\"):
                quote = None
            i += 1
            continue
        if ch in ("'", '"'):
            quote = ch
            buf.append(ch)
            i += 1
            continue
        if command[i : i + 2] in ("&&", "||"):
            segments.append("".join(buf))
            buf = []
            i += 2
            continue
        if ch in (";", "|"):
            segments.append("".join(buf))
            buf = []
            i += 1
            continue
        buf.append(ch)
        i += 1
    segments.append("".join(buf))
    return [s for s in (seg.strip() for seg in segments) if s]


_ENV_ASSIGN_RE = re.compile(r"^[A-Za-z_][A-Za-z0-9_]*=")
# `env` is included here (not just handled by the dedicated branch below) so
# it reads as a wrapper token like the rest of this set even though its
# actual skipping logic -- because `env` can take its own flags and
# `VAR=value` pairs before the real command -- needs more than "skip one
# token" (see the `env`-specific branch in `strip_leading_wrappers`).
_WRAPPER_TOKENS = {"sudo", "command", "exec", "nohup", "time", "env"}
# `env -u NAME` (`--unset NAME`), `env -C DIR` (`--chdir DIR`), and
# `env -S STRING` (`--split-string STRING`) each take their value as the
# next token, so they must be recognized here or the loop below will
# mistake the value token for the start of the real command and stop
# skipping too early -- letting a wrapped destructive command through
# unrecognized. Every other `env` flag (e.g. `-i`/`--ignore-environment`)
# takes no value and this parser doesn't need to distinguish it from a
# no-value flag to keep skipping past it.
_ENV_FLAGS_WITH_VALUE = {"-u", "--unset", "-C", "--chdir", "-S", "--split-string"}

_GIT_GLOBAL_FLAGS_WITH_VALUE = {"-C", "--git-dir", "--work-tree", "--namespace", "-c"}


def strip_leading_wrappers(tokens: list[str]) -> list[str]:
    """Strip leading `VAR=value` environment assignments and wrapper
    commands (`sudo`, `env`, `command`, `exec`, `nohup`, `time`, ...) so the
    real command is exposed at `tokens[0]`.

    `env` gets dedicated handling because, unlike the other wrapper tokens,
    it routinely carries its own flags and `VAR=value` assignments before
    the command it runs (`env -i FOO=bar git reset --hard`, `env FOO=bar
    git reset --hard`) -- a bare "skip one token" rule would leave those
    trailing behind and stop `parse_git_invocation` from ever recognizing
    `git` at `tokens[0]`.
    """
    i = 0
    while i < len(tokens):
        t = tokens[i]
        if _ENV_ASSIGN_RE.match(t):
            i += 1
            continue
        if t == "env":
            i += 1
            while i < len(tokens):
                nt = tokens[i]
                if nt == "--":
                    i += 1
                    break
                if nt in _ENV_FLAGS_WITH_VALUE:
                    i += 2
                    continue
                if nt.startswith("-") and nt != "-":
                    i += 1
                    continue
                if _ENV_ASSIGN_RE.match(nt):
                    i += 1
                    continue
                break
            continue
        if t in _WRAPPER_TOKENS:
            i += 1
            continue
        break
    return tokens[i:]


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


def parse_git_invocation(tokens: list[str]):
    """Return (subcommand, sub_args, explicit_cwd) for a token list that
    starts with `git`, skipping global flags (including `-C <dir>`), or
    None if this isn't a recognizable `git <subcommand> ...` invocation.
    """
    if not tokens or tokens[0] != "git":
        return None
    i = 1
    explicit_cwd = None
    while i < len(tokens):
        t = tokens[i]
        if t == "-C":
            if i + 1 < len(tokens):
                explicit_cwd = tokens[i + 1]
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
    return subcommand, sub_args, explicit_cwd


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
# Per-subcommand checks. Each returns a reason dict to deny, or None to
# express no opinion (allow).
# ---------------------------------------------------------------------------


def check_reset(sub_args: list[str], cwd: str):
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


def check_clean(sub_args: list[str], cwd: str):
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


def check_branch(sub_args: list[str], cwd: str):
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


def check_push(sub_args: list[str], cwd: str):  # noqa: ARG001 - cwd unused, kept for handler symmetry
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


def check_checkout(sub_args: list[str], cwd: str):
    if not sub_args:
        return None  # bare `git checkout`: lists status, not destructive

    if "-b" in sub_args or "-B" in sub_args:
        # Creating (or resetting) a branch pointer at the implicit/explicit
        # start point. Left unblocked -- see module docstring.
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


def check_restore(sub_args: list[str], cwd: str):
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


# `git worktree add` flags that consume the following token as their value,
# so it must not be mistaken for a positional (the new worktree's path, or
# its start point). Conservative, not exhaustive -- an unrecognized flag
# falls through to the generic `startswith("-")` skip below without
# consuming a value. The failure mode of getting this wrong is a
# mis-resolved start point, which `git rev-parse` then fails to resolve,
# which fails open. See check_worktree.
_WORKTREE_ADD_FLAGS_WITH_VALUE = {"-b", "-B", "--reason"}


def _worktree_positionals(args: list[str], flags_with_value: set[str]) -> list[str]:
    positional: list[str] = []
    i = 0
    while i < len(args):
        a = args[i]
        if a == "--":
            positional.extend(args[i + 1 :])
            break
        if a in flags_with_value:
            i += 2
            continue
        if a.startswith("-") and a != "-":
            i += 1
            continue
        positional.append(a)
        i += 1
    return positional


def _worktree_flag_value(args: list[str], flag: str) -> Optional[str]:
    """Value of `<flag> <value>`, `<flag>=<value>`, or -- for a short flag
    only -- git's attached spelling `-Bvalue`.

    The attached short form is not a nicety: verified against git 2.53.0
    that `git worktree add -Bexisting <path>` resets `existing` exactly as
    the detached `-B existing` spelling does, so missing it would leave the
    same destructive operation unguarded behind a one-space difference.
    """
    is_short = flag.startswith("-") and not flag.startswith("--")
    for i, a in enumerate(args):
        if a == flag:
            return args[i + 1] if i + 1 < len(args) else None
        if a.startswith(f"{flag}="):
            return a.split("=", 1)[1]
        if is_short and len(flag) == 2 and a.startswith(flag) and len(a) > 2:
            return a[2:]
    return None


def check_worktree(sub_args: list[str], cwd: str):
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
        expire = _worktree_flag_value(rest, "--expire")
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
        forced = _worktree_flag_value(rest, "-B")
        if not forced:
            return None  # plain `add`/`-b`: explicitly allowed, creates only
        if not is_local_branch(cwd, forced):
            return None  # `-B` on a new name behaves like `-b`: nothing to move
        positional = _worktree_positionals(rest, _WORKTREE_ADD_FLAGS_WITH_VALUE)
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


_HANDLERS = {
    "reset": check_reset,
    "checkout": check_checkout,
    "restore": check_restore,
    "clean": check_clean,
    "branch": check_branch,
    "push": check_push,
    "worktree": check_worktree,
}


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

        parsed = parse_git_invocation(tokens)
        if not parsed:
            continue
        subcommand, sub_args, explicit_cwd = parsed
        handler = _HANDLERS.get(subcommand)
        if handler is None:
            continue
        cwd = resolve_cwd(base_cwd, explicit_cwd)
        decision = handler(sub_args, cwd)
        if decision:
            return decision
    return None


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
