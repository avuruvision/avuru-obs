import { test, expect } from "@playwright/test";

// Per-project isolation in the UI. Seeded fixtures: `seed-checkout` in the
// default project, `seed-checkout-staging` under the staging tenant
// (avuru.tenant resource attribute). NOTE: the default service name is a
// PREFIX of the staging one — absence checks for it need the lookahead.
const STAGING_SVC = "seed-checkout-staging";
const DEFAULT_SVC_ONLY = /seed-checkout(?!-staging)/;

test.describe("project switcher (seeded data)", () => {
  test("default project shows only default-tenant data", async ({ page }) => {
    await page.goto("/traces");
    await expect(page.getByText(DEFAULT_SVC_ONLY).first()).toBeVisible();
    await expect(page.getByText(STAGING_SVC)).toHaveCount(0);
    // Switcher renders in the sidebar footer showing the active project.
    await expect(page.getByRole("button", { name: "Switch project" })).toContainText("default");
  });

  test("switching to staging swaps data and marks the URL", async ({ page }) => {
    await page.goto("/traces");
    await expect(page.getByText(DEFAULT_SVC_ONLY).first()).toBeVisible();

    await page.getByRole("button", { name: "Switch project" }).click();
    await page.getByRole("option", { name: "staging" }).click();

    await expect(page).toHaveURL(/project=staging/);
    await expect(page.getByText(STAGING_SVC).first()).toBeVisible();
    // Cross-project leakage check: no default-only service text anywhere.
    await expect(page.getByText(DEFAULT_SVC_ONLY)).toHaveCount(0);
  });

  test("?project= is shareable and survives navigation + reload", async ({ page }) => {
    await page.goto("/traces?project=staging&tab=traces");
    await expect(page.getByText(STAGING_SVC).first()).toBeVisible();

    // In-app navigation keeps the project (localStorage bridge, then the
    // provider re-materializes the param for shareability).
    await page.getByRole("link", { name: "Logs", exact: true }).click();
    await expect(page).toHaveURL(/project=staging/);

    await page.reload();
    await expect(page).toHaveURL(/project=staging/);
    await expect(page.getByRole("button", { name: "Switch project" })).toContainText("staging");
  });
});
