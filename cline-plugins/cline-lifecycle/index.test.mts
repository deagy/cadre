import { afterAll, afterEach, beforeAll, describe, expect, it } from "vitest";
import { execFile } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { promisify } from "node:util";
import type { AgentTool } from "@cline/sdk";
import {
  plugin,
  type SetupApi,
  type SetupContext,
  buildPublishGateStatusArgs,
  buildCreateGateIssuesGitlabArgs,
  buildCreateGithubGateIssuesArgs,
  buildPublishReviewerNudgeArgs,
  buildLinkIntentFromGithubIssueArgs,
  buildLinkRequirementsFromGithubIssueArgs,
  buildRequestGateReviewersGitlabArgs,
  buildRequestGateReviewersGithubArgs,
} from "./index.ts";

const execFileAsync = promisify(execFile);

const PLUGIN_DIR = path.dirname(fileURLToPath(import.meta.url));
// One level up, not two: cline-lifecycle/ is this repository's direct
// child, matching index.ts's own PLUGIN_DIR/CADRE_BIN resolution.
const REPO_ROOT = path.resolve(PLUGIN_DIR, "..");

type RegisteredRule = Parameters<SetupApi["registerRule"]>[0];

async function registerTools(workspaceRootPath: string | undefined) {
  const tools: AgentTool[] = [];
  const api: SetupApi = {
    registerTool: (tool: AgentTool) => {
      tools.push(tool);
    },
    registerCommand: () => {},
    registerRule: () => {},
    registerMessageBuilder: () => {},
    registerProvider: () => {},
    registerAutomationEventType: () => {},
    registerMcpServer: () => {},
  };
  const ctx: SetupContext = {
    workspaceInfo: workspaceRootPath ? { rootPath: workspaceRootPath } : undefined,
  };
  await plugin.setup?.(api, ctx);
  return tools;
}

// Separate from registerTools() above (used pervasively throughout this
// file for tool-surface assertions) so that adding rule capture doesn't
// require touching every existing call site.
async function registerRules(workspaceRootPath: string | undefined) {
  const rules: RegisteredRule[] = [];
  const api: SetupApi = {
    registerTool: () => {},
    registerCommand: () => {},
    registerRule: (rule: RegisteredRule) => {
      rules.push(rule);
    },
    registerMessageBuilder: () => {},
    registerProvider: () => {},
    registerAutomationEventType: () => {},
    registerMcpServer: () => {},
  };
  const ctx: SetupContext = {
    workspaceInfo: workspaceRootPath ? { rootPath: workspaceRootPath } : undefined,
  };
  await plugin.setup?.(api, ctx);
  return rules;
}

function findTool(tools: AgentTool[], name: string): AgentTool {
  const tool = tools.find((t) => t.name === name);
  if (!tool) throw new Error(`tool ${name} was not registered`);
  return tool;
}

const GITLAB_TOOL_NAMES = [
  "sdlc_approve_from_gitlab",
  "sdlc_approve_from_gitlab_mr",
  "sdlc_link_intent_from_gitlab_issue",
  "sdlc_link_requirements_from_gitlab_issue",
];

const GITHUB_TOOL_NAMES = [
  "sdlc_approve_from_github",
  "sdlc_approve_from_github_pr",
  "sdlc_link_intent_from_github_issue",
  "sdlc_link_requirements_from_github_issue",
];

// These 10 wrap kernel subcommands not present in every agentic-sdlc release
// within this repository's declared kernel_compatibility range -- see
// index.ts's "About this plugin" comment. Grouped separately so the
// "real bin/cadre sdlc subprocess calls" describe block below can assert
// the specific, deterministic "invalid choice" failure shape they produce
// against a kernel that predates them, distinctly from the other tools'
// ordinary structured-error assertions.
const UNSHIPPED_KERNEL_TOOL_NAMES = [
  "sdlc_list_gate_issues_gitlab",
  "sdlc_create_gate_issues_gitlab",
  "sdlc_list_github_gate_issues",
  "sdlc_create_github_gate_issues",
  "sdlc_list_gate_status",
  "sdlc_publish_gate_status",
  "sdlc_request_gate_reviewers_gitlab",
  "sdlc_request_gate_reviewers_github",
  "sdlc_list_reviewer_nudge",
  "sdlc_publish_reviewer_nudge",
];

describe("cadre-lifecycle plugin", () => {
  it("declares the tools and rules capabilities and registers exactly the 5 forge-agnostic sdlc tools plus the 8 forge-specific approval/link tools and the 10 gate-issues/status/reviewer tools", async () => {
    expect(plugin.manifest.capabilities).toEqual(["tools", "rules"]);

    const tools = await registerTools(REPO_ROOT);
    expect(tools.map((t) => t.name).sort()).toEqual(
      [
        "sdlc_init",
        "sdlc_validate",
        "sdlc_plan",
        "sdlc_status",
        "sdlc_decide",
        ...GITLAB_TOOL_NAMES,
        ...GITHUB_TOOL_NAMES,
        ...UNSHIPPED_KERNEL_TOOL_NAMES,
      ].sort(),
    );
  });

  it("registers a system-prompt rule via the real registerRule injection point", async () => {
    const rules = await registerRules(REPO_ROOT);
    expect(rules).toHaveLength(1);
    const [rule] = rules;
    expect(rule.id).toBe("cline-lifecycle-system-prompt");
    const content = typeof rule.content === "function" ? await rule.content() : rule.content;
    expect(content).toContain("You are a coding assistant with access to Cadre role subagents.");
    expect(content).toMatch(/sdlc_/);
  });

  it("each tool's description names the bin/cadre sdlc subcommand it wraps", async () => {
    const tools = await registerTools(REPO_ROOT);
    expect(findTool(tools, "sdlc_init").description).toMatch(/bin\/cadre sdlc init/);
    expect(findTool(tools, "sdlc_validate").description).toMatch(/bin\/cadre sdlc validate/);
    expect(findTool(tools, "sdlc_plan").description).toMatch(/bin\/cadre sdlc plan/);
    expect(findTool(tools, "sdlc_status").description).toMatch(/bin\/cadre sdlc status/);
    expect(findTool(tools, "sdlc_decide").description).toMatch(/bin\/cadre sdlc decide/);
    expect(findTool(tools, "sdlc_approve_from_gitlab").description).toMatch(
      /bin\/cadre sdlc approve-from-gitlab`/,
    );
    expect(findTool(tools, "sdlc_approve_from_gitlab_mr").description).toMatch(
      /bin\/cadre sdlc approve-from-gitlab-mr/,
    );
    expect(findTool(tools, "sdlc_link_intent_from_gitlab_issue").description).toMatch(
      /bin\/cadre sdlc link-intent-from-gitlab-issue/,
    );
    expect(findTool(tools, "sdlc_link_requirements_from_gitlab_issue").description).toMatch(
      /bin\/cadre sdlc link-requirements-from-gitlab-issue/,
    );
    expect(findTool(tools, "sdlc_approve_from_github").description).toMatch(
      /bin\/cadre sdlc approve-from-github`/,
    );
    expect(findTool(tools, "sdlc_approve_from_github_pr").description).toMatch(
      /bin\/cadre sdlc approve-from-github-pr/,
    );
    expect(findTool(tools, "sdlc_link_intent_from_github_issue").description).toMatch(
      /bin\/cadre sdlc link-intent-from-github-issue/,
    );
    expect(findTool(tools, "sdlc_link_requirements_from_github_issue").description).toMatch(
      /bin\/cadre sdlc link-requirements-from-github-issue/,
    );
    expect(findTool(tools, "sdlc_list_gate_issues_gitlab").description).toMatch(
      /bin\/cadre sdlc list-gate-issues/,
    );
    expect(findTool(tools, "sdlc_create_gate_issues_gitlab").description).toMatch(
      /bin\/cadre sdlc create-gate-issues/,
    );
    expect(findTool(tools, "sdlc_list_github_gate_issues").description).toMatch(
      /bin\/cadre sdlc list-github-gate-issues/,
    );
    expect(findTool(tools, "sdlc_create_github_gate_issues").description).toMatch(
      /bin\/cadre sdlc create-github-gate-issues/,
    );
    expect(findTool(tools, "sdlc_list_gate_status").description).toMatch(/bin\/cadre sdlc list-gate-status/);
    expect(findTool(tools, "sdlc_publish_gate_status").description).toMatch(
      /bin\/cadre sdlc publish-gate-status/,
    );
    expect(findTool(tools, "sdlc_request_gate_reviewers_gitlab").description).toMatch(
      /bin\/cadre sdlc request-gate-reviewers-gitlab/,
    );
    expect(findTool(tools, "sdlc_request_gate_reviewers_github").description).toMatch(
      /bin\/cadre sdlc request-gate-reviewers/,
    );
    expect(findTool(tools, "sdlc_list_reviewer_nudge").description).toMatch(
      /bin\/cadre sdlc list-reviewer-nudge/,
    );
    expect(findTool(tools, "sdlc_publish_reviewer_nudge").description).toMatch(
      /bin\/cadre sdlc publish-reviewer-nudge/,
    );
  });

  it("the four approve-from-gitlab/github tools add no approval logic of their own", async () => {
    const tools = await registerTools(REPO_ROOT);
    for (const name of [
      "sdlc_approve_from_gitlab",
      "sdlc_approve_from_gitlab_mr",
      "sdlc_approve_from_github",
      "sdlc_approve_from_github_pr",
    ]) {
      const description = findTool(tools, name).description ?? "";
      expect(description).toMatch(/preparer\/verifier/);
      expect(description).toMatch(/no approval logic of its own/);
    }
  });

  it("sdlc_decide's description states it adds no approval logic of its own", async () => {
    const tools = await registerTools(REPO_ROOT);
    const description = findTool(tools, "sdlc_decide").description ?? "";
    expect(description).toMatch(/preparer\/verifier/);
    expect(description).toMatch(/no approval logic of its own/);
  });

  it("every one of the 4 tools throws when no root is given and no workspace root was resolved", async () => {
    const tools = await registerTools(undefined);
    await expect(
      findTool(tools, "sdlc_validate").execute({}, {} as never),
    ).rejects.toThrow(/root/i);
    await expect(
      findTool(tools, "sdlc_status").execute({ taskId: "x" }, {} as never),
    ).rejects.toThrow(/root/i);
    await expect(
      findTool(tools, "sdlc_init").execute(
        { profile: "secure-cloud", projectId: "x", classification: "internal" },
        {} as never,
      ),
    ).rejects.toThrow(/root/i);
    await expect(
      findTool(tools, "sdlc_decide").execute(
        {
          taskId: "x",
          gate: "G1",
          role: "test",
          decision: "approved",
          actorId: "tester",
          evidenceUri: "doc:test",
        },
        {} as never,
      ),
    ).rejects.toThrow(/root/i);
    await expect(
      findTool(tools, "sdlc_approve_from_gitlab").execute(
        {
          taskId: "x",
          gate: "G1",
          role: "test",
          projectPath: "group/project",
          mrIid: 1,
          approvalId: "1",
          approverUsername: "tester",
          commitSha: "deadbeef",
        },
        {} as never,
      ),
    ).rejects.toThrow(/root/i);
    await expect(
      findTool(tools, "sdlc_approve_from_gitlab_mr").execute(
        { taskId: "x", gate: "G1", role: "test", projectPath: "group/project", mrIid: 1 },
        {} as never,
      ),
    ).rejects.toThrow(/root/i);
    await expect(
      findTool(tools, "sdlc_link_intent_from_gitlab_issue").execute(
        { taskId: "x", role: "test", projectPath: "group/project", issueIid: 1 },
        {} as never,
      ),
    ).rejects.toThrow(/root/i);
    await expect(
      findTool(tools, "sdlc_link_requirements_from_gitlab_issue").execute(
        { taskId: "x", role: "test", projectPath: "group/project", issueIid: 1 },
        {} as never,
      ),
    ).rejects.toThrow(/root/i);
    await expect(
      findTool(tools, "sdlc_approve_from_github").execute(
        {
          taskId: "x",
          gate: "G1",
          role: "test",
          repo: "owner/repo",
          pr: 1,
          reviewId: "1",
          reviewerLogin: "tester",
          commitSha: "deadbeef",
        },
        {} as never,
      ),
    ).rejects.toThrow(/root/i);
    await expect(
      findTool(tools, "sdlc_approve_from_github_pr").execute(
        { taskId: "x", gate: "G1", role: "test", repo: "owner/repo", pr: 1 },
        {} as never,
      ),
    ).rejects.toThrow(/root/i);
    await expect(
      findTool(tools, "sdlc_list_gate_issues_gitlab").execute({ taskId: "x" }, {} as never),
    ).rejects.toThrow(/root/i);
    await expect(
      findTool(tools, "sdlc_create_gate_issues_gitlab").execute(
        { taskId: "x", projectPath: "group/project", asBot: "bot" },
        {} as never,
      ),
    ).rejects.toThrow(/root/i);
    await expect(
      findTool(tools, "sdlc_list_github_gate_issues").execute({ taskId: "x" }, {} as never),
    ).rejects.toThrow(/root/i);
    await expect(
      findTool(tools, "sdlc_create_github_gate_issues").execute(
        { taskId: "x", repo: "owner/repo", asBot: "bot", allowClassification: "internal" },
        {} as never,
      ),
    ).rejects.toThrow(/root/i);
    await expect(
      findTool(tools, "sdlc_list_gate_status").execute({ taskId: "x" }, {} as never),
    ).rejects.toThrow(/root/i);
    await expect(
      findTool(tools, "sdlc_publish_gate_status").execute(
        {
          forge: "gitlab",
          taskId: "x",
          projectPath: "group/project",
          mrIid: 1,
          asBot: "bot",
          allowClassification: "internal",
        },
        {} as never,
      ),
    ).rejects.toThrow(/root/i);
    await expect(
      findTool(tools, "sdlc_request_gate_reviewers_gitlab").execute(
        { taskId: "x", projectPath: "group/project", mrIid: 1, asBot: "bot", allowClassification: "internal" },
        {} as never,
      ),
    ).rejects.toThrow(/root/i);
    await expect(
      findTool(tools, "sdlc_request_gate_reviewers_github").execute(
        { taskId: "x", repo: "owner/repo", pr: 1, asBot: "bot", allowClassification: "internal" },
        {} as never,
      ),
    ).rejects.toThrow(/root/i);
    await expect(
      findTool(tools, "sdlc_list_reviewer_nudge").execute({ taskId: "x" }, {} as never),
    ).rejects.toThrow(/root/i);
    await expect(
      findTool(tools, "sdlc_publish_reviewer_nudge").execute(
        { taskId: "x", repo: "owner/repo", pr: 1, asBot: "bot", allowClassification: "internal" },
        {} as never,
      ),
    ).rejects.toThrow(/root/i);
  });

  describe("argument construction (kernel-free)", () => {
    // These assert the exact argv built for the kernel subprocess directly,
    // with no subprocess call at all -- unlike the "real subprocess calls"
    // tests below, these can't be fooled by a missing/stale kernel binary
    // into passing vacuously, and are the right tool for asserting a
    // branch/join was built correctly rather than merely "didn't throw".
    // Covers gaps two review rounds found untested: the
    // sdlc_publish_gate_status discriminated union's GitHub branch (only
    // the GitLab branch had any coverage, even at the smoke-test level), and
    // the `gates` array -> comma-separated `--gates` string join used by
    // buildCreateGateIssuesGitlabArgs/buildCreateGithubGateIssuesArgs and
    // buildRequestGateReviewersGitlabArgs/buildRequestGateReviewersGithubArgs
    // -- both the GitLab and GitHub side of each pair are exported and
    // tested here (a first round only covered the GitLab side and treated
    // it as standing in for both, which a follow-up review correctly
    // called out as an overclaim -- the expression is identical, but
    // "identical code" and "tested code" aren't the same thing).

    it("buildPublishGateStatusArgs (gitlab branch) emits --project-path/--mr-iid, not --repo/--pr", () => {
      const args = buildPublishGateStatusArgs(
        {
          forge: "gitlab",
          taskId: "t1",
          projectPath: "group/project",
          mrIid: 7,
          asBot: "bot",
          apply: true,
        },
        "/root",
      );
      expect(args).toEqual([
        "sdlc",
        "publish-gate-status",
        "--root",
        "/root",
        "--task-id",
        "t1",
        "--forge",
        "gitlab",
        "--project-path",
        "group/project",
        "--mr-iid",
        "7",
        "--as-bot",
        "bot",
        "--apply",
      ]);
    });

    it("buildPublishGateStatusArgs (github branch) emits --repo/--pr, not --project-path/--mr-iid", () => {
      const args = buildPublishGateStatusArgs(
        {
          forge: "github",
          taskId: "t1",
          repo: "owner/repo",
          pr: 9,
          asBot: "bot",
          allowClassification: "internal",
        },
        "/root",
      );
      expect(args).toEqual([
        "sdlc",
        "publish-gate-status",
        "--root",
        "/root",
        "--task-id",
        "t1",
        "--forge",
        "github",
        "--repo",
        "owner/repo",
        "--pr",
        "9",
        "--as-bot",
        "bot",
        "--allow-classification",
        "internal",
      ]);
      expect(args).not.toContain("--project-path");
      expect(args).not.toContain("--mr-iid");
    });

    it("buildCreateGateIssuesGitlabArgs joins a gates array into a single comma-separated --gates value", () => {
      const args = buildCreateGateIssuesGitlabArgs(
        { taskId: "t1", projectPath: "group/project", asBot: "bot", gates: ["G3", "G9"] },
        "/root",
      );
      const gatesIndex = args.indexOf("--gates");
      expect(gatesIndex).toBeGreaterThan(-1);
      expect(args[gatesIndex + 1]).toBe("G3,G9");
      // Neither element ends up as its own argv entry -- a naive
      // implementation could pass the array through unjoined.
      expect(args).not.toContain("G3");
      expect(args).not.toContain("G9");
    });

    it("buildRequestGateReviewersGitlabArgs omits --gates entirely when the array is empty or absent", () => {
      const withEmpty = buildRequestGateReviewersGitlabArgs(
        { taskId: "t1", projectPath: "group/project", mrIid: 1, asBot: "bot", gates: [] },
        "/root",
      );
      const withAbsent = buildRequestGateReviewersGitlabArgs(
        { taskId: "t1", projectPath: "group/project", mrIid: 1, asBot: "bot" },
        "/root",
      );
      expect(withEmpty).not.toContain("--gates");
      expect(withAbsent).not.toContain("--gates");
    });

    // GitHub-side counterparts of the two `gates` tests above -- a prior
    // review round found the GitLab-side pair was exported and tested but
    // the identical `input.gates?.length ? args.push("--gates", ...) : ...`
    // expression in the GitHub builders was left with zero coverage despite
    // a code comment implying the GitLab pair "stood in for all of them."

    it("buildCreateGithubGateIssuesArgs joins a gates array into a single comma-separated --gates value", () => {
      const args = buildCreateGithubGateIssuesArgs(
        { taskId: "t1", repo: "owner/repo", asBot: "bot", gates: ["G3", "G9"] },
        "/root",
      );
      const gatesIndex = args.indexOf("--gates");
      expect(gatesIndex).toBeGreaterThan(-1);
      expect(args[gatesIndex + 1]).toBe("G3,G9");
      expect(args).not.toContain("G3");
      expect(args).not.toContain("G9");
    });

    it("buildCreateGithubGateIssuesArgs includes --allow-public-repo only when explicitly set", () => {
      const withFlag = buildCreateGithubGateIssuesArgs(
        { taskId: "t1", repo: "owner/repo", asBot: "bot", allowPublicRepo: true },
        "/root",
      );
      const withoutFlag = buildCreateGithubGateIssuesArgs(
        { taskId: "t1", repo: "owner/repo", asBot: "bot" },
        "/root",
      );
      expect(withFlag).toContain("--allow-public-repo");
      expect(withoutFlag).not.toContain("--allow-public-repo");
    });

    it("buildRequestGateReviewersGithubArgs omits --gates entirely when the array is empty or absent", () => {
      const withEmpty = buildRequestGateReviewersGithubArgs(
        { taskId: "t1", repo: "owner/repo", pr: 1, asBot: "bot", gates: [] },
        "/root",
      );
      const withAbsent = buildRequestGateReviewersGithubArgs(
        { taskId: "t1", repo: "owner/repo", pr: 1, asBot: "bot" },
        "/root",
      );
      expect(withEmpty).not.toContain("--gates");
      expect(withAbsent).not.toContain("--gates");
    });

    // The four consequential opt-in flags below are the ones this plugin
    // previously had no way to pass at all. `--reconcile-assignees` is the
    // one that mattered most in practice: the kernel reports assignee drift
    // and changes nothing by default, and gitlab-gate-tracking's SKILL.md
    // makes re-running with the flag the human-authorized remediation, so a
    // Cline session could see the drift but never correct it. These assert
    // both directions -- present when set, and (critically, for flags whose
    // whole point is that they are not the default) absent when omitted.

    it("buildCreateGateIssuesGitlabArgs emits the four opt-in flags only when explicitly set", () => {
      const withFlags = buildCreateGateIssuesGitlabArgs(
        {
          taskId: "t1",
          projectPath: "group/project",
          asBot: "bot",
          linkType: "relates_to",
          includeScope: true,
          reconcileAssignees: true,
          breakLock: true,
        },
        "/root",
      );
      expect(withFlags).toContain("--link-type");
      expect(withFlags[withFlags.indexOf("--link-type") + 1]).toBe("relates_to");
      expect(withFlags).toContain("--include-scope");
      expect(withFlags).toContain("--reconcile-assignees");
      expect(withFlags).toContain("--break-lock");

      const withoutFlags = buildCreateGateIssuesGitlabArgs(
        { taskId: "t1", projectPath: "group/project", asBot: "bot" },
        "/root",
      );
      expect(withoutFlags).not.toContain("--link-type");
      expect(withoutFlags).not.toContain("--include-scope");
      expect(withoutFlags).not.toContain("--reconcile-assignees");
      expect(withoutFlags).not.toContain("--break-lock");
    });

    it("buildCreateGithubGateIssuesArgs emits its three opt-in flags only when explicitly set, and has no --link-type", () => {
      const withFlags = buildCreateGithubGateIssuesArgs(
        {
          taskId: "t1",
          repo: "owner/repo",
          asBot: "bot",
          includeScope: true,
          reconcileAssignees: true,
          breakLock: true,
        },
        "/root",
      );
      expect(withFlags).toContain("--include-scope");
      expect(withFlags).toContain("--reconcile-assignees");
      expect(withFlags).toContain("--break-lock");
      // The kernel's GitHub variant has no --link-type; the GitLab-only
      // field must not leak across into this builder.
      expect(withFlags).not.toContain("--link-type");

      const withoutFlags = buildCreateGithubGateIssuesArgs(
        { taskId: "t1", repo: "owner/repo", asBot: "bot" },
        "/root",
      );
      expect(withoutFlags).not.toContain("--include-scope");
      expect(withoutFlags).not.toContain("--reconcile-assignees");
      expect(withoutFlags).not.toContain("--break-lock");
    });

    it("buildPublishGateStatusArgs emits --break-lock on both forge branches only when set", () => {
      const gitlab = buildPublishGateStatusArgs(
        { forge: "gitlab", taskId: "t1", projectPath: "group/project", mrIid: 7, asBot: "bot", breakLock: true },
        "/root",
      );
      const github = buildPublishGateStatusArgs(
        { forge: "github", taskId: "t1", repo: "owner/repo", pr: 9, asBot: "bot", breakLock: true },
        "/root",
      );
      expect(gitlab).toContain("--break-lock");
      expect(github).toContain("--break-lock");

      const gitlabWithout = buildPublishGateStatusArgs(
        { forge: "gitlab", taskId: "t1", projectPath: "group/project", mrIid: 7, asBot: "bot" },
        "/root",
      );
      expect(gitlabWithout).not.toContain("--break-lock");
    });

    it("buildPublishReviewerNudgeArgs emits --break-lock only when set", () => {
      const withFlag = buildPublishReviewerNudgeArgs(
        { taskId: "t1", repo: "owner/repo", pr: 9, asBot: "bot", breakLock: true },
        "/root",
      );
      const withoutFlag = buildPublishReviewerNudgeArgs(
        { taskId: "t1", repo: "owner/repo", pr: 9, asBot: "bot" },
        "/root",
      );
      expect(withFlag).toContain("--break-lock");
      expect(withoutFlag).not.toContain("--break-lock");
    });

    // The GitHub link-source pair takes --issue-number where the GitLab pair
    // takes --issue-iid. Asserting the exact argv catches a copy-paste of the
    // GitLab builder, which would fail only at runtime against a real kernel.

    it("buildLinkIntentFromGithubIssueArgs emits --repo/--issue-number, not --project-path/--issue-iid", () => {
      const args = buildLinkIntentFromGithubIssueArgs(
        { taskId: "t1", role: "product_owner", repo: "owner/repo", issueNumber: 42 },
        "/root",
      );
      expect(args).toEqual([
        "sdlc",
        "link-intent-from-github-issue",
        "--root",
        "/root",
        "--task-id",
        "t1",
        "--role",
        "product_owner",
        "--repo",
        "owner/repo",
        "--issue-number",
        "42",
      ]);
      expect(args).not.toContain("--issue-iid");
      expect(args).not.toContain("--project-path");
    });

    it("buildLinkRequirementsFromGithubIssueArgs targets its own subcommand, not the intent one", () => {
      const args = buildLinkRequirementsFromGithubIssueArgs(
        { taskId: "t1", role: "product_owner", repo: "owner/repo", issueNumber: 42 },
        "/root",
      );
      expect(args[1]).toBe("link-requirements-from-github-issue");
      expect(args).toContain("--issue-number");
      expect(args).not.toContain("--issue-iid");
    });
  });

  describe("real bin/cadre sdlc subprocess calls", () => {
    // These exercise the actual CLI path (same convention as cline/'s own
    // index.test.mts): the local environment used to develop this plugin
    // happens to have the external `agentic-sdlc` kernel installed and on
    // PATH, so these are real, deterministic outcomes -- a structured
    // error for an un-onboarded/nonexistent target, not a mock. If
    // `agentic-sdlc` is not installed elsewhere, `bin/cadre sdlc` itself
    // fails closed with its own clear message, which these tests would
    // also correctly observe as `result.error` being set.

    it("sdlc_validate returns a structured error (not a throw) for this repository, which has no .agentic-sdlc/", async () => {
      const tools = await registerTools(REPO_ROOT);
      const result = (await findTool(tools, "sdlc_validate").execute({}, {} as never)) as Record<string, unknown>;
      // Either shape is a legitimate real outcome depending on whether
      // agentic-sdlc is installed in the environment running this test:
      // a kernel-reported validation failure (valid: false) or this
      // plugin's own structured error if the kernel binary is missing.
      expect(result.valid === false || typeof result.error === "string").toBe(true);
    });

    it("sdlc_status returns a structured error for a task with no run record", async () => {
      const tools = await registerTools(REPO_ROOT);
      const result = (await findTool(tools, "sdlc_status").execute(
        { taskId: "cline-lifecycle-test-nonexistent-task" },
        {} as never,
      )) as Record<string, unknown>;
      expect(typeof result.error).toBe("string");
    });

    it("sdlc_decide returns a structured error (never a false success) for a nonexistent task/gate", async () => {
      // This repository has no .agentic-sdlc/ run record at all, so a real
      // decide call against it can never legitimately succeed -- the
      // kernel either rejects the gate/task as unknown, or (if the
      // installed kernel version predates the `decide` subcommand) its
      // own argparse rejects the subcommand name itself. Either way this
      // must surface as result.error, never as a false "success" -- the
      // one outcome that would mean this tool is laundering a kernel
      // refusal into an apparent approval.
      const tools = await registerTools(REPO_ROOT);
      const result = (await findTool(tools, "sdlc_decide").execute(
        {
          taskId: "cline-lifecycle-test-nonexistent-task",
          gate: "G1",
          role: "cline-lifecycle-test-role",
          decision: "approved",
          actorId: "cline-lifecycle-test-actor",
          evidenceUri: "doc:cline-lifecycle-test",
        },
        {} as never,
      )) as Record<string, unknown>;
      expect(typeof result.error).toBe("string");
      expect(result.status).not.toBe("approved");
    });

    it("sdlc_approve_from_gitlab returns a structured error (never a false success) for a nonexistent task/gate", async () => {
      const tools = await registerTools(REPO_ROOT);
      const result = (await findTool(tools, "sdlc_approve_from_gitlab").execute(
        {
          taskId: "cline-lifecycle-test-nonexistent-task",
          gate: "G1",
          role: "cline-lifecycle-test-role",
          projectPath: "cline-lifecycle-test/project",
          mrIid: 1,
          approvalId: "1",
          approverUsername: "cline-lifecycle-test-approver",
          commitSha: "0000000000000000000000000000000000000000",
        },
        {} as never,
      )) as Record<string, unknown>;
      expect(typeof result.error).toBe("string");
      expect(result.status).not.toBe("approved");
    });

    it("sdlc_approve_from_gitlab_mr returns a structured error (never a false success) for a nonexistent task/gate", async () => {
      const tools = await registerTools(REPO_ROOT);
      const result = (await findTool(tools, "sdlc_approve_from_gitlab_mr").execute(
        {
          taskId: "cline-lifecycle-test-nonexistent-task",
          gate: "G1",
          role: "cline-lifecycle-test-role",
          projectPath: "cline-lifecycle-test/project",
          mrIid: 1,
        },
        {} as never,
      )) as Record<string, unknown>;
      expect(typeof result.error).toBe("string");
      expect(result.status).not.toBe("approved");
    });

    it("sdlc_link_intent_from_gitlab_issue returns a structured error for a nonexistent task", async () => {
      const tools = await registerTools(REPO_ROOT);
      const result = (await findTool(tools, "sdlc_link_intent_from_gitlab_issue").execute(
        {
          taskId: "cline-lifecycle-test-nonexistent-task",
          role: "cline-lifecycle-test-role",
          projectPath: "cline-lifecycle-test/project",
          issueIid: 1,
        },
        {} as never,
      )) as Record<string, unknown>;
      expect(typeof result.error).toBe("string");
    });

    it("sdlc_link_requirements_from_gitlab_issue returns a structured error for a nonexistent task", async () => {
      const tools = await registerTools(REPO_ROOT);
      const result = (await findTool(tools, "sdlc_link_requirements_from_gitlab_issue").execute(
        {
          taskId: "cline-lifecycle-test-nonexistent-task",
          role: "cline-lifecycle-test-role",
          projectPath: "cline-lifecycle-test/project",
          issueIid: 1,
        },
        {} as never,
      )) as Record<string, unknown>;
      expect(typeof result.error).toBe("string");
    });

    it("sdlc_approve_from_github returns a structured error (never a false success) for a nonexistent task/gate", async () => {
      const tools = await registerTools(REPO_ROOT);
      const result = (await findTool(tools, "sdlc_approve_from_github").execute(
        {
          taskId: "cline-lifecycle-test-nonexistent-task",
          gate: "G1",
          role: "cline-lifecycle-test-role",
          repo: "cline-lifecycle-test/repo",
          pr: 1,
          reviewId: "1",
          reviewerLogin: "cline-lifecycle-test-reviewer",
          commitSha: "0000000000000000000000000000000000000000",
        },
        {} as never,
      )) as Record<string, unknown>;
      expect(typeof result.error).toBe("string");
      expect(result.status).not.toBe("approved");
    });

    it("sdlc_approve_from_github_pr returns a structured error (never a false success) for a nonexistent task/gate", async () => {
      const tools = await registerTools(REPO_ROOT);
      const result = (await findTool(tools, "sdlc_approve_from_github_pr").execute(
        {
          taskId: "cline-lifecycle-test-nonexistent-task",
          gate: "G1",
          role: "cline-lifecycle-test-role",
          repo: "cline-lifecycle-test/repo",
          pr: 1,
        },
        {} as never,
      )) as Record<string, unknown>;
      expect(typeof result.error).toBe("string");
      expect(result.status).not.toBe("approved");
    });

    // GitHub counterparts of the two link-source-gitlab tests above. These
    // also confirm the subcommands resolve at all: they were left unmirrored
    // until now because the kernel this plugin was first developed against
    // did not ship them, so an "invalid choice" argparse failure here would
    // mean the kernel_compatibility floor is wrong, not just that the task
    // is missing.

    it("sdlc_link_intent_from_github_issue returns a structured error for a nonexistent task", async () => {
      const tools = await registerTools(REPO_ROOT);
      const result = (await findTool(tools, "sdlc_link_intent_from_github_issue").execute(
        {
          taskId: "cline-lifecycle-test-nonexistent-task",
          role: "product_owner",
          repo: "cline-lifecycle-test/repo",
          issueNumber: 1,
        },
        {} as never,
      )) as Record<string, unknown>;
      expect(typeof result.error).toBe("string");
      expect(result.error as string).not.toMatch(/invalid choice/);
    });

    it("sdlc_link_requirements_from_github_issue returns a structured error for a nonexistent task", async () => {
      const tools = await registerTools(REPO_ROOT);
      const result = (await findTool(tools, "sdlc_link_requirements_from_github_issue").execute(
        {
          taskId: "cline-lifecycle-test-nonexistent-task",
          role: "product_owner",
          repo: "cline-lifecycle-test/repo",
          issueNumber: 1,
        },
        {} as never,
      )) as Record<string, unknown>;
      expect(typeof result.error).toBe("string");
      expect(result.error as string).not.toMatch(/invalid choice/);
    });

    describe("kernel subcommands not shipped by every agentic-sdlc release in range", () => {
      // These 10 tools wrap kernel subcommands the packaged skills document
      // that were, when first added, missing from the kernel version this
      // plugin's development environment had installed -- see index.ts's
      // "About this plugin" comment. `provider.json` now pins
      // `kernel_compatibility.minimum` to the fixed agentic-sdlc release, so
      // in any environment satisfying that pin (this one included) these
      // subcommands always return a real success/refusal shape, never the
      // historical "invalid choice" argparse failure -- assert the real
      // shape, the same way sdlc_init/sdlc_plan's tests do, not just
      // `toBeTypeOf("object")` (which would also pass against a stale
      // "invalid choice" error and so proves nothing about kernel support).
      // The 4 read-only list-* tools need no seed data and always succeed;
      // the other 6 write-style tools need a run record this repository's
      // checkout never has, so they always hit the same genuine, structured
      // ENOENT refusal instead.

      it("sdlc_list_gate_issues_gitlab returns a real ledger, not an error", async () => {
        const tools = await registerTools(REPO_ROOT);
        const result = (await findTool(tools, "sdlc_list_gate_issues_gitlab").execute(
          { taskId: "cline-lifecycle-test-nonexistent-task" },
          {} as never,
        )) as Record<string, unknown>;
        expect(result.error).toBeUndefined();
        expect(result.entries).toEqual({});
      });

      it("sdlc_create_gate_issues_gitlab returns a structured error for a task with no run record", async () => {
        const tools = await registerTools(REPO_ROOT);
        const result = (await findTool(tools, "sdlc_create_gate_issues_gitlab").execute(
          {
            taskId: "cline-lifecycle-test-nonexistent-task",
            projectPath: "cline-lifecycle-test/project",
            asBot: "cline-lifecycle-test-bot",
          },
          {} as never,
        )) as Record<string, unknown>;
        expect(typeof result.error).toBe("string");
      });

      it("sdlc_list_github_gate_issues returns a real ledger, not an error", async () => {
        const tools = await registerTools(REPO_ROOT);
        const result = (await findTool(tools, "sdlc_list_github_gate_issues").execute(
          { taskId: "cline-lifecycle-test-nonexistent-task" },
          {} as never,
        )) as Record<string, unknown>;
        expect(result.error).toBeUndefined();
        expect(result.entries).toEqual({});
      });

      it("sdlc_create_github_gate_issues returns a structured error for a task with no run record", async () => {
        const tools = await registerTools(REPO_ROOT);
        const result = (await findTool(tools, "sdlc_create_github_gate_issues").execute(
          {
            taskId: "cline-lifecycle-test-nonexistent-task",
            repo: "cline-lifecycle-test/repo",
            asBot: "cline-lifecycle-test-bot",
            allowClassification: "internal",
          },
          {} as never,
        )) as Record<string, unknown>;
        expect(typeof result.error).toBe("string");
      });

      it("sdlc_list_gate_status returns real per-forge ledgers, not an error", async () => {
        const tools = await registerTools(REPO_ROOT);
        const result = (await findTool(tools, "sdlc_list_gate_status").execute(
          { taskId: "cline-lifecycle-test-nonexistent-task" },
          {} as never,
        )) as Record<string, unknown>;
        expect(result.error).toBeUndefined();
        expect(result).toHaveProperty("github");
        expect(result).toHaveProperty("gitlab");
      });

      it("sdlc_publish_gate_status (gitlab) returns a structured error for a task with no run record", async () => {
        const tools = await registerTools(REPO_ROOT);
        const result = (await findTool(tools, "sdlc_publish_gate_status").execute(
          {
            forge: "gitlab",
            taskId: "cline-lifecycle-test-nonexistent-task",
            projectPath: "cline-lifecycle-test/project",
            mrIid: 1,
            asBot: "cline-lifecycle-test-bot",
            allowClassification: "internal",
          },
          {} as never,
        )) as Record<string, unknown>;
        expect(typeof result.error).toBe("string");
      });

      it("sdlc_publish_gate_status (github) returns a structured error for a task with no run record", async () => {
        const tools = await registerTools(REPO_ROOT);
        const result = (await findTool(tools, "sdlc_publish_gate_status").execute(
          {
            forge: "github",
            taskId: "cline-lifecycle-test-nonexistent-task",
            repo: "cline-lifecycle-test/repo",
            pr: 1,
            asBot: "cline-lifecycle-test-bot",
            allowClassification: "internal",
          },
          {} as never,
        )) as Record<string, unknown>;
        expect(typeof result.error).toBe("string");
      });

      it("sdlc_request_gate_reviewers_gitlab returns a structured error for a task with no run record", async () => {
        const tools = await registerTools(REPO_ROOT);
        const result = (await findTool(tools, "sdlc_request_gate_reviewers_gitlab").execute(
          {
            taskId: "cline-lifecycle-test-nonexistent-task",
            projectPath: "cline-lifecycle-test/project",
            mrIid: 1,
            asBot: "cline-lifecycle-test-bot",
            allowClassification: "internal",
          },
          {} as never,
        )) as Record<string, unknown>;
        expect(typeof result.error).toBe("string");
      });

      it("sdlc_request_gate_reviewers_github returns a structured error for a task with no run record", async () => {
        const tools = await registerTools(REPO_ROOT);
        const result = (await findTool(tools, "sdlc_request_gate_reviewers_github").execute(
          {
            taskId: "cline-lifecycle-test-nonexistent-task",
            repo: "cline-lifecycle-test/repo",
            pr: 1,
            asBot: "cline-lifecycle-test-bot",
            allowClassification: "internal",
          },
          {} as never,
        )) as Record<string, unknown>;
        expect(typeof result.error).toBe("string");
      });

      it("sdlc_list_reviewer_nudge returns a real ledger, not an error", async () => {
        const tools = await registerTools(REPO_ROOT);
        const result = (await findTool(tools, "sdlc_list_reviewer_nudge").execute(
          { taskId: "cline-lifecycle-test-nonexistent-task" },
          {} as never,
        )) as Record<string, unknown>;
        expect(result.error).toBeUndefined();
        expect(result.entries).toEqual([]);
      });

      it("sdlc_publish_reviewer_nudge returns a structured error for a task with no run record", async () => {
        const tools = await registerTools(REPO_ROOT);
        const result = (await findTool(tools, "sdlc_publish_reviewer_nudge").execute(
          {
            taskId: "cline-lifecycle-test-nonexistent-task",
            repo: "cline-lifecycle-test/repo",
            pr: 1,
            asBot: "cline-lifecycle-test-bot",
            allowClassification: "internal",
          },
          {} as never,
        )) as Record<string, unknown>;
        expect(typeof result.error).toBe("string");
      });
    });

    describe("sdlc_init dry-run against a scratch project", () => {
      let scratchDir: string;

      beforeAll(async () => {
        scratchDir = mkdtempSync(path.join(tmpdir(), "cline-lifecycle-sdlc-init-test-"));
        await execFileAsync("git", ["init", "-q"], { cwd: scratchDir });
      });

      afterAll(() => {
        rmSync(scratchDir, { recursive: true, force: true });
      });

      it("previews without writing anything, or reports a structured error if the kernel is unavailable", async () => {
        const tools = await registerTools(scratchDir);
        const result = (await findTool(tools, "sdlc_init").execute(
          {
            profile: "secure-cloud",
            projectId: "cline-lifecycle-test-proj",
            classification: "internal",
            dryRun: true,
          },
          {} as never,
        )) as Record<string, unknown>;

        if (result.error) {
          expect(typeof result.error).toBe("string");
          return;
        }
        expect(result.status).toBe("dry-run");
        expect(result.mutation).toBe(false);
        expect(Array.isArray(result.would_create)).toBe(true);
      });
    });

    describe("sdlc_plan against a scratch project", () => {
      let scratchDir: string;
      let initSucceeded = false;

      beforeAll(async () => {
        scratchDir = mkdtempSync(path.join(tmpdir(), "cline-lifecycle-sdlc-plan-test-"));
        await execFileAsync("git", ["init", "-q"], { cwd: scratchDir });
        // sdlc_plan requires .agentic-sdlc/project.json to already exist -- seed it
        // with a real (non-dry-run) sdlc_init first, exactly like a real caller
        // would have to. Track whether this succeeded so the assertions below can
        // tell "kernel unavailable" apart from "plan itself failed unexpectedly".
        const tools = await registerTools(scratchDir);
        const initResult = (await findTool(tools, "sdlc_init").execute(
          { profile: "secure-cloud", projectId: "cline-lifecycle-test-plan-proj", classification: "internal" },
          {} as never,
        )) as Record<string, unknown>;
        initSucceeded = !initResult.error;
      });

      afterAll(() => {
        rmSync(scratchDir, { recursive: true, force: true });
      });

      it("writes a dispatch plan and run record for a new task-id, or reports a structured error if the kernel is unavailable", async () => {
        const tools = await registerTools(scratchDir);
        const result = (await findTool(tools, "sdlc_plan").execute(
          { taskId: "cline-lifecycle-test-plan-task", task: "cline-lifecycle-test task" },
          {} as never,
        )) as Record<string, unknown>;

        if (!initSucceeded) {
          // The kernel itself wasn't usable for sdlc_init either (e.g. not
          // installed in this environment) -- sdlc_plan failing the same way
          // is expected, not a signal about sdlc_plan's own correctness.
          expect(typeof result.error).toBe("string");
          return;
        }
        // Seeding succeeded, so a real plan call must actually succeed and
        // return real fields -- an error here would be a genuine sdlc_plan
        // defect, not an environment gap.
        expect(result.error).toBeUndefined();
        expect(result.task_id).toBe("cline-lifecycle-test-plan-task");
        expect(typeof result.status).toBe("string");
      });
    });
  });
});
