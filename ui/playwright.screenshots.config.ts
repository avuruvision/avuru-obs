import { defineConfig, devices } from "@playwright/test";

// Screenshot capture against the release sandbox started via
// tools/screenshots/compose.screenshots.yaml (UI published on :13001).
// Not part of the e2e suite: `npx playwright test -c playwright.screenshots.config.ts`.
export default defineConfig({
  testDir: "./e2e-screenshots",
  fullyParallel: false,
  retries: 0,
  reporter: "list",
  timeout: 600_000,
  use: {
    baseURL: process.env.AVURUOPS_BASE_URL ?? "http://localhost:13001",
    // 2x density so README images stay crisp on hi-DPI displays.
    viewport: { width: 1600, height: 1000 },
    deviceScaleFactor: 2,
    trace: "off",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"], viewport: { width: 1600, height: 1000 }, deviceScaleFactor: 2 } }],
});
