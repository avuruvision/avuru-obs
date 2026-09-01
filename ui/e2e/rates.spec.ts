import { test, expect } from "@playwright/test";

// The one rate table. The seeded compose stack declares AI prices in
// deploy/compose/ai.json and compute rates in the cost env vars, so both
// arrive here as CHART-declared entries the UI must show and must not offer
// to edit.
const RATES_URL = "/settings?tab=rates";

test.describe("rate table", () => {
  test("chart-declared prices are shown and marked read-only", async ({
    page,
  }) => {
    await page.goto(RATES_URL);

    const panel = page.getByTestId("rates-panel");
    await expect(panel).toBeVisible();
    // Hiding chart values would leave an operator unable to explain a price
    // the screens are already using.
    await expect(
      panel.getByText("gpt-4o", { exact: true }).first(),
    ).toBeVisible();
    await expect(
      panel.getByText("chart", { exact: true }).first(),
    ).toBeVisible();
  });

  test("a UI-authored price overlays the chart and survives a reload", async ({
    page,
  }) => {
    await page.goto(RATES_URL);
    const panel = page.getByTestId("rates-panel");

    await panel.getByRole("button", { name: "Add" }).click();
    await panel.getByLabel("Model").last().fill("e2e-test-model");
    await panel.getByLabel("Input / 1M").last().fill("1.25");
    await panel.getByRole("button", { name: "Save", exact: true }).click();

    // The write must be visible to its own author immediately — a stale read
    // of your own edit reads as the save not having worked.
    await page.reload();
    await expect(panel.locator('input[value="e2e-test-model"]')).toBeVisible();

    // And clearing returns the install to the chart without un-pricing it.
    await panel.getByRole("button", { name: "Reset to chart values" }).click();
    await page.reload();
    await expect(panel.locator('input[value="e2e-test-model"]')).toHaveCount(0);
    await expect(
      panel.getByText("gpt-4o", { exact: true }).first(),
    ).toBeVisible();
  });
});
