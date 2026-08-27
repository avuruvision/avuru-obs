import { test, expect } from "@playwright/test";

// Seeded fixtures (deploy/compose/seed/fixtures): seed-checkout's own trace and
// a seed-gateway → seed-payments trace. Assertions stay relative — which values
// appear, how the scopes differ, where a click lands — because the seed's exact
// span counts change whenever a fixture is added.
const ENTRY_SERVICE = "seed-payments";
const ROOT_SERVICE = "seed-gateway";

const breakdown = (params = "") => `/traces?tab=breakdown${params}`;

test.describe("trace breakdown", () => {
  test("draws the part-of-whole views and the numbers behind them", async ({ page }) => {
    await page.goto(breakdown());

    // Both charts, and the legend that keeps identity off colour alone.
    await expect(page.getByRole("img", { name: "Traffic breakdown treemap" })).toBeVisible();
    await expect(page.getByRole("img", { name: "Traffic breakdown donut" })).toBeVisible();

    // The table is not decoration: three of the light-mode palette steps sit
    // below 3:1 contrast, and it is the relief that makes them legal.
    const table = page.getByRole("table");
    await expect(table).toBeVisible();
    await expect(table.getByRole("row", { name: new RegExp(ENTRY_SERVICE) })).toBeVisible();
  });

  test("the scope selector changes which spans are counted", async ({ page }) => {
    // Requests served: every service that answered something, seed-payments
    // included — it is a callee, never an entry point.
    await page.goto(breakdown());
    const table = page.getByRole("table");
    await expect(table.getByRole("row", { name: new RegExp(ENTRY_SERVICE) })).toBeVisible();

    // Trace entry points: only where traffic entered. The callee must drop out,
    // which is the whole reason the two scopes are separate questions.
    await page.goto(breakdown("&scope=root"));
    await expect(table.getByRole("row", { name: new RegExp(ROOT_SERVICE) })).toBeVisible();
    await expect(table.getByRole("row", { name: new RegExp(ENTRY_SERVICE) })).toHaveCount(0);
  });

  test("groups by outcome using the product's three states", async ({ page }) => {
    await page.goto(breakdown("&groupBy=status"));
    const table = page.getByRole("table");
    // The seed carries successful traffic and one errored root span; "refused"
    // may legitimately be absent, so it is not asserted.
    await expect(table.getByRole("row", { name: /^ok/ })).toBeVisible();
  });

  test("a slice drills into exactly the traffic it represents", async ({ page }) => {
    await page.goto(breakdown());
    await page
      .getByRole("table")
      .getByRole("row", { name: new RegExp(ENTRY_SERVICE) })
      .click();

    await expect(page).toHaveURL(new RegExp(`service=${ENTRY_SERVICE}`));
    await expect(page).toHaveURL(/tab=traces/);
  });

  test("controls live in the URL, so a breakdown can be shared", async ({ page }) => {
    await page.goto(breakdown("&groupBy=attribute%3Ahttp.route&weight=time"));
    // The chart reopens on the shared dimension rather than the default.
    await expect(page.getByLabel("Group by")).toContainText("HTTP route");
    await expect(page.getByLabel("Weight")).toContainText("By total time");
  });
});
