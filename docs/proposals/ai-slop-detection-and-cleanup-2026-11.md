# Proposal: detect and clean up AI-slop text so output reads as hand-written

Status: **APPROVED by the Product Owner on 2026-11-05. Phase 1 in progress.**
Task ID: `ai-slop-detection-and-cleanup-2026-11-05`
Classification: internal
Requested by: repository owner / declared Product Owner
Approver: Product Owner (approved 2026-11-05, decisions recorded below).

## Problem

Text that cadre agents and contributors produce (docs, commit and PR/MR text,
agent output, code comments) carries recognizable machine tells: em dashes,
"not X, it's Y" constructions, recap endings, filler vocabulary, sycophancy,
uniform rhythm. The goal is that shipped text reads as if a person wrote it, in
the owner's own voice.

Nothing in cadre addresses this today. Prototypes exist outside the repo, in the
owner's second-brain vault: `slop-gate` (deterministic regex scanner, exit 0/1,
`--hook` mode), `no-ai-slop` (editing skill, detect and edit modes) and
`voice-baseline` (counts a writing corpus for over-used words and shapes). This
proposal productizes them inside cadre.

## Decisions already made

| Question | Decision |
| --- | --- |
| Form factor | Go CLI command **and** a skill |
| Text covered | Markdown prose, commit/PR/MR text, agent-produced output, code comments |
| Cleanup mode | Full agent rewrite of flagged text |
| Voice target | Personal voice baseline measured from the owner's corpus |
| Hard-tell configuration | One default list shipped by cadre; a repo may disable a rule by id with a recorded reason, shown in reports. No custom rule lists in v1 |
| Second reviewer | Opt-in `--review-with <runner>` flag on `fix`, off by default; intended to be a different model family from the rewriter |
| Rules source of truth | Cadre. The vault's `slop-gate` reads `roster/shared/slop-rules.yaml` instead of keeping its own copy |

## Non-goals

- Not a general AI-authorship detector. It flags tells; it does not score
  "probability this was AI-written" and must never be presented as proof of
  authorship.
- Not a style linter for correctness, grammar or terminology.
- No auto-publish. Rewrites are proposed as diffs and applied only on approval.

## Design

### 1. Detection: `cadre slop scan`

Deterministic, offline, no model calls. Lives in a new `internal/slop` package
(reusing `internal/textutil` where it fits) with a thin command in `internal/cli`.

- Inputs: file paths, globs, stdin (`-`), `--git-range A..B` (scans commit
  messages and the added lines of the diff).
- Surface-aware extraction so one engine covers every text kind:
  - `markdown`: prose only; fenced code, inline code, link targets and quoted
    spans are stripped before matching (a document may quote a tell as an
    exhibit).
  - `commit`: subject and body; conventional-commit prefixes are ignored.
  - `code-comment`: comments and docstrings extracted per language (Go, Python,
    TS/JS, YAML, shell to start); code is never scanned.
  - `agent-output`: any of the above, tagged by the caller (`--surface`).
- Rule classes:
  - **Hard tells**: one hit fails (em dash, "not X, it's Y" and trailing
    "X, not Y", rhetorical and "The <Noun>" headings, verdict kickers,
    sycophancy openers, recap endings, honesty framing).
  - **Counted tells**: filler vocabulary that is legitimate once; fails at a
    configurable count (default 3 per piece).
  - **Voice drift** (needs a baseline, see section 3): words or shapes used
    markedly above the owner's measured rate.
- Rules are data (`roster/shared/slop-rules.yaml`), versioned, each with an id,
  class, pattern, rationale and a worked example, so a hit is explainable and a
  rule can be disabled per repo.
- Exemptions: `slop-ok: <reason>` comment exempts a file or region; a reason is
  mandatory and listed in the report.
- Output: human text by default, `--format json` for tooling. Each hit carries
  file, line, rule id, class, matched span and surrounding context.
- Exit codes: 0 clear, 1 tells found, 2 usage or I/O error. `--hook` answers a
  Claude Code PreToolUse/Stop payload with a permission decision, matching
  slop-gate's behavior.

Limit, stated in the command help: regexes cannot see structural slop (invented
frameworks, restatement, uniform rhythm). Those are the rewrite step's job and a
human reviewer's.

### 2. Cleanup: `cadre slop fix` and the `ai-slop-cleanup` skill

Full agent rewrite, per decision. Safety comes from process, not from limiting
the rewrite.

- `cadre slop fix <paths>` does not call a model itself (cadre is runner-agnostic
  and holds no model credentials). It runs `scan`, then emits a **rewrite brief**
  per flagged unit: the original text, the hit list, the voice baseline summary,
  and hard constraints (preserve facts, numbers, names, links, code and
  structure; change wording only).
- The skill (`.agents/skills/ai-slop-cleanup/`, pointer in `.claude/skills/`)
  consumes the briefs, performs the rewrite in whichever runner is active, and
  writes results as a unified diff or patch set. It never edits in place.
- `cadre slop fix --apply <patch>` applies only after the verification below
  passes and the user confirms.
- **Verification loop (mandatory, deterministic):**
  1. Rescan the rewritten text; it must have zero hard tells and be under the
     counted threshold.
  2. Fact-preservation check: every number, identifier, URL, path, version and
     code span in the original must appear unchanged in the rewrite; headings and
     list structure must match unless `--allow-restructure`.
  3. Length drift guard: rewrite within a configurable band of the original
     (default plus or minus 25%), so cleanup cannot become summarizing.
  4. Rewrite attempts are capped (default 3). After the cap the unit is reported
     as unresolved for a human, never silently accepted.
- Commit messages and already-pushed history are never rewritten; for those,
  `scan` reports and `fix` produces a suggested replacement message only.
- A rewrite pass is not a substitute for review: the report states plainly that
  the checks are mechanical.

### 3. Voice baseline: `cadre slop baseline`

- `cadre slop baseline <corpus paths...>` counts word and sentence-shape
  frequencies and writes `slop-baseline.json` (word n-grams, sentence-length
  distribution, punctuation habits, opener patterns) with the corpus manifest and
  file hashes. Port the logic from `voice-baseline`'s `census.py` into Go.
- Baseline location: user scope by default (`~/.config/cadre/slop-baseline.json`),
  because it describes a person and never a repository. Repo-scope override allowed,
  but the corpus itself is never committed or copied.
- Used two ways: (a) as a drift rule in `scan` (over-used words flagged relative
  to the owner's norm, which also stops the rewrite from amplifying the owner's
  own tics into a caricature), and (b) as target style context in rewrite briefs.
- Privacy: the corpus is read locally; briefs contain only aggregate statistics
  and short exemplar sentences the owner opted into, never whole documents.
- Without a baseline the commands run in generic mode (rules only) and say so.
- Re-measure prompt after N new pieces (default 10) via `cadre doctor`.

### 4. Integration points

- `cadre doctor`: reports rule-file version, baseline age and whether a hook is wired.
- CI: `cadre slop scan --git-range origin/main..HEAD` as an optional job in
  `validate.yml`, advisory at first.
- Workflows: roster workflows that produce prose (release notes, runbooks,
  changelog entries) gain an optional "slop scan" step before handoff. Wiring
  into lifecycle gates is deferred until the false-positive rate is known.
- Hook mode for Claude Code / Codex, opt-in, never installed by default.

## Phasing

1. **Scan** (generic rules, markdown + commit surfaces, json output, tests with
   a golden corpus of slop and clean text). Port rules from `slop-gate`.
2. **Code-comment and agent-output surfaces**, `--hook` mode, `slop-ok`.
3. **Baseline** command and drift rule.
4. **Fix**: rewrite briefs, skill, verification loop, `--apply`.
5. **Integrations**: doctor, CI job, workflow steps.

Each phase ships independently and is useful alone.

## Acceptance criteria

- AC-1: `scan` exits 1 on a fixture containing each hard tell and 0 on a clean
  fixture; every rule has a positive and a negative test.
- AC-2: Text inside code fences, inline code and quotes never produces a hit.
- AC-3: Code comment extraction never scans code tokens (tested per language).
- AC-4: `scan --git-range` reports commit-message hits with the commit SHA.
- AC-5: A rewrite that drops or alters any number, URL, path or identifier from
  the original is rejected by the fact-preservation check.
- AC-6: A rewrite outside the length band, or still carrying a hard tell, is
  rejected; after 3 attempts the unit is reported unresolved.
- AC-7: `fix` never modifies a file without `--apply` and confirmation, and
  never touches pushed commit history.
- AC-8: Without a baseline, commands run in generic mode and report that.
- AC-9: The baseline file contains aggregate statistics and opt-in exemplars
  only; no verbatim corpus documents.
- AC-10: Rules are loaded from `roster/shared/slop-rules.yaml`; a malformed rule
  file fails closed with a clear error.

## Risks and open questions

- **False positives on legitimate text.** Em dashes and "not X, but Y" are valid
  writing. Mitigation: `slop-ok` with reason, per-rule disable, advisory-first CI.
- **Arms race.** A fixed tell list ages as models change. Rules are data with a
  version; budget a periodic review.
- **Rewrite quality.** Mechanical checks catch fact loss, not blandness or lost
  meaning. A human still reviews the diff. The optional second-model reviewer
  (`--review-with`) covers part of this gap when enabled.
- **Voice overfitting.** A rewrite pushed hard toward the baseline can imitate
  the owner's habits too literally. Drift rule is relative, not a style
  template; test with the "does it sound like a caricature" check from
  `voice-baseline`.
- **Language scope.** English only in v1; rules and baseline are English-specific.
- **Vault skills depend on cadre's rules file.** `slop-gate` must read the
  cadre path (or a vendored copy refreshed by script); decide the mechanism in
  phase 1.

## Out of scope for this proposal

Model selection for the rewrite, a hosted service, authorship attribution,
non-English text, rewriting binary or generated files.
