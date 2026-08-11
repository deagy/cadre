#!/usr/bin/env node
// Run the Cline guard's TypeScript implementation against a prepared plan of
// (command, cwd) cases and report each decision as "blocked" or "allowed".
//
// This is the TypeScript half of the shared behavioural fixture that pins
// `.claude/hooks/guard_workspace_mutation.py` and
// `cline-plugins/cline-agents/index.ts` to the same OUTCOMES rather than the
// same structure (deagy/cadre#222). `plugin/tools/test_guard_parity.py`
// prepares one disposable repository per fixture case, evaluates every case
// with the Python guard, then spawns this script ONCE with a plan file and
// compares the two sets of decisions against each other and against the
// fixture's declared expectation.
//
// It cannot simply `import` the plugin: `index.ts` pulls in the Cline agent
// SDK and `yaml`/`zod`, none of which need to be installed to check guard
// behaviour. Instead it slices out the region between the
// `cadre:guard-region:begin`/`:end` markers -- which is written to be
// self-contained for exactly this purpose -- prepends a prelude supplying the
// handful of things that region expects from its enclosing module, and
// imports the result.
//
// Type annotations are removed by whichever of three mechanisms this machine
// actually has, tried in order: node's own built-in stripping (unflagged
// since v22.18, but absent from builds compiled without it -- including the
// node that verified this file), then `esbuild`, then `typescript`, both
// resolved from `cline-plugins/node_modules` where the Cline plugin's own
// toolchain already installs them. If none is available the runner exits
// with EXIT_UNSUPPORTED and the Python side SKIPS rather than failing, so a
// CI job without node or without those dev dependencies stays green instead
// of turning an unavailable tool into a false red.
//
//   node plugin/tools/guard_parity_runner.mjs <plan.json>
//
// The plan is `{"cases": [{"id": ..., "command": ..., "cwd": ...}, ...]}` and
// the output on stdout is `{"results": {"<id>": {"decision": "...",
// "reason": "..."}}}`. Any failure to build or import the region is reported
// as a non-zero exit with a message on stderr, never as a silent pass.

import { createRequire } from "node:module";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve as resolvePath } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

// Distinct from 1 (a real failure) and 2 (bad usage): "this machine cannot
// run the TypeScript side at all", which is a skip, not a result.
const EXIT_UNSUPPORTED = 3;

const HERE = dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = resolvePath(HERE, "..", "..");
const GUARD_SOURCE = join(REPO_ROOT, "cline-plugins", "cline-agents", "index.ts");
const BEGIN_MARKER = "// cadre:guard-region:begin";
const END_MARKER = "// cadre:guard-region:end";

// What the extracted region expects from the module it normally lives in.
// Deliberately tiny: if this prelude has to grow, the region has stopped
// being self-contained and the marker comments say to fix that instead.
const PRELUDE = `import { execFile } from "node:child_process";
import { isAbsolute, join, resolve } from "node:path";
import { promisify } from "node:util";

const execFileAsync = promisify(execFile);

interface GitGuardDecision {
  reason: string;
}
`;

const EXPORTS = `
export { evaluateGitCommand, GIT_GUARD_HANDLERS, WRAPPER_TOKENS, GIT_GLOBAL_FLAGS_WITH_VALUE };
`;

export function extractGuardRegion(source) {
  const begin = source.indexOf(BEGIN_MARKER);
  const end = source.indexOf(END_MARKER);
  if (begin === -1 || end === -1 || end < begin) {
    throw new Error(
      `guard region markers not found in ${GUARD_SOURCE} ` +
        `(expected ${BEGIN_MARKER} ... ${END_MARKER}); the parity runner cannot ` +
        "check anything without them",
    );
  }
  return source.slice(begin + BEGIN_MARKER.length, end);
}

/**
 * Transpile TypeScript source to ESM JavaScript, or return null when this
 * machine has no way to do it. Tries node's built-in stripping first (by
 * writing a `.mts` file and letting the loader handle it), then `esbuild`,
 * then `typescript`, both resolved from the Cline plugin's own
 * `node_modules`.
 */
async function transpile(source, dir) {
  const mtsPath = join(dir, "guard-region.mts");
  await writeFile(mtsPath, source, "utf8");
  try {
    await import(pathToFileURL(mtsPath).href);
    return mtsPath; // node stripped the types itself
  } catch (err) {
    const code = err && err.code;
    if (code !== "ERR_UNKNOWN_FILE_EXTENSION" && code !== "ERR_NO_TYPESCRIPT") throw err;
  }

  const require = createRequire(join(REPO_ROOT, "cline-plugins", "package.json"));
  const jsPath = join(dir, "guard-region.mjs");

  try {
    const { transformSync } = require("esbuild");
    const { code } = transformSync(source, { loader: "ts", format: "esm", target: "node20" });
    await writeFile(jsPath, code, "utf8");
    return jsPath;
  } catch (err) {
    if (err && err.code !== "MODULE_NOT_FOUND") throw err;
  }

  try {
    const ts = require("typescript");
    const { outputText } = ts.transpileModule(source, {
      compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 },
    });
    await writeFile(jsPath, outputText, "utf8");
    return jsPath;
  } catch (err) {
    if (err && err.code !== "MODULE_NOT_FOUND") throw err;
  }

  return null;
}

async function loadGuard() {
  const source = await readFile(GUARD_SOURCE, "utf8");
  const region = PRELUDE + extractGuardRegion(source) + EXPORTS;
  const dir = await mkdtemp(join(tmpdir(), "cadre-guard-parity-"));
  try {
    const modulePath = await transpile(region, dir);
    if (modulePath === null) return { module: null, dir };
    return { module: await import(pathToFileURL(modulePath).href), dir };
  } catch (err) {
    await rm(dir, { recursive: true, force: true });
    throw err;
  }
}

async function main() {
  const planPath = process.argv[2];
  if (!planPath) {
    process.stderr.write("usage: guard_parity_runner.mjs <plan.json>\n");
    process.exit(2);
  }
  const plan = JSON.parse(await readFile(planPath, "utf8"));
  const { module, dir } = await loadGuard();
  if (module === null) {
    await rm(dir, { recursive: true, force: true });
    process.stderr.write(
      "guard_parity_runner: no TypeScript transform available (node built without " +
        "type stripping, and neither esbuild nor typescript resolvable from " +
        "cline-plugins/node_modules) -- skipping the TypeScript half\n",
    );
    process.exit(EXIT_UNSUPPORTED);
  }
  const results = {};
  try {
    for (const testCase of plan.cases) {
      const decision = await module.evaluateGitCommand(testCase.command, testCase.cwd);
      results[testCase.id] = decision
        ? { decision: "blocked", reason: decision.reason }
        : { decision: "allowed", reason: "" };
    }
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
  process.stdout.write(JSON.stringify({ results }, null, 2));
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch((err) => {
    process.stderr.write(`guard_parity_runner: ${err && err.stack ? err.stack : err}\n`);
    process.exit(1);
  });
}
