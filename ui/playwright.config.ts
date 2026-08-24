import { defineConfig, devices } from "@playwright/test";

// E2E smoke against the compose stack (lifecycle owned by `make e2e-ui`).
// Assertions rely ONLY on seeded deterministic data (tools/seed), never on
// HotROD's load-dependent traces.
export default defineConfig({
  testDir: "./e2e",
  fullyParallel: false,
  retries: 0,
  reporter: process.env.CI ? "line" : "list",
  timeout: 30_000,
  // One login for the whole run, reused as every spec's starting state. The
  // suite is written against an admin view of a REAL auth-enabled stack; before
  // this it needed the stack started with anonymous access on, which is why
  // auth.spec.ts could not run beside the rest and the suite could not be a CI
  // gate. See e2e/global-setup.ts.
  globalSetup: "./e2e/global-setup.ts",
  use: {
    baseURL: process.env.AVURUOBS_BASE_URL ?? "http://localhost:3001",
    storageState: "e2e/.auth/state.json",
    trace: "retain-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
