# How much shared policy can safely be referenced instead of embedded

**Date:** 2026-08-10 · **Status:** answered, with a recommendation ·
**Measured at:** the second-wave execution-specialist branch (159 roles)

`docs/proposals/small-context-execution-roles-2026-08.md` leaves this open
decision unresolved:

> How small a context window this proposal optimizes for, and how much shared
> policy text can be safely referenced instead of embedded.

It also specifies the mechanism that would depend on the answer — a
"versioned policy envelope containing role ID, authority, allowed tools, task
path scope, relevant policy hashes, and **only task-relevant policy
excerpts**" — and makes it a prerequisite for the compaction the execution
specialists were introduced to deliver.

This records the measurement. **The answer is: not much, and the ceiling is
too low to justify the envelope generator on compaction grounds alone.**

## The finding that prompted it

A specialist's generated wrapper is not meaningfully smaller than the broad
role it accompanies:

| Wrapper | Lines | Bytes |
| --- | --- | --- |
| `plugin/agents/go-service-implementer.md` | 1006 | 58,887 |
| `plugin/agents/backend-engineer.md` | 1016 | 59,937 |

1.0% by line, 1.8% by byte. The role body is a rounding error against the
shared policy every wrapper carries.

## Where the 1007 lines go

Measured on `go-service-implementer`'s wrapper:

| Block | Lines | Excerptable? |
| --- | ---: | --- |
| Role body (`AGENT.md`) | 59 | n/a — already minimal |
| `workspace-isolation.md` | 323 | **No** for write-capable tiers |
| `team-profile.yaml` | 220 | **No** — project-wide |
| `agent-autonomy.yaml` | 77 | **No** — action-keyed, binds all |
| `documentation-style.md` | 33 | No |
| `knowledge-use-policy.md` | 22 | No |
| `operating-principles.md` | 17 | No |
| `technology-standards.md` | 91 | Partly — 10 `##` domain sections |
| `library-standards.yaml` | 165 | Partly — `golang:` is 142 of 165 |

**692 lines (69%) bind every write-capable tier** and cannot be excerpted
without removing policy the role is subject to. Only the two standards files
(256 lines, 25%) are domain-scoped.

## Why the big blocks cannot be trimmed

- **`workspace-isolation.md` (323 lines, the largest).** Its own applicability
  header already answers this: Steps 0–2 and the result block apply to *every*
  write-capable tier, and "Never mutate a working tree you did not create"
  applies to "every role, every tier, no exceptions." Every execution
  specialist is `code_author`, `test_author`, or `document_author` — all
  write-capable. The only tier that could drop the bulk of this file is
  `read_only`, which is reviewers, not the specialists the premise concerns.
  `generate_global_plugin.py`'s comment records that gating this file behind a
  write-capable tier was already tried and reverted as "coupled to the wrong
  thing."
- **`agent-autonomy.yaml` (77).** Keyed by action class, not role, with
  `default_rule: deny_unless_allowed_or_explicitly_authorized`. Excerpting it
  by role is precisely what its structure is designed to prevent.
- **`team-profile.yaml` (220).** Project-wide resolved standards. A role
  cannot know in advance which block it will need.

## The realistic ceiling

| Role | Safely excerptable | Reduction |
| --- | ---: | ---: |
| Theoretical maximum (needs no domain standards) | 256 lines | 25% |
| `react-component-implementer` (drops the `golang:` block) | ~222 lines | ~22% |
| `go-service-implementer` (keeps `golang:` and Go/Python) | ~83 lines | ~8% |

A Go specialist — the canonical example in the proposal — gets **8%**. That
is not a small-context prompt; it is the same prompt.

## Recommendation

1. **Do not build the envelope generator to chase compaction.** At an 8–22%
   ceiling it does not change which runners can host these roles, and every
   line it removes is a line the role was previously subject to. Omission risk
   is paid on every dispatch; the benefit is a fifth of a prompt at best.
2. **Stop justifying the execution specialists by context savings.** The
   defensible justification — recorded in `CHANGELOG.md` — is routing
   precision and a narrower authority envelope. Both are real and neither
   depends on this mechanism.
3. **If small-context hosting is genuinely required, the lever is retrieval,
   not excerpting.** `cadre resolve-shared <file>` already returns any shared
   policy verbatim on request, and the proposal's "policy hashes" idea fits
   that shape: ship a compact envelope naming the policies and their hashes,
   and have the role fetch what it needs. That is a different and larger
   change — it moves policy from guaranteed-present to fetched-on-demand, so
   it needs its own risk assessment, and a low-reasoning-tier role that
   silently skips the fetch is the failure mode to design against. It should
   not be attempted as an incremental trim of the current generator.
4. **The one excerpt that is safe and already documented** is dropping
   `workspace-isolation.md`'s Steps 0–2 and result block for `read_only`
   roles, whose applicability header already says they do not apply. That is
   ~240 lines off 28 reviewer wrappers. It does nothing for the execution
   specialists, and is worth doing only on its own merits.

   **Implemented (deagy/cadre#211), and smaller than this estimate.**
   `UNIVERSAL_POLICY_SECTIONS` in `generate_global_plugin.py` excerpts the
   file section by section for tiers outside `WRITE_CAPABLE_TIERS`, raising
   `PolicyExcerptError` rather than truncating if anything it names goes
   missing. Review of the first attempt corrected the scope: **four**
   sections bind every tier, not one. A read-only role creates worktrees
   too — inspection worktrees — so `The security-relevant-resolver rule`,
   `Never remove or prune a worktree yourself`, and `No runner names as
   behavioral conditions` bind it alongside the never-mutate rule. Only the
   steps about where *edits* land are write-capable-only.

   The realised saving is therefore ~175 lines per read-only wrapper, not
   the ~240 estimated above — which is the honest shape of this ceiling
   generally: the excerptable fraction shrinks once you check each section
   against what the role actually does rather than against its tool
   allowlist. Recommendation 1 stands unchanged. See
   `roster/shared/README.md`, "Section-granular excerpts".

## Reproducing

```sh
wc -l plugin/agents/go-service-implementer.md plugin/agents/backend-engineer.md
grep -n '^## ' roster/shared/technology-standards.md          # 10 domain sections
grep -nE '^[a-z_]+:' roster/shared/library-standards.yaml     # golang: 15-157 of 165
sed -n '1,20p' roster/shared/workspace-isolation.md           # applicability header
```

The per-block line counts come from the `# Shared policy:` separators
`generate_global_plugin.py` writes into each wrapper.
