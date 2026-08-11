import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    // See test-setup.mts: redirects Cline's user-global hook search paths into
    // a sandbox before `@cline/*` is imported, so the suite never executes a
    // hook script installed on the developer's or runner's own machine.
    setupFiles: ["./test-setup.mts"],
  },
});
