import { test, expect } from "@playwright/test";

// The compose stack seeds a down service (seed-checkout) and an alerting rule
// (deploy/compose/alerts.json) that fires on it, so the board shows a firing
// alert and the configured rule.

test.describe("alerts board (seeded data)", () => {
  test("renders firing alerts and the configured rule", async ({ page }) => {
    await page.goto("/alerts");

    // The Firing card is present; the seeded rule fires on seed-checkout.
    await expect(page.getByRole("heading", { name: "Firing" })).toBeVisible();
    await expect(page.getByText("service:seed-checkout").first()).toBeVisible();

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
