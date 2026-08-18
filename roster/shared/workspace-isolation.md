# Workspace Isolation

**These four sections bind every role, every tier, no exceptions**, and they
are the four this file opens with:

- Never mutate a working tree you did not create
- The security-relevant-resolver rule
- Never remove or prune a worktree yourself
- No runner names as behavioral conditions

Read the first before running any `git` command that is not purely a query.

**Applies to:** everything from `Isolating your own edits (write-capable
tiers)` onward -- the worktree-isolation steps (Steps 0-2), the dirty-scope
guard, the teams rule, escalation, and the end-of-task result block -- binds
write-capable capability tiers only (any tier whose `sandbox_mode` in
`roster/runner-capabilities.json` is not `read-only` -- currently
`document_author`, `code_author`, `test_author`, and `environment_operator`;
see `internal/generators/plugin_generation.go`'s `WRITE_CAPABLE_TIERS`). A read-only role
has no edits to isolate, so those sections do not apply to it, and its
generated wrapper carries this header plus the four sections above and
nothing else.

**The scoping line to keep straight, because it is not "can this role write
files":** a read-only role still *creates* worktrees -- for inspection, as
the never-mutate section below instructs it to. So every rule about a
worktree a role creates, removes, or resolves configuration from inside
stays universal. Only the decision about where your *edits* land is
write-capable-only.

`cadre resolve-shared workspace-isolation.md` returns this file in full on
request regardless of the caller's tier -- shared policy resolution is
filename-based, not capability-aware. So if you are read-only and need the
sections your wrapper omitted (to review another role's isolation choice,
say), fetch them; nothing hides them from you.

## Never mutate a working tree you did not create

**Applies to every role and every capability tier, whether or not the rest
of this file does.** Being dispatched for a read-only task does not exempt
you: "I am only reviewing" describes your *intent*, not the effect of the
command you are about to run.

You may run any `git` command that only *reads* state: `status`, `log`,
`show`, `diff`, `rev-parse`, `branch --list`, `worktree list`, `cat-file`.

Never run a `git` command that discards uncommitted work or moves a branch
off commits it already had, in a working tree you did not create yourself.
The governing rule is that sentence, not the list below;
`agent-autonomy.yaml`'s `repository.discard_uncommitted_work_or_move_branches:
never` states it normatively. The list is illustrative and not exhaustive --
a command's absence from it is never permission:

- `git reset --hard` (and `--merge`/`--keep`)
- `git checkout <ref>` / `git switch <ref>` that would leave dirty state behind
- `git checkout -B` / `git switch -C` (force-create, resetting an existing branch)
- `git checkout -- <path>` / `git restore <path>` (discards that file's changes)
- `git stash` in any form -- "I'll stash and pop it back" is still a mutation,
  and a failed pop loses the work
- `git clean -f` / `-fd`
- `git branch -f`, `git branch -D`, `git branch -m`
- `git update-ref`, `git tag -f` (direct ref manipulation)
- `git rebase`, `git cherry-pick`, `git revert`, `git merge`
- `git push --force` / `--force-with-lease`

This is the rule an agent is most likely to talk itself past, because the
reasoning feels safe and sounds responsible: *I just need to see this branch's
diff; I will reset to `main` and put it back afterwards.* Two things make that
wrong. The tree may hold uncommitted work the caller has not pushed, which a
hard reset destroys with no undo. And the branch pointer you move may be the
only local reference to a commit -- recoverable from `git reflog` only if
someone notices in time to look.

Note what this rule is *not* protected by: a role with file-write tools has
no extra license here, and a role without them has no automatic immunity.
The real incident behind this section was a write-capable documentation role
that ran `git reset --hard main` to read a branch's diff, restored nothing,
and truthfully reported that it had made no edits -- it never touched a file.
It had already been given, and followed, the worktree-isolation steps that
govern write-capable roles; what was missing was this rule.

**To inspect a revision that is not checked out, read it without changing
anything:**

```sh
git diff main...HEAD              # the branch's own changes
git show <ref>:<path>             # one file at a revision
git log --oneline <base>..<head>  # what a branch adds
gh pr diff <number>               # a PR's diff, no checkout at all
```

If you genuinely cannot review without a different revision checked out, do
**not** mutate the caller's tree to get one. Create your own worktree
(`create_local_branch_or_worktree: allowed` covers doing this purely for
inspection, at any tier), which leaves the caller's tree untouched:

```sh
git -C <repository_root> worktree add --detach \
  ".worktrees/<task-id>/<role-id>-review" <ref>
```

This is the one place `--detach` is correct: an inspection worktree needs no
branch, and creating one risks colliding with a real branch name.

If even that is not possible, stop and return a labeled blocking question
saying what you needed checked out and why -- do not proceed by mutating
someone else's tree.

If you mutate a tree anyway -- deliberately, or by discovering after the fact
that a command you ran was destructive -- **say so explicitly and
prominently in your result**, including the exact command and what state
preceded it. A destructive action reported immediately is recoverable
(`git reflog` still holds the old tip); the same action discovered three
steps later, by someone wondering where their work went, may not be.

## The security-relevant-resolver rule

Some project state a resolver depends on is deliberately not tracked by
git, so it is **absent** in a freshly created worktree even though it exists
in the main tree. This applies to any worktree you create, including an
inspection worktree created purely to read a revision. If a resolver whose
result is security-relevant would resolve differently from inside it,
**degrade or block -- never resolve silently as if nothing changed.**

The concrete case to know: `.agents/knowledge-store/config.json` is
git-ignored by design (it is untracked, project-local configuration -- see
`roster/shared/README.md`'s "three things that live under `.agents/`"
table). `find_project_local_config()`
(`internal/knowledge/config.go`) walks upward from the current
working directory looking for that file, and **stops at the first directory
containing `.git`** -- which in a linked worktree is the worktree's own
`.git` file (pointing at the shared administrative directory), not the main
checkout's tree. That walk-and-stop boundary means the search never crosses
into the main working tree to find a config file that does exist there, and
falls through to the machine-global shared store instead
(`KNOWLEDGE_STORE_HOME`, defaulting to `~/.agents/knowledge-store/`). A
project that relies on its own project-local store for tenant/classification
partitioning (see `roster/knowledge-store/SECURITY.md`) would silently and
invisibly lose that partitioning the moment retrieval runs from inside a
fresh worktree instead of the main tree.

Knowledge retrieval is squarely a read-only role's work, so this is not a
write-capable concern: create an inspection worktree, run a retrieval from
inside it, and you have quietly widened the store you are reading from.

When you detect this condition -- a security-relevant resolver whose config
file is untracked and therefore absent from a worktree you just created --
do not proceed as if the global store is an equivalent substitute.
Explicitly degrade (treat retrieval as unavailable and say so) or block
(raise it as a blocking question) rather than resolving to the broader,
differently-scoped store without comment. This applies to any future
resolver with the same shape (untracked project-local file, walk-to-`.git`
boundary, security- or classification-relevant result), not only this one.

## Never remove or prune a worktree yourself

Never run `git worktree remove`, `git worktree prune`, or `git worktree
move` (or delete a worktree directory directly) as part of your own task.
`move` belongs here for the same reason: it rewrites a registration in
place, so a session whose working directory is the old path loses its tree
mid-task with no error at the moment of the move. This covers every
worktree, including an inspection worktree you created yourself and are
finished with: tidying up afterwards is exactly the reasoning to refuse,
because `git worktree prune` is not scoped to your worktree -- it
deregisters any worktree git currently considers unreachable, which can
include a teammate's in-progress tree on a mounted or momentarily
unavailable path.

A worktree that holds work *is* the deliverable location until a human or
the dispatching process decides otherwise, and removing worktree
registrations is a destructive git-metadata operation
(`destructive_action: human_approval` in `agent-autonomy.yaml`). Leave
cleanup to the operator; see `roster/RUNBOOK.md`'s worktree-operations
section. If a leftover inspection worktree is untidy, say so in your result
and let the operator remove it.

On Claude Code and Cline this rule is also enforced structurally, not by
prompt text alone: a guard (`hooks/guard` for
Claude Code, a compiled binary shipped per platform; the equivalent in the
Cline agents plugin) refuses `git
worktree remove` and `git worktree move` outright, refuses `git worktree
prune` whenever its own dry run shows a registration would actually be
removed, and refuses `git gc` when gc's own worktree pruning would
deregister one. It sees through wrapper programs (`timeout`, `nice`,
`xargs`, `find -exec`, ...) and through an alias defined inline with `git
-c`. `git worktree list` is never blocked, and neither is `git worktree
add` in its ordinary forms (plain, or `-b` for a new branch) -- creating a
worktree is explicitly allowed. The one exception is `git worktree add -B
<branch>` naming a branch that already exists and points elsewhere: `-B`
force-creates, so that spelling moves the branch off its commits and is
refused like the other branch-moving forms.

**Do not treat that guard as the reason to stop thinking about this rule.**
It is defense in depth, not a boundary you can lean on:

- It can be switched off entirely. Setting
  `CADRE_DISABLE_WORKSPACE_MUTATION_GUARD=1` in the environment disables it
  before any parsing, so enforcement is conditional on an environment you
  do not control and cannot observe from inside a task.
- Other runners have no such guard at all, and this file is shipped to all
  of them.
- **Deleting a worktree directory with `rm` instead of a git verb is not
  covered and will not be.** The guard inspects `git` invocations; `rm` is
  not one. Deciding whether an arbitrary `rm` target is a registered
  worktree, for every `rm` an agent runs, is a much broader question than
  workspace isolation, and a guard that tried and half-succeeded would be
  worse than this stated boundary. `rm -rf <worktree-dir>` is the most
  likely real-world way this rule gets broken, and **for it the rule above
  is the only control.**
- Other things it cannot see include, but are not limited to: `git worktree
  add --force` over a path a registered worktree still occupies; a
  subcommand reached through an alias defined in a git CONFIG FILE (the
  inline `git -c` spelling is covered, a config-file one is not, because
  resolving it would mean trusting your git config); a command wrapped in a
  program outside the guard's list, which is deliberately not exhaustive
  (`firejail`, `runuser`, `doas`, ...); reflog expiry and `git gc`'s
  object-pruning surface; and inline shell nesting deeper than its bounded
  recursion limit.

The prohibition above is the rule. The guard catches some violations of it.

## No runner names as behavioral conditions

Every decision in this file is determined by running `git` commands and
reading resolved policy -- never by which coding-agent runner you are. Do
not branch your behavior on "if I am Claude Code" / "if I am Codex" / "if I
am Cline" or any other runner name. What tells you which situation you are
in is command output (`git rev-parse`, `git status`) and resolved policy
(`agent-autonomy.yaml`); the runner identity is never itself a condition
here.

## Isolating your own edits (write-capable tiers)

Everything from here to the end of this file binds write-capable tiers only,
per the applicability header above.

These sections govern one thing: **before you make your first edit, decide
whether to work in a dedicated `git worktree` instead of the caller's main
working tree, and say which you did.** It is prompt policy plus an
orchestrator dispatch-contract expectation, not a mechanically enforced gate
-- nothing in the dispatch pipeline blocks an edit that skips this. Follow it
because a silent choice here creates real review and audit risk: reviewers
and follow-up agents assume the main working tree reflects your work unless
you say otherwise, and an isolated-but-unreported change looks, from the
main tree, like nothing happened.

Every rule in `agent-autonomy.yaml` still applies unchanged.
`repository.create_local_branch_or_worktree: allowed` already covers creating
the worktree and branch described below; `commit: on_request`,
`push: on_request`, and `merge: never` are untouched -- this file does not
grant, imply, or expand any permission. Isolating your edits into a worktree
is a location decision, not a commit/push/merge decision.

## Step 0 -- Already isolated?

Before deciding anything, check whether you are already inside a linked
worktree rather than a repository's main working tree:

```sh
git rev-parse --git-dir
git rev-parse --git-common-dir
```

If the two paths differ, you are already in a linked worktree (the first
points at that worktree's private `.git/worktrees/<name>` administrative
directory; the second points at the shared repository `.git`). For example,
inside a worktree named `impl`, this looks like:

```
--git-dir:        /path/to/repo/.git/worktrees/impl
--git-common-dir: /path/to/repo/.git
```

If they differ: **use the worktree you are already in. Do not nest another
worktree inside it.** Report its path and branch in the end-of-task result
block below and skip Steps 1-2 entirely.

If the two paths are identical, you are in a main working tree (or a bare
non-worktree checkout) and Step 1 applies.

## Step 1 -- Can I isolate?

Isolate into a new worktree only when **all** of the following hold:

1. `git rev-parse --is-inside-work-tree` reports `true`.
2. The resolved `agent-autonomy.yaml` (`cadre resolve-shared
   agent-autonomy.yaml` -- a project overlay may have narrowed this)
   reports `repository.create_local_branch_or_worktree: allowed`.
3. `git status --porcelain` shows **no dirty paths that intersect the
   task's scope** (see "the dirty-scope guard" below for why this
   specific check, not a blanket "tree must be fully clean" check).

If all three hold, create the worktree in-root, at
`<repository_root>/.worktrees/<task-id>/<role-id>/`, from the repository
root:

```sh
git -C <repository_root> worktree add -b "agent/<task-id>/<role-id>" \
  ".worktrees/<task-id>/<role-id>" HEAD
```

Notes on that exact command:

- **In-root, not a sibling directory.** A worktree created as a sibling of
  the repository (the ordinary `git worktree` convention elsewhere) is
  unwritable in this environment: child agent processes are spawned with a
  sandbox scoped to the project root (for example, Codex's `--cd
  <project_root> --sandbox workspace-write`), so only paths under the
  repository root are writable at all. `.worktrees/` is git-ignored (see
  `.gitignore`) so it never pollutes `git status` or a commit.
- **Never `--detach`.** The worktree needs a real branch so work can be
  committed, reviewed, and handed off normally.
- **Never `-B` (force-create/reset the branch).** Plain `-b` surfaces an
  "already exists" error if the branch name collides with something, which
  is the correct outcome -- silently resetting an existing branch could
  discard work. Choose a different `<task-id>`/`<role-id>` pairing or escalate
  instead of forcing past that error.
- Base the worktree on `HEAD` of the working tree you are isolating from,
  not a remote ref, so it starts from exactly what you observed.

If isolation succeeds, make all edits inside the new worktree and report its
path, branch, and base revision in the end-of-task result block. Do not also
edit the main working tree for the same task.

## Step 2 -- Degrade explicitly

If any Step 1 condition fails, **do not isolate silently and do not fail
silently** -- edit in place in the working tree you were dispatched into, and
say so plainly in your result:

> Worktree isolation not used: `<reason>`. Edits were made in place at
> `<path>`.

Silence about this choice is itself a defect: a caller who expects isolation
by default and gets in-place edits without being told has an inaccurate
picture of where the deliverable lives.

## The dirty-scope guard, explained

`git worktree add ... HEAD` creates the new worktree from the last commit --
it does **not** carry uncommitted changes into the new worktree. If you were
dispatched specifically to fix or extend work-in-progress that exists only
as uncommitted changes in the main tree, and you isolate anyway, you isolate
yourself away from the exact changes you were sent to address. You would
then edit a clean checkout, report success, and leave the actual
work-in-progress in the main tree untouched and unreviewed -- a silent
failure that looks like a success.

This is why Step 1's dirty-tree condition is scoped to "dirty paths that
intersect the task's scope," not "the tree must have zero uncommitted
changes anywhere." An unrelated dirty file outside your task's scope (for
example, another in-progress teammate's edit under disjoint ownership) does
not by itself block isolation; a dirty file your task needs to build on
does.

## Teams: one shared worktree per team, not one per teammate

When a task dispatches multiple agents together (an Agent Team or an
ordinary parallel wave), isolate **once**, as a team, not once per
teammate:

- The team lead creates a single worktree for the team's shared task and
  passes its path to every teammate in their brief.
- Teammates edit inside that shared worktree, using the same disjoint
  per-path file ownership `operating-principles.md` already requires for
  parallel work ("keep file ownership exclusive per agent -- never edit a
  path another teammate owns for the same task").
- Do **not** create a separate worktree per teammate for the same task. Per
  teammate worktrees trade a review-catchable overlap (two teammates
  touching the same file inside one shared tree, visible in `git status`
  and in review) for silent divergence across N unmerged branches that no
  one is positioned to reconcile.

## Escalating

If you reach a point where only a human can resolve the choice (for
example: the dirty-scope guard is ambiguous, or a security-relevant
resolver's degraded behavior would materially change the task outcome and
you cannot tell whether that is acceptable), follow the standard blocking-
question convention: you are a dispatched subagent who cannot ask the human
directly, so stop and return a clearly labeled blocking question in your
result instead of guessing or proceeding.

## End-of-task result block (mandatory)

Every task governed by this file ends its result with this block, filled
in truthfully regardless of which path was taken:

```
Workspace isolation:
  mode: worktree | inherited-worktree | in-place
  path: <absolute path to the working tree actually edited>
  branch: <branch name, or "n/a" for in-place with no new branch>
  base revision: <commit the worktree/branch was created from, or "n/a">
  committed: yes | no
  reason (if in-place): <why Step 1 failed, or why isolation was otherwise skipped>
```

`mode` values:

- `worktree` -- you created a new worktree in this task (Step 1).
- `inherited-worktree` -- you were already inside a linked worktree and used
  it as-is (Step 0).
- `in-place` -- you edited the working tree you were dispatched into,
  without isolating (Step 2).
