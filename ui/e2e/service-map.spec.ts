import { test, expect } from "@playwright/test";

// The restyled service map (v0.5 W7). The seeded fixtures give EIGHT services
// in the default tenant and five derived dependencies (databases, caches and a
// broker — from traces_dependencies.json plus the db exits already in
// traces_checkout.json), rebased to now-5m so the default 15m range covers them.
// Verified against a live compose stack.
//
// Those counts are the contract, so a new traces_*.json fixture lands with an
// update to this file. They went stale once already, silently, because
// Playwright is not a CI gate: run `make e2e-ui` when you touch a fixture.
//
// GET /api/v1/health/groups shows seed-checkout
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
    await expect(count).toContainText(/8 services · 7 call edges · 5 dependencies/);

    await page.getByRole("searchbox", { name: "Filter services" }).fill("gateway");
    await expect(count).toContainText(/1 services · 0 call edges/);
    await expect(count).toContainText(/filtered from 8/);
    await expect(page).toHaveURL(/q=gateway/);

    // The filter survives a reload — the URL is the truth, not component state.
    await page.reload();
    await expect(page.getByTestId("map-count")).toContainText(/1 services/);
  });

  test("problems-only keeps just the unhealthy services", async ({ page }) => {
    await page.goto("/service-map");

    // The seed is NOT all-healthy: seed-checkout's one trace root-spans an
    // error (effectiveStatus "down" per /api/v1/health/groups), while the rest
    // are healthy. So "problems only" narrows to exactly seed-checkout — proof
    // the filter reached the graph — rather than emptying it. No surviving edge
    // keeps both its endpoints, so the edge count drops to zero, and the
    // dependencies go with it: a derived target has no health rollup, so it is
    // never something "problems only" keeps.
    await page.getByRole("checkbox", { name: "Problems only" }).check();
    await expect(page).toHaveURL(/problems=true/);
    await expect(page.getByTestId("map-count")).toContainText(/1 services · 0 call edges/);

    await page.getByRole("checkbox", { name: "Problems only" }).uncheck();
    await expect(page.getByTestId("map-count")).toContainText(/8 services/);
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

  // Module-off regression (see graph-elements.ts / map-legend.tsx): with
  // service-health off there is no rollup to ring, so the map falls back to a
  // binary "saw any error" ring and the legend switches to a single line
  // explaining it, instead of the healthy/degraded/down/idle swatches. The
  // status filter and group select are gated on the same module, so they must
  // disappear too. Stubbing /api/v1/capabilities to omit "service-health"
  // simulates that install, following modules.spec.ts's exact technique
  // rather than standing up a second stack.
  //
  // Honest limitation, same as above: this pins the legend copy and the
  // control gating - the CONTRACT - not the ring's actual color. Cytoscape
  // draws to a canvas, so whether a node is really rendered red here is not
  // something the DOM can answer.
  test("falls back to an error-only legend when service-health is off", async ({ page }) => {
    await page.route("**/api/v1/capabilities", (route) =>
      route.fulfill({ json: { version: "test", modules: ["core"] } }),
    );

    await page.goto("/service-map");

    const legend = page.getByTestId("map-legend");
    await expect(legend).toBeVisible();
    await expect(legend.getByText("ring: red = errors in window")).toBeVisible();
    await expect(legend.getByText("Healthy")).toHaveCount(0);
    await expect(legend.getByText("Degraded")).toHaveCount(0);
    await expect(legend.getByText("Down")).toHaveCount(0);

    await expect(page.getByRole("checkbox", { name: "Problems only" })).toHaveCount(0);
    await expect(page.getByRole("combobox", { name: "Filter by group" })).toHaveCount(0);
  });
});
