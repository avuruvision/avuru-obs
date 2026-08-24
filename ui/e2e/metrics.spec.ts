import { test, expect } from "@playwright/test";

// Seeded fixture: one deterministic trace from seed-checkout whose root span
// errors — the RED dashboard must show a card for it.
//
// The dashboard shows a card per service and the seed has several, so every
// lookup here is SCOPED to seed-checkout's card. Reaching for `.first()`
// instead silently follows whichever service happens to sort first, which is
// how this spec broke when a later fixture added services ahead of it
// alphabetically.
const SEED_SERVICE = "seed-checkout";

test.describe("RED metrics dashboard (seeded data)", () => {
  test("shows a card for the seeded service with the three charts", async ({ page }) => {
    await page.goto("/metrics");

    const card = page
      .getByTestId("red-card")
      .filter({ has: page.getByRole("heading", { name: SEED_SERVICE, exact: true }) });
    await expect(card).toBeVisible();
    for (const chart of ["Rate", "Errors", "Duration"]) {
      await expect(card.getByText(chart, { exact: true })).toBeVisible();
    }

    // The card links back to ITS OWN service's traces.
    await card.getByRole("link", { name: "traces →" }).click();
    await expect(page).toHaveURL(new RegExp(`service=${SEED_SERVICE}`));
  });
});
