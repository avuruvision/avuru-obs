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

  test("filtering pods narrows the table and survives a reload", async ({ page }) => {
    await page.goto("/nodes");
    const podsTable = page.getByRole("table").last();
    await expect(podsTable.getByRole("row").filter({ hasText: "seed-checkout-0" })).toBeVisible();
    await expect(podsTable.getByRole("row").filter({ hasText: "seed-payments-0" })).toBeVisible();

    await page.getByTestId("pod-filter").fill("checkout");
    await expect(podsTable.getByRole("row").filter({ hasText: "seed-payments-0" })).toHaveCount(0);
    await expect(podsTable.getByRole("row").filter({ hasText: "seed-checkout-0" })).toBeVisible();
    // The count says what was hidden, so a narrowed table can't be mistaken
    // for a shrinking cluster.
    await expect(page.getByText(/1 of 2/)).toBeVisible();

    // Filter state lives in the URL, so the view is shareable like the rest of
    // the app — a reload must land on the same filtered table, not a reset one.
    await expect(page).toHaveURL(/podq=checkout/);
    await page.reload();
    await expect(page.getByTestId("pod-filter")).toHaveValue("checkout");
    await expect(page.getByRole("table").last().getByRole("row")
      .filter({ hasText: "seed-payments-0" })).toHaveCount(0);
  });

  test("the workload column is matched too, not just the pod name", async ({ page }) => {
    await page.goto("/nodes");
    // "shop" is the seeded namespace — matching it proves the filter reaches
    // past the pod name into the namespace/workload columns.
    await page.getByTestId("pod-filter").fill("shop");
    await expect(page.getByRole("table").last().getByRole("row")
      .filter({ hasText: "seed-checkout-0" })).toBeVisible();
  });

  test("a filter matching nothing says so, and never reads as a setup problem", async ({ page }) => {
    await page.goto("/nodes");
    await page.getByTestId("node-filter").fill("no-such-node");

    await expect(page.getByText(/No nodes match/)).toBeVisible();
    // The "install the sensor" empty state is for having NO data at all.
    // Showing it here would send someone to debug a healthy install.
    await expect(page.getByText("No node metrics yet")).toHaveCount(0);
  });

  test("sorting the pods table by name reverses the order", async ({ page }) => {
    await page.goto("/nodes");
    const podsTable = page.getByRole("table").last();
    const header = podsTable.getByRole("button", { name: "Pod" });

    await header.click();
    await expect(podsTable.getByRole("row").nth(1)).toContainText("seed-checkout-0");
    await header.click();
    await expect(podsTable.getByRole("row").nth(1)).toContainText("seed-payments-0");
  });

  test("the namespace facet stays hidden when there is only one namespace", async ({ page }) => {
    await page.goto("/nodes");
    // The seed runs everything in `shop`. A one-option dropdown is a control
    // that can only be a no-op, so it isn't offered.
    await expect(page.getByLabel("Filter pods by namespace")).toHaveCount(0);
  });
});
