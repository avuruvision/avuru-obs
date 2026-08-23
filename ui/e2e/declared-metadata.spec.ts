import { test, expect } from "@playwright/test";

// Declared service metadata (deploy/compose/seed/fixtures/traces_declared.json):
// two services declare the same domain `seed-financial` in different
// environments and at different tiers, and a third declares a tier the hub
// cannot use.
//
// The point of the feature is that none of this required hub config: the
// services said what they are, and the board believed them.

test.describe("declared service metadata", () => {
  test("one declared domain becomes one group per environment", async ({ page }) => {
    await page.goto("/health");

    // Same name, two groups — the environment is what tells them apart, so it
    // has to be on the card.
    const prod = page.locator("[data-testid='group-card']", { hasText: "seed-financial" }).filter({
      hasText: "prod",
    });
    const staging = page
      .locator("[data-testid='group-card']", { hasText: "seed-financial" })
      .filter({ hasText: "staging" });
    await expect(prod).toHaveCount(1);
    await expect(staging).toHaveCount(1);

    // And the tier each declared put it in a different lane. That is the
    // capability config could not express: one domain, two criticalities.
    await expect(page.getByTestId("tier-lane-T0")).toContainText("seed-financial");
    await expect(page.getByTestId("tier-lane-T2")).toContainText("seed-financial");
  });

  test("a tier the service chose says so on the card", async ({ page }) => {
    await page.goto("/health");
    const prod = page
      .locator("[data-testid='group-card']", { hasText: "seed-financial" })
      .filter({ hasText: "prod" });
    await expect(prod.getByText("declared")).toBeVisible();
  });

  test("a declaration the hub cannot use is said out loud, not swallowed", async ({ page }) => {
    await page.goto("/health");

    // seed-identity declares avuru.tier="critical", which is not a tier. The
    // board must still render (declarations fail soft — application telemetry
    // gets no operator review) AND the team must be able to find out why their
    // declaration did nothing.
    const warnings = page.getByTestId("health-warnings");
    await expect(warnings).toBeVisible();
    await expect(warnings).toContainText(/critical/);
    await expect(page.getByTestId("group-card").first()).toBeVisible();
  });
});
