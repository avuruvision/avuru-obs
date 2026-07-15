import { test, expect } from "@playwright/test";

// Settings tabs (Coroot-inspired): General (project), Collection (agents),
// Status (instance). The compose stack has no k8s sensor, so Collection
// shows the empty inventory — the structure is what's under test.
test.describe("settings", () => {
  test("general tab shows the project and the config-defined banner", async ({ page }) => {
    await page.goto("/settings");
    await expect(
      page.getByText(/Projects are defined through deployment configuration/),
    ).toBeVisible();
    // Active project chip (the switcher in the sidebar also says "default").
    await expect(page.getByText("built-in")).toBeVisible();
    await expect(page.getByText("Retention")).toBeVisible();
  });

  test("tabs are shareable via ?tab=", async ({ page }) => {
    await page.goto("/settings");
    await page.getByRole("tab", { name: "Collection" }).click();
    await expect(page).toHaveURL(/tab=collection/);
    await expect(page.getByText("Deactivating collection")).toBeVisible();
    await expect(page.getByText("sensor.collection.excludeNamespaces")).toBeVisible();

    await page.goto("/settings?tab=status");
    await expect(page.getByText(/Instance-wide \(all projects\)/)).toBeVisible();
    await expect(page.getByText("Components")).toBeVisible();
  });

  test("collection tab reports sensor inventory (empty in compose)", async ({ page }) => {
    await page.goto("/settings?tab=collection");
    await expect(page.getByText(/nodes? reporting/)).toBeVisible();
    await expect(page.getByText("No sensors reporting")).toBeVisible();
  });
});
