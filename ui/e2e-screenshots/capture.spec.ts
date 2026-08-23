import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { test, expect, type Page } from "@playwright/test";

// Captures the README/launch screenshots from the release sandbox
// (tools/screenshots/compose.screenshots.yaml). Unlike the e2e suite this
// DOES rely on HotROD traffic — the service map and trace waterfall should
// look like a real system, not two seed services. Seeded fixtures still back
// /errors, /health, /alerts and /green so those shots are deterministic.
const OUT_DIR = join(__dirname, "..", "..", "docs", "images");
const HOTROD_URL = process.env.HOTROD_URL ?? "http://localhost:18088";
const ADMIN_EMAIL = "admin";
const ADMIN_PASSWORD = process.env.AVURUOBS_AUTH_ADMIN_PASSWORD ?? "shots-admin-pw";

// HotROD's four demo customers; rotating them fans traffic across drivers.
const CUSTOMERS = [123, 392, 731, 567];

async function signIn(page: Page): Promise<void> {
  await page.goto("/login");
  await page.getByLabel("Email").fill(ADMIN_EMAIL);
  await page.getByLabel("Password").fill(ADMIN_PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page).not.toHaveURL(/\/login/);
}

async function shoot(page: Page, name: string): Promise<void> {
  // Small settle for chart animations before the viewport-sized capture.
  await page.waitForTimeout(1_500);
  await page.screenshot({ path: join(OUT_DIR, `${name}.png`) });
}

test("capture launch screenshots", async ({ page }) => {
  mkdirSync(OUT_DIR, { recursive: true });

  // 1. Warm HotROD: ~30 dispatches spread over ~90s so the map, RED charts
  //    and error tracking have realistic shape (HotROD simulates Redis
  //    timeouts, which surface as real errors).
  for (let i = 0; i < 30; i++) {
    const customer = CUSTOMERS[i % CUSTOMERS.length];
    await page.request
      .get(`${HOTROD_URL}/dispatch?customer=${customer}&nonse=${i}`)
      .catch(() => {});
    await page.waitForTimeout(3_000);
  }

  await signIn(page);

  // 2. Service map — the hero shot.
  await page.goto("/service-map");
  await expect(page.getByText(/click a service for its traces/)).toBeVisible();
  // Give the force layout a moment to relax before freezing the frame.
  await page.waitForTimeout(4_000);
  await shoot(page, "service-map");

  // 3. Trace waterfall — open the DEEPEST trace in the window (most spans →
  //    the richest timeline). Selecting by trace id is deterministic; clicking
  //    a list row is not: the Overview tab and the Traces list both render
  //    role="row", and a click on an Overview row only sets an operation filter
  //    and switches tabs — it never opens a trace, so the waterfall stays shut
  //    (the earlier `getByText("Spans")` assertion passed on the list's column
  //    header, masking the miss). page.request shares the signed-in cookie.
  const from = new Date(Date.now() - 60 * 60 * 1000).toISOString();
  const to = new Date().toISOString();
  const res = await page.request.get(`/api/v1/traces?start=${from}&end=${to}&limit=100`);
  const traces = (await res.json()).traces as Array<{ traceId: string; spanCount: number }>;
  const deepest = [...traces].sort((a, b) => b.spanCount - a.spanCount)[0];
  await page.goto(`/traces?trace=${deepest.traceId}`);
  // The detail panel (default "Timeline" view = the Waterfall) is open once its
  // close button renders.
  await expect(page.getByRole("button", { name: "Close trace" })).toBeVisible();
  await page.waitForTimeout(1_000);
  await shoot(page, "trace-waterfall");

  // 4. Error tracking — seeded NullPointerException issue plus HotROD's
  //    Redis timeouts.
  await page.goto("/errors");
  await expect(page.getByRole("tab", { name: "Unresolved", exact: true })).toBeVisible();
  await expect(page.getByText("java.lang.NullPointerException").first()).toBeVisible();
  await shoot(page, "error-tracking");

  // 5. Service health — tier lanes from the mounted groups.json.
  await page.goto("/health");
  await expect(page.getByText(/Overall:/)).toBeVisible();
  await expect(page.getByRole("heading", { name: "Tier 0" })).toBeVisible();
  await shoot(page, "service-health");

  // 6. Alerting — the seeded checkout-down rule fires immediately.
  await page.goto("/alerts");
  await expect(page.getByRole("heading", { name: "Firing" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Configured rules" })).toBeVisible();
  await shoot(page, "alerting");

  // 7. Green Obs dashboard — MUST be range=24h: the seeded Kepler counter
  //    pair sits 60s apart and only co-buckets at wide ranges (see
  //    ui/e2e/green.spec.ts header). At 15m the page shows the empty state.
  await page.goto("/green?range=24h");
  const energyTile = page.getByText("Total energy", { exact: true }).locator("..");
  await expect(energyTile).toContainText("Wh");
  await expect(page.getByRole("heading", { name: "Carbon budgets" })).toBeVisible();
  await shoot(page, "green-dashboard");
});
