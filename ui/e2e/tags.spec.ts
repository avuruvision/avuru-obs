import { test, expect } from "@playwright/test";

// Business tags are mapped in the chart from Kubernetes labels, so the compose
// stack has none — which makes the seeded run the honest test of the absent
// case, and the stub the test of the present one.
const TAGS = "**/api/v1/tags*";

const MAPPED = {
  tags: [
    { key: "avuru.tag.team", name: "team", values: ["payments", "storefront"] },
    { key: "avuru.tag.tier", name: "tier", values: ["critical", "standard"] },
  ],
};

test.describe("business tags", () => {
  test("no tags mapped means no tag controls at all", async ({ page }) => {
    await page.goto("/traces");
    await expect(page.getByLabel("Filter by span tags")).toBeVisible();
    await expect(page.getByTestId("tag-chips")).toHaveCount(0);
  });

  test("a mapped tag becomes a filter, and lands in the URL", async ({ page }) => {
    await page.route(TAGS, (route) => route.fulfill({ json: MAPPED }));
    await page.goto("/traces");

    const chips = page.getByTestId("tag-chips");
    await expect(chips).toBeVisible();
    await expect(chips.getByText("team")).toBeVisible();
    await expect(chips.getByText("tier")).toBeVisible();

    await chips.getByLabel("Filter by team").selectOption("payments");
    // The full reserved key goes to the API; the short name is what is shown.
    await expect(page).toHaveURL(/tags=avuru\.tag\.team%3Dpayments/);
    await expect(page.getByLabel("Filter by span tags")).toHaveValue(
      "avuru.tag.team=payments",
    );
  });

  test("two tags compose, sorted so a shared link is stable", async ({ page }) => {
    await page.route(TAGS, (route) => route.fulfill({ json: MAPPED }));
    await page.goto("/traces");
    const chips = page.getByTestId("tag-chips");

    // Chosen in reverse order on purpose: the URL must not depend on it.
    await chips.getByLabel("Filter by tier").selectOption("critical");
    await chips.getByLabel("Filter by team").selectOption("payments");
    await expect(page.getByLabel("Filter by span tags")).toHaveValue(
      "avuru.tag.team=payments,avuru.tag.tier=critical",
    );

    await chips.getByLabel("Clear team filter").click();
    await expect(page.getByLabel("Filter by span tags")).toHaveValue(
      "avuru.tag.tier=critical",
    );
  });

  test("the same vocabulary filters logs", async ({ page }) => {
    await page.route(TAGS, (route) => route.fulfill({ json: MAPPED }));
    await page.goto("/logs");
    const chips = page.getByTestId("tag-chips");
    await expect(chips).toBeVisible();
    await chips.getByLabel("Filter by team").selectOption("storefront");
    await expect(page).toHaveURL(/tags=avuru\.tag\.team%3Dstorefront/);
  });

  test("a tag filter from a link survives even when its value left the window", async ({
    page,
  }) => {
    await page.route(TAGS, (route) => route.fulfill({ json: MAPPED }));
    // "retired" is not in the sampled values: a select that silently dropped it
    // would change the filter the link asked for.
    await page.goto("/traces?tags=avuru.tag.team%3Dretired");
    const chips = page.getByTestId("tag-chips");
    await expect(chips.getByLabel("Filter by team")).toHaveValue("retired");
  });
});
