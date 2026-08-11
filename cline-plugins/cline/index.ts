import { execFile } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { z } from "zod";
import { type AgentPlugin, type AgentToolContext, createTool } from "@cline/sdk";
import { safeJsonStringify, truncateStr } from "@cline/shared";

const execFileAsync = promisify(execFile);

// Bounds the catch path's error/stderr text (see below). sanitizeToolResult
// only guarantees JSON-serialization safety, not content redaction -- it
// would happily pass through an arbitrarily large or path-laden blob
// verbatim. The bounded, developer-authored text every currently-known
// failure mode produces (spawn ENOENT, select_agents.py's own argument
// validation) is nowhere close to this limit; it exists to cap worst-case
// exposure/size if that assumption ever stops holding (e.g. a future
// shell wrapper or an uncaught Python traceback with interpolated absolute
// paths), not to redact anything today.
const MAX_ERROR_TEXT_LENGTH = 2000;

// Resolved from this module's own location, not the target workspace: the
// `cadre` CLI lives at <this repository's root>/bin/cadre regardless of which
// project's rootPath the tool is invoked against. Resolving it relative to
// rootPath instead (as a bare "./bin/cadre" with `cwd: rootPath`) only works
// when rootPath happens to be this repository itself, and fails closed with
// ENOENT in every other consumer project.
//
// Two levels up: this plugin sits at cline-plugins/cline/, so the repository's
// own bin/cadre dispatcher is two directories above it. It was one level while
// these plugins lived inside plugin/ (whose root is the packaged Cadre plugin,
// making plugin/bin/cadre a sibling), and two levels before that. They moved
// back out because plugin/package.json made npm a dependency of installing the
// *Claude Code* plugin, which shipped 263 MB of this workspace's node_modules
// to every Claude Code user -- see docs/proposals/cline-npm-packaging.md.
const PLUGIN_DIR = path.dirname(fileURLToPath(import.meta.url));
const CADRE_BIN = path.resolve(PLUGIN_DIR, "..", "..", "bin", "cadre");

// ---------------------------------------------------------------------------
// Schema
// ---------------------------------------------------------------------------

const AgentsSelectInputSchema = z.object({
  task: z
    .string()
    .describe("Task objective used for deterministic routing (required)."),
  files: z
    .string()
    .optional()
    .describe("Changed path, or comma-separated paths, to scope the plan to."),
  base: z
    .string()
    .optional()
    .describe("Git base ref used with <base>...HEAD for committed changes."),
  taskId: z
    .string()
    .optional()
    .describe("Stable caller-supplied task identifier. Omit to let the selector derive one."),
  classification: z
    .string()
    .optional()
    .describe("Authorized knowledge classification for this task, if known."),
  requireSdlc: z
    .boolean()
    .optional()
    .describe("Fail instead of degrading to standalone mode if Agentic SDLC isn't available."),
});

type AgentsSelectInput = z.infer<typeof AgentsSelectInputSchema>;

interface AgentsSelectError {
  error: string;
  stderr?: string;
}

// ---------------------------------------------------------------------------
// CLI argument builder
// ---------------------------------------------------------------------------

function buildSelectArgs(input: AgentsSelectInput, rootPath: string): string[] {
  const args = ["select", "--root", rootPath, "--task", input.task];
  if (input.files) args.push("--files", input.files);
  if (input.base) args.push("--base", input.base);
  if (input.taskId) args.push("--task-id", input.taskId);
  if (input.classification) args.push("--classification", input.classification);
  if (input.requireSdlc) args.push("--require-sdlc");
  return args;
}

// ---------------------------------------------------------------------------
// Sanitization
// ---------------------------------------------------------------------------

/**
 * Sanitize a tool result to ensure it is fully JSON-serializable without
 * circular references, hidden properties, or non-JSON values (functions,
 * symbols, undefined). Uses the SDK's safeJsonStringify which detects and
 * replaces cycles with "[Circular]" rather than throwing.
 */
function sanitizeToolResult(input: unknown): Record<string, unknown> | AgentsSelectError {
  try {
    return JSON.parse(safeJsonStringify(input)) as Record<string, unknown>;
  } catch {
    return { error: "agents_select failed: result could not be serialized" };
  }
}

// ---------------------------------------------------------------------------
// Setup and tool registration
// ---------------------------------------------------------------------------

type SetupFn = NonNullable<AgentPlugin["setup"]>;
export type SetupApi = Parameters<SetupFn>[0];
export type SetupContext = Parameters<SetupFn>[1];
export type { AgentsSelectInput, AgentsSelectError };

const setup = (api: SetupApi, ctx: SetupContext) => {
  const rootPath = ctx.workspaceInfo?.rootPath;

  // Real, plugin-controlled system-prompt injection: `registerRule` content is
  // appended to the host's `config.systemPrompt` (if any) when the session
  // composes its final system prompt -- confirmed by reading
  // `@cline/shared`'s `AgentExtensionApi.registerRule` type declaration
  // ("Register prompt rules included in the runtime system prompt. Requires
  // the `rules` capability.") and `@cline/core`'s compiled
  // `SessionRuntime.composeSystemPrompt()`, which joins `this.config
  // .systemPrompt.trim()` with every registered rule's trimmed content. This
  // is distinct from (and additive to) a host application's own
  // `systemPrompt` config field on `ClineCore.create()`/`cline.start()` --
  // this plugin does not need the host to set anything for this sentence to
  // reach the model. Requires the "rules" capability, declared below and in
  // package.json's `cline.plugins[0].capabilities`.
  api.registerRule({
    id: "cadre-system-prompt",
    content:
      "You are a coding assistant with access to Cadre role subagents. " +
      "Call the `agents_select` tool to get a deterministic, reviewable dispatch plan from the Cadre " +
      "role catalog before choosing which specialist role(s) a task needs. It returns a plan only -- " +
      "it never invokes, dispatches, retrieves knowledge for, merges, deploys, or mutates anything " +
      "itself.",
    source: "cadre",
  });

  api.registerTool(
    createTool({
      name: "agents_select",
      description:
        "Get a deterministic, reviewable agent dispatch plan from this repository's Cadre catalog " +
        "(routes, primary/reviewer/support roles, quality gates), via this repository's `bin/cadre " +
        "select` CLI, the single authoritative dispatch implementation. Plan only: never invokes " +
        "agents, retrieves knowledge, merges, deploys, or mutates infrastructure or approvals. The " +
        "role catalog is bundled with this plugin itself, not read from the target workspace, so " +
        "`root` may be any project — it does not need to be a checkout of deagy/cadre or contain its " +
        "own catalog.yaml. This plugin does not dispatch the selected role(s) itself — a Cline " +
        "plugin's setup(api, ctx) registers contributions (registerTool/registerCommand/etc.) and " +
        "does not expose the session's spawn-agent or team primitives. After calling this tool, " +
        "the orchestrating Cline session must dispatch " +
        "manually: see the \"## Cline\" section of " +
        ".agents/skills/run-agent-orchestration/references/runner-adapters.md for the current " +
        "manual-injection workaround and /team limitations.",
      // Converted to plain JSON Schema with this plugin's own zod, rather than
      // handed to createTool() as a raw ZodObject. createTool() only converts
      // a Zod schema on its own via `instanceof ZodType`, and that check runs
      // against the *host's* bundled zod, not this plugin's. A Cline plugin
      // runs in a separate installation from its host, so even a
      // version-matching zod is a different module instance there — the
      // instanceof check silently fails, the conversion is skipped, and the
      // raw ZodObject (which has circular internal refs) is passed straight
      // through as if it were already JSON Schema, breaking serialization of
      // the tool declaration itself for every call. Converting here removes
      // the dependency on that cross-realm check entirely.
      inputSchema: z.toJSONSchema(AgentsSelectInputSchema),
      execute: async (
        rawInput: unknown,
        context: AgentToolContext,
      ): Promise<Record<string, unknown> | AgentsSelectError> => {
        const input = AgentsSelectInputSchema.parse(rawInput);
        if (!rootPath) {
          return sanitizeToolResult({
            error:
              "Could not resolve the workspace root from the host session; agents_select requires a known " +
              "workspace root and will not fall back to the process's current directory.",
            // "" not undefined: sanitizeToolResult JSON-round-trips every
            // result, and JSON.stringify silently drops undefined-valued
            // keys -- an undefined stderr here would vanish just like an
            // omitted one, achieving no actual consistency with the
            // CLI-failure catch path below, which always carries a real
            // (possibly empty) stderr string.
            stderr: "",
          }) as AgentsSelectError;
        }

        try {
          const { stdout } = await execFileAsync(
            CADRE_BIN,
            buildSelectArgs(input, rootPath),
            { cwd: rootPath, signal: context.signal },
          );
          return sanitizeToolResult(JSON.parse(stdout));
        } catch (caught) {
          const err = caught as {
            message?: string;
            stderr?: string;
            stdout?: string;
            detail?: string;
          };
          // `caught as {...}` is a compile-time assertion only -- nothing
          // guarantees `err.stderr`/`err.message` are actually strings at
          // runtime (execFile's documented contract can be violated by an
          // unexpected thrown shape). Normalize with a real `typeof` check
          // before calling `.trim()`/truncateStr on either: both throw
          // (uncaught, since we're not inside sanitizeToolResult's own
          // try/catch yet) when handed a non-string, which would defeat the
          // very "never throw" guarantee this catch block exists to provide.
          const stderr = typeof err.stderr === "string" ? truncateStr(err.stderr, MAX_ERROR_TEXT_LENGTH) : "";
          const message = typeof err.message === "string" ? truncateStr(err.message, MAX_ERROR_TEXT_LENGTH) : "";

          return sanitizeToolResult({
            error: [stderr.trim(), message].filter(Boolean).join("\n") || "agents_select failed",
            stderr,
          }) as AgentsSelectError;
        }
      },
    }),
  );
};

const plugin: AgentPlugin = {
  name: "cadre",
  manifest: { capabilities: ["tools", "rules"] },
  setup,
};

export { plugin };
export default plugin;
