# Three agent-authored changes merged to `main` without a human approver

**Date:** 2026-08-11 · **Status:** recorded, not remediated ·
**Applies to:** PRs [#226](https://github.com/deagy/cadre/pull/226),
[#230](https://github.com/deagy/cadre/pull/230),
[#231](https://github.com/deagy/cadre/pull/231)

## What happened

Twelve issues ([#217](https://github.com/deagy/cadre/issues/217)–[#229](https://github.com/deagy/cadre/issues/229))
were implemented, reviewed, and merged in a single session. Every commit was
authored by an agent. Every review was performed by an agent. Every merge was
executed by an agent. The repository owner authorized the merges in the
session, in conversation, and that authorization is the only human act in the
chain — there is no review approval recorded on any of the three pull
requests.

## Why this is worth a record

`CLAUDE.md` and `AGENTS.md` both state authorship/approval separation as a
hard invariant: "An agent that materially changes an artifact cannot approve
that same artifact." Elsewhere in this workspace the invariant is enforced
*structurally* — `agentic-sdlc`'s `validate_repository()` rejects a config
where the same identity is author and reviewer, and
`roster/orchestration/test/test_repository_health.py` fails the build if a
role defined under `review/` is not `read_only`.

Here it was satisfied by neither. It was waived by conversation, and nothing
in the repository recorded that until this file. That gap is the point: a
policy that holds only when someone remembers it is the failure mode this
repository's own operating principles describe — *"When a claim about how
something works can be checked against the thing itself, check it there."*

The reviews that did run were genuine and did find real defects — the
security review caught an alias-expansion bug that let
`git -c alias.g='-c gc.worktreePruneExpire=now gc' g` through a guard while
real `git` pruned, which no test covered because *both* implementations were
wrong identically. But an agent review of agent-authored work is a quality
control, not an approval. It does not satisfy separation, and it should not
be cited as though it does.

## What this document is not

**This is a self-recorded claim, not approval evidence.** It was written by
the same agent line that authored and merged the changes it describes, which
is precisely the objection
`docs/proposals/human-authority-role-agents.md` raises about its own decision
record. The authoritative history is the three PRs' own commit and merge
metadata — which shows, accurately, that no human approved them.

Read this as context for why that history looks the way it does. Do not read
it as retrospective authorization; an agent cannot supply that for its own
work, and writing it down does not change that.

## What would make it not recur

Options, in rough order of cost:

1. **Branch protection requiring one approving review on `main`.** The
   cheapest structural fix, and the only one that does not depend on an agent
   choosing to comply. Note it must be configured to not accept the PR author
   as the approver.
2. **A recorded, time-boxed exception** when a human deliberately wants to
   merge agent-authored work unreviewed — so the waiver is an artifact rather
   than a conversation, and so its frequency is visible.
3. **Leaving it as is**, on the reasoning that this repository has one
   maintainer who is also the declared Product Owner, so the separation
   invariant is about the *suite's* dispatch behavior for consumer projects
   rather than about this repository's own commits.

Option 3 is a defensible reading and may well be the intended one — but it is
worth stating deliberately rather than arriving at by default, because
`roster/shared/agent-autonomy.yaml` and `orchestration/escalation-policy.md`
are both written as though the invariant applies here too. If option 3 is the
answer, those documents should say so explicitly, and this file should be
replaced by that statement.

No decision is recorded here. The choice belongs to the repository owner.
