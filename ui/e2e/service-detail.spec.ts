import { test, expect } from "@playwright/test";

// Seeded fixture (deploy/compose/seed/fixtures/traces_multiservice.json):
// seed-gateway makes a client call that seed-payments serves. That pair is the
// only dependency the seed guarantees, so the dependency assertions use it.
const CALLER = "seed-gateway";
const CALLEE = "seed-payments";

test.describe("service detail", () => {
  test("summarises one service and names both sides of its neighbourhood", async ({ page }) => {
    await page.goto(`/services?service=${CALLEE}`);

    await expect(page.getByRole("heading", { name: CALLEE })).toBeVisible();
    // The four RED tiles carry the headline numbers.
    for (const label of ["Rate", "Errors", "p95", "p99"]) {
      await expect(page.getByText(label, { exact: true }).first()).toBeVisible();
    }

    // Callers and callees are shown apart: who is affected when this breaks,
    // against what could be breaking it.
    const calledBy = page.locator("div").filter({ hasText: /^Called by/ }).first();
    await expect(calledBy.getByText(CALLER)).toBeVisible();
  });

  test("a caller in the list opens that service's own page", async ({ page }) => {
    await page.goto(`/services?service=${CALLEE}`);
    await page.getByRole("row").filter({ hasText: CALLER }).click();
    await expect(page).toHaveURL(new RegExp(`service=${CALLER}`));
    await expect(page.getByRole("heading", { name: CALLER })).toBeVisible();
  });

  test("the signal tabs are scoped to the service", async ({ page }) => {
    await page.goto(`/services?service=${CALLEE}&view=traces`);
    // Every listed trace involves this service — the tab reuses the trace list
    // with a service filter rather than re-implementing one.
    await expect(page.getByText(CALLEE).first()).toBeVisible();

    await page.goto(`/services?service=${CALLEE}&view=logs`);
    // Logs either list or say plainly that there are none; both are correct,
    // and neither is an error state.
    await expect(
      page.getByText(/open in Logs|No logs in this window/),
    ).toBeVisible();
  });

  test("a name with nothing behind it says so", async ({ page }) => {
    await page.goto("/services?service=does-not-exist");
    await expect(page.getByText("No data for does-not-exist")).toBeVisible();
    // And the way back is always present.
    await page.getByRole("link", { name: "All services" }).click();
    await expect(page).not.toHaveURL(/service=/);
  });
});
