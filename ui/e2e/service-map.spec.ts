import { test, expect } from "@playwright/test";

// The restyled service map (v0.5 W7). Seeded fixtures give three services
// (seed-checkout, seed-gateway, seed-payments) and exactly one call edge,
// seed-gateway -> seed-payments, rebased to now-5m so the default 15m range
// covers them (verified against a live compose stack: GET /api/v1/service-map
// returns 3 services / 1 edge). GET /api/v1/health/groups shows seed-checkout
// as effectiveStatus "down" (its one seeded trace root-spans an error, 100%
// error rate >= the 5% budget) while seed-gateway and seed-payments are
// "healthy" - so this seed is NOT all-healthy, and "problems only" narrows to
// exactly one service rather than emptying the graph.
//
// Honest limitation: cytoscape draws to a canvas, so rings, halos, focus and
// edge labels CANNOT be asserted from the DOM. The contract held here is the
// surrounding chrome - the legend, the controls, and the count line that every
// filter must move. The count line is the proof a filter reached the graph.
test.describe("service map (seeded data)", () => {
  test("explains its encoding in a legend", async ({ page }) => {
    await page.goto("/service-map");

    const legend = page.getByTestId("map-legend");
    await expect(legend).toBeVisible();
    await expect(legend.getByText("Healthy")).toBeVisible();
    await expect(legend.getByText("Degraded")).toBeVisible();
    await expect(legend.getByText("Down")).toBeVisible();
    await expect(legend.getByText(/size = rate/)).toBeVisible();
  });

  test("search narrows the graph and round-trips through the URL", async ({ page }) => {
    await page.goto("/service-map");

    const count = page.getByTestId("map-count");
    await expect(count).toContainText(/3 services · 1 call edges/);

    await page.getByRole("searchbox", { name: "Filter services" }).fill("gateway");
    await expect(count).toContainText(/1 services · 0 call edges/);
    await expect(count).toContainText(/filtered from 3/);
    await expect(page).toHaveURL(/q=gateway/);

    // The filter survives a reload — the URL is the truth, not component state.
    await page.reload();
    await expect(page.getByTestId("map-count")).toContainText(/1 services/);
  });

  test("problems-only keeps just the unhealthy services", async ({ page }) => {
    await page.goto("/service-map");

    // The seed is NOT all-healthy: seed-checkout's one trace root-spans an
    // error (effectiveStatus "down" per /api/v1/health/groups), while
    // seed-gateway and seed-payments are healthy. So "problems only" narrows
    // to exactly seed-checkout — proof the filter reached the graph — rather
    // than emptying it. Its one edge (gateway -> payments) touches neither
    // endpoint of the surviving node, so the edge count drops to zero too.
    await page.getByRole("checkbox", { name: "Problems only" }).check();
    await expect(page).toHaveURL(/problems=true/);
    await expect(page.getByTestId("map-count")).toContainText(/1 services · 0 call edges/);

    await page.getByRole("checkbox", { name: "Problems only" }).uncheck();
    await expect(page.getByTestId("map-count")).toContainText(/3 services/);
  });

  test("offers zoom, fit and re-layout", async ({ page }) => {
    await page.goto("/service-map");

    // Canvas state is not observable, so this holds the affordance contract:
    // the controls exist, are reachable by name, and clicking them does not
    // break the screen.
    await page.getByRole("button", { name: "Zoom in" }).click();
    await page.getByRole("button", { name: "Zoom out" }).click();
    await page.getByRole("button", { name: "Fit to view" }).click();
    await page.getByRole("button", { name: "Re-run layout" }).click();
    await expect(page.getByTestId("service-map")).toBeVisible();
  });
});
