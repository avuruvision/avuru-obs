import { test, expect } from "@playwright/test";

// Seeded fixture: one deterministic trace from seed-checkout whose root span
// errors — the RED dashboard must show a card for it.
const SEED_SERVICE = "seed-checkout";

test.describe("RED metrics dashboard (seeded data)", () => {
  test("shows a card for the seeded service with the three charts", async ({ page }) => {
    await page.goto("/metrics");

    const card = page.getByRole("heading", { name: SEED_SERVICE });
    await expect(card).toBeVisible();
    for (const chart of ["Rate", "Errors", "Duration"]) {
      await expect(page.getByText(chart, { exact: true }).first()).toBeVisible();
    }

    // Card links back to the service's traces.
    await page.getByRole("link", { name: "traces →" }).first().click();
    await expect(page).toHaveURL(new RegExp(`service=${SEED_SERVICE}`));
  });
});
