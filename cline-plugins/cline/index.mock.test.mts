import { describe, expect, it, vi } from "vitest";
import { promisify } from "node:util";

// This file exists solely to force agents_select's catch path into shapes a
// real `cadre select` subprocess failure can never produce today (a
// circular-reference error, an oversized stderr blob) -- index.test.mts's
// "surfaces a real dispatch failure as a structured error" test only
// exercises execFile's ordinary error shape (plain-string `.message`/
// `.stderr`), which would pass identically whether or not the catch path
// were routed through sanitizeToolResult/truncateStr at all. Same rationale
// and mocking approach as cline-lifecycle/index.exitcode.test.mts.
//
// index.ts calls `promisify(execFile)`, and Node's real `child_process`
// module tags its `execFile` export with `util.promisify.custom` so that
// promisified call resolves to `{stdout, stderr}` on success and rejects
// with an error object carrying `.stdout`/`.stderr` on a nonzero exit --
// generic util.promisify (single-callback-arg convention) does not produce
// that shape on its own. The mock below replicates that custom binding
// directly rather than relying on generic promisify semantics.

const execFileMock = vi.hoisted(() => vi.fn());

vi.mock("node:child_process", () => {
  const execFile = (
    file: string,
    args: string[],
    options: unknown,
    callback: (error: unknown, stdout: string, stderr: string) => void,
  ) => execFileMock(file, args, options, callback);
  Object.defineProperty(execFile, promisify.custom, {
    value: (file: string, args: string[], options: unknown) =>
      new Promise((resolve, reject) => {
        execFileMock(
          file,
          args,
          options,
          (
            error: (Error & { stdout?: string; stderr?: string }) | null,
            stdout: string,
            stderr: string,
          ) => {
            if (error) {
              error.stdout = stdout;
              error.stderr = stderr;
              reject(error);
            } else {
              resolve({ stdout, stderr });
            }
          },
        );
      }),
  });
  return { execFile };
});

const { plugin } = await import("./index.ts");

async function registerTool() {
  const tools: Array<{ execute: (input: unknown, context: unknown) => Promise<unknown> }> = [];
  const api = {
    registerTool: (tool: (typeof tools)[number]) => tools.push(tool),
    registerCommand: () => {},
    registerRule: () => {},
    registerMessageBuilder: () => {},
    registerProvider: () => {},
    registerAutomationEventType: () => {},
    registerMcpServer: () => {},
  };
  await plugin.setup?.(api as never, {
    workspaceInfo: { rootPath: "/fake/workspace" },
  } as never);
  return tools[0];
}

describe("agents_select execFile options (mocked child_process)", () => {
  // Node validates `options.signal` itself: anything that is not a real
  // AbortSignal makes execFile throw ERR_INVALID_ARG_TYPE before the child
  // is spawned. The host that injects `AgentToolContext` is not obliged to
  // hand over a real one -- cline 3.0.55 (@cline/sdk 0.0.75) passed a plain
  // object, and agents_select failed on every call with "The
  // \"options.signal\" property must be an instance of AbortSignal". The
  // tool must forward only a usable signal and otherwise omit the option.
  it("omits options.signal when the host context's signal is not an AbortSignal", async () => {
    let seenOptions: Record<string, unknown> | undefined;
    execFileMock.mockImplementation((_file, _args, options, callback) => {
      seenOptions = options as Record<string, unknown>;
      callback(null, JSON.stringify({ status: "ready" }), "");
    });

    const tool = await registerTool();
    const result = (await tool.execute({ task: "test" }, { signal: { fake: true } })) as Record<
      string,
      unknown
    >;

    expect(seenOptions).toBeDefined();
    expect("signal" in (seenOptions as Record<string, unknown>)).toBe(false);
    expect(result.error).toBeUndefined();
    expect(result.status).toBe("ready");
  });

  it("forwards a real AbortSignal unchanged", async () => {
    let seenOptions: Record<string, unknown> | undefined;
    execFileMock.mockImplementation((_file, _args, options, callback) => {
      seenOptions = options as Record<string, unknown>;
      callback(null, JSON.stringify({ status: "ready" }), "");
    });

    const controller = new AbortController();
    const tool = await registerTool();
    await tool.execute({ task: "test" }, { signal: controller.signal });

    expect((seenOptions as Record<string, unknown>).signal).toBe(controller.signal);
  });
});

describe("agents_select catch path (mocked child_process)", () => {
  it("sanitizes a circular-reference error into a well-formed, JSON-serializable result instead of throwing", async () => {
    // Mirrors how a real Error's `cause` chain (or a library that attaches
    // the triggering object back onto itself) can self-reference -- exactly
    // the shape plain JSON.stringify chokes on ("Converting circular
    // structure to JSON") without sanitizeToolResult's safeJsonStringify.
    execFileMock.mockImplementation((_file, _args, _options, callback) => {
      const error = new Error("boom") as Error & { stderr?: string; self?: unknown };
      error.stderr = "some cli stderr";
      error.self = error;
      callback(error, "", "some cli stderr");
    });

    const tool = await registerTool();
    let result: unknown;
    await expect(
      (async () => {
        result = await tool.execute({ task: "test" }, {});
      })(),
    ).resolves.not.toThrow();

    expect(() => JSON.stringify(result)).not.toThrow();
    const reparsed = JSON.parse(JSON.stringify(result));
    expect(reparsed).toEqual(result);
    expect(typeof (result as Record<string, unknown>).error).toBe("string");
  });

  it("truncates an oversized stderr/message instead of passing it through unbounded", async () => {
    const hugeStderr = "x".repeat(10_000);
    execFileMock.mockImplementation((_file, _args, _options, callback) => {
      const error = new Error("boom") as Error & { stderr?: string };
      error.stderr = hugeStderr;
      callback(error, "", hugeStderr);
    });

    const tool = await registerTool();
    const result = (await tool.execute({ task: "test" }, {})) as Record<string, unknown>;

    // Pins the current bounded-text assumption (see MAX_ERROR_TEXT_LENGTH's
    // comment in index.ts): a future change to the spawned binary's error
    // output that starts emitting something unexpectedly large is caught
    // here rather than silently flowing through verbatim.
    expect(typeof result.stderr).toBe("string");
    expect((result.stderr as string).length).toBeLessThan(hugeStderr.length);
    expect((result.error as string).length).toBeLessThan(hugeStderr.length);
  });

  it("does not throw when err.stderr/err.message violate their assumed string type at runtime", async () => {
    // `caught as {...}` in the catch block is a compile-time assertion only
    // -- nothing guarantees execFile's documented contract actually holds
    // for whatever gets thrown. A circular, non-string `.stderr` is the
    // sharpest version of that: naive `.trim()`/truncateStr calls on it
    // throw a TypeError (uncaught, since this is before sanitizeToolResult's
    // own try/catch), defeating agents_select's "never throw" guarantee
    // regardless of how well-sanitized the eventual *returned* object is.
    execFileMock.mockImplementation((_file, _args, _options, callback) => {
      const circularStderr: Record<string, unknown> = { note: "not a string" };
      circularStderr.self = circularStderr;
      const error = new Error("boom") as Error & { stderr?: unknown };
      error.stderr = circularStderr;
      callback(error, "", circularStderr);
    });

    const tool = await registerTool();
    let result: unknown;
    await expect(
      (async () => {
        result = await tool.execute({ task: "test" }, {});
      })(),
    ).resolves.not.toThrow();

    const typed = result as Record<string, unknown>;
    expect(typeof typed.stderr).toBe("string");
    expect(typeof typed.error).toBe("string");
    expect(() => JSON.stringify(result)).not.toThrow();
  });
});
