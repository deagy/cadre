import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { execFile } from "node:child_process";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { promisify } from "node:util";
import type { AgentTool, AgentToolContext } from "@cline/sdk";
import { plugin, type SetupApi, type SetupContext } from "./index.ts";

const execFileAsync = promisify(execFile);

const PLUGIN_DIR = path.dirname(fileURLToPath(import.meta.url));
// One level up, not two: cline/ is this plugin root's direct child, matching
// index.ts's own PLUGIN_DIR/CADRE_BIN resolution. Two levels up used to land
// on the multi-project workspace root (/home/deagy/sdk) rather than this
// repository -- harmless only because every REPO_ROOT-based test below
// happens to pass an explicit `files` argument, bypassing the git-status
// discovery path that would otherwise hit the workspace root's git state.
const REPO_ROOT = path.resolve(PLUGIN_DIR, "..");

type RegisteredRule = Parameters<SetupApi["registerRule"]>[0];

async function registerContributions(workspaceRootPath: string | undefined) {
  const tools: AgentTool[] = [];
  const rules: RegisteredRule[] = [];
  const api: SetupApi = {
    registerTool: (tool: AgentTool) => {
      tools.push(tool);
    },
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
  return { tools, rules };
}

async function registerTools(workspaceRootPath: string | undefined) {
  return (await registerContributions(workspaceRootPath)).tools;
}

function findTool(tools: AgentTool[], name: string): AgentTool {
  const tool = tools.find((t) => t.name === name);
  if (!tool) throw new Error(`tool ${name} was not registered`);
  return tool;
}

describe("cadre plugin", () => {
  it("declares the tools and rules capabilities and registers exactly one tool: agents_select", async () => {
    expect(plugin.manifest.capabilities).toEqual(["tools", "rules"]);

    const { tools } = await registerContributions(REPO_ROOT);
    expect(tools.map((t) => t.name)).toEqual(["agents_select"]);
    expect(tools[0].description).toMatch(/plan only/i);
    expect(tools[0].description).toMatch(/never invokes agents/i);
    expect(tools[0].description).toMatch(/bin\/cadre select/);
    // `setup(api, ctx)` exposes no spawn/team primitive to a registered
    // tool's execute() (see runner-adapters.md's "## Cline" section); the
    // description must say this tool does not dispatch rather than imply it
    // does. Deliberately NOT asserting the old "cannot dispatch" phrasing:
    // that claimed an API-wide impossibility, which `cline-agents` disproves
    // by dispatching through its own embedded ClineCore, and which invited
    // the broader over-read that a plugin has no hook stages at all (see
    // hook-surface.test.mts). This plugin not dispatching is the fact; the
    // API forbidding it universally is not.
    expect(tools[0].description).toMatch(/does not dispatch/i);
    expect(tools[0].description).toMatch(/runner-adapters\.md/);
  });

  it("registers a system-prompt rule via the real registerRule injection point", async () => {
    const { rules } = await registerContributions(REPO_ROOT);
    expect(rules).toHaveLength(1);
    const [rule] = rules;
    expect(rule.id).toBe("cadre-system-prompt");
    const content = typeof rule.content === "function" ? await rule.content() : rule.content;
    expect(content).toContain("You are a coding assistant with access to Cadre role subagents.");
    expect(content).toMatch(/agents_select/);
  });

  it("agents_select returns a real dispatch plan for this repository", async () => {
    const tools = await registerTools(REPO_ROOT);
    const tool = findTool(tools, "agents_select");

    const result = (await tool.execute(
      { task: "Review README changes", files: "README.md", classification: "internal" },
      {} as never,
    )) as Record<string, unknown>;

    expect(result.error).toBeUndefined();
    expect(result).toHaveProperty("status");
    expect(result).toHaveProperty("agents");
    expect(result).toHaveProperty("matched_routes");
  });

  it("agents_select honors an explicit taskId in the returned plan", async () => {
    const tools = await registerTools(REPO_ROOT);
    const tool = findTool(tools, "agents_select");

    const result = (await tool.execute(
      {
        task: "Review README changes",
        files: "README.md",
        classification: "internal",
        taskId: "PLUGIN-TEST-TASK-ID",
      },
      {} as never,
    )) as Record<string, unknown>;

    expect(result.error).toBeUndefined();
    expect(result.task_id).toBe("PLUGIN-TEST-TASK-ID");
  });

  it("agents_select returns needs-triage for a scope with no matching route", async () => {
    const tools = await registerTools(REPO_ROOT);
    const tool = findTool(tools, "agents_select");

    // An explicit file with no keyword/path match in routing.yaml, and task text with
    // no routable keywords either, gives the selector nothing to route on. Pinning
    // --files (rather than omitting it) avoids the selector's own git-status fallback,
    // which would otherwise make this depend on the caller's dirty working tree.
    const result = (await tool.execute(
      { task: "xyzzy plugh", files: "no-such-extension.zzz" },
      {} as never,
    )) as Record<string, unknown>;

    expect(result.error).toBeUndefined();
    expect(result.status).toBe("needs-triage");
  });

  it("agents_select surfaces a real dispatch failure as a structured error, not a throw", async () => {
    const tools = await registerTools(REPO_ROOT);
    const tool = findTool(tools, "agents_select");

    // select_agents.py's own argument validation rejects --base combined
    // with --files; this exercises the CLI path's non-zero-exit error
    // mapping end to end, and incidentally covers the --base flag, which
    // no other test passes.
    const result = (await tool.execute(
      { task: "test", files: "README.md", base: "main" },
      {} as never,
    )) as Record<string, unknown>;

    expect(typeof result.error).toBe("string");
    expect(result.error).toMatch(/--base cannot be combined with --files/);
  });

  it("agents_select propagates context.signal so an aborted call cancels the underlying process", async () => {
    const tools = await registerTools(REPO_ROOT);
    const tool = findTool(tools, "agents_select");

    const controller = new AbortController();
    controller.abort();

    // Node's execFile refuses to spawn at all for an already-aborted
    // signal, rejecting synchronously with an AbortError -- the fastest,
    // most deterministic way to prove context.signal actually reaches
    // execFileAsync (as opposed to being silently dropped, which would
    // instead run the real CLI to completion and return a normal plan).
    const result = (await tool.execute(
      { task: "test", files: "README.md" },
      { signal: controller.signal } as AgentToolContext,
    )) as Record<string, unknown>;

    expect(result.status).toBeUndefined();
    expect(typeof result.error).toBe("string");
    expect(result.error).toMatch(/abort/i);
  });

  it("agents_select returns a structured error when the workspace root could not be resolved", async () => {
    const tools = await registerTools(undefined);
    const tool = findTool(tools, "agents_select");

    const result = (await tool.execute({ task: "anything" }, {} as never)) as Record<
      string,
      unknown
    >;

    expect(typeof result.error).toBe("string");
    expect(result.error).toMatch(/workspace root/i);
    // Structural consistency with the CLI-failure catch path below, which
    // always sets both `error` and `stderr` -- a consumer iterating over
    // error response fields should never have to guess whether `stderr` is
    // present depending on which error source produced the result. Must be
    // "" not undefined: sanitizeToolResult's JSON round-trip silently drops
    // undefined-valued keys, so only a real (if empty) string actually
    // survives to the caller.
    expect(result.stderr).toBe("");
  });

  it("agents_select result is fully re-serializable (sanitizeToolResult guards against cycles)", async () => {
    const tools = await registerTools(REPO_ROOT);
    const tool = findTool(tools, "agents_select");

    const result = (await tool.execute(
      { task: "Review README changes", files: "README.md", classification: "internal" },
      {} as never,
    )) as Record<string, unknown>;

    // The sanitizeToolResult wrapper must produce a result that can be
    // round-tripped through JSON.stringify without throwing — this is the
    // contract that protects against downstream SDK hooks injecting circular
    // references (e.g., Error objects with e.error === e).
    expect(() => JSON.stringify(result)).not.toThrow();

    // And the round-trip must preserve the data.
    const reparsed = JSON.parse(JSON.stringify(result));
    expect(reparsed).toEqual(result);
  });

  describe("agents_select against a workspace root that is not this checkout", () => {
    // Regression test for the bug found investigating a Cline report of
    // non-deterministic "JSON.stringify cannot serialize cyclic structures"
    // errors: the plugin used to resolve the cadre binary as a bare
    // "./bin/cadre" spawned with `cwd: rootPath`, which only ever worked
    // when rootPath happened to be this repository itself. Any other
    // project (e.g. deagy/agentic-sdlc, which has no bin/cadre of its own)
    // deterministically failed with `spawn ./bin/cadre ENOENT`. This fixture
    // is a throwaway git repo standing in for "some other project" — it has
    // no bin/cadre, matching that failure mode exactly.
    let otherWorkspace: string;

    beforeAll(async () => {
      otherWorkspace = mkdtempSync(path.join(tmpdir(), "cadre-cline-plugin-test-"));
      await execFileAsync("git", ["init", "-q"], { cwd: otherWorkspace });
    });

    afterAll(() => {
      rmSync(otherWorkspace, { recursive: true, force: true });
    });

    it("resolves the cadre binary relative to this plugin, not the target workspace", async () => {
      // Before the fix this was `{ error: "spawn ./bin/cadre ENOENT", ... }`.
      const tools = await registerTools(otherWorkspace);
      const tool = findTool(tools, "agents_select");

      const result = (await tool.execute(
        { task: "xyzzy plugh", files: "no-such-extension.zzz" },
        {} as never,
      )) as Record<string, unknown>;

      expect(result.error).toBeUndefined();
      expect(result.status).toBe("needs-triage");
      expect((result.inputs as Record<string, unknown>).repository_root).toBe(otherWorkspace);
    });
  });

  describe("agents_select default invocation (no files, no base)", () => {
    // Regression test for the bug where the default no-args invocation shape
    // (the single most common way this tool is actually called) failed to
    // mirror the CLI's git-status fallback for discovering changed files.
    let dirtyWorkspace: string;

    beforeAll(async () => {
      dirtyWorkspace = mkdtempSync(path.join(tmpdir(), "cadre-cline-plugin-git-status-test-"));
      await execFileAsync("git", ["init", "-q"], { cwd: dirtyWorkspace });
      writeFileSync(path.join(dirtyWorkspace, "dirty.txt"), "uncommitted\n");
    });

    afterAll(() => {
      rmSync(dirtyWorkspace, { recursive: true, force: true });
    });

    it("discovers the dirty working tree via git-status, not an empty file list", async () => {
      const tools = await registerTools(dirtyWorkspace);
      const tool = findTool(tools, "agents_select");

      const result = (await tool.execute({ task: "xyzzy plugh" }, {} as never)) as Record<
        string,
        unknown
      >;

      expect(result.error).toBeUndefined();
      const inputs = result.inputs as Record<string, unknown>;
      expect(inputs.changed_file_source).toBe("git-status");
      expect(inputs.changed_files).toContain("dirty.txt");
    });
  });

  describe("agents_select requireSdlc forwarding", () => {
    // build_dispatch_plan.py branches on --require-sdlc:
    // require_lifecycle_contract() (hard failure if the Agentic SDLC kernel
    // isn't resolvable) vs. try_lifecycle_contract() (silent standalone-mode
    // degrade). This environment happens to have the kernel installed
    // (`agentic-sdlc` on PATH), so requireSdlc:true and its absence are
    // indistinguishable unless the kernel is made unresolvable for the
    // duration of the test — mirroring agentic_sdlc_contracts.py's own
    // resolution order (AGENTIC_SDLC_BIN, then `agentic-sdlc` on PATH).
    const originalPath = process.env.PATH;
    const originalAgenticSdlcBin = process.env.AGENTIC_SDLC_BIN;

    beforeAll(() => {
      process.env.PATH = "/usr/bin:/bin";
      delete process.env.AGENTIC_SDLC_BIN;
    });

    afterAll(() => {
      process.env.PATH = originalPath;
      if (originalAgenticSdlcBin === undefined) {
        delete process.env.AGENTIC_SDLC_BIN;
      } else {
        process.env.AGENTIC_SDLC_BIN = originalAgenticSdlcBin;
      }
    });

    it("forwards --require-sdlc and hard-fails when Agentic SDLC is unavailable", async () => {
      const tools = await registerTools(REPO_ROOT);
      const tool = findTool(tools, "agents_select");

      const result = (await tool.execute(
        { task: "test", files: "README.md", requireSdlc: true },
        {} as never,
      )) as Record<string, unknown>;

      expect(typeof result.error).toBe("string");
      // The version comes from provider.json's kernel_compatibility, not a
      // literal. It used to be hardcoded as "v0.3.x" -- which was actually
      // provider.json's own `version` field, not the kernel range it meant,
      // and was ten minor versions behind the kernel by the time anyone
      // noticed. Match the shape, and separately assert it is not the
      // "a compatible version" fallback, which is what this returns when it
      // cannot find the manifest at all.
      expect(result.error).toMatch(/Agentic SDLC v\d+\.\d+\.\d+ or newer/);
      expect(result.error).not.toMatch(/a compatible version/);
    });

    it("omits --require-sdlc by default and degrades to standalone mode instead of failing", async () => {
      const tools = await registerTools(REPO_ROOT);
      const tool = findTool(tools, "agents_select");

      const result = (await tool.execute(
        { task: "test", files: "README.md" },
        {} as never,
      )) as Record<string, unknown>;

      expect(result.error).toBeUndefined();
      expect((result.lifecycle_tracking as Record<string, unknown>).status).toBe("standalone");
    });
  });

  describe("agents_select with base alone (no files)", () => {
    // Regression coverage for the <base>...HEAD git-diff discovery path,
    // which was previously only ever exercised together with `files` (in
    // the mutual-exclusion failure test above). This fixture is a real git
    // repo with two commits so `base` resolves to a real ancestor.
    let baseWorkspace: string;

    beforeAll(async () => {
      baseWorkspace = mkdtempSync(path.join(tmpdir(), "cadre-cline-plugin-base-test-"));
      await execFileAsync("git", ["init", "-q"], { cwd: baseWorkspace });
      await execFileAsync("git", ["config", "user.email", "test@example.com"], {
        cwd: baseWorkspace,
      });
      await execFileAsync("git", ["config", "user.name", "Test"], { cwd: baseWorkspace });
      writeFileSync(path.join(baseWorkspace, "README.md"), "initial\n");
      await execFileAsync("git", ["add", "README.md"], { cwd: baseWorkspace });
      await execFileAsync("git", ["commit", "-q", "-m", "initial"], { cwd: baseWorkspace });
      await execFileAsync("git", ["branch", "base-point"], { cwd: baseWorkspace });
      writeFileSync(path.join(baseWorkspace, "README.md"), "changed\n");
      await execFileAsync("git", ["commit", "-q", "-am", "change README"], { cwd: baseWorkspace });
    });

    afterAll(() => {
      rmSync(baseWorkspace, { recursive: true, force: true });
    });

    it("discovers changed files via <base>...HEAD, not git-status", async () => {
      const tools = await registerTools(baseWorkspace);
      const tool = findTool(tools, "agents_select");

      const result = (await tool.execute(
        { task: "Review README changes", base: "base-point" },
        {} as never,
      )) as Record<string, unknown>;

      expect(result.error).toBeUndefined();
      const inputs = result.inputs as Record<string, unknown>;
      expect(inputs.changed_file_source).toBe("git-diff:base-point...HEAD");
      expect(inputs.changed_files).toContain("README.md");
    });
  });

  it("agents_select rejects an invalid classification for a task that actually routes", async () => {
    const tools = await registerTools(REPO_ROOT);
    const tool = findTool(tools, "agents_select");

    // "Review README changes" + files: "README.md" is the same task/files
    // pair used by several tests above and matches real routes (see
    // matched_routes assertions elsewhere in this file), so classification
    // validation actually runs (build_dispatch_plan.py's
    // _build_knowledge_context only validates classification when at least
    // one agent was selected; an unrouted needs-triage task never reaches
    // this check, per PR #9).
    const result = (await tool.execute(
      {
        task: "Review README changes",
        files: "README.md",
        classification: "not-a-real-classification",
      },
      {} as never,
    )) as Record<string, unknown>;

    expect(typeof result.error).toBe("string");
    expect(result.error).toMatch(/Invalid classification/);
  });

  it("agents_select result is a plain object with no hidden properties", async () => {
    const tools = await registerTools(REPO_ROOT);
    const tool = findTool(tools, "agents_select");

    const result = (await tool.execute(
      { task: "Review README changes", files: "README.md", classification: "internal" },
      {} as never,
    )) as Record<string, unknown>;

    // sanitizeToolResult re-parses through JSON, which strips functions,
    // symbols, and undefined values. The result must be a plain object.
    expect(Object.getPrototypeOf(result)).toBe(Object.prototype);
  });
});
