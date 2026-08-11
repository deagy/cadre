// Redirect Cline's USER-GLOBAL config lookups into a throwaway sandbox before
// any `@cline/*` module is evaluated.
//
// Why this exists: `resolveHooksConfigSearchPaths(ws)` returns FOUR paths, and
// only the last two are workspace-local:
//
//   ~/Documents/Cline/Hooks      <- user-global
//   $CLINE_DIR/hooks             <- user-global
//   <ws>/.clinerules/hooks
//   <ws>/.cline/hooks
//
// `hook-surface.test.mts` dispatches through the real hook runner, so without
// this file a developer or runner with a real `PreToolUse`/`UserPromptSubmit`
// hook installed globally would have their own script EXECUTED by the test
// suite, and would see its output folded into assertions about a temp
// workspace. CI passes today only because its runners have no global hooks.
//
// Why a setup file rather than a `beforeAll`: the `~/Documents/Cline` path is
// derived from a home directory captured once, at module-init time, so `HOME`
// has to be redirected before `@cline/shared` is first imported -- a
// `beforeAll` runs far too late. (`$CLINE_DIR` is re-read on every call and
// would tolerate a late assignment; both are set here so the two travel
// together and neither can be half-applied.)
//
// Why environment variables rather than the exported `setHomeDir`/`setClineDir`:
// those mutate module-level state, and this workspace resolves TWO copies of
// `@cline/shared` -- 0.0.71 nested here, and the 0.0.65 copy that `@cline/core`
// (the dispatcher actually under test) loads from the hoisted root. Calling the
// setter would move only the copy this test imported and leave the dispatcher
// pointed at the real home, i.e. it would look like isolation while providing
// none. `process.env` is process-global and reaches both copies.
//
// `hook-surface.test.mts` re-asserts the resulting isolation at dispatch time,
// so if this ever silently stops working the suite fails rather than quietly
// going back to reading the developer's home directory.
import { tmpdir } from "node:os";
import { join } from "node:path";

// Deliberately not `mkdtempSync`: nothing needs to exist on disk (the resolver
// only lists directories that happen to exist), and a per-run temp directory
// would leak one empty directory per vitest process. The pid keeps concurrent
// runs, and other users of a shared /tmp, from colliding.
const sandboxHome = join(tmpdir(), `cadre-cline-test-home-${process.pid}`);

process.env.HOME = sandboxHome;
process.env.USERPROFILE = sandboxHome;
process.env.CLINE_DIR = join(sandboxHome, ".cline");
