import { afterAll, describe, expect, it } from "vitest";
import { existsSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, sep } from "node:path";

import {
  createHookConfigFileExtension,
  createHookConfigFileHooks,
  HOOK_CONFIG_FILE_EVENT_MAP,
  HookConfigFileName,
  listHookConfigFiles,
  // Deliberately `@cline/core`'s re-export, not `@cline/shared/storage`'s. Both
  // resolve here, but to DIFFERENT copies (see the version note below), and
  // only this one is the resolver the dispatcher under test actually consults.
  resolveHooksConfigSearchPaths,
  toHookConfigFileName,
} from "@cline/core";
import { HookEventNameSchema } from "@cline/shared";

// Drift guard over the Cline runtime hook surface that a "route every request
// in a Cadre-configured project through the orchestrator" design depends on.
//
// Why this file exists: this repository twice described `setup(api, ctx)`'s
// surface -- accurately -- in places where it read as a statement about the
// plugin contract as a whole (`cline-plugins/cline/index.ts`'s `agents_select`
// tool description, and the "## Cline" section of
// `.agents/skills/run-agent-orchestration/references/runner-adapters.md`). Both
// have since been corrected, because the contract has a second surface:
// `ContributionRegistryExtension` carries a top-level `hooks` field beside
// `setup`, and separately Cline ships a config-file subprocess hook system with
// `UserPromptSubmit` and `PreToolUse` events. Prose cannot hold that
// correction across an `@cline/*` version bump; this file can.
//
// Every assertion below was observed by executing the real dispatcher rather
// than read off a `.d.ts`, against BOTH `@cline/core` 0.0.65 and 0.0.71 --
// identical behavior on both, 2026-08-11. A failure here means the runtime
// contract genuinely moved, not that a type was renamed.
//
// What `npm test` actually resolves is MIXED, and the distinction matters when
// a run disagrees with the paragraph above. The dispatcher under test comes
// from `@cline/core` 0.0.65, hoisted, which this workspace pins in
// `devDependencies`. The schema and path helpers (`HookEventNameSchema`,
// `resolveHooksConfigSearchPaths`) come from `@cline/shared`, which all three
// workspaces pin at 0.0.71 and npm installs NESTED, above the 0.0.65 copy
// `@cline/sdk` hoists. So a green run here is core 0.0.65 + shared 0.0.71, not
// a single-version check of either. The 0.0.71 dispatcher result was obtained
// out-of-band against CLI 3.0.51's own install and is NOT reproduced by
// `npm test`; re-verify it there, by hand, after an `@cline/*` bump.
//
// THE ASYMMETRY THIS FILE PINS, because it decides what is buildable on Cline:
// a `PreToolUse` hook's stdout is consumed and can stop a tool call; a
// `UserPromptSubmit` hook's stdout is DISCARDED. Both hooks run. Only one is
// listened to. The last test below proves this against the real dispatcher
// rather than asserting it from prose, because the difference is invisible at
// the type level (`createHookConfigFileHooks` returns `{onEvent, beforeTool}`
// in both cases) and invisible in a hook script's own logs (a
// `UserPromptSubmit` hook that emits `contextModification` looks like it
// worked -- it ran, it exited 0, and nothing reports that its output went
// nowhere).
//
// Mechanism, for whoever has to re-verify this after a version bump: the two
// dispatch paths in `@cline/core` differ deliberately. `tool_call` goes through
// a helper that spawns NON-detached with a timeout, awaits it, parses stdout as
// JSON, and returns a merged `HookControl`. Every other event -- including
// `prompt_submit` -- goes through a second helper that spawns `detached: true`,
// attaches only a `.catch()` for logging, and returns nothing.
//
// DELIBERATELY NOT ASSERTED -- read this before treating a green run as
// "auto-routing is fully supported on Cline":
//
//   * Anything about `HubRuntimeHost`. `cline-agents/index.ts` documents that a
//     hub host silently drops `localRuntime.hooks` at a `JSON.stringify`
//     boundary, which is why it force-pins `backendMode: "local"`. Whether an
//     *extension-level* `hooks` field survives the same boundary is untested
//     here -- it needs a running hub, and getting it wrong fails silently, so
//     do not assume the local-runtime result generalizes.
//   * Any richer prompt-stage injection reached some other way. `HookControl`
//     (`@cline/shared`) carries `context`, `systemPrompt`, `appendMessages`,
//     and `replaceMessages`, and `@cline/core` does normalize a hook's
//     `contextModification` into `HookControl.context` -- that machinery is
//     real and is what `tool_call` uses. This file asserts only that the
//     config-file `prompt_submit` path does not reach it. A future Cline
//     release could wire it up; the last test is what would notice.

const HOOKS_DIR_SEGMENTS = [".cline", "hooks"] as const;

// One test below EXECUTES its `#!/bin/sh` fixtures through the real runner and
// depends on the executable bit; neither survives Windows. The resulting
// failure would be indistinguishable from a genuine contract regression, which
// is why it skips explicitly rather than being left to fail. Every other test
// here only writes or lists fixture files and runs anywhere. CI is
// ubuntu-latest, so this costs no coverage there.
const itOnPosix = process.platform === "win32" ? it.skip : it;

/** Temp workspaces created by this file, removed in `afterAll`. */
const tempWorkspaces: string[] = [];

function makeTempWorkspace(prefix: string): string {
  const workspace = mkdtempSync(join(tmpdir(), prefix));
  tempWorkspaces.push(workspace);
  return workspace;
}

afterAll(() => {
  // Each workspace holds executable scripts under a 0700 mkdtemp directory;
  // leaving them behind litters /tmp with one set per run.
  for (const workspace of tempWorkspaces) {
    rmSync(workspace, { recursive: true, force: true });
  }
});

/** A temp workspace containing `.cline/hooks/<name>` for each name given. */
function workspaceWithHookFiles(names: readonly string[]): string {
  const workspace = makeTempWorkspace("cadre-cline-hook-surface-");
  const hooksDir = join(workspace, ...HOOKS_DIR_SEGMENTS);
  mkdirSync(hooksDir, { recursive: true });
  for (const name of names) {
    writeFileSync(join(hooksDir, name), "#!/bin/sh\nexit 0\n", { mode: 0o755 });
  }
  return workspace;
}

describe("Cline config-file hook events", () => {
  it("exposes the two events auto-routing needs, bound to real runtime events", () => {
    // Layer 1 (see the design note in this file's header): a per-prompt entry
    // point. Layer 2: a mutation gate.
    expect(HookConfigFileName.UserPromptSubmit).toBe("UserPromptSubmit");
    expect(HookConfigFileName.PreToolUse).toBe("PreToolUse");

    const promptEvent = HOOK_CONFIG_FILE_EVENT_MAP[HookConfigFileName.UserPromptSubmit];
    const toolEvent = HOOK_CONFIG_FILE_EVENT_MAP[HookConfigFileName.PreToolUse];
    expect(promptEvent).toBe("prompt_submit");
    expect(toolEvent).toBe("tool_call");

    // Both must be members of the runtime event enum, not orphaned strings --
    // a rename on either side of the map is the realistic drift.
    expect(HookEventNameSchema.options).toContain(promptEvent);
    expect(HookEventNameSchema.options).toContain(toolEvent);
  });

  it("resolves hook config file names case-insensitively, over an extension allowlist", () => {
    // This is what lets a hook be committed as an executable `UserPromptSubmit.py`
    // rather than an extensionless file -- relevant because a Cadre hook would
    // be Python, matching `plugin/hooks/guard_workspace_mutation.py`.
    expect(toHookConfigFileName("UserPromptSubmit")).toBe(HookConfigFileName.UserPromptSubmit);
    expect(toHookConfigFileName("userpromptsubmit")).toBe(HookConfigFileName.UserPromptSubmit);
    expect(toHookConfigFileName("UserPromptSubmit.sh")).toBe(HookConfigFileName.UserPromptSubmit);
    expect(toHookConfigFileName("PreToolUse.py")).toBe(HookConfigFileName.PreToolUse);
    expect(toHookConfigFileName("PreToolUse.ps1")).toBe(HookConfigFileName.PreToolUse);

    // The extension set is an ALLOWLIST, not "anything after the dot" -- an
    // otherwise correctly named file with an unlisted extension is silently
    // not a hook, which is a plausible way to ship a hook that never runs.
    expect(toHookConfigFileName("UserPromptSubmit.rb")).toBeUndefined();
    expect(toHookConfigFileName("UserPromptSubmit.txt")).toBeUndefined();
    expect(toHookConfigFileName("NotAHook")).toBeUndefined();
  });
});

describe("Cline project-local hook discovery", () => {
  it("searches workspace-local directories, not just user-global ones", () => {
    // The whole "if cadre is configured for THIS project" premise rests on a
    // per-workspace surface existing. Assert containment rather than exact
    // array equality: the user-global entries ahead of these are real but
    // machine-dependent (`~/Documents/Cline/Hooks`, `$CLINE_DIR/hooks`).
    const workspace = join(sep, "tmp", "some-workspace");
    const paths = resolveHooksConfigSearchPaths(workspace);

    expect(paths).toContain(join(workspace, ".cline", "hooks"));
    expect(paths).toContain(join(workspace, ".clinerules", "hooks"));

    // ...and that passing no workspace yields strictly fewer paths, i.e. the
    // workspace-local entries are genuinely workspace-derived.
    expect(resolveHooksConfigSearchPaths().length).toBeLessThan(paths.length);
  });

  it("discovers a workspace-local UserPromptSubmit hook and binds it to prompt_submit", () => {
    const workspace = workspaceWithHookFiles([
      "UserPromptSubmit",
      "PreToolUse.py",
      "NotAHook",
    ]);

    // Filter to this temp workspace: `listHookConfigFiles` also scans the
    // user-global search paths, and a developer or CI runner with real hooks
    // installed there would otherwise change the result.
    const found = listHookConfigFiles(workspace).filter((entry) =>
      entry.path.startsWith(workspace + sep),
    );

    expect(found).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          fileName: HookConfigFileName.UserPromptSubmit,
          hookEventName: "prompt_submit",
          path: join(workspace, ...HOOKS_DIR_SEGMENTS, "UserPromptSubmit"),
        }),
        expect.objectContaining({
          fileName: HookConfigFileName.PreToolUse,
          hookEventName: "tool_call",
          path: join(workspace, ...HOOKS_DIR_SEGMENTS, "PreToolUse.py"),
        }),
      ]),
    );

    // A file whose name maps to no event is ignored rather than surfaced with
    // an undefined event -- otherwise an unrelated script dropped in the hooks
    // directory would register as a hook.
    expect(found.map((entry) => entry.path)).not.toContain(
      join(workspace, ...HOOKS_DIR_SEGMENTS, "NotAHook"),
    );
  });
});

describe("extension-level hook composition", () => {
  it("composes config-file hooks into an extension that declares the `hooks` capability", () => {
    // The correction this file exists for. Cline's own first-party hook-file
    // bridge is delivered as an extension carrying a TOP-LEVEL `hooks` field
    // and `manifest.capabilities: ["hooks"]` -- the same contract
    // (`ContributionRegistryExtension.hooks`, typed
    // `AgentExtensionHooks = Partial<AgentRuntimeHooks>`) that this repo's own
    // `cline` plugin object could declare beside its `setup`. If core ships its
    // own gate this way, the shape is available to a plugin too.
    const workspace = workspaceWithHookFiles(["UserPromptSubmit", "PreToolUse"]);

    const extension = createHookConfigFileExtension({ cwd: workspace, workspacePath: workspace });

    expect(extension).toBeDefined();
    expect(extension?.manifest.capabilities).toContain("hooks");
    expect(extension?.hooks).toBeDefined();

    // `beforeTool` is the mutation gate (Layer 2). Its presence here is the
    // single fact that moves Cline off "advisory-only" -- see this file's
    // header for what is NOT thereby proven about `prompt_submit`.
    expect(Object.keys(extension?.hooks ?? {})).toContain("beforeTool");
    expect(typeof extension?.hooks?.beforeTool).toBe("function");
  });

  itOnPosix("consumes a PreToolUse hook's output but discards a UserPromptSubmit hook's", async () => {
    // The single most decision-relevant fact about auto-routing on Cline, and
    // the one that cannot be read off a type. Both hooks below emit valid
    // control JSON on stdout; only one of them is listened to.
    const workspace = makeTempWorkspace("cadre-cline-hook-dispatch-");
    const hooksDir = join(workspace, ...HOOKS_DIR_SEGMENTS);
    mkdirSync(hooksDir, { recursive: true });

    // This test EXECUTES every hook the runner discovers, and the runner also
    // searches two user-global locations. `test-setup.mts` redirects those into
    // a sandbox; re-assert it here, because if that isolation ever silently
    // stops working the failure mode is not a red test -- it is this suite
    // quietly running a developer's own hook scripts and folding their output
    // into the assertions below.
    // Assert the sandbox positively, not just the absence of stray hooks: on a
    // machine that happens to have no global hooks installed, an absence check
    // passes whether or not the redirection works, and would keep passing right
    // up until it ran on a machine that does.
    const globalSearchPaths = resolveHooksConfigSearchPaths(workspace).filter(
      (path) => !path.startsWith(workspace + sep),
    );
    expect(globalSearchPaths.length).toBeGreaterThan(0);
    for (const path of globalSearchPaths) {
      expect(
        path.startsWith(tmpdir() + sep),
        `user-global hook search path ${path} is outside ${tmpdir()} -- test-setup.mts's ` +
          "HOME/CLINE_DIR sandboxing is not in effect, and this test would execute hook " +
          "scripts installed on this machine",
      ).toBe(true);
    }

    // ...and belt-and-braces, the discovered set really is workspace-only.
    const strayHooks = listHookConfigFiles(workspace).filter(
      (entry) => !entry.path.startsWith(workspace + sep),
    );
    expect(strayHooks.map((entry) => entry.path)).toEqual([]);

    // Proves the prompt hook RAN, separating "its output was discarded" from
    // "it was never dispatched" -- opposite conclusions for anyone designing
    // around this.
    const sentinel = join(workspace, "user-prompt-submit-executed");

    // `cat > /dev/null` drains the JSON payload on stdin; a hook that exits
    // without reading it can take EPIPE instead of running to completion.
    writeFileSync(
      join(hooksDir, "PreToolUse"),
      `#!/bin/sh\ncat > /dev/null\nprintf '%s' '{"cancel":true}'\n`,
      { mode: 0o755 },
    );
    writeFileSync(
      join(hooksDir, "UserPromptSubmit"),
      `#!/bin/sh\ncat > /dev/null\ntouch ${JSON.stringify(sentinel)}\nprintf '%s' '{"contextModification":"INJECTED"}'\n`,
      { mode: 0o755 },
    );

    const hooks = createHookConfigFileHooks({ cwd: workspace, workspacePath: workspace });
    expect(hooks).toBeDefined();

    const snapshot = {
      agentId: "agent-1",
      conversationId: "conv-1",
      runId: "run-1",
      parentAgentId: null,
      iteration: 1,
    };

    // Layer 2 works: `cancel: true` on stdout comes back as a runtime stop.
    const beforeToolResult = await hooks?.beforeTool?.({
      snapshot,
      tool: { name: "Write" },
      toolCall: { toolCallId: "call-1", toolName: "Write", input: { path: "x" } },
      input: { path: "x" },
    } as never);
    expect(beforeToolResult).toEqual({ stop: true });

    // Layer 1 does not: `onEvent` resolves to nothing regardless of what the
    // prompt hook printed. `message.content` must be an array of parts -- a
    // bare string throws inside the dispatcher.
    const onEventResult = await hooks?.onEvent?.({
      type: "message-added",
      snapshot,
      message: { role: "user", content: [{ type: "text", text: "refactor the auth module" }] },
    } as never);
    expect(onEventResult).toBeUndefined();

    // ...and yet the prompt hook did run. Detached, so poll rather than assume
    // it completed before `onEvent` resolved -- that ordering is exactly what
    // "fire and forget" gives up.
    for (let attempt = 0; attempt < 100 && !existsSync(sentinel); attempt++) {
      await new Promise((resolve) => setTimeout(resolve, 100));
    }
    expect(existsSync(sentinel)).toBe(true);
  }, 30_000);

  it("contributes nothing when a workspace has no hook files", () => {
    // Opt-in, and free when unconfigured: a project that has not asked for
    // routing pays no hook dispatch at all. This is the property that lets a
    // Cadre hook be shipped without imposing itself on every Cline workspace.
    const workspace = makeTempWorkspace("cadre-cline-hook-surface-bare-");

    expect(
      createHookConfigFileExtension({ cwd: workspace, workspacePath: workspace }),
    ).toBeUndefined();
  });
});
