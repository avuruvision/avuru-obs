import { test, expect, type Page } from "@playwright/test";

// Cost & waste, stubbed at the API. Reproducing it against the compose stack
// would need a Kubernetes API server behind the collector; what is under test
// here is what the screen SAYS — which workload it puts first, how it treats
// one that declared no request at all, and that it never invents a currency.
const GiB = 1024 * 1024 * 1024;

const WORKLOADS = {
  priced: false,
  workloads: [
    {
      // Reserves eight cores, peaks at half of one. The finding.
      workload: "batch-importer",
      namespace: "jobs",
      pods: 2,
      requestsNothing: false,
      reservedCpuCores: 8,
      reservedMemBytes: 16 * GiB,
      usedCpuCoresPeak: 0.5,
      usedCpuCoresMean: 0.05,
      usedMemBytesPeak: 2 * GiB,
      usedMemBytesMean: 1 * GiB,
      idleCpuCores: 7.5,
      idleMemBytes: 14 * GiB,
    },
    {
      // Declared nothing: unschedulable by accident, evicted first.
      workload: "debug-shell",
      namespace: "jobs",
      pods: 1,
      requestsNothing: true,
      reservedCpuCores: 0,
      reservedMemBytes: 0,
      usedCpuCoresPeak: 1.2,
      usedCpuCoresMean: 0.3,
      usedMemBytesPeak: 3 * GiB,
      usedMemBytesMean: 1 * GiB,
      idleCpuCores: 0,
      idleMemBytes: 0,
    },
  ],
};

const NODES = {
  nodes: [
    {
      node: "node-a",
      allocatableCpuCores: 8,
      allocatableMemBytes: 32 * GiB,
      requestedCpuCores: 7.2,
      requestedMemBytes: 28 * GiB,
      usedCpuCores: 0.8,
      usedMemBytes: 4 * GiB,
    },
  ],
};

async function stubCost(page: Page, workloads: unknown) {
  await page.route("**/api/v1/cost/workloads*", (r) => r.fulfill({ json: workloads }));
  await page.route("**/api/v1/cost/nodes*", (r) => r.fulfill({ json: NODES }));
}

test.describe("cost & waste", () => {
  test("ranks reserved-and-unused capacity, and names what declared nothing", async ({ page }) => {
    await stubCost(page, WORKLOADS);
    await page.goto("/cost");

    const table = page.getByTestId("cost-workloads");
    await expect(table).toContainText("batch-importer");
    // Idle is reserved minus the PEAK: 8 − 0.5. Against the mean it would say
    // 7.95, which is capacity the workload demonstrably used.
    await expect(table).toContainText("7.50");

    // Reserving nothing is its own state, not a zero in a column.
    await expect(page.getByTestId("cost-unbounded-warning")).toContainText("debug-shell");
    await expect(page.getByTestId("cost-unbounded-warning")).toContainText("requests nothing");
  });

  test("shows no money at all when the install declared no rates", async ({ page }) => {
    await stubCost(page, WORKLOADS);
    await page.goto("/cost");

    await expect(page.getByText(/no rates configured/)).toBeVisible();
    await expect(page.getByTestId("cost-idle-money")).toHaveCount(0);
    // Not a column of zeros — no column at all. A zero under a currency
    // header reads as "this workload is free".
    await expect(page.getByRole("columnheader", { name: /Idle .*\/h/ })).toHaveCount(0);
  });

  test("prices idle capacity once rates are configured", async ({ page }) => {
    await stubCost(page, {
      ...WORKLOADS,
      priced: true,
      currency: "EUR",
      workloads: WORKLOADS.workloads.map((w) => ({
        ...w,
        reservedCostPerHour: w.reservedCpuCores * 0.03,
        idleCostPerHour: w.idleCpuCores * 0.03,
      })),
    });
    await page.goto("/cost");

    await expect(page.getByTestId("cost-idle-money")).toContainText("EUR/h");
    await expect(page.getByTestId("cost-workloads")).toContainText("0.225");
    await expect(page.getByText(/no rates configured/)).toHaveCount(0);
  });

  test("says a node can be full and idle at once", async ({ page }) => {
    await stubCost(page, WORKLOADS);
    await page.goto("/cost");

    const nodes = page.getByTestId("cost-nodes");
    await expect(nodes).toContainText("node-a");
    // 7.2 of 8 cores claimed by requests, 0.8 of 8 in use: the node takes no
    // more pods while doing almost nothing.
    await expect(nodes).toContainText("90.0%");
    await expect(nodes).toContainText("10.0%");
  });

  test("teaches how to switch the collection on when nothing is reserved yet", async ({ page }) => {
    await stubCost(page, { priced: false, workloads: [] });
    await page.goto("/cost");

    await expect(page.getByRole("heading", { name: /Nothing reserved yet/ })).toBeVisible();
    await expect(page.getByText("modules.cost.enabled=true")).toBeVisible();
  });
});
