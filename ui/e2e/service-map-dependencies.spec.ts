import { test, expect } from "@playwright/test";

// Virtual targets: the databases, caches and brokers that send no telemetry of
// their own and exist on the map only because their callers' exit spans name
// them (design/2026-08-23-virtual-targets.md).
//
// The seeded fixtures give five of them in the default tenant —
// postgresql://seed-orders-db, redis://seed-cache and kafka://seed-broker from
// traces_dependencies.json, plus the peerless `postgresql` and `redis` that
// traces_checkout.json's exits degrade to because those spans record no address.
// That degradation is deliberately part of the fixture set: it is what real
// SDK output looks like when nobody set server.address.
//
// Honest limitation, as everywhere on this screen: cytoscape draws to a canvas,
// so the barrel itself cannot be asserted from the DOM. What is held here is
// the contract around it — the count line, the legend, the toggle, and the URL.
test.describe("service map dependencies (seeded data)", () => {
  test("counts derived dependencies apart from services", async ({ page }) => {
    await page.goto("/service-map");

    // Two different kinds of thing, two different numbers. Folding a database
    // into "services" would inflate a count people check against their own
    // deployment list.
    await expect(page.getByTestId("map-count")).toContainText(
      /8 services · 7 call edges · 5 dependencies/,
    );
  });

  test("explains the shape in the legend", async ({ page }) => {
    await page.goto("/service-map");
    await expect(page.getByTestId("map-legend")).toContainText(
      "dashed barrel = database, cache or queue",
    );
  });

  test("the toggle hides them, and the URL carries the opt-out", async ({ page }) => {
    await page.goto("/service-map");

    const toggle = page.getByRole("checkbox", { name: "Databases & queues" });
    // Shown by default: a database the map already knows about is the point of
    // drawing it, so the URL carries the opt-OUT, not the opt-in.
    await expect(toggle).toBeChecked();
    await expect(page).not.toHaveURL(/virtual=/);

    await toggle.uncheck();
    await expect(page).toHaveURL(/virtual=false/);

    // Six of the seven call edges end at a dependency, so hiding the targets
    // takes those edges with them — a dangling edge would draw to nothing.
    const count = page.getByTestId("map-count");
    await expect(count).toContainText(/8 services · 1 call edges/);
    await expect(count).toContainText(/5 dependencies hidden/);
    await expect(page.getByTestId("map-legend")).not.toContainText("dashed barrel");

    // The opt-out is the URL, not component state.
    await page.reload();
    await expect(page.getByTestId("map-count")).toContainText(/5 dependencies hidden/);
  });

  test("the overview map counts them too", async ({ page }) => {
    await page.goto("/dashboard");

    // The compact map KEEPS derived dependencies where it drops mesh hops: a
    // transport hop is a relationship that does not exist, a database is one
    // that does and that nothing else on this dashboard would mention.
    await expect(page.getByText(/8 services · 5 dependencies · \d+ edges/)).toBeVisible();
  });
});
