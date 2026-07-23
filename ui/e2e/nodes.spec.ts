import { test, expect } from "@playwright/test";

// The green seed (deploy/compose/seed/fixtures/metrics_kubeletstats_pods.json)
// permanently gives the compose stack one node — seed-node-1 carrying the two
// seeded pods — so the no-metrics condition can no longer be produced from
// seeded data. The empty state is therefore simulated by stubbing the nodes
// endpoint, the same reasoning as the capabilities stub in modules.spec.ts.
// Trailing `*`: the request carries time params.
const NODES = "**/api/v1/infra/nodes*";

test.describe("nodes screen", () => {
  test("renders the seeded node with its pod count", async ({ page }) => {
    await page.goto("/nodes");

    // Scope to the nodes table (the first one): the pods panel below lists the
    // node name on every pod row too.
    const row = page
      .getByRole("table")
      .first()
      .getByRole("row")
      .filter({ hasText: "seed-node-1" });
    await expect(row).toBeVisible();
    // Both seeded pods (seed-checkout-0, seed-payments-0) roll up to the node;
    // Pods is the last column.
    await expect(row.getByRole("cell").last()).toHaveText("2");
  });

  test("renders the empty state without metric data", async ({ page }) => {
    await page.route(NODES, (route) => route.fulfill({ json: { nodes: [] } }));

    await page.goto("/nodes");
    await expect(page.getByText("No node metrics yet")).toBeVisible();
    await expect(page.getByText(/sensor DaemonSet/)).toBeVisible();
  });
});
