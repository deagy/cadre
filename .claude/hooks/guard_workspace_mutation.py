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

Deliberately NOT covered, with reasoning:

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
    invisible to this hook by construction, not by choice.
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
_WRAPPER_TOKENS = {"sudo", "command", "exec", "nohup", "time"}

_GIT_GLOBAL_FLAGS_WITH_VALUE = {"-C", "--git-dir", "--work-tree", "--namespace", "-c"}


def strip_leading_wrappers(tokens: list[str]) -> list[str]:
    i = 0
    while i < len(tokens) and (_ENV_ASSIGN_RE.match(tokens[i]) or tokens[i] in _WRAPPER_TOKENS):
        i += 1
    return tokens[i:]


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


_HANDLERS = {
    "reset": check_reset,
    "checkout": check_checkout,
    "restore": check_restore,
    "clean": check_clean,
    "branch": check_branch,
    "push": check_push,
}


def evaluate_command(command: str, base_cwd: str):
    """Return a `{"reason": "..."}` dict to deny the Bash command, or None
    to express no opinion (allow). Never raises: unparseable segments are
    skipped rather than treated as destructive.
    """
    for segment in split_top_level(command):
        try:
            tokens = shlex.split(segment, posix=True)
        except ValueError:
            continue  # unbalanced quoting or similar; skip, don't guess
        tokens = strip_leading_wrappers(tokens)
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


def main() -> int:
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
