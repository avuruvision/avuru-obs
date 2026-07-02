import { test, expect } from "@playwright/test";

// Seeded fixture (deploy/compose/seed/fixtures): one deterministic trace whose
// root span errors — the inventory must list the service with an error badge.
const SEED_SERVICE = "seed-checkout";

test.describe("services inventory (seeded data)", () => {
  test("lists the seeded service and drills into its traces", async ({ page }) => {
    await page.goto("/services");

    const row = page.getByRole("row").filter({ hasText: SEED_SERVICE });
    await expect(row).toBeVisible();
    await expect(row.locator("text=%")).toBeVisible();

    await row.click();
    await expect(page).toHaveURL(new RegExp(`service=${SEED_SERVICE}`));
  });

  test("sorts by a column and toggles direction", async ({ page }) => {
    await page.goto("/services");

    const p99 = page.getByRole("button", { name: "p99" });
    await p99.click();
    await expect(page.getByRole("columnheader", { name: /p99/ })).toHaveAttribute(
      "aria-sort",
      "descending",
    );
    await p99.click();
    await expect(page.getByRole("columnheader", { name: /p99/ })).toHaveAttribute(
      "aria-sort",
      "ascending",
    );
  });
});
