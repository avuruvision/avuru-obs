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
  use: {
    baseURL: process.env.AVURUOPS_BASE_URL ?? "http://localhost:3001",
    trace: "retain-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
