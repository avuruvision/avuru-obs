import { test, expect } from "@playwright/test";

// The compose stack seeds a down service (seed-checkout) and an alerting rule
// (deploy/compose/alerts.json) that fires on it, so the board shows a firing
// alert and the configured rule.

test.describe("alerts board (seeded data)", () => {
  test("renders firing alerts and the configured rule", async ({ page }) => {
    await page.goto("/alerts");

    // The Firing card is present; the seeded rule fires on seed-checkout.
    await expect(page.getByRole("heading", { name: "Firing" })).toBeVisible();

    // The evaluator ticks on its OWN clock (evalIntervalSec: 2 in
    // deploy/compose/alerts.json), and the fixtures land moments before this
    // suite starts — so on a cold run the first tick can fall after the page
    // did. Retry with a reload rather than assume the board was already right:
    // asserting once here is how this spec fails on a fresh stack and passes on
    // a warm one, which is the worst kind of gate.
    await expect(async () => {
      await page.reload();
      await expect(page.getByText("service:seed-checkout").first()).toBeVisible({
        timeout: 2_000,
      });
    }).toPass({ timeout: 30_000 });

    // The read-only rule list shows the configured rule (exact — the name also
    // appears in the firing row as "· rule checkout-down").
    await expect(page.getByRole("heading", { name: "Configured rules" })).toBeVisible();
    await expect(page.getByText("checkout-down", { exact: true })).toBeVisible();
  });

  test("gates the board when the module is off", async ({ page }) => {
    await page.route("**/api/v1/capabilities", (route) =>
      route.fulfill({ json: { version: "test", modules: ["core"] } }),
    );

    await page.goto("/traces");
    const nav = page.getByRole("navigation", { name: "Primary" });
    await expect(nav.getByRole("link", { name: "Alerts" })).toHaveCount(0);

    await page.goto("/alerts");
    await expect(page.getByRole("heading", { name: /Alerting module is off/ })).toBeVisible();
    await expect(page.getByText("modules.alerting.enabled=true")).toBeVisible();
  });
});
