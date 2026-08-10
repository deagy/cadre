# Investigation: does Cline have a per-role tool allowlist? (issue #129, Gap 1)

Status: **INVESTIGATION COMPLETE — Gap 1 as described in issue #129 is
already closed in the current tree. This document records the evidence and
recommends closing that half of the issue, not a new control.**
Task ID: `cline-tool-restriction-2026-08-09`
Classification: internal
Author role: ai-engineer (Cadre suite)
Requested by: repository owner (dispatched to investigate issue #129 Gap 1)
Required approver: Product Owner (to close/re-scope #129); no approval is
requested for a change here, since none is proposed.

## Headline finding

Issue #129's Gap 1 claims two things as of its filing (2026-08-08T17:28Z):
`runners.cline.has_generated_wrapper: false`, and "the Cline preset format
carries no per-role `tools:` line (confirmed:
`cline-plugins/cline-agents/agents/security-reviewer.md` has none)."

Both claims are false against the current repository, and were already false
one day *before* the issue was filed:

- `cline-plugins/cline-agents/agents/security-reviewer.md`, read directly,
  carries `allowedTools: [read_files, search_codebase]` in its frontmatter
  today. `git log -S"resolveToolPolicyConfig" -- cline-plugins/cline-agents/index.ts`
  shows this enforcement mechanism was introduced in commit `13deb5e` ("fix:
  move the Cline npm workspace out of `plugin/`", PR #121), merged
  **2026-08-07 22:14:54 -0700** — the day before issue #129 was opened.
  `git show df43bce:cline-plugins/cline-agents/agents/security-reviewer.md`
  (the commit immediately preceding it) already had the same
  `allowedTools:` line, so the mechanism predates even that PR in this
  history.
- `roster/runner-capabilities.json`'s `runners.cline.has_generated_wrapper`
  reads `true` in the current tree, not `false`. `git log -- roster/runner-capabilities.json`
  shows exactly one commit changed it: `ea4dc2b` ("fix: correct the false
  cline capability record (#148)", closing issue #143), merged
  **2026-08-09T02:57:56Z** — after #129 was filed but independent of it. Its
  own description states the manifest was simply stale ("described a runner
  that stopped existing at the monorepo merge") and that nothing but one test
  file consumed the field, so correcting it changed no runtime behavior — the
  underlying dispatch capability (`start_subagent`'s `preset:` argument) was
  already live.

I did not find a git blame explanation for why #129 asserted the opposite of
what was already committed at filing time; the most likely reading is that
whoever filed it read a stale local checkout or an earlier draft of
`security-reviewer.md`, not the tip of the branch it was filed against. I am
not asserting bad faith or guessing further — I am reporting what `git log`
shows against what the issue claims, which is the only way I could reconcile
the two.

## What I verified, and how

### 1. Can a Cline preset express a tool restriction? Yes — verified by reading three layers

**Preset frontmatter** (`cline-plugins/cline-agents/agents/security-reviewer.md`,
read directly): carries `allowedTools: [read_files, search_codebase]`. All 74
bundled presets carry this field; verified structurally, not by sampling one
file — `plugin/tools/port_cline_agents.py` line 283 derives
`allowed_tools = list(dict.fromkeys(TOOL_MAP[t] for t in tools))` from the
*source* Cadre role's `tools:` frontmatter for every role it emits, and
`plugin/tools/test_port_cline_agents.py`'s
`test_agents_reproduce_committed_content_exactly` fails the build if any
generated file drifts from what's committed. There is no per-file opt-out.

**Porting tool's tool-name mapping** (`port_cline_agents.py:67-74`):

```python
TOOL_MAP = {
    "Read": "read_files",
    "Grep": "search_codebase",
    "Glob": "search_codebase",
    "Bash": "run_commands",
    "Edit": "editor",
    "Write": "editor",
}
```

This is a coarsening, not a 1:1 mapping — `Edit`/`Write` both collapse to
`editor`, `Grep`/`Glob` both collapse to `search_codebase`. PR #149's own
cross-runner parity test documents this explicitly and deliberately excludes
Cline from the exact tool-list comparison it runs between Claude Code and
Codex, comparing Cline only on the coarser "is this role write-capable or
not" axis. This is a real, stated precision limit, not a gap in enforcement.

**Runtime translation** (`cline-plugins/cline-agents/index.ts:763-778`,
`resolveToolPolicyConfig`): translates `allowedTools` into a deny-by-default
`toolPolicies` map (`{"*": {enabled: false}, <each allowed tool>: {enabled:
true}}`), and — for a preset whose `allowedTools` contains none of
`run_commands`/`editor`/`apply_patch` (i.e., genuinely read-only, 28 of the
74 roles, confirmed by `grep -rl '^capability: read_only'
roster/*/*/AGENT.md | wc -l` = 28) — also sets `mode: "plan"` as an
additional guard. This is exercised by `cline-plugins/cline-agents/index.test.mts`
(`resolveToolPolicyConfig` unit tests at lines 361-390), which assert the
wildcard-deny shape, the per-tool allow overrides, the `mode: "plan"`
defense-in-depth for read-only roles, and that a preset with no declared
`allowedTools` gets no restriction at all (see residual risk below).

### 2. Does the Cline runtime offer an equivalent to a `PreToolUse` hook? Yes, a different mechanism — `toolPolicies` + `mode: "plan"`, both real SDK constructs

I checked this against the actual installed `@cline/sdk`/`@cline/core`
package under `cline-plugins/node_modules/@cline/`, not just the plugin's own
comments claiming it:

- `grep -c "toolPolicies" cline-plugins/node_modules/@cline/core/dist/index.js`
  returns 12 — the field is real, used by the SDK's own bundled runtime, not
  invented by this plugin.
- The bundled SDK's export list (same file) includes
  `MY as createToolPoliciesWithPreset`, `Y1 as ToolPresets`,
  `R0 as DefaultToolNames` — names that correspond to what `index.ts`'s
  comments cite (`packages/core/src/runtime/orchestration/runtime-builder.ts`'s
  `isToolEnabledByPolicies`/`filterToolsByPolicies`, and
  `packages/core/src/extensions/tools/presets.ts`'s `"plan"` preset).
- The bundled SDK's `ChatSessionConfigSchema` (`DU` in that same minified
  bundle) declares `mode: z.enum(["act","plan"]).default("act")` — confirming
  `"plan"` is a real, schema-validated session mode, not a string this
  plugin invented and hoped the runtime would honor.

**What I could not verify:** whether the live `@cline/core` session runtime
actually *enforces* `toolPolicies`/`mode: "plan"` at tool-dispatch time when
a model attempts a denied tool call — as opposed to merely accepting the
config shape. `cline-plugins/cline-agents/index.test.mts` mocks `ClineCore`
(`vi.fn().mockImplementation(...)`) specifically because, per its own
comment, "a real ClineCore session requires a live, model-backed call." So
this repository's test suite verifies the plugin *constructs* the correct
policy object and passes it to `start()`; it does not exercise the SDK
actually blocking a denied tool call end-to-end. I am treating that as a
genuine "unknown, not guessed" rather than asserting enforcement is proven —
the evidence for actual runtime enforcement is reading the SDK's own
minified source and its schema (both point the same direction as the
plugin's claims), not an executed integration test in this tree.

### 3. Honest options, given the above

Since Gap 1's premise (no restriction exists) does not hold, this section is
about the residual gaps that *do* still exist, not about building a control
from scratch:

**a. Runtime guard inside the Cline plugin's own tool surface — already
built, cost already paid.** `resolveToolPolicyConfig` plus `mode: "plan"` is
exactly this option, already implemented and unit-tested (see above). No
further cost to incur here; the remaining cost is the unverified live-runtime
question above, which requires either a live model-backed integration run
(outside what I can do without network/credentials) or is upstream's
responsibility to have implemented correctly per its own schema.

**b. Narrowing what gets dispatched — a real residual gap exists here, and
is the one thing actually worth fixing.** `resolveToolPolicyConfig` returns
`{}` (no restriction, full ambient tool access) when a preset declares no
`allowedTools` at all — this is documented, deliberate upstream-template
compatibility behavior ("preserving the upstream template's default
full-tool behavior for a hand-authored custom preset that never opted into
this field"), not a bug. It means:
  - All 74 **bundled** Cadre-role presets are covered (`allowedTools` always
    set by the generator) — the specific case #129 raised, closed.
  - A **global** preset (`~/.cline/data/settings/agents/`) or a **project**
    preset (`<workspaceRoot>/.cline/agents/`) that a human writes by hand and
    forgets to add `allowedTools:` to gets full ambient tool access,
    unrestricted, by design of this port's fidelity to the upstream
    template. This is a real, currently-undocumented-as-a-risk gap, but it
    is a *human-authored custom preset* gap, not a Cadre-catalog-role gap —
    out of #129's stated scope, which was specifically about the 74 ported
    roles.

**c. Documenting accepted risk — appropriate for the live-runtime-enforcement
unknown in 2(above), not for the closed part of Gap 1.** The honest
documentation move here is not "Cline has no control, accept the risk" (that
premise is false) but "the control's construction is verified by source
reading, not by an executed live integration test, because that test needs a
live model-backed session this environment cannot run" — record that
explicitly rather than either overclaiming full verification or repeating
#129's now-incorrect premise.

### 4. Actual exposure today

- **Which capability tiers reach Cline:** all of them — the port has no
  tier-based exclusion; every role in `roster/catalog.yaml` gets a preset
  (74 presets = 74 catalog roles, enforced by
  `plugin/tools/test_port_cline_agents.py` and PR #149's parity test).
- **What each can do:** gated by `allowedTools` → `toolPolicies`, coarsened
  to Cline's four canonical tool names (`read_files`, `search_codebase`,
  `run_commands`, `editor`) plus `mode: "plan"` for the 28 read-only roles.
  A `document_author`/`code_author`/`test_author`/`environment_operator`-tier
  role (the write-capable tiers per `roster/runner-capabilities.json`) maps
  to `run_commands`/`editor` being present in its `allowedTools`, same as it
  would need `Bash`/`Edit`/`Write` on Claude Code or `sandbox_mode !=
  read-only` on Codex.
- **Is the destructive-git incident behind #128/#129 (a write-capable role
  running `git reset --hard` in a caller's tree) reachable on Cline today?**
  In principle, yes, on the same terms it is reachable on Claude Code or
  Codex: `git`/`run_commands` access is a **tool-category** grant
  (`run_commands` is not further scoped to a git subcommand allowlist on any
  of the three runners, per my reading of `runner-capabilities.json` and this
  plugin's tool mapping), and `workspace-isolation.md`'s "Never mutate a
  working tree you did not create" rule is prompt policy layered on top of
  that grant, identically on all three runners — this file's own "No runner
  names as behavioral conditions" section says the rule is meant to bind
  every runner equally, and nothing in the Cline `toolPolicies` mechanism is
  git-subcommand-aware. In other words: **Gap 1 (tool-category
  restriction) is closed; the deeper concern Gap 2's sibling text points at
  (no runner mechanically distinguishes `git status` from `git reset
  --hard`) is not, and is unaffected by anything in this investigation** —
  it is the same open question the other agent's Claude Code `PreToolUse`
  hook work is addressing for Claude Code only, and I confirm here that
  nothing in Cline's `toolPolicies`/`mode: "plan"` construct substitutes for
  it either, since both operate at the tool-name level, not the
  argument/subcommand level.

## What remains open (not resolved by this investigation)

1. **Live-runtime enforcement is source-verified, not integration-tested.**
   Whether `@cline/core`'s actual session loop honors `toolPolicies`/`mode:
   "plan"` at the moment a model attempts a denied call was not exercised
   end-to-end in this repository's test suite (it mocks `ClineCore`
   deliberately, per its own comment, because that requires a live
   model-backed session). I read the shipped SDK's own minified source and
   schema and found consistent, non-fabricated support for the claims
   `index.ts`'s comments make, but that is not the same as watching a denied
   call actually get refused.
2. **Global/project custom presets with no `allowedTools` get full ambient
   access.** Documented behavior, not a defect in the 74 bundled roles, but
   worth a line in `cline-plugins/cline-agents/README.md` if it doesn't
   already carry one warning a project-preset author to set `allowedTools`
   explicitly. (I did not find such a warning in the README section I read;
   I did not exhaustively re-read the whole file for one, so I am not
   asserting its absence with full confidence.)
3. **Subcommand-level git restriction is out of scope for `toolPolicies`
   entirely**, on every runner, and is the actual live gap behind #129's
   framing, not the tool-category question Gap 1 asked. This is squarely
   the prompt-policy-plus-no-structural-backing situation
   `workspace-isolation.md` already names as such.

## Recommendation

Ask the Product Owner to **narrow issue #129's Gap 1** rather than build a
new control against it: the specific claim it makes (no per-role tool
allowlist exists for Cline) is false against the current tree and has been
false since before the issue was filed, with the `runner-capabilities.json`
half of it separately and independently corrected by PR #148 the next day.
Closing Gap 1 as "already resolved, evidence attached" avoids duplicate work
against a premise that no longer holds.

If the Product Owner wants residual risk tracked rather than dropped, the
two items worth carrying forward are open item 1 above (record as an
accepted, documented gap: source-verified but not live-integration-verified,
revisit if/when a live-model integration test becomes feasible) and open
item 2 (a one-line README addition warning custom preset authors to set
`allowedTools` explicitly, which I have not made since this task's
deliverable is this document only). Item 3 is not new information — it
restates, for Cline specifically, the same "no runner mechanically
distinguishes safe git reads from destructive git writes" gap #129's own
text already flagged in general terms, and is unaffected by anything found
here.

## Files read as evidence (paths, for follow-up verification)

- `cline-plugins/cline-agents/agents/security-reviewer.md`
- `cline-plugins/cline-agents/README.md`
- `cline-plugins/cline-agents/index.ts` (lines ~1-120, ~460-540, ~730-800,
  ~1240-1290)
- `cline-plugins/cline-agents/index.test.mts` (lines ~340-390, ~618-650)
- `plugin/tools/port_cline_agents.py` (lines 1-130, 283-296)
- `plugin/tools/test_port_cline_agents.py`
- `roster/runner-capabilities.json`
- `roster/review/security-reviewer/AGENT.md`
- `cline-plugins/node_modules/@cline/core/dist/index.js` (grep only, not
  fully read — a generated/minified bundle)
- `git log`/`git show` against `13deb5e`, `df43bce`, `ea4dc2b`, `d7823a5`,
  `00cdf58`; `gh issue view 129`; `gh pr view 148`, `gh pr view 149`
