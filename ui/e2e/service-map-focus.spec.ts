import { test, expect } from "@playwright/test";

// "Show me this service on the map" used to mean "filter the map to this name",
// which kept the node and dropped every edge it had: the reader arrived on an
// isolated dot. `focus=` keeps the service AND the services it talks to.
//
// The API is stubbed so the neighbourhood is exact — the assertions are counts,
// because cytoscape draws to a canvas and nothing inside it is queryable.
const FOCUS = "payments";

const MAP = {
  services: [
    { name: "gateway", spanCount: 40, ratePerSec: 1, errorRate: 0, p50Ms: 3, p95Ms: 7, p99Ms: 9 },
    { name: FOCUS, spanCount: 60, ratePerSec: 1, errorRate: 0, p50Ms: 4, p95Ms: 8, p99Ms: 12 },
    { name: "ledger", spanCount: 20, ratePerSec: 0.5, errorRate: 0, p50Ms: 9, p95Ms: 20, p99Ms: 30 },
    // A pair with nothing to do with the focused service: the point of the
    // test is that focusing removes exactly these two and nothing else.
    { name: "reporting", spanCount: 10, ratePerSec: 0.2, errorRate: 0, p50Ms: 5, p95Ms: 11, p99Ms: 15 },
    { name: "warehouse", spanCount: 8, ratePerSec: 0.2, errorRate: 0, p50Ms: 6, p95Ms: 13, p99Ms: 18 },
  ],
  edges: [
    { source: "gateway", target: FOCUS, calls: 30, errorCount: 0, errorRate: 0, provenance: "trace", p50Ms: 4, p95Ms: 8 },
    { source: FOCUS, target: "ledger", calls: 12, errorCount: 0, errorRate: 0, provenance: "trace", p50Ms: 9, p95Ms: 20 },
    { source: "reporting", target: "warehouse", calls: 5, errorCount: 0, errorRate: 0, provenance: "trace", p50Ms: 6, p95Ms: 13 },
  ],
};

test.describe("service map focus", () => {
  test.beforeEach(async ({ page }) => {
    await page.route("**/api/v1/service-map*", (route) => route.fulfill({ json: MAP }));
  });

  test("keeps the focused service and everything it talks to", async ({ page }) => {
    await page.goto(`/service-map?focus=${FOCUS}`);

    // One hop: the caller, the service, and what it calls. The unrelated pair
    // goes, and both of the focused service's edges stay.
    await expect(page.getByTestId("map-count")).toContainText("3 services · 2 call edges");
    await expect(page.getByTestId("map-count")).toContainText("filtered from 5");
    // A narrowed map has to say so, and say how to leave.
    await expect(page.getByTestId("map-focus")).toContainText(FOCUS);
  });

  test("a name filter is not a neighbourhood", async ({ page }) => {
    // The old behaviour, kept as a filter and demonstrated as the wrong answer
    // to "show me this service": the node survives alone, with no edges.
    await page.goto(`/service-map?q=${FOCUS}`);

    await expect(page.getByTestId("map-count")).toContainText("1 services · 0 call edges");
    await expect(page.getByTestId("map-focus")).toHaveCount(0);
  });

  test("show the whole map clears the focus", async ({ page }) => {
    await page.goto(`/service-map?focus=${FOCUS}`);

    await page.getByRole("button", { name: "show the whole map" }).click();
    await expect(page).not.toHaveURL(/focus=/);
    await expect(page.getByTestId("map-focus")).toHaveCount(0);
    await expect(page.getByTestId("map-count")).toContainText("5 services · 3 call edges");
  });

  test("the service page links to the neighbourhood, not to a name match", async ({ page }) => {
    await page.goto(`/services?service=${FOCUS}`);

    await page.getByRole("link", { name: "show on the map" }).click();
    await expect(page).toHaveURL(new RegExp(`focus=${FOCUS}`));
    await expect(page.getByTestId("map-count")).toContainText("3 services · 2 call edges");
  });
});
