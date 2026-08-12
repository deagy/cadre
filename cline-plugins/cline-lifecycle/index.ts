import { execFile } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { z } from "zod";
import { type AgentPlugin, createTool } from "@cline/sdk";
import { safeJsonStringify } from "@cline/shared";

const execFileAsync = promisify(execFile);

// Same resolution convention as cline/index.ts's CADRE_BIN: relative to this
// plugin module's own location (this plugin sits at cline-plugins/cline-lifecycle/,
// a sibling of cline/ and cline-agents/), never relative to the target
// workspace. Two levels up reaches the repository's own bin/cadre.
const PLUGIN_DIR = path.dirname(fileURLToPath(import.meta.url));
const CADRE_BIN = path.resolve(PLUGIN_DIR, "..", "..", "bin", "cadre");

// ---------------------------------------------------------------------------
// About this plugin
// ---------------------------------------------------------------------------
//
// `bin/cadre sdlc` is a thin pass-through to the separately-installed
// `agentic-sdlc` kernel binary (see CLAUDE.md's "Architecture Notes" --
// gate state and transitions live entirely in that external kernel; this
// repository's role selection never executes gates itself). The
// `cadre-lifecycle-core`/`-github`/`-gitlab` plugins already expose G1-G10
// lifecycle governance on Claude Code / Codex, but only as conversational
// skills (lifecycle-onboarding, lifecycle-review, brief-pending-gates) --
// skills are a Claude Code / Codex mechanism with no Cline equivalent (see
// skills/run-agent-orchestration/references/runner-adapters.md's "## Cline"
// section), so none of that is reachable from Cline today. This plugin
// closes that gap the same way `cline/` and `cline-agents/` already do for
// role selection/dispatch: deterministic tool calls wrapping the exact
// `bin/cadre sdlc <subcommand>` invocations those skills already document
// (see plugins/lifecycle/skills/{lifecycle-onboarding,lifecycle-review,
// brief-pending-gates}/SKILL.md), turning a conversational flow into 5
// direct tool calls: sdlc_init, sdlc_validate, sdlc_plan, sdlc_status,
// sdlc_decide. sdlc_plan wraps `agentic-sdlc plan`, the fallback command 13
// forge-specific SKILL.md files point to in passing ("... or `cadre sdlc
// plan` first") when a task-id has no run record yet -- it was never given
// a numbered step of its own in any skill, so this tool's description cites
// that same fallback phrasing rather than a specific step.
//
// This plugin does no interpretation of its own beyond argument-building and
// JSON pass-through -- in particular sdlc_decide never adds its own
// preparer/verifier separation logic, because the kernel already refuses a
// decision from the same identity as the gate's preparer/verifier
// structurally (see CLAUDE.md's "Human approval invariant"); this tool only
// relays whatever the kernel decides, success or refusal.
//
// The 5 tools above are forge-agnostic. `cadre-lifecycle-gitlab`/`-github`
// additionally bundle 8 forge-specific skills each (lifecycle-review-gitlab,
// link-source-issue-gitlab, etc. -- see plugins/lifecycle-{gitlab,github}/
// skills/) for Claude Code / Codex, driving forge-specific kernel
// subcommands the 5 tools above have no access to. This plugin closes that
// same gap with the tool calls below, one per forge-specific kernel
// subcommand, following the exact same convention throughout: deterministic
// argument-building and JSON pass-through, no approval/linking/publishing
// logic of this plugin's own. `brief-pending-gates-gitlab`/`-github` are the
// only two forge-specific skills NOT mirrored below -- both just wrap
// `bin/cadre sdlc status`, i.e. exactly what `sdlc_status` above already
// calls, so there is nothing new to wrap.
//
// `link-source-issue-github` was previously the one other unmirrored skill:
// its two subcommands (`link-intent-from-github-issue`,
// `link-requirements-from-github-issue`) were, like the 10 listed below,
// missing from the stale kernel this plugin was first developed against, so
// they were left out rather than shipped unverified. `provider.json` now pins
// `kernel_compatibility.minimum` past that, both are live-verified, and
// `sdlc_link_intent_from_github_issue`/`sdlc_link_requirements_from_github_issue`
// close it -- the GitHub and GitLab link-source pairs are now symmetric.
//
// Flag coverage is deliberately total per subcommand, not a convenient
// subset: a flag a skill documents but a tool omits is the same gap in a
// less visible form, since a Cline session cannot fall back to a hand-run
// CLI the way a Claude Code / Codex session reading the SKILL.md can. The two
// exceptions, both on `sdlc init`: `--force`, which the kernel itself
// documents as "reserved for future use; in this release init never
// overwrites existing wrapper or managed overlay files, with or without
// --force" (a no-op, and the `--force` the onboarding SKILL.md files
// reference is `bin/cadre init`'s, a different command), and `--extension`,
// which no lifecycle SKILL.md drives -- adding it would exceed Claude/Codex
// parity rather than reach it. Every `--i-know-this-is-mocked` is likewise
// omitted: it is a test-harness interlock, required only when
// AGENTIC_SDLC_TEST_ISSUE_CREATE_FILE is set, and exposing it to a model
// would hand it a way to make a mocked run look real.
//
// Several of the tool calls below (everything from sdlc_list_gate_issues_gitlab
// through sdlc_publish_reviewer_nudge) wrap kernel subcommands
// (`create-gate-issues`, `list-gate-issues`, `create-github-gate-issues`,
// `list-github-gate-issues`, `publish-gate-status`, `list-gate-status`,
// `request-gate-reviewers-gitlab`, `request-gate-reviewers`,
// `publish-reviewer-nudge`, `list-reviewer-nudge`) that were, when these
// tools were first added, missing ("invalid choice") from the `agentic-sdlc`
// version this plugin's development environment had installed, despite
// being documented by the packaged `plugins/lifecycle-{gitlab,github}/
// skills/*/SKILL.md` files and within this repository's declared
// `kernel_compatibility` range -- traced upstream to `agentic-sdlc`'s own
// VERSION constant not having been bumped across 9 tagged releases that
// actually shipped these subcommands (fixed in `deagy/agentic-sdlc` v0.13.0;
// see that repo's `agentic_sdlc/__init__.py` VERSION comment). This
// repository's own `provider.json` now pins `kernel_compatibility.minimum`
// to that fixed release, and every one of these 10 subcommands has been
// live-verified against it. This was never a Cline-specific gap: Claude
// Code and Codex hit the identical "invalid choice" error running the exact
// same commands their own skills document, against the same stale kernel.

// ---------------------------------------------------------------------------
// Schemas
// ---------------------------------------------------------------------------

const RootInput = z.string().min(1).describe("Target project root. Defaults to the workspace root.");

const SdlcInitInput = z
  .object({
    root: RootInput.optional(),
    profile: z.string().min(1).describe("Resolved Agentic SDLC profile name (required)."),
    projectId: z.string().min(1).describe("Project identifier slug (required)."),
    classification: z.string().min(1).describe("Resolved data classification (required)."),
    runner: z
      .enum(["claude", "codex", "both"])
      .optional()
      .describe(
        "Write generated Claude Code / Codex subagent wrapper files into the target project. Omit " +
          "for a project that is itself the source of those wrappers rather than a consumer of them.",
      ),
    dryRun: z
      .boolean()
      .optional()
      .describe("Preview what init would create/change without writing anything. Recommended first call."),
  })
  .strict();

const SdlcValidateInput = z.object({ root: RootInput.optional() }).strict();

const SdlcPlanInput = z
  .object({
    root: RootInput.optional(),
    taskId: z.string().min(1).describe("Task ID to create (or overwrite) a dispatch plan and pending run record for (required)."),
    task: z.string().min(1).describe("Task objective used for routing (required)."),
  })
  .strict();

const SdlcStatusInput = z
  .object({
    root: RootInput.optional(),
    taskId: z.string().min(1).describe("Task ID whose lifecycle gate status to report (required)."),
  })
  .strict();

const SdlcDecideInput = z
  .object({
    root: RootInput.optional(),
    taskId: z.string().min(1).describe("Task ID the decision applies to (required)."),
    gate: z.string().min(1).describe("Gate ID, e.g. G1 (required)."),
    role: z.string().min(1).describe("Authority role deciding this gate (required)."),
    decision: z.string().min(1).describe("Decision verb the kernel accepts for this gate (required)."),
    actorId: z.string().min(1).describe("Identity of the human recording this decision (required)."),
    evidenceUri: z
      .string()
      .min(1)
      .describe("Evidence URI backing this decision, e.g. a PR/issue/doc reference (required)."),
    note: z.string().min(1).optional().describe("Optional rationale note."),
    decidedAt: z.string().min(1).optional().describe("Optional RFC3339 timestamp; defaults to kernel's own clock."),
  })
  .strict();

const SdlcApproveFromGitlabInput = z
  .object({
    root: RootInput.optional(),
    taskId: z.string().min(1).describe("Task ID the approval applies to (required)."),
    gate: z.string().min(1).describe("Gate ID, e.g. G1 (required)."),
    role: z.string().min(1).describe("Authority role recording the approval (required)."),
    projectPath: z.string().min(1).describe("GitLab project path, namespace/project (required)."),
    mrIid: z.number().int().positive().describe("Merge request internal ID, iid (required)."),
    approvalId: z.string().min(1).describe("GitLab approval identifier (required)."),
    approverUsername: z.string().min(1).describe("GitLab username that authored the approval (required)."),
    commitSha: z.string().min(1).describe("Commit SHA reviewed by the GitLab approval (required)."),
    decidedAt: z.string().min(1).optional().describe("Optional RFC3339 approval time; defaults to now."),
  })
  .strict();

const SdlcApproveFromGitlabMrInput = z
  .object({
    root: RootInput.optional(),
    taskId: z.string().min(1).describe("Task ID the approval applies to (required)."),
    gate: z.string().min(1).describe("Gate ID, e.g. G1 (required)."),
    role: z.string().min(1).describe("Authority role recording the approval (required)."),
    projectPath: z.string().min(1).describe("GitLab project path, namespace/project (required)."),
    mrIid: z.number().int().positive().describe("Merge request internal ID, iid (required)."),
    approverUsername: z
      .string()
      .min(1)
      .optional()
      .describe("GitLab username to match; defaults to the authority's GitLab binding."),
    commitSha: z
      .string()
      .min(1)
      .optional()
      .describe("Optional commit SHA to require when selecting an approved approval."),
  })
  .strict();

const GitlabIssueLinkInput = z
  .object({
    root: RootInput.optional(),
    taskId: z.string().min(1).describe("Task ID the link applies to (required)."),
    role: z.string().min(1).describe("Authority role recording the link (required)."),
    projectPath: z.string().min(1).describe("GitLab project path, namespace/project (required)."),
    issueIid: z.number().int().positive().describe("Issue internal ID, iid (required)."),
  })
  .strict();

const GithubIssueLinkInput = z
  .object({
    root: RootInput.optional(),
    taskId: z.string().min(1).describe("Task ID the link applies to (required)."),
    role: z.string().min(1).describe("Authority role recording the link (required)."),
    repo: z.string().min(1).describe("GitHub repository, owner/name form (required)."),
    issueNumber: z.number().int().positive().describe("Issue number (required)."),
  })
  .strict();

const SdlcApproveFromGithubInput = z
  .object({
    root: RootInput.optional(),
    taskId: z.string().min(1).describe("Task ID the approval applies to (required)."),
    gate: z.string().min(1).describe("Gate ID, e.g. G1 (required)."),
    role: z.string().min(1).describe("Authority role recording the approval (required)."),
    repo: z.string().min(1).describe("GitHub repository, owner/name form (required)."),
    pr: z.number().int().positive().describe("Pull request number (required)."),
    reviewId: z.string().min(1).describe("GitHub review identifier (required)."),
    reviewerLogin: z.string().min(1).describe("GitHub login that authored the review (required)."),
    commitSha: z.string().min(1).describe("Commit SHA reviewed by the GitHub approval (required)."),
    decidedAt: z.string().min(1).optional().describe("Optional RFC3339 approval time; defaults to now."),
  })
  .strict();

const SdlcApproveFromGithubPrInput = z
  .object({
    root: RootInput.optional(),
    taskId: z.string().min(1).describe("Task ID the approval applies to (required)."),
    gate: z.string().min(1).describe("Gate ID, e.g. G1 (required)."),
    role: z.string().min(1).describe("Authority role recording the approval (required)."),
    repo: z.string().min(1).describe("GitHub repository, owner/name form (required)."),
    pr: z.number().int().positive().describe("Pull request number (required)."),
    reviewerLogin: z
      .string()
      .min(1)
      .optional()
      .describe("GitHub login to match; defaults to the authority's GitHub binding."),
    commitSha: z
      .string()
      .min(1)
      .optional()
      .describe("Optional commit SHA to require when selecting an approved review."),
  })
  .strict();

// Shared by every kernel subcommand that accepts an optional `--gates
// G3,G9` scope filter; a tool caller passes a string array, joined with
// commas when building CLI args, rather than a pre-formatted CSV string.
const GatesFilterInput = z
  .array(z.string().min(1))
  .optional()
  .describe('Optional gate subset, e.g. ["G3","G9"]; omit for all eligible gates.');

// Shared by both create-gate-issues variants. The kernel's default for
// assignee drift is report-only: it tells you the forge's assignee no longer
// matches authorities.json and changes nothing. Passing this overwrites the
// forge's assignee to match. gitlab-gate-tracking's/create-github-gate-issues'
// SKILL.md both make this an explicitly human-authorized remediation step
// ("only re-run with --reconcile-assignees if they explicitly say yes; never
// pass that flag proactively"), so the same rule binds a Cline session.
const ReconcileAssigneesInput = z
  .boolean()
  .optional()
  .describe(
    "Overwrite the forge's issue assignee to match authorities.json when the kernel reports drift. " +
      "Default (omitted) is report-only. Only ever pass true after the human has seen the reported " +
      "drift and explicitly asked for it to be corrected -- never proactively.",
  );

const IncludeScopeInput = z
  .boolean()
  .optional()
  .describe(
    "Add a sanitized scope line to each gate issue's description (kernel default: off). The scope " +
      "text lands on a real forge issue, so leave it off for anything whose task scope should not be " +
      "externally visible.",
  );

// `--break-lock` overrides a *held* ledger lock, which normally means another
// run is in flight against the same task. Every kernel command that takes it
// treats it as a deliberate operator override, not a retry knob.
const BreakLockInput = z
  .boolean()
  .optional()
  .describe(
    "Override a held ledger lock file. A held lock usually means a concurrent run is in flight -- " +
      "only pass true when the human has confirmed no other run is active and the lock is stale.",
  );

const GateIssuesListInput = z
  .object({
    root: RootInput.optional(),
    taskId: z.string().min(1).describe("Task ID whose tracking issues to list (required)."),
  })
  .strict();

const SdlcCreateGateIssuesGitlabInput = z
  .object({
    root: RootInput.optional(),
    taskId: z.string().min(1).describe("Task ID to create/reuse GitLab tracking issues for (required)."),
    projectPath: z.string().min(1).describe("GitLab project path, namespace/project (required)."),
    asBot: z.string().min(1).describe("Bot/service-account GitLab username the kernel verifies as (required)."),
    allowClassification: z
      .string()
      .min(1)
      .optional()
      .describe("Must exactly match the task's recorded classification, if set -- no default."),
    gates: GatesFilterInput,
    // GitLab-only: the kernel's GitHub variant has no --link-type equivalent.
    linkType: z
      .literal("relates_to")
      .optional()
      .describe(
        "Opt-in: also record the gate/approval issue relationship through the GitLab Issue Links API. " +
          "Fails closed (the kernel's exit 2) if that API is unavailable. 'relates_to' is the only value " +
          "the kernel accepts.",
      ),
    includeScope: IncludeScopeInput,
    reconcileAssignees: ReconcileAssigneesInput,
    breakLock: BreakLockInput,
    apply: z
      .boolean()
      .optional()
      .describe("Actually create/assign issues. Omit (or false) for a dry-run preview -- the kernel's own default."),
    planDigest: z
      .string()
      .min(1)
      .optional()
      .describe("Required with apply: true -- the exact planDigest value returned by the preceding dry-run."),
  })
  .strict();

const SdlcCreateGithubGateIssuesInput = z
  .object({
    root: RootInput.optional(),
    taskId: z.string().min(1).describe("Task ID to create/reuse GitHub tracking issues for (required)."),
    repo: z.string().min(1).describe("GitHub repository, owner/name form (required)."),
    asBot: z.string().min(1).describe("Bot/service-account GitHub login the kernel verifies as (required)."),
    allowClassification: z
      .string()
      .min(1)
      .optional()
      .describe("Must exactly match the task's recorded classification, if set -- no default."),
    gates: GatesFilterInput,
    includeScope: IncludeScopeInput,
    reconcileAssignees: ReconcileAssigneesInput,
    breakLock: BreakLockInput,
    allowPublicRepo: z
      .boolean()
      .optional()
      .describe("Only set true once the human has explicitly confirmed the repo is public and accepted that."),
    apply: z
      .boolean()
      .optional()
      .describe("Actually create/assign issues. Omit (or false) for a dry-run preview -- the kernel's own default."),
    planDigest: z
      .string()
      .min(1)
      .optional()
      .describe("Required with apply: true -- the exact planDigest value returned by the preceding dry-run."),
  })
  .strict();

const SdlcPublishGateStatusInput = z.discriminatedUnion("forge", [
  z
    .object({
      forge: z.literal("gitlab"),
      root: RootInput.optional(),
      taskId: z.string().min(1).describe("Task ID whose gate-status note to publish (required)."),
      projectPath: z.string().min(1).describe("GitLab project path, namespace/project (required)."),
      mrIid: z.number().int().positive().describe("Merge request internal ID, iid (required)."),
      asBot: z.string().min(1).describe("Bot/service-account GitLab username the kernel verifies as (required)."),
      allowClassification: z
        .string()
        .min(1)
        .optional()
        .describe("Must exactly match the task's recorded classification, if set -- no default."),
      breakLock: BreakLockInput,
      apply: z
        .boolean()
        .optional()
        .describe("Actually post/update the note. Omit (or false) for a dry-run preview -- the kernel's own default."),
    })
    .strict(),
  z
    .object({
      forge: z.literal("github"),
      root: RootInput.optional(),
      taskId: z.string().min(1).describe("Task ID whose gate-status comment to publish (required)."),
      repo: z.string().min(1).describe("GitHub repository, owner/name form (required)."),
      pr: z.number().int().positive().describe("Pull request number (required)."),
      asBot: z.string().min(1).describe("Bot/service-account GitHub login the kernel verifies as (required)."),
      allowClassification: z
        .string()
        .min(1)
        .optional()
        .describe("Must exactly match the task's recorded classification, if set -- no default."),
      breakLock: BreakLockInput,
      apply: z
        .boolean()
        .optional()
        .describe(
          "Actually post/update the comment. Omit (or false) for a dry-run preview -- the kernel's own default.",
        ),
    })
    .strict(),
]);

const SdlcRequestGateReviewersGitlabInput = z
  .object({
    root: RootInput.optional(),
    taskId: z.string().min(1).describe("Task ID whose gate reviewer candidates to report (required)."),
    projectPath: z.string().min(1).describe("GitLab project path, namespace/project (required)."),
    mrIid: z.number().int().positive().describe("Merge request internal ID, iid (required) -- never auto-discovered."),
    asBot: z
      .string()
      .min(1)
      .describe("Required GitLab bot/machine username; verified via `glab api user` (required)."),
    allowClassification: z
      .string()
      .min(1)
      .optional()
      .describe("Must exactly match the task's recorded classification, if set -- no default."),
    gates: GatesFilterInput,
  })
  .strict();

const SdlcRequestGateReviewersGithubInput = z
  .object({
    root: RootInput.optional(),
    taskId: z.string().min(1).describe("Task ID whose gate reviewer candidates to report (required)."),
    repo: z.string().min(1).describe("GitHub repository, owner/name form (required)."),
    pr: z.number().int().positive().describe("Pull request number (required) -- never auto-discovered."),
    asBot: z
      .string()
      .min(1)
      .describe("Required GitHub bot/machine login; verified via `gh api user` (required)."),
    allowClassification: z
      .string()
      .min(1)
      .optional()
      .describe("Must exactly match the task's recorded classification, if set -- no default."),
    gates: GatesFilterInput,
  })
  .strict();

const ReviewerNudgeListInput = z
  .object({
    root: RootInput.optional(),
    taskId: z.string().min(1).describe("Task ID whose reviewer-nudge ledger entry to list (required)."),
  })
  .strict();

const SdlcPublishReviewerNudgeInput = z
  .object({
    root: RootInput.optional(),
    taskId: z.string().min(1).describe("Task ID whose reviewer-nudge comment to publish (required)."),
    repo: z.string().min(1).describe("GitHub repository, owner/name form (required)."),
    pr: z.number().int().positive().describe("Pull request number (required) -- never auto-discovered."),
    asBot: z.string().min(1).describe("Bot/service-account GitHub login the kernel verifies as (required)."),
    allowClassification: z
      .string()
      .min(1)
      .optional()
      .describe("Must exactly match the task's recorded classification, if set -- no default."),
    gates: GatesFilterInput,
    breakLock: BreakLockInput,
    apply: z
      .boolean()
      .optional()
      .describe("Actually post/update the comment. Omit (or false) for a dry-run preview -- the kernel's own default."),
  })
  .strict();

type SdlcInitInputShape = z.infer<typeof SdlcInitInput>;
type SdlcValidateInputShape = z.infer<typeof SdlcValidateInput>;
type SdlcPlanInputShape = z.infer<typeof SdlcPlanInput>;
type SdlcStatusInputShape = z.infer<typeof SdlcStatusInput>;
type SdlcDecideInputShape = z.infer<typeof SdlcDecideInput>;
type SdlcApproveFromGitlabInputShape = z.infer<typeof SdlcApproveFromGitlabInput>;
type SdlcApproveFromGitlabMrInputShape = z.infer<typeof SdlcApproveFromGitlabMrInput>;
type GitlabIssueLinkInputShape = z.infer<typeof GitlabIssueLinkInput>;
type GithubIssueLinkInputShape = z.infer<typeof GithubIssueLinkInput>;
type SdlcApproveFromGithubInputShape = z.infer<typeof SdlcApproveFromGithubInput>;
type SdlcApproveFromGithubPrInputShape = z.infer<typeof SdlcApproveFromGithubPrInput>;
type GateIssuesListInputShape = z.infer<typeof GateIssuesListInput>;
type SdlcCreateGateIssuesGitlabInputShape = z.infer<typeof SdlcCreateGateIssuesGitlabInput>;
type SdlcCreateGithubGateIssuesInputShape = z.infer<typeof SdlcCreateGithubGateIssuesInput>;
type SdlcPublishGateStatusInputShape = z.infer<typeof SdlcPublishGateStatusInput>;
type SdlcRequestGateReviewersGitlabInputShape = z.infer<typeof SdlcRequestGateReviewersGitlabInput>;
type SdlcRequestGateReviewersGithubInputShape = z.infer<typeof SdlcRequestGateReviewersGithubInput>;
type ReviewerNudgeListInputShape = z.infer<typeof ReviewerNudgeListInput>;
type SdlcPublishReviewerNudgeInputShape = z.infer<typeof SdlcPublishReviewerNudgeInput>;

interface SdlcToolError {
  error: string;
  stderr?: string;
}

// ---------------------------------------------------------------------------
// CLI argument builders
// ---------------------------------------------------------------------------

function buildInitArgs(input: SdlcInitInputShape, rootPath: string): string[] {
  const args = [
    "sdlc",
    "init",
    "--root",
    input.root ?? rootPath,
    "--profile",
    input.profile,
    "--project-id",
    input.projectId,
    "--classification",
    input.classification,
  ];
  if (input.runner) args.push("--runner", input.runner);
  if (input.dryRun) args.push("--dry-run");
  return args;
}

function buildValidateArgs(input: SdlcValidateInputShape, rootPath: string): string[] {
  return ["sdlc", "validate", "--root", input.root ?? rootPath];
}

function buildPlanArgs(input: SdlcPlanInputShape, rootPath: string): string[] {
  return ["sdlc", "plan", "--root", input.root ?? rootPath, "--task-id", input.taskId, "--task", input.task];
}

function buildStatusArgs(input: SdlcStatusInputShape, rootPath: string): string[] {
  return ["sdlc", "status", "--root", input.root ?? rootPath, "--task-id", input.taskId];
}

function buildDecideArgs(input: SdlcDecideInputShape, rootPath: string): string[] {
  const args = [
    "sdlc",
    "decide",
    "--root",
    input.root ?? rootPath,
    "--task-id",
    input.taskId,
    "--gate",
    input.gate,
    "--role",
    input.role,
    "--decision",
    input.decision,
    "--actor-id",
    input.actorId,
    "--evidence-uri",
    input.evidenceUri,
  ];
  if (input.note) args.push("--note", input.note);
  if (input.decidedAt) args.push("--decided-at", input.decidedAt);
  return args;
}

function buildApproveFromGitlabArgs(input: SdlcApproveFromGitlabInputShape, rootPath: string): string[] {
  const args = [
    "sdlc",
    "approve-from-gitlab",
    "--root",
    input.root ?? rootPath,
    "--task-id",
    input.taskId,
    "--gate",
    input.gate,
    "--role",
    input.role,
    "--project-path",
    input.projectPath,
    "--mr-iid",
    String(input.mrIid),
    "--approval-id",
    input.approvalId,
    "--approver-username",
    input.approverUsername,
    "--commit-sha",
    input.commitSha,
  ];
  if (input.decidedAt) args.push("--decided-at", input.decidedAt);
  return args;
}

function buildApproveFromGitlabMrArgs(input: SdlcApproveFromGitlabMrInputShape, rootPath: string): string[] {
  const args = [
    "sdlc",
    "approve-from-gitlab-mr",
    "--root",
    input.root ?? rootPath,
    "--task-id",
    input.taskId,
    "--gate",
    input.gate,
    "--role",
    input.role,
    "--project-path",
    input.projectPath,
    "--mr-iid",
    String(input.mrIid),
  ];
  if (input.approverUsername) args.push("--approver-username", input.approverUsername);
  if (input.commitSha) args.push("--commit-sha", input.commitSha);
  return args;
}

function buildLinkIntentFromGitlabIssueArgs(input: GitlabIssueLinkInputShape, rootPath: string): string[] {
  return [
    "sdlc",
    "link-intent-from-gitlab-issue",
    "--root",
    input.root ?? rootPath,
    "--task-id",
    input.taskId,
    "--role",
    input.role,
    "--project-path",
    input.projectPath,
    "--issue-iid",
    String(input.issueIid),
  ];
}

function buildLinkRequirementsFromGitlabIssueArgs(input: GitlabIssueLinkInputShape, rootPath: string): string[] {
  return [
    "sdlc",
    "link-requirements-from-gitlab-issue",
    "--root",
    input.root ?? rootPath,
    "--task-id",
    input.taskId,
    "--role",
    input.role,
    "--project-path",
    input.projectPath,
    "--issue-iid",
    String(input.issueIid),
  ];
}

// The GitHub link-source-issue pair takes `--issue-number` where the GitLab
// pair takes `--issue-iid` -- the forges' own naming, mirrored rather than
// normalized, so a flag in a kernel error message maps back to a tool field.
function buildLinkIntentFromGithubIssueArgs(input: GithubIssueLinkInputShape, rootPath: string): string[] {
  return [
    "sdlc",
    "link-intent-from-github-issue",
    "--root",
    input.root ?? rootPath,
    "--task-id",
    input.taskId,
    "--role",
    input.role,
    "--repo",
    input.repo,
    "--issue-number",
    String(input.issueNumber),
  ];
}

function buildLinkRequirementsFromGithubIssueArgs(input: GithubIssueLinkInputShape, rootPath: string): string[] {
  return [
    "sdlc",
    "link-requirements-from-github-issue",
    "--root",
    input.root ?? rootPath,
    "--task-id",
    input.taskId,
    "--role",
    input.role,
    "--repo",
    input.repo,
    "--issue-number",
    String(input.issueNumber),
  ];
}

function buildApproveFromGithubArgs(input: SdlcApproveFromGithubInputShape, rootPath: string): string[] {
  const args = [
    "sdlc",
    "approve-from-github",
    "--root",
    input.root ?? rootPath,
    "--task-id",
    input.taskId,
    "--gate",
    input.gate,
    "--role",
    input.role,
    "--repo",
    input.repo,
    "--pr",
    String(input.pr),
    "--review-id",
    input.reviewId,
    "--reviewer-login",
    input.reviewerLogin,
    "--commit-sha",
    input.commitSha,
  ];
  if (input.decidedAt) args.push("--decided-at", input.decidedAt);
  return args;
}

function buildApproveFromGithubPrArgs(input: SdlcApproveFromGithubPrInputShape, rootPath: string): string[] {
  const args = [
    "sdlc",
    "approve-from-github-pr",
    "--root",
    input.root ?? rootPath,
    "--task-id",
    input.taskId,
    "--gate",
    input.gate,
    "--role",
    input.role,
    "--repo",
    input.repo,
    "--pr",
    String(input.pr),
  ];
  if (input.reviewerLogin) args.push("--reviewer-login", input.reviewerLogin);
  if (input.commitSha) args.push("--commit-sha", input.commitSha);
  return args;
}

function buildListGateIssuesGitlabArgs(input: GateIssuesListInputShape, rootPath: string): string[] {
  return ["sdlc", "list-gate-issues", "--root", input.root ?? rootPath, "--task-id", input.taskId];
}

function buildCreateGateIssuesGitlabArgs(input: SdlcCreateGateIssuesGitlabInputShape, rootPath: string): string[] {
  const args = [
    "sdlc",
    "create-gate-issues",
    "--root",
    input.root ?? rootPath,
    "--task-id",
    input.taskId,
    "--project-path",
    input.projectPath,
    "--as-bot",
    input.asBot,
  ];
  if (input.allowClassification) args.push("--allow-classification", input.allowClassification);
  if (input.gates?.length) args.push("--gates", input.gates.join(","));
  if (input.linkType) args.push("--link-type", input.linkType);
  if (input.includeScope) args.push("--include-scope");
  if (input.reconcileAssignees) args.push("--reconcile-assignees");
  if (input.breakLock) args.push("--break-lock");
  if (input.apply) args.push("--apply");
  if (input.planDigest) args.push("--plan-digest", input.planDigest);
  return args;
}

function buildListGithubGateIssuesArgs(input: GateIssuesListInputShape, rootPath: string): string[] {
  return ["sdlc", "list-github-gate-issues", "--root", input.root ?? rootPath, "--task-id", input.taskId];
}

function buildCreateGithubGateIssuesArgs(input: SdlcCreateGithubGateIssuesInputShape, rootPath: string): string[] {
  const args = [
    "sdlc",
    "create-github-gate-issues",
    "--root",
    input.root ?? rootPath,
    "--task-id",
    input.taskId,
    "--repo",
    input.repo,
    "--as-bot",
    input.asBot,
  ];
  if (input.allowClassification) args.push("--allow-classification", input.allowClassification);
  if (input.gates?.length) args.push("--gates", input.gates.join(","));
  if (input.includeScope) args.push("--include-scope");
  if (input.reconcileAssignees) args.push("--reconcile-assignees");
  if (input.breakLock) args.push("--break-lock");
  if (input.allowPublicRepo) args.push("--allow-public-repo");
  if (input.apply) args.push("--apply");
  if (input.planDigest) args.push("--plan-digest", input.planDigest);
  return args;
}

function buildListGateStatusArgs(input: GateIssuesListInputShape, rootPath: string): string[] {
  return ["sdlc", "list-gate-status", "--root", input.root ?? rootPath, "--task-id", input.taskId];
}

function buildPublishGateStatusArgs(input: SdlcPublishGateStatusInputShape, rootPath: string): string[] {
  const args = ["sdlc", "publish-gate-status", "--root", input.root ?? rootPath, "--task-id", input.taskId];
  if (input.forge === "gitlab") {
    args.push("--forge", "gitlab", "--project-path", input.projectPath, "--mr-iid", String(input.mrIid));
  } else {
    args.push("--forge", "github", "--repo", input.repo, "--pr", String(input.pr));
  }
  args.push("--as-bot", input.asBot);
  if (input.allowClassification) args.push("--allow-classification", input.allowClassification);
  if (input.breakLock) args.push("--break-lock");
  if (input.apply) args.push("--apply");
  return args;
}

function buildRequestGateReviewersGitlabArgs(
  input: SdlcRequestGateReviewersGitlabInputShape,
  rootPath: string,
): string[] {
  const args = [
    "sdlc",
    "request-gate-reviewers-gitlab",
    "--root",
    input.root ?? rootPath,
    "--task-id",
    input.taskId,
    "--project-path",
    input.projectPath,
    "--mr-iid",
    String(input.mrIid),
    "--as-bot",
    input.asBot,
  ];
  if (input.allowClassification) args.push("--allow-classification", input.allowClassification);
  if (input.gates?.length) args.push("--gates", input.gates.join(","));
  return args;
}

function buildRequestGateReviewersGithubArgs(
  input: SdlcRequestGateReviewersGithubInputShape,
  rootPath: string,
): string[] {
  const args = [
    "sdlc",
    "request-gate-reviewers",
    "--root",
    input.root ?? rootPath,
    "--task-id",
    input.taskId,
    "--repo",
    input.repo,
    "--pr",
    String(input.pr),
    "--as-bot",
    input.asBot,
  ];
  if (input.allowClassification) args.push("--allow-classification", input.allowClassification);
  if (input.gates?.length) args.push("--gates", input.gates.join(","));
  return args;
}

function buildListReviewerNudgeArgs(input: ReviewerNudgeListInputShape, rootPath: string): string[] {
  return ["sdlc", "list-reviewer-nudge", "--root", input.root ?? rootPath, "--task-id", input.taskId];
}

function buildPublishReviewerNudgeArgs(input: SdlcPublishReviewerNudgeInputShape, rootPath: string): string[] {
  const args = [
    "sdlc",
    "publish-reviewer-nudge",
    "--root",
    input.root ?? rootPath,
    "--task-id",
    input.taskId,
    "--repo",
    input.repo,
    "--pr",
    String(input.pr),
    "--as-bot",
    input.asBot,
  ];
  if (input.allowClassification) args.push("--allow-classification", input.allowClassification);
  if (input.gates?.length) args.push("--gates", input.gates.join(","));
  if (input.breakLock) args.push("--break-lock");
  if (input.apply) args.push("--apply");
  return args;
}

// ---------------------------------------------------------------------------
// Sanitization (same convention as cline/index.ts's sanitizeToolResult)
// ---------------------------------------------------------------------------

function sanitizeToolResult(input: unknown): Record<string, unknown> | SdlcToolError {
  try {
    return JSON.parse(safeJsonStringify(input)) as Record<string, unknown>;
  } catch {
    return { error: "sdlc tool call failed: result could not be serialized" };
  }
}

async function runCadreSdlc(args: string[], rootPath: string): Promise<Record<string, unknown> | SdlcToolError> {
  try {
    const { stdout } = await execFileAsync(CADRE_BIN, args, { cwd: rootPath });
    return sanitizeToolResult(JSON.parse(stdout));
  } catch (caught) {
    const err = caught as { message?: string; stderr?: string; stdout?: string };
    return sanitizeToolResult({
      error: [err.stderr?.trim(), err.message].filter(Boolean).join("\n") || "sdlc tool call failed",
      stderr: err.stderr,
    }) as SdlcToolError;
  }
}

// Four kernel commands share the same non-zero-exit-can-still-be-a-valid-JSON-
// report shape: `request-gate-reviewers-gitlab`/`request-gate-reviewers` exit
// 2 for "report completed, contains refusals/withheld-conflict entries --
// still valid JSON, not a failure" (per their own SKILL.md docs), and
// `create-gate-issues`/`create-github-gate-issues` exit 2 whenever their own
// result has non-empty `refusals` or `drift_detected` -- including during an
// ordinary dry-run preview, and, in `--apply` mode, *after* issues have
// already been created/assigned on the real forge (confirmed against
// `cmd_create_gate_issues`/`cmd_create_github_gate_issues` in the kernel
// source: both print the full JSON result to stdout, then `return 2`). Exit 1
// is reserved for a genuine structural failure in all four (MR/PR not found,
// identity mismatch, malformed request) -- with one caveat found in review:
// for the two create-issues commands, *any* `GateIssuesBlocked`/
// `GateIssuesGithubBlocked`-raising failure during `--apply` also exits 2
// but does NOT get the full structured result -- this is not limited to a
// concurrent plan-digest mismatch (the case an earlier round of this same
// review singled out); tracing `gate_issues.py`/`gate_issues_github.py`
// directly turns up at least: an ambiguous label match, a matched issue
// missing its own anchor label or carrying a foreign one, an author-identity
// mismatch on a matched issue, post-creation verification failure, an
// unresolvable/ambiguous username during `--reconcile-assignees`, the
// GitLab Issue Links API being unavailable, a held ledger lock, and the
// *initial* (non-concurrent) plan-digest mismatch too -- every one of these
// prints only `{"error": "<message>"}` to stderr (still JSON, just not the
// full result payload -- not literally "prose"), never `gate_results`/
// `approval_results`/`plan_digest`. This helper's `err.stdout` is empty in
// all of these cases, so it falls through to the same generic error shape a
// real exit-1 would produce -- still safe (no false success, no crash),
// just less structured than the ordinary refusals-on-a-completed-run case
// gets. Plain `runCadreSdlc` would discard a real exit-2 report's stdout --
// including, for the two create-issues commands, the `plan_digest` a
// subsequent `apply: true` call needs, and, in the ordinary
// (no-GateIssuesBlocked-raised) case, confirmation of what was already
// created -- and misreport it as an opaque error; this variant parses
// `err.stdout` as JSON first and only falls back to the generic error shape
// if that fails.
async function runCadreSdlcAllowingReportExitCodes(
  args: string[],
  rootPath: string,
): Promise<Record<string, unknown> | SdlcToolError> {
  try {
    const { stdout } = await execFileAsync(CADRE_BIN, args, { cwd: rootPath });
    return sanitizeToolResult(JSON.parse(stdout));
  } catch (caught) {
    const err = caught as { message?: string; stderr?: string; stdout?: string };
    if (err.stdout) {
      try {
        return sanitizeToolResult(JSON.parse(err.stdout));
      } catch {
        // Not parseable JSON -- fall through to the generic error shape,
        // matching runCadreSdlc's own behavior for a genuine failure.
      }
    }
    return sanitizeToolResult({
      error: [err.stderr?.trim(), err.message].filter(Boolean).join("\n") || "sdlc tool call failed",
      stderr: err.stderr,
    }) as SdlcToolError;
  }
}

// ---------------------------------------------------------------------------
// Setup and tool registration
// ---------------------------------------------------------------------------

type SetupFn = NonNullable<AgentPlugin["setup"]>;
export type SetupApi = Parameters<SetupFn>[0];
export type SetupContext = Parameters<SetupFn>[1];
export type {
  SdlcInitInputShape,
  SdlcValidateInputShape,
  SdlcStatusInputShape,
  SdlcDecideInputShape,
  SdlcApproveFromGitlabInputShape,
  SdlcApproveFromGitlabMrInputShape,
  GitlabIssueLinkInputShape,
  GithubIssueLinkInputShape,
  SdlcApproveFromGithubInputShape,
  SdlcApproveFromGithubPrInputShape,
  GateIssuesListInputShape,
  SdlcCreateGateIssuesGitlabInputShape,
  SdlcCreateGithubGateIssuesInputShape,
  SdlcPublishGateStatusInputShape,
  SdlcRequestGateReviewersGitlabInputShape,
  SdlcRequestGateReviewersGithubInputShape,
  ReviewerNudgeListInputShape,
  SdlcPublishReviewerNudgeInputShape,
  SdlcToolError,
};

// Exported for direct, kernel-free unit testing of argument construction --
// in particular the sdlc_publish_gate_status discriminated union's two
// branches and the `gates` array -> CSV join shared by several builders,
// neither of which a real-subprocess test can distinguish from a wrong
// implementation when no kernel is installed to reject a malformed
// invocation (see index.test.mts's "argument construction (kernel-free)"
// tests).
export {
  buildPublishGateStatusArgs,
  buildCreateGateIssuesGitlabArgs,
  buildCreateGithubGateIssuesArgs,
  buildPublishReviewerNudgeArgs,
  buildLinkIntentFromGithubIssueArgs,
  buildLinkRequirementsFromGithubIssueArgs,
  buildRequestGateReviewersGitlabArgs,
  buildRequestGateReviewersGithubArgs,
  runCadreSdlcAllowingReportExitCodes,
};

const setup = (api: SetupApi, ctx: SetupContext) => {
  const rootPath = ctx.workspaceInfo?.rootPath;

  // Real, plugin-controlled system-prompt injection -- see cline/index.ts's
  // equivalent registerRule call for the confirmation this is a genuine
  // runtime-system-prompt contribution, not host-application config. Scoped
  // to this plugin's own lifecycle-governance tools so a session with
  // `cline` and/or `cline-agents` also installed composes an addendum
  // rather than repeating the base sentence unchanged.
  api.registerRule({
    id: "cline-lifecycle-system-prompt",
    content:
      "You are a coding assistant with access to Cadre role subagents. " +
      "This session also has Agentic SDLC G1-G10 lifecycle governance available via the `sdlc_*` tool " +
      "calls (`sdlc_init`, `sdlc_validate`, `sdlc_plan`, `sdlc_status`, `sdlc_decide`, plus GitHub- and " +
      "GitLab-specific gate-review/reviewer-nudge/gate-status tools) -- use them for lifecycle gate " +
      "tracking and decisions instead of asking a human to run `bin/cadre sdlc` by hand. Never approve " +
      "or decide a gate you prepared evidence for yourself; separation of duties is enforced by the " +
      "external Agentic SDLC kernel these tools call, not by this plugin.",
    source: "cline-lifecycle",
  });

  function requireRootPath(explicitRoot: string | undefined): string {
    const resolved = explicitRoot ?? rootPath;
    if (!resolved) {
      throw new Error(
        "Could not resolve a project root: no `root` argument was given and the host session has no " +
          "known workspace root.",
      );
    }
    return resolved;
  }

  api.registerTool(
    createTool({
      name: "sdlc_init",
      description:
        "Initialize Agentic SDLC G1-G10 lifecycle tracking for a project, via `bin/cadre sdlc init` -- " +
        "the same command plugins/lifecycle/skills/lifecycle-onboarding/SKILL.md's Step 3 documents for " +
        "Claude Code / Codex. Requires `agentic-sdlc` to already be installed and resolvable (see this " +
        "repository's bootstrap_sdlc.py; not run by this tool). Pass dryRun: true first and inspect the " +
        "result before writing for real.",
      inputSchema: z.toJSONSchema(SdlcInitInput),
      execute: async (rawInput: unknown): Promise<Record<string, unknown> | SdlcToolError> => {
        const input = SdlcInitInput.parse(rawInput);
        const root = requireRootPath(input.root);
        return runCadreSdlc(buildInitArgs(input, root), root);
      },
    }),
  );

  api.registerTool(
    createTool({
      name: "sdlc_validate",
      description:
        "Validate a project's Agentic SDLC configuration and run-record state, via `bin/cadre sdlc " +
        "validate` -- the same command lifecycle-onboarding's Step 9 documents. Returns errors/blockers " +
        "as JSON; this tool does not translate them into human-facing prose, the caller does.",
      inputSchema: z.toJSONSchema(SdlcValidateInput),
      execute: async (rawInput: unknown): Promise<Record<string, unknown> | SdlcToolError> => {
        const input = SdlcValidateInput.parse(rawInput);
        const root = requireRootPath(input.root);
        return runCadreSdlc(buildValidateArgs(input, root), root);
      },
    }),
  );

  api.registerTool(
    createTool({
      name: "sdlc_plan",
      description:
        "Create (or overwrite) a task's dispatch plan and pending run record, via `bin/cadre sdlc plan` -- " +
        "the same fallback command 13 forge-specific SKILL.md files point to (e.g. " +
        "lifecycle-review-github/SKILL.md's Step 2: 'or `cadre sdlc plan` first') when `sdlc_status`/" +
        "`sdlc_decide` need a run record for a task-id that doesn't exist yet. Writes " +
        "`.agentic-sdlc/runs/<taskId>/dispatch-plan.json` and `run-record.json` -- this is a real write, " +
        "not a dry-run preview; the kernel's `plan` subcommand has no dry-run mode.",
      inputSchema: z.toJSONSchema(SdlcPlanInput),
      execute: async (rawInput: unknown): Promise<Record<string, unknown> | SdlcToolError> => {
        const input = SdlcPlanInput.parse(rawInput);
        const root = requireRootPath(input.root);
        return runCadreSdlc(buildPlanArgs(input, root), root);
      },
    }),
  );

  api.registerTool(
    createTool({
      name: "sdlc_status",
      description:
        "Report a task's pending/decided Agentic SDLC lifecycle gates, via `bin/cadre sdlc status` -- " +
        "the same command lifecycle-review's Step 2 and brief-pending-gates document. Read-only: never " +
        "decides or invalidates a gate.",
      inputSchema: z.toJSONSchema(SdlcStatusInput),
      execute: async (rawInput: unknown): Promise<Record<string, unknown> | SdlcToolError> => {
        const input = SdlcStatusInput.parse(rawInput);
        const root = requireRootPath(input.root);
        return runCadreSdlc(buildStatusArgs(input, root), root);
      },
    }),
  );

  api.registerTool(
    createTool({
      name: "sdlc_decide",
      description:
        "Record a lifecycle gate decision, via `bin/cadre sdlc decide` -- the same command " +
        "lifecycle-review's Step 5 documents. This tool adds no approval logic of its own: the " +
        "`agentic-sdlc` kernel itself structurally refuses a decision from the same identity as the " +
        "gate's preparer/verifier (this repository's human-approval invariant), and this tool only " +
        "relays that outcome, success or refusal, as JSON. Never call this on behalf of a human who " +
        "has not actually made the decision being recorded.",
      inputSchema: z.toJSONSchema(SdlcDecideInput),
      execute: async (rawInput: unknown): Promise<Record<string, unknown> | SdlcToolError> => {
        const input = SdlcDecideInput.parse(rawInput);
        const root = requireRootPath(input.root);
        return runCadreSdlc(buildDecideArgs(input, root), root);
      },
    }),
  );

  api.registerTool(
    createTool({
      name: "sdlc_approve_from_gitlab",
      description:
        "Record a human gate approval from prepared GitLab MR-approval evidence, via `bin/cadre sdlc " +
        "approve-from-gitlab` -- the same command lifecycle-review-gitlab's Step 4a documents for " +
        "Claude Code / Codex, for when the human already reported the approval details rather than " +
        "having this tool fetch them live from the MR (use sdlc_approve_from_gitlab_mr for that). This " +
        "tool adds no approval logic of its own: the kernel structurally refuses a decision from the " +
        "same identity as the gate's preparer/verifier. Never call this on behalf of a human who has " +
        "not actually recorded the GitLab approval being cited.",
      inputSchema: z.toJSONSchema(SdlcApproveFromGitlabInput),
      execute: async (rawInput: unknown): Promise<Record<string, unknown> | SdlcToolError> => {
        const input = SdlcApproveFromGitlabInput.parse(rawInput);
        const root = requireRootPath(input.root);
        return runCadreSdlc(buildApproveFromGitlabArgs(input, root), root);
      },
    }),
  );

  api.registerTool(
    createTool({
      name: "sdlc_approve_from_gitlab_mr",
      description:
        "Record a human gate approval by fetching and verifying an actual approved GitLab MR approval " +
        "live, via `bin/cadre sdlc approve-from-gitlab-mr` -- the same command lifecycle-review-gitlab's " +
        "Step 4b documents for Claude Code / Codex. Fails closed if no matching approved approval is " +
        "found on the merge request. This tool adds no approval logic of its own: the kernel " +
        "structurally refuses a decision from the same identity as the gate's preparer/verifier.",
      inputSchema: z.toJSONSchema(SdlcApproveFromGitlabMrInput),
      execute: async (rawInput: unknown): Promise<Record<string, unknown> | SdlcToolError> => {
        const input = SdlcApproveFromGitlabMrInput.parse(rawInput);
        const root = requireRootPath(input.root);
        return runCadreSdlc(buildApproveFromGitlabMrArgs(input, root), root);
      },
    }),
  );

  api.registerTool(
    createTool({
      name: "sdlc_link_intent_from_gitlab_issue",
      description:
        "Record a real GitLab issue as the recorded source for a task's G1 (Intent) gate, via " +
        "`bin/cadre sdlc link-intent-from-gitlab-issue` -- the same command link-source-issue-gitlab's " +
        "Step 4 documents for Claude Code / Codex. Only ever applies to G1: records a source, not an " +
        "approval -- for a GitLab MR approval use sdlc_approve_from_gitlab/sdlc_approve_from_gitlab_mr.",
      inputSchema: z.toJSONSchema(GitlabIssueLinkInput),
      execute: async (rawInput: unknown): Promise<Record<string, unknown> | SdlcToolError> => {
        const input = GitlabIssueLinkInput.parse(rawInput);
        const root = requireRootPath(input.root);
        return runCadreSdlc(buildLinkIntentFromGitlabIssueArgs(input, root), root);
      },
    }),
  );

  api.registerTool(
    createTool({
      name: "sdlc_link_requirements_from_gitlab_issue",
      description:
        "Record a real GitLab issue as the recorded source for a task's G2 (Requirements Baseline) " +
        "gate, via `bin/cadre sdlc link-requirements-from-gitlab-issue` -- the same command " +
        "link-source-issue-gitlab's Step 4 documents for Claude Code / Codex. Only ever applies to G2: " +
        "records a source, not an approval -- for a GitLab MR approval use " +
        "sdlc_approve_from_gitlab/sdlc_approve_from_gitlab_mr.",
      inputSchema: z.toJSONSchema(GitlabIssueLinkInput),
      execute: async (rawInput: unknown): Promise<Record<string, unknown> | SdlcToolError> => {
        const input = GitlabIssueLinkInput.parse(rawInput);
        const root = requireRootPath(input.root);
        return runCadreSdlc(buildLinkRequirementsFromGitlabIssueArgs(input, root), root);
      },
    }),
  );

  api.registerTool(
    createTool({
      name: "sdlc_link_intent_from_github_issue",
      description:
        "Record a real GitHub issue as the recorded source for a task's G1 (Intent) gate, via " +
        "`bin/cadre sdlc link-intent-from-github-issue` -- the same command link-source-issue-github's " +
        "Step 4 documents for Claude Code / Codex. Only ever applies to G1: records a source, not an " +
        "approval -- for a GitHub PR approval use sdlc_approve_from_github/sdlc_approve_from_github_pr.",
      inputSchema: z.toJSONSchema(GithubIssueLinkInput),
      execute: async (rawInput: unknown): Promise<Record<string, unknown> | SdlcToolError> => {
        const input = GithubIssueLinkInput.parse(rawInput);
        const root = requireRootPath(input.root);
        return runCadreSdlc(buildLinkIntentFromGithubIssueArgs(input, root), root);
      },
    }),
  );

  api.registerTool(
    createTool({
      name: "sdlc_link_requirements_from_github_issue",
      description:
        "Record a real GitHub issue as the recorded source for a task's G2 (Requirements Baseline) " +
        "gate, via `bin/cadre sdlc link-requirements-from-github-issue` -- the same command " +
        "link-source-issue-github's Step 4 documents for Claude Code / Codex. Only ever applies to G2: " +
        "records a source, not an approval -- for a GitHub PR approval use " +
        "sdlc_approve_from_github/sdlc_approve_from_github_pr.",
      inputSchema: z.toJSONSchema(GithubIssueLinkInput),
      execute: async (rawInput: unknown): Promise<Record<string, unknown> | SdlcToolError> => {
        const input = GithubIssueLinkInput.parse(rawInput);
        const root = requireRootPath(input.root);
        return runCadreSdlc(buildLinkRequirementsFromGithubIssueArgs(input, root), root);
      },
    }),
  );

  api.registerTool(
    createTool({
      name: "sdlc_approve_from_github",
      description:
        "Record a human gate approval from prepared GitHub PR-review evidence, via `bin/cadre sdlc " +
        "approve-from-github` -- the same command lifecycle-review-github's Step 4a documents for " +
        "Claude Code / Codex, for when the human already reported the review details rather than " +
        "having this tool fetch them live from the PR (use sdlc_approve_from_github_pr for that). This " +
        "tool adds no approval logic of its own: the kernel structurally refuses a decision from the " +
        "same identity as the gate's preparer/verifier. Never call this on behalf of a human who has " +
        "not actually recorded the GitHub review being cited.",
      inputSchema: z.toJSONSchema(SdlcApproveFromGithubInput),
      execute: async (rawInput: unknown): Promise<Record<string, unknown> | SdlcToolError> => {
        const input = SdlcApproveFromGithubInput.parse(rawInput);
        const root = requireRootPath(input.root);
        return runCadreSdlc(buildApproveFromGithubArgs(input, root), root);
      },
    }),
  );

  api.registerTool(
    createTool({
      name: "sdlc_approve_from_github_pr",
      description:
        "Record a human gate approval by fetching and verifying an actual approved GitHub PR review " +
        "live, via `bin/cadre sdlc approve-from-github-pr` -- the same command lifecycle-review-github's " +
        "Step 4b documents for Claude Code / Codex. Fails closed if no matching approved review is " +
        "found on the pull request. This tool adds no approval logic of its own: the kernel " +
        "structurally refuses a decision from the same identity as the gate's preparer/verifier.",
      inputSchema: z.toJSONSchema(SdlcApproveFromGithubPrInput),
      execute: async (rawInput: unknown): Promise<Record<string, unknown> | SdlcToolError> => {
        const input = SdlcApproveFromGithubPrInput.parse(rawInput);
        const root = requireRootPath(input.root);
        return runCadreSdlc(buildApproveFromGithubPrArgs(input, root), root);
      },
    }),
  );

  api.registerTool(
    createTool({
      name: "sdlc_list_gate_issues_gitlab",
      description:
        "List a task's existing GitLab gate-tracking issues and their assigned approval sub-issues, " +
        "via `bin/cadre sdlc list-gate-issues` -- the same command gitlab-gate-tracking's Step 1 " +
        "documents for Claude Code / Codex. Read-only: never creates or changes anything.",
      inputSchema: z.toJSONSchema(GateIssuesListInput),
      execute: async (rawInput: unknown): Promise<Record<string, unknown> | SdlcToolError> => {
        const input = GateIssuesListInput.parse(rawInput);
        const root = requireRootPath(input.root);
        return runCadreSdlc(buildListGateIssuesGitlabArgs(input, root), root);
      },
    }),
  );

  api.registerTool(
    createTool({
      name: "sdlc_create_gate_issues_gitlab",
      description:
        "Create (or reuse) one GitLab tracking issue per eligible lifecycle gate, plus a linked " +
        "approval sub-issue per required authority, assigned to that authority's real GitLab account, " +
        "via `bin/cadre sdlc create-gate-issues` -- the same command gitlab-gate-tracking's Steps 3-4 " +
        "document for Claude Code / Codex. Defaults to a dry-run preview (omit `apply`); assigning a " +
        "real person is consequential and externally visible, so only pass `apply: true` with the exact " +
        "`planDigest` value the preceding dry-run returned, after the human has explicitly confirmed " +
        "the assignments shown in that preview -- never fabricate or guess a planDigest.",
      inputSchema: z.toJSONSchema(SdlcCreateGateIssuesGitlabInput),
      execute: async (rawInput: unknown): Promise<Record<string, unknown> | SdlcToolError> => {
        const input = SdlcCreateGateIssuesGitlabInput.parse(rawInput);
        const root = requireRootPath(input.root);
        return runCadreSdlcAllowingReportExitCodes(buildCreateGateIssuesGitlabArgs(input, root), root);
      },
    }),
  );

  api.registerTool(
    createTool({
      name: "sdlc_list_github_gate_issues",
      description:
        "List a task's existing GitHub gate-tracking issues and their assigned approval sub-issues, " +
        "via `bin/cadre sdlc list-github-gate-issues` -- the same command create-github-gate-issues' " +
        "Step 1 documents for Claude Code / Codex. Read-only: never creates or changes anything.",
      inputSchema: z.toJSONSchema(GateIssuesListInput),
      execute: async (rawInput: unknown): Promise<Record<string, unknown> | SdlcToolError> => {
        const input = GateIssuesListInput.parse(rawInput);
        const root = requireRootPath(input.root);
        return runCadreSdlc(buildListGithubGateIssuesArgs(input, root), root);
      },
    }),
  );

  api.registerTool(
    createTool({
      name: "sdlc_create_github_gate_issues",
      description:
        "Create (or reuse) one GitHub tracking issue per eligible lifecycle gate, plus a linked " +
        "approval sub-issue per required authority, assigned to that authority's real GitHub account, " +
        "via `bin/cadre sdlc create-github-gate-issues` -- the same command create-github-gate-issues' " +
        "Steps 3-4 document for Claude Code / Codex. Defaults to a dry-run preview (omit `apply`); " +
        "assigning a real person is consequential and externally visible, so only pass `apply: true` " +
        "with the exact `planDigest` value the preceding dry-run returned, after the human has " +
        "explicitly confirmed the assignments shown in that preview -- never fabricate or guess a " +
        "planDigest. Only set `allowPublicRepo: true` once the human has explicitly confirmed and " +
        "accepted that the repository is public.",
      inputSchema: z.toJSONSchema(SdlcCreateGithubGateIssuesInput),
      execute: async (rawInput: unknown): Promise<Record<string, unknown> | SdlcToolError> => {
        const input = SdlcCreateGithubGateIssuesInput.parse(rawInput);
        const root = requireRootPath(input.root);
        return runCadreSdlcAllowingReportExitCodes(buildCreateGithubGateIssuesArgs(input, root), root);
      },
    }),
  );

  api.registerTool(
    createTool({
      name: "sdlc_list_gate_status",
      description:
        "Show a task's locally-recorded gate-status publication ledger (both GitLab and GitHub " +
        "entries, if any) without touching either forge, via `bin/cadre sdlc list-gate-status` -- the " +
        "same command publish-gate-status-gitlab's/-github's Step 1 documents for Claude Code / Codex. " +
        "Zero-network and can be stale relative to what's actually posted; a convenience, not " +
        "authoritative. Read-only.",
      inputSchema: z.toJSONSchema(GateIssuesListInput),
      execute: async (rawInput: unknown): Promise<Record<string, unknown> | SdlcToolError> => {
        const input = GateIssuesListInput.parse(rawInput);
        const root = requireRootPath(input.root);
        return runCadreSdlc(buildListGateStatusArgs(input, root), root);
      },
    }),
  );

  api.registerTool(
    createTool({
      name: "sdlc_publish_gate_status",
      description:
        "Publish (or idempotently update in place) a one-way, read-only gate-status summary note on a " +
        "task's GitLab MR or GitHub PR, via `bin/cadre sdlc publish-gate-status` -- the same command " +
        "publish-gate-status-gitlab's/-github's Steps 2-3 document for Claude Code / Codex. This note is " +
        "never read back as an approval; gate approval remains exclusively `sdlc_decide`/" +
        "`sdlc_approve_from_gitlab*`/`sdlc_approve_from_github*`. Defaults to a dry-run preview (omit " +
        "`apply`) -- unlike the gate-tracking-issue tools above there is no plan-digest handshake here " +
        "since nothing gets assigned to anyone, but still confirm the project/MR or repo/PR and task " +
        "with the human before passing `apply: true`. `forge` selects which shape (`projectPath`+`mrIid` " +
        "for GitLab, `repo`+`pr` for GitHub) the remaining fields must take.",
      inputSchema: z.toJSONSchema(SdlcPublishGateStatusInput),
      execute: async (rawInput: unknown): Promise<Record<string, unknown> | SdlcToolError> => {
        const input = SdlcPublishGateStatusInput.parse(rawInput);
        const root = requireRootPath(input.root);
        return runCadreSdlc(buildPublishGateStatusArgs(input, root), root);
      },
    }),
  );

  api.registerTool(
    createTool({
      name: "sdlc_request_gate_reviewers_gitlab",
      description:
        "Report which GitLab usernames would be set as MR reviewers for a task's lifecycle gates -- " +
        "read-only/reporting only, never actually sets reviewer_ids -- via `bin/cadre sdlc " +
        "request-gate-reviewers-gitlab`, the same command report-gate-reviewers-gitlab's Step 1 " +
        "documents for Claude Code / Codex. The underlying command's own exit code 2 means the report " +
        "completed but contains refusals/withheld-conflict entries -- this tool still returns that " +
        "report's JSON normally in that case, exactly as exit 0 would; only a structural failure " +
        "(exit 1: MR not found/closed/merged, project-path mismatch, `asBot` identity mismatch) " +
        "surfaces as this tool's `error` field.",
      inputSchema: z.toJSONSchema(SdlcRequestGateReviewersGitlabInput),
      execute: async (rawInput: unknown): Promise<Record<string, unknown> | SdlcToolError> => {
        const input = SdlcRequestGateReviewersGitlabInput.parse(rawInput);
        const root = requireRootPath(input.root);
        return runCadreSdlcAllowingReportExitCodes(buildRequestGateReviewersGitlabArgs(input, root), root);
      },
    }),
  );

  api.registerTool(
    createTool({
      name: "sdlc_request_gate_reviewers_github",
      description:
        "Report which GitHub logins would be requested as PR reviewers for a task's lifecycle gates -- " +
        "read-only/reporting only, never actually requests a review -- via `bin/cadre sdlc " +
        "request-gate-reviewers`, the same command report-gate-reviewers-github's Step 1 documents for " +
        "Claude Code / Codex. The underlying command's own exit code 2 means the report completed but " +
        "contains refusals/withheld-conflict entries -- this tool still returns that report's JSON " +
        "normally in that case, exactly as exit 0 would; only a structural failure (exit 1: PR not " +
        "found/closed/merged, repo mismatch, `asBot` identity mismatch) surfaces as this tool's `error` " +
        "field.",
      inputSchema: z.toJSONSchema(SdlcRequestGateReviewersGithubInput),
      execute: async (rawInput: unknown): Promise<Record<string, unknown> | SdlcToolError> => {
        const input = SdlcRequestGateReviewersGithubInput.parse(rawInput);
        const root = requireRootPath(input.root);
        return runCadreSdlcAllowingReportExitCodes(buildRequestGateReviewersGithubArgs(input, root), root);
      },
    }),
  );

  api.registerTool(
    createTool({
      name: "sdlc_list_reviewer_nudge",
      description:
        "Show a task's locally-recorded reviewer-nudge publication ledger without touching GitHub, via " +
        "`bin/cadre sdlc list-reviewer-nudge` -- the same command publish-reviewer-nudge-github's Step 1 " +
        "documents for Claude Code / Codex. Zero-network and can be stale relative to what's actually " +
        "posted; a convenience, not authoritative. Read-only. GitHub-only -- there is no GitLab " +
        "equivalent of this advisory nudge skill.",
      inputSchema: z.toJSONSchema(ReviewerNudgeListInput),
      execute: async (rawInput: unknown): Promise<Record<string, unknown> | SdlcToolError> => {
        const input = ReviewerNudgeListInput.parse(rawInput);
        const root = requireRootPath(input.root);
        return runCadreSdlc(buildListReviewerNudgeArgs(input, root), root);
      },
    }),
  );

  api.registerTool(
    createTool({
      name: "sdlc_publish_reviewer_nudge",
      description:
        "Publish (or idempotently update in place) an advisory PR comment naming which GitHub logins " +
        "would be good reviewers for a task's pending gates -- never a formal review request, and never " +
        "names a withheld-conflict login even in this tool's own result (point the human at " +
        "`sdlc_request_gate_reviewers_github`'s full report for that) -- via `bin/cadre sdlc " +
        "publish-reviewer-nudge`, the same command publish-reviewer-nudge-github's Steps 2-3 document " +
        "for Claude Code / Codex. Defaults to a dry-run preview (omit `apply`); confirm the repo/PR and " +
        "task with the human before passing `apply: true`. GitHub-only -- there is no GitLab equivalent " +
        "of this advisory nudge skill.",
      inputSchema: z.toJSONSchema(SdlcPublishReviewerNudgeInput),
      execute: async (rawInput: unknown): Promise<Record<string, unknown> | SdlcToolError> => {
        const input = SdlcPublishReviewerNudgeInput.parse(rawInput);
        const root = requireRootPath(input.root);
        return runCadreSdlc(buildPublishReviewerNudgeArgs(input, root), root);
      },
    }),
  );
};

const plugin: AgentPlugin = {
  name: "cadre-lifecycle",
  manifest: { capabilities: ["tools", "rules"] },
  setup,
};

export { plugin };
export default plugin;
