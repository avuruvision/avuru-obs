import { test, expect } from "@playwright/test";

// Cross-zone traffic accounting is opt-in at the sensor and needs a
// multi-zone cluster, so the compose stack can never produce a row. Both
// states are therefore stubbed: the seeded run proves the absent case is the
// real default, and the stub proves the card reads correctly when zones do
// report.
const ZONES = "**/api/v1/network/zones*";

test.describe("cross-zone traffic", () => {
  test("no crossings means no card — not an empty table", async ({ page }) => {
    await page.goto("/dashboard");
    // The capacity band is present (the green seed provides a node), which is
    // what makes this assertion meaningful: the band rendered and still chose
    // not to draw a zone card.
    await expect(page.getByTestId("dashboard-capacity")).toBeVisible();
    await expect(page.getByTestId("zone-crossings")).toHaveCount(0);
  });

  test("zone pairs render heaviest first, with the window total", async ({ page }) => {
    await page.route(ZONES, (route) =>
      route.fulfill({
        json: {
          zones: [
            { srcZone: "eu-west-1a", dstZone: "eu-west-1b", bytes: 42949672960 },
            { srcZone: "eu-west-1b", dstZone: "eu-west-1a", bytes: 1073741824 },
          ],
        },
      }),
    );
    await page.goto("/dashboard");

    const card = page.getByTestId("zone-crossings");
    await expect(card).toBeVisible();
    await expect(card.getByText("Cross-zone traffic")).toBeVisible();
    // 40 GiB + 1 GiB over the window.
    await expect(card.getByText(/41 GB over the window/)).toBeVisible();

    // Direction is not symmetric: a -> b and b -> a are separate rows, as they
    // are on a cloud bill, and the heavier crossing leads.
    await expect(card).toHaveText(
      /eu-west-1a\s*→?\s*eu-west-1b\s*40 GB[\s\S]*eu-west-1b\s*→?\s*eu-west-1a\s*1\.0 GB/,
    );
  });

  test("a reported pair carrying no bytes yet does not draw a card", async ({ page }) => {
    await page.route(ZONES, (route) =>
      route.fulfill({ json: { zones: [{ srcZone: "a", dstZone: "b", bytes: 0 }] } }),
    );
    await page.goto("/dashboard");
    await expect(page.getByTestId("dashboard-capacity")).toBeVisible();
    await expect(page.getByTestId("zone-crossings")).toHaveCount(0);
  });
});
